import type { components } from "../../shared/api/schema";

import type { GameView } from "./game";
import type { TournamentFormat } from "./format";
import type {
  OperatorSeriesView,
  PublicSeriesView,
  SeriesScoreView,
  SeriesSlotView,
  SeriesState,
  TournamentCategory,
  ParticipantSeriesView,
} from "./series";
import type { TournamentStage, TournamentState } from "./stage";

export type TournamentPreset = components["schemas"]["TournamentPreset"];

export interface PublicTournamentView {
  tournamentId: string;
  preset: TournamentPreset;
  state: TournamentState;
  rosterSize: number;
  startedAt: string | null;
  finishedAt: string | null;
  projectionRevision: number;
}

export interface PublicScoreboardEntryView {
  rank: number;
  displayName: string;
  points: number;
  wins: number;
  losses: number;
  byeCount: number;
  provisionalTie: boolean;
  qualificationStatus: PublicQualificationStatus;
  buchholz: number;
  effectiveTimeMs: number;
}

export type PublicQualificationStatus = "pending" | "qualified" | "eliminated";

export interface PublicBracketMatchView {
  stage: Extract<TournamentStage, "semifinal" | "final">;
  position: number;
  firstDisplayName: string;
  secondDisplayName: string;
  score: SeriesScoreView;
  state: SeriesState;
  scheduledAt: string | null;
}

export interface PublicDraftActionView {
  turn: number;
  action: components["schemas"]["DraftActionType"];
  category: TournamentCategory;
  actorDisplayName: string;
  occurredAt: string;
}

export interface PublicLiveDraftView {
  tournamentId: string;
  seriesId: string;
  format: TournamentFormat;
  state: components["schemas"]["DraftState"];
  pool: readonly TournamentCategory[];
  selectedCategories: readonly TournamentCategory[];
  actions: readonly PublicDraftActionView[];
  projectionRevision: number;
}

export interface PublicOfficialResultView {
  revisionId: string;
  seriesId: string;
  state: SeriesState;
  winnerDisplayName?: string;
  score: SeriesScoreView;
  recordedAt: string;
}

export interface PublicRecoveryCursorView {
  projectionRevision: number;
  eventSequence: number;
}

export interface PublicArenaView {
  tournament: PublicTournamentView;
  scoreboard: readonly PublicScoreboardEntryView[];
  bracket: readonly PublicBracketMatchView[];
  liveSeries: readonly PublicSeriesView[];
  officialResults: readonly PublicOfficialResultView[];
  liveDraft: PublicLiveDraftView | null;
  nextCursor: PublicRecoveryCursorView;
}

export interface ParticipantLobbySeriesView {
  seriesId: string;
  waveId: string;
  opponentDisplayName: string;
  format: TournamentFormat;
  state: SeriesState;
}

export interface ParticipantLobbyView {
  tournamentId: string;
  state: TournamentState;
  projectionRevision: number;
  rosterLocked: boolean;
  series: readonly ParticipantLobbySeriesView[];
}

export interface ParticipantTaskView {
  snapshotId: string;
  taskId: string;
  version: number;
  kind: components["schemas"]["TaskKind"];
  title: string;
  description: string;
  category: TournamentCategory;
  difficulty: components["schemas"]["Difficulty"];
  timeLimit: number;
  hints: readonly string[];
  taskUrl?: string | null;
  sourceFileAvailable: boolean;
}

export interface ParticipantDeliveryReceiptView {
  id: string;
  assignmentId: string;
  attemptId: string;
  participantId: string;
  snapshotId: string;
  taskId: string;
  deliveredAt: string;
}

export interface ParticipantAssignmentView {
  assignmentId: string;
  attemptId: string;
  activeSnapshot: ParticipantTaskView;
  receipt: ParticipantDeliveryReceiptView;
  undisclosedReserveCount: number;
}

export interface ParticipantDraftActionView {
  action: components["schemas"]["DraftActionType"];
  actorId: string;
  category: TournamentCategory;
  occurredAt: string;
  turn: number;
  turnDeadline: string;
}

export interface ParticipantDraftView {
  draftId: string;
  seriesId: string;
  firstParticipantId: string;
  secondParticipantId: string;
  format: TournamentFormat;
  state: components["schemas"]["DraftState"];
  pool: readonly TournamentCategory[];
  selectedCategories: readonly TournamentCategory[];
  revision: number;
  turn: number;
  turnDeadline: string | null;
  actions: readonly ParticipantDraftActionView[];
}

export interface ParticipantWaveView {
  waveId: string;
  tournamentId: string;
  state: components["schemas"]["WaveState"];
  revision: number;
  revisionId: string;
  startedAt: string | null;
  pausedAt: string | null;
  readyWindow: ParticipantReadyWindowView | null;
  members: readonly ParticipantWaveMemberView[];
}

export interface ParticipantReadyWindowView {
  id: string;
  waveId: string;
  revisionId: string;
  state: components["schemas"]["ReadyWindowState"];
  openedAt: string;
  deadline: string;
  consumedAt: string | null;
}

export interface ParticipantWaveMemberView {
  participantId: string;
  seriesId: string | null;
  ready: boolean;
  readinessRevision: number;
}

