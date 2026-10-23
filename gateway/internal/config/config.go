// Package config defines the Kyurem gateway configuration: loading,
// defaults, environment overrides and validation.
//
// Design goals (PRD §2.3, §8):
//   - one YAML file, validated at startup with clear, field-scoped errors;
//   - `kyurem validate` reuses load without starting the server;
//   - scalar leaves can be overridden from the environment so containers
//     stay declarative (agents remain a list and are configured via file);
//   - unknown keys are rejected (catches typos early);
//   - every duration accepts a string like "60s" or a plain number of
//     seconds.
package config

import (
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that (un)marshals from YAML/JSON strings such
// as "60s", "1m", or a plain number treated as seconds (DX sugar).
type Duration time.Duration

func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	parsed, err := parseDurationValue(v)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var v any
	switch node.Tag {
	case "!!int":
		n, err := strconv.ParseInt(node.Value, 10, 64)
		if err != nil {
			v = node.Value
		} else {
			v = float64(n)
		}
	case "!!float":
		n, err := strconv.ParseFloat(node.Value, 64)
		if err != nil {
			v = node.Value
		} else {
			v = n
		}
	default:
		v = node.Value
	}
	parsed, err := parseDurationValue(v)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	*d = parsed
	return nil
}

func parseDurationValue(v any) (Duration, error) {
	switch t := v.(type) {
	case float64:
		return Duration(time.Duration(t * float64(time.Second))), nil
	case int:
		return Duration(time.Duration(t) * time.Second), nil
	case string:
		if t == "" {
			return 0, errors.New("empty duration")
		}
		// Bare numbers ("60") mean seconds, consistent with numeric YAML.
		if !strings.ContainsAny(t, "smh") {
			n, err := strconv.ParseFloat(t, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q: use \"60s\" or seconds", t)
			}
			return Duration(time.Duration(n * float64(time.Second))), nil
		}
		parsed, err := time.ParseDuration(t)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", t, err)
		}
		return Duration(parsed), nil
	default:
		return 0, fmt.Errorf("duration must be a string or a number of seconds, got %T", v)
	}
}

// Listeners configures the HTTP listeners.
type Listeners struct {
	A2A   A2AListener `yaml:"a2a" env:"A2A"`
	Admin Admin       `yaml:"admin" env:"ADMIN"`
	// Mgmt hosts the management API (M3). It is separate from the A2A
	// data path on purpose: the data path stays transparent, the
	// management API is authenticated and versioned.
	Mgmt Mgmt `yaml:"mgmt" env:"MGMT"`
}

// Mgmt is the management API listener.
type Mgmt struct {
	Addr string `yaml:"addr" env:"ADDR" default:"127.0.0.1:8082"`
}

// A2AListener is the A2A data path listener.
type A2AListener struct {
	Addr string `yaml:"addr" env:"ADDR"`
	// PublicBaseURL is used to rewrite agent card interface URLs so that
	// discovery loops back through the gateway. When empty the gateway
	// derives a base URL from the incoming request's Host header.
	PublicBaseURL string `yaml:"public_base_url" env:"PUBLIC_BASE_URL"`
	// RoutePrefix is the path prefix under which agents are exposed,
	// defaulting to /a2a.
	RoutePrefix string `yaml:"route_prefix" env:"ROUTE_PREFIX"`
	// TLS terminates TLS on the data path. Optional client certificates
	// provide mTLS caller identities (TP-5).
	TLS TLS `yaml:"tls" env:"TLS"`
}

// TLS configures transport security on the A2A listener. CertFile and
// KeyFile are both required to enable TLS. ClientCAFile enables client
// certificate verification; a certificate is optional at the handshake
// because callers may authenticate with a bearer token instead.
type TLS struct {
	CertFile string `yaml:"cert_file" env:"CERT_FILE"`
	KeyFile  string `yaml:"key_file" env:"KEY_FILE"`
	// ClientCAFile is the PEM bundle of certificate authorities trusted for
	// client certificates.
	ClientCAFile string `yaml:"client_ca_file" env:"CLIENT_CA_FILE"`
	// MinVersion is "1.2" (default) or "1.3".
	MinVersion string `yaml:"min_version" env:"MIN_VERSION" default:"1.2"`
}

