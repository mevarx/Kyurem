import { createContext } from "react";
import type { Theme } from "./theme";

export interface ThemeContextValue {
  theme: Theme;
  setTheme: (theme: Theme) => void;
  toggle: () => void;
}

/** Context carrying the active colour theme. */
export const ThemeContext = createContext<ThemeContextValue>({
  theme: "light",
  setTheme: () => undefined,
  toggle: () => undefined,
});
