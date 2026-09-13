import type { components } from "./schema";
import { isUUID } from "../lib/validation";

type AdminPlayer = components["schemas"]["PlayerManagementView"];
type AdminPlayerAuditEvent = components["schemas"]["PlayerAuditEvent"];
type AdminPlayerAuditState = components["schemas"]["PlayerAuditState"];
type AdminTask = components["schemas"]["TaskDetails"];
type AdminSessionResponse = components["schemas"]["AdminSessionResponse"];
type JoinPlayerResponse = components["schemas"]["JoinPlayerResponse"];
type LeaderboardEntry = components["schemas"]["LeaderboardEntry"];
type LeaderboardResponse = components["schemas"]["LeaderboardResponse"];
type CurrentPlayerResponse = components["schemas"]["CurrentPlayerResponse"];
type PlayerResponse = components["schemas"]["PlayerResponse"];
type UploadSourceResponse = components["schemas"]["TaskSourceUploadResponse"];
export type TournamentContentSelection = components["schemas"]["TournamentContentSelection"];

type Guard<T> = (value: unknown) => value is T;

const TASK_CATEGORIES = new Set<AdminTask["category"]>([
  "web",
  "crypto",
  "forensics",
  "reverse",
  "pwn",
  "steganography",
  "ppc",
  "osint",
  "mobile",
  "hardware",
  "misc",
]);

const TASK_DIFFICULTIES = new Set<AdminTask["difficulty"]>([
  "easy",
  "medium",
  "hard",
]);

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === "object";

const isString = (value: unknown): value is string => typeof value === "string";

const isDateString = (value: unknown): value is string =>
  isString(value) && Number.isFinite(Date.parse(value));

const isOptionalDateStringOrNull = (value: unknown): value is string | null | undefined =>
  value === undefined || value === null || isDateString(value);

const isNumber = (value: unknown): value is number =>
  typeof value === "number" && Number.isFinite(value);

const isInteger = (value: unknown): value is number =>
  isNumber(value) && Number.isInteger(value);

const isPositiveInteger = (value: unknown): value is number => isInteger(value) && value > 0;

const isSafePositiveInteger = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value > 0;

const isNonNegativeInteger = (value: unknown): value is number =>
  isInteger(value) && value >= 0;

const isOptionalStringOrNull = (value: unknown): value is string | null | undefined =>
  value === undefined || value === null || isString(value);

const isHttpURL = (value: unknown): value is string => {
  if (!isString(value)) {
    return false;
  }
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false;
  }
};

const isOptionalHttpURLOrNull = (value: unknown): value is string | null | undefined =>
  value === undefined || value === null || isHttpURL(value);

const isPositionalHintArray = (value: unknown): value is (string | null)[] =>
  Array.isArray(value) &&
  value.length <= 3 &&
  value.every((item) => item === null || (isString(item) && item.trim().length > 0));

const NIL_UUID = "00000000-0000-0000-0000-000000000000";
const DATE_TIME_PATTERN =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;

const isNonNilUUID = (value: unknown): value is string =>
  isUUID(value) && value.toLowerCase() !== NIL_UUID;

const isDateTimeString = (value: unknown): value is string =>
  isDateString(value) && DATE_TIME_PATTERN.test(value);

const hasExactKeys = (
  value: Record<string, unknown>,
  allowedKeys: readonly string[],
): boolean => {
  const keys = Object.keys(value);
  return keys.length === allowedKeys.length && keys.every((key) => allowedKeys.includes(key));
};

export class ApiContractError extends Error {
  constructor(contract: string) {
    super(`Invalid API response: ${contract}`);
    this.name = "ApiContractError";
  }
}

export const assertApiResponse = <T>(
  value: unknown,
  guard: Guard<T>,
  contract: string,
): T => {
  if (!guard(value)) {
    throw new ApiContractError(contract);
  }
  return value;
};

export const isJoinPlayerResponse = (value: unknown): value is JoinPlayerResponse =>
  isRecord(value) && isUUID(value.player_id);

const isPlayerResponse = (value: unknown): value is PlayerResponse =>
  isRecord(value) &&
  isUUID(value.id) &&
  isString(value.username) &&
  isDateString(value.created_at);

export const isCurrentPlayerResponse = (value: unknown): value is CurrentPlayerResponse =>
  isRecord(value) &&
  isPlayerResponse(value.player);

