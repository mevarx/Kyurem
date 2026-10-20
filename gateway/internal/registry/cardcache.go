package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// CardResult is a cached upstream agent card snapshot (GW-4).
type CardResult struct {
	Agent *Entry
	// Card is the parsed upstream card; served cards are derived from it.
	Card *a2a.AgentCard
	// Raw is the unmodified upstream card body (trust verification in M5
	// verifies over these bytes, not over rewritten output).
	Raw []byte
	// ETag is the upstream ETag ("" when the agent does not send one).
	ETag string
	// FetchedAt is the last successful response time (200 or 304).
	FetchedAt time.Time
	// Source records how this snapshot was produced: "fetched" (fresh
	// download), "revalidated" (upstream 304), "stale" (served after a
	// failed refresh within the grace) or "" (untouched cache read). The
	// discovery endpoint reports it as a metric outcome.
	Source string
	// FailedRefresh is true when refresh attempts have failed; the cached
	// card is then eligible for stale serving only within the grace.
	FailedRefresh bool
	LastErr       error
}

// Fresh reports whether the card is within its TTL.
func (c *CardResult) Fresh(ttl time.Duration, now time.Time) bool {
	return now.Sub(c.FetchedAt) < ttl
}

// CardError classifies cache lookups for callers that must react
// differently to fully-miss versus stale.
type CardError struct {
	Stale bool
	Err   error
}

func (e *CardError) Error() string {
	if e.Stale {
		return fmt.Sprintf("agent card stale: %v", e.Err)
	}
	return fmt.Sprintf("agent card fetch failed: %v", e.Err)
}

// CardCache fetches, caches and refreshes upstream agent cards.
//
// Guarantees (PRD GW-4):
//   - served cards are fetched at most once per TTL per agent;
//   - upstream ETags trigger conditional revalidation instead of full
//     reloads when supported;
//   - on refresh failure a stale card is served within StaleGrace, then
//     the route errors so callers notice prolonged outages;
//   - the background refresher keeps cards warm so the request path usually
//     stays cache-hit fast; it stops when the cache's context is cancelled.
type CardCache struct {
	reg        *Registry
	client     *http.Client
	ttl        time.Duration
	staleGrace time.Duration
	maxBytes   int64
	logger     *slog.Logger

	mu      sync.Mutex
	cards   map[string]*CardResult
	fetches map[string]*fetchTicket // per-agent request singleflight
	now     func() time.Time
}

type fetchTicket struct {
	ready chan struct{}
	res   *CardResult
	err   error
}

// NewCardCache builds a cache. Start launches its refresher; Close stops it.
func NewCardCache(reg *Registry, client *http.Client, ttl, staleGrace time.Duration, maxCardBytes int64, logger *slog.Logger) *CardCache {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &CardCache{
		reg:        reg,
		client:     client,
		ttl:        ttl,
		staleGrace: staleGrace,
		maxBytes:   maxCardBytes,
		logger:     logger,
		cards:      map[string]*CardResult{},
		fetches:    map[string]*fetchTicket{},
		now:        time.Now,
	}
}

// Get returns the current card snapshot for the agent, refreshing it when
// stale. Concurrent fetches for the same agent collapse (singleflight).
func (c *CardCache) Get(ctx context.Context, e *Entry) (*CardResult, error) {
	if c.ttl > 0 {
		c.mu.Lock()
		if cr, ok := c.cards[e.Name]; ok && cr.Fresh(c.ttl, c.now()) {
			c.mu.Unlock()
			return cr, nil
		}
		c.mu.Unlock()
	}
	return c.fetch(ctx, e)
}

