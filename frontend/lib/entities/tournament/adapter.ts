import type { components } from "../../shared/api/schema";

import type { GameView } from "./game";
import type {
  OperatorFrozenDeadlineView,
  OperatorPauseGameView,
  OperatorPauseGraphView,
  OperatorPauseSeriesView,
  OperatorParticipantView,
  OperatorPresenceView,
  OperatorReconnectCounterView,
  OperatorReconnectView,
  OperatorReadyWindowView,
  OperatorRecoveryCursorView,
  OperatorRosterView,
  OperatorTournamentSummaryView,
  OperatorTournamentView,
  OperatorWaveMemberView,
  OperatorWaveView,
  ParticipantAssignmentView,
  ParticipantDeliveryReceiptView,
  ParticipantDraftActionView,
  ParticipantDraftView,
  ParticipantLobbySeriesView,
  ParticipantLobbyView,
  ParticipantReadyWindowView,
  ParticipantRecoveryCursorView,
  ParticipantTaskView,
  ParticipantTournamentView,
  ParticipantWaveMemberView,
  ParticipantWaveView,
  PublicArenaView,
  PublicBracketMatchView,
  PublicDraftActionView,
  PublicLiveDraftView,
  PublicOfficialResultView,
  PublicRecoveryCursorView,
  PublicScoreboardEntryView,
  PublicTournamentView,
} from "./model";
import type {
  OperatorSeriesView,
  ParticipantSeriesView,
  PublicSeriesView,
  SeriesScoreView,
  SeriesSlotView,
} from "./series";

type Tournament = components["schemas"]["Tournament"];
type PublicTournament = components["schemas"]["PublicTournamentResponse"];
type PublicSeriesScore = components["schemas"]["PublicSeriesScore"];
type Series = components["schemas"]["Series"];
type Game = components["schemas"]["Game"];
type GameSlot = components["schemas"]["GameSlot"];
type ParticipantLobby = components["schemas"]["ParticipantLobbyResponse"];
type ParticipantSeries = components["schemas"]["ParticipantLobbySeries"];
type ParticipantTask = components["schemas"]["ParticipantTaskSnapshot"];
type ParticipantAssignment = components["schemas"]["ParticipantAssignment"];
type DeliveryReceipt = components["schemas"]["DeliveryReceipt"];
type Draft = components["schemas"]["Draft"];
type Wave = components["schemas"]["Wave"];
type ReadyWindow = components["schemas"]["ReadyWindow"];
type WaveMember = components["schemas"]["WaveMember"];
type OperatorSnapshot = components["schemas"]["OperatorRecoverySnapshot"];
type ParticipantSnapshot = components["schemas"]["ParticipantRecoverySnapshot"];
type PublicSnapshot = components["schemas"]["PublicRecoverySnapshot"];

export const toGameView = (value: Game): GameView => ({
  id: value.id,
  slotId: value.slot_id,
  attemptNo: value.attempt_no,
  state: value.state,
  resultReason: value.result_reason,
  winnerId: value.winner_id,
  resultRevisionId: value.result_revision_id,
});

const toSeriesScoreView = (
  value: components["schemas"]["SeriesScore"],
): SeriesScoreView => ({
  firstWins: value.first_participant_wins,
  secondWins: value.second_participant_wins,
});

const toPublicSeriesScoreView = (value: PublicSeriesScore): SeriesScoreView => ({
  firstWins: value.first_wins,
  secondWins: value.second_wins,
});

export const toSeriesSlotView = (value: GameSlot): SeriesSlotView => ({
  slotId: value.id,
  seriesId: value.series_id,
  position: value.position,
  category: value.category,
  scoreBefore: toSeriesScoreView(value.score_before),
  attempts: value.attempts.map(toGameView),
});

export const toParticipantSeriesView = (value: Series): ParticipantSeriesView => ({
  seriesId: value.id,
  tournamentId: value.tournament_id,
  firstParticipantId: value.first_participant_id,
  secondParticipantId: value.second_participant_id,
  format: value.format,
  state: value.state,
  score: toSeriesScoreView(value.score),
  winnerId: value.winner_id,
  slots: value.slots.map(toSeriesSlotView),
});

