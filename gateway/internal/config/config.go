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
	"encoding/json"
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

// Listeners configures the two HTTP listeners.
type Listeners struct {
	A2A   A2AListener `yaml:"a2a" env:"A2A"`
	Admin Admin       `yaml:"admin" env:"ADMIN"`
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

type Telemetry struct {
	// OTLPEndpoint is accepted now (validated) and consumed by tracing in
	// M2. Empty disables export.
	OTLPEndpoint string `yaml:"otlp_endpoint" env:"OTLP_ENDPOINT"`
	// PayloadCapture is reserved for TR-4 (off by default per PRD).
	PayloadCapture bool `yaml:"payload_capture" env:"PAYLOAD_CAPTURE"`
}

// Config is the root configuration document.
type Config struct {
	Listeners Listeners  `yaml:"listeners" env:"LISTENERS"`
	Logging   LogConfig  `yaml:"logging" env:"LOGGING"`
	Telemetry Telemetry  `yaml:"telemetry" env:"TELEMETRY"`
	CardCache CardCache  `yaml:"card_cache" env:"CARD_CACHE"`
	Limits    Limits     `yaml:"limits" env:"LIMITS"`
	Agents    []AgentReg `yaml:"agents"`
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
			if def, ok := f.Tag.Lookup("default"); ok && fv.IsZero() {
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
				if err := setScalar(fv, raw); err != nil {
					*envErrs = append(*envErrs, fmt.Errorf("%s=%q: %w", key, raw, err))
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
