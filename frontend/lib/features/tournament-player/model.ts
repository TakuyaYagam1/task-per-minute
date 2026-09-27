import type { RoleAwareRecoveryState } from "../../shared/api";
import { isParticipantRecoverySnapshot } from "../../shared/api/guards";
import type { components } from "../../shared/api/schema";
import {
  formatCategory,
  formatSeriesFormat,
  formatTournamentState,
} from "../../shared/lib";

export type ParticipantPlayerState =
  | "waiting"
  | "assigned"
  | "ready"
  | "bye"
  | "eliminated"
  | "completed";

export type ParticipantCheckInState = "confirmed" | "pending" | "unknown";

export type ParticipantAssignmentDeliveryState = "waiting" | "delivered" | "superseded";

export type ParticipantSeriesScore = Readonly<{
  own: number;
  opponent: number;
}>;

export type ParticipantReadinessKey = Readonly<{
  tournamentId: string;
  projectionRevision: number;
  waveId: string | null;
  readyWindowId: string | null;
  readyWindowRevisionId: string | null;
  assignmentAttemptId: string | null;
}>;

export type ParticipantReadyIntent = Readonly<{
  key: ParticipantReadinessKey;
  ready: boolean;
}>;

export type ParticipantReadyResult = Readonly<{
  status: "accepted" | "conflict" | "rate_limited";
  message?: string;
}>;

export type ParticipantDraftActionView = Readonly<{
  action: components["schemas"]["DraftActionType"];
  actorId: string;
  automatic: boolean;
  category: components["schemas"]["Category"];
  decisionEvidence: components["schemas"]["DraftDecisionEvidence"] | null;
  occurredAt: string;
  turn: number;
  turnDeadline: string;
}>;

export type ParticipantDraftView = Readonly<{
  id: string;
  seriesId: string;
  format: components["schemas"]["SeriesFormat"];
  firstParticipantId: string;
  secondParticipantId: string;
  pool: readonly components["schemas"]["Category"][];
  state: components["schemas"]["DraftState"];
  revision: number;
  turn: number;
  turnDeadline: string | null;
  currentActorId: string | null;
  currentAction: components["schemas"]["DraftActionType"] | null;
  legalCategories: readonly components["schemas"]["Category"][];
  selectedCategories: readonly components["schemas"]["Category"][];
  actions: readonly ParticipantDraftActionView[];
}>;

export type ParticipantDraftIntent = Readonly<{
  tournamentId: string;
  seriesId: string;
  draftId: string;
  draftRevision: number;
  projectionRevision: number;
  expectedTurn: number;
  action: components["schemas"]["DraftActionType"];
  category: components["schemas"]["Category"];
}>;

export type ParticipantDraftResult = Readonly<{
  status: "accepted" | "conflict" | "rate_limited";
  message?: string;
}>;

export type ParticipantAssignmentView = Readonly<{
  assignmentId: string;
  attemptId: string;
  title: string;
  description: string;
  category: string;
  difficulty: string;
  effectiveDeadline: string | null;
  deliveredAt: string;
  gameId: string;
  gameNumber: number;
  gameState: components["schemas"]["GameState"];
  hints: readonly string[];
  seriesId: string;
  seriesScore: ParticipantSeriesScore | null;
  slotId: string;
  stage: components["schemas"]["ParticipantAssignmentContext"]["stage"];
  startedAt: string | null;
  swissRound: number | null;
  timeLimitSeconds: number;
  taskUrl: string | null;
  sourceFileAvailable: boolean;
  version: number;
  waveId: string;
}>;

export type ParticipantPauseSource = "wave" | "series" | "game" | "draft";

export type ParticipantPauseView = Readonly<{
  active: boolean;
  deadlinesSuppressed: boolean;
  pausedAt: string | null;
  pauseId: string | null;
  reason: components["schemas"]["PauseReason"] | null;
  reconnectDeadline: string | null;
  resumedAt: string | null;
  resumedDeadline: string | null;
  source: ParticipantPauseSource | null;
  state: components["schemas"]["PauseState"] | null;
  frozenRemainingMs: number | null;
}>;

