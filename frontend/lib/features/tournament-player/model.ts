import type { RoleAwareRecoveryState } from "../../shared/api";
import { isParticipantRecoverySnapshot } from "../../shared/api/guards";
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
  category: string;
  difficulty: string;
  timeLimitSeconds: number;
  taskUrl: string | null;
  sourceFileAvailable: boolean;
}>;

export type ParticipantPlayerView = Readonly<{
  tournamentId: string;
  projectionRevision: number;
  state: ParticipantPlayerState;
  stateLabel: string;
  stateDescription: string;
  tournamentLabel: string;
  roundLabel: string;
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
  const assignmentView: ParticipantAssignmentView | null = assignment
    ? {
        attemptId: assignment.attempt_id,
        category: formatCategory(assignment.active_snapshot.category),
        difficulty: assignment.active_snapshot.difficulty,
        sourceFileAvailable: assignment.active_snapshot.source_file_available,
        taskUrl: assignment.active_snapshot.task_url ?? null,
        timeLimitSeconds: assignment.active_snapshot.time_limit,
        title: assignment.active_snapshot.title,
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
    assignment?.attempt_id ?? null,
  );

  const requiredAction = participantActionCopy[lobby.required_action];

  return {
    assignment: assignmentView,
    category: assignmentView?.category ?? null,
    checkIn: checkInStateFor(lobby.attendance),
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
    roundLabel: lobby.current_swiss_round === null
      ? "Раунд еще не назначен"
      : `Раунд ${lobby.current_swiss_round}`,
    state,
    stateDescription: participantStateCopy[state].description,
    stateLabel: participantStateCopy[state].label,
    tournamentId: snapshot.tournament_id,
    tournamentLabel: formatTournamentState(lobby.state),
  };
};
