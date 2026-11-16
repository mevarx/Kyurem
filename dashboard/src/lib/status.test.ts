import { describe, expect, it } from "vitest";
import { verificationTone } from "./status";

// gateway/internal/trust/trust.go defines the four explicit outcomes.
const verificationCases = [
  ["verified", "good"], ["unverified", "bad"], ["unsigned", "warn"],
  ["unchecked", "neutral"], ["unknown", "neutral"], ["checked", "neutral"],
  ["future-status", "neutral"], ["", "neutral"], [undefined, "neutral"],
] as const;

describe("verificationTone", () => {
  it.each(verificationCases)("maps %s to %s", (status, tone) => {
    expect(verificationTone(status)).toBe(tone);
  });
});