export const toOperatorSeriesView = (value: Series): OperatorSeriesView => ({
  seriesId: value.id,
  tournamentId: value.tournament_id,
  firstParticipantId: value.first_participant_id,
  secondParticipantId: value.second_participant_id,
  format: value.format,
  state: value.state,
  score: toSeriesScoreView(value.score),
  winnerId: value.winner_id,
  slots: value.slots.map(toSeriesSlotView),
});

export const toPublicTournamentView = (value: PublicTournament): PublicTournamentView => ({
  tournamentId: value.tournament_id,
  preset: value.preset,
  state: value.state,
  rosterSize: value.roster_size,
  startedAt: value.started_at,
  finishedAt: value.finished_at,
  projectionRevision: value.projection_revision,
});

export const toOperatorTournamentSummaryView = (
  value: Tournament,
): OperatorTournamentSummaryView => ({
  tournamentId: value.id,
  rosterId: value.roster_id,
  preset: value.preset,
  name: value.name,
  publicId: value.public_id,
  plannedRosterSize: value.planned_roster_size,
  contentRevision: value.content_revision,
  state: value.state,
  revision: value.revision,
  rosterSize: value.roster_size,
  pausedFromState: value.paused_from_state ?? null,
  createdAt: value.created_at,
  updatedAt: value.updated_at,
  startedAt: value.started_at ?? null,
  finishedAt: value.finished_at ?? null,
});

const toPublicScoreboardEntryView = (
  value: components["schemas"]["PublicScoreboardEntry"],
): PublicScoreboardEntryView => ({
  rank: value.rank,
  displayName: value.display_name,
  points: value.points,
  wins: value.wins,
  losses: value.losses,
  byeCount: value.bye_count,
  provisionalTie: value.provisional_tie,
  qualificationStatus: value.qualification_status,
  buchholz: value.buchholz,
  effectiveTimeMs: value.effective_time_ms,
});

const toPublicBracketMatchView = (
  value: components["schemas"]["PublicBracketMatch"],
): PublicBracketMatchView => ({
  stage: value.stage,
  position: value.position,
  firstDisplayName: value.first_display_name,
  secondDisplayName: value.second_display_name,
  score: toSeriesScoreView(value.score),
  state: value.state,
  scheduledAt: value.scheduled_at,
});

const toPublicSeriesView = (
  value: components["schemas"]["PublicLiveSeries"],
): PublicSeriesView => ({
  seriesId: value.series_id,
  format: value.format,
  state: value.state,
  firstDisplayName: value.first_display_name,
  secondDisplayName: value.second_display_name,
  score: toPublicSeriesScoreView(value.score),
  currentGamePosition: value.current_game_position ?? null,
  stage: value.stage,
  roundNumber: value.round_number,
  scheduledAt: value.scheduled_at,
});

const toPublicDraftActionView = (
  value: components["schemas"]["PublicDraftAction"],
): PublicDraftActionView => ({
  turn: value.turn,
  action: value.action,
  category: value.category,
  actorDisplayName: value.actor_display_name,
  occurredAt: value.occurred_at,
});

const toPublicLiveDraftView = (
  value: components["schemas"]["PublicLiveDraftResponse"],
): PublicLiveDraftView => ({
  tournamentId: value.tournament_id,
  seriesId: value.series_id,
  format: value.format,
  state: value.state,
  pool: value.pool,
  selectedCategories: value.selected_categories,
  actions: value.actions.map(toPublicDraftActionView),
  projectionRevision: value.projection_revision,
});

const toPublicOfficialResultView = (
  value: components["schemas"]["PublicOfficialResult"],
): PublicOfficialResultView => ({
  revisionId: value.revision_id,
  seriesId: value.series_id,
  state: value.state,
  winnerDisplayName: value.winner_display_name,
  score: toPublicSeriesScoreView(value.score),
  recordedAt: value.recorded_at,
});