const isLeaderboardEntry = (value: unknown): value is LeaderboardEntry =>
  isRecord(value) &&
  isPositiveInteger(value.rank) &&
  isString(value.username) &&
  isNonNegativeInteger(value.wins) &&
  isNonNegativeInteger(value.average_solve_time_ms);

export const isLeaderboardResponse = (value: unknown): value is LeaderboardResponse =>
  isRecord(value) && Array.isArray(value.entries) && value.entries.every(isLeaderboardEntry);

export const isAdminSessionResponse = (value: unknown): value is AdminSessionResponse =>
  isRecord(value) && isNonNegativeInteger(value.expires_in);

export const isAdminTask = (value: unknown): value is AdminTask =>
  isRecord(value) &&
  isUUID(value.id) &&
  isString(value.title) &&
  isString(value.description) &&
  isString(value.category) &&
  TASK_CATEGORIES.has(value.category as AdminTask["category"]) &&
  isString(value.difficulty) &&
  TASK_DIFFICULTIES.has(value.difficulty as AdminTask["difficulty"]) &&
  isPositiveInteger(value.time_limit) &&
  isString(value.flag) &&
  isPositionalHintArray(value.hints) &&
  isDateString(value.created_at) &&
  isOptionalStringOrNull(value.task_url) &&
  isOptionalHttpURLOrNull(value.source_file_url);

export const isAdminTaskArray = (value: unknown): value is AdminTask[] =>
  Array.isArray(value) && value.every(isAdminTask);

export const isAdminPlayer = (value: unknown): value is AdminPlayer =>
  isRecord(value) &&
  isUUID(value.id) &&
  isString(value.username) &&
  isDateString(value.created_at) &&
  isOptionalDateStringOrNull(value.deleted_at) &&
  isNonNegativeInteger(value.wins) &&
  isNonNegativeInteger(value.average_solve_time_ms) &&
  typeof value.stats_overridden === "boolean";

export const isAdminPlayerArray = (value: unknown): value is AdminPlayer[] =>
  Array.isArray(value) && value.every(isAdminPlayer);

const isAdminPlayerAuditState = (value: unknown): value is AdminPlayerAuditState =>
  isRecord(value) &&
  isString(value.username) &&
  isNonNegativeInteger(value.wins) &&
  isNonNegativeInteger(value.average_solve_time_ms) &&
  typeof value.stats_overridden === "boolean" &&
  typeof value.deleted === "boolean";

const isAdminPlayerAuditEvent = (value: unknown): value is AdminPlayerAuditEvent =>
  isRecord(value) &&
  isUUID(value.id) &&
  isString(value.actor_subject) &&
  isString(value.actor_jti) &&
  (value.action === "update" || value.action === "delete") &&
  isUUID(value.player_id) &&
  isAdminPlayerAuditState(value.before_state) &&
  isAdminPlayerAuditState(value.after_state) &&
  isDateString(value.created_at);

export const isAdminPlayerAuditEventArray = (value: unknown): value is AdminPlayerAuditEvent[] =>
  Array.isArray(value) && value.every(isAdminPlayerAuditEvent);

export const isUploadSourceResponse = (value: unknown): value is UploadSourceResponse =>
  isRecord(value) && isHttpURL(value.source_file_url);

export const isTournamentContentSelection = (
  value: unknown,
): value is TournamentContentSelection => {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, [
      "content_revision",
      "publication_id",
      "published_at",
      "normal_pool_revision_id",
      "golden_pool_revision_id",
    ]) ||
    !isSafePositiveInteger(value.content_revision) ||
    !isNonNilUUID(value.publication_id) ||
    !isDateTimeString(value.published_at) ||
    !isNonNilUUID(value.normal_pool_revision_id) ||
    !isNonNilUUID(value.golden_pool_revision_id)
  ) {
    return false;
  }

  return value.normal_pool_revision_id.toLowerCase() !== value.golden_pool_revision_id.toLowerCase();
};

type ParticipantLobbyResponse = components["schemas"]["ParticipantLobbyResponse"];
type ParticipantAssignmentResponse = components["schemas"]["ParticipantAssignmentResponse"];
type ParticipantSourceFileResponse = components["schemas"]["ParticipantSourceFileResponse"];
type ParticipantReadyEvent = components["schemas"]["ReadinessEvent"];
type ParticipantDraft = components["schemas"]["Draft"];
type ParticipantSubmissionResponse = components["schemas"]["ParticipantSubmissionResponse"];
type ParticipantOfficialResult = components["schemas"]["OfficialResultRevision"];
type ParticipantPostSeriesResponse = components["schemas"]["ParticipantPostSeriesResponse"];
type ParticipantRecoverySnapshot = components["schemas"]["ParticipantRecoverySnapshot"];

