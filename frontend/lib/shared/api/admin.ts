import { CONFIG } from "../config";
import { log } from "../lib/logger";
import {
  adminClient,
  ApiError,
  clearAdminCSRFTokens,
  credentialedFetch,
  type ProblemDetails,
  unwrapApi,
  unwrapApiVoid,
} from "./client";
import {
  assertApiResponse,
  isAdminPlayer,
  isAdminPlayerArray,
  isAdminPlayerAuditEventArray,
  isAdminTask,
  isAdminTaskArray,
  isAdminTokenResponse,
  isUploadSourceResponse,
} from "./guards";
import type { components } from "./schema";

export type AdminTokenResponse = components["schemas"]["AdminTokenResponse"];
export type AdminPlayer = components["schemas"]["AdminPlayerResponse"];
export type AdminPlayerAuditEvent = components["schemas"]["AdminPlayerAuditEventResponse"];
export type AdminTask = components["schemas"]["TaskResponse"];
export type CreateTaskRequest = components["schemas"]["CreateTaskRequest"];
export type UpdateAdminPlayerRequest = components["schemas"]["UpdateAdminPlayerRequest"];
export type UpdateTaskRequest = components["schemas"]["UpdateTaskRequest"];
export type UploadSourceResponse = components["schemas"]["UploadSourceResponse"];

const SESSION_MARKER_KEY = "admin_session_active";
const LEGACY_ACCESS_TOKEN_KEY = "admin_access_token";
const LEGACY_REFRESH_TOKEN_KEY = "admin_refresh_token";
const COOKIE_SESSION_TOKEN = "__cookie_admin_session__";

const UPLOAD_SOURCE_TIMEOUT_MS = 5 * 60 * 1000;
export const ADMIN_PLAYERS_CHANGED_EVENT = "players_changed";

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

const cookieSessionTokens = (expiresIn = 0): AdminTokenResponse => ({
  access_token: COOKIE_SESSION_TOKEN,
  refresh_token: COOKIE_SESSION_TOKEN,
  token_type: "Bearer",
  expires_in: expiresIn,
});

const clearLegacyAdminTokens = (): void => {
  sessionStorage.removeItem(LEGACY_ACCESS_TOKEN_KEY);
  sessionStorage.removeItem(LEGACY_REFRESH_TOKEN_KEY);
};

type ClearAdminSessionOptions = {
  preserveCSRF?: boolean;
};

export const adminSession = {
  load(): AdminTokenResponse | null {
    try {
      clearLegacyAdminTokens();
      if (sessionStorage.getItem(SESSION_MARKER_KEY) !== "1") {
        return null;
      }
      return cookieSessionTokens();
    } catch (error) {
      log.warn("adminSession.load: sessionStorage unavailable", error);
      return null;
    }
  },

  save(_tokens: AdminTokenResponse): void {
    try {
      clearLegacyAdminTokens();
      sessionStorage.setItem(SESSION_MARKER_KEY, "1");
    } catch (error) {
      log.warn("adminSession.save: sessionStorage write failed", error);
    }
  },

  clear(options: ClearAdminSessionOptions = {}): void {
    try {
      sessionStorage.removeItem(SESSION_MARKER_KEY);
      clearLegacyAdminTokens();
      if (!options.preserveCSRF) {
        clearAdminCSRFTokens();
      }
    } catch (error) {
      log.warn("adminSession.clear: sessionStorage remove failed", error);
    }
  },
};

export const adminApi = {
  async login(password: string, signal?: AbortSignal): Promise<AdminTokenResponse> {
    const data = await unwrapApi(
      await adminClient.POST("/api/v1/admin/login", {
        body: { password },
        signal,
      }),
    );
    const tokens = assertApiResponse(data, isAdminTokenResponse, "admin/login");
    return cookieSessionTokens(tokens.expires_in);
  },

  async refresh(_refreshToken: string, signal?: AbortSignal): Promise<AdminTokenResponse> {
    const data = await unwrapApi(
      await adminClient.POST("/api/v1/admin/refresh", {
        body: { refresh_token: "" },
        signal,
      }),
    );
    const tokens = assertApiResponse(data, isAdminTokenResponse, "admin/refresh");
    return cookieSessionTokens(tokens.expires_in);
  },

  async logout(_accessToken: string, _refreshToken: string, signal?: AbortSignal): Promise<void> {
    await unwrapApiVoid(
      await adminClient.POST("/api/v1/admin/logout", {
        body: { refresh_token: "" },
        signal,
      }),
    );
  },

  async listTasks(_accessToken: string, signal?: AbortSignal): Promise<AdminTask[]> {
    const data = await unwrapApi(
      await adminClient.GET("/api/v1/admin/tasks", {
        signal,
      }),
    );
    return assertApiResponse(data, isAdminTaskArray, "admin/tasks list");
  },

  async createTask(
    _accessToken: string,
    body: CreateTaskRequest,
    signal?: AbortSignal,
  ): Promise<AdminTask> {
    const data = await unwrapApi(
      await adminClient.POST("/api/v1/admin/tasks", {
        body,
        signal,
      }),
    );
    return assertApiResponse(data, isAdminTask, "admin/tasks create");
  },

  async updateTask(
    _accessToken: string,
    id: string,
    body: UpdateTaskRequest,
    signal?: AbortSignal,
  ): Promise<AdminTask> {
    const data = await unwrapApi(
      await adminClient.PUT("/api/v1/admin/tasks/{id}", {
        params: { path: { id } },
        body,
        signal,
      }),
    );
    return assertApiResponse(data, isAdminTask, "admin/tasks update");
  },

  async deleteTask(_accessToken: string, id: string, signal?: AbortSignal): Promise<void> {
    await unwrapApiVoid(
      await adminClient.DELETE("/api/v1/admin/tasks/{id}", {
        params: { path: { id } },
        signal,
      }),
    );
  },

  async listPlayers(
    _accessToken: string,
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
    _accessToken: string,
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
    _accessToken: string,
    id: string,
    body: UpdateAdminPlayerRequest,
    signal?: AbortSignal,
  ): Promise<AdminPlayer> {
    const data = await unwrapApi(
      await adminClient.PUT("/api/v1/admin/players/{id}", {
        params: { path: { id } },
        body,
        signal,
      }),
    );
    return assertApiResponse(data, isAdminPlayer, "admin/players update");
  },

  async deletePlayer(_accessToken: string, id: string, signal?: AbortSignal): Promise<void> {
    await unwrapApiVoid(
      await adminClient.DELETE("/api/v1/admin/players/{id}", {
        params: { path: { id } },
        signal,
      }),
    );
  },

  openPlayerEvents(): EventSource {
    return new EventSource(adminURL("/api/v1/admin/players/events"), {
      withCredentials: true,
    });
  },

  async uploadSource(
    _accessToken: string,
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
      const response = await credentialedFetch(adminURL(`/api/v1/admin/tasks/${id}/source`), {
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
