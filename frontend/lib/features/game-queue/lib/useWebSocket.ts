"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import { CONFIG } from "../../../shared/config";
import { log, parseWebSocketMessage } from "../../../shared/lib";
import {
  ClientMessageType,
  ClientMessageWithoutPayload,
  ClientMessageWithPayload,
  ClientPayloadByType,
  ClientWebSocketMessage,
  WebSocketMessage,
} from "../../../shared/types";

type SendMessage = {
  <TType extends ClientMessageWithoutPayload>(type: TType): boolean;
  <TType extends ClientMessageWithPayload>(
    type: TType,
    payload: ClientPayloadByType[TType],
  ): boolean;
};

export type ReconnectGiveUpReason =
  | "max_attempts"
  | "auth"
  | "forbidden"
  | "stale_duel";

type ConnectOptions = {
  onMessage?: (message: WebSocketMessage, ws: WebSocket) => void;
  onOpen?: (ws: WebSocket) => void;
  onClose?: (event: CloseEvent, ws: WebSocket) => void;
  onError?: (event: Event, ws: WebSocket) => void;
  onReconnect?: (ws: WebSocket, attempt: number) => void;
  onReconnectGiveUp?: (reason: ReconnectGiveUpReason) => void;
  onBeforeReconnect?: () =>
    | ReconnectGiveUpReason
    | null
    | Promise<ReconnectGiveUpReason | null>;
};

export type WebSocketConnectionState =
  | "closed"
  | "connecting"
  | "open"
  | "closing"
  | "reconnecting"
  | "auth_failed";

const MAX_RECONNECT_ATTEMPTS = 10;
const BASE_RECONNECT_DELAY_MS = 1_000;
const MAX_RECONNECT_DELAY_MS = 30_000;
const RECONNECT_JITTER_MS = 500;
const RECONNECT_STABLE_MS = 5_000;
const HEARTBEAT_INTERVAL_MS = 20_000;
const PONG_TIMEOUT_MS = 10_000;
const NORMAL_CLOSURE_CODE = 1000;
const PONG_TIMEOUT_CLOSE_CODE = 4000;
const POLICY_VIOLATION_CODE = 1008;
const AUTH_FAILED_CODE = 4401;
const FORBIDDEN_CODE = 4403;

const closeCodeToGiveUpReason = (
  code: number,
): ReconnectGiveUpReason | null => {
  if (code === POLICY_VIOLATION_CODE || code === AUTH_FAILED_CODE) {
    return "auth";
  }
  if (code === FORBIDDEN_CODE) {
    return "forbidden";
  }
  return null;
};

const computeReconnectDelay = (attemptIndex: number): number => {
  const exponential = Math.min(
    MAX_RECONNECT_DELAY_MS,
    BASE_RECONNECT_DELAY_MS * 2 ** attemptIndex,
  );
  return exponential + Math.random() * RECONNECT_JITTER_MS;
};

