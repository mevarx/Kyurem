import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { useCosts, useOverview } from "../api/hooks";
import type { CostGroupBy } from "../api/types";
import { CostTable } from "../components/CostTable";
import { CreditGauge } from "../components/credit-gauge";
import { DonutLegend } from "../components/donut-legend";
import { EmptyState } from "../components/EmptyState";
import { ErrorState } from "../components/ErrorState";
import { Skeleton, TableSkeleton } from "../components/Skeleton";
import {
  DashboardCard,
  DashboardChartTooltip,
  DetailHeader,
  DetailTag,
} from "../components/watermelon/capitalio-dashboard/components/capitalio/shared";
import { TimeRangePicker } from "../components/TimeRangePicker";
import { downloadCsv, toCsv } from "../lib/csv";
import { formatCost, formatNumber, formatTokens } from "../lib/format";
import { DEFAULT_TIME_RANGE, toApiWindow, type TimeRange } from "../lib/time";

const GROUP_OPTIONS: Array<{ value: CostGroupBy; label: string }> = [
  { value: "agent", label: "Agent" },
  { value: "model", label: "Model" },
  { value: "task", label: "Task" },
  { value: "window", label: "Time window" },
];

const DONUT_COLORS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
  "var(--muted-foreground)",
  "var(--text-subtle)",
];

const MONTHLY_BUDGET_USD = Number(import.meta.env.VITE_KYUREM_MONTHLY_BUDGET_USD ?? 500);