export interface ParticipantRecoveryCursorView {
  projectionRevision: number;
  participantViewRevision: number;
  eventSequence: number;
}

export interface ParticipantTournamentView {
  tournamentId: string;
  projectionRevision: number;
  lobby: ParticipantLobbyView;
  series: ParticipantSeriesView | null;
  wave: ParticipantWaveView | null;
  draft: ParticipantDraftView | null;
  assignment: ParticipantAssignmentView | null;
  nextCursor: ParticipantRecoveryCursorView;
}

export interface OperatorParticipantView {
  participantId: string;
  playerId: string;
  rosterId: string;
  tournamentId: string;
  seed: number;
  attendance: components["schemas"]["AttendanceState"];
  createdAt: string;
  updatedAt: string;
}

export interface OperatorRosterView {
  rosterId: string;
  tournamentId: string;
  revision: number;
  locked: boolean;
  executionStarted: boolean;
  lockedAt: string | null;
  executionStartedAt: string | null;
  createdAt: string;
  updatedAt: string;
  participants: readonly OperatorParticipantView[];
}

export interface OperatorReadyWindowView {
  id: string;
  waveId: string;
  revisionId: string;
  state: components["schemas"]["ReadyWindowState"];
  openedAt: string;
  deadline: string;
  consumedAt: string | null;
}

export interface OperatorWaveMemberView {
  participantId: string;
  seriesId: string | null;
  ready: boolean;
  readinessRevision: number;
}

export interface OperatorWaveView {
  waveId: string;
  tournamentId: string;
  state: components["schemas"]["WaveState"];
  revision: number;
  revisionId: string;
  startedAt: string | null;
  pausedAt: string | null;
  readyWindow: OperatorReadyWindowView | null;
  members: readonly OperatorWaveMemberView[];
}

export interface OperatorTournamentSummaryView {
  tournamentId: string;
  rosterId: string;
  preset: TournamentPreset;
  name: string;
  publicId: string;
  plannedRosterSize: number;
  contentRevision: number;
  state: TournamentState;
  revision: number;
  rosterSize: number;
  pausedFromState: TournamentState | null;
  createdAt: string;
  updatedAt: string;
  startedAt: string | null;
  finishedAt: string | null;
}

export interface OperatorRecoveryCursorView {
  projectionRevision: number;
  authorityRevision: number;
  auditSequence: number;
}

export interface OperatorPauseSeriesView {
  series: OperatorSeriesView;
  currentGameId: string | null;
  resumeState: SeriesState | null;
  revision: number;
}

export interface OperatorPauseGameView {
  game: GameView;
  seriesId: string;
  deadline: string | null;
  resumeState: components["schemas"]["GameState"] | null;
  revision: number;
}

export interface OperatorPresenceView {
  id: string;
  participantId: string;
  seriesId: string;
  tournamentId: string;
  rosterId: string;
  state: components["schemas"]["PresenceState"];
  connectedAt: string;
  disconnectedAt: string | null;
  presenceEpoch: number;
  revision: number;
  updatedAt: string;
}

export interface OperatorReconnectView {
  id: string;
  participantId: string;
  seriesId: string;
  gameId: string;
  pauseId: string;
  rosterId: string;
  state: components["schemas"]["ReconnectState"];
  number: number;
  continuationNumber: number;
  presenceEpoch: number;
  deadline: string;
  openedAt: string;
  closedAt: string | null;
  continuedFromId: string | null;
  suspendedByPauseId: string | null;
  revision: number;
  updatedAt: string;
}

export interface OperatorReconnectCounterView {
  participantId: string;
  pauseId: string;
  rosterId: string;
  limit: number;
  used: number;
  revision: number;
}

export interface OperatorFrozenDeadlineView {
  kind: components["schemas"]["PauseDeadlineKind"];
  ownerId: string;
  originalDeadline: string;
  frozenAt: string;
  resumedAt: string | null;
  resumedDeadline: string | null;
  remainingMs: number;
  revision: number;
}

export interface OperatorPauseGraphView {
  tournamentId: string;
  rosterId: string;
  wave: OperatorWaveView;
  series: readonly OperatorPauseSeriesView[];
  games: readonly OperatorPauseGameView[];
  draft: ParticipantDraftView | null;
  presence: readonly OperatorPresenceView[];
  reconnect: readonly OperatorReconnectView[];
  counters: readonly OperatorReconnectCounterView[];
  frozenDeadlines: readonly OperatorFrozenDeadlineView[];
  activePauseId: string | null;
  pausedAt: string | null;
  deadlinesSuppressed: boolean;
  graphRevision: number;
  terminalActionRevision: number;
}

export interface OperatorTournamentView {
  tournament: OperatorTournamentSummaryView;
  roster: OperatorRosterView;
  waves: readonly OperatorWaveView[];
  series: readonly OperatorSeriesView[];
  pauseGraph: OperatorPauseGraphView | null;
  nextCursor: OperatorRecoveryCursorView;
}

export type TournamentGameView = GameView;
export type TournamentSlotView = SeriesSlotView;
