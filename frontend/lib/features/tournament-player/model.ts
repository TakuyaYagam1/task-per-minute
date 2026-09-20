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

export type ParticipantAssignmentView = Readonly<{
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

export type ParticipantPlayerView = Readonly<{
  tournamentId: string;
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
}>;

const participantStateCopy: Readonly<
  Record<ParticipantPlayerState, Readonly<{ label: string; description: string }>>
> = {
  waiting: {
    label: "Ожидание",
    description: "Следующий матч еще не назначен. Оставайтесь в этом окне турнира.",
  },
  assigned: {
    label: "Матч назначен",
    description: "Матч назначен сервером. Проверьте соперника и ожидаемое действие.",
  },
  ready: {
    label: "Готовность подтверждена",
    description: "Сервер зафиксировал готовность для текущего окна.",
  },
  bye: {
    label: "Проход без матча",
    description: "В этом раунде соперник не назначен. Следующее состояние определит сервер.",
  },
  eliminated: {
    label: "Выбывание",
    description: "Участие в текущем турнире завершено. Новые действия недоступны.",
  },
  completed: {
    label: "Турнир завершен",
    description: "Итоговое состояние турнира зафиксировано сервером.",
  },
};

const participantActionCopy: Readonly<
  Record<
    "wait" | "check_in" | "ready" | "draft" | "play" | "review_result" | "none",
    string
  >
> = {
  wait: "Ожидайте обновления турнира",
  check_in: "Подтвердите участие",
  ready: "Подтвердите готовность",
  draft: "Сделайте ход в драфте",
  play: "Откройте назначенное задание",
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
      return "Швейцарский этап";
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
  const assignmentDeliveryState = assignmentDeliveryStateFor(
    assignment,
    wave?.state ?? null,
    currentSeries?.state ?? null,
    assignment?.context.game_state ?? null,
  );
  const currentSeriesDetails = snapshot.series;
  const assignmentScore = assignment && currentSeriesDetails !== null &&
      currentSeriesDetails.id === assignment.context.series_id
    ? currentSeriesDetails.first_participant_id === lobby.participant_id
      ? {
          opponent: assignment.context.series_score.second_participant_wins,
          own: assignment.context.series_score.first_participant_wins,
        }
      : currentSeriesDetails.second_participant_id === lobby.participant_id
        ? {
            opponent: assignment.context.series_score.first_participant_wins,
            own: assignment.context.series_score.second_participant_wins,
          }
        : null
    : null;
  const assignmentView: ParticipantAssignmentView | null = assignment && assignmentDeliveryState === "delivered"
    ? {
        attemptId: assignment.attempt_id,
        category: formatCategory(assignment.active_snapshot.category),
        description: assignment.active_snapshot.description,
        effectiveDeadline: assignment.context.effective_deadline,
        deliveredAt: assignment.receipt.delivered_at,
        difficulty: assignment.active_snapshot.difficulty,
        gameId: assignment.context.game_id,
        gameNumber: assignment.context.game_number,
        gameState: assignment.context.game_state,
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
  };
};
