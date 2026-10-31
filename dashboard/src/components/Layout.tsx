import { useEffect, useState } from "react";
import { Outlet } from "react-router-dom";
import DashboardLayout from "./watermelon/capitalio-dashboard/dashboard-layout";
import { CommandPalette } from "./CommandPalette";

/** Application shell: Capitalio-based sidebar + topbar with the routed page. */
export function Layout() {
  const [paletteOpen, setPaletteOpen] = useState(false);

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
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
    </div>
  );
}