export const useWebSocket = () => {
  const [connectionState, setConnectionState] =
    useState<WebSocketConnectionState>("closed");
  const websocketRef = useRef<WebSocket | null>(null);
  const attemptsRef = useRef(0);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const stableOpenTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const heartbeatTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const pongDeadlineTimerRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );
  const lastOptionsRef = useRef<ConnectOptions | null>(null);
  const manualCloseRef = useRef(false);
  const generationRef = useRef(0);

  const cancelStableOpenTimer = useCallback(() => {
    if (stableOpenTimerRef.current !== null) {
      clearTimeout(stableOpenTimerRef.current);
      stableOpenTimerRef.current = null;
    }
  }, []);

  const cancelPendingReconnect = useCallback(() => {
    if (reconnectTimerRef.current !== null) {
      clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
  }, []);

  const cancelHeartbeatTimer = useCallback(() => {
    if (heartbeatTimerRef.current !== null) {
      clearInterval(heartbeatTimerRef.current);
      heartbeatTimerRef.current = null;
    }
  }, []);

  const cancelPongDeadline = useCallback(() => {
    if (pongDeadlineTimerRef.current !== null) {
      clearTimeout(pongDeadlineTimerRef.current);
      pongDeadlineTimerRef.current = null;
    }
  }, []);

  const closeWebSocket = useCallback(() => {
    generationRef.current += 1;
    manualCloseRef.current = true;
    cancelPendingReconnect();
    cancelStableOpenTimer();
    cancelHeartbeatTimer();
    cancelPongDeadline();
    attemptsRef.current = 0;
    lastOptionsRef.current = null;
    setConnectionState("closing");
    if (
      websocketRef.current &&
      websocketRef.current.readyState !== WebSocket.CLOSED
    ) {
      websocketRef.current.close(NORMAL_CLOSURE_CODE);
    }
    websocketRef.current = null;
    setConnectionState("closed");
  }, [
    cancelHeartbeatTimer,
    cancelPendingReconnect,
    cancelPongDeadline,
    cancelStableOpenTimer,
  ]);

  const openSocket = useCallback(
    (options: ConnectOptions): WebSocket => {
      const generation = generationRef.current;
      const ws = new WebSocket(CONFIG.websocketUrl);
      websocketRef.current = ws;
      setConnectionState("connecting");

      const isCurrentGeneration = (): boolean =>
        generationRef.current === generation;

      const armPongDeadline = () => {
        if (pongDeadlineTimerRef.current !== null) {
          clearTimeout(pongDeadlineTimerRef.current);
        }
        pongDeadlineTimerRef.current = setTimeout(() => {
          pongDeadlineTimerRef.current = null;
          if (
            !isCurrentGeneration() ||
            websocketRef.current !== ws ||
            manualCloseRef.current ||
            ws.readyState !== WebSocket.OPEN
          ) {
            return;
          }
          log.warn("ws: pong timeout, forcing reconnect");
          ws.close(PONG_TIMEOUT_CLOSE_CODE, "pong timeout");
        }, PONG_TIMEOUT_MS);
      };

      const sendHeartbeat = () => {
        if (
          !isCurrentGeneration() ||
          websocketRef.current !== ws ||
          ws.readyState !== WebSocket.OPEN ||
          manualCloseRef.current
        ) {
          return;
        }
        try {
          const message: ClientWebSocketMessage = { type: "ping" };
          ws.send(JSON.stringify(message));
          armPongDeadline();
        } catch (error) {
          log.warn("ws: heartbeat ping failed", error);
        }
      };

      ws.addEventListener("message", (event) => {
        if (!isCurrentGeneration() || websocketRef.current !== ws) {
          return;
        }
        if (typeof event.data !== "string") {
          return;
        }
        const message = parseWebSocketMessage(event.data);
        if (message === null) {
          log.warn("ws: invalid message ignored");
          return;
        }
        if (message.type === "pong") {
          cancelPongDeadline();
        }
        options.onMessage?.(message, ws);
      });

      ws.addEventListener("open", () => {
        if (!isCurrentGeneration()) {
          return;
        }
        cancelStableOpenTimer();
        if (websocketRef.current === ws) {
          cancelHeartbeatTimer();
          cancelPongDeadline();
          setConnectionState("open");
          sendHeartbeat();
          heartbeatTimerRef.current = setInterval(
            sendHeartbeat,
            HEARTBEAT_INTERVAL_MS,
          );
          stableOpenTimerRef.current = setTimeout(() => {
            stableOpenTimerRef.current = null;
            if (
              isCurrentGeneration() &&
              websocketRef.current === ws &&
              !manualCloseRef.current
            ) {
              attemptsRef.current = 0;
            }
          }, RECONNECT_STABLE_MS);
          options.onOpen?.(ws);
        }
      });

      ws.addEventListener("error", (event) => {
        if (!isCurrentGeneration() || websocketRef.current !== ws) {
          return;
        }
        options.onError?.(event, ws);
      });

      ws.addEventListener("close", (event) => {
        if (!isCurrentGeneration()) {
          return;
        }
        options.onClose?.(event, ws);
        if (websocketRef.current === ws) {
          cancelStableOpenTimer();
          cancelHeartbeatTimer();
          cancelPongDeadline();
        }
        if (websocketRef.current === ws) {
          websocketRef.current = null;
        }
        if (manualCloseRef.current || event.code === NORMAL_CLOSURE_CODE) {
          setConnectionState("closed");
          return;
        }
        const giveUp = lastOptionsRef.current?.onReconnectGiveUp;
        const nonRecoverableReason = closeCodeToGiveUpReason(event.code);
        if (nonRecoverableReason !== null) {
          log.error(
            `ws: non-recoverable close code ${event.code} (${nonRecoverableReason}); aborting reconnect`,
          );
          attemptsRef.current = 0;
          lastOptionsRef.current = null;
          setConnectionState(
            nonRecoverableReason === "auth" ? "auth_failed" : "closed",
          );
          giveUp?.(nonRecoverableReason);
          return;
        }
        if (lastOptionsRef.current?.onReconnect === undefined) {
          setConnectionState("closed");
          return;
        }

        const scheduleReconnect = () => {
          if (!isCurrentGeneration() || manualCloseRef.current) {
            return;
          }
          if (lastOptionsRef.current?.onReconnect === undefined) {
            return;
          }
          if (attemptsRef.current >= MAX_RECONNECT_ATTEMPTS) {
            log.error(
              `ws: gave up after ${attemptsRef.current} reconnect attempts`,
            );
            attemptsRef.current = 0;
            lastOptionsRef.current = null;
            setConnectionState("closed");
            giveUp?.("max_attempts");
            return;
          }
          const attemptIndex = attemptsRef.current;
          const attempt = attemptIndex + 1;
          attemptsRef.current = attempt;
          const delay = computeReconnectDelay(attemptIndex);
          setConnectionState("reconnecting");
          log.warn(
            `ws: reconnect attempt ${attempt} in ${Math.round(delay)}ms`,
          );
          reconnectTimerRef.current = setTimeout(() => {
            reconnectTimerRef.current = null;
            const opts = lastOptionsRef.current;
            if (manualCloseRef.current || opts === null) {
              return;
            }
            const newWs = openSocket(opts);
            opts.onReconnect?.(newWs, attempt);
          }, delay);
        };

        const beforeReconnect = lastOptionsRef.current?.onBeforeReconnect;
        if (beforeReconnect) {
          void Promise.resolve()
            .then(beforeReconnect)
            .then((reason) => {
              if (!isCurrentGeneration() || manualCloseRef.current) {
                return;
              }
              if (reason === null) {
                scheduleReconnect();
                return;
              }
              attemptsRef.current = 0;
              lastOptionsRef.current = null;
              setConnectionState(reason === "auth" ? "auth_failed" : "closed");
              giveUp?.(reason);
            })
            .catch((error) => {
              log.warn("ws: reconnect precheck failed", error);
              scheduleReconnect();
            });
          return;
        }
        scheduleReconnect();
      });

      return ws;
    },
    [cancelHeartbeatTimer, cancelPongDeadline, cancelStableOpenTimer],
  );

  const connectWebSocket = useCallback(
    (options: ConnectOptions = {}): WebSocket => {
      generationRef.current += 1;
      cancelPendingReconnect();
      cancelStableOpenTimer();
      cancelHeartbeatTimer();
      cancelPongDeadline();
      manualCloseRef.current = false;
      attemptsRef.current = 0;
      lastOptionsRef.current = options;
      setConnectionState("connecting");

      if (
        websocketRef.current &&
        websocketRef.current.readyState !== WebSocket.CLOSED
      ) {
        const stale = websocketRef.current;
        websocketRef.current = null;
        stale.close(NORMAL_CLOSURE_CODE);
      }

      return openSocket(options);
    },
    [
      cancelHeartbeatTimer,
      cancelPendingReconnect,
      cancelPongDeadline,
      cancelStableOpenTimer,
      openSocket,
    ],
  );

  const sendMessage = useCallback(
    ((type: ClientMessageType, payload?: unknown) => {
      const ws = websocketRef.current;
      if (ws?.readyState !== WebSocket.OPEN) {
        return false;
      }

      const message = (
        payload === undefined ? { type } : { type, payload }
      ) as ClientWebSocketMessage;
      try {
        ws.send(JSON.stringify(message));
        return true;
      } catch (error) {
        log.warn("ws: send failed", error);
        return false;
      }
    }) as SendMessage,
    [],
  );

  useEffect(() => {
    return () => {
      generationRef.current += 1;
      cancelPendingReconnect();
      cancelStableOpenTimer();
      cancelHeartbeatTimer();
      cancelPongDeadline();
      manualCloseRef.current = true;
      setConnectionState("closing");
      if (
        websocketRef.current &&
        websocketRef.current.readyState !== WebSocket.CLOSED
      ) {
        websocketRef.current.close(NORMAL_CLOSURE_CODE);
      }
      websocketRef.current = null;
      setConnectionState("closed");
    };
  }, [
    cancelHeartbeatTimer,
    cancelPendingReconnect,
    cancelPongDeadline,
    cancelStableOpenTimer,
  ]);

  return {
    websocketRef,
    connectionState,
    connectWebSocket,
    sendMessage,
    closeWebSocket,
  };
};
