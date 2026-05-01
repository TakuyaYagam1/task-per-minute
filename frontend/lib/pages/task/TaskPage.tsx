"use client";

import React, { useState, useEffect, useCallback, useRef } from "react";
import { useRouter } from "next/navigation";

import { GameState, FlagStatus, WebSocketMessage } from "../../shared/types";
import { gameModel } from "../../entities/game";
import { playerModel } from "../../entities/player";
import { TaskDescription } from "../../entities/task";
import { FlagSubmitForm, useWebSocket } from "../../features/flag-submit";
import { GameHeader } from "../../widgets/game-header";
import { GameResultModal } from "../../widgets/game-result-modal";

const STYLES = {
  main: {
    minHeight: "100vh",
    backgroundColor: "#3E7284",
    display: "flex",
    flexDirection: "column" as const,
    alignItems: "center",
    justifyContent: "center",
    fontFamily: "Inter, sans-serif",
    color: "#FFF",
    padding: "0.75rem",
    boxSizing: "border-box" as const,
  },

  container: {
    width: "100%",
    maxWidth: "900px",
    display: "flex",
    flexDirection: "column" as const,
    gap: "1rem",
  },

  notification: {
    position: "fixed" as const,
    top: "2rem",
    left: "50%",
    transform: "translateX(-50%)",
    backgroundColor: "rgba(0, 0, 0, 0.9)",
    color: "#FFF",
    padding: "1rem 2rem",
    borderRadius: "0.5rem",
    zIndex: 1000,
    boxShadow: "0 4px 15px rgba(0, 0, 0, 0.3)",
  },
};

