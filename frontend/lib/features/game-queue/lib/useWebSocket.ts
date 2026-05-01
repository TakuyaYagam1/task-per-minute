"use client";

import { useRef, useCallback } from "react";
import { MessageType, WebSocketMessage, GameWebSocket } from "../../../shared/types";
import { CONFIG } from "../../../shared/config";

// Хук для управления WebSocket соединениями в игре
export const useWebSocket = () => {
  const websocketRef = useRef<GameWebSocket | null>(null);

  // Устанавливает WebSocket соединение для указанного игрока
  const connectWebSocket = useCallback((playerId: number) => {
    if (websocketRef.current && websocketRef.current.readyState !== WebSocket.CLOSED) {
      console.log("Closing existing WebSocket connection before creating new one");
      websocketRef.current.close();
      websocketRef.current = null;
      window.gameWebSocket = undefined;
    }

    const ws = new WebSocket(
      `${CONFIG.websocketUrl}?player_id=${playerId}`
    ) as GameWebSocket;
    ws.isGameSocket = true;

    ws.onopen = () => {
      console.log("WebSocket connected for player:", playerId);
      websocketRef.current = ws;
      window.gameWebSocket = ws;
    };

    ws.onclose = () => {
      console.log("WebSocket disconnected");
      websocketRef.current = null;
      if (window.gameWebSocket?.isGameSocket) {
        window.gameWebSocket = undefined;
      }
    };

    ws.onerror = (error) => {
      console.error("WebSocket error:", error);
    };

    return ws;
  }, []);

  // Отправляет сообщение через WebSocket
  const sendMessage = useCallback((type: MessageType, data?: any, playerId?: number) => {
    if (websocketRef.current?.readyState === WebSocket.OPEN) {
      const message: WebSocketMessage = {
        type,
        data,
        player_id: playerId,
      };
      console.log("Sending WebSocket message:", message);
      websocketRef.current.send(JSON.stringify(message));
    } else {
      console.error("WebSocket not ready, state:", websocketRef.current?.readyState);
    }
  }, []);

  // Закрывает WebSocket соединение
  const closeWebSocket = useCallback(() => {
    if (websocketRef.current && websocketRef.current.readyState !== WebSocket.CLOSED) {
      console.log("Closing WebSocket connection");
      websocketRef.current.close();
      websocketRef.current = null;
      window.gameWebSocket = undefined;
    }
  }, []);

  return {
    websocketRef,
    connectWebSocket,
    sendMessage,
    closeWebSocket,
  };
};
