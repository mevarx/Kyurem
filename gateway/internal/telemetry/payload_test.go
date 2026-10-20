package telemetry

import (
	"io"
	"strings"
	"testing"
)

func TestExtractPayloadIDsResponseWins(t *testing.T) {
	req := []byte(`{"jsonrpc":"2.0","method":"SendMessage","params":{"message":{"taskId":"req-task","contextId":"req-ctx"}}}`)
	resp := []byte(`{"jsonrpc":"2.0","id":1,"result":{"id":"task-1","contextId":"ctx-1","status":{"state":"working"}}}`)

	ids := ExtractPayloadIDs(req, resp)
	if ids.TaskID != "task-1" || ids.ContextID != "ctx-1" || ids.TaskStatus != "working" {
		t.Fatalf("ids = %+v", ids)
	}
}

func TestExtractPayloadIDsFillsGapsFromRequest(t *testing.T) {
	req := []byte(`{"taskId":"task-1","contextId":"ctx-1"}`)
	resp := []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)

	ids := ExtractPayloadIDs(req, resp)
	if ids.TaskID != "task-1" || ids.ContextID != "ctx-1" {
		t.Fatalf("ids = %+v, want request identities", ids)
	}
}

func TestExtractPayloadIDsSSEFrames(t *testing.T) {
	body := strings.Join([]string{
		"data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"id\":\"task-9\",\"contextId\":\"ctx-9\",\"status\":{\"state\":\"TASK_STATE_WORKING\"}}}",
		"",
		"data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"artifact\":{\"parts\":[{\"kind\":\"text\",\"text\":\"hello\"}]}}}",
		"",
		"data: [DONE]",
	}, "\r\n")

	ids := ExtractPayloadIDs(nil, []byte(body))
	if ids.TaskID != "task-9" || ids.ContextID != "ctx-9" || ids.TaskStatus != "TASK_STATE_WORKING" {
		t.Fatalf("ids = %+v", ids)
	}
}

func TestExtractPayloadIDsPrettyAndNestedJSON(t *testing.T) {
	body := []byte(`{
	  "result": {
	    "task": {
	      "id": "task-nested",
	      "contextId": "ctx-nested",
	      "status": {"state": "completed"}
	    }
	  }
	}`)
	ids := ExtractPayloadIDs(nil, body)
	if ids.TaskID != "task-nested" || ids.ContextID != "ctx-nested" || ids.TaskStatus != "completed" {
		t.Fatalf("ids = %+v", ids)
	}
}

// A prefix truncated at MaxTapBytes is not valid JSON; the tolerant regex
// fallback still finds identities that appeared early in the stream.
func TestExtractPayloadIDsTruncatedPrefix(t *testing.T) {
	body := []byte(`{"task":{"id":"task-cut","contextId":"ctx-cut","status":{"state":"working"}},"artifacts":[{"parts":[{"text":"` +
		strings.Repeat("x", 1000))
	ids := ExtractPayloadIDs(nil, body)
	if ids.TaskID != "task-cut" || ids.ContextID != "ctx-cut" || ids.TaskStatus != "working" {
		t.Fatalf("ids = %+v", ids)
	}
}

func TestExtractPayloadIDsRejectsControlCharacters(t *testing.T) {
	body := []byte("{\"taskId\":\"bad\\u0000id\",\"contextId\":\"ok\"}")
	ids := ExtractPayloadIDs(nil, body)
	if ids.TaskID != "" {
		t.Fatalf("task id = %q, want rejected", ids.TaskID)
	}
	if ids.ContextID != "ok" {
		t.Fatalf("context id = %q, want ok", ids.ContextID)
	}
}

func TestTaskFailedStates(t *testing.T) {
	failed := []string{"failed", "TASK_STATE_FAILED", "canceled", "cancelled", "rejected"}
	for _, state := range failed {
		if !TaskFailed(state) {
			t.Errorf("TaskFailed(%q) = false, want true", state)
		}
	}
	for _, state := range []string{"completed", "working", "TASK_STATE_COMPLETED", ""} {
		if TaskFailed(state) {
			t.Errorf("TaskFailed(%q) = true, want false", state)
		}
	}
}

func TestBodyTapCountsAllBytesKeepsBoundedPrefix(t *testing.T) {
	payload := strings.Repeat("a", 100) + "TAIL"
	tap := NewBodyTap(io.NopCloser(strings.NewReader(payload)), 16)

	got, err := io.ReadAll(tap)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("body changed while tapping")
	}
	if tap.Bytes() != int64(len(payload)) {
		t.Fatalf("bytes = %d, want %d", tap.Bytes(), len(payload))
	}
	if string(tap.Prefix()) != payload[:16] {
		t.Fatalf("prefix = %q, want first 16 bytes", tap.Prefix())
	}
}
