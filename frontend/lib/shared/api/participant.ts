import { ApiError, publicClient, unwrapApi, type ApiResult } from "./client";
import {
  assertApiResponse,
  isParticipantAssignmentResponse,
  isParticipantDraft,
  isParticipantLobbyResponse,
  isParticipantOfficialResult,
  isParticipantPostSeriesResponse,
  isParticipantReadyEvent,
  isParticipantRecoverySnapshot,
  isParticipantSourceFileResponse,
  isParticipantSubmissionResponse,
} from "./guards";
import type { components } from "./schema";
import { CONFIG } from "../config";

export type ParticipantLobbyResponse = components["schemas"]["ParticipantLobbyResponse"];
export type ParticipantAssignmentResponse = components["schemas"]["ParticipantAssignmentResponse"];
export type ParticipantSourceFileResponse = components["schemas"]["ParticipantSourceFileResponse"];
export type ParticipantReadyRequest = components["schemas"]["ParticipantReadyRequest"];
export type ParticipantReadyEvent = components["schemas"]["ReadinessEvent"];
export type ParticipantDraftActionRequest = components["schemas"]["ParticipantDraftActionRequest"];
export type ParticipantDraft = components["schemas"]["Draft"];
export type ParticipantSubmissionRequest = components["schemas"]["ParticipantSubmissionRequest"];
export type ParticipantSubmissionResponse = components["schemas"]["ParticipantSubmissionResponse"];
export type ParticipantSurrenderRequest = components["schemas"]["ParticipantSurrenderRequest"];
export type ParticipantOfficialResult = components["schemas"]["OfficialResultRevision"];
export type ParticipantPostSeriesRequest = components["schemas"]["ParticipantPostSeriesRequest"];
export type ParticipantPostSeriesResponse = components["schemas"]["ParticipantPostSeriesResponse"];
export type ParticipantRecoveryCursor = components["schemas"]["ParticipantRecoveryCursor"];
export type ParticipantRecoverySnapshot = components["schemas"]["ParticipantRecoverySnapshot"];

const sourceFileProtocols = new Set(["http:", "https:"]);

const normalizeAllowedOrigin = (value: string): string | null => {
  const candidate = value.trim();
  if (candidate.length === 0) {
    return null;
  }

  try {
    const url = new URL(candidate);
    if (
      !sourceFileProtocols.has(url.protocol) ||
      url.username.length > 0 ||
      url.password.length > 0 ||
      url.pathname !== "/" ||
      url.search.length > 0 ||
      url.hash.length > 0
    ) {
      return null;
    }
    return url.origin;
  } catch {
    return null;
  }
};

const allowedSourceFileOrigins = (): ReadonlySet<string> => {
  if (CONFIG.sourceFileOrigins.length > 0) {
    return new Set(
      CONFIG.sourceFileOrigins
        .map(normalizeAllowedOrigin)
        .filter((origin): origin is string => origin !== null),
    );
  }

  if (typeof window === "undefined") {
    return new Set();
  }

  const currentOrigin = normalizeAllowedOrigin(window.location.origin);
  return currentOrigin === null ? new Set() : new Set([currentOrigin]);
};

export class ParticipantSourceFileURLPolicyError extends Error {
  constructor() {
    super("Participant source file URL failed the browser origin policy");
    this.name = "ParticipantSourceFileURLPolicyError";
  }
}

/**
 * Keep temporary archive URLs out of snapshots and accept only an explicitly
 * configured public origin (or the current browser origin in same-origin mode).
 */
export const validateParticipantSourceFileURL = (value: string): string => {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new ParticipantSourceFileURLPolicyError();
  }

  if (
    !sourceFileProtocols.has(url.protocol) ||
    url.username.length > 0 ||
    url.password.length > 0 ||
    url.hash.length > 0 ||
    !allowedSourceFileOrigins().has(url.origin)
  ) {
    throw new ParticipantSourceFileURLPolicyError();
  }

  return url.toString();
};

export type ParticipantCommandIntent = Readonly<{
  idempotencyKey: string;
}>;

