import { useState } from "react";
import { Link } from "react-router-dom";
import { useAgents, useGraph, useTasks } from "../api/hooks";
import type { AgentInfo, GraphNode } from "../api/types";
import { AgentGraphCanvas } from "../components/AgentGraphCanvas";
import { EmptyState } from "../components/EmptyState";
import { ErrorState } from "../components/ErrorState";
import { LoadingState } from "../components/LoadingState";
import { StatusBadge } from "../components/StatusBadge";
import { TimeRangePicker } from "../components/TimeRangePicker";
import { formatCost, formatDuration, formatNumber, formatPercent, formatRelativeTime } from "../lib/format";
import { breakerStateTone, cardStatusTone, isUnknownState } from "../lib/status";
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
        <div>
          <h1>Agent graph</h1>
          <p className="page-subtitle">Who called whom, how often, and where it went wrong.</p>
        </div>
        <TimeRangePicker value={range} onChange={setRange} idPrefix="graph-range" />
      </header>

      {graphQuery.isPending ? (
        <LoadingState label="Loading call graph…" />
      ) : graphQuery.isError ? (
        <ErrorState error={graphQuery.error} onRetry={() => void graphQuery.refetch()} />
      ) : (graphQuery.data?.nodes.length ?? 0) === 0 && (graphQuery.data?.edges.length ?? 0) === 0 ? (
        <EmptyState
          title="No agent traffic in this window"
          description="The gateway did not record any call between agents for the selected range."
        />
      ) : (
        <div className="graph-layout">
          <section className="panel graph-panel" aria-labelledby="graph-heading">
            <h2 id="graph-heading" className="sr-only">
              Call graph
            </h2>
            <AgentGraphCanvas
              nodes={graphQuery.data?.nodes ?? []}
              edges={graphQuery.data?.edges ?? []}
              agents={agents}
              selectedAgent={selected}
              onSelectAgent={(name) => setSelected(name)}
            />
            <p className="muted graph-legend">
              Edge label: call count · Edge colour: error rate · Hover an edge for p95 latency and cost.
            </p>
          </section>

          <aside className="panel graph-side-panel" aria-labelledby="agent-detail-heading">
            <h2 id="agent-detail-heading">Agent</h2>
            {selected === null ? (
              <EmptyState title="No agent selected" description="Click a node to inspect the agent." />
            ) : (
              <AgentDetail
                name={selected}
                agent={agentByName.get(selected)}
                node={nodeByName.get(selected)}
                errors={errorsQuery.data?.items ?? []}
                errorsPending={errorsQuery.isPending}
              />
            )}
          </aside>
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
}: {
  name: string;
  agent?: AgentInfo;
  node?: GraphNode;
  errors: Array<{ task_id: string; error_message?: string; started_at: string }>;
  errorsPending: boolean;
}) {
  const cardStatus = agent?.card_status ?? "unknown";
  const verification = agent?.verification ?? "unknown";
  const breaker = agent?.breaker_state;

  return (
    <div className="agent-detail">
      <h3 className="agent-detail-name mono">{name}</h3>

      <div className="badge-row">
        <StatusBadge label={`card: ${cardStatus}`} tone={cardStatusTone(cardStatus)} />
        <StatusBadge
          label={`verification: ${verification}`}
          tone={isUnknownState(verification) ? "neutral" : "good"}
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
                  <li key={upstream}>{upstream}</li>
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
      ) : errors.length === 0 ? (
        <EmptyState title="No recent errors" />
      ) : (
        <ul className="task-list">
          {errors.map((error) => (
            <li key={error.task_id} className="task-list-item">
              <Link to={`/tasks/${encodeURIComponent(error.task_id)}`}>{error.task_id}</Link>
              {error.error_message ? (
                <span className="task-list-error">{error.error_message}</span>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
