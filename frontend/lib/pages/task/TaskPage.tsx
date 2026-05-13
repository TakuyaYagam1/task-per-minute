"use client";

import { useRouter } from "next/navigation";
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import styles from "../../../app/task/task.module.css";

import { gameModel } from "../../entities/game";
import { playerModel } from "../../entities/player";
import { useWebSocket } from "../../features/game-queue";
import {
  isInvalidSessionWebSocketMessage,
  isSafeNavigationURL,
  log,
  openExternalUrl,
  parseWebSocketMessage,
  redirectNotificationStorage,
  useTimedNotification,
} from "../../shared/lib";
import {
  FlagStatus,
  GameData,
  GameState,
  HintUnlockedPayload,
  TaskPayload,
  WebSocketMessage,
} from "../../shared/types";

type TaskCategory = TaskPayload["category"];
type TaskDifficulty = TaskPayload["difficulty"];
type TerminalSource = "none" | "local_timer" | "server";
type SafeNavigationResult = "opened" | "copied" | "failed";

interface HintView {
  index: number;
  hint?: string;
  unlockedAt?: string;
}

const CATEGORY_CONFIG: Record<
  TaskCategory,
  { label: string; icon: string; color: string }
> = {
  web: { label: "Web", icon: "🌐", color: "#72d1eb" },
  crypto: { label: "Crypto", icon: "🔐", color: "#fbbf24" },
  forensics: { label: "Forensics", icon: "🔍", color: "#a78bfa" },
  reverse: { label: "Reverse", icon: "⚙️", color: "#f472b6" },
  pwn: { label: "Pwn", icon: "💥", color: "#ef4444" },
  steganography: { label: "Steganography", icon: "🖼️", color: "#38bdf8" },
  ppc: { label: "PPC", icon: "🧮", color: "#fb7185" },
  osint: { label: "OSINT", icon: "🛰️", color: "#22c55e" },
  mobile: { label: "Mobile", icon: "📱", color: "#60a5fa" },
  hardware: { label: "Hardware", icon: "🔧", color: "#f97316" },
  misc: { label: "Misc", icon: "🧩", color: "#34d399" },
};

const DIFFICULTY_CONFIG: Record<
  TaskDifficulty,
  { label: string; badgeClass: string }
> = {
  easy: { label: "Easy", badgeClass: styles.badgeEasy },
  medium: { label: "Medium", badgeClass: styles.badgeMedium },
  hard: { label: "Hard", badgeClass: styles.badgeHard },
};

