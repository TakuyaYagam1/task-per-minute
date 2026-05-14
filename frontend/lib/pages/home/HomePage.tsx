"use client";

import Image from "next/image";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { FormEvent, useEffect, useRef, useState } from "react";

import { gameModel } from "../../entities/game";
import { playerModel, type RefreshPlayerResult } from "../../entities/player";
import { PlayButton, useWebSocket } from "../../features/game-queue";
import {
  isInvalidSessionWebSocketMessage,
  isValidUsername,
  log,
  parseWebSocketMessage,
  redirectNotificationStorage,
  useTimedNotification,
} from "../../shared/lib";
import {
  GameState,
  MatchFoundPayload,
  Player,
  WebSocketMessage,
} from "../../shared/types";
import { WaitingOverlay } from "../../widgets/waiting-overlay";

type HomeFlow = "queue" | "restore";

const NORMAL_CLOSURE_CODE = 1000;

const buildRateLimitMessage = (retryAfter?: string | null): string => {
  const value = retryAfter?.trim();
  if (!value) {
    return "Слишком много попыток. Повторите чуть позже.";
  }
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds > 0) {
    return `Слишком много попыток. Повторите через ${Math.ceil(seconds)} секунд.`;
  }
  return "Слишком много попыток. Повторите позже.";
};

const opponentIDFromMatch = (
  match: MatchFoundPayload,
  playerID: string,
): string | undefined => {
  if (match.duel.player1_id === playerID) {
    return match.duel.player2_id;
  }
  if (match.duel.player2_id === playerID) {
    return match.duel.player1_id;
  }
  return undefined;
};

