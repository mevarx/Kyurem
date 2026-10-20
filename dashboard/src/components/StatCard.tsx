export interface StatCardProps {
  label: string;
  value: string;
  hint?: string;
  tone?: "neutral" | "good" | "bad" | "warn";
}

/** A single headline number with an optional explanation. */
export function StatCard({ label, value, hint, tone = "neutral" }: StatCardProps) {
  return (
    <div className={`stat-card stat-card-${tone}`}>
      <span className="stat-label">{label}</span>
      <span className="stat-value">{value}</span>
      {hint ? <span className="stat-hint">{hint}</span> : null}
    </div>
  );
}
