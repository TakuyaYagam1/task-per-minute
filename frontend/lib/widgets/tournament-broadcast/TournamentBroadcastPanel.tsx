"use client";

import { useEffect, useMemo, useState } from "react";

import type { PublicRecoveryState } from "../../shared/api";
import {
  formatArenaDateTime,
  formatDurationClock,
  formatSeriesFormat,
  formatSeriesState,
  formatWaveState,
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
  winnerName?: string;
}>;

type SwissRound = Readonly<{
  roundNumber: number;
  state: string;
  bye: Readonly<{
    displayName: string;
    pointsAwarded: number;
  }> | null;
}>;

type PublicDisplayWithSwissRounds = PublicRecoveryState["display"] & {
  swissRounds?: readonly Record<string, unknown>[];
};

type BroadcastPhase = "waiting" | "live" | "technical_pause" | "cancelled" | "completed";
type ProjectionView = "scoreboard" | "swiss" | "playoff";

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

const displayName = (value: unknown): string => stringValue(value) ?? "Ожидается";

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
  if (!seriesId) {
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
    firstName: displayName(item.first_display_name),
    secondName: displayName(item.second_display_name),
    firstWins,
    secondWins,
    format: stringValue(item.format),
    state: stringValue(item.state) ?? "planned",
    scheduledAt,
  }];
});

const bracketMatchFrom = (
  item: Record<string, unknown> | undefined,
  stage: "semifinal" | "final",
  position: number,
): BroadcastMatch => {
  const [firstWins, secondWins] = scoreValues(item?.score);
  return {
    key: `bracket:${stage}:${position}`,
    stage: stageLabel(stage),
    round: `Матч ${position}`,
    firstName: displayName(item?.first_display_name),
    secondName: displayName(item?.second_display_name),
    firstWins,
    secondWins,
    format: stringValue(item?.format) ?? (stage === "final" ? "bo3" : undefined),
    state: stringValue(item?.state) ?? "planned",
    scheduledAt: stringValue(item?.scheduled_at ?? item?.starts_at),
    winnerName: stringValue(item?.winner_display_name),
  };
};

const bracketMatchesFrom = (
  items: readonly Record<string, unknown>[],
): BroadcastMatch[] => {
  if (items.length === 0) {
    return [];
  }
  const findBracketItem = (stage: "semifinal" | "final", position: number) =>
    items.find((item) => item.stage === stage && item.position === position);
  const semifinalOne = bracketMatchFrom(findBracketItem("semifinal", 1), "semifinal", 1);
  const semifinalTwo = bracketMatchFrom(findBracketItem("semifinal", 2), "semifinal", 2);
  const finalItem = items
    .filter((item) => item.stage === "final")
    .sort((first, second) => (integerValue(first.position) ?? 1) - (integerValue(second.position) ?? 1))[0];
  const final = bracketMatchFrom(finalItem, "final", 1);
  return [semifinalOne, semifinalTwo, final];
};

const swissRoundsFrom = (state: PublicRecoveryState | null): SwissRound[] => {
  const display = state?.display as PublicDisplayWithSwissRounds | undefined;
  const rounds = display?.swissRounds;
  if (!Array.isArray(rounds)) {
    return [];
  }
  return rounds.flatMap((item) => {
    const roundNumber = integerValue(item.round_number);
    if (roundNumber === undefined) {
      return [];
    }
    const bye = isRecord(item.bye)
      ? {
          displayName: displayName(item.bye.display_name),
          pointsAwarded: integerValue(item.bye.points_awarded) ?? 0,
        }
      : null;
    return [{
      roundNumber,
      state: stringValue(item.state) ?? "planned",
      bye,
    }];
  });
};

const swissMatchesFrom = (
  items: readonly Record<string, unknown>[],
): ReadonlyMap<number, BroadcastMatch[]> => {
  const grouped = new Map<number, BroadcastMatch[]>();
  for (const item of items) {
    if (item.stage !== "swiss") {
      continue;
    }
    const roundNumber = integerValue(item.round_number ?? item.round);
    const seriesId = stringValue(item.series_id);
    if (roundNumber === undefined || !seriesId) {
      continue;
    }
    const matches = grouped.get(roundNumber) ?? [];
    const [firstWins, secondWins] = scoreValues(item.score);
    matches.push({
      key: `series:${seriesId}`,
      stage: "Swiss",
      round: `Раунд ${roundNumber}`,
      firstName: displayName(item.first_display_name),
      secondName: displayName(item.second_display_name),
      firstWins,
      secondWins,
      format: stringValue(item.format),
      state: stringValue(item.state) ?? "planned",
      scheduledAt: stringValue(item.scheduled_at ?? item.starts_at),
    });
    grouped.set(roundNumber, matches);
  }
  return grouped;
};

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

const matchButtonLabel = (match: BroadcastMatch): string =>
  `${match.stage} ${match.round}: ${match.firstName} - ${match.secondName}, ` +
  `${match.firstWins}:${match.secondWins}, ` +
  `${match.format ? `${formatSeriesFormat(match.format)}, ` : ""}${formatSeriesState(match.state)}`;

