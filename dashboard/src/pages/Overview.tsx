import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { LIVE_REFETCH_INTERVAL_MS, useOverview } from "../api/hooks";
import type { Overview as OverviewData } from "../api/types";
import {
  DashboardCard,
  DashboardChartTooltip,
  DetailHeader,
  DetailTag,
} from "../components/watermelon/capitalio-dashboard/components/capitalio/shared";
import { EmptyState } from "../components/EmptyState";
import { ErrorState } from "../components/ErrorState";
import { Sparkline } from "../components/Sparkline";
import { OverviewSkeleton } from "../components/Skeleton";
import { StatusBadge } from "../components/StatusBadge";
import { TimeRangePicker } from "../components/TimeRangePicker";
import { useCountUp } from "../lib/useCountUp";
import { formatCost, formatDuration, formatNumber, formatPercent, formatTime } from "../lib/format";
import { taskStatusTone } from "../lib/status";
import { DEFAULT_TIME_RANGE, toApiWindow, type TimeRange } from "../lib/time";

/** Dashboard landing page: headline KPIs, throughput and recent failures. */
export function Overview() {
  const [range, setRange] = useState<TimeRange>(DEFAULT_TIME_RANGE);
  const query = useOverview(toApiWindow(range), { refetchInterval: LIVE_REFETCH_INTERVAL_MS });
  const data = query.data;

  const taskChart = useMemo(
    () =>
      (data?.tasks_over_time ?? []).map((bucket) => ({
        label: formatTime(bucket.bucket),
        total: bucket.succeeded + bucket.failed + bucket.unknown,
        succeeded: bucket.succeeded,
        failed: bucket.failed,
        unknown: bucket.unknown,
      })),
    [data],
  );

  const throughputTotals = useMemo(() => taskChart.map((b) => b.total), [taskChart]);
  const spendSeries = useMemo(() => (data?.cost_over_time ?? []).map((b) => b.cost_usd ?? 0), [data]);

  const successRate = data?.tasks_total ? (data.tasks_succeeded / data.tasks_total) : null;

  if (query.isPending) {
    return (
      <div className="page">
        <PageHeader range={range} onRangeChange={setRange} />
        <OverviewSkeleton />
      </div>
    );
  }

  if (query.isError) {
    return (
      <div className="page">
        <PageHeader range={range} onRangeChange={setRange} />
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      </div>
    );
  }

  const overview: OverviewData | undefined = data;

  return (
    <div className="page">
      <PageHeader range={range} onRangeChange={setRange} />

      <section className="kpi-grid" aria-label="Key indicators">
        <KpiCard
          label="Requests"
          value={formatNumber(overview?.tasks_total)}
          numeric={overview?.tasks_total ?? null}
          format={(v) => formatNumber(Math.round(v))}
          pill={<DeltaPill change={halfChange(throughputTotals)} suffix="vs 1st half" tone="neutral" />}
          hint={`${formatNumber(overview?.tasks_succeeded)} succeeded`}
          sparkValues={throughputTotals}
        />
        <KpiCard
          label="Success rate"
          value={successRate === null ? "unknown" : formatPercent(successRate)}
          numeric={successRate === null ? null : successRate * 100}
          format={(v) => `${v.toFixed(1)}%`}
          pill={<DeltaPill change={halfChange(taskChart.map((b) => b.succeeded))} suffix="volume" tone="success" />}
          hint={`${formatNumber(overview?.tasks_failed)} failed`}
          sparkValues={taskChart.map((b) => (b.total === 0 ? 0 : b.succeeded / b.total))}
          sparkStroke="var(--success)"
        />
        <KpiCard
          label="p95 latency"
          value={formatDuration(overview?.p95_task_duration_ms)}
          numeric={overview?.p95_task_duration_ms ?? null}
          format={(v) => formatDuration(v)}
          hint={`p50 ${formatDuration(overview?.p50_task_duration_ms)}`}
          sparkValues={[]}
        />
        <KpiCard
          label="Active agents"
          value={formatNumber(overview?.active_agents)}
          numeric={overview?.active_agents ?? null}
          format={(v) => formatNumber(Math.round(v))}
          pill={<span className="pill pill-info">live</span>}
          hint={`${formatNumber(overview?.spans_total)} spans`}
          sparkValues={[]}
        />
        <KpiCard
          label="Spend"
          value={formatCost(overview?.total_cost_usd)}
          numeric={overview?.total_cost_usd ?? null}
          format={(v) =>
            formatCost(overview?.total_cost_usd === null || overview?.total_cost_usd === undefined ? null : v)
          }
          pill={<DeltaPill change={halfChange(spendSeries)} suffix="vs 1st half" tone="warning" />}
          hint="Selected window"
          sparkValues={spendSeries}
        />
      </section>

      <div className="panel-row">
        <DashboardCard aria-label="Throughput">
          <DetailHeader title="Throughput" subtitle="Requests per bucket across the window">
            <DetailTag color="var(--chart-1)">
              {formatNumber(overview?.tasks_total)} tasks · {formatPercent(overview?.error_rate)} errors
            </DetailTag>
          </DetailHeader>
          <div id="throughput" className="sr-only">Throughput</div>
          {taskChart.length === 0 ? (
            <EmptyState
              title="No tasks in this window"
              description="Nothing was recorded for the selected range."
              icon="search"
            />
          ) : (
            <div className="chart-frame chart-frame-main">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={taskChart} margin={{ top: 8, right: 16, bottom: 4, left: 0 }}>
                  <defs>
                    <linearGradient id="tputFill" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="var(--primary)" stopOpacity={0.3} />
                      <stop offset="100%" stopColor="var(--primary)" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="var(--border)" />
                  <XAxis
                    dataKey="label"
                    tick={{ fontSize: 11, fill: "var(--text-subtle)" }}
                    tickLine={false}
                    axisLine={false}
                    interval="preserveStartEnd"
                    minTickGap={36}
                    padding={{ left: 4, right: 8 }}
                  />
                  <YAxis
                    allowDecimals={false}
                    tick={{ fontSize: 11, fill: "var(--text-subtle)" }}
                    width={44}
                    tickLine={false}
                    axisLine={false}
                  />
                  <Tooltip
                    cursor={false}
                    content={
                      <DashboardChartTooltip
                        colors={{ total: "var(--chart-1)" }}
                        names={{ total: "requests" }}
                      />
                    }
                  />
                  <Area
                    type="monotone"
                    dataKey="total"
                    name="requests"
                    stroke="var(--primary)"
                    strokeWidth={2}
                    fill="url(#tputFill)"
                    dot={false}
                    activeDot={{ r: 4, fill: "var(--primary)", stroke: "var(--card)" }}
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          )}
        </DashboardCard>

        <DashboardCard aria-label="Top agents">
          <DetailHeader title="Top agents" subtitle="Ranked by spend in this window">
            <DetailTag>Ranked list</DetailTag>
          </DetailHeader>
          {(data?.top_agents_by_cost?.length ?? 0) === 0 ? (
            <EmptyState title="No agent cost data" icon="coins" />
          ) : (
            <>
              <RankedBars
                rows={(data?.top_agents_by_cost ?? []).slice(0, 5).map((bucket) => ({
                  key: bucket.key,
                  display: formatCost(bucket.cost_usd),
                  ratio: maxBy(data?.top_agents_by_cost ?? [], (b) => b.cost_usd ?? 0),
                  value: bucket.cost_usd ?? 0,
                  accent: true,
                }))}
              />
              <div className="card-footer">
                <span className="count-meta">{formatCost(overview?.total_cost_usd)} total</span>
                <Link className="link-inline" to="/costs">View all</Link>
              </div>
            </>
          )}
        </DashboardCard>
      </div>

      <div className="panel-row">
        <DashboardCard aria-label="Top failing agents">
          <DetailHeader title="Top failing agents" subtitle="Where the errors concentrate">
            <DetailTag color="var(--destructive)">{formatNumber(overview?.tasks_failed)} failures</DetailTag>
          </DetailHeader>
          {(data?.top_failing_agents?.length ?? 0) === 0 ? (
            <EmptyState title="No agent failures in this window" icon="check" />
          ) : (
            <>
              <RankedBars
                rows={(data?.top_failing_agents ?? []).slice(0, 5).map((agent) => ({
                  key: agent.name,
                  display: `${formatNumber(agent.errors)} · ${formatPercent(agent.error_rate)}`,
                  ratio: maxBy(data?.top_failing_agents ?? [], (a) => a.errors),
                  value: agent.errors,
                  bad: true,
                }))}
              />
              <div className="card-footer">
                <span className="count-meta">
                  {formatNumber(overview?.tasks_failed)} failed tasks
                </span>
                <Link className="link-inline" to="/agents">View all agents</Link>
              </div>
            </>
          )}
        </DashboardCard>

        <DashboardCard aria-label="Recent failed tasks">
          <DetailHeader title="Recent failed tasks" subtitle="Newest failures first">
            <Link className="link-inline" to="/tasks">All tasks</Link>
          </DetailHeader>
          {overview && overview.recent_failed_tasks.length > 0 ? (
            <ul className="task-list">
              {overview.recent_failed_tasks.map((task) => (
                <li key={task.task_id} className="task-list-item">
                  <Link className="task-id-link" to={`/tasks/${encodeURIComponent(task.task_id)}`}>
                    {task.task_id}
                  </Link>
                  <span className="task-list-meta">
                    <StatusBadge label={task.status} tone={taskStatusTone(task.status)} />
                    <span>{task.root_caller ?? "unknown"}</span>
                    <span>{formatDuration(task.duration_ms)}</span>
                    <span>{formatCost(task.cost_usd)}</span>
                  </span>
                  {task.error_message ? (
                    <span className="task-list-error">{task.error_message}</span>
                  ) : null}
                </li>
              ))}
            </ul>
          ) : (
            <EmptyState title="No failed tasks" description="Nothing failed in the selected window." icon="check" />
          )}
        </DashboardCard>
      </div>
    </div>
  );
}

