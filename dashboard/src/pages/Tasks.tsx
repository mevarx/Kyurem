import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { LIVE_REFETCH_INTERVAL_MS, useTasks } from "../api/hooks";
import type { Task, TaskStatus } from "../api/types";
import { DataTable, type DataTableColumn } from "../components/DataTable";
import { ErrorState } from "../components/ErrorState";
import { LoadingState } from "../components/LoadingState";
import { StatusBadge } from "../components/StatusBadge";
import { TimeRangePicker } from "../components/TimeRangePicker";
import { formatCost, formatDateTime, formatDuration, formatNumber } from "../lib/format";
import { taskStatusTone } from "../lib/status";
import { DEFAULT_TIME_RANGE, toApiWindow, type TimeRange } from "../lib/time";

const PAGE_SIZE = 50;

const STATUS_OPTIONS: Array<{ value: TaskStatus | ""; label: string }> = [
  { value: "", label: "All statuses" },
  { value: "succeeded", label: "succeeded" },
  { value: "failed", label: "failed" },
  { value: "unknown", label: "unknown" },
];

/** Searchable, filterable and paginated list of tasks. */
export function Tasks() {
  const navigate = useNavigate();
  const [range, setRange] = useState<TimeRange>(DEFAULT_TIME_RANGE);
  const [taskId, setTaskId] = useState("");
  const [status, setStatus] = useState<TaskStatus | "">("");
  const [agent, setAgent] = useState("");
  const [page, setPage] = useState(0);

  const apiWindow = toApiWindow(range);
  const query = useTasks(
    {
      from: apiWindow.from,
      to: apiWindow.to,
      status: status === "" ? undefined : status,
      agent: agent.trim() === "" ? undefined : agent.trim(),
      task_id: taskId.trim() === "" ? undefined : taskId.trim(),
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
    },
    { refetchInterval: LIVE_REFETCH_INTERVAL_MS },
  );

  const rows = query.data?.items ?? [];
  const total = query.data?.total ?? 0;
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const columns: DataTableColumn<Task>[] = [
    {
      key: "task_id",
      header: "Task",
      render: (task) => (
        <Link
          className="task-link"
          to={`/tasks/${encodeURIComponent(task.task_id)}`}
          onClick={(event) => event.stopPropagation()}
        >
          {task.task_id}
        </Link>
      ),
    },
    {
      key: "root_caller",
      header: "Root caller",
      render: (task) => task.root_caller ?? "unknown",
    },
    {
      key: "status",
      header: "Status",
      render: (task) => <StatusBadge label={task.status} tone={taskStatusTone(task.status)} />,
    },
    {
      key: "duration",
      header: "Duration",
      align: "right",
      render: (task) => formatDuration(task.duration_ms),
    },
    {
      key: "agents",
      header: "Agents",
      align: "right",
      render: (task) => formatNumber(task.agents.length),
    },
    {
      key: "cost",
      header: "Cost",
      align: "right",
      render: (task) => formatCost(task.cost_usd),
    },
    {
      key: "started_at",
      header: "Started",
      render: (task) => formatDateTime(task.started_at),
    },
  ];

  return (
    <div className="page">
      <header className="page-header">
        <div>
          <h1>Tasks</h1>
          <p className="page-subtitle">Every task the gateway proxied, newest first.</p>
        </div>
        <TimeRangePicker value={range} onChange={setRange} idPrefix="tasks-range" />
      </header>

      <section className="panel" aria-labelledby="task-filters">
        <h2 id="task-filters" className="sr-only">
          Filters
        </h2>
        <div className="filter-bar">
          <div className="field">
            <label htmlFor="task-search">Task id</label>
            <input
              id="task-search"
              type="search"
              placeholder="Exact id or prefix"
              value={taskId}
              onChange={(event) => {
                setTaskId(event.target.value);
                setPage(0);
              }}
            />
          </div>
          <div className="field">
            <label htmlFor="task-status">Status</label>
            <select
              id="task-status"
              value={status}
              onChange={(event) => {
                setStatus(event.target.value as TaskStatus | "");
                setPage(0);
              }}
            >
              {STATUS_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="task-agent">Agent</label>
            <input
              id="task-agent"
              type="search"
              placeholder="Caller or callee"
              value={agent}
              onChange={(event) => {
                setAgent(event.target.value);
                setPage(0);
              }}
            />
          </div>
        </div>
      </section>

      {query.isPending ? (
        <LoadingState label="Loading tasks…" />
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <section className="panel" aria-labelledby="task-table">
          <div className="panel-header">
            <h2 id="task-table">Tasks</h2>
            <span className="muted">
              {formatNumber(total)} task{total === 1 ? "" : "s"} · page {page + 1} of {pageCount}
            </span>
          </div>
          <DataTable
            caption="Tasks seen by the Kyurem gateway"
            columns={columns}
            rows={rows}
            rowKey={(task) => task.task_id}
            onRowClick={(task) => navigate(`/tasks/${encodeURIComponent(task.task_id)}`)}
            emptyTitle="No tasks match these filters"
            emptyDescription="Try a wider time range or clear the filters."
          />
          <div className="pagination">
            <button
              type="button"
              className="button"
              disabled={page === 0}
              onClick={() => setPage((current) => Math.max(0, current - 1))}
            >
              Previous
            </button>
            <button
              type="button"
              className="button"
              disabled={page + 1 >= pageCount}
              onClick={() => setPage((current) => current + 1)}
            >
              Next
            </button>
          </div>
        </section>
      )}
    </div>
  );
}