export type ParticipantPresenceView = Readonly<{
  connectedAt: string;
  disconnectedAt: string | null;
  participantId: string;
  presenceEpoch: number;
  revision: number;
  state: components["schemas"]["PresenceState"];
  updatedAt: string;
}>;

export type ParticipantReconnectView = Readonly<{
  closedAt: string | null;
  continuationNumber: number;
  continuedFromId: string | null;
  deadline: string;
  id: string;
  number: number;
  openedAt: string;
  participantId: string;
  pauseId: string;
  presenceEpoch: number;
  revision: number;
  state: components["schemas"]["ReconnectState"];
  suspendedByPauseId: string | null;
  updatedAt: string;
}>;

export type ParticipantRuntimeView = Readonly<{
  gameId: string;
  gameRevision: number;
  gameState: components["schemas"]["GameState"];
  pause: ParticipantPauseView | null;
  presence: readonly ParticipantPresenceView[];
  reconnect: readonly ParticipantReconnectView[];
  resultReason: components["schemas"]["GameResultReason"] | null;
  resultRevisionId: string | null;
  winnerId: string | null;
}>;

export type ParticipantOfficialOutcome = Readonly<{
  reason: components["schemas"]["GameResultReason"] |
    components["schemas"]["SeriesResultReason"] |
    null;
  revisionId: string;
  state: components["schemas"]["GameState"] | components["schemas"]["SeriesState"];
  subject: "game" | "series";
  winnerId: string | null;
}>;

export type ParticipantGameResultView = Readonly<{
  attemptNo: number;
  category: string;
  gameId: string;
  position: number;
  reason: components["schemas"]["GameResultReason"] | null;
  resultRevisionId: string | null;
  scoreBefore: ParticipantSeriesScore;
  state: components["schemas"]["GameState"];
  winnerId: string | null;
}>;

export type ParticipantSeriesResultView = Readonly<{
  currentResultRevisionId: string | null;
  currentScoreRevisionId: string | null;
  format: components["schemas"]["SeriesFormat"];
  games: readonly ParticipantGameResultView[];
  id: string;
  score: ParticipantSeriesScore;
  state: components["schemas"]["SeriesState"];
  winnerId: string | null;
}>;

export type ParticipantPlayerView = Readonly<{
  tournamentId: string;
  participantId: string;
  projectionRevision: number;
  state: ParticipantPlayerState;
  stateLabel: string;
  stateDescription: string;
  tournamentLabel: string;
  stageLabel: string;
  roundLabel: string;
  gameNumber: number | null;
  seriesScore: ParticipantSeriesScore | null;
  taskDeadlineAt: string | null;
  ownPoints: number | null;
  checkIn: ParticipantCheckInState;
  opponentName: string | null;
  matchFormat: string | null;
  category: string | null;
  requiredAction: string;
  ready: boolean | null;
  readyWindowOpen: boolean;
  readyWindowDeadline: string | null;
  readyWindowState: string | null;
  readyKey: ParticipantReadinessKey;
  assignmentDeliveryState: ParticipantAssignmentDeliveryState;
  assignment: ParticipantAssignmentView | null;
  draft: ParticipantDraftView | null;
  officialOutcome: ParticipantOfficialOutcome | null;
  seriesResult: ParticipantSeriesResultView | null;
  pause: ParticipantPauseView;
  runtime: ParticipantRuntimeView | null;
}>;

const participantStateCopy: Readonly<
  Record<ParticipantPlayerState, Readonly<{ label: string; description: string }>>
> = {
  waiting: {
    label: "Ожидание",
    description: "Следующий матч еще не назначен. Мы покажем его здесь.",
  },
  assigned: {
    label: "Матч назначен",
    description: "Проверьте соперника и следующее действие.",
  },
  ready: {
    label: "Готовность подтверждена",
    description: "Готовность подтверждена для текущего окна.",
  },
  bye: {
    label: "Проход без матча",
    description: "В этом раунде соперник не назначен. Следующий матч появится здесь.",
  },
  eliminated: {
    label: "Выбывание",
    description: "Участие в текущем соревновании завершено. Новые действия недоступны.",
  },
  completed: {
    label: "Соревнование завершено",
    description: "Итог соревнования зафиксирован.",
  },
};

