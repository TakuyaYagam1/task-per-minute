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
type PublicTournamentResponse = components["schemas"]["PublicTournamentResponse"];
type PublicScoreboardEntry = components["schemas"]["PublicScoreboardEntry"];
type PublicScoreboardResponse = components["schemas"]["PublicScoreboardResponse"];
type PublicBracketMatch = components["schemas"]["PublicBracketMatch"];
type PublicBracketResponse = components["schemas"]["PublicBracketResponse"];
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

const isSafeInteger = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value);

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

const PUBLIC_TOURNAMENT_STATES = new Set<string>([
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

const PUBLIC_SERIES_STATES = new Set<string>([
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

const INT32_MAX = 2_147_483_647;

const isNonNegativeInt32 = (value: unknown): value is number =>
  isSafeInteger(value) && value >= 0 && value <= INT32_MAX;

const isBoundedDisplayName = (value: unknown): value is string =>
  isString(value) && value.trim().length > 0 && value.length <= 64;

const isPublicOptionalDateTime = (value: unknown): value is string | null =>
  value === null || isDateTimeString(value);

export const isPublicTournamentResponse = (
  value: unknown,
): value is PublicTournamentResponse =>
  isRecord(value) &&
  hasExactKeys(value, [
    "tournament_id",
    "preset",
    "state",
    "roster_size",
    "started_at",
    "finished_at",
    "projection_revision",
  ]) &&
  isNonNilUUID(value.tournament_id) &&
  value.preset === "tournament_v1" &&
  isString(value.state) &&
  PUBLIC_TOURNAMENT_STATES.has(value.state) &&
  isNonNegativeInteger(value.roster_size) &&
  value.roster_size <= 16 &&
  isPublicOptionalDateTime(value.started_at) &&
  isPublicOptionalDateTime(value.finished_at) &&
  isSafePositiveInteger(value.projection_revision);

export const isPublicScoreboardEntry = (
  value: unknown,
): value is PublicScoreboardEntry =>
  isRecord(value) &&
  hasExactKeys(value, [
    "rank",
    "display_name",
    "points",
    "buchholz",
    "effective_time_ms",
  ]) &&
  isPositiveInteger(value.rank) &&
  value.rank <= INT32_MAX &&
  isBoundedDisplayName(value.display_name) &&
  isNonNegativeInt32(value.points) &&
  isNonNegativeInt32(value.buchholz) &&
  isSafeInteger(value.effective_time_ms) &&
  value.effective_time_ms >= 0;

export const isPublicScoreboardResponse = (
  value: unknown,
): value is PublicScoreboardResponse =>
  isRecord(value) &&
  hasExactKeys(value, ["tournament_id", "projection_revision", "entries"]) &&
  isNonNilUUID(value.tournament_id) &&
  isSafePositiveInteger(value.projection_revision) &&
  Array.isArray(value.entries) &&
  value.entries.length <= 16 &&
  value.entries.every(isPublicScoreboardEntry);

export const isPublicBracketMatch = (value: unknown): value is PublicBracketMatch =>
  isRecord(value) &&
  hasExactKeys(value, [
    "stage",
    "position",
    "first_display_name",
    "second_display_name",
    "score",
    "state",
  ]) &&
  (value.stage === "semifinal" || value.stage === "final") &&
  isPositiveInteger(value.position) &&
  value.position <= INT32_MAX &&
  isBoundedDisplayName(value.first_display_name) &&
  isBoundedDisplayName(value.second_display_name) &&
  isRecord(value.score) &&
  hasExactKeys(value.score, ["first_participant_wins", "second_participant_wins"]) &&
  isNonNegativeInt32(value.score.first_participant_wins) &&
  value.score.first_participant_wins <= 2 &&
  isNonNegativeInt32(value.score.second_participant_wins) &&
  value.score.second_participant_wins <= 2 &&
  isString(value.state) &&
  PUBLIC_SERIES_STATES.has(value.state);

export const isPublicBracketResponse = (
  value: unknown,
): value is PublicBracketResponse =>
  isRecord(value) &&
  hasExactKeys(value, ["tournament_id", "projection_revision", "matches"]) &&
  isNonNilUUID(value.tournament_id) &&
  isSafePositiveInteger(value.projection_revision) &&
  Array.isArray(value.matches) &&
  value.matches.length <= 3 &&
  value.matches.every(isPublicBracketMatch);

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
  (value.kind === "normal" || value.kind === "golden") &&
  typeof value.enabled === "boolean" &&
  isSafePositiveInteger(value.version) &&
  value.version <= INT32_MAX &&
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

type Tournament = components["schemas"]["Tournament"];
type TournamentListResponse = components["schemas"]["TournamentListResponse"];
type Roster = components["schemas"]["Roster"];
type PreflightReport = components["schemas"]["PreflightReport"];
type SwissBye = components["schemas"]["SwissBye"];
type SwissPairing = components["schemas"]["SwissPairing"];
type SwissPairingEvidence = components["schemas"]["SwissPairingEvidence"];
type SwissStanding = components["schemas"]["SwissStanding"];
type SwissRound = components["schemas"]["SwissRound"];
type AuditCursor = components["schemas"]["AuditCursor"];
export type OperatorAuditRedactedPayload = Readonly<Partial<{
  attempt_id: string;
  entity_id: string;
  entity_kind: string;
  previous_revision_id: string;
  projection_revision_id: string;
  reason: string;
  result_reason: string;
  revision_number: number;
  series_id: string;
  source_projection_revision_id: string;
  state: string;
  tournament_id: string;
  winner_id: string;
}>>;
export type OperatorAuditEvent = Omit<components["schemas"]["AuditEvent"], "redacted_payload"> & {
  readonly redacted_payload: OperatorAuditRedactedPayload;
};
export type OperatorAuditPage = Omit<components["schemas"]["AuditPage"], "events"> & {
  readonly events: OperatorAuditEvent[];
};
type AuditEvent = OperatorAuditEvent;
type AuditPage = OperatorAuditPage;
type IncidentBundle = components["schemas"]["IncidentBundle"];
type OperatorRecoveryCursor = components["schemas"]["OperatorRecoveryCursor"];
type OperatorRecoverySnapshot = components["schemas"]["OperatorRecoverySnapshot"];

const operatorTournamentStates = new Set<string>([
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
const operatorSeriesStates = new Set<string>([
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
const operatorWaveStates = new Set<string>([
  "planned",
  "ready_window_open",
  "ready",
  "active",
  "paused",
  "completed",
  "ready_window_expired",
  "superseded",
]);
const operatorGameStates = new Set<string>([
  "planned",
  "ready",
  "active",
  "paused",
  "completed",
  "void",
  "cancelled",
  "superseded",
]);
const operatorGameResultReasons = new Set<string>([
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
const operatorSeriesResultReasons = new Set<string>([
  "score_complete",
  "operator_correction",
  "series_cancelled",
  "tournament_cancelled",
]);
const operatorCategories = new Set<string>([
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
const operatorAuditEntityKinds = new Set<string>(["game_attempt", "series"]);
const operatorActorKinds = new Set<string>(["server", "operator"]);

const isOperatorNonBlank = (value: unknown): value is string =>
  isString(value) && value.trim().length > 0;

const isOperatorOptionalDateTime = (value: unknown): value is string | null | undefined =>
  value === undefined || value === null || isDateTimeString(value);

const isOperatorUUIDOrNull = (value: unknown): value is string | null =>
  value === null || isNonNilUUID(value);

const isOperatorCategory = (value: unknown): boolean =>
  isString(value) && operatorCategories.has(value);

const isSafeNonNegativeInteger = (value: unknown): value is number =>
  isSafeInteger(value) && value >= 0;

const isSwissPairingEvidence = (value: unknown): value is SwissPairingEvidence =>
  isRecord(value) &&
  hasExactKeys(value, [
    "algorithm_version",
    "decided_at",
    "id",
    "normalized_inputs",
    "owner_id",
    "purpose",
    "replay_digest",
    "result",
  ]) &&
  value.algorithm_version === "hmac-sha256-order-v1" &&
  isDateTimeString(value.decided_at) &&
  isNonNilUUID(value.id) &&
  Array.isArray(value.normalized_inputs) &&
  value.normalized_inputs.length > 0 &&
  value.normalized_inputs.every(isString) &&
  isNonNilUUID(value.owner_id) &&
  value.purpose === "pairing" &&
  isString(value.replay_digest) &&
  /^[0-9a-f]{64}$/.test(value.replay_digest) &&
  Array.isArray(value.result) &&
  value.result.length > 0 &&
  value.result.every(isString);

const isSwissPairing = (value: unknown): value is SwissPairing =>
  isRecord(value) &&
  hasExactRequiredKeys(
    value,
    ["evidence_id", "first_participant_id", "id", "round_id", "second_participant_id"],
    ["override_actor_id", "override_reason", "repeated"],
  ) &&
  isNonNilUUID(value.evidence_id) &&
  isNonNilUUID(value.first_participant_id) &&
  isNonNilUUID(value.id) &&
  (value.override_actor_id === undefined ||
    value.override_actor_id === null ||
    isNonNilUUID(value.override_actor_id)) &&
  (value.override_reason === undefined ||
    value.override_reason === null ||
    isString(value.override_reason)) &&
  (value.repeated === undefined || typeof value.repeated === "boolean") &&
  isNonNilUUID(value.round_id) &&
  isNonNilUUID(value.second_participant_id);

const isSwissBye = (value: unknown): value is SwissBye =>
  isRecord(value) &&
  hasExactKeys(value, [
    "evidence_id",
    "id",
    "participant_id",
    "points_awarded",
    "revision_id",
    "round_id",
  ]) &&
  isNonNilUUID(value.evidence_id) &&
  isNonNilUUID(value.id) &&
  isNonNilUUID(value.participant_id) &&
  value.points_awarded === 1 &&
  isNonNilUUID(value.revision_id) &&
  isNonNilUUID(value.round_id);

const isSwissStanding = (value: unknown): value is SwissStanding =>
  isRecord(value) &&
  hasExactRequiredKeys(
    value,
    [
      "buchholz",
      "buchholz_status",
      "effective_time_ms",
      "head_to_head_applied",
      "head_to_head_points",
      "participant_id",
      "points",
      "points_label",
      "position",
      "stable_seed",
    ],
    ["accepted_solve_time_ms"],
  ) &&
  (value.accepted_solve_time_ms === undefined ||
    value.accepted_solve_time_ms === null ||
    isSafeNonNegativeInteger(value.accepted_solve_time_ms)) &&
  isNonNegativeInt32(value.buchholz) &&
  (value.buchholz_status === "provisional" || value.buchholz_status === "final") &&
  isSafeNonNegativeInteger(value.effective_time_ms) &&
  typeof value.head_to_head_applied === "boolean" &&
  isNonNegativeInt32(value.head_to_head_points) &&
  isNonNilUUID(value.participant_id) &&
  isNonNegativeInt32(value.points) &&
  (value.points_label === "provisional" || value.points_label === "final") &&
  isSafeInteger(value.position) &&
  value.position >= 1 &&
  value.position <= 16 &&
  isSafeInteger(value.stable_seed) &&
  value.stable_seed >= 1 &&
  value.stable_seed <= 16;

export const isSwissRound = (value: unknown): value is SwissRound =>
  isRecord(value) &&
  hasExactRequiredKeys(
    value,
    [
      "created_at",
      "id",
      "locked",
      "pairings",
      "revision",
      "roster_participant_ids",
      "round_number",
      "standings",
      "tournament_id",
      "updated_at",
    ],
    ["bye", "completed_at", "locked_at", "pairing_evidence", "started_at"],
  ) &&
  (value.bye === undefined || value.bye === null || isSwissBye(value.bye)) &&
  (value.completed_at === undefined ||
    value.completed_at === null ||
    isDateTimeString(value.completed_at)) &&
  isDateTimeString(value.created_at) &&
  isNonNilUUID(value.id) &&
  typeof value.locked === "boolean" &&
  (value.locked_at === undefined || value.locked_at === null || isDateTimeString(value.locked_at)) &&
  (value.pairing_evidence === undefined || isSwissPairingEvidence(value.pairing_evidence)) &&
  Array.isArray(value.pairings) &&
  value.pairings.length >= 1 &&
  value.pairings.length <= 8 &&
  value.pairings.every(isSwissPairing) &&
  isSafePositiveInteger(value.revision) &&
  Array.isArray(value.roster_participant_ids) &&
  value.roster_participant_ids.length >= 4 &&
  value.roster_participant_ids.length <= 16 &&
  value.roster_participant_ids.every(isNonNilUUID) &&
  new Set(value.roster_participant_ids).size === value.roster_participant_ids.length &&
  isSafeInteger(value.round_number) &&
  value.round_number >= 1 &&
  value.round_number <= 4 &&
  Array.isArray(value.standings) &&
  value.standings.length >= 4 &&
  value.standings.length <= 16 &&
  value.standings.every(isSwissStanding) &&
  (value.started_at === undefined || value.started_at === null || isDateTimeString(value.started_at)) &&
  isNonNilUUID(value.tournament_id) &&
  isDateTimeString(value.updated_at);

const isOperatorSeriesScore = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["first_participant_wins", "second_participant_wins"]) &&
  isNonNegativeInteger(value.first_participant_wins) &&
  value.first_participant_wins <= 2 &&
  isNonNegativeInteger(value.second_participant_wins) &&
  value.second_participant_wins <= 2;

const isOperatorGame = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, [
    "attempt_no",
    "id",
    "result_reason",
    "result_revision_id",
    "slot_id",
    "state",
    "winner_id",
  ]) &&
  isSafePositiveInteger(value.attempt_no) &&
  isNonNilUUID(value.id) &&
  (value.result_reason === null ||
    (isString(value.result_reason) && operatorGameResultReasons.has(value.result_reason))) &&
  isOperatorUUIDOrNull(value.result_revision_id) &&
  isNonNilUUID(value.slot_id) &&
  isString(value.state) &&
  operatorGameStates.has(value.state) &&
  isOperatorUUIDOrNull(value.winner_id);

const isOperatorGameSlot = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["attempts", "category", "id", "position", "score_before", "series_id"]) &&
  Array.isArray(value.attempts) &&
  value.attempts.every(isOperatorGame) &&
  isOperatorCategory(value.category) &&
  isNonNilUUID(value.id) &&
  isSafePositiveInteger(value.position) &&
  value.position <= 3 &&
  isOperatorSeriesScore(value.score_before) &&
  isNonNilUUID(value.series_id);

const isOperatorSeries = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, [
    "current_result_revision_id",
    "current_score_revision_id",
    "first_participant_id",
    "format",
    "id",
    "score",
    "second_participant_id",
    "slots",
    "state",
    "tournament_id",
    "winner_id",
  ]) &&
  isOperatorUUIDOrNull(value.current_result_revision_id) &&
  isOperatorUUIDOrNull(value.current_score_revision_id) &&
  isNonNilUUID(value.first_participant_id) &&
  (value.format === "bo1" || value.format === "bo3") &&
  isNonNilUUID(value.id) &&
  isOperatorSeriesScore(value.score) &&
  isNonNilUUID(value.second_participant_id) &&
  value.first_participant_id !== value.second_participant_id &&
  Array.isArray(value.slots) &&
  value.slots.length <= 3 &&
  value.slots.every(isOperatorGameSlot) &&
  isString(value.state) &&
  operatorSeriesStates.has(value.state) &&
  isNonNilUUID(value.tournament_id) &&
  isOperatorUUIDOrNull(value.winner_id);

const isOperatorWaveMember = (value: unknown): boolean =>
  isRecord(value) &&
  hasOnlyParticipantKeys(value, ["participant_id", "readiness_revision", "ready", "series_id"]) &&
  isNonNilUUID(value.participant_id) &&
  isSafePositiveInteger(value.readiness_revision) &&
  typeof value.ready === "boolean" &&
  (value.series_id === undefined || isOperatorUUIDOrNull(value.series_id));

const isOperatorReadyWindow = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["consumed_at", "deadline", "id", "opened_at", "revision_id", "state", "wave_id"]) &&
  isOperatorOptionalDateTime(value.consumed_at) &&
  isDateTimeString(value.deadline) &&
  isNonNilUUID(value.id) &&
  isDateTimeString(value.opened_at) &&
  isNonNilUUID(value.revision_id) &&
  isString(value.state) &&
  new Set(["open", "consumed", "expired", "superseded"]).has(value.state) &&
  isNonNilUUID(value.wave_id);

export const isOperatorWave = (
  value: unknown,
): value is components["schemas"]["Wave"] =>
  isRecord(value) &&
  hasExactKeys(value, [
    "id",
    "members",
    "paused_at",
    "ready_window",
    "revision",
    "revision_id",
    "started_at",
    "state",
    "tournament_id",
  ]) &&
  isNonNilUUID(value.id) &&
  Array.isArray(value.members) &&
  value.members.length >= 2 &&
  value.members.every(isOperatorWaveMember) &&
  isOperatorOptionalDateTime(value.paused_at) &&
  (value.ready_window === null || isOperatorReadyWindow(value.ready_window)) &&
  isSafePositiveInteger(value.revision) &&
  isNonNilUUID(value.revision_id) &&
  isOperatorOptionalDateTime(value.started_at) &&
  isString(value.state) &&
  operatorWaveStates.has(value.state) &&
  isNonNilUUID(value.tournament_id);

const isOperatorParticipant = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, [
    "attendance",
    "created_at",
    "id",
    "player_id",
    "roster_id",
    "seed",
    "tournament_id",
    "updated_at",
  ]) &&
  new Set(["invited", "registered", "checked_in", "withdrawn"]).has(String(value.attendance)) &&
  isDateTimeString(value.created_at) &&
  isNonNilUUID(value.id) &&
  isNonNilUUID(value.player_id) &&
  isNonNilUUID(value.roster_id) &&
  isSafePositiveInteger(value.seed) &&
  value.seed <= 16 &&
  isNonNilUUID(value.tournament_id) &&
  isDateTimeString(value.updated_at);

const isOperatorTournament = (value: unknown): value is Tournament =>
  isRecord(value) &&
  hasOnlyParticipantKeys(value, [
    "content_revision",
    "created_at",
    "finished_at",
    "id",
    "name",
    "paused_from_state",
    "planned_roster_size",
    "preset",
    "public_id",
    "revision",
    "roster_id",
    "roster_size",
    "started_at",
    "state",
    "updated_at",
  ]) &&
  [
    "content_revision",
    "created_at",
    "id",
    "name",
    "planned_roster_size",
    "preset",
    "public_id",
    "revision",
    "roster_id",
    "roster_size",
    "state",
    "updated_at",
  ].every((key) => key in value) &&
  isSafePositiveInteger(value.content_revision) &&
  isDateTimeString(value.created_at) &&
  isOperatorOptionalDateTime(value.finished_at) &&
  isNonNilUUID(value.id) &&
  isOperatorNonBlank(value.name) &&
  value.name.length <= 120 &&
  (value.paused_from_state === undefined ||
    value.paused_from_state === null ||
    (isString(value.paused_from_state) && operatorTournamentStates.has(value.paused_from_state))) &&
  isSafePositiveInteger(value.planned_roster_size) &&
  value.planned_roster_size >= 4 &&
  value.planned_roster_size <= 16 &&
  value.preset === "tournament_v1" &&
  isString(value.public_id) &&
  /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(value.public_id) &&
  value.public_id.length <= 64 &&
  isSafePositiveInteger(value.revision) &&
  isNonNilUUID(value.roster_id) &&
  isNonNegativeInteger(value.roster_size) &&
  value.roster_size <= 16 &&
  isOperatorOptionalDateTime(value.started_at) &&
  isString(value.state) &&
  operatorTournamentStates.has(value.state) &&
  isDateTimeString(value.updated_at);

export const isTournamentListResponse = (value: unknown): value is TournamentListResponse =>
  isRecord(value) &&
  hasExactKeys(value, ["items", "next_cursor"]) &&
  Array.isArray(value.items) &&
  value.items.length <= 200 &&
  value.items.every(isOperatorTournament) &&
  (value.next_cursor === null || isOperatorNonBlank(value.next_cursor));

export const isTournamentResponse = (value: unknown): value is Tournament =>
  isOperatorTournament(value);

export const isRoster = (value: unknown): value is Roster =>
  isRecord(value) &&
  hasOnlyParticipantKeys(value, [
    "created_at",
    "execution_started",
    "execution_started_at",
    "id",
    "locked",
    "locked_at",
    "participants",
    "revision",
    "tournament_id",
    "updated_at",
  ]) &&
  [
    "created_at",
    "execution_started",
    "id",
    "locked",
    "participants",
    "revision",
    "tournament_id",
    "updated_at",
  ].every((key) => key in value) &&
  isDateTimeString(value.created_at) &&
  typeof value.execution_started === "boolean" &&
  isOperatorOptionalDateTime(value.execution_started_at) &&
  isNonNilUUID(value.id) &&
  typeof value.locked === "boolean" &&
  isOperatorOptionalDateTime(value.locked_at) &&
  Array.isArray(value.participants) &&
  value.participants.length <= 16 &&
  value.participants.every(isOperatorParticipant) &&
  isSafePositiveInteger(value.revision) &&
  isNonNilUUID(value.tournament_id) &&
  isDateTimeString(value.updated_at);

const operatorPreflightCodes = new Set<string>([
  "tournament.preflight.structure.roster_complete",
  "tournament.preflight.structure.attendance",
  "tournament.preflight.structure.participant_exclusive",
  "tournament.preflight.structure.preset",
  "tournament.preflight.structure.categories",
  "tournament.preflight.structure.pairings",
  "tournament.preflight.structure.byes",
  "tournament.preflight.structure.overrides",
  "tournament.preflight.tasks.pool_configuration",
  "tournament.preflight.tasks.inventory",
  "tournament.preflight.tasks.missing",
  "tournament.preflight.tasks.disabled",
  "tournament.preflight.tasks.unhealthy",
  "tournament.preflight.tasks.mutable",
  "tournament.preflight.tasks.publicly_exposed",
  "tournament.preflight.tasks.wrong_pool",
  "tournament.preflight.runtime.configuration",
  "tournament.preflight.runtime.authoritative_storage",
  "tournament.preflight.runtime.submission",
  "tournament.preflight.runtime.task_delivery",
  "tournament.preflight.runtime.realtime",
  "tournament.preflight.runtime.capacity",
  "tournament.preflight.runtime.clock",
  "tournament.preflight.runtime.dependencies",
  "tournament.preflight.runtime.schedule",
]);

const isOperatorPreflightCheck = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["code", "evidence", "explanation", "passed"]) &&
  isString(value.code) &&
  operatorPreflightCodes.has(value.code) &&
  Array.isArray(value.evidence) &&
  value.evidence.every(isString) &&
  isString(value.explanation) &&
  typeof value.passed === "boolean";

const isOperatorPreflightSourceRevision = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["source", "value"]) &&
  isOperatorNonBlank(value.source) &&
  isOperatorNonBlank(value.value);

export const isPreflightReport = (value: unknown): value is PreflightReport =>
  isRecord(value) &&
  hasExactKeys(value, [
    "algorithm_version",
    "checks",
    "evaluated_at",
    "id",
    "normalized_inputs",
    "passed",
    "proof_hash",
    "revisions",
    "tournament_id",
  ]) &&
  (value.algorithm_version === "tournament-preflight-report-v1" ||
    value.algorithm_version === "tournament-preflight-report-v2") &&
  Array.isArray(value.checks) &&
  value.checks.length > 0 &&
  value.checks.every(isOperatorPreflightCheck) &&
  isDateTimeString(value.evaluated_at) &&
  isNonNilUUID(value.id) &&
  Array.isArray(value.normalized_inputs) &&
  value.normalized_inputs.every(isString) &&
  typeof value.passed === "boolean" &&
  isString(value.proof_hash) &&
  /^[0-9a-f]{64}$/.test(value.proof_hash) &&
  Array.isArray(value.revisions) &&
  value.revisions.length > 0 &&
  value.revisions.every(isOperatorPreflightSourceRevision) &&
  isNonNilUUID(value.tournament_id);

const isOperatorDraftAction = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["action", "actor_id", "category", "occurred_at", "turn", "turn_deadline"]) &&
  (value.action === "ban" || value.action === "pick") &&
  isNonNilUUID(value.actor_id) &&
  isOperatorCategory(value.category) &&
  isDateTimeString(value.occurred_at) &&
  isSafePositiveInteger(value.turn) &&
  isDateTimeString(value.turn_deadline);

const isOperatorDraft = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, [
    "actions",
    "first_participant_id",
    "format",
    "id",
    "pool",
    "revision",
    "second_participant_id",
    "selected_categories",
    "series_id",
    "state",
    "turn",
    "turn_deadline",
  ]) &&
  Array.isArray(value.actions) &&
  value.actions.every(isOperatorDraftAction) &&
  isNonNilUUID(value.first_participant_id) &&
  (value.format === "bo1" || value.format === "bo3") &&
  isNonNilUUID(value.id) &&
  Array.isArray(value.pool) &&
  value.pool.every(isOperatorCategory) &&
  isSafePositiveInteger(value.revision) &&
  isNonNilUUID(value.second_participant_id) &&
  Array.isArray(value.selected_categories) &&
  value.selected_categories.every(isOperatorCategory) &&
  isNonNilUUID(value.series_id) &&
  (value.state === "active" || value.state === "completed") &&
  isSafePositiveInteger(value.turn) &&
  isOperatorOptionalDateTime(value.turn_deadline);

const isOperatorPauseGame = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["deadline", "game", "resume_state", "revision", "series_id"]) &&
  isOperatorOptionalDateTime(value.deadline) &&
  isOperatorGame(value.game) &&
  (value.resume_state === null ||
    (isString(value.resume_state) && operatorGameStates.has(value.resume_state))) &&
  isNonNegativeInteger(value.revision) &&
  isNonNilUUID(value.series_id);

const isOperatorPauseSeries = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["current_game_id", "resume_state", "revision", "series"]) &&
  isOperatorUUIDOrNull(value.current_game_id) &&
  (value.resume_state === null ||
    (isString(value.resume_state) && operatorSeriesStates.has(value.resume_state))) &&
  isNonNegativeInteger(value.revision) &&
  isOperatorSeries(value.series);

