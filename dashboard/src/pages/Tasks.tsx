import { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { LIVE_REFETCH_INTERVAL_MS, useTasks } from "../api/hooks";
import type { Task, TaskStatus } from "../api/types";
import { DataTable, type DataTableColumn } from "../components/DataTable";
import { AgentAvatar, CopyButton } from "../components/CopyableId";
import { ErrorState } from "../components/ErrorState";
import { TableSkeleton } from "../components/Skeleton";
import {
  DashboardCard,
  DetailHeader,
  DetailTag,
} from "../components/watermelon/capitalio-dashboard/components/capitalio/shared";
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
  const [drawerTask, setDrawerTask] = useState<Task | null>(null);

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

  const rows = useMemo(() => query.data?.items ?? [], [query.data]);
  const total = query.data?.total ?? 0;
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const maxDuration = useMemo(
    () => rows.reduce((m, t) => Math.max(m, t.duration_ms ?? 0), 0),
    [rows],
  );

  const activeChips: Array<{ key: string; label: React.ReactNode; clear: () => void }> = [];
  if (taskId.trim() !== "") {
    activeChips.push({
      key: "task",
      label: (
        <>
          Task <strong>{taskId.trim()}</strong>
        </>
      ),
      clear: () => {
        setTaskId("");
        setPage(0);
      },
    });
  }
  if (status !== "") {
    activeChips.push({
      key: "status",
      label: (
        <>
          Status <strong>{status}</strong>
        </>
      ),
      clear: () => {
        setStatus("");
        setPage(0);
      },
    });
  }
  if (agent.trim() !== "") {
    activeChips.push({
      key: "agent",
      label: (
        <>
          Agent <strong>{agent.trim()}</strong>
        </>
      ),
      clear: () => {
        setAgent("");
        setPage(0);
      },
    });
  }

  const columns: DataTableColumn<Task>[] = [
    {
      key: "task_id",
      header: "Task",
      render: (task) => (
        <span className="task-id-cell">
          <Link
            className="task-link"
            to={`/tasks/${encodeURIComponent(task.task_id)}`}
            onClick={(event) => event.stopPropagation()}
          >
            {task.task_id}
          </Link>
          <CopyButton value={task.task_id} />
        </span>
      ),
    },
    {
      key: "root_caller",
      header: "Root caller",
      hideOnMobile: true,
      render: (task) => (
        <span className="agent-cell">
          <AgentAvatar name={task.root_caller ?? "unknown"} />
          {task.root_caller ?? "unknown"}
        </span>
      ),
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
      render: (task) => (
        <span className="duration-cell">
          <span className="duration-bar" aria-hidden="true">
            <span
              style={{
                width: `${maxDuration > 0 ? Math.max(4, ((task.duration_ms ?? 0) / maxDuration) * 100) : 4}%`,
              }}
            />
          </span>
          {formatDuration(task.duration_ms)}
        </span>
      ),
    },
    {
      key: "agents",
      header: "Agents",
      align: "right",
      hideOnMobile: true,
      render: (task) => formatNumber(task.agents.length),
    },
    {
      key: "cost",
      header: "Cost",
      align: "right",
      hideOnMobile: true,
      render: (task) => formatCost(task.cost_usd),
    },
    {
      key: "started_at",
      header: "Started",
      hideOnMobile: true,
      render: (task) => formatDateTime(task.started_at),
    },
  ];

  return (
    <div className="page">
      <header className="page-header">
        <div className="page-title-pair">
          <span className="page-kicker">Operate</span>
          <h1>Tasks</h1>
          <p className="page-subtitle">Every task the gateway proxied, newest first.</p>
        </div>
        <TimeRangePicker value={range} onChange={setRange} idPrefix="tasks-range" />
      </header>

      <section className="filter-toolbar" aria-label="Task filters">
        <div className="field">
          <label htmlFor="task-search">Task id</label>
          <input
            id="task-search"
            type="search"
            placeholder="Task id"
            value={taskId}
            onChange={(event) => {
              setTaskId(event.target.value);
              setPage(0);
            }}
          />
        </div>
        <div className="field" style={{ flexGrow: 0, minWidth: 150 }}>
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
            placeholder="Agent name"
            value={agent}
            onChange={(event) => {
              setAgent(event.target.value);
              setPage(0);
            }}
          />
        </div>
        {activeChips.length > 0 ? (
          <div className="chip-row" aria-label="Active filters">
            {activeChips.map((chip) => (
              <span key={chip.key} className="chip">
                {chip.label}
                <button
                  type="button"
                  onClick={chip.clear}
                  aria-label={`Clear ${chip.key} filter`}
                >
                  ×
                </button>
              </span>
            ))}
            <button
              type="button"
              className="button-link button"
              onClick={() => {
                setTaskId("");
                setStatus("");
                setAgent("");
                setPage(0);
              }}
            >
              Clear all
            </button>
          </div>
        ) : null}
      </section>

      {query.isPending ? (
        <DashboardCard aria-label="Tasks loading">
          <TableSkeleton rows={8} />
        </DashboardCard>
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <DashboardCard aria-label="Tasks" className="overflow-hidden p-0">
          <div className="p-4 pb-0">
            <DetailHeader title="Tasks" subtitle="Click a row to inspect it">
              <DetailTag>
                {formatNumber(total)} task{total === 1 ? "" : "s"} · page {page + 1} of {pageCount}
              </DetailTag>
            </DetailHeader>
          </div>
          <div className="p-3">
            <div className="table-wrapper" tabIndex={0} style={{ border: "none", borderRadius: 12 }}>
              <DataTable
                caption="Tasks seen by the Kyurem gateway"
                columns={columns}
                rows={rows}
                rowKey={(task) => task.task_id}
                onRowClick={(task) => setDrawerTask(task)}
                emptyTitle="No tasks match these filters"
                emptyDescription="Try a wider time range or clear the filters."
              />
            </div>
            <div className="pagination px-3 pb-1">
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
          </div>
        </DashboardCard>
      )}

      {drawerTask ? (
        <TaskDrawer
          task={drawerTask}
          onClose={() => setDrawerTask(null)}
          onOpenFull={() => navigate(`/tasks/${encodeURIComponent(drawerTask.task_id)}`)}
        />
      ) : null}
    </div>
  );
}