// Callers configures authentication of callers into the gateway (TP-5).
// Credentials are optional or required depending on Mode; the resolved
// caller identity is what the policy engine evaluates.
type Callers struct {
	// Mode is off, optional or required.
	Mode string `yaml:"mode" env:"MODE" default:"off"`
	// BearerTokens maps static bearer tokens to caller names. Tokens may be
	// written inline or resolved from an environment variable (token_env).
	BearerTokens []BearerToken `yaml:"bearer_tokens"`
}

// BearerToken is one caller credential.
type BearerToken struct {
	// Name is the caller identity reported to policy and the audit log.
	Name string `yaml:"name"`
	// Token is the literal bearer token. Either Token or TokenEnv must be
	// set, never both.
	Token string `yaml:"token"`
	// TokenEnv names the environment variable holding the token.
	TokenEnv string `yaml:"token_env"`
}

// Trust configures agent card signature verification (TP-1, TP-2).
type Trust struct {
	// Mode is the default mode for every agent: off, warn or enforce.
	Mode string `yaml:"mode" env:"MODE" default:"off"`
	// Keys are the public keys the gateway trusts.
	Keys []TrustedKey `yaml:"keys"`
	// Agents overrides the mode and/or restricts the allowed keys per agent.
	Agents []AgentTrust `yaml:"agents"`
}

// TrustedKey is one verifier-side public key. The signature's kid selects
// it; a signer-supplied jku is never fetched or trusted.
type TrustedKey struct {
	// ID is the JWS key id (kid).
	ID string `yaml:"id"`
	// PublicKeyPEM, PublicKeyFile and PublicKeyEnv are three ways to supply
	// the PEM-encoded public key; exactly one must be set.
	PublicKeyPEM  string `yaml:"public_key_pem"`
	PublicKeyFile string `yaml:"public_key_file"`
	PublicKeyEnv  string `yaml:"public_key_env"`
}

// AgentTrust overrides trust settings for one agent.
type AgentTrust struct {
	Name string `yaml:"name"`
	// Mode inherits trust.mode when empty.
	Mode string `yaml:"mode"`
	// Keys restricts verification to these key ids. Empty means every
	// configured key is acceptable.
	Keys []string `yaml:"keys"`
}

// Policy configures the caller -> callee authorization engine (TP-3).
type Policy struct {
	// Mode is off, warn or enforce. Warn records decisions but allows the
	// request; enforce blocks denied calls.
	Mode string `yaml:"mode" env:"MODE" default:"off"`
	// Default is allow or deny when no rule matches.
	Default string `yaml:"default" env:"DEFAULT" default:"allow"`
	// Rules are evaluated deny-overrides: any matching deny wins.
	Rules []PolicyRule `yaml:"rules"`
	// AuditCapacity bounds the in-process decision log served by the
	// management API.
	AuditCapacity int `yaml:"audit_capacity" env:"AUDIT_CAPACITY" default:"1024"`
}

// PolicyRule authorizes (or forbids) calls from a caller to a callee.
// Caller and Callee accept "*" as a wildcard. Skill filters use an
// untrusted caller hint and are therefore rejected in enforce mode.
type PolicyRule struct {
	Name   string   `yaml:"name"`
	Caller string   `yaml:"caller"`
	Callee string   `yaml:"callee"`
	Skills []string `yaml:"skills,omitempty"`
	// Effect is allow or deny.
	Effect string `yaml:"effect"`
}

// Admin hosts health/readiness/metrics (and, in M3, the management API).
type Admin struct {
	Addr string `yaml:"addr" env:"ADDR"`
}

type LogConfig struct {
	Level  string `yaml:"level" env:"LEVEL" default:"info"`
	Format string `yaml:"format" env:"FORMAT" default:"json"`
}

