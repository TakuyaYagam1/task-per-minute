import { ApiError, publicClient, unwrapApi, type ApiResult } from "./client";
import { assertApiResponse } from "./guards";
import type { components } from "./schema";
import { isUUID } from "../lib/validation";

export type TournamentAdmissionStatus = components["schemas"]["TournamentAdmissionStatus"];
export type TournamentAdmissionView = components["schemas"]["TournamentAdmissionView"];
export type TournamentAdmissionMutation = components["schemas"]["TournamentAdmissionMutation"];
export type TournamentAdmissionConflictReason =
  components["schemas"]["TournamentAdmissionConflictReason"];

export type TournamentAdmissionCommandIntent = Readonly<{
  idempotencyKey: string;
}>;

export type TournamentAdmissionMutationResult =
  | {
      status: "success";
      value: TournamentAdmissionMutation;
    }
  | {
      status: "unauthorized";
      error: ApiError;
    }
  | {
      status: "forbidden";
      error: ApiError;
    }
  | {
      status: "conflict";
      error: ApiError;
      reason: TournamentAdmissionConflictReason;
    }
  | {
      status: "rate_limited";
      error: ApiError;
      retryAfter: string | null;
    };

const NIL_UUID = "00000000-0000-0000-0000-000000000000";
const ADMISSION_STATUSES = new Set<TournamentAdmissionStatus>([
  "not_registered",
  "invited",
  "registered",
  "checked_in",
  "withdrawn",
]);
const ATTENDANCE_STATES = new Set<NonNullable<TournamentAdmissionView["attendance"]>>([
  "invited",
  "registered",
  "checked_in",
  "withdrawn",
]);
const TOURNAMENT_STATES = new Set<components["schemas"]["TournamentState"]>([
  "registration",
  "roster_locked",
  "swiss",
  "golden",
  "playoffs",
  "technical_pause",
  "completed",
  "cancelled",
]);
const CONFLICT_REASONS = new Set<TournamentAdmissionConflictReason>([
  "closed",
  "full",
  "withdrawn",
  "conflicting_reservation",
  "conflict",
]);

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === "object" && !Array.isArray(value);

const hasExactKeys = (
  value: Record<string, unknown>,
  allowedKeys: readonly string[],
): boolean => {
  const keys = Object.keys(value);
  return keys.length === allowedKeys.length && keys.every((key) => allowedKeys.includes(key));
};

const isNonNilUUID = (value: unknown): value is string =>
  isUUID(value) && value.toLowerCase() !== NIL_UUID;

const isSafeInteger = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value);

const isAdmissionView = (value: unknown): value is TournamentAdmissionView => {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, [
      "attendance",
      "participant_id",
      "planned_roster_size",
      "player_id",
      "roster_locked",
      "roster_revision",
      "roster_size",
      "seed",
      "status",
      "tournament_id",
      "tournament_state",
    ]) ||
    !isNonNilUUID(value.player_id) ||
    !isNonNilUUID(value.tournament_id) ||
    !isStringEnum(value.status, ADMISSION_STATUSES) ||
    !isStringEnum(value.tournament_state, TOURNAMENT_STATES) ||
    typeof value.roster_locked !== "boolean" ||
    !isSafeInteger(value.roster_revision) ||
    value.roster_revision < 1 ||
    !isSafeInteger(value.planned_roster_size) ||
    value.planned_roster_size < 4 ||
    value.planned_roster_size > 16 ||
    !isSafeInteger(value.roster_size) ||
    value.roster_size < 0 ||
    value.roster_size > 16
  ) {
    return false;
  }

  const attendance = value.attendance;
  const participantID = value.participant_id;
  const seed = value.seed;
  if (
    !(attendance === null || isStringEnum(attendance, ATTENDANCE_STATES)) ||
    !(participantID === null || isNonNilUUID(participantID)) ||
    !(seed === null || (isSafeInteger(seed) && seed >= 1 && seed <= 16))
  ) {
    return false;
  }

  if (value.status === "not_registered") {
    return attendance === null && participantID === null && seed === null;
  }
  return attendance === value.status && participantID !== null && seed !== null;
};

const isTournamentAdmissionMutation = (
  value: unknown,
): value is TournamentAdmissionMutation =>
  isRecord(value) &&
  hasExactKeys(value, ["changed", "view"]) &&
  typeof value.changed === "boolean" &&
  isAdmissionView(value.view);

const isStringEnum = <T extends string>(value: unknown, values: ReadonlySet<T>): value is T =>
  typeof value === "string" && values.has(value as T);

const createFallbackUUID = (): string => {
  const bytes = new Uint8Array(16);
  const source = globalThis.crypto;
  if (source?.getRandomValues) {
    source.getRandomValues(bytes);
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256);
    }
  }
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
};

