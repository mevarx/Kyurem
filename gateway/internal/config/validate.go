package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// routeSegmentRe guards names/aliases used as URL path segments. Route
// segments must stay unambiguous and un escaped: no slashes, spaces or
// percent signs.
var routeSegmentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// maxCardBytesUpper bounds MaxCardBytes so a misconfiguration cannot turn
// card fetches into unbounded memory (the scanner caps reads anyway).
const maxCardBytesUpper = int64(16 << 20) // 16 MiB

// Validate checks a configuration deeply. It aggregates all violations so
// `kyurem validate` shows the complete picture in one run.
func Validate(cfg *Config) error {
	var errs []error

	if cfg.Listeners.A2A.Addr == "" {
		errs = append(errs, errors.New("listeners.a2a.addr: required"))
	}
	if cfg.Listeners.Admin.Addr == "" {
		errs = append(errs, errors.New("listeners.admin.addr: required"))
	}
	if cfg.Listeners.A2A.Addr != "" {
		errs = append(errs, checkAddr("listeners.a2a.addr", cfg.Listeners.A2A.Addr)...)
	}
	if cfg.Listeners.Admin.Addr != "" {
		errs = append(errs, checkAddr("listeners.admin.addr", cfg.Listeners.Admin.Addr)...)
	}

	prefix := cfg.Listeners.A2A.RoutePrefix
	if prefix == "" {
		prefix = "/a2a"
	}
	if !strings.HasPrefix(prefix, "/") || strings.HasSuffix(prefix, "/") {
		errs = append(errs, fmt.Errorf("listeners.a2a.route_prefix %q: must start with '/' and not end with '/'", cfg.Listeners.A2A.RoutePrefix))
	}
	if base := cfg.Listeners.A2A.PublicBaseURL; base != "" {
		errs = append(errs, checkPublicBaseURL(base)...)
	}

	if len(cfg.Agents) == 0 {
		errs = append(errs, errors.New("agents: at least one agent is required"))
	}

	// Routing keys share one namespace: every agent claims its canonical
	// name (case-insensitive) and every name/alias also claims a route
	// path `{prefix}/{key}`. Aliases therefore resolve under their own
	// path and cards served through them are rewritten to it (ADR 0002).
	pathOwner := map[string]string{}
	keyOwner := map[string]string{}

	claimPath := func(path, owner string) {
		if prev, dup := pathOwner[path]; dup {
			errs = append(errs, fmt.Errorf("%s: route path %s is already claimed by %s", owner, path, prev))
		} else {
			pathOwner[path] = owner
		}
	}
	claimKey := func(key, owner string) {
		k := strings.ToLower(key)
		if prev, dup := keyOwner[k]; dup && prev != owner {
			errs = append(errs, fmt.Errorf("%s: routing key %q already claimed by %s", owner, key, prev))
		} else {
			keyOwner[k] = owner
		}
	}

	for i, a := range cfg.Agents {
		label := fmt.Sprintf("agents[%d]", i)
		if a.Name != "" {
			label += " (" + a.Name + ")"
		}
		if a.Name == "" {
			errs = append(errs, fmt.Errorf("%s.name: required", label))
			continue
		}
		if !routeSegmentRe.MatchString(a.Name) {
			errs = append(errs, fmt.Errorf("%s.name %q: must be a URL-safe route segment [A-Za-z0-9][A-Za-z0-9._-]*", label, a.Name))
			continue
		}
		claimKey(a.Name, label)
		canonical := a.Path
		if canonical == "" {
			canonical = prefix + "/" + a.Name
		}
		canonical = normalizePath(canonical)
		claimPath(canonical, label)

		for j, alias := range a.Aliases {
			if alias == "" {
				errs = append(errs, fmt.Errorf("%s.aliases[%d]: empty alias", label, j))
				continue
			}
			if !routeSegmentRe.MatchString(alias) {
				errs = append(errs, fmt.Errorf("%s.aliases[%d] %q: must be a URL-safe route segment", label, j, alias))
				continue
			}
			claimKey(alias, label)
			claimPath(prefix+"/"+alias, label)
		}

		if len(a.Upstreams) == 0 {
			errs = append(errs, fmt.Errorf("%s.upstreams: at least one upstream is required", label))
		}
		for j, up := range a.Upstreams {
			if up.URL == "" {
				errs = append(errs, fmt.Errorf("%s.upstreams[%d].url: required", label, j))
				continue
			}
			errs = append(errs, checkUpstreamURL(fmt.Sprintf("%s.upstreams[%d].url", label, j), up.URL)...)
			if up.CardURL != "" {
				errs = append(errs, checkUpstreamURL(fmt.Sprintf("%s.upstreams[%d].card_url", label, j), up.CardURL)...)
			}
			if up.Binding != "" && !bindingTokenRe.MatchString(up.Binding) {
				errs = append(errs, fmt.Errorf("%s.upstreams[%d].binding %q: must match [A-Za-z0-9._+-]+", label, j, up.Binding))
			}
		}
	}

	errs = append(errs, checkNonNegative("card_cache.ttl", cfg.CardCache.TTL)...)
	if cfg.CardCache.TTL > 0 && cfg.CardCache.TTL < Duration(1_000_000_000) /* 1s */ {
		errs = append(errs, errors.New("card_cache.ttl: must be at least 1s"))
	}
	errs = append(errs, checkNonNegative("card_cache.stale_grace", cfg.CardCache.StaleGrace)...)
	if cfg.CardCache.MaxCardBytes <= 0 {
		errs = append(errs, errors.New("card_cache.max_card_bytes: must be positive"))
	}
	if cfg.CardCache.MaxCardBytes > maxCardBytesUpper {
		errs = append(errs, fmt.Errorf("card_cache.max_card_bytes: must be at most %d", maxCardBytesUpper))
	}

	errs = append(errs, checkNonNegativeI64("limits.max_concurrent_streams", cfg.Limits.MaxConcurrentStreams)...)
	errs = append(errs, checkNonNegativeI64("limits.max_request_body_bytes", cfg.Limits.MaxRequestBodyBytes)...)
	if cfg.Limits.MaxRequestBodyBytes > 0 && cfg.Limits.MaxRequestBodyBytes < 1024 {
		errs = append(errs, errors.New("limits.max_request_body_bytes: must be at least 1024"))
	}
	errs = append(errs, checkNonNegative("limits.shutdown_grace", cfg.Limits.ShutdownGrace)...)
	errs = append(errs, checkNonNegative("limits.timeouts.connect", cfg.Limits.Timeouts.Connect)...)
	errs = append(errs, checkNonNegative("limits.timeouts.response_header", cfg.Limits.Timeouts.ResponseHeader)...)
	errs = append(errs, checkNonNegative("limits.timeouts.request", cfg.Limits.Timeouts.Request)...)

	errs = append(errs, validateTelemetry(cfg.Telemetry)...)
	errs = append(errs, validateManagement(cfg.Management, cfg.Listeners.Mgmt.Addr)...)
	errs = append(errs, validatePricing(cfg.Pricing)...)

	switch cfg.Logging.Level {
	case "", "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("logging.level %q: one of debug|info|warn|error", cfg.Logging.Level))
	}
	switch cfg.Logging.Format {
	case "", "text", "json":
	default:
		errs = append(errs, fmt.Errorf("logging.format %q: one of text|json", cfg.Logging.Format))
	}

	return errors.Join(append([]error{}, errs...)...)
}

