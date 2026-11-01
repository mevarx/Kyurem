import { useEffect, useMemo, useState } from "react";
import { Outlet, useNavigate } from "react-router-dom";
import {
  BotIcon,
  CoinsIcon,
  LayoutDashboardIcon,
  ListTodoIcon,
  MoonIcon,
  SettingsIcon,
  ShieldCheckIcon,
  WorkflowIcon,
} from "lucide-react";
import DashboardLayout from "./watermelon/capitalio-dashboard/dashboard-layout";
import { Launcher, type LauncherAction } from "./launcher";
import { useTheme } from "../lib/useTheme";

/** Application shell: Capitalio-based sidebar + topbar with the routed page. */
export function Layout() {
  const [paletteOpen, setPaletteOpen] = useState(false);
  const navigate = useNavigate();
  const { toggle } = useTheme();

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPaletteOpen((open) => !open);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const actions = useMemo<LauncherAction[]>(
    () => [
      { id: "nav-overview", label: "Go to Overview", icon: <LayoutDashboardIcon />, group: "Navigation", onSelect: () => navigate("/") },
      { id: "nav-tasks", label: "Go to Tasks", icon: <ListTodoIcon />, group: "Navigation", onSelect: () => navigate("/tasks") },
      { id: "nav-graph", label: "Go to Agent graph", icon: <WorkflowIcon />, group: "Navigation", onSelect: () => navigate("/graph") },
      { id: "nav-costs", label: "Go to Costs", icon: <CoinsIcon />, group: "Navigation", onSelect: () => navigate("/costs") },
      { id: "nav-agents", label: "Go to Agents", icon: <BotIcon />, group: "Navigation", onSelect: () => navigate("/agents") },
      { id: "nav-policy", label: "Go to Policy & audit", icon: <ShieldCheckIcon />, group: "Navigation", onSelect: () => navigate("/policy") },
      { id: "nav-settings", label: "Go to Settings", icon: <SettingsIcon />, group: "Navigation", onSelect: () => navigate("/settings") },
      { id: "theme-toggle", label: "Toggle theme", icon: <MoonIcon />, group: "Settings", keywords: ["dark", "light", "appearance"], done: "Theme toggled", onSelect: () => toggle() },
    ],
    [navigate, toggle],
  );

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      <DashboardLayout onSearch={() => setPaletteOpen(true)}>
        <main id="main-content" className="app-main" tabIndex={-1}>
          <Outlet />
        </main>
      </DashboardLayout>
      {paletteOpen ? (
        <div className="palette-scrim" onClick={() => setPaletteOpen(false)}>
          <div
            aria-label="Command menu"
            aria-modal="true"
            className="palette-modal"
            onClick={(event) => event.stopPropagation()}
            role="dialog"
          >
            <Launcher
        actions={actions}
        defaultRecent={["nav-tasks", "nav-overview"]}
        onOpenChange={setPaletteOpen}
        open={paletteOpen}
        placeholder="Go to a page or paste a task id…"
        queryAction={(query) =>
          query
            ? {
                id: `task:${query}`,
                label: `Open task ${query}`,
                group: "Tasks",
                done: `Opened task ${query}`,
                onSelect: () => navigate(`/tasks/${encodeURIComponent(query)}`),
              }
            : undefined
        }
        triggerLabel="Search or jump to…"
      />
          </div>
        </div>
      ) : null}
    </div>
  );
}
