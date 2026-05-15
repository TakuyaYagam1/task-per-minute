import createClient from "openapi-fetch";

import { CONFIG } from "../config";
import { isAdminTokenResponse } from "./guards";
import type { components, paths } from "./schema";

export type ProblemDetails = components["schemas"]["ProblemDetails"];

export class ApiError extends Error {
  readonly status: number;
  readonly problem?: ProblemDetails;
  readonly retryAfter?: string | null;

  constructor(response: Response | null | undefined, problem?: ProblemDetails) {
    const status = response?.status ?? 0;
    super(
      problem?.detail || problem?.title || (status > 0 ? `HTTP ${status}` : "Network error"),
    );
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
    this.retryAfter = response?.headers.get("Retry-After") ?? null;
  }
}

type ApiResult<T> = {
  data?: T;
  error?: unknown;
  response: Response;
};

const CSRF_COOKIE_NAME = "tpm_player_csrf";
const ADMIN_ACCESS_CSRF_COOKIE_NAME = "tpm_admin_access_csrf";
const ADMIN_REFRESH_CSRF_COOKIE_NAME = "tpm_admin_refresh_csrf";
const CSRF_HEADER_NAME = "X-CSRF-Token";
const ADMIN_REFRESH_CSRF_HEADER_NAME = "X-Admin-Refresh-CSRF-Token";
const CSRF_STORAGE_KEY = "player_csrf_token";
const ADMIN_ACCESS_CSRF_STORAGE_KEY = "admin_access_csrf_token";
const ADMIN_REFRESH_CSRF_STORAGE_KEY = "admin_refresh_csrf_token";

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
      return decodeURIComponent(trimmed.slice(prefix.length));
    }
  }
  return null;
};

const readStoredToken = (key: string): string | null => {
  try {
    return window.sessionStorage.getItem(key);
  } catch {
    return null;
  }
};

const saveStoredToken = (key: string, token: string): void => {
  try {
    window.sessionStorage.setItem(key, token);
  } catch {
    // sessionStorage can be unavailable in restricted browser contexts.
  }
};

const clearStoredToken = (key: string): void => {
  try {
    window.sessionStorage.removeItem(key);
  } catch {
    // sessionStorage can be unavailable in restricted browser contexts.
  }
};

export const clearAdminCSRFTokens = (): void => {
  clearStoredToken(ADMIN_ACCESS_CSRF_STORAGE_KEY);
  clearStoredToken(ADMIN_REFRESH_CSRF_STORAGE_KEY);
};

let adminRefreshPromise: Promise<boolean> | null = null;
let adminRefreshFailureHandler: (() => void) | null = null;
let adminSessionEpoch = 0;

export const setAdminRefreshFailureHandler = (
  handler: (() => void) | null,
): void => {
  adminRefreshFailureHandler = handler;
};

export const advanceAdminSessionEpoch = (): void => {
  adminSessionEpoch += 1;
};

const readPlayerCSRFToken = (): string | null =>
  readCookie(CSRF_COOKIE_NAME) || readStoredToken(CSRF_STORAGE_KEY);

const readAdminAccessCSRFToken = (): string | null =>
  readCookie(ADMIN_ACCESS_CSRF_COOKIE_NAME) || readStoredToken(ADMIN_ACCESS_CSRF_STORAGE_KEY);

const readAdminRefreshCSRFToken = (): string | null =>
  readCookie(ADMIN_REFRESH_CSRF_COOKIE_NAME) || readStoredToken(ADMIN_REFRESH_CSRF_STORAGE_KEY);

const csrfTokenForRequest = (request: Request): string | null => {
  const pathname = new URL(request.url).pathname;
  if (pathname === "/api/v1/admin/refresh" || pathname === "/api/v1/admin/logout") {
    return readAdminRefreshCSRFToken();
  }
  if (pathname.startsWith("/api/v1/admin/") && pathname !== "/api/v1/admin/login") {
    return readAdminAccessCSRFToken();
  }
  if (pathname.startsWith("/api/v1/players/")) {
    return readPlayerCSRFToken();
  }
  return null;
};