// AgentRoutes returns the canonical listen path plus per-alias listen paths
// for agent i, using the same normalization applied during validation. Used
// by the registry builder so route construction cannot drift.
func (c *Config) AgentRoutes(i int) (path string, aliases []string) {
	prefix := normalizedPrefix(c.Listeners.A2A.RoutePrefix)
	a := c.Agents[i]
	if a.Path != "" {
		path = normalizePath(a.Path)
	} else {
		path = prefix + "/" + a.Name
	}
	for _, alias := range a.Aliases {
		aliases = append(aliases, prefix+"/"+alias)
	}
	return path, aliases
}

// NormalizedRoutePrefix returns the effective route prefix ("" -> "/a2a").
func (c *Config) NormalizedRoutePrefix() string {
	return normalizedPrefix(c.Listeners.A2A.RoutePrefix)
}

func normalizedPrefix(p string) string {
	if p == "" {
		return "/a2a"
	}
	return strings.TrimSuffix(p, "/")
}

func normalizePath(p string) string {
	return "/" + strings.Trim(p, "/")
}

// validateTelemetry checks the M2 export settings. Export may be disabled
// (empty endpoint), but configured values must still be internally
// consistent so a typo cannot silently turn into dropped spans at runtime.
func validateTelemetry(t Telemetry) []error {
	var errs []error

	switch t.OTLPProtocol {
	case "", "http/protobuf", "grpc":
	default:
		errs = append(errs, fmt.Errorf("telemetry.otlp_protocol %q: one of http/protobuf|grpc", t.OTLPProtocol))
	}
	if t.OTLPEndpoint != "" {
		errs = append(errs, checkOTLPEndpoint(t.OTLPEndpoint, t.OTLPProtocol)...)
	}

	ratio := t.EffectiveSampleRatio()
	if math.IsNaN(ratio) || ratio < 0 || ratio > 1 {
		errs = append(errs, fmt.Errorf("telemetry.sample_ratio: must be between 0 and 1, got %v", ratio))
	}

	if t.QueueSize <= 0 {
		errs = append(errs, errors.New("telemetry.queue_size: must be positive"))
	} else if t.QueueSize > 1<<20 {
		errs = append(errs, errors.New("telemetry.queue_size: must be at most 1048576"))
	}
	if t.BatchSize <= 0 {
		errs = append(errs, errors.New("telemetry.batch_size: must be positive"))
	} else if t.QueueSize > 0 && t.BatchSize > t.QueueSize {
		errs = append(errs, fmt.Errorf("telemetry.batch_size: must not exceed telemetry.queue_size (%d)", t.QueueSize))
	}
	if t.BatchTimeout <= 0 {
		errs = append(errs, errors.New("telemetry.batch_timeout: must be positive"))
	}
	if t.ExportTimeout <= 0 {
		errs = append(errs, errors.New("telemetry.export_timeout: must be positive"))
	}
	if t.MaxRetries < 0 {
		errs = append(errs, errors.New("telemetry.max_retries: must be >= 0"))
	} else if t.MaxRetries > 10 {
		errs = append(errs, errors.New("telemetry.max_retries: must be at most 10"))
	}
	if t.InitialBackoff <= 0 {
		errs = append(errs, errors.New("telemetry.initial_backoff: must be positive"))
	}
	if t.MaxBackoff <= 0 {
		errs = append(errs, errors.New("telemetry.max_backoff: must be positive"))
	} else if t.InitialBackoff > 0 && t.MaxBackoff < t.InitialBackoff {
		errs = append(errs, errors.New("telemetry.max_backoff: must be >= telemetry.initial_backoff"))
	}
	if t.PayloadCapture {
		errs = append(errs, errors.New("telemetry.payload_capture: not implemented in M2 (TR-4); must remain false"))
	}
	return errs
}

