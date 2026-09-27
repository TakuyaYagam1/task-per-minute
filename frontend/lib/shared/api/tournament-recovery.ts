import {
  adminClient,
  ApiError,
  publicClient,
  unwrapApi,
  type ApiResult,
} from "./client";
import {
  ApiContractError,
  isGoldenOperatorGroup,
  isOperatorRecoverySnapshot,
  isParticipantRecoverySnapshot,
} from "./guards";
import { CONFIG } from "../config/app";
import type { components } from "./schema";

export type PublicRecoveryCursor = components["schemas"]["PublicRecoveryCursor"];
export type PublicRecoverySnapshot = components["schemas"]["PublicRecoverySnapshot"];
type PublicRecoveryCursorConflictProblem =
  components["schemas"]["PublicRecoveryCursorConflictProblem"];

class UnknownRecoverySchemaError extends ApiContractError {
  constructor(contract: string) {
    super(contract);
    this.name = "UnknownRecoverySchemaError";
  }
}

type PublicDisplay = {
  tournament: Record<string, unknown>;
  scoreboard: readonly Record<string, unknown>[];
  swissRounds: readonly Record<string, unknown>[];
  bracket: readonly Record<string, unknown>[];
  liveSeries: readonly Record<string, unknown>[];
  officialResults: readonly Record<string, unknown>[];
  liveDraft: Record<string, unknown> | null;
};

export type PublicRecoveryState = {
  tournamentId: string;
  cursor: PublicRecoveryCursor;
  /** Latest server-authoritative timestamp from the accepted public WS frame. */
  serverTimestamp?: string;
  resumeId: string | null;
  seenEventIds: readonly string[];
  display: PublicDisplay;
};

export type PublicRealtimeApplyResult = {
  state: PublicRecoveryState;
  outcome: "applied" | "duplicate" | "out_of_order" | "wrong_tournament";
};

export type PublicRealtimeEnvelope = {
  schema_version: 1;
  tournament_id: string;
  sequence: number;
  event_id: string;
  occurred_at: string;
  projection_revision: number;
  resume_id?: string;
  public: {
    revision: number;
    last_sequence: number;
    tournament: Record<string, unknown>;
    scoreboard: readonly Record<string, unknown>[];
    swiss_rounds: readonly Record<string, unknown>[];
    bracket: readonly Record<string, unknown>[];
    live_series: readonly Record<string, unknown>[];
    official_results: readonly Record<string, unknown>[];
    draft?: Record<string, unknown>;
  };
};

