"use client";

import { useEffect, useMemo, useState } from "react";

import type { PublicRecoveryState } from "../../shared/api";
import {
  formatArenaDateTime,
  formatDurationClock,
  formatSeriesFormat,
  formatSeriesState,
  formatTournamentState,
} from "../../shared/lib";

import styles from "./TournamentBroadcastPanel.module.css";

type PublicConnectionStatus =
  | "idle"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "recovering"
  | "rejected"
  | "error";

type TournamentBroadcastPanelProps = Readonly<{
  connectionStatus: PublicConnectionStatus;
  state: PublicRecoveryState | null;
}>;

type BroadcastMatch = Readonly<{
  key: string;
  stage: string;
  round: string;
  firstName: string;
  secondName: string;
  firstWins: number;
  secondWins: number;
  format?: string;
  state: string;
  scheduledAt?: string;
}>;

type BroadcastPhase = "waiting" | "live" | "technical_pause" | "cancelled" | "completed";

const CONNECTION_LABELS: Readonly<Record<PublicConnectionStatus, string>> = {
  idle: "Ожидание",
  connecting: "Подключение",
  connected: "На связи",
  reconnecting: "Переподключение",
  recovering: "Синхронизация",
  rejected: "Доступ отклонен",
  error: "Данные устарели",
};