const isOperatorFrozenDeadline = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, [
    "frozen_at",
    "kind",
    "original_deadline",
    "owner_id",
    "remaining_ms",
    "resumed_at",
    "resumed_deadline",
    "revision",
  ]) &&
  isDateTimeString(value.frozen_at) &&
  new Set(["ready_window", "game", "draft"]).has(String(value.kind)) &&
  isDateTimeString(value.original_deadline) &&
  isNonNilUUID(value.owner_id) &&
  isNonNegativeInteger(value.remaining_ms) &&
  isOperatorOptionalDateTime(value.resumed_at) &&
  isOperatorOptionalDateTime(value.resumed_deadline) &&
  isNonNegativeInteger(value.revision);

const isOperatorPresence = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, [
    "connected_at",
    "disconnected_at",
    "id",
    "participant_id",
    "presence_epoch",
    "revision",
    "roster_id",
    "series_id",
    "state",
    "tournament_id",
    "updated_at",
  ]) &&
  isDateTimeString(value.connected_at) &&
  isOperatorOptionalDateTime(value.disconnected_at) &&
  isNonNilUUID(value.id) &&
  isNonNilUUID(value.participant_id) &&
  isNonNegativeInteger(value.presence_epoch) &&
  isNonNegativeInteger(value.revision) &&
  isNonNilUUID(value.roster_id) &&
  isNonNilUUID(value.series_id) &&
  (value.state === "connected" || value.state === "disconnected") &&
  isNonNilUUID(value.tournament_id) &&
  isDateTimeString(value.updated_at);

