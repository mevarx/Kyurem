import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@xyflow/react/dist/style.css";
import "./styles.css";
import { App } from "./App";
import { ThemeProvider } from "./lib/ThemeProvider";
import { TooltipProvider } from "./components/ui/tooltip";
import { applyTheme, getInitialTheme } from "./lib/theme";
import { installMockApi } from "./lib/mockData";

// Dev-only mock data mode: `VITE_MOCK_DATA=1` serves canned API responses
// so every screen renders populated without a running gateway.
if (import.meta.env.VITE_MOCK_DATA === "1") {
  installMockApi();
}

// Apply the theme before the first paint so that the page never flashes.
applyTheme(getInitialTheme());

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

const container = document.getElementById("root");
if (!container) {
  throw new Error('Cannot start the Kyurem dashboard: #root is missing in index.html');
}

createRoot(container).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <TooltipProvider>
          <BrowserRouter>
            <App />
          </BrowserRouter>
        </TooltipProvider>
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>,
);