/** Relative change of the second half vs the first half of a series. */
function halfChange(values: number[]): number | null {
  if (values.length < 4) return null;
  const mid = Math.floor(values.length / 2);
  const first = values.slice(0, mid).reduce((a, b) => a + b, 0);
  const second = values.slice(mid).reduce((a, b) => a + b, 0);
  if (first <= 0) return second > 0 ? 1 : null;
  return (second - first) / first;
}

function maxBy<T>(rows: T[], pick: (row: T) => number): number {
  return rows.reduce((max, row) => Math.max(max, pick(row)), 0);
}

function DeltaPill({
  change,
  suffix,
  tone,
}: {
  change: number | null;
  suffix: string;
  tone: "success" | "warning" | "danger" | "neutral";
}) {
  if (change === null || !Number.isFinite(change)) {
    return <span className="pill pill-neutral">— {suffix}</span>;
  }
  const up = change >= 0;
  // Colour follows meaning; the arrow keeps it readable without colour.
  const meaning =
    tone === "neutral" ? "neutral" : tone === "warning" ? "warning" : tone === "success" ? (up ? "success" : "danger") : up ? "danger" : "success";
  const icon = up ? "↑" : "↓";
  return (
    <span className={`pill pill-${meaning}`}>
      <span aria-hidden="true">{icon}</span>
      {Math.abs(change * 100).toFixed(0)}% {suffix}
    </span>
  );
}

