import type { ReactNode } from "react";

export interface EmptyStateProps {
  title: string;
  description?: string;
  action?: ReactNode;
}

/** Shown when a view has no data to display. */
export function EmptyState({ title, description, action }: EmptyStateProps) {
  return (
    <div className="state-panel state-empty" role="status">
      <p className="state-title">{title}</p>
      {description ? <p className="state-description">{description}</p> : null}
      {action ? <div className="state-action">{action}</div> : null}
    </div>
  );
}
