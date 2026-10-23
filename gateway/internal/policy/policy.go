// Package policy evaluates caller -> callee authorization rules (TP-3)
// and keeps a bounded audit ring of decisions (TP-4).
//
// Rules are deny-overrides: every matching rule is considered and a single
// deny wins regardless of rule order. When no rule matches, the configured
// default decides. A rule may restrict itself to specific skill ids; a call
// without a skill only matches rules that do not require one.
package policy

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

// Mode is the global policy mode.
type Mode string

const (
	ModeOff     Mode = "off"
	ModeWarn    Mode = "warn"
	ModeEnforce Mode = "enforce"
)

// Effects and decision words.
const (
	EffectAllow = "allow"
	EffectDeny  = "deny"

	DecisionAllow = "allow"
	DecisionDeny  = "deny"
	DecisionWarn  = "warn"
)

// ParseMode normalizes a configured mode. Empty means off.
func ParseMode(v string) Mode {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "warn":
		return ModeWarn
	case "enforce":
		return ModeEnforce
	default:
		return ModeOff
	}
}

// parseEffect normalizes a configured effect. Empty means allow.
func parseEffect(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case EffectDeny:
		return EffectDeny
	default:
		return EffectAllow
	}
}

// Rule is one evaluated authorization rule. Caller and Callee may be "*".
type Rule struct {
	Name   string   `json:"name"`
	Caller string   `json:"caller"`
	Callee string   `json:"callee"`
	Skills []string `json:"skills"`
	Effect string   `json:"effect"`
}

// Decision is the outcome of one policy evaluation.
type Decision struct {
	Allowed  bool
	Enforced bool
	Decision string // allow, deny or warn
	Effect   string // the winning effect (allow or deny)
	Mode     Mode
	Caller   string
	Callee   string
	Skill    string
	Rule     string
	Reason   string
}

// MatrixCell is one caller x callee cell of the policy snapshot. It is
// evaluated without a skill, so skill-scoped rules only show up when they
// match an unspecified call.
type MatrixCell struct {
	Caller   string `json:"caller"`
	Callee   string `json:"callee"`
	Effect   string `json:"effect"`
	Decision string `json:"decision"`
	Rule     string `json:"rule,omitempty"`
	Reason   string `json:"reason"`
}

// Snapshot is the read-only policy view served by the management API.
type Snapshot struct {
	Mode    string       `json:"mode"`
	Default string       `json:"default"`
	Rules   []Rule       `json:"rules"`
	Callers []string     `json:"callers"`
	Callees []string     `json:"callees"`
	Matrix  []MatrixCell `json:"matrix"`
}

// Engine evaluates rules and builds policy snapshots.
type Engine struct {
	mode    Mode
	def     string
	rules   []Rule
	callers []string
	callees []string
}

// New builds an engine from configuration. callers and callees are the
// identities and agent names shown in the dashboard matrix.
func New(cfg config.Policy, callers, callees []string) *Engine {
	e := &Engine{
		mode:    ParseMode(cfg.Mode),
		def:     parseEffect(cfg.Default),
		callers: uniqueSorted(callers, true),
		callees: uniqueSorted(callees, false),
	}
	for _, r := range cfg.Rules {
		rule := Rule{
			Name:   r.Name,
			Caller: r.Caller,
			Callee: r.Callee,
			Effect: parseEffect(r.Effect),
			Skills: append([]string(nil), r.Skills...),
		}
		e.rules = append(e.rules, rule)
	}
	return e
}

// Mode returns the global mode.
func (e *Engine) Mode() Mode {
	if e == nil {
		return ModeOff
	}
	return e.mode
}

// Evaluate returns the decision for one call without recording anything.
func (e *Engine) Evaluate(caller, callee, skill string) Decision {
	d := Decision{
		Allowed:  true,
		Decision: DecisionAllow,
		Effect:   EffectAllow,
		Mode:     ModeOff,
		Caller:   caller,
		Callee:   callee,
		Skill:    skill,
	}
	if e == nil {
		d.Reason = "policy is not configured"
		return d
	}
	d.Mode = e.mode
	if e.mode == ModeOff {
		d.Reason = "policy disabled"
		return d
	}

	var deny, allow *Rule
	for i := range e.rules {
		rule := &e.rules[i]
		if !rule.matches(caller, callee, skill) {
			continue
		}
		if rule.Effect == EffectDeny && deny == nil {
			deny = rule
		}
		if rule.Effect == EffectAllow && allow == nil {
			allow = rule
		}
	}
	switch {
	case deny != nil:
		return e.decideDeny(d, deny, fmt.Sprintf("rule %q denies caller %q calling %q", deny.Name, caller, callee))
	case allow != nil:
		d.Effect = EffectAllow
		d.Rule = allow.Name
		d.Reason = fmt.Sprintf("rule %q allows caller %q calling %q", allow.Name, caller, callee)
		if skill != "" && len(allow.Skills) > 0 {
			d.Reason += fmt.Sprintf(" for skill %q", skill)
		}
		return d
	case e.def == EffectDeny:
		return e.decideDeny(d, nil, fmt.Sprintf("default deny for caller %q calling %q", caller, callee))
	default:
		d.Reason = "default allow"
		return d
	}
}

