package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/kyurem-project/kyurem/gateway/internal/authn"
	"github.com/kyurem-project/kyurem/gateway/internal/policy"
	"github.com/kyurem-project/kyurem/gateway/internal/registry"
	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
	"github.com/kyurem-project/kyurem/gateway/internal/trust"
)

// rejection is a request refused before it reaches the upstream.
type rejection struct {
	status  int
	kind    string
	reason  string
	message string
}

// identify resolves the authenticated caller. Without an authenticator the
// gateway behaves exactly like M1: the caller is anonymous and the
// X-Kyurem-Caller hint remains telemetry-only.
func (h *Handler) identify(r *http.Request) (authn.Identity, error) {
	if h.auth == nil {
		return authn.Identity{Caller: authn.AnonymousCaller, Method: authn.MethodOff, Anonymous: true}, nil
	}
	return h.auth.Authenticate(r)
}

// reject writes a pre-classified denial and marks the hop span as failed.
func (h *Handler) reject(w http.ResponseWriter, st *handoffState, rej *rejection) {
	if rej == nil {
		return
	}
	if st != nil {
		st.fail(rej.status, rej.reason, errors.New(rej.message))
	}
	h.logger.Debug("request rejected", "reason", rej.reason)
	h.writeJSONError(w, rej.status, rej.kind, rej.message)
}

// rejectAuth handles a missing or invalid caller credential (TP-5).
func (h *Handler) rejectAuth(r *http.Request, w http.ResponseWriter, st *handoffState, agent string, err error) {
	reason := "invalid"
	if errors.Is(err, authn.ErrMissingCredentials) {
		reason = "missing"
	}
	method := "missing"
	if r.Header.Get("Authorization") != "" {
		method = authn.MethodBearer
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.AttrAuthMethod, method),
		attribute.String(telemetry.AttrPolicyDecision, policy.DecisionDeny),
		attribute.String(telemetry.AttrPolicyReason, reason),
	}
	if st != nil && st.span != nil {
		st.span.SetAttributes(attrs...)
		st.span.AddEvent(telemetry.EventAuthDecision, trace.WithAttributes(attrs...))
	}
	mode := "off"
	if h.auth != nil {
		mode = string(h.auth.Mode())
	}
	h.recordAudit("auth", "", agent, "", policy.DecisionDeny, mode, "", reason, st)
	h.mtr.ObserveAuthFailure(reason)
	h.reject(w, st, &rejection{
		status:  http.StatusUnauthorized,
		kind:    "unauthorized",
		reason:  "unauthenticated",
		message: "caller authentication failed: missing or invalid credentials",
	})
}

// checkTrust verifies the agent's card when the configured mode is warn or
// enforce (TP-1, TP-2). The verification result is cached with the card, so
// only the first request after a refresh pays for it.
func (h *Handler) checkTrust(ctx context.Context, match *routeMatch, st *handoffState) *rejection {
	if h.trustV == nil {
		return nil
	}
	agent := match.entry.Name
	mode := h.trustV.Mode(agent)
	if mode == trust.ModeOff {
		return nil
	}

	verification := registry.CardVerification{
		Mode:   string(mode),
		Status: trust.StatusUnchecked,
		Reason: "no card snapshot available",
	}
	if h.cache != nil {
		snap, err := h.cache.Get(ctx, match.entry)
		switch {
		case err != nil:
			verification.Status = "error"
			verification.Reason = "agent card unavailable for verification: " + err.Error()
		case snap != nil:
			verification = snap.Verification
			if verification.Status == "" {
				verification.Status = trust.StatusUnchecked
			}
			if verification.Mode == "" {
				verification.Mode = string(mode)
			}
		}
	}
	if verification.Status == "" {
		verification.Status = trust.StatusUnchecked
	}
	if verification.Mode == "" {
		verification.Mode = string(mode)
	}

	decision := policy.DecisionAllow
	switch {
	case verification.Verified:
		decision = policy.DecisionAllow
	case mode == trust.ModeEnforce:
		decision = policy.DecisionDeny
	default:
		decision = policy.DecisionWarn
	}

	verifiedValue := telemetry.CardVerifiedFalse
	if verification.Verified {
		verifiedValue = telemetry.CardVerifiedTrue
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.AttrCardVerified, verifiedValue),
		attribute.String(telemetry.AttrTrustMode, string(mode)),
		attribute.String(telemetry.AttrTrustStatus, verification.Status),
		attribute.String(telemetry.AttrPolicyDecision, decision),
	}
	if verification.KeyID != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrTrustKey, verification.KeyID))
	}
	if verification.Reason != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrTrustReason, verification.Reason))
	}
	if st != nil && st.span != nil {
		st.span.SetAttributes(attrs...)
		st.span.AddEvent(telemetry.EventTrustDecision, trace.WithAttributes(attrs...))
	}
	h.recordAudit("trust", "", agent, "", decision, string(mode), "", verification.Reason, st)
	h.mtr.ObserveTrust(verification.Status)

	if mode == trust.ModeEnforce && !verification.Verified {
		return &rejection{
			status:  http.StatusForbidden,
			kind:    "untrusted_card",
			reason:  "untrusted_card",
			message: fmt.Sprintf("agent card verification failed: %s", verification.Reason),
		}
	}
	return nil
}

