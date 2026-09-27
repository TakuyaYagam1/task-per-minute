"use client";

import type { ReactNode } from "react";

import type { TournamentLiveRole } from "../../shared/api/tournament-recovery";
import { formatCountdown, type CountdownView } from "./countdown";
import styles from "./TournamentLivePanel.module.css";

export type TournamentLiveConnectionStatus =
  | "connecting"
  | "recovering"
  | "live"
  | "stale"
  | "awaiting_server"
  | "rejected";

export type TournamentLiveAction = Readonly<{
  label: string;
  onClick: () => void;
  disabled?: boolean;
}>;

export type TournamentLivePanelProps = Readonly<{
  role: TournamentLiveRole;
  status: TournamentLiveConnectionStatus;
  title?: string;
  tournamentId?: string;
  revision?: number;
  countdown?: CountdownView;
  actions?: readonly TournamentLiveAction[];
  onRetry?: () => void;
  children?: ReactNode;
}>;

const roleLabels: Record<TournamentLiveRole, string> = {
  public: "Публичный просмотр",
  participant: "Участник",
  operator: "Оператор",
};

const statusLabels: Record<TournamentLiveConnectionStatus, string> = {
  connecting: "Подключаемся",
  recovering: "Обновляем данные",
  live: "На связи",
  stale: "Данные устарели",
  awaiting_server: "Ожидаем подтверждения",
  rejected: "Не удалось подключиться",
};

const statusMessages: Record<TournamentLiveConnectionStatus, string> = {
  connecting: "Загружаем данные турнира.",
  recovering: "Обновляем данные турнира.",
  live: "Показываем подтвержденные данные турнира.",
  stale: "Не удалось обновить данные. Повторите попытку.",
  awaiting_server: "Время истекло. Ожидаем результат.",
  rejected: "Не удалось подключиться к трансляции. Проверьте доступ и повторите попытку.",
};

const effectiveStatus = (
  status: TournamentLiveConnectionStatus,
  countdown?: CountdownView,
): TournamentLiveConnectionStatus =>
  countdown?.status === "awaiting_server" ? "awaiting_server" : status;

export const TournamentLivePanel = ({
  role,
  status,
  title,
  countdown,
  actions = [],
  onRetry,
  children,
}: TournamentLivePanelProps) => {
  const displayStatus = effectiveStatus(status, countdown);
  const commandsEnabled = displayStatus === "live" && (countdown?.commandsEnabled ?? true);
  const panelTitle = title ?? (role === "public" ? "Трансляция турнира" : "Состояние турнира");
  const panelRole = role === "public" ? "Трансляция" : roleLabels[role];

  return (
    <section className={styles.panel} aria-labelledby="tournament-live-title">
      <div className={styles.headingRow}>
        <div>
          <h2 className={styles.title} id="tournament-live-title">{panelTitle}</h2>
          <p className={styles.role}>{panelRole}</p>
        </div>
        <span
          className={styles.status}
          data-state={displayStatus}
          role="status"
          aria-live="polite"
        >
          {statusLabels[displayStatus]}
        </span>
      </div>

      <p className={styles.statusMessage} aria-live="polite">
        {statusMessages[displayStatus]}
      </p>

      {countdown !== undefined && (
        <div className={styles.countdown} aria-label="Отсчет до конца">
          <span className={styles.countdownLabel}>До конца</span>
          <strong className={styles.countdownValue} data-testid="server-countdown">
            {formatCountdown(countdown.remainingMs)}
          </strong>
          {countdown.status === "awaiting_server" && (
            <p className={styles.waiting}>Время вышло. Ожидаем подтверждение.</p>
          )}
        </div>
      )}


      {children}

      {(actions.length > 0 || onRetry !== undefined) && (
        <div className={styles.actions}>
          {actions.map((action) => (
            <button
              className={styles.action}
              disabled={!commandsEnabled || action.disabled === true}
              key={action.label}
              onClick={action.onClick}
              type="button"
            >
              {action.label}
            </button>
          ))}
          {onRetry !== undefined && (
            <button className={styles.action} onClick={onRetry} type="button">
              {role === "public" ? "Обновить трансляцию" : "Повторить"}
            </button>
          )}
        </div>
      )}
    </section>
  );
};
