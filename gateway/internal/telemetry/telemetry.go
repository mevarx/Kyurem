package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
	"github.com/kyurem-project/kyurem/gateway/internal/version"
)

// coreSemconvVersion documents the stable semantic conventions the HTTP and
// resource attributes follow (TR-6 companion pin).
const coreSemconvVersion = "1.44.0"

// Telemetry owns the tracer provider, propagator and caller tracker. It is
// deliberately explicit (no OTel globals) so multiple test gateways can run
// in one process without interfering.
type Telemetry struct {
	provider   *sdktrace.TracerProvider
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	tracker    *CallerTracker
	cfg        config.Telemetry
	exporting  bool
	sinkProc   *sinkProcessor
}

// Option customizes the telemetry setup.
type Option func(*options)

// options collects the optional wiring of the telemetry package.
type options struct {
	sink Sink
}

// WithSink publishes finished spans to a management-API sink (the
// in-process span store). The sink is fed independently of the OTLP
// exporter, so the dashboard keeps working when export is disabled.
func WithSink(s Sink) Option {
	return func(o *options) { o.sink = s }
}

// New builds telemetry from configuration. An empty OTLP endpoint creates a
// real TracerProvider without an exporter: spans and W3C propagation still
// work, nothing leaves the process.
func New(ctx context.Context, cfg config.Telemetry, logger *slog.Logger, rec Recorder, opts ...Option) (*Telemetry, error) {
	cfg = normalize(cfg)
	exporter, err := newExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return NewWithExporter(ctx, cfg, logger, rec, exporter, opts...)
}

// NewWithExporter is the test seam: it wires the same processor, resource
// and sampler but uses the supplied exporter. Passing nil disables export.
func NewWithExporter(ctx context.Context, cfg config.Telemetry, logger *slog.Logger, rec Recorder, exporter sdktrace.SpanExporter, opts ...Option) (*Telemetry, error) {
	cfg = normalize(cfg)
	if logger == nil {
		logger = slog.Default()
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes("",
			attribute.String("service.name", cfg.ServiceName),
			attribute.String("service.version", version.Version),
			attribute.String(AttrGenAISemconvCommit, GenAISemconvCommit),
			attribute.String(AttrGenAISemconvRepo, GenAISemconvRepo),
			attribute.String(AttrGenAISemconvStability, "development"),
			attribute.String("kyurem.otel.semconv.version", coreSemconvVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	opts2 := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.EffectiveSampleRatio()))),
	}
	exporting := exporter != nil
	if exporting {
		opts2 = append(opts2, sdktrace.WithSpanProcessor(newAsyncProcessor(
			exporter,
			rec,
			logger,
			cfg.QueueSize,
			cfg.BatchSize,
			cfg.BatchTimeout.Std(),
			cfg.ExportTimeout.Std(),
			cfg.MaxRetries,
			cfg.InitialBackoff.Std(),
			cfg.MaxBackoff.Std(),
		)))
	}
	// The management sink is fed even without an exporter.
	sinkProc := newSinkProcessor(o.sink, logger)
	if sinkProc != nil {
		opts2 = append(opts2, sdktrace.WithSpanProcessor(sinkProc))
	}
	provider := sdktrace.NewTracerProvider(opts2...)

	return &Telemetry{
		provider:   provider,
		tracer:     provider.Tracer("github.com/kyurem-project/kyurem/gateway"),
		propagator: propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}),
		tracker:    NewCallerTracker(4096),
		cfg:        cfg,
		exporting:  exporting,
		sinkProc:   sinkProc,
	}, nil
}

// StartSpan begins one gateway handoff span. The span is a SERVER span and
// follows GenAI naming (`invoke_agent {callee}`).
func (t *Telemetry) StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)
}

// Extract returns a context carrying the remote parent from W3C headers.
func (t *Telemetry) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	return t.propagator.Extract(ctx, carrier)
}

// Inject writes the current span's W3C context to outbound headers.
func (t *Telemetry) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	t.propagator.Inject(ctx, carrier)
}

// Caller resolves caller attribution for an extracted context.
func (t *Telemetry) Caller(ctx context.Context, hint string) CallerInfo {
	return t.tracker.Caller(ctx, hint)
}

