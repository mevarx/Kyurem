import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "../App";
import { ThemeProvider } from "../lib/ThemeProvider";

const API_TOKEN_STORAGE_KEY = "kyurem.apiToken";

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
  recent_failed_tasks: [],
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

function renderSettings() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 5_000 } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ThemeProvider>
        <MemoryRouter initialEntries={["/settings"]}>
          <App />
        </MemoryRouter>
      </ThemeProvider>
    </QueryClientProvider>,
  );
}

describe("settings API token", () => {
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

  it("hides the token by default and toggles its visibility", async () => {
    renderSettings();

    // Theme is controlled in the global top bar, so Settings must not render a second switch.
    expect(screen.getAllByRole("button", { name: /theme.*switch to/i })).toHaveLength(1);

    const input = await screen.findByLabelText(/API token/i);
    expect(input).toHaveAttribute("type", "password");

    fireEvent.click(screen.getByRole("button", { name: "Show token" }));

    expect(screen.getByLabelText(/API token/i)).toHaveAttribute("type", "text");
    const hideButton = screen.getByRole("button", { name: "Hide token" });
    expect(hideButton).toHaveAttribute("aria-pressed", "true");
    expect(screen.queryByRole("button", { name: "Show token" })).not.toBeInTheDocument();

    fireEvent.click(hideButton);

    expect(screen.getByLabelText(/API token/i)).toHaveAttribute("type", "password");
    expect(screen.getByRole("button", { name: "Show token" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });

  it("saves the typed token to localStorage", async () => {
    renderSettings();

    const input = await screen.findByLabelText(/API token/i);
    fireEvent.change(input, { target: { value: "sk-test-123" } });
    expect(input).toHaveValue("sk-test-123");

    fireEvent.click(screen.getByRole("button", { name: "Save token" }));

    expect(window.localStorage.getItem(API_TOKEN_STORAGE_KEY)).toBe("sk-test-123");
    expect(await screen.findByText("Token saved.")).toBeInTheDocument();
  });
});
