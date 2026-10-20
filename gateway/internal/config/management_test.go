package config

import (
	"os"
	"strings"
	"testing"
)

// M3: the management API configuration (listener, auth, storage driver) and
// the price table are loaded, overridden and validated like everything else.
func TestManagementDefaults(t *testing.T) {
	cfg, err := LoadBytes([]byte(`
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: echo
    upstreams:
      - url: http://127.0.0.1:9010
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Listeners.Mgmt.Addr != "127.0.0.1:8082" {
		t.Errorf("mgmt addr = %q, want 127.0.0.1:8082", cfg.Listeners.Mgmt.Addr)
	}
	if !cfg.Management.Enabled {
		t.Error("management API disabled by default")
	}
	if cfg.Management.Storage.Driver != "memory" {
		t.Errorf("storage driver = %q, want memory", cfg.Management.Storage.Driver)
	}
	if cfg.Management.Storage.MemoryCapacity != 4096 {
		t.Errorf("memory capacity = %d, want 4096", cfg.Management.Storage.MemoryCapacity)
	}
	if cfg.Management.Storage.ClickHouse.Database != "otel" {
		t.Errorf("clickhouse database = %q, want otel", cfg.Management.Storage.ClickHouse.Database)
	}
}

func TestManagementEnvOverrides(t *testing.T) {
	t.Setenv("KYUREM_LISTENERS_MGMT_ADDR", "0.0.0.0:9099")
	t.Setenv("KYUREM_MANAGEMENT_AUTH_TOKEN", "s3cret")
	t.Setenv("KYUREM_MANAGEMENT_STORAGE_DRIVER", "clickhouse")
	t.Setenv("KYUREM_MANAGEMENT_STORAGE_CLICKHOUSE_URL", "http://clickhouse:8123")
	t.Setenv("KYUREM_MANAGEMENT_STORAGE_CLICKHOUSE_PASSWORD", "pw")

	cfg, err := LoadBytes([]byte(`
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: echo
    upstreams:
      - url: http://127.0.0.1:9010
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Listeners.Mgmt.Addr != "0.0.0.0:9099" {
		t.Errorf("mgmt addr = %q", cfg.Listeners.Mgmt.Addr)
	}
	if cfg.Management.AuthToken != "s3cret" {
		t.Errorf("auth token = %q", cfg.Management.AuthToken)
	}
	if cfg.Management.Storage.Driver != "clickhouse" {
		t.Errorf("driver = %q", cfg.Management.Storage.Driver)
	}
	if cfg.Management.Storage.ClickHouse.URL != "http://clickhouse:8123" {
		t.Errorf("clickhouse url = %q", cfg.Management.Storage.ClickHouse.URL)
	}
	if cfg.Management.Storage.ClickHouse.Password != "pw" {
		t.Errorf("clickhouse password = %q", cfg.Management.Storage.ClickHouse.Password)
	}
}

func TestManagementValidation(t *testing.T) {
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
`
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "unknown driver",
			yaml:    base + "management:\n  storage:\n    driver: postgres\n",
			wantErr: "management.storage.driver",
		},
		{
			name:    "clickhouse without url",
			yaml:    base + "management:\n  storage:\n    driver: clickhouse\n",
			wantErr: "management.storage.clickhouse.url",
		},
		{
			name:    "negative memory capacity",
			yaml:    base + "management:\n  storage:\n    memory_capacity: -1\n",
			wantErr: "memory_capacity",
		},
		{
			name: "open api on a public listener",
			yaml: `
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
  mgmt:
    addr: "0.0.0.0:8082"
agents:
  - name: echo
    upstreams:
      - url: http://127.0.0.1:9010
`,
			wantErr: "management.auth_token",
		},
		{
			name:    "pricing without a model",
			yaml:    base + "pricing:\n  - input_per_1k_usd: 0.1\n",
			wantErr: "pricing[0].model",
		},
		{
			name:    "negative price",
			yaml:    base + "pricing:\n  - model: m\n    input_per_1k_usd: -1\n",
			wantErr: "pricing[0].input_per_1k_usd",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected a validation error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestManagementPublicListenerNeedsToken(t *testing.T) {
	yaml := `
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
  mgmt:
    addr: "0.0.0.0:8082"
agents:
  - name: echo
    upstreams:
      - url: http://127.0.0.1:9010
management:
  auth_token: "s3cret"
`
	if _, err := LoadBytes([]byte(yaml)); err != nil {
		t.Fatalf("a public listener with a token must validate: %v", err)
	}
}

func TestPricingIsFileOnly(t *testing.T) {
	// A list cannot be overridden from the environment.
	t.Setenv("KYUREM_PRICING", "model: m")
	cfg, err := LoadBytes([]byte(`
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: echo
    upstreams:
      - url: http://127.0.0.1:9010
pricing:
  - model: demo-echo
    input_per_1k_usd: 0.001
    output_per_1k_usd: 0.002
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Pricing) != 1 || cfg.Pricing[0].Model != "demo-echo" {
		t.Fatalf("pricing = %+v", cfg.Pricing)
	}
	if cfg.Pricing[0].InputPer1KUSD != 0.001 || cfg.Pricing[0].OutputPer1KUSD != 0.002 {
		t.Errorf("pricing = %+v", cfg.Pricing[0])
	}
}

func TestShippedConfigsValidate(t *testing.T) {
	for _, path := range []string{"../../../config/kyurem.example.yaml", "../../../config/kyurem.compose.yaml"} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("config %s not available: %v", path, err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if !cfg.Management.Enabled {
			t.Errorf("%s: management API disabled", path)
		}
	}
}
