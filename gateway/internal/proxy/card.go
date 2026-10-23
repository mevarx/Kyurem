package proxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/kyurem-project/kyurem/gateway/internal/registry"
)

// serveCard handles GET {route}/.well-known/agent-card.json (GW-4):
// fetches (or revalidates) the upstream card, rewrites interface URLs to
// point callers back at the gateway, and answers conditional requests with
// 304 using the upstream ETag.
func (h *Handler) serveCard(w http.ResponseWriter, r *http.Request, match *routeMatch) {
	e := match.entry
	res, err := h.cache.Get(r.Context(), e)
	if err != nil {
		h.mtr.CardServedTotal.WithLabelValues(e.Name, "error").Inc()
		// Discovery failures must be loud: clients cannot route without
		// the card, and silent failures would hide upstream outages.
		h.logger.Error("agent card unavailable", "agent", e.Name, "err", err.Error())
		h.writeJSONError(w, http.StatusBadGateway, "card_unavailable",
			"upstream agent card is unavailable")
		return
	}

	outcome := res.Source
	if outcome == "" {
		outcome = "cache"
	}

	// The served URL must send callers back through this very route path
	// (canonical or alias, ADR 0002), never at the upstream URL directly:
	// that is what keeps the gateway the discovery root (PRD §2.2).
	base := h.publicBase(r)
	card := rewriteCard(res, base, match.raw)

	body, err := json.Marshal(card)
	if err != nil {
		h.mtr.CardServedTotal.WithLabelValues(e.Name, "error").Inc()
		h.logger.Error("agent card rewrite failed", "agent", e.Name, "err", err.Error())
		h.writeJSONError(w, http.StatusInternalServerError, "card_rewrite_failed",
			"failed to marshal the rewritten agent card")
		return
	}

	etag := cardETag(res)
	if matchesConditional(r, etag) {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		h.mtr.CardServedTotal.WithLabelValues(e.Name, outcome).Inc()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body) // HEAD responses stay header-only
	}
	h.mtr.CardServedTotal.WithLabelValues(e.Name, outcome).Inc()
}

// matchesConditional reports whether If-None-Match equals the upstream
// ETag. Rewritten bytes derive from the same upstream card, so the upstream
// ETag remains the freshness oracle (ADR 0002).
func matchesConditional(r *http.Request, etag string) bool {
	if etag == "" {
		return false
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" && strings.Contains(inm, etag) {
		return true
	}
	return false
}

// publicBase resolves the scheme://host[:port] used for rewritten card
// URLs: the configured public_base_url when set, else derived from the
// inbound request (X-Forwarded-Proto from an upstream proxy, otherwise
// http, plus the request Host). Scheme values are sanitized: cards must
// never contain attacker-controlled scheme text.
func (h *Handler) publicBase(r *http.Request) string {
	if h.knownBase != "" {
		return h.knownBase
	}
	return forwardedScheme(r) + "://" + r.Host
}

func forwardedScheme(r *http.Request) string {
	p := r.Header.Get("X-Forwarded-Proto")
	switch strings.ToLower(firstSegment(p)) {
	case "https":
		return "https"
	case "http":
		return "http"
	default:
		return "http"
	}
}

func firstSegment(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		return strings.TrimSpace(v[:i])
	}
	return strings.TrimSpace(v)
}

// rewriteCard copies the cached card and rewrites every proxyable
// interface back to the gateway. Bindings the gateway cannot proxy today
// (gRPC passthrough is GW-8, P2) are dropped so clients only see endpoints
// that actually work through the gateway.
func rewriteCard(res *registry.CardResult, base, routePath string) *a2a.AgentCard {
	card := &a2a.AgentCard{
		Capabilities:         res.Card.Capabilities,
		DefaultInputModes:    res.Card.DefaultInputModes,
		DefaultOutputModes:   res.Card.DefaultOutputModes,
		Description:          res.Card.Description,
		DocumentationURL:     res.Card.DocumentationURL,
		IconURL:              res.Card.IconURL,
		Name:                 res.Card.Name,
		Provider:             res.Card.Provider,
		SecurityRequirements: res.Card.SecurityRequirements,
		SecuritySchemes:      res.Card.SecuritySchemes,
		// Rewriting interface URLs changes the signed bytes, so the
		// upstream signatures are not valid for the served card. The
		// verification result is exposed through the management API
		// instead of advertising a signature that would fail.
		Signatures: nil,
		Skills:     res.Card.Skills,
		Version:    res.Card.Version,
	}

	gatewayURL := strings.TrimSuffix(base, "/") + routePath
	for _, in := range res.Card.SupportedInterfaces {
		if in == nil {
			continue
		}
		if in.ProtocolBinding == a2a.TransportProtocolGRPC {
			// GW-8 (P2): gRPC passthrough not implemented; keeping the
			// interface would advertise an unusable endpoint.
			continue
		}
		iface := *in
		iface.URL = gatewayURL
		card.SupportedInterfaces = append(card.SupportedInterfaces, &iface)
	}
	return card
}

// cardETag is the upstream ETag when present, else one derived from the
// card bytes so conditional requests still work against plain agents.
func cardETag(res *registry.CardResult) string {
	if res.ETag != "" {
		return res.ETag
	}
	if res.Raw == nil {
		return ""
	}
	return `"kyurem-` + shortHash(res.Raw) + `"`
}

var _ = errors.New // reserved: card paths gain error classification in M2
