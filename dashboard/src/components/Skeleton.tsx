/** Skeleton shimmer placeholders that mirror the final layout. */
export function Skeleton({ className = "", style }: { className?: string; style?: React.CSSProperties }) {
  return <div className={`skeleton ${className}`.trim()} style={style} aria-hidden="true" />;
}

/** Loading skeleton for the Overview KPI + chart layout. */
export function OverviewSkeleton() {
  return (
    <div role="status" aria-label="Loading overview">
      <div className="skeleton-grid" style={{ marginBottom: 12 }}>
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} className="skeleton-card" />
        ))}
      </div>
      <Skeleton className="skeleton-panel" />
      <span className="sr-only">Loading overview…</span>
    </div>
  );
}

/** Loading skeleton for dense tables. */
export function TableSkeleton({ rows = 8 }: { rows?: number }) {
  return (
    <div role="status" aria-label="Loading table">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} style={{ height: 40, marginBottom: 8 }} />
      ))}
      <span className="sr-only">Loading…</span>
    </div>
  );
}