// Upstream describes one upstream agent endpoint.
type Upstream struct {
	// URL is the upstream base: for JSONRPC binding this is the endpoint a
	// JSON-RPC POST is sent to; the origin of this URL is also used to
	// fetch the agent card (overridable via CardURL).
	URL string `yaml:"url"`
	// CardURL optionally overrides where cards are fetched from (needed
	// when the agent's A2A endpoint is not the deployment root).
	CardURL string `yaml:"card_url,omitempty"`
	// Binding optionally restricts which requests route here:
	// JSONRPC, HTTP+JSON, GRPC (GW-8 passthrough is P2), or a custom token.
	Binding string `yaml:"binding,omitempty"`
}

// AgentReg is one registry entry, keyed by name and optional aliases.
type AgentReg struct {
	Name string `yaml:"name"`
	// Aliases are extra, case-insensitive routing lookup keys.
	Aliases []string `yaml:"aliases,omitempty"`
	// Path overrides the default listen path `{route_prefix}/{name}`.
	// Validation normalizes it (leading slash, no trailing slash).
	Path string `yaml:"path,omitempty"`
	// Tags describe the agent (M3 dashboard grouping).
	Tags []string `yaml:"tags,omitempty"`
	// Upstreams, in priority order; failover handling (M6) reuses this
	// order.
	Upstreams []Upstream `yaml:"upstreams"`
}

type CardCache struct {
	// TTL is how long a fetched card is considered fresh.
	TTL Duration `yaml:"ttl" env:"TTL" default:"60s"`
	// StaleGrace bounds how long a stale card is served after refresh
	// failures. Beyond it the route answers with an error so callers
	// notice outages instead of silently using very old cards.
	StaleGrace Duration `yaml:"stale_grace" env:"STALE_GRACE" default:"10m"`
	// MaxCardBytes is a hard cap on a fetched card's size; untrusted
	// input is always bounded.
	MaxCardBytes int64 `yaml:"max_card_bytes" env:"MAX_CARD_BYTES" default:"262144"` // 256 KiB
}

type Timeouts struct {
	// Connect is the upstream TCP dial timeout.
	Connect Duration `yaml:"connect" env:"CONNECT" default:"5s"`
	// ResponseHeader bounds waiting for the first response byte.
	ResponseHeader Duration `yaml:"response_header" env:"RESPONSE_HEADER" default:"30s"`
	// Request is the total deadline per proxied request, streams
	// included. Zero means no total deadline; long-running A2A tasks rely
	// on ResponseHeader plus client cancellation.
	Request Duration `yaml:"request" env:"REQUEST" default:"0s"`
}

type Limits struct {
	// MaxConcurrentStreams bounds in-flight SSE responses.
	MaxConcurrentStreams int64 `yaml:"max_concurrent_streams" env:"MAX_CONCURRENT_STREAMS" default:"1024"`
	// MaxRequestBodyBytes caps proxied request bodies.
	MaxRequestBodyBytes int64 `yaml:"max_request_body_bytes" env:"MAX_REQUEST_BODY_BYTES" default:"67108864"`
	// ShutdownGrace is the drain deadline on SIGTERM/SIGINT.
	ShutdownGrace Duration `yaml:"shutdown_grace" env:"SHUTDOWN_GRACE" default:"30s"`
	Timeouts      Timeouts `yaml:"timeouts" env:"TIMEOUTS"`
}

// FailureHandling bounds safe retries and per-upstream circuit breakers.
type FailureHandling struct {
	MaxAttempts       int            `yaml:"max_attempts" env:"MAX_ATTEMPTS" default:"2"`
	RetryBudget       RetryBudget    `yaml:"retry_budget" env:"RETRY_BUDGET"`
	CircuitBreaker    CircuitBreaker `yaml:"circuit_breaker" env:"CIRCUIT_BREAKER"`
	MaxRetryBodyBytes int64          `yaml:"max_retry_body_bytes" env:"MAX_RETRY_BODY_BYTES" default:"65536"`
}

