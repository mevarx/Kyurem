import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "./App";
import { ThemeProvider } from "./lib/ThemeProvider";

const overview = {
  tasks_total: 128,
  tasks_succeeded: 110,
  tasks_failed: 14,
  error_rate: 0.1094,
  p50_task_duration_ms: 1420,
  p95_task_duration_ms: 30250,
  total_cost_usd: null,
  active_agents: 3,
  spans_total: 512,
  tasks_over_time: [{ bucket: "2024-05-01T11:00:00Z", succeeded: 20, failed: 2, unknown: 0, spans: 88 }],
  cost_over_time: [{ bucket: "2024-05-01T11:00:00Z", succeeded: 0, failed: 0, unknown: 0, spans: 88 }],
  top_agents_by_cost: [{ key: "travel", cost_usd: 0.81, spans: 120, unknown_spans: 4 }],
  top_failing_agents: [{ name: "travel", errors: 12, error_rate: 0.1 }],
  recent_failed_tasks: [
    {
      task_id: "task-1001",
      trace_id: "trace-1001",
      status: "failed",
      started_at: "2024-05-01T11:57:00Z",
      duration_ms: 30250,
      agents: ["planner", "travel"],
      root_caller: "planner",
      cost_usd: 0.0432,
      tokens_known: true,
      span_count: 5,
    },
  ],
};

const taskPage = {
  items: [
    {
      task_id: "task-1001",
      trace_id: "trace-1001",
      status: "failed",
      started_at: "2024-05-01T11:57:00Z",
      duration_ms: 30250,
      agents: ["planner", "travel"],
      root_caller: "planner",
      cost_usd: 0.0432,
      tokens_known: true,
      span_count: 5,
    },
  ],
  total: 1,
  limit: 50,
  offset: 0,
};

const taskDetail = {
  task: taskPage.items[0],
  spans: [
    {
      trace_id: "trace-1001",
      span_id: "s1",
      name: "planner: plan trip",
      kind: "server",
      agent: "planner",
      started_at: "2024-05-01T11:57:00Z",
      duration_ms: 30250,
      status_code: "ERROR",
      attributes: {},
    },
  ],
  root_cause: {
    span_id: "s1",
    span_name: "planner: plan trip",
    agent: "planner",
    error_type: "timeout",
    message: "The planner did not answer within 30s.",
    affected_span_ids: ["s1"],
    confidence: "heuristic",
  },
  cost: {
    group_by: "agent",
    total_usd: 0.0432,
    spans: 1,
    unknown_spans: 0,
    buckets: [{ key: "planner", cost_usd: 0.0432, spans: 1, unknown_spans: 0 }],
  },
  events: [],
};

const graph = {
  nodes: [{ name: "planner", calls: 128, errors: 2, error_rate: 0.0156, p50_ms: 900, p95_ms: 30250 }],
  edges: [],
};

const agents = {
  items: [
    {
      name: "planner",
      aliases: ["trip-planner"],
      upstreams: ["http://planner:9000"],
      card_status: "ok",
      verification: "unchecked",
      last_seen: "2024-05-01T11:57:00Z",
      spans: 300,
      errors: 2,
      error_rate: 0.0067,
      p95_ms: 30250,
      cost_usd: 0.22,
    },
  ],
};

const costs = {
  group_by: "agent",
  total_usd: 0.22,
  spans: 300,
  unknown_spans: 0,
  buckets: [{ key: "planner", cost_usd: 0.22, spans: 300, unknown_spans: 0 }],
};

const settings = {
  version: "0.3.0",
  spec_version: "1.0.0",
  service_name: "kyurem-gateway",
  agents: 1,
  telemetry: {
    exporting: true,
    queue_depth: 12,
    queue_capacity: 2048,
    spans_ended: 512,
    spans_exported: 508,
    spans_dropped: {},
  },
  storage: { driver: "memory" },
};

function responseFor(path: string): unknown {
  if (path.startsWith("/api/v1/overview")) return overview;
  if (path.startsWith("/api/v1/tasks/")) return taskDetail;
  if (path.startsWith("/api/v1/tasks")) return taskPage;
  if (path.startsWith("/api/v1/graph")) return graph;
  if (path.startsWith("/api/v1/agents")) return agents;
  if (path.startsWith("/api/v1/costs")) return costs;
  if (path.startsWith("/api/v1/settings")) return settings;
  return { error: { code: "not_found", message: `unknown path ${path}` } };
}

function renderAt(path: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 5_000 } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ThemeProvider>
        <MemoryRouter initialEntries={[path]}>
          <App />
        </MemoryRouter>
      </ThemeProvider>
    </QueryClientProvider>,
  );
}

describe("dashboard routes", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : input.toString();
        const path = url.split("?")[0];
        return new Response(JSON.stringify(responseFor(path)), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }),
    );
  });

  it("renders the overview", async () => {
    renderAt("/");
    expect(await screen.findByRole("heading", { name: "Overview" })).toBeInTheDocument();
    expect(await screen.findByText("128")).toBeInTheDocument();
    expect(await screen.findByText("unknown")).toBeInTheDocument();
  });

  it("renders the task list", async () => {
    renderAt("/tasks");
    expect(await screen.findByRole("link", { name: "task-1001" })).toBeInTheDocument();
  });

  it("renders the task detail", async () => {
    renderAt("/tasks/task-1001");
    expect(await screen.findByRole("heading", { name: "task-1001" })).toBeInTheDocument();
    expect(await screen.findByText(/heuristic hint, not a verdict/i)).toBeInTheDocument();
  });

  it("renders the agents registry", async () => {
    renderAt("/agents");
    expect(await screen.findByText("trip-planner")).toBeInTheDocument();
  });

  it("renders the cost report", async () => {
    renderAt("/costs");
    expect(await screen.findByRole("heading", { name: /Cost per agent/i })).toBeInTheDocument();
  });

  it("renders the agent graph", async () => {
    renderAt("/graph");
    expect(await screen.findByRole("heading", { name: "Agent graph" })).toBeInTheDocument();
    expect(await screen.findByRole("group", { name: "Agent call graph" })).toBeInTheDocument();
  });

  it("renders the settings page", async () => {
    renderAt("/settings");
    expect(await screen.findByText("kyurem-gateway")).toBeInTheDocument();
    expect(await screen.findByLabelText(/API token/i)).toBeInTheDocument();
  });
});