const eliminatedNameFrom = (match: BroadcastMatch): string | undefined => {
  if (!match.winnerName) {
    return undefined;
  }
  return [match.firstName, match.secondName]
    .find((name) => name !== "Ожидается" && name !== match.winnerName);
};

const MatchButton = ({
  match,
  selected,
  onSelect,
}: Readonly<{
  match: BroadcastMatch;
  selected: boolean;
  onSelect: (match: BroadcastMatch) => void;
}>) => (
  <button
    aria-label={matchButtonLabel(match)}
    aria-pressed={selected}
    className={styles.matchButton}
    data-match-key={match.key}
    data-selected={selected ? "true" : "false"}
    onClick={() => onSelect(match)}
    type="button"
  >
    <span className={styles.matchMeta}>{match.stage} | {match.round}</span>
    <span className={styles.matchNames}>{match.firstName} - {match.secondName}</span>
    <span className={styles.matchScore}>{match.firstWins}:{match.secondWins}</span>
    {match.format && <span className={styles.matchFormat}>{formatSeriesFormat(match.format)}</span>}
    <span className={styles.matchStateInline}>{formatSeriesState(match.state)}</span>
  </button>
);

export const TournamentBroadcastPanel = ({
  connectionStatus,
  state,
}: TournamentBroadcastPanelProps) => {
  const [selectedMatchKey, setSelectedMatchKey] = useState<string | null>(null);
  const [activeView, setActiveView] = useState<ProjectionView>("scoreboard");
  const tournament = state?.display.tournament;
  const tournamentState = tournament ? stringValue(tournament.state) : undefined;
  const phase = phaseFrom(tournamentState);
  const phaseCopy = PHASE_COPY[phase];
  const swissRounds = useMemo(() => swissRoundsFrom(state), [state]);
  const swissSeries = useMemo(
    () => (state?.display.liveSeries ?? []).filter((item) => item.stage === "swiss"),
    [state],
  );
  const swissMatches = useMemo(() => swissMatchesFrom(swissSeries), [swissSeries]);
  const bracket = useMemo(() => state?.display.bracket ?? [], [state]);
  const playoffMatches = useMemo(() => bracketMatchesFrom(bracket), [bracket]);
  const matches = useMemo(() => {
    if (state === null) {
      return [];
    }
    const liveMatches = liveMatchesFrom(
      state.display.liveSeries.filter((item) => item.stage !== "semifinal" && item.stage !== "final"),
      tournamentState,
    );
    return [...liveMatches, ...playoffMatches];
  }, [playoffMatches, state, tournamentState]);

  useEffect(() => {
    if (state !== null) {
      setSelectedMatchKey(selectedMatchFromLocation(matches));
    }
  }, [matches, state]);

  const selectedMatch = matches.find((match) => match.key === selectedMatchKey) ?? null;
  const startedAt = tournament ? stringValue(tournament.started_at) : undefined;
  const finishedAt = tournament ? stringValue(tournament.finished_at) : undefined;
  const scoreboard = useMemo(() => state?.display.scoreboard ?? [], [state]);
  const qualifiedEntries = useMemo(() => scoreboard
    .filter((entry) => entry.qualification_status === "qualified")
    .sort((first, second) => (integerValue(first.rank) ?? 0) - (integerValue(second.rank) ?? 0))
    .slice(0, 4), [scoreboard]);

  const selectMatch = (match: BroadcastMatch): void => {
    setSelectedMatchKey(match.key);
    updateSelectedMatch(match.key);
  };

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
                  <MatchButton
                    match={match}
                    onSelect={selectMatch}
                    selected={selectedMatch?.key === match.key}
                  />
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
              {selectedMatch.winnerName && (
                <p className={styles.serverOutcome}>
                  {selectedMatch.stage === "Финал" ? "Чемпион" : "Победитель"}: {selectedMatch.winnerName}
                </p>
              )}
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
        <div className={styles.tabs} role="tablist" aria-label="Таблица и этапы турнира">
          <button
            aria-controls="broadcast-scoreboard"
            aria-selected={activeView === "scoreboard"}
            className={styles.tab}
            onClick={() => setActiveView("scoreboard")}
            role="tab"
            type="button"
          >
            Таблица
          </button>
          <button
            aria-controls="broadcast-swiss"
            aria-selected={activeView === "swiss"}
            className={styles.tab}
            onClick={() => setActiveView("swiss")}
            role="tab"
            type="button"
          >
            Swiss
          </button>
          <button
            aria-controls="broadcast-playoff"
            aria-selected={activeView === "playoff"}
            className={styles.tab}
            onClick={() => setActiveView("playoff")}
            role="tab"
            type="button"
          >
            Плей-офф
          </button>
        </div>

        {activeView === "scoreboard" ? (
          <div className={styles.tableWrap} id="broadcast-scoreboard" role="tabpanel">
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
                      <th scope="row">{displayName(entry.display_name)}</th>
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
        ) : activeView === "swiss" ? (
          <div className={styles.swiss} id="broadcast-swiss" role="tabpanel">
            {swissRounds.length === 0 ? (
              <p className={styles.empty}>Сервер пока не опубликовал Swiss-туры.</p>
            ) : (
              swissRounds.map((round) => {
                const roundMatches = swissMatches.get(round.roundNumber) ?? [];
                return (
                  <section className={styles.swissRound} data-testid={`swiss-round-${round.roundNumber}`} key={round.roundNumber}>
                    <div className={styles.swissRoundHeader}>
                      <h3>Раунд {round.roundNumber}</h3>
                      <span className={styles.roundState} data-state={round.state}>
                        {formatWaveState(round.state)}
                      </span>
                    </div>
                    {roundMatches.length > 0 ? (
                      <ul className={styles.swissMatchList}>
                        {roundMatches.map((match) => (
                          <li key={match.key}>
                            <MatchButton
                              match={match}
                              onSelect={selectMatch}
                              selected={selectedMatch?.key === match.key}
                            />
                          </li>
                        ))}
                      </ul>
                    ) : (
                      <p className={styles.empty}>Пары пока не опубликованы.</p>
                    )}
                    {round.bye && (
                      <div className={styles.byeRow} data-testid={`swiss-bye-${round.roundNumber}`}>
                        <span><strong>Bye</strong> - {round.bye.displayName}</span>
                        <strong>+{round.bye.pointsAwarded} {round.bye.pointsAwarded === 1 ? "очко" : "очка"}</strong>
                      </div>
                    )}
                  </section>
                );
              })
            )}
          </div>
        ) : (
          <div className={styles.playoff} id="broadcast-playoff" role="tabpanel">
            <section className={styles.topFour} aria-labelledby="top-four-title">
              <div className={styles.sectionHeading}>
                <h3 id="top-four-title">Top 4</h3>
                <span>{qualifiedEntries.length}</span>
              </div>
              {qualifiedEntries.length === 0 ? (
                <p className={styles.empty}>Сервер пока не определил Top 4.</p>
              ) : (
                <ol className={styles.topFourList}>
                  {qualifiedEntries.map((entry) => (
                    <li key={`${String(entry.rank)}:${String(entry.display_name)}`}>
                      <span className={styles.topFourRank}>#{integerValue(entry.rank) ?? "-"}</span>
                      <span className={styles.topFourName}>{displayName(entry.display_name)}</span>
                      <span className={styles.qualification} data-status={stringValue(entry.qualification_status) ?? "pending"}>
                        {qualificationLabel(entry.qualification_status)}
                      </span>
                    </li>
                  ))}
                </ol>
              )}
            </section>

            <div className={styles.bracketScroller}>
              {playoffMatches.length === 0 ? (
                <p className={styles.empty}>Сетка появится после публикации плей-офф.</p>
              ) : (
                <div className={styles.bracketStages}>
                  {playoffMatches.slice(0, 2).map((match) => (
                    <article className={styles.bracketMatch} data-testid={`playoff-${match.key}`} key={match.key}>
                      <div className={styles.bracketMatchHeader}>
                        <span>{match.stage} | {match.round}</span>
                        <span className={styles.matchStateInline}>{formatSeriesState(match.state)}</span>
                      </div>
                      <MatchButton
                        match={match}
                        onSelect={selectMatch}
                        selected={selectedMatch?.key === match.key}
                      />
                      {match.winnerName && (
                        <p className={styles.bracketOutcome}>Прошел дальше: {match.winnerName}</p>
                      )}
                      {eliminatedNameFrom(match) && (
                        <p className={styles.bracketElimination}>Выбыл: {eliminatedNameFrom(match)}</p>
                      )}
                    </article>
                  ))}
                  <article className={`${styles.bracketMatch} ${styles.finalMatch}`} data-testid="playoff-final">
                    <div className={styles.bracketMatchHeader}>
                      <span>Финал | Матч 1</span>
                      <span className={styles.matchStateInline}>{formatSeriesState(playoffMatches[2]?.state)}</span>
                    </div>
                    {playoffMatches[2] && (
                      <MatchButton
                        match={playoffMatches[2]}
                        onSelect={selectMatch}
                        selected={selectedMatch?.key === playoffMatches[2].key}
                      />
                    )}
                    {playoffMatches[2]?.winnerName && (
                      <p className={styles.bracketOutcome}>Чемпион: {playoffMatches[2].winnerName}</p>
                    )}
                    {playoffMatches[2] && eliminatedNameFrom(playoffMatches[2]) && (
                      <p className={styles.bracketElimination}>Выбыл: {eliminatedNameFrom(playoffMatches[2])}</p>
                    )}
                  </article>
                </div>
              )}
            </div>
          </div>
        )}
      </section>
    </section>
  );
};

TournamentBroadcastPanel.displayName = "TournamentBroadcastPanel";