function KpiCard({
  label,
  value,
  numeric,
  format,
  pill,
  hint,
  sparkValues,
  sparkStroke,
}: {
  label: string;
  value: string;
  numeric: number | null;
  format?: (v: number) => string;
  pill?: React.ReactNode;
  hint?: string;
  sparkValues: number[];
  sparkStroke?: string;
}) {
  const animated = useCountUp(numeric ?? Number.NaN);
  const display =
    numeric === null || !Number.isFinite(numeric) || !format ? value : format(animated);
  return (
    <div className="card panel-gradient kpi-card">
      <div className="kpi-top">
        <span className="kpi-label">{label}</span>
        {pill}
      </div>
      <span className="kpi-value">{display}</span>
      {hint ? <span className="kpi-hint">{hint}</span> : null}
      {sparkValues.length >= 2 ? (
        <Sparkline values={sparkValues} stroke={sparkStroke} />
      ) : (
        <span className="spark-empty" aria-hidden="true">
          <span className="spark-empty-line" />
          <span className="spark-empty-label">no trend data</span>
        </span>
      )}
    </div>
  );
}

interface RankedRow {
  key: string;
  display: string;
  ratio: number;
  value: number;
  accent?: boolean;
  bad?: boolean;
}

function RankedBars({ rows }: { rows: RankedRow[] }) {
  const max = rows.reduce((m, r) => Math.max(m, r.ratio), 0) || 1;
  return (
    <ol className="ranked-list">
      {rows.map((row, index) => (
        <li key={row.key} className={`ranked-row${index === 0 ? " ranked-row-top" : ""}`}>
          <span className="ranked-name">
            <span className="avatar" aria-hidden="true">
              {row.key.slice(0, 2).toUpperCase()}
            </span>
            {row.key}
          </span>
          <span className="ranked-value">{row.display}</span>
          <span className="ranked-bar-track" aria-hidden="true">
            <span
              className={`ranked-bar-fill${row.bad ? " ranked-bar-fill-bad" : row.accent ? " ranked-bar-fill-accent" : ""}`}
              style={{ width: `${Math.max(4, Math.min(100, (row.value / max) * 100))}%`, display: "block" }}
            />
          </span>
        </li>
      ))}
    </ol>
  );
}

function PageHeader({
  range,
  onRangeChange,
}: {
  range: TimeRange;
  onRangeChange: (range: TimeRange) => void;
}) {
  return (
    <header className="page-header">
      <div className="page-title-pair">
        <span className="page-kicker">Operate</span>
        <h1>Overview</h1>
        <p className="page-subtitle">Tasks, traces, cost and policy across every agent in the selected window.</p>
      </div>
      <TimeRangePicker value={range} onChange={onRangeChange} idPrefix="overview-range" />
    </header>
  );
}
