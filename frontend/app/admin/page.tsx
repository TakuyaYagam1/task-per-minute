"use client";
import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ADMIN_PLAYERS_CHANGED_EVENT,
  activateAdminSession,
  adminApi,
  ApiError,
  canResumeAdminSession,
  clearAdminSession,
  type AdminPlayer,
  type AdminPlayerAuditEvent,
  type AdminSessionResponse,
  type UpdateAdminPlayerRequest,
} from "../../lib/shared/api";
import {
  getSafeArenaReturnPath,
  log,
  useTimedNotification,
} from "../../lib/shared/lib";
import { ViewportPortal } from "../../lib/shared/ui";
import { TournamentAdminPanel } from "../../lib/widgets/tournament-admin";
import styles from "./admin.module.css";

type Player = AdminPlayer;
type PlayerAuditEvent = AdminPlayerAuditEvent;
type AdminSection = "players" | "tournaments";
type PlayerFormErrorField = "username" | "wins" | "averageMs" | "form";
type PlayerFormErrors = Partial<Record<PlayerFormErrorField, string>>;

interface Notification {
  type: "success" | "error" | "warning";
  message: string;
}

const USERNAME_RE = /^[a-zA-Z0-9_-]{2,50}$/;
const MAX_INT32 = 2_147_483_647;
const LOGOUT_TIMEOUT_MS = 8_000;
const PLAYERS_EVENTS_RETRY_BASE_MS = 1_000;
const PLAYERS_EVENTS_RETRY_MAX_MS = 30_000;
const PLAYERS_EVENTS_REFRESH_COOLDOWN_MS = 60_000;
const PLAYERS_EVENTS_FALLBACK_POLL_MS = 5_000;

const operatorReturnPath = (): string | null =>
  getSafeArenaReturnPath(
    new URLSearchParams(window.location.search).get("next"),
    "operator",
  );

const parseNonNegativeInt32 = (value: string): number | null => {
  const trimmed = value.trim();
  if (!/^\d+$/.test(trimmed)) {
    return null;
  }
  const parsed = Number(trimmed);
  return Number.isSafeInteger(parsed) && parsed <= MAX_INT32 ? parsed : null;
};

const parseNonNegativeInt64 = (value: string): number | null => {
  const trimmed = value.trim();
  if (!/^\d+$/.test(trimmed)) {
    return null;
  }
  const parsed = Number(trimmed);
  return Number.isSafeInteger(parsed) ? parsed : null;
};

const formatRetryAfter = (value: string | null | undefined): string => {
  if (!value) return "несколько минут";
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds > 0) {
    if (seconds < 60) return `${Math.ceil(seconds)} сек`;
    return `${Math.ceil(seconds / 60)} мин`;
  }
  const retryAt = Date.parse(value);
  if (!Number.isNaN(retryAt)) {
    const secondsLeft = Math.max(1, Math.ceil((retryAt - Date.now()) / 1000));
    if (secondsLeft < 60) return `${secondsLeft} сек`;
    return `${Math.ceil(secondsLeft / 60)} мин`;
  }
  return "несколько минут";
};

const formatMilliseconds = (ms: number): string => {
  if (ms <= 0) return "0.0с";
  const totalSeconds = ms / 1000;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = (totalSeconds % 60).toFixed(1);
  if (minutes > 0) {
    return `${minutes}м ${seconds}с`;
  }
  return `${seconds}с`;
};

const formatDateTime = (value: string | null | undefined): string => {
  if (!value) return "-";
  const parsed = Date.parse(value);
  if (Number.isNaN(parsed)) return value;
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "medium",
  }).format(parsed);
};

const shortJTI = (value: string): string =>
  value.length > 12 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;

const auditActionLabel = (action: PlayerAuditEvent["action"]): string =>
  action === "delete" ? "Удаление" : "Обновление";

const auditFieldLabels = {
  username: "Имя",
  wins: "Победы",
  average_solve_time_ms: "Среднее время",
  stats_overridden: "Ручная правка",
  deleted: "Удаление",
} as const;

type AuditField = keyof typeof auditFieldLabels;

const auditFields: AuditField[] = [
  "username",
  "wins",
  "average_solve_time_ms",
  "stats_overridden",
  "deleted",
];

const auditStateValue = (
  state: PlayerAuditEvent["before_state"],
  field: AuditField,
): string => {
  if (field === "average_solve_time_ms") {
    return formatMilliseconds(state[field]);
  }
  if (field === "stats_overridden" || field === "deleted") {
    return state[field] ? "да" : "нет";
  }
  return String(state[field]);
};

const auditDiffs = (
  event: PlayerAuditEvent,
): Array<{ field: AuditField; before: string; after: string }> =>
  auditFields
    .filter((field) => event.before_state[field] !== event.after_state[field])
    .map((field) => ({
      field,
      before: auditStateValue(event.before_state, field),
      after: auditStateValue(event.after_state, field),
    }));

const apiErrorMessage = (error: unknown, fallback: string): string => {
  if (!(error instanceof ApiError)) {
    return fallback;
  }
  if (error.status === 403) {
    return error.problem?.detail || "Нет доступа к этой операции";
  }
  if (error.status === 422) {
    return error.problem?.detail || "Некорректные данные";
  }
  if (error.status === 429) {
    return `Слишком много запросов, попробуйте через ${formatRetryAfter(error.retryAfter)}`;
  }
  return error.problem?.detail || error.message || fallback;
};

