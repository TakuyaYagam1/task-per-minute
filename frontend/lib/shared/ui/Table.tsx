import {
  type Key,
  type ReactNode,
  type TableHTMLAttributes,
} from "react";

import styles from "./TournamentUi.module.css";

export type TableAlign = "left" | "center" | "right";

export interface TableColumn<Row> {
  key: string;
  header: ReactNode;
  align?: TableAlign;
  className?: string;
  /** Render a cell for a row. `render` is kept as a familiar alias. */
  cell?: (row: Row, index: number) => ReactNode;
  render?: (row: Row, index: number) => ReactNode;
  accessor?: keyof Row | ((row: Row) => ReactNode);
}

export type TableRowKey<Row> =
  | keyof Row
  | ((row: Row, index: number) => Key | undefined);

export interface TableProps<Row>
  extends Omit<TableHTMLAttributes<HTMLTableElement>, "children"> {
  children?: ReactNode;
  columns?: readonly TableColumn<Row>[];
  /** `data` is an alias for `rows` for data-table call sites. */
  data?: readonly Row[];
  rows?: readonly Row[];
  rowKey?: TableRowKey<Row>;
  getRowKey?: (row: Row, index: number) => Key | undefined;
  rowClassName?: string | ((row: Row, index: number) => string | undefined);
  caption?: ReactNode;
  loading?: boolean;
  loadingMessage?: ReactNode;
  error?: ReactNode;
  empty?: ReactNode;
  emptyMessage?: ReactNode;
  ariaLabel?: string;
  wrapperClassName?: string;
  stickyHeader?: boolean;
}

const getCellValue = <Row,>(
  column: TableColumn<Row>,
  row: Row,
  index: number,
): ReactNode => {
  const renderer = column.cell ?? column.render;
  if (renderer) {
    return renderer(row, index);
  }

  if (typeof column.accessor === "function") {
    return column.accessor(row);
  }

  if (column.accessor !== undefined) {
    return row[column.accessor] as ReactNode;
  }

  return row[column.key as keyof Row] as ReactNode;
};

const resolveRowKey = <Row,>(
  row: Row,
  index: number,
  rowKey: TableRowKey<Row> | undefined,
  getRowKey: TableProps<Row>["getRowKey"],
): Key => {
  if (typeof rowKey === "function") {
    return rowKey(row, index) ?? index;
  }
  if (rowKey !== undefined) {
    const value = row[rowKey];
    return (value as Key | undefined) ?? index;
  }
  if (getRowKey) {
    return getRowKey(row, index) ?? index;
  }
  return index;
};

export const Table = <Row,>({
  ariaLabel,
  caption,
  children,
  className,
  columns,
  data,
  empty,
  emptyMessage = "Нет данных для отображения",
  error,
  getRowKey,
  loading = false,
  loadingMessage = "Загрузка данных",
  rowClassName,
  rowKey,
  rows,
  stickyHeader = false,
  wrapperClassName,
  ...tableProps
}: TableProps<Row>) => {
  const resolvedRows = rows ?? data ?? [];
  const isStructured = columns !== undefined;
  const stateMessage = error ?? (empty !== undefined ? empty : emptyMessage);
  const hasRows = resolvedRows.length > 0;
  const colSpan = Math.max(columns?.length ?? 0, 1);

  return (
    <div
      className={[styles.tableWrapper, wrapperClassName].filter(Boolean).join(" ")}
      role={ariaLabel ? "region" : undefined}
      aria-label={ariaLabel}
    >
      <table
        {...tableProps}
        className={[styles.table, stickyHeader && styles.tableSticky, className]
          .filter(Boolean)
          .join(" ")}
        aria-busy={loading || tableProps["aria-busy"] ? true : undefined}
      >
        {caption !== undefined && <caption className={styles.tableCaption}>{caption}</caption>}
        {isStructured ? (
          <>
            <thead>
              <tr>
                {columns.map((column) => (
                  <th
                    key={column.key}
                    className={[styles.tableCell, styles[`align-${column.align ?? "left"}`], column.className]
                      .filter(Boolean)
                      .join(" ")}
                    scope="col"
                  >
                    {column.header}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td className={styles.tableState} colSpan={colSpan}>
                    <span role="status">
                      <span className={styles.stateSpinner} aria-hidden="true" />
                      {loadingMessage}
                    </span>
                  </td>
                </tr>
              ) : error ? (
                <tr>
                  <td className={[styles.tableState, styles.tableStateError].join(" ")} colSpan={colSpan}>
                    <span role="alert">
                      <strong>Ошибка.</strong> {error}
                    </span>
                  </td>
                </tr>
              ) : !hasRows ? (
                <tr>
                  <td className={styles.tableState} colSpan={colSpan}>
                    <span className={styles.stateMark} aria-hidden="true">-</span>
                    {stateMessage}
                  </td>
                </tr>
              ) : (
                resolvedRows.map((row, index) => {
                  const rowClasses = [
                    styles.tableRow,
                    typeof rowClassName === "function" ? rowClassName(row, index) : rowClassName,
                  ];

                  return (
                    <tr
                      key={resolveRowKey(row, index, rowKey, getRowKey)}
                      className={rowClasses.filter(Boolean).join(" ")}
                    >
                      {columns.map((column) => (
                        <td
                          key={column.key}
                          className={[styles.tableCell, styles[`align-${column.align ?? "left"}`], column.className]
                            .filter(Boolean)
                            .join(" ")}
                        >
                          {getCellValue(column, row, index)}
                        </td>
                      ))}
                    </tr>
                  );
                })
              )}
            </tbody>
          </>
        ) : (
          children
        )}
      </table>
    </div>
  );
};

Table.displayName = "Table";