const PHASE_COPY: Readonly<Record<BroadcastPhase, Readonly<{ title: string; description: string }>>> = {
  waiting: {
    title: "Турнир ожидает старта",
    description: "Матчи появятся после публикации серверного расписания.",
  },
  live: {
    title: "Турнир идет",
    description: "Счет и состояния матчей обновляются из публичного server snapshot.",
  },
  technical_pause: {
    title: "Техническая пауза",
    description: "Сервер остановил ход турнира. Результаты и дедлайны не вычисляются локально.",
  },
  cancelled: {
    title: "Турнир отменен",
    description: "Показывается последнее подтвержденное публичное состояние.",
  },
  completed: {
    title: "Турнир завершен",
    description: "Таблица и сетка зафиксированы сервером и доступны только для просмотра.",
  },
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === "object" && !Array.isArray(value);

const stringValue = (value: unknown): string | undefined =>
  typeof value === "string" && value.trim().length > 0 ? value : undefined;

const integerValue = (value: unknown): number | undefined =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 0
    ? value
    : undefined;

const qualificationLabel = (value: unknown): string => {
  switch (value) {
    case "qualified":
      return "Прошел дальше";
    case "eliminated":
      return "Выбыл";
    default:
      return "Ожидает решения";
  }
};

const scoreValues = (value: unknown): readonly [number, number] => {
  if (!isRecord(value)) {
    return [0, 0];
  }
  return [
    integerValue(value.first_wins ?? value.first_participant_wins) ?? 0,
    integerValue(value.second_wins ?? value.second_participant_wins) ?? 0,
  ];
};

const stageLabel = (value: unknown): string => {
  switch (value) {
    case "swiss":
      return "Swiss";
    case "golden":
      return "Golden";
    case "playoffs":
      return "Плей-офф";
    case "semifinal":
      return "Полуфинал";
    case "final":
      return "Финал";
    default:
      return formatTournamentState(value);
  }
};

const phaseFrom = (state: unknown): BroadcastPhase => {
  switch (state) {
    case "technical_pause":
      return "technical_pause";
    case "cancelled":
      return "cancelled";
    case "completed":
      return "completed";
    case "swiss":
    case "golden":
    case "playoffs":
      return "live";
    default:
      return "waiting";
  }
};

const liveMatchesFrom = (
  items: readonly Record<string, unknown>[],
  tournamentStage: unknown,
): BroadcastMatch[] => items.flatMap((item) => {
  const seriesId = stringValue(item.series_id);
  const firstName = stringValue(item.first_display_name);
  const secondName = stringValue(item.second_display_name);
  const state = stringValue(item.state);
  if (!seriesId || !firstName || !secondName || !state) {
    return [];
  }
  const [firstWins, secondWins] = scoreValues(item.score);
  const round = integerValue(item.round_number ?? item.round);
  const gamePosition = integerValue(item.current_game_position);
  const scheduledAt = stringValue(item.scheduled_at ?? item.starts_at);
  return [{
    key: `series:${seriesId}`,
    stage: stageLabel(item.stage ?? tournamentStage),
    round: round !== undefined
      ? `Раунд ${round}`
      : gamePosition !== undefined
        ? `Игра ${gamePosition}`
        : "Раунд не объявлен",
    firstName,
    secondName,
    firstWins,
    secondWins,
    format: stringValue(item.format),
    state,
    scheduledAt,
  }];
});

const bracketMatchesFrom = (
  items: readonly Record<string, unknown>[],
): BroadcastMatch[] => items.flatMap((item) => {
  const stage = stringValue(item.stage);
  const position = integerValue(item.position);
  const firstName = stringValue(item.first_display_name);
  const secondName = stringValue(item.second_display_name);
  const state = stringValue(item.state);
  if (!stage || position === undefined || !firstName || !secondName || !state) {
    return [];
  }
  const [firstWins, secondWins] = scoreValues(item.score);
  return [{
    key: `bracket:${stage}:${position}`,
    stage: stageLabel(stage),
    round: `Матч ${position}`,
    firstName,
    secondName,
    firstWins,
    secondWins,
    state,
    scheduledAt: stringValue(item.scheduled_at ?? item.starts_at),
  }];
});

const selectedMatchFromLocation = (matches: readonly BroadcastMatch[]): string | null => {
  if (typeof window === "undefined") {
    return null;
  }
  const requested = new URLSearchParams(window.location.search).get("match");
  return matches.some((match) => match.key === requested)
    ? requested
    : matches[0]?.key ?? null;
};

const updateSelectedMatch = (key: string): void => {
  const url = new URL(window.location.href);
  url.searchParams.set("match", key);
  window.history.replaceState(window.history.state, "", url);
};

export const TournamentBroadcastPanel = ({
  connectionStatus,
  state,
}: TournamentBroadcastPanelProps) => {
  const [selectedMatchKey, setSelectedMatchKey] = useState<string | null>(null);
  const [activeView, setActiveView] = useState<"scoreboard" | "bracket">("scoreboard");
  const tournament = state?.display.tournament;
  const tournamentState = tournament ? stringValue(tournament.state) : undefined;
  const phase = phaseFrom(tournamentState);
  const phaseCopy = PHASE_COPY[phase];
  const matches = useMemo(() => {
    if (state === null) {
      return [];
    }
    return [
      ...liveMatchesFrom(state.display.liveSeries, tournamentState),
      ...bracketMatchesFrom(state.display.bracket),
    ];
  }, [state, tournamentState]);

  useEffect(() => {
    if (state !== null) {
      setSelectedMatchKey(selectedMatchFromLocation(matches));
    }
  }, [matches, state]);

  const selectedMatch = matches.find((match) => match.key === selectedMatchKey) ?? null;
  const startedAt = tournament ? stringValue(tournament.started_at) : undefined;
  const finishedAt = tournament ? stringValue(tournament.finished_at) : undefined;
  const scoreboard = state?.display.scoreboard ?? [];
  const bracket = state?.display.bracket ?? [];

  return (
    <section
      aria-labelledby="broadcast-title"
      className={styles.panel}
      data-phase={phase}
      data-testid="tournament-broadcast"
    >
      <header className={styles.header}>
        <div>
          <h2 className={styles.title} id="broadcast-title">Матч-центр</h2>
          <p className={styles.lead}>{phaseCopy.description}</p>
        </div>
        <div className={styles.connection} data-connection={connectionStatus} role="status">
          <span aria-hidden="true" className={styles.connectionDot} />
          {CONNECTION_LABELS[connectionStatus]}
        </div>
      </header>

      <div className={styles.phase}>
        <strong data-testid="broadcast-phase-title">{phaseCopy.title}</strong>
        <span>{tournamentState ? stageLabel(tournamentState) : "Состояние загружается"}</span>
        {startedAt && <span>Старт: {formatArenaDateTime(startedAt)}</span>}
        {finishedAt && <span>Финиш: {formatArenaDateTime(finishedAt)}</span>}
      </div>

      <div className={styles.matchCenter}>
        <section className={styles.schedule} aria-labelledby="schedule-title">
          <div className={styles.sectionHeading}>
            <h3 id="schedule-title">Матчи сервера</h3>
            <span>{matches.length}</span>
          </div>
          {matches.length === 0 ? (
            <p className={styles.empty}>Сервер пока не опубликовал матчи для просмотра.</p>
          ) : (
            <ul className={styles.matchList}>
              {matches.map((match) => (
                <li className={styles.matchItem} key={match.key}>
                  <button
                    aria-pressed={selectedMatch?.key === match.key}
                    className={styles.matchButton}
                    data-selected={selectedMatch?.key === match.key ? "true" : "false"}
                    onClick={() => {
                      setSelectedMatchKey(match.key);
                      updateSelectedMatch(match.key);
                    }}
                    type="button"
                  >
                    <span className={styles.matchMeta}>{match.stage} | {match.round}</span>
                    <span className={styles.matchNames}>{match.firstName} - {match.secondName}</span>
                    <span className={styles.matchScore}>{match.firstWins}:{match.secondWins}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className={styles.selected} aria-labelledby="selected-match-title">
          {selectedMatch ? (
            <>
              <div className={styles.selectedHeader}>
                <div>
                  <p className={styles.selectedMeta}>{selectedMatch.stage} | {selectedMatch.round}</p>
                  <h3 id="selected-match-title">{selectedMatch.firstName} - {selectedMatch.secondName}</h3>
                </div>
                <span className={styles.matchState} data-state={selectedMatch.state}>
                  {formatSeriesState(selectedMatch.state)}
                </span>
              </div>
              <div className={styles.scoreboardLine} aria-label="Счет выбранного матча">
                <span>{selectedMatch.firstName}</span>
                <strong>{selectedMatch.firstWins}:{selectedMatch.secondWins}</strong>
                <span>{selectedMatch.secondName}</span>
              </div>
              <dl className={styles.matchFacts}>
                <div>
                  <dt>Этап</dt>
                  <dd>{selectedMatch.stage}</dd>
                </div>
                <div>
                  <dt>Раунд</dt>
                  <dd>{selectedMatch.round}</dd>
                </div>
                <div>
                  <dt>Формат</dt>
                  <dd>{selectedMatch.format ? formatSeriesFormat(selectedMatch.format) : "По сетке"}</dd>
                </div>
                <div>
                  <dt>Время</dt>
                  <dd>{selectedMatch.scheduledAt ? formatArenaDateTime(selectedMatch.scheduledAt) : "Не объявлено"}</dd>
                </div>
              </dl>
            </>
          ) : (
            <div className={styles.selectedEmpty}>
              <h3 id="selected-match-title">Матч еще не выбран</h3>
              <p>Здесь появятся только опубликованные сервером пары и результаты.</p>
            </div>
          )}
        </section>
      </div>

      <section className={styles.projections} aria-label="Публичные проекции турнира">
        <div className={styles.tabs} role="tablist" aria-label="Таблица и сетка">
          <button
            aria-selected={activeView === "scoreboard"}
            className={styles.tab}
            onClick={() => setActiveView("scoreboard")}
            role="tab"
            type="button"
          >
            Таблица
          </button>
          <button
            aria-selected={activeView === "bracket"}
            className={styles.tab}
            onClick={() => setActiveView("bracket")}
            role="tab"
            type="button"
          >
            Сетка
          </button>
        </div>

        {activeView === "scoreboard" ? (
          <div className={styles.tableWrap} role="tabpanel">
            {scoreboard.length === 0 ? (
              <p className={styles.empty}>Сервер пока не опубликовал состав таблицы.</p>
            ) : (
              <table aria-label="Публичная таблица турнира" className={styles.table}>
                <thead>
                  <tr>
                    <th scope="col">Место</th>
                    <th scope="col">Участник</th>
                    <th scope="col">Очки</th>
                    <th scope="col">Победы</th>
                    <th scope="col">Поражения</th>
                    <th scope="col">Bye</th>
                    <th scope="col">Бухгольц</th>
                    <th scope="col">Время</th>
                    <th scope="col">Статус</th>
                  </tr>
                </thead>
                <tbody>
                  {scoreboard.map((entry, index) => (
                    <tr key={`${String(entry.display_name)}:${String(entry.rank)}:${index}`}>
                      <td>{integerValue(entry.rank) ?? "-"}</td>
                      <th scope="row">{stringValue(entry.display_name) ?? "Участник"}</th>
                      <td>{integerValue(entry.points) ?? "-"}</td>
                      <td>{integerValue(entry.wins) ?? "-"}</td>
                      <td>{integerValue(entry.losses) ?? "-"}</td>
                      <td>{integerValue(entry.bye_count) ?? "-"}</td>
                      <td>{integerValue(entry.buchholz) ?? "-"}</td>
                      <td>{formatDurationClock(integerValue(entry.effective_time_ms))}</td>
                      <td>
                        <span
                          className={styles.qualification}
                          data-status={stringValue(entry.qualification_status) ?? "pending"}
                        >
                          {qualificationLabel(entry.qualification_status)}
                        </span>
                        {entry.provisional_tie === true && (
                          <span className={styles.provisionalTie}>Тай-брейк не решен</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        ) : (
          <div className={styles.bracket} role="tabpanel">
            {bracket.length === 0 ? (
              <p className={styles.empty}>Сетка появится после публикации плей-офф.</p>
            ) : bracketMatchesFrom(bracket).map((match) => (
              <article className={styles.bracketMatch} key={match.key}>
                <span>{match.stage} | {match.round}</span>
                <strong>{match.firstName} {match.firstWins}:{match.secondWins} {match.secondName}</strong>
                <small>{formatSeriesState(match.state)}</small>
              </article>
            ))}
          </div>
        )}
      </section>
    </section>
  );
};

TournamentBroadcastPanel.displayName = "TournamentBroadcastPanel";
