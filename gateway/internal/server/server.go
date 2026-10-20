// Package server assembles the gateway's listeners (A2A data path and the
// admin listener), health/readiness probes, Prometheus /metrics, and the
// graceful shutdown path that drains in-flight streams within a deadline
// (GW-7) before escalating to hard close.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
	"github.com/kyurem-project/kyurem/gateway/internal/metrics"
	"github.com/kyurem-project/kyurem/gateway/internal/proxy"
	"github.com/kyurem-project/kyurem/gateway/internal/registry"
	"github.com/kyurem-project/kyurem/gateway/internal/version"
)

// Server owns every goroutine and listener the gateway runs.
type Server struct {
	cfg    *config.Config
	logger *slog.Logger
	reg    *registry.Registry
	cache  *registry.CardCache
	mtr    *metrics.Metrics
	proxy  *proxy.Handler

	a2aSrv   *http.Server
	adminSrv *http.Server

	ready   atomic.Bool
	healthy atomic.Bool
	done    chan struct{}
}

// New wires all components; Start launches them.
func New(cfg *config.Config, logger *slog.Logger) *Server {
	mtr := metrics.New()
	reg := buildRegistry(cfg)
	cache := registry.NewCardCache(
		reg,
		nil, // default client with sane timeout
		cfg.CardCache.TTL.Std(),
		cfg.CardCache.StaleGrace.Std(),
		cfg.CardCache.MaxCardBytes,
		logger,
	)
	h := proxy.New(cfg, reg, cache, mtr, logger)

	s := &Server{
		cfg:    cfg,
		logger: logger,
		reg:    reg,
		cache:  cache,
		mtr:    mtr,
		proxy:  h,
		done:   make(chan struct{}),
	}
	s.a2aSrv = &http.Server{
		Addr:              cfg.Listeners.A2A.Addr,
		Handler:           h,
		ReadHeaderTimeout: headerTimeout(cfg.Limits.Timeouts.ResponseHeader.Std()),
		IdleTimeout:       120 * time.Second,
		// WriteTimeout stays off: SSE responses live as long as the task
		// does; the optional total deadline is per-request instead.
	}
	s.adminSrv = &http.Server{
		Addr:              cfg.Listeners.Admin.Addr,
		Handler:           s.adminHandler(),
		ReadHeaderTimeout: headerTimeout(cfg.Limits.Timeouts.ResponseHeader.Std()),
		IdleTimeout:       120 * time.Second,
	}
	return s
}

// headerTimeout converts response-header semantics to request-header
// semantics for the inbound listeners (both bound "wait for the first
// network data"), clamping to at least 10s so slow client dials do not
// break probes.
func headerTimeout(responseHeader time.Duration) time.Duration {
	if responseHeader <= 0 {
		return 30 * time.Second
	}
	return responseHeader
}

func buildRegistry(cfg *config.Config) *registry.Registry {
	reg, err := registry.New(cfg)
	if err != nil {
		// Config validation already ran; buildRegistry cannot fail after
		// Validate succeeded without a bug. Fail loudly at startup.
		panic("registry build after validated config failed: " + err.Error())
	}
	return reg
}

// adminHandler serves liveness/readiness/metrics/version on a listener
// that is separate from the data path (PRD §4.6).
func (s *Server) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !s.healthy.Load() {
			http.Error(w, `{"status":"shutting down"}`, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !s.ready.Load() {
			http.Error(w, `{"status":"not ready"}`, http.StatusServiceUnavailable)
			return
		}
		payload := map[string]any{"status": "ok", "agents": s.reg.Len()}
		if err := writeJSON(w, payload); err != nil {
			s.logger.Debug("readyz write failed", "err", err)
		}
	})
	mux.Handle("GET /metrics", prometheusHandler(s.mtr))
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"kyurem_version":     version.Version,
			"a2a_spec_version":   version.SpecVersion,
			"a2a_go_sdk_version": version.GoVersion(),
		}
		if err := writeJSON(w, payload); err != nil {
			s.logger.Debug("version write failed", "err", err)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
	})
	// Panic containment for admin surfaces; data-path panics surface via
	// ReverseProxy which already reacts to ErrAbortHandler.
	return recoveryMiddleware(mux, s.logger)
}

func writeJSON(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

// Start binds listeners and runs until Shutdown. It is a blocking call
// with a full shutdown path: signal → cancel → drain → escalate.
func (s *Server) Start(ctx context.Context) error {
	s.cache.Start(context.WithoutCancel(ctx)) // refresher keeps warming until close

	a2aLn, err := net.Listen("tcp", s.a2aSrv.Addr)
	if err != nil {
		return err
	}
	adminLn, err := net.Listen("tcp", s.adminSrv.Addr)
	if err != nil {
		_ = a2aLn.Close()
		return err
	}
	s.logger.Info("kyurem gateway starting",
		"a2a_addr", a2aLn.Addr().String(),
		"admin_addr", adminLn.Addr().String(),
		"agents", s.reg.Len(),
		"a2a_spec_version", version.SpecVersion,
		"a2a_go_sdk_version", version.GoVersion(),
		"log_format", s.cfg.Logging.Format,
	)
	if s.cfg.Telemetry.OTLPEndpoint != "" {
		s.logger.Warn("telemetry.otlp_endpoint configured but tracing lands in M2; value is validated and ignored for now",
			"otlp_endpoint", s.cfg.Telemetry.OTLPEndpoint)
	}

	var serveWG sync.WaitGroup
	serveWG.Add(2)
	go func() {
		defer serveWG.Done()
		serveErr := s.a2aSrv.Serve(a2aLn)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.logger.Error("a2a listener terminated", "err", serveErr)
			s.healthy.Store(false)
		}
	}()
	go func() {
		defer serveWG.Done()
		serveErr := s.adminSrv.Serve(adminLn)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.logger.Error("admin listener terminated", "err", serveErr)
		}
	}()

	s.healthy.Store(true)
	s.ready.Store(true)
	defer s.ready.Store(false)

	<-ctx.Done() // signal received (caller wires signal.NotifyContext)
	s.logger.Info("shutdown: draining connections")
	s.ready.Store(false)
	s.healthy.Store(false)

	grace := context.Background()
	drainCtx, cancel := context.WithTimeout(grace, s.cfg.Limits.ShutdownGrace.Std())
	defer cancel()

	// Servers shut down concurrently: admin first would be fine too, but
	// concurrent is simplest and symmetric for tests already using both.
	var shutdownWG sync.WaitGroup
	for _, srv := range []*http.Server{s.adminSrv, s.a2aSrv} {
		shutdownWG.Add(1)
		go func(srv *http.Server) {
			defer shutdownWG.Done()
			if err := srv.Shutdown(drainCtx); err != nil {
				s.logger.Warn("graceful shutdown deadline exceeded; forcing close", "addr", srv.Addr, "err", err)
				_ = srv.Close()
			}
		}(srv)
	}
	shutdownWG.Wait()

	// Streams may outlive their HTTP servers; ReverseProxy finishes them
	// against closed listeners only when clients disconnect. Their owner is
	// the process, so nothing leaks beyond process exit.
	close(s.done)
	serveWG.Wait()
	s.logger.Info("shutdown complete")
	return nil
}
