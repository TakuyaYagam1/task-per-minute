import { CONFIG } from "../config";
import {
  adminCredentialedFetch,
  adminClient,
  ApiError,
  advanceAdminSessionEpoch,
  canResumeAdminSession,
  clearAdminCSRFTokens,
  isCurrentAdminSessionEpoch,
  setAdminRefreshFailureHandler,
  type ProblemDetails,
  unwrapApi,
  unwrapApiVoid,
} from "./client";
import {
  assertApiResponse,
  isAdminPlayer,
  isAdminPlayerArray,
  isAdminPlayerAuditEventArray,
  isAdminSessionResponse,
  isAdminTask,
  isAdminTaskArray,
  isUploadSourceResponse,
} from "./guards";
import type { components } from "./schema";

export type AdminSessionResponse = components["schemas"]["AdminSessionResponse"];
export type AdminPlayer = components["schemas"]["PlayerManagementView"];
export type AdminPlayerAuditEvent = components["schemas"]["PlayerAuditEvent"];
export type AdminTask = components["schemas"]["TaskDetails"];
export type CreateTaskRequest = components["schemas"]["CreateTaskRequest"];
export type UpdateAdminPlayerRequest = components["schemas"]["UpdatePlayerRequest"];
export type UpdateTaskRequest = components["schemas"]["UpdateTaskRequest"];
export type UploadSourceResponse = components["schemas"]["TaskSourceUploadResponse"];

const UPLOAD_SOURCE_TIMEOUT_MS = 5 * 60 * 1000;
export const ADMIN_PLAYERS_CHANGED_EVENT = "players_changed";

// Generated request types require the header at each unsafe endpoint. The
// credentialed fetch layer replaces this placeholder with the current token.
const requiredCSRFHeader = { "X-CSRF-Token": "" } as const;

const adminURL = (path: string): string => `${CONFIG.adminApiUrl}${path}`;

const parseProblem = async (response: Response): Promise<ProblemDetails | undefined> => {
  const contentType = response.headers.get("Content-Type") || "";
  if (!contentType.includes("json")) {
    return undefined;
  }
  try {
    const value = (await response.json()) as Partial<ProblemDetails>;
    if (typeof value.status === "number" && typeof value.title === "string") {
      return value as ProblemDetails;
    }
  } catch {
    return undefined;
  }
  return undefined;
};

type LinkedAbortSignal = {
  signal: AbortSignal;
  cleanup: () => void;
};

const linkSignals = (signals: Array<AbortSignal | undefined>): LinkedAbortSignal => {
  const controller = new AbortController();
  const cleanups: Array<() => void> = [];
  const cleanup = (): void => {
    for (const remove of cleanups.splice(0)) {
      remove();
    }
  };

  for (const signal of signals) {
    if (!signal) {
      continue;
    }
    if (signal.aborted) {
      controller.abort(signal.reason);
      cleanup();
      return { signal: controller.signal, cleanup };
    }
    const onAbort = () => controller.abort(signal.reason);
    signal.addEventListener(
      "abort",
      onAbort,
      { once: true },
    );
    cleanups.push(() => signal.removeEventListener("abort", onAbort));
  }
  return { signal: controller.signal, cleanup };
};

const synthesizeProblem = (status: number, title: string, detail: string): ProblemDetails => ({
  type: "about:blank",
  status,
  title,
  detail,
});

const isAbortLikeError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const adminEventSources = new Set<EventSource>();

const closeAdminEventSources = (): void => {
  for (const source of adminEventSources) {
    source.close();
  }
  adminEventSources.clear();
};

type ClearAdminSessionOptions = {
  preserveCSRF?: boolean;
};

export { canResumeAdminSession };

export const activateAdminSession = (): number => {
  closeAdminEventSources();
  return advanceAdminSessionEpoch();
};

export const clearAdminSession = (options: ClearAdminSessionOptions = {}): void => {
  closeAdminEventSources();
  if (!options.preserveCSRF) {
    clearAdminCSRFTokens();
  }
  advanceAdminSessionEpoch();
};

setAdminRefreshFailureHandler(() => clearAdminSession());

