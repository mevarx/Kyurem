import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "../App";
import { ThemeProvider } from "../lib/ThemeProvider";

const MISSING_TASK_PATH = "/api/v1/tasks/missing-task";
const NOT_FOUND_BODY = { error: { code: "not_found", message: "task not found" } };

const overview = {
  tasks_total: 0,
  tasks_succeeded: 0,
  tasks_failed: 0,
  error_rate: 0,
  p50_task_duration_ms: 0,
  p95_task_duration_ms: 0,
  total_cost_usd: null,
  active_agents: 0,
  spans_total: 0,
  tasks_over_time: [],
  cost_over_time: [],
  top_agents_by_cost: [],
  top_failing_agents: [],
  recent_failed_tasks: [],
};

const taskPage = { items: [], total: 0, limit: 50, offset: 0 };

const graph = { nodes: [], edges: [] };

const agents = { items: [] };

const costs = {
  group_by: "agent",
  total_usd: null,
  spans: 0,
  unknown_spans: 0,
  buckets: [],
};

const settings = {
  version: "0.3.0",
  spec_version: "1.0.0",
  service_name: "kyurem-gateway",
  agents: 0,
  telemetry: {
    exporting: false,
    queue_depth: 0,
    queue_capacity: 2048,
    spans_ended: 0,
    spans_exported: 0,
    spans_dropped: {},
  },
  storage: { driver: "memory" },
};

function bodyFor(path: string): unknown {
  if (path.startsWith("/api/v1/overview")) return overview;
  if (path.startsWith("/api/v1/tasks")) return taskPage;
  if (path.startsWith("/api/v1/graph")) return graph;
  if (path.startsWith("/api/v1/agents")) return agents;
  if (path.startsWith("/api/v1/costs")) return costs;
  if (path.startsWith("/api/v1/settings")) return settings;
  return { error: { code: "not_found", message: `unknown path ${path}` } };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function pathOf(input: RequestInfo | URL): string {
  const url = typeof input === "string" ? input : input.toString();
  return url.split("?")[0];
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

describe("task detail not-found handling", () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
  });

  it("shows a task-not-found state for a 404 instead of the gateway-offline panel", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = pathOf(input);
        if (path.endsWith(MISSING_TASK_PATH)) return jsonResponse(NOT_FOUND_BODY, 404);
        return jsonResponse(bodyFor(path));
      }),
    );

    renderAt("/tasks/missing-task");

    expect(await screen.findByText("Task not found")).toBeInTheDocument();
    expect(screen.getByText(/missing-task/)).toBeInTheDocument();
    expect(screen.queryByText(/offline/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/check gateway URL/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
  });

  it("shows the gateway-offline panel when the request itself fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = pathOf(input);
        if (path.endsWith(MISSING_TASK_PATH)) throw new TypeError("Failed to fetch");
        return jsonResponse(bodyFor(path));
      }),
    );

    renderAt("/tasks/missing-task");

    expect(await screen.findByText("Could not load task")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    expect(screen.getByText(/check gateway URL/i)).toBeInTheDocument();
    expect(screen.queryByText("Task not found")).not.toBeInTheDocument();
  });
});
