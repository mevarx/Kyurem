package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// clickHouseFake is a scripted ClickHouse HTTP endpoint.
type clickHouseFake struct {
	ts     *httptest.Server
	status int
	body   string

	mu   sync.Mutex
	reqs []clickHouseCapturedReq
}

// clickHouseCapturedReq is one request the fake endpoint received.
type clickHouseCapturedReq struct {
	method string
	url    *url.URL
	body   string
	header http.Header
}

// newClickHouseFake starts a fake ClickHouse endpoint that always
// answers with the given status and body.
func newClickHouseFake(t *testing.T, status int, body string) *clickHouseFake {
	t.Helper()
	f := &clickHouseFake{status: status, body: body}
	f.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, clickHouseCapturedReq{
			method: r.Method,
			url:    r.URL,
			body:   string(raw),
			header: r.Header.Clone(),
		})
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.body)
	}))
	t.Cleanup(f.ts.Close)
	return f
}

// last returns the most recent request the fake endpoint received.
func (f *clickHouseFake) last(t *testing.T) clickHouseCapturedReq {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		t.Fatal("no request was received by the fake ClickHouse endpoint")
	}
	return f.reqs[len(f.reqs)-1]
}

// withCredentials embeds credentials in the fake endpoint URL.
func (f *clickHouseFake) withCredentials(user, pass string) string {
	return "http://" + user + ":" + pass + "@" + strings.TrimPrefix(f.ts.URL, "http://")
}

// countUnescapedQuotes counts the single quotes that are not escaped,
// i.e. the quotes that delimit a SQL literal.
func countUnescapedQuotes(s string) int {
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\'' {
			continue
		}
		backslashes := 0
		for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			count++
		}
	}
	return count
}

const clickHouseRowOne = `{"Timestamp":"2026-10-20T12:00:00.123456789Z",` +
	`"TraceId":"4bf92f3577b34da6a3ce929d0e0e4736",` +
	`"SpanId":"00f067aa0ba902b7",` +
	`"ParentSpanId":"",` +
	`"SpanName":"invoke_agent",` +
	`"SpanKind":"Server",` +
	`"ServiceName":"kyurem-gateway",` +
	`"ResourceAttributes":{"service.name":"kyurem-gateway","service.version":"0.1.0"},` +
	`"SpanAttributes":{"kyurem.task.id":"task-1","kyurem.agent":"upper"},` +
	`"Duration":1500000000,` +
	`"StatusCode":"Unset",` +
	`"StatusMessage":""}`

const clickHouseRowTwo = `{"Timestamp":"2026-10-20T12:00:01.000000000Z",` +
	`"TraceId":"4bf92f3577b34da6a3ce929d0e0e4736",` +
	`"SpanId":"a3ce929d0e0e4736",` +
	`"ParentSpanId":"00f067aa0ba902b7",` +
	`"SpanName":"call_agent",` +
	`"SpanKind":"Client",` +
	`"ServiceName":"kyurem-gateway",` +
	`"ResourceAttributes":{"service.name":"kyurem-gateway"},` +
	`"SpanAttributes":{},` +
	`"Duration":250000000,` +
	`"StatusCode":"Error",` +
	`"StatusMessage":"boom"}`

