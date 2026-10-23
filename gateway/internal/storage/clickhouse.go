package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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
	b.WriteString("SELECT Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName, ResourceAttributes, SpanAttributes, Duration, StatusCode, StatusMessage, Events\n")
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
	// The collector's ClickHouse exporter has emitted span events both as
	// an array of tuples ("Events") and as Nested columns ("Events.Name",
	// ...). Both shapes are accepted; unknown shapes are ignored rather
	// than failing the whole query.
	Events           json.RawMessage   `json:"Events"`
	EventsName       []string          `json:"Events.Name"`
	EventsTimestamp  []json.RawMessage `json:"Events.Timestamp"`
	EventsTime       []json.RawMessage `json:"Events.Time"`
	EventsAttributes []json.RawMessage `json:"Events.Attributes"`
}

// clickHouseEvent is one event of the array-of-tuples encoding.
type clickHouseEvent struct {
	Name       *string         `json:"Name"`
	Timestamp  json.RawMessage `json:"Timestamp"`
	Time       json.RawMessage `json:"Time"`
	Attributes json.RawMessage `json:"Attributes"`
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
	rec.Events = r.events()
	for i := range rec.Events {
		if rec.Events[i].Time.IsZero() {
			// An event without a usable timestamp still happened inside the
			// span; anchor it at the start instead of dropping it.
			rec.Events[i].Time = rec.StartedAt
		}
	}
	return rec
}

// events decodes the span events of a row. Newer collector versions emit
// an array of tuples, older ones a set of Nested columns; both are read.
// A shape that cannot be decoded yields no events instead of failing the
// whole query: losing an event must not hide the span.
func (r clickHouseRow) events() []telemetry.SpanEvent {
	if events := decodeClickHouseEventArray(r.Events); len(events) > 0 {
		return events
	}
	events := make([]telemetry.SpanEvent, 0, len(r.EventsName))
	for i, name := range r.EventsName {
		if name == "" {
			continue
		}
		event := telemetry.SpanEvent{
			Name:       name,
			Time:       clickHouseEventTime(r.EventsTimestamp, r.EventsTime, i),
			Attributes: decodeClickHouseAttributes(clickHouseEventAttrs(r.EventsAttributes, i)),
		}
		events = append(events, event)
	}
	return events
}

// decodeClickHouseEventArray decodes the "Events" column when it is an
// array: normally an array of objects, but an array of positional tuples
// is accepted too.
func decodeClickHouseEventArray(raw json.RawMessage) []telemetry.SpanEvent {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] != '[' {
		return nil
	}

	var objects []clickHouseEvent
	if err := json.Unmarshal(trimmed, &objects); err == nil {
		events := make([]telemetry.SpanEvent, 0, len(objects))
		for _, obj := range objects {
			if obj.Name == nil || *obj.Name == "" {
				continue
			}
			events = append(events, telemetry.SpanEvent{
				Name:       *obj.Name,
				Time:       clickHouseEventTime([]json.RawMessage{obj.Timestamp}, []json.RawMessage{obj.Time}, 0),
				Attributes: decodeClickHouseAttributes(obj.Attributes),
			})
		}
		if len(events) > 0 {
			return events
		}
	}

	// Positional encoding: [["name", "timestamp", {attributes}], ...].
	var tuples [][]json.RawMessage
	if err := json.Unmarshal(trimmed, &tuples); err != nil {
		return nil
	}
	events := make([]telemetry.SpanEvent, 0, len(tuples))
	for _, tuple := range tuples {
		if len(tuple) == 0 {
			continue
		}
		name := decodeClickHouseEventName(tuple[0])
		if name == "" {
			continue
		}
		event := telemetry.SpanEvent{Name: name}
		if len(tuple) > 1 {
			event.Time = clickHouseEventTime([]json.RawMessage{tuple[1]}, nil, 0)
		}
		if len(tuple) > 2 {
			event.Attributes = decodeClickHouseAttributes(tuple[2])
		}
		events = append(events, event)
	}
	return events
}

// decodeClickHouseEventName reads the name of a positional event.
func decodeClickHouseEventName(raw json.RawMessage) string {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return name
	}
	return ""
}

// clickHouseEventAttrs returns the attributes entry at an index of a
// Nested column, if the column is long enough.
func clickHouseEventAttrs(attrs []json.RawMessage, i int) json.RawMessage {
	if i < 0 || i >= len(attrs) {
		return nil
	}
	return attrs[i]
}

// clickHouseEventTime reads the event time at an index: the primary
// timestamp column first, then the legacy time column.
func clickHouseEventTime(timestamps, legacy []json.RawMessage, i int) time.Time {
	if i >= 0 && i < len(timestamps) {
		if t := parseClickHouseEventTime(timestamps[i]); !t.IsZero() {
			return t
		}
	}
	if i >= 0 && i < len(legacy) {
		return parseClickHouseEventTime(legacy[i])
	}
	return time.Time{}
}

