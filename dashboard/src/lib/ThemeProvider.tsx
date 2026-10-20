import { useEffect, useMemo, useState, type ReactNode } from "react";
import { applyTheme, getInitialTheme, storeTheme, type Theme } from "./theme";
import { ThemeContext, type ThemeContextValue } from "./theme-context";

/** Provides the active theme to the whole application. */
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(() => getInitialTheme());

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  const value = useMemo<ThemeContextValue>(
    () => ({
      theme,
      setTheme: (next: Theme) => {
        storeTheme(next);
        setThemeState(next);
      },
      toggle: () =>
        setThemeState((current) => {
          const next: Theme = current === "dark" ? "light" : "dark";
          storeTheme(next);
          return next;
        }),
    }),
    [theme],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}
