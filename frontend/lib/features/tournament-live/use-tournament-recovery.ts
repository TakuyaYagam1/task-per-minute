"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import {
  applyRoleRecoverySnapshot,
  classifyRoleRecoveryError,
  getRoleRecoverySnapshot,
  ApiError,
  type RoleAwareRecoveryState,
  type TournamentLiveRole,
} from "../../shared/api";
import { readMonotonicNow } from "./countdown";
import type { TournamentLiveConnectionStatus } from "./TournamentLivePanel";

type RecoveryView = Readonly<{
  error: TournamentRecoveryError | null;
  recovery: RoleAwareRecoveryState | null;
  receivedAtMonotonicMs?: number;
  status: TournamentLiveConnectionStatus;
}>;

export type TournamentRecoveryErrorKind =
  | "not_found"
  | "rate_limited"
  | "transport"
  | "contract"
  | "http";

export type TournamentRecoveryError = Readonly<{
  kind: TournamentRecoveryErrorKind;
  status: number;
}>;

type RecoveryActions = Readonly<{
  refresh: () => Promise<boolean>;
  retry: () => Promise<boolean>;
}>;

const initialView: RecoveryView = {
  error: null,
  recovery: null,
  status: "connecting",
};

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  error.name === "AbortError";

const recoveryErrorFor = (error: unknown): TournamentRecoveryError => {
  if (error instanceof DOMException && error.name === "TimeoutError") {
    return { kind: "transport", status: 0 };
  }
  if (error instanceof ApiError) {
    if (error.kind === "not_found" || error.kind === "rate_limited" || error.kind === "transport") {
      return { kind: error.kind, status: error.status };
    }
    return { kind: "http", status: error.status };
  }
  return { kind: "contract", status: 0 };
};

export const useTournamentRecovery = (
  role: TournamentLiveRole,
  tournamentId: string,
): RecoveryView & RecoveryActions => {
  const [view, setView] = useState<RecoveryView>(initialView);
  const recoveryRef = useRef<RoleAwareRecoveryState | null>(null);
  const controllerRef = useRef<AbortController | null>(null);
  const requestRef = useRef(0);

  const load = useCallback(async (withCursor: boolean): Promise<boolean> => {
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    const request = ++requestRef.current;
    const previous = recoveryRef.current;

    setView((current) => ({
      ...current,
      error: null,
      status: previous === null
        ? "connecting"
        : current.status === "live"
          ? "live"
          : "recovering",
    }));

    const accept = async (fresh: boolean): Promise<boolean> => {
      const response = await getRoleRecoverySnapshot(
        role,
        tournamentId,
        fresh ? undefined : previous?.cursor,
        controller.signal,
      );
      const receivedAtMonotonicMs = readMonotonicNow();
      if (controller.signal.aborted || request !== requestRef.current) {
        return false;
      }
      const transition = applyRoleRecoverySnapshot(recoveryRef.current, {
        role,
        tournamentId,
        snapshot: response.snapshot,
        serverTimestamp: response.serverTimestamp,
        allowEqualCursor: true,
        fresh,
      });
      recoveryRef.current = transition.state;
      setView({
        error: null,
        recovery: transition.state,
        receivedAtMonotonicMs,
        status: transition.changed || transition.outcome === "duplicate" ? "live" : "stale",
      });
      return true;
    };

    try {
      return await accept(!withCursor);
    } catch (error) {
      if (controller.signal.aborted || request !== requestRef.current || isAbortError(error)) {
        return false;
      }
      let finalError = error;
      const failure = classifyRoleRecoveryError(previous, error);
      const shouldRetryFresh = failure.outcome === "unknown_schema" || (
        withCursor &&
        (failure.outcome === "future_cursor" || failure.outcome === "invalid_cursor")
      );
      if (shouldRetryFresh) {
        setView((current) => ({ ...current, status: "recovering" }));
        try {
          return await accept(true);
        } catch (freshError) {
          if (
            controller.signal.aborted ||
            request !== requestRef.current ||
            isAbortError(freshError)
          ) {
            return false;
          }
          finalError = freshError;
        }
      }
      setView((current) => ({
        ...current,
        error: recoveryErrorFor(finalError),
        status: previous === null ? "rejected" : "stale",
      }));
      return false;
    }
  }, [role, tournamentId]);

  useEffect(() => {
    recoveryRef.current = null;
    setView(initialView);
    void load(false);
    return () => {
      requestRef.current += 1;
      controllerRef.current?.abort();
    };
  }, [load]);

  const retry = useCallback(() => {
    return load(recoveryRef.current !== null);
  }, [load]);

  const refresh = useCallback(() => {
    return load(false);
  }, [load]);

  return { ...view, refresh, retry };
};
