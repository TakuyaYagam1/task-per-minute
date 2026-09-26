"use client";

import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";

import { formatCountdown, useServerCountdown } from "../../features/tournament-live";
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

export type TournamentBroadcastView =
  | "all"
  | "overview"
  | "matches"
  | "bracket"
  | "standings";

type TournamentBroadcastPanelProps = Readonly<{
  connectionStatus: PublicConnectionStatus;
  receivedAtMonotonicMs?: number;
  serverTimestamp?: string;
  state: PublicRecoveryState | null;
  view?: TournamentBroadcastView;
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

type BroadcastCurrentGame = Readonly<{
  category: string | null;
  effectiveDeadline: string | null;
  finishedAt: string | null;
  firstConnectionStatus: string | null;
  position: number | null;
  resultReason: string | null;
  secondConnectionStatus: string | null;
  startedAt: string | null;
  state: string | null;
  winnerName: string | null;
}>;

type BroadcastDraftAction = Readonly<{
  action: string | null;
  actorName: string;
  automatic: boolean;
  category: string | null;
  occurredAt: string | null;
  turn: number;
}>;

type BroadcastDraft = Readonly<{
  actions: readonly BroadcastDraftAction[];
  autoActionPending: boolean;
  currentAction: string | null;
  currentActorName: string;
  currentTurn: number | null;
  deadline: string | null;
  firstActorName: string;
  format: string | null;
  state: string | null;
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

const PROJECTION_TABS: ReadonlyArray<Readonly<{
  view: ProjectionView;
  label: string;
  tabId: string;
  panelId: string;
}>> = [
  {
    view: "scoreboard",
    label: "Таблица",
    tabId: "broadcast-scoreboard-tab",
    panelId: "broadcast-scoreboard",
  },
  {
    view: "swiss",
    label: "Swiss",
    tabId: "broadcast-swiss-tab",
    panelId: "broadcast-swiss",
  },
  {
    view: "playoff",
    label: "Плей-офф",
    tabId: "broadcast-playoff-tab",
    panelId: "broadcast-playoff",
  },
];

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
    description: "Матчи появятся после публикации расписания.",
  },
  live: {
    title: "Турнир идет",
    description: "Счет и статусы матчей обновляются автоматически.",
  },
  technical_pause: {
    title: "Техническая пауза",
    description: "Турнир приостановлен. Ожидайте продолжения.",
  },
  cancelled: {
    title: "Турнир отменен",
    description: "Показаны результаты на момент отмены турнира.",
  },
  completed: {
    title: "Турнир завершен",
    description: "Итоговая таблица и сетка доступны для просмотра.",
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

const booleanValue = (value: unknown): boolean => value === true;

const dateTimeValue = (value: unknown): string | null => {
  const candidate = stringValue(value);
  return candidate !== undefined && Number.isFinite(Date.parse(candidate)) ? candidate : null;
};

const displayValueFrom = (
  record: Record<string, unknown> | null | undefined,
  key: string,
): string | undefined => {
  return stringValue(record?.[key]);
};

const displayName = (value: unknown): string => stringValue(value) ?? "Ожидается";

const optionalDisplayName = (value: unknown): string => stringValue(value) ?? "Не объявлен";

const resultReasonLabel = (value: unknown): string | null => {
  switch (value) {
    case "solved":
      return "Решено";
    case "timeout":
    case "deadline":
      return "Время вышло";
    case "forfeit":
    case "participant_forfeit":
    case "surrender":
      return "Сдача";
    case "operator_forfeit":
      return "Решение сервера";
    case "cancelled":
      return "Отменено сервером";
    case "technical_failure":
    case "task_failure":
    case "common_platform_failure":
      return "Техническая причина";
    case "score_complete":
      return "Победа по счету";
    case "no_solve":
      return "Не решено";
    case "disconnect":
      return "Отключение участника";
    case "no_show":
      return "Неявка участника";
    case "series_cancelled":
    case "tournament_cancelled":
      return "Отменено сервером";
    case "operator_correction":
      return "Исправление сервера";
    default:
      return null;
  }
};

const connectionLabel = (value: string | null): string => {
  switch (value) {
    case "connected":
    case "online":
    case "present":
      return "На связи";
    case "reconnecting":
      return "Переподключение";
    case "disconnected":
    case "offline":
    case "absent":
      return "Не в сети";
    default:
      return "Не опубликован";
  }
};

const actionLabel = (value: string | null): string => {
  switch (value) {
    case "ban":
      return "Бан";
    case "pick":
      return "Выбор";
    case "automatic":
    case "auto":
      return "Автоматический ход";
    case "wait":
      return "Ожидание";
    default:
      return value ?? "Не объявлено";
  }
};

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

const connectionStatusFrom = (
  record: Record<string, unknown> | null,
  key: string,
): string | null => {
  return stringValue(record?.[key]) ?? null;
};

const currentGameFrom = (
  item: Record<string, unknown> | undefined,
): BroadcastCurrentGame | null => {
  if (item === undefined || !isRecord(item.current_game)) {
    return null;
  }
  const source = item.current_game;
  const winner = displayValueFrom(source, "winner_display_name");
  return {
    category: stringValue(source.category) ?? null,
    effectiveDeadline: dateTimeValue(source.effective_deadline),
    finishedAt: dateTimeValue(source.finished_at),
    firstConnectionStatus: connectionStatusFrom(
      source,
      "first_connection_status",
    ),
    position: integerValue(source.position) ?? null,
    resultReason: stringValue(source.result_reason) ?? null,
    secondConnectionStatus: connectionStatusFrom(
      source,
      "second_connection_status",
    ),
    startedAt: dateTimeValue(source.started_at),
    state: stringValue(source.state) ?? null,
    winnerName: winner ?? null,
  };
};

const draftActionFrom = (value: unknown): BroadcastDraftAction | null => {
  if (!isRecord(value)) {
    return null;
  }
  const turn = integerValue(value.turn);
  if (turn === undefined) {
    return null;
  }
  return {
    action: stringValue(value.action) ?? null,
    actorName: optionalDisplayName(value.actor_display_name),
    automatic: booleanValue(value.automatic),
    category: stringValue(value.category) ?? null,
    occurredAt: dateTimeValue(value.occurred_at),
    turn,
  };
};

const draftFrom = (
  liveDraft: Record<string, unknown> | null,
  seriesId: string | null,
): BroadcastDraft | null => {
  if (!isRecord(liveDraft) || stringValue(liveDraft.series_id) !== seriesId) {
    return null;
  }
  const source = liveDraft;
  const actions = Array.isArray(source.actions)
    ? source.actions.flatMap((action) => {
      const parsed = draftActionFrom(action);
      return parsed === null ? [] : [parsed];
    })
    : [];
  const currentActor = displayValueFrom(
    source,
    "current_actor_display_name",
  );
  const currentTurn = integerValue(source.current_turn);
  return {
    actions,
    autoActionPending: booleanValue(source.auto_action_pending),
    currentAction: stringValue(source.current_action) ?? null,
    currentActorName: currentActor ?? "Не объявлен",
    currentTurn: currentTurn ?? null,
    deadline: dateTimeValue(source.turn_deadline),
    firstActorName: displayValueFrom(
      source,
      "first_actor_display_name",
    ) ?? "Не объявлен",
    format: stringValue(source.format) ?? null,
    state: stringValue(source.state) ?? null,
  };
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
  const scheduledAt = stringValue(item.scheduled_at);
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
  const seriesId = stringValue(item?.series_id ?? item?.seriesId);
  return {
    key: seriesId ? `series:${seriesId}` : `bracket:${stage}:${position}`,
    stage: stageLabel(stage),
    round: `Матч ${position}`,
    firstName: displayName(item?.first_display_name),
    secondName: displayName(item?.second_display_name),
    firstWins,
    secondWins,
    format: stringValue(item?.format) ?? (stage === "final" ? "bo3" : undefined),
    state: stringValue(item?.state) ?? "planned",
    scheduledAt: stringValue(item?.scheduled_at),
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
      scheduledAt: stringValue(item.scheduled_at),
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

const GameCountdown = ({
  deadline,
  receivedAtMonotonicMs,
  serverTimestamp,
}: Readonly<{
  deadline: string;
  receivedAtMonotonicMs?: number;
  serverTimestamp: string;
}>) => {
  const countdown = useServerCountdown({
    deadline,
    receivedAtMonotonicMs,
    serverTimestamp,
  });
  return (
    <span
      data-countdown-status={countdown.status}
      data-testid="broadcast-game-countdown"
    >
      {countdown.status === "running"
        ? formatCountdown(countdown.remainingMs)
        : "Ожидает подтверждения сервера"}
    </span>
  );
};

const CurrentGamePanel = ({
  game,
  receivedAtMonotonicMs,
  serverTimestamp,
  seriesId,
}: Readonly<{
  game: BroadcastCurrentGame | null;
  receivedAtMonotonicMs?: number;
  serverTimestamp?: string;
  seriesId: string | null;
}>) => {
  const validServerTimestamp = serverTimestamp !== undefined && Number.isFinite(Date.parse(serverTimestamp))
    ? serverTimestamp
    : undefined;
  const validDeadline = game?.effectiveDeadline !== null && game?.effectiveDeadline !== undefined &&
    Number.isFinite(Date.parse(game.effectiveDeadline))
    ? game.effectiveDeadline
    : undefined;

  return (
    <section
      className={styles.currentGame}
      aria-labelledby="broadcast-current-game-title"
      data-deadline={game?.effectiveDeadline ?? ""}
      data-series-id={seriesId ?? ""}
      data-testid="broadcast-selected-game"
    >
      <div className={styles.subsectionHeader}>
        <div>
          <h4 id="broadcast-current-game-title">Игра {game?.position ?? "-"}</h4>
        </div>
        <span className={styles.gameState}>{game?.state ? formatSeriesState(game.state) : "Не опубликована"}</span>
      </div>
      {game === null ? (
        <p className={styles.empty}>Текущая игра еще не опубликована сервером.</p>
      ) : (
        <>
          <dl className={styles.gameFacts}>
            <div>
              <dt>Категория</dt>
              <dd data-testid="broadcast-game-category">{game.category ?? "Не объявлена"}</dd>
            </div>
            <div>
              <dt>Дедлайн</dt>
              <dd data-testid="broadcast-game-deadline">
                {game.effectiveDeadline ? (
                  <time dateTime={game.effectiveDeadline}>{formatArenaDateTime(game.effectiveDeadline)}</time>
                ) : "Не объявлен"}
              </dd>
            </div>
            <div>
              <dt>Начало</dt>
              <dd>{game.startedAt ? formatArenaDateTime(game.startedAt) : "Не начата"}</dd>
            </div>
            <div>
              <dt>Завершение</dt>
              <dd>{game.finishedAt ? formatArenaDateTime(game.finishedAt) : "Не завершена"}</dd>
            </div>
            <div>
              <dt>Первый участник</dt>
              <dd>{connectionLabel(game.firstConnectionStatus)}</dd>
            </div>
            <div>
              <dt>Второй участник</dt>
              <dd>{connectionLabel(game.secondConnectionStatus)}</dd>
            </div>
          </dl>
          <div className={styles.gameCountdown}>
            <span>До серверного дедлайна</span>
            {validDeadline && validServerTimestamp ? (
              <GameCountdown
                deadline={validDeadline}
                receivedAtMonotonicMs={receivedAtMonotonicMs}
                serverTimestamp={validServerTimestamp}
              />
            ) : (
              <span data-testid="broadcast-game-countdown">Ожидает серверного дедлайна</span>
            )}
          </div>
          {(game.finishedAt || game.winnerName || resultReasonLabel(game.resultReason)) && (
            <p className={styles.gameOutcome}>
              {game.winnerName ? `Победитель: ${game.winnerName}` : "Результат ожидает подтверждения сервера"}
              {resultReasonLabel(game.resultReason) ? ` - ${resultReasonLabel(game.resultReason)}` : ""}
            </p>
          )}
        </>
      )}
    </section>
  );
};

const DraftPanel = ({
  draft,
  seriesId,
}: Readonly<{ draft: BroadcastDraft; seriesId: string | null }>) => (
  <section
    className={styles.draft}
    aria-labelledby="broadcast-draft-title"
    data-deadline={draft.deadline ?? ""}
    data-series-id={seriesId ?? ""}
    data-testid="broadcast-draft"
  >
    <div className={styles.subsectionHeader}>
      <div>
        <h4 id="broadcast-draft-title">Драфт {draft.format ? formatSeriesFormat(draft.format) : ""}</h4>
      </div>
      <span className={styles.gameState}>{draft.state ? formatSeriesState(draft.state) : "Синхронизация"}</span>
    </div>
    <dl className={styles.draftFacts}>
      <div>
        <dt>Первый ход</dt>
        <dd data-testid="broadcast-draft-first-actor">{draft.firstActorName}</dd>
      </div>
      <div>
        <dt>Текущий ход</dt>
        <dd data-testid="broadcast-draft-turn">{draft.currentTurn ?? "-"}</dd>
      </div>
      <div>
        <dt>Текущий участник</dt>
        <dd data-testid="broadcast-draft-current-actor">{draft.currentActorName}</dd>
      </div>
      <div>
        <dt>Действие</dt>
        <dd data-testid="broadcast-draft-current-action">{actionLabel(draft.currentAction)}</dd>
      </div>
      <div>
        <dt>Дедлайн хода</dt>
        <dd data-testid="broadcast-draft-deadline">
          {draft.deadline ? (
            <time dateTime={draft.deadline}>{formatArenaDateTime(draft.deadline)}</time>
          ) : "Не объявлен"}
        </dd>
      </div>
    </dl>
    {draft.autoActionPending && (
      <p className={styles.draftNotice}>Сервер готовит автоматическое действие.</p>
    )}
    {draft.actions.length > 0 && (
      <ol className={styles.draftActions} aria-label="Опубликованные ходы драфта">
        {draft.actions.map((action) => (
          <li
            data-automatic={action.automatic ? "true" : "false"}
            data-testid={`broadcast-draft-action-${action.turn}`}
            key={`${action.turn}:${action.category ?? "unknown"}`}
          >
            <span>Ход {action.turn}</span>
            <strong>{actionLabel(action.action)}</strong>
            {action.category && <span>{action.category}</span>}
            <span>{action.actorName}</span>
            {action.automatic && <em>Автоматически</em>}
          </li>
        ))}
      </ol>
    )}
  </section>
);

const OfficialResultPanel = ({ result }: Readonly<{ result: Record<string, unknown> }>) => {
  const [firstWins, secondWins] = scoreValues(result.score);
  const winnerName = displayValueFrom(result, "winner_display_name");
  const reason = resultReasonLabel(result.result_reason);
  return (
    <section className={styles.officialResult} aria-labelledby="broadcast-official-result-title" data-testid="broadcast-official-result">
      <div className={styles.subsectionHeader}>
        <div>
          <h4 id="broadcast-official-result-title">Официальный результат</h4>
        </div>
        <span className={styles.officialBadge}>Подтвержден сервером</span>
      </div>
      <p className={styles.officialScore}>{firstWins}:{secondWins}</p>
      {winnerName && <p className={styles.officialWinner}>Победитель: {winnerName}</p>}
      {reason && <p className={styles.officialReason}>Причина: {reason}</p>}
    </section>
  );
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
  receivedAtMonotonicMs,
  serverTimestamp,
  state,
  view = "all",
}: TournamentBroadcastPanelProps) => {
  const panelRef = useRef<HTMLElement | null>(null);
  const tabRefs = useRef<Partial<Record<ProjectionView, HTMLButtonElement | null>>>({});
  const [selectedMatchKey, setSelectedMatchKey] = useState<string | null>(null);
  const [activeView, setActiveView] = useState<ProjectionView>("scoreboard");
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [fullscreenSupported, setFullscreenSupported] = useState(false);
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
    const liveMatches = liveMatchesFrom(state.display.liveSeries, tournamentState);
    const fallbackBracket = playoffMatches.filter((match) => !liveMatches.some((liveMatch) => (
      liveMatch.stage === match.stage &&
      liveMatch.firstName === match.firstName &&
      liveMatch.secondName === match.secondName &&
      liveMatch.firstWins === match.firstWins &&
      liveMatch.secondWins === match.secondWins
    )));
    return [...liveMatches, ...fallbackBracket];
  }, [playoffMatches, state, tournamentState]);
  const selectableMatches = useMemo(() => [
    ...matches,
    ...playoffMatches.filter((match) => !matches.some((candidate) => candidate.key === match.key)),
  ], [matches, playoffMatches]);

  useEffect(() => {
    if (state !== null) {
      setSelectedMatchKey(selectedMatchFromLocation(selectableMatches));
    }
  }, [selectableMatches, state]);

  useEffect(() => {
    const handlePopState = (): void => {
      setSelectedMatchKey(selectedMatchFromLocation(selectableMatches));
    };
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, [selectableMatches]);

  const selectedMatch = selectableMatches.find((match) => match.key === selectedMatchKey) ?? null;
  const selectedSeriesId = selectedMatch?.key.startsWith("series:")
    ? selectedMatch.key.slice("series:".length)
    : null;
  const selectedSeries = state?.display.liveSeries.find(
    (item) => stringValue(item.series_id) === selectedSeriesId,
  );
  const selectedGame = useMemo(() => currentGameFrom(selectedSeries), [selectedSeries]);
  const selectedDraft = useMemo(
    () => draftFrom(state?.display.liveDraft ?? null, selectedSeriesId),
    [selectedSeriesId, state],
  );
  const selectedOfficialResult = useMemo(
    () => state?.display.officialResults.find(
      (item) => stringValue(item.series_id) === selectedSeriesId,
    ) ?? null,
    [selectedSeriesId, state],
  );
  const startedAt = tournament ? stringValue(tournament.started_at) : undefined;
  const finishedAt = tournament ? stringValue(tournament.finished_at) : undefined;
  const scoreboard = useMemo(() => state?.display.scoreboard ?? [], [state]);
  const qualifiedEntries = useMemo(() => scoreboard
    .filter((entry) => entry.qualification_status === "qualified")
    .sort((first, second) => (integerValue(first.rank) ?? 0) - (integerValue(second.rank) ?? 0))
    .slice(0, 4), [scoreboard]);
  const projectionView: ProjectionView = view === "bracket"
    ? (activeView === "swiss" || activeView === "playoff" ? activeView : "playoff")
    : view === "standings"
      ? "scoreboard"
      : activeView;
  const showMatchCenter = view === "all" || view === "overview" || view === "matches";
  const showProjections = view !== "matches";
  const showProjectionTabs = view === "all" || view === "overview" || view === "bracket";
  const projectionTabs = view === "bracket"
    ? PROJECTION_TABS.filter((tab) => tab.view === "swiss" || tab.view === "playoff")
    : PROJECTION_TABS;
  const projectionTabVisible = (tabView: ProjectionView): boolean =>
    showProjectionTabs && projectionTabs.some((tab) => tab.view === tabView);

  const selectMatch = (match: BroadcastMatch): void => {
    setSelectedMatchKey(match.key);
    updateSelectedMatch(match.key);
  };

  const selectProjectionView = (view: ProjectionView, moveFocus = false): void => {
    setActiveView(view);
    if (moveFocus) {
      tabRefs.current[view]?.focus();
    }
  };

  const handleProjectionKeyDown = (
    event: KeyboardEvent<HTMLButtonElement>,
    view: ProjectionView,
  ): void => {
    const currentIndex = projectionTabs.findIndex((tab) => tab.view === view);
    if (currentIndex < 0) {
      return;
    }

    let nextIndex: number | null = null;
    if (event.key === "ArrowRight") {
      nextIndex = (currentIndex + 1) % projectionTabs.length;
    } else if (event.key === "ArrowLeft") {
      nextIndex = (currentIndex - 1 + projectionTabs.length) % projectionTabs.length;
    } else if (event.key === "Home") {
      nextIndex = 0;
    } else if (event.key === "End") {
      nextIndex = projectionTabs.length - 1;
    }

    if (nextIndex === null) {
      return;
    }

    event.preventDefault();
    const nextTab = projectionTabs[nextIndex];
    if (nextTab !== undefined) {
      selectProjectionView(nextTab.view, true);
    }
  };

  useEffect(() => {
    const panel = panelRef.current;
    if (panel === null || typeof document === "undefined") {
      return undefined;
    }
    setFullscreenSupported(typeof panel.requestFullscreen === "function");
    const handleFullscreenChange = (): void => {
      setIsFullscreen(document.fullscreenElement === panel);
    };
    document.addEventListener("fullscreenchange", handleFullscreenChange);
    handleFullscreenChange();
    return () => {
      document.removeEventListener("fullscreenchange", handleFullscreenChange);
    };
  }, []);

  const toggleFullscreen = async (): Promise<void> => {
    const panel = panelRef.current;
    if (panel === null) {
      return;
    }
    if (document.fullscreenElement === panel) {
      if (typeof document.exitFullscreen === "function") {
        await document.exitFullscreen();
      }
      return;
    }
    if (typeof panel.requestFullscreen !== "function") {
      setFullscreenSupported(false);
      return;
    }
    try {
      await panel.requestFullscreen();
    } catch {
      setFullscreenSupported(false);
    }
  };

  return (
    <section
      aria-labelledby="broadcast-title"
      className={styles.panel}
      data-phase={phase}
      data-fullscreen={isFullscreen ? "true" : "false"}
      data-testid="tournament-broadcast"
      ref={panelRef}
    >
      <header className={styles.header}>
        <div>
          <h2 className={styles.title} id="broadcast-title">Матч-центр</h2>
          <p className={styles.lead}>{phaseCopy.description}</p>
        </div>
        <div className={styles.headerActions}>
          <div className={styles.connection} data-connection={connectionStatus} role="status" data-testid="broadcast-connection">
            <span aria-hidden="true" className={styles.connectionDot} />
            {CONNECTION_LABELS[connectionStatus]}
          </div>
          <button
            aria-pressed={isFullscreen}
            className={styles.fullscreenButton}
            data-testid="broadcast-fullscreen-toggle"
            disabled={!fullscreenSupported}
            onClick={() => { void toggleFullscreen(); }}
            type="button"
          >
            {fullscreenSupported
              ? (isFullscreen ? "Выйти из полного экрана" : "На весь экран")
              : "Полный экран недоступен"}
          </button>
        </div>
      </header>

      <div className={styles.phase}>
        <strong data-testid="broadcast-phase-title">{phaseCopy.title}</strong>
        <span>{tournamentState ? stageLabel(tournamentState) : "Состояние загружается"}</span>
        {startedAt && <span>Старт: {formatArenaDateTime(startedAt)}</span>}
        {finishedAt && <span>Финиш: {formatArenaDateTime(finishedAt)}</span>}
      </div>

      {showMatchCenter && <div className={styles.matchCenter}>
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

        <section className={styles.selected} aria-labelledby="selected-match-title" data-testid="broadcast-selected-series">
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
              <CurrentGamePanel
                game={selectedGame}
                key={`${selectedSeriesId ?? "none"}:${selectedGame?.position ?? "none"}`}
                receivedAtMonotonicMs={receivedAtMonotonicMs}
                serverTimestamp={serverTimestamp}
                seriesId={selectedSeriesId}
              />
              {selectedDraft && (selectedDraft.format === "bo1" || selectedDraft.format === "bo3") && (
                <DraftPanel draft={selectedDraft} seriesId={selectedSeriesId} />
              )}
              {selectedOfficialResult && <OfficialResultPanel result={selectedOfficialResult} />}
            </>
          ) : (
            <div className={styles.selectedEmpty}>
              <h3 id="selected-match-title">Матч еще не выбран</h3>
              <p>Здесь появятся только опубликованные сервером пары и результаты.</p>
            </div>
          )}
        </section>
      </div>}

      {showProjections && <section className={styles.projections} aria-label="Публичные проекции турнира">
        {showProjectionTabs && <div className={styles.tabs} role="tablist" aria-label="Таблица и этапы турнира">
          {projectionTabs.map((tab) => (
            <button
              aria-controls={tab.panelId}
              aria-selected={projectionView === tab.view}
              className={styles.tab}
              id={tab.tabId}
              key={tab.view}
              onClick={() => selectProjectionView(tab.view)}
              onKeyDown={(event) => handleProjectionKeyDown(event, tab.view)}
              ref={(element) => {
                tabRefs.current[tab.view] = element;
              }}
              role="tab"
              tabIndex={projectionView === tab.view ? 0 : -1}
              type="button"
            >
              {tab.label}
            </button>
          ))}
        </div>}

        {projectionView === "scoreboard" ? (
          <div
            {...(showProjectionTabs
              ? { "aria-labelledby": "broadcast-scoreboard-tab" }
              : { "aria-label": "Турнирная таблица" })}
            className={styles.tableWrap}
            id="broadcast-scoreboard"
            role="tabpanel"
          >
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
        ) : projectionView === "swiss" ? (
          <div
            {...(showProjectionTabs
              ? { "aria-labelledby": "broadcast-swiss-tab" }
              : { "aria-label": "Swiss этап" })}
            className={styles.swiss}
            id="broadcast-swiss"
            role="tabpanel"
          >
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
          <div
            {...(showProjectionTabs
              ? { "aria-labelledby": "broadcast-playoff-tab" }
              : { "aria-label": "Плей-офф" })}
            className={styles.playoff}
            id="broadcast-playoff"
            role="tabpanel"
          >
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
        {projectionTabVisible("scoreboard") && projectionView !== "scoreboard" && (
          <div
            aria-labelledby="broadcast-scoreboard-tab"
            hidden
            id="broadcast-scoreboard"
            role="tabpanel"
          />
        )}
        {projectionTabVisible("swiss") && projectionView !== "swiss" && (
          <div
            aria-labelledby="broadcast-swiss-tab"
            hidden
            id="broadcast-swiss"
            role="tabpanel"
          />
        )}
        {projectionTabVisible("playoff") && projectionView !== "playoff" && (
          <div
            aria-labelledby="broadcast-playoff-tab"
            hidden
            id="broadcast-playoff"
            role="tabpanel"
          />
        )}
      </section>}
    </section>
  );
};

TournamentBroadcastPanel.displayName = "TournamentBroadcastPanel";
