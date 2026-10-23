// Package server assembles the gateway's listeners (A2A data path and the
// admin listener), health/readiness probes, Prometheus /metrics, and the
// graceful shutdown path that drains in-flight streams within a deadline
// (GW-7) before escalating to hard close.
package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/kyurem-project/kyurem/gateway/internal/authn"
	"github.com/kyurem-project/kyurem/gateway/internal/config"
	"github.com/kyurem-project/kyurem/gateway/internal/cost"
	"github.com/kyurem-project/kyurem/gateway/internal/metrics"
	"github.com/kyurem-project/kyurem/gateway/internal/mgmtapi"
	"github.com/kyurem-project/kyurem/gateway/internal/policy"
	"github.com/kyurem-project/kyurem/gateway/internal/proxy"
	"github.com/kyurem-project/kyurem/gateway/internal/registry"
	"github.com/kyurem-project/kyurem/gateway/internal/storage"
	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
	"github.com/kyurem-project/kyurem/gateway/internal/trust"
	"github.com/kyurem-project/kyurem/gateway/internal/version"
)

// Server owns every goroutine and listener the gateway runs.
type Server struct {
	cfg     *config.Config
	logger  *slog.Logger
	reg     *registry.Registry
	cache   *registry.CardCache
	mtr     *metrics.Metrics
	tel     *telemetry.Telemetry
	proxy   *proxy.Handler
	store   storage.Store
	queries storage.Queries

	auth   *authn.Authenticator
	trust  *trust.Verifier
	pol    *policy.Engine
	audit  *policy.AuditLog
	a2aTLS *tls.Config

	a2aSrv   *http.Server
	adminSrv *http.Server
	mgmtSrv  *http.Server

	ready   atomic.Bool
	healthy atomic.Bool
	started time.Time
}

