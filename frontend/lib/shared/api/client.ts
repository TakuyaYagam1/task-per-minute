import createClient from "openapi-fetch";

import { CONFIG } from "../config";
import { ApiContractError, isAdminSessionResponse } from "./guards";
import type { components, paths } from "./schema";

export type ProblemDetails = components["schemas"]["ProblemDetails"];

export type ApiErrorKind =
  | "transport"
  | "unauthorized"
  | "forbidden"
  | "not_found"
  | "conflict"
  | "validation"
  | "rate_limited"
  | "http";

const apiErrorKindForStatus = (status: number): ApiErrorKind => {
  switch (status) {
    case 401:
      return "unauthorized";
    case 403:
      return "forbidden";
    case 404:
      return "not_found";
    case 409:
      return "conflict";
    case 422:
      return "validation";
    case 429:
      return "rate_limited";
    case 0:
      return "transport";
    default:
      return "http";
  }
};

export class ApiError extends Error {
  readonly status: number;
  readonly kind: ApiErrorKind;
  readonly problem?: ProblemDetails;
  readonly retryAfter?: string | null;

  constructor(
    response: Response | null | undefined,
    problem?: ProblemDetails,
    cause?: unknown,
  ) {
    const status = response?.status ?? 0;
    super(
      problem?.detail || problem?.title || (status > 0 ? `HTTP ${status}` : "Network error"),
    );
    this.name = "ApiError";
    this.status = status;
    this.kind = apiErrorKindForStatus(status);
    this.problem = problem;
    this.retryAfter = response?.headers.get("Retry-After") ?? null;
    if (cause !== undefined) {
      this.cause = cause;
    }
  }
}

export type ApiResult<T> = {
  data?: T;
  error?: unknown;
  response: Response;
};

const CSRF_COOKIE_NAME = "tpm_player_csrf";
const ADMIN_ACCESS_CSRF_COOKIE_NAME = "tpm_admin_access_csrf";
const ADMIN_REFRESH_CSRF_COOKIE_NAME = "tpm_admin_refresh_csrf";
const CSRF_HEADER_NAME = "X-CSRF-Token";
const ADMIN_REFRESH_CSRF_HEADER_NAME = "X-Admin-Refresh-CSRF-Token";

type SessionRole = "player" | "admin";

type LinkedAbortSignal = {
  signal: AbortSignal;
  cleanup: () => void;
};

let playerCSRFToken: string | null = null;
let adminAccessCSRFToken: string | null = null;
let adminRefreshCSRFToken: string | null = null;

let playerSessionEpoch = 0;
let playerSessionController = new AbortController();

type AdminRefreshFlight = {
  epoch: number;
  controller: AbortController;
  promise: Promise<boolean>;
};

let adminRefreshFlight: AdminRefreshFlight | null = null;
let adminSessionEpoch = 0;
let adminSessionController = new AbortController();

const problemFromUnknown = (value: unknown): ProblemDetails | undefined => {
  if (!value || typeof value !== "object") {
    return undefined;
  }
  const candidate = value as Partial<ProblemDetails>;
  if (typeof candidate.status === "number" && typeof candidate.title === "string") {
    return candidate as ProblemDetails;
  }
  return undefined;
};

const isUnsafeMethod = (method: string): boolean => {
  const normalized = method.toUpperCase();
  return normalized !== "GET" && normalized !== "HEAD" && normalized !== "OPTIONS" && normalized !== "TRACE";
};

const readCookie = (name: string): string | null => {
  if (typeof document === "undefined") {
    return null;
  }
  const prefix = `${encodeURIComponent(name)}=`;
  const parts = document.cookie ? document.cookie.split(";") : [];
  for (const part of parts) {
    const trimmed = part.trim();
    if (trimmed.startsWith(prefix)) {
      try {
        return decodeURIComponent(trimmed.slice(prefix.length));
      } catch {
        return null;
      }
    }
  }
  return null;
};

const clearReadableCookie = (name: string): void => {
  if (typeof document === "undefined") {
    return;
  }
  try {
    document.cookie = `${encodeURIComponent(name)}=; Max-Age=0; Path=/`;
  } catch {
    // A cross-origin API cookie is cleared by the backend response instead.
  }
};