export type PublicRealtimeMessage = Readonly<{
  type: "tournament.public";
  payload: Readonly<{ envelope: PublicRealtimeEnvelope }>;
}>;

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const PRIVATE_KEYS = new Set([
  "assignment",
  "assignment_id",
  "audit",
  "audit_links",
  "flag",
  "first_participant_id",
  "participant_id",
  "participant_ids",
  "player_id",
  "second_participant_id",
  "snapshot_id",
  "source_projection_revision_id",
  "task",
  "task_id",
  "task_snapshot",
  "winner_id",
]);
const TOURNAMENT_STATES = new Set([
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
const SERIES_STATES = new Set([
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
const GAME_STATES = new Set([
  "planned",
  "ready",
  "active",
  "paused",
  "completed",
  "void",
  "cancelled",
  "superseded",
]);
const DRAFT_STATES = new Set([
  "active",
  "paused",
  "recovery_required",
  "completed",
  "superseded",
]);
const WAVE_STATES = new Set([
  "planned",
  "ready_window_open",
  "ready",
  "active",
  "paused",
  "completed",
  "ready_window_expired",
  "superseded",
]);
const PUBLIC_SERIES_STAGES = new Set([
  "swiss",
  "golden",
  "semifinal",
  "final",
]);
const CATEGORIES = new Set([
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

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === "object" && !Array.isArray(value);

const hasOnlyKeys = (value: Record<string, unknown>, allowed: readonly string[]): boolean => {
  const allowedKeys = new Set(allowed);
  return Object.keys(value).every((key) => allowedKeys.has(key));
};

const hasNoPrivateKeys = (value: unknown): boolean => {
  if (Array.isArray(value)) {
    return value.every(hasNoPrivateKeys);
  }
  if (!isRecord(value)) {
    return true;
  }
  return Object.entries(value).every(
    ([key, child]) => !PRIVATE_KEYS.has(key.toLowerCase()) && hasNoPrivateKeys(child),
  );
};

const isUUID = (value: unknown): value is string =>
  typeof value === "string" && UUID_PATTERN.test(value);

const isInteger = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value);

const isNonNegativeInteger = (value: unknown): value is number =>
  isInteger(value) && value >= 0;

const isPositiveInteger = (value: unknown): value is number => isInteger(value) && value > 0;

const isDateTime = (value: unknown): value is string =>
  typeof value === "string" && value.endsWith("Z") && Number.isFinite(Date.parse(value));

const isServerTimestamp = (value: unknown): value is string =>
  typeof value === "string" && Number.isFinite(Date.parse(value));

const isOptionalDateTime = (value: unknown): boolean =>
  value === null || value === undefined || isDateTime(value);

const isNullableDateTime = (value: unknown): boolean =>
  value === null || isDateTime(value);

const isNonBlank = (value: unknown): value is string =>
  typeof value === "string" && value.trim().length > 0;

const isPublicDisplayName = (value: unknown): value is string =>
  isNonBlank(value) && value.trim().length <= 64;

const isPublicConnectionStatus = (value: unknown): boolean =>
  value === "connected" || value === "disconnected" || value === "unknown";

const isPublicGameResultReason = (state: unknown, reason: unknown): boolean => {
  if (typeof reason !== "string") {
    return false;
  }
  switch (state) {
    case "completed":
      return new Set(["solved", "surrender", "operator_forfeit"]).has(reason);
    case "void":
      return new Set(["no_solve", "task_failure", "common_platform_failure", "disconnect", "execution_epoch_break"]).has(reason);
    case "cancelled":
      return new Set(["no_show", "series_cancelled", "tournament_cancelled"]).has(reason);
    case "superseded":
      return reason === "derived_revision_superseded";
    default:
      return false;
  }
};

const isCursor = (value: unknown): value is PublicRecoveryCursor =>
  isRecord(value) &&
  hasOnlyKeys(value, ["projection_revision", "event_sequence"]) &&
  isPositiveInteger(value.projection_revision) &&
  isNonNegativeInteger(value.event_sequence);

const isPublicSeriesScore = (value: unknown): boolean =>
  isRecord(value) &&
  hasOnlyKeys(value, ["first_wins", "second_wins"]) &&
  isNonNegativeInteger(value.first_wins) &&
  value.first_wins <= 2 &&
  isNonNegativeInteger(value.second_wins) &&
  value.second_wins <= 2;

const isBracketScore = (value: unknown): boolean =>
  isRecord(value) &&
  hasOnlyKeys(value, ["first_participant_wins", "second_participant_wins"]) &&
  isNonNegativeInteger(value.first_participant_wins) &&
  value.first_participant_wins <= 2 &&
  isNonNegativeInteger(value.second_participant_wins) &&
  value.second_participant_wins <= 2;

const isPublicLiveGame = (value: unknown): value is Record<string, unknown> => {
  if (
    !isRecord(value) ||
    !hasOnlyKeys(value, [
      "position",
      "category",
      "state",
      "started_at",
      "effective_deadline",
      "finished_at",
      "solve_time_ms",
      "result_reason",
      "winner_display_name",
      "first_connection_status",
      "second_connection_status",
    ]) ||
    !isPositiveInteger(value.position) ||
    value.position > 3 ||
    typeof value.category !== "string" ||
    !CATEGORIES.has(value.category) ||
    typeof value.state !== "string" ||
    !GAME_STATES.has(value.state) ||
    !isNullableDateTime(value.started_at) ||
    !isNullableDateTime(value.effective_deadline) ||
    !isNullableDateTime(value.finished_at) ||
    !(value.solve_time_ms === undefined || value.solve_time_ms === null || isNonNegativeInteger(value.solve_time_ms)) ||
    (value.result_reason !== null && !isNonBlank(value.result_reason)) ||
    (value.winner_display_name !== null && !isPublicDisplayName(value.winner_display_name)) ||
    !isPublicConnectionStatus(value.first_connection_status) ||
    !isPublicConnectionStatus(value.second_connection_status)
  ) {
    return false;
  }

  const terminal = value.state === "completed" || value.state === "void" ||
    value.state === "cancelled" || value.state === "superseded";
  if (terminal) {
    if (
      value.finished_at === null ||
      value.effective_deadline !== null ||
      !isPublicGameResultReason(value.state, value.result_reason)
    ) {
      return false;
    }
    if (value.state === "completed" && value.winner_display_name === null) {
      return false;
    }
    if (value.state !== "completed" && value.winner_display_name !== null) {
      return false;
    }
    if (
      value.state === "completed" &&
      value.result_reason !== "solved" &&
      value.solve_time_ms !== null &&
      value.solve_time_ms !== undefined
    ) {
      return false;
    }
    if (value.state !== "completed" && value.solve_time_ms !== null && value.solve_time_ms !== undefined) {
      return false;
    }
    return true;
  }

  if (
    value.finished_at !== null ||
    value.result_reason !== null ||
    value.winner_display_name !== null ||
    (value.solve_time_ms !== null && value.solve_time_ms !== undefined)
  ) {
    return false;
  }
  if (value.state === "active") {
    return value.started_at !== null && value.effective_deadline !== null;
  }
  if (value.state === "paused") {
    return value.started_at !== null && value.effective_deadline === null;
  }
  return value.effective_deadline === null;
};

const isTournament = (value: unknown, includeProjection: boolean): value is Record<string, unknown> => {
  if (!isRecord(value)) {
    return false;
  }
  const allowed = ["tournament_id", "preset", "state", "roster_size", "started_at", "finished_at"];
  if (includeProjection) {
    allowed.push("projection_revision");
  }
  return (
    hasOnlyKeys(value, allowed) &&
    isUUID(value.tournament_id) &&
    value.preset === "tournament_v1" &&
    typeof value.state === "string" &&
    TOURNAMENT_STATES.has(value.state) &&
    isNonNegativeInteger(value.roster_size) &&
    "started_at" in value &&
    "finished_at" in value &&
    isOptionalDateTime(value.started_at) &&
    isOptionalDateTime(value.finished_at) &&
    (!includeProjection || isPositiveInteger(value.projection_revision))
  );
};

const isScoreboardEntry = (value: unknown): value is Record<string, unknown> =>
  isRecord(value) &&
  hasOnlyKeys(value, [
    "rank",
    "display_name",
    "points",
    "wins",
    "losses",
    "bye_count",
    "provisional_tie",
    "qualification_status",
    "buchholz",
    "effective_time_ms",
  ]) &&
  isPositiveInteger(value.rank) &&
  isNonBlank(value.display_name) &&
  isNonNegativeInteger(value.points) &&
  isNonNegativeInteger(value.wins) &&
  isNonNegativeInteger(value.losses) &&
  isNonNegativeInteger(value.bye_count) &&
  typeof value.provisional_tie === "boolean" &&
  typeof value.qualification_status === "string" &&
  (value.qualification_status === "pending" ||
    value.qualification_status === "qualified" ||
    value.qualification_status === "eliminated") &&
  isNonNegativeInteger(value.buchholz) &&
  isNonNegativeInteger(value.effective_time_ms);

const isPublicSwissBye = (value: unknown): value is Record<string, unknown> =>
  isRecord(value) &&
  hasOnlyKeys(value, ["display_name", "points_awarded"]) &&
  isPublicDisplayName(value.display_name) &&
  value.points_awarded === 1;

const isPublicSwissRounds = (value: unknown): value is Record<string, unknown>[] => {
  if (!Array.isArray(value) || value.length > 4) {
    return false;
  }
  let previousRound = 0;
  for (const item of value) {
    if (
      !isRecord(item) ||
      !hasOnlyKeys(item, ["round_number", "state", "bye"]) ||
      !isInteger(item.round_number) ||
      item.round_number < 1 ||
      item.round_number > 4 ||
      item.round_number <= previousRound ||
      typeof item.state !== "string" ||
      !WAVE_STATES.has(item.state) ||
      (item.bye !== null && !isPublicSwissBye(item.bye))
    ) {
      return false;
    }
    previousRound = item.round_number;
  }
  return true;
};

const isBracketMatch = (value: unknown, websocket: boolean): value is Record<string, unknown> => {
  if (
    !isRecord(value) ||
    !hasOnlyKeys(value, [
      "stage",
      "position",
      "format",
      "first_display_name",
      "second_display_name",
      "score",
      "state",
      "scheduled_at",
      "winner_display_name",
    ]) ||
    !isPositiveInteger(value.position) ||
    (value.stage !== "semifinal" && value.stage !== "final") ||
    (value.format !== "bo1" && value.format !== "bo3") ||
    (value.stage === "semifinal" && value.format !== "bo1") ||
    (value.stage === "final" && value.format !== "bo3") ||
    typeof value.state !== "string" ||
    !SERIES_STATES.has(value.state) ||
    !("first_display_name" in value) ||
    !("second_display_name" in value) ||
    !("winner_display_name" in value) ||
    !("scheduled_at" in value) ||
    !isOptionalDateTime(value.scheduled_at) ||
    (websocket ? !isPublicSeriesScore(value.score) : !isBracketScore(value.score))
  ) {
    return false;
  }
  const score = value.score as Record<string, unknown>;
  const firstWins = websocket ? score.first_wins : score.first_participant_wins;
  const secondWins = websocket ? score.second_wins : score.second_participant_wins;
  const maxWins = value.stage === "semifinal" ? 1 : 2;
  if (
    typeof firstWins !== "number" ||
    typeof secondWins !== "number" ||
    firstWins > maxWins ||
    secondWins > maxWins
  ) {
    return false;
  }
  const placeholder =
    value.stage === "final" &&
    value.state === "planned" &&
    value.first_display_name === null &&
    value.second_display_name === null &&
    value.winner_display_name === null &&
    firstWins === 0 &&
    secondWins === 0;
  if (placeholder) {
    return true;
  }
  if (!isPublicDisplayName(value.first_display_name) || !isPublicDisplayName(value.second_display_name)) {
    return false;
  }
  if (value.state !== "completed") {
    return value.winner_display_name === null;
  }
  if (!isPublicDisplayName(value.winner_display_name) || firstWins === secondWins) {
    return false;
  }
  return (
    (firstWins > secondWins && value.winner_display_name === value.first_display_name) ||
    (secondWins > firstWins && value.winner_display_name === value.second_display_name)
  );
};

const isPublicBracket = (value: unknown, websocket: boolean): value is Record<string, unknown>[] => {
  if (!isRecordArray(value, (item): item is Record<string, unknown> => isBracketMatch(item, websocket))) {
    return false;
  }
  if (value.length === 0) {
    return true;
  }
  if (value.length !== 3) {
    return false;
  }
  const positions = new Set<string>();
  for (const match of value) {
    if (
      (match.stage === "semifinal" && (match.position === 1 || match.position === 2)) ||
      (match.stage === "final" && match.position === 1)
    ) {
      positions.add(`${match.stage}:${String(match.position)}`);
      continue;
    }
    return false;
  }
  return positions.size === 3;
};

const isLiveSeries = (value: unknown): value is Record<string, unknown> =>
  isRecord(value) &&
  hasOnlyKeys(value, [
    "series_id",
    "format",
    "state",
    "first_display_name",
    "second_display_name",
    "score",
    "current_game",
    "current_game_position",
    "stage",
    "round_number",
    "scheduled_at",
  ]) &&
  isUUID(value.series_id) &&
  (value.format === "bo1" || value.format === "bo3") &&
  typeof value.state === "string" &&
  SERIES_STATES.has(value.state) &&
  isNonBlank(value.first_display_name) &&
  isNonBlank(value.second_display_name) &&
  isPublicSeriesScore(value.score) &&
  "current_game" in value &&
  (value.current_game === null || isPublicLiveGame(value.current_game)) &&
  typeof value.stage === "string" &&
  PUBLIC_SERIES_STAGES.has(value.stage) &&
  "round_number" in value &&
  (value.round_number === null || isPositiveInteger(value.round_number)) &&
  "scheduled_at" in value &&
  isOptionalDateTime(value.scheduled_at) &&
  (value.current_game_position === undefined ||
    (isNonNegativeInteger(value.current_game_position) && value.current_game_position <= 3)) &&
  (value.current_game === null ||
    value.current_game_position === undefined ||
    value.current_game_position === value.current_game.position);

const isOfficialResult = (value: unknown): value is Record<string, unknown> =>
  isRecord(value) &&
  hasOnlyKeys(value, ["revision_id", "series_id", "state", "winner_display_name", "score", "recorded_at"]) &&
  isUUID(value.revision_id) &&
  isUUID(value.series_id) &&
  typeof value.state === "string" &&
  SERIES_STATES.has(value.state) &&
  (value.winner_display_name === undefined || isNonBlank(value.winner_display_name)) &&
  isPublicSeriesScore(value.score) &&
  isDateTime(value.recorded_at);

const isDraft = (
  value: unknown,
  includeProjection: boolean,
): value is Record<string, unknown> => {
  if (!isRecord(value)) {
    return false;
  }
  const allowed = [
    "series_id",
    "format",
    "state",
    "first_actor_display_name",
    "current_turn",
    "current_action",
    "current_actor_display_name",
    "turn_deadline",
    "auto_action_pending",
    "pool",
    "selected_categories",
    "actions",
  ];
  if (includeProjection) {
    allowed.push("tournament_id", "projection_revision");
  }
  return (
    hasOnlyKeys(value, allowed) &&
    isUUID(value.series_id) &&
    (value.format === "bo1" || value.format === "bo3") &&
    typeof value.state === "string" &&
    DRAFT_STATES.has(value.state) &&
    isPublicDisplayName(value.first_actor_display_name) &&
    Array.isArray(value.pool) &&
    value.pool.every((item) => typeof item === "string" && CATEGORIES.has(item)) &&
    Array.isArray(value.selected_categories) &&
    value.selected_categories.every((item) => typeof item === "string" && CATEGORIES.has(item)) &&
    Array.isArray(value.actions) &&
    value.actions.every(isDraftAction) &&
    isDraftCardinality(value) &&
    (!includeProjection ||
      (isUUID(value.tournament_id) && isPositiveInteger(value.projection_revision)))
  );
};

const isDraftAction = (value: unknown): boolean =>
  isRecord(value) &&
  hasOnlyKeys(value, ["turn", "action", "category", "actor_display_name", "occurred_at", "automatic"]) &&
  isPositiveInteger(value.turn) &&
  (value.action === "ban" || value.action === "pick") &&
  typeof value.category === "string" &&
  CATEGORIES.has(value.category) &&
  isPublicDisplayName(value.actor_display_name) &&
  isDateTime(value.occurred_at) &&
  typeof value.automatic === "boolean";

const isDraftCardinality = (value: Record<string, unknown>): boolean => {
  const actionCount = value.format === "bo1" ? 2 : value.format === "bo3" ? 4 : 0;
  const poolSize = value.format === "bo1" ? 3 : value.format === "bo3" ? 5 : 0;
  const selectedCount = value.format === "bo1" ? 1 : value.format === "bo3" ? 3 : 0;
  if (poolSize === 0 || !Array.isArray(value.pool) || value.pool.length !== poolSize) {
    return false;
  }
  const pool = value.pool;
  if (!pool.every((category): category is string => typeof category === "string" && CATEGORIES.has(category))) {
    return false;
  }
  if (new Set(pool).size !== pool.length || !Array.isArray(value.selected_categories)) {
    return false;
  }
  const selectedCategories = value.selected_categories;
  if (
    !selectedCategories.every((category): category is string => typeof category === "string" && CATEGORIES.has(category)) ||
    new Set(selectedCategories).size !== selectedCategories.length ||
    selectedCategories.some((category) => !pool.includes(category))
  ) {
    return false;
  }
  if (!Array.isArray(value.actions) || value.actions.length > actionCount) {
    return false;
  }
  const actionCategories = new Set<string>();
  for (const [index, action] of value.actions.entries()) {
    if (
      !isRecord(action) ||
      action.turn !== index + 1 ||
      typeof action.category !== "string" ||
      !pool.includes(action.category) ||
      actionCategories.has(action.category)
    ) {
      return false;
    }
    actionCategories.add(action.category);
  }

  const state = value.state;
  const terminal = state === "completed" || state === "superseded";
  if (state === "completed" &&
      (value.actions.length !== actionCount || value.selected_categories.length !== selectedCount)) {
    return false;
  }
  if (!terminal && value.selected_categories.length !== 0) {
    return false;
  }
  if (terminal) {
    return value.current_turn === null &&
      value.current_action === null &&
      value.current_actor_display_name === null &&
      value.turn_deadline === null &&
      value.auto_action_pending === false;
  }

  if (
    !isPositiveInteger(value.current_turn) ||
    value.current_turn !== value.actions.length + 1 ||
    value.current_turn > actionCount ||
    (value.current_action !== "ban" && value.current_action !== "pick") ||
    !isPublicDisplayName(value.current_actor_display_name) ||
    typeof value.auto_action_pending !== "boolean"
  ) {
    return false;
  }
  if (state === "active") {
    return isDateTime(value.turn_deadline);
  }
  return value.turn_deadline === null && value.auto_action_pending === false;
};

const isRecordArray = (
  value: unknown,
  guard: (item: unknown) => item is Record<string, unknown>,
): value is Record<string, unknown>[] => Array.isArray(value) && value.every(guard);

export const isPublicRecoverySnapshot = (value: unknown): value is PublicRecoverySnapshot => {
  if (
    !isRecord(value) ||
    !hasOnlyKeys(value, [
      "tournament",
      "scoreboard",
      "swiss_rounds",
      "bracket",
      "live_series",
      "official_results",
      "live_draft",
      "next_cursor",
    ]) ||
    !hasNoPrivateKeys(value) ||
    !isTournament(value.tournament, true) ||
    !isCursor(value.next_cursor) ||
    !isRecord(value.scoreboard) ||
    !hasOnlyKeys(value.scoreboard, ["tournament_id", "projection_revision", "entries"]) ||
    value.scoreboard.tournament_id !== value.tournament.tournament_id ||
    value.scoreboard.projection_revision !== value.next_cursor.projection_revision ||
    !isRecordArray(value.scoreboard.entries, isScoreboardEntry) ||
    !isPublicSwissRounds(value.swiss_rounds) ||
    !isRecord(value.bracket) ||
    !hasOnlyKeys(value.bracket, ["tournament_id", "projection_revision", "matches"]) ||
    value.bracket.tournament_id !== value.tournament.tournament_id ||
    value.bracket.projection_revision !== value.next_cursor.projection_revision ||
    !isPublicBracket(value.bracket.matches, false) ||
    !isRecordArray(value.live_series, isLiveSeries) ||
    !isRecordArray(value.official_results, isOfficialResult) ||
    (value.live_draft !== null && !isDraft(value.live_draft, true))
  ) {
    return false;
  }
  if (
    value.tournament.projection_revision !== value.next_cursor.projection_revision ||
    (value.live_draft !== null &&
      (value.live_draft.tournament_id !== value.tournament.tournament_id ||
        value.live_draft.projection_revision !== value.next_cursor.projection_revision))
  ) {
    return false;
  }
  return true;
};

const isPublicRealtimeEnvelope = (value: unknown): value is PublicRealtimeEnvelope => {
  if (
    !isRecord(value) ||
    !hasOnlyKeys(value, [
      "schema_version",
      "tournament_id",
      "sequence",
      "event_id",
      "occurred_at",
      "projection_revision",
      "resume_id",
      "public",
    ]) ||
    value.schema_version !== 1 ||
    !isUUID(value.tournament_id) ||
    !isNonNegativeInteger(value.sequence) ||
    !isUUID(value.event_id) ||
    !isDateTime(value.occurred_at) ||
    !isPositiveInteger(value.projection_revision) ||
    (value.resume_id !== undefined && !isUUID(value.resume_id)) ||
    !isRecord(value.public) ||
    !hasOnlyKeys(value.public, [
      "revision",
      "last_sequence",
      "tournament",
      "scoreboard",
      "swiss_rounds",
      "bracket",
      "live_series",
      "official_results",
      "draft",
    ]) ||
    !hasNoPrivateKeys(value.public) ||
    value.public.revision !== value.projection_revision ||
    value.public.last_sequence !== value.sequence ||
    !isTournament(value.public.tournament, false) ||
    value.public.tournament.tournament_id !== value.tournament_id ||
    !isRecordArray(value.public.scoreboard, isScoreboardEntry) ||
    !isPublicSwissRounds(value.public.swiss_rounds) ||
    !isPublicBracket(value.public.bracket, true) ||
    !isRecordArray(value.public.live_series, isLiveSeries) ||
    !isRecordArray(value.public.official_results, isOfficialResult) ||
    (value.public.draft !== undefined && !isDraft(value.public.draft, false))
  ) {
    return false;
  }
  return true;
};

export const parsePublicRealtimeMessage = (
  value: unknown,
  expectedTournamentId?: string,
): PublicRealtimeEnvelope => {
  if (!isRecord(value) || !hasOnlyKeys(value, ["type", "payload"]) ||
      value.type !== "tournament.public" || !isRecord(value.payload) ||
      !hasOnlyKeys(value.payload, ["envelope"]) ||
      !isPublicRealtimeEnvelope(value.payload.envelope)) {
    throw new Error("Invalid public realtime envelope");
  }
  if (
    expectedTournamentId !== undefined &&
    value.payload.envelope.tournament_id !== expectedTournamentId
  ) {
    throw new Error("Public realtime envelope has the wrong tournament");
  }
  return value.payload.envelope;
};

const publicRejectionCodes = new Set([
  "tournament.unauthenticated",
  "tournament.forbidden",
  "tournament.unavailable",
  "tournament.capacity",
  "tournament.invalid_frame",
  "tournament.rate_limited",
]);

const PUBLIC_TERMINAL_STATES = new Set(["completed", "cancelled"]);

export const isPublicRealtimeRejection = (value: unknown): boolean => {
  if (!isRecord(value) || !hasOnlyKeys(value, ["type", "code", "message"]) ||
      value.type !== "tournament.rejected" || !isNonBlank(value.code) ||
      !publicRejectionCodes.has(value.code)) {
    return false;
  }
  return isNonBlank(value.message);
};

export const isPublicRealtimeTerminal = (
  value: unknown,
  expectedTournamentId: string,
): boolean => {
  if (
    !isRecord(value) ||
    !hasOnlyKeys(value, ["type", "payload"]) ||
    value.type !== "tournament.terminal" ||
    !isRecord(value.payload) ||
    !hasOnlyKeys(value.payload, [
      "schema_version",
      "tournament_id",
      "sequence",
      "event_id",
      "occurred_at",
      "state",
    ])
  ) {
    return false;
  }
  return (
    value.payload.schema_version === 1 &&
    value.payload.tournament_id === expectedTournamentId &&
    isUUID(value.payload.tournament_id) &&
    isPositiveInteger(value.payload.sequence) &&
    isUUID(value.payload.event_id) &&
    isDateTime(value.payload.occurred_at) &&
    typeof value.payload.state === "string" &&
    PUBLIC_TERMINAL_STATES.has(value.payload.state)
  );
};

const restDisplay = (snapshot: PublicRecoverySnapshot): PublicDisplay => ({
  tournament: snapshot.tournament,
  scoreboard: snapshot.scoreboard.entries,
  swissRounds: snapshot.swiss_rounds,
  bracket: snapshot.bracket.matches,
  liveSeries: snapshot.live_series,
  officialResults: snapshot.official_results,
  liveDraft: snapshot.live_draft,
});

const realtimeDisplay = (envelope: PublicRealtimeEnvelope): PublicDisplay => ({
  tournament: envelope.public.tournament,
  scoreboard: envelope.public.scoreboard,
  swissRounds: envelope.public.swiss_rounds,
  bracket: envelope.public.bracket,
  liveSeries: envelope.public.live_series,
  officialResults: envelope.public.official_results,
  liveDraft: envelope.public.draft ?? null,
});

export const recoverPublicTournament = (
  value: unknown,
  previous: PublicRecoveryState | null = null,
): PublicRecoveryState => {
  if (!isPublicRecoverySnapshot(value)) {
    throw new Error("Invalid public recovery snapshot");
  }
  return {
    tournamentId: value.tournament.tournament_id,
    cursor: value.next_cursor,
    resumeId: previous?.tournamentId === value.tournament.tournament_id ? previous.resumeId : null,
    seenEventIds: [],
    display: restDisplay(value),
  };
};

export const openPublicRealtime = (value: unknown): PublicRecoveryState => {
  if (!isPublicRealtimeEnvelope(value)) {
    throw new Error("Invalid public realtime envelope");
  }
  return {
    tournamentId: value.tournament_id,
    cursor: {
      projection_revision: value.projection_revision,
      event_sequence: value.sequence,
    },
    serverTimestamp: value.occurred_at,
    resumeId: value.resume_id ?? null,
    seenEventIds: [value.event_id],
    display: realtimeDisplay(value),
  };
};

export const openPublicRealtimeMessage = (
  value: unknown,
  tournamentId: string,
): PublicRecoveryState => openPublicRealtime(parsePublicRealtimeMessage(value, tournamentId));

export const isPublicRealtimeGap = (
  state: PublicRecoveryState,
  value: PublicRealtimeEnvelope,
): boolean => value.sequence > state.cursor.event_sequence + 1;

export const applyPublicRealtime = (
  state: PublicRecoveryState,
  value: unknown,
): PublicRealtimeApplyResult => {
  if (!isPublicRealtimeEnvelope(value)) {
    throw new Error("Invalid public realtime envelope");
  }
  if (value.tournament_id !== state.tournamentId) {
    return { state, outcome: "wrong_tournament" };
  }
  if (state.seenEventIds.includes(value.event_id)) {
    return { state, outcome: "duplicate" };
  }
  const nextCursor: PublicRecoveryCursor = {
    projection_revision: value.projection_revision,
    event_sequence: value.sequence,
  };
  if (comparePublicRecoveryCursor(nextCursor, state.cursor) <= 0) {
    return { state, outcome: "out_of_order" };
  }
  const nextState: PublicRecoveryState = {
    tournamentId: state.tournamentId,
    cursor: nextCursor,
    serverTimestamp: value.occurred_at,
    resumeId: value.resume_id ?? state.resumeId,
    seenEventIds: [...state.seenEventIds, value.event_id].slice(-128),
    display: realtimeDisplay(value),
  };
  return { state: nextState, outcome: "applied" };
};

export const publicRealtimeUrl = (
  tournamentId: string,
  resumeId?: string | null,
  baseOrigin?: string,
): string => {
  if (!isUUID(tournamentId)) {
    throw new TypeError("Public realtime requires a UUID tournament id");
  }
  if (resumeId !== undefined && resumeId !== null && !isUUID(resumeId)) {
    throw new TypeError("Public realtime requires a UUID resume id");
  }
  const configuredOrigin = baseOrigin || CONFIG.apiUrl ||
    (typeof window === "undefined" ? "" : window.location.origin);
  if (!configuredOrigin) {
    throw new Error("Public realtime requires a player API origin");
  }
  const configured = new URL(configuredOrigin);
  const protocol = configured.protocol === "https:" || configured.protocol === "wss:"
    ? "wss:"
    : "ws:";
  const url = new URL(
    `/api/v1/tournaments/${encodeURIComponent(tournamentId)}/realtime`,
    `${protocol}//${configured.host}`,
  );
  if (resumeId) {
    url.searchParams.set("resume_id", resumeId);
  }
  return url.toString();
};

export const isPublicRecoveryCursorConflict = (
  value: unknown,
): value is ApiError & { problem: PublicRecoveryCursorConflictProblem } => {
  if (!(value instanceof ApiError) || value.status !== 409) {
    return false;
  }
  const problem: unknown = value.problem;
  if (!isRecord(problem)) {
    return false;
  }
  return (
    problem.status === 409 &&
    typeof problem.type === "string" &&
    isNonBlank(problem.title) &&
    isCursor(problem.requested_cursor) &&
    isCursor(problem.current_cursor)
  );
};

/** Compare the server's public recovery watermark, revision before sequence. */
export const comparePublicRecoveryCursor = (
  left: PublicRecoveryCursor,
  right: PublicRecoveryCursor,
): number => {
  if (left.projection_revision > right.projection_revision) {
    return 1;
  }
  if (left.projection_revision < right.projection_revision) {
    return -1;
  }
  if (left.event_sequence > right.event_sequence) {
    return 1;
  }
  if (left.event_sequence < right.event_sequence) {
    return -1;
  }
  return 0;
};

/** Role-aware REST recovery keeps each generated cursor shape intact. */
export type TournamentLiveRole = "public" | "participant" | "operator";

type ParticipantRecoverySnapshot = components["schemas"]["ParticipantRecoverySnapshot"];
type OperatorRecoverySnapshot = components["schemas"]["OperatorRecoverySnapshot"];
type ParticipantRecoveryCursor = components["schemas"]["ParticipantRecoveryCursor"];
type OperatorRecoveryCursor = components["schemas"]["OperatorRecoveryCursor"];

export type RoleAwareRecoverySnapshot =
  | PublicRecoverySnapshot
  | ParticipantRecoverySnapshot
  | OperatorRecoverySnapshot;

export type RoleAwareRecoveryCursor =
  | PublicRecoveryCursor
  | ParticipantRecoveryCursor
  | OperatorRecoveryCursor;

export type RoleRecoveryResponse = Readonly<{
  role: TournamentLiveRole;
  snapshot: RoleAwareRecoverySnapshot;
  /** The original HTTP Date header used as the server clock anchor. */
  serverTimestamp: string;
}>;

export type RoleAwareRecoveryState = {
  role: TournamentLiveRole;
  tournamentId: string;
  cursor: RoleAwareRecoveryCursor;
  snapshot: RoleAwareRecoverySnapshot;
  serverTimestamp: string;
  resumeId: string | null;
};

export type RoleRecoverySnapshotInput = {
  role: TournamentLiveRole;
  tournamentId: string;
  snapshot: unknown;
  serverTimestamp: string;
  resumeId?: string | null;
  /** A no-cursor response is authoritative even when its cursor is lower or equal. */
  fresh?: boolean;
  /** A cursor-bearing REST response is still a complete snapshot and may refresh equal data. */
  allowEqualCursor?: boolean;
};

export type RoleRecoveryOutcome =
  | "initialized"
  | "replaced"
  | "duplicate"
  | "stale"
  | "wrong_role"
  | "wrong_tournament"
  | "malformed"
  | "unknown_schema"
  | "future_cursor"
  | "invalid_cursor";

export type RoleRecoveryTransition = {
  state: RoleAwareRecoveryState | null;
  outcome: RoleRecoveryOutcome;
  changed: boolean;
};

const readRoleRecoveryResponse = async <T>(
  result: ApiResult<T> | Promise<ApiResult<T>>,
  guard: (value: unknown) => value is T,
  contract: string,
): Promise<Readonly<{ snapshot: T; serverTimestamp: string }>> => {
  const resolved = await result;
  const snapshot = await unwrapApi(resolved, contract);
  if (
    isRecord(snapshot) &&
    "schema_version" in snapshot &&
    snapshot.schema_version !== 1
  ) {
    throw new UnknownRecoverySchemaError(contract);
  }
  if (!guard(snapshot)) {
    throw new ApiContractError(contract);
  }
  const serverTimestamp = resolved.response.headers.get("Date");
  if (!isServerTimestamp(serverTimestamp)) {
    throw new ApiContractError(`${contract} HTTP Date header`);
  }
  return { snapshot, serverTimestamp };
};

const isPublicCursorValue = (value: RoleAwareRecoveryCursor): value is PublicRecoveryCursor =>
  "event_sequence" in value &&
  !("participant_view_revision" in value) &&
  !("authority_revision" in value);

const isParticipantCursorValue = (
  value: RoleAwareRecoveryCursor,
): value is ParticipantRecoveryCursor => "participant_view_revision" in value;

const isOperatorCursorValue = (value: RoleAwareRecoveryCursor): value is OperatorRecoveryCursor =>
  "authority_revision" in value && "audit_sequence" in value;

/** Fetch and validate the full recovery projection for one Arena role. */
export const getRoleRecoverySnapshot = async (
  role: TournamentLiveRole,
  tournamentId: string,
  cursor?: RoleAwareRecoveryCursor,
  signal?: AbortSignal,
): Promise<RoleRecoveryResponse> => {
  if (role === "public") {
    if (cursor !== undefined && !isPublicCursorValue(cursor)) {
      throw new TypeError("Public recovery requires a public cursor");
    }
    const params = cursor === undefined
      ? { path: { tournament_id: tournamentId } }
      : { path: { tournament_id: tournamentId }, query: { cursor } };
    const response = await readRoleRecoveryResponse(
      publicClient.GET("/api/v1/tournaments/{tournament_id}/snapshot", {
        params,
        signal,
      }),
      (value): value is PublicRecoverySnapshot => isPublicRecoverySnapshot(value),
      "public tournament recovery snapshot",
    );
    return { role, ...response };
  }

  if (role === "participant") {
    if (cursor !== undefined && !isParticipantCursorValue(cursor)) {
      throw new TypeError("Participant recovery requires a participant cursor");
    }
    const params = cursor === undefined
      ? { path: { tournament_id: tournamentId } }
      : { path: { tournament_id: tournamentId }, query: { cursor } };
    const response = await readRoleRecoveryResponse(
      publicClient.GET("/api/v1/tournaments/{tournament_id}/participant/snapshot", {
        params,
        signal,
      }),
      (value): value is ParticipantRecoverySnapshot => isParticipantRecoverySnapshot(value),
      "participant tournament recovery snapshot",
    );
    return { role, ...response };
  }

  if (cursor !== undefined && !isOperatorCursorValue(cursor)) {
    throw new TypeError("Operator recovery requires an operator cursor");
  }
  const params = cursor === undefined
    ? { path: { tournament_id: tournamentId } }
    : { path: { tournament_id: tournamentId }, query: { cursor } };
  const response = await readRoleRecoveryResponse(
    adminClient.GET("/api/v1/admin/tournaments/{tournament_id}/snapshot", {
      params,
      signal,
    }),
    (value): value is OperatorRecoverySnapshot => isOperatorRecoverySnapshot(value),
    "operator tournament recovery snapshot",
  );
  return { role, ...response };
};

const hasSnapshotScope = (
  role: TournamentLiveRole,
  value: unknown,
): value is RoleAwareRecoverySnapshot => {
  switch (role) {
    case "public":
      return isPublicRecoverySnapshot(value);
    case "participant":
      if (!isParticipantRecoverySnapshot(value)) {
        return false;
      }
      return value.lobby.tournament_id === value.tournament_id &&
        value.lobby.projection_revision === value.projection_revision &&
        (value.series === null || value.series.tournament_id === value.tournament_id) &&
        (value.wave === null || value.wave.tournament_id === value.tournament_id) &&
        value.next_cursor.projection_revision === value.projection_revision;
    case "operator":
      if (!isOperatorRecoverySnapshot(value)) {
        return false;
      }
      return value.tournament.id === value.roster.tournament_id &&
        value.series.every((series) => series.tournament_id === value.tournament.id) &&
        value.waves.every((wave) => wave.tournament_id === value.tournament.id) &&
        (value.pause_graph === null || (
          value.pause_graph.tournament_id === value.tournament.id &&
          value.pause_graph.roster_id === value.roster.id
        ));
  }
};

const snapshotTournamentId = (
  role: TournamentLiveRole,
  snapshot: RoleAwareRecoverySnapshot,
): string => {
  if (role === "public" && isPublicRecoverySnapshot(snapshot)) {
    return snapshot.tournament.tournament_id;
  }
  if (role === "participant" && isParticipantRecoverySnapshot(snapshot)) {
    return snapshot.tournament_id;
  }
  if (role === "operator" && isOperatorRecoverySnapshot(snapshot)) {
    return snapshot.tournament.id;
  }
  throw new Error("recovery role does not match snapshot");
};

const snapshotCursor = (
  role: TournamentLiveRole,
  snapshot: RoleAwareRecoverySnapshot,
): RoleAwareRecoveryCursor => {
  if (role === "public" && isPublicRecoverySnapshot(snapshot)) {
    return snapshot.next_cursor;
  }
  if (role === "participant" && isParticipantRecoverySnapshot(snapshot)) {
    return snapshot.next_cursor;
  }
  if (role === "operator" && isOperatorRecoverySnapshot(snapshot)) {
    return snapshot.next_cursor;
  }
  throw new Error("recovery role does not match snapshot");
};

const compareRoleCursor = (
  role: TournamentLiveRole,
  left: RoleAwareRecoveryCursor,
  right: RoleAwareRecoveryCursor,
): number => {
  const values: ReadonlyArray<readonly [number, number]> = (() => {
    switch (role) {
      case "public": {
        if (
          "participant_view_revision" in left ||
          "authority_revision" in left ||
          "audit_sequence" in left ||
          "participant_view_revision" in right ||
          "authority_revision" in right ||
          "audit_sequence" in right
        ) {
          return [];
        }
        return [
          [left.projection_revision, right.projection_revision],
          [left.event_sequence, right.event_sequence],
        ];
      }
      case "participant": {
        if (
          !("participant_view_revision" in left) ||
          !("participant_view_revision" in right)
        ) {
          return [];
        }
        return [
          [left.projection_revision, right.projection_revision],
          [left.participant_view_revision, right.participant_view_revision],
          [left.event_sequence, right.event_sequence],
        ];
      }
      case "operator": {
        if (
          !("authority_revision" in left) ||
          !("authority_revision" in right) ||
          !("audit_sequence" in left) ||
          !("audit_sequence" in right)
        ) {
          return [];
        }
        return [
          [left.projection_revision, right.projection_revision],
          [left.authority_revision, right.authority_revision],
          [left.audit_sequence, right.audit_sequence],
        ];
      }
    }
  })();
  for (const [current, previous] of values) {
    if (current > previous) {
      return 1;
    }
    if (current < previous) {
      return -1;
    }
  }
  return 0;
};

export const applyRoleRecoverySnapshot = (
  previous: RoleAwareRecoveryState | null,
  input: RoleRecoverySnapshotInput,
): RoleRecoveryTransition => {
  if (previous !== null && previous.role !== input.role) {
    return {
      state: previous,
      outcome: "wrong_role",
      changed: false,
    };
  }
  if (
    isRecord(input.snapshot) &&
    "schema_version" in input.snapshot &&
    input.snapshot.schema_version !== 1
  ) {
    return {
      state: previous,
      outcome: "unknown_schema",
      changed: false,
    };
  }
  if (!isServerTimestamp(input.serverTimestamp) || !hasSnapshotScope(input.role, input.snapshot)) {
    return {
      state: previous,
      outcome: "malformed",
      changed: false,
    };
  }
  const tournamentId = snapshotTournamentId(input.role, input.snapshot);
  if (tournamentId !== input.tournamentId) {
    return {
      state: previous,
      outcome: "wrong_tournament",
      changed: false,
    };
  }
  if (
    previous !== null &&
    (previous.tournamentId !== input.tournamentId || previous.tournamentId !== tournamentId)
  ) {
    return {
      state: previous,
      outcome: "wrong_tournament",
      changed: false,
    };
  }
  const cursor = snapshotCursor(input.role, input.snapshot);
  if (previous === null) {
    return {
      state: {
        role: input.role,
        tournamentId,
        cursor,
        snapshot: input.snapshot,
        serverTimestamp: input.serverTimestamp,
        resumeId: input.resumeId ?? null,
      },
      outcome: "initialized",
      changed: true,
    };
  }
  if (input.fresh === true) {
    return {
      state: {
        role: input.role,
        tournamentId,
        cursor,
        snapshot: input.snapshot,
        serverTimestamp: input.serverTimestamp,
        resumeId: input.resumeId ?? previous.resumeId,
      },
      outcome: "replaced",
      changed: true,
    };
  }
  const comparison = compareRoleCursor(input.role, cursor, previous.cursor);
  if (comparison < 0) {
    return {
      state: previous,
      outcome: "stale",
      changed: false,
    };
  }
  if (comparison === 0 && input.allowEqualCursor !== true) {
    return {
      state: previous,
      outcome: "duplicate",
      changed: false,
    };
  }
  const nextState: RoleAwareRecoveryState = {
    role: input.role,
    tournamentId,
    cursor,
    snapshot: input.snapshot,
    serverTimestamp: input.serverTimestamp,
    resumeId: input.resumeId ?? previous.resumeId,
  };
  return {
    state: nextState,
    outcome: "replaced",
    changed: true,
  };
};

export const recoverRoleSnapshot = (
  input: RoleRecoverySnapshotInput,
  previous: RoleAwareRecoveryState | null = null,
): RoleRecoveryTransition => applyRoleRecoverySnapshot(previous, input);

const isFutureRecoveryCursor = (
  previous: RoleAwareRecoveryState | null,
  error: ApiError,
): boolean =>
  (previous === null || previous.role === "public") && isPublicRecoveryCursorConflict(error);

export const classifyRoleRecoveryError = (
  previous: RoleAwareRecoveryState | null,
  error: unknown,
): RoleRecoveryTransition => {
  if (error instanceof UnknownRecoverySchemaError) {
    return { state: previous, outcome: "unknown_schema", changed: false };
  }
  if (!(error instanceof ApiError)) {
    return { state: previous, outcome: "malformed", changed: false };
  }
  if (isFutureRecoveryCursor(previous, error)) {
    return { state: previous, outcome: "future_cursor", changed: false };
  }
  if (error.status === 400 || error.status === 409 || error.status === 422) {
    return { state: previous, outcome: "invalid_cursor", changed: false };
  }
  return {
    state: previous,
    outcome: "malformed",
    changed: false,
  };
};

type OperatorProjection = Readonly<{
  tournament_id: string;
  revision: number;
  last_sequence: number;
  waves: readonly Record<string, unknown>[];
  presence: readonly Record<string, unknown>[];
  replays: readonly Record<string, unknown>[];
  pause?: Record<string, unknown> | null;
  audit_links: readonly Record<string, unknown>[];
  golden: readonly Record<string, unknown>[];
}>;

export type OperatorRealtimeEnvelope = Readonly<{
  schema_version: 1;
  tournament_id: string;
  sequence: number;
  event_id: string;
  occurred_at: string;
  projection_revision: number;
  resume_id?: string;
  operator: OperatorProjection;
}>;

export type OperatorRealtimeState = Readonly<{
  tournamentId: string;
  projectionRevision: number;
  sequence: number;
  resumeId: string | null;
  seenEventIds: readonly string[];
  operator: OperatorProjection;
}>;

export type OperatorRealtimeApplyResult = Readonly<{
  state: OperatorRealtimeState;
  outcome: "applied" | "duplicate" | "out_of_order" | "wrong_tournament";
}>;

const hasRequiredOnlyKeys = (
  value: Record<string, unknown>,
  required: readonly string[],
  optional: readonly string[] = [],
): boolean =>
  hasOnlyKeys(value, [...required, ...optional]) && required.every((key) => key in value);

const isOperatorRealtimeWaveMember = (value: unknown): boolean =>
  isRecord(value) &&
  hasRequiredOnlyKeys(value, ["participant_id", "readiness_revision", "ready"], ["series_id"]) &&
  isUUID(value.participant_id) &&
  isPositiveInteger(value.readiness_revision) &&
  typeof value.ready === "boolean" &&
  (value.series_id === undefined || value.series_id === null || isUUID(value.series_id));

const isOperatorRealtimeWave = (value: unknown): boolean =>
  isRecord(value) &&
  hasRequiredOnlyKeys(value, ["wave_id", "state", "members"], ["window_deadline"]) &&
  isUUID(value.wave_id) &&
  typeof value.state === "string" &&
  WAVE_STATES.has(value.state) &&
  Array.isArray(value.members) &&
  isOptionalDateTime(value.window_deadline) &&
  value.members.every(isOperatorRealtimeWaveMember);

const isOperatorRealtimePresence = (value: unknown): boolean =>
  isRecord(value) &&
  hasRequiredOnlyKeys(value, [
    "participant_id",
    "series_id",
    "state",
    "presence_epoch",
    "updated_at",
  ]) &&
  isUUID(value.participant_id) &&
  isUUID(value.series_id) &&
  (value.state === "connected" || value.state === "disconnected") &&
  isPositiveInteger(value.presence_epoch) &&
  isDateTime(value.updated_at);

const isOperatorRealtimeReplay = (value: unknown): boolean =>
  isRecord(value) &&
  hasRequiredOnlyKeys(value, [
    "series_id",
    "slot_id",
    "failed_game_id",
    "replacement_game_id",
    "replacement_wave_id",
    "state",
    "revision",
  ]) &&
  isUUID(value.series_id) &&
  isUUID(value.slot_id) &&
  isUUID(value.failed_game_id) &&
  isUUID(value.replacement_game_id) &&
  isUUID(value.replacement_wave_id) &&
  isNonBlank(value.state) &&
  isPositiveInteger(value.revision);

const isOperatorRealtimeAuditLink = (value: unknown): boolean =>
  isRecord(value) &&
  hasRequiredOnlyKeys(value, [
    "audit_event_id",
    "entity_kind",
    "entity_id",
    "official_result_revision_id",
  ]) &&
  isUUID(value.audit_event_id) &&
  isNonBlank(value.entity_kind) &&
  isUUID(value.entity_id) &&
  isUUID(value.official_result_revision_id);

const isOperatorRealtimeGolden = (value: unknown): boolean =>
  isGoldenOperatorGroup(value);

const isOperatorRealtimePause = (value: unknown): boolean =>
  isRecord(value) &&
  hasRequiredOnlyKeys(value, [
    "pause_id",
    "state",
    "reason",
    "paused_at",
    "graph_revision",
  ], ["game_id", "frozen_remaining_ms", "reconnect_deadline"]) &&
  isUUID(value.pause_id) &&
  isNonBlank(value.state) &&
  isNonBlank(value.reason) &&
  isDateTime(value.paused_at) &&
  isPositiveInteger(value.graph_revision) &&
  (value.game_id === undefined || value.game_id === null || isUUID(value.game_id)) &&
  (value.frozen_remaining_ms === undefined ||
    value.frozen_remaining_ms === null ||
    isPositiveInteger(value.frozen_remaining_ms)) &&
  ((value.game_id === undefined || value.game_id === null) ===
    (value.frozen_remaining_ms === undefined || value.frozen_remaining_ms === null)) &&
  (value.reconnect_deadline === undefined || isUUID(value.game_id)) &&
  isOptionalDateTime(value.reconnect_deadline);

const isOperatorRealtimeRecordList = (
  value: unknown,
  guard: (item: unknown) => boolean,
): boolean => Array.isArray(value) && value.every(guard);

const isOperatorProjection = (value: unknown): value is OperatorProjection => {
  if (!isRecord(value) || !hasOnlyKeys(value, [
    "tournament_id",
    "revision",
    "last_sequence",
    "waves",
    "presence",
    "replays",
    "pause",
    "audit_links",
    "golden",
  ])) {
    return false;
  }
  return (
    isUUID(value.tournament_id) &&
    isPositiveInteger(value.revision) &&
    isNonNegativeInteger(value.last_sequence) &&
    isOperatorRealtimeRecordList(value.waves, isOperatorRealtimeWave) &&
    isOperatorRealtimeRecordList(value.presence, isOperatorRealtimePresence) &&
    isOperatorRealtimeRecordList(value.replays, isOperatorRealtimeReplay) &&
    (value.pause === undefined || value.pause === null || isOperatorRealtimePause(value.pause)) &&
    isOperatorRealtimeRecordList(value.audit_links, isOperatorRealtimeAuditLink) &&
    isOperatorRealtimeRecordList(value.golden, isOperatorRealtimeGolden)
  );
};

const isOperatorRealtimeEnvelope = (
  value: unknown,
): value is OperatorRealtimeEnvelope => {
  if (!isRecord(value) || !hasOnlyKeys(value, [
    "schema_version",
    "tournament_id",
    "sequence",
    "event_id",
    "occurred_at",
    "projection_revision",
    "resume_id",
    "operator",
  ])) {
    return false;
  }
  return (
    value.schema_version === 1 &&
    isUUID(value.tournament_id) &&
    isNonNegativeInteger(value.sequence) &&
    isUUID(value.event_id) &&
    isDateTime(value.occurred_at) &&
    isPositiveInteger(value.projection_revision) &&
    (value.resume_id === undefined || isUUID(value.resume_id)) &&
    isOperatorProjection(value.operator) &&
    value.operator.tournament_id === value.tournament_id &&
    value.operator.revision === value.projection_revision &&
    value.operator.last_sequence === value.sequence
  );
};

export const parseOperatorRealtimeMessage = (
  value: unknown,
  expectedTournamentId?: string,
): OperatorRealtimeEnvelope => {
  if (!isRecord(value) || !hasOnlyKeys(value, ["type", "payload"]) ||
      value.type !== "tournament.operator" || !isRecord(value.payload) ||
      !hasOnlyKeys(value.payload, ["envelope"]) ||
      !isOperatorRealtimeEnvelope(value.payload.envelope)) {
    throw new Error("Invalid operator realtime envelope");
  }
  if (
    expectedTournamentId !== undefined &&
    value.payload.envelope.tournament_id !== expectedTournamentId
  ) {
    throw new Error("Operator realtime envelope has the wrong tournament");
  }
  return value.payload.envelope;
};

const operatorRejectionCodes = new Set([
  "tournament.unauthenticated",
  "tournament.forbidden",
  "tournament.unavailable",
  "tournament.capacity",
  "tournament.invalid_frame",
  "tournament.rate_limited",
]);

export const isOperatorRealtimeRejection = (value: unknown): boolean => {
  if (!isRecord(value) || !hasOnlyKeys(value, ["type", "code", "message"]) ||
      value.type !== "tournament.rejected" || !isNonBlank(value.code) ||
      !operatorRejectionCodes.has(value.code)) {
    return false;
  }
  return isNonBlank(value.message);
};

export const openOperatorRealtime = (
  value: unknown,
  tournamentId: string,
): OperatorRealtimeState => {
  const envelope = parseOperatorRealtimeMessage(value, tournamentId);
  return {
    tournamentId: envelope.tournament_id,
    projectionRevision: envelope.projection_revision,
    sequence: envelope.sequence,
    resumeId: envelope.resume_id ?? null,
    seenEventIds: [envelope.event_id],
    operator: envelope.operator,
  };
};

export const applyOperatorRealtime = (
  state: OperatorRealtimeState,
  value: unknown,
): OperatorRealtimeApplyResult => {
  const envelope = parseOperatorRealtimeMessage(value);
  if (envelope.tournament_id !== state.tournamentId) {
    return { state, outcome: "wrong_tournament" };
  }
  if (state.seenEventIds.includes(envelope.event_id)) {
    return { state, outcome: "duplicate" };
  }
  if (
    envelope.sequence <= state.sequence ||
    envelope.projection_revision < state.projectionRevision ||
    envelope.operator.revision < state.operator.revision
  ) {
    return { state, outcome: "out_of_order" };
  }
  return {
    state: {
      tournamentId: state.tournamentId,
      projectionRevision: envelope.projection_revision,
      sequence: envelope.sequence,
      resumeId: envelope.resume_id ?? state.resumeId,
      seenEventIds: [...state.seenEventIds, envelope.event_id].slice(-128),
      operator: envelope.operator,
    },
    outcome: "applied",
  };
};

const isResumeId = (value: string): boolean => isUUID(value);

export const operatorRealtimeUrl = (
  tournamentId: string,
  resumeId?: string | null,
  baseOrigin?: string,
): string => {
  if (!isUUID(tournamentId)) {
    throw new TypeError("Operator realtime requires a UUID tournament id");
  }
  if (resumeId !== undefined && resumeId !== null && !isResumeId(resumeId)) {
    throw new TypeError("Operator realtime requires a UUID resume id");
  }
  const configuredOrigin = baseOrigin || CONFIG.adminApiUrl ||
    (typeof window === "undefined" ? "" : window.location.origin);
  if (!configuredOrigin) {
    throw new Error("Operator realtime requires an admin API origin");
  }
  const configured = new URL(configuredOrigin);
  const protocol = configured.protocol === "https:" || configured.protocol === "wss:"
    ? "wss:"
    : "ws:";
  const url = new URL(
    `/api/v1/admin/tournaments/${encodeURIComponent(tournamentId)}/realtime`,
    `${protocol}//${configured.host}`,
  );
  if (resumeId) {
    url.searchParams.set("resume_id", resumeId);
  }
  return url.toString();
};
