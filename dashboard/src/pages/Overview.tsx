import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { LIVE_REFETCH_INTERVAL_MS, useOverview } from "../api/hooks";
import type { Overview as OverviewData } from "../api/types";
import { EmptyState } from "../components/EmptyState";
import { ErrorState } from "../components/ErrorState";
import { LoadingState } from "../components/LoadingState";
import { StatCard } from "../components/StatCard";
import { StatusBadge } from "../components/StatusBadge";
import { TimeRangePicker } from "../components/TimeRangePicker";
import { formatCost, formatDuration, formatNumber, formatPercent, formatTime } from "../lib/format";
import { taskStatusTone } from "../lib/status";
import { DEFAULT_TIME_RANGE, toApiWindow, type TimeRange } from "../lib/time";

const SERIES = {
  succeeded: "#4f9d69",
  failed: "#d64545",
  unknown: "#8a8f98",
  cost: "#4a7ebb",
} as const;

/** Dashboard landing page: headline numbers, trends and recent failures. */
export function Overview() {
  const [range, setRange] = useState<TimeRange>(DEFAULT_TIME_RANGE);
  const query = useOverview(toApiWindow(range), { refetchInterval: LIVE_REFETCH_INTERVAL_MS });
  const data = query.data;

  const taskChart = useMemo(
    () =>
      (data?.tasks_over_time ?? []).map((bucket) => ({
        label: formatTime(bucket.bucket),
        succeeded: bucket.succeeded,
        failed: bucket.failed,
        unknown: bucket.unknown,
      })),
    [data],
  );

  const costChart = useMemo(
    () =>
      (data?.cost_over_time ?? []).map((bucket) => ({
        label: formatTime(bucket.bucket),
        cost_usd: bucket.cost_usd,
      })),
    [data],
  );

  const agentCostChart = useMemo(
    () =>
      (data?.top_agents_by_cost ?? []).map((bucket) => ({
        key: bucket.key,
        cost_usd: bucket.cost_usd,
      })),
    [data],
  );

  const failingChart = useMemo(
    () =>
      (data?.top_failing_agents ?? []).map((agent) => ({
        name: agent.name,
        errors: agent.errors,
        error_rate: agent.error_rate,
      })),
    [data],
  );

  if (query.isPending) {
    return (
      <div className="page">
        <PageHeader range={range} onRangeChange={setRange} />
        <LoadingState label="Loading overview…" />
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

      <section className="stat-grid" aria-label="Summary">
        <StatCard label="Tasks" value={formatNumber(overview?.tasks_total)} />
        <StatCard label="Succeeded" value={formatNumber(overview?.tasks_succeeded)} tone="good" />
        <StatCard label="Failed" value={formatNumber(overview?.tasks_failed)} tone="bad" />
        <StatCard label="Error rate" value={formatPercent(overview?.error_rate)} />
        <StatCard label="p50 task duration" value={formatDuration(overview?.p50_task_duration_ms)} />
        <StatCard label="p95 task duration" value={formatDuration(overview?.p95_task_duration_ms)} />
        <StatCard label="Total cost" value={formatCost(overview?.total_cost_usd)} />
        <StatCard label="Active agents" value={formatNumber(overview?.active_agents)} />
        <StatCard label="Spans" value={formatNumber(overview?.spans_total)} />
      </section>

      <section className="panel" aria-labelledby="tasks-over-time">
        <h2 id="tasks-over-time">Tasks over time</h2>
        {taskChart.length === 0 ? (
          <EmptyState title="No tasks in this window" description="Nothing was recorded for the selected range." />
        ) : (
          <div className="chart-frame">
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={taskChart} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="label" tick={{ fontSize: 12 }} />
                <YAxis allowDecimals={false} tick={{ fontSize: 12 }} />
                <Tooltip />
                <Legend />
                <Bar dataKey="succeeded" stackId="tasks" fill={SERIES.succeeded} name="succeeded" />
                <Bar dataKey="failed" stackId="tasks" fill={SERIES.failed} name="failed" />
                <Bar dataKey="unknown" stackId="tasks" fill={SERIES.unknown} name="unknown" />
              </BarChart>
            </ResponsiveContainer>
          </div>
        )}
      </section>

      <div className="panel-row">
        <section className="panel" aria-labelledby="cost-over-time">
          <h2 id="cost-over-time">Cost over time</h2>
          {costChart.length === 0 ? (
            <EmptyState title="No cost data in this window" />
          ) : (
            <div className="chart-frame">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={costChart} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
                  <CartesianGrid strokeDasharray="3 3" vertical={false} />
                  <XAxis dataKey="label" tick={{ fontSize: 12 }} />
                  <YAxis tick={{ fontSize: 12 }} tickFormatter={(value: number) => formatCost(value)} />
                  <Tooltip
                    formatter={(value) => formatCost(typeof value === "number" ? value : null)}
                  />
                  <Bar dataKey="cost_usd" fill={SERIES.cost} name="cost" />
                </BarChart>
              </ResponsiveContainer>
            </div>
          )}
        </section>

        <section className="panel" aria-labelledby="top-agents-cost">
          <h2 id="top-agents-cost">Top agents by cost</h2>
          {agentCostChart.length === 0 ? (
            <EmptyState title="No agent cost data" />
          ) : (
            <div className="chart-frame">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart
                  data={agentCostChart}
                  layout="vertical"
                  margin={{ top: 8, right: 24, bottom: 8, left: 24 }}
                >
                  <CartesianGrid strokeDasharray="3 3" horizontal={false} />
                  <XAxis type="number" tick={{ fontSize: 12 }} tickFormatter={(value: number) => formatCost(value)} />
                  <YAxis type="category" dataKey="key" width={120} tick={{ fontSize: 12 }} />
                  <Tooltip
                    formatter={(value) => formatCost(typeof value === "number" ? value : null)}
                  />
                  <Bar dataKey="cost_usd" fill={SERIES.cost} name="cost" />
                </BarChart>
              </ResponsiveContainer>
            </div>
          )}
        </section>
      </div>

      <div className="panel-row">
        <section className="panel" aria-labelledby="top-failing-agents">
          <h2 id="top-failing-agents">Top failing agents</h2>
          {failingChart.length === 0 ? (
            <EmptyState title="No agent failures in this window" />
          ) : (
            <div className="chart-frame">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart
                  data={failingChart}
                  layout="vertical"
                  margin={{ top: 8, right: 24, bottom: 8, left: 24 }}
                >
                  <CartesianGrid strokeDasharray="3 3" horizontal={false} />
                  <XAxis type="number" allowDecimals={false} tick={{ fontSize: 12 }} />
                  <YAxis type="category" dataKey="name" width={120} tick={{ fontSize: 12 }} />
                  <Tooltip
                    formatter={(value, name) =>
                      name === "errors" ? formatNumber(Number(value)) : formatPercent(Number(value))
                    }
                  />
                  <Bar dataKey="errors" fill={SERIES.failed} name="errors" />
                </BarChart>
              </ResponsiveContainer>
            </div>
          )}
        </section>

        <section className="panel" aria-labelledby="recent-failed-tasks">
          <h2 id="recent-failed-tasks">Recent failed tasks</h2>
          {overview && overview.recent_failed_tasks.length > 0 ? (
            <ul className="task-list">
              {overview.recent_failed_tasks.map((task) => (
                <li key={task.task_id} className="task-list-item">
                  <Link to={`/tasks/${encodeURIComponent(task.task_id)}`}>{task.task_id}</Link>
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
            <EmptyState title="No failed tasks" description="Nothing failed in the selected window." />
          )}
        </section>
      </div>
    </div>
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
      <div>
        <h1>Overview</h1>
        <p className="page-subtitle">What happened in the Kyurem gateway.</p>
      </div>
      <TimeRangePicker value={range} onChange={onRangeChange} idPrefix="overview-range" />
    </header>
  );
}
