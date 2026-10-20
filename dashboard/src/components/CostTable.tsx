import type { CostReport } from "../api/types";
import { formatCost, formatNumber, formatTokens } from "../lib/format";
import { DataTable, type DataTableColumn } from "./DataTable";

export interface CostTableProps {
  report: CostReport;
  caption?: string;
  /** Label of the grouping column (agent, model, task, …). */
  groupLabel?: string;
}

/** Cost and token breakdown per group key (agent, model or task). */
export function CostTable({ report, caption, groupLabel }: CostTableProps) {
  const columns: DataTableColumn<CostReport["buckets"][number]>[] = [
    {
      key: "key",
      header: groupLabel ?? report.group_by ?? "group",
      render: (bucket) => <span className="mono">{bucket.key}</span>,
    },
    {
      key: "cost",
      header: "Cost",
      align: "right",
      render: (bucket) => formatCost(bucket.cost_usd),
    },
    {
      key: "input_tokens",
      header: "Input tokens",
      align: "right",
      render: (bucket) => formatTokens(bucket.input_tokens),
    },
    {
      key: "output_tokens",
      header: "Output tokens",
      align: "right",
      render: (bucket) => formatTokens(bucket.output_tokens),
    },
    {
      key: "spans",
      header: "Spans",
      align: "right",
      render: (bucket) => formatNumber(bucket.spans),
    },
    {
      key: "unknown_spans",
      header: "Spans without usage",
      align: "right",
      render: (bucket) => formatNumber(bucket.unknown_spans),
    },
  ];

  return (
    <div className="cost-table">
      <dl className="detail-grid">
        <div>
          <dt>Total cost</dt>
          <dd>{formatCost(report.total_usd)}</dd>
        </div>
        <div>
          <dt>Input tokens</dt>
          <dd>{formatTokens(report.input_tokens)}</dd>
        </div>
        <div>
          <dt>Output tokens</dt>
          <dd>{formatTokens(report.output_tokens)}</dd>
        </div>
        <div>
          <dt>Spans</dt>
          <dd>{formatNumber(report.spans)}</dd>
        </div>
        <div>
          <dt>Spans without usage</dt>
          <dd>{formatNumber(report.unknown_spans)}</dd>
        </div>
      </dl>

      <DataTable
        caption={caption ?? `Cost breakdown by ${report.group_by ?? "group"}`}
        columns={columns}
        rows={report.buckets}
        rowKey={(bucket) => bucket.key}
        emptyTitle="No cost data"
        emptyDescription="The gateway did not report any cost for this selection."
      />
    </div>
  );
}
