package mgmtapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/storage"
)

// Query parameter defaults and bounds (mirrored in api/openapi.yaml).
const (
	// DefaultWindow is the look-back used when "from" is absent.
	DefaultWindow = 24 * time.Hour
	// DefaultLimit is the task page size when "limit" is absent.
	DefaultLimit = 50
	// MinLimit and MaxLimit bound the task page size.
	MinLimit = 1
	MaxLimit = 500
	// DefaultGroupBy is the default cost roll-up grouping.
	DefaultGroupBy = "agent"
	// DefaultDecisionsLimit is the audit page size when "limit" is absent.
	DefaultDecisionsLimit = 100
	// MaxDecisionsLimit bounds the audit page size.
	MaxDecisionsLimit = 500
)

// validDecisionKinds are the audit kinds the API accepts.
var validDecisionKinds = map[string]bool{
	"policy": true,
	"trust":  true,
	"auth":   true,
}

// validStatuses are the task statuses the API accepts.
var validStatuses = map[string]bool{
	"succeeded": true,
	"failed":    true,
	"unknown":   true,
}

// validGroupBys are the cost roll-up groupings the API accepts.
var validGroupBys = map[string]bool{
	"agent":  true,
	"model":  true,
	"task":   true,
	"window": true,
}

// parseWindow reads the "from" and "to" parameters.
//
// Both accept an RFC 3339 timestamp or a relative duration ("15m", "1h",
// "24h", "7d"). A relative duration is an offset backwards from now: the
// default window is the last 24 hours, "from=15m" is the last 15 minutes.
func (s *server) parseWindow(r *http.Request) (storage.Window, error) {
	q := r.URL.Query()
	now := s.deps.Now()
	from, err := parseTimeParam(q.Get("from"), now, now.Add(-DefaultWindow))
	if err != nil {
		return storage.Window{}, fmt.Errorf("invalid from parameter: %w", err)
	}
	to, err := parseTimeParam(q.Get("to"), now, now)
	if err != nil {
		return storage.Window{}, fmt.Errorf("invalid to parameter: %w", err)
	}
	if from.After(to) {
		return storage.Window{}, fmt.Errorf("invalid window: from (%s) is after to (%s)",
			from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	return storage.Window{From: from, To: to}, nil
}

// parseTimeParam parses one timestamp parameter: an RFC 3339 timestamp, a
// relative duration or the default.
func parseTimeParam(raw string, now time.Time, def time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	d, err := parseRelativeDuration(raw)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(-d), nil
}

// parseRelativeDuration parses "15m", "1h", "24h", "7d" and friends. It
// accepts the Go duration syntax plus the day and week units Go does not
// know.
func parseRelativeDuration(raw string) (time.Duration, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("%q is not a duration", raw)
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration", raw)
	}
	switch strings.TrimSpace(s[i:]) {
	case "s":
		return time.Duration(n) * time.Second, nil
	case "m":
		return time.Duration(n) * time.Minute, nil
	case "h":
		return time.Duration(n) * time.Hour, nil
	case "d":
		return time.Duration(n) * 24 * time.Hour, nil
	case "w":
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("%q is not a duration", raw)
	}
}

// parseInt parses an integer parameter, falling back to def when the
// parameter is absent.
func parseInt(raw string, def int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%q is not an integer", raw)
	}
	return n, nil
}

// parseFloat parses a float parameter.
func parseFloat(raw string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(raw), 64)
}

// clampLimit keeps the page size inside the documented bounds. Out-of-range
// values are clamped, not rejected.
func clampLimit(limit int) int {
	if limit < MinLimit {
		return MinLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}

// aliasSegment is the last segment of an alias path: the routing key a
// client actually uses ("/a2a/plan" -> "plan").
func aliasSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