const isOperatorReconnectInterval = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, [
    "closed_at",
    "continuation_number",
    "continued_from_id",
    "deadline",
    "game_id",
    "id",
    "number",
    "opened_at",
    "participant_id",
    "pause_id",
    "presence_epoch",
    "revision",
    "roster_id",
    "series_id",
    "state",
    "suspended_by_pause_id",
    "updated_at",
  ]) &&
  isOperatorOptionalDateTime(value.closed_at) &&
  isNonNegativeInteger(value.continuation_number) &&
  isOperatorUUIDOrNull(value.continued_from_id) &&
  isDateTimeString(value.deadline) &&
  isNonNilUUID(value.game_id) &&
  isNonNilUUID(value.id) &&
  isNonNegativeInteger(value.number) &&
  isDateTimeString(value.opened_at) &&
  isNonNilUUID(value.participant_id) &&
  isNonNilUUID(value.pause_id) &&
  isNonNegativeInteger(value.presence_epoch) &&
  isNonNegativeInteger(value.revision) &&
  isNonNilUUID(value.roster_id) &&
  isNonNilUUID(value.series_id) &&
  new Set(["open", "reconnected", "expired", "cancelled"]).has(String(value.state)) &&
  isOperatorUUIDOrNull(value.suspended_by_pause_id) &&
  isDateTimeString(value.updated_at);

