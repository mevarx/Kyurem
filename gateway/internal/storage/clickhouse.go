package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

const (
	// clickHouseTable is the trace table the OTel collector's ClickHouse
	// exporter creates (`otel.otel_traces`). The table belongs to the
	// collector, not to the gateway: the gateway only reads it back.
	clickHouseTable = "otel.otel_traces"
	// defaultClickHouseTimeout bounds one query when no timeout is set.
	defaultClickHouseTimeout = 10 * time.Second
	// maxClickHouseErrorBody bounds the response body copied into an
	// error message.
	maxClickHouseErrorBody = 512
	// clickHouseTimeLayout renders a timestamp the way ClickHouse wants
	// it in a DateTime64 literal: RFC3339 with nanosecond precision.
	clickHouseTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"
)

// ClickHouseOptions configures the ClickHouse store.
type ClickHouseOptions struct {
	// URL is the base URL of the ClickHouse HTTP interface, for example
	// "http://clickhouse:8123". A "clickhouse://" scheme is accepted too.
	URL string
	// Database holds the spans (the collector creates it, usually "otel").
	Database string
	// Username and Password authenticate the HTTP requests.
	Username string
	Password string
	// Timeout bounds one query. Zero means 10s.
	Timeout time.Duration
}

// ClickHouseStore is a Store backed by the ClickHouse HTTP interface.
//
// The OTel collector's ClickHouse exporter owns the trace table; the
// gateway reads it back with plain SQL so the management API and the
// dashboard see exactly the spans the gateway exported.
//
// The HTTP interface is used directly (POST with the statement in the
// body, query parameters in the URL) so the store needs no client
// dependency.
type ClickHouseStore struct {
	base     *url.URL
	database string
	username string
	password string
	client   *http.Client
}

// Compile-time proof that the store satisfies the frozen contract.
var _ Store = (*ClickHouseStore)(nil)

// NewClickHouseStore builds a ClickHouse-backed Store.
//
// It fails fast when the URL is missing or unusable: a misconfigured
// store should never look like an empty database.
func NewClickHouseStore(opts ClickHouseOptions) (*ClickHouseStore, error) {
	raw := strings.TrimSpace(opts.URL)
	if raw == "" {
		return nil, errors.New("storage: clickhouse URL is required")
	}

	// The credentials are split off by hand: a malformed URL must never
	// leak the password through a parse error message.
	userinfo, hostPart := splitClickHouseCredentials(raw)
	base, err := url.Parse(hostPart)
	if err != nil {
		return nil, fmt.Errorf("storage: invalid clickhouse URL: %w", err)
	}
	if base.Opaque != "" && base.Host == "" {
		// "clickhouse:8123" parses as scheme + opaque, not host:port.
		base = &url.URL{Scheme: base.Scheme, Host: base.Opaque}
	}
	switch base.Scheme {
	case "", "http":
		base.Scheme = "http"
	case "https":
	case "clickhouse", "clickhouse+http":
		base.Scheme = "http"
	default:
		return nil, fmt.Errorf("storage: unsupported clickhouse URL scheme %q", base.Scheme)
	}
	if base.Host == "" {
		return nil, errors.New("storage: invalid clickhouse URL: missing host")
	}

	// Credentials embedded in the URL take precedence over the fields.
	username, password := opts.Username, opts.Password
	if name, pass, ok := decodeClickHouseUserInfo(userinfo); ok {
		if name != "" {
			username = name
		}
		if pass != "" {
			password = pass
		}
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultClickHouseTimeout
	}

	return &ClickHouseStore{
		base:     base,
		database: strings.TrimSpace(opts.Database),
		username: username,
		password: password,
		client:   &http.Client{Timeout: timeout},
	}, nil
}

