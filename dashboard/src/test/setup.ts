import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Vitest runs without globals in this project, so Testing Library cannot
// register its automatic cleanup hook by itself.
afterEach(() => {
  cleanup();
});

// jsdom does not implement ResizeObserver, which React Flow relies on to size
// its canvas. The stub is enough for rendering.
if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = class ResizeObserverStub {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  };
}
