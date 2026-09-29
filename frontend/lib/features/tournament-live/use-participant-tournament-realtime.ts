"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import type { RoleAwareRecoveryState } from "../../shared/api";
import { CONFIG } from "../../shared/config/app";

export type ParticipantRealtimeConnectionStatus =
  | "idle"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "recovering"
  | "rejected"
  | "error";

type UseParticipantTournamentRealtimeInput = Readonly<{
  enabled: boolean;
  recovery: RoleAwareRecoveryState | null;
  retry: () => Promise<boolean> | void;
  tournamentId: string;
}>;

export type ParticipantTournamentRealtime = Readonly<{
  connectionNoticeVisible: boolean;
  refreshSequence: number;
  status: ParticipantRealtimeConnectionStatus;
  retry: () => void;
}>;

type ParticipantRealtimeEnvelope = Readonly<{
  resumeId: string | null;
}>;

const RECONNECT_DELAYS_MS = [250, 500, 1_000, 2_000, 5_000] as const;
const REST_REFRESH_INTERVAL_MS = 5_000;
const NORMAL_CLOSE_CODE = 1000;
const TERMINAL_CLOSE_CODES = new Set([1008, 4001, 4003, 4401, 4403]);
const PARTICIPANT_REJECTION_CODES = new Set([
  "tournament.unauthenticated",
  "tournament.forbidden",
  "tournament.unavailable",
  "tournament.capacity",
  "tournament.invalid_frame",
  "tournament.rate_limited",
]);
const PARTICIPANT_TERMINAL_STATES = new Set(["completed", "cancelled"]);
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const hasExactKeys = (
  value: Record<string, unknown>,
  keys: readonly string[],
): boolean => {
  const actual = Object.keys(value).sort();
  const expected = [...keys].sort();
  return actual.length === expected.length && actual.every((key, index) => key === expected[index]);
};

const isUUID = (value: unknown): value is string =>
  typeof value === "string" && UUID_PATTERN.test(value);

const isSafeInteger = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value);

const isDateTime = (value: unknown): value is string =>
  typeof value === "string" && value.endsWith("Z") && !Number.isNaN(Date.parse(value));

const isNonBlank = (value: unknown): value is string =>
  typeof value === "string" && value.trim().length > 0;

const parseParticipantRealtimeMessage = (
  value: unknown,
  expectedTournamentId: string,
): ParticipantRealtimeEnvelope => {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["type", "payload"]) ||
    value.type !== "tournament.participant" ||
    !isRecord(value.payload) ||
    !hasExactKeys(value.payload, ["envelope"]) ||
    !isRecord(value.payload.envelope)
  ) {
    throw new Error("Invalid participant realtime envelope");
  }

  const envelope = value.payload.envelope;
  const requiredKeys = [
    "schema_version",
    "tournament_id",
    "sequence",
    "event_id",
    "occurred_at",
    "projection_revision",
    "participant",
  ] as const;
  const optionalResumeKeys = ["resume_id", ...requiredKeys] as const;
  if (
    !hasExactKeys(envelope, optionalResumeKeys) &&
    !hasExactKeys(envelope, requiredKeys)
  ) {
    throw new Error("Invalid participant realtime metadata");
  }
  const resumeId = envelope.resume_id;
  if (
    envelope.schema_version !== 1 ||
    !isUUID(envelope.tournament_id) ||
    envelope.tournament_id !== expectedTournamentId ||
    !isSafeInteger(envelope.sequence) ||
    envelope.sequence < 0 ||
    !isUUID(envelope.event_id) ||
    !isDateTime(envelope.occurred_at) ||
    !isSafeInteger(envelope.projection_revision) ||
    envelope.projection_revision < 1 ||
    (resumeId !== undefined && !isUUID(resumeId)) ||
    !isRecord(envelope.participant)
  ) {
    throw new Error("Invalid participant realtime metadata");
  }

  return {
    resumeId: resumeId === undefined ? null : resumeId,
  };
};

/*
 * The participant snapshot is intentionally opaque here. REST recovery owns
 * its complete schema; the socket only proves that the server sent a valid
 * participant envelope before asking REST to reconcile the view.
 */
const isParticipantRealtimeRejection = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["type", "code", "message"]) &&
  value.type === "tournament.rejected" &&
  typeof value.code === "string" &&
  PARTICIPANT_REJECTION_CODES.has(value.code) &&
  isNonBlank(value.message);

const isParticipantRealtimeTerminal = (
  value: unknown,
  expectedTournamentId: string,
): boolean => {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["type", "payload"]) ||
    value.type !== "tournament.terminal" ||
    !isRecord(value.payload) ||
    !hasExactKeys(value.payload, [
      "schema_version",
      "tournament_id",
      "sequence",
      "event_id",
      "occurred_at",
      "state",
    ])
  ) {
    return false;
  }
  return (
    value.payload.schema_version === 1 &&
    value.payload.tournament_id === expectedTournamentId &&
    isUUID(value.payload.tournament_id) &&
    isSafeInteger(value.payload.sequence) &&
    value.payload.sequence >= 1 &&
    isUUID(value.payload.event_id) &&
    isDateTime(value.payload.occurred_at) &&
    typeof value.payload.state === "string" &&
    PARTICIPANT_TERMINAL_STATES.has(value.payload.state)
  );
};

