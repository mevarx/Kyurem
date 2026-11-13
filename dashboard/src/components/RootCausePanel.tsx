import { useMemo, useState } from "react";
import type { RootCause, Span } from "../api/types";
import { formatDuration } from "../lib/format";
import { EmptyState } from "./EmptyState";
import { StatusBadge } from "./StatusBadge";
import {
  DashboardCard,
  DetailHeader,
} from "./watermelon/capitalio-dashboard/components/capitalio/shared";

export interface RootCausePanelProps {
  rootCause: RootCause | null | undefined;
  /** Spans of the task, used to name the affected spans. */
  spans?: Span[];
  /**
   * Optional "jump to span" affordance. When provided, each affected span
   * renders a button that selects the span. Defaults to a plain list.
   */
  onSelectSpan?: (spanId: string) => void;
}

interface AffectedSpan {
  id: string;
  name: string;
  agent: string;
  durationMs: number | null;
}

/**
 * Root-cause panel.
 *
 * The gateway derives the root cause heuristically ("earliest error in time
 * order whose descendants contain the failure"). The panel therefore labels
 * the result as a hint, never as a verdict (PRD UI-3).
 */
export function RootCausePanel({ rootCause, spans = [], onSelectSpan }: RootCausePanelProps) {
  const [showDetails, setShowDetails] = useState(false);

  const affected: AffectedSpan[] = useMemo(() => {
    if (!rootCause) return [];
    return rootCause.affected_span_ids.map((id: string) => {
      const span = spans.find((candidate) => candidate.span_id === id);
      return {
        id,
        name: span?.name ?? id,
        agent: span?.agent ?? "",
        durationMs: typeof span?.duration_ms === "number" ? span.duration_ms : null,
      };
    });
  }, [rootCause, spans]);

  if (!rootCause) {
    return (
      <DashboardCard aria-label="Root cause">
        <DetailHeader title="Root cause" />
        <EmptyState
          title="No root cause identified"
          description="The gateway found no failing span on the critical path of this task."
        />
      </DashboardCard>
    );
  }

  return (
    <div
      style={{
        background: "var(--danger-bg)",
        border: "1px solid var(--danger-border)",
        borderRadius: "inherit",
      }}
    >
      <DashboardCard aria-label="Root cause" className="root-cause-panel root-cause-panel-failed">
      <DetailHeader title="Root cause">
        <StatusBadge label="heuristic hint, not a verdict" tone="warn" />
      </DetailHeader>

      <p className="root-cause-message title-lg" style={{ fontSize: "1.05rem", fontWeight: 650 }}>
        {rootCause.message}
      </p>

      <dl className="detail-grid">
        <div>
          <dt>Span</dt>
          <dd>{rootCause.span_name}</dd>
        </div>
        <div>
          <dt>Agent</dt>
          <dd>{rootCause.agent ?? "unknown"}</dd>
        </div>
        <div>
          <dt>Error type</dt>
          <dd>{rootCause.error_type ?? "unknown"}</dd>
        </div>
        <div>
          <dt>Confidence</dt>
          <dd>{rootCause.confidence}</dd>
        </div>
      </dl>

      <h3 className="sub-heading">Affected spans ({affected.length})</h3>
      {affected.length === 0 ? (
        <EmptyState title="No downstream spans affected" />
      ) : (
        <ul className="span-list" aria-label="Affected spans">
          {affected.map((span) => (
            <li key={span.id}>
              {onSelectSpan ? (
                <button
                  type="button"
                  className="button button-link span-name"
                  aria-label={`Jump to span ${span.id}`}
                  onClick={() => onSelectSpan(span.id)}
                >
                  {span.name}
                </button>
              ) : (
                <span className="span-name">{span.name}</span>
              )}
              {span.agent ? <span className="span-meta mono">{span.agent}</span> : null}
              {span.durationMs !== null ? (
                <span className="span-meta mono">{formatDuration(span.durationMs)}</span>
              ) : null}
              <code className="span-id">{span.id}</code>
            </li>
          ))}
        </ul>
      )}

      <button
        type="button"
        className="button button-link"
        aria-expanded={showDetails}
        onClick={() => setShowDetails((current) => !current)}
      >
        {showDetails ? "Hide technical details" : "Show technical details"}
      </button>
      {showDetails ? (
        <p className="root-cause-detail">
          Span <code>{rootCause.span_id}</code>
        </p>
      ) : null}
      </DashboardCard>
    </div>
  );
}
