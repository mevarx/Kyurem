/**
 * Presentation helpers.
 *
 * Every helper renders "unknown" for missing values: the management API uses
 * `null` for costs and token counts it cannot compute, and showing those as
 * zero would be a lie (PRD §5.1).
 */

export const UNKNOWN = "unknown";

function isFiniteNumber(value: number | null | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

/** Format a USD amount. `null`/`undefined`/non-finite render as "unknown". */
export function formatCost(usd: number | null | undefined): string {
  if (!isFiniteNumber(usd)) return UNKNOWN;
  if (usd === 0) return "$0.00";
  const magnitude = Math.abs(usd);
  const digits = magnitude >= 1 ? 2 : magnitude >= 0.01 ? 4 : 6;
  return `$${usd.toFixed(digits)}`;
}

/** Format a token count. `null`/`undefined`/non-finite render as "unknown". */
export function formatTokens(count: number | null | undefined): string {
  if (!isFiniteNumber(count)) return UNKNOWN;
  return count.toLocaleString("en-US");
}

/** Format a duration given in milliseconds. */
export function formatDuration(ms: number | null | undefined): string {
  if (!isFiniteNumber(ms)) return UNKNOWN;
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(2)} s`;
  const totalSeconds = ms / 1000;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds - minutes * 60;
  return `${minutes}m ${seconds.toFixed(1)}s`;
}

/** Format a ratio in the 0..1 range as a percentage. */
export function formatPercent(rate: number | null | undefined): string {
  if (!isFiniteNumber(rate)) return UNKNOWN;
  return `${(rate * 100).toFixed(1)}%`;
}

/** Format a plain integer with thousand separators. */
export function formatNumber(value: number | null | undefined): string {
  if (!isFiniteNumber(value)) return UNKNOWN;
  return value.toLocaleString("en-US");
}

/** Format an uptime given in seconds, e.g. "2h 2m". */
export function formatUptime(seconds: number | null | undefined): string {
  if (!isFiniteNumber(seconds)) return UNKNOWN;
  if (seconds < 60) return `${Math.round(seconds)}s`;
  const totalMinutes = Math.floor(seconds / 60);
  if (totalMinutes < 60) return `${totalMinutes}m`;
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (hours < 24) return `${hours}h ${minutes}m`;
  const days = Math.floor(hours / 24);
  return `${days}d ${hours % 24}h`;
}

/** Format an RFC 3339 timestamp in the user's locale. */
export function formatDateTime(iso: string | null | undefined): string {
  if (typeof iso !== "string" || iso.trim() === "") return UNKNOWN;
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return UNKNOWN;
  return parsed.toLocaleString();
}

/** Format an RFC 3339 timestamp as a short local time (for chart axes). */
export function formatTime(iso: string | null | undefined): string {
  if (typeof iso !== "string" || iso.trim() === "") return UNKNOWN;
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return UNKNOWN;
  return parsed.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

/** Human readable age of an RFC 3339 timestamp, e.g. "3 min ago". */
export function formatRelativeTime(iso: string | null | undefined, now: Date = new Date()): string {
  if (typeof iso !== "string" || iso.trim() === "") return UNKNOWN;
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return UNKNOWN;
  const deltaMs = now.getTime() - parsed.getTime();
  const future = deltaMs < 0;
  const seconds = Math.round(Math.abs(deltaMs) / 1000);
  let text: string;
  if (seconds < 60) text = `${seconds}s`;
  else if (seconds < 3600) text = `${Math.floor(seconds / 60)}m`;
  else if (seconds < 86_400) text = `${Math.floor(seconds / 3600)}h`;
  else text = `${Math.floor(seconds / 86_400)}d`;
  return future ? `in ${text}` : `${text} ago`;
}

/** Short label for a token count, e.g. "1.2k". */
export function formatTokenShort(count: number | null | undefined): string {
  if (!isFiniteNumber(count)) return UNKNOWN;
  if (Math.abs(count) < 1000) return String(count);
  if (Math.abs(count) < 1_000_000) return `${(count / 1000).toFixed(1)}k`;
  return `${(count / 1_000_000).toFixed(1)}M`;
}

/** Truncate a long identifier for display, keeping it recognisable. */
export function truncateId(value: string | null | undefined, max = 16): string {
  if (typeof value !== "string" || value === "") return UNKNOWN;
  return value.length <= max ? value : `${value.slice(0, max - 1)}…`;
}

/** Extract a short, human readable message from an unknown error value. */
export function errorMessage(error: unknown, fallback = "Something went wrong"): string {
  if (error instanceof Error && error.message !== "") return error.message;
  if (typeof error === "string" && error !== "") return error;
  return fallback;
}
