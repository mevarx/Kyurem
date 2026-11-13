/** Skeleton shimmer placeholders that mirror the final layout. */
import type { CSSProperties } from "react";
import { SkeletonSweep, SkeletonSweepBlock } from "./skeleton-sweep";

export function Skeleton({ className = "", style }: { className?: string; style?: CSSProperties }) {
  return <SkeletonSweepBlock className={className} style={style} />;
}

/** Loading skeleton for the Overview KPI + chart layout. */
export function OverviewSkeleton() {
  return (
    <SkeletonSweep aria-label="Loading overview">
      <div className="skeleton-grid" style={{ marginBottom: 12 }}>
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} className="skeleton-card" />
        ))}
      </div>
      <Skeleton className="skeleton-panel" />
      <div aria-hidden="true" style={{ display: "grid", gridTemplateColumns: "2fr 1fr", gap: 12, marginTop: 12 }}>
        <Skeleton style={{ height: 260, borderRadius: "var(--radius-card)" }} />
        <Skeleton style={{ height: 260, borderRadius: "var(--radius-card)" }} />
      </div>
      <span className="sr-only">Loading overview…</span>
    </SkeletonSweep>
  );
}

/** Loading skeleton for dense tables (header + 40px rows). */
export function TableSkeleton({ rows = 8 }: { rows?: number }) {
  return (
    <SkeletonSweep aria-label="Loading table">
      <Skeleton style={{ height: 28, marginBottom: 8 }} />
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} style={{ height: 40, marginBottom: 8 }} />
      ))}
      <span className="sr-only">Loading…</span>
    </SkeletonSweep>
  );
}

/** Compact card skeleton for detail/side panels (exported for later use). */
export function CardSkeleton({ lines = 3 }: { lines?: number }) {
  return (
    <SkeletonSweep aria-label="Loading card">
      <Skeleton style={{ height: 28, width: "55%", marginBottom: 12 }} />
      {Array.from({ length: lines }, (_, i) => (
        <Skeleton key={i} style={{ height: 14, marginBottom: 8 }} />
      ))}
      <span className="sr-only">Loading…</span>
    </SkeletonSweep>
  );
}

/** Compact list skeleton for ranked/span rows (exported for later use). */
export function ListSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <SkeletonSweep aria-label="Loading list">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} style={{ height: 32, marginBottom: 8 }} />
      ))}
      <span className="sr-only">Loading…</span>
    </SkeletonSweep>
  );
}
