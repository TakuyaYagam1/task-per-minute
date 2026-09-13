import { adminClient, ApiError, publicClient, unwrapApi, type ApiResult } from "./client";
import {
  assertApiResponse,
  isGoldenOperatorResponse,
  isGoldenParticipantResponse,
} from "./guards";
import {
  createOperatorCommandIntent,
  type OperatorCommandIntent,
  type OperatorIdempotencyKey,
} from "./tournament-operator";
import {
  createParticipantCommandIntent,
  type ParticipantCommandIntent,
} from "./participant";
import type { components } from "./schema";

export type GoldenRuntimeState = components["schemas"]["GoldenRuntimeState"];
export type GoldenRuntimeMember = components["schemas"]["GoldenRuntimeMember"];
export type GoldenRuntimeTask = components["schemas"]["GoldenRuntimeTask"];
export type GoldenOperatorGroup = components["schemas"]["GoldenOperatorGroup"];
export type GoldenOperatorResponse = components["schemas"]["GoldenOperatorResponse"];
export type GoldenParticipantResponse = components["schemas"]["GoldenParticipantResponse"];
export type GoldenRuntimeConflictProblem = components["schemas"]["GoldenRuntimeConflictProblem"];
export type GoldenOpenRequest = components["schemas"]["GoldenOpenRequest"];
export type GoldenStartRequest = components["schemas"]["GoldenStartRequest"];
export type GoldenReadyRequest = components["schemas"]["GoldenReadyRequest"];
export type GoldenSubmissionRequest = components["schemas"]["GoldenSubmissionRequest"];

export type GoldenOperatorCommandIntent = OperatorCommandIntent;
export type GoldenOperatorIdempotencyKey = OperatorIdempotencyKey;
export type GoldenParticipantCommandIntent = ParticipantCommandIntent;

export type GoldenParticipantMutationResult<T> =
  | {
      status: "success";
      value: T;
    }
  | {
      status: "conflict";
      recovered: true;
      error: ApiError;
      snapshot: GoldenParticipantResponse;
    }
  | {
      status: "rate_limited";
      error: ApiError;
      retryAfter: string | null;
    };

type GoldenMutationHeaders = {
  "Idempotency-Key": string;
  "X-CSRF-Token": "";
};

const idempotencyKeyFrom = (value: OperatorIdempotencyKey): string => {
  const key = typeof value === "string" ? value : value.idempotencyKey;
  if (typeof key !== "string" || key.trim().length === 0) {
    throw new TypeError("Golden operator command requires a non-empty idempotency key");
  }
  return key;
};

const operatorMutationHeaders = (
  intent: OperatorIdempotencyKey,
): GoldenMutationHeaders => ({
  "Idempotency-Key": idempotencyKeyFrom(intent),
  "X-CSRF-Token": "",
});

const participantMutationHeaders = (
  intent: ParticipantCommandIntent,
): GoldenMutationHeaders => {
  if (intent.idempotencyKey.trim().length === 0) {
    throw new TypeError("Golden participant command requires a non-empty idempotency key");
  }
  return {
    "Idempotency-Key": intent.idempotencyKey,
    "X-CSRF-Token": "",
  };
};

const readGoldenResponse = async <T>(
  result: ApiResult<T> | Promise<ApiResult<T>>,
  guard: (value: unknown) => value is T,
  contract: string,
): Promise<T> => {
  const data = await unwrapApi(result, contract);
  return assertApiResponse(data, guard, contract);
};

const participantPath = (tournamentId: string): { tournament_id: string } => ({
  tournament_id: tournamentId,
});

const readGoldenParticipantState = async (
  tournamentId: string,
  signal?: AbortSignal,
): Promise<GoldenParticipantResponse> => readGoldenResponse(
  publicClient.GET("/api/v1/tournaments/{tournament_id}/participant/golden", {
    params: { path: participantPath(tournamentId) },
    signal,
  }),
  isGoldenParticipantResponse,
  "participant Golden state",
);

