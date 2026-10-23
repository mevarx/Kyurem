package failure

import (
	"testing"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

func TestRetryBudgetBoundsRetriesPerAgent(t *testing.T) {
	m := New(config.FailureHandling{
		MaxAttempts:    3,
		RetryBudget:    config.RetryBudget{MaxRetries: 1, Window: config.Duration(time.Minute)},
		CircuitBreaker: config.CircuitBreaker{FailureThreshold: 5, OpenDuration: config.Duration(time.Second)},
	})
	if !m.Begin("upstream-a", "caller", true) {
		t.Fatal("first retry should fit in budget")
	}
	m.Complete("upstream-a", true)
	if m.Begin("upstream-b", "caller", true) {
		t.Fatal("retry budget should be shared by requests to the same agent")
	}
	if !m.Begin("upstream-b", "other-caller", true) {
		t.Fatal("a different agent should have an independent budget")
	}
}

func TestCircuitBreakerOpensAndAllowsOneProbe(t *testing.T) {
	m := New(config.FailureHandling{
		MaxAttempts:    1,
		RetryBudget:    config.RetryBudget{MaxRetries: 1, Window: config.Duration(time.Minute)},
		CircuitBreaker: config.CircuitBreaker{FailureThreshold: 2, OpenDuration: config.Duration(20 * time.Millisecond)},
	})
	for i := 0; i < 2; i++ {
		if !m.Begin("upstream", "agent", false) {
			t.Fatal("closed breaker rejected an attempt")
		}
		m.Complete("upstream", false)
	}
	if m.Begin("upstream", "agent", false) {
		t.Fatal("open breaker accepted a request")
	}
	time.Sleep(25 * time.Millisecond)
	if !m.Begin("upstream", "agent", false) {
		t.Fatal("breaker did not allow a half-open probe")
	}
	if m.Begin("upstream", "agent", false) {
		t.Fatal("breaker allowed concurrent half-open probes")
	}
	m.Complete("upstream", true)
	if !m.Begin("upstream", "agent", false) {
		t.Fatal("successful probe did not close breaker")
	}
}
