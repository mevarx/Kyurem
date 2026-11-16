import type { Span } from "../api/types";

export interface SpanNode {
  span: Span;
  children: SpanNode[];
  depth: number;
}

/** Build a span tree from the flat span list using `parent_span_id`. */
export function buildSpanTree(spans: Span[]): SpanNode[] {
  const nodes = new Map<string, SpanNode>();
  for (const span of spans) {
    nodes.set(span.span_id, { span, children: [], depth: 0 });
  }

  const roots: SpanNode[] = [];
  for (const span of spans) {
    const node = nodes.get(span.span_id);
    if (!node) continue;
    const parentId = span.parent_span_id;
    const parent = parentId ? nodes.get(parentId) : undefined;
    if (parent && parent.span.span_id !== span.span_id) {
      parent.children.push(node);
    } else {
      roots.push(node);
    }
  }

  const assignDepth = (node: SpanNode, depth: number): void => {
    node.depth = depth;
    for (const child of node.children) assignDepth(child, depth + 1);
  };
  for (const root of roots) assignDepth(root, 0);

  return roots;
}

/** Epoch-millis start of a span. Falls back to 0 for unparseable input. */
export function spanStartMs(span: Span): number {
  const start = new Date(span.started_at).getTime();
  return Number.isFinite(start) ? start : 0;
}

/** Epoch-millis end of a span (start + duration). */
export function spanEndMs(span: Span): number {
  return spanStartMs(span) + Math.max(0, span.duration_ms);
}

export interface TimeWindow {
  min: number;
  max: number;
}

/**
 * Task window covering every span: earliest start to latest end
 * (epoch millis). Empty input yields `{ min: 0, max: 0 }`.
 */
export function timeWindow(spans: Span[]): TimeWindow {
  if (spans.length === 0) return { min: 0, max: 0 };
  let min = Infinity;
  let max = -Infinity;
  for (const span of spans) {
    const start = spanStartMs(span);
    const end = spanEndMs(span);
    if (start < min) min = start;
    if (end > max) max = end;
  }
  return { min, max };
}

/** Largest `duration_ms` in the list, or 0 when empty. */
export function maxDuration(spans: Span[]): number {
  let max = 0;
  for (const span of spans) {
    if (span.duration_ms > max) max = span.duration_ms;
  }
  return max;
}

/** Pre-order flatten of a span forest. */
export function flattenSpanTree(roots: SpanNode[]): SpanNode[] {
  const out: SpanNode[] = [];
  const visit = (node: SpanNode): void => {
    out.push(node);
    for (const child of node.children) visit(child);
  };
  for (const root of roots) visit(root);
  return out;
}

export interface DepthStats {
  maxDepth: number;
  nodeCount: number;
}

/** Depth statistics for a span forest: deepest level + total node count. */
export function depthStats(roots: SpanNode[]): DepthStats {
  let maxDepth = 0;
  let nodeCount = 0;
  for (const node of flattenSpanTree(roots)) {
    nodeCount += 1;
    if (node.depth > maxDepth) maxDepth = node.depth;
  }
  return { maxDepth, nodeCount };
}
