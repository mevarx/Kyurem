import { useMemo, useState } from "react";
import type { Span } from "../api/types";
import { formatDuration } from "../lib/format";
import { buildSpanTree, type SpanNode } from "../lib/spans";
import { spanTone } from "../lib/status";
import { StatusBadge } from "./StatusBadge";

export interface SpanTreeProps {
  spans: Span[];
  /** Span ids highlighted as affected by the root cause. */
  highlightSpanIds?: string[];
}

function SpanRow({
  node,
  highlightedIds,
}: {
  node: SpanNode;
  highlightedIds: Set<string>;
}) {
  const [expanded, setExpanded] = useState(true);
  const { span } = node;
  const tone = spanTone(span);
  const attributes = Object.entries(span.attributes ?? {});
  const highlighted = highlightedIds.has(span.span_id);

  return (
    <li className={`span-node${highlighted ? " is-highlighted" : ""}`}>
      <div className="span-node-row">
        <button
          type="button"
          className="span-toggle"
          aria-expanded={expanded}
          aria-label={expanded ? `Collapse span ${span.name}` : `Expand span ${span.name}`}
          onClick={() => setExpanded((current) => !current)}
          disabled={node.children.length === 0}
        >
          <span aria-hidden="true">{node.children.length === 0 ? "·" : expanded ? "▾" : "▸"}</span>
        </button>
        <span className="span-node-name">{span.name}</span>
        <span className="span-node-agent">{span.agent ?? "unknown"}</span>
        <StatusBadge label={span.status_code ?? span.error_type ?? "unknown"} tone={tone} />
        <span className="span-node-duration">{formatDuration(span.duration_ms)}</span>
      </div>

      {expanded ? (
        <div className="span-node-body">
          <dl className="detail-grid detail-grid-tight">
            <div>
              <dt>Span id</dt>
              <dd>
                <code>{span.span_id}</code>
              </dd>
            </div>
            <div>
              <dt>Kind</dt>
              <dd>{span.kind}</dd>
            </div>
            <div>
              <dt>Caller</dt>
              <dd>{span.caller ?? "unknown"}</dd>
            </div>
            <div>
              <dt>Service</dt>
              <dd>{span.service_name ?? "unknown"}</dd>
            </div>
          </dl>
          {span.status_message ? (
            <p className="span-status-message">{span.status_message}</p>
          ) : null}
          {attributes.length > 0 ? (
            <dl className="attribute-list">
              {attributes.map(([key, value]) => (
                <div key={key}>
                  <dt>{key}</dt>
                  <dd>{value}</dd>
                </div>
              ))}
            </dl>
          ) : (
            <p className="muted">No span attributes.</p>
          )}
        </div>
      ) : null}

      {expanded && node.children.length > 0 ? (
        <ul className="span-children">
          {node.children.map((child) => (
            <SpanRow key={child.span.span_id} node={child} highlightedIds={highlightedIds} />
          ))}
        </ul>
      ) : null}
    </li>
  );
}

/** Expandable tree of the spans of a task. */
export function SpanTree({ spans, highlightSpanIds = [] }: SpanTreeProps) {
  const roots = useMemo(() => buildSpanTree(spans), [spans]);
  const highlighted = useMemo(() => new Set(highlightSpanIds), [highlightSpanIds]);

  if (spans.length === 0) {
    return <p className="muted">This task has no spans.</p>;
  }

  return (
    <ul className="span-tree">
      {roots.map((node) => (
        <SpanRow key={node.span.span_id} node={node} highlightedIds={highlighted} />
      ))}
    </ul>
  );
}
