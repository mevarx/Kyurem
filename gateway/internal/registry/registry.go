// Package registry holds the gateway's agent routing table: registered
// agents (name, aliases, route paths) and their upstream endpoints, plus
// upstream selection for a given request.
package registry

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

// WellKnownAgentCardPath is the discovery location per A2A spec (the a2a-go
// SDK serves and resolves it at this exact path). The integration tests
// assert against the SDK's resolver behavior to catch drift.
const WellKnownAgentCardPath = "/.well-known/agent-card.json"

// Upstream is one upstream agent endpoint.
type Upstream struct {
	// URL is the upstream base. For JSONRPC binding, JSON-RPC POSTs go to
	// this URL verbatim; the origin is used for card discovery unless
	// CardURL is set.
	URL string
	// CardURL overrides where the agent card is fetched from.
	CardURL string
	// Binding narrows which requests route here. Empty binds all.
	Binding a2a.TransportProtocol
}

// Entry is a registered agent: canonical name, listen paths (canonical and
// aliases), tags, and the ordered upstream list used for failover.
type Entry struct {
	Name string
	// RoutePath is the canonical listen path ({prefix}/{name} unless
	// overridden in config).
	RoutePath string
	// AliasPaths are additional listen paths resolving to this entry.
	AliasPaths []string
	Tags       []string
	Upstreams  []Upstream
}

// PickUpstreams returns the primary and its same-binding failover targets.
// If no binding matches, it preserves the legacy first-upstream fallback.
func (e *Entry) PickUpstreams(binding a2a.TransportProtocol) []*Upstream {
	if len(e.Upstreams) == 0 {
		return nil
	}
	if binding != "" {
		var matches []*Upstream
		for i := range e.Upstreams {
			if e.Upstreams[i].Binding != "" && e.Upstreams[i].Binding == binding {
				matches = append(matches, &e.Upstreams[i])
			}
		}
		if len(matches) > 0 {
			return matches
		}
	}
	return []*Upstream{&e.Upstreams[0]}
}

// PickUpstream selects the primary upstream for a request with the given binding.
func (e *Entry) PickUpstream(binding a2a.TransportProtocol) *Upstream {
	upstreams := e.PickUpstreams(binding)
	if len(upstreams) == 0 {
		return nil
	}
	return upstreams[0]
}

// CardFetchURL resolves where to GET /.well-known/agent-card.json from:
// the first upstream's CardURL when set, else its origin + well-known path.
func (e *Entry) CardFetchURL() string {
	if len(e.Upstreams) == 0 {
		return ""
	}
	first := &e.Upstreams[0]
	if first.CardURL != "" {
		return first.CardURL
	}
	origin := first.URL
	if u, err := url.Parse(origin); err == nil && u.Host != "" {
		origin = u.Scheme + "://" + u.Host
	}
	return strings.TrimSuffix(origin, "/") + WellKnownAgentCardPath
}

// Registry is the read-mostly routing table keyed by listen path (both
// canonical name paths and alias paths) and by routing key (name/alias).
type Registry struct {
	entries []*Entry
	byPath  map[string]*Entry
	byKey   map[string]*Entry // lower-cased name/alias key
}

// New builds the registry from validated configuration. It recomputes route
// normalization via the config package's helper (`AgentRoutes`), so
// validation-time and runtime-time routing cannot drift apart.
func New(cfg *config.Config) (*Registry, error) {
	r := &Registry{byPath: map[string]*Entry{}, byKey: map[string]*Entry{}}
	for i := range cfg.Agents {
		path, aliases := cfg.AgentRoutes(i)
		e := &Entry{
			Name:       cfg.Agents[i].Name,
			RoutePath:  path,
			AliasPaths: aliases,
			Tags:       append([]string(nil), cfg.Agents[i].Tags...),
		}
		for j := range cfg.Agents[i].Upstreams {
			u := cfg.Agents[i].Upstreams[j]
			e.Upstreams = append(e.Upstreams, Upstream{
				URL:     u.URL,
				CardURL: u.CardURL,
				Binding: a2a.TransportProtocol(u.Binding),
			})
		}
		r.entries = append(r.entries, e)
		r.byPath[strings.ToLower(strings.TrimSuffix(e.RoutePath, "/"))] = e
		for _, al := range e.AliasPaths {
			r.byPath[strings.ToLower(strings.TrimSuffix(al, "/"))] = e
		}
		r.byKey[strings.ToLower(e.Name)] = e
		for _, al := range e.AliasPaths {
			aliasKey := al[strings.LastIndex(al, "/")+1:]
			r.byKey[strings.ToLower(aliasKey)] = e
		}
	}
	return r, nil
}

// LookupPath resolves by full listen path (exact match against configured
// paths, case-insensitive).
func (r *Registry) LookupPath(path string) *Entry {
	if e, ok := r.byPath[strings.ToLower(strings.TrimSuffix(path, "/"))]; ok {
		return e
	}
	return nil
}

// LookupKey resolves by agent name or alias (case-insensitive).
func (r *Registry) LookupKey(key string) *Entry {
	if e, ok := r.byKey[strings.ToLower(key)]; ok {
		return e
	}
	return nil
}

// Entries lists registered agents in config order.
func (r *Registry) Entries() []*Entry {
	return append([]*Entry(nil), r.entries...)
}

// Len returns the number of registered agents.
func (r *Registry) Len() int { return len(r.entries) }

// String satisfies fmt.Stringer for logs.
func (r *Registry) String() string { return fmt.Sprintf("registry(%d agents)", r.Len()) }
