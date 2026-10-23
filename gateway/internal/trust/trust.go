// Package trust verifies agent card signatures against a static set of
// trusted public keys (TP-1, TP-2).
//
// Trust is verifier-side: the signature's kid selects a key the operator
// configured, and a signer-supplied jku is never fetched or trusted. The
// per-agent mode decides what a verification result means:
//
//	off      no verification is performed; the status is "unchecked"
//	warn     verify, record the result, but do not block the call
//	enforce  verify and block calls whose card is not validly signed
package trust

import (
	"context"
	"crypto"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2acrypto"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

// Mode is the verification mode of one agent.
type Mode string

const (
	ModeOff     Mode = "off"
	ModeWarn    Mode = "warn"
	ModeEnforce Mode = "enforce"
)

// Verification statuses surfaced by the management API and the dashboard.
const (
	StatusVerified   = "verified"
	StatusUnverified = "unverified"
	StatusUnsigned   = "unsigned"
	StatusUnchecked  = "unchecked"
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

// Result is the cached outcome of verifying one card.
type Result struct {
	Mode     Mode
	Signed   bool
	Verified bool
	KeyID    string
	Reason   string
	At       time.Time
}

// Status maps the result to the vocabulary of the management API.
func (r Result) Status() string {
	switch {
	case r.Mode == ModeOff:
		return StatusUnchecked
	case r.Verified:
		return StatusVerified
	case !r.Signed:
		return StatusUnsigned
	default:
		return StatusUnverified
	}
}

// Blocked reports whether enforce mode must reject the call for this
// result.
func (r Result) Blocked() bool {
	return r.Mode == ModeEnforce && !r.Verified
}

type agentRule struct {
	mode Mode
	keys map[string]struct{} // nil means every configured key
}

// Verifier verifies cards with a fixed key store and per-agent modes.
type Verifier struct {
	mode   Mode
	keys   map[string]crypto.PublicKey
	agents map[string]agentRule
	jws    *a2acrypto.Verifier
}

// New builds a verifier from configuration. Key material is resolved and
// parsed here; validation normally catches errors first, but construction
// repeats the work so the package is safe to use on its own.
func New(cfg config.Trust) (*Verifier, error) {
	v := &Verifier{
		mode:   ParseMode(cfg.Mode),
		keys:   make(map[string]crypto.PublicKey, len(cfg.Keys)),
		agents: make(map[string]agentRule, len(cfg.Agents)),
	}
	for _, key := range cfg.Keys {
		raw, err := key.ResolvePEM()
		if err != nil {
			return nil, err
		}
		pub, err := config.ParsePublicKeyPEM(raw)
		if err != nil {
			return nil, fmt.Errorf("trusted key %q: %w", key.ID, err)
		}
		v.keys[key.ID] = pub
	}
	v.jws = a2acrypto.NewVerifier(a2acrypto.VerifierConfig{
		KeyResolver: a2acrypto.KeyResolverFunc(func(_ context.Context, kid string) (crypto.PublicKey, error) {
			key, ok := v.keys[kid]
			if !ok {
				return nil, errors.New("untrusted key")
			}
			return key, nil
		}),
	})
	for _, a := range cfg.Agents {
		rule := agentRule{mode: v.mode}
		if a.Mode != "" {
			rule.mode = ParseMode(a.Mode)
		}
		if len(a.Keys) > 0 {
			rule.keys = make(map[string]struct{}, len(a.Keys))
			for _, id := range a.Keys {
				rule.keys[id] = struct{}{}
			}
		}
		v.agents[strings.ToLower(a.Name)] = rule
	}
	return v, nil
}

// Mode returns the effective mode for an agent.
func (v *Verifier) Mode(agent string) Mode {
	if v == nil {
		return ModeOff
	}
	if rule, ok := v.agents[strings.ToLower(agent)]; ok {
		return rule.mode
	}
	return v.mode
}

// Keys returns the configured key ids, sorted by the caller if needed.
func (v *Verifier) Keys() []string {
	if v == nil {
		return []string{}
	}
	ids := make([]string, 0, len(v.keys))
	for id := range v.keys {
		ids = append(ids, id)
	}
	return ids
}

// Verify checks the raw card bytes against the card's signatures. Every
// signature is tried until one verifies; the result records why the last
// candidate failed otherwise. Verification runs only when the agent's mode
// is warn or enforce.
func (v *Verifier) Verify(ctx context.Context, agent string, raw []byte, card *a2a.AgentCard) Result {
	mode := v.Mode(agent)
	res := Result{Mode: mode, At: time.Now()}
	if mode == ModeOff {
		res.Reason = "card verification disabled"
		return res
	}
	if card == nil {
		res.Reason = "agent card unavailable"
		return res
	}
	if len(card.Signatures) == 0 {
		res.Reason = "agent card is not signed"
		return res
	}
	res.Signed = true

	var rule agentRule
	if r, ok := v.agents[strings.ToLower(agent)]; ok {
		rule = r
	}
	lastReason := "no verifiable signature"
	for i := range card.Signatures {
		sig := &card.Signatures[i]
		kid, alg, err := protectedHeader(sig)
		if err != nil {
			lastReason = "invalid signature header: " + err.Error()
			continue
		}
		if kid == "" {
			lastReason = "signature header has no kid"
			continue
		}
		if _, ok := v.keys[kid]; !ok {
			lastReason = fmt.Sprintf("key %q is not trusted", kid)
			continue
		}
		if rule.keys != nil {
			if _, ok := rule.keys[kid]; !ok {
				lastReason = fmt.Sprintf("key %q is not allowed for agent %q", kid, agent)
				continue
			}
		}
		if err := v.jws.Verify(ctx, raw, sig); err != nil {
			lastReason = fmt.Sprintf("signature with key %q did not verify", kid)
			continue
		}
		res.Verified = true
		res.KeyID = kid
		res.Reason = fmt.Sprintf("signature verified with key %q (%s)", kid, alg)
		return res
	}
	res.Reason = lastReason
	return res
}

// protectedHeader decodes the base64url JWS protected header and extracts
// kid and alg. It never trusts values for key lookup beyond the kid.
func protectedHeader(sig *a2a.AgentCardSignature) (kid, alg string, err error) {
	if sig == nil || sig.Protected == "" {
		return "", "", errors.New("missing protected header")
	}
	raw, err := base64.RawURLEncoding.DecodeString(sig.Protected)
	if err != nil {
		return "", "", errors.New("invalid base64url")
	}
	var header struct {
		KID string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return "", "", errors.New("invalid JSON")
	}
	return strings.TrimSpace(header.KID), strings.TrimSpace(header.Alg), nil
}