const participantCategories = new Set<string>([
  "web",
  "crypto",
  "forensics",
  "reverse",
  "pwn",
  "steganography",
  "ppc",
  "osint",
  "mobile",
  "hardware",
  "misc",
]);
const participantTournamentStates = new Set<string>([
  "draft",
  "registration",
  "roster_locked",
  "swiss",
  "golden",
  "playoffs",
  "technical_pause",
  "completed",
  "cancelled",
]);
const participantSeriesStates = new Set<string>([
  "planned",
  "locked",
  "draft",
  "ready",
  "active",
  "replay_required",
  "technical_pause",
  "completed",
  "cancelled",
]);
const participantWaveStates = new Set<string>([
  "planned",
  "ready_window_open",
  "ready",
  "active",
  "paused",
  "completed",
  "ready_window_expired",
  "superseded",
]);
const participantReadyWindowStates = new Set<string>([
  "open",
  "consumed",
  "expired",
  "superseded",
]);
const participantGameStates = new Set<string>([
  "planned",
  "ready",
  "active",
  "paused",
  "completed",
  "void",
  "cancelled",
  "superseded",
]);
const participantGameResultReasons = new Set<string>([
  "solved",
  "surrender",
  "operator_forfeit",
  "no_solve",
  "task_failure",
  "common_platform_failure",
  "disconnect",
  "execution_epoch_break",
  "no_show",
  "series_cancelled",
  "tournament_cancelled",
  "derived_revision_superseded",
]);
const participantSeriesResultReasons = new Set<string>([
  "score_complete",
  "operator_correction",
  "series_cancelled",
  "tournament_cancelled",
]);

const isParticipantObject = (value: unknown): value is Record<string, unknown> =>
  isRecord(value) && !Array.isArray(value);

const hasOnlyParticipantKeys = (
  value: Record<string, unknown>,
  keys: readonly string[],
): boolean => Object.keys(value).every((key) => keys.includes(key));

const isParticipantUUIDOrNull = (value: unknown): boolean =>
  value === null || isNonNilUUID(value);

const isParticipantDateOrNull = (value: unknown): boolean =>
  value === null || isDateTimeString(value);

const isParticipantCategory = (value: unknown): boolean =>
  isString(value) && participantCategories.has(value);

const isParticipantFormat = (value: unknown): boolean => value === "bo1" || value === "bo3";

const isParticipantSeriesState = (value: unknown): boolean =>
  isString(value) && participantSeriesStates.has(value);

const isParticipantScore = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["first_participant_wins", "second_participant_wins"]) &&
  isNonNegativeInteger(value.first_participant_wins) &&
  value.first_participant_wins <= 2 &&
  isNonNegativeInteger(value.second_participant_wins) &&
  value.second_participant_wins <= 2;

const isParticipantTaskSnapshot = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasOnlyParticipantKeys(value, [
    "snapshot_id",
    "task_id",
    "version",
    "kind",
    "title",
    "description",
    "category",
    "difficulty",
    "time_limit",
    "hints",
    "task_url",
    "source_file_available",
  ]) &&
  isNonNilUUID(value.snapshot_id) &&
  isNonNilUUID(value.task_id) &&
  isSafePositiveInteger(value.version) &&
  (value.kind === "normal" || value.kind === "golden") &&
  isString(value.title) &&
  isString(value.description) &&
  isParticipantCategory(value.category) &&
  (value.difficulty === "easy" || value.difficulty === "medium" || value.difficulty === "hard") &&
  isSafePositiveInteger(value.time_limit) &&
  Array.isArray(value.hints) &&
  value.hints.every(isString) &&
  isOptionalStringOrNull(value.task_url) &&
  typeof value.source_file_available === "boolean";

const isParticipantDeliveryReceipt = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, [
    "id",
    "assignment_id",
    "attempt_id",
    "participant_id",
    "snapshot_id",
    "task_id",
    "delivered_at",
  ]) &&
  isNonNilUUID(value.id) &&
  isNonNilUUID(value.assignment_id) &&
  isNonNilUUID(value.attempt_id) &&
  isNonNilUUID(value.participant_id) &&
  isNonNilUUID(value.snapshot_id) &&
  isNonNilUUID(value.task_id) &&
  isDateTimeString(value.delivered_at);