const isOperatorPauseReconnectCounter = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["limit", "participant_id", "pause_id", "revision", "roster_id", "used"]) &&
  isNonNegativeInteger(value.limit) &&
  isNonNilUUID(value.participant_id) &&
  isNonNilUUID(value.pause_id) &&
  isNonNegativeInteger(value.revision) &&
  isNonNilUUID(value.roster_id) &&
  isNonNegativeInteger(value.used);

const isOperatorPauseGraph = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, [
    "active_pause_id",
    "counters",
    "deadlines_suppressed",
    "draft",
    "frozen_deadlines",
    "games",
    "graph_revision",
    "paused_at",
    "presence",
    "reconnect",
    "roster_id",
    "series",
    "terminal_action_revision",
    "tournament_id",
    "wave",
  ]) &&
  isOperatorUUIDOrNull(value.active_pause_id) &&
  Array.isArray(value.counters) &&
  value.counters.every(isOperatorPauseReconnectCounter) &&
  typeof value.deadlines_suppressed === "boolean" &&
  (value.draft === null || isOperatorDraft(value.draft)) &&
  Array.isArray(value.frozen_deadlines) &&
  value.frozen_deadlines.every(isOperatorFrozenDeadline) &&
  Array.isArray(value.games) &&
  value.games.every(isOperatorPauseGame) &&
  isNonNegativeInteger(value.graph_revision) &&
  isOperatorOptionalDateTime(value.paused_at) &&
  Array.isArray(value.presence) &&
  value.presence.every(isOperatorPresence) &&
  Array.isArray(value.reconnect) &&
  value.reconnect.every(isOperatorReconnectInterval) &&
  isNonNilUUID(value.roster_id) &&
  Array.isArray(value.series) &&
  value.series.every(isOperatorPauseSeries) &&
  isNonNegativeInteger(value.terminal_action_revision) &&
  isNonNilUUID(value.tournament_id) &&
  isOperatorWave(value.wave);

