import { useState } from "react";
import { useAgents } from "../api/hooks";
import type { AgentInfo } from "../api/types";
import { DataTable, type DataTableColumn } from "../components/DataTable";
import { ErrorState } from "../components/ErrorState";
import { LoadingState } from "../components/LoadingState";
import { StatusBadge } from "../components/StatusBadge";
import { TimeRangePicker } from "../components/TimeRangePicker";
import { formatCost, formatDuration, formatNumber, formatPercent, formatRelativeTime } from "../lib/format";
import { breakerStateTone, cardStatusTone, isUnknownState } from "../lib/status";
import { DEFAULT_TIME_RANGE, toApiWindow, type TimeRange } from "../lib/time";

/** Registry of the agents known to the gateway. */
export function Agents() {
  const [range, setRange] = useState<TimeRange>(DEFAULT_TIME_RANGE);
  const query = useAgents(toApiWindow(range));

  const rows = query.data?.items ?? [];

  const columns: DataTableColumn<AgentInfo>[] = [
    {
      key: "name",
      header: "Agent",
      render: (agent) => <span className="mono">{agent.name}</span>,
    },
    {
      key: "aliases",
      header: "Aliases",
      render: (agent) => (agent.aliases.length > 0 ? agent.aliases.join(", ") : "unknown"),
    },
    {
      key: "upstreams",
      header: "Upstreams",
      render: (agent) => (
        <ul className="plain-list">
          {agent.upstreams.length > 0 ? (
            agent.upstreams.map((upstream) => <li key={upstream}>{upstream}</li>)
          ) : (
            <li className="muted">unknown</li>
          )}
        </ul>
      ),
    },
    {
      key: "card_status",
      header: "Card",
      render: (agent) => (
        <StatusBadge label={agent.card_status} tone={cardStatusTone(agent.card_status)} />
      ),
    },
    {
      key: "verification",
      header: "Verification",
      render: (agent) => (
        <StatusBadge
          label={agent.verification}
          tone={isUnknownState(agent.verification) ? "neutral" : "good"}
          title="Card signature verification"
        />
      ),
    },
    {
      key: "breaker_state",
      header: "Breaker",
      render: (agent) => (
        <StatusBadge
          label={agent.breaker_state ?? "unknown"}
          tone={breakerStateTone(agent.breaker_state)}
        />
      ),
    },
    {
      key: "last_seen",
      header: "Last seen",
      render: (agent) => formatRelativeTime(agent.last_seen),
    },
    {
      key: "spans",
      header: "Spans",
      align: "right",
      render: (agent) => formatNumber(agent.spans),
    },
    {
      key: "errors",
      header: "Errors",
      align: "right",
      render: (agent) => formatNumber(agent.errors),
    },
    {
      key: "error_rate",
      header: "Error rate",
      align: "right",
      render: (agent) => formatPercent(agent.error_rate),
    },
    {
      key: "p95",
      header: "p95",
      align: "right",
      render: (agent) => formatDuration(agent.p95_ms ?? null),
    },
    {
      key: "cost",
      header: "Cost",
      align: "right",
      render: (agent) => formatCost(agent.cost_usd),
    },
  ];

  return (
    <div className="page">
      <header className="page-header">
        <div>
          <h1>Agents</h1>
          <p className="page-subtitle">Registry, cards and traffic of every known agent.</p>
        </div>
        <TimeRangePicker value={range} onChange={setRange} idPrefix="agents-range" />
      </header>

      {query.isPending ? (
        <LoadingState label="Loading agents…" />
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <section className="panel" aria-labelledby="agent-table">
          <div className="panel-header">
            <h2 id="agent-table">Registered agents</h2>
            <span className="muted">{formatNumber(rows.length)} agents</span>
          </div>
          <DataTable
            caption="Agents registered in the Kyurem gateway"
            columns={columns}
            rows={rows}
            rowKey={(agent) => agent.name}
            emptyTitle="No agents registered"
            emptyDescription="The gateway does not know any agent yet."
          />
        </section>
      )}
    </div>
  );
}
