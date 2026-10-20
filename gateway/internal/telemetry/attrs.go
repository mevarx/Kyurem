// Package telemetry wires OpenTelemetry tracing for the A2A handoff path:
// W3C context propagation, one span per proxied handoff, and a bounded
// asynchronous OTLP exporter (TR-1, TR-2, TR-3, TR-5, TR-6).
package telemetry

// Pinned OpenTelemetry GenAI semantic conventions (TR-6). The conventions
// have no tagged release for the agent spans used here, so the repository
// commit is the pin; docs/telemetry-conventions.md records the upgrade path.
const (
	GenAISemconvRepo   = "https://github.com/open-telemetry/semantic-conventions-genai"
	GenAISemconvCommit = "547934afa90a34298441caf80c43b27255c3a292"
)

// Resource attributes recording the conventions pin on every span.
const (
	AttrGenAISemconvCommit    = "kyurem.genai.semconv.commit"
	AttrGenAISemconvRepo      = "kyurem.genai.semconv.repo"
	AttrGenAISemconvStability = "kyurem.genai.semconv.stability"
)

// GenAI semantic-convention attribute names used by handoff spans.
const (
	AttrOperationName    = "gen_ai.operation.name"
	AttrProviderName     = "gen_ai.provider.name"
	AttrAgentName        = "gen_ai.agent.name"
	AttrConversationID   = "gen_ai.conversation.id"
	AttrErrorType        = "error.type"
	OperationInvokeAgent = "invoke_agent"
	ProviderKyurem       = "kyurem"
)

// Kyurem-namespaced attributes (PRD §6.1).
const (
	AttrTaskID       = "kyurem.task.id"
	AttrContextID    = "kyurem.context.id"
	AttrCallerAgent  = "kyurem.caller.agent"
	AttrCalleeAgent  = "kyurem.callee.agent"
	AttrHopIndex     = "kyurem.hop.index"
	AttrCardVerified = "kyurem.card.verified"
	AttrTokensKnown  = "kyurem.tokens.known"
	AttrBytesIn      = "kyurem.bytes.in"
	AttrBytesOut     = "kyurem.bytes.out"
	AttrLatencyMS    = "kyurem.latency.ms"
	AttrTaskStatus   = "kyurem.task.status"
)

// Standard HTTP attributes carried alongside the A2A fields.
const (
	AttrHTTPStatusCode  = "http.response.status_code"
	AttrHTTPRequestMeth = "http.request.method"
	AttrURLPath         = "url.path"
)

const (
	// CallerHeader is the optional, untrusted caller-attribution hint a
	// nested agent may send when it cannot propagate a trace itself.
	CallerHeader = "X-Kyurem-Caller"
	// CallerUnknown is the fallback caller label.
	CallerUnknown = "unknown"
	// CardVerifiedUnchecked is the M2 value: cards are fetched but not
	// cryptographically verified until M5.
	CardVerifiedUnchecked = "unchecked"
)

// InvokeAgentSpanName follows the GenAI convention `invoke_agent {name}`.
func InvokeAgentSpanName(agent string) string {
	return OperationInvokeAgent + " " + agent
}