function TaskDrawer({
  task,
  onClose,
  onOpenFull,
}: {
  task: Task;
  onClose: () => void;
  onOpenFull: () => void;
}) {
  return (
    <>
      <div className="drawer-scrim" onClick={onClose} aria-hidden="true" />
      <aside className="drawer" role="dialog" aria-modal="true" aria-label={`Task ${task.task_id}`}>
        <div className="drawer-head">
          <div className="tab-pair">
            <strong className="mono">{task.task_id}</strong>
            <small>Trace {task.trace_id}</small>
          </div>
          <button type="button" className="icon-button" onClick={onClose} aria-label="Close task details">
            ✕
          </button>
        </div>
        <div className="badge-row">
          <StatusBadge label={task.status} tone={taskStatusTone(task.status)} />
        </div>
        <dl className="detail-grid">
          <div>
            <dt>Root caller</dt>
            <dd>{task.root_caller ?? "unknown"}</dd>
          </div>
          <div>
            <dt>Duration</dt>
            <dd className="mono">{formatDuration(task.duration_ms)}</dd>
          </div>
          <div>
            <dt>Cost</dt>
            <dd className="mono">{formatCost(task.cost_usd)}</dd>
          </div>
          <div>
            <dt>Agents</dt>
            <dd className="mono">{formatNumber(task.agents.length)}</dd>
          </div>
          <div>
            <dt>Started</dt>
            <dd>{formatDateTime(task.started_at)}</dd>
          </div>
        </dl>
        {task.error_message ? <p className="state-mono">{task.error_message}</p> : null}
        <div className="button-row">
          <button type="button" className="button button-primary" onClick={onOpenFull}>
            Open full trace
          </button>
          <Link className="button" to={`/tasks/${encodeURIComponent(task.task_id)}`}>
            Details page
          </Link>
        </div>
      </aside>
    </>
  );
}