const participantRealtimeUrl = (
  tournamentId: string,
  resumeId?: string | null,
): string => {
  if (!isUUID(tournamentId)) {
    throw new TypeError("Participant realtime requires a UUID tournament id");
  }
  if (resumeId !== undefined && resumeId !== null && !isUUID(resumeId)) {
    throw new TypeError("Participant realtime requires a UUID resume id");
  }
  const configuredOrigin = CONFIG.apiUrl ||
    (typeof window === "undefined" ? "" : window.location.origin);
  if (!configuredOrigin) {
    throw new Error("Participant realtime requires a player API origin");
  }
  const configured = new URL(configuredOrigin);
  const protocol = configured.protocol === "https:" || configured.protocol === "wss:"
    ? "wss:"
    : "ws:";
  const url = new URL(
    `/api/v1/tournaments/${encodeURIComponent(tournamentId)}/participant/realtime`,
    `${protocol}//${configured.host}`,
  );
  if (resumeId) {
    url.searchParams.set("resume_id", resumeId);
  }
  return url.toString();
};

export const useParticipantTournamentRealtime = ({
  enabled,
  recovery,
  retry,
  tournamentId,
}: UseParticipantTournamentRealtimeInput): ParticipantTournamentRealtime => {
  const [status, setStatus] = useState<ParticipantRealtimeConnectionStatus>("idle");
  const [connectionNoticeVisible, setConnectionNoticeVisible] = useState(false);
  const [refreshSequence, setRefreshSequence] = useState(0);
  const socketRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const generationRef = useRef(0);
  const reconnectCountRef = useRef(0);
  const terminalGenerationRef = useRef<number | null>(null);
  const connectionNoticeRef = useRef(false);
  const hasOpenedConnectionRef = useRef(false);
  const confirmedResumeIdRef = useRef<string | null>(null);
  const activeTournamentRef = useRef<string | null>(null);
  const connectRef = useRef<(() => void) | null>(null);
  const previousParticipantCursorRef = useRef<string | null>(null);
  const hasRecovery = recovery !== null;
  const participantCursorKey = recovery?.role === "participant" &&
      "participant_view_revision" in recovery.cursor
    ? [
        recovery.cursor.event_sequence,
        recovery.cursor.participant_view_revision,
        recovery.cursor.projection_revision,
      ].join(":")
    : null;

  const setReconnectNotice = useCallback((visible: boolean) => {
    connectionNoticeRef.current = visible;
    setConnectionNoticeVisible(visible);
  }, []);

  useEffect(() => {
    if (participantCursorKey === null) {
      previousParticipantCursorRef.current = null;
      return;
    }
    if (
      previousParticipantCursorRef.current !== null &&
      previousParticipantCursorRef.current !== participantCursorKey
    ) {
      setRefreshSequence((current) => current + 1);
    }
    previousParticipantCursorRef.current = participantCursorKey;
  }, [participantCursorKey]);

  const clearSocket = useCallback(() => {
    if (reconnectTimerRef.current !== null) {
      clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
    const socket = socketRef.current;
    socketRef.current = null;
    if (socket && socket.readyState < WebSocket.CLOSING) {
      socket.close(1000, "participant realtime cleanup");
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
      reconnectCountRef.current = 0;
      hasOpenedConnectionRef.current = false;
      setReconnectNotice(false);
    }

    if (!active) {
      setStatus("idle");
      setReconnectNotice(false);
      connectRef.current = null;
      return () => clearSocket();
    }

    let disposed = false;
    let recoveryRefreshTimer: ReturnType<typeof setTimeout> | null = null;
    let recoveryRefreshInFlight = false;
    let recoveryRefreshQueued = false;
    let clearNoticeAfterQueuedRefresh = false;

    const refreshRecovery = async (
      generation: number,
      clearNoticeAfterSuccess = false,
    ): Promise<boolean> => {
      if (disposed || generation !== generationRef.current) {
        return false;
      }
      if (recoveryRefreshInFlight) {
        recoveryRefreshQueued = true;
        clearNoticeAfterQueuedRefresh ||= clearNoticeAfterSuccess;
        return false;
      }

      recoveryRefreshInFlight = true;
      let succeeded = false;
      try {
        succeeded = (await retry()) !== false;
      } catch {
        succeeded = false;
      } finally {
        recoveryRefreshInFlight = false;
      }

      if (
        succeeded &&
        clearNoticeAfterSuccess &&
        !disposed &&
        generation === generationRef.current
      ) {
        setReconnectNotice(false);
      }

      if (!disposed && recoveryRefreshQueued) {
        const clearNotice = clearNoticeAfterQueuedRefresh;
        recoveryRefreshQueued = false;
        clearNoticeAfterQueuedRefresh = false;
        void refreshRecovery(generationRef.current, clearNotice);
      }
      return succeeded;
    };

    const scheduleRecoveryRefresh = (): void => {
      if (disposed || recoveryRefreshTimer !== null) {
        return;
      }
      recoveryRefreshTimer = setTimeout(() => {
        recoveryRefreshTimer = null;
        if (typeof document !== "undefined" && document.visibilityState !== "visible") {
          scheduleRecoveryRefresh();
          return;
        }
        void refreshRecovery(generationRef.current).finally(scheduleRecoveryRefresh);
      }, REST_REFRESH_INTERVAL_MS);
    };

    const refreshWhenVisible = (): void => {
      if (document.visibilityState === "visible") {
        void refreshRecovery(generationRef.current);
      }
    };

    const scheduleReconnect = (generation: number): void => {
      if (
        disposed ||
        generation !== generationRef.current ||
        terminalGenerationRef.current === generation
      ) {
        return;
      }
      const delay = RECONNECT_DELAYS_MS[reconnectCountRef.current] ?? 5_000;
      reconnectCountRef.current = Math.min(
        reconnectCountRef.current + 1,
        RECONNECT_DELAYS_MS.length,
      );
      setStatus("reconnecting");
      setReconnectNotice(true);
      reconnectTimerRef.current = setTimeout(() => {
        reconnectTimerRef.current = null;
        connectRef.current?.();
      }, delay);
    };

    const closeAsInvalid = (generation: number, socket: WebSocket): void => {
      terminalGenerationRef.current = generation;
      setStatus("error");
      setReconnectNotice(true);
      socket.close(NORMAL_CLOSE_CODE, "invalid participant realtime frame");
    };

    const acceptFrame = (generation: number, envelope: ParticipantRealtimeEnvelope): void => {
      if (envelope.resumeId !== null) {
        confirmedResumeIdRef.current = envelope.resumeId;
      }
      reconnectCountRef.current = 0;
      setStatus("connected");
      void refreshRecovery(generation, true);
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
          participantRealtimeUrl(tournamentId, confirmedResumeIdRef.current),
        );
      } catch {
        scheduleReconnect(generation);
        return;
      }
      socketRef.current = socket;

      socket.onopen = () => {
        if (disposed || generation !== generationRef.current) {
          return;
        }
        hasOpenedConnectionRef.current = true;
        setStatus("connecting");
        if (connectionNoticeRef.current) {
          void refreshRecovery(generation, true);
        }
      };

      socket.onmessage = (event) => {
        if (disposed || generation !== generationRef.current) {
          return;
        }
        try {
          if (typeof event.data !== "string") {
            throw new Error("Participant realtime frame is not text");
          }
          const value: unknown = JSON.parse(event.data);
          if (isParticipantRealtimeRejection(value)) {
            terminalGenerationRef.current = generation;
            setStatus("rejected");
            setReconnectNotice(true);
            socket.close(NORMAL_CLOSE_CODE, "participant realtime rejected");
            return;
          }
          if (isParticipantRealtimeTerminal(value, tournamentId)) {
            terminalGenerationRef.current = generation;
            setStatus("recovering");
            setRefreshSequence((current) => current + 1);
            void refreshRecovery(generation, true);
            socket.close(1000, "participant realtime terminal");
            return;
          }
          const envelope = parseParticipantRealtimeMessage(value, tournamentId);
          acceptFrame(generation, envelope);
        } catch {
          closeAsInvalid(generation, socket);
        }
      };

      socket.onerror = () => {
        if (disposed || generation !== generationRef.current) {
          return;
        }
        setStatus("reconnecting");
        if (hasOpenedConnectionRef.current) {
          setReconnectNotice(true);
        }
      };

      socket.onclose = (event) => {
        if (disposed || generation !== generationRef.current) {
          return;
        }
        socketRef.current = null;
        if (terminalGenerationRef.current === generation) {
          return;
        }
        if (event.code === NORMAL_CLOSE_CODE) {
          terminalGenerationRef.current = generation;
          setStatus("idle");
          return;
        }
        if (TERMINAL_CLOSE_CODES.has(event.code)) {
          terminalGenerationRef.current = generation;
          setStatus("rejected");
          setReconnectNotice(true);
          return;
        }
        scheduleReconnect(generation);
      };
    };

    connectRef.current = connect;
    connect();
    scheduleRecoveryRefresh();
    document.addEventListener("visibilitychange", refreshWhenVisible);

    return () => {
      disposed = true;
      generationRef.current += 1;
      connectRef.current = null;
      if (recoveryRefreshTimer !== null) {
        clearTimeout(recoveryRefreshTimer);
        recoveryRefreshTimer = null;
      }
      document.removeEventListener("visibilitychange", refreshWhenVisible);
      clearSocket();
    };
  }, [clearSocket, enabled, hasRecovery, retry, setReconnectNotice, tournamentId]);

  const reconnect = useCallback(() => {
    reconnectCountRef.current = 0;
    terminalGenerationRef.current = null;
    setConnectionNoticeVisible(true);
    connectionNoticeRef.current = true;
    setStatus("connecting");
    connectRef.current?.();
  }, []);

  return { connectionNoticeVisible, refreshSequence, status, retry: reconnect };
};