// Страница с игровым заданием и формой отправки флага
export const TaskPage: React.FC = () => {
  const router = useRouter();
  const [gameState, setGameState] = useState<GameState>("playing");
  const [hasReceivedResult, setHasReceivedResult] = useState(false);
  const [flagStatus, setFlagStatus] = useState<FlagStatus>("idle");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [notification, setNotification] = useState<string | null>(null);

  const [timeLimit, setTimeLimit] = useState(300);
  const [taskUrl, setTaskUrl] = useState("");
  const [taskTitle, setTaskTitle] = useState("");
  const [taskDescription, setTaskDescription] = useState("");
  const [sessionId, setSessionId] = useState<number | null>(null);
  const [playerId, setPlayerId] = useState<number | null>(null);  const { websocketRef, connectWebSocket, sendMessage } = useWebSocket();
  
  const handleWebSocketMessage = useCallback((message: WebSocketMessage) => {
    if (
      (message.type === "game_won" ||
        message.type === "game_lost" ||
        message.type === "game_end") &&
      hasReceivedResult
    ) {
      console.log("Ignoring duplicate game result message:", message.type);
      return;
    }

    switch (message.type) {
      case "flag_correct":
        setIsSubmitting(false);
        if (message.data?.correct) {
          setFlagStatus("correct");
          if (!hasReceivedResult) {
            setGameState("won");
            setHasReceivedResult(true);
            gameModel.saveGameResult({ state: "won" });
          }
        } else {
          setFlagStatus("incorrect");
          setTimeout(() => setFlagStatus("idle"), 3000);
        }
        break;

      case "opponent_solved":
        if (!hasReceivedResult) {
          setGameState("lost");
          setHasReceivedResult(true);
          gameModel.saveGameResult({ state: "lost" });
        }
        break;

      case "game_end":
        console.log(
          "Received game_end:",
          message.data,
          "Current player ID:",
          playerId
        );

        if (!hasReceivedResult) {
          setHasReceivedResult(true);

          if (message.data?.winner_id) {
            if (message.data.winner_id === playerId) {
              console.log("Player is the winner!");
              setGameState("won");
              gameModel.saveGameResult({ state: "won" });
            } else {
              console.log("Player is not the winner, setting lost state");
              setGameState("lost");
              gameModel.saveGameResult({ state: "lost" });
            }
          } else {
            console.log("No winner, time is up");
            setGameState("timeup");
            gameModel.saveGameResult({ state: "timeup" });
          }
        }
        break;

      case "game_won":
        console.log("Received game_won:", message.data);
        if (!hasReceivedResult) {
          const winnerIdFromMessage = message.data?.winner_id;
          const sessionIdFromMessage = message.data?.session_id;

          if (
            winnerIdFromMessage &&
            parseInt(winnerIdFromMessage.toString()) === playerId &&
            sessionIdFromMessage &&
            parseInt(sessionIdFromMessage.toString()) === sessionId
          ) {
            console.log("Показываем победу для игрока", playerId);
            setHasReceivedResult(true);
            setGameState("won");
            gameModel.saveGameResult({
              state: "won",
              reason: message.data?.reason || "Вы победили!",
              winner_id: winnerIdFromMessage,
              loser_id: message.data?.loser_id,
              session_id: sessionIdFromMessage,
            });
          }
        }
        break;

      case "game_lost":
        console.log("Received game_lost:", message.data);
        if (!hasReceivedResult) {
          const loserIdFromMessage = message.data?.loser_id;
          const sessionIdFromMessage = message.data?.session_id;

          if (
            loserIdFromMessage &&
            parseInt(loserIdFromMessage.toString()) === playerId &&
            sessionIdFromMessage &&
            parseInt(sessionIdFromMessage.toString()) === sessionId
          ) {
            console.log("Показываем поражение для игрока", playerId);
            setHasReceivedResult(true);
            setGameState("lost");
            gameModel.saveGameResult({
              state: "lost",
              reason: message.data?.reason || "Вы проиграли",
              winner_id: message.data?.winner_id,
              loser_id: loserIdFromMessage,
              session_id: sessionIdFromMessage,
            });
          }
        }
        break;

      default:
        console.log("Unhandled message type in task page:", message.type);
    }
  }, [hasReceivedResult, playerId, sessionId]);

  const setupWebSocketListeners = useCallback((ws: WebSocket) => {
    ws.onmessage = (event) => {
      try {
        const message: WebSocketMessage = JSON.parse(event.data);
        console.log("Task page received message:", message);

        if (message.type === "ping") {
          ws.send(JSON.stringify({ type: "pong", data: message.data }));
          console.log("Received ping from server, sending pong");
          return;
        }

        if (message.type === "pong") {
          console.log("Received pong from server");
          return;
        }

        handleWebSocketMessage(message);
      } catch (error) {
        console.error("Error parsing WebSocket message in task page:", error);
      }
    };
  }, [handleWebSocketMessage]);
  const setupWebSocket = useCallback(
    (playerId: number) => {
      if (
        window.gameWebSocket &&
        window.gameWebSocket.readyState === WebSocket.OPEN
      ) {
        setupWebSocketListeners(window.gameWebSocket);
      } else {
        console.log("WebSocket not connected, attempting to reconnect...");
        const ws = connectWebSocket(playerId);
        setupWebSocketListeners(ws);
      }
    },
    [connectWebSocket, setupWebSocketListeners]
  );
  useEffect(() => {
    const currentWebSocketRef = websocketRef.current;

    const gameData = gameModel.getGameData();
    const gameResult = gameModel.getGameResult();
    const currentPlayer = playerModel.getCurrentPlayer();

    if (gameResult) {
      console.log("Found saved game result:", gameResult);
      setGameState(gameResult.state);
      setHasReceivedResult(true);
    }

    setTimeLimit(gameData.timeLimit);
    setTaskUrl(gameData.taskUrl);
    setTaskTitle(gameData.taskTitle);
    setTaskDescription(gameData.taskDescription);
    setSessionId(gameData.sessionId);
    setPlayerId(currentPlayer?.id || null);

    const remainingTime = gameModel.calculateRemainingTime();
    if (remainingTime > 0) {
      setTimeLimit(remainingTime);
    } else if (!hasReceivedResult) {
      setGameState("timeup");
    }

    if (gameState === "playing" && sessionId && currentPlayer) {
      setupWebSocket(currentPlayer.id);
    }

    const preventBack = (e: PopStateEvent) => {
      if (gameState === "playing" && sessionId) {
        window.history.pushState(null, "", window.location.href);
      }
    };

    if (sessionId && gameState === "playing") {
      window.history.pushState(null, "", window.location.href);
      window.addEventListener("popstate", preventBack);
    }
    return () => {
      window.removeEventListener("popstate", preventBack);
      if (
        currentWebSocketRef &&
        currentWebSocketRef.readyState !== WebSocket.CLOSED
      ) {
        console.log("Closing WebSocket connection on component unmount");
        currentWebSocketRef.close();
        window.gameWebSocket = undefined;
      }
    };  }, [gameState, sessionId, hasReceivedResult, setupWebSocket, websocketRef]);

  // Обрабатывает отправку флага игроком
  const handleFlagSubmit = async (flag: string) => {
    if (!sessionId || !playerId) {
      setFlagStatus("incorrect");
      setTimeout(() => setFlagStatus("idle"), 3000);
      return;
    }

    setIsSubmitting(true);
    setFlagStatus("idle");

    try {
      if (
        websocketRef.current &&
        websocketRef.current.readyState === WebSocket.OPEN
      ) {
        sendMessage(
          "flag_submit",
          {
            session_id: sessionId,
            flag: flag,
          },
          playerId
        );
        console.log("Flag submitted via WebSocket");
      } else {
        const { playerApi } = await import("../../shared/api");
        const result = await playerApi.submitFlag(sessionId, playerId, flag);

        setIsSubmitting(false);
        if (result.correct) {
          setFlagStatus("correct");
          setGameState("won");
          gameModel.saveGameResult({ state: "won" });
        } else {
          setFlagStatus("incorrect");
          setTimeout(() => setFlagStatus("idle"), 3000);
        }
      }
    } catch (error) {
      console.error("Error submitting flag:", error);
      setFlagStatus("incorrect");
      setTimeout(() => setFlagStatus("idle"), 3000);
      setIsSubmitting(false);
    }
  };

  // Обрабатывает истечение времени игры
  const handleTimeUp = () => {
    if (!hasReceivedResult) {
      setGameState("timeup");
      setHasReceivedResult(true);
      gameModel.saveGameResult({ state: "timeup" });
    }
  };

  // Возвращает игрока на главную страницу
  const handleReturnHome = () => {
    gameModel.clearGameData();
    router.push("/");
  };

  return (
    <main style={STYLES.main}>
      {notification && <div style={STYLES.notification}>{notification}</div>}

      <div style={STYLES.container}>
        <GameHeader
          taskTitle={taskTitle}
          timeLimit={timeLimit}
          onTimeUp={handleTimeUp}
        />

        <TaskDescription
          title={taskTitle}
          description={taskDescription}
          taskUrl={taskUrl}
        />

        <FlagSubmitForm
          onSubmit={handleFlagSubmit}
          isSubmitting={isSubmitting}
          flagStatus={flagStatus}
          disabled={gameState !== "playing"}
        />
      </div>

      {gameState !== "playing" && (
        <GameResultModal
          gameState={gameState}
          onReturnHome={handleReturnHome}
        />
      )}
    </main>
  );
};
