import type { ReactNode } from "react";

export interface EmptyStateProps {
  title: string;
  description?: string;
  action?: ReactNode;
  /** Icon key: inbox, search, graph, coins, shield, check */
  icon?: string;
}

const EMPTY_ICON_PATHS: Record<string, string> = {
  inbox: "M22 12h-4l-3 9L9 3l-3 9H2",
  search: "M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm10 2-4.3-4.3",
  graph: "M18 5H6a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2ZM12 9v6M9 12h6",
  coins: "M8 9h8M8 12h8M12 17h.01M12 3a9 9 0 1 0 9 9",
  shield: "M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1 1 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1v7Z",
  check: "M20 6 9 17l-5-5",
};

/** Illustrated empty state: icon, one-line hint, optional action. */
export function EmptyState({ title, description, action, icon = "inbox" }: EmptyStateProps) {
  return (
    <div className="state-card" role="status">
      <span className="state-icon" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round">
          <path d={EMPTY_ICON_PATHS[icon] ?? EMPTY_ICON_PATHS.inbox} />
        </svg>
      </span>
      <p className="state-title">{title}</p>
      {description ? <p className="state-description">{description}</p> : null}
      {action ? <div className="state-action">{action}</div> : null}
    </div>
  );
}
