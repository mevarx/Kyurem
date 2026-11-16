import { useState } from "react";
import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import type { Span } from "../api/types";
import { SpanTree } from "./SpanTree";
import { SpanWaterfall } from "./SpanWaterfall";

const spans: Span[] = [
  {
    trace_id: "trace-1",
    span_id: "span-parent",
    name: "orchestrator run",
    kind: "server",
    agent: "orchestrator",
    started_at: "2024-05-01T10:00:00Z",
    duration_ms: 4_000,
    status_code: "OK",
    attributes: {},
  },
  {
    trace_id: "trace-1",
    span_id: "span-child",
    parent_span_id: "span-parent",
    name: "fetch weather",
    kind: "client",
    agent: "weather",
    started_at: "2024-05-01T10:00:01Z",
    duration_ms: 1_500,
    status_code: "OK",
    attributes: {},
  },
];

/** Tree and waterfall sharing one selection, exactly as the detail page wires them. */
function SpanSelectionHarness() {
  const [selected, setSelected] = useState<string | null>(null);
  return (
    <div>
      <div data-testid="span-tree">
        <SpanTree spans={spans} selectedSpanId={selected} onSelectSpan={setSelected} />
      </div>
      <div data-testid="waterfall">
        <SpanWaterfall spans={spans} selectedSpanId={selected} onSelectSpan={setSelected} />
      </div>
    </div>
  );
}

function treeButton(scope: HTMLElement, spanName: string): HTMLElement {
  return within(scope).getByRole("button", { name: `Select span ${spanName}` });
}

function waterfallBar(scope: HTMLElement, spanName: string): HTMLElement {
  return within(scope).getByRole("button", { name: new RegExp(`^Select span ${spanName},`) });
}

describe("span selection across tree and waterfall", () => {
  it("selecting a waterfall bar marks the matching span in the tree", () => {
    render(<SpanSelectionHarness />);
    const tree = screen.getByTestId("span-tree");
    const waterfall = screen.getByTestId("waterfall");

    expect(treeButton(tree, "fetch weather")).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(waterfallBar(waterfall, "fetch weather"));

    expect(treeButton(tree, "fetch weather")).toHaveAttribute("aria-pressed", "true");
    expect(treeButton(tree, "orchestrator run")).toHaveAttribute("aria-pressed", "false");
    expect(waterfallBar(waterfall, "fetch weather")).toHaveAttribute("aria-pressed", "true");
    expect(waterfallBar(waterfall, "orchestrator run")).toHaveAttribute("aria-pressed", "false");
  });

  it("selecting a different waterfall bar moves the selection", () => {
    render(<SpanSelectionHarness />);
    const tree = screen.getByTestId("span-tree");
    const waterfall = screen.getByTestId("waterfall");

    fireEvent.click(waterfallBar(waterfall, "fetch weather"));
    fireEvent.click(waterfallBar(waterfall, "orchestrator run"));

    expect(treeButton(tree, "fetch weather")).toHaveAttribute("aria-pressed", "false");
    expect(treeButton(tree, "orchestrator run")).toHaveAttribute("aria-pressed", "true");
    expect(waterfallBar(waterfall, "fetch weather")).toHaveAttribute("aria-pressed", "false");
    expect(waterfallBar(waterfall, "orchestrator run")).toHaveAttribute("aria-pressed", "true");
  });

  it("selecting a span in the tree drives the waterfall selection", () => {
    render(<SpanSelectionHarness />);
    const tree = screen.getByTestId("span-tree");
    const waterfall = screen.getByTestId("waterfall");

    fireEvent.click(waterfallBar(waterfall, "fetch weather"));
    fireEvent.click(treeButton(tree, "orchestrator run"));

    expect(waterfallBar(waterfall, "fetch weather")).toHaveAttribute("aria-pressed", "false");
    expect(waterfallBar(waterfall, "orchestrator run")).toHaveAttribute("aria-pressed", "true");
    expect(treeButton(tree, "orchestrator run")).toHaveAttribute("aria-pressed", "true");
    expect(treeButton(tree, "fetch weather")).toHaveAttribute("aria-pressed", "false");
  });
});