// checkPolicy evaluates caller -> callee authorization (TP-3). The caller is
// the authenticated identity, never the telemetry hint.
func (h *Handler) checkPolicy(r *http.Request, match *routeMatch, st *handoffState, identity authn.Identity) *rejection {
	if h.pol == nil || h.pol.Mode() == policy.ModeOff {
		return nil
	}
	caller := strings.TrimSpace(identity.Caller)
	if caller == "" {
		caller = authn.AnonymousCaller
	}
	skill := sanitizeSkill(r.Header.Get(SkillHeader))
	decision := h.pol.Evaluate(caller, match.entry.Name, skill)

	name := policy.DecisionAllow
	switch {
	case decision.Decision == policy.DecisionDeny:
		name = policy.DecisionDeny
	case decision.Decision == policy.DecisionWarn:
		name = policy.DecisionWarn
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.AttrPolicyDecision, name),
		attribute.String(telemetry.AttrPolicyMode, string(decision.Mode)),
		attribute.String(telemetry.AttrPolicyReason, decision.Reason),
	}
	if decision.Rule != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrPolicyRule, decision.Rule))
	}
	if skill != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrSkillID, skill))
	}
	if st != nil && st.span != nil {
		st.span.SetAttributes(attrs...)
		st.span.AddEvent(telemetry.EventPolicyDecision, trace.WithAttributes(attrs...))
	}
	h.recordAudit("policy", caller, match.entry.Name, skill, name, string(decision.Mode), decision.Rule, decision.Reason, st)
	h.mtr.ObservePolicyDecision(name)

	if !decision.Allowed {
		return &rejection{
			status:  http.StatusForbidden,
			kind:    "policy_denied",
			reason:  "policy_denied",
			message: "call denied by policy: " + decision.Reason,
		}
	}
	return nil
}

// recordAudit appends one decision to the bounded audit ring.
func (h *Handler) recordAudit(kind, caller, callee, skill, decision, mode, rule, reason string, st *handoffState) {
	if h.audit == nil {
		return
	}
	entry := policy.AuditEntry{
		Kind:     kind,
		Caller:   caller,
		Callee:   callee,
		Skill:    skill,
		Decision: decision,
		Mode:     mode,
		Rule:     rule,
		Reason:   reason,
	}
	if st != nil && st.span != nil {
		sc := st.span.SpanContext()
		entry.TraceID = sc.TraceID().String()
		entry.SpanID = sc.SpanID().String()
	}
	h.audit.Record(entry)
}

// sanitizeSkill bounds the optional X-Kyurem-Skill header to a printable
// token. An invalid value is treated as "no skill", which can only make a
// skill-scoped allow rule stricter.
func sanitizeSkill(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	runes := []rune(v)
	if len(runes) > 128 {
		runes = runes[:128]
	}
	for _, r := range runes {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return string(runes)
}