const formatTime = (seconds: number): string => {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m.toString().padStart(2, "0")}:${s.toString().padStart(2, "0")}`;
};

const isHttpURL = (value: string): boolean =>
  /^https?:\/\//i.test(value.trim());

const buildHints = (task: TaskPayload): HintView[] => {
  const unlocked = new Map<number, HintUnlockedPayload>();
  for (const hint of task.unlocked_hints || []) {
    unlocked.set(hint.hint_index, {
      duel_id: "",
      task_id: task.id,
      hint_index: hint.hint_index,
      hint: hint.hint,
      unlocked_at: hint.unlocked_at,
    });
  }

  const count = Math.max(task.hint_schedule?.length || 0, unlocked.size, 3);
  return Array.from({ length: count }, (_, offset) => {
    const index = offset + 1;
    const item = unlocked.get(index);
    return {
      index,
      hint: item?.hint,
      unlockedAt: item?.unlocked_at,
    };
  });
};

export const TaskPage: React.FC = () => {
  const router = useRouter();
  const { connectWebSocket, sendMessage, closeWebSocket } = useWebSocket();

  const [gameData, setGameData] = useState<GameData | null>(null);
  const [gameState, setGameState] = useState<GameState>("playing");
  const [flagStatus, setFlagStatus] = useState<FlagStatus>("idle");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isSurrendering, setIsSurrendering] = useState(false);
  const [timeLeft, setTimeLeft] = useState(0);
  const [hints, setHints] = useState<HintView[]>([]);
  const [taskOpened, setTaskOpened] = useState(false);
  const [flagInput, setFlagInput] = useState("");
  const [isPaused, setIsPaused] = useState(false);
  const [opponentReconnectDeadline, setOpponentReconnectDeadline] = useState<
    string | undefined
  >();

  const currentPlayer = useMemo(() => playerModel.getCurrentPlayer(), []);
  const gameDataRef = useRef<GameData | null>(null);
  const currentPlayerRef = useRef(currentPlayer);
  const hasFinished = useRef(false);
  const terminalSource = useRef<TerminalSource>("none");
  const flagStatusTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const {
    notification,
    setNotification,
    showNotification: showTimedNotification,
  } = useTimedNotification<string>();

  useEffect(() => {
    currentPlayerRef.current = currentPlayer;
  }, [currentPlayer]);

  const clearFlagStatusTimer = useCallback(() => {
    if (flagStatusTimer.current) {
      clearTimeout(flagStatusTimer.current);
      flagStatusTimer.current = null;
    }
  }, []);

  const isCurrentDuelID = useCallback((duelID?: string): boolean => {
    const currentDuelID = gameDataRef.current?.duel_id;
    if (!currentDuelID || duelID !== currentDuelID) {
      log.error("Ignored WebSocket event for unexpected duel");
      return false;
    }
    return true;
  }, []);

  const isCurrentTaskID = useCallback((taskID?: string): boolean => {
    const currentTaskID = gameDataRef.current?.task.id;
    if (!currentTaskID || taskID !== currentTaskID) {
      log.error("Ignored WebSocket hint for unexpected task");
      return false;
    }
    return true;
  }, []);

  const isExpectedOpponentPlayerID = useCallback(
    (playerID?: string): boolean => {
      const activePlayer = currentPlayerRef.current;
      const opponentID = gameDataRef.current?.opponent_id;
      if (!activePlayer || !playerID || playerID === activePlayer.id) {
        log.error("Ignored WebSocket opponent event for current player");
        return false;
      }
      if (opponentID && playerID !== opponentID) {
        log.error("Ignored WebSocket opponent event for unexpected player");
        return false;
      }
      return true;
    },
    [],
  );

  const isExpectedResumeOpponentID = useCallback(
    (opponentID?: string): boolean => {
      const activePlayer = currentPlayerRef.current;
      const expectedOpponentID = gameDataRef.current?.opponent_id;
      if (!opponentID) {
        return true;
      }
      if (!activePlayer || opponentID === activePlayer.id) {
        log.error("Ignored duel_resume with current player as opponent");
        return false;
      }
      if (expectedOpponentID && opponentID !== expectedOpponentID) {
        log.error("Ignored duel_resume for unexpected opponent");
        return false;
      }
      return true;
    },
    [],
  );

  const setPersistentFlagStatus = useCallback(
    (status: FlagStatus) => {
      clearFlagStatusTimer();
      setFlagStatus(status);
    },
    [clearFlagStatusTimer],
  );

  const showTemporaryFlagStatus = useCallback(
    (status: Exclude<FlagStatus, "idle">, duration = 3000) => {
      clearFlagStatusTimer();
      setFlagStatus(status);
      flagStatusTimer.current = setTimeout(() => {
        flagStatusTimer.current = null;
        setFlagStatus("idle");
      }, duration);
    },
    [clearFlagStatusTimer],
  );

  const updateGameData = useCallback((patch: Partial<GameData>) => {
    setGameData((prev) => {
      if (!prev) {
        return prev;
      }
      const next = { ...prev, ...patch };
      gameDataRef.current = next;
      gameModel.saveGameData(next);
      return next;
    });
  }, []);

  const recoverActiveDuelFromLocalTimer = useCallback(() => {
    if (terminalSource.current !== "local_timer") {
      return;
    }
    terminalSource.current = "none";
    hasFinished.current = false;
    clearFlagStatusTimer();
    setGameState("playing");
    gameModel.clearGameResult();
  }, [clearFlagStatusTimer]);

  const shouldApplyActiveDuelEvent = useCallback(
    (duelID?: string): boolean => {
      if (!isCurrentDuelID(duelID)) {
        return false;
      }
      if (terminalSource.current === "server") {
        return false;
      }
      recoverActiveDuelFromLocalTimer();
      return true;
    },
    [isCurrentDuelID, recoverActiveDuelFromLocalTimer],
  );

  const clearActiveDuelState = useCallback(
    (persistGameData = true) => {
      setIsSubmitting(false);
      setIsSurrendering(false);
      clearFlagStatusTimer();
      setIsPaused(false);
      setOpponentReconnectDeadline(undefined);
      if (!persistGameData) {
        setGameData((prev) => {
          if (!prev) {
            return prev;
          }
          const next = {
            ...prev,
            opponent_disconnected: false,
            opponent_reconnect_deadline: undefined,
          };
          gameDataRef.current = next;
          return next;
        });
        return;
      }
      updateGameData({
        opponent_disconnected: false,
        opponent_reconnect_deadline: undefined,
      });
    },
    [clearFlagStatusTimer, updateGameData],
  );

  const clearStoredPlayerSessionForNextEntrant = useCallback(() => {
    void playerModel.clearCurrentPlayer();
  }, []);

  const handleExpiredSession = useCallback(() => {
    hasFinished.current = true;
    terminalSource.current = "none";
    void playerModel.clearCurrentPlayer();
    gameModel.clearGameData();
    gameDataRef.current = null;
    clearFlagStatusTimer();
    setGameData(null);
    setGameState("playing");
    setFlagStatus("idle");
    setIsSubmitting(false);
    setIsSurrendering(false);
    setHints([]);
    setTaskOpened(false);
    setFlagInput("");
    setIsPaused(false);
    setOpponentReconnectDeadline(undefined);
    closeWebSocket();
    const message = "Сессия истекла. Возвращаемся на главную.";
    redirectNotificationStorage.set(message);
    setNotification(message);
    router.replace("/");
  }, [clearFlagStatusTimer, closeWebSocket, router, setNotification]);

  const redirectStaleActiveDuel = useCallback(
    (message: string) => {
      hasFinished.current = true;
      terminalSource.current = "none";
      gameModel.clearGameData();
      gameDataRef.current = null;
      clearFlagStatusTimer();
      setGameData(null);
      setGameState("playing");
      setFlagStatus("idle");
      setIsSubmitting(false);
      setIsSurrendering(false);
      setHints([]);
      setTaskOpened(false);
      setFlagInput("");
      setIsPaused(false);
      setOpponentReconnectDeadline(undefined);
      closeWebSocket();
      redirectNotificationStorage.set(message);
      setNotification(message);
      router.replace("/");
    },
    [clearFlagStatusTimer, closeWebSocket, router, setNotification],
  );

  const finishDuel = useCallback(
    (message: Extract<WebSocketMessage, { type: "duel_finished" }>) => {
      const activePlayer = currentPlayerRef.current;
      if (
        !message.payload ||
        !activePlayer ||
        terminalSource.current === "server"
      ) {
        return;
      }
      terminalSource.current = "server";
      hasFinished.current = true;
      clearActiveDuelState(false);

      const winnerID = message.payload.winner_id || null;
      const state: GameState =
        winnerID === null
          ? "timeup"
          : winnerID === activePlayer.id
            ? "won"
            : "lost";
      setGameState(state);
      gameModel.saveGameResult({
        state,
        source: "server",
        duel_id: message.payload.duel_id,
        winner_id: winnerID,
        winner_username: message.payload.winner_username || null,
      });
      gameModel.clearCurrentGame();
      clearStoredPlayerSessionForNextEntrant();
    },
    [clearActiveDuelState, clearStoredPlayerSessionForNextEntrant],
  );

  const handleWebSocketMessage = useCallback(
    (message: WebSocketMessage) => {
      switch (message.type) {
        case "pong":
          break;

        case "flag_result":
          if (
            !message.payload ||
            !shouldApplyActiveDuelEvent(message.payload.duel_id)
          ) {
            break;
          }
          setIsSubmitting(false);
          if (message.payload.correct) {
            setPersistentFlagStatus("correct");
          } else {
            showTemporaryFlagStatus("incorrect");
          }
          break;

        case "hint_unlocked":
          if (
            !message.payload ||
            !isCurrentDuelID(message.payload.duel_id) ||
            !isCurrentTaskID(message.payload.task_id) ||
            !shouldApplyActiveDuelEvent(message.payload.duel_id)
          ) {
            break;
          }
          const unlockedHint = message.payload;
          setHints((prev) => {
            const next = [...prev];
            const index = next.findIndex(
              (hint) => hint.index === unlockedHint.hint_index,
            );
            const item = {
              index: unlockedHint.hint_index,
              hint: unlockedHint.hint,
              unlockedAt: unlockedHint.unlocked_at,
            };
            if (index >= 0) {
              next[index] = item;
              return next;
            }
            return [...next, item].sort((a, b) => a.index - b.index);
          });
          break;

        case "opponent_solved":
          if (
            !message.payload ||
            !isCurrentDuelID(message.payload.duel_id) ||
            !isExpectedOpponentPlayerID(message.payload.player_id) ||
            !shouldApplyActiveDuelEvent(message.payload.duel_id)
          ) {
            break;
          }
          setNotification(
            "Соперник уже решил задание. Ждем завершение дуэли...",
          );
          break;

        case "opponent_disconnected":
          if (
            !message.payload ||
            !isCurrentDuelID(message.payload.duel_id) ||
            !isExpectedOpponentPlayerID(message.payload.player_id) ||
            !shouldApplyActiveDuelEvent(message.payload.duel_id)
          ) {
            break;
          }
          setIsPaused(true);
          setOpponentReconnectDeadline(message.payload.reconnect_deadline);
          updateGameData({
            opponent_disconnected: true,
            opponent_reconnect_deadline: message.payload.reconnect_deadline,
          });
          setNotification(
            "Соперник отключился. Отправка флага временно недоступна.",
          );
          break;

        case "opponent_reconnected":
          if (
            !message.payload ||
            !isCurrentDuelID(message.payload.duel_id) ||
            !isExpectedOpponentPlayerID(message.payload.player_id) ||
            !shouldApplyActiveDuelEvent(message.payload.duel_id)
          ) {
            break;
          }
          setIsPaused(false);
          setOpponentReconnectDeadline(undefined);
          setTimeLeft(
            gameModel.calculateRemainingTime(message.payload.deadline),
          );
          updateGameData({
            deadline: message.payload.deadline,
            opponent_disconnected: false,
            opponent_reconnect_deadline: undefined,
          });
          setNotification("Соперник вернулся. Дуэль продолжается.");
          break;

        case "duel_resume":
          if (
            !message.payload ||
            !isExpectedResumeOpponentID(message.payload.opponent_id) ||
            !shouldApplyActiveDuelEvent(message.payload.duel_id)
          ) {
            break;
          }
          const resume = message.payload;
          const patch: Partial<GameData> = {
            deadline: resume.deadline,
            opponent_disconnected: resume.opponent_disconnected,
            opponent_reconnect_deadline: resume.opponent_reconnect_deadline,
          };
          if (resume.opponent_id) {
            patch.opponent_id = resume.opponent_id;
          }
          if (resume.task) {
            patch.task = resume.task;
            patch.time_limit_seconds = resume.task.time_limit_seconds;
          }
          updateGameData(patch);
          setTimeLeft(gameModel.calculateRemainingTime(resume.deadline));
          setIsPaused(Boolean(resume.opponent_disconnected));
          setOpponentReconnectDeadline(resume.opponent_reconnect_deadline);
          if (resume.task) {
            setHints(buildHints(resume.task));
          }
          break;

        case "duel_expired":
          if (!message.payload || !isCurrentDuelID(message.payload.duel_id)) {
            break;
          }
          if (terminalSource.current !== "server") {
            terminalSource.current = "server";
            hasFinished.current = true;
            clearActiveDuelState(false);
            setGameState("timeup");
            gameModel.saveGameResult({
              state: "timeup",
              source: "server",
              duel_id: message.payload.duel_id,
            });
            gameModel.clearCurrentGame();
            clearStoredPlayerSessionForNextEntrant();
          }
          break;

        case "duel_finished":
          if (!message.payload || !isCurrentDuelID(message.payload.duel_id)) {
            break;
          }
          finishDuel(message);
          break;

        case "error":
          log.warn("ws server error", {
            code: message.code,
            message: message.message,
          });
          if (isInvalidSessionWebSocketMessage(message)) {
            handleExpiredSession();
            break;
          }
          if (hasFinished.current) {
            break;
          }
          setIsSubmitting(false);
          setIsSurrendering(false);
          if (message.code === "duel.paused") {
            setPersistentFlagStatus("idle");
            setIsPaused(true);
            setOpponentReconnectDeadline(undefined);
            updateGameData({
              opponent_disconnected: true,
              opponent_reconnect_deadline: undefined,
            });
            setNotification("Дуэль на паузе: дождитесь возвращения соперника.");
            break;
          }
          setNotification(message.message || message.code || "Ошибка сервера");
          break;

        default:
          break;
      }
    },
    [
      clearActiveDuelState,
      clearStoredPlayerSessionForNextEntrant,
      finishDuel,
      handleExpiredSession,
      isCurrentDuelID,
      isCurrentTaskID,
      isExpectedOpponentPlayerID,
      isExpectedResumeOpponentID,
      shouldApplyActiveDuelEvent,
      setPersistentFlagStatus,
      setNotification,
      showTemporaryFlagStatus,
      updateGameData,
    ],
  );

  useEffect(() => {
    return clearFlagStatusTimer;
  }, [clearFlagStatusTimer]);

  useEffect(() => {
    const storedGame = gameModel.getGameData();
    const storedResult = gameModel.getGameResult();

    if (!storedGame) {
      router.replace("/");
      return;
    }

    const hydrateStoredGame = () => {
      gameDataRef.current = storedGame;
      setGameData(storedGame);
      setHints(buildHints(storedGame.task));
      setIsPaused(Boolean(storedGame.opponent_disconnected));
      setOpponentReconnectDeadline(storedGame.opponent_reconnect_deadline);
      setTimeLeft(gameModel.calculateRemainingTime(storedGame.deadline));
    };
    let storedGameHydrated = false;
    const ensureStoredGameHydrated = () => {
      if (storedGameHydrated) {
        return;
      }
      storedGameHydrated = true;
      hydrateStoredGame();
    };

    const hasMatchingTerminalResult =
      storedResult &&
      storedResult.state !== "playing" &&
      storedResult.source !== "local_timer" &&
      (!storedResult.duel_id || storedResult.duel_id === storedGame.duel_id);

    if (hasMatchingTerminalResult) {
      hydrateStoredGame();
      hasFinished.current = true;
      terminalSource.current = "server";
      setGameState(storedResult.state);
      return;
    }

    if (!currentPlayer) {
      router.replace("/");
      return;
    }

    if (storedResult) {
      gameModel.clearGameResult();
    }

    const setupWebSocketListeners = (websocket: WebSocket) => {
      websocket.onmessage = (event) => {
        if (typeof event.data !== "string") {
          log.error("Ignored non-text WebSocket message");
          return;
        }
        const message = parseWebSocketMessage(event.data);
        if (!message) {
          log.error("Ignored invalid WebSocket message");
          return;
        }
        handleWebSocketMessage(message);
      };
      websocket.onclose = () => {
        if (hasFinished.current) {
          return;
        }
        setIsSubmitting(false);
        setIsSurrendering(false);
      };
      websocket.onerror = () => {
        if (hasFinished.current) {
          return;
        }
        setIsSubmitting(false);
        setIsSurrendering(false);
      };
    };

    const preventBack = () => {
      if (!hasFinished.current) {
        window.history.pushState(null, "", window.location.href);
      }
    };

    window.history.pushState(null, "", window.location.href);
    window.addEventListener("popstate", preventBack);

    const controller = new AbortController();
    let cancelled = false;

    const openActiveDuelWebSocket = () => {
      const activePlayer = currentPlayerRef.current;
      if (!activePlayer || cancelled) {
        return;
      }
      const ws = connectWebSocket({
        onReconnect: (newWs) => {
          setupWebSocketListeners(newWs);
        },
        onBeforeReconnect: async () => {
          const player = currentPlayerRef.current;
          if (!player) {
            return "auth";
          }
          const refreshResult = await playerModel.refreshCurrentPlayer(player);
          if (refreshResult.kind === "expired") {
            return "auth";
          }
          if (refreshResult.kind === "ok") {
            currentPlayerRef.current = refreshResult.state.player;
            const activeDuelID = refreshResult.state.activeDuel?.id;
            const currentDuelID = gameDataRef.current?.duel_id;
            if (
              !activeDuelID ||
              !currentDuelID ||
              activeDuelID !== currentDuelID
            ) {
              redirectStaleActiveDuel(
                "Активная дуэль не найдена. Возвращаемся на главную.",
              );
              return "stale_duel";
            }
          }
          return null;
        },
        onReconnectGiveUp: (reason) => {
          if (hasFinished.current) {
            return;
          }
          if (reason === "stale_duel") {
            return;
          }
          setIsSubmitting(false);
          setIsSurrendering(false);
          if (reason === "auth" || reason === "forbidden") {
            handleExpiredSession();
            return;
          }
          setNotification("Соединение потеряно. Обновите страницу.");
        },
      });
      setupWebSocketListeners(ws);
    };

    void (async () => {
      const result = await playerModel.refreshCurrentPlayer(
        currentPlayer,
        controller.signal,
      );
      if (cancelled) {
        return;
      }
      if (result.kind === "expired") {
        handleExpiredSession();
        return;
      }
      if (result.kind === "aborted") {
        return;
      }
      if (result.kind === "ok") {
        let sessionState = result.state;
        if (!sessionState.activeDuel) {
          const retry = await playerModel.refreshCurrentPlayer(
            sessionState.player,
            controller.signal,
          );
          if (cancelled) {
            return;
          }
          if (retry.kind === "expired") {
            handleExpiredSession();
            return;
          }
          if (retry.kind === "aborted") {
            return;
          }
          if (retry.kind === "ok") {
            sessionState = retry.state;
          } else {
            log.warn(
              `players/me retry returned ${retry.kind}; keeping local task restore`,
            );
            ensureStoredGameHydrated();
            openActiveDuelWebSocket();
            return;
          }
        }
        currentPlayerRef.current = sessionState.player;
        if (
          !sessionState.activeDuel ||
          sessionState.activeDuel.id !== storedGame.duel_id
        ) {
          redirectStaleActiveDuel(
            "Активная дуэль не найдена. Возвращаемся на главную.",
          );
          return;
        }
      } else {
        log.warn(
          `players/me preflight returned ${result.kind}; keeping local task restore`,
        );
      }
      ensureStoredGameHydrated();
      openActiveDuelWebSocket();
    })();

    return () => {
      cancelled = true;
      controller.abort();
      window.removeEventListener("popstate", preventBack);
      closeWebSocket();
    };
  }, [
    clearFlagStatusTimer,
    closeWebSocket,
    connectWebSocket,
    currentPlayer,
    handleExpiredSession,
    handleWebSocketMessage,
    redirectStaleActiveDuel,
    router,
    setNotification,
  ]);

  useEffect(() => {
    if (gameState !== "playing" || !gameData?.deadline || isPaused) {
      return;
    }

    const tick = () => {
      const remaining = gameModel.calculateRemainingTime(gameData.deadline);
      setTimeLeft(remaining);
      if (remaining <= 0 && !hasFinished.current) {
        hasFinished.current = true;
        terminalSource.current = "local_timer";
        clearActiveDuelState();
        setGameState("timeup");
        gameModel.saveGameResult({
          state: "timeup",
          source: "local_timer",
          duel_id: gameData.duel_id,
        });
        clearStoredPlayerSessionForNextEntrant();
      }
    };

    tick();
    const interval = setInterval(tick, 1000);
    return () => clearInterval(interval);
  }, [
    clearActiveDuelState,
    clearStoredPlayerSessionForNextEntrant,
    gameData?.deadline,
    gameData?.duel_id,
    gameState,
    isPaused,
  ]);

  useEffect(() => {
    if (
      gameState !== "playing" ||
      !isPaused ||
      !opponentReconnectDeadline ||
      hasFinished.current
    ) {
      return;
    }
    const deadlineEpoch = Date.parse(opponentReconnectDeadline);
    if (!Number.isFinite(deadlineEpoch)) {
      return;
    }
    const FALLBACK_GRACE_MS = 5_000;
    const delay = Math.max(0, deadlineEpoch - Date.now()) + FALLBACK_GRACE_MS;
    const timer = setTimeout(() => {
      if (hasFinished.current || terminalSource.current === "server") {
        return;
      }
      hasFinished.current = true;
      terminalSource.current = "local_timer";
      clearActiveDuelState(false);
      setGameState("timeup");
      gameModel.clearGameData();
      clearStoredPlayerSessionForNextEntrant();
      const message = "Соперник не вернулся вовремя. Дуэль закрыта.";
      redirectNotificationStorage.set(message);
      setNotification(message);
      closeWebSocket();
      router.push("/");
    }, delay);
    return () => clearTimeout(timer);
  }, [
    clearActiveDuelState,
    clearStoredPlayerSessionForNextEntrant,
    closeWebSocket,
    gameState,
    isPaused,
    opponentReconnectDeadline,
    router,
    setNotification,
  ]);

  const handleFlagSubmit = () => {
    const flag = flagInput.trim();
    if (!flag || !gameData || !currentPlayer) {
      showTemporaryFlagStatus("incorrect");
      return;
    }
    if (isPaused) {
      setNotification("Дуэль на паузе: дождитесь возвращения соперника.");
      return;
    }

    setIsSubmitting(true);
    setPersistentFlagStatus("idle");

    const sent = sendMessage("flag_submit", {
      duel_id: gameData.duel_id,
      flag,
    });
    if (!sent) {
      setIsSubmitting(false);
      setNotification("Соединение потеряно. Обновите страницу для реконнекта.");
    }
  };

  const handleSurrender = () => {
    if (!gameData || gameState !== "playing" || isSurrendering) {
      return;
    }
    if (!window.confirm("Сдаться и завершить дуэль поражением?")) {
      return;
    }

    setIsSurrendering(true);
    const sent = sendMessage("surrender", {
      duel_id: gameData.duel_id,
    });
    if (!sent) {
      setIsSurrendering(false);
      setNotification("Соединение потеряно. Обновите страницу для реконнекта.");
    }
  };

  const handleReturnHome = async () => {
    gameModel.clearGameData();
    await playerModel.clearCurrentPlayer();
    closeWebSocket();
    router.push("/");
  };

  const taskData = gameData?.task || null;
  const taskSourceURL =
    taskData?.source_url || taskData?.source_file_url || null;
  const taskTarget = taskData?.task_url?.trim() || null;
  const isExternalTaskURL = Boolean(taskTarget && isHttpURL(taskTarget));

  const openWithSafeNavigationGuard = async (
    url: string,
    fallbackLabel: string,
  ): Promise<SafeNavigationResult> => {
    if (isSafeNavigationURL(url)) {
      openExternalUrl(url);
      return "opened";
    }
    try {
      if (
        !navigator.clipboard ||
        typeof navigator.clipboard.writeText !== "function"
      ) {
        showTimedNotification(
          `${fallbackLabel} небезопасен (mixed content). Откройте вручную: ${url}`,
        );
        return "failed";
      }
      await navigator.clipboard.writeText(url);
      showTimedNotification(
        `${fallbackLabel} небезопасен (mixed content). Ссылка скопирована: ${url}`,
      );
      return "copied";
    } catch {
      showTimedNotification(
        `${fallbackLabel} небезопасен (mixed content). Откройте вручную: ${url}`,
      );
      return "failed";
    }
  };

  const openTask = () => {
    if (taskTarget && isExternalTaskURL) {
      void (async () => {
        if (
          (await openWithSafeNavigationGuard(taskTarget, "URL задания")) ===
          "opened"
        ) {
          setTaskOpened(true);
        }
      })();
    }
  };

  const copyTaskTarget = async () => {
    if (!taskTarget) {
      return;
    }

    try {
      await navigator.clipboard.writeText(taskTarget);
      setTaskOpened(true);
      showTimedNotification("Endpoint скопирован");
    } catch {
      showTimedNotification(
        "Не удалось скопировать endpoint. Скопируйте вручную.",
      );
    }
  };

  const downloadFile = () => {
    if (taskSourceURL) {
      void openWithSafeNavigationGuard(taskSourceURL, "URL исходника");
    }
  };

  if (!taskData) {
    return (
      <main className={styles.container}>
        <div className={styles.loading}>
          <div className={styles.spinner}></div>
          <p className={styles.loadingText}>Загрузка задания...</p>
        </div>
      </main>
    );
  }

  const categoryConfig =
    CATEGORY_CONFIG[taskData.category] || CATEGORY_CONFIG.web;
  const difficultyConfig =
    DIFFICULTY_CONFIG[taskData.difficulty] || DIFFICULTY_CONFIG.easy;

  const getResultConfig = () => {
    switch (gameState) {
      case "won":
        return {
          emoji: "🏆",
          title: "ПОБЕДА!",
          message: "Поздравляем! Вы успешно решили задание!",
          color: "#4ade80",
        };
      case "lost":
        return {
          emoji: "😢",
          title: "ПОРАЖЕНИЕ",
          message: "Другой игрок раньше ввел правильный флаг.",
          color: "#ef4444",
        };
      case "timeup":
        return {
          emoji: "⏰",
          title: "ВРЕМЯ ВЫШЛО!",
          message: "Дуэль завершилась без победителя.",
          color: "#fbbf24",
        };
      default:
        return {
          emoji: "❓",
          title: "ИГРА ЗАВЕРШЕНА",
          message: "Игра завершена.",
          color: "#888",
        };
    }
  };

  const timerClass =
    timeLeft <= 10
      ? styles.timerDanger
      : timeLeft <= 60
        ? styles.timerWarning
        : styles.timerNormal;

  return (
    <main className={styles.container}>
      {notification && (
        <div className={`${styles.notification} ${styles.notificationError}`}>
          {notification}
        </div>
      )}

      {isPaused && gameState === "playing" && (
        <div className={styles.overlay}>
          <div className={styles.modal}>
            <span className={styles.modalEmoji}>⏸</span>
            <h2 className={styles.modalTitle} style={{ color: "#fbbf24" }}>
              СОПЕРНИК ОТКЛЮЧИЛСЯ
            </h2>
            <p className={styles.modalMessage}>
              Дуэль на паузе. Отправка флага будет доступна после реконнекта.
            </p>
            {opponentReconnectDeadline && (
              <p className={styles.modalMessage}>
                Окно реконнекта до{" "}
                {new Date(opponentReconnectDeadline).toLocaleTimeString(
                  "ru-RU",
                )}
              </p>
            )}
            <button
              className={`${styles.modalBtn} ${styles.modalBtnDanger}`}
              onClick={handleSurrender}
              disabled={isSurrendering || gameState !== "playing"}
            >
              {isSurrendering ? "Сдаёмся..." : "Сдаться"}
            </button>
          </div>
        </div>
      )}

      <div className={styles.content}>
        <div className={styles.header}>
          <div className={styles.headerTop}>
            <span className={styles.headerIcon}>{categoryConfig.icon}</span>
            <h1 className={styles.title}>{taskData.title}</h1>
          </div>
          <p className={styles.subtitle}>Решите задание быстрее соперника</p>
        </div>

        <div className={styles.card}>
          <h2 className={styles.cardTitle}>⏱ Осталось времени</h2>
          <div className={styles.timerWrapper}>
            <div>
              <div className={`${styles.timerDisplay} ${timerClass}`}>
                {formatTime(timeLeft)}
              </div>
              <div className={styles.timerLabel}>
                {isPaused
                  ? "Пауза"
                  : timeLeft <= 10
                    ? "Критично!"
                    : timeLeft <= 60
                      ? "Мало времени"
                      : "В запасе"}
              </div>
            </div>
          </div>
        </div>

        <div className={styles.card}>
          <h2 className={styles.cardTitle}>
            {categoryConfig.icon} {categoryConfig.label} — Задание
          </h2>

          <div className={styles.taskBadges}>
            <span className={`${styles.badge} ${styles.badgeCategory}`}>
              {categoryConfig.icon} {categoryConfig.label}
            </span>
            <span className={`${styles.badge} ${difficultyConfig.badgeClass}`}>
              {difficultyConfig.label}
            </span>
          </div>

          <p className={styles.taskDescription}>{taskData.description}</p>

          {taskTarget && isExternalTaskURL && (
            <button
              className={`${styles.taskLinkBtn} ${taskOpened ? styles.taskLinkBtnOpened : ""}`}
              onClick={openTask}
            >
              {taskOpened
                ? "✓ Задание открыто"
                : `${categoryConfig.icon} Перейти к заданию`}
            </button>
          )}

          {taskTarget && !isExternalTaskURL && (
            <div className={styles.connectionTarget}>
              <span className={styles.connectionTargetIcon}>⌁</span>
              <div className={styles.connectionTargetInfo}>
                <div className={styles.connectionTargetLabel}>
                  Endpoint подключения
                </div>
                <code className={styles.connectionTargetValue}>
                  {taskTarget}
                </code>
              </div>
              <button
                className={styles.connectionTargetBtn}
                onClick={copyTaskTarget}
              >
                {taskOpened ? "✓ Скопировано" : "Копировать"}
              </button>
            </div>
          )}

          {taskSourceURL && (
            <div className={styles.fileDownload}>
              <span className={styles.fileDownloadIcon}>📦</span>
              <div className={styles.fileDownloadInfo}>
                <div className={styles.fileDownloadName}>source.zip</div>
                <div className={styles.fileDownloadHint}>
                  Файл задания для скачивания
                </div>
              </div>
              <button className={styles.fileDownloadBtn} onClick={downloadFile}>
                ⬇ Скачать
              </button>
            </div>
          )}
        </div>

        {hints.length > 0 && (
          <div className={styles.card}>
            <h2 className={styles.cardTitle}>💡 Подсказки</h2>
            <div className={styles.hintsSection}>
              {hints.map((hint) => (
                <div
                  key={hint.index}
                  className={`${styles.hintItem} ${hint.hint ? styles.hintRevealed : ""}`}
                >
                  <span className={styles.hintNumber}>{hint.index}</span>
                  <span className={styles.hintText}>
                    {hint.hint || `Подсказка #${hint.index} пока закрыта`}
                  </span>
                  {!hint.hint && (
                    <button className={styles.hintRevealBtn} disabled>
                      Ждите
                    </button>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}

        <div className={styles.card}>
          <h2 className={styles.cardTitle}>🚩 Отправка флага</h2>

          <div className={styles.flagForm}>
            <div className={styles.flagInputRow}>
              <input
                type="text"
                className={styles.flagInput}
                value={flagInput}
                onChange={(e) => setFlagInput(e.target.value)}
                onKeyDown={(e) => {
                  if (
                    e.key === "Enter" &&
                    !isSubmitting &&
                    gameState === "playing"
                  ) {
                    handleFlagSubmit();
                  }
                }}
                placeholder="ctf{...}"
                disabled={gameState !== "playing" || isPaused}
              />
              <button
                className={styles.flagSubmitBtn}
                onClick={handleFlagSubmit}
                disabled={
                  isSubmitting ||
                  !flagInput.trim() ||
                  gameState !== "playing" ||
                  isPaused
                }
              >
                {isSubmitting ? (
                  <>
                    <div
                      className={styles.spinner}
                      style={{ width: 16, height: 16, borderWidth: 2 }}
                    ></div>
                    Отправка...
                  </>
                ) : (
                  "🚀 Отправить"
                )}
              </button>
            </div>

            <p className={styles.flagHint}>
              Флаг должен быть в формате ctf&#123;...&#125;
            </p>

            <button
              type="button"
              className={styles.surrenderBtn}
              onClick={handleSurrender}
              disabled={isSurrendering || gameState !== "playing"}
            >
              {isSurrendering ? (
                <>
                  <div
                    className={styles.spinner}
                    style={{ width: 16, height: 16, borderWidth: 2 }}
                  ></div>
                  Сдаёмся...
                </>
              ) : (
                "Сдаться"
              )}
            </button>

            {flagStatus !== "idle" && (
              <div
                className={`${styles.flagStatus} ${
                  flagStatus === "correct"
                    ? styles.flagStatusCorrect
                    : styles.flagStatusIncorrect
                }`}
              >
                <span className={styles.flagStatusIcon}>
                  {flagStatus === "correct" ? "✅" : "❌"}
                </span>
                <span>
                  {flagStatus === "correct" ? "Флаг верный!" : "Неверный флаг"}
                </span>
              </div>
            )}
          </div>
        </div>
      </div>

      {gameState !== "playing" && (
        <div className={styles.overlay}>
          <div className={styles.modal}>
            <span className={styles.modalEmoji}>{getResultConfig().emoji}</span>
            <h2
              className={styles.modalTitle}
              style={{ color: getResultConfig().color }}
            >
              {getResultConfig().title}
            </h2>
            <p className={styles.modalMessage}>{getResultConfig().message}</p>
            <button className={styles.modalBtn} onClick={handleReturnHome}>
              🏠 Вернуться на главную
            </button>
          </div>
        </div>
      )}
    </main>
  );
};