// Remember records a span emitted by this gateway for nested-hop lookup.
func (t *Telemetry) Remember(sc trace.SpanContext, agent string, hop int) {
	t.tracker.Remember(sc, agent, hop)
}

// ForceFlush exports queued spans, bounded by ctx.
func (t *Telemetry) ForceFlush(ctx context.Context) error {
	if t.sinkProc != nil {
		if err := t.sinkProc.ForceFlush(ctx); err != nil {
			return err
		}
	}
	return t.provider.ForceFlush(ctx)
}

// Shutdown drains and shuts the provider down, bounded by ctx.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t.sinkProc != nil {
		if err := t.sinkProc.Shutdown(ctx); err != nil {
			return err
		}
	}
	return t.provider.Shutdown(ctx)
}

// Exporting reports whether an OTLP exporter is configured.
func (t *Telemetry) Exporting() bool { return t.exporting }

// Config returns the normalized telemetry configuration.
func (t *Telemetry) Config() config.Telemetry { return t.cfg }

// Provider exposes the SDK provider for in-process tests.
func (t *Telemetry) Provider() *sdktrace.TracerProvider { return t.provider }

func normalize(cfg config.Telemetry) config.Telemetry {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "kyurem-gateway"
	}
	if cfg.OTLPProtocol == "" {
		cfg.OTLPProtocol = "http/protobuf"
	}
	if cfg.SampleRatio == nil {
		one := 1.0
		cfg.SampleRatio = &one
	} else if *cfg.SampleRatio < 0 {
		zero := 0.0
		cfg.SampleRatio = &zero
	} else if *cfg.SampleRatio > 1 {
		one := 1.0
		cfg.SampleRatio = &one
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 2048
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 512
	}
	if cfg.BatchSize > cfg.QueueSize {
		cfg.BatchSize = cfg.QueueSize
	}
	if cfg.BatchTimeout <= 0 {
		cfg.BatchTimeout = config.Duration(5 * time.Second)
	}
	if cfg.ExportTimeout <= 0 {
		cfg.ExportTimeout = config.Duration(10 * time.Second)
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = config.Duration(500 * time.Millisecond)
	}
	if cfg.MaxBackoff <= 0 || cfg.MaxBackoff < cfg.InitialBackoff {
		cfg.MaxBackoff = cfg.InitialBackoff
	}
	return cfg
}

func newExporter(ctx context.Context, cfg config.Telemetry) (sdktrace.SpanExporter, error) {
	endpoint := strings.TrimSpace(cfg.OTLPEndpoint)
	if endpoint == "" {
		return nil, nil
	}

	switch cfg.OTLPProtocol {
	case "", "http/protobuf":
		opts := make([]otlptracehttp.Option, 0, 3)
		if strings.Contains(endpoint, "://") {
			u, err := url.Parse(endpoint)
			if err != nil {
				return nil, fmt.Errorf("otlp http/protobuf endpoint: %w", err)
			}
			opts = append(opts, otlptracehttp.WithEndpoint(u.Host))
			if u.Scheme == "http" {
				opts = append(opts, otlptracehttp.WithInsecure())
			}
			// WithEndpointURL would use the URL path as-is; the canonical
			// collector API lives at /v1/traces, so default to it while
			// still honoring an explicit path.
			path := u.Path
			if path == "" || path == "/" {
				path = "/v1/traces"
			}
			opts = append(opts, otlptracehttp.WithURLPath(path))
		} else {
			opts = append(opts, otlptracehttp.WithEndpoint(endpoint))
			if cfg.OTLPInsecure {
				opts = append(opts, otlptracehttp.WithInsecure())
			}
		}
		exp, err := otlptracehttp.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("otlp http/protobuf exporter: %w", err)
		}
		return exp, nil
	case "grpc":
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
		if cfg.OTLPInsecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		exp, err := otlptracegrpc.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("otlp grpc exporter: %w", err)
		}
		return exp, nil
	default:
		return nil, fmt.Errorf("unsupported otlp protocol %q", cfg.OTLPProtocol)
	}
}
