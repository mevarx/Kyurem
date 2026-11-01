import { useMemo, useState } from "react";
import { StatusBadge } from "../components/StatusBadge";
import { DataTable } from "../components/DataTable";
import { EmptyState } from "../components/EmptyState";
import { ErrorState } from "../components/ErrorState";
import { TableSkeleton } from "../components/Skeleton";
import {
  DashboardCard,
  DetailHeader,
} from "../components/watermelon/capitalio-dashboard/components/capitalio/shared";
import { GoNoGo, type GoNoGoDecision } from "../components/go-no-go";
import { TriageDesk, type TriageItem } from "../components/triage-desk";
import { useDecisions, usePolicy } from "../api/hooks";
import type { Decision, DecisionKind, PolicyMatrixCell, PolicyRule } from "../api/types";
import { decisionTone, policyModeTone } from "../lib/status";
import { formatDateTime } from "../lib/format";

const KIND_OPTIONS: Array<{ value: DecisionKind | ""; label: string }> = [
  { value: "", label: "All" },
  { value: "policy", label: "Policy" },
  { value: "trust", label: "Trust" },
  { value: "auth", label: "Auth" },
];

/** Risk score for review ordering: denies first, then warnings. */
function decisionRisk(decision: Decision["decision"]): number {
  if (decision === "deny") return 90;
  if (decision === "warn") return 60;
  return 10;
}

function triageId(item: Decision): string {
  return `${item.timestamp}-${item.kind}-${item.caller}-${item.callee}`;
}

/* access-pass is intentionally unwired: the API exposes no access-grant
   flow (decisions are a read-only audit log), so once/hour/always
   durations plus revoke would imply a grant that cannot be enforced. */
