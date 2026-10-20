import { useMemo, useState } from "react";
import type { RootCause, Span } from "../api/types";
import { EmptyState } from "./EmptyState";
import { StatusBadge } from "./StatusBadge";

export interface RootCausePanelProps {
  rootCause: RootCause | null | undefined;
  /** Spans of the task, used to name the affected spans. */
  spans?: Span[];
}

interface AffectedSpan {
  id: string;
  name: string;
  agent: string;
}

/**
 * Root-cause panel.
 *
 * The gateway derives the root cause heuristically ("earliest error in time
 * order whose descendants contain the failure"). The panel therefore labels
 * the result as a hint, never as a verdict (PRD UI-3).
 */
export function RootCausePanel({ rootCause, spans = [] }: RootCausePanelProps) {
  const [showDetails, setShowDetails] = useState(false);

  const affected: AffectedSpan[] = useMemo(() => {
    if (!rootCause) return [];
    return rootCause.affected_span_ids.map((id) => {
      const span = spans.find((candidate) => candidate.span_id === id);
      return {
        id,
        name: span?.name ?? id,
        agent: span?.agent ?? "",
      };
    });
  }, [rootCause, spans]);

  if (!rootCause) {
    return (
      <section className="panel" aria-labelledby="root-cause-heading">
        <h2 id="root-cause-heading">Root cause</h2>
        <EmptyState
          title="No root cause identified"
          description="The gateway found no failing span on the critical path of this task."
        />
      </section>
    );
  }

  return (
    <section className="panel root-cause-panel" aria-labelledby="root-cause-heading">
      <div className="panel-header">
        <h2 id="root-cause-heading">Root cause</h2>
        <StatusBadge label="heuristic hint, not a verdict" tone="warn" />
      </div>

      <p className="root-cause-message">{rootCause.message}</p>

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
              <span className="span-name">{span.name}</span>
              {span.agent ? <span className="span-meta">{span.agent}</span> : null}
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
    </section>
  );
}
