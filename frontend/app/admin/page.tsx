"use client";
import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ADMIN_PLAYERS_CHANGED_EVENT,
  adminApi,
  adminSession,
  ApiError,
  type AdminPlayer,
  type AdminPlayerAuditEvent,
  type AdminTask,
  type AdminTokenResponse,
  type CreateTaskRequest,
  type UpdateAdminPlayerRequest,
  type UpdateTaskRequest,
} from "../../lib/shared/api";
import { log, useTimedNotification } from "../../lib/shared/lib";
import styles from "./admin.module.css";

type Task = AdminTask;
type Player = AdminPlayer;
type PlayerAuditEvent = AdminPlayerAuditEvent;
type TaskCategory = Task["category"];
type TaskDifficulty = Task["difficulty"];
type AdminSection = "tasks" | "players";
type TaskFormErrorField =
  | "title"
  | "description"
  | "timeLimit"
  | "flag"
  | "taskUrl"
  | "sourceFile"
  | "hint0"
  | "hint1"
  | "hint2"
  | "form";
type PlayerFormErrorField = "username" | "wins" | "averageMs" | "form";
type TaskFormErrors = Partial<Record<TaskFormErrorField, string>>;
type PlayerFormErrors = Partial<Record<PlayerFormErrorField, string>>;

interface Notification {
  type: "success" | "error" | "warning";
  message: string;
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
  easy: { label: "Easy", badgeClass: styles.taskBadgeEasy },
  medium: { label: "Medium", badgeClass: styles.taskBadgeMedium },
  hard: { label: "Hard", badgeClass: styles.taskBadgeHard },
};

const MAX_INT32 = 2147483647;
const MAX_TASK_TITLE_LENGTH = 255;
const MAX_TASK_FLAG_LENGTH = 255;
const USERNAME_RE = /^[a-zA-Z0-9_-]{2,50}$/;
const LOGOUT_TIMEOUT_MS = 8_000;

const countChars = (value: string): number => Array.from(value).length;

const parsePositiveInt32 = (value: string): number | null => {
  const trimmed = value.trim();
  if (!/^[1-9]\d*$/.test(trimmed)) {
    return null;
  }
  const parsed = Number(trimmed);
  return Number.isSafeInteger(parsed) && parsed <= MAX_INT32 ? parsed : null;
};

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

const parsePortNumber = (value: string): number | null => {
  if (!/^\d+$/.test(value)) {
    return null;
  }
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 && parsed <= 65535
    ? parsed
    : null;
};

const isValidHttpTaskUrl = (value: string): boolean => {
  try {
    const url = new URL(value);
    return (
      (url.protocol === "http:" || url.protocol === "https:") &&
      Boolean(url.host)
    );
  } catch {
    return false;
  }
};

const isValidHostPortTaskUrl = (value: string): boolean => {
  if (value.includes("://")) {
    return false;
  }
  const portSeparator = value.lastIndexOf(":");
  if (portSeparator <= 0 || portSeparator === value.length - 1) {
    return false;
  }
  const host = value.slice(0, portSeparator).trim();
  const port = value.slice(portSeparator + 1);
  const portNumber = parsePortNumber(port);
  return host.length > 0 && portNumber !== null && portNumber <= 65535;
};

const isValidTaskUrl = (value: string): boolean =>
  isValidHostPortTaskUrl(value) || isValidHttpTaskUrl(value);

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
  status: "Статус",
  wins: "Победы",
  average_solve_time_ms: "Среднее время",
  stats_overridden: "Ручная правка",
  deleted: "Удаление",
} as const;

type AuditField = keyof typeof auditFieldLabels;