type RetryBudget struct {
	MaxRetries int      `yaml:"max_retries" env:"MAX_RETRIES" default:"10"`
	Window     Duration `yaml:"window" env:"WINDOW" default:"1m"`
}

type CircuitBreaker struct {
	FailureThreshold int      `yaml:"failure_threshold" env:"FAILURE_THRESHOLD" default:"5"`
	OpenDuration     Duration `yaml:"open_duration" env:"OPEN_DURATION" default:"30s"`
}

// Telemetry configures OTLP trace export (M2). Trace-context propagation
// always runs, even when export is disabled; only the exporter is optional.
type Telemetry struct {
	// ServiceName is the OpenTelemetry service.name resource attribute.
	ServiceName string `yaml:"service_name" env:"SERVICE_NAME" default:"kyurem-gateway"`
	// OTLPEndpoint is the collector endpoint. Empty disables export while
	// keeping W3C propagation. http/protobuf accepts `host:port` or an
	// http(s):// URL; grpc accepts `host:port`.
	OTLPEndpoint string `yaml:"otlp_endpoint" env:"OTLP_ENDPOINT"`
	// OTLPProtocol is `http/protobuf` or `grpc`.
	OTLPProtocol string `yaml:"otlp_protocol" env:"OTLP_PROTOCOL" default:"http/protobuf"`
	// OTLPInsecure disables TLS for `host:port` endpoints (local collectors).
	// URL endpoints decide by scheme.
	OTLPInsecure bool `yaml:"otlp_insecure" env:"OTLP_INSECURE"`
	// SampleRatio is the head-sampling probability for gateway-rooted
	// traces. Inbound sampled traces always follow the caller's decision.
	// A pointer keeps an explicit 0 (sample nothing at the root) distinct
	// from "unset" (default 1).
	SampleRatio *float64 `yaml:"sample_ratio" env:"SAMPLE_RATIO" default:"1"`
	// QueueSize bounds spans waiting for export; overflow is counted and
	// dropped, never blocking the data path (TR-5).
	QueueSize int `yaml:"queue_size" env:"QUEUE_SIZE" default:"2048"`
	// BatchSize is the maximum spans per OTLP request.
	BatchSize int `yaml:"batch_size" env:"BATCH_SIZE" default:"512"`
	// BatchTimeout flushes a partial batch after this delay.
	BatchTimeout Duration `yaml:"batch_timeout" env:"BATCH_TIMEOUT" default:"5s"`
	// ExportTimeout bounds one OTLP export attempt (per retry).
	ExportTimeout Duration `yaml:"export_timeout" env:"EXPORT_TIMEOUT" default:"10s"`
	// MaxRetries is the number of retries after a failed export attempt.
	MaxRetries int `yaml:"max_retries" env:"MAX_RETRIES" default:"4"`
	// InitialBackoff and MaxBackoff bound the exponential retry delay.
	InitialBackoff Duration `yaml:"initial_backoff" env:"INITIAL_BACKOFF" default:"500ms"`
	MaxBackoff     Duration `yaml:"max_backoff" env:"MAX_BACKOFF" default:"10s"`
	// PayloadCapture is reserved for TR-4 (off by default per PRD). M2
	// rejects true: capturing payloads is not implemented yet.
	PayloadCapture bool `yaml:"payload_capture" env:"PAYLOAD_CAPTURE"`
}

// EffectiveSampleRatio returns the configured head-sampling probability,
// defaulting to 1 when the field was constructed without config loading.
func (t Telemetry) EffectiveSampleRatio() float64 {
	if t.SampleRatio == nil {
		return 1
	}
	return *t.SampleRatio
}

