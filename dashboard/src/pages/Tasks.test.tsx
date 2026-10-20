import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { Task } from "../api/types";
import { useTasks } from "../api/hooks";
import { Tasks } from "./Tasks";

vi.mock("../api/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/hooks")>();
  return { ...actual, useTasks: vi.fn() };
});

const mockedUseTasks = vi.mocked(useTasks);

const task: Task = {
  task_id: "task-42",
  trace_id: "trace-42",
  status: "failed",
  started_at: "2024-05-01T10:00:00Z",
  duration_ms: 1_250,
  agents: ["planner", "travel"],
  root_caller: "planner",
  cost_usd: null,
  input_tokens: null,
  output_tokens: null,
  tokens_known: false,
  span_count: 4,
  error_type: "timeout",
  error_message: "planner did not answer",
};

function mockTasks(items: Task[], total = items.length) {
  mockedUseTasks.mockReturnValue({
    data: { items, total, limit: 50, offset: 0 },
    isPending: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useTasks>);
}

describe("Tasks page", () => {
  beforeEach(() => {
    mockedUseTasks.mockReset();
  });

  it("renders the rows returned by the query hook", () => {
    mockTasks([task]);

    render(
      <MemoryRouter>
        <Tasks />
      </MemoryRouter>,
    );

    expect(screen.getByRole("link", { name: "task-42" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "planner" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "failed" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "1.25 s" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "unknown" })).toBeInTheDocument();
  });

  it("shows the empty state when there are no tasks", () => {
    mockTasks([]);

    render(
      <MemoryRouter>
        <Tasks />
      </MemoryRouter>,
    );

    expect(screen.getByText(/no tasks match these filters/i)).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "task-42" })).not.toBeInTheDocument();
  });

  it("shows the error state when the query fails", () => {
    mockedUseTasks.mockReturnValue({
      data: undefined,
      isPending: false,
      isError: true,
      error: new Error("gateway is down"),
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useTasks>);

    render(
      <MemoryRouter>
        <Tasks />
      </MemoryRouter>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("gateway is down");
  });
});
