// Package authn authenticates callers into the A2A data path (TP-5).
//
// Two credential types are supported: a static bearer token resolved from
// configuration or the environment, and an mTLS client certificate whose
// common name (or first DNS SAN) becomes the caller identity. Policy always
// uses this authenticated identity; the X-Kyurem-Caller header remains an
// untrusted telemetry hint and never grants access.
package authn

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

// Mode is the caller authentication mode.
type Mode string

const (
	ModeOff      Mode = "off"
	ModeOptional Mode = "optional"
	ModeRequired Mode = "required"
)

// Credential methods reported in spans and the audit log.
const (
	MethodBearer    = "bearer"
	MethodMTLS      = "mtls"
	MethodAnonymous = "anonymous"
	MethodOff       = "off"
)

// AnonymousCaller is the identity used when authentication is optional and
// no credential was presented.
const AnonymousCaller = "anonymous"

// ParseMode normalizes a configured mode. Empty means off.
func ParseMode(v string) Mode {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "optional":
		return ModeOptional
	case "required":
		return ModeRequired
	default:
		return ModeOff
	}
}

// Errors classifies authentication failures. The proxy maps them to a 401
// and a bounded metric label.
var (
	ErrMissingCredentials = errors.New("authentication required")
	ErrInvalidCredentials = errors.New("invalid credentials")
)

// Identity is the authenticated caller.
type Identity struct {
	Caller    string
	Method    string
	Anonymous bool
}

type tokenEntry struct {
	name string
	hash [sha256.Size]byte
}

// Authenticator validates bearer tokens and mTLS identities.
type Authenticator struct {
	mode   Mode
	tokens []tokenEntry
}

// New builds an authenticator. Every token is resolved and hashed once;
// plaintext tokens are not retained.
func New(cfg config.Callers) (*Authenticator, error) {
	a := &Authenticator{mode: ParseMode(cfg.Mode)}
	for _, tok := range cfg.BearerTokens {
		value, err := tok.ResolveToken()
		if err != nil {
			return nil, err
		}
		a.tokens = append(a.tokens, tokenEntry{
			name: tok.Name,
			hash: sha256.Sum256([]byte(value)),
		})
	}
	return a, nil
}

// Mode returns the configured mode.
func (a *Authenticator) Mode() Mode {
	if a == nil {
		return ModeOff
	}
	return a.mode
}

// CallerNames returns the static caller identities in configuration order.
func (a *Authenticator) CallerNames() []string {
	if a == nil {
		return []string{}
	}
	names := make([]string, 0, len(a.tokens))
	for _, tok := range a.tokens {
		names = append(names, tok.name)
	}
	return names
}

// Authenticate resolves the caller identity of a request.
//
// In off mode credentials are ignored. In optional and required mode a
// presented bearer token must be valid; a client certificate is accepted
// when no bearer token is presented. Required mode rejects requests with no
// credential at all.
func (a *Authenticator) Authenticate(r *http.Request) (Identity, error) {
	if a == nil || a.mode == ModeOff {
		return Identity{Caller: AnonymousCaller, Method: MethodOff, Anonymous: true}, nil
	}

	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header != "" {
		scheme, token, ok := strings.Cut(header, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
			return Identity{}, fmt.Errorf("%w: malformed Authorization header", ErrInvalidCredentials)
		}
		if name, ok := a.lookup(strings.TrimSpace(token)); ok {
			return Identity{Caller: name, Method: MethodBearer}, nil
		}
		return Identity{}, fmt.Errorf("%w: bearer token not recognized", ErrInvalidCredentials)
	}

	if identity, ok := mtlsIdentity(r); ok {
		return identity, nil
	}

	if a.mode == ModeRequired {
		return Identity{}, ErrMissingCredentials
	}
	return Identity{Caller: AnonymousCaller, Method: MethodAnonymous, Anonymous: true}, nil
}

// lookup compares the presented token against every configured hash in
// constant time. The token count is small and fixed, so this is cheap.
func (a *Authenticator) lookup(token string) (string, bool) {
	sum := sha256.Sum256([]byte(token))
	name := ""
	found := 0
	for _, candidate := range a.tokens {
		if subtle.ConstantTimeCompare(sum[:], candidate.hash[:]) == 1 {
			name = candidate.name
			found = 1
		}
	}
	return name, found == 1
}

// mtlsIdentity derives a caller identity from an already-verified client
// certificate. The TLS listener verifies the chain against the configured
// client CA; this function only extracts a bounded printable name.
func mtlsIdentity(r *http.Request) (Identity, bool) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return Identity{}, false
	}
	cert := r.TLS.PeerCertificates[0]
	name := strings.TrimSpace(cert.Subject.CommonName)
	if name == "" && len(cert.DNSNames) > 0 {
		name = strings.TrimSpace(cert.DNSNames[0])
	}
	if !validName(name) {
		return Identity{}, false
	}
	return Identity{Caller: name, Method: MethodMTLS}, true
}

// validName bounds a certificate identity to a printable token.
func validName(v string) bool {
	if v == "" || len([]rune(v)) > 128 {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
