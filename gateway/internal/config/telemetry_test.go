package config

import (
	"strings"
	"testing"
	"time"
)

func TestTelemetryDefaults(t *testing.T) {
	cfg := load(t, validYAML())

	tm := cfg.Telemetry
	if tm.ServiceName != "kyurem-gateway" {
		t.Errorf("service_name = %q", tm.ServiceName)
	}
	if tm.OTLPEndpoint != "" {
		t.Errorf("otlp_endpoint default = %q, want export disabled", tm.OTLPEndpoint)
	}
	if tm.OTLPProtocol != "http/protobuf" {
		t.Errorf("otlp_protocol = %q", tm.OTLPProtocol)
	}
	if tm.SampleRatio == nil || *tm.SampleRatio != 1 {
		t.Errorf("sample_ratio = %v, want 1", tm.SampleRatio)
	}
	if tm.QueueSize != 2048 || tm.BatchSize != 512 {
		t.Errorf("queue/batch = %d/%d, want 2048/512", tm.QueueSize, tm.BatchSize)
	}
	if got := tm.BatchTimeout.Std(); got != 5*time.Second {
		t.Errorf("batch_timeout = %s", got)
	}
	if got := tm.ExportTimeout.Std(); got != 10*time.Second {
		t.Errorf("export_timeout = %s", got)
	}
	if tm.MaxRetries != 4 {
		t.Errorf("max_retries = %d", tm.MaxRetries)
	}
	if got := tm.InitialBackoff.Std(); got != 500*time.Millisecond {
		t.Errorf("initial_backoff = %s", got)
	}
	if got := tm.MaxBackoff.Std(); got != 10*time.Second {
		t.Errorf("max_backoff = %s", got)
	}
	if tm.PayloadCapture {
		t.Error("payload_capture must default off (TR-4)")
	}
}

// An explicit 0 is a valid head-sampling choice and must not be replaced by
// the default 1.
func TestTelemetryExplicitZeroSampleRatio(t *testing.T) {
	cfg := load(t, validYAML()+`
telemetry:
  sample_ratio: 0
`)
	if cfg.Telemetry.SampleRatio == nil || *cfg.Telemetry.SampleRatio != 0 {
		t.Fatalf("sample_ratio = %v, want explicit 0", cfg.Telemetry.SampleRatio)
	}
	if got := cfg.Telemetry.EffectiveSampleRatio(); got != 0 {
		t.Fatalf("effective ratio = %v, want 0", got)
	}
}

func TestTelemetryEnvOverrides(t *testing.T) {
	t.Setenv("KYUREM_TELEMETRY_SERVICE_NAME", "kyurem-test")
	t.Setenv("KYUREM_TELEMETRY_OTLP_ENDPOINT", "collector:4317")
	t.Setenv("KYUREM_TELEMETRY_OTLP_PROTOCOL", "grpc")
	t.Setenv("KYUREM_TELEMETRY_SAMPLE_RATIO", "0.25")
	t.Setenv("KYUREM_TELEMETRY_QUEUE_SIZE", "64")
	t.Setenv("KYUREM_TELEMETRY_BATCH_SIZE", "8")
	t.Setenv("KYUREM_TELEMETRY_BATCH_TIMEOUT", "2s")
	t.Setenv("KYUREM_TELEMETRY_EXPORT_TIMEOUT", "3s")
	t.Setenv("KYUREM_TELEMETRY_MAX_RETRIES", "2")
	t.Setenv("KYUREM_TELEMETRY_INITIAL_BACKOFF", "10ms")
	t.Setenv("KYUREM_TELEMETRY_MAX_BACKOFF", "1s")
	t.Setenv("KYUREM_TELEMETRY_OTLP_INSECURE", "true")

	cfg := load(t, validYAML())
	tm := cfg.Telemetry
	if tm.ServiceName != "kyurem-test" || tm.OTLPEndpoint != "collector:4317" || tm.OTLPProtocol != "grpc" {
		t.Fatalf("env endpoint/protocol not applied: %+v", tm)
	}
	if tm.SampleRatio == nil || *tm.SampleRatio != 0.25 {
		t.Fatalf("sample ratio = %v", tm.SampleRatio)
	}
	if tm.QueueSize != 64 || tm.BatchSize != 8 || tm.MaxRetries != 2 {
		t.Fatalf("ints = %+v", tm)
	}
	if tm.BatchTimeout.Std() != 2*time.Second || tm.ExportTimeout.Std() != 3*time.Second {
		t.Fatalf("timeouts = %+v", tm)
	}
	if tm.InitialBackoff.Std() != 10*time.Millisecond || tm.MaxBackoff.Std() != time.Second {
		t.Fatalf("backoffs = %+v", tm)
	}
	if !tm.OTLPInsecure {
		t.Fatal("otlp_insecure env override not applied")
	}
}

func TestTelemetryValidation(t *testing.T) {
	base := `
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: echo
    upstreams:
      - url: http://127.0.0.1:9010
telemetry:
`
	cases := []struct {
		name string
		body string
		want string
	}{
		{"bad protocol", "  otlp_protocol: carrier-pigeon\n", "otlp_protocol"},
		{"ratio high", "  sample_ratio: 1.5\n", "sample_ratio"},
		{"ratio negative", "  sample_ratio: -0.1\n", "sample_ratio"},
		{"endpoint no port", "  otlp_endpoint: collector\n", "otlp_endpoint"},
		{"endpoint bad scheme", "  otlp_endpoint: ftp://collector:4318\n", "scheme"},
		{"endpoint credentials", "  otlp_endpoint: http://user:secret@collector:4318\n", "credentials"},
		{"grpc url", "  otlp_protocol: grpc\n  otlp_endpoint: http://collector:4317\n", "host:port"},
		{"queue negative", "  queue_size: -1\n", "queue_size"},
		{"batch negative", "  batch_size: -1\n", "batch_size"},
		{"batch exceeds queue", "  queue_size: 8\n  batch_size: 9\n", "batch_size"},
		{"batch timeout", "  batch_timeout: -1s\n", "batch_timeout"},
		{"export timeout", "  export_timeout: -1s\n", "export_timeout"},
		{"negative retries", "  max_retries: -1\n", "max_retries"},
		{"too many retries", "  max_retries: 11\n", "max_retries"},
		{"backoff inversion", "  initial_backoff: 2s\n  max_backoff: 1s\n", "max_backoff"},
		{"payload capture", "  payload_capture: true\n", "payload_capture"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(base + tc.body))
			if err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error should mention %q, got: %v", tc.want, err)
			}
		})
	}
}

func TestTelemetryEndpointFormsAccepted(t *testing.T) {
	for _, endpoint := range []string{
		"collector:4318",
		"http://collector:4318",
		"http://collector:4318/v1/traces",
		"https://collector.example.com:4318",
	} {
		raw := validYAML() + "\ntelemetry:\n  otlp_endpoint: \"" + endpoint + "\"\n"
		if _, err := LoadBytes([]byte(raw)); err != nil {
			t.Errorf("endpoint %q should be accepted: %v", endpoint, err)
		}
	}
}