const toPublicRecoveryCursorView = (
  value: components["schemas"]["PublicRecoveryCursor"],
): PublicRecoveryCursorView => ({
  projectionRevision: value.projection_revision,
  eventSequence: value.event_sequence,
});

export const toPublicArenaView = (value: PublicSnapshot): PublicArenaView => ({
  tournament: toPublicTournamentView(value.tournament),
  scoreboard: value.scoreboard.entries.map(toPublicScoreboardEntryView),
  bracket: value.bracket.matches.map(toPublicBracketMatchView),
  liveSeries: value.live_series.map(toPublicSeriesView),
  officialResults: value.official_results.map(toPublicOfficialResultView),
  liveDraft: value.live_draft === null ? null : toPublicLiveDraftView(value.live_draft),
  nextCursor: toPublicRecoveryCursorView(value.next_cursor),
});

const toParticipantLobbySeriesView = (
  value: ParticipantSeries,
): ParticipantLobbySeriesView => ({
  seriesId: value.series_id,
  waveId: value.wave_id,
  opponentDisplayName: value.opponent_display_name,
  format: value.format,
  state: value.state,
});

export const toParticipantLobbyView = (value: ParticipantLobby): ParticipantLobbyView => ({
  tournamentId: value.tournament_id,
  state: value.state,
  projectionRevision: value.projection_revision,
  rosterLocked: value.roster_locked,
  series: value.series.map(toParticipantLobbySeriesView),
});

const toParticipantTaskView = (value: ParticipantTask): ParticipantTaskView => ({
  snapshotId: value.snapshot_id,
  taskId: value.task_id,
  version: value.version,
  kind: value.kind,
  title: value.title,
  description: value.description,
  category: value.category,
  difficulty: value.difficulty,
  timeLimit: value.time_limit,
  hints: value.hints,
  taskUrl: value.task_url,
  sourceFileAvailable: value.source_file_available,
});

const toDeliveryReceiptView = (value: DeliveryReceipt): ParticipantDeliveryReceiptView => ({
  id: value.id,
  assignmentId: value.assignment_id,
  attemptId: value.attempt_id,
  participantId: value.participant_id,
  snapshotId: value.snapshot_id,
  taskId: value.task_id,
  deliveredAt: value.delivered_at,
});

const toParticipantAssignmentView = (
  value: ParticipantAssignment,
): ParticipantAssignmentView => ({
  assignmentId: value.id,
  attemptId: value.attempt_id,
  activeSnapshot: toParticipantTaskView(value.active_snapshot),
  receipt: toDeliveryReceiptView(value.receipt),
  undisclosedReserveCount: value.undisclosed_reserve_count,
});

const toParticipantDraftActionView = (
  value: components["schemas"]["DraftAction"],
): ParticipantDraftActionView => ({
  action: value.action,
  actorId: value.actor_id,
  category: value.category,
  occurredAt: value.occurred_at,
  turn: value.turn,
  turnDeadline: value.turn_deadline,
});

const toParticipantDraftView = (value: Draft): ParticipantDraftView => ({
  draftId: value.id,
  seriesId: value.series_id,
  firstParticipantId: value.first_participant_id,
  secondParticipantId: value.second_participant_id,
  format: value.format,
  state: value.state,
  pool: value.pool,
  selectedCategories: value.selected_categories,
  revision: value.revision,
  turn: value.turn,
  turnDeadline: value.turn_deadline,
  actions: value.actions.map(toParticipantDraftActionView),
});

const toParticipantReadyWindowView = (
  value: ReadyWindow,
): ParticipantReadyWindowView => ({
  id: value.id,
  waveId: value.wave_id,
  revisionId: value.revision_id,
  state: value.state,
  openedAt: value.opened_at,
  deadline: value.deadline,
  consumedAt: value.consumed_at,
});

