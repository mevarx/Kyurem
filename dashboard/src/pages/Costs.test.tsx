import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { useCosts, useOverview } from "../api/hooks";
import { Costs } from "./Costs";

vi.mock("../api/hooks", () => ({ useCosts: vi.fn(), useOverview: vi.fn() }));

const mockedUseCosts = vi.mocked(useCosts);
const mockedUseOverview = vi.mocked(useOverview);

const report = {
  group_by: "agent" as const,
  total_usd: 0.72,
  spans: 12,
  unknown_spans: 0,
  input_tokens: 80,
  output_tokens: 40,
  buckets: [{ key: "echo", cost_usd: 0.72, spans: 12, unknown_spans: 0, input_tokens: 80, output_tokens: 40 }],
};

describe("Costs header controls", () => {
  beforeEach(() => {
    mockedUseCosts.mockReset();
    mockedUseCosts.mockReturnValue({ data: report, isPending: false, isError: false } as unknown as ReturnType<typeof useCosts>);
    mockedUseOverview.mockReturnValue({ data: { cost_over_time: [] }, isPending: false, isError: false } as unknown as ReturnType<typeof useOverview>);
  });

  it("keeps the real grouping selector beside the time range and export action", () => {
    render(<MemoryRouter><Costs /></MemoryRouter>);

    const headerControls = screen.getByLabelText("Group by").closest(".costs-page-controls");
    expect(headerControls).not.toBeNull();
    expect(headerControls).toContainElement(screen.getByRole("radio", { name: "24h" }));
    expect(headerControls).toContainElement(screen.getByRole("button", { name: "Export CSV" }));
    expect(screen.queryByText("grouped by agent")).not.toBeInTheDocument();
  });

  it("refetches the report when grouping changes", () => {
    render(<MemoryRouter><Costs /></MemoryRouter>);

    fireEvent.change(screen.getByLabelText("Group by"), { target: { value: "task" } });

    expect(mockedUseCosts.mock.lastCall?.[1]).toBe("task");
  });
});