// validateManagement checks the M3 management API settings. The API is
// authenticated, so an open API is only acceptable on a loopback listener.
func validateManagement(m Management, mgmtAddr string) []error {
	var errs []error

	switch m.Storage.Driver {
	case "", "memory", "clickhouse":
	default:
		errs = append(errs, fmt.Errorf("management.storage.driver %q: one of memory|clickhouse", m.Storage.Driver))
	}
	if m.Storage.MemoryCapacity <= 0 {
		errs = append(errs, errors.New("management.storage.memory_capacity: must be positive"))
	} else if m.Storage.MemoryCapacity > 1<<20 {
		errs = append(errs, errors.New("management.storage.memory_capacity: must be at most 1048576"))
	}

	if m.Storage.Driver == "clickhouse" {
		ch := m.Storage.ClickHouse
		if strings.TrimSpace(ch.URL) == "" {
			errs = append(errs, errors.New("management.storage.clickhouse.url: required when the driver is clickhouse"))
		} else if u, err := url.Parse(ch.URL); err != nil {
			errs = append(errs, fmt.Errorf("management.storage.clickhouse.url %q: %v", ch.URL, err))
		} else if u.Host == "" {
			errs = append(errs, fmt.Errorf("management.storage.clickhouse.url %q: missing host", ch.URL))
		}
		if ch.Timeout < 0 {
			errs = append(errs, errors.New("management.storage.clickhouse.timeout: must be positive"))
		}
	}

	if m.Enabled && m.AuthToken == "" && mgmtAddr != "" && !isLoopbackAddr(mgmtAddr) {
		errs = append(errs, fmt.Errorf(
			"management.auth_token: required when the management listener %s is not loopback", mgmtAddr))
	}
	return errs
}