// parseClickHouseEventTime parses an event timestamp. Exporter versions
// emit either an RFC3339 string or a Unix timestamp in seconds,
// milliseconds, microseconds or nanoseconds.
func parseClickHouseEventTime(raw json.RawMessage) time.Time {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return time.Time{}
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		if t := parseClickHouseTime(text); !t.IsZero() {
			return t
		}
		trimmedText := strings.TrimSpace(text)
		if i, err := strconv.ParseInt(trimmedText, 10, 64); err == nil {
			return clickHouseUnixInt(i)
		}
		if f, err := strconv.ParseFloat(trimmedText, 64); err == nil {
			return clickHouseUnixTime(f)
		}
		return time.Time{}
	}
	var num json.Number
	if err := json.Unmarshal(trimmed, &num); err == nil {
		if i, err := num.Int64(); err == nil {
			return clickHouseUnixInt(i)
		}
		if f, err := num.Float64(); err == nil {
			return clickHouseUnixTime(f)
		}
	}
	return time.Time{}
}

// clickHouseUnixInt converts an integral Unix timestamp to a time,
// choosing the unit from its magnitude. The integer path avoids the
// precision loss of a float64 for nanosecond timestamps.
func clickHouseUnixInt(v int64) time.Time {
	abs := v
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 1e17:
		return time.Unix(0, v).UTC()
	case abs >= 1e14:
		return time.Unix(0, v*int64(time.Microsecond)).UTC()
	case abs >= 1e11:
		return time.Unix(0, v*int64(time.Millisecond)).UTC()
	case abs >= 1e8:
		return time.Unix(v, 0).UTC()
	default:
		return time.Time{}
	}
}

// clickHouseUnixTime converts a Unix timestamp to a time, choosing the
// unit from its magnitude (seconds, milliseconds, microseconds or
// nanoseconds).
func clickHouseUnixTime(v float64) time.Time {
	abs := math.Abs(v)
	switch {
	case abs >= 1e17:
		return time.Unix(0, int64(v)).UTC()
	case abs >= 1e14:
		return time.Unix(0, int64(v)*int64(time.Microsecond)).UTC()
	case abs >= 1e11:
		return time.Unix(0, int64(v)*int64(time.Millisecond)).UTC()
	case abs >= 1e8:
		seconds := int64(v)
		nanos := int64((v - float64(seconds)) * float64(time.Second))
		return time.Unix(seconds, nanos).UTC()
	default:
		return time.Time{}
	}
}

// decodeClickHouseAttributes decodes an event attribute map. The exporter
// emits a plain string map; an array of {key, value} pairs is accepted
// too. Anything unrecognized yields an empty map, never nil.
func decodeClickHouseAttributes(raw json.RawMessage) map[string]string {
	attrs := map[string]string{}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return attrs
	}
	if trimmed[0] == '{' {
		var values map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return attrs
		}
		for key, value := range values {
			attrs[key] = clickHouseAttributeValue(value)
		}
		return attrs
	}
	if trimmed[0] == '[' {
		var pairs []struct {
			Key   string          `json:"Key"`
			Value json.RawMessage `json:"Value"`
		}
		if err := json.Unmarshal(trimmed, &pairs); err != nil {
			return attrs
		}
		for _, pair := range pairs {
			if pair.Key == "" {
				continue
			}
			attrs[pair.Key] = clickHouseAttributeValue(pair.Value)
		}
	}
	return attrs
}

// clickHouseAttributeValue renders an attribute value as the flattened
// string the query layer uses.
func clickHouseAttributeValue(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return text
	}
	// OTel AnyValue shapes: {"StringValue": ...} or {"stringValue": ...}.
	var anyValue struct {
		StringValue *string  `json:"StringValue"`
		BoolValue   *bool    `json:"BoolValue"`
		IntValue    *int64   `json:"IntValue"`
		DoubleValue *float64 `json:"DoubleValue"`
	}
	if err := json.Unmarshal(trimmed, &anyValue); err == nil {
		switch {
		case anyValue.StringValue != nil:
			return *anyValue.StringValue
		case anyValue.BoolValue != nil:
			return strconv.FormatBool(*anyValue.BoolValue)
		case anyValue.IntValue != nil:
			return strconv.FormatInt(*anyValue.IntValue, 10)
		case anyValue.DoubleValue != nil:
			return strconv.FormatFloat(*anyValue.DoubleValue, 'f', -1, 64)
		}
	}
	// Numbers, booleans and arrays are flattened as their JSON text.
	return string(trimmed)
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