const tokenFromMemoryOrCookie = (
  memoryToken: string | null,
  cookieName: string,
): string | null => memoryToken || readCookie(cookieName);

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
    const onAbort = (): void => controller.abort(signal.reason);
    signal.addEventListener("abort", onAbort, { once: true });
    cleanups.push(() => signal.removeEventListener("abort", onAbort));
  }
  return { signal: controller.signal, cleanup };
};

const isAbortLikeError = (value: unknown): boolean =>
  value instanceof DOMException &&
  (value.name === "AbortError" || value.name === "TimeoutError");

const normalizeTransportError = (error: unknown): unknown => {
  if (isAbortLikeError(error) || error instanceof ApiError) {
    return error;
  }
  return new ApiError(null, undefined, error);
};

export const clearPlayerCSRFTokens = (): void => {
  playerCSRFToken = null;
  clearReadableCookie(CSRF_COOKIE_NAME);
};

export const clearAdminCSRFTokens = (): void => {
  adminAccessCSRFToken = null;
  adminRefreshCSRFToken = null;
  clearReadableCookie(ADMIN_ACCESS_CSRF_COOKIE_NAME);
  clearReadableCookie(ADMIN_REFRESH_CSRF_COOKIE_NAME);
};

let adminRefreshFailureHandler: (() => void) | null = null;

export const setAdminRefreshFailureHandler = (
  handler: (() => void) | null,
): void => {
  adminRefreshFailureHandler = handler;
};

export const advanceAdminSessionEpoch = (): number => {
  adminSessionEpoch += 1;
  adminSessionController.abort();
  adminSessionController = new AbortController();

  const refreshFlight = adminRefreshFlight;
  adminRefreshFlight = null;
  refreshFlight?.controller.abort();
  return adminSessionEpoch;
};

export const advancePlayerSessionEpoch = (): number => {
  playerSessionEpoch += 1;
  playerSessionController.abort();
  playerSessionController = new AbortController();
  return playerSessionEpoch;
};

export const isCurrentAdminSessionEpoch = (epoch: number): boolean =>
  epoch === adminSessionEpoch;

export const isCurrentPlayerSessionEpoch = (epoch: number): boolean =>
  epoch === playerSessionEpoch;

const readPlayerCSRFToken = (): string | null =>
  tokenFromMemoryOrCookie(playerCSRFToken, CSRF_COOKIE_NAME);

const readAdminAccessCSRFToken = (): string | null =>
  tokenFromMemoryOrCookie(adminAccessCSRFToken, ADMIN_ACCESS_CSRF_COOKIE_NAME);

const readAdminRefreshCSRFToken = (): string | null =>
  tokenFromMemoryOrCookie(adminRefreshCSRFToken, ADMIN_REFRESH_CSRF_COOKIE_NAME);

export const canResumeAdminSession = (): boolean =>
  readAdminRefreshCSRFToken() !== null;

const isPlayerScopedPath = (pathname: string): boolean =>
  pathname.startsWith("/api/v1/players/")
  || /^\/api\/v1\/tournaments\/[^/]+\/participant(?:\/|$)/.test(pathname);

const sessionRoleForPath = (pathname: string): SessionRole | null => {
  if (pathname.startsWith("/api/v1/admin/")) {
    return "admin";
  }
  if (isPlayerScopedPath(pathname)) {
    return "player";
  }
  return null;
};

const isSessionTerminationPath = (pathname: string): boolean =>
  pathname === "/api/v1/admin/logout" || pathname === "/api/v1/players/logout";

const sessionEpochForRole = (role: SessionRole | null): number => {
  if (role === "admin") {
    return adminSessionEpoch;
  }
  if (role === "player") {
    return playerSessionEpoch;
  }
  return 0;
};

const sessionSignalForRole = (role: SessionRole | null): AbortSignal | undefined => {
  if (role === "admin") {
    return adminSessionController.signal;
  }
  if (role === "player") {
    return playerSessionController.signal;
  }
  return undefined;
};

