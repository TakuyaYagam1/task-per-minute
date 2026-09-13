import { type HTMLAttributes, type ReactNode } from "react";

import styles from "./TournamentUi.module.css";

export type StatusTone =
  | "neutral"
  | "info"
  | "success"
  | "warning"
  | "error"
  | "live"
  | "loading"
  | "empty"
  | "disabled";

export type StatusSize = "small" | "medium";

const STATUS_LABELS: Record<StatusTone, string> = {
  neutral: "Без статуса",
  info: "Информация",
  success: "Готово",
  warning: "Внимание",
  error: "Ошибка",
  live: "В эфире",
  loading: "Загрузка",
  empty: "Нет данных",
  disabled: "Недоступно",
};

const STATUS_MARKS: Record<StatusTone, string> = {
  neutral: "-",
  info: "i",
  success: "v",
  warning: "!",
  error: "!",
  live: "*",
  loading: "",
  empty: "-",
  disabled: "x",
};

export interface StatusProps extends HTMLAttributes<HTMLSpanElement> {
  children?: ReactNode;
  label?: ReactNode;
  /** `status` is an alias for `tone` for state-oriented call sites. */
  status?: StatusTone;
  tone?: StatusTone;
  size?: StatusSize;
}

export const Status = ({
  children,
  className,
  label,
  size = "medium",
  status,
  tone = "neutral",
  ...statusProps
}: StatusProps) => {
  const currentTone = status ?? tone;
  const isLive = currentTone === "live";
  const isError = currentTone === "error";
  const content = children ?? label ?? STATUS_LABELS[currentTone];

  return (
    <span
      {...statusProps}
      className={[styles.status, styles[`status-${currentTone}`], styles[`status-${size}`], className]
        .filter(Boolean)
        .join(" ")}
      data-tone={currentTone}
      aria-busy={currentTone === "loading" ? true : statusProps["aria-busy"]}
      aria-disabled={currentTone === "disabled" ? true : statusProps["aria-disabled"]}
      role={statusProps.role ?? (isError ? "alert" : "status")}
      aria-live={statusProps["aria-live"] ?? (isError || isLive ? "polite" : undefined)}
    >
      <span className={styles.statusMark} aria-hidden="true">
        {currentTone === "loading" ? <span className={styles.statusSpinner} /> : STATUS_MARKS[currentTone]}
      </span>
      <span>{content}</span>
    </span>
  );
};

Status.displayName = "Status";
