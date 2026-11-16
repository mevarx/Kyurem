import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { JsonExplorer } from "./json-explorer";

describe("JsonExplorer layout", () => {
  it("uses the width of its task detail card instead of a narrow fixed cap", () => {
    render(<JsonExplorer data={{ task_id: "task-1", status: "failed" }} title="task" />);

    const explorer = screen.getByRole("tree").parentElement;
    expect(explorer).toHaveClass("w-full", "min-w-0");
    expect(explorer?.className).not.toContain("max-w-[460px]");
  });
});