// Management configures the management API (M3): its own listener, its
// authentication and where the dashboard reads its data from.
type Management struct {
	// Enabled serves the management API. When false the listener is not
	// started and /api/v1 answers nothing.
	Enabled bool `yaml:"enabled" env:"ENABLED" default:"true"`
	// AuthToken is the bearer token the API requires ("Authorization:
	// Bearer <token>"). Empty means the API is open, which is only
	// acceptable on a loopback listener: validation rejects an open API
	// bound to a non-loopback address.
	AuthToken string `yaml:"auth_token" env:"AUTH_TOKEN"`
	// Storage selects where the dashboard data comes from.
	Storage Storage `yaml:"storage" env:"STORAGE"`
}

// Storage selects the span store behind the management API.
type Storage struct {
	// Driver is "memory" (bounded in-process ring, no dependencies) or
	// "clickhouse" (the store the OTel collector writes to).
	Driver string `yaml:"driver" env:"DRIVER" default:"memory"`
	// MemoryCapacity is the number of spans the in-process ring keeps.
	MemoryCapacity int `yaml:"memory_capacity" env:"MEMORY_CAPACITY" default:"4096"`
	// ClickHouse configures the clickhouse driver.
	ClickHouse ClickHouse `yaml:"clickhouse" env:"CLICKHOUSE"`
}

// ClickHouse points at the ClickHouse HTTP interface that holds the spans.
type ClickHouse struct {
	// URL is the HTTP endpoint, e.g. "http://clickhouse:8123". The
	// "clickhouse://" scheme is accepted as an alias.
	URL string `yaml:"url" env:"URL"`
	// Database holds the spans (the collector creates it, "otel" by
	// default).
	Database string `yaml:"database" env:"DATABASE" default:"otel"`
	// Username and Password authenticate the queries.
	Username string `yaml:"username" env:"USERNAME"`
	Password string `yaml:"password" env:"PASSWORD"`
	// Timeout bounds one query.
	Timeout Duration `yaml:"timeout" env:"TIMEOUT" default:"10s"`
}

// Price is one entry of the price table (CO-2). The model key matches the
// model an agent reports (gen_ai.request.model); an agent that reports no
// model has no computable cost, which is shown as unknown, never as zero.
type Price struct {
	// Model is the model name (or alias) the prices apply to.
	Model string `yaml:"model"`
	// InputPer1KUSD is the price per 1000 input tokens.
	InputPer1KUSD float64 `yaml:"input_per_1k_usd"`
	// OutputPer1KUSD is the price per 1000 output tokens.
	OutputPer1KUSD float64 `yaml:"output_per_1k_usd"`
}

// Config is the root configuration document.
type Config struct {
	Listeners       Listeners       `yaml:"listeners" env:"LISTENERS"`
	Logging         LogConfig       `yaml:"logging" env:"LOGGING"`
	Telemetry       Telemetry       `yaml:"telemetry" env:"TELEMETRY"`
	Management      Management      `yaml:"management" env:"MANAGEMENT"`
	CardCache       CardCache       `yaml:"card_cache" env:"CARD_CACHE"`
	Limits          Limits          `yaml:"limits" env:"LIMITS"`
	FailureHandling FailureHandling `yaml:"failure_handling" env:"FAILURE_HANDLING"`
	// Callers, Trust and Policy implement M5: caller authentication,
	// signed-card verification and the authorization matrix.
	Callers Callers    `yaml:"callers" env:"CALLERS"`
	Trust   Trust      `yaml:"trust" env:"TRUST"`
	Policy  Policy     `yaml:"policy" env:"POLICY"`
	Pricing []Price    `yaml:"pricing"`
	Agents  []AgentReg `yaml:"agents"`
}

// Load reads, defaults, environment-overrides and validates a config file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return LoadBytes(raw)
}