export default function AdminPanel() {
  const [session, setSession] = useState<AdminSessionResponse | null>(null);
  const [sessionChecking, setSessionChecking] = useState(true);
  const [activeSection, setActiveSection] = useState<AdminSection>("tournaments");
  const [password, setPassword] = useState("");
  const [loginFormError, setLoginFormError] = useState<string | null>(null);
  const [authLoading, setAuthLoading] = useState(false);
  const [logoutPending, setLogoutPending] = useState(false);
  const [players, setPlayers] = useState<Player[]>([]);
  const [playersLoading, setPlayersLoading] = useState(false);
  const [editingPlayerId, setEditingPlayerId] = useState<string | null>(null);
  const [playerUsername, setPlayerUsername] = useState("");
  const [playerWins, setPlayerWins] = useState("0");
  const [playerAverageMs, setPlayerAverageMs] = useState("0");
  const [playerSubmitting, setPlayerSubmitting] = useState(false);
  const [playerFormErrors, setPlayerFormErrors] = useState<PlayerFormErrors>(
    {},
  );
  const [showDeletedPlayers, setShowDeletedPlayers] = useState(false);
  const [auditPlayer, setAuditPlayer] = useState<Player | null>(null);
  const [playerAuditEvents, setPlayerAuditEvents] = useState<
    PlayerAuditEvent[]
  >([]);
  const [playerAuditLoading, setPlayerAuditLoading] = useState(false);
  const [playerAuditError, setPlayerAuditError] = useState<string | null>(null);
  const sessionRef = useRef<AdminSessionResponse | null>(null);
  const logoutAbortRef = useRef<AbortController | null>(null);
  const isMountedRef = useRef(false);
  const authSessionVersionRef = useRef(0);
  const playersRequestIDRef = useRef(0);
  const playersEventsRef = useRef<EventSource | null>(null);
  const playersRealtimeRefreshTimerRef = useRef<number | null>(null);
  const playersEventsRetryTimerRef = useRef<number | null>(null);
  const playersEventsFallbackPollTimerRef = useRef<number | null>(null);
  const playerAuditRequestIDRef = useRef(0);
  const passwordInputRef = useRef<HTMLInputElement>(null);
  const { notification, showNotification: showTimedNotification } =
    useTimedNotification<Notification>();

  useEffect(() => {
    isMountedRef.current = true;
    const pendingPassword = passwordInputRef.current?.value;
    if (pendingPassword) {
      setPassword(pendingPassword);
    }

    if (!canResumeAdminSession()) {
      setSessionChecking(false);
    } else {
      const sessionVersion = authSessionVersionRef.current + 1;
      void adminApi.ensureFreshSession()
        .then((nextSession) => {
          if (!isMountedRef.current) {
            return;
          }
          authSessionVersionRef.current = sessionVersion;
          activateAdminSession();
          sessionRef.current = nextSession;
          setSession(nextSession);
          const returnPath = operatorReturnPath();
          if (returnPath) {
            window.location.replace(returnPath);
          }
        })
        .catch(() => {
          if (!isMountedRef.current) {
            return;
          }
          clearAdminSession();
          sessionRef.current = null;
          setSession(null);
        })
        .finally(() => {
          if (isMountedRef.current) {
            setSessionChecking(false);
          }
        });
    }

    return () => {
      isMountedRef.current = false;
      authSessionVersionRef.current += 1;
      logoutAbortRef.current?.abort();
      playersEventsRef.current?.close();
      playersEventsRef.current = null;
      if (playersRealtimeRefreshTimerRef.current !== null) {
        window.clearTimeout(playersRealtimeRefreshTimerRef.current);
        playersRealtimeRefreshTimerRef.current = null;
      }
      if (playersEventsRetryTimerRef.current !== null) {
        window.clearTimeout(playersEventsRetryTimerRef.current);
        playersEventsRetryTimerRef.current = null;
      }
      if (playersEventsFallbackPollTimerRef.current !== null) {
        window.clearTimeout(playersEventsFallbackPollTimerRef.current);
        playersEventsFallbackPollTimerRef.current = null;
      }
    };
  }, []);

  const showNotification = useCallback(
    (type: "success" | "error" | "warning", message: string) => {
      showTimedNotification({ type, message }, 4000);
    },
    [showTimedNotification],
  );

  const clearPlayerFormError = useCallback((field: PlayerFormErrorField) => {
    setPlayerFormErrors((current) => {
      if (!current[field]) return current;
      const next = { ...current };
      delete next[field];
      return next;
    });
  }, []);

  const isCurrentAuthSession = useCallback(
    (sessionVersion: number): boolean =>
      isMountedRef.current && authSessionVersionRef.current === sessionVersion,
    [],
  );

  const saveSession = useCallback(
    (nextSession: AdminSessionResponse, sessionVersion?: number) => {
      if (
        sessionVersion !== undefined &&
        !isCurrentAuthSession(sessionVersion)
      ) {
        return;
      }
      activateAdminSession();
      sessionRef.current = nextSession;
      setSession(nextSession);
    },
    [isCurrentAuthSession],
  );

  const clearSession = useCallback(
    (options: { preserveAdminCSRF?: boolean } = {}) => {
      const nextSessionVersion = authSessionVersionRef.current + 1;
      authSessionVersionRef.current = nextSessionVersion;
      playersRequestIDRef.current += 1;
      playerAuditRequestIDRef.current += 1;
      playersEventsRef.current?.close();
      playersEventsRef.current = null;
      if (playersRealtimeRefreshTimerRef.current !== null) {
        window.clearTimeout(playersRealtimeRefreshTimerRef.current);
        playersRealtimeRefreshTimerRef.current = null;
      }
      if (playersEventsRetryTimerRef.current !== null) {
        window.clearTimeout(playersEventsRetryTimerRef.current);
        playersEventsRetryTimerRef.current = null;
      }
      if (playersEventsFallbackPollTimerRef.current !== null) {
        window.clearTimeout(playersEventsFallbackPollTimerRef.current);
        playersEventsFallbackPollTimerRef.current = null;
      }
      clearAdminSession({ preserveCSRF: options.preserveAdminCSRF });
      sessionRef.current = null;
      setSession(null);
      setActiveSection("tournaments");
      setPlayers([]);
      setPlayersLoading(false);
      setPlayerSubmitting(false);
      setEditingPlayerId(null);
      setShowDeletedPlayers(false);
      setAuditPlayer(null);
      setPlayerAuditEvents([]);
      setPlayerAuditLoading(false);
      setPlayerAuditError(null);
      return nextSessionVersion;
    },
    [],
  );

  const runAdminRequest = useCallback(
    async <T,>(request: () => Promise<T>): Promise<T> => {
      if (!sessionRef.current) {
        throw new Error("Unauthorized");
      }
      const sessionVersion = authSessionVersionRef.current;

      try {
        const result = await request();
        if (!isCurrentAuthSession(sessionVersion)) {
          throw new Error("Unauthorized");
        }
        return result;
      } catch (error) {
        if (!(error instanceof ApiError) || error.status !== 401) {
          throw error;
        }
        if (!isCurrentAuthSession(sessionVersion)) {
          throw new Error("Unauthorized");
        }
        clearSession();
        showNotification("error", "Сессия истекла. Войдите снова.");
        throw new Error("Unauthorized");
      }
    },
    [clearSession, isCurrentAuthSession, showNotification],
  );

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    if (logoutPending) return;
    if (!password.trim()) {
      setLoginFormError("Введите пароль администратора");
      return;
    }
    setLoginFormError(null);
    setAuthLoading(true);
    const sessionVersion = authSessionVersionRef.current + 1;
    try {
      const nextSession = await adminApi.login(password);
      if (!isMountedRef.current) {
        return;
      }
      authSessionVersionRef.current = sessionVersion;
      saveSession(nextSession, sessionVersion);
      setPassword("");
      setLoginFormError(null);
      const returnPath = operatorReturnPath();
      if (returnPath) {
        window.location.replace(returnPath);
        return;
      }
      showNotification("success", "Успешный вход в админ-панель");
    } catch (error) {
      if (!isMountedRef.current) {
        return;
      }
      if (error instanceof ApiError && error.status === 429) {
        setLoginFormError(
          `Слишком много попыток. Повторите через ${formatRetryAfter(error.retryAfter)}.`,
        );
      } else if (error instanceof ApiError && error.status === 401) {
        setLoginFormError("Неверный пароль");
      } else {
        setLoginFormError(
          apiErrorMessage(error, "Ошибка подключения к серверу"),
        );
      }
    } finally {
      if (isMountedRef.current) {
        setAuthLoading(false);
      }
    }
  };

  const handleLogout = async () => {
    const currentSession = session;
    const logoutSessionVersion = clearSession({ preserveAdminCSRF: true });
    if (!currentSession) {
      clearAdminSession();
      return;
    }
    logoutAbortRef.current?.abort();
    const logoutController = new AbortController();
    logoutAbortRef.current = logoutController;
    const logoutTimeout = window.setTimeout(() => {
      logoutController.abort(
        new DOMException("Admin logout timed out", "TimeoutError"),
      );
    }, LOGOUT_TIMEOUT_MS);
    setLogoutPending(true);
    try {
      await adminApi.logout(logoutController.signal);
    } catch (logoutError) {
      // Local logout wins even if remote session revocation fails offline.
      log.warn("admin logout request failed", logoutError);
    } finally {
      window.clearTimeout(logoutTimeout);
      if (logoutAbortRef.current === logoutController) {
        logoutAbortRef.current = null;
      }
      if (authSessionVersionRef.current === logoutSessionVersion) {
        clearAdminSession();
      }
      if (isMountedRef.current) {
        setLogoutPending(false);
      }
    }
  };

  const fetchPlayers = useCallback(
    async (options: { silent?: boolean } = {}) => {
      if (!session) return;
      const sessionVersion = authSessionVersionRef.current;
      const requestID = playersRequestIDRef.current + 1;
      playersRequestIDRef.current = requestID;
      const canApplyPlayersRequest = () =>
        isCurrentAuthSession(sessionVersion) &&
        playersRequestIDRef.current === requestID;
      if (!options.silent) {
        setPlayersLoading(true);
      }
      try {
        const data = await runAdminRequest(() => adminApi.listPlayers(showDeletedPlayers));
        if (canApplyPlayersRequest()) {
          setPlayers(data);
        }
      } catch (error) {
        if (
          !canApplyPlayersRequest() ||
          (error instanceof Error && error.message === "Unauthorized")
        ) {
          return;
        }
        if (options.silent) {
          log.warn("admin players realtime refresh failed", error);
          return;
        }
        showNotification(
          "error",
          apiErrorMessage(error, "Не удалось загрузить игроков"),
        );
      } finally {
        if (canApplyPlayersRequest()) {
          setPlayersLoading(false);
        }
      }
    },
    [
      isCurrentAuthSession,
      runAdminRequest,
      showDeletedPlayers,
      showNotification,
      session,
    ],
  );

  const schedulePlayersRealtimeRefresh = useCallback(() => {
    if (!sessionRef.current || activeSection !== "players") {
      return;
    }
    if (playersRealtimeRefreshTimerRef.current !== null) {
      window.clearTimeout(playersRealtimeRefreshTimerRef.current);
    }
    playersRealtimeRefreshTimerRef.current = window.setTimeout(() => {
      playersRealtimeRefreshTimerRef.current = null;
      fetchPlayers({ silent: true });
    }, 150);
  }, [activeSection, fetchPlayers]);

  useEffect(() => {
    if (!session) return;
    if (activeSection === "players") {
      fetchPlayers();
    }
  }, [activeSection, fetchPlayers, session]);

  useEffect(() => {
    if (!session || activeSection !== "players") {
      return undefined;
    }

    let active = true;
    let retryAttempt = 0;
    let lastRefreshAttemptAt = 0;
    const handlePlayersChanged: EventListener = () => {
      schedulePlayersRealtimeRefresh();
    };

    const clearRetryTimer = (): void => {
      if (playersEventsRetryTimerRef.current !== null) {
        window.clearTimeout(playersEventsRetryTimerRef.current);
        playersEventsRetryTimerRef.current = null;
      }
    };

    const clearFallbackPollTimer = (): void => {
      if (playersEventsFallbackPollTimerRef.current !== null) {
        window.clearTimeout(playersEventsFallbackPollTimerRef.current);
        playersEventsFallbackPollTimerRef.current = null;
      }
    };

    const markStreamOpen = (): void => {
      retryAttempt = 0;
      clearFallbackPollTimer();
    };

    const scheduleFallbackPoll = (): void => {
      if (
        !active ||
        !sessionRef.current ||
        activeSection !== "players" ||
        playersEventsFallbackPollTimerRef.current !== null
      ) {
        return;
      }
      playersEventsFallbackPollTimerRef.current = window.setTimeout(() => {
        playersEventsFallbackPollTimerRef.current = null;
        if (!active || !sessionRef.current || activeSection !== "players") {
          return;
        }
        void fetchPlayers({ silent: true });
        scheduleFallbackPoll();
      }, PLAYERS_EVENTS_FALLBACK_POLL_MS);
    };

    const closeCurrentSource = (): void => {
      const source = playersEventsRef.current;
      if (!source) {
        return;
      }
      source.removeEventListener(
        ADMIN_PLAYERS_CHANGED_EVENT,
        handlePlayersChanged,
      );
      source.removeEventListener("ready", markStreamOpen);
      source.onopen = null;
      source.onerror = null;
      playersEventsRef.current = null;
      source.close();
    };

    const scheduleOpen = (): void => {
      if (!active || !sessionRef.current || activeSection !== "players") {
        return;
      }
      clearRetryTimer();
      const delay = Math.min(
        PLAYERS_EVENTS_RETRY_MAX_MS,
        PLAYERS_EVENTS_RETRY_BASE_MS * 2 ** Math.min(retryAttempt, 5),
      );
      retryAttempt += 1;
      playersEventsRetryTimerRef.current = window.setTimeout(() => {
        playersEventsRetryTimerRef.current = null;
        void openStream();
      }, delay);
    };

    const openStream = async (): Promise<void> => {
      if (!active || !sessionRef.current || activeSection !== "players") {
        return;
      }
      const sessionVersion = authSessionVersionRef.current;
      const now = Date.now();
      const shouldRefreshSession =
        now - lastRefreshAttemptAt >= PLAYERS_EVENTS_REFRESH_COOLDOWN_MS;
      if (shouldRefreshSession) {
        lastRefreshAttemptAt = now;
        try {
          await adminApi.ensureFreshSession();
          if (!active || !isCurrentAuthSession(sessionVersion)) {
            return;
          }
        } catch (error) {
          log.warn("admin players realtime refresh failed", error);
          scheduleOpen();
          return;
        }
      }

      if (!active || !sessionRef.current || activeSection !== "players") {
        return;
      }
      closeCurrentSource();
      const source = adminApi.openPlayerEvents();
      playersEventsRef.current = source;
      source.onopen = markStreamOpen;
      source.addEventListener("ready", markStreamOpen);
      source.addEventListener(
        ADMIN_PLAYERS_CHANGED_EVENT,
        handlePlayersChanged,
      );
      source.onerror = (event) => {
        log.warn("admin players realtime stream error", event);
        if (playersEventsRef.current === source) {
          closeCurrentSource();
          void fetchPlayers({ silent: true });
          scheduleFallbackPoll();
          scheduleOpen();
        }
      };
    };

    void openStream();

    return () => {
      active = false;
      clearRetryTimer();
      clearFallbackPollTimer();
      closeCurrentSource();
      if (playersRealtimeRefreshTimerRef.current !== null) {
        window.clearTimeout(playersRealtimeRefreshTimerRef.current);
        playersRealtimeRefreshTimerRef.current = null;
      }
    };
  }, [
    activeSection,
    fetchPlayers,
    isCurrentAuthSession,
    schedulePlayersRealtimeRefresh,
    session,
  ]);

  const resetPlayerForm = useCallback(() => {
    setEditingPlayerId(null);
    setPlayerUsername("");
    setPlayerWins("0");
    setPlayerAverageMs("0");
    setPlayerFormErrors({});
  }, []);

  const startEditingPlayer = (player: Player) => {
    setEditingPlayerId(player.id);
    setPlayerUsername(player.username);
    setPlayerWins(String(player.wins));
    setPlayerAverageMs(String(player.average_solve_time_ms));
    setPlayerFormErrors({});
  };

  const handlePlayerSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const nextErrors: PlayerFormErrors = {};
    if (!editingPlayerId) {
      setPlayerFormErrors({ form: "Сначала выберите игрока из списка ниже" });
      return;
    }
    const username = playerUsername.trim();
    if (!USERNAME_RE.test(username)) {
      nextErrors.username =
        "Имя игрока: 2-50 символов, латиница, цифры, _ или -";
    }
    const wins = parseNonNegativeInt32(playerWins);
    if (wins === null) {
      nextErrors.wins = "Победы должны быть целым числом от 0 до 2147483647";
    }
    const averageSolveTimeMs = parseNonNegativeInt64(playerAverageMs);
    if (averageSolveTimeMs === null) {
      nextErrors.averageMs =
        "Среднее время должно быть целым числом в миллисекундах";
    }
    if (wins !== null && averageSolveTimeMs !== null) {
      if (wins === 0 && averageSolveTimeMs !== 0) {
        nextErrors.averageMs = "При 0 побед среднее время должно быть 0";
      }
      if (wins > 0 && averageSolveTimeMs === 0) {
        nextErrors.averageMs = "При победах среднее время должно быть больше 0";
      }
    }
    if (Object.keys(nextErrors).length > 0) {
      setPlayerFormErrors(nextErrors);
      return;
    }
    if (wins === null || averageSolveTimeMs === null) {
      return;
    }

    setPlayerFormErrors({});
    setPlayerSubmitting(true);
    const sessionVersion = authSessionVersionRef.current;
    try {
      const body: UpdateAdminPlayerRequest = {
        username,
        wins,
        average_solve_time_ms: averageSolveTimeMs,
      };
      const updated = await runAdminRequest(() =>
        adminApi.updatePlayer(editingPlayerId, body),
      );
      if (!isCurrentAuthSession(sessionVersion)) {
        return;
      }
      setPlayers((current) =>
        current.map((player) => (player.id === updated.id ? updated : player)),
      );
      resetPlayerForm();
      showNotification("success", "Игрок обновлён");
    } catch (error) {
      if (
        !isCurrentAuthSession(sessionVersion) ||
        (error instanceof Error && error.message === "Unauthorized")
      ) {
        return;
      }
      if (error instanceof ApiError && error.status === 409) {
        setPlayerFormErrors({ username: "Такое имя уже занято" });
      } else {
        setPlayerFormErrors({
          form: apiErrorMessage(error, "Ошибка при обновлении игрока"),
        });
      }
    } finally {
      if (isCurrentAuthSession(sessionVersion)) {
        setPlayerSubmitting(false);
      }
    }
  };

  const handleDeletePlayer = async (player: Player) => {
    if (!confirm(`Удалить игрока ${player.username}?`)) return;
    const sessionVersion = authSessionVersionRef.current;
    try {
      await runAdminRequest(() => adminApi.deletePlayer(player.id));
      if (!isCurrentAuthSession(sessionVersion)) {
        return;
      }
      if (showDeletedPlayers) {
        fetchPlayers();
      } else {
        setPlayers((current) =>
          current.filter((item) => item.id !== player.id),
        );
      }
      if (editingPlayerId === player.id) {
        resetPlayerForm();
      }
      showNotification("success", "Игрок удалён");
    } catch (error) {
      if (
        !isCurrentAuthSession(sessionVersion) ||
        (error instanceof Error && error.message === "Unauthorized")
      ) {
        return;
      }
      if (error instanceof ApiError && error.status === 409) {
        showNotification("error", "Игрок сейчас в очереди или дуэли");
      } else {
        showNotification(
          "error",
          apiErrorMessage(error, "Ошибка при удалении игрока"),
        );
      }
    }
  };

  const closePlayerAudit = () => {
    playerAuditRequestIDRef.current += 1;
    setAuditPlayer(null);
    setPlayerAuditEvents([]);
    setPlayerAuditLoading(false);
    setPlayerAuditError(null);
  };

  const openPlayerAudit = async (player: Player) => {
    setAuditPlayer(player);
    setPlayerAuditEvents([]);
    setPlayerAuditError(null);
    setPlayerAuditLoading(true);
    const sessionVersion = authSessionVersionRef.current;
    const requestID = playerAuditRequestIDRef.current + 1;
    playerAuditRequestIDRef.current = requestID;
    const canApplyAuditRequest = () =>
      isCurrentAuthSession(sessionVersion) &&
      playerAuditRequestIDRef.current === requestID;
    try {
      const events = await runAdminRequest(() => adminApi.listPlayerAudit(player.id));
      if (canApplyAuditRequest()) {
        setPlayerAuditEvents(events);
      }
    } catch (error) {
      if (
        !canApplyAuditRequest() ||
        (error instanceof Error && error.message === "Unauthorized")
      ) {
        return;
      }
      const message = apiErrorMessage(
        error,
        "Не удалось загрузить историю игрока",
      );
      setPlayerAuditError(message);
      showNotification("error", message);
    } finally {
      if (canApplyAuditRequest()) {
        setPlayerAuditLoading(false);
      }
    }
  };

  const renderPlayersSection = () => (
    <>
      <div className={`${styles.card} motion-panel`}>
        <h2 className={styles.cardTitle}>👥 Игроки</h2>
        <form onSubmit={handlePlayerSubmit} className={styles.form} noValidate>
          <div className={styles.formRow}>
            <div className={styles.inputGroup}>
              <label>Имя игрока</label>
              <input
                type="text"
                aria-label="Имя игрока"
                value={playerUsername}
                onChange={(e) => {
                  setPlayerUsername(e.target.value);
                  clearPlayerFormError("username");
                  clearPlayerFormError("form");
                }}
                placeholder="username"
                maxLength={50}
                disabled={!editingPlayerId}
                className={
                  playerFormErrors.username ? styles.inputError : undefined
                }
                aria-invalid={Boolean(playerFormErrors.username)}
                aria-describedby={
                  playerFormErrors.username
                    ? "admin-player-username-error"
                    : undefined
                }
              />
              {playerFormErrors.username && (
                <p
                  id="admin-player-username-error"
                  className={styles.fieldError}
                >
                  {playerFormErrors.username}
                </p>
              )}
            </div>
            <div className={styles.inputGroup}>
              <label>Победы</label>
              <input
                type="number"
                aria-label="Победы игрока"
                min="0"
                value={playerWins}
                onChange={(e) => {
                  setPlayerWins(e.target.value);
                  clearPlayerFormError("wins");
                  clearPlayerFormError("averageMs");
                  clearPlayerFormError("form");
                }}
                placeholder="0"
                disabled={!editingPlayerId}
                className={
                  playerFormErrors.wins ? styles.inputError : undefined
                }
                aria-invalid={Boolean(playerFormErrors.wins)}
                aria-describedby={
                  playerFormErrors.wins ? "admin-player-wins-error" : undefined
                }
              />
              {playerFormErrors.wins && (
                <p id="admin-player-wins-error" className={styles.fieldError}>
                  {playerFormErrors.wins}
                </p>
              )}
            </div>
          </div>
          <div className={styles.formRow}>
            <div className={styles.inputGroup}>
              <label>Среднее время (мс)</label>
              <input
                type="number"
                aria-label="Среднее время игрока"
                min="0"
                value={playerAverageMs}
                onChange={(e) => {
                  setPlayerAverageMs(e.target.value);
                  clearPlayerFormError("averageMs");
                  clearPlayerFormError("form");
                }}
                placeholder="0"
                disabled={!editingPlayerId}
                className={
                  playerFormErrors.averageMs ? styles.inputError : undefined
                }
                aria-invalid={Boolean(playerFormErrors.averageMs)}
                aria-describedby={
                  playerFormErrors.averageMs
                    ? "admin-player-average-error"
                    : undefined
                }
              />
              {playerFormErrors.averageMs && (
                <p
                  id="admin-player-average-error"
                  className={styles.fieldError}
                >
                  {playerFormErrors.averageMs}
                </p>
              )}
            </div>
            <div className={styles.playerFormHint}>
              {editingPlayerId
                ? `На табло: ${formatMilliseconds(Number(playerAverageMs) || 0)}`
                : "Выберите игрока из списка ниже"}
              {playerFormErrors.form && (
                <p className={`${styles.fieldError} ${styles.formLevelError}`}>
                  {playerFormErrors.form}
                </p>
              )}
            </div>
          </div>
          <div className={styles.btnGroup}>
            <button
              type="submit"
              className={`${styles.btn} ${styles.btnPrimary} motion-button`}
              disabled={!editingPlayerId || playerSubmitting}
            >
              {playerSubmitting ? (
                <>
                  <div
                    className={styles.spinner}
                    style={{ width: 18, height: 18 }}
                  ></div>
                  Сохранение...
                </>
              ) : (
                "💾 Сохранить игрока"
              )}
            </button>
            <button
              type="button"
              className={`${styles.btn} ${styles.btnSecondary} motion-button`}
              onClick={resetPlayerForm}
              disabled={!editingPlayerId || playerSubmitting}
            >
              Отменить
            </button>
          </div>
        </form>
      </div>

      <div className={styles.taskList}>
        <div className={styles.playerListHeader}>
          <h2 className={styles.taskListTitle}>👥 Список игроков</h2>
          <label className={styles.toggleRow}>
            <input
              type="checkbox"
              checked={showDeletedPlayers}
              onChange={(e) => setShowDeletedPlayers(e.target.checked)}
            />
            Показывать удаленных
          </label>
        </div>

        {playersLoading ? (
          <div className={styles.loading}>
            <div className={styles.spinner}></div>
            <p style={{ color: "rgba(255,255,255,0.5)", fontSize: "0.9rem" }}>
              Загрузка игроков...
            </p>
          </div>
        ) : players.length === 0 ? (
          <div className={styles.empty}>
            <div className={styles.emptyIcon}>👤</div>
            <p className={styles.emptyText}>Пока нет игроков</p>
          </div>
        ) : (
          players.map((player) => {
            const isDeleted = Boolean(player.deleted_at);
            return (
              <div
                key={player.id}
                className={`${styles.taskItem} ${editingPlayerId === player.id ? styles.playerItemActive : ""} ${isDeleted ? styles.playerItemDeleted : ""} motion-list-item`}
              >
                <div className={styles.taskItemInfo}>
                  <div className={styles.taskItemTitle}>{player.username}</div>
                  <div className={styles.taskItemMeta}>
                    <span className={styles.taskBadge}>
                      Победы: {player.wins}
                    </span>
                    <span className={styles.taskBadge}>
                      Среднее:{" "}
                      {formatMilliseconds(player.average_solve_time_ms)}
                    </span>
                    {player.stats_overridden && (
                      <span
                        className={`${styles.taskBadge} ${styles.taskBadgeOverride}`}
                      >
                        ручная правка
                      </span>
                    )}
                    {isDeleted && (
                      <span
                        className={`${styles.taskBadge} ${styles.taskBadgeDeleted}`}
                      >
                        удален: {formatDateTime(player.deleted_at)}
                      </span>
                    )}
                  </div>
                </div>
                <div className={styles.taskItemActions}>
                  <button
                    className={`${styles.taskItemBtn} motion-button`}
                    onClick={() => openPlayerAudit(player)}
                    aria-label={`История игрока ${player.username}`}
                    title="История изменений"
                  >
                    🕘
                  </button>
                  <button
                    className={`${styles.taskItemBtn} motion-button`}
                    onClick={() => startEditingPlayer(player)}
                    aria-label={`Редактировать игрока ${player.username}`}
                    title={
                      isDeleted
                        ? "Удаленного игрока нельзя редактировать"
                        : "Редактировать игрока"
                    }
                    disabled={isDeleted}
                  >
                    ✏️
                  </button>
                  <button
                    className={`${styles.taskItemBtn} ${styles.taskItemBtnDanger} motion-button`}
                    onClick={() => handleDeletePlayer(player)}
                    aria-label={`Удалить игрока ${player.username}`}
                    title={isDeleted ? "Игрок уже удален" : "Удалить игрока"}
                    disabled={isDeleted}
                  >
                    🗑️
                  </button>
                </div>
              </div>
            );
          })
        )}
      </div>
    </>
  );

  const renderPlayerAuditModal = () => {
    if (!auditPlayer) return null;
    return (
      <div
        className={`${styles.modalBackdrop} motion-modal-backdrop`}
        onMouseDown={closePlayerAudit}
      >
        <div
          className={`${styles.auditModal} motion-modal`}
          role="dialog"
          aria-modal="true"
          aria-labelledby="player-audit-title"
          onMouseDown={(event) => event.stopPropagation()}
        >
          <div className={styles.modalHeader}>
            <div>
              <h2 id="player-audit-title" className={styles.modalTitle}>
                История игрока
              </h2>
              <p className={styles.modalSubtitle}>{auditPlayer.username}</p>
            </div>
            <button
              type="button"
              className={`${styles.modalClose} motion-button`}
              onClick={closePlayerAudit}
              aria-label="Закрыть историю"
            >
              ×
            </button>
          </div>

          {playerAuditLoading ? (
            <div className={styles.loading}>
              <div className={styles.spinner}></div>
              <p style={{ color: "rgba(255,255,255,0.5)", fontSize: "0.9rem" }}>
                Загрузка истории...
              </p>
            </div>
          ) : playerAuditError ? (
            <div className={styles.auditEmpty}>{playerAuditError}</div>
          ) : playerAuditEvents.length === 0 ? (
            <div className={styles.auditEmpty}>История изменений пуста</div>
          ) : (
            <div className={styles.auditTimeline}>
              {playerAuditEvents.map((event) => {
                const diffs = auditDiffs(event);
                return (
                  <article
                    key={event.id}
                    className={`${styles.auditEvent} motion-list-item`}
                  >
                    <div className={styles.auditEventHeader}>
                      <span className={styles.auditAction}>
                        {auditActionLabel(event.action)}
                      </span>
                      <span className={styles.auditMeta}>
                        {formatDateTime(event.created_at)}
                      </span>
                    </div>
                    <div className={styles.auditMeta}>
                      actor: {event.actor_subject} · jti:{" "}
                      {shortJTI(event.actor_jti)}
                    </div>
                    <div className={styles.auditDiffs}>
                      {diffs.length === 0 ? (
                        <div className={styles.auditDiff}>
                          Изменений в полях нет
                        </div>
                      ) : (
                        diffs.map((diff) => (
                          <div key={diff.field} className={styles.auditDiff}>
                            <span className={styles.auditField}>
                              {auditFieldLabels[diff.field]}
                            </span>
                            <span className={styles.auditValues}>
                              <span className={styles.auditValue}>
                                {diff.before}
                              </span>
                              <span className={styles.auditArrow}>{"->"}</span>
                              <span className={styles.auditValue}>
                                {diff.after}
                              </span>
                            </span>
                          </div>
                        ))
                      )}
                    </div>
                  </article>
                );
              })}
            </div>
          )}
        </div>
      </div>
    );
  };

  if (sessionChecking) {
    return (
      <main className={`${styles.container} motion-page gpu-optimized`}>
        <div className={`${styles.card} ${styles.loginCard} motion-panel`}>
          <p role="status">Проверяем сессию...</p>
        </div>
      </main>
    );
  }

  if (!session) {
    return (
      <main className={`${styles.container} motion-page gpu-optimized`}>
        <div className={`${styles.header} motion-panel`}>
          <div className={styles.headerTop}>
            <h1 className={styles.title}>Admin</h1>
          </div>
          <p className={styles.subtitle}>Панель управления задачами</p>
        </div>

        <div className={`${styles.card} ${styles.loginCard} motion-panel`}>
          <h2 className={styles.cardTitle}>Авторизация</h2>
          <form onSubmit={handleLogin} className={styles.form} noValidate>
            <div className={styles.inputGroup}>
              <label>Пароль администратора</label>
              <input
                ref={passwordInputRef}
                type="password"
                required
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value);
                  setLoginFormError(null);
                }}
                placeholder="Введите пароль..."
                className={loginFormError ? styles.inputError : undefined}
                aria-invalid={Boolean(loginFormError)}
                aria-describedby={
                  loginFormError ? "admin-password-error" : undefined
                }
              />
              {loginFormError && (
                <p id="admin-password-error" className={styles.fieldError}>
                  {loginFormError}
                </p>
              )}
            </div>
            <button
              type="submit"
              className={`${styles.btn} ${styles.btnPrimary} motion-button`}
              disabled={authLoading || logoutPending || !password.trim()}
            >
              {authLoading || logoutPending ? (
                <>
                  <div
                    className={styles.spinner}
                    style={{ width: 18, height: 18 }}
                  ></div>
                  {logoutPending ? "Выход..." : "Вход..."}
                </>
              ) : (
                "Войти"
              )}
            </button>
          </form>
        </div>

        {notification && (
          <ViewportPortal>
            <div
              className={`${styles.notification} ${
                notification.type === "success"
                  ? styles.notificationSuccess
                  : notification.type === "warning"
                    ? styles.notificationWarning
                    : styles.notificationError
              }`}
            >
              {notification.message}
            </div>
          </ViewportPortal>
        )}
      </main>
    );
  }
  return (
    <main className={`${styles.container} motion-page gpu-optimized`}>
      {notification && (
        <ViewportPortal>
          <div
            className={`${styles.notification} ${
              notification.type === "success"
                ? styles.notificationSuccess
                : notification.type === "warning"
                  ? styles.notificationWarning
                  : styles.notificationError
            }`}
          >
            {notification.message}
          </div>
        </ViewportPortal>
      )}
      <div className={`${styles.header} motion-panel`}>
        <button
          type="button"
          className={`${styles.btn} ${styles.btnSecondary} ${styles.logoutButton} motion-button`}
          onClick={handleLogout}
        >
          Выйти
        </button>
        <div className={styles.headerTop}>
          <h1 className={styles.title}>Admin</h1>
        </div>
        <p className={styles.subtitle}>
          {activeSection === "players"
            ? "Панель управления игроками"
            : "Панель управления турнирами и контентом"}
        </p>
        <div className={styles.sectionTabs}>
          <button
            type="button"
            className={`${styles.sectionTab} ${activeSection === "players" ? styles.sectionTabActive : ""} motion-button`}
            onClick={() => setActiveSection("players")}
          >
            Игроки
          </button>
          <button
            type="button"
            className={`${styles.sectionTab} ${activeSection === "tournaments" ? styles.sectionTabActive : ""} motion-button`}
            onClick={() => setActiveSection("tournaments")}
          >
            Турниры
          </button>
        </div>
      </div>
      <div
        key={activeSection}
        className={`${styles.sectionPanel} ${styles.sectionPanelEnter}`}
      >
        {activeSection === "players" ? (
          renderPlayersSection()
        ) : (
          <TournamentAdminPanel
            onSessionExpired={clearSession}
            runAdminRequest={runAdminRequest}
          />
        )}
      </div>
      {renderPlayerAuditModal()}
    </main>
  );
}
