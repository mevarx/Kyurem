/**
 * Time window helpers.
 *
 * The management API accepts either an RFC 3339 timestamp or a relative
 * window such as `15m`, `1h`, `24h`, `7d` for the `from`/`to` query
 * parameters. Relative windows are resolved by the gateway, which keeps the
 * dashboard free of clock skew.
 */

export type TimeRangePresetId = "15m" | "1h" | "24h" | "7d";

export interface TimeRange {
  /** Selected preset, or "custom" when the user picked explicit timestamps. */
  readonly preset: TimeRangePresetId | "custom";
  /** API `from` value: a relative window ("24h") or an RFC 3339 timestamp. */
  readonly from: string;
  /** API `to` value: an RFC 3339 timestamp, or "" to let the server use now. */
  readonly to: string;
}

export interface TimeRangePreset {
  readonly id: TimeRangePresetId;
  readonly label: string;
  readonly shortLabel: string;
}

export const TIME_RANGE_PRESETS: readonly TimeRangePreset[] = [
  { id: "15m", label: "Last 15 minutes", shortLabel: "15m" },
  { id: "1h", label: "Last hour", shortLabel: "1h" },
  { id: "24h", label: "Last 24 hours", shortLabel: "24h" },
  { id: "7d", label: "Last 7 days", shortLabel: "7d" },
];

export const DEFAULT_TIME_RANGE: TimeRange = { preset: "24h", from: "24h", to: "" };

const RELATIVE_PATTERN = /^(\d+)\s*(s|m|h|d)$/i;

const UNIT_MS: Record<string, number> = {
  s: 1_000,
  m: 60_000,
  h: 3_600_000,
  d: 86_400_000,
};

/**
 * Parse a relative window such as "15m", "1h", "24h" or "7d" into
 * milliseconds. Returns `null` when the value is not a relative window.
 */
export function parseRelativeWindow(value: string): number | null {
  const match = RELATIVE_PATTERN.exec(value.trim());
  if (!match) return null;
  const amount = Number(match[1]);
  const unit = match[2].toLowerCase();
  const factor = UNIT_MS[unit];
  if (!Number.isFinite(amount) || factor === undefined) return null;
  return amount * factor;
}

/** Resolve a relative window (or RFC 3339 timestamp) into an absolute date. */
export function resolveWindowStart(value: string, now: Date = new Date()): Date | null {
  const relative = parseRelativeWindow(value);
  if (relative !== null) return new Date(now.getTime() - relative);
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

/** Build the time range for a preset. */
export function presetRange(id: TimeRangePresetId): TimeRange {
  return { preset: id, from: id, to: "" };
}

/** Build a custom time range from two RFC 3339 timestamps. */
export function customRange(from: string, to: string): TimeRange {
  return { preset: "custom", from, to };
}

/** Query parameters for the management API describing this window. */
export function toApiWindow(range: TimeRange): { from?: string; to?: string } {
  const query: { from?: string; to?: string } = {};
  if (range.from !== "") query.from = range.from;
  if (range.to !== "") query.to = range.to;
  return query;
}

/** Human readable description of the selected window. */
export function describeRange(range: TimeRange): string {
  if (range.preset === "custom") {
    const from = resolveWindowStart(range.from);
    const to = resolveWindowStart(range.to);
    if (!from || !to) return "Custom range";
    return `${from.toLocaleString()} → ${to.toLocaleString()}`;
  }
  const preset = TIME_RANGE_PRESETS.find((entry) => entry.id === range.preset);
  return preset ? preset.label : range.from;
}

/** Format a date for a `datetime-local` input value. */
export function toLocalInputValue(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, "0");
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}` +
    `T${pad(date.getHours())}:${pad(date.getMinutes())}`
  );
}

/** Convert a `datetime-local` input value into an RFC 3339 timestamp. */
export function fromLocalInputValue(value: string): string {
  if (value.trim() === "") return "";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "";
  return parsed.toISOString();
}
