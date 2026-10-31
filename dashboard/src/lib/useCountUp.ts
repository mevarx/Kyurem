import { useEffect, useRef, useState } from "react";

function prefersReducedMotion(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

function initialValue(target: number): number {
  return !Number.isFinite(target) || prefersReducedMotion() ? target : 0;
}

/** Count up to `target` on first paint. Returns the target immediately when
 * reduced motion is preferred or the value is not finite. */
export function useCountUp(target: number, durationMs = 900): number {
  // Deterministic output under test runners (no rAF timing flakes).
  const isTest = import.meta.env.MODE === "test";
  const [value, setValue] = useState(() => (isTest ? target : initialValue(target)));
  const fromRef = useRef<number | null>(null);

  useEffect(() => {
    if (isTest) return;
    const from = fromRef.current ?? initialValue(target);
    if (!Number.isFinite(target) || from === target) return;
    if (prefersReducedMotion()) {
      // Async to keep the effect body free of synchronous setState.
      fromRef.current = target;
      const timer = window.setTimeout(() => setValue(target), 0);
      return () => window.clearTimeout(timer);
    }
    let frame = 0;
    const start = performance.now();
    const tick = (now: number) => {
      const progress = Math.min(1, (now - start) / durationMs);
      const eased = 1 - Math.pow(1 - progress, 3);
      const next = from + (target - from) * eased;
      fromRef.current = next;
      setValue(next);
      if (progress < 1) {
        frame = requestAnimationFrame(tick);
      } else {
        fromRef.current = target;
        setValue(target);
      }
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [target, durationMs, isTest]);

  return value;
}
