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
import { ErrorState } from "../components/ErrorState";
import { LoadingState } from "../components/LoadingState";
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
        cost_usd: bucket.cost_usd,
        spans: bucket.spans,
      })),
    [report],
  );

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
        <div>
          <h1>Costs</h1>
          <p className="page-subtitle">What the selected window cost — and who spent it.</p>
        </div>
        <TimeRangePicker value={range} onChange={setRange} idPrefix="costs-range" />
      </header>

      <section className="panel" aria-labelledby="cost-controls">
        <h2 id="cost-controls" className="sr-only">
          Grouping
        </h2>
        <div className="filter-bar">
          <div className="field">
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
          <div className="field field-end">
            <button
              type="button"
              className="button button-primary"
              onClick={exportCsv}
              disabled={!report || report.buckets.length === 0}
            >
              Export CSV
            </button>
          </div>
        </div>
      </section>

      {query.isPending ? (
        <LoadingState label="Loading costs…" />
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : !report ? (
        <LoadingState />
      ) : (
        <>
          <section className="panel" aria-labelledby="cost-chart">
            <h2 id="cost-chart">Cost per {groupBy}</h2>
            {chartData.length === 0 ? (
              <p className="muted">No cost data for this window.</p>
            ) : (
              <div className="chart-frame">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={chartData} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" vertical={false} />
                    <XAxis dataKey="key" tick={{ fontSize: 11 }} interval={0} angle={-20} height={60} />
                    <YAxis tick={{ fontSize: 12 }} tickFormatter={(value: number) => formatCost(value)} />
                    <Tooltip
                      formatter={(value) => formatCost(typeof value === "number" ? value : null)}
                    />
                    <Bar dataKey="cost_usd" fill="#4a7ebb" name="cost" />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            )}
          </section>

          <section className="panel" aria-labelledby="cost-table">
            <h2 id="cost-table">Breakdown</h2>
            <CostTable report={report} groupLabel={groupBy} />
          </section>

          {groupBy === "task" ? (
            <section className="panel" aria-labelledby="cost-task-links">
              <h2 id="cost-task-links">Tasks in this report</h2>
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
            </section>
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
