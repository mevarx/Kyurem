import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ApiError } from "../api/client";
import { ErrorState } from "./ErrorState";

describe("management API error guidance", () => {
  it.each([
    [new ApiError(0, "Could not connect to the management API", "network_error"), "Management API unavailable"],
    [new ApiError(500, "Internal Server Error"), "Management API request failed"],
    [new ApiError(500, "database query timed out", "query_failed"), "Management API request failed"],
    [new ApiError(401, "missing bearer token", "unauthorized"), "Management API authentication required"],
    [new ApiError(403, "permission denied", "forbidden"), "Management API access denied"],
  ])("preserves %s with appropriate guidance", (error, heading) => {
    const retry = vi.fn();
    render(<MemoryRouter><ErrorState error={error} onRetry={retry} /></MemoryRouter>);
    expect(screen.getByText(heading)).toBeInTheDocument();
    expect(screen.getByText(new RegExp(error.message))).toBeInTheDocument();
    if (error.status === 401 || error.status === 403) {
      expect(screen.getByText(/Check the management API bearer token/)).toBeInTheDocument();
      expect(screen.queryByText(/Check that the gateway management API is running/)).not.toBeInTheDocument();
    } else {
      expect(screen.getByText(/VITE_KYUREM_API_BASE/)).toHaveTextContent("KYUREM_API_ORIGIN");
    }
    expect(screen.getByRole("link", { name: /Settings · API/ })).toHaveAttribute("href", "/settings");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(retry).toHaveBeenCalledOnce();
    if (error.status !== 0) expect(screen.queryByText("Can't reach the gateway")).not.toBeInTheDocument();
  });
});