// decideDeny fills a deny decision. In warn mode the call stays allowed but
// the decision word is "warn" so the event and audit log remain honest.
func (e *Engine) decideDeny(d Decision, rule *Rule, reason string) Decision {
	d.Effect = EffectDeny
	if rule != nil {
		d.Rule = rule.Name
	}
	d.Reason = reason
	if e.mode == ModeEnforce {
		d.Allowed = false
		d.Decision = DecisionDeny
		d.Enforced = true
		return d
	}
	d.Allowed = true
	d.Decision = DecisionWarn
	d.Reason = reason + " (warn mode: call allowed)"
	return d
}

// matches reports whether a rule applies to the call.
func (r *Rule) matches(caller, callee, skill string) bool {
	if !matchesName(r.Caller, caller) || !matchesName(r.Callee, callee) {
		return false
	}
	if len(r.Skills) == 0 {
		return true
	}
	if skill == "" {
		return false
	}
	for _, want := range r.Skills {
		if strings.EqualFold(strings.TrimSpace(want), skill) {
			return true
		}
	}
	return false
}

func matchesName(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(pattern), strings.TrimSpace(value))
}

// Snapshot returns the current policy and the caller x callee matrix.
func (e *Engine) Snapshot() Snapshot {
	if e == nil {
		return Snapshot{
			Mode:    string(ModeOff),
			Default: EffectAllow,
			Rules:   []Rule{},
			Callers: []string{},
			Callees: []string{},
			Matrix:  []MatrixCell{},
		}
	}
	snap := Snapshot{
		Mode:    string(e.mode),
		Default: e.def,
		Rules:   append([]Rule{}, e.rules...),
		Callers: append([]string{}, e.callers...),
		Callees: append([]string{}, e.callees...),
		Matrix:  []MatrixCell{},
	}
	for _, caller := range e.callers {
		for _, callee := range e.callees {
			d := e.Evaluate(caller, callee, "")
			snap.Matrix = append(snap.Matrix, MatrixCell{
				Caller:   caller,
				Callee:   callee,
				Effect:   d.Effect,
				Decision: d.Decision,
				Rule:     d.Rule,
				Reason:   d.Reason,
			})
		}
	}
	return snap
}

// uniqueSorted trims, de-duplicates and sorts names. A leading "*" is kept
// first; empty and "*" values are dropped from callee lists.
func uniqueSorted(names []string, keepAnonymous bool) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || (name == "*" && !keepAnonymous) {
			continue
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i] == "*" {
			return true
		}
		if out[j] == "*" {
			return false
		}
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// AuditEntry is one recorded trust, policy or authentication decision.
type AuditEntry struct {
	Timestamp  time.Time         `json:"timestamp"`
	Kind       string            `json:"kind"`
	Caller     string            `json:"caller"`
	Callee     string            `json:"callee"`
	Skill      string            `json:"skill,omitempty"`
	Decision   string            `json:"decision"`
	Mode       string            `json:"mode,omitempty"`
	Rule       string            `json:"rule,omitempty"`
	Reason     string            `json:"reason,omitempty"`
	TraceID    string            `json:"trace_id,omitempty"`
	SpanID     string            `json:"span_id,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// AuditLog is a bounded, concurrency-safe ring of recent decisions.
type AuditLog struct {
	mu       sync.Mutex
	entries  []AuditEntry
	next     int
	filled   bool
	capacity int
	now      func() time.Time
}

// NewAuditLog builds a ring with the configured capacity.
func NewAuditLog(capacity int) *AuditLog {
	if capacity <= 0 {
		capacity = 1024
	}
	return &AuditLog{
		entries:  make([]AuditEntry, capacity),
		capacity: capacity,
		now:      time.Now,
	}
}

// Record appends one decision, overwriting the oldest entry when full.
func (a *AuditLog) Record(entry AuditEntry) {
	if a == nil {
		return
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = a.now()
	}
	if entry.Attributes == nil {
		entry.Attributes = nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries[a.next] = entry
	a.next = (a.next + 1) % a.capacity
	if a.next == 0 {
		a.filled = true
	}
}

// Recent returns up to limit decisions, newest first, optionally filtered
// by kind. A non-positive limit uses the whole ring.
func (a *AuditLog) Recent(kind string, limit int) []AuditEntry {
	if a == nil {
		return []AuditEntry{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	total := a.next
	if a.filled {
		total = a.capacity
	} else if total == 0 {
		return []AuditEntry{}
	}
	if limit <= 0 || limit > total {
		limit = total
	}
	out := make([]AuditEntry, 0, limit)
	for i := 0; i < total && len(out) < limit; i++ {
		index := (a.next - 1 - i + a.capacity*2) % a.capacity
		entry := a.entries[index]
		if kind != "" && entry.Kind != kind {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// Len returns the number of recorded decisions.
func (a *AuditLog) Len() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.filled {
		return a.capacity
	}
	return a.next
}