const participantActionCopy: Readonly<
  Record<
    "wait" | "check_in" | "ready" | "draft" | "play" | "review_result" | "none",
    string
  >
> = {
  wait: "Ждите следующий матч",
  check_in: "Подтвердите участие",
  ready: "Подтвердите готовность",
  draft: "Сделайте ход в драфте",
  play: "Откройте задание матча",
  review_result: "Проверьте результат матча",
  none: "Действий не требуется",
};

const assignmentDeliveryStateFor = (
  assignment: unknown,
  waveState: string | null,
  seriesState: string | null,
  gameState: string | null,
): ParticipantAssignmentDeliveryState => {
  if (waveState === "superseded" || seriesState === "cancelled" || gameState === "superseded") {
    return "superseded";
  }

  return assignment === null ? "waiting" : "delivered";
};

const assignmentStageLabelFor = (
  stage: ParticipantAssignmentView["stage"],
): string => {
  switch (stage) {
    case "swiss":
      return "Квалификация";
    case "semifinal":
      return "Полуфинал";
    case "final":
      return "Финал";
  }
};

const checkInStateFor = (
  attendance: "invited" | "registered" | "checked_in" | "withdrawn",
): ParticipantCheckInState => {
  switch (attendance) {
    case "checked_in":
      return "confirmed";
    case "invited":
    case "registered":
      return "pending";
    case "withdrawn":
      return "unknown";
  }
};

const readyKeyFor = (
  tournamentId: string,
  projectionRevision: number,
  waveId: string | null,
  readyWindowId: string | null,
  readyWindowRevisionId: string | null,
  assignmentAttemptId: string | null,
): ParticipantReadinessKey => ({
  assignmentAttemptId,
  projectionRevision,
  readyWindowId,
  readyWindowRevisionId,
  tournamentId,
  waveId,
});

const participantDraftViewFor = (
  value: components["schemas"]["Draft"] | null,
): ParticipantDraftView | null => value === null
  ? null
  : {
      actions: value.actions.map((action) => ({
        action: action.action,
        actorId: action.actor_id,
        automatic: action.automatic,
        category: action.category,
        decisionEvidence: action.decision_evidence,
        occurredAt: action.occurred_at,
        turn: action.turn,
        turnDeadline: action.turn_deadline,
      })),
      currentAction: value.current_action,
      currentActorId: value.current_actor_id,
      firstParticipantId: value.first_participant_id,
      format: value.format,
      id: value.id,
      legalCategories: value.legal_categories,
      pool: value.pool,
      revision: value.revision,
      secondParticipantId: value.second_participant_id,
      selectedCategories: value.selected_categories,
      seriesId: value.series_id,
      state: value.state,
      turn: value.turn,
      turnDeadline: value.turn_deadline,
    };

const participantRuntimePauseViewFor = (
  value: components["schemas"]["ParticipantRuntimePause"] | null,
): ParticipantPauseView | null => value === null
  ? null
  : {
      active: value.state === "active",
      deadlinesSuppressed: value.deadlines_suppressed,
      frozenRemainingMs: value.frozen_remaining_ms,
      pausedAt: value.frozen_at,
      pauseId: value.pause_id,
      reason: value.reason,
      reconnectDeadline: value.reconnect_deadline,
      resumedAt: value.resumed_at,
      resumedDeadline: value.resumed_deadline,
      source: null,
      state: value.state,
    };

