import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { afterEach } from "vitest";

// Routes are code-split (React.lazy); under the full parallel suite the chunk
// transform can exceed RTL's 1s default. Assertions are unchanged; only the
// wait for the dynamic import gets headroom.
configure({ asyncUtilTimeout: 5_000 });

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

// jsdom lacks IntersectionObserver
if (typeof globalThis.IntersectionObserver === "undefined") {
  globalThis.IntersectionObserver = class IntersectionObserverStub {
    readonly root: Element | null = null;
    readonly rootMargin = "";
    readonly thresholds: readonly number[] = [];
    private callback: IntersectionObserverCallback;
    constructor(callback: IntersectionObserverCallback) {
      this.callback = callback;
    }
    observe(): void {
      this.callback([{ isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver);
    }
    unobserve(): void {}
    disconnect(): void {}
    takeRecords(): IntersectionObserverEntry[] {
      return [];
    }
  };
}

// jsdom does not implement matchMedia, which the sidebar's mobile hook
// relies on. The stub is enough for rendering.
if (typeof window !== "undefined" && typeof window.matchMedia !== "function") {
  window.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  });
}