// Spans returns the spans matching the query, oldest first.
func (s *ClickHouseStore) Spans(ctx context.Context, q SpanQuery) ([]telemetry.SpanRecord, error) {
	statement := buildSpanQuery(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.requestURL(q), strings.NewReader(statement))
	if err != nil {
		return nil, fmt.Errorf("storage: build clickhouse request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if s.username != "" {
		req.Header.Set("X-ClickHouse-User", s.username)
	}
	if s.password != "" {
		req.Header.Set("X-ClickHouse-Key", s.password)
	}
	if s.database != "" {
		req.Header.Set("X-ClickHouse-Database", s.database)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage: clickhouse query failed: %w", err)
	}
	defer func() {
		// Drain a little so the connection can be reused, then close.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("storage: clickhouse query failed: status %d: %s",
			resp.StatusCode, s.errorBody(resp.Body))
	}
	return decodeClickHouseSpans(resp.Body, q.Limit)
}

// Close releases the idle connections held by the store.
func (s *ClickHouseStore) Close() error {
	if s.client != nil {
		s.client.CloseIdleConnections()
	}
	return nil
}

// requestURL builds the query URL: the base URL, the database and the
// query parameters the statement refers to.
func (s *ClickHouseStore) requestURL(q SpanQuery) string {
	u := *s.base
	u.Path = strings.TrimSuffix(u.Path, "/") + "/"
	if u.RawPath != "" {
		u.RawPath = strings.TrimSuffix(u.RawPath, "/") + "/"
	}
	params := url.Values{}
	if s.database != "" {
		params.Set("database", s.database)
	}
	params.Set("param_from", formatClickHouseTime(clickHouseWindowFrom(q.From)))
	params.Set("param_to", formatClickHouseTime(clickHouseWindowTo(q.To)))
	if q.Limit > 0 {
		params.Set("param_n", strconv.Itoa(q.Limit))
	}
	u.RawQuery = params.Encode()
	return u.String()
}

// errorBody copies a bounded, credential-free excerpt of an error
// response into the error message.
func (s *ClickHouseStore) errorBody(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, maxClickHouseErrorBody+1))
	body := string(data)
	if len(body) > maxClickHouseErrorBody {
		body = body[:maxClickHouseErrorBody] + "..."
	}
	body = strings.TrimSpace(body)
	if s.password != "" {
		body = strings.ReplaceAll(body, s.password, "***")
	}
	return body
}

// buildSpanQuery renders the one statement the store sends.
//
// The time bounds are passed as query parameters ({from:DateTime64(9)}
// and {to:DateTime64(9)}), so the statement carries no timestamp
// literal. The trace id is caller-supplied and is inlined as an escaped
// literal: single quotes and backslashes are escaped, so a trace id can
// never terminate the literal and change the statement.
func buildSpanQuery(q SpanQuery) string {
	var b strings.Builder
	b.WriteString("SELECT Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName, ResourceAttributes, SpanAttributes, Duration, StatusCode, StatusMessage\n")
	b.WriteString("FROM " + clickHouseTable + "\n")
	b.WriteString("WHERE Timestamp >= {from:DateTime64(9)} AND Timestamp < {to:DateTime64(9)}\n")
	if q.TraceID != "" {
		b.WriteString("AND TraceId = '" + escapeClickHouseLiteral(q.TraceID) + "'\n")
	}
	b.WriteString("ORDER BY Timestamp ASC\n")
	if q.Limit > 0 {
		b.WriteString("LIMIT {n:UInt64}\n")
	}
	b.WriteString("FORMAT JSONEachRow")
	return b.String()
}

// escapeClickHouseLiteral escapes a string for use inside a single
// quoted ClickHouse literal.
func escapeClickHouseLiteral(v string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`'`, `\'`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	return r.Replace(v)
}

// splitClickHouseCredentials separates the optional "user:pass@" part
// of a ClickHouse URL from the rest. The scheme is kept: only the
// credentials are removed.
func splitClickHouseCredentials(raw string) (userinfo, rest string) {
	scheme := ""
	remainder := raw
	if i := strings.Index(raw, "://"); i >= 0 {
		scheme = raw[:i+3]
		remainder = raw[i+3:]
	}
	at := strings.Index(remainder, "@")
	if at < 0 {
		return "", raw
	}
	if slash := strings.Index(remainder, "/"); slash >= 0 && slash < at {
		// The '@' belongs to the path, not to the authority.
		return "", raw
	}
	return remainder[:at], scheme + remainder[at+1:]
}

