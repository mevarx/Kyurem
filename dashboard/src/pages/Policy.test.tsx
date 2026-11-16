import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { useDecisions, usePolicy } from "../api/hooks";
import { Policy } from "./Policy";

vi.mock("../api/hooks", () => ({ useDecisions: vi.fn(), usePolicy: vi.fn() }));

const mockedUseDecisions = vi.mocked(useDecisions);
const mockedUsePolicy = vi.mocked(usePolicy);

describe("Policy review actions", () => {
  beforeEach(() => {
    mockedUsePolicy.mockReturnValue({
      data: { mode: "warn", default: "deny", rules: [], callers: ["echo"], callees: ["upper"], matrix: [] },
      isPending: false,
      isError: false,
    } as unknown as ReturnType<typeof usePolicy>);
    mockedUseDecisions.mockReturnValue({
      data: { items: [{
        timestamp: "2026-11-16T10:00:00Z",
        kind: "auth",
        caller: "echo",
        callee: "upper",
        decision: "deny",
        reason: "fixture denied",
      }], total: 1 },
      isPending: false,
      isError: false,
    } as unknown as ReturnType<typeof useDecisions>);
  });

  it("records and undoes the local approval acknowledgement from the button", () => {
    render(<MemoryRouter><Policy /></MemoryRouter>);

    const card = screen.getByLabelText("Decision approval");
    fireEvent.click(within(card).getByRole("button", { name: /Approve/ }));
    expect(within(card).getByText(/Marked approved locally/)).toBeInTheDocument();

    fireEvent.click(within(card).getByRole("button", { name: "Undo decision" }));
    expect(within(card).queryByText(/Marked approved locally/)).not.toBeInTheDocument();
  });
});
