import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "../App";
import { ThemeProvider } from "../lib/ThemeProvider";

const SIDEBAR_STORAGE_KEY = "kyurem.sidebar";

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

describe("sidebar compact toggle", () => {
  beforeEach(() => {
    window.localStorage.clear();
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

  it("collapses and expands the sidebar, persisting the choice", () => {
    renderAt("/");

    const desktopToggle = screen.getByRole("button", { name: "Toggle sidebar" });
    const mobileTrigger = screen.getByRole("button", { name: "Open navigation" });

    expect(desktopToggle).toHaveAttribute("aria-expanded", "true");
    expect(mobileTrigger).toHaveAttribute("aria-expanded", "false");

    fireEvent.click(desktopToggle);

    expect(screen.getByRole("button", { name: "Toggle sidebar" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(window.localStorage.getItem(SIDEBAR_STORAGE_KEY)).toBe("collapsed");
    // The mobile trigger is a separate control and must not follow the desktop state.
    expect(screen.getByRole("button", { name: "Open navigation" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );

    fireEvent.click(screen.getByRole("button", { name: "Toggle sidebar" }));

    expect(screen.getByRole("button", { name: "Toggle sidebar" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    expect(window.localStorage.getItem(SIDEBAR_STORAGE_KEY)).toBe("expanded");
  });
});
