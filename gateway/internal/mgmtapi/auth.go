package mgmtapi

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// bearerScheme is the only authentication scheme the API accepts. The
// comparison is case-insensitive, as RFC 7235 requires.
const bearerScheme = "bearer"

// authed wraps a handler with bearer-token authentication. When no token
// is configured the API is open (acceptable on a loopback listener only).
func (s *server) authed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kyurem-management"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authorized reports whether the request carries the configured bearer
// token. The comparison is constant time so a wrong token leaks no
// information about the expected one.
func (s *server) authorized(r *http.Request) bool {
	if s.deps.AuthToken == "" {
		return true
	}
	header := r.Header.Get("Authorization")
	if header == "" {
		return false
	}
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(strings.TrimSpace(scheme), bearerScheme) {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.deps.AuthToken)) == 1
}
