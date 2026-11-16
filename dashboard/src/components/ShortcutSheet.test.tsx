import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ShortcutSheet } from "./shortcut-sheet";

describe("keyboard shortcut sheet", () => {
  it("opens from the focused region, filters shortcuts and closes with Escape", async () => {
    render(<ShortcutSheet groups={[{ title: "Navigation", shortcuts: [{ label: "Open menu", keys: [["mod", "K"]] }] }]}><p>Help</p></ShortcutSheet>);
    const region = screen.getByRole("region", { name: /press question mark/i });
    region.focus();
    fireEvent.keyDown(region, { key: "?" });
    const search = screen.getByRole("textbox", { name: "Search shortcuts" });
    expect(search).toHaveFocus();
    expect(screen.getByRole("dialog")).toHaveTextContent("Open menu");
    fireEvent.change(search, { target: { value: "missing" } });
    expect(screen.getByText(/No shortcuts for/)).toBeInTheDocument();
    fireEvent.keyDown(search, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(region).toHaveFocus();
  });
});
