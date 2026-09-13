import { ApiError } from "./client";
import {
  isOperatorRecoverySnapshot,
  isParticipantRecoverySnapshot,
} from "./guards";
import type { components } from "./schema";

export type PublicRecoveryCursor = components["schemas"]["PublicRecoveryCursor"];
export type PublicRecoverySnapshot = components["schemas"]["PublicRecoverySnapshot"];
type PublicRecoveryCursorConflictProblem =
  components["schemas"]["PublicRecoveryCursorConflictProblem"];

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

export type RoleAwareRecoverySnapshot =
  | PublicRecoverySnapshot
  | ParticipantRecoverySnapshot
  | OperatorRecoverySnapshot;

export type RoleAwareRecoveryCursor =
  | PublicRecoveryCursor
  | components["schemas"]["ParticipantRecoveryCursor"]
  | components["schemas"]["OperatorRecoveryCursor"];

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
  | "future_cursor";

export type RoleRecoveryTransition = {
  state: RoleAwareRecoveryState | null;
  outcome: RoleRecoveryOutcome;
  changed: boolean;
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
  if (!isDateTime(input.serverTimestamp) || !hasSnapshotScope(input.role, input.snapshot)) {
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

export const classifyRoleRecoveryError = (
  previous: RoleAwareRecoveryState | null,
  error: unknown,
): RoleRecoveryTransition => ({
  state: previous,
  outcome: error instanceof ApiError && error.status === 409 ? "future_cursor" : "malformed",
  changed: false,
});
