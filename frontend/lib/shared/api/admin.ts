import { CONFIG } from "../config";
import { log } from "../lib/logger";
import { adminClient, ApiError, type ProblemDetails, unwrapApi, unwrapApiVoid } from "./client";
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

const ACCESS_TOKEN_KEY = "admin_access_token";
const REFRESH_TOKEN_KEY = "admin_refresh_token";

const UPLOAD_SOURCE_TIMEOUT_MS = 5 * 60 * 1000;

const adminHeaders = (accessToken: string): Record<string, string> => ({
  Authorization: `Bearer ${accessToken}`,
});

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

const linkSignals = (signals: Array<AbortSignal | undefined>): AbortSignal => {
  const controller = new AbortController();
  for (const signal of signals) {
    if (!signal) {
      continue;
    }
    if (signal.aborted) {
      controller.abort(signal.reason);
      return controller.signal;
    }
    signal.addEventListener(
      "abort",
      () => controller.abort(signal.reason),
      { once: true },
    );
  }
  return controller.signal;
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

export const adminSession = {
  load(): AdminTokenResponse | null {
    try {
      const access_token = sessionStorage.getItem(ACCESS_TOKEN_KEY);
      const refresh_token = sessionStorage.getItem(REFRESH_TOKEN_KEY);
      if (!access_token || !refresh_token) {
        return null;
      }
      return {
        access_token,
        refresh_token,
        token_type: "Bearer",
        expires_in: 0,
      };
    } catch (error) {
      log.warn("adminSession.load: sessionStorage unavailable", error);
      return null;
    }
  },

  save(tokens: AdminTokenResponse): void {
    try {
      sessionStorage.setItem(ACCESS_TOKEN_KEY, tokens.access_token);
      sessionStorage.setItem(REFRESH_TOKEN_KEY, tokens.refresh_token);
    } catch (error) {
      log.warn("adminSession.save: sessionStorage write failed", error);
    }
  },

  clear(): void {
    try {
      sessionStorage.removeItem(ACCESS_TOKEN_KEY);
      sessionStorage.removeItem(REFRESH_TOKEN_KEY);
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
    return assertApiResponse(data, isAdminTokenResponse, "admin/login");
  },

  async refresh(refreshToken: string, signal?: AbortSignal): Promise<AdminTokenResponse> {
    const data = await unwrapApi(
      await adminClient.POST("/api/v1/admin/refresh", {
        body: { refresh_token: refreshToken },
        signal,
      }),
    );
    return assertApiResponse(data, isAdminTokenResponse, "admin/refresh");
  },

  async logout(accessToken: string, refreshToken: string, signal?: AbortSignal): Promise<void> {
    await unwrapApiVoid(
      await adminClient.POST("/api/v1/admin/logout", {
        headers: adminHeaders(accessToken),
        body: { refresh_token: refreshToken },
        signal,
      }),
    );
  },

  async listTasks(accessToken: string, signal?: AbortSignal): Promise<AdminTask[]> {
    const data = await unwrapApi(
      await adminClient.GET("/api/v1/admin/tasks", {
        headers: adminHeaders(accessToken),
        signal,
      }),
    );
    return assertApiResponse(data, isAdminTaskArray, "admin/tasks list");
  },

  async createTask(
    accessToken: string,
    body: CreateTaskRequest,
    signal?: AbortSignal,
  ): Promise<AdminTask> {
    const data = await unwrapApi(
      await adminClient.POST("/api/v1/admin/tasks", {
        headers: adminHeaders(accessToken),
        body,
        signal,
      }),
    );
    return assertApiResponse(data, isAdminTask, "admin/tasks create");
  },

  async updateTask(
    accessToken: string,
    id: string,
    body: UpdateTaskRequest,
    signal?: AbortSignal,
  ): Promise<AdminTask> {
    const data = await unwrapApi(
      await adminClient.PUT("/api/v1/admin/tasks/{id}", {
        params: { path: { id } },
        headers: adminHeaders(accessToken),
        body,
        signal,
      }),
    );
    return assertApiResponse(data, isAdminTask, "admin/tasks update");
  },

  async deleteTask(accessToken: string, id: string, signal?: AbortSignal): Promise<void> {
    await unwrapApiVoid(
      await adminClient.DELETE("/api/v1/admin/tasks/{id}", {
        params: { path: { id } },
        headers: adminHeaders(accessToken),
        signal,
      }),
    );
  },

  async listPlayers(
    accessToken: string,
    includeDeleted = false,
    signal?: AbortSignal,
  ): Promise<AdminPlayer[]> {
    const data = await unwrapApi(
      await adminClient.GET("/api/v1/admin/players", {
        params: includeDeleted ? { query: { include_deleted: true } } : undefined,
        headers: adminHeaders(accessToken),
        signal,
      }),
    );
    return assertApiResponse(data, isAdminPlayerArray, "admin/players list");
  },

  async listPlayerAudit(
    accessToken: string,
    id: string,
    limit = 50,
    signal?: AbortSignal,
  ): Promise<AdminPlayerAuditEvent[]> {
    const data = await unwrapApi(
      await adminClient.GET("/api/v1/admin/players/{id}/audit", {
        params: { path: { id }, query: { limit } },
        headers: adminHeaders(accessToken),
        signal,
      }),
    );
    return assertApiResponse(data, isAdminPlayerAuditEventArray, "admin/players audit");
  },

  async updatePlayer(
    accessToken: string,
    id: string,
    body: UpdateAdminPlayerRequest,
    signal?: AbortSignal,
  ): Promise<AdminPlayer> {
    const data = await unwrapApi(
      await adminClient.PUT("/api/v1/admin/players/{id}", {
        params: { path: { id } },
        headers: adminHeaders(accessToken),
        body,
        signal,
      }),
    );
    return assertApiResponse(data, isAdminPlayer, "admin/players update");
  },

  async deletePlayer(accessToken: string, id: string, signal?: AbortSignal): Promise<void> {
    await unwrapApiVoid(
      await adminClient.DELETE("/api/v1/admin/players/{id}", {
        params: { path: { id } },
        headers: adminHeaders(accessToken),
        signal,
      }),
    );
  },

  async uploadSource(
    accessToken: string,
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

    try {
      const response = await fetch(adminURL(`/api/v1/admin/tasks/${id}/source`), {
        method: "POST",
        headers: adminHeaders(accessToken),
        body: formData,
        signal: linkSignals([options.signal, timeoutController.signal]),
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
      clearTimeout(timeoutHandle);
    }
  },
};
