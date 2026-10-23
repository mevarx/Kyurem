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
	if allowed, _ := m.Begin("upstream-a", "caller", true); !allowed {
		t.Fatal("first retry should fit in budget")
	}
	m.Complete("upstream-a", false, true)
	if allowed, _ := m.Begin("upstream-b", "caller", true); allowed {
		t.Fatal("retry budget should be shared by requests to the same agent")
	}
	if allowed, _ := m.Begin("upstream-b", "other-caller", true); !allowed {
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
		allowed, probe := m.Begin("upstream", "agent", false)
		if !allowed {
			t.Fatal("closed breaker rejected an attempt")
		}
		m.Complete("upstream", probe, false)
	}
	if allowed, _ := m.Begin("upstream", "agent", false); allowed {
		t.Fatal("open breaker accepted a request")
	}
	time.Sleep(25 * time.Millisecond)
	allowed, probe := m.Begin("upstream", "agent", false)
	if !allowed || !probe {
		t.Fatal("breaker did not allow a half-open probe")
	}
	if allowed, _ := m.Begin("upstream", "agent", false); allowed {
		t.Fatal("breaker allowed concurrent half-open probes")
	}
	m.Complete("upstream", probe, true)
	if allowed, _ := m.Begin("upstream", "agent", false); !allowed {
		t.Fatal("successful probe did not close breaker")
	}
}

func TestInFlightCompletionCannotCloseOpenCircuit(t *testing.T) {
	m := New(config.FailureHandling{
		MaxAttempts:    1,
		CircuitBreaker: config.CircuitBreaker{FailureThreshold: 1, OpenDuration: config.Duration(time.Second)},
	})
	if allowed, _ := m.Begin("upstream", "agent", false); !allowed {
		t.Fatal("initial request rejected")
	}
	if allowed, _ := m.Begin("upstream", "agent", false); !allowed {
		t.Fatal("parallel in-flight request rejected before breaker opened")
	}
	m.Complete("upstream", false, false)
	m.Complete("upstream", false, true)
	if allowed, _ := m.Begin("upstream", "agent", false); allowed {
		t.Fatal("in-flight success incorrectly closed the open circuit")
	}
}