// LoadBytes is the in-memory variant used by tests, the integration
// harness and `kyurem validate`. Input is optional but never a complete
// document on its own: defaults fill in what is missing and validation
// still applies (at minimum listeners and one agent are required).
func LoadBytes(raw []byte) (*Config, error) {
	cfg := &Config{}
	if len(raw) > 0 {
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true) // reject unknown keys: catches typos early
		if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}
	applyDefaults(cfg)
	if err := applyEnvOverrides(cfg); err != nil {
		return nil, err
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// WalkState exists only to document the traversal used by the reflection
// helpers below; it holds no state.
type walkMode int

const (
	walkDefaults walkMode = iota
	walkEnv
)

// applyDefaults fills leaf fields tagged `default:"..."` that are still
// zero. The traversal mirrors applyEnvOverrides so both stay in sync.
func applyDefaults(cfg *Config) {
	walk(cfg, walkDefaults, nil)
}

// applyEnvOverrides sets leaf fields from KYUREM_* environment variables.
// Names are built from the nested `env` tags, e.g.:
//
//	Listeners.A2A.Addr      -> KYUREM_LISTENERS_A2A_ADDR
//	CardCache.TTL           -> KYUREM_CARD_CACHE_TTL
//
// Agents (a list) and fields tagged envskip are file-only, documented in
// docs/configuration.md.
func applyEnvOverrides(cfg *Config) error {
	var envErrs []error
	walk(cfg, walkEnv, &envErrs)
	return errors.Join(envErrs...)
}

func walk(root any, mode walkMode, envErrs *[]error) {
	walkStruct(reflect.TypeOf(root).Elem(), reflect.ValueOf(root).Elem(), "", mode, envErrs)
}

func walkStruct(t reflect.Type, val reflect.Value, prefix string, mode walkMode, envErrs *[]error) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Tag.Get("envskip") == "true" {
			continue
		}
		envSeg, hasEnv := f.Tag.Lookup("env")
		fv := val.Field(i)

		if f.Type.Kind() == reflect.Struct {
			// Named structs form an env-prefix segment; default-only
			// structs (timeouts) recurse transparently.
			nextPrefix := prefix
			if hasEnv && mode == walkEnv {
				nextPrefix = prefix + envSeg + "_"
			}
			walkStruct(f.Type, fv, nextPrefix, mode, envErrs)
			continue
		}

		switch mode {
		case walkDefaults:
			def, ok := f.Tag.Lookup("default")
			if !ok {
				continue
			}
			if fv.Kind() == reflect.Ptr {
				if fv.IsNil() {
					elem := reflect.New(fv.Type().Elem())
					if err := setScalar(elem.Elem(), def); err != nil {
						panic(fmt.Sprintf("bad default tag %s.%s: %v", t.Name(), f.Name, err))
					}
					fv.Set(elem)
				}
				continue
			}
			if fv.IsZero() {
				if err := setScalar(fv, def); err != nil {
					panic(fmt.Sprintf("bad default tag %s.%s: %v", t.Name(), f.Name, err))
				}
			}
		case walkEnv:
			if !hasEnv {
				continue
			}
			key := "KYUREM_" + prefix + envSeg
			if raw, ok := os.LookupEnv(key); ok {
				target := fv
				var ptr reflect.Value
				if fv.Kind() == reflect.Ptr {
					ptr = reflect.New(fv.Type().Elem())
					target = ptr.Elem()
				}
				if err := setScalar(target, raw); err != nil {
					*envErrs = append(*envErrs, fmt.Errorf("%s=%q: %w", key, raw, err))
				} else if ptr.IsValid() {
					fv.Set(ptr)
				}
			}
		}
	}
}

func setScalar(fv reflect.Value, raw string) error {
	switch fv.Type() {
	case reflect.TypeOf(Duration(0)):
		parsed, err := time.ParseDuration(raw)
		if err == nil {
			fv.Set(reflect.ValueOf(Duration(parsed)))
			return nil
		}
		// Fall back to bare-number seconds for parity with YAML handling.
		n, err2 := strconv.ParseFloat(raw, 64)
		if err2 != nil {
			return fmt.Errorf("invalid duration %q: %v", raw, err)
		}
		fv.Set(reflect.ValueOf(Duration(time.Duration(n * float64(time.Second)))))
	case reflect.TypeOf(""):
		fv.SetString(raw)
	default:
		switch fv.Kind() {
		case reflect.Int64, reflect.Int:
			n, err := strconv.ParseInt(raw, 10, fv.Type().Bits())
			if err != nil {
				return fmt.Errorf("not an integer: %w", err)
			}
			fv.SetInt(n)
		case reflect.Float32, reflect.Float64:
			n, err := strconv.ParseFloat(raw, fv.Type().Bits())
			if err != nil {
				return fmt.Errorf("not a number: %w", err)
			}
			fv.SetFloat(n)
		case reflect.Bool:
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return fmt.Errorf("not a boolean: %w", err)
			}
			fv.SetBool(b)
		default:
			return fmt.Errorf("unsupported field type %s", fv.Type())
		}
	}
	return nil
}

