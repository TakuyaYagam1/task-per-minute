"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  ApiError,
  createOperatorCommandIntent,
  goldenApi,
  operatorApi,
  publicTournamentApi,
  type GoldenOperatorGroup,
  type GoldenOperatorResponse,
  type PublicBracketResponse,
  type PublicScoreboardResponse,
  type PublicTournamentResponse,
  type Tournament,
} from "../../shared/api";
import { useAdminLiveRefresh } from "../../features/admin-live";
import { GOLDEN_STATE_LABELS, formatSeriesState, formatTournamentState } from "../../shared/lib";
import { useParticipantNames } from "../../entities/tournament";
import { Button, Message, Panel, Status, type StatusTone } from "../../shared/ui";

import styles from "./GoldenPlayoffControlPanel.module.css";

type GoldenPlayoffControlPanelProps = Readonly<{
  onSessionExpired?: () => void;
  onTournamentUpdated?: (tournament: Tournament) => void;
  tournament: Tournament | null;
}>;

type LoadState = "loading" | "ready" | "error";
type LoadOptions = Readonly<{ silent?: boolean }>;
type LifecycleAction = "start_golden" | "start_playoffs";
type PublicBracketMatch = PublicBracketResponse["matches"][number];

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const problemMessage = (error: unknown, fallback: string): string => {
  if (error instanceof ApiError) {
    return error.problem?.detail || error.problem?.title || fallback;
  }
  return fallback;
};

const stateTone = (state: GoldenOperatorGroup["state"]): StatusTone => {
  switch (state) {
    case "active":
      return "live";
    case "completed":
      return "success";
    case "technical_pause":
      return "warning";
    case "cancelled":
    case "superseded":
      return "error";
    case "ready":
      return "info";
    default:
      return "neutral";
  }
};

const memberTone = (ready: boolean): StatusTone => (ready ? "success" : "warning");

const formatDateTime = (value: string | null | undefined): string => {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "Дата недоступна";
  }
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(date);
};

const bracketDisplayName = (value: string | null): string => value ?? "Ожидается";

const groupIsReady = (group: GoldenOperatorGroup): boolean =>
  group.state === "ready" &&
  group.members.length > 0 &&
  group.members.every((member) => member.ready);

const GOLDEN_CONTROL_STATES: ReadonlySet<Tournament["state"]> = new Set([
  "swiss",
  "golden",
  "playoffs",
  "completed",
]);

const hasCompleteGolden = (golden: GoldenOperatorResponse | null): boolean =>
  golden !== null &&
  golden.groups.length > 0 &&
  golden.groups.every((group) => group.state === "completed");

const isExactPlayoffBracket = (matches: readonly PublicBracketMatch[]): boolean => {
  const semifinalPositions = matches
    .filter((match) => match.stage === "semifinal")
    .map((match) => match.position)
    .sort((left, right) => left - right);
  const finals = matches.filter((match) => match.stage === "final");
  return (
    semifinalPositions.length === 2 &&
    semifinalPositions[0] === 1 &&
    semifinalPositions[1] === 2 &&
    finals.length <= 1 &&
    (finals.length === 0 || finals[0]?.position === 1) &&
    matches.length === 2 + finals.length
  );
};

const orderedBracketMatches = (
  bracket: PublicBracketResponse | null,
): readonly PublicBracketMatch[] => {
  if (bracket === null || !isExactPlayoffBracket(bracket.matches)) {
    return [];
  }
  return [...bracket.matches].sort((left, right) => {
    if (left.stage === right.stage) {
      return left.position - right.position;
    }
    return left.stage === "semifinal" ? -1 : 1;
  });
};

