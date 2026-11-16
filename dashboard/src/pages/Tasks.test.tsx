import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
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


describe("task pagination regressions", () => {
  it("returns to page one when page two is narrowed to a one-task time range", () => {
    mockedUseTasks.mockImplementation((filters) => ({
      data: { items: filters.from === "15m" && filters.offset !== 0 ? [] : [task], total: filters.from === "15m" ? 1 : 51 },
      isPending: false, isError: false,
    }) as ReturnType<typeof useTasks>);
    render(<MemoryRouter><Tasks /></MemoryRouter>);
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Page 2 of 2")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("radio", { name: "15m" }));
    expect(screen.getByText("1 task · page 1 of 1")).toBeInTheDocument();
    expect(mockedUseTasks.mock.lastCall?.[0]).toMatchObject({ from: "15m", offset: 0 });
    expect(screen.getByRole("link", { name: "task-42" })).toBeInTheDocument();
  });
  it("requests a valid page after a live refresh shrinks the total", () => {
    mockTasks([task], 51);
    const view = render(<MemoryRouter><Tasks /></MemoryRouter>);
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Page 2 of 2")).toBeInTheDocument();
    mockTasks([], 1);
    view.rerender(<MemoryRouter><Tasks /></MemoryRouter>);
    expect(mockedUseTasks.mock.lastCall?.[0]).toMatchObject({ offset: 0 });
    mockTasks([task], 1);
    view.rerender(<MemoryRouter><Tasks /></MemoryRouter>);
    expect(screen.getByText("Page 1 of 1")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "task-42" })).toBeInTheDocument();
  });
});