const csrfTokenForRequest = (request: Request): string | null => {
  const pathname = new URL(request.url).pathname;
  if (pathname === "/api/v1/admin/refresh" || pathname === "/api/v1/admin/logout") {
    return readAdminRefreshCSRFToken();
  }
  if (pathname.startsWith("/api/v1/admin/") && pathname !== "/api/v1/admin/login") {
    return readAdminAccessCSRFToken();
  }
  if (isPlayerScopedPath(pathname)) {
    return readPlayerCSRFToken();
  }
  return null;
};

const isCurrentSessionEpoch = (role: SessionRole | null, epoch: number): boolean => {
  if (role === "admin") {
    return epoch === adminSessionEpoch;
  }
  if (role === "player") {
    return epoch === playerSessionEpoch;
  }
  return true;
};

const syncCSRFTokenFromResponse = (
  request: Request,
  response: Response,
  role: SessionRole | null,
  epoch: number,
): void => {
  if (!isCurrentSessionEpoch(role, epoch)) {
    return;
  }

  const pathname = new URL(request.url).pathname;
  if (pathname === "/api/v1/admin/login" || pathname === "/api/v1/admin/refresh") {
    if (!response.ok) {
      return;
    }
    const adminAccessToken = response.headers.get(CSRF_HEADER_NAME);
    if (adminAccessToken) {
      adminAccessCSRFToken = adminAccessToken;
    }
    const adminRefreshToken = response.headers.get(ADMIN_REFRESH_CSRF_HEADER_NAME);
    if (adminRefreshToken) {
      adminRefreshCSRFToken = adminRefreshToken;
    }
    return;
  }
  if (pathname === "/api/v1/admin/logout") {
    if (response.ok) {
      clearAdminCSRFTokens();
    }
    return;
  }
  if (role !== "player") {
    return;
  }
  if (pathname === "/api/v1/players/logout") {
    if (response.ok) {
      clearPlayerCSRFTokens();
    }
    return;
  }
  if (response.ok) {
    const token = response.headers.get(CSRF_HEADER_NAME);
    if (token) {
      playerCSRFToken = token;
    }
  }
};

const requestBaseURL = (): string => {
  if (typeof window !== "undefined" && window.location?.origin) {
    return window.location.origin;
  }
  return "http://localhost";
};

const requestFromInput = (input: RequestInfo | URL, init?: RequestInit): Request => {
  if (typeof input === "string" && input.startsWith("/")) {
    return new Request(new URL(input, requestBaseURL()), init);
  }
  return new Request(input, init);
};

export const credentialedFetch: typeof fetch = async (input, init) => {
  const request = requestFromInput(input, init);
  const pathname = new URL(request.url).pathname;
  const role = sessionRoleForPath(pathname);
  const epoch = sessionEpochForRole(role);
  const headers = new Headers(request.headers);
  if (isUnsafeMethod(request.method)) {
    const csrfToken = csrfTokenForRequest(request);
    if (csrfToken && !headers.get(CSRF_HEADER_NAME)) {
      headers.set(CSRF_HEADER_NAME, csrfToken);
    }
  }
  const shouldFence = role !== null && !isSessionTerminationPath(pathname);
  const linkedSignal = shouldFence
    ? linkSignals([request.signal, sessionSignalForRole(role)])
    : { signal: request.signal, cleanup: () => {} };
  const credentialedRequest = new Request(request, {
    credentials: "include",
    headers,
    signal: linkedSignal.signal,
  });
  try {
    const response = await fetch(credentialedRequest);
    syncCSRFTokenFromResponse(credentialedRequest, response, role, epoch);
    return response;
  } catch (error) {
    throw normalizeTransportError(error);
  } finally {
    linkedSignal.cleanup();
  }
};

const isAdminRefreshableRequest = (request: Request): boolean => {
  const pathname = new URL(request.url).pathname;
  if (!pathname.startsWith("/api/v1/admin/")) {
    return false;
  }
  return ![
    "/api/v1/admin/login",
    "/api/v1/admin/refresh",
    "/api/v1/admin/logout",
  ].includes(pathname);
};

