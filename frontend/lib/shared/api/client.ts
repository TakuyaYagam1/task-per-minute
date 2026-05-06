import createClient from "openapi-fetch";

import { CONFIG } from "../config";
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

export const publicClient = createClient<paths>({
  baseUrl: CONFIG.apiUrl,
});

export const adminClient = createClient<paths>({
  baseUrl: CONFIG.adminApiUrl,
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