// validatePricing checks the price table (CO-2). Negative prices are
// rejected: a negative price would produce a negative cost, which is
// worse than an unknown cost.
func validatePricing(prices []Price) []error {
	var errs []error
	for i, p := range prices {
		switch {
		case strings.TrimSpace(p.Model) == "":
			errs = append(errs, fmt.Errorf("pricing[%d].model: required", i))
		case p.InputPer1KUSD < 0:
			errs = append(errs, fmt.Errorf("pricing[%d].input_per_1k_usd: must not be negative", i))
		case p.OutputPer1KUSD < 0:
			errs = append(errs, fmt.Errorf("pricing[%d].output_per_1k_usd: must not be negative", i))
		}
	}
	return errs
}

// isLoopbackAddr reports whether a listen address only accepts local
// connections.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func checkOTLPEndpoint(raw, protocol string) []error {
	if strings.Contains(raw, "://") {
		if protocol == "grpc" {
			return []error{fmt.Errorf("telemetry.otlp_endpoint %q: grpc endpoints use host:port, not a URL", raw)}
		}
		u, err := url.Parse(raw)
		if err != nil {
			return []error{fmt.Errorf("telemetry.otlp_endpoint %q: %v", raw, err)}
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return []error{fmt.Errorf("telemetry.otlp_endpoint %q: scheme must be http or https", raw)}
		}
		if u.Host == "" {
			return []error{fmt.Errorf("telemetry.otlp_endpoint %q: missing host", raw)}
		}
		if u.User != nil {
			return []error{fmt.Errorf("telemetry.otlp_endpoint %q: must not contain credentials", raw)}
		}
		return nil
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return []error{fmt.Errorf("telemetry.otlp_endpoint %q: must be host:port or an http(s) URL: %v", raw, err)}
	}
	if host == "" {
		return []error{fmt.Errorf("telemetry.otlp_endpoint %q: missing host", raw)}
	}
	if port == "" {
		return []error{fmt.Errorf("telemetry.otlp_endpoint %q: missing port", raw)}
	}
	return nil
}

func checkAddr(field, addr string) []error {
	// Accept host:port only; a URL typed here is a common mistake worth
	// catching loudly instead of failing at listen() with a cryptic error.
	if strings.Contains(addr, "://") {
		return []error{fmt.Errorf("%s %q: must be host:port, not a URL", field, addr)}
	}
	return nil
}

func checkPublicBaseURL(base string) []error {
	u, err := url.Parse(base)
	if err != nil {
		return []error{fmt.Errorf("listeners.a2a.public_base_url %q: %v", base, err)}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return []error{fmt.Errorf("listeners.a2a.public_base_url %q: scheme must be http or https", base)}
	}
	if u.Host == "" {
		return []error{fmt.Errorf("listeners.a2a.public_base_url %q: missing host", base)}
	}
	if u.Path != "" && u.Path != "/" {
		return []error{fmt.Errorf("listeners.a2a.public_base_url %q: must not include a path (agents get their own {route_prefix}/{name} path)", base)}
	}
	return nil
}

func checkUpstreamURL(field, raw string) []error {
	u, err := url.Parse(raw)
	if err != nil {
		return []error{fmt.Errorf("%s %q: %v", field, raw, err)}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return []error{fmt.Errorf("%s %q: scheme must be http or https", field, raw)}
	}
	if u.Host == "" {
		return []error{fmt.Errorf("%s %q: missing host", field, raw)}
	}
	// Secrets never belong in URLs: reject embedded credentials loudly.
	if u.User != nil {
		return []error{fmt.Errorf("%s: URL must not contain credentials (use upstream auth config; secrets must not appear in config files)", field)}
	}
	return nil
}

func checkNonNegative(field string, d Duration) []error {
	if d < 0 {
		return []error{fmt.Errorf("%s: must be >= 0, got %s", field, d)}
	}
	return nil
}

func checkNonNegativeI64(field string, v int64) []error {
	if v < 0 {
		return []error{fmt.Errorf("%s: must be >= 0, got %d", field, v)}
	}
	return nil
}

// bindingTokenRe accepts the standard bindings and any custom binding token
// (the protocol type is open-ended per spec).
var bindingTokenRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
