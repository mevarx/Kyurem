// Package failure provides bounded retry budgets and per-upstream breakers.
package failure

import (
	"sync"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

type Manager struct {
	settings config.FailureHandling
	mu       sync.Mutex
	breakers map[string]*breaker
	budgets  map[string]*budget
}

type breaker struct {
	failures      int
	openUntil     time.Time
	probeInFlight bool
}

type budget struct {
	started time.Time
	used    int
}

func New(settings config.FailureHandling) *Manager {
	if settings.MaxAttempts < 1 {
		settings.MaxAttempts = 1
	}
	if settings.RetryBudget.Window.Std() <= 0 {
		settings.RetryBudget.Window = config.Duration(time.Minute)
	}
	if settings.CircuitBreaker.FailureThreshold < 1 {
		settings.CircuitBreaker.FailureThreshold = 5
	}
	if settings.CircuitBreaker.OpenDuration.Std() <= 0 {
		settings.CircuitBreaker.OpenDuration = config.Duration(30 * time.Second)
	}
	return &Manager{settings: settings, breakers: make(map[string]*breaker), budgets: make(map[string]*budget)}
}

func (m *Manager) MaxAttempts() int { return m.settings.MaxAttempts }

// Begin reserves one attempt. Retries consume an agent-scoped fixed-window budget.
func (m *Manager) Begin(upstream, agent string, retry bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	b := m.breakers[upstream]
	if b == nil {
		b = &breaker{}
		m.breakers[upstream] = b
	}
	probe := false
	if !b.openUntil.IsZero() {
		if time.Now().Before(b.openUntil) || b.probeInFlight {
			return false
		}
		b.probeInFlight = true
		probe = true
	}

	if retry {
		limit := m.settings.RetryBudget.MaxRetries
		if limit <= 0 {
			if probe {
				b.probeInFlight = false
			}
			return false
		}
		window := m.settings.RetryBudget.Window.Std()
		now := time.Now()
		bgt := m.budgets[agent]
		if bgt == nil || !now.Before(bgt.started.Add(window)) {
			bgt = &budget{started: now}
			m.budgets[agent] = bgt
		}
		if bgt.used >= limit {
			if probe {
				b.probeInFlight = false
			}
			return false
		}
		bgt.used++
	}
	return true
}

func (m *Manager) Complete(upstream string, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.breakers[upstream]
	if b == nil {
		return
	}
	if success {
		b.failures = 0
		b.openUntil = time.Time{}
		b.probeInFlight = false
		return
	}
	if b.probeInFlight || !b.openUntil.IsZero() {
		b.failures = 0
		b.openUntil = time.Now().Add(m.settings.CircuitBreaker.OpenDuration.Std())
		b.probeInFlight = false
		return
	}
	b.failures++
	if b.failures >= m.settings.CircuitBreaker.FailureThreshold {
		b.failures = 0
		b.openUntil = time.Now().Add(m.settings.CircuitBreaker.OpenDuration.Std())
	}
}