func TestClickHouseStoreSpansParsesRows(t *testing.T) {
	fake := newClickHouseFake(t, http.StatusOK, clickHouseRowOne+"\n"+clickHouseRowTwo+"\n")

	store, err := NewClickHouseStore(ClickHouseOptions{
		URL:      fake.ts.URL,
		Database: "otel",
		Username: "default",
		Password: "kyurem-demo",
	})
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	from := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	records, err := store.Spans(context.Background(), SpanQuery{
		Window: Window{From: from, To: to},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("Spans: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}

	first := records[0]
	if first.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("TraceID = %q", first.TraceID)
	}
	if first.SpanID != "00f067aa0ba902b7" {
		t.Errorf("SpanID = %q", first.SpanID)
	}
	if first.ParentSpanID != "" {
		t.Errorf("ParentSpanID = %q, want empty", first.ParentSpanID)
	}
	if first.Name != "invoke_agent" {
		t.Errorf("Name = %q", first.Name)
	}
	if first.Kind != "Server" {
		t.Errorf("Kind = %q", first.Kind)
	}
	if first.ServiceName != "kyurem-gateway" {
		t.Errorf("ServiceName = %q", first.ServiceName)
	}
	wantStart := time.Date(2026, 10, 20, 12, 0, 0, 123456789, time.UTC)
	if !first.StartedAt.Equal(wantStart) {
		t.Errorf("StartedAt = %s, want %s", first.StartedAt, wantStart)
	}
	wantEnd := wantStart.Add(1500 * time.Millisecond)
	if !first.EndedAt.Equal(wantEnd) {
		t.Errorf("EndedAt = %s, want %s", first.EndedAt, wantEnd)
	}
	if first.StatusCode != "Unset" {
		t.Errorf("StatusCode = %q", first.StatusCode)
	}
	if first.StatusMessage != "" {
		t.Errorf("StatusMessage = %q, want empty", first.StatusMessage)
	}
	if got := first.Attributes["kyurem.task.id"]; got != "task-1" {
		t.Errorf("Attributes[kyurem.task.id] = %q", got)
	}
	if got := first.Attributes["kyurem.agent"]; got != "upper" {
		t.Errorf("Attributes[kyurem.agent] = %q", got)
	}
	if got := first.ResourceAttributes["service.name"]; got != "kyurem-gateway" {
		t.Errorf("ResourceAttributes[service.name] = %q", got)
	}
	if got := first.ResourceAttributes["service.version"]; got != "0.1.0" {
		t.Errorf("ResourceAttributes[service.version] = %q", got)
	}

	second := records[1]
	if second.ParentSpanID != "00f067aa0ba902b7" {
		t.Errorf("ParentSpanID = %q", second.ParentSpanID)
	}
	if second.StatusCode != "Error" {
		t.Errorf("StatusCode = %q", second.StatusCode)
	}
	if second.StatusMessage != "boom" {
		t.Errorf("StatusMessage = %q", second.StatusMessage)
	}
	if want := time.Date(2026, 10, 20, 12, 0, 1, 0, time.UTC); !second.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %s, want %s", second.StartedAt, want)
	}
	if want := time.Date(2026, 10, 20, 12, 0, 1, 250000000, time.UTC); !second.EndedAt.Equal(want) {
		t.Errorf("EndedAt = %s, want %s", second.EndedAt, want)
	}
	if second.Attributes == nil {
		t.Error("Attributes must never be nil")
	}
	if second.ResourceAttributes == nil {
		t.Error("ResourceAttributes must never be nil")
	}
}

func TestClickHouseStoreSendsExpectedRequest(t *testing.T) {
	fake := newClickHouseFake(t, http.StatusOK, "")

	// Credentials in the URL take precedence over the fields.
	store, err := NewClickHouseStore(ClickHouseOptions{
		URL:      fake.withCredentials("urluser", "urlpass"),
		Database: "otel",
		Username: "fielduser",
		Password: "fieldpass",
	})
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	from := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	if _, err := store.Spans(context.Background(), SpanQuery{
		Window:  Window{From: from, To: to},
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		Limit:   25,
	}); err != nil {
		t.Fatalf("Spans: %v", err)
	}

	req := fake.last(t)
	if req.method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.method)
	}
	if !strings.Contains(req.body, "FROM otel.otel_traces") {
		t.Errorf("body does not select the trace table: %s", req.body)
	}
	if !strings.Contains(req.body, "Timestamp >= {from:DateTime64(9)}") ||
		!strings.Contains(req.body, "Timestamp < {to:DateTime64(9)}") {
		t.Errorf("body does not carry the time bounds: %s", req.body)
	}
	if !strings.Contains(req.body, "AND TraceId = '4bf92f3577b34da6a3ce929d0e0e4736'") {
		t.Errorf("body does not carry the trace filter: %s", req.body)
	}
	if !strings.Contains(req.body, "ORDER BY Timestamp ASC") {
		t.Errorf("body does not order by timestamp: %s", req.body)
	}
	if !strings.Contains(req.body, "LIMIT {n:UInt64}") {
		t.Errorf("body does not carry the limit: %s", req.body)
	}
	if !strings.Contains(req.body, "FORMAT JSONEachRow") {
		t.Errorf("body does not ask for JSONEachRow: %s", req.body)
	}
	if strings.Contains(req.body, "SELECT") && !strings.Contains(req.body, "SELECT Timestamp, TraceId, SpanId") {
		t.Errorf("body does not select the span columns: %s", req.body)
	}

	query := req.url.Query()
	if got := query.Get("database"); got != "otel" {
		t.Errorf("database param = %q, want otel", got)
	}
	if got := query.Get("param_from"); got != "2026-10-20T12:00:00.000000000Z" {
		t.Errorf("param_from = %q", got)
	}
	if got := query.Get("param_to"); got != "2026-10-20T13:00:00.000000000Z" {
		t.Errorf("param_to = %q", got)
	}
	if got := query.Get("param_n"); got != "25" {
		t.Errorf("param_n = %q, want 25", got)
	}

	if got := req.header.Get("X-ClickHouse-User"); got != "urluser" {
		t.Errorf("X-ClickHouse-User = %q, want urluser", got)
	}
	if got := req.header.Get("X-ClickHouse-Key"); got != "urlpass" {
		t.Errorf("X-ClickHouse-Key = %q, want urlpass", got)
	}
	if got := req.header.Get("X-ClickHouse-Database"); got != "otel" {
		t.Errorf("X-ClickHouse-Database = %q, want otel", got)
	}
	if got := req.header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := req.header.Get("Authorization"); got != "" {
		t.Errorf("Authorization header must not be set, got %q", got)
	}
	if strings.Contains(req.url.String(), "urlpass") {
		t.Errorf("password leaked into the request URL: %s", req.url.String())
	}
	if strings.Contains(req.url.RawQuery, "urlpass") {
		t.Errorf("password leaked into the request query: %s", req.url.RawQuery)
	}
}