const isAdminRefreshCSRFRequest = (request: Request): boolean => {
  const pathname = new URL(request.url).pathname;
  return pathname === "/api/v1/admin/refresh" || pathname === "/api/v1/admin/logout";
};

const syncCSRFTokenFromResponse = (request: Request, response: Response): void => {
  const pathname = new URL(request.url).pathname;
  if (pathname === "/api/v1/admin/login" || pathname === "/api/v1/admin/refresh") {
    const adminAccessToken = response.headers.get(CSRF_HEADER_NAME);
    if (adminAccessToken) {
      saveStoredToken(ADMIN_ACCESS_CSRF_STORAGE_KEY, adminAccessToken);
    }
    const adminRefreshToken = response.headers.get(ADMIN_REFRESH_CSRF_HEADER_NAME);
    if (adminRefreshToken) {
      saveStoredToken(ADMIN_REFRESH_CSRF_STORAGE_KEY, adminRefreshToken);
    }
    return;
  }
  if (response.ok && pathname === "/api/v1/admin/logout") {
    clearAdminCSRFTokens();
    return;
  }
  const token = response.headers.get(CSRF_HEADER_NAME);
  if (token) {
    saveStoredToken(CSRF_STORAGE_KEY, token);
    return;
  }
  if (response.ok && pathname === "/api/v1/players/logout") {
    clearStoredToken(CSRF_STORAGE_KEY);
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
  const headers = new Headers(request.headers);
  if (isUnsafeMethod(request.method)) {
    const csrfToken = csrfTokenForRequest(request);
    if (csrfToken) {
      if (!headers.has(CSRF_HEADER_NAME)) {
        headers.set(CSRF_HEADER_NAME, csrfToken);
      }
      if (isAdminRefreshCSRFRequest(request) && !headers.has(ADMIN_REFRESH_CSRF_HEADER_NAME)) {
        headers.set(ADMIN_REFRESH_CSRF_HEADER_NAME, csrfToken);
      }
    }
  }
  const credentialedRequest = new Request(request, { credentials: "include", headers });
  const response = await fetch(credentialedRequest);
  syncCSRFTokenFromResponse(credentialedRequest, response);
  return response;
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

const refreshAdminSession = async (): Promise<boolean> => {
  try {
    const response = await credentialedFetch("/api/v1/admin/refresh", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: "" }),
    });
    const body: unknown = await response.clone().json().catch(() => null);
    if (response.ok && isAdminTokenResponse(body)) {
      return true;
    }
  } catch {
    // The original 401 is the response the caller should see.
  }
  clearAdminCSRFTokens();
  adminRefreshFailureHandler?.();
  return false;
};

const ensureAdminSessionFresh = (): Promise<boolean> => {
  if (adminRefreshPromise === null) {
    adminRefreshPromise = refreshAdminSession().finally(() => {
      adminRefreshPromise = null;
    });
  }
  return adminRefreshPromise;
};

export const adminCredentialedFetch: typeof fetch = async (input, init) => {
  const request = requestFromInput(input, init);
  const retryRequest = request.clone();
  const requestSessionEpoch = adminSessionEpoch;
  const response = await credentialedFetch(request);
  if (response.status !== 401 || !isAdminRefreshableRequest(request)) {
    return response;
  }

  const refreshed = await ensureAdminSessionFresh();
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

const isAbortLikeError = (value: unknown): boolean =>
  value instanceof DOMException &&
  (value.name === "AbortError" || value.name === "TimeoutError");

export const unwrapApi = async <T>(result: ApiResult<T>): Promise<T> => {
  if (isAbortLikeError(result.error)) {
    throw result.error;
  }
  if (result.error || !result.response?.ok) {
    throw new ApiError(result.response, problemFromUnknown(result.error));
  }
  if (result.data === undefined) {
    throw new ApiError(result.response, {
      type: "about:blank",
      status: result.response.status,
      title: "Empty response body",
      detail: "Server returned a successful status without expected response data.",
    });
  }
  return result.data;
};

export const unwrapApiVoid = async (result: ApiResult<unknown>): Promise<void> => {
  if (isAbortLikeError(result.error)) {
    throw result.error;
  }
  if (result.error || !result.response?.ok) {
    throw new ApiError(result.response, problemFromUnknown(result.error));
  }
};