export type ParticipantMutationResult<T> =
  | {
      status: "success";
      value: T;
    }
  | {
      status: "conflict";
      recovered: true;
      error: ApiError;
      snapshot: ParticipantRecoverySnapshot;
    }
  | {
      status: "rate_limited";
      error: ApiError;
      retryAfter: string | null;
    };

type ParticipantMutationHeaders = {
  "Idempotency-Key": string;
  "X-CSRF-Token": "";
};

const UUID_VERSION_MASK = 0x0f;
const UUID_VARIANT_MASK = 0x3f;

const createRandomUUID = (): string => {
  const bytes = new Uint8Array(16);
  const cryptoSource = globalThis.crypto;
  if (cryptoSource?.getRandomValues) {
    cryptoSource.getRandomValues(bytes);
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256);
    }
  }
  bytes[6] = (bytes[6] & UUID_VERSION_MASK) | 0x40;
  bytes[8] = (bytes[8] & UUID_VARIANT_MASK) | 0x80;
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
};

/** Create one in-memory command intent. The key is reused only by this object. */
export const createParticipantCommandIntent = (): ParticipantCommandIntent =>
  Object.freeze({ idempotencyKey: createRandomUUID() });

const mutationHeaders = (intent: ParticipantCommandIntent): ParticipantMutationHeaders => {
  if (intent.idempotencyKey.trim().length === 0) {
    throw new TypeError("Participant command intent requires a non-empty idempotency key");
  }
  return {
    "Idempotency-Key": intent.idempotencyKey,
    "X-CSRF-Token": "",
  };
};

const readParticipantResponse = async <T>(
  result: ApiResult<T> | Promise<ApiResult<T>>,
  guard: (value: unknown) => value is T,
  contract: string,
): Promise<T> => {
  const data = await unwrapApi(result, contract);
  return assertApiResponse(data, guard, contract);
};

