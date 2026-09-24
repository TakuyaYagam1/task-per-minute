"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import {
  applyPublicRealtime,
  isPublicRealtimeGap,
  isPublicRealtimeRejection,
  isPublicRealtimeTerminal,
  openPublicRealtimeMessage,
  parsePublicRealtimeMessage,
  publicRealtimeUrl,
  type PublicRecoveryState,
  type RoleAwareRecoveryState,
} from "../../shared/api";

export type PublicRealtimeConnectionStatus =
  | "idle"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "recovering"
  | "rejected"
  | "error";

type UsePublicTournamentRealtimeInput = Readonly<{
  enabled: boolean;
  recovery: RoleAwareRecoveryState | null;
  retry: () => void;
  tournamentId: string;
}>;

export type PublicTournamentRealtime = Readonly<{
  state: PublicRecoveryState | null;
  status: PublicRealtimeConnectionStatus;
  ready: boolean;
}>;

const MAX_RECONNECTS = 3;
const RECONNECT_DELAYS_MS = [250, 500, 1_000] as const;
const TERMINAL_CLOSE_CODES = new Set([1008, 4001, 4003, 4401, 4403]);

export const usePublicTournamentRealtime = ({
  enabled,
  recovery,
  retry,
  tournamentId,
}: UsePublicTournamentRealtimeInput): PublicTournamentRealtime => {
  const [state, setState] = useState<PublicRecoveryState | null>(null);
  const [status, setStatus] = useState<PublicRealtimeConnectionStatus>("idle");
  const stateRef = useRef<PublicRecoveryState | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const generationRef = useRef(0);
  const reconnectCountRef = useRef(0);
  const terminalGenerationRef = useRef<number | null>(null);
  const recoveryGenerationRef = useRef<number | null>(null);
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
      socket.close(1000, "public realtime cleanup");
    }
  }, []);

  useEffect(() => {
    const active = enabled && hasRecovery;
    generationRef.current += 1;
    terminalGenerationRef.current = null;
    recoveryGenerationRef.current = null;
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
      return () => clearSocket();
    }

    let disposed = false;

    const scheduleReconnect = (generation: number): void => {
      if (
        disposed ||
        generation !== generationRef.current ||
        terminalGenerationRef.current === generation ||
        recoveryGenerationRef.current === generation
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

    const requestRestRecovery = (generation: number): void => {
      if (
        disposed ||
        generation !== generationRef.current ||
        recoveryGenerationRef.current === generation
      ) {
        return;
      }
      recoveryGenerationRef.current = generation;
      setStatus("recovering");
      retry();
      socketRef.current?.close(1000, "public realtime recovery required");
    };

    const connect = (): void => {
      if (disposed) {
        return;
      }
      const generation = ++generationRef.current;
      terminalGenerationRef.current = null;
      recoveryGenerationRef.current = null;
      clearSocket();
      setStatus(reconnectCountRef.current > 0 ? "reconnecting" : "connecting");

      let socket: WebSocket;
      try {
        socket = new WebSocket(publicRealtimeUrl(tournamentId, confirmedResumeIdRef.current));
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
          if (isPublicRealtimeRejection(value)) {
            terminalGenerationRef.current = generation;
            setStatus("rejected");
            socket.close(1000, "public realtime rejected");
            return;
          }
          if (isPublicRealtimeTerminal(value, tournamentId)) {
            const terminalState = (value as { payload: { state: "cancelled" | "completed" } }).payload.state;
            const currentState = stateRef.current;
            if (currentState !== null) {
              const terminalRealtimeState: PublicRecoveryState = {
                ...currentState,
                display: {
                  ...currentState.display,
                  tournament: {
                    ...currentState.display.tournament,
                    state: terminalState,
                  },
                },
              };
              stateRef.current = terminalRealtimeState;
              setState(terminalRealtimeState);
            }
            requestRestRecovery(generation);
            return;
          }
          if (!receivedInitialFrame) {
            const nextState = openPublicRealtimeMessage(value, tournamentId);
            receivedInitialFrame = true;
            reconnectCountRef.current = 0;
            confirmedResumeIdRef.current = nextState.resumeId;
            stateRef.current = nextState;
            setState(nextState);
            setStatus("connected");
            return;
          }
          const currentState = stateRef.current;
          if (currentState === null) {
            throw new Error("Public realtime update arrived before its snapshot");
          }
          const envelope = parsePublicRealtimeMessage(value, tournamentId);
          if (isPublicRealtimeGap(currentState, envelope)) {
            requestRestRecovery(generation);
            return;
          }
          const applied = applyPublicRealtime(currentState, envelope);
          if (applied.outcome === "applied") {
            confirmedResumeIdRef.current = applied.state.resumeId;
            stateRef.current = applied.state;
            setState(applied.state);
            setStatus("connected");
          }
        } catch {
          requestRestRecovery(generation);
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
        if (recoveryGenerationRef.current === generation) {
          return;
        }
        if (
          terminalGenerationRef.current === generation ||
          TERMINAL_CLOSE_CODES.has(event.code)
        ) {
          setStatus("rejected");
          return;
        }
        if (event.code === 1000 && stateRef.current !== null) {
          requestRestRecovery(generation);
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
  }, [clearSocket, enabled, hasRecovery, recovery, retry, tournamentId]);

  return {
    state,
    status,
    ready: state !== null,
  };
};
