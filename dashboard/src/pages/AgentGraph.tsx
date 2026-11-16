import { useState } from "react";
import { Link } from "react-router-dom";
import { useAgents, useGraph, useTasks } from "../api/hooks";
import type { AgentInfo, GraphNode } from "../api/types";
import { AgentGraphCanvas } from "../components/AgentGraphCanvas";
import { EmptyState } from "../components/EmptyState";
import { ErrorState } from "../components/ErrorState";
import { TableSkeleton } from "../components/Skeleton";
import {
  DashboardCard,
  DetailHeader,
  DetailTag,
} from "../components/watermelon/capitalio-dashboard/components/capitalio/shared";
import { StatusBadge } from "../components/StatusBadge";
import { TimeRangePicker } from "../components/TimeRangePicker";
import { formatCost, formatDuration, formatNumber, formatPercent, formatRelativeTime } from "../lib/format";
import { breakerStateTone, cardStatusTone, verificationTone } from "../lib/status";
import { DEFAULT_TIME_RANGE, toApiWindow, type TimeRange } from "../lib/time";

/** Interactive agent-to-agent call graph with a detail side panel. */
export function AgentGraph() {
  const [range, setRange] = useState<TimeRange>(DEFAULT_TIME_RANGE);
  const [selected, setSelected] = useState<string | null>(null);
  const apiWindow = toApiWindow(range);

  const graphQuery = useGraph(apiWindow);
  const agentsQuery = useAgents(apiWindow);
  const errorsQuery = useTasks(
    {
      from: apiWindow.from,
      to: apiWindow.to,
      agent: selected ?? undefined,
      status: "failed",
      limit: 10,
    },
    { enabled: selected !== null },
  );

  const agents = agentsQuery.data?.items ?? [];
  const agentByName = new Map<string, AgentInfo>(agents.map((agent) => [agent.name, agent]));
  const nodeByName = new Map<string, GraphNode>(
    (graphQuery.data?.nodes ?? []).map((node) => [node.name, node]),
  );

  return (
    <div className="page">
      <header className="page-header">
        <div className="page-title-pair">
          <span className="page-kicker">Operate</span>
          <h1>Agent graph</h1>
          <p className="page-subtitle">Who called whom, how often, and where it went wrong.</p>
        </div>
        <div className="page-header-meta">
          <DetailTag>{`${formatNumber(graphQuery.data?.nodes.length ?? 0)} nodes`}</DetailTag>
          <TimeRangePicker value={range} onChange={setRange} idPrefix="graph-range" />
        </div>
      </header>

      {graphQuery.isPending ? (
        <DashboardCard aria-label="Call graph loading">
          <TableSkeleton rows={6} />
        </DashboardCard>
      ) : graphQuery.isError ? (
        <ErrorState error={graphQuery.error} onRetry={() => void graphQuery.refetch()} />
      ) : (graphQuery.data?.nodes.length ?? 0) === 0 && (graphQuery.data?.edges.length ?? 0) === 0 ? (
        <EmptyState
          title="No agent traffic in this window"
          description="The gateway did not record any call between agents for the selected range."
        />
      ) : (
        <div className="graph-layout">
          <DashboardCard aria-label="Call graph" className="min-w-0 p-2">
            <h2 id="graph-heading" className="sr-only">
              Call graph
            </h2>
            <div className="min-w-0 [&_.graph-canvas]:h-[420px]! sm:[&_.graph-canvas]:h-[620px]!">
              <AgentGraphCanvas
                nodes={graphQuery.data?.nodes ?? []}
                edges={graphQuery.data?.edges ?? []}
                agents={agents}
                selectedAgent={selected}
                onSelectAgent={(name) => setSelected(name)}
              />
            </div>
            <p className="graph-legend mono tabular">
              <span className="graph-legend-item">
                <span className="graph-legend-line" style={{ borderColor: "var(--chart-1)" }} aria-hidden="true" />{" "}
                healthy
              </span>
              <span className="graph-legend-item">
                <span className="graph-legend-line graph-legend-line-warn" aria-hidden="true" /> errors &gt; 0
              </span>
              <span className="graph-legend-item">
                <span className="graph-legend-line graph-legend-line-bad" aria-hidden="true" /> erroring · error rate
                ≥ 25%
              </span>
              <span className="min-w-0 break-words">
                Arrows show call direction · width ∝ calls · labels show calls · p95 · hover an edge for errors and
                cost.
              </span>
            </p>
          </DashboardCard>

          <DashboardCard aria-label="Agent details" className="graph-side-panel min-w-0">
            <DetailHeader title="Agent" subtitle={selected ? "Selected node inspector" : "Select a node to inspect"}>
              {selected ? <DetailTag><span className="min-w-0 break-all">{selected}</span></DetailTag> : null}
            </DetailHeader>
            {agentsQuery.isPending ? (
              <p className="muted">Loading agent metadata…</p>
            ) : agentsQuery.isError ? (
              <ErrorState
                error={agentsQuery.error}
                title="Could not load agent metadata"
                onRetry={() => void agentsQuery.refetch()}
              />
            ) : null}
            {selected === null ? (
              <EmptyState title="No agent selected" description="Click a node to inspect the agent." />
            ) : (
              <AgentDetail
                name={selected}
                agent={agentByName.get(selected)}
                node={nodeByName.get(selected)}
                errors={errorsQuery.data?.items ?? []}
                errorsPending={errorsQuery.isPending}
                errorsError={errorsQuery.isError ? errorsQuery.error : null}
                onRetryErrors={() => void errorsQuery.refetch()}
                metadataUnavailable={agentsQuery.isPending || agentsQuery.isError}
              />
            )}
          </DashboardCard>
        </div>
      )}
    </div>
  );
}

