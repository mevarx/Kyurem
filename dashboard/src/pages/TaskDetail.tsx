import { Link, useParams } from "react-router-dom";
import { useTaskDetail } from "../api/hooks";
import type { Event, Span, Task } from "../api/types";
import { CostTable } from "../components/CostTable";
import { JsonExplorer } from "../components/json-explorer";
import { LogStream, type LogLine, type LogLevel } from "../components/log-stream";
import { ToolReceiptSpike, type ToolReceiptCall } from "../components/tool-receipt";
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
  const receiptCalls = toReceiptCalls(spans);
  const logLines = toLogLines(events ?? []);

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

      <div className="panel-row">
        <DashboardCard aria-label="Tool calls">
          <DetailHeader title="Tool calls" subtitle="Per-span time; the gateway does not expose per-span cost" />
          {receiptCalls.length === 0 ? (
            <EmptyState title="No spans recorded" description="The gateway returned no spans for this task." />
          ) : (
            <ToolReceiptSpike calls={receiptCalls} currency="$" />
          )}
        </DashboardCard>

        <DashboardCard aria-label="Task payload">
          <DetailHeader title="Task payload" subtitle="Raw task record" />
          <JsonExplorer data={taskPayload(task)} title="task" />
        </DashboardCard>
      </div>

      <DashboardCard aria-label="Span events">
        <DetailHeader title="Span events" subtitle="Searchable stream; repeated bursts fold together" />
        {logLines.length === 0 ? (
          <EmptyState
            title="No events recorded"
            description="The gateway recorded no policy or lifecycle events for this task."
          />
        ) : (
          <LogStream initialLines={logLines} title="events" />
        )}
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

/** Map spans to thermal receipts: time always, cost/tokens only when the span carries them. */
function toReceiptCalls(spans: Span[]): ToolReceiptCall[] {
  return spans.map((span) => {
    const code = (span.status_code ?? "").toUpperCase();
    const failed = Boolean(span.error_type) || code === "ERROR" || code === "STATUS_CODE_ERROR";
    return {
      id: span.span_id,
      name: span.name,
      summary: span.service_name ?? span.agent ?? span.caller ?? undefined,
      status: failed ? "error" : "success",
      durationMs: span.duration_ms,
      args: span.attributes,
      error: span.status_message ?? span.error_type ?? undefined,
    };
  });
}

/** Map gateway events to log lines; access-decision and error names surface as errors. */
function toLogLines(events: Event[]): LogLine[] {
  return events.map((event, index) => {
    const name = event.name.toLowerCase();
    const level: LogLevel =
      name.includes("error") || name.includes("fail") || name.includes("deny")
        ? "error"
        : name.includes("warn") || name.includes("policy")
          ? "warn"
          : "info";
    return {
      id: `${event.span_id}-${event.time}-${index}`,
      ts: Date.parse(event.time),
      level,
      service: event.span_id.slice(0, 8),
      message: event.name,
    };
  });
}

/** Task record as explorer data; unknown shapes degrade to a string. */
function taskPayload(task: Task) {
  try {
    return JSON.parse(JSON.stringify(task)) as Parameters<typeof JsonExplorer>[0]["data"];
  } catch {
    return String(task?.task_id ?? "task");
  }
}