const auditFields: AuditField[] = [
  "username",
  "status",
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
  const [tokens, setTokens] = useState<AdminTokenResponse | null>(null);
  const [activeSection, setActiveSection] = useState<AdminSection>("tasks");
  const [password, setPassword] = useState("");
  const [loginFormError, setLoginFormError] = useState<string | null>(null);
  const [authLoading, setAuthLoading] = useState(false);
  const [logoutPending, setLogoutPending] = useState(false);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [category, setCategory] = useState<TaskCategory>("web");
  const [difficulty, setDifficulty] = useState<TaskDifficulty>("easy");
  const [timeLimit, setTimeLimit] = useState("60");
  const [flag, setFlag] = useState("");
  const [hints, setHints] = useState<string[]>(["", "", ""]);
  const [taskUrl, setTaskUrl] = useState("");
  const [sourceFile, setSourceFile] = useState<File | null>(null);
  const [existingSourceFileURL, setExistingSourceFileURL] = useState<
    string | null
  >(null);
  const [sourceFileCleared, setSourceFileCleared] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [taskFormErrors, setTaskFormErrors] = useState<TaskFormErrors>({});
  const [editingTaskId, setEditingTaskId] = useState<string | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [tasksLoading, setTasksLoading] = useState(false);
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
  const tokensRef = useRef<AdminTokenResponse | null>(null);
  const refreshPromiseRef = useRef<Promise<AdminTokenResponse> | null>(null);
  const logoutAbortRef = useRef<AbortController | null>(null);
  const isMountedRef = useRef(false);
  const authSessionVersionRef = useRef(0);
  const tasksRequestIDRef = useRef(0);
  const playersRequestIDRef = useRef(0);
  const playersEventsRef = useRef<EventSource | null>(null);
  const playersRealtimeRefreshTimerRef = useRef<number | null>(null);
  const playerAuditRequestIDRef = useRef(0);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const passwordInputRef = useRef<HTMLInputElement>(null);
  const { notification, showNotification: showTimedNotification } =
    useTimedNotification<Notification>();

  useEffect(() => {
    isMountedRef.current = true;
    const loadedTokens = adminSession.load();
    tokensRef.current = loadedTokens;
    setTokens(loadedTokens);
    const pendingPassword = passwordInputRef.current?.value;
    if (pendingPassword) {
      setPassword(pendingPassword);
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
    };
  }, []);

  const showNotification = useCallback(
    (type: "success" | "error" | "warning", message: string) => {
      showTimedNotification({ type, message }, 4000);
    },
    [showTimedNotification],
  );

  const clearTaskFormError = useCallback((field: TaskFormErrorField) => {
    setTaskFormErrors((current) => {
      if (!current[field]) return current;
      const next = { ...current };
      delete next[field];
      return next;
    });
  }, []);

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

  const saveTokens = useCallback(
    (nextTokens: AdminTokenResponse, sessionVersion?: number) => {
      if (
        sessionVersion !== undefined &&
        !isCurrentAuthSession(sessionVersion)
      ) {
        return;
      }
      adminSession.save(nextTokens);
      tokensRef.current = nextTokens;
      setTokens(nextTokens);
    },
    [isCurrentAuthSession],
  );

  const clearTokens = useCallback(
    (options: { preserveAdminCSRF?: boolean } = {}) => {
      const nextSessionVersion = authSessionVersionRef.current + 1;
      authSessionVersionRef.current = nextSessionVersion;
      tasksRequestIDRef.current += 1;
      playersRequestIDRef.current += 1;
      playerAuditRequestIDRef.current += 1;
      refreshPromiseRef.current = null;
      playersEventsRef.current?.close();
      playersEventsRef.current = null;
      if (playersRealtimeRefreshTimerRef.current !== null) {
        window.clearTimeout(playersRealtimeRefreshTimerRef.current);
        playersRealtimeRefreshTimerRef.current = null;
      }
      adminSession.clear({ preserveCSRF: options.preserveAdminCSRF });
      tokensRef.current = null;
      setTokens(null);
      setActiveSection("tasks");
      setTasks([]);
      setPlayers([]);
      setTasksLoading(false);
      setPlayersLoading(false);
      setSubmitting(false);
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

  const refreshTokens = useCallback(async (): Promise<AdminTokenResponse> => {
    const currentTokens = tokensRef.current;
    if (!currentTokens) {
      throw new Error("Unauthorized");
    }
    const sessionVersion = authSessionVersionRef.current;
    const refreshToken = currentTokens.refresh_token;

    if (!refreshPromiseRef.current) {
      const refreshPromise = adminApi
        .refresh(refreshToken)
        .then((nextTokens) => {
          if (!isCurrentAuthSession(sessionVersion)) {
            throw new Error("Unauthorized");
          }
          saveTokens(nextTokens, sessionVersion);
          return nextTokens;
        })
        .finally(() => {
          if (refreshPromiseRef.current === refreshPromise) {
            refreshPromiseRef.current = null;
          }
        });
      refreshPromiseRef.current = refreshPromise;
    }

    return refreshPromiseRef.current;
  }, [isCurrentAuthSession, saveTokens]);

  const runAdminRequest = useCallback(
    async <T,>(request: (accessToken: string) => Promise<T>): Promise<T> => {
      const currentTokens = tokensRef.current;
      if (!currentTokens) {
        throw new Error("Unauthorized");
      }
      const sessionVersion = authSessionVersionRef.current;

      try {
        const result = await request(currentTokens.access_token);
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

        const latestTokens = tokensRef.current;
        if (
          latestTokens &&
          latestTokens.access_token !== currentTokens.access_token
        ) {
          const result = await request(latestTokens.access_token);
          if (!isCurrentAuthSession(sessionVersion)) {
            throw new Error("Unauthorized");
          }
          return result;
        }

        try {
          const refreshed = await refreshTokens();
          const result = await request(refreshed.access_token);
          if (!isCurrentAuthSession(sessionVersion)) {
            throw new Error("Unauthorized");
          }
          return result;
        } catch (refreshError) {
          log.warn("admin token refresh failed", refreshError);
          if (isCurrentAuthSession(sessionVersion)) {
            clearTokens();
            showNotification("error", "Сессия истекла. Войдите снова.");
          }
          throw new Error("Unauthorized");
        }
      }
    },
    [clearTokens, isCurrentAuthSession, refreshTokens, showNotification],
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
    refreshPromiseRef.current = null;
    try {
      const nextTokens = await adminApi.login(password);
      if (!isMountedRef.current) {
        return;
      }
      authSessionVersionRef.current = sessionVersion;
      saveTokens(nextTokens, sessionVersion);
      setPassword("");
      setLoginFormError(null);
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
    const currentTokens = tokens;
    const logoutSessionVersion = clearTokens({ preserveAdminCSRF: true });
    if (!currentTokens) {
      adminSession.clear();
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
      await adminApi.logout(
        currentTokens.access_token,
        currentTokens.refresh_token,
        logoutController.signal,
      );
    } catch (logoutError) {
      // Local logout wins; access tokens are short-lived if refresh revocation fails offline.
      log.warn("admin logout request failed", logoutError);
    } finally {
      window.clearTimeout(logoutTimeout);
      if (logoutAbortRef.current === logoutController) {
        logoutAbortRef.current = null;
      }
      if (authSessionVersionRef.current === logoutSessionVersion) {
        adminSession.clear();
      }
      if (isMountedRef.current) {
        setLogoutPending(false);
      }
    }
  };

  const fetchTasks = useCallback(async () => {
    if (!tokens) return;
    const sessionVersion = authSessionVersionRef.current;
    const requestID = tasksRequestIDRef.current + 1;
    tasksRequestIDRef.current = requestID;
    const canApplyTasksRequest = () =>
      isCurrentAuthSession(sessionVersion) &&
      tasksRequestIDRef.current === requestID;
    setTasksLoading(true);
    try {
      const data = await runAdminRequest((accessToken) =>
        adminApi.listTasks(accessToken),
      );
      if (canApplyTasksRequest()) {
        setTasks(data);
      }
    } catch (error) {
      if (
        !canApplyTasksRequest() ||
        (error instanceof Error && error.message === "Unauthorized")
      ) {
        return;
      }
      showNotification(
        "error",
        apiErrorMessage(error, "Не удалось загрузить задачи"),
      );
    } finally {
      if (canApplyTasksRequest()) {
        setTasksLoading(false);
      }
    }
  }, [isCurrentAuthSession, runAdminRequest, showNotification, tokens]);

  const fetchPlayers = useCallback(
    async (options: { silent?: boolean } = {}) => {
      if (!tokens) return;
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
        const data = await runAdminRequest((accessToken) =>
          adminApi.listPlayers(accessToken, showDeletedPlayers),
        );
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
      tokens,
    ],
  );

  const schedulePlayersRealtimeRefresh = useCallback(() => {
    if (!tokensRef.current || activeSection !== "players") {
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
    if (!tokens) return;
    if (activeSection === "tasks") {
      fetchTasks();
    } else {
      fetchPlayers();
    }
  }, [activeSection, tokens, fetchPlayers, fetchTasks]);

  useEffect(() => {
    if (!tokens || activeSection !== "players") {
      return undefined;
    }

    const source = adminApi.openPlayerEvents();
    playersEventsRef.current = source;

    const handlePlayersChanged: EventListener = () => {
      schedulePlayersRealtimeRefresh();
    };
    source.addEventListener(ADMIN_PLAYERS_CHANGED_EVENT, handlePlayersChanged);
    source.onerror = (event) => {
      log.warn("admin players realtime stream error", event);
    };

    return () => {
      source.removeEventListener(
        ADMIN_PLAYERS_CHANGED_EVENT,
        handlePlayersChanged,
      );
      if (playersEventsRef.current === source) {
        playersEventsRef.current = null;
      }
      source.close();
      if (playersRealtimeRefreshTimerRef.current !== null) {
        window.clearTimeout(playersRealtimeRefreshTimerRef.current);
        playersRealtimeRefreshTimerRef.current = null;
      }
    };
  }, [activeSection, schedulePlayersRealtimeRefresh, tokens]);

  const resetForm = useCallback(() => {
    setEditingTaskId(null);
    setTitle("");
    setDescription("");
    setCategory("web");
    setDifficulty("easy");
    setTimeLimit("60");
    setFlag("");
    setHints(["", "", ""]);
    setTaskUrl("");
    setSourceFile(null);
    setExistingSourceFileURL(null);
    setSourceFileCleared(false);
    setTaskFormErrors({});
    if (fileInputRef.current) fileInputRef.current.value = "";
  }, []);

  const startEditing = (task: Task) => {
    setEditingTaskId(task.id);
    setTitle(task.title);
    setDescription(task.description);
    setCategory(task.category);
    setDifficulty(task.difficulty);
    setTimeLimit(String(task.time_limit));
    setFlag(task.flag);
    setHints(task.hints.length === 3 ? task.hints : ["", "", ""]);
    setTaskUrl(task.task_url ?? "");
    setSourceFile(null);
    setExistingSourceFileURL(task.source_file_url ?? null);
    setSourceFileCleared(false);
    setTaskFormErrors({});
    if (fileInputRef.current) fileInputRef.current.value = "";
    window.scrollTo({ top: 0, behavior: "smooth" });
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmedTitle = title.trim();
    const trimmedDescription = description.trim();
    const trimmedFlag = flag.trim();
    const validHints = hints.map((h) => h.trim());
    const parsedTimeLimit = parsePositiveInt32(timeLimit);
    const taskUrlValue = taskUrl.trim() || null;
    const nextErrors: TaskFormErrors = {};

    if (!trimmedTitle) {
      nextErrors.title = "Введите название задачи";
    } else if (countChars(trimmedTitle) > MAX_TASK_TITLE_LENGTH) {
      nextErrors.title = "Название должно быть не длиннее 255 символов";
    }

    if (!trimmedDescription) {
      nextErrors.description = "Описание не должно быть пустым";
    }

    if (parsedTimeLimit === null) {
      nextErrors.timeLimit =
        "Лимит времени должен быть целым числом от 1 до 2147483647";
    }

    if (!trimmedFlag) {
      nextErrors.flag = "Введите флаг";
    } else if (countChars(trimmedFlag) > MAX_TASK_FLAG_LENGTH) {
      nextErrors.flag = "Флаг должен быть не длиннее 255 символов";
    }

    validHints.forEach((hint, index) => {
      if (!hint) {
        nextErrors[`hint${index}` as TaskFormErrorField] =
          `Заполните подсказку ${index + 1}`;
      }
    });

    if (taskUrlValue && !isValidTaskUrl(taskUrlValue)) {
      nextErrors.taskUrl = "Укажите http(s) ссылку или host:port";
    }

    if (Object.keys(nextErrors).length > 0) {
      setTaskFormErrors(nextErrors);
      return;
    }

    if (parsedTimeLimit === null) {
      return;
    }

    setTaskFormErrors({});
    setSubmitting(true);
    const sessionVersion = authSessionVersionRef.current;
    try {
      const body: CreateTaskRequest = {
        title: trimmedTitle,
        description: trimmedDescription,
        category,
        difficulty,
        time_limit: parsedTimeLimit,
        flag: trimmedFlag,
        hints: validHints,
        task_url: taskUrlValue,
      };

      let savedTask: AdminTask;
      if (editingTaskId) {
        const updateBody: UpdateTaskRequest = { ...body };
        if (sourceFileCleared) {
          updateBody.source_file_url = null;
        }
        savedTask = await runAdminRequest((accessToken) =>
          adminApi.updateTask(accessToken, editingTaskId, updateBody),
        );
      } else {
        savedTask = await runAdminRequest((accessToken) =>
          adminApi.createTask(accessToken, body),
        );
      }

      let uploadFailed = false;
      if (sourceFile) {
        try {
          await runAdminRequest((accessToken) =>
            adminApi.uploadSource(accessToken, savedTask.id, sourceFile),
          );
        } catch (uploadError) {
          if (
            uploadError instanceof Error &&
            uploadError.message === "Unauthorized"
          ) {
            throw uploadError;
          }
          log.error("admin uploadSource failed", uploadError);
          uploadFailed = true;
        }
      }
      if (!isCurrentAuthSession(sessionVersion)) {
        return;
      }

      if (uploadFailed) {
        showNotification(
          "warning",
          `${editingTaskId ? "Задача обновлена" : "Задача создана"}, но файл не загрузился`,
        );
      } else {
        showNotification(
          "success",
          editingTaskId
            ? "Задача успешно обновлена!"
            : "Задача успешно создана!",
        );
      }
      resetForm();
      fetchTasks();
    } catch (err) {
      if (
        !isCurrentAuthSession(sessionVersion) ||
        (err instanceof Error && err.message === "Unauthorized")
      )
        return;
      setTaskFormErrors({
        form: apiErrorMessage(
          err,
          editingTaskId
            ? "Ошибка при обновлении задачи"
            : "Ошибка при создании задачи",
        ),
      });
    } finally {
      if (isCurrentAuthSession(sessionVersion)) {
        setSubmitting(false);
      }
    }
  };

  const handleDeleteTask = async (taskId: string) => {
    if (!confirm("Вы уверены, что хотите удалить эту задачу?")) return;
    const sessionVersion = authSessionVersionRef.current;
    try {
      await runAdminRequest((accessToken) =>
        adminApi.deleteTask(accessToken, taskId),
      );
      if (!isCurrentAuthSession(sessionVersion)) {
        return;
      }
      showNotification("success", "Задача удалена");
      fetchTasks();
    } catch (error) {
      if (
        !isCurrentAuthSession(sessionVersion) ||
        (error instanceof Error && error.message === "Unauthorized")
      ) {
        return;
      }
      if (error instanceof ApiError && error.status === 409) {
        showNotification(
          "error",
          "Нельзя удалить: задача используется в дуэлях",
        );
      } else {
        showNotification(
          "error",
          apiErrorMessage(error, "Ошибка при удалении задачи"),
        );
      }
    }
  };

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
      const updated = await runAdminRequest((accessToken) =>
        adminApi.updatePlayer(accessToken, editingPlayerId, body),
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
      await runAdminRequest((accessToken) =>
        adminApi.deletePlayer(accessToken, player.id),
      );
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
      const events = await runAdminRequest((accessToken) =>
        adminApi.listPlayerAudit(accessToken, player.id),
      );
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

  const updateHint = (index: number, value: string) => {
    const newHints = [...hints];
    newHints[index] = value;
    setHints(newHints);
    clearTaskFormError(`hint${index}` as TaskFormErrorField);
  };

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0] || null;
    if (file && !file.name.toLowerCase().endsWith(".zip")) {
      setSourceFile(null);
      if (fileInputRef.current) fileInputRef.current.value = "";
      setTaskFormErrors((current) => ({
        ...current,
        sourceFile: "Можно загружать только ZIP-архивы",
      }));
      return;
    }
    if (file && file.size > 100 * 1024 * 1024) {
      setSourceFile(null);
      if (fileInputRef.current) fileInputRef.current.value = "";
      setTaskFormErrors((current) => ({
        ...current,
        sourceFile: "Файл превышает 100 MB",
      }));
      return;
    }
    setSourceFile(file);
    clearTaskFormError("sourceFile");
    if (file) {
      setSourceFileCleared(false);
    }
  };

  const removeFile = () => {
    setSourceFile(null);
    clearTaskFormError("sourceFile");
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const removeExistingSourceFile = () => {
    setSourceFile(null);
    setSourceFileCleared(true);
    clearTaskFormError("sourceFile");
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const restoreExistingSourceFile = () => {
    setSourceFileCleared(false);
    clearTaskFormError("sourceFile");
  };

  const renderCategoryFields = () => {
    const taskURLPlaceholder =
      category === "pwn" ? "host:port" : "https://example.com/task";

    return (
      <>
        <div className={styles.categoryField}>
          <div className={styles.categoryFieldLabel}>
            {CATEGORY_CONFIG[category].icon} {CATEGORY_CONFIG[category].label}{" "}
            URL
          </div>
          <div className={styles.inputGroup}>
            <input
              type="text"
              value={taskUrl}
              onChange={(e) => {
                setTaskUrl(e.target.value);
                clearTaskFormError("taskUrl");
                clearTaskFormError("form");
              }}
              placeholder={taskURLPlaceholder}
              className={taskFormErrors.taskUrl ? styles.inputError : undefined}
              aria-invalid={Boolean(taskFormErrors.taskUrl)}
              aria-describedby={
                taskFormErrors.taskUrl ? "admin-task-url-error" : undefined
              }
            />
            {taskFormErrors.taskUrl && (
              <p id="admin-task-url-error" className={styles.fieldError}>
                {taskFormErrors.taskUrl}
              </p>
            )}
          </div>
        </div>

        <div className={styles.categoryField}>
          <div className={styles.categoryFieldLabel}>
            {CATEGORY_CONFIG[category].icon} ZIP-архив с исходниками
          </div>
          <div className={styles.fileUpload}>
            <div
              className={`${styles.fileUploadZone} ${taskFormErrors.sourceFile ? styles.fileUploadZoneError : ""}`}
              onClick={() => fileInputRef.current?.click()}
            >
              <div className={styles.fileUploadIcon}>📁</div>
              <div className={styles.fileUploadText}>
                <strong>Нажмите для выбора</strong> или перетащите ZIP-архив
              </div>
            </div>
            <input
              ref={fileInputRef}
              type="file"
              accept=".zip"
              onChange={handleFileChange}
              style={{ display: "none" }}
            />
            {sourceFile && (
              <div className={styles.fileInfo}>
                <span>📦</span>
                <span className={styles.fileInfoName}>{sourceFile.name}</span>
                <span>({(sourceFile.size / 1024 / 1024).toFixed(1)} MB)</span>
                <span className={styles.fileInfoRemove} onClick={removeFile}>
                  ✕
                </span>
              </div>
            )}
            {!sourceFile && existingSourceFileURL && !sourceFileCleared && (
              <div className={styles.fileInfo}>
                <span>📦</span>
                <span className={styles.fileInfoName}>
                  Текущий архив сохранён
                </span>
                <span
                  className={styles.fileInfoRemove}
                  onClick={removeExistingSourceFile}
                >
                  ✕
                </span>
              </div>
            )}
            {!sourceFile && existingSourceFileURL && sourceFileCleared && (
              <div className={styles.fileInfo}>
                <span>🗑</span>
                <span className={styles.fileInfoName}>
                  Текущий архив будет удалён
                </span>
                <span
                  className={styles.fileInfoRemove}
                  onClick={restoreExistingSourceFile}
                >
                  ↺
                </span>
              </div>
            )}
            {taskFormErrors.sourceFile && (
              <p className={styles.fieldError}>{taskFormErrors.sourceFile}</p>
            )}
          </div>
        </div>
      </>
    );
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
                    <span className={styles.taskBadge}>{player.status}</span>
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

  if (!tokens) {
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
        )}
      </main>
    );
  }
  return (
    <main className={`${styles.container} motion-page gpu-optimized`}>
      {notification && (
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
          {activeSection === "tasks"
            ? "Панель управления задачами"
            : "Панель управления игроками"}
        </p>
        <div className={styles.sectionTabs}>
          <button
            type="button"
            className={`${styles.sectionTab} ${activeSection === "tasks" ? styles.sectionTabActive : ""} motion-button`}
            onClick={() => setActiveSection("tasks")}
          >
            Задания
          </button>
          <button
            type="button"
            className={`${styles.sectionTab} ${activeSection === "players" ? styles.sectionTabActive : ""} motion-button`}
            onClick={() => setActiveSection("players")}
          >
            Игроки
          </button>
        </div>
      </div>
      <div
        key={activeSection}
        className={`${styles.sectionPanel} ${styles.sectionPanelEnter}`}
      >
        {activeSection === "tasks" ? (
          <>
            <div className={`${styles.card} motion-panel`}>
              <h2 className={styles.cardTitle}>
                {editingTaskId
                  ? "✏️ Редактировать задачу"
                  : "➕ Создать задачу"}
              </h2>
              <form onSubmit={handleSubmit} className={styles.form} noValidate>
                <div className={styles.inputGroup}>
                  <label>Название задачи</label>
                  <input
                    type="text"
                    required
                    value={title}
                    onChange={(e) => {
                      setTitle(e.target.value);
                      clearTaskFormError("title");
                      clearTaskFormError("form");
                    }}
                    placeholder="Введите название..."
                    maxLength={255}
                    className={
                      taskFormErrors.title ? styles.inputError : undefined
                    }
                    aria-invalid={Boolean(taskFormErrors.title)}
                    aria-describedby={
                      taskFormErrors.title
                        ? "admin-task-title-error"
                        : undefined
                    }
                  />
                  {taskFormErrors.title && (
                    <p
                      id="admin-task-title-error"
                      className={styles.fieldError}
                    >
                      {taskFormErrors.title}
                    </p>
                  )}
                </div>
                <div className={styles.inputGroup}>
                  <label>Описание</label>
                  <textarea
                    required
                    value={description}
                    onChange={(e) => {
                      setDescription(e.target.value);
                      clearTaskFormError("description");
                      clearTaskFormError("form");
                    }}
                    placeholder="Опишите задачу..."
                    rows={3}
                    className={
                      taskFormErrors.description ? styles.inputError : undefined
                    }
                    aria-invalid={Boolean(taskFormErrors.description)}
                    aria-describedby={
                      taskFormErrors.description
                        ? "admin-task-description-error"
                        : undefined
                    }
                  />
                  {taskFormErrors.description && (
                    <p
                      id="admin-task-description-error"
                      className={styles.fieldError}
                    >
                      {taskFormErrors.description}
                    </p>
                  )}
                </div>
                <div className={styles.formRow}>
                  <div className={styles.inputGroup}>
                    <label>Категория</label>
                    <select
                      value={category}
                      onChange={(e) => {
                        const nextCategory = e.target.value as TaskCategory;
                        setCategory(nextCategory);
                      }}
                      className={styles.select}
                    >
                      <option value="web">🌐 Web</option>
                      <option value="crypto">🔐 Crypto</option>
                      <option value="forensics">🔍 Forensics</option>
                      <option value="reverse">⚙️ Reverse</option>
                      <option value="pwn">💥 Pwn</option>
                      <option value="steganography">🖼️ Steganography</option>
                      <option value="ppc">🧮 PPC</option>
                      <option value="osint">🛰️ OSINT</option>
                      <option value="mobile">📱 Mobile</option>
                      <option value="hardware">🔧 Hardware</option>
                      <option value="misc">🧩 Misc</option>
                    </select>
                  </div>

                  <div className={styles.inputGroup}>
                    <label>Сложность</label>
                    <select
                      value={difficulty}
                      onChange={(e) =>
                        setDifficulty(e.target.value as TaskDifficulty)
                      }
                      className={styles.select}
                    >
                      <option value="easy">🟢 Лёгкая</option>
                      <option value="medium">🟡 Средняя</option>
                      <option value="hard">🔴 Сложная</option>
                    </select>
                  </div>
                </div>
                <div className={styles.formRow}>
                  <div className={styles.inputGroup}>
                    <label>Лимит времени (сек)</label>
                    <input
                      type="number"
                      required
                      min="1"
                      value={timeLimit}
                      onChange={(e) => {
                        setTimeLimit(e.target.value);
                        clearTaskFormError("timeLimit");
                        clearTaskFormError("form");
                      }}
                      placeholder="60"
                      className={
                        taskFormErrors.timeLimit ? styles.inputError : undefined
                      }
                      aria-invalid={Boolean(taskFormErrors.timeLimit)}
                      aria-describedby={
                        taskFormErrors.timeLimit
                          ? "admin-task-time-limit-error"
                          : undefined
                      }
                    />
                    {taskFormErrors.timeLimit && (
                      <p
                        id="admin-task-time-limit-error"
                        className={styles.fieldError}
                      >
                        {taskFormErrors.timeLimit}
                      </p>
                    )}
                  </div>

                  <div className={styles.inputGroup}>
                    <label>Флаг</label>
                    <input
                      type="text"
                      required
                      value={flag}
                      onChange={(e) => {
                        setFlag(e.target.value);
                        clearTaskFormError("flag");
                        clearTaskFormError("form");
                      }}
                      placeholder="ctf{...}"
                      className={
                        taskFormErrors.flag ? styles.inputError : undefined
                      }
                      aria-invalid={Boolean(taskFormErrors.flag)}
                      aria-describedby={
                        taskFormErrors.flag
                          ? "admin-task-flag-error"
                          : undefined
                      }
                    />
                    {taskFormErrors.flag && (
                      <p
                        id="admin-task-flag-error"
                        className={styles.fieldError}
                      >
                        {taskFormErrors.flag}
                      </p>
                    )}
                  </div>
                </div>
                {renderCategoryFields()}
                <div className={styles.inputGroup}>
                  <label>Подсказки (3 шт)</label>
                  <div className={styles.hintsGrid}>
                    {hints.map((hint, i) => (
                      <div key={i}>
                        <input
                          type="text"
                          required
                          value={hint}
                          onChange={(e) => updateHint(i, e.target.value)}
                          placeholder={`Подсказка ${i + 1}`}
                          className={
                            taskFormErrors[`hint${i}` as TaskFormErrorField]
                              ? styles.inputError
                              : undefined
                          }
                          aria-invalid={Boolean(
                            taskFormErrors[`hint${i}` as TaskFormErrorField],
                          )}
                          aria-describedby={
                            taskFormErrors[`hint${i}` as TaskFormErrorField]
                              ? `admin-task-hint-${i}-error`
                              : undefined
                          }
                        />
                        {taskFormErrors[`hint${i}` as TaskFormErrorField] && (
                          <p
                            id={`admin-task-hint-${i}-error`}
                            className={styles.fieldError}
                          >
                            {taskFormErrors[`hint${i}` as TaskFormErrorField]}
                          </p>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
                <div className={styles.btnGroup}>
                  <button
                    type="submit"
                    className={`${styles.btn} ${styles.btnPrimary} motion-button`}
                    disabled={submitting}
                  >
                    {submitting ? (
                      <>
                        <div
                          className={styles.spinner}
                          style={{ width: 18, height: 18 }}
                        ></div>
                        {editingTaskId ? "Сохранение..." : "Создание..."}
                      </>
                    ) : editingTaskId ? (
                      "💾 Сохранить задачу"
                    ) : (
                      "🚀 Создать задачу"
                    )}
                  </button>
                  <button
                    type="button"
                    className={`${styles.btn} ${styles.btnSecondary} motion-button`}
                    onClick={resetForm}
                  >
                    {editingTaskId ? "Отменить" : "Очистить"}
                  </button>
                </div>
                {taskFormErrors.form && (
                  <p
                    className={`${styles.fieldError} ${styles.formLevelError}`}
                  >
                    {taskFormErrors.form}
                  </p>
                )}
              </form>
            </div>
            <div className={styles.taskList}>
              <h2 className={styles.taskListTitle}>📋 Список задач</h2>

              {tasksLoading ? (
                <div className={styles.loading}>
                  <div className={styles.spinner}></div>
                  <p
                    style={{
                      color: "rgba(255,255,255,0.5)",
                      fontSize: "0.9rem",
                    }}
                  >
                    Загрузка задач...
                  </p>
                </div>
              ) : tasks.length === 0 ? (
                <div className={styles.empty}>
                  <div className={styles.emptyIcon}>📭</div>
                  <p className={styles.emptyText}>Пока нет созданных задач</p>
                </div>
              ) : (
                tasks.map((task) => (
                  <div
                    key={task.id}
                    className={`${styles.taskItem} motion-list-item`}
                  >
                    <div className={styles.taskItemInfo}>
                      <div className={styles.taskItemTitle}>{task.title}</div>
                      <div className={styles.taskItemMeta}>
                        <span
                          className={`${styles.taskBadge} ${
                            task.category === "web"
                              ? styles.taskBadgeWeb
                              : task.category === "crypto"
                                ? styles.taskBadgeCrypto
                                : styles.taskBadgeFile
                          }`}
                        >
                          {CATEGORY_CONFIG[task.category]?.icon || "📦"}{" "}
                          {CATEGORY_CONFIG[task.category]?.label ||
                            task.category}
                        </span>
                        <span
                          className={`${styles.taskBadge} ${DIFFICULTY_CONFIG[task.difficulty]?.badgeClass || ""}`}
                        >
                          {DIFFICULTY_CONFIG[task.difficulty]?.label ||
                            task.difficulty}
                        </span>
                        <span
                          style={{
                            fontSize: "0.65rem",
                            color: "rgba(255,255,255,0.3)",
                          }}
                        >
                          ⏱ {task.time_limit}с
                        </span>
                      </div>
                    </div>
                    <div className={styles.taskItemActions}>
                      <button
                        className={`${styles.taskItemBtn} motion-button`}
                        onClick={() => startEditing(task)}
                        title="Редактировать задачу"
                      >
                        ✏️
                      </button>
                      <button
                        className={`${styles.taskItemBtn} ${styles.taskItemBtnDanger} motion-button`}
                        onClick={() => handleDeleteTask(task.id)}
                        title="Удалить задачу"
                      >
                        🗑️
                      </button>
                    </div>
                  </div>
                ))
              )}
            </div>
          </>
        ) : (
          renderPlayersSection()
        )}
      </div>
      {renderPlayerAuditModal()}
    </main>
  );
}