export default function HomePage() {
  const router = useRouter();
  const [nickname, setNickname] = useState("");
  const [playerID, setPlayerID] = useState<string | null>(null);
  const [currentPlayer, setCurrentPlayer] = useState<Player | null>(null);
  const [isWaiting, setIsWaiting] = useState(false);
  const [queueSize, setQueueSize] = useState<number | undefined>(undefined);
  const [isInitializing, setIsInitializing] = useState(false);
  const [isClearingPlayer, setIsClearingPlayer] = useState(false);
  const currentPlayerRef = useRef<Player | null>(null);
  const pendingMatch = useRef<MatchFoundPayload | null>(null);
  const expectedRestoreDuelID = useRef<string | null>(null);
  const transitionDuelIDRef = useRef<string | null>(null);
  const currentHomeFlow = useRef<HomeFlow | null>(null);
  const queueAttemptRef = useRef(0);
  const sessionPromiseRef = useRef<Promise<RefreshPlayerResult> | null>(null);
  const { notification, showNotification } = useTimedNotification<string>();

  const { websocketRef, connectWebSocket, sendMessage, closeWebSocket } =
    useWebSocket();

  useEffect(() => {
    const redirectNotification = redirectNotificationStorage.consume();
    if (redirectNotification) {
      showNotification(redirectNotification);
    }
  }, [showNotification]);

  useEffect(() => {
    const player = playerModel.getCurrentPlayer();
    if (!player) {
      return;
    }
    currentPlayerRef.current = player;
    setCurrentPlayer(player);
    setPlayerID(player.id);
    setNickname(player.username);

    const controller = new AbortController();
    let cancelled = false;
    const promise = playerModel.refreshCurrentPlayer(player, controller.signal);
    sessionPromiseRef.current = promise;
    void (async () => {
      const result = await promise;
      if (sessionPromiseRef.current === promise) {
        sessionPromiseRef.current = null;
      }
      if (cancelled) {
        return;
      }
      if (result.kind === "aborted") {
        return;
      }
      if (result.kind === "expired") {
        currentPlayerRef.current = null;
        setCurrentPlayer(null);
        setPlayerID(null);
        setNickname("");
        showNotification("Сессия истекла. Введите никнейм заново.");
        return;
      }
      if (result.kind === "contract") {
        log.warn(
          "players/me returned malformed payload; keeping session, surface as soft error",
        );
        showNotification("Не удалось проверить сессию. Попробуйте ещё раз.");
        return;
      }
      if (result.kind === "error") {
        log.warn("players/me failed transiently on mount; keeping session");
        return;
      }
    })();

    return () => {
      cancelled = true;
      controller.abort();
      if (sessionPromiseRef.current === promise) {
        sessionPromiseRef.current = null;
      }
    };
  }, [showNotification]);
  const handleJoin = async (e: FormEvent) => {
    e.preventDefault();
    const trimmed = nickname.trim();
    if (!isValidUsername(trimmed)) {
      showNotification("Никнейм: 2-50 символов, латиница, цифры, _ или -");
      return;
    }

    setIsInitializing(true);
    const result = await playerModel.initializePlayer(trimmed);
    setIsInitializing(false);

    if (result.kind === "ok") {
      currentPlayerRef.current = result.player;
      setPlayerID(result.player.id);
      setCurrentPlayer(result.player);
    } else if (result.kind === "in_duel") {
      showNotification(
        "Игрок уже в активной дуэли. Откройте текущую сессию или дождитесь завершения.",
      );
    } else if (result.kind === "rate_limited") {
      showNotification(buildRateLimitMessage(result.retryAfter));
    } else if (result.kind === "error") {
      showNotification("Ошибка подключения к серверу");
    }
  };

  useEffect(() => {
    return closeWebSocket;
  }, [closeWebSocket]);

  const clearHomeFlow = () => {
    pendingMatch.current = null;
    expectedRestoreDuelID.current = null;
    currentHomeFlow.current = null;
    setQueueSize(undefined);
  };

  const clearTransitionDuel = () => {
    transitionDuelIDRef.current = null;
  };

  const clearPlayerSessionForNextEntrant = () => {
    void playerModel.clearCurrentPlayer();
    currentPlayerRef.current = null;
    setCurrentPlayer(null);
    setPlayerID(null);
    setNickname("");
    sessionPromiseRef.current = null;
  };

  const expectedTerminalDuelID = (): string | null => {
    if (currentHomeFlow.current === "queue") {
      return pendingMatch.current?.duel_id || null;
    }
    if (currentHomeFlow.current === "restore") {
      return expectedRestoreDuelID.current;
    }
    return (
      transitionDuelIDRef.current || gameModel.getGameData()?.duel_id || null
    );
  };

  const saveTransitionTerminalResult = (
    message: Extract<WebSocketMessage, { type: "duel_finished" }>,
    activePlayer: Player,
  ) => {
    if (
      !message.payload ||
      gameModel.getGameData()?.duel_id !== message.payload.duel_id
    ) {
      return;
    }
    const winnerID = message.payload.winner_id || null;
    const state: GameState =
      winnerID === null
        ? "timeup"
        : winnerID === activePlayer.id
          ? "won"
          : "lost";
    gameModel.saveGameResult({
      state,
      source: "server",
      duel_id: message.payload.duel_id,
      winner_id: winnerID,
      winner_username: message.payload.winner_username || null,
    });
  };

  const handleExpiredSession = () => {
    void playerModel.clearCurrentPlayer();
    gameModel.clearGameData();
    currentPlayerRef.current = null;
    setCurrentPlayer(null);
    setPlayerID(null);
    setNickname("");
    clearHomeFlow();
    clearTransitionDuel();
    setIsWaiting(false);
    closeWebSocket();
    showNotification("Сессия истекла. Введите никнейм заново.");
  };

  const handleWebSocketMessage = (message: WebSocketMessage) => {
    switch (message.type) {
      case "pong":
        break;

      case "queue_joined":
        if (currentHomeFlow.current !== "queue") {
          log.error("Ignored queue_joined outside queue flow");
          break;
        }
        showNotification("Вы добавлены в очередь");
        break;

      case "queue_left":
        if (currentHomeFlow.current !== "queue") {
          log.error("Ignored queue_left outside queue flow");
          break;
        }
        clearHomeFlow();
        setIsWaiting(false);
        break;

      case "match_found":
        if (currentHomeFlow.current !== "queue") {
          log.error("Ignored match_found outside queue flow");
          break;
        }
        if (message.payload) {
          pendingMatch.current = message.payload;
          showNotification("Соперник найден! Игра начинается...");
        }
        break;

      case "task_assigned":
        if (currentHomeFlow.current !== "queue") {
          log.error("Ignored task_assigned outside queue flow");
          break;
        }
        if (message.payload) {
          if (pendingMatch.current?.duel_id !== message.payload.duel_id) {
            log.error("Ignored task_assigned for unexpected duel");
            break;
          }
          const activePlayer = currentPlayerRef.current;
          const opponentID =
            activePlayer && pendingMatch.current
              ? opponentIDFromMatch(pendingMatch.current, activePlayer.id)
              : undefined;
          if (!opponentID) {
            log.error("Ignored task_assigned without matching opponent");
            break;
          }
          gameModel.clearGameData();
          gameModel.saveGameData({
            duel_id: message.payload.duel_id,
            deadline: message.payload.deadline,
            time_limit_seconds: message.payload.time_limit_seconds,
            task: message.payload.task,
            opponent_username: pendingMatch.current?.opponent_username,
            opponent_id: opponentID,
          });
          transitionDuelIDRef.current = message.payload.duel_id;
        }
        clearHomeFlow();
        setIsWaiting(false);
        router.push("/task");
        break;

      case "duel_resume":
        if (currentHomeFlow.current !== "restore") {
          log.error("Ignored duel_resume outside restore flow");
          break;
        }
        if (!message.payload) {
          break;
        }
        if (expectedRestoreDuelID.current !== message.payload.duel_id) {
          log.error("Ignored duel_resume for unexpected duel");
          break;
        }
        if (!message.payload.task) {
          showNotification(
            "Не удалось восстановить активную дуэль. Обновите страницу.",
          );
          clearHomeFlow();
          setIsWaiting(false);
          closeWebSocket();
          break;
        }
        const resumePlayer = currentPlayerRef.current;
        if (
          message.payload.opponent_id &&
          message.payload.opponent_id === resumePlayer?.id
        ) {
          log.error("Ignored duel_resume with current player as opponent");
          break;
        }
        gameModel.clearGameData();
        gameModel.saveGameData({
          duel_id: message.payload.duel_id,
          deadline: message.payload.deadline,
          time_limit_seconds: message.payload.task.time_limit_seconds,
          task: message.payload.task,
          opponent_id: message.payload.opponent_id,
          opponent_disconnected: message.payload.opponent_disconnected,
          opponent_reconnect_deadline:
            message.payload.opponent_reconnect_deadline,
        });
        transitionDuelIDRef.current = message.payload.duel_id;
        clearHomeFlow();
        setIsWaiting(false);
        router.push("/task");
        break;

      case "duel_finished":
        const activePlayer = currentPlayerRef.current;
        if (message.payload && activePlayer) {
          const expectedDuelID = expectedTerminalDuelID();
          if (expectedDuelID !== message.payload.duel_id) {
            log.error("Ignored duel_finished for unexpected duel");
            break;
          }
          saveTransitionTerminalResult(message, activePlayer);
          clearTransitionDuel();
          clearHomeFlow();
          closeWebSocket();
          clearPlayerSessionForNextEntrant();
          setIsWaiting(false);
          const winnerID = message.payload.winner_id;
          if (!winnerID) {
            showNotification("Игра закончилась вничью");
          } else if (winnerID === activePlayer.id) {
            showNotification("Поздравляем! Вы выиграли!");
          } else {
            showNotification("Вы проиграли. Попробуйте снова!");
          }
        }
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
        const wasQueueFlow = currentHomeFlow.current === "queue";
        showNotification(message.message || message.code || "Произошла ошибка");
        clearHomeFlow();
        setIsWaiting(false);
        break;

      default:
        break;
    }
  };

  const cancelSearch = () => {
    const shouldLeaveQueue = currentHomeFlow.current === "queue";
    queueAttemptRef.current += 1;
    clearHomeFlow();
    clearTransitionDuel();
    setIsWaiting(false);

    if (
      shouldLeaveQueue &&
      playerID &&
      websocketRef.current?.readyState === WebSocket.OPEN
    ) {
      sendMessage("leave_queue");
    }
    closeWebSocket();
  };

  const canChangePlayer =
    Boolean(currentPlayer && playerID) && !isClearingPlayer;

  const handleChangePlayer = async () => {
    if (!canChangePlayer) {
      return;
    }

    setIsClearingPlayer(true);
    const shouldLeaveQueue = currentHomeFlow.current === "queue";
    queueAttemptRef.current += 1;
    if (
      shouldLeaveQueue &&
      playerID &&
      websocketRef.current?.readyState === WebSocket.OPEN
    ) {
      sendMessage("leave_queue");
    }
    clearHomeFlow();
    clearTransitionDuel();
    setIsWaiting(false);
    closeWebSocket();
    gameModel.clearGameData();
    await playerModel.clearCurrentPlayer();
    currentPlayerRef.current = null;
    setCurrentPlayer(null);
    setPlayerID(null);
    setNickname("");
    sessionPromiseRef.current = null;
    setIsClearingPlayer(false);
    showNotification("Сессия отменена.");
  };

  const handleReady = async () => {
    if (isWaiting) {
      return;
    }
    if (!currentPlayer) {
      showNotification("Инициализация игрока...");
      return;
    }

    setIsWaiting(true);
    clearTransitionDuel();
    clearHomeFlow();
    const attemptID = queueAttemptRef.current + 1;
    queueAttemptRef.current = attemptID;
    const isCurrentAttempt = () => queueAttemptRef.current === attemptID;

    try {
      const cachedPromise = sessionPromiseRef.current;
      const refreshResult = cachedPromise
        ? await cachedPromise
        : await playerModel.refreshCurrentPlayer(currentPlayer);
      if (sessionPromiseRef.current === cachedPromise) {
        sessionPromiseRef.current = null;
      }
      if (!isCurrentAttempt()) {
        return;
      }
      if (refreshResult.kind === "aborted") {
        clearHomeFlow();
        setIsWaiting(false);
        return;
      }
      if (refreshResult.kind === "expired") {
        handleExpiredSession();
        return;
      }
      if (refreshResult.kind === "contract" || refreshResult.kind === "error") {
        log.warn(
          `refreshCurrentPlayer returned ${refreshResult.kind} during handleReady`,
        );
        clearHomeFlow();
        setIsWaiting(false);
        closeWebSocket();
        showNotification(
          refreshResult.kind === "contract"
            ? "Не удалось проверить сессию. Попробуйте ещё раз."
            : "Ошибка подключения к серверу",
        );
        return;
      }

      const sessionState = refreshResult.state;
      currentPlayerRef.current = sessionState.player;
      setCurrentPlayer(sessionState.player);
      setPlayerID(sessionState.player.id);
      setNickname(sessionState.player.username);

      const shouldJoinQueue = !sessionState.activeDuel;
      expectedRestoreDuelID.current = sessionState.activeDuel?.id || null;
      currentHomeFlow.current = shouldJoinQueue ? "queue" : "restore";
      if (!shouldJoinQueue) {
        showNotification("Восстанавливаем активную дуэль...");
      }

      const sendJoinQueueForCurrentSocket = (websocket: WebSocket) => {
        if (!isCurrentAttempt()) {
          websocket.close();
          return;
        }
        if (
          !shouldJoinQueue ||
          currentHomeFlow.current !== "queue" ||
          websocketRef.current !== websocket
        ) {
          return;
        }
        sendMessage("join_queue");
      };

      const sendJoinQueueOnOpen = (websocket: WebSocket) => {
        if (!shouldJoinQueue) {
          return;
        }
        if (websocket.readyState === WebSocket.OPEN) {
          sendJoinQueueForCurrentSocket(websocket);
          return;
        }
        websocket.addEventListener(
          "open",
          () => sendJoinQueueForCurrentSocket(websocket),
          { once: true },
        );
      };

      const setupWebSocketListeners = (websocket: WebSocket) => {
        let activeDuelRestored = false;
        let flowCompleted = false;
        let restoreFailureNotified = false;
        let socketOpened = false;
        const notifyRestoreFailure = () => {
          if (shouldJoinQueue || activeDuelRestored || restoreFailureNotified) {
            return;
          }
          restoreFailureNotified = true;
          showNotification(
            "Не удалось восстановить активную дуэль. Обновите страницу.",
          );
        };

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
          if (
            isInvalidSessionWebSocketMessage(message) ||
            (message.type === "duel_resume" &&
              currentHomeFlow.current === "restore" &&
              message.payload &&
              expectedRestoreDuelID.current === message.payload.duel_id &&
              !message.payload.task)
          ) {
            flowCompleted = true;
          }
          if (
            message.type === "task_assigned" &&
            currentHomeFlow.current === "queue" &&
            message.payload &&
            pendingMatch.current?.duel_id === message.payload.duel_id
          ) {
            flowCompleted = true;
          }
          if (
            message.type === "duel_resume" &&
            currentHomeFlow.current === "restore" &&
            message.payload?.task &&
            expectedRestoreDuelID.current === message.payload.duel_id
          ) {
            activeDuelRestored = true;
            flowCompleted = true;
          }
          if (
            message.type === "duel_finished" &&
            message.payload &&
            expectedTerminalDuelID() === message.payload.duel_id
          ) {
            activeDuelRestored = true;
            flowCompleted = true;
          }
          handleWebSocketMessage(message);
        };

        websocket.onopen = () => {
          socketOpened = true;
        };

        websocket.onclose = (event) => {
          if (isCurrentAttempt() && !flowCompleted) {
            if (event.code !== NORMAL_CLOSURE_CODE && !socketOpened) {
              setIsWaiting(false);
              clearHomeFlow();
              showNotification(
                "Ошибка WebSocket соединения. Проверьте адрес страницы и обновите.",
              );
              return;
            }
            if (event.code !== NORMAL_CLOSURE_CODE) {
              return;
            }
            setIsWaiting(false);
            notifyRestoreFailure();
            clearHomeFlow();
          }
        };

        websocket.onerror = (error) => {
          log.error("WebSocket error:", error);
        };
      };

      const ws = connectWebSocket({
        onReconnect: (newWs) => {
          setupWebSocketListeners(newWs);
          sendJoinQueueOnOpen(newWs);
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
            setCurrentPlayer(refreshResult.state.player);
            setPlayerID(refreshResult.state.player.id);
            setNickname(refreshResult.state.player.username);
          }
          return null;
        },
        onReconnectGiveUp: (reason) => {
          if (!isCurrentAttempt()) {
            return;
          }
          if (reason === "auth" || reason === "forbidden") {
            handleExpiredSession();
            return;
          }
          showNotification("Соединение потеряно. Обновите страницу.");
          clearHomeFlow();
          setIsWaiting(false);
        },
      });

      setupWebSocketListeners(ws);
      sendJoinQueueOnOpen(ws);
    } catch {
      if (isCurrentAttempt()) {
        showNotification("Ошибка подключения");
        clearHomeFlow();
        setIsWaiting(false);
      }
    }
  };

  return (
    <>
      {notification && (
        <div className="fixed inset-x-0 top-4 z-50 flex justify-center px-4 pointer-events-none">
          <div className="pointer-events-auto bg-black/80 text-white px-4 py-2 rounded-lg animate-fadeIn">
            {notification}
          </div>
        </div>
      )}

      <main className="min-h-screen flex flex-col items-center justify-center p-3 lg:p-6 relative animate-fadeIn gpu-optimized">
        {isWaiting && (
          <WaitingOverlay
            onCancel={cancelSearch}
            onChangePlayer={canChangePlayer ? handleChangePlayer : undefined}
            changePlayerDisabled={isClearingPlayer}
            queueSize={queueSize}
          />
        )}

        <div className="container max-w-6xl">
          <div className="card overflow-hidden animate-scaleIn will-change-transform">
            <div className="flex flex-col lg:flex-row">
              <div className="lg:w-2/3 relative overflow-hidden">
                <Image
                  src="/task.png"
                  alt="Task Per Minute"
                  width={900}
                  height={600}
                  className="w-full h-72 sm:h-64 lg:h-full object-cover animate-slideInLeft will-change-transform"
                  priority
                />
                <div className="absolute inset-0 bg-gradient-to-t from-black/60 via-transparent to-transparent lg:bg-gradient-to-r lg:from-transparent lg:via-transparent lg:to-black/60"></div>

                <div className="absolute top-4 right-4 w-12 h-12 bg-white/10 rounded-full animate-bounce hidden lg:block"></div>
                <div className="absolute bottom-6 left-6 w-8 h-8 bg-white/20 rounded-full animate-pulse hidden lg:block"></div>
                <div className="absolute inset-x-0 bottom-6 z-10 flex justify-center px-4">
                  <Link
                    href="/leaderboard"
                    className="btn btn-secondary !w-auto !min-w-[92px] sm:!min-w-[140px] lg:!min-w-[180px] border-white/35 bg-white/15 !px-3 sm:!px-4 lg:!px-5 !py-1.5 sm:!py-2 lg:!py-3 !text-[10px] sm:!text-xs lg:!text-base font-bold uppercase leading-none text-white shadow-lg backdrop-blur-md hover:bg-white/25 active:scale-95 !gap-1 sm:!gap-2"
                  >
                    <span aria-hidden="true">🏆</span>
                    Лидерборд
                  </Link>
                </div>
              </div>

              <div className="lg:w-1/3 p-6 lg:p-8 flex flex-col justify-center animate-slideInRight">
                <h1 className="text-2xl lg:text-3xl xl:text-4xl font-bold mb-4 lg:mb-6 text-center">
                  Стань первым!
                </h1>

                <h2 className="text-lg lg:text-xl font-semibold mb-4 text-blue-200 text-center">
                  Сразись в CTF дуэли!
                </h2>

                <p className="text-sm lg:text-base text-gray-300 mb-6 lg:mb-8 leading-relaxed text-center">
                  Испытай наш новый формат CTF соревнований на скорость! Каждая
                  секунда решает исход битвы.
                </p>

                {!playerID ? (
                  <form onSubmit={handleJoin} className="mb-4">
                    <div className="flex flex-col gap-3">
                      <input
                        type="text"
                        value={nickname}
                        onChange={(e) => setNickname(e.target.value)}
                        placeholder="Введите никнейм..."
                        maxLength={50}
                        className="w-full px-4 py-2.5 bg-white/10 border border-white/20 rounded-lg text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-400 focus:border-transparent transition-all text-sm"
                        disabled={isInitializing}
                      />
                      <button
                        type="submit"
                        disabled={isInitializing || !nickname.trim()}
                        className="btn btn-primary w-full text-lg font-bold py-4 px-8 transition-all duration-300 ease-out transform disabled:opacity-50 disabled:cursor-not-allowed hover:scale-105 hover:shadow-lg animate-glow active:scale-95"
                      >
                        <span className="flex items-center justify-center gap-2">
                          {isInitializing ? (
                            <>
                              <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin"></div>
                              ПОДКЛЮЧЕНИЕ...
                            </>
                          ) : (
                            <>ПОДКЛЮЧИТЬСЯ</>
                          )}
                        </span>
                      </button>
                    </div>
                  </form>
                ) : (
                  <div className="mb-4 flex flex-col gap-3">
                    <div className="animate-on-hover will-change-transform">
                      <PlayButton
                        onClick={handleReady}
                        disabled={!playerID || isWaiting || isClearingPlayer}
                      />
                    </div>
                    {!isWaiting && (
                      <button
                        type="button"
                        onClick={handleChangePlayer}
                        disabled={isClearingPlayer}
                        className="btn btn-secondary w-full text-sm font-bold py-3 px-5 transition-all duration-300 ease-out transform disabled:opacity-50 disabled:cursor-not-allowed hover:scale-105 hover:shadow-lg active:scale-95"
                      >
                        {isClearingPlayer
                          ? "Смена игрока..."
                          : "Сменить игрока"}
                      </button>
                    )}
                  </div>
                )}

                <div className="text-xs text-gray-400">
                  {playerID ? (
                    <div className="flex items-center justify-center gap-2">
                      <div className="w-2 h-2 bg-green-400 rounded-full animate-pulse"></div>
                      Игрок готов
                    </div>
                  ) : (
                    <div className="flex items-center justify-center gap-2">
                      <div className="w-2 h-2 bg-yellow-400 rounded-full animate-pulse"></div>
                      {isInitializing ? "Подключение..." : "Введите никнейм"}
                    </div>
                  )}
                </div>
              </div>
            </div>
          </div>

          <div
            className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 lg:gap-6 mt-6 lg:mt-8 animate-fadeIn"
            style={{ animationDelay: "0.3s" }}
          >
            <div className="card p-4 lg:p-6 animate-on-hover will-change-transform">
              <div className="text-3xl lg:text-4xl mb-3 text-center">⚡</div>
              <h3 className="font-bold text-lg mb-2 text-center">
                Скоростные дуэли
              </h3>
              <p className="text-sm text-gray-300 text-center">
                2 игрока, общий дедлайн, быстрый матч
              </p>
            </div>

            <div className="card p-4 lg:p-6 animate-on-hover will-change-transform">
              <div className="text-3xl lg:text-4xl mb-3 text-center">🏆</div>
              <h3 className="font-bold text-lg mb-2 text-center">Победитель</h3>
              <p className="text-sm text-gray-300 text-center">
                Первый корректный флаг завершает дуэль
              </p>
            </div>

            <div className="card p-4 lg:p-6 animate-on-hover will-change-transform md:col-span-2 lg:col-span-1">
              <div className="text-3xl lg:text-4xl mb-3 text-center">🧩</div>
              <h3 className="font-bold text-lg mb-2 text-center">
                Разные категории
              </h3>
              <p className="text-sm text-gray-300 text-center">
                Категория любая, сложность по прогрессу
              </p>
            </div>
          </div>

          <div
            className="card p-6 lg:p-8 mt-6 lg:mt-8 animate-fadeIn"
            style={{ animationDelay: "0.6s" }}
          >
            <h3 className="text-xl lg:text-2xl font-bold mb-2 text-center">
              Правила игры
            </h3>
            <p className="text-sm lg:text-base text-gray-300 text-center max-w-3xl mx-auto mb-6 lg:mb-8">
              <strong className="text-white">CTF (Capture The Flag)</strong> -
              формат соревнований по информационной безопасности: участники ищут
              уязвимости и собирают «флаги» - секретные строки, подтверждающие
              выполнение задания.
            </p>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 lg:gap-5">
              <div className="rule-step-card relative rounded-xl border border-white/10 bg-white/5 p-4 lg:p-5 backdrop-blur-sm hover:border-white/20 hover:bg-white/10">
                <div className="absolute -top-3 -left-3 flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-blue-400 to-cyan-500 text-sm font-bold text-white shadow-lg">
                  1
                </div>
                <div className="mb-2 text-2xl lg:text-3xl">👥</div>
                <h4 className="mb-1 text-base lg:text-lg font-semibold text-white">
                  Двое в очереди
                </h4>
                <p className="text-xs lg:text-sm text-gray-300 leading-relaxed">
                  Нажимаешь Play, попадаешь в очередь, а сервер подбирает
                  второго игрока и стартует дуэль.
                </p>
              </div>

              <div className="rule-step-card relative rounded-xl border border-white/10 bg-white/5 p-4 lg:p-5 backdrop-blur-sm hover:border-white/20 hover:bg-white/10">
                <div className="absolute -top-3 -left-3 flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-blue-400 to-cyan-500 text-sm font-bold text-white shadow-lg">
                  2
                </div>
                <div className="mb-2 text-2xl lg:text-3xl">🎲</div>
                <h4 className="mb-1 text-base lg:text-lg font-semibold text-white">
                  Случайный таск
                </h4>
                <p className="text-xs lg:text-sm text-gray-300 leading-relaxed">
                  Сложность берётся из открытого для игрока пула. Если общего
                  нерешённого таска нет, задания могут отличаться.
                </p>
              </div>

              <div className="rule-step-card relative rounded-xl border border-white/10 bg-white/5 p-4 lg:p-5 backdrop-blur-sm hover:border-white/20 hover:bg-white/10">
                <div className="absolute -top-3 -left-3 flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-blue-400 to-cyan-500 text-sm font-bold text-white shadow-lg">
                  3
                </div>
                <div className="mb-2 text-2xl lg:text-3xl">⏱️</div>
                <h4 className="mb-1 text-base lg:text-lg font-semibold text-white">
                  Время на решение
                </h4>
                <p className="text-xs lg:text-sm text-gray-300 leading-relaxed">
                  Дедлайн общий и считается по самому длинному лимиту выданных
                  заданий. Подсказки открываются по таймеру.
                </p>
              </div>

              <div className="rule-step-card relative rounded-xl border border-white/10 bg-white/5 p-4 lg:p-5 backdrop-blur-sm hover:border-white/20 hover:bg-white/10">
                <div className="absolute -top-3 -left-3 flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-blue-400 to-cyan-500 text-sm font-bold text-white shadow-lg">
                  4
                </div>
                <div className="mb-2 text-2xl lg:text-3xl">🏁</div>
                <h4 className="mb-1 text-base lg:text-lg font-semibold text-white">
                  Победа за флаг
                </h4>
                <p className="text-xs lg:text-sm text-gray-300 leading-relaxed">
                  Первый корректный флаг даёт победу. Если время вышло без
                  решения, дуэль завершается без победителя.
                </p>
              </div>
            </div>
          </div>
        </div>
      </main>
    </>
  );
}