/** Authorization policy, the caller x callee matrix and the decision audit log. */
export function Policy() {
  const policyQuery = usePolicy();
  const [kind, setKind] = useState<DecisionKind | "">("");
  const decisionsQuery = useDecisions(kind, 100);

  const policy = policyQuery.data;
  const decisions = useMemo(() => decisionsQuery.data?.items ?? [], [decisionsQuery.data]);

  // Riskiest-first review queue over failing decisions; allows stay green
  // and are excluded so the queue complements the full audit log below.
  const triageItems = useMemo<TriageItem[]>(
    () =>
      decisions
        .filter((item) => item.decision !== "allow")
        .map((item) => {
          const route = `${item.caller} → ${item.callee}`;
          const note = item.reason || item.rule || "";
          return {
            id: triageId(item),
            title: `${item.decision} · ${route}`,
            agent: item.caller,
            kind: item.kind,
            risk: decisionRisk(item.decision),
            body: [
              { text: `${item.kind} decision ${item.decision} on ${route}` },
              ...(item.skill ? [{ text: ` via skill ${item.skill}` }] : []),
              ...(note
                ? [
                    {
                      text: ` — ${note}`,
                      risk: (item.decision === "deny" ? "high" : "medium") as "high" | "medium",
                      note,
                    },
                  ]
                : [{ text: ` · mode ${item.mode ?? "unknown"}` }]),
            ],
          };
        }),
    [decisions],
  );

  // Most urgent failing decision for the approve/undo review card.
  const pendingDecision = useMemo(
    () =>
      decisions.find((item) => item.decision === "deny") ??
      decisions.find((item) => item.decision === "warn") ??
      null,
    [decisions],
  );
  const pendingId = pendingDecision ? triageId(pendingDecision) : null;
  const [outcome, setOutcome] = useState<{ id: string; decision: GoNoGoDecision } | null>(null);
  const visibleOutcome = outcome && outcome.id === pendingId ? outcome : null;
  const [lastResolution, setLastResolution] = useState<string | null>(null);

  const matrixByCaller = useMemo(() => {
    if (!policy) return new Map<string, Map<string, PolicyMatrixCell>>();
    const map = new Map<string, Map<string, PolicyMatrixCell>>();
    for (const cell of policy.matrix) {
      let row = map.get(cell.caller);
      if (!row) {
        row = new Map<string, PolicyMatrixCell>();
        map.set(cell.caller, row);
      }
      row.set(cell.callee, cell);
    }
    return map;
  }, [policy]);

  return (
    <div className="page">
      <header className="page-header">
        <div className="page-title-pair">
          <span className="page-kicker">Manage</span>
          <h1>Policy &amp; audit</h1>
          <p className="page-subtitle">
            Authorization rules, the caller × callee matrix and recent decisions.
          </p>
        </div>
        {policy ? (
          <StatusBadge
            label={`mode: ${policy.mode}`}
            tone={policyModeTone(policy.mode)}
          />
        ) : null}
      </header>

      {policyQuery.isPending ? (
        <DashboardCard aria-label="Policy loading">
          <TableSkeleton rows={4} />
        </DashboardCard>
      ) : policyQuery.isError ? (
        <ErrorState
          error={policyQuery.error}
          onRetry={() => void policyQuery.refetch()}
          title="Could not load policy"
        />
      ) : !policy ? (
        <DashboardCard aria-label="Policy loading">
          <TableSkeleton rows={4} />
        </DashboardCard>
      ) : (
        <>
          <DashboardCard aria-label="Policy summary">
            <DetailHeader title="Summary" />
            <dl className="detail-grid">
              <div>
                <dt>Mode</dt>
                <dd>
                  <StatusBadge label={policy.mode} tone={policyModeTone(policy.mode)} />
                </dd>
              </div>
              <div>
                <dt>Default</dt>
                <dd>{policy.default}</dd>
              </div>
              <div>
                <dt>Rules</dt>
                <dd>{policy.rules.length}</dd>
              </div>
              <div>
                <dt>Callers</dt>
                <dd>{policy.callers.length}</dd>
              </div>
              <div>
                <dt>Callees</dt>
                <dd>{policy.callees.length}</dd>
              </div>
            </dl>
          </DashboardCard>

          <DashboardCard aria-label="Policy rules">
            <DetailHeader title="Rules" />
            {policy.rules.length === 0 ? (
              <p className="muted">No rules configured — the default applies.</p>
            ) : (
              <DataTable<PolicyRule>
                caption="Policy rules"
                rows={policy.rules}
                rowKey={(rule) => `${rule.name}-${rule.caller}-${rule.callee}`}
                columns={[
                  { key: "name", header: "Name", render: (rule) => rule.name },
                  { key: "caller", header: "Caller", render: (rule) => rule.caller },
                  { key: "callee", header: "Callee", render: (rule) => rule.callee },
                  {
                    key: "skills",
                    header: "Skills",
                    render: (rule) =>
                      rule.skills && rule.skills.length > 0
                        ? rule.skills.join(", ")
                        : "—",
                  },
                  {
                    key: "effect",
                    header: "Effect",
                    render: (rule) => (
                      <StatusBadge
                        label={rule.effect}
                        tone={decisionTone(rule.effect)}
                      />
                    ),
                  },
                ]}
              />
            )}
          </DashboardCard>

          <DashboardCard aria-label="Caller by callee matrix">
            <DetailHeader title="Caller × callee matrix" />
            {policy.callers.length === 0 || policy.callees.length === 0 ? (
              <p className="muted">No callers or callees configured.</p>
            ) : (
              <div className="table-wrapper" tabIndex={0}>
                <table className="data-table">
                  <caption className="sr-only">Policy matrix</caption>
                  <thead>
                    <tr>
                      <th scope="col">caller \ callee</th>
                      {policy.callees.map((callee) => (
                        <th key={callee} scope="col">
                          {callee}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {policy.callers.map((caller) => (
                      <tr key={caller}>
                        <th scope="row">{caller}</th>
                        {policy.callees.map((callee) => {
                          const cell = matrixByCaller.get(caller)?.get(callee);
                          return (
                            <td key={callee}>
                              {cell ? (
                                <StatusBadge
                                  label={cell.decision}
                                  tone={decisionTone(cell.decision)}
                                />
                              ) : (
                                <span className="muted">—</span>
                              )}
                            </td>
                          );
                        })}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <p className="muted field-hint">
              Cells are evaluated without a skill; skill-scoped rules appear in
              the rule list above.
            </p>
          </DashboardCard>
        </>
      )}

      {!decisionsQuery.isPending && !decisionsQuery.isError && triageItems.length > 0 ? (
        <>
          <DashboardCard aria-label="Decision review queue">
            <DetailHeader
              title="Review queue"
              subtitle="Riskiest failing decisions first — complements the full audit log below"
            />
            <div className="mt-4 flex justify-center">
              <TriageDesk
                key={`${kind}-${triageItems.length}-${triageItems[0]?.id ?? "empty"}`}
                items={triageItems}
                onResolve={(item, action) =>
                  setLastResolution(`${item.title} → ${action} (local review; audit log unchanged)`)
                }
                title="Needs review"
              />
            </div>
            {lastResolution ? <p className="muted mt-3 text-center">{lastResolution}</p> : null}
          </DashboardCard>

          {pendingDecision && pendingId ? (
            <DashboardCard aria-label="Decision approval">
              <DetailHeader
                title="Approve or deny"
                subtitle="Local acknowledgement — the decisions log is read-only and gateway policy is unchanged"
              />
              <div className="mt-4 flex justify-center">
                <GoNoGo
                  key={pendingId}
                  onDecision={(decision) => setOutcome({ id: pendingId, decision })}
                  onUndo={() => setOutcome(null)}
                  request={{
                    agent: pendingDecision.caller,
                    action: `access ${pendingDecision.callee}${pendingDecision.skill ? ` with ${pendingDecision.skill}` : ""}`,
                    risk: pendingDecision.decision === "deny" ? "high" : "medium",
                    preview: {
                      type: "command",
                      command: `${pendingDecision.kind} → ${pendingDecision.decision}`,
                      cwd: `${pendingDecision.caller} → ${pendingDecision.callee}`,
                    },
                    reason:
                      pendingDecision.reason ||
                      pendingDecision.rule ||
                      `mode: ${pendingDecision.mode ?? "unknown"} · ${formatDateTime(pendingDecision.timestamp)}`,
                  }}
                />
              </div>
              {visibleOutcome ? (
                <p className="muted mt-3 text-center">
                  Marked {visibleOutcome.decision} locally — recorded in this view only.
                </p>
              ) : null}
            </DashboardCard>
          ) : null}
        </>
      ) : null}

      <DashboardCard aria-label="Recent decisions">
        <DetailHeader title="Recent decisions" subtitle="Newest audit events first" />
        <div className="filter-bar" style={{ marginTop: 14, marginBottom: 4 }}>
          <div className="field" style={{ flexGrow: 0, minWidth: 150 }}>
            <label htmlFor="decision-kind">Kind</label>
            <select
              id="decision-kind"
              value={kind}
              onChange={(event) =>
                setKind(event.target.value as DecisionKind | "")
              }
            >
              {KIND_OPTIONS.map((option) => (
                <option key={option.value || "all"} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
        </div>

        {decisionsQuery.isPending ? (
          <TableSkeleton rows={6} />
        ) : decisionsQuery.isError ? (
          <ErrorState
            error={decisionsQuery.error}
            onRetry={() => void decisionsQuery.refetch()}
            title="Could not load decisions"
          />
        ) : (decisionsQuery.data?.items?.length ?? 0) === 0 ? (
          <EmptyState
            title="No decisions recorded"
            description="The decision ring is process-local and starts empty after a restart."
            icon="shield"
          />
        ) : (
          <ol className="timeline">
            {(decisionsQuery.data?.items ?? []).map((item) => {
              const tone = decisionTone(item.decision);
              const dotClass =
                tone === "good" ? "timeline-dot-good" : tone === "bad" ? "timeline-dot-bad" : tone === "warn" ? "timeline-dot-warn" : "";
              return (
                <li key={`${item.timestamp}-${item.caller}-${item.callee}`} className="timeline-item">
                  <span className={`timeline-dot ${dotClass}`.trim()} aria-hidden="true" />
                  <div className="timeline-body">
                    <div className="chip-row">
                      <StatusBadge label={item.decision} tone={tone} />
                      <StatusBadge label={item.kind} tone="neutral" />
                      <span className="timeline-meta">
                        {item.caller} → {item.callee}
                        {item.skill ? ` · ${item.skill}` : ""}
                        {item.mode ? ` · ${item.mode}` : ""}
                      </span>
                    </div>
                    <span className="timeline-meta">{formatDateTime(item.timestamp)}</span>
                    <span className="muted">{item.reason || item.rule || "—"}</span>
                  </div>
                </li>
              );
            })}
          </ol>
        )}
      </DashboardCard>
    </div>
  );
}
