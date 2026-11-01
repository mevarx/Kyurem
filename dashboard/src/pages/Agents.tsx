import { useState } from "react";
import { useAgents } from "../api/hooks";
import type { AgentInfo } from "../api/types";
import { DataTable, type DataTableColumn } from "../components/DataTable";
import {
  DashboardCard,
  DetailHeader,
  DetailTag,
} from "../components/watermelon/capitalio-dashboard/components/capitalio/shared";
import { AgentAvatar } from "../components/CopyableId";
import { ErrorState } from "../components/ErrorState";
import { RadialGauge } from "../components/radial-gauge";
import { TableSkeleton } from "../components/Skeleton";
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

  const meanErrorRate = rows.length === 0 ? 0 : (rows.reduce((sum, a) => sum + (a.error_rate ?? 0), 0) / rows.length) * 100;

  const columns: DataTableColumn<AgentInfo>[] = [
    {
      key: "name",
      header: "Agent",
      render: (agent) => (
        <span className="agent-cell">
          <AgentAvatar name={agent.name} />
          <span className="mono">{agent.name}</span>
        </span>
      ),
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
        <div className="page-title-pair">
          <span className="page-kicker">Manage</span>
          <h1>Agents</h1>
          <p className="page-subtitle">Registry, cards and traffic of every known agent.</p>
        </div>
        <TimeRangePicker value={range} onChange={setRange} idPrefix="agents-range" />
      </header>

      {query.isPending ? (
        <DashboardCard aria-label="Agents loading">
          <TableSkeleton rows={6} />
        </DashboardCard>
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <div className="panel-row">
          <DashboardCard aria-label="Fleet error rate">
            <RadialGauge
              bands={[
                { to: 1, label: "Healthy", tone: "success" },
                { to: 5, label: "Elevated", tone: "warning" },
                { to: 100, label: "Critical", tone: "destructive" },
              ]}
              caption={`Mean error rate across ${formatNumber(rows.length)} agents · lower is better`}
              label="Fleet error rate"
              unit="%"
              value={meanErrorRate}
            />
          </DashboardCard>
          <DashboardCard aria-label="Registered agents" className="overflow-hidden p-0">
          <div className="p-4 pb-0">
            <DetailHeader title="Registered agents" subtitle="Cards, verification and traffic">
              <DetailTag>{formatNumber(rows.length)} agents</DetailTag>
            </DetailHeader>
          </div>
          <div className="p-3">
            <DataTable
              caption="Agents registered in the Kyurem gateway"
              columns={columns}
              rows={rows}
              rowKey={(agent) => agent.name}
              emptyTitle="No agents registered"
              emptyDescription="The gateway does not know any agent yet."
            />
          </div>
        </DashboardCard>
        </div>
      )}
    </div>
  );
}