/** Cost and token report with grouping and CSV export. */
export function Costs() {
  const [range, setRange] = useState<TimeRange>(DEFAULT_TIME_RANGE);
  const [groupBy, setGroupBy] = useState<CostGroupBy>("agent");
  const apiWindow = toApiWindow(range);
  const query = useCosts(apiWindow, groupBy);
  const overviewQuery = useOverview(apiWindow);

  const report = query.data;

  const chartData = useMemo(
    () =>
      (report?.buckets ?? []).slice(0, 20).map((bucket) => ({
        key: bucket.key,
        cost_usd: bucket.cost_usd ?? 0,
        spans: bucket.spans,
      })),
    [report],
  );

  const donutData = useMemo(() => {
    const top = chartData.slice(0, 6);
    const rest = chartData.slice(6).reduce((sum, b) => sum + b.cost_usd, 0);
    const out = top.map((b) => ({ name: b.key, value: b.cost_usd }));
    if (rest > 0) out.push({ name: "Other", value: rest });
    return out;
  }, [chartData]);

  /** Aggregate overview window buckets into calendar-day spend for the gauge strip. */
  const dailySpend = useMemo(() => {
    const byDay = new Map<string, number>();
    for (const bucket of overviewQuery.data?.cost_over_time ?? []) {
      const day = new Date(bucket.bucket).toISOString().slice(0, 10);
      byDay.set(day, (byDay.get(day) ?? 0) + (bucket.cost_usd ?? 0));
    }
    const days = [...byDay.entries()].sort(([a], [b]) => (a < b ? -1 : 1));
    return {
      start: days.length > 0 ? new Date(`${days[0][0]}T00:00:00Z`) : undefined,
      values: days.map(([, value]) => value),
    };
  }, [overviewQuery.data]);

  const exportCsv = () => {
    if (!report) return;
    const rows: Array<Array<string | number | null | undefined>> = [
      ["group", "cost_usd", "input_tokens", "output_tokens", "spans", "spans_without_usage"],
      ...report.buckets.map((bucket) => [
        bucket.key,
        bucket.cost_usd,
        bucket.input_tokens,
        bucket.output_tokens,
        bucket.spans,
        bucket.unknown_spans,
      ]),
    ];
    downloadCsv(`kyurem-costs-${groupBy}.csv`, toCsv(rows));
  };

  return (
    <div className="page costs-page">
      <header className="page-header">
        <div className="page-title-pair">
          <span className="page-kicker">Manage</span>
          <h1>Costs</h1>
          <p className="page-subtitle">What the selected window cost — and who spent it.</p>
        </div>
        <div className="page-header-meta costs-page-controls">
          <div className="field costs-group-field">
            <label htmlFor="cost-group-by">Group by</label>
            <select
              id="cost-group-by"
              value={groupBy}
              onChange={(event) => setGroupBy(event.target.value as CostGroupBy)}
            >
              {GROUP_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
          <TimeRangePicker value={range} onChange={setRange} idPrefix="costs-range" />
          <button
            type="button"
            className="button button-primary costs-export-button"
            onClick={exportCsv}
            disabled={!report || report.buckets.length === 0}
          >
            Export CSV
          </button>
        </div>
      </header>

      {/* Totals sit above the controls: the headline number is the window cost. Errors are
          handled by ErrorState below, so no card may present a zeroed total here. */}
      {query.isError ? null : (
        <section className="stat-grid" aria-label="Cost totals" aria-busy={query.isPending}>
          {query.isPending ? (
            <>
              <div className="stat-card">
                <span className="stat-label">Total cost</span>
                <Skeleton style={{ height: 38, width: "70%" }} />
              </div>
              <div className="stat-card">
                <span className="stat-label">Input tokens</span>
                <Skeleton style={{ height: 22, width: "55%" }} />
              </div>
              <div className="stat-card">
                <span className="stat-label">Output tokens</span>
                <Skeleton style={{ height: 22, width: "55%" }} />
              </div>
              <div className="stat-card">
                <span className="stat-label">Unknown usage</span>
                <Skeleton style={{ height: 22, width: "80%" }} />
              </div>
              <span className="sr-only">Loading cost totals…</span>
            </>
          ) : (
            <>
              <div className="stat-card">
                <span className="stat-label">Total cost</span>
                <span
                  className="stat-value"
                  style={{ fontSize: "clamp(1.6rem, 2vw, 2.2rem)", lineHeight: 1.15, letterSpacing: "-0.02em", whiteSpace: "nowrap" }}
                >
                  {formatCost(report?.total_usd ?? null)}
                </span>
              </div>
              <div className="stat-card">
                <span className="stat-label">Input tokens</span>
                <span className="stat-value">{formatTokens(report?.input_tokens ?? null)}</span>
              </div>
              <div className="stat-card">
                <span className="stat-label">Output tokens</span>
                <span className="stat-value">{formatTokens(report?.output_tokens ?? null)}</span>
              </div>
              <div className="stat-card">
                <span className="stat-label">Unknown usage</span>
                {report === undefined ? (
                  <span className="stat-value">unknown</span>
                ) : report.unknown_spans > 0 ? (
                  <DetailTag color="var(--muted-foreground)">
                    <span className="mono">
                      {`${formatNumber(report.unknown_spans)} of ${formatNumber(report.spans)} spans without usage`}
                    </span>
                  </DetailTag>
                ) : (
                  <span className="stat-value">complete</span>
                )}
              </div>
            </>
          )}
        </section>
      )}

      {query.isPending ? (
        <DashboardCard aria-label="Costs loading">
          <TableSkeleton rows={6} />
        </DashboardCard>
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : !report ? (
        <EmptyState title="No cost data for this window" icon="coins" />
      ) : (
        <>
          <CreditGauge
            className="max-w-none! rounded-xl!"
            categories={chartData.map((bucket) => ({
              id: bucket.key,
              label: bucket.key,
              used: bucket.cost_usd,
            }))}
            daily={dailySpend.values}
            limit={MONTHLY_BUDGET_USD}
            periodStart={dailySpend.start}
            title="Monthly budget"
            unit="$"
            warnAt={0.8}
          />

          <div className="panel-row">
            <DashboardCard aria-label={`Cost per ${groupBy}`}>
              <DetailHeader title={`Cost per ${groupBy}`} subtitle="Top 20 groups in the window" />
              {chartData.length === 0 ? (
                <EmptyState title="No cost data for this window" icon="coins" />
              ) : (
                <div className="chart-frame">
                  <ResponsiveContainer width="100%" height="100%">
                    <BarChart data={chartData} margin={{ top: 8, right: 16, bottom: 4, left: 0 }}>
                      <defs>
                        <linearGradient id="costFill" x1="0" y1="0" x2="0" y2="1">
                          <stop offset="0%" stopColor="var(--chart-1)" stopOpacity={0.9} />
                          <stop offset="100%" stopColor="var(--chart-1)" stopOpacity={0.45} />
                        </linearGradient>
                      </defs>
                      <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="var(--border)" />
                      <XAxis
                        dataKey="key"
                        tick={{ fontSize: 11, fill: "var(--text-subtle)" }}
                        interval="preserveStartEnd"
                        minTickGap={40}
                        tickLine={false}
                        axisLine={false}
                        height={36}
                      />
                      <YAxis
                        tick={{ fontSize: 11, fill: "var(--text-subtle)" }}
                        width={60}
                        tickFormatter={(value: number) => formatCost(value)}
                        tickLine={false}
                        axisLine={false}
                      />
                      <Tooltip
                        cursor={false}
                        content={
                          <DashboardChartTooltip
                            colors={{ cost_usd: "var(--chart-1)" }}
                            names={{ cost_usd: "cost" }}
                            valueFormatter={(value) => formatCost(typeof value === "number" ? value : null)}
                          />
                        }
                      />
                      <Bar dataKey="cost_usd" fill="url(#costFill)" name="cost" radius={[4, 4, 0, 0]} maxBarSize={28} />
                    </BarChart>
                  </ResponsiveContainer>
                </div>
              )}
            </DashboardCard>

            {donutData.length === 0 ? (
              <DashboardCard aria-label="Share of spend">
                <DetailHeader title="Share of spend" subtitle="Top groups by cost" />
                <EmptyState title="No cost data for this window" icon="coins" />
              </DashboardCard>
            ) : (
              <DonutLegend
                caption={`By ${groupBy} · click a row to isolate it`}
                format={{ style: "currency", currency: "USD" }}
                slices={donutData.map((entry, i) => ({
                  id: entry.name,
                  label: entry.name,
                  value: entry.value,
                  color: DONUT_COLORS[i % DONUT_COLORS.length],
                }))}
                title="Share of spend"
                totalLabel="spend"
              />
            )}
          </div>

          <DashboardCard aria-label="Breakdown" className="overflow-hidden p-0">
            <div className="p-4 pb-0">
              <DetailHeader title="Breakdown" subtitle={`Per ${groupBy}, with tokens and spans`}>
                <DetailTag>{`${formatNumber(report.spans)} spans`}</DetailTag>
                <DetailTag color="var(--muted-foreground)">{`${formatNumber(report.unknown_spans)} unknown cost`}</DetailTag>
              </DetailHeader>
            </div>
            <div className="table-wrapper max-w-full p-3" tabIndex={0} style={{ border: "none", borderRadius: 12 }}>
              <CostTable report={report} groupLabel={groupBy} />
            </div>
          </DashboardCard>

          {groupBy === "task" ? (
            <DashboardCard aria-label="Tasks in this report">
              <DetailHeader title="Tasks in this report" subtitle="Jump straight to the trace" />
              <ul className="task-list">
                {report.buckets.map((bucket) => (
                  <li key={bucket.key} className="task-list-item">
                    <Link to={`/tasks/${encodeURIComponent(bucket.key)}`} className="mono">{bucket.key}</Link>
                    <span className="task-list-meta mono tabular">
                      <span>{formatCost(bucket.cost_usd)}</span>
                      <span>{formatNumber(bucket.spans)} spans</span>
                    </span>
                  </li>
                ))}
              </ul>
            </DashboardCard>
          ) : (
            <DashboardCard aria-label="Window totals">
              <DetailHeader title="Window totals" subtitle="Spans counted in this window" />
              <p className="muted mono tabular">
                {formatNumber(report.spans)} spans counted · {formatNumber(report.unknown_spans)} spans without usage
                data. Switch the grouping to “Task” to see the same numbers per task.
              </p>
            </DashboardCard>
          )}
        </>
      )}
    </div>
  );
}