export const GoldenPlayoffControlPanel = ({
  onSessionExpired,
  onTournamentUpdated,
  tournament,
}: GoldenPlayoffControlPanelProps) => {
  const { participantName } = useParticipantNames(tournament?.id ?? "");
  const [golden, setGolden] = useState<GoldenOperatorResponse | null>(null);
  const [goldenState, setGoldenState] = useState<LoadState>("loading");
  const [publicTournament, setPublicTournament] = useState<PublicTournamentResponse | null>(null);
  const [bracket, setBracket] = useState<PublicBracketResponse | null>(null);
  const [scoreboard, setScoreboard] = useState<PublicScoreboardResponse | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [commandError, setCommandError] = useState<string | null>(null);
  const [stale, setStale] = useState(false);
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const tournamentId = tournament?.id ?? null;
  const requestGenerationRef = useRef(0);
  const controllerRef = useRef<AbortController | null>(null);
  const commandInFlightRef = useRef(false);
  const goldenRef = useRef<GoldenOperatorResponse | null>(null);

  const loadState = useCallback(async (options: LoadOptions = {}): Promise<void> => {
    const silent = options.silent === true;
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    const requestGeneration = requestGenerationRef.current + 1;
    requestGenerationRef.current = requestGeneration;
    if (!silent || goldenRef.current === null) {
      setGoldenState("loading");
    }
    setLoadError(null);
    try {
      if (tournamentId === null) {
        return;
      }
      const nextPublicTournament = await publicTournamentApi.getPublicTournament(
        tournamentId,
        controller.signal,
      );
      if (!GOLDEN_CONTROL_STATES.has(nextPublicTournament.state)) {
        if (!controller.signal.aborted && requestGeneration === requestGenerationRef.current) {
          goldenRef.current = null;
          setGolden(null);
          setPublicTournament(nextPublicTournament);
          setBracket(null);
          setScoreboard(null);
          setGoldenState("ready");
          setStale(false);
          setCommandError(null);
        }
        return;
      }
      const nextGolden = await goldenApi.getOperatorState(tournamentId, controller.signal);
      let nextBracket: PublicBracketResponse | null = null;
      let nextScoreboard: PublicScoreboardResponse | null = null;
      if (
        nextPublicTournament?.state === "playoffs" ||
        nextPublicTournament?.state === "completed"
      ) {
        [nextBracket, nextScoreboard] = await Promise.all([
          publicTournamentApi.getPublicBracket(tournamentId, controller.signal),
          publicTournamentApi.getPublicScoreboard(tournamentId, controller.signal),
        ]);
      }
      if (
        controller.signal.aborted ||
        requestGeneration !== requestGenerationRef.current
      ) {
        return;
      }
      goldenRef.current = nextGolden;
      setGolden(nextGolden);
      setPublicTournament(nextPublicTournament);
      setBracket(nextBracket);
      setScoreboard(nextScoreboard);
      setGoldenState("ready");
      setStale(false);
      setCommandError(null);
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (requestGeneration === requestGenerationRef.current && (!silent || goldenRef.current === null)) {
        setGoldenState("error");
        setLoadError(problemMessage(error, "Не удалось загрузить дополнительный отбор"));
      }
    } finally {
      if (controllerRef.current === controller) {
        controllerRef.current = null;
      }
    }
  }, [onSessionExpired, tournamentId]);

  useEffect(() => {
    goldenRef.current = null;
    setGolden(null);
    setPublicTournament(null);
    setBracket(null);
    setScoreboard(null);
    void loadState();
    return () => {
      controllerRef.current?.abort();
      requestGenerationRef.current += 1;
    };
  }, [loadState]);

  const currentState = publicTournament?.state ?? tournament?.state ?? "draft";
  const projectionRevision = publicTournament?.projection_revision ?? tournament?.revision ?? 0;
  const groups = golden?.groups ?? [];
  const completeGolden = hasCompleteGolden(golden);
  const bracketMatches = useMemo(() => orderedBracketMatches(bracket), [bracket]);
  const champion = useMemo(
    () => scoreboard?.entries.find((entry) => entry.rank === 1) ?? null,
    [scoreboard],
  );
  const topSeeds = useMemo(
    () => scoreboard?.entries
      .filter((entry) => entry.rank >= 1 && entry.rank <= 4)
      .sort((left, right) => left.rank - right.rank) ?? [],
    [scoreboard],
  );
  const seedByDisplayName = useMemo(
    () => new Map(topSeeds.map((entry) => [entry.display_name, entry.rank] as const)),
    [topSeeds],
  );
  const runtimeRevision = groups[0]?.runtime_revision ?? 0;
  const canStartGolden = currentState === "swiss" && !stale;
  const canOpenRuntime =
    currentState === "golden" &&
    groups.length === 0 &&
    !stale;
  const canStartPlayoffs =
    (currentState === "swiss" || (currentState === "golden" && completeGolden)) &&
    !stale;
  const terminal = currentState === "completed" || currentState === "cancelled";

  const refreshLive = useCallback(async (): Promise<void> => {
    if (commandInFlightRef.current || tournamentId === null) {
      return;
    }
    await loadState({ silent: true });
  }, [loadState, tournamentId]);
  useAdminLiveRefresh(
    "tournaments",
    refreshLive,
    tournamentId !== null && busyAction === null && (golden !== null || goldenState !== "loading"),
  );

  const handleLifecycleAction = useCallback(async (action: LifecycleAction): Promise<void> => {
    if (
      commandInFlightRef.current ||
      tournament === null ||
      projectionRevision < 1 ||
      (action === "start_golden" && !canStartGolden) ||
      (action === "start_playoffs" && !canStartPlayoffs)
    ) {
      return;
    }
    commandInFlightRef.current = true;
    setBusyAction(action);
    setCommandError(null);
    try {
      const nextTournament = await operatorApi.applyTournamentAction(
        tournament.id,
        {
          action,
          confirmed: true,
          expected_projection_revision: projectionRevision,
          reason: action === "start_golden"
            ? "Оператор подтвердил переход в дополнительный отбор"
            : "Оператор подтвердил переход в плей-офф",
        },
        createOperatorCommandIntent(),
      );
      onTournamentUpdated?.(nextTournament);
      await loadState();
    } catch (error) {
      if (isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
        setStale(true);
        setCommandError("Состояние соревнования изменилось.");
        await loadState();
      } else {
        setCommandError(problemMessage(error, "Не удалось изменить этап соревнования"));
      }
    } finally {
      commandInFlightRef.current = false;
      setBusyAction(null);
    }
  }, [canStartGolden, canStartPlayoffs, loadState, onSessionExpired, onTournamentUpdated, projectionRevision, tournament]);

  const handleOpenRuntime = useCallback(async (): Promise<void> => {
    if (commandInFlightRef.current || tournament === null || projectionRevision < 1 || !canOpenRuntime) {
      return;
    }
    commandInFlightRef.current = true;
    setBusyAction("open");
    setCommandError(null);
    try {
      await goldenApi.open(
        tournament.id,
        {
          expected_projection_revision: projectionRevision,
          expected_runtime_revision: runtimeRevision,
        },
        createOperatorCommandIntent(),
      );
      await loadState();
    } catch (error) {
      if (isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
        setStale(true);
        setCommandError("Состояние дополнительного отбора изменилось.");
        await loadState();
      } else {
        setCommandError(problemMessage(error, "Не удалось открыть подготовку к дополнительному отбору"));
      }
    } finally {
      commandInFlightRef.current = false;
      setBusyAction(null);
    }
  }, [canOpenRuntime, loadState, onSessionExpired, projectionRevision, runtimeRevision, tournament]);

  const handleStartAttempt = useCallback(async (group: GoldenOperatorGroup): Promise<void> => {
    if (
      commandInFlightRef.current ||
      tournament === null ||
      !groupIsReady(group) ||
      stale ||
      terminal ||
      currentState !== "golden"
    ) {
      return;
    }
    commandInFlightRef.current = true;
    setBusyAction(`start:${group.group_id}`);
    setCommandError(null);
    try {
      await goldenApi.start(
        tournament.id,
        group.attempt_id,
        {
          expected_runtime_revision: group.runtime_revision,
          ready_window_id: group.ready_window_id,
        },
        createOperatorCommandIntent(),
      );
      await loadState();
    } catch (error) {
      if (isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
        setStale(true);
        setCommandError("Игра дополнительного отбора изменилась.");
        await loadState();
      } else {
        setCommandError(problemMessage(error, "Не удалось начать игру дополнительного отбора"));
      }
    } finally {
      commandInFlightRef.current = false;
      setBusyAction(null);
    }
  }, [currentState, loadState, onSessionExpired, stale, terminal, tournament]);

  if (tournament === null) {
    return null;
  }

  return (
    <Panel
      title="Дополнительный отбор и плей-офф"
      description="Проверяйте готовность игроков, запускайте дополнительные игры и следите за сеткой плей-офф."
      className={styles.root}
      data-testid="operator-golden-playoff-control-panel"
    >
      <div className={styles.toolbar}>
        <div className={styles.identity}>
          <strong>{tournament.name}</strong>
        </div>
        <div className={styles.serverState}>
          <Status tone={terminal ? "success" : currentState === "golden" || currentState === "playoffs" ? "live" : "info"}>
            {formatTournamentState(currentState)}
          </Status>
        </div>
      </div>

      {goldenState === "loading" && golden === null ? (
        <Message tone="loading" title="Загружаем дополнительный отбор">
          Получаем группы участников и готовность к играм.
        </Message>
      ) : null}
      {goldenState === "error" ? (
        <Message tone="error" title="Дополнительный отбор недоступен">
          {loadError ?? "Не удалось загрузить данные дополнительного отбора."}
        </Message>
      ) : null}
      {commandError ? (
        <Message tone="error" title={stale ? "Состояние устарело" : "Команда не выполнена"}>
          {commandError}
        </Message>
      ) : null}
      {goldenState === "ready" && golden === null && !GOLDEN_CONTROL_STATES.has(currentState) ? (
        <Message tone="empty" title="Дополнительный отбор пока недоступен">
          Управление появится после завершения квалификации.
        </Message>
      ) : null}

      {goldenState === "ready" && golden !== null ? (
        <>
          <div className={styles.actions} data-testid="golden-lifecycle-actions">
            <Button
              variant="primary"
              data-testid="golden-start-lifecycle"
              disabled={!canStartGolden || terminal || busyAction !== null}
              loading={busyAction === "start_golden"}
              loadingLabel="Запускаем этап"
              onClick={() => void handleLifecycleAction("start_golden")}
            >
              Начать дополнительный отбор
            </Button>
            <Button
              variant="secondary"
              data-testid="golden-open-runtime"
              disabled={!canOpenRuntime || busyAction !== null}
              loading={busyAction === "open"}
              loadingLabel="Готовим группы"
              onClick={() => void handleOpenRuntime()}
            >
              Подготовить группы
            </Button>
            <Button
              variant="success"
              data-testid="golden-start-playoffs"
              disabled={!canStartPlayoffs || terminal || busyAction !== null}
              loading={busyAction === "start_playoffs"}
              loadingLabel="Запускаем плей-офф"
              onClick={() => void handleLifecycleAction("start_playoffs")}
            >
              Подтвердить четверку и начать плей-офф
            </Button>
          </div>
          {currentState === "golden" && !completeGolden ? (
            <p className={styles.blockingHint}>
              Плей-офф станет доступен после завершения всех групп дополнительного отбора.
            </p>
          ) : null}
          <div className={styles.groups} data-testid="golden-groups">
            {groups.map((group) => {
              const allReady = group.members.length > 0 && group.members.every((member) => member.ready);
              const readyAttempt = groupIsReady(group);
              return (
                <article className={styles.groupCard} data-testid="golden-group" data-group-id={group.group_id} key={group.group_id}>
                  <div className={styles.groupHeading}>
                    <div>
                      <h3>Группа за места {group.position_from}-{group.position_to}</h3>
                    </div>
                    <Status tone={stateTone(group.state)}>
                      {GOLDEN_STATE_LABELS[group.state] ?? group.state}
                    </Status>
                  </div>
                  <div className={styles.groupMeta}>
                    <span>Участников: {group.members.length}</span>
                  </div>
                  <div className={styles.memberList} aria-label={`Участники группы за места ${group.position_from}-${group.position_to}`}>
                    {group.members.map((member) => (
                      <div className={styles.memberRow} data-testid="golden-member" key={member.participant_id}>
                        <div className={styles.memberIdentity}>
                          <span>Участник</span>
                          <strong>{participantName(member.participant_id)}</strong>
                          <small>{member.position ? `Позиция ${member.position}` : "Позиция не определена"}</small>
                        </div>
                        <div className={styles.memberSignals}>
                          <Status tone={memberTone(member.ready)}>
                            {member.ready ? "Готов" : "Не готов"}
                          </Status>
                          <Status tone={member.submitted ? "success" : "neutral"}>
                            {member.submitted ? "Отправил" : "Не отправил"}
                          </Status>
                        </div>
                      </div>
                    ))}
                  </div>
                  <div className={styles.groupFooter}>
                    <span>{allReady ? "Все участники готовы" : "Ожидаем готовность участников"}</span>
                    <Button
                      size="small"
                      variant="success"
                      data-testid={`golden-start-attempt-${group.group_id}`}
                      disabled={!readyAttempt || terminal || currentState !== "golden" || busyAction !== null}
                      loading={busyAction === `start:${group.group_id}`}
                      loadingLabel="Запускаем"
                      onClick={() => void handleStartAttempt(group)}
                    >
                      Начать игру
                    </Button>
                  </div>
                </article>
              );
            })}
          </div>
          {groups.length === 0 ? (
            <Message tone="empty" title="Группы еще не сформированы">
              {currentState === "golden"
                ? "Нажмите кнопку подготовки групп, чтобы распределить участников."
                : "Группы для выбранного соревнования пока не готовы."}
            </Message>
          ) : null}
        </>
      ) : null}

      {(currentState === "playoffs" || currentState === "completed") && bracket === null && goldenState === "ready" ? (
        <Message tone="loading" title="Загружаем сетку">
          Получаем официальные полуфиналы и финал.
        </Message>
      ) : null}
      {bracket !== null && bracketMatches.length === 0 ? (
        <Message tone="error" title="Сетка недоступна">
          Не удалось получить полуфиналы и финал.
        </Message>
      ) : null}
      {(currentState === "playoffs" || currentState === "completed") && scoreboard !== null ? (
        <section className={styles.seeds} data-testid="server-top-four" aria-label="Участники плей-офф">
          <div className={styles.sectionHeading}>
            <h3>Участники плей-офф</h3>
          </div>
          {topSeeds.length > 0 ? (
            <div className={styles.seedList}>
              {topSeeds.map((entry) => (
                <div className={styles.seedRow} data-testid="server-seed" data-seed-rank={entry.rank} key={`${entry.rank}-${entry.display_name}`}>
                  <strong>#{entry.rank}</strong>
                  <span>{entry.display_name}</span>
                  <small>{entry.points} очк.</small>
                </div>
              ))}
            </div>
          ) : (
            <p className={styles.seedEmpty}>Четверка участников еще не определена.</p>
          )}
        </section>
      ) : null}
      {bracketMatches.length > 0 ? (
        <section className={styles.bracket} data-testid="server-playoff-bracket" aria-label="Сетка плей-офф">
          <div className={styles.sectionHeading}>
            <h3>Сетка плей-офф</h3>
          </div>
          <div className={styles.bracketGrid}>
            {bracketMatches.map((match) => (
              <article className={styles.matchCard} data-testid="playoff-match" data-stage={match.stage} key={`${match.stage}-${match.position}`}>
                <div className={styles.matchHeading}>
                  <strong>
                    {match.stage === "semifinal"
                      ? `Полуфинал ${match.position} - BO1`
                      : "Финал - BO3"}
                  </strong>
                  <Status tone={match.state === "completed" ? "success" : match.state === "active" ? "live" : "neutral"}>
                    {formatSeriesState(match.state)}
                  </Status>
                </div>
                <div className={styles.teams}>
                  <span>
                    {bracketDisplayName(match.first_display_name)}
                    {seedByDisplayName.has(bracketDisplayName(match.first_display_name))
                      ? ` (посев #${seedByDisplayName.get(bracketDisplayName(match.first_display_name))})`
                      : ""}
                  </span>
                  <strong>{match.score.first_participant_wins}</strong>
                  <span>
                    {bracketDisplayName(match.second_display_name)}
                    {seedByDisplayName.has(bracketDisplayName(match.second_display_name))
                      ? ` (посев #${seedByDisplayName.get(bracketDisplayName(match.second_display_name))})`
                      : ""}
                  </span>
                  <strong>{match.score.second_participant_wins}</strong>
                </div>
                <div className={styles.scoreLabel} aria-label={`Счет ${bracketDisplayName(match.first_display_name)} - ${bracketDisplayName(match.second_display_name)}`}>
                  {match.score.first_participant_wins}:{match.score.second_participant_wins}
                </div>
              </article>
            ))}
          </div>
        </section>
      ) : null}
      {currentState === "completed" && scoreboard !== null ? (
        <section className={styles.champion} data-testid="server-champion" aria-label="Чемпион соревнования">
          <span>Победитель по итоговой таблице</span>
          <strong>{champion?.display_name ?? "Чемпион пока не определен"}</strong>
          {champion ? <small>Место 1, очки: {champion.points}</small> : null}
        </section>
      ) : null}
    </Panel>
  );
};

GoldenPlayoffControlPanel.displayName = "GoldenPlayoffControlPanel";
