package telemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
)

// MaxTapBytes bounds the request/response prefix kept for task/context ID
// extraction. Payload capture (TR-4) is off; these bytes never leave process
// memory and are discarded when the handoff span ends.
const MaxTapBytes = 64 << 10

// BodyTap counts every byte flowing through a body while retaining only a
// bounded prefix for attribute extraction. Reads stay streaming: nothing
// waits for the whole body.
type BodyTap struct {
	rc    io.ReadCloser
	limit int
	n     atomic.Int64

	mu  sync.Mutex
	buf []byte
}

// NewBodyTap wraps rc. A non-positive limit uses MaxTapBytes.
func NewBodyTap(rc io.ReadCloser, limit int) *BodyTap {
	if limit <= 0 {
		limit = MaxTapBytes
	}
	return &BodyTap{rc: rc, limit: limit}
}

func (t *BodyTap) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 {
		t.n.Add(int64(n))
		t.mu.Lock()
		if remain := t.limit - len(t.buf); remain > 0 {
			take := n
			if take > remain {
				take = remain
			}
			t.buf = append(t.buf, p[:take]...)
		}
		t.mu.Unlock()
	}
	return n, err
}

// Close closes the wrapped body.
func (t *BodyTap) Close() error { return t.rc.Close() }

// Bytes returns the total bytes read, including those beyond the prefix cap.
func (t *BodyTap) Bytes() int64 { return t.n.Load() }

// Prefix returns a copy of the retained prefix.
func (t *BodyTap) Prefix() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.buf...)
}

// PayloadIDs are the A2A identifiers observed in a bounded payload prefix.
type PayloadIDs struct {
	TaskID     string
	ContextID  string
	TaskStatus string
}

// ExtractPayloadIDs merges IDs from request and response prefixes. Response
// values win because they describe the actual task the upstream produced;
// request values fill gaps for streaming responses whose IDs were not yet
// in the captured prefix.
func ExtractPayloadIDs(request, response []byte) PayloadIDs {
	ids := idsFromBody(response)
	req := idsFromBody(request)
	if ids.TaskID == "" {
		ids.TaskID = req.TaskID
	}
	if ids.ContextID == "" {
		ids.ContextID = req.ContextID
	}
	return ids
}

func idsFromBody(b []byte) PayloadIDs {
	var out PayloadIDs
	if len(b) == 0 {
		return out
	}

	// SSE frames and newline-delimited JSON: each data line is a complete
	// JSON document.
	for _, raw := range bytes.Split(b, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
			continue
		}
		var v any
		if err := json.Unmarshal(line, &v); err == nil {
			scanIDs(v, &out)
		}
	}

	// Plain (possibly pretty-printed) JSON body.
	var v any
	if err := json.Unmarshal(bytes.TrimSpace(b), &v); err == nil {
		scanIDs(v, &out)
	}

	// Tolerant fallback for prefixes truncated at MaxTapBytes and for SSE
	// frames the line parser could not decode.
	scanIDsRegex(b, &out)
	return out
}

func scanIDs(v any, out *PayloadIDs) {
	switch t := v.(type) {
	case map[string]any:
		if s := stringField(t, "taskId"); s != "" && out.TaskID == "" {
			out.TaskID = s
		}
		if s := stringField(t, "contextId"); s != "" && out.ContextID == "" {
			out.ContextID = s
		}
		if s := stringField(t, "state"); s != "" {
			// Streaming frames advance the task state; keep the latest
			// observed state rather than the first (submitted).
			out.TaskStatus = s
		}
		// A2A Task JSON uses "id" alongside "contextId"/"status"; message
		// and JSON-RPC envelope IDs never have those siblings.
		if id := stringField(t, "id"); id != "" && out.TaskID == "" {
			_, hasContext := t["contextId"]
			_, hasStatus := t["status"]
			if hasContext || hasStatus {
				out.TaskID = id
			}
		}
		for _, child := range t {
			scanIDs(child, out)
		}
	case []any:
		for _, child := range t {
			scanIDs(child, out)
		}
	}
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return sanitizeID(s)
}

var (
	taskIDRe       = regexp.MustCompile(`"taskId"\s*:\s*"([^"]{1,256})"`)
	contextIDRe    = regexp.MustCompile(`"contextId"\s*:\s*"([^"]{1,256})"`)
	taskStateRe    = regexp.MustCompile(`"state"\s*:\s*"([^"]{1,64})"`)
	taskObjectIDRe = regexp.MustCompile(`"task"\s*:\s*\{[^{}]{0,1000}?"id"\s*:\s*"([^"]{1,256})"`)
)

func scanIDsRegex(b []byte, out *PayloadIDs) {
	if len(b) == 0 {
		return
	}
	if out.TaskID == "" {
		out.TaskID = firstMatch(taskIDRe, b)
	}
	if out.TaskID == "" {
		out.TaskID = firstMatch(taskObjectIDRe, b)
	}
	if out.ContextID == "" {
		out.ContextID = firstMatch(contextIDRe, b)
	}
	if out.TaskStatus == "" {
		out.TaskStatus = firstMatch(taskStateRe, b)
	}
}

func firstMatch(re *regexp.Regexp, b []byte) string {
	m := re.FindSubmatch(b)
	if len(m) < 2 {
		return ""
	}
	return sanitizeID(string(m[1]))
}

func sanitizeID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	runes := []rune(v)
	if len(runes) > 256 {
		runes = runes[:256]
	}
	for _, r := range runes {
		// Backslashes only appear here for JSON escapes copied by the
		// tolerant regex fallback (for example `\u0000`); rejecting them
		// keeps control characters out of span attributes.
		if r == '\\' || !unicode.IsPrint(r) {
			return ""
		}
	}
	return string(runes)
}

// TaskFailed reports whether an A2A task state should mark the span as an
// error. Values may be proto-style (TASK_STATE_FAILED) or SDK-style
// (failed), so matching is case-insensitive and substring-based.
func TaskFailed(status string) bool {
	s := strings.ToLower(status)
	return strings.Contains(s, "failed") ||
		strings.Contains(s, "canceled") ||
		strings.Contains(s, "cancelled") ||
		strings.Contains(s, "rejected")
}
