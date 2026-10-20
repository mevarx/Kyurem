import { useContext } from "react";
import { ThemeContext, type ThemeContextValue } from "./theme-context";

/** Read and control the active colour theme. */
export function useTheme(): ThemeContextValue {
  return useContext(ThemeContext);
}
