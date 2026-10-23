package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validYAML() string {
	return `
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: echo
    aliases: [greeter]
    upstreams:
      - url: http://127.0.0.1:9010
        binding: JSONRPC
`
}

func load(t *testing.T, raw string) *Config {
	t.Helper()
	cfg, err := LoadBytes([]byte(raw))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	return cfg
}

func TestLoadDefaults(t *testing.T) {
	cfg := load(t, validYAML())

	if got := cfg.Logging.Level; got != "info" {
		t.Errorf("logging level default = %q, want info", got)
	}
	if got := cfg.Logging.Format; got != "json" {
		t.Errorf("logging format default = %q, want json", got)
	}
	if got := cfg.CardCache.TTL.Std(); got != 60*time.Second {
		t.Errorf("card_cache.ttl default = %s, want 60s", got)
	}
	if got := cfg.CardCache.StaleGrace.Std(); got != 10*time.Minute {
		t.Errorf("card_cache.stale_grace default = %s, want 10m", got)
	}
	if got := cfg.Limits.MaxConcurrentStreams; got != 1024 {
		t.Errorf("max_concurrent_streams default = %d, want 1024", got)
	}
	if got := cfg.Limits.MaxRequestBodyBytes; got != 64<<20 {
		t.Errorf("max_request_body_bytes default = %d, want 64MiB", got)
	}
	if got := cfg.Limits.Timeouts.Connect.Std(); got != 5*time.Second {
		t.Errorf("timeouts.connect default = %s, want 5s", got)
	}
	if got := cfg.Limits.Timeouts.ResponseHeader.Std(); got != 30*time.Second {
		t.Errorf("timeouts.response_header default = %s, want 30s", got)
	}
	if got := cfg.FailureHandling.MaxAttempts; got != 2 {
		t.Errorf("failure_handling.max_attempts default = %d, want 2", got)
	}
	if got := cfg.FailureHandling.RetryBudget.MaxRetries; got != 10 {
		t.Errorf("failure_handling.retry_budget.max_retries default = %d, want 10", got)
	}
	if got := cfg.FailureHandling.CircuitBreaker.FailureThreshold; got != 5 {
		t.Errorf("failure_handling.circuit_breaker.failure_threshold default = %d, want 5", got)
	}

	path, aliases := cfg.AgentRoutes(0)
	if path != "/a2a/echo" {
		t.Errorf("agent route path = %q, want /a2a/echo", path)
	}
	if len(aliases) != 1 || aliases[0] != "/a2a/greeter" {
		t.Errorf("agent alias paths = %v, want [/a2a/greeter]", aliases)
	}
}

func TestEnforcedSkillRulesRequireTrustedOperationData(t *testing.T) {
	rule := PolicyRule{Name: "skill-rule", Caller: "*", Callee: "echo", Skills: []string{"payments"}, Effect: "deny"}
	base := Policy{Default: "allow", AuditCapacity: 8, Rules: []PolicyRule{rule}}
	base.Mode = "enforce"
	if errs := validatePolicy(base); len(errs) == 0 || !strings.Contains(errs[0].Error(), "X-Kyurem-Skill") {
		t.Fatalf("enforced skill rule should be rejected: %v", errs)
	}
	base.Mode = "warn"
	if errs := validatePolicy(base); len(errs) != 0 {
		t.Fatalf("warn-mode skill rule should remain available for audit: %v", errs)
	}
}

func TestFailureHandlingBounds(t *testing.T) {
	cfg := load(t, validYAML())
	cfg.FailureHandling.MaxAttempts = 6
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "failure_handling.max_attempts") {
		t.Fatalf("expected retry attempt limit error, got %v", err)
	}
}

func TestUnknownKeysRejected(t *testing.T) {
	_, err := LoadBytes([]byte(`
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agenst: []`))
	if err == nil {
		t.Fatal("unknown top-level key must be rejected")
	}
	if !strings.Contains(err.Error(), "agenst") {
		t.Errorf("error should name the unknown key, got: %v", err)
	}
}

