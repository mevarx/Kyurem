package authn

import (
	"net/http/httptest"
	"testing"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

func TestRequiredBearerAuthentication(t *testing.T) {
	a, err := New(config.Callers{Mode: "required", BearerTokens: []config.BearerToken{{Name: "worker", Token: "secret-token"}}})
	if err != nil {
		t.Fatal(err)
	}

	missing := httptest.NewRequest("POST", "/", nil)
	missing.Header.Set("X-Kyurem-Caller", "worker")
	if _, err := a.Authenticate(missing); err != ErrMissingCredentials {
		t.Fatalf("caller hint authenticated request: %v", err)
	}

	valid := httptest.NewRequest("POST", "/", nil)
	valid.Header.Set("Authorization", "Bearer secret-token")
	id, err := a.Authenticate(valid)
	if err != nil || id.Caller != "worker" || id.Method != MethodBearer {
		t.Fatalf("identity=%+v err=%v", id, err)
	}

	invalid := httptest.NewRequest("POST", "/", nil)
	invalid.Header.Set("Authorization", "Bearer wrong")
	if _, err := a.Authenticate(invalid); err == nil {
		t.Fatal("invalid bearer token was accepted")
	}
}
