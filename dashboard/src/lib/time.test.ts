import { describe, expect, it } from "vitest";
import {
  customRange,
  describeRange,
  fromLocalInputValue,
  parseRelativeWindow,
  presetRange,
  resolveWindowStart,
  toApiWindow,
  toLocalInputValue,
} from "./time";

describe("parseRelativeWindow", () => {
  it("parses the windows the management API understands", () => {
    expect(parseRelativeWindow("15m")).toBe(15 * 60_000);
    expect(parseRelativeWindow("1h")).toBe(3_600_000);
    expect(parseRelativeWindow("24h")).toBe(86_400_000);
    expect(parseRelativeWindow("7d")).toBe(7 * 86_400_000);
  });

  it("returns null for values that are not relative windows", () => {
    expect(parseRelativeWindow("2024-05-01T00:00:00Z")).toBeNull();
    expect(parseRelativeWindow("")).toBeNull();
    expect(parseRelativeWindow("12x")).toBeNull();
  });
});

describe("resolveWindowStart", () => {
  it("resolves a relative window against the current time", () => {
    const now = new Date("2024-05-01T12:00:00Z");
    expect(resolveWindowStart("1h", now)?.toISOString()).toBe("2024-05-01T11:00:00.000Z");
  });

  it("resolves an absolute timestamp unchanged", () => {
    const now = new Date("2024-05-01T12:00:00Z");
    expect(resolveWindowStart("2024-04-01T00:00:00Z", now)?.toISOString()).toBe(
      "2024-04-01T00:00:00.000Z",
    );
  });
});

describe("toApiWindow", () => {
  it("only sends `from` for relative presets", () => {
    expect(toApiWindow(presetRange("24h"))).toEqual({ from: "24h" });
  });

  it("sends both bounds for a custom range", () => {
    const range = customRange("2024-05-01T00:00:00Z", "2024-05-02T00:00:00Z");
    expect(toApiWindow(range)).toEqual({
      from: "2024-05-01T00:00:00Z",
      to: "2024-05-02T00:00:00Z",
    });
  });
});

describe("describeRange", () => {
  it("describes presets and custom ranges", () => {
    expect(describeRange(presetRange("15m"))).toBe("Last 15 minutes");
    expect(describeRange(customRange("", ""))).toBe("Custom range");
  });
});

describe("local input helpers", () => {
  it("round-trips a datetime-local value", () => {
    const date = new Date("2024-05-01T12:34:00Z");
    const local = toLocalInputValue(date);
    expect(local).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/);
    expect(fromLocalInputValue(local)).toBe(date.toISOString());
  });

  it("returns an empty string for invalid input", () => {
    expect(fromLocalInputValue("")).toBe("");
    expect(fromLocalInputValue("not a date")).toBe("");
  });
});
