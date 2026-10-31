import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";

interface PaletteEntry {
  label: string;
  hint: string;
  to: string;
  icon: string;
}

const ROUTES: PaletteEntry[] = [
  { label: "Overview", hint: "Operate", to: "/", icon: "activity" },
  { label: "Tasks", hint: "Operate", to: "/tasks", icon: "list" },
  { label: "Agent graph", hint: "Operate", to: "/graph", icon: "graph" },
  { label: "Costs", hint: "Manage", to: "/costs", icon: "coins" },
  { label: "Agents", hint: "Manage", to: "/agents", icon: "bot" },
  { label: "Policy & audit", hint: "Manage", to: "/policy", icon: "shield" },
  { label: "Settings", hint: "Manage", to: "/settings", icon: "settings" },
];

const ICONS: Record<string, string> = {
  activity: "M22 12h-4l-3 9L9 3l-3 9H2",
  list: "M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01",
  graph: "M18 5H6a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2ZM12 9v6M9 12h6",
  coins: "M8 9h8M8 12h8M12 17h.01M12 3a9 9 0 1 0 9 9",
  bot: "M12 8V4H8M4 8h16v9a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4V8Z",
  shield: "M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1 1 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1v7Z",
  settings: "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z",
  search: "M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm10 2-4.3-4.3",
};

/** Global command palette (Cmd/Ctrl+K): jump to pages or a task id. */
export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  // Reset the palette each time it opens (render-time adjustment avoids
  // cascading renders from setState-in-effect).
  const [wasOpen, setWasOpen] = useState(false);
  if (open && !wasOpen) {
    setWasOpen(true);
    setQuery("");
    setActive(0);
  } else if (!open && wasOpen) {
    setWasOpen(false);
  }

  useEffect(() => {
    if (!open) return;
    const timer = window.setTimeout(() => inputRef.current?.focus(), 30);
    return () => window.clearTimeout(timer);
  }, [open ]);

  const entries = useMemo<PaletteEntry[]>(() => {
    const q = query.trim().toLowerCase();
    const matches = q === "" ? ROUTES : ROUTES.filter((r) => r.label.toLowerCase().includes(q));
    const out = [...matches];
    if (q !== "") {
      out.push({
        label: `Open task “${query.trim()}”`,
        hint: "Task",
        to: `/tasks/${encodeURIComponent(query.trim())}`,
        icon: "search",
      });
    }
    return out;
  }, [query]);

  const safeActive = Math.min(active, Math.max(0, entries.length - 1));

  if (!open) return null;

  const go = (to: string) => {
    onClose();
    navigate(to);
  };

  return (
    <div
      className="palette-scrim"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-label="Command palette"
    >
      <div className="palette" onClick={(e) => e.stopPropagation()}>
        <div className="palette-input-row">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d={ICONS.search} />
          </svg>
          <input
            ref={inputRef}
            type="text"
            placeholder="Go to a page or paste a task id…"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setActive(0);
            }}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setActive((a) => Math.min(entries.length - 1, a + 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setActive((a) => Math.max(0, a - 1));
              } else if (e.key === "Enter") {
                e.preventDefault();
                const entry = entries[safeActive];
                if (entry) go(entry.to);
              } else if (e.key === "Escape") {
                onClose();
              }
            }}
            aria-label="Search pages and tasks"
          />
        </div>
        <ul className="palette-list">
          {entries.length === 0 ? (
            <li className="palette-item" aria-disabled="true">
              No matches
            </li>
          ) : (
            entries.map((entry, i) => (
              <li key={`${entry.to}-${i}`}>
                <button
                  type="button"
                  className={`palette-item${i === safeActive ? " is-active" : ""}`}
                  onMouseEnter={() => setActive(i)}
                  onClick={() => go(entry.to)}
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d={ICONS[entry.icon] ?? ICONS.activity} />
                  </svg>
                  {entry.label}
                  <span className="mono">{entry.hint}</span>
                </button>
              </li>
            ))
          )}
        </ul>
      </div>
    </div>
  );
}
