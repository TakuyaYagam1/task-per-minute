"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import {
  applyRoleRecoverySnapshot,
  classifyRoleRecoveryError,
  getRoleRecoverySnapshot,
  type RoleAwareRecoveryState,
  type TournamentLiveRole,
} from "../../shared/api";
import { readMonotonicNow } from "./countdown";
import type { TournamentLiveConnectionStatus } from "./TournamentLivePanel";

type RecoveryView = Readonly<{
  recovery: RoleAwareRecoveryState | null;
  receivedAtMonotonicMs?: number;
  status: TournamentLiveConnectionStatus;
}>;

const initialView: RecoveryView = {
  recovery: null,
  status: "connecting",
};

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

export const useTournamentRecovery = (
  role: TournamentLiveRole,
  tournamentId: string,
): RecoveryView & Readonly<{ retry: () => void }> => {
  const [view, setView] = useState<RecoveryView>(initialView);
  const recoveryRef = useRef<RoleAwareRecoveryState | null>(null);
  const controllerRef = useRef<AbortController | null>(null);
  const requestRef = useRef(0);

  const load = useCallback(async (withCursor: boolean): Promise<void> => {
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    const request = ++requestRef.current;
    const previous = recoveryRef.current;

    setView((current) => ({
      ...current,
      status: previous === null ? "connecting" : "recovering",
    }));

    const accept = async (fresh: boolean): Promise<void> => {
      const response = await getRoleRecoverySnapshot(
        role,
        tournamentId,
        fresh ? undefined : previous?.cursor,
        controller.signal,
      );
      const receivedAtMonotonicMs = readMonotonicNow();
      if (controller.signal.aborted || request !== requestRef.current) {
        return;
      }
      const transition = applyRoleRecoverySnapshot(recoveryRef.current, {
        role,
        tournamentId,
        snapshot: response.snapshot,
        serverTimestamp: response.serverTimestamp,
        fresh,
      });
      recoveryRef.current = transition.state;
      setView({
        recovery: transition.state,
        receivedAtMonotonicMs,
        status: transition.changed || transition.outcome === "duplicate" ? "live" : "stale",
      });
    };

    try {
      await accept(!withCursor);
    } catch (error) {
      if (controller.signal.aborted || request !== requestRef.current || isAbortError(error)) {
        return;
      }
      const failure = classifyRoleRecoveryError(previous, error);
      if (
        withCursor &&
        (failure.outcome === "future_cursor" || failure.outcome === "invalid_cursor")
      ) {
        setView((current) => ({ ...current, status: "recovering" }));
        try {
          await accept(true);
          return;
        } catch (freshError) {
          if (
            controller.signal.aborted ||
            request !== requestRef.current ||
            isAbortError(freshError)
          ) {
            return;
          }
        }
      }
      setView((current) => ({
        ...current,
        status: previous === null ? "rejected" : "stale",
      }));
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
    void load(recoveryRef.current !== null);
  }, [load]);

  return { ...view, retry };
};
