import { useMemo, useState } from "react";
import type { Span } from "../api/types";
import { formatDuration } from "../lib/format";
import { timeWindow } from "../lib/spans";
import { spanTone } from "../lib/status";

export interface SpanWaterfallProps {
  spans: Span[];
  /** Controlled selection; omit for uncontrolled (defaults to null). */
  selectedSpanId?: string | null;
  /** Called with the newly selected span id (or null on deselect). */
  onSelectSpan?: (spanId: string | null) => void;
}

interface WaterfallRow {
  span: Span;
  agent: string;
  leftPercent: number;
  widthPercent: number;
}

const TICK_FRACTIONS = [0, 0.25, 0.5, 0.75, 1];

/**
 * Simple waterfall of the spans of a task: one lane per agent, bars are
 * positioned by start time and duration. Bars are buttons so a span can be
 * selected (linked with the span tree via `selectedSpanId`/`onSelectSpan`).
 */
export function SpanWaterfall({ spans, selectedSpanId, onSelectSpan }: SpanWaterfallProps) {
  const [internalSelection, setInternalSelection] = useState<string | null>(null);
  const selected = selectedSpanId !== undefined ? selectedSpanId : internalSelection;
  const win = useMemo(() => timeWindow(spans), [spans]);
  const total = Math.max(1, win.max - win.min);

  const rows = useMemo<WaterfallRow[]>(() => {
    if (spans.length === 0) return [];
    return spans.map((span) => {
      const start = new Date(span.started_at).getTime();
      const safeStart = Number.isFinite(start) ? start : win.min;
      const left = safeStart - win.min;
      const width = Math.max(0, span.duration_ms);
      return {
        span,
        agent: span.agent ?? "unknown",
        leftPercent: (left / total) * 100,
        widthPercent: Math.max(0.5, (width / total) * 100),
      };
    });
  }, [spans, total, win.min]);

  const ticks = useMemo(
    () => TICK_FRACTIONS.map((fraction) => formatDuration(Math.round(fraction * total))),
    [total],
  );

  if (spans.length === 0) {
    return <p className="muted">No spans to display.</p>;
  }

  const toggle = (spanId: string) => {
    const next = selected === spanId ? null : spanId;
    if (selectedSpanId === undefined) setInternalSelection(next);
    onSelectSpan?.(next);
  };

  return (
    <div className="waterfall">
      <div className="waterfall-axis" aria-hidden="true">
        {ticks.map((label, index) => (
          <span key={index}>{label}</span>
        ))}
      </div>
      <ul className="waterfall-rows">
        {rows.map((row) => {
          const isSelected = selected === row.span.span_id;
          return (
            <li key={row.span.span_id} className="waterfall-row">
              <span className="waterfall-label" title={row.span.name}>
                {row.span.name}
              </span>
              <span className="waterfall-track">
                <button
                  type="button"
                  className={`waterfall-bar waterfall-bar-${spanTone(row.span)}${
                    isSelected ? " is-selected" : ""
                  }`}
                  style={{
                    left: `${row.leftPercent}%`,
                    width: `${row.widthPercent}%`,
                    padding: 0,
                    border: "none",
                    cursor: "pointer",
                    ...(isSelected
                      ? { outline: "2px solid var(--ring)", outlineOffset: "1px" }
                      : undefined),
                  }}
                  title={`${row.span.name} · ${row.agent} · ${formatDuration(
                    row.span.duration_ms,
                  )}`}
                  aria-label={`Select span ${row.span.name}, ${row.agent}, ${formatDuration(
                    row.span.duration_ms,
                  )}, status ${row.span.status_code ?? row.span.error_type ?? "unknown"}`}
                  aria-pressed={isSelected}
                  onClick={() => toggle(row.span.span_id)}
                />
              </span>
              <span className="waterfall-duration">{formatDuration(row.span.duration_ms)}</span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