export const isOperatorRecoveryCursor = (
  value: unknown,
): value is OperatorRecoveryCursor =>
  isRecord(value) &&
  hasExactKeys(value, ["audit_sequence", "authority_revision", "projection_revision"]) &&
  isNonNegativeInteger(value.audit_sequence) &&
  isSafePositiveInteger(value.authority_revision) &&
  isSafePositiveInteger(value.projection_revision);

export const isOperatorRecoverySnapshot = (
  value: unknown,
): value is OperatorRecoverySnapshot =>
  isRecord(value) &&
  hasExactKeys(value, ["next_cursor", "pause_graph", "roster", "series", "tournament", "waves"]) &&
  isOperatorRecoveryCursor(value.next_cursor) &&
  (value.pause_graph === null || isOperatorPauseGraph(value.pause_graph)) &&
  isRoster(value.roster) &&
  Array.isArray(value.series) &&
  value.series.every(isOperatorSeries) &&
  isOperatorTournament(value.tournament) &&
  Array.isArray(value.waves) &&
  value.waves.every(isOperatorWave);

export const isAuditCursor = (value: unknown): value is AuditCursor =>
  isRecord(value) &&
  hasExactKeys(value, ["audit_event_id", "occurred_at", "revision_id", "snapshot_bound"]) &&
  isNonNilUUID(value.audit_event_id) &&
  isDateTimeString(value.occurred_at) &&
  isNonNilUUID(value.revision_id) &&
  isOperatorNonBlank(value.snapshot_bound);

