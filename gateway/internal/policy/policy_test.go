package policy

import (
	"testing"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

func TestEnforcePolicyDenyOverridesAllow(t *testing.T) {
	e := New(config.Policy{
		Mode: "enforce", Default: "deny", AuditCapacity: 8,
		Rules: []config.PolicyRule{
			{Name: "allow-worker", Caller: "worker", Callee: "echo", Effect: "allow"},
			{Name: "deny-worker", Caller: "worker", Callee: "echo", Skills: []string{"payments"}, Effect: "deny"},
		},
	}, []string{"worker"}, []string{"echo"})

	decision := e.Evaluate("worker", "echo", "payments")
	if decision.Allowed || !decision.Enforced || decision.Rule != "deny-worker" {
		t.Fatalf("deny did not override allow: %+v", decision)
	}
	if !e.Evaluate("worker", "echo", "other").Allowed {
		t.Fatal("matching allow rule should permit an unscoped skill")
	}
}

func TestWarnPolicyRecordsDenyWithoutBlocking(t *testing.T) {
	e := New(config.Policy{
		Mode: "warn", Default: "allow", AuditCapacity: 8,
		Rules: []config.PolicyRule{{Name: "deny", Caller: "*", Callee: "echo", Effect: "deny"}},
	}, nil, []string{"echo"})
	d := e.Evaluate("caller", "echo", "")
	if !d.Allowed || d.Enforced || d.Decision != DecisionWarn {
		t.Fatalf("warn mode outcome=%+v", d)
	}
}