const readParticipantMutationResponse = async <T>(
  result: ApiResult<T> | Promise<ApiResult<T>>,
  guard: (value: unknown) => value is T,
  contract: string,
  tournamentId: string,
  signal?: AbortSignal,
): Promise<ParticipantMutationResult<T>> => {
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
      const snapshot = await participantApi.getSnapshot(tournamentId, undefined, signal);
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

const participantPath = (tournamentId: string): { tournament_id: string } => ({
  tournament_id: tournamentId,
});

export const participantApi = {
  async getLobby(tournamentId: string, signal?: AbortSignal): Promise<ParticipantLobbyResponse> {
    return readParticipantResponse(
      publicClient.GET("/api/v1/tournaments/{tournament_id}/participant/lobby", {
        params: { path: participantPath(tournamentId) },
        signal,
      }),
      isParticipantLobbyResponse,
      "participant lobby",
    );
  },

  async getAssignment(
    tournamentId: string,
    assignmentId: string,
    signal?: AbortSignal,
  ): Promise<ParticipantAssignmentResponse> {
    return readParticipantResponse(
      publicClient.GET("/api/v1/tournaments/{tournament_id}/participant/assignments/{assignment_id}", {
        params: {
          path: {
            ...participantPath(tournamentId),
            assignment_id: assignmentId,
          },
        },
        signal,
      }),
      isParticipantAssignmentResponse,
      "participant assignment",
    );
  },

  async getAssignmentSourceFile(
    tournamentId: string,
    assignmentId: string,
    signal?: AbortSignal,
  ): Promise<ParticipantSourceFileResponse> {
    const response = await readParticipantResponse(
      publicClient.GET(
        "/api/v1/tournaments/{tournament_id}/participant/assignments/{assignment_id}/source-file",
        {
          params: {
            path: {
              ...participantPath(tournamentId),
              assignment_id: assignmentId,
            },
          },
          signal,
        },
      ),
      isParticipantSourceFileResponse,
      "participant assignment source file",
    );
    return {
      ...response,
      source_file_url: validateParticipantSourceFileURL(response.source_file_url),
    };
  },

  async getSnapshot(
    tournamentId: string,
    cursor?: ParticipantRecoveryCursor,
    signal?: AbortSignal,
  ): Promise<ParticipantRecoverySnapshot> {
    const params = cursor === undefined
      ? { path: participantPath(tournamentId) }
      : { path: participantPath(tournamentId), query: { cursor } };
    return readParticipantResponse(
      publicClient.GET("/api/v1/tournaments/{tournament_id}/participant/snapshot", {
        params,
        signal,
      }),
      isParticipantRecoverySnapshot,
      "participant recovery snapshot",
    );
  },

  async ready(
    tournamentId: string,
    waveId: string,
    body: ParticipantReadyRequest,
    intent: ParticipantCommandIntent,
    signal?: AbortSignal,
  ): Promise<ParticipantMutationResult<ParticipantReadyEvent>> {
    return readParticipantMutationResponse(
      publicClient.POST("/api/v1/tournaments/{tournament_id}/participant/waves/{wave_id}/ready", {
        params: {
          path: {
            ...participantPath(tournamentId),
            wave_id: waveId,
          },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      isParticipantReadyEvent,
      "participant readiness",
      tournamentId,
      signal,
    );
  },

  async draftAction(
    tournamentId: string,
    seriesId: string,
    body: ParticipantDraftActionRequest,
    intent: ParticipantCommandIntent,
    signal?: AbortSignal,
  ): Promise<ParticipantMutationResult<ParticipantDraft>> {
    return readParticipantMutationResponse(
      publicClient.POST("/api/v1/tournaments/{tournament_id}/participant/series/{series_id}/draft/actions", {
        params: {
          path: {
            ...participantPath(tournamentId),
            series_id: seriesId,
          },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      isParticipantDraft,
      "participant draft action",
      tournamentId,
      signal,
    );
  },

  async submitFlag(
    tournamentId: string,
    seriesId: string,
    gameId: string,
    body: ParticipantSubmissionRequest,
    intent: ParticipantCommandIntent,
    signal?: AbortSignal,
  ): Promise<ParticipantMutationResult<ParticipantSubmissionResponse>> {
    return readParticipantMutationResponse(
      publicClient.POST(
        "/api/v1/tournaments/{tournament_id}/participant/series/{series_id}/games/{game_id}/submissions",
        {
          params: {
            path: {
              ...participantPath(tournamentId),
              series_id: seriesId,
              game_id: gameId,
            },
            header: mutationHeaders(intent),
          },
          body,
          signal,
        },
      ),
      isParticipantSubmissionResponse,
      "participant flag submission",
      tournamentId,
      signal,
    );
  },

  async surrender(
    tournamentId: string,
    seriesId: string,
    body: ParticipantSurrenderRequest,
    intent: ParticipantCommandIntent,
    signal?: AbortSignal,
  ): Promise<ParticipantMutationResult<ParticipantOfficialResult>> {
    return readParticipantMutationResponse(
      publicClient.POST("/api/v1/tournaments/{tournament_id}/participant/series/{series_id}/surrender", {
        params: {
          path: {
            ...participantPath(tournamentId),
            series_id: seriesId,
          },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      isParticipantOfficialResult,
      "participant surrender",
      tournamentId,
      signal,
    );
  },

  async postSeries(
    tournamentId: string,
    seriesId: string,
    body: ParticipantPostSeriesRequest,
    intent: ParticipantCommandIntent,
    signal?: AbortSignal,
  ): Promise<ParticipantMutationResult<ParticipantPostSeriesResponse>> {
    return readParticipantMutationResponse(
      publicClient.POST("/api/v1/tournaments/{tournament_id}/participant/series/{series_id}/post-series", {
        params: {
          path: {
            ...participantPath(tournamentId),
            series_id: seriesId,
          },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      isParticipantPostSeriesResponse,
      "participant post-series action",
      tournamentId,
      signal,
    );
  },
} as const;

export const getParticipantLobby = participantApi.getLobby;
export const getParticipantAssignment = participantApi.getAssignment;
export const getParticipantAssignmentSourceFile = participantApi.getAssignmentSourceFile;
export const getParticipantSnapshot = participantApi.getSnapshot;
export const setParticipantReady = participantApi.ready;
export const submitParticipantDraftAction = participantApi.draftAction;
export const submitParticipantFlag = participantApi.submitFlag;
export const surrenderParticipantSeries = participantApi.surrender;
export const applyParticipantPostSeriesAction = participantApi.postSeries;
