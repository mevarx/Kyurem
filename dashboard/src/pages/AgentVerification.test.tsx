import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { useAgents, useGraph, useTasks } from "../api/hooks";
import { ApiError } from "../api/client";
import { Agents } from "./Agents";
import { AgentGraph } from "./AgentGraph";
import type { AgentInfo } from "../api/types";

vi.mock("../api/hooks", () => ({ useAgents: vi.fn(), useGraph: vi.fn(), useTasks: vi.fn() }));
vi.mock("../lib/useTheme", () => ({ useTheme: () => ({ theme: "dark" }) }));
// Render the real node component without React Flow's browser measurement machinery.
vi.mock("@xyflow/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@xyflow/react")>();
  return { ...actual, Handle: () => null, Background: () => null, Controls: () => null,
    ReactFlow: ({ nodes, nodeTypes, onNodeClick }: {
      nodes: Array<{ id: string; data: object }>;
      nodeTypes: { agent: React.ComponentType<{ data: object; selected: boolean }> };
      onNodeClick: (event: unknown, node: { id: string }) => void;
    }) => <div data-testid="graph">{nodes.map(node => <button key={node.id} onClick={() => onNodeClick({}, node)}>
      <nodeTypes.agent data={node.data} selected={false} />
    </button>)}</div>,
  };
});

const agent = { name: "echo", tags: [], card_version: "1", skills: [], verification_mode: "off", aliases: [], upstreams: [], card_status: "ok", verification: "unchecked",
  spans: 3, errors: 0, error_rate: 0, p95_ms: 10, cost_usd: null, last_seen: null } as AgentInfo;
const refetch = vi.fn();
function result(data: unknown, error: Error | null = null, isPending = false) {
  return { data, error, isError: !!error, isPending, refetch };
}
function setAgents(verification = "unchecked", error: Error | null = null, pending = false) {
  vi.mocked(useAgents).mockReturnValue(result({ items: [{ ...agent, verification }] }, error, pending) as unknown as ReturnType<typeof useAgents>);
}
function setErrors(error: Error | null = null, pending = false) {
  vi.mocked(useTasks).mockReturnValue(result({ items: [] }, error, pending) as unknown as ReturnType<typeof useTasks>);
}
function graph() {
  return render(<MemoryRouter><AgentGraph /></MemoryRouter>);
}
function selectAgent() { fireEvent.click(screen.getByTestId("graph").querySelector("button")!); }

beforeEach(() => {
  refetch.mockReset();
  setAgents(); setErrors();
  vi.mocked(useGraph).mockReturnValue(result({ nodes: [{ name: "echo", calls: 3, errors: 0, error_rate: 0, p95_ms: 10 }], edges: [] }) as unknown as ReturnType<typeof useGraph>);
});

describe("verification badges in affected views", () => {
  it.each([
    ["verified", "good"], ["unverified", "bad"], ["unsigned", "warn"],
    ["unchecked", "neutral"], ["unknown", "neutral"], ["checked", "neutral"], ["future-status", "neutral"],
  ])("renders %s as %s in the table, graph node and inspector", (status, tone) => {
    setAgents(status);
    const table = render(<MemoryRouter><Agents /></MemoryRouter>);
    expect(screen.getByTitle("Card signature verification")).toHaveClass(`badge-${tone}`);
    table.unmount();
    graph();
    expect(screen.getByText(`verify: ${status}`)).toHaveClass(`badge-${tone}`);
    selectAgent();
    expect(screen.getByText(`verification: ${status}`)).toHaveClass(`badge-${tone}`);
  });
});

describe("graph secondary requests", () => {
  it("distinguishes loading from a successful empty response", () => {
    setErrors(null, true);
    const view = graph(); selectAgent();
    expect(screen.getByText("Loading errors…")).toBeInTheDocument();
    expect(screen.queryByText("No recent errors")).not.toBeInTheDocument();
    setErrors();
    view.rerender(<MemoryRouter><AgentGraph /></MemoryRouter>);
    expect(screen.getByText("No recent errors")).toBeInTheDocument();
  });
  it("shows a failed recent-errors lookup with retry while keeping the graph usable", () => {
    setErrors(new ApiError(500, "storage lookup failed", "internal"));
    const view = graph(); selectAgent();
    expect(screen.getByRole("alert")).toHaveTextContent("Could not load recent errors");
    expect(screen.getByRole("alert")).toHaveTextContent("storage lookup failed");
    expect(screen.queryByText("No recent errors")).not.toBeInTheDocument();
    expect(screen.getByTestId("graph")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(refetch).toHaveBeenCalledOnce();
    setErrors(); view.rerender(<MemoryRouter><AgentGraph /></MemoryRouter>);
    expect(screen.getByText("No recent errors")).toBeInTheDocument();
  });
  it("communicates failed metadata with retry instead of implying known empty metadata", () => {
    setAgents("unchecked", new ApiError(500, "registry unavailable"));
    graph();
    expect(screen.getByRole("alert")).toHaveTextContent("Could not load agent metadata");
    selectAgent();
    expect(screen.queryByText("No registry metadata returned for this agent.")).not.toBeInTheDocument();
    expect(screen.getByTestId("graph")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(refetch).toHaveBeenCalledOnce();
  });
});
