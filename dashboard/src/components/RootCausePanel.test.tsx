import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import type { RootCause, Span } from "../api/types";
import { RootCausePanel } from "./RootCausePanel";

const rootCause: RootCause = {
  span_id: "span-1",
  span_name: "call planner",
  agent: "planner",
  error_type: "timeout",
  message: "The planner did not answer within 30s, so the caller gave up.",
  affected_span_ids: ["span-2", "span-3"],
  confidence: "heuristic",
};

const spans: Span[] = [
  {
    trace_id: "trace-1",
    span_id: "span-1",
    name: "call planner",
    kind: "client",
    agent: "planner",
    started_at: "2024-05-01T10:00:00Z",
    duration_ms: 30_000,
    attributes: {},
  },
  {
    trace_id: "trace-1",
    span_id: "span-2",
    name: "fetch itinerary",
    kind: "client",
    agent: "travel",
    started_at: "2024-05-01T10:00:01Z",
    duration_ms: 1_000,
    attributes: {},
  },
  {
    trace_id: "trace-1",
    span_id: "span-3",
    name: "book hotel",
    kind: "client",
    agent: "booking",
    started_at: "2024-05-01T10:00:02Z",
    duration_ms: 1_000,
    attributes: {},
  },
];

describe("RootCausePanel", () => {
  it("renders the heuristic label, the message and the affected spans", () => {
    render(<RootCausePanel rootCause={rootCause} spans={spans} />);

    expect(screen.getByText(/heuristic hint, not a verdict/i)).toBeInTheDocument();
    expect(
      screen.getByText("The planner did not answer within 30s, so the caller gave up."),
    ).toBeInTheDocument();

    expect(screen.getByText("fetch itinerary")).toBeInTheDocument();
    expect(screen.getByText("book hotel")).toBeInTheDocument();
    expect(screen.getByText("Affected spans (2)")).toBeInTheDocument();
  });

  it("falls back to the span id when the span is unknown", () => {
    render(<RootCausePanel rootCause={{ ...rootCause, affected_span_ids: ["span-404"] }} />);
    expect(screen.getByText("span-404", { selector: "code" })).toBeInTheDocument();
  });

  it("renders an empty state when there is no root cause", () => {
    render(<RootCausePanel rootCause={null} />);
    expect(screen.getByText(/no root cause identified/i)).toBeInTheDocument();
    expect(screen.queryByText(/heuristic hint, not a verdict/i)).not.toBeInTheDocument();
  });
});
