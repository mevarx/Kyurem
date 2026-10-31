import { Link, useParams } from "react-router-dom";
import { useTaskDetail } from "../api/hooks";
import type { Event } from "../api/types";
import { CostTable } from "../components/CostTable";
import { DataTable, type DataTableColumn } from "../components/DataTable";
import {
  DashboardCard,
  DetailHeader,
} from "../components/watermelon/capitalio-dashboard/components/capitalio/shared";
import { EmptyState } from "../components/EmptyState";
import { ErrorState } from "../components/ErrorState";
import { RootCausePanel } from "../components/RootCausePanel";
import { SpanTree } from "../components/SpanTree";
import { SpanWaterfall } from "../components/SpanWaterfall";
import { StatusBadge } from "../components/StatusBadge";
import { TableSkeleton } from "../components/Skeleton";
import { formatCost, formatDateTime, formatDuration, formatNumber } from "../lib/format";
import { taskStatusTone } from "../lib/status";

/** Everything the gateway knows about a single task. */
export function TaskDetail() {
  const { taskId = "" } = useParams<{ taskId: string }>();
  const query = useTaskDetail(taskId);

  if (query.isPending) {
    return (
      <div className="page">
        <BackLink />
        <DashboardCard aria-label="Task loading">
          <TableSkeleton rows={8} />
        </DashboardCard>
      </div>
    );
  }

  if (query.isError) {
    return (
      <div className="page">
        <BackLink />
        <ErrorState error={query.error} onRetry={() => void query.refetch()} title="Could not load task" />
      </div>
    );
  }

  const detail = query.data;
  if (!detail) {
    return (
      <div className="page">
        <BackLink />
        <EmptyState title="Task not found" description="The gateway returned no task for this id." />
      </div>
    );
  }

  const { task, spans, root_cause, cost, events } = detail;
  const affectedSpanIds = root_cause?.affected_span_ids ?? [];

  const eventColumns: DataTableColumn<Event>[] = [
    { key: "time", header: "Time", render: (event) => formatDateTime(event.time) },
    { key: "name", header: "Event", render: (event) => event.name },
    { key: "span_id", header: "Span", render: (event) => <code>{event.span_id}</code> },
    {
      key: "attributes",
      header: "Attributes",
      render: (event) => {
        const entries = Object.entries(event.attributes ?? {});
        if (entries.length === 0) return <span className="muted">none</span>;
        return (
          <ul className="event-attributes">
            {entries.map(([key, value]) => (
              <li key={key}>
                <code>{key}</code>: {value}
              </li>
            ))}
          </ul>
        );
      },
    },
  ];

  return (
    <div className="page">
      <BackLink />

      <header className="page-header">
        <div>
          <h1 className="mono">{task.task_id}</h1>
          <p className="page-subtitle">
            Trace <code>{task.trace_id}</code> · started {formatDateTime(task.started_at)}
          </p>
        </div>
        <StatusBadge label={task.status} tone={taskStatusTone(task.status)} />
      </header>

      <section className="stat-grid stat-grid-compact" aria-label="Task summary">
        <div className="stat-card">
          <span className="stat-label">Duration</span>
          <span className="stat-value">{formatDuration(task.duration_ms)}</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">Cost</span>
          <span className="stat-value">{formatCost(task.cost_usd)}</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">Agents</span>
          <span className="stat-value">{formatNumber(task.agents.length)}</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">Spans</span>
          <span className="stat-value">{formatNumber(task.span_count)}</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">Tokens</span>
          <span className="stat-value">
            {task.tokens_known ? "known" : "unknown"}
          </span>
        </div>
      </section>

      {task.error_message ? (
        <DashboardCard aria-label="Task error">
          <DetailHeader title="Error" />
          <p>
            <strong>{task.error_type ?? "unknown"}</strong>: {task.error_message}
          </p>
        </DashboardCard>
      ) : null}

      <RootCausePanel rootCause={root_cause} spans={spans} />

      <div className="panel-row">
        <DashboardCard aria-label="Span tree">
          <DetailHeader title="Span tree" />
          <SpanTree spans={spans} highlightSpanIds={affectedSpanIds} />
        </DashboardCard>

        <DashboardCard aria-label="Timeline">
          <DetailHeader title="Timeline" />
          <SpanWaterfall spans={spans} />
        </DashboardCard>
      </div>

      <DashboardCard aria-label="Cost breakdown">
        <DetailHeader title="Cost breakdown" />
        <CostTable report={cost} caption="Cost breakdown of this task" />
      </DashboardCard>

      <DashboardCard aria-label="Span events">
        <DetailHeader title="Span events" />
        <DataTable
          caption="Events recorded for this task"
          columns={eventColumns}
          rows={events ?? []}
          rowKey={(event) => `${event.span_id}-${event.time}-${event.name}`}
          emptyTitle="No events recorded"
          emptyDescription="The gateway recorded no policy or lifecycle events for this task."
        />
      </DashboardCard>
    </div>
  );
}

function BackLink() {
  return (
    <Link className="back-link" to="/tasks">
      ← All tasks
    </Link>
  );
}