const operatorRedactedPayloadKeys = new Set([
  "attempt_id",
  "entity_id",
  "entity_kind",
  "previous_revision_id",
  "projection_revision_id",
  "reason",
  "result_reason",
  "revision_number",
  "series_id",
  "source_projection_revision_id",
  "state",
  "tournament_id",
  "winner_id",
]);
const operatorRedactedUUIDKeys = new Set([
  "attempt_id",
  "entity_id",
  "previous_revision_id",
  "projection_revision_id",
  "series_id",
  "source_projection_revision_id",
  "tournament_id",
  "winner_id",
]);
const operatorRedactedStringKeys = new Set([
  "entity_kind",
  "reason",
  "result_reason",
  "state",
]);

const isOperatorRedactedPayload = (value: unknown): value is OperatorAuditRedactedPayload =>
  isRecord(value) &&
  Object.entries(value).every(([key, child]) => {
    if (!operatorRedactedPayloadKeys.has(key)) {
      return false;
    }
    if (operatorRedactedUUIDKeys.has(key)) {
      return isNonNilUUID(child);
    }
    if (operatorRedactedStringKeys.has(key)) {
      return isOperatorNonBlank(child) && child.length <= 512;
    }
    return key === "revision_number" && isSafePositiveInteger(child);
  });

