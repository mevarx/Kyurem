package telemetry

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Usage is the token usage an agent reported for one handoff.
//
// A2A has no dedicated usage field yet, so the gateway accepts the shapes
// agents realistically use: a `usage` object (OpenAI-style
// `input_tokens`/`prompt_tokens` and `output_tokens`/`completion_tokens`)
// anywhere in the response, plus a `model` string. When nothing is
// reported the usage stays unknown: the span says so instead of claiming
// zero tokens (PRD §4.3, CO-1).
type Usage struct {
	// Model is the model the agent reported, empty when it reported none.
	Model string
	// InputTokens and OutputTokens are the reported token counts.
	InputTokens  int64
	OutputTokens int64
	// Known is true when the agent reported usage.
	Known bool
}

// ExtractUsage reads the usage an agent reported from the bounded request
// and response prefixes. Response values win: they describe the work that
// was actually done.
func ExtractUsage(request, response []byte) Usage {
	out := Usage{}
	scanUsage(response, &out)
	if !out.Known {
		scanUsage(request, &out)
	}
	if !out.Known {
		extractUsageRegex(response, &out)
	}
	if out.Model == "" {
		if m := usageModelRegex.FindSubmatch(response); m != nil {
			out.Model = sanitizeID(string(m[1]))
		}
	}
	return out
}

// scanUsage walks a JSON payload and collects the first usage object and
// the first model name it finds.
func scanUsage(b []byte, out *Usage) {
	if len(b) == 0 {
		return
	}
	// SSE frames and newline-delimited JSON: every data line is a whole
	// document.
	for _, raw := range bytes.Split(b, []byte("\n")) {
		line := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(raw), []byte("data:")))
		if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
			continue
		}
		var v any
		if err := json.Unmarshal(line, &v); err == nil {
			scanUsageValue(v, out)
		}
	}
	// Plain (possibly pretty-printed) JSON body.
	var v any
	if err := json.Unmarshal(bytes.TrimSpace(b), &v); err == nil {
		scanUsageValue(v, out)
	}
}

func scanUsageValue(v any, out *Usage) {
	switch t := v.(type) {
	case map[string]any:
		if out.Model == "" {
			if m, ok := t["model"].(string); ok {
				out.Model = sanitizeID(m)
			}
		}
		if u, ok := t["usage"].(map[string]any); ok {
			applyUsage(u, out)
		}
		for _, child := range t {
			scanUsageValue(child, out)
		}
	case []any:
		for _, child := range t {
			scanUsageValue(child, out)
		}
	}
}

// applyUsage reads the token counts of a usage object. The first object
// that carries counts wins: later ones (nested in a list, for instance)
// must not overwrite it.
func applyUsage(u map[string]any, out *Usage) {
	if out.Known {
		return
	}
	in, inOK := usageNumber(u, "input_tokens", "inputTokens", "prompt_tokens", "promptTokens")
	outTok, outOK := usageNumber(u, "output_tokens", "outputTokens", "completion_tokens", "completionTokens")
	if !inOK && !outOK {
		return
	}
	out.InputTokens = in
	out.OutputTokens = outTok
	out.Known = true
}

func usageNumber(u map[string]any, keys ...string) (int64, bool) {
	for _, k := range keys {
		raw, ok := u[k]
		if !ok {
			continue
		}
		switch n := raw.(type) {
		case float64:
			if n < 0 {
				return 0, false
			}
			return int64(n), true
		case json.Number:
			v, err := n.Int64()
			if err != nil || v < 0 {
				return 0, false
			}
			return v, true
		case string:
			v, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
			if err != nil || v < 0 {
				return 0, false
			}
			return v, true
		}
	}
	return 0, false
}

var (
	// Tolerant fallbacks for prefixes truncated at MaxTapBytes.
	usageInputRegex  = regexp.MustCompile(`"(?:input_tokens|prompt_tokens)"\s*:\s*(\d+)`)
	usageOutputRegex = regexp.MustCompile(`"(?:output_tokens|completion_tokens)"\s*:\s*(\d+)`)
	usageModelRegex  = regexp.MustCompile(`"model"\s*:\s*"([^"]{1,128})"`)
)

// extractUsageRegex recovers the token counts of a truncated payload.
func extractUsageRegex(b []byte, out *Usage) {
	if len(b) == 0 || out.Known {
		return
	}
	in := usageInputRegex.FindSubmatch(b)
	outTok := usageOutputRegex.FindSubmatch(b)
	if in == nil && outTok == nil {
		return
	}
	out.Known = true
	if in != nil {
		out.InputTokens, _ = strconv.ParseInt(string(in[1]), 10, 64)
	}
	if outTok != nil {
		out.OutputTokens, _ = strconv.ParseInt(string(outTok[1]), 10, 64)
	}
}
