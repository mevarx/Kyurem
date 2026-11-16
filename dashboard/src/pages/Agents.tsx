import { useState } from "react";
import { Activity, Bot, ShieldCheck, Workflow } from "lucide-react";
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
import { TableSkeleton } from "../components/Skeleton";
import { StatusBadge } from "../components/StatusBadge";
import { TimeRangePicker } from "../components/TimeRangePicker";
import { formatCost, formatDuration, formatNumber, formatPercent, formatRelativeTime } from "../lib/format";
import { breakerStateTone, cardStatusTone, verificationTone } from "../lib/status";
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
      key: "health",
      header: "Health",
      render: (agent) => (
        <div className="flex flex-wrap items-center gap-1">
          <StatusBadge
            label={`card ${agent.card_status}`}
            tone={cardStatusTone(agent.card_status)}
            title={`Card status: ${agent.card_status}`}
          />
          <StatusBadge
            label={agent.verification}
            tone={verificationTone(agent.verification)}
            title="Card signature verification"
          />
          {agent.breaker_state && agent.breaker_state !== "unknown" ? (
            <StatusBadge
              label={`breaker ${agent.breaker_state}`}
              tone={breakerStateTone(agent.breaker_state)}
              title={`Circuit breaker: ${agent.breaker_state}`}
            />
          ) : null}
        </div>
      ),
    },
    {
      // The registry reports spanned work per agent; label it as such.
      key: "calls",
      header: "Spans",
      align: "right",
      render: (agent) => formatNumber(agent.spans),
    },
    {
      key: "errors",
      header: "Errors",
      align: "right",
      hideOnMobile: true,
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
      hideOnMobile: true,
      render: (agent) => formatDuration(agent.p95_ms ?? null),
    },
    {
      key: "cost",
      header: "Cost",
      align: "right",
      hideOnMobile: true,
      render: (agent) => formatCost(agent.cost_usd),
    },
    {
      key: "last_seen",
      header: "Last seen",
      hideOnMobile: true,
      render: (agent) => formatRelativeTime(agent.last_seen),
    },
    {
      key: "aliases",
      header: "Aliases",
      hideOnMobile: true,
      render: (agent) =>
        agent.aliases.length > 0 ? (
          <span className="flex flex-wrap items-center gap-1">
            {agent.aliases.map((alias) => (
              <span key={alias} className="badge badge-neutral">
                {alias}
              </span>
            ))}
          </span>
        ) : (
          "unknown"
        ),
    },
    {
      key: "upstreams",
      header: "Upstreams",
      hideOnMobile: true,
      render: (agent) => (
        <ul className="plain-list mono break-all">
          {agent.upstreams.length > 0 ? (
            agent.upstreams.map((upstream) => <li key={upstream}>{upstream}</li>)
          ) : (
            <li className="muted">unknown</li>
          )}
        </ul>
      ),
    },
  ];

  return (
    <div className="page">
      <header className="page-header">
        <div className="page-title-pair">
          <span className="page-kicker">Fleet management</span>
          <h1>Agents</h1>
          <p className="page-subtitle">Your agent directory, verification and traffic in one place.</p>
        </div>
        <div className="page-header-meta">
          <DetailTag>{`${formatNumber(rows.length)} agents`}</DetailTag>
          <TimeRangePicker value={range} onChange={setRange} idPrefix="agents-range" />
        </div>
      </header>

      {query.isPending ? (
        <DashboardCard aria-label="Agents loading">
          <TableSkeleton rows={6} />
        </DashboardCard>
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <>
          <section className="grid min-w-0 grid-cols-2 gap-3 lg:grid-cols-4" aria-label="Fleet summary">
            {[
              { label: "Registered agents", value: formatNumber(rows.length), hint: "Known to the gateway", Icon: Bot },
              { label: "Verified cards", value: formatNumber(rows.filter(agent => agent.verification === "verified").length), hint: "Signature verified", Icon: ShieldCheck },
              { label: "Recorded spans", value: formatNumber(rows.reduce((sum, agent) => sum + (agent.spans ?? 0), 0)), hint: "In the selected window", Icon: Workflow },
              { label: "Fleet error rate", value: rows.length ? `${meanErrorRate.toFixed(1)}%` : "—", hint: "Mean across agents", Icon: Activity },
            ].map(({ label, value, hint, Icon }) => (
              <DashboardCard key={label} className="p-4!">
                <div className="mb-3 flex items-center justify-between gap-2">
                  <span className="text-[11px] text-muted-foreground">{label}</span>
                  <Icon className="size-3.5 shrink-0 text-primary" aria-hidden="true" />
                </div>
                <p className="text-2xl font-semibold tracking-tight text-foreground tabular-nums">{value}</p>
                <p className="mt-1 text-[10px] text-muted-foreground">{hint}</p>
              </DashboardCard>
            ))}
          </section>
          <DashboardCard aria-label="Registered agents" className="overflow-hidden p-0!">
            <div className="p-5">
              <DetailHeader title="Agent directory" subtitle="Card health, trust and traffic across your fleet">
                <DetailTag>{formatNumber(rows.length)} agents</DetailTag>
              </DetailHeader>
            </div>
            <DataTable
              caption="Agents registered in the Kyurem gateway"
              columns={columns}
              rows={rows}
              rowKey={(agent) => agent.name}
              emptyTitle="No agents registered"
              emptyDescription="The gateway does not know any agent yet."
              className="tabular rounded-none! border-x-0! border-b-0!"
            />
            <div className="border-t border-border px-5 py-3 text-[11px] text-muted-foreground">
              Verification reflects the gateway’s latest agent-card check.
            </div>
          </DashboardCard>
        </>
      )}
    </div>
  );
}