export const isAuditEvent = (value: unknown): value is AuditEvent =>
  isRecord(value) &&
  hasExactKeys(value, [
    "actor_id",
    "actor_kind",
    "audit_event_id",
    "created_at",
    "entity_id",
    "entity_kind",
    "event_type",
    "is_current",
    "is_superseded",
    "occurred_at",
    "official_result_revision_id",
    "redacted_payload",
    "result_event_id",
    "result_reason",
    "result_state",
    "revision_number",
    "roster_id",
    "series_id",
    "tournament_id",
    "winner_id",
  ]) &&
  isOperatorUUIDOrNull(value.actor_id) &&
  isString(value.actor_kind) &&
  operatorActorKinds.has(value.actor_kind) &&
  isNonNilUUID(value.audit_event_id) &&
  isDateTimeString(value.created_at) &&
  isNonNilUUID(value.entity_id) &&
  isString(value.entity_kind) &&
  operatorAuditEntityKinds.has(value.entity_kind) &&
  isOperatorNonBlank(value.event_type) &&
  typeof value.is_current === "boolean" &&
  typeof value.is_superseded === "boolean" &&
  isDateTimeString(value.occurred_at) &&
  isNonNilUUID(value.official_result_revision_id) &&
  isOperatorRedactedPayload(value.redacted_payload) &&
  isNonNilUUID(value.result_event_id) &&
  isOperatorNonBlank(value.result_reason) &&
  isOperatorNonBlank(value.result_state) &&
  isSafePositiveInteger(value.revision_number) &&
  isNonNilUUID(value.roster_id) &&
  isNonNilUUID(value.series_id) &&
  isNonNilUUID(value.tournament_id) &&
  isOperatorUUIDOrNull(value.winner_id);

export const isAuditPage = (value: unknown): value is AuditPage =>
  isRecord(value) &&
  hasExactKeys(value, ["events", "next_cursor"]) &&
  Array.isArray(value.events) &&
  value.events.length <= 200 &&
  value.events.every(isAuditEvent) &&
  (value.next_cursor === null || isAuditCursor(value.next_cursor));

const HEX_64 = /^[0-9a-f]{64}$/;
const BASE64 = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;
const INCIDENT_KEY_ID = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;

export const isIncidentBundle = (value: unknown): value is IncidentBundle =>
  isRecord(value) &&
  hasExactKeys(value, [
    "algorithm",
    "canonical_content",
    "canonical_content_encoding",
    "canonical_content_length",
    "canonical_content_type",
    "generated_at",
    "key_id",
    "mac",
    "projection_revision",
    "sha256",
    "tournament_id",
  ]) &&
  value.algorithm === "hmac-sha256-v1" &&
  isString(value.canonical_content) &&
  value.canonical_content.length > 0 &&
  BASE64.test(value.canonical_content) &&
  value.canonical_content_encoding === "base64" &&
  isSafePositiveInteger(value.canonical_content_length) &&
  value.canonical_content_type === "application/json" &&
  isDateTimeString(value.generated_at) &&
  isString(value.key_id) &&
  INCIDENT_KEY_ID.test(value.key_id) &&
  value.key_id.length <= 64 &&
  HEX_64.test(String(value.mac)) &&
  isSafePositiveInteger(value.projection_revision) &&
  HEX_64.test(String(value.sha256)) &&
  isNonNilUUID(value.tournament_id);

type GoldenRuntimeMember = components["schemas"]["GoldenRuntimeMember"];
type GoldenRuntimeState = components["schemas"]["GoldenRuntimeState"];
type GoldenOperatorGroup = components["schemas"]["GoldenOperatorGroup"];
type GoldenOperatorResponse = components["schemas"]["GoldenOperatorResponse"];
type GoldenRuntimeTask = components["schemas"]["GoldenRuntimeTask"];
type GoldenParticipantResponse = components["schemas"]["GoldenParticipantResponse"];
export type GoldenRuntimeConflictProblem = components["schemas"]["GoldenRuntimeConflictProblem"];

const GOLDEN_RUNTIME_STATES = new Set<GoldenRuntimeState>([
  "prepared",
  "ready",
  "active",
  "technical_pause",
  "completed",
  "cancelled",
  "superseded",
]);

const hasExactRequiredKeys = (
  value: Record<string, unknown>,
  requiredKeys: readonly string[],
  optionalKeys: readonly string[] = [],
): boolean => {
  const keys = Object.keys(value);
  const allowedKeys = [...requiredKeys, ...optionalKeys];
  return requiredKeys.every((key) => keys.includes(key)) &&
    keys.every((key) => allowedKeys.includes(key));
};

export const isGoldenRuntimeState = (value: unknown): value is GoldenRuntimeState =>
  isString(value) && GOLDEN_RUNTIME_STATES.has(value as GoldenRuntimeState);

const isGoldenPosition = (value: unknown): value is number | null =>
  value === null || (isSafeInteger(value) && value >= 1 && value <= 16);

export const isGoldenRuntimeMember = (value: unknown): value is GoldenRuntimeMember =>
  isRecord(value) &&
  hasExactKeys(value, ["participant_id", "ready", "submitted", "position"]) &&
  isNonNilUUID(value.participant_id) &&
  typeof value.ready === "boolean" &&
  typeof value.submitted === "boolean" &&
  isGoldenPosition(value.position);

