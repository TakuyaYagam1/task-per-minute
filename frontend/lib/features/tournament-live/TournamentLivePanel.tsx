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
  recovering: "Синхронизируемся",
  live: "Сервер на связи",
  stale: "Данные устарели",
  awaiting_server: "Ждем сервер",
  rejected: "Соединение отклонено",
};

const statusMessages: Record<TournamentLiveConnectionStatus, string> = {
  connecting: "Проверяем доступ к текущему состоянию турнира.",
  recovering: "Получаем подтвержденный снимок состояния.",
  live: "Отображается подтвержденное состояние турнира.",
  stale: "Команды временно недоступны. Получаем актуальное состояние с сервера.",
  awaiting_server: "Локальное время не объявляет результат. Ждем подтверждение сервера.",
  rejected: "Сервер отклонил realtime-соединение. Проверьте доступ и повторите попытку.",
};

const effectiveStatus = (
  status: TournamentLiveConnectionStatus,
  countdown?: CountdownView,
): TournamentLiveConnectionStatus =>
  countdown?.status === "awaiting_server" ? "awaiting_server" : status;

export const TournamentLivePanel = ({
  role,
  status,
  title = "Состояние турнира",
  tournamentId,
  revision,
  countdown,
  actions = [],
  onRetry,
  children,
}: TournamentLivePanelProps) => {
  const displayStatus = effectiveStatus(status, countdown);
  const commandsEnabled = displayStatus === "live" && (countdown?.commandsEnabled ?? true);

  return (
    <section className={styles.panel} aria-labelledby="tournament-live-title">
      <div className={styles.headingRow}>
        <div>
          <h2 className={styles.title} id="tournament-live-title">{title}</h2>
          <p className={styles.role}>{roleLabels[role]}</p>
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
        <div className={styles.countdown} aria-label="Серверный отсчет">
          <span className={styles.countdownLabel}>До серверного дедлайна</span>
          <strong className={styles.countdownValue} data-testid="server-countdown">
            {formatCountdown(countdown.remainingMs)}
          </strong>
          {countdown.status === "awaiting_server" && (
            <p className={styles.waiting}>Время вышло. Ожидаем событие от сервера.</p>
          )}
        </div>
      )}

      {(tournamentId !== undefined || revision !== undefined) && (
        <p className={styles.statusMessage}>
          {tournamentId !== undefined ? `Турнир: ${tournamentId}` : ""}
          {tournamentId !== undefined && revision !== undefined ? " | " : ""}
          {revision !== undefined ? `Ревизия сервера: ${revision}` : ""}
        </p>
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
              Повторить синхронизацию
            </button>
          )}
        </div>
      )}
    </section>
  );
};
