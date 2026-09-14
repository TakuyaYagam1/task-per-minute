import Link from "next/link";
import type { ReactNode } from "react";

import { Button, Message, Panel, Status } from "../../shared/ui";

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
  tournamentStateLabel?: string;
  accessStatus: ArenaAccessStatus;
  accessMessage?: ArenaAccessMessage;
  summary?: ArenaRoleSummary;
  onLogout?: () => void;
  logoutPending?: boolean;
  children?: ReactNode;
}>;

const ROLE_LINKS: ReadonlyArray<Readonly<{ role: ArenaRole; label: string }>> = [
  { role: "participant", label: "Участник" },
  { role: "operator", label: "Оператор" },
  { role: "spectator", label: "Наблюдатель" },
];

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
  missing: "Турнир не найден",
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
  tournamentStateLabel,
}: ArenaShellProps) => {
  const hasTournamentContext = Boolean(tournamentId);

  return (
    <div className={styles.shell} data-testid="arena-shell">
      <div className={styles.container}>
        <header>
          <div className={styles.topbar}>
            <div className={styles.brandBlock}>
              <Link href="/arena" className={styles.brand}>
                <span className={styles.brandMark} aria-hidden="true">TPM</span>
                <span>Arena</span>
              </Link>
            </div>
            <nav className={styles.utilityNav} aria-label="Основная навигация">
              <Link href="/" className={styles.utilityLink}>Главная</Link>
              <Link href="/leaderboard" className={styles.utilityLink}>Рейтинг</Link>
            </nav>
          </div>

          {hasTournamentContext && (
            <>
              <nav className={styles.roleNav} aria-label="Режим Arena" data-testid="arena-role-nav">
                {ROLE_LINKS.map((item) => (
                  <Link
                    key={item.role}
                    href={rolePath(item.role, tournamentId!)}
                    className={styles.navLink}
                    aria-current={role === item.role ? "page" : undefined}
                  >
                    {item.label}
                  </Link>
                ))}
              </nav>

              <section className={styles.context} aria-label="Контекст турнира">
                <div className={styles.contextDetails}>
                  <code
                    className={styles.contextId}
                    data-testid="arena-tournament-id"
                    aria-label="Идентификатор турнира"
                  >
                    {tournamentId}
                  </code>
                </div>
                <div className={styles.contextAside}>
                  {role && <span className={styles.roleName}>{ROLE_LABELS[role]}</span>}
                  <Status
                    tone={STATUS_TONES[accessStatus]}
                    data-testid="arena-status"
                  >
                    {tournamentStateLabel ?? STATUS_LABELS[accessStatus]}
                  </Status>
                  {onLogout && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="small"
                      loading={logoutPending}
                      loadingLabel="Выход"
                      onClick={onLogout}
                    >
                      Выйти
                    </Button>
                  )}
                </div>
              </section>
            </>
          )}
        </header>

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
              <div className={styles.metrics} role="list" aria-label="Сводка турнира">
                {summary.metrics.map((metric) => (
                  <div className={styles.metric} role="listitem" key={metric.label}>
                    <span className={styles.metricLabel}>{metric.label}</span>
                    <span className={styles.metricValue}>{metric.value}</span>
                  </div>
                ))}
              </div>
              {summary.readOnly && (
                <Message className={styles.readOnlyNote} tone="info" title="Результаты зафиксированы">
                  Действия турнира отключены. Доступен только просмотр состояния.
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