// fetch performs a deduplicated outbound fetch and stores the result.
func (c *CardCache) fetch(ctx context.Context, e *Entry) (*CardResult, error) {
	c.mu.Lock()
	if t, ok := c.fetches[e.Name]; ok {
		c.mu.Unlock()
		select {
		case <-t.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if t.err != nil {
			return t.res, t.err
		}
		return t.res, nil
	}
	ticket := &fetchTicket{ready: make(chan struct{})}
	c.fetches[e.Name] = ticket
	c.mu.Unlock()

	res, err := c.fetchUncached(ctx, e)

	c.mu.Lock()
	delete(c.fetches, e.Name)
	ticket.res = res
	ticket.err = err
	c.mu.Unlock()
	close(ticket.ready)
	return res, err
}

func (c *CardCache) fetchUncached(ctx context.Context, e *Entry) (*CardResult, error) {
	url := e.CardFetchURL()
	if url == "" {
		return nil, &CardError{Err: errors.New("agent has no upstream to fetch a card from")}
	}

	c.mu.Lock()
	prev := c.cards[e.Name]
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return c.staleOrErr(prev, e, fmt.Errorf("card request build failed: %w", err), "build")
	}
	if prev != nil && prev.ETag != "" {
		req.Header.Set("If-None-Match", prev.ETag)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return c.staleOrErr(prev, e, fmt.Errorf("card fetch: %w", err), "transport")
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		raw, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes))
		if err != nil {
			return c.staleOrErr(prev, e, fmt.Errorf("card read: %w", err), "read")
		}
		card := &a2a.AgentCard{}
		if err := json.Unmarshal(raw, card); err != nil {
			return c.staleOrErr(prev, e, fmt.Errorf("card parse: %w", err), "parse")
		}
		if card.Name != "" && card.Name != e.Name {
			// Cards may carry any display name; note registry drift at
			// debug level rather than rejecting (transparent gateway).
			c.logger.Debug("upstream card name differs from registry name",
				"agent", e.Name, "card_name", card.Name)
		}
		out := &CardResult{
			Agent:     e,
			Card:      card,
			Raw:       raw,
			ETag:      resp.Header.Get("ETag"),
			FetchedAt: c.now(),
			Source:    "fetched",
		}
		if len(card.SupportedInterfaces) == 0 {
			c.logger.Warn("upstream card has no supported interfaces; A2A clients will fail transport selection",
				"agent", e.Name, "url", url)
		}
		c.store(out)
		return out, nil

	case http.StatusNotModified:
		if prev == nil {
			return nil, &CardError{Err: errors.New("304 without cached card")}
		}
		next := *prev
		next.FetchedAt = c.now()
		next.Source = "revalidated"
		next.FailedRefresh = false
		next.LastErr = nil
		c.store(&next)
		return &next, nil

	default:
		return c.staleOrErr(prev, e, fmt.Errorf("card fetch status %d", resp.StatusCode), "status")
	}
}

// staleOrErr serves the previous snapshot within StaleGrace when a refresh
// fails; past the grace it returns the error so callers notice the outage.
func (c *CardCache) staleOrErr(prev *CardResult, e *Entry, err error, kind string) (*CardResult, error) {
	tag := &CardError{Err: err}
	if prev == nil {
		c.logger.Warn("agent card fetch failed", "agent", e.Name, "kind", kind, "err", err)
		return nil, tag
	}
	c.mu.Lock()
	prev.LastErr = err
	prev.FailedRefresh = true
	before := prev.FetchedAt
	c.mu.Unlock()

	age := c.now().Sub(before)
	if age <= c.staleGrace {
		c.logger.Warn("serving stale agent card within grace", "agent", e.Name,
			"age", age.String(), "stale_grace", c.staleGrace.String(), "err", err)
		next := *prev
		next.Source = "stale"
		return &next, nil
	}
	c.logger.Error("agent card fetch failed and stale grace expired",
		"agent", e.Name, "age", age.String(), "stale_grace", c.staleGrace.String(), "err", err)
	return prev, tag
}

func (c *CardCache) store(res *CardResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cards[res.Agent.Name] = res
}

// Start launches one refresher goroutine per agent: cheap, bounded by agent
// count, with per-agent jitter to avoid thundering herds against a shared
// upstream cluster. Every goroutine is owned by the returned context.
func (c *CardCache) Start(ctx context.Context) {
	for _, e := range c.reg.Entries() {
		go c.refreshLoop(ctx, e)
	}
}

func (c *CardCache) refreshLoop(ctx context.Context, e *Entry) {
	// Warm the card immediately; a gateway with all cards hot answers its
	// very first discovery request from cache.
	if _, err := c.Get(ctx, e); err != nil {
		// Logged deeper in the fetch path.
		_ = err
	}
	if c.ttl <= 0 {
		return
	}
	for {
		d := c.nextRefreshDelay()
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
		if _, err := c.Get(ctx, e); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Debug("background card refresh failed", "agent", e.Name, "err", err)
		}
	}
}

func (c *CardCache) nextRefreshDelay() time.Duration {
	// Half the TTL plus jitter up to a quarter TTL: multiple gateways and
	// multiple agents stay spread out while cards remain fresh on demand.
	half := c.ttl / 2
	jitter := time.Duration(rand.Int63n(int64(c.ttl / 4 + 1)))
	return half + jitter
}