// ResolvePEM returns the PEM bytes of a trusted key from exactly one of the
// configured sources: inline PEM, a file, or an environment variable.
func (k TrustedKey) ResolvePEM() ([]byte, error) {
	sources := 0
	if strings.TrimSpace(k.PublicKeyPEM) != "" {
		sources++
	}
	if strings.TrimSpace(k.PublicKeyFile) != "" {
		sources++
	}
	if strings.TrimSpace(k.PublicKeyEnv) != "" {
		sources++
	}
	switch sources {
	case 0:
		return nil, fmt.Errorf("trusted key %q: one of public_key_pem, public_key_file or public_key_env is required", k.ID)
	case 1:
	default:
		return nil, fmt.Errorf("trusted key %q: only one of public_key_pem, public_key_file or public_key_env may be set", k.ID)
	}

	var raw []byte
	var err error
	switch {
	case strings.TrimSpace(k.PublicKeyFile) != "":
		raw, err = os.ReadFile(k.PublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("trusted key %q: read public_key_file: %w", k.ID, err)
		}
	case strings.TrimSpace(k.PublicKeyEnv) != "":
		value, ok := os.LookupEnv(k.PublicKeyEnv)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("trusted key %q: environment variable %s is empty or unset", k.ID, k.PublicKeyEnv)
		}
		raw = []byte(value)
	default:
		raw = []byte(k.PublicKeyPEM)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, fmt.Errorf("trusted key %q: public key is empty", k.ID)
	}
	return raw, nil
}

// ParsePublicKeyPEM parses a PEM-encoded public key. PKIX ("PUBLIC KEY"),
// PKCS#1 ("RSA PUBLIC KEY") and the first certificate in a PEM bundle are
// accepted; the algorithms supported by a2acrypto (ECDSA, RSA, Ed25519)
// follow from the key type.
func ParsePublicKeyPEM(data []byte) (crypto.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	switch block.Type {
	case "PUBLIC KEY":
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKIX public key: %w", err)
		}
		return key, nil
	case "RSA PUBLIC KEY":
		key, err := x509.ParsePKCS1PublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#1 public key: %w", err)
		}
		return key, nil
	case "CERTIFICATE":
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}
		return cert.PublicKey, nil
	default:
		// Try PKIX anyway: some tools label blocks differently.
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("unsupported PEM block %q (want PUBLIC KEY, RSA PUBLIC KEY or CERTIFICATE)", block.Type)
		}
		return key, nil
	}
}

// ResolveToken returns the bearer token literal or the value of the
// configured environment variable. Exactly one source must be set.
func (t BearerToken) ResolveToken() (string, error) {
	hasLiteral := strings.TrimSpace(t.Token) != ""
	hasEnv := strings.TrimSpace(t.TokenEnv) != ""
	switch {
	case hasLiteral && hasEnv:
		return "", fmt.Errorf("caller %q: token and token_env are mutually exclusive", t.Name)
	case hasEnv:
		value, ok := os.LookupEnv(t.TokenEnv)
		if !ok || value == "" {
			return "", fmt.Errorf("caller %q: environment variable %s is empty or unset", t.Name, t.TokenEnv)
		}
		return value, nil
	case hasLiteral:
		return t.Token, nil
	default:
		return "", fmt.Errorf("caller %q: token or token_env is required", t.Name)
	}
}
