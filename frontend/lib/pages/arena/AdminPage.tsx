"use client";
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  activateAdminSession,
  adminApi,
  ApiError,
  canResumeAdminSession,
  clearAdminSession,
  type AdminPlayer,
  type AdminPlayerAuditEvent,
  type AdminSessionResponse,
  type CreateAdminPlayerRequest,
  type UpdateAdminPlayerRequest,
} from "../../shared/api";
import {
  getSafeArenaReturnPath,
  log,
  useTimedNotification,
} from "../../shared/lib";
import { Dialog, TechnicalDetails, ViewportPortal } from "../../shared/ui";
import { AdminLiveProvider } from "../../features/admin-live";
import { useSiteHeaderAuth } from "../../features/site-header";
import {
  TournamentAdminPanel,
  TournamentJournalSection,
  TournamentTasksSection,
} from "../../widgets/tournament-admin";
import {
  AdminShell,
  tournamentNavigation,
  useAdminNavigation,
} from "./admin/AdminShell";
import { AdminLogin } from "./admin/AdminLogin";
import type { AdminNavigation } from "./admin/navigation";
import styles from "./admin.module.css";

type Player = AdminPlayer;
type PlayerAuditEvent = AdminPlayerAuditEvent;
type PlayerFormErrorField = "username" | "wins" | "averageMs" | "form";
type PlayerFormErrors = Partial<Record<PlayerFormErrorField, string>>;
type PlayerStateFilter = "active" | "deleted" | "all";
type PlayerWinsFilter = "all" | "with_wins" | "without_wins";
type PlayerDialogMode = "create" | "edit";
type PlayerEditSnapshot = {
  id: string;
  username: string;
  wins: string;
  averageMs: string;
};

interface Notification {
  type: "success" | "error" | "warning";
  message: string;
}

const USERNAME_RE = /^[a-zA-Z0-9_-]{2,50}$/;
const MAX_INT32 = 2_147_483_647;
const LOGOUT_TIMEOUT_MS = 8_000;

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