const isGoldenOperatorGroupValue = (value: unknown): value is GoldenOperatorGroup =>
  isRecord(value) &&
  hasExactKeys(value, [
    "attempt_id",
    "deadline",
    "group_id",
    "group_revision_id",
    "members",
    "position_from",
    "position_to",
    "ready_window_id",
    "runtime_revision",
    "started_at",
    "state",
  ]) &&
  isNonNilUUID(value.attempt_id) &&
  isParticipantDateOrNull(value.deadline) &&
  isNonNilUUID(value.group_id) &&
  isNonNilUUID(value.group_revision_id) &&
  Array.isArray(value.members) &&
  value.members.length >= 2 &&
  value.members.length <= 16 &&
  value.members.every(isGoldenRuntimeMember) &&
  new Set(value.members.map((member) => member.participant_id)).size === value.members.length &&
  isSafeInteger(value.position_from) &&
  value.position_from >= 1 &&
  value.position_from <= 16 &&
  isSafeInteger(value.position_to) &&
  value.position_to >= value.position_from &&
  value.position_to <= 16 &&
  isNonNilUUID(value.ready_window_id) &&
  isSafePositiveInteger(value.runtime_revision) &&
  isParticipantDateOrNull(value.started_at) &&
  isGoldenRuntimeState(value.state);

export const isGoldenOperatorGroup = (value: unknown): value is GoldenOperatorGroup =>
  isGoldenOperatorGroupValue(value);

export const isGoldenOperatorResponse = (
  value: unknown,
): value is GoldenOperatorResponse =>
  isRecord(value) &&
  hasExactKeys(value, ["groups", "observed_at", "tournament_id"]) &&
  Array.isArray(value.groups) &&
  value.groups.every(isGoldenOperatorGroupValue) &&
  isDateTimeString(value.observed_at) &&
  isNonNilUUID(value.tournament_id);

export const isGoldenRuntimeTask = (value: unknown): value is GoldenRuntimeTask =>
  isRecord(value) &&
  hasExactRequiredKeys(
    value,
    [
      "assignment_id",
      "category",
      "description",
      "difficulty",
      "snapshot_id",
      "source_file_available",
      "task_id",
      "time_limit_seconds",
      "title",
      "version",
    ],
    ["task_url"],
  ) &&
  isNonNilUUID(value.assignment_id) &&
  isString(value.category) &&
  value.category.trim().length > 0 &&
  isString(value.description) &&
  value.description.trim().length > 0 &&
  isString(value.difficulty) &&
  value.difficulty.trim().length > 0 &&
  isNonNilUUID(value.snapshot_id) &&
  typeof value.source_file_available === "boolean" &&
  isNonNilUUID(value.task_id) &&
  isOptionalStringOrNull(value.task_url) &&
  (value.task_url === undefined || value.task_url === null || value.task_url.trim().length > 0) &&
  value.time_limit_seconds === 180 &&
  isString(value.title) &&
  value.title.trim().length > 0 &&
  isSafePositiveInteger(value.version) &&
  value.version <= INT32_MAX;

export const isGoldenParticipantResponse = (
  value: unknown,
): value is GoldenParticipantResponse => {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, [
      "attempt_id",
      "deadline",
      "group_id",
      "group_revision_id",
      "participant_id",
      "position",
      "ready",
      "ready_window_id",
      "runtime_revision",
      "started_at",
      "state",
      "submitted",
      "task",
      "tournament_id",
    ]) ||
    !isNonNilUUID(value.attempt_id) ||
    !isParticipantDateOrNull(value.deadline) ||
    !isNonNilUUID(value.group_id) ||
    !isNonNilUUID(value.group_revision_id) ||
    !isNonNilUUID(value.participant_id) ||
    !isGoldenPosition(value.position) ||
    typeof value.ready !== "boolean" ||
    !isNonNilUUID(value.ready_window_id) ||
    !isSafePositiveInteger(value.runtime_revision) ||
    !isParticipantDateOrNull(value.started_at) ||
    !isGoldenRuntimeState(value.state) ||
    typeof value.submitted !== "boolean" ||
    !(value.task === null || isGoldenRuntimeTask(value.task)) ||
    !isNonNilUUID(value.tournament_id)
  ) {
    return false;
  }

  if (value.task === null) {
    // The backend also keeps no-show or excluded members taskless after a
    // window has started. A null task must stay null; the client never fills
    // it from surrounding metadata.
    return true;
  }
  return value.started_at !== null && value.deadline !== null;
};

const isOptionalGoldenConflictUUID = (value: unknown): boolean =>
  value === undefined || value === null || isNonNilUUID(value);

export const isGoldenRuntimeConflictProblem = (
  value: unknown,
): value is GoldenRuntimeConflictProblem =>
  isRecord(value) &&
  hasExactRequiredKeys(
    value,
    ["status", "title", "type"],
    [
      "current_attempt_id",
      "current_ready_window_id",
      "current_runtime_revision",
      "detail",
      "expected_attempt_id",
      "expected_ready_window_id",
      "expected_runtime_revision",
      "instance",
      "request_id",
    ],
  ) &&
  value.status === 409 &&
  isString(value.title) &&
  isString(value.type) &&
  (value.detail === undefined || isString(value.detail)) &&
  (value.instance === undefined || isString(value.instance)) &&
  (value.request_id === undefined || isString(value.request_id)) &&
  isOptionalGoldenConflictUUID(value.current_attempt_id) &&
  isOptionalGoldenConflictUUID(value.current_ready_window_id) &&
  (value.current_runtime_revision === undefined || isNonNegativeInteger(value.current_runtime_revision)) &&
  isOptionalGoldenConflictUUID(value.expected_attempt_id) &&
  isOptionalGoldenConflictUUID(value.expected_ready_window_id) &&
  (value.expected_runtime_revision === undefined || isNonNegativeInteger(value.expected_runtime_revision));
