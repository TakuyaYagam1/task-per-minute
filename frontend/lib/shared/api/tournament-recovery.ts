import {
  adminClient,
  ApiError,
  publicClient,
  unwrapApi,
  type ApiResult,
} from "./client";
import {
  ApiContractError,
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
  bracket: readonly Record<string, unknown>[];
  liveSeries: readonly Record<string, unknown>[];
  officialResults: readonly Record<string, unknown>[];
  liveDraft: Record<string, unknown> | null;
};

export type PublicRecoveryState = {
  tournamentId: string;
  cursor: PublicRecoveryCursor;
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
    bracket: readonly Record<string, unknown>[];
    live_series: readonly Record<string, unknown>[];
    official_results: readonly Record<string, unknown>[];
    draft?: Record<string, unknown>;
  };
};

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

const isNonBlank = (value: unknown): value is string =>
  typeof value === "string" && value.trim().length > 0;

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
  isNonNegativeInteger(value.second_participant_wins);

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
  hasOnlyKeys(value, ["rank", "display_name", "points", "buchholz", "effective_time_ms"]) &&
  isPositiveInteger(value.rank) &&
  isNonBlank(value.display_name) &&
  isNonNegativeInteger(value.points) &&
  isNonNegativeInteger(value.buchholz) &&
  isNonNegativeInteger(value.effective_time_ms);

const isBracketMatch = (value: unknown, websocket: boolean): value is Record<string, unknown> =>
  isRecord(value) &&
  hasOnlyKeys(value, ["stage", "position", "first_display_name", "second_display_name", "score", "state"]) &&
  (value.stage === "semifinal" || value.stage === "final") &&
  isPositiveInteger(value.position) &&
  isNonBlank(value.first_display_name) &&
  isNonBlank(value.second_display_name) &&
  (websocket ? isPublicSeriesScore(value.score) : isBracketScore(value.score)) &&
  typeof value.state === "string" &&
  SERIES_STATES.has(value.state);

const isLiveSeries = (value: unknown): value is Record<string, unknown> =>
  isRecord(value) &&
  hasOnlyKeys(value, [
    "series_id",
    "format",
    "state",
    "first_display_name",
    "second_display_name",
    "score",
    "current_game_position",
  ]) &&
  isUUID(value.series_id) &&
  (value.format === "bo1" || value.format === "bo3") &&
  typeof value.state === "string" &&
  SERIES_STATES.has(value.state) &&
  isNonBlank(value.first_display_name) &&
  isNonBlank(value.second_display_name) &&
  isPublicSeriesScore(value.score) &&
  (value.current_game_position === undefined ||
    (isNonNegativeInteger(value.current_game_position) && value.current_game_position <= 3));

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
  const allowed = ["series_id", "format", "state", "pool", "selected_categories", "actions"];
  if (includeProjection) {
    allowed.push("tournament_id", "projection_revision");
  }
  return (
    hasOnlyKeys(value, allowed) &&
    isUUID(value.series_id) &&
    (value.format === "bo1" || value.format === "bo3") &&
    (value.state === "active" || value.state === "completed") &&
    Array.isArray(value.pool) &&
    value.pool.every((item) => typeof item === "string" && CATEGORIES.has(item)) &&
    Array.isArray(value.selected_categories) &&
    value.selected_categories.every((item) => typeof item === "string" && CATEGORIES.has(item)) &&
    Array.isArray(value.actions) &&
    value.actions.every(isDraftAction) &&
    (!includeProjection ||
      (isUUID(value.tournament_id) && isPositiveInteger(value.projection_revision)))
  );
};

const isDraftAction = (value: unknown): boolean =>
  isRecord(value) &&
  hasOnlyKeys(value, ["turn", "action", "category", "actor_display_name", "occurred_at"]) &&
  isPositiveInteger(value.turn) &&
  (value.action === "ban" || value.action === "pick") &&
  typeof value.category === "string" &&
  CATEGORIES.has(value.category) &&
  isNonBlank(value.actor_display_name) &&
  isDateTime(value.occurred_at);

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
    !isRecord(value.bracket) ||
    !hasOnlyKeys(value.bracket, ["tournament_id", "projection_revision", "matches"]) ||
    value.bracket.tournament_id !== value.tournament.tournament_id ||
    value.bracket.projection_revision !== value.next_cursor.projection_revision ||
    !isRecordArray(value.bracket.matches, (item): item is Record<string, unknown> =>
      isBracketMatch(item, false),
    ) ||
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
    !isRecordArray(value.public.bracket, (item): item is Record<string, unknown> =>
      isBracketMatch(item, true),
    ) ||
    !isRecordArray(value.public.live_series, isLiveSeries) ||
    !isRecordArray(value.public.official_results, isOfficialResult) ||
    (value.public.draft !== undefined && !isDraft(value.public.draft, false))
  ) {
    return false;
  }
  return true;
};

const restDisplay = (snapshot: PublicRecoverySnapshot): PublicDisplay => ({
  tournament: snapshot.tournament,
  scoreboard: snapshot.scoreboard.entries,
  bracket: snapshot.bracket.matches,
  liveSeries: snapshot.live_series,
  officialResults: snapshot.official_results,
  liveDraft: snapshot.live_draft,
});

const realtimeDisplay = (envelope: PublicRealtimeEnvelope): PublicDisplay => ({
  tournament: envelope.public.tournament,
  scoreboard: envelope.public.scoreboard,
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
    resumeId: value.resume_id ?? null,
    seenEventIds: [value.event_id],
    display: realtimeDisplay(value),
  };
};

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
  if (
    value.sequence <= state.cursor.event_sequence ||
    value.projection_revision < state.cursor.projection_revision
  ) {
    return { state, outcome: "out_of_order" };
  }
  const nextState: PublicRecoveryState = {
    tournamentId: state.tournamentId,
    cursor: {
      projection_revision: value.projection_revision,
      event_sequence: value.sequence,
    },
    resumeId: value.resume_id ?? state.resumeId,
    seenEventIds: [...state.seenEventIds, value.event_id].slice(-128),
    display: realtimeDisplay(value),
  };
  return { state: nextState, outcome: "applied" };
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
        )) &&
        value.next_cursor.projection_revision === value.tournament.revision;
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
  if (comparison === 0) {
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
  const isRecordList = (candidate: unknown): candidate is Record<string, unknown>[] =>
    Array.isArray(candidate) && candidate.every(isRecord);
  return (
    isUUID(value.tournament_id) &&
    isPositiveInteger(value.revision) &&
    isNonNegativeInteger(value.last_sequence) &&
    isRecordList(value.waves) &&
    isRecordList(value.presence) &&
    isRecordList(value.replays) &&
    (value.pause === undefined || value.pause === null || isRecord(value.pause)) &&
    isRecordList(value.audit_links) &&
    isRecordList(value.golden)
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