export default function AdminPage() {
  const [session, setSession] = useState<AdminSessionResponse | null>(null);
  const [sessionChecking, setSessionChecking] = useState(true);
  const [password, setPassword] = useState("");
  const [loginFormError, setLoginFormError] = useState<string | null>(null);
  const [authLoading, setAuthLoading] = useState(false);
  const [logoutPending, setLogoutPending] = useState(false);
  const [childDirty, setChildDirty] = useState(false);
  const [players, setPlayers] = useState<Player[]>([]);
  const [playersLoading, setPlayersLoading] = useState(false);
  const [editingPlayerId, setEditingPlayerId] = useState<string | null>(null);
  const [playerDialogMode, setPlayerDialogMode] =
    useState<PlayerDialogMode | null>(null);
  const [playerUsername, setPlayerUsername] = useState("");
  const [playerWins, setPlayerWins] = useState("0");
  const [playerAverageMs, setPlayerAverageMs] = useState("0");
  const [playerSubmitting, setPlayerSubmitting] = useState(false);
  const [playerFormErrors, setPlayerFormErrors] = useState<PlayerFormErrors>(
    {},
  );
  const [playerSearch, setPlayerSearch] = useState("");
  const [playerStateFilter, setPlayerStateFilter] =
    useState<PlayerStateFilter>("active");
  const [playerWinsFilter, setPlayerWinsFilter] =
    useState<PlayerWinsFilter>("all");
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
  const playerAuditRequestIDRef = useRef(0);
  const playerDialogInitialFocusRef = useRef<HTMLInputElement>(null);
  const playerDialogReturnFocusRef = useRef<HTMLElement | null>(null);
  const playerEditInitialRef = useRef<PlayerEditSnapshot | null>(null);
  const createPlayerButtonRef = useRef<HTMLButtonElement>(null);
  const passwordInputRef = useRef<HTMLInputElement>(null);
  const { notification, showNotification: showTimedNotification } =
    useTimedNotification<Notification>();
  const playerDialogDirty =
    playerDialogMode === "create"
      ? playerUsername.trim() !== ""
      : Boolean(
          playerEditInitialRef.current &&
            playerEditInitialRef.current.id === editingPlayerId &&
            (playerUsername !== playerEditInitialRef.current.username ||
              playerWins !== playerEditInitialRef.current.wins ||
              playerAverageMs !== playerEditInitialRef.current.averageMs),
        );
  const playerFormDirty = Boolean(
    playerDialogDirty,
  );
  const { navigation, navigate, setDirty } = useAdminNavigation({
    enabled: Boolean(session),
    dirty: playerFormDirty || childDirty,
  });
  const activeSection = navigation.section;
  const handleChildDirtyChange = useCallback(
    (dirty: boolean): void => {
      setDirty(dirty || playerFormDirty);
      setChildDirty(dirty);
    },
    [playerFormDirty, setDirty],
  );

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
    };
  }, []);

  useEffect(() => {
    if (activeSection !== "tasks" && activeSection !== "tournaments") {
      setChildDirty(false);
    }
  }, [activeSection]);

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
      clearAdminSession({ preserveCSRF: options.preserveAdminCSRF });
      sessionRef.current = null;
      setSession(null);
      setChildDirty(false);
      setPlayers([]);
      setPlayersLoading(false);
      setPlayerSubmitting(false);
      setEditingPlayerId(null);
      setPlayerDialogMode(null);
      playerEditInitialRef.current = null;
      setPlayerSearch("");
      setPlayerStateFilter("active");
      setPlayerWinsFilter("all");
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
          apiErrorMessage(error, "Ошибка соединения"),
        );
      }
    } finally {
      if (isMountedRef.current) {
        setAuthLoading(false);
      }
    }
  };

  const handleLogout = async () => {
    if (
      (playerFormDirty || childDirty) &&
      !window.confirm("Есть несохраненные изменения. Выйти без сохранения?")
    ) {
      return;
    }
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

  useSiteHeaderAuth(
    session || logoutPending ? handleLogout : undefined,
    logoutPending,
  );

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
        const data = await runAdminRequest(() =>
          adminApi.listPlayers(playerStateFilter !== "active"),
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
      playerStateFilter,
      showNotification,
      session,
    ],
  );

  useEffect(() => {
    if (session && activeSection === "players") {
      void fetchPlayers();
    }
  }, [activeSection, fetchPlayers, session]);

  const resetPlayerForm = useCallback(
    (options: { skipConfirm?: boolean } = {}): boolean => {
      if (playerSubmitting && !options.skipConfirm) {
        return false;
      }
      if (
        playerFormDirty &&
        !options.skipConfirm &&
        !window.confirm("Есть несохраненные изменения. Отменить их?")
      ) {
        return false;
      }
      playerEditInitialRef.current = null;
      setEditingPlayerId(null);
      setPlayerDialogMode(null);
      setPlayerUsername("");
      setPlayerWins("0");
      setPlayerAverageMs("0");
      setPlayerFormErrors({});
      return true;
    },
    [playerFormDirty, playerSubmitting],
  );

  useEffect(() => {
    if (activeSection === "players" || playerSubmitting) {
      return;
    }
    setEditingPlayerId(null);
    setPlayerDialogMode(null);
    playerEditInitialRef.current = null;
    setPlayerUsername("");
    setPlayerWins("0");
    setPlayerAverageMs("0");
    setPlayerFormErrors({});
    setAuditPlayer(null);
    setPlayerAuditEvents([]);
    setPlayerAuditLoading(false);
    setPlayerAuditError(null);
  }, [activeSection, playerSubmitting]);

  const openCreatePlayerDialog = (trigger?: HTMLElement | null): void => {
    if (playerSubmitting) {
      return;
    }
    if (playerDialogMode && !resetPlayerForm()) {
      return;
    }
    playerDialogReturnFocusRef.current =
      trigger ?? createPlayerButtonRef.current;
    playerEditInitialRef.current = null;
    setEditingPlayerId(null);
    setPlayerUsername("");
    setPlayerWins("0");
    setPlayerAverageMs("0");
    setPlayerFormErrors({});
    setPlayerDialogMode("create");
  };

  const startEditingPlayer = (
    player: Player,
    trigger?: HTMLElement | null,
  ): void => {
    if (playerSubmitting) {
      return;
    }
    if (editingPlayerId === player.id && playerDialogMode === "edit") {
      return;
    }
    if (playerDialogMode && !resetPlayerForm()) {
      return;
    }
    playerDialogReturnFocusRef.current = trigger ?? null;
    playerEditInitialRef.current = {
      id: player.id,
      username: player.username,
      wins: String(player.wins),
      averageMs: String(player.average_solve_time_ms),
    };
    setEditingPlayerId(player.id);
    setPlayerUsername(player.username);
    setPlayerWins(String(player.wins));
    setPlayerAverageMs(String(player.average_solve_time_ms));
    setPlayerFormErrors({});
    setPlayerDialogMode("edit");
  };

  const handlePlayerSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (playerSubmitting || !playerDialogMode) {
      return;
    }
    const nextErrors: PlayerFormErrors = {};
    const username = playerUsername.trim();
    if (!USERNAME_RE.test(username)) {
      nextErrors.username =
        "Имя игрока: 2-50 символов, латиница, цифры, _ или -";
    }
    if (playerDialogMode === "create") {
      if (Object.keys(nextErrors).length > 0) {
        setPlayerFormErrors(nextErrors);
        return;
      }

      setPlayerFormErrors({});
      setPlayerSubmitting(true);
      const sessionVersion = authSessionVersionRef.current;
      try {
        const body: CreateAdminPlayerRequest = { username };
        const created = await runAdminRequest(() =>
          adminApi.createPlayer(body),
        );
        if (!isCurrentAuthSession(sessionVersion)) {
          return;
        }
        setPlayers((current) => [
          created,
          ...current.filter((player) => player.id !== created.id),
        ]);
        resetPlayerForm({ skipConfirm: true });
        showNotification("success", "Игрок создан");
      } catch (error) {
        if (
          !isCurrentAuthSession(sessionVersion) ||
          (error instanceof Error && error.message === "Unauthorized")
        ) {
          return;
        }
        if (
          error instanceof ApiError &&
          (error.status === 409 || error.status === 422)
        ) {
          setPlayerFormErrors({
            username:
              error.status === 409
                ? "Такое имя уже занято"
                : "Имя игрока: 2-50 символов, латиница, цифры, _ или -",
          });
        } else {
          setPlayerFormErrors({
            form: apiErrorMessage(error, "Ошибка при создании игрока"),
          });
        }
      } finally {
        if (isCurrentAuthSession(sessionVersion)) {
          setPlayerSubmitting(false);
        }
      }
      return;
    }

    if (!editingPlayerId) {
      setPlayerFormErrors({ form: "Сначала выберите игрока из списка" });
      return;
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
      resetPlayerForm({ skipConfirm: true });
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
      } else if (error instanceof ApiError && error.status === 422) {
        setPlayerFormErrors({
          username: "Имя игрока: 2-50 символов, латиница, цифры, _ или -",
        });
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
    if (
      editingPlayerId === player.id &&
      playerFormDirty &&
      !window.confirm("Есть несохраненные изменения. Продолжить удаление?")
    ) {
      return;
    }
    if (
      !window.confirm(
        `Удалить игрока ${player.username}? Будут удалены аккаунт, email и логин. История действий сохранится.`,
      )
    ) {
      return;
    }

    const sessionVersion = authSessionVersionRef.current;
    try {
      await runAdminRequest(() => adminApi.deletePlayer(player.id));
      if (!isCurrentAuthSession(sessionVersion)) {
        return;
      }
      if (playerStateFilter !== "active") {
        fetchPlayers();
      } else {
        setPlayers((current) =>
          current.filter((item) => item.id !== player.id),
        );
      }
      if (editingPlayerId === player.id) {
        resetPlayerForm({ skipConfirm: true });
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

  const openPlayerAudit = async (player: Player, silent = false) => {
    if (!silent) {
      setAuditPlayer(player);
      setPlayerAuditEvents([]);
      setPlayerAuditError(null);
      setPlayerAuditLoading(true);
    }
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
        setPlayerAuditError(null);
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
      if (!silent) {
        setPlayerAuditError(message);
        showNotification("error", message);
      }
    } finally {
      if (canApplyAuditRequest()) {
        setPlayerAuditLoading(false);
      }
    }
  };

  const filteredPlayers = useMemo(() => {
    const search = playerSearch.trim().toLocaleLowerCase("ru-RU");
    return players.filter((player) => {
      const isDeleted = Boolean(player.deleted_at);
      if (playerStateFilter === "active" && isDeleted) {
        return false;
      }
      if (playerStateFilter === "deleted" && !isDeleted) {
        return false;
      }
      if (playerWinsFilter === "with_wins" && player.wins <= 0) {
        return false;
      }
      if (playerWinsFilter === "without_wins" && player.wins > 0) {
        return false;
      }
      return (
        search === "" ||
        player.username.toLocaleLowerCase("ru-RU").includes(search)
      );
    });
  }, [playerSearch, playerStateFilter, playerWinsFilter, players]);

  const playerFiltersActive =
    playerSearch.trim() !== "" ||
    playerStateFilter !== "active" ||
    playerWinsFilter !== "all";

  const resetPlayerFilters = (): void => {
    setPlayerSearch("");
    setPlayerStateFilter("active");
    setPlayerWinsFilter("all");
  };

  const renderPlayerDialog = () => {
    if (!playerDialogMode) {
      return null;
    }
    const isCreate = playerDialogMode === "create";
    return (
      <Dialog
        open
        title={isCreate ? "Создать игрока" : "Редактировать игрока"}
        size="small"
        initialFocusRef={playerDialogInitialFocusRef}
        returnFocusRef={playerDialogReturnFocusRef}
        closeLabel={isCreate ? "Закрыть создание игрока" : "Закрыть редактирование игрока"}
        closeOnEscape={!playerSubmitting}
        closeOnBackdrop={false}
        showCloseButton={!playerSubmitting}
        footer={
          <div className={styles.btnGroup}>
            <button
              type="submit"
              form="admin-player-form"
              className={`${styles.btn} ${styles.btnPrimary} motion-button`}
              disabled={playerSubmitting}
            >
              {playerSubmitting ? (
                <>
                  <div
                    className={styles.spinner}
                    style={{ width: 18, height: 18 }}
                  ></div>
                  {isCreate ? "Создание..." : "Сохранение..."}
                </>
              ) : isCreate ? (
                "Создать игрока"
              ) : (
                "Сохранить игрока"
              )}
            </button>
            <button
              type="button"
              className={`${styles.btn} ${styles.btnSecondary} motion-button`}
              onClick={() => resetPlayerForm()}
              disabled={playerSubmitting}
            >
              Отменить
            </button>
          </div>
        }
        onOpenChange={(open) => {
          if (!open && !playerSubmitting) {
            resetPlayerForm();
          }
        }}
      >
        <form
          id="admin-player-form"
          onSubmit={handlePlayerSubmit}
          className={`${styles.form} ${styles.playerDialogForm}`}
          noValidate
        >
          <div className={styles.inputGroup}>
            <label htmlFor="admin-player-username">Имя игрока</label>
            <input
              ref={playerDialogInitialFocusRef}
              type="text"
              id="admin-player-username"
              name="username"
              aria-label="Имя игрока"
              value={playerUsername}
              onChange={(event) => {
                setPlayerUsername(event.target.value);
                clearPlayerFormError("username");
                clearPlayerFormError("form");
              }}
              placeholder="username"
              maxLength={50}
              disabled={playerSubmitting}
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

          {!isCreate && (
            <>
              <div className={styles.formRow}>
                <div className={styles.inputGroup}>
                  <label htmlFor="admin-player-wins">Победы</label>
                  <input
                    type="number"
                    id="admin-player-wins"
                    name="wins"
                    aria-label="Победы игрока"
                    min="0"
                    value={playerWins}
                    onChange={(event) => {
                      setPlayerWins(event.target.value);
                      clearPlayerFormError("wins");
                      clearPlayerFormError("averageMs");
                      clearPlayerFormError("form");
                    }}
                    placeholder="0"
                    disabled={playerSubmitting}
                    className={
                      playerFormErrors.wins ? styles.inputError : undefined
                    }
                    aria-invalid={Boolean(playerFormErrors.wins)}
                    aria-describedby={
                      playerFormErrors.wins
                        ? "admin-player-wins-error"
                        : undefined
                    }
                  />
                  {playerFormErrors.wins && (
                    <p
                      id="admin-player-wins-error"
                      className={styles.fieldError}
                    >
                      {playerFormErrors.wins}
                    </p>
                  )}
                </div>
                <div className={styles.inputGroup}>
                  <label htmlFor="admin-player-average">Среднее время (мс)</label>
                  <input
                    type="number"
                    id="admin-player-average"
                    name="average_solve_time_ms"
                    aria-label="Среднее время игрока"
                    min="0"
                    value={playerAverageMs}
                    onChange={(event) => {
                      setPlayerAverageMs(event.target.value);
                      clearPlayerFormError("averageMs");
                      clearPlayerFormError("form");
                    }}
                    placeholder="0"
                    disabled={playerSubmitting}
                    className={
                      playerFormErrors.averageMs
                        ? styles.inputError
                        : undefined
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
              </div>
              <p className={styles.playerFormHint}>
                На табло: {formatMilliseconds(Number(playerAverageMs) || 0)}
              </p>
            </>
          )}

          {playerFormErrors.form && (
            <p className={`${styles.fieldError} ${styles.formLevelError}`}>
              {playerFormErrors.form}
            </p>
          )}
        </form>
      </Dialog>
    );
  };

  const renderPlayersSection = () => (
    <>
      <div className={styles.taskList}>
        <div className={styles.playersHeader}>
          <div>
            <h2 className={styles.taskListTitle}>Список игроков</h2>
            <p className={styles.playerCount} aria-live="polite">
              Показано: {filteredPlayers.length} из {players.length}
            </p>
          </div>
          <button
            ref={createPlayerButtonRef}
            type="button"
            className={`${styles.btn} ${styles.btnPrimary} motion-button`}
            onClick={(event) => openCreatePlayerDialog(event.currentTarget)}
            disabled={playerSubmitting}
          >
            Создать игрока
          </button>
        </div>
        <div className={styles.playerFilters}>
          <div className={styles.inputGroup}>
            <label htmlFor="admin-player-search">Поиск по имени</label>
            <input
              id="admin-player-search"
              type="search"
              value={playerSearch}
              onChange={(event) => setPlayerSearch(event.target.value)}
              placeholder="username"
              autoComplete="off"
            />
          </div>
          <div className={styles.inputGroup}>
            <label htmlFor="admin-player-state">Состояние</label>
            <select
              id="admin-player-state"
              value={playerStateFilter}
              onChange={(event) =>
                setPlayerStateFilter(event.target.value as PlayerStateFilter)
              }
            >
              <option value="active">Активные</option>
              <option value="deleted">Удаленные</option>
              <option value="all">Все</option>
            </select>
          </div>
          <div className={styles.inputGroup}>
            <label htmlFor="admin-player-wins-filter">Победы</label>
            <select
              id="admin-player-wins-filter"
              value={playerWinsFilter}
              onChange={(event) =>
                setPlayerWinsFilter(event.target.value as PlayerWinsFilter)
              }
            >
              <option value="all">Все</option>
              <option value="with_wins">Есть победы</option>
              <option value="without_wins">Без побед</option>
            </select>
          </div>
          {playerFiltersActive && (
            <button
              type="button"
              className={`${styles.btn} ${styles.btnSecondary} motion-button`}
              onClick={resetPlayerFilters}
            >
              Сбросить фильтры
            </button>
          )}
        </div>

        {playersLoading ? (
          <div className={styles.loading}>
            <div className={styles.spinner}></div>
            <p className={styles.loadingText}>
              Загрузка игроков...
            </p>
          </div>
        ) : players.length === 0 ? (
          <div className={styles.empty}>
            <div className={styles.emptyIcon}>-</div>
            <p className={styles.emptyText}>Пока нет игроков</p>
          </div>
        ) : filteredPlayers.length === 0 ? (
          <div className={styles.empty}>
            <div className={styles.emptyIcon}>-</div>
            <p className={styles.emptyText}>
              По заданным фильтрам игроки не найдены
            </p>
          </div>
        ) : (
          <div className={styles.playerGrid}>
            {filteredPlayers.map((player) => {
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
                      История
                    </button>
                    <button
                      className={`${styles.taskItemBtn} motion-button`}
                      onClick={(event) =>
                        startEditingPlayer(player, event.currentTarget)
                      }
                      aria-label={`Редактировать игрока ${player.username}`}
                      title={
                        isDeleted
                          ? "Удаленного игрока нельзя редактировать"
                          : "Редактировать игрока"
                      }
                      disabled={isDeleted || playerSubmitting}
                    >
                      Изменить
                    </button>
                    <button
                      className={`${styles.taskItemBtn} ${styles.taskItemBtnDanger} motion-button`}
                      onClick={() => handleDeletePlayer(player)}
                      aria-label={`Удалить игрока ${player.username}`}
                      title={isDeleted ? "Игрок уже удален" : "Удалить игрока"}
                      disabled={isDeleted}
                    >
                      Удалить
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
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
              <p className={styles.loadingText}>
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
                    <TechnicalDetails>
                      <p>Учетная запись: {event.actor_subject}</p>
                      <p>ID сессии: <code>{event.actor_jti}</code></p>
                    </TechnicalDetails>
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
      <main className={`${styles.container} ${styles.loginLayout} motion-page gpu-optimized`}>
        <AdminLogin
          password={password}
          error={loginFormError}
          loading={authLoading}
          logoutPending={logoutPending}
          passwordInputRef={passwordInputRef}
          onPasswordChange={(nextPassword) => {
            setPassword(nextPassword);
            setLoginFormError(null);
          }}
          onSubmit={handleLogin}
        />

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

  const renderAdminSection = (currentNavigation: AdminNavigation) => {
    if (currentNavigation.section === "players") {
      return renderPlayersSection();
    }
    if (currentNavigation.section === "tasks") {
      return (
        <TournamentTasksSection
          onSessionExpired={clearSession}
          onDirtyChange={handleChildDirtyChange}
          runAdminRequest={runAdminRequest}
        />
      );
    }
    if (currentNavigation.section === "audit") {
      return (
        <TournamentJournalSection
          selectedTournamentId={currentNavigation.tournamentId}
          onSelectTournament={(tournamentId) =>
            navigate({
              section: "audit",
              tournamentId,
              view: "audit",
            })
          }
          onSessionExpired={clearSession}
        />
      );
    }
    return (
      <TournamentAdminPanel
        selectedTournamentId={currentNavigation.tournamentId}
        activeView={currentNavigation.view}
        onNavigate={(tournamentId, view) =>
          navigate(tournamentNavigation(tournamentId, view))
        }
        onDirtyChange={handleChildDirtyChange}
        onSessionExpired={clearSession}
        runAdminRequest={runAdminRequest}
      />
    );
  };

  return (
    <AdminLiveProvider
      runAdminRequest={runAdminRequest}
      onChange={(topics) => {
        if (activeSection === "players" && topics.has("players")) {
          void fetchPlayers({ silent: true });
          if (auditPlayer) void openPlayerAudit(auditPlayer, true);
        }
      }}
    >
      <AdminShell
        navigation={navigation}
        onNavigate={navigate}
      >
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
        <div
          key={`${navigation.section}:${navigation.tournamentId ?? "all"}:${navigation.view}`}
          className={`${styles.sectionPanel} ${styles.sectionPanelEnter}`}
        >
          {renderAdminSection(navigation)}
        </div>
        {renderPlayerDialog()}
        {renderPlayerAuditModal()}
      </AdminShell>
    </AdminLiveProvider>
  );
}
