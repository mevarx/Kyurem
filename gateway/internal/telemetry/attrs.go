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
	// AttrCostUSD is the computed cost of the span (CO-2). It is only
	// set when the agent reported usage AND the price table knows the
	// model; an unknown cost is never written as zero.
	AttrCostUSD = "kyurem.cost.usd"
)

// Trust and policy attributes (M5, TP-3, TP-4).
const (
	AttrAuthMethod     = "kyurem.auth.method"
	AttrAuthCaller     = "kyurem.auth.caller"
	AttrSkillID        = "kyurem.skill.id"
	AttrPolicyMode     = "kyurem.policy.mode"
	AttrPolicyDecision = "kyurem.policy.decision"
	AttrPolicyRule     = "kyurem.policy.rule"
	AttrPolicyReason   = "kyurem.policy.reason"
	AttrTrustMode      = "kyurem.trust.mode"
	AttrTrustStatus    = "kyurem.trust.status"
	AttrTrustKey       = "kyurem.trust.key"
	AttrTrustReason    = "kyurem.trust.reason"
)

// Span event names for trust, policy and authentication decisions.
const (
	EventAuthDecision   = "kyurem.auth.decision"
	EventPolicyDecision = "kyurem.policy.decision"
	EventTrustDecision  = "kyurem.trust.decision"
)

// GenAI usage and model attributes (CO-1). They are only set when the
// agent reported them.
const (
	AttrUsageInput   = "gen_ai.usage.input_tokens"
	AttrUsageOutput  = "gen_ai.usage.output_tokens"
	AttrRequestModel = "gen_ai.request.model"
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
	// CardVerifiedUnchecked means card verification was not performed
	// (trust.mode off or no card snapshot yet).
	CardVerifiedUnchecked = "unchecked"
	// CardVerifiedTrue/False are the trust decision values.
	CardVerifiedTrue  = "true"
	CardVerifiedFalse = "false"
)

// InvokeAgentSpanName follows the GenAI convention `invoke_agent {name}`.
func InvokeAgentSpanName(agent string) string {
	return OperationInvokeAgent + " " + agent
}
