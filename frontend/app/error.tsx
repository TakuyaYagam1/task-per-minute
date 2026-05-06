"use client";

import { useEffect } from "react";

import { log } from "../lib/shared/lib";
import { ErrorPageShell } from "../lib/widgets/error-page";

type ApiErrorLike = {
  status?: unknown;
  retryAfter?: unknown;
  problem?: unknown;
};

const isApiErrorLike = (value: unknown): value is ApiErrorLike =>
  typeof value === "object" &&
  value !== null &&
  ("status" in value || "problem" in value);

const parseRetryAfter = (raw: unknown): number | undefined => {
  if (typeof raw !== "string" || raw.length === 0) {
    return undefined;
  }
  const seconds = Number(raw);
  if (Number.isFinite(seconds) && seconds >= 0) {
    return Math.round(seconds);
  }
  const epoch = Date.parse(raw);
  if (!Number.isFinite(epoch)) {
    return undefined;
  }
  const diff = Math.round((epoch - Date.now()) / 1000);
  return diff > 0 ? diff : 0;
};

export default function ErrorBoundary({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    log.error("app/error.tsx caught:", error);
  }, [error]);

  let statusCode = 500;
  let retryAfterSeconds: number | undefined;
  let description: string | undefined;

  if (isApiErrorLike(error)) {
    if (typeof error.status === "number" && error.status > 0) {
      statusCode = error.status;
    }
    retryAfterSeconds = parseRetryAfter(error.retryAfter);
  }

  if (statusCode >= 500 && error.message) {
    description = undefined;
  } else if (statusCode >= 400 && statusCode < 500 && error.message) {
    description = error.message;
  }

  return (
    <ErrorPageShell
      statusCode={statusCode}
      description={description}
      retryAfterSeconds={retryAfterSeconds}
      onRetry={statusCode >= 500 ? reset : undefined}
    />
  );
}