// New wires all components; Start launches them. Telemetry setup can fail
// (for example an unusable exporter configuration), so callers must handle
// the error instead of serving without the promised tracing.
func New(cfg *config.Config, logger *slog.Logger) (*Server, error) {
	mtr := metrics.New()

	// The span store feeds the management API. The in-process driver also
	// receives the finished spans, so the dashboard works without any
	// external database.
	store, err := buildStore(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}

	var sink telemetry.Sink
	if s, ok := store.(telemetry.Sink); ok {
		sink = s
	}
	tel, err := telemetry.New(context.Background(), cfg.Telemetry, logger, mtr, telemetry.WithSink(sink))
	if err != nil {
		return nil, fmt.Errorf("telemetry: %w", err)
	}

	reg := buildRegistry(cfg)
	cache := registry.NewCardCache(
		reg,
		nil, // default client with sane timeout
		cfg.CardCache.TTL.Std(),
		cfg.CardCache.StaleGrace.Std(),
		cfg.CardCache.MaxCardBytes,
		logger,
	)

	// M5 security: caller authentication (TP-5), card verification
	// (TP-1/TP-2), authorization rules (TP-3) and the audit ring (TP-4).
	auth, err := authn.New(cfg.Callers)
	if err != nil {
		return nil, fmt.Errorf("callers: %w", err)
	}
	trustV, err := trust.New(cfg.Trust)
	if err != nil {
		return nil, fmt.Errorf("trust: %w", err)
	}
	cache.SetVerifier(func(ctx context.Context, agent string, raw []byte, card *a2a.AgentCard) registry.CardVerification {
		return cardVerification(trustV.Verify(ctx, agent, raw, card))
	})
	pol := policy.New(cfg.Policy, policyCallers(auth, cfg), agentNames(reg))
	audit := policy.NewAuditLog(cfg.Policy.AuditCapacity)
	tlsCfg, err := buildA2ATLS(cfg.Listeners.A2A.TLS)
	if err != nil {
		return nil, fmt.Errorf("listeners.a2a.tls: %w", err)
	}

	h := proxy.New(cfg, reg, cache, mtr, tel, logger,
		proxy.WithPriceTable(buildPriceTable(cfg)),
		proxy.WithAuth(auth),
		proxy.WithTrust(trustV),
		proxy.WithPolicy(pol),
		proxy.WithAudit(audit),
	)

	s := &Server{
		cfg:     cfg,
		logger:  logger,
		reg:     reg,
		cache:   cache,
		mtr:     mtr,
		tel:     tel,
		proxy:   h,
		store:   store,
		queries: storage.NewQueries(store),
		auth:    auth,
		trust:   trustV,
		pol:     pol,
		audit:   audit,
		a2aTLS:  tlsCfg,
		started: time.Now(),
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
	if cfg.Management.Enabled {
		s.mgmtSrv = &http.Server{
			Addr:              cfg.Listeners.Mgmt.Addr,
			Handler:           s.mgmtHandler(),
			ReadHeaderTimeout: headerTimeout(cfg.Limits.Timeouts.ResponseHeader.Std()),
			IdleTimeout:       120 * time.Second,
		}
	}
	return s, nil
}

// buildStore creates the span store the management API reads from.
func buildStore(cfg *config.Config, logger *slog.Logger) (storage.Store, error) {
	switch cfg.Management.Storage.Driver {
	case "", "memory":
		return storage.NewMemoryStore(cfg.Management.Storage.MemoryCapacity), nil
	case "clickhouse":
		ch := cfg.Management.Storage.ClickHouse
		return storage.NewClickHouseStore(storage.ClickHouseOptions{
			URL:      ch.URL,
			Database: ch.Database,
			Username: ch.Username,
			Password: ch.Password,
			Timeout:  ch.Timeout.Std(),
		})
	default:
		return nil, fmt.Errorf("unknown storage driver %q", cfg.Management.Storage.Driver)
	}
}

// buildPriceTable converts the configured prices into the cost table the
// proxy uses to price a span (CO-2).
func buildPriceTable(cfg *config.Config) cost.Table {
	prices := make([]cost.Price, 0, len(cfg.Pricing))
	for _, p := range cfg.Pricing {
		prices = append(prices, cost.Price{
			Model:          p.Model,
			InputPer1KUSD:  p.InputPer1KUSD,
			OutputPer1KUSD: p.OutputPer1KUSD,
		})
	}
	return cost.NewTable(prices)
}

// cardVerification adapts the trust package's result to the snapshot type
// the card cache stores (the registry stays free of trust imports).
func cardVerification(res trust.Result) registry.CardVerification {
	return registry.CardVerification{
		Mode:     string(res.Mode),
		Status:   res.Status(),
		Signed:   res.Signed,
		Verified: res.Verified,
		KeyID:    res.KeyID,
		Reason:   res.Reason,
	}
}

// policyCallers lists the caller identities shown in the policy matrix:
// the configured bearer-token callers, the anonymous identity when it can
// still reach the gateway, and any caller named by a rule.
func policyCallers(auth *authn.Authenticator, cfg *config.Config) []string {
	names := []string{}
	if auth != nil {
		names = append(names, auth.CallerNames()...)
	}
	if authn.ParseMode(cfg.Callers.Mode) != authn.ModeRequired {
		names = append(names, authn.AnonymousCaller)
	}
	for _, rule := range cfg.Policy.Rules {
		if name := strings.TrimSpace(rule.Caller); name != "" && name != "*" {
			names = append(names, name)
		}
	}
	return names
}

// agentNames lists the registered agent names that form the matrix columns.
func agentNames(reg *registry.Registry) []string {
	if reg == nil {
		return []string{}
	}
	names := make([]string, 0, reg.Len())
	for _, e := range reg.Entries() {
		if e != nil && e.Name != "" {
			names = append(names, e.Name)
		}
	}
	return names
}

// buildA2ATLS loads the data-path TLS material. It returns nil when TLS is
// not configured. Client certificates are optional at the handshake: a
// caller may authenticate with a bearer token instead, and required mode
// still rejects a request without any credential.
func buildA2ATLS(c config.TLS) (*tls.Config, error) {
	certFile := strings.TrimSpace(c.CertFile)
	keyFile := strings.TrimSpace(c.KeyFile)
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, errors.New("cert_file and key_file must both be set")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load cert/key pair: %w", err)
	}
	minVersion := uint16(tls.VersionTLS12)
	if c.MinVersion == "1.3" {
		minVersion = tls.VersionTLS13
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   minVersion,
	}
	if caFile := strings.TrimSpace(c.ClientCAFile); caFile != "" {
		raw, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read client_ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(raw) {
			return nil, fmt.Errorf("client_ca_file %q: no certificates found", caFile)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.VerifyClientCertIfGiven
	}
	return cfg, nil
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

// mgmtHandler serves the management API (M3) on its own listener: the A2A
// data path stays transparent, the API is authenticated and versioned.
func (s *Server) mgmtHandler() http.Handler {
	return mgmtapi.New(mgmtapi.Deps{
		Queries:         s.queries,
		Registry:        s.reg,
		Cards:           s.cache,
		Metrics:         s.mtr,
		Telemetry:       s.tel,
		Policy:          s.pol,
		Audit:           s.audit,
		Trust:           s.trust,
		StorageDriver:   s.cfg.Management.Storage.Driver,
		StorageDatabase: s.cfg.Management.Storage.ClickHouse.Database,
		AuthToken:       s.cfg.Management.AuthToken,
		Logger:          s.logger,
		Uptime:          s.started,
	})
}

func writeJSON(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

// logSecurityModes states the effective M5 posture once at startup, so an
// operator can see whether calls are being authenticated and enforced.
func (s *Server) logSecurityModes() {
	authMode := "off"
	if s.auth != nil {
		authMode = string(s.auth.Mode())
	}
	trustMode := "off"
	if s.trust != nil {
		trustMode = string(s.trust.Mode(""))
	}
	policyMode := "off"
	if s.pol != nil {
		policyMode = string(s.pol.Mode())
	}
	s.logger.Info("security modes",
		"callers", authMode,
		"trust", trustMode,
		"policy", policyMode,
		"a2a_tls", s.a2aTLS != nil,
	)
}

// Start binds listeners and runs until Shutdown. It is a blocking call
// with a full shutdown path: signal → cancel → drain → escalate.
func (s *Server) Start(ctx context.Context) error {
	// Every exit path, including bind failures, flushes the export queue.
	defer s.shutdownTelemetry()
	// Card refreshers live on this run-scoped context: they keep warming
	// through the drain and are stopped when Start returns, so no
	// refresher goroutine or background card fetch outlives the gateway
	// (GW-7).
	cacheCtx, cacheStop := context.WithCancel(context.Background())
	defer cacheStop()
	s.cache.Start(cacheCtx)

	a2aLn, err := net.Listen("tcp", s.a2aSrv.Addr)
	if err != nil {
		return err
	}
	if s.a2aTLS != nil {
		a2aLn = tls.NewListener(a2aLn, s.a2aTLS)
	}
	adminLn, err := net.Listen("tcp", s.adminSrv.Addr)
	if err != nil {
		_ = a2aLn.Close()
		return err
	}
	var mgmtLn net.Listener
	if s.mgmtSrv != nil {
		mgmtLn, err = net.Listen("tcp", s.mgmtSrv.Addr)
		if err != nil {
			_ = a2aLn.Close()
			_ = adminLn.Close()
			return err
		}
	}
	s.logger.Info("kyurem gateway starting",
		"a2a_addr", a2aLn.Addr().String(),
		"admin_addr", adminLn.Addr().String(),
		"agents", s.reg.Len(),
		"a2a_spec_version", version.SpecVersion,
		"a2a_go_sdk_version", version.GoVersion(),
		"log_format", s.cfg.Logging.Format,
	)
	s.logSecurityModes()
	if mgmtLn != nil {
		s.logger.Info("management API enabled",
			"mgmt_addr", mgmtLn.Addr().String(),
			"storage_driver", s.cfg.Management.Storage.Driver,
			"authenticated", s.cfg.Management.AuthToken != "")
	}
	if s.tel.Exporting() {
		s.logger.Info("telemetry export enabled",
			"otlp_endpoint", s.cfg.Telemetry.OTLPEndpoint,
			"otlp_protocol", s.cfg.Telemetry.OTLPProtocol,
			"sample_ratio", s.cfg.Telemetry.EffectiveSampleRatio())
	} else {
		s.logger.Info("telemetry export disabled; W3C trace context is still propagated")
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
	if mgmtLn != nil {
		serveWG.Add(1)
		go func() {
			defer serveWG.Done()
			serveErr := s.mgmtSrv.Serve(mgmtLn)
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				s.logger.Error("management listener terminated", "err", serveErr)
			}
		}()
	}

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
	servers := []*http.Server{s.adminSrv, s.a2aSrv}
	if s.mgmtSrv != nil {
		servers = append(servers, s.mgmtSrv)
	}
	for _, srv := range servers {
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
	serveWG.Wait()
	s.logger.Info("shutdown complete")
	return nil
}

// shutdownTelemetry drains queued spans. The deadline is independent from
// the HTTP drain so a slow collector cannot extend process shutdown
// indefinitely; the processor cancels outstanding exports on expiry.
func (s *Server) shutdownTelemetry() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.tel.Shutdown(ctx); err != nil {
		s.logger.Warn("telemetry shutdown incomplete; queued spans were dropped", "err", err)
	}
	if s.store != nil {
		if err := s.store.Close(); err != nil {
			s.logger.Warn("span store close failed", "err", err)
		}
	}
}
