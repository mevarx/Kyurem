import { useMemo, useState } from "react";
import { StatusBadge } from "../components/StatusBadge";
import { DataTable } from "../components/DataTable";
import { ErrorState } from "../components/ErrorState";
import { LoadingState } from "../components/LoadingState";
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

/** Authorization policy, the caller x callee matrix and the decision audit log. */
export function Policy() {
  const policyQuery = usePolicy();
  const [kind, setKind] = useState<DecisionKind | "">("");
  const decisionsQuery = useDecisions(kind, 100);

  const policy = policyQuery.data;

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
        <div>
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
        <LoadingState label="Loading policy…" />
      ) : policyQuery.isError ? (
        <ErrorState
          error={policyQuery.error}
          onRetry={() => void policyQuery.refetch()}
          title="Could not load policy"
        />
      ) : !policy ? (
        <LoadingState />
      ) : (
        <>
          <section className="panel" aria-labelledby="policy-summary">
            <h2 id="policy-summary">Summary</h2>
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
          </section>

          <section className="panel" aria-labelledby="policy-rules">
            <h2 id="policy-rules">Rules</h2>
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
          </section>

          <section className="panel" aria-labelledby="policy-matrix">
            <h2 id="policy-matrix">Caller × callee matrix</h2>
            {policy.callers.length === 0 || policy.callees.length === 0 ? (
              <p className="muted">No callers or callees configured.</p>
            ) : (
              <div className="table-wrapper">
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
          </section>
        </>
      )}

      <section className="panel" aria-labelledby="decision-log">
        <h2 id="decision-log">Recent decisions</h2>
        <div className="filter-bar">
          <div className="field">
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
          <LoadingState label="Loading decisions…" />
        ) : decisionsQuery.isError ? (
          <ErrorState
            error={decisionsQuery.error}
            onRetry={() => void decisionsQuery.refetch()}
            title="Could not load decisions"
          />
        ) : (
          <DataTable<Decision>
            caption="Recent decisions"
            rows={decisionsQuery.data?.items ?? []}
            rowKey={(item) => `${item.timestamp}-${item.caller}-${item.callee}`}
            emptyTitle="No decisions recorded"
            emptyDescription="The decision ring is process-local and starts empty after a restart."
            columns={[
              {
                key: "time",
                header: "Time",
                render: (item) => formatDateTime(item.timestamp),
              },
              {
                key: "kind",
                header: "Kind",
                render: (item) => (
                  <StatusBadge label={item.kind} tone="neutral" />
                ),
              },
              { key: "caller", header: "Caller", render: (item) => item.caller },
              { key: "callee", header: "Callee", render: (item) => item.callee },
              {
                key: "skill",
                header: "Skill",
                render: (item) => item.skill || "—",
              },
              {
                key: "decision",
                header: "Decision",
                render: (item) => (
                  <StatusBadge
                    label={item.decision}
                    tone={decisionTone(item.decision)}
                  />
                ),
              },
              {
                key: "mode",
                header: "Mode",
                render: (item) => item.mode || "—",
              },
              {
                key: "reason",
                header: "Reason",
                render: (item) => item.reason || item.rule || "—",
              },
            ]}
          />
        )}
      </section>
    </div>
  );
}