// decodeClickHouseUserInfo decodes a "user:pass" userinfo section.
func decodeClickHouseUserInfo(userinfo string) (username, password string, ok bool) {
	if userinfo == "" {
		return "", "", false
	}
	name := userinfo
	pass := ""
	if i := strings.Index(userinfo, ":"); i >= 0 {
		name = userinfo[:i]
		pass = userinfo[i+1:]
	}
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	if decoded, err := url.PathUnescape(pass); err == nil {
		pass = decoded
	}
	return name, pass, true
}

// clickHouseWindowFrom resolves the lower bound of the query window. A
// zero bound means "no lower bound", which the database expresses as
// the epoch.
func clickHouseWindowFrom(from time.Time) time.Time {
	if from.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return from
}

// clickHouseWindowTo resolves the upper bound of the query window. A
// zero bound means "now".
func clickHouseWindowTo(to time.Time) time.Time {
	if to.IsZero() {
		return time.Now()
	}
	return to
}

// formatClickHouseTime renders a timestamp as a ClickHouse DateTime64
// literal value (RFC3339 with nanoseconds).
func formatClickHouseTime(t time.Time) string {
	return t.UTC().Format(clickHouseTimeLayout)
}

// clickHouseRow is one row of a JSONEachRow response.
type clickHouseRow struct {
	Timestamp          *string           `json:"Timestamp"`
	TraceID            *string           `json:"TraceId"`
	SpanID             *string           `json:"SpanId"`
	ParentSpanID       *string           `json:"ParentSpanId"`
	SpanName           *string           `json:"SpanName"`
	SpanKind           *string           `json:"SpanKind"`
	ServiceName        *string           `json:"ServiceName"`
	ResourceAttributes map[string]string `json:"ResourceAttributes"`
	SpanAttributes     map[string]string `json:"SpanAttributes"`
	Duration           *json.Number      `json:"Duration"`
	StatusCode         *string           `json:"StatusCode"`
	StatusMessage      *string           `json:"StatusMessage"`
}

// record converts a decoded row into the normalized span record.
func (r clickHouseRow) record() telemetry.SpanRecord {
	rec := telemetry.SpanRecord{
		TraceID:            clickHouseString(r.TraceID),
		SpanID:             clickHouseString(r.SpanID),
		ParentSpanID:       clickHouseString(r.ParentSpanID),
		Name:               clickHouseString(r.SpanName),
		Kind:               clickHouseString(r.SpanKind),
		ServiceName:        clickHouseString(r.ServiceName),
		StartedAt:          parseClickHouseTime(clickHouseString(r.Timestamp)),
		StatusCode:         clickHouseString(r.StatusCode),
		StatusMessage:      clickHouseString(r.StatusMessage),
		Attributes:         clickHouseMap(r.SpanAttributes),
		ResourceAttributes: clickHouseMap(r.ResourceAttributes),
	}
	rec.EndedAt = rec.StartedAt.Add(r.duration())
	return rec
}

// duration reads the span duration in nanoseconds.
func (r clickHouseRow) duration() time.Duration {
	if r.Duration == nil {
		return 0
	}
	if ns, err := r.Duration.Int64(); err == nil {
		return time.Duration(ns)
	}
	if f, err := r.Duration.Float64(); err == nil {
		return time.Duration(f)
	}
	return 0
}

// clickHouseString reads an optional string field. A missing or null
// value is skipped and reads as the empty string.
func clickHouseString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// clickHouseMap reads an optional map field. A missing or null value
// becomes an empty map, never nil.
func clickHouseMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// parseClickHouseTime parses the timestamp of a row. A missing or
// unparsable timestamp yields the zero time.
func parseClickHouseTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	layouts := [...]string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// decodeClickHouseSpans decodes a JSONEachRow response. The body is
// streamed: rows are decoded one by one and the read stops as soon as
// the limit is reached.
func decodeClickHouseSpans(r io.Reader, limit int) ([]telemetry.SpanRecord, error) {
	dec := json.NewDecoder(r)
	records := make([]telemetry.SpanRecord, 0, 16)
	for {
		var row clickHouseRow
		err := dec.Decode(&row)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("storage: decode clickhouse response: %w", err)
		}
		records = append(records, row.record())
		if limit > 0 && len(records) >= limit {
			break
		}
	}
	return records, nil
}
