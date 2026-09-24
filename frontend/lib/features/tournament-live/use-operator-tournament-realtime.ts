"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import {
  applyOperatorRealtime,
  openOperatorRealtime,
  operatorRealtimeUrl,
  isOperatorRealtimeRejection,
  type OperatorRealtimeState,
  type RoleAwareRecoveryState,
} from "../../shared/api";

export type OperatorRealtimeConnectionStatus =
  | "idle"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "paused"
  | "rejected"
  | "error";

type UseOperatorTournamentRealtimeInput = Readonly<{
  enabled: boolean;
  recovery: RoleAwareRecoveryState | null;
  recoveryReceivedAtMonotonicMs?: number;
  tournamentId: string;
}>;

export type OperatorTournamentRealtime = Readonly<{
  state: OperatorRealtimeState | null;
  status: OperatorRealtimeConnectionStatus;
  ready: boolean;
  paused: boolean;
  retry: () => void;
}>;

const MAX_RECONNECTS = 3;
const RECONNECT_DELAYS_MS = [250, 500, 1_000] as const;
const TERMINAL_CLOSE_CODES = new Set([1008, 4001, 4003, 4401, 4403]);

const isPausedState = (state: OperatorRealtimeState | null): boolean =>
  state?.operator.pause !== undefined && state.operator.pause !== null;

export const useOperatorTournamentRealtime = ({
  enabled,
  recovery,
  recoveryReceivedAtMonotonicMs,
  tournamentId,
}: UseOperatorTournamentRealtimeInput): OperatorTournamentRealtime => {
  const [state, setState] = useState<OperatorRealtimeState | null>(null);
  const [status, setStatus] = useState<OperatorRealtimeConnectionStatus>("idle");
  const stateRef = useRef<OperatorRealtimeState | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const generationRef = useRef(0);
  const reconnectCountRef = useRef(0);
  const terminalGenerationRef = useRef<number | null>(null);
  const confirmedResumeIdRef = useRef<string | null>(null);
  const activeTournamentRef = useRef<string | null>(null);
  const connectRef = useRef<(() => void) | null>(null);
  stateRef.current = state;
  const hasRecovery = recovery !== null;

  const clearSocket = useCallback(() => {
    if (reconnectTimerRef.current !== null) {
      clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
    const socket = socketRef.current;
    socketRef.current = null;
    if (socket && socket.readyState < WebSocket.CLOSING) {
      socket.close(1000, "operator realtime cleanup");
    }
  }, []);

  useEffect(() => {
    const active = enabled && hasRecovery;

    generationRef.current += 1;
    terminalGenerationRef.current = null;
    clearSocket();

    if (activeTournamentRef.current !== tournamentId) {
      activeTournamentRef.current = tournamentId;
      confirmedResumeIdRef.current = null;
    }

    if (!active) {
      stateRef.current = null;
      setState(null);
      setStatus("idle");
      connectRef.current = null;
      return () => {
        clearSocket();
      };
    }

    let disposed = false;

    const scheduleReconnect = (generation: number): void => {
      if (
        disposed ||
        generation !== generationRef.current ||
        terminalGenerationRef.current === generation
      ) {
        return;
      }
      if (reconnectCountRef.current >= MAX_RECONNECTS) {
        setStatus("error");
        return;
      }
      const delay = RECONNECT_DELAYS_MS[reconnectCountRef.current] ?? 1_000;
      reconnectCountRef.current += 1;
      setStatus("reconnecting");
      reconnectTimerRef.current = setTimeout(() => {
        reconnectTimerRef.current = null;
        connectRef.current?.();
      }, delay);
    };

    const connect = (): void => {
      if (disposed) {
        return;
      }
      const generation = ++generationRef.current;
      terminalGenerationRef.current = null;
      clearSocket();
      setStatus(reconnectCountRef.current > 0 ? "reconnecting" : "connecting");

      let socket: WebSocket;
      try {
        socket = new WebSocket(
          operatorRealtimeUrl(tournamentId, confirmedResumeIdRef.current),
        );
      } catch {
        scheduleReconnect(generation);
        return;
      }
      socketRef.current = socket;
      let receivedInitialFrame = false;

      socket.onopen = () => {
        if (disposed || generation !== generationRef.current) {
          return;
        }
        setStatus("connecting");
      };

      socket.onmessage = (event) => {
        if (disposed || generation !== generationRef.current) {
          return;
        }
        let value: unknown;
        try {
          value = typeof event.data === "string" ? JSON.parse(event.data) : event.data;
          if (isOperatorRealtimeRejection(value)) {
            terminalGenerationRef.current = generation;
            setStatus("rejected");
            socket.close(1000, "operator realtime rejected");
            return;
          }
          if (!receivedInitialFrame) {
            const nextState = openOperatorRealtime(value, tournamentId);
            confirmedResumeIdRef.current = nextState.resumeId;
            stateRef.current = nextState;
            receivedInitialFrame = true;
            reconnectCountRef.current = 0;
            setState(nextState);
            setStatus(isPausedState(nextState) ? "paused" : "connected");
            return;
          }
          const currentState = stateRef.current;
          if (currentState === null) {
            throw new Error("Operator realtime update arrived before its snapshot");
          }
          const applied = applyOperatorRealtime(currentState, value);
          if (applied.outcome === "applied") {
            confirmedResumeIdRef.current = applied.state.resumeId;
            stateRef.current = applied.state;
            setState(applied.state);
            setStatus(isPausedState(applied.state) ? "paused" : "connected");
          }
        } catch {
          setStatus("error");
          socket.close(1000, "invalid operator realtime frame");
        }
      };

      socket.onerror = () => {
        if (disposed || generation !== generationRef.current) {
          return;
        }
        setStatus("reconnecting");
      };

      socket.onclose = (event) => {
        if (disposed || generation !== generationRef.current) {
          return;
        }
        socketRef.current = null;
        if (
          terminalGenerationRef.current === generation ||
          TERMINAL_CLOSE_CODES.has(event.code)
        ) {
          setStatus("rejected");
          return;
        }
        scheduleReconnect(generation);
      };
    };

    connectRef.current = connect;
    connect();

    return () => {
      disposed = true;
      generationRef.current += 1;
      connectRef.current = null;
      clearSocket();
    };
  }, [clearSocket, enabled, hasRecovery, recovery, recoveryReceivedAtMonotonicMs, tournamentId]);

  const retry = useCallback(() => {
    reconnectCountRef.current = 0;
    terminalGenerationRef.current = null;
    stateRef.current = null;
    setState(null);
    setStatus("connecting");
    connectRef.current?.();
  }, []);

  return {
    state,
    status,
    ready: state !== null,
    paused: isPausedState(state),
    retry,
  };
};