const participantRuntimeViewFor = (
  value: components["schemas"]["ParticipantRuntime"] | null,
): ParticipantRuntimeView | null => value === null
  ? null
  : {
      gameId: value.game_id,
      gameRevision: value.game_revision,
      gameState: value.game_state,
      pause: participantRuntimePauseViewFor(value.pause),
      presence: value.presence.map((item) => ({
        connectedAt: item.connected_at,
        disconnectedAt: item.disconnected_at,
        participantId: item.participant_id,
        presenceEpoch: item.presence_epoch,
        revision: item.revision,
        state: item.state,
        updatedAt: item.updated_at,
      })),
      reconnect: value.reconnect.map((item) => ({
        closedAt: item.closed_at,
        continuationNumber: item.continuation_number,
        continuedFromId: item.continued_from_id,
        deadline: item.deadline,
        id: item.id,
        number: item.number,
        openedAt: item.opened_at,
        participantId: item.participant_id,
        pauseId: item.pause_id,
        presenceEpoch: item.presence_epoch,
        revision: item.revision,
        state: item.state,
        suspendedByPauseId: item.suspended_by_pause_id,
        updatedAt: item.updated_at,
      })),
      resultReason: value.result_reason,
      resultRevisionId: value.result_revision_id,
      winnerId: value.winner_id,
    };

const participantPauseFor = (
  runtimePause: ParticipantPauseView | null,
  waveState: components["schemas"]["WaveState"] | null,
  wavePausedAt: string | null,
  seriesState: components["schemas"]["SeriesState"] | null,
  gameState: components["schemas"]["GameState"] | null,
  draftState: components["schemas"]["DraftState"] | null,
): ParticipantPauseView => {
  const derivedSource: ParticipantPauseSource | null = waveState === "paused"
    ? "wave"
    : seriesState === "technical_pause"
      ? "series"
      : gameState === "paused"
        ? "game"
        : draftState === "paused"
          ? "draft"
          : null;

  if (runtimePause !== null) {
    return {
      ...runtimePause,
      source: runtimePause.active ? derivedSource ?? "game" : null,
    };
  }

  const source = derivedSource;

  return {
    active: source !== null,
    deadlinesSuppressed: source !== null,
    frozenRemainingMs: null,
    pausedAt: source === "wave" ? wavePausedAt : null,
    pauseId: null,
    reason: null,
    reconnectDeadline: null,
    resumedAt: null,
    resumedDeadline: null,
    source,
    state: source === null ? null : "active",
  };
};

const terminalGameStates = new Set<components["schemas"]["GameState"]>([
  "cancelled",
  "completed",
  "superseded",
  "void",
]);

const terminalSeriesStates = new Set<components["schemas"]["SeriesState"]>([
  "cancelled",
  "completed",
]);

const participantOfficialOutcomeFor = (
  runtime: ParticipantRuntimeView | null,
  series: components["schemas"]["Series"] | null,
  seriesId: string | null,
  gameId: string | null,
): ParticipantOfficialOutcome | null => {
  if (series === null || seriesId === null || series.id !== seriesId) {
    return null;
  }

  const seriesGames = series.slots.flatMap((slot) =>
    slot.attempts.map((attempt) => ({ attempt, position: slot.position })),
  );
  const terminalGames = seriesGames
    .filter(({ attempt }) =>
      terminalGameStates.has(attempt.state) && attempt.result_revision_id !== null,
    )
    .sort((left, right) =>
      left.position - right.position || left.attempt.attempt_no - right.attempt.attempt_no,
    );
  const terminalGame = gameId === null
    ? terminalGames[terminalGames.length - 1]?.attempt ?? null
    : runtime !== null && runtime.gameId === gameId
      ? null
      : seriesGames.find(({ attempt }) => attempt.id === gameId)?.attempt ?? null;

  if (terminalGame !== null && terminalGameStates.has(terminalGame.state) && terminalGame.result_revision_id !== null) {
    return {
      reason: terminalGame.result_reason,
      revisionId: terminalGame.result_revision_id,
      state: terminalGame.state,
      subject: "game",
      winnerId: terminalGame.winner_id,
    };
  }

  if (
    terminalSeriesStates.has(series.state) &&
    series.current_result_revision_id !== null
  ) {
    return {
      reason: null,
      revisionId: series.current_result_revision_id,
      state: series.state,
      subject: "series",
      winnerId: series.winner_id,
    };
  }

  if (
    runtime !== null &&
    (gameId === null || runtime.gameId === gameId) &&
    terminalGameStates.has(runtime.gameState) &&
    runtime.resultRevisionId !== null
  ) {
    return {
      reason: runtime.resultReason,
      revisionId: runtime.resultRevisionId,
      state: runtime.gameState,
      subject: "game",
      winnerId: runtime.winnerId,
    };
  }

  return null;
};