func TestValidationErrorsAggregate(t *testing.T) {
	_, err := LoadBytes([]byte(`
listeners: {}
agents:
  - name: ""
    upstreams: []
`))
	if err == nil {
		t.Fatal("expected validation failure")
	}
	for _, want := range []string{"listeners.a2a.addr", "listeners.admin.addr"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestDuplicateRoutesRejected(t *testing.T) {
	_, err := LoadBytes([]byte(`
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: dup
    upstreams:
      - url: http://127.0.0.1:9100
  - name: dup
    upstreams:
      - url: http://127.0.0.1:9101
`))
	if err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("duplicate name must be rejected, got: %v", err)
	}
}

func TestCredentialInUpstreamURLRejected(t *testing.T) {
	_, err := LoadBytes([]byte(`
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: a
    upstreams:
      - url: http://user:secret@127.0.0.1:9100
`))
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("credential URLs must be rejected, got: %v", err)
	}
}

func TestDurationParsing(t *testing.T) {
	cfg := load(t, validYAML()+`
card_cache:
  ttl: "90s"
  stale_grace: 5
`)
	if got := cfg.CardCache.TTL.Std(); got != 90*time.Second {
		t.Errorf("ttl = %s, want 90s", got)
	}
	if got := cfg.CardCache.StaleGrace.Std(); got != 5*time.Second {
		t.Errorf("stale_grace = %s, want 5s (plain number = seconds)", got)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("KYUREM_LISTENERS_A2A_ADDR", "0.0.0.0:9999")
	t.Setenv("KYUREM_LISTENERS_A2A_ROUTE_PREFIX", "/api")
	t.Setenv("KYUREM_CARD_CACHE_TTL", "2m")
	t.Setenv("KYUREM_LOGGING_LEVEL", "debug")
	t.Setenv("KYUREM_LIMITS_MAX_CONCURRENT_STREAMS", "16")

	cfg := load(t, validYAML())
	if cfg.Listeners.A2A.Addr != "0.0.0.0:9999" {
		t.Errorf("a2a addr env override failed: %q", cfg.Listeners.A2A.Addr)
	}
	if cfg.Listeners.A2A.RoutePrefix != "/api" {
		t.Errorf("route prefix env override failed: %q", cfg.Listeners.A2A.RoutePrefix)
	}
	if cfg.CardCache.TTL.Std() != 2*time.Minute {
		t.Errorf("ttl env override failed: %s", cfg.CardCache.TTL)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("level env override failed: %q", cfg.Logging.Level)
	}
	if cfg.Limits.MaxConcurrentStreams != 16 {
		t.Errorf("streams env override failed: %v", cfg.Limits.MaxConcurrentStreams)
	}

	// Agents list must not be overridable via env (documented file-only).
	t.Setenv("KYUREM_AGENTS", "whatever")
	cfg2, err := LoadBytes([]byte(validYAML()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg2.Agents) != 1 {
		t.Errorf("agents list changed by env: %v", cfg2.Agents)
	}
}

func TestEnvOverrideInvalidValue(t *testing.T) {
	t.Setenv("KYUREM_CARD_CACHE_TTL", "not-a-duration")
	_, err := LoadBytes([]byte(validYAML()))
	if err == nil || !strings.Contains(err.Error(), "KYUREM_CARD_CACHE_TTL") {
		t.Fatalf("bad env duration must fail with the key named, got: %v", err)
	}
}

func TestLoadFileMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("missing file error expected, got: %v", err)
	}
}

func TestValidateExampleConfig(t *testing.T) {
	// The shipped example must always stay valid: it is part of the demo.
	raw, err := os.ReadFile("../../../config/kyurem.example.yaml")
	if err != nil {
		t.Skipf("example config not found in workspace: %v", err)
	}
	if _, err := LoadBytes(raw); err != nil {
		t.Errorf("example config must validate: %v", err)
	}
}

func TestExplicitAgentPathOverride(t *testing.T) {
	cfg := load(t, `
listeners:
  a2a:
    addr: "127.0.0.1:8080"
    route_prefix: "/gateway"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: echo
    path: /custom/echo
    upstreams:
      - url: http://127.0.0.1:9010
`)
	path, aliases := cfg.AgentRoutes(0)
	if path != "/custom/echo" {
		t.Errorf("explicit path = %q, want /custom/echo", path)
	}
	if len(aliases) != 0 {
		t.Errorf("aliases = %v, want empty", aliases)
	}
}

func TestDurationUnmarshalNegative(t *testing.T) {
	_, err := LoadBytes([]byte(`
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: a
    upstreams:
      - url: http://127.0.0.1:9010
card_cache:
  ttl: "-5s"
`))
	if err == nil || !strings.Contains(err.Error(), "card_cache.ttl") {
		t.Fatalf("negative ttl must be rejected, got: %v", err)
	}
}
