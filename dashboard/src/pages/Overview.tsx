import { memo, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Activity, ArrowUpRight, Clock3, Coins, Layers3, ShieldAlert, type LucideIcon } from "lucide-react";
import "./overview.css";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { LIVE_REFETCH_INTERVAL_MS, useCosts, useOverview, useSettings, useTasks } from "../api/hooks";
import type { Overview as OverviewData } from "../api/types";
import {
  DashboardCard,
  DashboardChartTooltip,
  DetailHeader,
  DetailTag,
} from "../components/watermelon/capitalio-dashboard/components/capitalio/shared";
import { EmptyState } from "../components/EmptyState";
import { ErrorState } from "../components/ErrorState";
import { LatencyHistogram } from "../components/latency-histogram";
import { SinceYouLeft } from "../components/since-you-left";
import { Sparkline } from "../components/Sparkline";
import { OverviewSkeleton } from "../components/Skeleton";
import { StatusBadge } from "../components/StatusBadge";
import { TimeRangePicker } from "../components/TimeRangePicker";
import { useCountUp } from "../lib/useCountUp";
import { formatCost, formatDuration, formatNumber, formatPercent, formatTime, formatTokens, formatUptime } from "../lib/format";
import { taskStatusTone } from "../lib/status";
import { DEFAULT_TIME_RANGE, toApiWindow, type TimeRange } from "../lib/time";

