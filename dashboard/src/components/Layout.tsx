import { NavLink, Outlet } from "react-router-dom";
import { ThemeToggle } from "./ThemeToggle";

interface NavItem {
  to: string;
  label: string;
  end?: boolean;
}

const NAV_ITEMS: NavItem[] = [
  { to: "/", label: "Overview", end: true },
  { to: "/tasks", label: "Tasks" },
  { to: "/graph", label: "Agent graph" },
  { to: "/costs", label: "Costs" },
  { to: "/agents", label: "Agents" },
  { to: "/settings", label: "Settings" },
];

/** Application shell: header, navigation and the routed page. */
export function Layout() {
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      <header className="app-header">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true">
            ◈
          </span>
          <span className="brand-name">Kyurem</span>
          <span className="brand-subtitle">A2A gateway</span>
        </div>
        <ThemeToggle />
      </header>
      <div className="app-body">
        <nav className="app-nav" aria-label="Primary">
          <ul>
            {NAV_ITEMS.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  end={item.end}
                  className={({ isActive }) => (isActive ? "nav-link is-active" : "nav-link")}
                >
                  {item.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <main id="main-content" className="app-main" tabIndex={-1}>
          <Outlet />
        </main>
      </div>
    </div>
  );
}
