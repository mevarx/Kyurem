import { describe, expect, it } from "vitest";
import {
  formatCost,
  formatDuration,
  formatNumber,
  formatPercent,
  formatRelativeTime,
  formatTokens,
  truncateId,
  UNKNOWN,
} from "./format";

describe("formatCost", () => {
  it("renders unknown for null and undefined", () => {
    expect(formatCost(null)).toBe(UNKNOWN);
    expect(formatCost(undefined)).toBe(UNKNOWN);
  });

  it("renders unknown for values that are not finite numbers", () => {
    expect(formatCost(Number.NaN)).toBe(UNKNOWN);
    expect(formatCost(Number.POSITIVE_INFINITY)).toBe(UNKNOWN);
  });

  it("never renders an unknown cost as zero", () => {
    expect(formatCost(null)).not.toBe("$0.00");
  });

  it("formats known costs with a currency sign", () => {
    expect(formatCost(0)).toBe("$0.00");
    expect(formatCost(1.5)).toBe("$1.50");
    expect(formatCost(0.0123)).toBe("$0.0123");
    expect(formatCost(0.000012)).toBe("$0.000012");
  });
});

describe("formatTokens", () => {
  it("renders unknown for null and undefined", () => {
    expect(formatTokens(null)).toBe(UNKNOWN);
    expect(formatTokens(undefined)).toBe(UNKNOWN);
  });

  it("formats known token counts", () => {
    expect(formatTokens(0)).toBe("0");
    expect(formatTokens(1234)).toBe("1,234");
  });
});

describe("formatDuration", () => {
  it("renders unknown for null and undefined", () => {
    expect(formatDuration(null)).toBe(UNKNOWN);
    expect(formatDuration(undefined)).toBe(UNKNOWN);
  });

  it("formats sub-second, second and minute durations", () => {
    expect(formatDuration(0)).toBe("0 ms");
    expect(formatDuration(12.4)).toBe("12 ms");
    expect(formatDuration(1500)).toBe("1.50 s");
    expect(formatDuration(90_000)).toBe("1m 30.0s");
  });
});

describe("formatPercent", () => {
  it("renders unknown for null and undefined", () => {
    expect(formatPercent(null)).toBe(UNKNOWN);
    expect(formatPercent(undefined)).toBe(UNKNOWN);
  });

  it("formats a ratio as a percentage", () => {
    expect(formatPercent(0)).toBe("0.0%");
    expect(formatPercent(0.1234)).toBe("12.3%");
    expect(formatPercent(1)).toBe("100.0%");
  });
});

describe("formatNumber and helpers", () => {
  it("renders unknown for missing numbers", () => {
    expect(formatNumber(null)).toBe(UNKNOWN);
  });

  it("formats relative times", () => {
    const now = new Date("2024-05-01T12:00:00Z");
    expect(formatRelativeTime("2024-05-01T11:55:00Z", now)).toBe("5m ago");
    expect(formatRelativeTime(null, now)).toBe(UNKNOWN);
  });

  it("truncates long identifiers", () => {
    expect(truncateId("abcdefghijklmnop", 8)).toBe("abcdefg…");
    expect(truncateId("")).toBe(UNKNOWN);
  });
});