func TestClickHouseStoreReportsHTTPError(t *testing.T) {
	fake := newClickHouseFake(t, http.StatusInternalServerError,
		"Code: 62. DB::Exception: Syntax error: failed at position 1")

	store, err := NewClickHouseStore(ClickHouseOptions{URL: fake.ts.URL, Database: "otel"})
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	_, err = store.Spans(context.Background(), SpanQuery{
		Window: Window{
			From: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC),
		},
	})
	if err == nil {
		t.Fatal("expected an error for a non-2xx response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error does not mention the status code: %v", err)
	}
	if !strings.Contains(err.Error(), "DB::Exception") {
		t.Errorf("error does not carry the response body: %v", err)
	}
}

func TestBuildSpanQuery(t *testing.T) {
	t.Run("without trace filter", func(t *testing.T) {
		got := buildSpanQuery(SpanQuery{})
		if strings.Contains(got, "TraceId =") {
			t.Errorf("unexpected trace filter: %s", got)
		}
		if !strings.Contains(got, "FROM otel.otel_traces") {
			t.Errorf("missing table: %s", got)
		}
		if !strings.Contains(got, "WHERE Timestamp >= {from:DateTime64(9)} AND Timestamp < {to:DateTime64(9)}") {
			t.Errorf("missing time bounds: %s", got)
		}
		if !strings.Contains(got, "ORDER BY Timestamp ASC") {
			t.Errorf("missing order: %s", got)
		}
		if strings.Contains(got, "LIMIT") {
			t.Errorf("unexpected limit: %s", got)
		}
		if !strings.HasSuffix(got, "FORMAT JSONEachRow") {
			t.Errorf("missing format: %s", got)
		}
	})

	t.Run("with trace filter", func(t *testing.T) {
		got := buildSpanQuery(SpanQuery{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"})
		if !strings.Contains(got, "AND TraceId = '4bf92f3577b34da6a3ce929d0e0e4736'") {
			t.Errorf("missing trace filter: %s", got)
		}
		if strings.Contains(got, "LIMIT") {
			t.Errorf("unexpected limit: %s", got)
		}
	})

	t.Run("with limit", func(t *testing.T) {
		got := buildSpanQuery(SpanQuery{Limit: 50})
		if !strings.Contains(got, "LIMIT {n:UInt64}") {
			t.Errorf("missing limit: %s", got)
		}
	})

	t.Run("escapes a quote in the trace id", func(t *testing.T) {
		got := buildSpanQuery(SpanQuery{TraceID: "abc' OR 1=1 --"})
		want := `AND TraceId = 'abc\' OR 1=1 --'`
		if !strings.Contains(got, want) {
			t.Errorf("trace id is not escaped: %s", got)
		}
		if n := countUnescapedQuotes(got); n != 2 {
			t.Errorf("the trace id can break out of the literal (%d unescaped quotes): %s", n, got)
		}
	})

	t.Run("escapes a backslash in the trace id", func(t *testing.T) {
		got := buildSpanQuery(SpanQuery{TraceID: `a\b`})
		if !strings.Contains(got, `AND TraceId = 'a\\b'`) {
			t.Errorf("backslash is not escaped: %s", got)
		}
	})
}

func TestClickHouseStoreMalformedResponse(t *testing.T) {
	fake := newClickHouseFake(t, http.StatusOK, "{not json")

	store, err := NewClickHouseStore(ClickHouseOptions{URL: fake.ts.URL, Database: "otel"})
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	_, err = store.Spans(context.Background(), SpanQuery{
		Window: Window{
			From: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC),
		},
	})
	if err == nil {
		t.Fatal("expected an error for a malformed response")
	}
}

func TestNewClickHouseStoreRejectsEmptyURL(t *testing.T) {
	if _, err := NewClickHouseStore(ClickHouseOptions{Database: "otel"}); err == nil {
		t.Fatal("expected an error for an empty URL")
	}
	if _, err := NewClickHouseStore(ClickHouseOptions{URL: "   "}); err == nil {
		t.Fatal("expected an error for a blank URL")
	}
	if _, err := NewClickHouseStore(ClickHouseOptions{URL: "http://"}); err == nil {
		t.Fatal("expected an error for a URL without a host")
	}
	if _, err := NewClickHouseStore(ClickHouseOptions{URL: "ftp://clickhouse:8123"}); err == nil {
		t.Fatal("expected an error for an unsupported scheme")
	}
}

func TestNewClickHouseStoreDefaults(t *testing.T) {
	store, err := NewClickHouseStore(ClickHouseOptions{URL: "clickhouse://clickhouse:8123"})
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	if store.client.Timeout != defaultClickHouseTimeout {
		t.Errorf("timeout = %s, want %s", store.client.Timeout, defaultClickHouseTimeout)
	}
	if store.base.Scheme != "http" {
		t.Errorf("scheme = %q, want http", store.base.Scheme)
	}
	if store.base.Host != "clickhouse:8123" {
		t.Errorf("host = %q", store.base.Host)
	}

	custom, err := NewClickHouseStore(ClickHouseOptions{URL: "http://clickhouse:8123", Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer func() { _ = custom.Close() }()
	if custom.client.Timeout != time.Second {
		t.Errorf("timeout = %s, want 1s", custom.client.Timeout)
	}
}

func TestClickHouseStoreSpansStopsAtLimit(t *testing.T) {
	body := strings.Join([]string{clickHouseRowOne, clickHouseRowTwo, clickHouseRowOne}, "\n")
	fake := newClickHouseFake(t, http.StatusOK, body)

	store, err := NewClickHouseStore(ClickHouseOptions{URL: fake.ts.URL, Database: "otel"})
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	records, err := store.Spans(context.Background(), SpanQuery{Limit: 2})
	if err != nil {
		t.Fatalf("Spans: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
}

func TestClickHouseStoreSpansTolerantRow(t *testing.T) {
	// Missing timestamp, missing maps, null values.
	body := `{"Timestamp":null,"TraceId":"t1","SpanId":"s1","SpanName":null,` +
		`"ResourceAttributes":null,"SpanAttributes":null,"Duration":null}`
	fake := newClickHouseFake(t, http.StatusOK, body)

	store, err := NewClickHouseStore(ClickHouseOptions{URL: fake.ts.URL, Database: "otel"})
	if err != nil {
		t.Fatalf("NewClickHouseStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	records, err := store.Spans(context.Background(), SpanQuery{})
	if err != nil {
		t.Fatalf("Spans: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	rec := records[0]
	if !rec.StartedAt.IsZero() {
		t.Errorf("StartedAt = %s, want the zero time", rec.StartedAt)
	}
	if rec.Name != "" {
		t.Errorf("Name = %q, want empty", rec.Name)
	}
	if rec.Attributes == nil || len(rec.Attributes) != 0 {
		t.Errorf("Attributes = %v, want an empty map", rec.Attributes)
	}
	if rec.ResourceAttributes == nil || len(rec.ResourceAttributes) != 0 {
		t.Errorf("ResourceAttributes = %v, want an empty map", rec.ResourceAttributes)
	}
}

func TestClickHouseStoreDecodesArrayEvents(t *testing.T) {
	wantTime := time.Date(2026, 10, 20, 12, 0, 1, 250000000, time.UTC)
	body := `{"Timestamp":"2026-10-20T12:00:00Z","TraceId":"t1","SpanId":"s1",` +
		`"Events":[` +
		`{"Name":"kyurem.policy.decision","Timestamp":"` + wantTime.Format(time.RFC3339Nano) + `",` +
		`"Attributes":{"kyurem.policy.decision":"deny","attempts":3,"ok":true}},` +
		`{"Name":"kyurem.trust.decision","Timestamp":` + strconv.FormatInt(wantTime.UnixNano(), 10) + `,` +
		`"Attributes":{"kyurem.trust.status":"verified"}}` +
		`]}`

	records, err := decodeClickHouseSpans(strings.NewReader(body), 0)
	if err != nil {
		t.Fatalf("decodeClickHouseSpans: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	events := records[0].Events
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Name != "kyurem.policy.decision" {
		t.Errorf("event name = %q", events[0].Name)
	}
	if !events[0].Time.Equal(wantTime) {
		t.Errorf("event time = %s, want %s", events[0].Time, wantTime)
	}
	if got := events[0].Attributes["kyurem.policy.decision"]; got != "deny" {
		t.Errorf("decision = %q, want deny", got)
	}
	if got := events[0].Attributes["attempts"]; got != "3" {
		t.Errorf("attempts = %q, want 3", got)
	}
	if got := events[0].Attributes["ok"]; got != "true" {
		t.Errorf("ok = %q, want true", got)
	}
	if !events[1].Time.Equal(wantTime) {
		t.Errorf("numeric event time = %s, want %s", events[1].Time, wantTime)
	}
	if got := events[1].Attributes["kyurem.trust.status"]; got != "verified" {
		t.Errorf("trust status = %q, want verified", got)
	}
}

func TestClickHouseStoreDecodesNestedEvents(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	body := `{"Timestamp":"2026-10-20T12:00:00Z","TraceId":"t1","SpanId":"s1",` +
		`"Events.Name":["kyurem.auth.decision","kyurem.policy.decision"],` +
		`"Events.Timestamp":["2026-10-20T12:00:00.100Z","2026-10-20T12:00:00.200Z"],` +
		`"Events.Attributes":[{"kyurem.auth.method":"bearer","kyurem.auth.caller":"upper"},{"kyurem.policy.rule":"deny-echo"}]}`

	records, err := decodeClickHouseSpans(strings.NewReader(body), 0)
	if err != nil {
		t.Fatalf("decodeClickHouseSpans: %v", err)
	}
	events := records[0].Events
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if !events[0].Time.Equal(start.Add(100 * time.Millisecond)) {
		t.Errorf("first event time = %s", events[0].Time)
	}
	if events[0].Attributes["kyurem.auth.caller"] != "upper" {
		t.Errorf("auth caller = %q", events[0].Attributes["kyurem.auth.caller"])
	}
	if events[1].Name != "kyurem.policy.decision" || events[1].Attributes["kyurem.policy.rule"] != "deny-echo" {
		t.Errorf("second event = %+v", events[1])
	}
}

func TestClickHouseStoreDecodesTupleEvents(t *testing.T) {
	body := `{"Timestamp":"2026-10-20T12:00:00Z","TraceId":"t1","SpanId":"s1",` +
		`"Events":[["kyurem.policy.decision","2026-10-20T12:00:00.500Z",{"kyurem.policy.decision":"deny"}]]}`

	records, err := decodeClickHouseSpans(strings.NewReader(body), 0)
	if err != nil {
		t.Fatalf("decodeClickHouseSpans: %v", err)
	}
	events := records[0].Events
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Name != "kyurem.policy.decision" || events[0].Attributes["kyurem.policy.decision"] != "deny" {
		t.Errorf("tuple event = %+v", events[0])
	}
}

func TestClickHouseStoreIgnoresUnusableEvents(t *testing.T) {
	// A malformed Events value must never hide the span.
	body := `{"Timestamp":"2026-10-20T12:00:00Z","TraceId":"t1","SpanId":"s1","Events":"not-an-array"}`
	records, err := decodeClickHouseSpans(strings.NewReader(body), 0)
	if err != nil {
		t.Fatalf("decodeClickHouseSpans: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if len(records[0].Events) != 0 {
		t.Errorf("events = %v, want none", records[0].Events)
	}
}

func TestParseClickHouseEventTimeUnits(t *testing.T) {
	want := time.Date(2023, 11, 14, 22, 13, 20, 500000000, time.UTC)
	cases := []string{
		strconv.FormatInt(want.UnixNano(), 10),
		strconv.FormatInt(want.UnixNano()/1000, 10),
		strconv.FormatInt(want.UnixNano()/1000000, 10),
		`"` + strconv.FormatInt(want.Unix(), 10) + `.5"`,
		`"` + want.Format(time.RFC3339Nano) + `"`,
	}
	for _, raw := range cases {
		got := parseClickHouseEventTime(json.RawMessage(raw))
		if !got.Equal(want) {
			t.Errorf("parseClickHouseEventTime(%s) = %s, want %s", raw, got, want)
		}
	}
	if got := parseClickHouseEventTime(json.RawMessage(`"garbage"`)); !got.IsZero() {
		t.Errorf("garbage time = %s, want the zero time", got)
	}
}