const participantScoreFor = (
  participantId: string,
  series: components["schemas"]["Series"],
  score: components["schemas"]["SeriesScore"],
): ParticipantSeriesScore | null => {
  if (series.first_participant_id === participantId) {
    return {
      opponent: score.second_participant_wins,
      own: score.first_participant_wins,
    };
  }

  if (series.second_participant_id === participantId) {
    return {
      opponent: score.first_participant_wins,
      own: score.second_participant_wins,
    };
  }

  return null;
};

const participantSeriesResultFor = (
  participantId: string,
  series: components["schemas"]["Series"] | null,
): ParticipantSeriesResultView | null => {
  if (series === null) {
    return null;
  }

  const score = participantScoreFor(participantId, series, series.score);
  if (score === null) {
    return null;
  }

  const games = series.slots
    .flatMap((slot) => slot.attempts
      .filter((attempt) => attempt.state !== "planned")
      .map((attempt) => ({
        attemptNo: attempt.attempt_no,
        category: formatCategory(slot.category),
        gameId: attempt.id,
        position: slot.position,
        reason: attempt.result_reason,
        resultRevisionId: attempt.result_revision_id,
        scoreBefore: participantScoreFor(participantId, series, slot.score_before) ?? {
          opponent: 0,
          own: 0,
        },
        state: attempt.state,
        winnerId: attempt.winner_id,
      })))
    .sort((left, right) => left.position - right.position || left.attemptNo - right.attemptNo);

  return {
    currentResultRevisionId: series.current_result_revision_id,
    currentScoreRevisionId: series.current_score_revision_id,
    format: series.format,
    games,
    id: series.id,
    score,
    state: series.state,
    winnerId: series.winner_id,
  };
};

/**
 * Converts the validated participant recovery projection into display state.
 *
 * The recovery reducer remains the source of truth. This mapper deliberately
 * uses the participant-safe fields resolved by the backend. Browser storage
 * and array position never participate in identity or authorization.
 */