const readGoldenParticipantMutation = async <T>(
  result: ApiResult<T> | Promise<ApiResult<T>>,
  guard: (value: unknown) => value is T,
  contract: string,
  tournamentId: string,
  signal?: AbortSignal,
): Promise<GoldenParticipantMutationResult<T>> => {
  try {
    const data = await unwrapApi(result, contract);
    return {
      status: "success",
      value: assertApiResponse(data, guard, contract),
    };
  } catch (error) {
    if (!(error instanceof ApiError)) {
      throw error;
    }
    if (error.status === 409) {
      const snapshot = await readGoldenParticipantState(tournamentId, signal);
      return {
        status: "conflict",
        recovered: true,
        error,
        snapshot,
      };
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

export const goldenApi = {
  async getOperatorState(
    tournamentId: string,
    signal?: AbortSignal,
  ): Promise<GoldenOperatorResponse> {
    return readGoldenResponse(
      adminClient.GET("/api/v1/admin/tournaments/{tournament_id}/golden", {
        params: { path: { tournament_id: tournamentId } },
        signal,
      }),
      isGoldenOperatorResponse,
      "admin Golden state",
    );
  },

  async open(
    tournamentId: string,
    body: GoldenOpenRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<GoldenOperatorResponse> {
    return readGoldenResponse(
      adminClient.POST("/api/v1/admin/tournaments/{tournament_id}/golden/open", {
        params: {
          path: { tournament_id: tournamentId },
          header: operatorMutationHeaders(intent),
        },
        body,
        signal,
      }),
      isGoldenOperatorResponse,
      "admin Golden open",
    );
  },

  async start(
    tournamentId: string,
    attemptId: string,
    body: GoldenStartRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<GoldenOperatorResponse> {
    return readGoldenResponse(
      adminClient.POST(
        "/api/v1/admin/tournaments/{tournament_id}/golden/attempts/{attempt_id}/start",
        {
          params: {
            path: {
              tournament_id: tournamentId,
              attempt_id: attemptId,
            },
            header: operatorMutationHeaders(intent),
          },
          body,
          signal,
        },
      ),
      isGoldenOperatorResponse,
      "admin Golden attempt start",
    );
  },

  getParticipantState: readGoldenParticipantState,

  async ready(
    tournamentId: string,
    body: GoldenReadyRequest,
    intent: ParticipantCommandIntent,
    signal?: AbortSignal,
  ): Promise<GoldenParticipantMutationResult<GoldenParticipantResponse>> {
    return readGoldenParticipantMutation(
      publicClient.POST("/api/v1/tournaments/{tournament_id}/participant/golden/ready", {
        params: {
          path: participantPath(tournamentId),
          header: participantMutationHeaders(intent),
        },
        body,
        signal,
      }),
      isGoldenParticipantResponse,
      "participant Golden readiness",
      tournamentId,
      signal,
    );
  },

  async submit(
    tournamentId: string,
    body: GoldenSubmissionRequest,
    intent: ParticipantCommandIntent,
    signal?: AbortSignal,
  ): Promise<GoldenParticipantMutationResult<GoldenParticipantResponse>> {
    return readGoldenParticipantMutation(
      publicClient.POST("/api/v1/tournaments/{tournament_id}/participant/golden/submissions", {
        params: {
          path: participantPath(tournamentId),
          header: participantMutationHeaders(intent),
        },
        body,
        signal,
      }),
      isGoldenParticipantResponse,
      "participant Golden submission",
      tournamentId,
      signal,
    );
  },
} as const;

export const createGoldenOperatorCommandIntent = createOperatorCommandIntent;
export const createGoldenParticipantCommandIntent = createParticipantCommandIntent;
export const getGoldenOperatorState = goldenApi.getOperatorState;
export const openGoldenExecution = goldenApi.open;
export const startGoldenAttempt = goldenApi.start;
export const getGoldenParticipantState = goldenApi.getParticipantState;
export const setGoldenParticipantReady = goldenApi.ready;
export const submitGoldenFlag = goldenApi.submit;
