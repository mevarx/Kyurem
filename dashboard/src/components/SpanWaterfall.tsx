import { useMemo } from "react";
import type { Span } from "../api/types";
import { formatDuration } from "../lib/format";
import { spanTone } from "../lib/status";

export interface SpanWaterfallProps {
  spans: Span[];
}

interface WaterfallRow {
  span: Span;
  agent: string;
  leftPercent: number;
  widthPercent: number;
}

/**
 * Simple waterfall of the spans of a task: one lane per agent, bars are
 * positioned by start time and duration.
 */
export function SpanWaterfall({ spans }: SpanWaterfallProps) {
  const rows = useMemo<WaterfallRow[]>(() => {
    if (spans.length === 0) return [];
    const starts = spans.map((span) => new Date(span.started_at).getTime());
    const ends = spans.map((span, index) => starts[index] + span.duration_ms);
    const min = Math.min(...starts);
    const max = Math.max(...ends);
    const total = Math.max(1, max - min);

    return spans.map((span, index) => {
      const left = starts[index] - min;
      const width = Math.max(0, ends[index] - starts[index]);
      return {
        span,
        agent: span.agent ?? "unknown",
        leftPercent: (left / total) * 100,
        widthPercent: Math.max(0.5, (width / total) * 100),
      };
    });
  }, [spans]);

  if (spans.length === 0) {
    return <p className="muted">No spans to display.</p>;
  }

  return (
    <div className="waterfall">
      <div className="waterfall-axis" aria-hidden="true">
        <span>start</span>
        <span>end</span>
      </div>
      <ul className="waterfall-rows">
        {rows.map((row) => (
          <li key={row.span.span_id} className="waterfall-row">
            <span className="waterfall-label" title={row.span.name}>
              {row.span.name}
            </span>
            <span className="waterfall-track">
              <span
                className={`waterfall-bar waterfall-bar-${spanTone(row.span)}`}
                style={{ left: `${row.leftPercent}%`, width: `${row.widthPercent}%` }}
                title={`${row.agent} · ${formatDuration(row.span.duration_ms)}`}
              />
            </span>
            <span className="waterfall-duration">{formatDuration(row.span.duration_ms)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