/** Dashboard landing page: headline KPIs, throughput and recent failures. */
export function Overview() {
  const [range, setRange] = useState<TimeRange>(DEFAULT_TIME_RANGE);
  const query = useOverview(toApiWindow(range), { refetchInterval: LIVE_REFETCH_INTERVAL_MS });
  const data = query.data;
  const costs = useCosts(toApiWindow(range), "agent");
  const tokenHint =
    costs.data?.input_tokens != null && costs.data.output_tokens != null
      ? `in ${formatTokens(costs.data.input_tokens)} / out ${formatTokens(costs.data.output_tokens)} tokens`
      : "tokens unknown";

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


  const recentDurations = useTasks({ limit: 100 }, { refetchInterval: LIVE_REFETCH_INTERVAL_MS });
  const latencyBins = useMemo(() => {
    const durations = (recentDurations.data?.items ?? [])
      .map((t) => t.duration_ms)
      .filter((d) => Number.isFinite(d) && d >= 0);
    if (durations.length === 0) return { bins: [] as number[], binSize: 50 };
    const max = Math.max(...durations);
    const binSize = Math.max(1, Math.ceil(max / 40));
    const count = Math.ceil((max + 1) / binSize);
    const bins = new Array<number>(count).fill(0);
    for (const d of durations) bins[Math.min(count - 1, Math.floor(d / binSize))] += 1;
    return { bins, binSize };
  }, [recentDurations.data]);

  const successSeries = useMemo(
    () => taskChart.map((b) => (b.total === 0 ? 0 : (b.succeeded / b.total) * 100)),
    [taskChart],
  );
  const failedSeries = useMemo(() => taskChart.map((b) => b.failed), [taskChart]);
  const errorRateSeries = useMemo(
    () => taskChart.map((b) => (b.total === 0 ? 0 : (b.failed / b.total) * 100)),
    [taskChart],
  );
  const topCostRows = useMemo<RankedRow[]>(() => {
    const buckets = data?.top_agents_by_cost ?? [];
    const max = maxBy(buckets, (b) => b.cost_usd ?? 0);
    return buckets.slice(0, 5).map((bucket) => ({
      key: bucket.key,
      display: formatCost(bucket.cost_usd),
      ratio: max,
      value: bucket.cost_usd ?? 0,
      accent: true,
    }));
  }, [data]);
  const topFailingRows = useMemo<RankedRow[]>(() => {
    const agents = data?.top_failing_agents ?? [];
    const max = maxBy(agents, (a) => a.errors);
    const failedTotal = data?.tasks_failed ?? 0;
    return agents.slice(0, 5).map((agent) => ({
      key: agent.name,
      display: `${formatNumber(agent.errors)} · ${formatPercent(agent.error_rate)}`,
      ratio: max,
      value: agent.errors,
      bad: true,
      sub: failedTotal > 0 ? `${formatPercent(agent.errors / (failedTotal || 1))} of failures` : undefined,
    }));
  }, [data]);

  if (query.isPending) {
    return (
      <div className="page overview-page">
        <PageHeader range={range} onRangeChange={setRange} />
        <OverviewSkeleton />
      </div>
    );
  }

  if (query.isError) {
    return (
      <div className="page overview-page">
        <PageHeader range={range} onRangeChange={setRange} />
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      </div>
    );
  }

  const overview: OverviewData | undefined = data;

  return (
    <div className="page overview-page">
      <PageHeader range={range} onRangeChange={setRange} />

      <section className="kpi-grid" aria-label="Key indicators">
        <KpiCard
          label="Throughput"
          icon={Activity}
          value={formatNumber(overview?.tasks_total)}
          numeric={overview?.tasks_total ?? null}
          format={(v) => formatNumber(Math.round(v))}
          pill={<DeltaPill change={halfChange(throughputTotals)} suffix="vs 1st half" tone="neutral" />}
          hint={`${formatNumber(overview?.tasks_succeeded)} succeeded`}
          sparkValues={throughputTotals}
        />
        <KpiCard
          label="Error rate"
          icon={ShieldAlert}
          value={formatPercent(overview?.error_rate)}
          numeric={
            overview?.error_rate === null || overview?.error_rate === undefined
              ? null
              : overview.error_rate * 100
          }
          format={(v) => `${v.toFixed(1)}%`}
          pill={<DeltaPill change={halfChange(failedSeries)} suffix="vs 1st half" tone="danger" />}
          hint={`${formatNumber(overview?.tasks_failed)} failed`}
          sparkValues={errorRateSeries}
          sparkStroke="var(--danger)"
        />
        <KpiCard
          label="p95 latency"
          icon={Clock3}
          value={formatDuration(overview?.p95_task_duration_ms)}
          numeric={overview?.p95_task_duration_ms ?? null}
          format={(v) => formatDuration(v)}
          hint={`p50 ${formatDuration(overview?.p50_task_duration_ms)}`}
          sparkValues={[]}
        />
        <KpiCard
          label="Active agents"
          icon={Layers3}
          value={formatNumber(overview?.active_agents)}
          numeric={overview?.active_agents ?? null}
          format={(v) => formatNumber(Math.round(v))}
          pill={<span className="pill pill-info">live</span>}
          hint={`${formatNumber(overview?.spans_total)} spans`}
          sparkValues={[]}
        />
        <KpiCard
          label="Cost"
          icon={Coins}
          value={formatCost(overview?.total_cost_usd)}
          numeric={overview?.total_cost_usd ?? null}
          format={(v) =>
            formatCost(overview?.total_cost_usd === null || overview?.total_cost_usd === undefined ? null : v)
          }
          pill={<DeltaPill change={halfChange(spendSeries)} suffix="vs 1st half" tone="warning" />}
          hint={`Selected window · ${tokenHint}`}
          sparkValues={spendSeries}
        />
      </section>

      <div className="overview-panels">
        <DashboardCard aria-label="Throughput" className="overview-panel-main">
          <DetailHeader title="Traffic overview" subtitle="Task activity over the selected window">
            <Link className="overview-card-link" to="/tasks">View tasks <ArrowUpRight size={14} /></Link>
          </DetailHeader>
          <div className="overview-chart-summary">
            <div><strong>{formatNumber(overview?.tasks_total)}</strong><span>total tasks</span></div>
            <div className="overview-chart-legend"><span><i />All tasks</span><span><i />Succeeded</span></div>
          </div>
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
                      <stop offset="0%" stopColor="var(--chart-1)" stopOpacity={0.2} />
                      <stop offset="100%" stopColor="var(--chart-1)" stopOpacity={0} />
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
                        colors={{ total: "var(--chart-1)", succeeded: "var(--chart-2)" }}
                        names={{ total: "tasks", succeeded: "succeeded" }}
                      />
                    }
                  />
                  <Area
                    type="monotone"
                    dataKey="total"
                    name="requests"
                    stroke="var(--chart-1)"
                    strokeWidth={2}
                    fill="url(#tputFill)"
                    dot={false}
                    activeDot={{ r: 4, fill: "var(--chart-1)", stroke: "var(--card)" }}
                  />
                  <Area
                    type="monotone"
                    dataKey="succeeded"
                    name="succeeded"
                    stroke="var(--chart-2)"
                    strokeWidth={2}
                    fill="transparent"
                    dot={false}
                    activeDot={{ r: 4, fill: "var(--chart-2)", stroke: "var(--card)" }}
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          )}
          </DashboardCard>

        <DashboardCard aria-label="Top agents" className="overview-panel-side flex flex-col">
          <DetailHeader title="Top agents" subtitle="Ranked by spend in this window">
            <span className="overview-section-icon"><Coins size={16} /></span>
          </DetailHeader>
          {(data?.top_agents_by_cost?.length ?? 0) === 0 ? (
            <EmptyState title="No agent cost data" icon="coins" />
          ) : (
            <>
              <RankedBars rows={topCostRows} />
              <div className="card-footer">
                <span className="count-meta">{formatCost(overview?.total_cost_usd)} total</span>
                <Link className="link-inline" to="/costs">View all</Link>
              </div>
            </>
          )}
        </DashboardCard>
      </div>

      <div className="overview-panels overview-panels-secondary">
        <DashboardCard aria-label="Top failing agents" className="overview-panel-side">
          <DetailHeader title="Top failing agents" subtitle="Where the errors concentrate">
            <DetailTag color="var(--destructive)">{formatNumber(overview?.tasks_failed)} failures</DetailTag>
          </DetailHeader>
          {(data?.top_failing_agents?.length ?? 0) === 0 ? (
            <EmptyState title="No agent failures in this window" icon="check" />
          ) : (
            <>
              <RankedBars rows={topFailingRows} />
              <div className="card-footer">
                <span className="count-meta">
                  {formatNumber(overview?.tasks_failed)} failed tasks
                </span>
                <Link className="link-inline" to="/agents">View all agents</Link>
              </div>
            </>
          )}
        </DashboardCard>

        <DashboardCard aria-label="Recent failed tasks" className="overview-panel-main">
          <DetailHeader title="Recent failed tasks" subtitle="Newest failures first">
            <Link className="link-inline" to="/tasks">All tasks</Link>
          </DetailHeader>
          {overview && overview.recent_failed_tasks.length > 0 ? (
            <ul className="task-list">
              {overview.recent_failed_tasks.map((task) => (
                <li key={task.task_id} className="task-list-item">
                  <Link className="task-id-link min-w-0 break-all" to={`/tasks/${encodeURIComponent(task.task_id)}`} title={task.task_id}>
                    {task.task_id}
                  </Link>
                  <span className="task-list-meta">
                    <StatusBadge label={task.status} tone={taskStatusTone(task.status)} />
                    <span>{task.root_caller ?? "unknown"}</span>
                    <span className="mono">{formatDuration(task.duration_ms)}</span>
                    <span className="mono">{formatCost(task.cost_usd)}</span>
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

      <div className="overview-panels">
        <LatencyHistogram
          className="overview-panel-main h-full min-w-0 max-w-none"
          bins={latencyBins.bins}
          binSize={latencyBins.binSize}
          caption={`Bucketed client-side from the latest ${recentDurations.data?.items.length ?? 0} task durations · markers are window p50/p95`}
          markerValues={[overview?.p50_task_duration_ms ?? 0, overview?.p95_task_duration_ms ?? 0]}
          title="Task latency"
        />
        <SinceYouLeft
          className="overview-panel-side h-full min-w-0 max-w-none"
          metrics={[
            { id: "requests", label: "Requests", good: "up", format: (v) => formatNumber(Math.round(v)), series: { visit: throughputTotals } },
            { id: "success", label: "Success rate", good: "up", format: (v) => `${v.toFixed(1)}%`, series: { visit: successSeries } },
            { id: "failed", label: "Failed tasks", good: "down", format: (v) => formatNumber(Math.round(v)), series: { visit: failedSeries } },
            { id: "spend", label: "Spend (USD)", good: "down", format: (v) => formatCost(v), series: { visit: spendSeries } },
          ]}
          periods={[
            {
              id: "visit",
              label: "This window",
              caption: "Compare the start and end of this window",
            },
          ]}
          title="Window comparison"
        />
      </div>
      <GatewayHealthStrip />
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

const DeltaPill = memo(function DeltaPill({
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
});

/**
 * One-line gateway posture strip: reachability, version, uptime, storage
 * driver and telemetry export state. Renders placeholders while settings
 * load and only marks the gateway unreachable when the request failed.
 */
function GatewayHealthStrip() {
  const query = useSettings();
  const settings = query.data;

  return (
    <section
      aria-label="Gateway health"
      className="overview-health flex flex-wrap items-center gap-x-5 gap-y-2 text-sm"
    >
      <span className="text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
        Gateway
      </span>
      {query.isError ? (
        <StatusBadge label="gateway unreachable" tone="bad" />
      ) : !settings ? (
        <>
          <StatusBadge label="checking gateway" tone="neutral" />
          <span aria-hidden="true" className="h-3 w-28 animate-pulse rounded bg-muted motion-reduce:animate-none" />
          <span aria-hidden="true" className="h-3 w-16 animate-pulse rounded bg-muted motion-reduce:animate-none" />
          <span aria-hidden="true" className="h-3 w-24 animate-pulse rounded bg-muted motion-reduce:animate-none" />
          <span aria-hidden="true" className="h-3 w-20 animate-pulse rounded bg-muted motion-reduce:animate-none" />
        </>
      ) : (
        <>
          <StatusBadge label="gateway reachable" tone="good" />
          <DetailTag>v{settings.version}</DetailTag>
          {settings.uptime_seconds != null ? (
            <span className="text-muted-foreground">
              uptime{" "}
              <span className="mono text-foreground">{formatUptime(settings.uptime_seconds)}</span>
            </span>
          ) : null}
          <span className="text-muted-foreground">
            storage{" "}
            <span className="mono text-foreground">{settings.storage.driver}</span>
          </span>
          <span className="flex items-center gap-2 text-muted-foreground">
            telemetry
            <StatusBadge
              label={settings.telemetry.exporting ? "on" : "off"}
              tone={settings.telemetry.exporting ? "good" : "warn"}
            />
          </span>
        </>
      )}
    </section>
  );
}

function KpiCard({
  label, icon: Icon, value, numeric, format, pill, hint, sparkValues, sparkStroke,
}: {
  label: string;
  icon: LucideIcon;
  value: string;
  numeric: number | null;
  format?: (v: number) => string;
  pill?: React.ReactNode;
  hint?: string;
  sparkValues: number[];
  sparkStroke?: string;
}) {
  const animated = useCountUp(numeric ?? Number.NaN);
  const display = numeric === null || !Number.isFinite(numeric) || !format ? value : format(animated);
  return (
    <div className="card kpi-card overview-kpi">
      <div className="overview-kpi-top"><span className="overview-metric-icon"><Icon size={14} /></span><span className="kpi-label">{label}</span></div>
      <div className="overview-kpi-reading">
        <span className="kpi-value" title={display}>{display}</span>
        {sparkValues.length >= 2 ? <Sparkline values={sparkValues} stroke={sparkStroke} /> : null}
      </div>
      <div className="kpi-foot">{pill}</div>
      {hint ? <span className="kpi-hint">{hint}</span> : null}
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
  /** Optional secondary line, e.g. share of total failures. */
  sub?: string;
}
const RankedBars = memo(function RankedBars({ rows }: { rows: RankedRow[] }) {
  const max = rows.reduce((m, r) => Math.max(m, r.ratio), 0) || 1;
  return (
    <ol className="ranked-list">
      {rows.map((row, index) => (
        <li key={row.key} className={`ranked-row${index === 0 ? " ranked-row-top" : ""}`}>
          <span className="ranked-name">
            <span className="avatar" aria-hidden="true">
              {row.key.slice(0, 2).toUpperCase()}
            </span>
            <span className="min-w-0 truncate" title={row.key}>{row.key}</span>
          </span>
          <span className="ranked-value min-w-0 break-words text-right">
            {row.display}
            {row.sub ? <span className="ranked-sub mono"> · {row.sub}</span> : null}
          </span>
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
});

function PageHeader({ range, onRangeChange }: {
  range: TimeRange;
  onRangeChange: (range: TimeRange) => void;
}) {
  return (
    <header className="page-header overview-header">
      <div className="page-title-pair">
        <span className="page-kicker">Workspace analytics</span>
        <h1>Overview</h1>
        <p className="page-subtitle">A clear view of your agents, traffic and spend.</p>
      </div>
      <TimeRangePicker value={range} onChange={onRangeChange} idPrefix="overview-range" />
    </header>
  );
}
