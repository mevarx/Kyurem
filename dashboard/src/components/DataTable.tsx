import type { ReactNode } from "react";
import { EmptyState } from "./EmptyState";

export interface DataTableColumn<T> {
  /** Stable column key. */
  key: string;
  header: string;
  render: (row: T) => ReactNode;
  align?: "left" | "right";
  width?: string;
}

export interface DataTableProps<T> {
  /** Accessible caption describing the table. */
  caption: string;
  columns: DataTableColumn<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  /** Optional mouse convenience: clicking a row triggers the callback. */
  onRowClick?: (row: T) => void;
  /** Message shown when there are no rows. */
  emptyTitle?: string;
  emptyDescription?: string;
  /** Extra class on the wrapping element. */
  className?: string;
}

/** Table with a caption, aligned columns and an explicit empty state. */
export function DataTable<T>({
  caption,
  columns,
  rows,
  rowKey,
  onRowClick,
  emptyTitle = "No data",
  emptyDescription,
  className,
}: DataTableProps<T>) {
  if (rows.length === 0) {
    return <EmptyState title={emptyTitle} description={emptyDescription} />;
  }

  return (
    <div className={`table-wrapper${className ? ` ${className}` : ""}`}>
      <table className="data-table">
        <caption className="sr-only">{caption}</caption>
        <thead>
          <tr>
            {columns.map((column) => (
              <th
                key={column.key}
                scope="col"
                style={column.width ? { width: column.width } : undefined}
                className={column.align === "right" ? "align-right" : undefined}
              >
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={rowKey(row)}
              className={onRowClick ? "is-clickable" : undefined}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
            >
              {columns.map((column) => (
                <td
                  key={column.key}
                  className={column.align === "right" ? "align-right" : undefined}
                >
                  {column.render(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