const refreshAdminSession = async (
  epoch: number,
  signal: AbortSignal,
): Promise<boolean> => {
  if (epoch !== adminSessionEpoch) {
    return false;
  }

  try {
    const response = await credentialedFetch(`${CONFIG.adminApiUrl}/api/v1/admin/refresh`, {
      method: "POST",
      signal,
    });
    const body: unknown = await response.clone().json().catch(() => null);
    if (epoch === adminSessionEpoch && response.ok && isAdminSessionResponse(body)) {
      return true;
    }
  } catch {
    // The original 401 is the response the caller should see.
  }

  if (epoch === adminSessionEpoch) {
    clearAdminCSRFTokens();
    adminRefreshFailureHandler?.();
  }
  return false;
};

const ensureAdminSessionFresh = (epoch: number): Promise<boolean> => {
  if (epoch !== adminSessionEpoch) {
    return Promise.resolve(false);
  }
  if (adminRefreshFlight?.epoch === epoch) {
    return adminRefreshFlight.promise;
  }

  const controller = new AbortController();
  const promise = refreshAdminSession(epoch, controller.signal).finally(() => {
    if (adminRefreshFlight?.promise === promise) {
      adminRefreshFlight = null;
    }
  });
  adminRefreshFlight = {
    epoch,
    controller,
    promise,
  };
  return promise;
};

export const adminCredentialedFetch: typeof fetch = async (input, init) => {
  const request = requestFromInput(input, init);
  const retryRequest = request.clone();
  const requestSessionEpoch = adminSessionEpoch;
  const response = await credentialedFetch(request);
  if (response.status !== 401 || !isAdminRefreshableRequest(request)) {
    return response;
  }

  if (request.signal.aborted || adminSessionEpoch !== requestSessionEpoch) {
    return response;
  }

  const refreshed = await ensureAdminSessionFresh(requestSessionEpoch);
  if (!refreshed || adminSessionEpoch !== requestSessionEpoch) {
    return response;
  }
  return credentialedFetch(retryRequest);
};

export const publicClient = createClient<paths>({
  baseUrl: CONFIG.apiUrl,
  fetch: credentialedFetch,
});

export const adminClient = createClient<paths>({
  baseUrl: CONFIG.adminApiUrl,
  fetch: adminCredentialedFetch,
});

const emptyResponseProblem = (response: Response): ProblemDetails => ({
  type: "about:blank",
  status: response.status,
  title: "Empty response body",
  detail: "Server returned a successful status without expected response data.",
});

const normalizeRequestError = (error: unknown, contract?: string): unknown => {
  if (isAbortLikeError(error) || error instanceof ApiError || error instanceof ApiContractError) {
    return error;
  }
  if (error instanceof SyntaxError) {
    return new ApiContractError(contract || "JSON response");
  }
  return normalizeTransportError(error);
};

export const unwrapApi = async <T>(
  result: ApiResult<T> | Promise<ApiResult<T>>,
  contract?: string,
): Promise<T> => {
  let resolved: ApiResult<T>;
  try {
    resolved = await result;
  } catch (error) {
    throw normalizeRequestError(error, contract);
  }

  if (isAbortLikeError(resolved.error)) {
    throw resolved.error;
  }
  if (resolved.error || !resolved.response?.ok) {
    throw new ApiError(resolved.response, problemFromUnknown(resolved.error));
  }
  if (resolved.data === undefined) {
    throw new ApiError(resolved.response, emptyResponseProblem(resolved.response));
  }
  return resolved.data;
};

export const unwrapApiVoid = async (
  result: ApiResult<unknown> | Promise<ApiResult<unknown>>,
  contract?: string,
): Promise<void> => {
  let resolved: ApiResult<unknown>;
  try {
    resolved = await result;
  } catch (error) {
    throw normalizeRequestError(error, contract);
  }

  if (isAbortLikeError(resolved.error)) {
    throw resolved.error;
  }
  if (resolved.error || !resolved.response?.ok) {
    throw new ApiError(resolved.response, problemFromUnknown(resolved.error));
  }
  if (resolved.response.status !== 204) {
    throw new ApiError(resolved.response, emptyResponseProblem(resolved.response));
  }
};