const toParticipantWaveMemberView = (
  value: WaveMember,
): ParticipantWaveMemberView => ({
  participantId: value.participant_id,
  seriesId: value.series_id ?? null,
  ready: value.ready,
  readinessRevision: value.readiness_revision,
});

const toParticipantWaveView = (value: Wave): ParticipantWaveView => ({
  waveId: value.id,
  tournamentId: value.tournament_id,
  state: value.state,
  revision: value.revision,
  revisionId: value.revision_id,
  startedAt: value.started_at,
  pausedAt: value.paused_at,
  readyWindow: value.ready_window === null ? null : toParticipantReadyWindowView(value.ready_window),
  members: value.members.map(toParticipantWaveMemberView),
});

const toParticipantRecoveryCursorView = (
  value: components["schemas"]["ParticipantRecoveryCursor"],
): ParticipantRecoveryCursorView => ({
  projectionRevision: value.projection_revision,
  participantViewRevision: value.participant_view_revision,
  eventSequence: value.event_sequence,
});

export const toParticipantTournamentView = (
  value: ParticipantSnapshot,
): ParticipantTournamentView => ({
  tournamentId: value.tournament_id,
  projectionRevision: value.projection_revision,
  lobby: toParticipantLobbyView(value.lobby),
  series: value.series === null ? null : toParticipantSeriesView(value.series),
  wave: value.wave === null ? null : toParticipantWaveView(value.wave),
  draft: value.draft === null ? null : toParticipantDraftView(value.draft),
  assignment: value.assignment === null ? null : toParticipantAssignmentView(value.assignment),
  nextCursor: toParticipantRecoveryCursorView(value.next_cursor),
});

const toOperatorParticipantView = (
  value: components["schemas"]["Participant"],
): OperatorParticipantView => ({
  participantId: value.id,
  playerId: value.player_id,
  rosterId: value.roster_id,
  tournamentId: value.tournament_id,
  seed: value.seed,
  attendance: value.attendance,
  createdAt: value.created_at,
  updatedAt: value.updated_at,
});

const toOperatorRosterView = (
  value: components["schemas"]["Roster"],
): OperatorRosterView => ({
  rosterId: value.id,
  tournamentId: value.tournament_id,
  revision: value.revision,
  locked: value.locked,
  executionStarted: value.execution_started,
  lockedAt: value.locked_at ?? null,
  executionStartedAt: value.execution_started_at ?? null,
  createdAt: value.created_at,
  updatedAt: value.updated_at,
  participants: value.participants.map(toOperatorParticipantView),
});

const toOperatorReadyWindowView = (value: ReadyWindow): OperatorReadyWindowView => ({
  id: value.id,
  waveId: value.wave_id,
  revisionId: value.revision_id,
  state: value.state,
  openedAt: value.opened_at,
  deadline: value.deadline,
  consumedAt: value.consumed_at,
});

const toOperatorWaveMemberView = (value: WaveMember): OperatorWaveMemberView => ({
  participantId: value.participant_id,
  seriesId: value.series_id ?? null,
  ready: value.ready,
  readinessRevision: value.readiness_revision,
});

const toOperatorWaveView = (value: Wave): OperatorWaveView => ({
  waveId: value.id,
  tournamentId: value.tournament_id,
  state: value.state,
  revision: value.revision,
  revisionId: value.revision_id,
  startedAt: value.started_at,
  pausedAt: value.paused_at,
  readyWindow: value.ready_window === null ? null : toOperatorReadyWindowView(value.ready_window),
  members: value.members.map(toOperatorWaveMemberView),
});

const toOperatorRecoveryCursorView = (
  value: components["schemas"]["OperatorRecoveryCursor"],
): OperatorRecoveryCursorView => ({
  projectionRevision: value.projection_revision,
  authorityRevision: value.authority_revision,
  auditSequence: value.audit_sequence,
});

const toOperatorPauseSeriesView = (
  value: components["schemas"]["PauseSeries"],
): OperatorPauseSeriesView => ({
  series: toOperatorSeriesView(value.series),
  currentGameId: value.current_game_id,
  resumeState: value.resume_state,
  revision: value.revision,
});

