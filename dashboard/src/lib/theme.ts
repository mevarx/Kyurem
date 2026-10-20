/**
 * Theme handling.
 *
 * The theme is stored as `data-theme` on the `<html>` element so that CSS
 * variables can switch between the light and the dark palette. The choice is
 * persisted in localStorage; on the very first visit the OS preference
 * (`prefers-color-scheme`) wins.
 */

export type Theme = "light" | "dark";

export const THEME_STORAGE_KEY = "kyurem.theme";

/** Read the persisted theme, falling back to the OS preference. */
export function getInitialTheme(): Theme {
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
    if (stored === "light" || stored === "dark") return stored;
  } catch {
    // Storage unavailable: fall through to the OS preference.
  }
  return getPreferredColorScheme();
}

/** The OS preference, defaulting to light. */
export function getPreferredColorScheme(): Theme {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return "light";
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

/** Persist the theme choice. */
export function storeTheme(theme: Theme): void {
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // Storage unavailable: the theme still applies for this page view.
  }
}

/** Apply the theme to the document element. */
export function applyTheme(theme: Theme): void {
  document.documentElement.setAttribute("data-theme", theme);
}