function AgentDetail({
  name,
  agent,
  node,
  errors,
  errorsPending,
  errorsError,
  onRetryErrors,
  metadataUnavailable,
}: {
  name: string;
  agent?: AgentInfo;
  node?: GraphNode;
  errors: Array<{ task_id: string; error_message?: string; started_at: string }>;
  errorsPending: boolean;
  errorsError: Error | null;
  onRetryErrors: () => void;
  metadataUnavailable: boolean;
}) {
  const cardStatus = agent?.card_status ?? "unknown";
  const verification = agent?.verification ?? "unknown";
  const breaker = agent?.breaker_state;

  return (
    <div className="agent-detail min-w-0">
      <h3 className="agent-detail-name mono min-w-0 break-all">{name}</h3>

      {!metadataUnavailable && !agent ? <p className="muted">No registry metadata returned for this agent.</p> : null}
      <div className="badge-row">
        <StatusBadge label={`card: ${cardStatus}`} tone={cardStatusTone(cardStatus)} />
        <StatusBadge
          label={`verification: ${verification}`}
          tone={verificationTone(verification)}
        />
        {breaker ? (
          <StatusBadge label={`breaker: ${breaker}`} tone={breakerStateTone(breaker)} />
        ) : null}
      </div>

      <dl className="detail-grid">
        <div>
          <dt>Aliases</dt>
          <dd>{agent?.aliases?.length ? agent.aliases.join(", ") : "unknown"}</dd>
        </div>
        <div>
          <dt>Upstreams</dt>
          <dd>
            {agent?.upstreams?.length ? (
              <ul className="plain-list">
                {agent.upstreams.map((upstream) => (
                  <li key={upstream} className="break-all">{upstream}</li>
                ))}
              </ul>
            ) : (
              "unknown"
            )}
          </dd>
        </div>
        <div>
          <dt>Calls</dt>
          <dd>{formatNumber(node?.calls ?? agent?.spans ?? null)}</dd>
        </div>
        <div>
          <dt>Errors</dt>
          <dd>
            {formatNumber(node?.errors ?? agent?.errors ?? null)} (
            {formatPercent(node?.error_rate ?? agent?.error_rate ?? null)})
          </dd>
        </div>
        <div>
          <dt>p95 latency</dt>
          <dd>{formatDuration(node?.p95_ms ?? agent?.p95_ms ?? null)}</dd>
        </div>
        <div>
          <dt>Cost</dt>
          <dd>{formatCost(node?.cost_usd ?? agent?.cost_usd ?? null)}</dd>
        </div>
        <div>
          <dt>Last seen</dt>
          <dd>
            {node?.last_seen ?? agent?.last_seen ? formatRelativeTime(node?.last_seen ?? agent?.last_seen) : "unknown"}
          </dd>
        </div>
      </dl>

      <h3 className="sub-heading">Recent errors</h3>
      {errorsPending ? (
        <p className="muted">Loading errors…</p>
      ) : errorsError ? (
        <ErrorState error={errorsError} title="Could not load recent errors" onRetry={onRetryErrors} />
      ) : errors.length === 0 ? (
        <EmptyState title="No recent errors" />
      ) : (
        <ul className="task-list">
          {errors.map((error) => (
            <li key={error.task_id} className="task-list-item">
              <Link to={`/tasks/${encodeURIComponent(error.task_id)}`} className="min-w-0 break-all">{error.task_id}</Link>
              {error.error_message ? (
                <span className="task-list-error break-words">{error.error_message}</span>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