const isParticipantAssignment = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["id", "attempt_id", "active_snapshot", "undisclosed_reserve_count", "receipt"]) &&
  isNonNilUUID(value.id) &&
  isNonNilUUID(value.attempt_id) &&
  isParticipantTaskSnapshot(value.active_snapshot) &&
  isNonNegativeInteger(value.undisclosed_reserve_count) &&
  value.undisclosed_reserve_count <= 2 &&
  isParticipantDeliveryReceipt(value.receipt);

const isParticipantLobbySeries = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["series_id", "state", "format", "opponent_display_name", "wave_id"]) &&
  isNonNilUUID(value.series_id) &&
  isParticipantSeriesState(value.state) &&
  isParticipantFormat(value.format) &&
  isString(value.opponent_display_name) &&
  value.opponent_display_name.length <= 64 &&
  isNonNilUUID(value.wave_id);

export const isParticipantLobbyResponse = (
  value: unknown,
): value is ParticipantLobbyResponse =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["tournament_id", "state", "projection_revision", "roster_locked", "series"]) &&
  isNonNilUUID(value.tournament_id) &&
  isString(value.state) &&
  participantTournamentStates.has(value.state) &&
  isSafePositiveInteger(value.projection_revision) &&
  typeof value.roster_locked === "boolean" &&
  Array.isArray(value.series) &&
  value.series.every(isParticipantLobbySeries);

export const isParticipantAssignmentResponse = (
  value: unknown,
): value is ParticipantAssignmentResponse =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["tournament_id", "projection_revision", "assignment"]) &&
  isNonNilUUID(value.tournament_id) &&
  isSafePositiveInteger(value.projection_revision) &&
  isParticipantAssignment(value.assignment);

export const isParticipantSourceFileResponse = (
  value: unknown,
): value is ParticipantSourceFileResponse =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["source_file_url", "expires_at"]) &&
  isHttpURL(value.source_file_url) &&
  isDateTimeString(value.expires_at);

export const isParticipantReadyEvent = (value: unknown): value is ParticipantReadyEvent =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["command_id", "wave_id", "window_id", "participant_id", "type", "occurred_at"]) &&
  isNonNilUUID(value.command_id) &&
  isNonNilUUID(value.wave_id) &&
  isNonNilUUID(value.window_id) &&
  isNonNilUUID(value.participant_id) &&
  (value.type === "ready" || value.type === "cleared") &&
  isDateTimeString(value.occurred_at);

const isParticipantDraftAction = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["turn", "actor_id", "action", "category", "occurred_at", "turn_deadline"]) &&
  isSafePositiveInteger(value.turn) &&
  isNonNilUUID(value.actor_id) &&
  (value.action === "ban" || value.action === "pick") &&
  isParticipantCategory(value.category) &&
  isDateTimeString(value.occurred_at) &&
  isDateTimeString(value.turn_deadline);

const isParticipantDraftValue = (value: unknown): value is ParticipantDraft =>
  isParticipantObject(value) &&
  hasExactKeys(value, [
    "id",
    "series_id",
    "revision",
    "format",
    "first_participant_id",
    "second_participant_id",
    "pool",
    "state",
    "turn",
    "turn_deadline",
    "actions",
    "selected_categories",
  ]) &&
  isNonNilUUID(value.id) &&
  isNonNilUUID(value.series_id) &&
  isSafePositiveInteger(value.revision) &&
  isParticipantFormat(value.format) &&
  isNonNilUUID(value.first_participant_id) &&
  isNonNilUUID(value.second_participant_id) &&
  value.first_participant_id !== value.second_participant_id &&
  Array.isArray(value.pool) &&
  value.pool.length >= 3 &&
  value.pool.length <= 5 &&
  value.pool.every(isParticipantCategory) &&
  new Set(value.pool).size === value.pool.length &&
  (value.state === "active" || value.state === "completed") &&
  isSafePositiveInteger(value.turn) &&
  isParticipantDateOrNull(value.turn_deadline) &&
  Array.isArray(value.actions) &&
  value.actions.length <= 4 &&
  value.actions.every(isParticipantDraftAction) &&
  Array.isArray(value.selected_categories) &&
  value.selected_categories.length <= 3 &&
  value.selected_categories.every(isParticipantCategory) &&
  new Set(value.selected_categories).size === value.selected_categories.length;