export const adminApi = {
  async login(password: string, signal?: AbortSignal): Promise<AdminSessionResponse> {
    activateAdminSession();
    clearAdminCSRFTokens();
    const data = await unwrapApi(
      await adminClient.POST("/api/v1/admin/login", {
        body: { password },
        signal,
      }),
    );
    return assertApiResponse(data, isAdminSessionResponse, "admin/login");
  },

  async refresh(signal?: AbortSignal): Promise<AdminSessionResponse> {
    const data = await unwrapApi(
      await adminClient.POST("/api/v1/admin/refresh", {
        params: { header: requiredCSRFHeader },
        signal,
      }),
    );
    return assertApiResponse(data, isAdminSessionResponse, "admin/refresh");
  },

  async ensureFreshSession(signal?: AbortSignal): Promise<AdminSessionResponse> {
    return this.refresh(signal);
  },

  async logout(signal?: AbortSignal): Promise<void> {
    // Advance the session before dispatch while preserving the refresh CSRF
    // token that the logout request must carry.
    const logoutEpoch = activateAdminSession();
    try {
      await unwrapApiVoid(
        await adminClient.POST("/api/v1/admin/logout", {
          params: { header: requiredCSRFHeader },
          signal,
        }),
      );
    } finally {
      if (isCurrentAdminSessionEpoch(logoutEpoch)) {
        clearAdminSession();
      }
    }
  },

  async listTasks(signal?: AbortSignal): Promise<AdminTask[]> {
    const data = await unwrapApi(
      await adminClient.GET("/api/v1/admin/tasks", {
        signal,
      }),
    );
    return assertApiResponse(data, isAdminTaskArray, "admin/tasks list");
  },

  async createTask(
    body: CreateTaskRequest,
    signal?: AbortSignal,
  ): Promise<AdminTask> {
    const data = await unwrapApi(
      await adminClient.POST("/api/v1/admin/tasks", {
        params: { header: requiredCSRFHeader },
        body,
        signal,
      }),
    );
    return assertApiResponse(data, isAdminTask, "admin/tasks create");
  },

  async updateTask(
    id: string,
    body: UpdateTaskRequest,
    signal?: AbortSignal,
  ): Promise<AdminTask> {
    const data = await unwrapApi(
      await adminClient.PUT("/api/v1/admin/tasks/{id}", {
        params: { path: { id }, header: requiredCSRFHeader },
        body,
        signal,
      }),
    );
    return assertApiResponse(data, isAdminTask, "admin/tasks update");
  },

  async deleteTask(id: string, signal?: AbortSignal): Promise<void> {
    await unwrapApiVoid(
      await adminClient.DELETE("/api/v1/admin/tasks/{id}", {
        params: { path: { id }, header: requiredCSRFHeader },
        signal,
      }),
    );
  },

  async listPlayers(
    includeDeleted = false,
    signal?: AbortSignal,
  ): Promise<AdminPlayer[]> {
    const data = await unwrapApi(
      await adminClient.GET("/api/v1/admin/players", {
        params: includeDeleted ? { query: { include_deleted: true } } : undefined,
        signal,
      }),
    );
    return assertApiResponse(data, isAdminPlayerArray, "admin/players list");
  },

  async listPlayerAudit(
    id: string,
    limit = 50,
    signal?: AbortSignal,
  ): Promise<AdminPlayerAuditEvent[]> {
    const data = await unwrapApi(
      await adminClient.GET("/api/v1/admin/players/{id}/audit", {
        params: { path: { id }, query: { limit } },
        signal,
      }),
    );
    return assertApiResponse(data, isAdminPlayerAuditEventArray, "admin/players audit");
  },

  async updatePlayer(
    id: string,
    body: UpdateAdminPlayerRequest,
    signal?: AbortSignal,
  ): Promise<AdminPlayer> {
    const data = await unwrapApi(
      await adminClient.PUT("/api/v1/admin/players/{id}", {
        params: { path: { id }, header: requiredCSRFHeader },
        body,
        signal,
      }),
    );
    return assertApiResponse(data, isAdminPlayer, "admin/players update");
  },

  async deletePlayer(id: string, signal?: AbortSignal): Promise<void> {
    await unwrapApiVoid(
      await adminClient.DELETE("/api/v1/admin/players/{id}", {
        params: { path: { id }, header: requiredCSRFHeader },
        signal,
      }),
    );
  },

  openPlayerEvents(): EventSource {
    const source = new EventSource(adminURL("/api/v1/admin/players/events"), {
      withCredentials: true,
    });
    adminEventSources.add(source);
    return source;
  },

  sourceDownloadURL(id: string): string {
    return adminURL(`/api/v1/admin/tasks/${encodeURIComponent(id)}/source`);
  },

  async uploadSource(
    id: string,
    file: File,
    options: { signal?: AbortSignal; timeoutMs?: number } = {},
  ): Promise<UploadSourceResponse> {
    const formData = new FormData();
    formData.append("file", file);

    const timeoutController = new AbortController();
    const timeoutMs = options.timeoutMs ?? UPLOAD_SOURCE_TIMEOUT_MS;
    const timeoutHandle = setTimeout(() => {
      timeoutController.abort(new DOMException("Upload timed out", "TimeoutError"));
    }, timeoutMs);
    const linkedSignal = linkSignals([options.signal, timeoutController.signal]);

    try {
      const response = await adminCredentialedFetch(adminURL(`/api/v1/admin/tasks/${id}/source`), {
        method: "POST",
        credentials: "include",
        body: formData,
        signal: linkedSignal.signal,
      });

      if (!response.ok) {
        throw new ApiError(response, await parseProblem(response));
      }

      const data: unknown = await response.json();
      return assertApiResponse(data, isUploadSourceResponse, "admin/tasks source upload");
    } catch (error) {
      const clientAborted = options.signal?.aborted === true;
      const timeoutAborted = timeoutController.signal.aborted && !clientAborted;
      if (isAbortLikeError(error) || clientAborted || timeoutAborted) {
        const synthetic = new Response(null, {
          status: clientAborted ? 499 : 408,
          statusText: clientAborted ? "Client Closed Request" : "Request Timeout",
        });
        const detail = clientAborted
          ? "Upload was cancelled"
          : `Upload exceeded ${Math.round(timeoutMs / 1000)}s timeout`;
        throw new ApiError(
          synthetic,
          synthesizeProblem(synthetic.status, "Upload aborted", detail),
        );
      }
      throw error;
    } finally {
      linkedSignal.cleanup();
      clearTimeout(timeoutHandle);
    }
  },
};
