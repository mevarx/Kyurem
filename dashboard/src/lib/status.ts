/**
 * Status vocabulary shared by badges and tables.
 *
 * The management API reports honest states such as `unknown`; those must
 * never be dressed up as a healthy value.
 */

export type BadgeTone = "good" | "bad" | "warn" | "neutral" | "info";

const TASK_TONES: Record<string, BadgeTone> = {
  succeeded: "good",
  failed: "bad",
  unknown: "neutral",
};

const CARD_TONES: Record<string, BadgeTone> = {
  ok: "good",
  stale: "warn",
  error: "bad",
  unknown: "neutral",
};

const BREAKER_TONES: Record<string, BadgeTone> = {
  closed: "good",
  half_open: "warn",
  open: "bad",
  unknown: "neutral",
};

const VERIFICATION_TONES: Record<string, BadgeTone> = {
  verified: "good",
  unverified: "bad",
  unsigned: "warn",
  unchecked: "neutral",
};

const DECISION_TONES: Record<string, BadgeTone> = {
  allow: "good",
  warn: "warn",
  deny: "bad",
};

const POLICY_MODE_TONES: Record<string, BadgeTone> = {
  off: "neutral",
  warn: "warn",
  enforce: "good",
};

const TRUST_MODE_TONES: Record<string, BadgeTone> = {
  off: "neutral",
  warn: "warn",
  enforce: "good",
};

/** Tone for a task status (`succeeded`, `failed`, `unknown`). */
export function taskStatusTone(status: string): BadgeTone {
  return TASK_TONES[status] ?? "neutral";
}

/** Tone for an agent card status (`ok`, `stale`, `error`, `unknown`). */
export function cardStatusTone(status: string): BadgeTone {
  return CARD_TONES[status] ?? "neutral";
}

/** Tone for a circuit breaker state. */
export function breakerStateTone(state: string | undefined): BadgeTone {
  if (!state) return "neutral";
  return BREAKER_TONES[state] ?? "neutral";
}

/** Tone for a card signature verification status. */
export function verificationTone(status: string | undefined): BadgeTone {
  if (!status) return "neutral";
  return VERIFICATION_TONES[status] ?? "neutral";
}

/** Tone for an allow/deny/warn decision. */
export function decisionTone(decision: string | undefined): BadgeTone {
  if (!decision) return "neutral";
  return DECISION_TONES[decision] ?? "neutral";
}

/** Tone for a policy mode (off, warn, enforce). */
export function policyModeTone(mode: string | undefined): BadgeTone {
  if (!mode) return "neutral";
  return POLICY_MODE_TONES[mode] ?? "neutral";
}

/** Tone for a trust mode (off, warn, enforce). */
export function trustModeTone(mode: string | undefined): BadgeTone {
  if (!mode) return "neutral";
  return TRUST_MODE_TONES[mode] ?? "neutral";
}

/** True when a span event name is an access decision (trust, policy, auth). */
export function isAccessDecisionEvent(name: string): boolean {
  return (
    name === "kyurem.policy.decision" ||
    name === "kyurem.trust.decision" ||
    name === "kyurem.auth.decision"
  );
}

/** Tone for an error rate between 0 and 1. */
export function errorRateTone(rate: number): BadgeTone {
  if (rate >= 0.25) return "bad";
  if (rate > 0) return "warn";
  return "good";
}

/** Tone for a span: failed spans are red, healthy ones green. */
export function spanTone(span: { error_type?: string; status_code?: string }): BadgeTone {
  if (span.error_type) return "bad";
  const code = (span.status_code ?? "").toUpperCase();
  if (code === "ERROR" || code === "STATUS_CODE_ERROR") return "bad";
  if (code === "OK" || code === "STATUS_CODE_OK" || code === "UNSET") return "good";
  return "neutral";
}

/** True when a status string means "we do not know". */
export function isUnknownState(status: string | undefined | null): boolean {
  return status === undefined || status === null || status === "" || status === "unknown";
}
