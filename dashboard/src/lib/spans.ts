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