export const createTournamentAdmissionCommandIntent = (): TournamentAdmissionCommandIntent => ({
  idempotencyKey: globalThis.crypto?.randomUUID?.() ?? createFallbackUUID(),
});

type TournamentAdmissionMutationHeaders = {
  "Idempotency-Key": string;
  "X-CSRF-Token": "";
};

const mutationHeaders = (
  intent: TournamentAdmissionCommandIntent,
): TournamentAdmissionMutationHeaders => {
  if (intent.idempotencyKey.trim().length === 0) {
    throw new TypeError("Tournament admission command intent requires a non-empty idempotency key");
  }
  return {
    "Idempotency-Key": intent.idempotencyKey,
    "X-CSRF-Token": "",
  };
};

const admissionPath = (tournamentId: string): { tournament_id: string } => ({
  tournament_id: tournamentId,
});

const readAdmissionResponse = async <T>(
  result: ApiResult<T> | Promise<ApiResult<T>>,
  guard: (value: unknown) => value is T,
  contract: string,
): Promise<T> => {
  const data = await unwrapApi(result, contract);
  return assertApiResponse(data, guard, contract);
};

const isConflictReason = (value: unknown): value is TournamentAdmissionConflictReason =>
  isStringEnum(value, CONFLICT_REASONS);

const conflictReasonFor = (error: ApiError): TournamentAdmissionConflictReason => {
  const problem: unknown = error.problem;
  const reason = isRecord(problem) ? problem.reason : undefined;
  return isConflictReason(reason) ? reason : "conflict";
};

const readMutationResponse = async (
  result: ApiResult<TournamentAdmissionMutation> | Promise<ApiResult<TournamentAdmissionMutation>>,
): Promise<TournamentAdmissionMutationResult> => {
  try {
    const data = await unwrapApi(result, "tournament admission mutation");
    return {
      status: "success",
      value: assertApiResponse(data, isTournamentAdmissionMutation, "tournament admission mutation"),
    };
  } catch (error) {
    if (!(error instanceof ApiError)) {
      throw error;
    }
    if (error.status === 401) {
      return { status: "unauthorized", error };
    }
    if (error.status === 403) {
      return { status: "forbidden", error };
    }
    if (error.status === 409) {
      return { status: "conflict", error, reason: conflictReasonFor(error) };
    }
    if (error.status === 429) {
      return {
        status: "rate_limited",
        error,
        retryAfter: error.retryAfter ?? null,
      };
    }
    throw error;
  }
};

export const getTournamentAdmissionStatus = async (
  tournamentId: string,
  signal?: AbortSignal,
): Promise<TournamentAdmissionView> =>
  readAdmissionResponse(
    publicClient.GET("/api/v1/tournaments/{tournament_id}/participant/queue", {
      params: { path: admissionPath(tournamentId) },
      signal,
    }),
    isAdmissionView,
    "tournament admission status",
  );

export const joinTournamentAdmission = async (
  tournamentId: string,
  intent: TournamentAdmissionCommandIntent,
  signal?: AbortSignal,
): Promise<TournamentAdmissionMutationResult> =>
  readMutationResponse(
    publicClient.POST("/api/v1/tournaments/{tournament_id}/participant/queue", {
      params: {
        path: admissionPath(tournamentId),
        header: mutationHeaders(intent),
      },
      signal,
    }),
  );

export const checkInTournamentAdmission = async (
  tournamentId: string,
  intent: TournamentAdmissionCommandIntent,
  signal?: AbortSignal,
): Promise<TournamentAdmissionMutationResult> =>
  readMutationResponse(
    publicClient.POST(
      "/api/v1/tournaments/{tournament_id}/participant/queue/check-in",
      {
        params: {
          path: admissionPath(tournamentId),
          header: mutationHeaders(intent),
        },
        signal,
      },
    ),
  );

export const cancelTournamentAdmission = async (
  tournamentId: string,
  intent: TournamentAdmissionCommandIntent,
  signal?: AbortSignal,
): Promise<TournamentAdmissionMutationResult> =>
  readMutationResponse(
    publicClient.DELETE("/api/v1/tournaments/{tournament_id}/participant/queue", {
      params: {
        path: admissionPath(tournamentId),
        header: mutationHeaders(intent),
      },
      signal,
    }),
  );

export const tournamentAdmissionApi = {
  getStatus: getTournamentAdmissionStatus,
  join: joinTournamentAdmission,
  checkIn: checkInTournamentAdmission,
  cancel: cancelTournamentAdmission,
} as const;

export const admissionApi = tournamentAdmissionApi;