export const isParticipantDraft = (value: unknown): value is ParticipantDraft =>
  isParticipantDraftValue(value);

const isParticipantGame = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["id", "slot_id", "attempt_no", "state", "result_reason", "winner_id", "result_revision_id"]) &&
  isNonNilUUID(value.id) &&
  isNonNilUUID(value.slot_id) &&
  isSafePositiveInteger(value.attempt_no) &&
  isString(value.state) &&
  participantGameStates.has(value.state) &&
  (value.result_reason === null || (isString(value.result_reason) && participantGameResultReasons.has(value.result_reason))) &&
  isParticipantUUIDOrNull(value.winner_id) &&
  isParticipantUUIDOrNull(value.result_revision_id);

const isParticipantGameSlot = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["id", "series_id", "position", "category", "score_before", "attempts"]) &&
  isNonNilUUID(value.id) &&
  isNonNilUUID(value.series_id) &&
  isSafePositiveInteger(value.position) &&
  value.position <= 3 &&
  isParticipantCategory(value.category) &&
  isParticipantScore(value.score_before) &&
  Array.isArray(value.attempts) &&
  value.attempts.every(isParticipantGame);

const isParticipantSeries = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, [
    "id",
    "tournament_id",
    "first_participant_id",
    "second_participant_id",
    "format",
    "state",
    "score",
    "winner_id",
    "slots",
    "current_score_revision_id",
    "current_result_revision_id",
  ]) &&
  isNonNilUUID(value.id) &&
  isNonNilUUID(value.tournament_id) &&
  isNonNilUUID(value.first_participant_id) &&
  isNonNilUUID(value.second_participant_id) &&
  value.first_participant_id !== value.second_participant_id &&
  isParticipantFormat(value.format) &&
  isParticipantSeriesState(value.state) &&
  isParticipantScore(value.score) &&
  isParticipantUUIDOrNull(value.winner_id) &&
  Array.isArray(value.slots) &&
  value.slots.length <= 3 &&
  value.slots.every(isParticipantGameSlot) &&
  isParticipantUUIDOrNull(value.current_score_revision_id) &&
  isParticipantUUIDOrNull(value.current_result_revision_id);

const isParticipantReadyWindow = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["id", "wave_id", "revision_id", "state", "opened_at", "deadline", "consumed_at"]) &&
  isNonNilUUID(value.id) &&
  isNonNilUUID(value.wave_id) &&
  isNonNilUUID(value.revision_id) &&
  isString(value.state) &&
  participantReadyWindowStates.has(value.state) &&
  isDateTimeString(value.opened_at) &&
  isDateTimeString(value.deadline) &&
  isParticipantDateOrNull(value.consumed_at);

const isParticipantWaveMember = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasOnlyParticipantKeys(value, ["participant_id", "series_id", "ready", "readiness_revision"]) &&
  isNonNilUUID(value.participant_id) &&
  (value.series_id === undefined || isParticipantUUIDOrNull(value.series_id)) &&
  typeof value.ready === "boolean" &&
  isSafePositiveInteger(value.readiness_revision);

const isParticipantWave = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, [
    "id",
    "tournament_id",
    "revision_id",
    "revision",
    "state",
    "members",
    "ready_window",
    "started_at",
    "paused_at",
  ]) &&
  isNonNilUUID(value.id) &&
  isNonNilUUID(value.tournament_id) &&
  isNonNilUUID(value.revision_id) &&
  isSafePositiveInteger(value.revision) &&
  isString(value.state) &&
  participantWaveStates.has(value.state) &&
  Array.isArray(value.members) &&
  value.members.length >= 2 &&
  value.members.every(isParticipantWaveMember) &&
  (value.ready_window === null || isParticipantReadyWindow(value.ready_window)) &&
  isParticipantDateOrNull(value.started_at) &&
  isParticipantDateOrNull(value.paused_at);

const isParticipantSubmissionScope = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["wave_id", "tournament_id", "series_id", "slot_id", "game_id", "assignment_id"]) &&
  isNonNilUUID(value.wave_id) &&
  isNonNilUUID(value.tournament_id) &&
  isNonNilUUID(value.series_id) &&
  isNonNilUUID(value.slot_id) &&
  isNonNilUUID(value.game_id) &&
  isNonNilUUID(value.assignment_id);