export const buildParticipantPlayerView = (
  recovery: RoleAwareRecoveryState | null,
): ParticipantPlayerView | null => {
  if (recovery === null || recovery.role !== "participant") {
    return null;
  }

  if (!isParticipantRecoverySnapshot(recovery.snapshot)) {
    return null;
  }

  const snapshot = recovery.snapshot;
  const lobby = snapshot.lobby;
  const currentSeries = lobby.series[0] ?? null;
  const wave = snapshot.wave;
  const readyWindow = wave?.ready_window ?? null;
  const assignment = snapshot.assignment;
  const runtime = participantRuntimeViewFor(snapshot.runtime);
  const runtimeGameState = assignment !== null && runtime?.gameId === assignment.context.game_id
    ? runtime.gameState
    : assignment?.context.game_state ?? null;
  const assignmentDeliveryState = assignmentDeliveryStateFor(
    assignment,
    wave?.state ?? null,
    currentSeries?.state ?? null,
    runtimeGameState,
  );
  const currentSeriesDetails = snapshot.series;
  const assignmentScore = assignment && currentSeriesDetails !== null &&
      currentSeriesDetails.id === assignment.context.series_id
    ? participantScoreFor(
        lobby.participant_id,
        currentSeriesDetails,
        assignment.context.series_score,
      )
    : null;
  const assignmentView: ParticipantAssignmentView | null = assignment && assignmentDeliveryState === "delivered"
    ? {
        assignmentId: assignment.id,
        attemptId: assignment.attempt_id,
        category: formatCategory(assignment.active_snapshot.category),
        description: assignment.active_snapshot.description,
        effectiveDeadline: assignment.context.effective_deadline,
        deliveredAt: assignment.receipt.delivered_at,
        difficulty: assignment.active_snapshot.difficulty,
        gameId: assignment.context.game_id,
        gameNumber: assignment.context.game_number,
        gameState: runtimeGameState ?? assignment.context.game_state,
        hints: assignment.active_snapshot.hints,
        seriesId: assignment.context.series_id,
        seriesScore: assignmentScore,
        slotId: assignment.context.slot_id,
        stage: assignment.context.stage,
        startedAt: assignment.context.started_at,
        swissRound: assignment.context.swiss_round,
        sourceFileAvailable: assignment.active_snapshot.source_file_available,
        taskUrl: assignment.active_snapshot.task_url ?? null,
        timeLimitSeconds: assignment.active_snapshot.time_limit,
        title: assignment.active_snapshot.title,
        version: assignment.active_snapshot.version,
        waveId: assignment.context.wave_id,
      }
    : null;

  const baseState: ParticipantPlayerState = lobby.status;
  const participantMember = wave?.members.find(
    (member) => member.participant_id === lobby.participant_id,
  ) ?? null;
  const ready = participantMember?.ready ?? null;
  const state: ParticipantPlayerState = ready === true && baseState === "assigned"
    ? "ready"
    : baseState;
  const waveId = wave?.id ?? currentSeries?.wave_id ?? null;
  const readyWindowId = readyWindow?.id ?? null;
  const readyWindowOpen = readyWindow?.state === "open";
  const readyKey = readyKeyFor(
    snapshot.tournament_id,
    snapshot.projection_revision,
    waveId,
    readyWindowId,
    readyWindow?.revision_id ?? null,
    assignmentView?.attemptId ?? null,
  );

  const pause = participantPauseFor(
    runtime?.pause ?? null,
    wave?.state ?? null,
    wave?.paused_at ?? null,
    currentSeriesDetails?.state ?? currentSeries?.state ?? null,
    runtimeGameState,
    snapshot.draft?.state ?? null,
  );
  const officialOutcome = participantOfficialOutcomeFor(
    runtime,
    currentSeriesDetails,
    assignment?.context.series_id ?? currentSeries?.series_id ?? currentSeriesDetails?.id ?? null,
    assignment?.context.game_id ?? null,
  );
  const seriesResult = participantSeriesResultFor(lobby.participant_id, currentSeriesDetails);

  const requiredAction = participantActionCopy[lobby.required_action];
  const roundLabel = assignmentView === null
    ? lobby.current_swiss_round === null
      ? "Раунд еще не назначен"
      : `Раунд ${lobby.current_swiss_round}`
    : assignmentView.stage === "swiss" && assignmentView.swissRound !== null
      ? `Раунд ${assignmentView.swissRound}`
      : "Не применяется";

  return {
    assignment: assignmentView,
    assignmentDeliveryState,
    category: assignmentView?.category ?? null,
    checkIn: checkInStateFor(lobby.attendance),
    gameNumber: assignmentView?.gameNumber ?? null,
    matchFormat: currentSeries ? formatSeriesFormat(currentSeries.format) : null,
    opponentName: currentSeries?.opponent_display_name ?? null,
    ownPoints: lobby.swiss_points,
    participantId: lobby.participant_id,
    projectionRevision: snapshot.projection_revision,
    ready,
    readyKey,
    readyWindowDeadline: readyWindow?.deadline ?? null,
    readyWindowOpen,
    readyWindowState: readyWindow?.state ?? null,
    requiredAction,
    roundLabel,
    seriesScore: assignmentView?.seriesScore ?? null,
    stageLabel: assignmentView === null
      ? formatTournamentState(lobby.state)
      : assignmentStageLabelFor(assignmentView.stage),
    state,
    stateDescription: participantStateCopy[state].description,
    taskDeadlineAt: assignmentView?.effectiveDeadline ?? null,
    stateLabel: participantStateCopy[state].label,
    tournamentId: snapshot.tournament_id,
    tournamentLabel: formatTournamentState(lobby.state),
    draft: participantDraftViewFor(snapshot.draft),
    officialOutcome,
    seriesResult,
    pause,
    runtime,
  };
};
