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
import { useCosts } from "../api/hooks";
import type { CostGroupBy } from "../api/types";
import { CostTable } from "../components/CostTable";
import { EmptyState } from "../components/EmptyState";
import { ErrorState } from "../components/ErrorState";
import { TableSkeleton } from "../components/Skeleton";
import {
  DashboardCard,
  DashboardChartTooltip,
  DetailHeader,
  DetailTag,
} from "../components/watermelon/capitalio-dashboard/components/capitalio/shared";
import { TimeRangePicker } from "../components/TimeRangePicker";
import { downloadCsv, toCsv } from "../lib/csv";
import { formatCost, formatNumber } from "../lib/format";
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

/** Build a conic-gradient ring from the cost shares (no SVG, no a11y noise). */
function buildConicGradient(rows: Array<{ value: number }>): string {
  const totalValue = rows.reduce((sum, row) => sum + row.value, 0);
  if (totalValue <= 0) return "var(--muted)";
  let cursor = 0;
  const stops = rows.map((row, index) => {
    const start = cursor;
    cursor += (row.value / totalValue) * 100;
    return `${DONUT_COLORS[index % DONUT_COLORS.length]} ${start}% ${cursor}%`;
  });
  return `conic-gradient(${stops.join(", ")})`;
}

const MONTHLY_BUDGET_USD = Number(import.meta.env.VITE_KYUREM_MONTHLY_BUDGET_USD ?? 500);

/** Cost and token report with grouping and CSV export. */
export function Costs() {
  const [range, setRange] = useState<TimeRange>(DEFAULT_TIME_RANGE);
  const [groupBy, setGroupBy] = useState<CostGroupBy>("agent");
  const apiWindow = toApiWindow(range);
  const query = useCosts(apiWindow, groupBy);

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

  const total = report?.total_usd ?? 0;
  const budgetRatio = MONTHLY_BUDGET_USD > 0 ? Math.min(1, total / MONTHLY_BUDGET_USD) : 0;

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
    <div className="page">
      <header className="page-header">
        <div className="page-title-pair">
          <span className="page-kicker">Manage</span>
          <h1>Costs</h1>
          <p className="page-subtitle">What the selected window cost — and who spent it.</p>
        </div>
        <TimeRangePicker value={range} onChange={setRange} idPrefix="costs-range" />
      </header>

      <section className="filter-toolbar" aria-label="Cost controls">
        <div className="field" style={{ flexGrow: 0, minWidth: 150 }}>
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
        <div className="field field-end" style={{ flexGrow: 0 }}>
          <button
            type="button"
            className="button button-primary"
            onClick={exportCsv}
            disabled={!report || report.buckets.length === 0}
          >
            Export CSV
          </button>
        </div>
      </section>

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
          <DashboardCard aria-label="Monthly budget">
            <DetailHeader title="Monthly budget" subtitle="Window spend vs the configured budget">
              <DetailTag color="var(--chart-1)">
                {formatCost(total)} of {formatCost(MONTHLY_BUDGET_USD)}
              </DetailTag>
            </DetailHeader>
            <div className="budget-wrap" role="progressbar" aria-valuenow={Math.round(budgetRatio * 100)} aria-valuemin={0} aria-valuemax={100} aria-label="Budget used">              <div className="budget-track">
                <div className="budget-fill" style={{ width: `${budgetRatio * 100}%` }} />
              </div>
              <div className="budget-labels">
                <span>{Math.round(budgetRatio * 100)}% used</span>
                <span>{formatCost(Math.max(0, MONTHLY_BUDGET_USD - total))} left</span>
              </div>
            </div>
          </DashboardCard>

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
                          <stop offset="0%" stopColor="var(--primary)" stopOpacity={0.9} />
                          <stop offset="100%" stopColor="var(--primary)" stopOpacity={0.45} />
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

            <DashboardCard aria-label="Share of spend">
              <DetailHeader title="Share of spend" subtitle="Top groups by cost" />
              {donutData.length === 0 ? (
                <EmptyState title="No cost data for this window" icon="coins" />
              ) : (
                <>
                  <div className="donut-ring" aria-hidden="true">
                    <div
                      className="donut-ring-arc"
                      style={{ background: buildConicGradient(donutData) }}
                    />
                    <div className="donut-ring-hole">
                      <span className="donut-ring-value">{formatCost(total)}</span>
                      <span className="donut-ring-label">spend</span>
                    </div>
                  </div>
                  <div className="donut-legend">
                    {donutData.slice(0, 6).map((entry, i) => (
                      <div key={entry.name} className="donut-legend-row">
                        <span className="donut-swatch" style={{ background: DONUT_COLORS[i % DONUT_COLORS.length] }} aria-hidden="true" />
                        {entry.name}
                        <span className="mono">{formatCost(entry.value)}</span>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </DashboardCard>
          </div>

          <DashboardCard aria-label="Breakdown" className="overflow-hidden p-0">
            <div className="p-4 pb-0">
              <DetailHeader title="Breakdown" subtitle={`Per ${groupBy}, with tokens and spans`} />
            </div>
            <div className="p-3">
              <CostTable report={report} groupLabel={groupBy} />
            </div>
          </DashboardCard>

          {groupBy === "task" ? (
            <DashboardCard aria-label="Tasks in this report">
              <DetailHeader title="Tasks in this report" subtitle="Jump straight to the trace" />
              <ul className="task-list">
                {report.buckets.map((bucket) => (
                  <li key={bucket.key} className="task-list-item">
                    <Link to={`/tasks/${encodeURIComponent(bucket.key)}`}>{bucket.key}</Link>
                    <span className="task-list-meta">
                      <span>{formatCost(bucket.cost_usd)}</span>
                      <span>{formatNumber(bucket.spans)} spans</span>
                    </span>
                  </li>
                ))}
              </ul>
            </DashboardCard>
          ) : (
            <p className="muted">
              {formatNumber(report.spans)} spans counted · {formatNumber(report.unknown_spans)} spans without usage
              data. Switch the grouping to “Task” to see the same numbers per task.
            </p>
          )}
        </>
      )}
    </div>
  );
}
