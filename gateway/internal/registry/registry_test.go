package registry

import (
	"fmt"
	"testing"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

const registryYAMLTemplate = `
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: echo
    aliases: [greeter]
    upstreams:
      - url: %s
        binding: JSONRPC
  - name: mixed
    upstreams:
      - url: http://127.0.0.1:9020
        binding: JSONRPC
      - url: http://127.0.0.1:9021
        binding: HTTP+JSON
`

func loadRegistry(t *testing.T, upstreamURL string) (*config.Config, *Registry) {
	t.Helper()
	cfg, err := config.LoadBytes([]byte(fmt.Sprintf(registryYAMLTemplate, upstreamURL)))
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	reg, err := New(cfg)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return cfg, reg
}

func TestRoutingResolution(t *testing.T) {
	cfg, reg := loadRegistry(t, "http://127.0.0.1:9010")

	if reg.Len() != 2 {
		t.Fatalf("registry size = %d, want 2", reg.Len())
	}
	echoPath, _ := cfg.AgentRoutes(0)
	if echoPath != "/a2a/echo" {
		t.Fatalf("echo path = %q", echoPath)
	}

	// canonical path
	if e := reg.LookupPath("/a2a/echo"); e == nil || e.Name != "echo" {
		t.Fatalf("LookupPath(/a2a/echo) = %v", e)
	}
	// alias path
	if e := reg.LookupPath("/a2a/greeter"); e == nil || e.Name != "echo" {
		t.Fatalf("LookupPath(/a2a/greeter) = %v", e)
	}
	// case-insensitive exact match only: no partial matches
	if reg.LookupPath("/a2a/ECHELON") != nil {
		t.Fatal("LookupPath must not match unrelated paths")
	}
	if reg.LookupPath("/a2a/echosuffix") != nil {
		t.Fatal("LookupPath must be exact-suffix aware, not a plain prefix find")
	}
	// by key (name or alias)
	if e := reg.LookupKey("GREETER"); e == nil || e.Name != "echo" {
		t.Fatalf("LookupKey(greeter) = %v", e)
	}
	if reg.LookupKey("nope") != nil {
		t.Fatal("unknown key must resolve nil")
	}
}

func TestPickUpstreamBindingMatching(t *testing.T) {
	_, reg := loadRegistry(t, "http://127.0.0.1:9010")
	mixed := reg.LookupKey("mixed")

	// JSONRPC request routes to the JSONRPC-annotated upstream.
	if u := mixed.PickUpstream("JSONRPC"); u.URL != "http://127.0.0.1:9020" {
		t.Errorf("JSONRPC pick = %q", u.URL)
	}
	// HTTP+JSON request routes to the annotated REST upstream.
	if u := mixed.PickUpstream("HTTP+JSON"); u.URL != "http://127.0.0.1:9021" {
		t.Errorf("REST pick = %q", u.URL)
	}
	// Unknown/custom bindings fall back to the first upstream (ordering
	// becomes failover priority in M6).
	if u := mixed.PickUpstream("GRPC"); u.URL != "http://127.0.0.1:9020" {
		t.Errorf("fallback pick = %q", u.URL)
	}

	echo := reg.LookupKey("echo")
	if u := echo.PickUpstream(""); u == nil || u.URL != "http://127.0.0.1:9010" {
		t.Errorf("single upstream fallback = %v", u)
	}
}

func TestCardFetchURL(t *testing.T) {
	_, reg := loadRegistry(t, "http://127.0.0.1:9010/invoke")
	echo := reg.LookupKey("echo")
	// Card lives at the origin + well-known path, even when the upstream
	// JSON-RPC endpoint itself has a path suffix.
	if got := echo.CardFetchURL(); got != "http://127.0.0.1:9010/.well-known/agent-card.json" {
		t.Errorf("CardFetchURL = %q", got)
	}

	// Explicit card_url override wins.
	cfg, err := config.LoadBytes([]byte(`
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: alt
    upstreams:
      - url: http://127.0.0.1:9030/invoke
        card_url: http://127.0.0.1:9030/discover/card.json
`))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	reg2, err := New(cfg)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if got := reg2.LookupKey("alt").CardFetchURL(); got != "http://127.0.0.1:9030/discover/card.json" {
		t.Errorf("explicit card url = %q", got)
	}
}
