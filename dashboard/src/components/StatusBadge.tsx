import type { BadgeTone } from "../lib/status";

export interface StatusBadgeProps {
  label: string;
  tone?: BadgeTone;
  title?: string;
}

/** Small coloured pill used for task, card and breaker states. */
export function StatusBadge({ label, tone = "neutral", title }: StatusBadgeProps) {
  return (
    <span className={`badge badge-${tone}`} title={title ?? label}>
      {label}
    </span>
  );
}