const toOperatorPauseGameView = (
  value: components["schemas"]["PauseGame"],
): OperatorPauseGameView => ({
  game: toGameView(value.game),
  seriesId: value.series_id,
  deadline: value.deadline,
  resumeState: value.resume_state,
  revision: value.revision,
});

const toOperatorPresenceView = (
  value: components["schemas"]["Presence"],
): OperatorPresenceView => ({
  id: value.id,
  participantId: value.participant_id,
  seriesId: value.series_id,
  tournamentId: value.tournament_id,
  rosterId: value.roster_id,
  state: value.state,
  connectedAt: value.connected_at,
  disconnectedAt: value.disconnected_at,
  presenceEpoch: value.presence_epoch,
  revision: value.revision,
  updatedAt: value.updated_at,
});

const toOperatorReconnectView = (
  value: components["schemas"]["ReconnectInterval"],
): OperatorReconnectView => ({
  id: value.id,
  participantId: value.participant_id,
  seriesId: value.series_id,
  gameId: value.game_id,
  pauseId: value.pause_id,
  rosterId: value.roster_id,
  state: value.state,
  number: value.number,
  continuationNumber: value.continuation_number,
  presenceEpoch: value.presence_epoch,
  deadline: value.deadline,
  openedAt: value.opened_at,
  closedAt: value.closed_at,
  continuedFromId: value.continued_from_id,
  suspendedByPauseId: value.suspended_by_pause_id,
  revision: value.revision,
  updatedAt: value.updated_at,
});

const toOperatorReconnectCounterView = (
  value: components["schemas"]["PauseReconnectCounter"],
): OperatorReconnectCounterView => ({
  participantId: value.participant_id,
  pauseId: value.pause_id,
  rosterId: value.roster_id,
  limit: value.limit,
  used: value.used,
  revision: value.revision,
});

const toOperatorFrozenDeadlineView = (
  value: components["schemas"]["FrozenDeadline"],
): OperatorFrozenDeadlineView => ({
  kind: value.kind,
  ownerId: value.owner_id,
  originalDeadline: value.original_deadline,
  frozenAt: value.frozen_at,
  resumedAt: value.resumed_at,
  resumedDeadline: value.resumed_deadline,
  remainingMs: value.remaining_ms,
  revision: value.revision,
});

const toOperatorPauseGraphView = (
  value: NonNullable<OperatorSnapshot["pause_graph"]>,
): OperatorPauseGraphView => ({
  tournamentId: value.tournament_id,
  rosterId: value.roster_id,
  wave: toOperatorWaveView(value.wave),
  series: value.series.map(toOperatorPauseSeriesView),
  games: value.games.map(toOperatorPauseGameView),
  draft: value.draft === null ? null : toParticipantDraftView(value.draft),
  presence: value.presence.map(toOperatorPresenceView),
  reconnect: value.reconnect.map(toOperatorReconnectView),
  counters: value.counters.map(toOperatorReconnectCounterView),
  frozenDeadlines: value.frozen_deadlines.map(toOperatorFrozenDeadlineView),
  activePauseId: value.active_pause_id,
  pausedAt: value.paused_at,
  deadlinesSuppressed: value.deadlines_suppressed,
  graphRevision: value.graph_revision,
  terminalActionRevision: value.terminal_action_revision,
});

export const toOperatorTournamentView = (
  value: OperatorSnapshot,
): OperatorTournamentView => ({
  tournament: toOperatorTournamentSummaryView(value.tournament),
  roster: toOperatorRosterView(value.roster),
  waves: value.waves.map(toOperatorWaveView),
  series: value.series.map(toOperatorSeriesView),
  pauseGraph: value.pause_graph === null ? null : toOperatorPauseGraphView(value.pause_graph),
  nextCursor: toOperatorRecoveryCursorView(value.next_cursor),
});