const isParticipantSubmissionRecord = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, [
    "scope",
    "command_id",
    "participant_id",
    "sequence",
    "committed_at",
    "correct",
    "snapshot_id",
    "task_id",
    "content_digest",
  ]) &&
  isParticipantSubmissionScope(value.scope) &&
  isNonNilUUID(value.command_id) &&
  isNonNilUUID(value.participant_id) &&
  isSafePositiveInteger(value.sequence) &&
  isDateTimeString(value.committed_at) &&
  typeof value.correct === "boolean" &&
  isNonNilUUID(value.snapshot_id) &&
  isNonNilUUID(value.task_id) &&
  isString(value.content_digest) &&
  /^[0-9a-f]{64}$/.test(value.content_digest);

export const isParticipantSubmissionResponse = (
  value: unknown,
): value is ParticipantSubmissionResponse =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["projection_revision", "submission"]) &&
  isSafePositiveInteger(value.projection_revision) &&
  isParticipantSubmissionRecord(value.submission);

export const isParticipantOfficialResult = (
  value: unknown,
): value is ParticipantOfficialResult =>
  isParticipantObject(value) &&
  hasExactKeys(value, [
    "id",
    "previous_revision_id",
    "ordinal",
    "command_id",
    "subject_kind",
    "tournament_id",
    "series_id",
    "game_id",
    "actor_kind",
    "actor_id",
    "game_state",
    "game_reason",
    "series_state",
    "series_reason",
    "winner_id",
    "score_revision_id",
    "source_projection_revision_id",
    "recorded_at",
  ]) &&
  isNonNilUUID(value.id) &&
  isParticipantUUIDOrNull(value.previous_revision_id) &&
  isSafePositiveInteger(value.ordinal) &&
  value.ordinal <= 2_147_483_647 &&
  isNonNilUUID(value.command_id) &&
  (value.subject_kind === "game" || value.subject_kind === "series") &&
  isNonNilUUID(value.tournament_id) &&
  isNonNilUUID(value.series_id) &&
  isParticipantUUIDOrNull(value.game_id) &&
  (value.actor_kind === "server" || value.actor_kind === "operator") &&
  isParticipantUUIDOrNull(value.actor_id) &&
  (value.game_state === null || (isString(value.game_state) && participantGameStates.has(value.game_state))) &&
  (value.game_reason === null || (isString(value.game_reason) && participantGameResultReasons.has(value.game_reason))) &&
  (value.series_state === null || isParticipantSeriesState(value.series_state)) &&
  (value.series_reason === null || (isString(value.series_reason) && participantSeriesResultReasons.has(value.series_reason))) &&
  isParticipantUUIDOrNull(value.winner_id) &&
  isParticipantUUIDOrNull(value.score_revision_id) &&
  isNonNilUUID(value.source_projection_revision_id) &&
  isDateTimeString(value.recorded_at);

export const isParticipantPostSeriesResponse = (
  value: unknown,
): value is ParticipantPostSeriesResponse =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["series_id", "projection_revision", "accepted_action"]) &&
  isNonNilUUID(value.series_id) &&
  isSafePositiveInteger(value.projection_revision) &&
  (value.accepted_action === "acknowledge_result" ||
    value.accepted_action === "request_next_assignment" ||
    value.accepted_action === "leave_lobby");

const isParticipantRecoveryCursor = (value: unknown): boolean =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["projection_revision", "participant_view_revision", "event_sequence"]) &&
  isSafePositiveInteger(value.projection_revision) &&
  isSafePositiveInteger(value.participant_view_revision) &&
  isNonNegativeInteger(value.event_sequence);

export const isParticipantRecoverySnapshot = (
  value: unknown,
): value is ParticipantRecoverySnapshot =>
  isParticipantObject(value) &&
  hasExactKeys(value, ["tournament_id", "projection_revision", "lobby", "series", "wave", "draft", "assignment", "next_cursor"]) &&
  isNonNilUUID(value.tournament_id) &&
  isSafePositiveInteger(value.projection_revision) &&
  isParticipantLobbyResponse(value.lobby) &&
  (value.series === null || isParticipantSeries(value.series)) &&
  (value.wave === null || isParticipantWave(value.wave)) &&
  (value.draft === null || isParticipantDraftValue(value.draft)) &&
  (value.assignment === null || isParticipantAssignment(value.assignment)) &&
  isParticipantRecoveryCursor(value.next_cursor);
