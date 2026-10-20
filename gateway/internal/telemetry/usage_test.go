package telemetry

import "testing"

func TestExtractUsageFromJSONRPCResult(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"result":{"id":"task-1","contextId":"ctx-1",
		"status":{"state":"TASK_STATE_COMPLETED"},
		"metadata":{"model":"demo-echo","usage":{"input_tokens":120,"output_tokens":45}}}}`)

	usage := ExtractUsage(nil, body)
	if !usage.Known {
		t.Fatal("usage not detected")
	}
	if usage.InputTokens != 120 || usage.OutputTokens != 45 {
		t.Errorf("tokens = %d/%d, want 120/45", usage.InputTokens, usage.OutputTokens)
	}
	if usage.Model != "demo-echo" {
		t.Errorf("model = %q, want demo-echo", usage.Model)
	}
}

func TestExtractUsageOpenAIStyle(t *testing.T) {
	body := []byte(`{"result":{"metadata":{"model":"gpt-x",
		"usage":{"prompt_tokens":10,"completion_tokens":3}}}}`)

	usage := ExtractUsage(nil, body)
	if !usage.Known {
		t.Fatal("usage not detected")
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 3 {
		t.Errorf("tokens = %d/%d, want 10/3", usage.InputTokens, usage.OutputTokens)
	}
}

func TestExtractUsageFromSSEFrame(t *testing.T) {
	body := []byte("data: {\"result\":{\"metadata\":{\"usage\":{\"input_tokens\":7,\"output_tokens\":2}}}}\n\n")

	usage := ExtractUsage(nil, body)
	if !usage.Known {
		t.Fatal("usage not detected in SSE frame")
	}
	if usage.InputTokens != 7 || usage.OutputTokens != 2 {
		t.Errorf("tokens = %d/%d, want 7/2", usage.InputTokens, usage.OutputTokens)
	}
}

func TestExtractUsageUnknown(t *testing.T) {
	body := []byte(`{"result":{"id":"task-1","status":{"state":"TASK_STATE_COMPLETED"}}}`)

	usage := ExtractUsage(nil, body)
	if usage.Known {
		t.Errorf("usage reported for a payload without usage: %+v", usage)
	}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Errorf("unknown usage must stay zero-valued, got %+v", usage)
	}
}

func TestExtractUsageTruncatedPrefix(t *testing.T) {
	// A prefix cut in the middle of a JSON document: the tolerant regex
	// fallback still recovers the token counts.
	body := []byte(`{"result":{"metadata":{"usage":{"input_tokens":99,"output_tokens":1`)

	usage := ExtractUsage(nil, body)
	if !usage.Known {
		t.Fatal("usage not recovered from the truncated prefix")
	}
	if usage.InputTokens != 99 {
		t.Errorf("input tokens = %d, want 99", usage.InputTokens)
	}
}

func TestExtractUsageResponseWins(t *testing.T) {
	request := []byte(`{"method":"message/send","metadata":{"usage":{"input_tokens":1,"output_tokens":1}}}`)
	response := []byte(`{"result":{"metadata":{"usage":{"input_tokens":50,"output_tokens":5}}}}`)

	usage := ExtractUsage(request, response)
	if usage.InputTokens != 50 || usage.OutputTokens != 5 {
		t.Errorf("tokens = %d/%d, want the response values 50/5", usage.InputTokens, usage.OutputTokens)
	}
}
