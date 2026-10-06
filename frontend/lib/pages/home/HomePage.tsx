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
  redirectNotificationStorage,
  useTimedNotification,
} from "../../shared/lib";
import {
  GameState,
  MatchFoundPayload,
  Player,
  WebSocketMessage,
} from "../../shared/types";
import { ViewportPortal } from "../../shared/ui";
import { WaitingOverlay } from "../../widgets/waiting-overlay";
import styles from "./HomePage.module.css";

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
  const playButtonRef = useRef<HTMLButtonElement>(null);
  const [nickname, setNickname] = useState("");
  const [playerID, setPlayerID] = useState<string | null>(null);
  const [currentPlayer, setCurrentPlayer] = useState<Player | null>(null);
  const [isWaiting, setIsWaiting] = useState(false);
  const [queueSize, setQueueSize] = useState<number | undefined>(undefined);
  const [isInitializing, setIsInitializing] = useState(false);
  const [isClearingPlayer, setIsClearingPlayer] = useState(false);
  const [isTransitioningToTask, setIsTransitioningToTask] = useState(false);
  const currentPlayerRef = useRef<Player | null>(null);
  const pendingMatch = useRef<MatchFoundPayload | null>(null);
  const expectedRestoreDuelID = useRef<string | null>(null);
  const transitionDuelIDRef = useRef<string | null>(null);
  const currentHomeFlow = useRef<HomeFlow | null>(null);
  const queueAttemptRef = useRef(0);
  const sessionPromiseRef = useRef<Promise<RefreshPlayerResult> | null>(null);
  const { notification, showNotification } = useTimedNotification<string>();

  const {
    websocketRef,
    connectionState,
    connectWebSocket,
    sendMessage,
    closeWebSocket,
  } = useWebSocket();

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

  const navigateToTask = () => {
    setIsTransitioningToTask(true);
    router.push("/task");
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
    setIsTransitioningToTask(false);
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
        navigateToTask();
        break;

      case "duel_resume":
        if (
          currentHomeFlow.current !== "restore" &&
          !(
            currentHomeFlow.current === "queue" &&
            message.payload &&
            expectedRestoreDuelID.current === message.payload.duel_id
          )
        ) {
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
        currentHomeFlow.current = "restore";
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
          opponent_username: message.payload.opponent_username,
          opponent_disconnected: message.payload.opponent_disconnected,
          opponent_reconnect_deadline:
            message.payload.opponent_reconnect_deadline,
        });
        transitionDuelIDRef.current = message.payload.duel_id;
        clearHomeFlow();
        setIsWaiting(false);
        navigateToTask();
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
    setIsTransitioningToTask(false);
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
    setIsTransitioningToTask(false);
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
        if (!sendMessage("join_queue")) {
          clearHomeFlow();
          setIsWaiting(false);
          showNotification("Соединение потеряно. Попробуйте ещё раз.");
        }
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

      type HomeSocketState = {
        activeDuelRestored: boolean;
        flowCompleted: boolean;
        restoreFailureNotified: boolean;
        socketOpened: boolean;
      };
      const socketStates = new WeakMap<WebSocket, HomeSocketState>();
      const stateFor = (websocket: WebSocket): HomeSocketState => {
        let state = socketStates.get(websocket);
        if (!state) {
          state = {
            activeDuelRestored: false,
            flowCompleted: false,
            restoreFailureNotified: false,
            socketOpened: false,
          };
          socketStates.set(websocket, state);
        }
        return state;
      };
      const notifyRestoreFailure = (state: HomeSocketState) => {
        if (
          shouldJoinQueue ||
          state.activeDuelRestored ||
          state.restoreFailureNotified
        ) {
          return;
        }
        state.restoreFailureNotified = true;
        showNotification(
          "Не удалось восстановить активную дуэль. Обновите страницу.",
        );
      };
      const handleSocketMessage = (
        message: WebSocketMessage,
        websocket: WebSocket,
      ) => {
        const state = stateFor(websocket);
        if (
          isInvalidSessionWebSocketMessage(message) ||
          (message.type === "duel_resume" &&
            currentHomeFlow.current === "restore" &&
            message.payload &&
            expectedRestoreDuelID.current === message.payload.duel_id &&
            !message.payload.task)
        ) {
          state.flowCompleted = true;
        }
        if (
          message.type === "task_assigned" &&
          currentHomeFlow.current === "queue" &&
          message.payload &&
          pendingMatch.current?.duel_id === message.payload.duel_id
        ) {
          state.flowCompleted = true;
        }
        if (
          message.type === "duel_resume" &&
          message.payload?.task &&
          expectedRestoreDuelID.current === message.payload.duel_id
        ) {
          currentHomeFlow.current = "restore";
          state.activeDuelRestored = true;
          state.flowCompleted = true;
        }
        if (
          message.type === "duel_finished" &&
          message.payload &&
          expectedTerminalDuelID() === message.payload.duel_id
        ) {
          state.activeDuelRestored = true;
          state.flowCompleted = true;
        }
        handleWebSocketMessage(message);
      };
      const handleSocketClose = (
        event: CloseEvent,
        websocket: WebSocket,
      ) => {
        const state = stateFor(websocket);
        if (isCurrentAttempt() && !state.flowCompleted) {
          if (event.code !== NORMAL_CLOSURE_CODE && !state.socketOpened) {
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
          notifyRestoreFailure(state);
          clearHomeFlow();
        }
      };

      const ws = connectWebSocket({
        onMessage: handleSocketMessage,
        onOpen: (websocket) => {
          stateFor(websocket).socketOpened = true;
        },
        onClose: handleSocketClose,
        onError: (error) => {
          log.error("WebSocket error:", error);
        },
        onReconnect: (newWs) => {
          stateFor(newWs);
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
            if (refreshResult.state.activeDuel) {
              expectedRestoreDuelID.current = refreshResult.state.activeDuel.id;
              pendingMatch.current = null;
              currentHomeFlow.current = "restore";
            }
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

      stateFor(ws);
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
        <ViewportPortal>
          <div className={styles.notification} role="status" aria-live="polite">
            {notification}
          </div>
        </ViewportPortal>
      )}

      {isTransitioningToTask && (
        <ViewportPortal>
          <div className={styles.transitionOverlay}>
            <div className={styles.transitionPanel} role="status" aria-live="polite">
              <span className={styles.loadingMark} aria-hidden="true" />
              <h2>Загрузка дуэли...</h2>
              <p>Подготавливаем задание и переходим в бой</p>
            </div>
          </div>
        </ViewportPortal>
      )}

      <main className={styles.page}>
        {isWaiting && (
          <WaitingOverlay
            returnFocusRef={playButtonRef}
            onCancel={cancelSearch}
            onChangePlayer={canChangePlayer ? handleChangePlayer : undefined}
            changePlayerDisabled={isClearingPlayer}
            queueSize={queueSize}
          />
        )}

        <section className={styles.hero} aria-labelledby="home-title">
          <div className={styles.heroArtwork} aria-hidden="true">
            <Image
              src="/brand/main.webp"
              alt=""
              fill
              sizes="(max-width: 760px) 1px, 1200px"
              className={styles.heroImage}
              priority
            />
          </div>
          <div className={styles.heroInner}>
            <div className={styles.heroContent}>
              <div className={styles.heroIntro}>
                <h1 id="home-title" className={styles.title} aria-label="Task Per Minute">
                  <span>TASK</span>
                  <span>PER MINUTE</span>
                </h1>
                <div className={styles.introCopy}>
                  <h2>CTF-дуэли один на один</h2>
                  <p>
                    Решай задачу быстрее соперника.
                    Первый корректный флаг завершает дуэль.
                  </p>
                </div>
              </div>

              <section className={styles.joinPanel} aria-labelledby="join-title">
                <div className={styles.panelHeading}>
                  <h2 id="join-title">{playerID ? "Начать дуэль" : "Вход в игру"}</h2>
                </div>
                <p className={styles.panelDescription}>
                  {playerID
                    ? 'Нажми "ИГРАТЬ", чтобы найти соперника.'
                    : "Выбери никнейм и подключись к игре."}
                </p>

                {!playerID ? (
                  <form onSubmit={handleJoin} className={styles.joinForm}>
                    <label htmlFor="player-nickname" className={styles.fieldLabel}>
                      Никнейм
                    </label>
                    <input
                      id="player-nickname"
                      name="nickname"
                      type="text"
                      value={nickname}
                      onChange={(e) => setNickname(e.target.value)}
                      placeholder="Введите никнейм..."
                      maxLength={50}
                      className={`input ${styles.nicknameInput}`}
                      disabled={isInitializing}
                      autoComplete="nickname"
                    />
                    <button
                      type="submit"
                      disabled={isInitializing || !nickname.trim()}
                      className={`btn btn-primary ${styles.mainButton}`}
                    >
                      {isInitializing ? (
                        <>
                          <span className={styles.buttonSpinner} aria-hidden="true" />
                          ПОДКЛЮЧЕНИЕ...
                        </>
                      ) : (
                        <>ПОДКЛЮЧИТЬСЯ<span aria-hidden="true">-&gt;</span></>
                      )}
                    </button>
                  </form>
                ) : (
                  <div className={styles.playerControls}>
                    <div className={styles.playerIdentity}>
                      <span className={styles.fieldLabel}>Никнейм</span>
                      <strong>{currentPlayer?.username || nickname}</strong>
                    </div>
                    <PlayButton
                      ref={playButtonRef}
                      onClick={handleReady}
                      disabled={
                        !playerID ||
                        isWaiting ||
                        isTransitioningToTask ||
                        isClearingPlayer ||
                        connectionState === "connecting" ||
                        connectionState === "reconnecting"
                      }
                    />
                    {!isWaiting && (
                      <button
                        type="button"
                        onClick={handleChangePlayer}
                        disabled={isClearingPlayer}
                        className={`btn btn-secondary ${styles.changePlayerButton}`}
                      >
                        {isClearingPlayer ? "Смена игрока..." : "Сменить игрока"}
                      </button>
                    )}
                  </div>
                )}

                <div className={styles.playerStatus} role="status" aria-live="polite">
                  <span
                    className={`${styles.statusDot} ${playerID ? styles.statusReady : ""}`}
                    aria-hidden="true"
                  />
                  {playerID
                    ? "Игрок готов"
                    : isInitializing ? "Подключение..." : "Введите никнейм"}
                </div>
              </section>
              <Link href="/leaderboard" className={styles.leaderboardLink}>
                Лидерборд
                <span aria-hidden="true">-&gt;</span>
              </Link>
            </div>
          </div>
        </section>

        <div className={styles.matchFacts} aria-label="Формат игры">
          <p><strong>Два игрока</strong><span>Одна дуэль</span></p>
          <p><strong>Общий дедлайн</strong><span>Лимит зависит от задания</span></p>
          <p><strong>Первый верный флаг</strong><span>Решает исход матча</span></p>
        </div>

        <section className={styles.rules} aria-labelledby="rules-title">
          <div className={styles.rulesInner}>
            <div className={styles.rulesIntro}>
              <h2 id="rules-title">Правила игры</h2>
              <p>
                CTF (Capture The Flag) - соревнование по информационной безопасности.
                Участники решают задания и находят флаги: секретные строки,
                подтверждающие решение.
              </p>
            </div>
            <ol className={styles.ruleList}>
              <li>
                <span className={styles.ruleNumber} aria-hidden="true">01</span>
                <div>
                  <h3>Найди соперника</h3>
                  <p>
                    Нажми &quot;ИГРАТЬ&quot;, чтобы встать в очередь. Сервер подберёт
                    второго игрока и запустит дуэль.
                  </p>
                </div>
              </li>
              <li>
                <span className={styles.ruleNumber} aria-hidden="true">02</span>
                <div>
                  <h3>Получи задание</h3>
                  <p>
                    Сложность зависит от открытого для игрока пула. Если общего
                    нерешённого задания нет, задания могут отличаться.
                  </p>
                </div>
              </li>
              <li>
                <span className={styles.ruleNumber} aria-hidden="true">03</span>
                <div>
                  <h3>Успей до дедлайна</h3>
                  <p>
                    Дедлайн общий и считается по самому длинному лимиту выданных
                    заданий. Подсказки открываются по таймеру.
                  </p>
                </div>
              </li>
              <li>
                <span className={styles.ruleNumber} aria-hidden="true">04</span>
                <div>
                  <h3>Отправь флаг</h3>
                  <p>
                    Первый корректный флаг даёт победу. Если время вышло без
                    решения, дуэль завершается без победителя.
                  </p>
                </div>
              </li>
            </ol>
          </div>
        </section>
      </main>
    </>
  );
}
