"use client";

import Link from "next/link";
import type { ReactNode } from "react";

import { useSiteHeaderAuth } from "../../features/site-header";
import { Message, Panel, Status } from "../../shared/ui";

import type {
  ArenaAccessMessage,
  ArenaAccessStatus,
  ArenaRole,
  ArenaRoleSummary,
} from "./types";
import styles from "./arena.module.css";

export type { ArenaAccessMessage, ArenaAccessStatus, ArenaRole, ArenaRoleSummary } from "./types";

type ArenaShellProps = Readonly<{
  role?: ArenaRole;
  tournamentId?: string;
  tournamentName?: string;
  tournamentStateLabel?: string;
  accessStatus: ArenaAccessStatus;
  accessMessage?: ArenaAccessMessage;
  summary?: ArenaRoleSummary;
  onLogout?: () => void;
  logoutPending?: boolean;
  children?: ReactNode;
}>;

const ROLE_LABELS: Record<ArenaRole, string> = {
  participant: "Участник",
  operator: "Оператор",
  spectator: "Наблюдатель",
};

const STATUS_LABELS: Record<ArenaAccessStatus, string> = {
  loading: "Проверка доступа",
  ready: "Доступ подтвержден",
  completed: "Только чтение",
  unauthorized: "Нужен вход",
  forbidden: "Доступ запрещен",
  missing: "Соревнование не найдено",
  transport: "Ошибка соединения",
};

const STATUS_TONES: Record<ArenaAccessStatus, "loading" | "success" | "warning" | "error" | "info"> = {
  loading: "loading",
  ready: "success",
  completed: "info",
  unauthorized: "warning",
  forbidden: "error",
  missing: "error",
  transport: "error",
};

const rolePath = (role: ArenaRole, tournamentId: string): string =>
  `/arena/${role}/${encodeURIComponent(tournamentId)}`;

export const isSafeTournamentId = (value: string): boolean =>
  /^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$/.test(value);

export const buildArenaRolePath = (role: ArenaRole, tournamentId: string): string =>
  rolePath(role, tournamentId);

export const buildArenaLoginHref = (role: Exclude<ArenaRole, "spectator">, tournamentId: string): string => {
  const loginPath = role === "participant" ? "/" : "/admin";
  const next = rolePath(role, tournamentId);
  const query = new URLSearchParams({ next });
  return `${loginPath}?${query.toString()}`;
};

export const ArenaShell = ({
  accessMessage,
  accessStatus,
  children,
  logoutPending = false,
  onLogout,
  role,
  summary,
  tournamentId,
  tournamentName,
  tournamentStateLabel,
}: ArenaShellProps) => {
  const hasTournamentContext = Boolean(tournamentId);

  useSiteHeaderAuth(onLogout, logoutPending);

  return (
    <div className={styles.shell} data-testid="arena-shell">
      <div className={styles.container}>
        {hasTournamentContext && (
          <section className={styles.context} aria-label="Контекст соревнования">
            <div className={styles.contextDetails}>
              <strong>{tournamentName || "Соревнование"}</strong>
            </div>
            <div className={styles.contextAside}>
              {role && <span className={styles.roleName}>{ROLE_LABELS[role]}</span>}
              <Status
                tone={STATUS_TONES[accessStatus]}
                data-testid="arena-status"
              >
                {tournamentStateLabel ?? STATUS_LABELS[accessStatus]}
              </Status>
            </div>
          </section>
        )}

        <main className={styles.content}>
          {accessMessage && (
            <Message tone={accessMessage.tone} title={accessMessage.title}>
              <p>{accessMessage.description}</p>
              {accessMessage.action && (
                <Link className={styles.stateAction} href={accessMessage.action.href}>
                  {accessMessage.action.label}
                </Link>
              )}
            </Message>
          )}
          {summary && (
            <Panel
              as="article"
              className={styles.overview}
              aria-label={summary.title}
            >
              <div className={styles.overviewHeader}>
                <div>
                  <h1 className={styles.overviewTitle}>{summary.title}</h1>
                  <p className={styles.overviewDescription}>{summary.description}</p>
                </div>
                {summary.readOnly && <Status tone="info">Только чтение</Status>}
              </div>
              <div className={styles.metrics} role="list" aria-label="Сводка соревнования">
                {summary.metrics.map((metric) => (
                  <div className={styles.metric} role="listitem" key={metric.label}>
                    <span className={styles.metricLabel}>{metric.label}</span>
                    <span className={styles.metricValue}>{metric.value}</span>
                  </div>
                ))}
              </div>
              {summary.readOnly && (
                <Message className={styles.readOnlyNote} tone="info" title="Результаты зафиксированы">
                  Действия соревнования отключены. Доступен только просмотр состояния.
                </Message>
              )}
            </Panel>
          )}
          {children}
        </main>
      </div>
    </div>
  );
};

ArenaShell.displayName = "ArenaShell";
