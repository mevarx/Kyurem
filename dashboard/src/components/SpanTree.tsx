import { useEffect, useMemo, useRef, useState } from "react";
import type { Span } from "../api/types";
import { formatDuration } from "../lib/format";
import { buildSpanTree, maxDuration, type SpanNode } from "../lib/spans";
import { spanTone } from "../lib/status";
import { StatusBadge } from "./StatusBadge";

export interface SpanTreeProps {
  spans: Span[];
  /** Span ids highlighted as affected by the root cause. */
  highlightSpanIds?: string[];
  /** Controlled selection; omit for uncontrolled (defaults to null). */
  selectedSpanId?: string | null;
  /** Called with the newly selected span id (or null on deselect). */
  onSelectSpan?: (spanId: string | null) => void;
}

function SpanRow({
  node,
  highlightedIds,
  selectedSpanId,
  onSelect,
  peakDuration,
}: {
  node: SpanNode;
  highlightedIds: Set<string>;
  selectedSpanId: string | null;
  onSelect: (spanId: string | null) => void;
  peakDuration: number;
}) {
  const [expanded, setExpanded] = useState(true);
  const rowRef = useRef<HTMLLIElement>(null);
  const { span } = node;
  const tone = spanTone(span);
  const attributes = Object.entries(span.attributes ?? {});
  const highlighted = highlightedIds.has(span.span_id);
  const selected = selectedSpanId === span.span_id;
  const barPercent =
    peakDuration > 0 ? Math.max(1, (Math.max(0, span.duration_ms) / peakDuration) * 100) : 0;

  useEffect(() => {
    if (selected && rowRef.current) {
      const el = rowRef.current;
      const scrollable = el.querySelector("[data-span-row]");
      const target =
        scrollable && typeof (scrollable as HTMLElement).scrollIntoView === "function"
          ? (scrollable as HTMLElement)
          : typeof el.scrollIntoView === "function"
            ? el
            : null;
      target?.scrollIntoView({ block: "nearest" });
    }
  }, [selected]);

  return (
    <li
      ref={rowRef}
      className={`span-node${highlighted ? " is-highlighted" : ""}${
        selected ? " is-selected" : ""
      }`}
    >
      <div data-span-row className="span-node-row" style={selected ? { outline: "2px solid var(--ring)", outlineOffset: "1px", borderRadius: "8px" } : undefined}>
        <button
          type="button"
          className="span-toggle"
          aria-expanded={expanded}
          aria-label={expanded ? `Collapse span ${span.name}` : `Expand span ${span.name}`}
          onClick={(event) => {
            event.stopPropagation();
            setExpanded((current) => !current);
          }}
          onKeyDown={(event) => event.stopPropagation()}
          disabled={node.children.length === 0}
        >
          <span aria-hidden="true">{node.children.length === 0 ? "·" : expanded ? "▾" : "▸"}</span>
        </button>
        <button
          type="button"
          className="span-node-name"
          aria-label={highlighted ? `Select span ${span.name}, affected by root cause` : `Select span ${span.name}`}
          aria-pressed={selected}
          title={span.name}
          onClick={() => onSelect(selected ? null : span.span_id)}
          style={{
            background: "none",
            border: "none",
            padding: 0,
            cursor: "pointer",
            font: "inherit",
            textAlign: "left",
          }}
        >
          {span.name}
        </button>
        <span className="span-node-agent">{span.agent ?? "unknown"}</span>
        <StatusBadge label={span.status_code ?? span.error_type ?? "unknown"} tone={tone} />
        <span className="span-node-duration">{formatDuration(span.duration_ms)}</span>
        <span
          className="span-duration-track"
          aria-hidden="true"
          style={{
            display: "block",
            flexBasis: "100%",
            height: "4px",
            borderRadius: "2px",
            background: "var(--surface-sunken)",
            overflow: "hidden",
          }}
        >
          <span
            className={`span-duration-bar span-duration-bar-${tone}`}
            style={{
              display: "block",
              height: "100%",
              width: `${barPercent}%`,
              borderRadius: "2px",
              background:
                tone === "bad"
                  ? "var(--danger)"
                  : tone === "warn"
                    ? "var(--warning)"
                    : tone === "good"
                      ? "var(--success)"
                      : tone === "info"
                        ? "var(--info)"
                        : "var(--muted-foreground)",
            }}
          />
        </span>
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
                  <dd title={value}>
                    {value.length > 120 ? `${value.slice(0, 120)}…` : value}
                  </dd>
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
            <SpanRow
              key={child.span.span_id}
              node={child}
              highlightedIds={highlightedIds}
              selectedSpanId={selectedSpanId}
              onSelect={onSelect}
              peakDuration={peakDuration}
            />
          ))}
        </ul>
      ) : null}
    </li>
  );
}

/** Expandable tree of the spans of a task. */
export function SpanTree({ spans, highlightSpanIds = [], selectedSpanId, onSelectSpan }: SpanTreeProps) {
  const roots = useMemo(() => buildSpanTree(spans), [spans]);
  const highlighted = useMemo(() => new Set(highlightSpanIds), [highlightSpanIds]);
  const peak = useMemo(() => maxDuration(spans), [spans]);
  const [internalSelection, setInternalSelection] = useState<string | null>(null);
  const selected = selectedSpanId !== undefined ? selectedSpanId : internalSelection;

  const handleSelect = (spanId: string | null) => {
    if (selectedSpanId === undefined) setInternalSelection(spanId);
    onSelectSpan?.(spanId);
  };

  if (spans.length === 0) {
    return <p className="muted">This task has no spans.</p>;
  }

  return (
    <ul className="span-tree">
      {roots.map((node) => (
        <SpanRow
          key={node.span.span_id}
          node={node}
          highlightedIds={highlighted}
          selectedSpanId={selected}
          onSelect={handleSelect}
          peakDuration={peak}
        />
      ))}
    </ul>
  );
}
