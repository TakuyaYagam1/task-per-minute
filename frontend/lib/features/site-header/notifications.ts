"use client";

import {
  createContext,
  createElement,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";

import {
  ApiError,
  getPlayerSessionEpoch,
  playerNotificationsApi,
  type PlayerNotification,
} from "../../shared/api";

const POLL_INTERVAL_MS = 10_000;
const SESSION_CHANGE_SETTLE_MS = 1_000;
const SESSION_RETRY_WINDOW_MS = 30_000;
const INVALIDATION_DEBOUNCE_MS = 150;
const TOAST_DURATION_MS = 6_000;
const noNotificationRefresh = (): void => undefined;

type RefreshReason = "initial" | "changed" | "poll" | "manual" | "resume" | "expiry";

type NotificationStatus = "checking" | "signed_out" | "ready" | "forbidden" | "error";

type NotificationState = Readonly<{
  status: NotificationStatus;
  notifications: readonly PlayerNotification[];
}>;

const INITIAL_STATE: NotificationState = {
  status: "checking",
  notifications: [],
};

const activeNotifications = (
  notifications: readonly PlayerNotification[],
): readonly PlayerNotification[] => {
  const now = Date.now();
  return notifications.filter((notification) => Date.parse(notification.expires_at) > now);
};

export function usePlayerNotifications(enabled = true) {
  const [state, setState] = useState<NotificationState>(INITIAL_STATE);
  const [toast, setToast] = useState<PlayerNotification | null>(null);
  const refreshRef = useRef<() => void>(() => undefined);
  const visible = enabled && (
    state.status === "ready" || state.status === "forbidden" || state.status === "error"
  );

  useEffect(() => {
    if (!enabled) {
      refreshRef.current = () => undefined;
      setState(INITIAL_STATE);
      setToast(null);
      return undefined;
    }

    let active = true;
    let sessionEpoch = getPlayerSessionEpoch();
    let lastSessionChangeAt = 0;
    let generation = 0;
    let source: EventSource | null = null;
    let controller: AbortController | null = null;
    let expiryTimer: number | undefined;
    let pollTimer: number | undefined;
    let retryTimer: number | undefined;
    let refreshTimer: number | undefined;
    let toastTimer: number | undefined;
    let sessionTimer: number | undefined;
    let retryAttempt = 0;
    let authenticated = false;
    let connected = false;
    let everAuthenticated = false;
    let hasSnapshotBaseline = false;
    let pendingRefreshReason: RefreshReason = "resume";
    const seenNotifications = new Map<string, number>();

    const clearTimer = (timer: number | undefined): void => {
      if (timer !== undefined) {
        window.clearTimeout(timer);
      }
    };

    const closeSource = (): void => {
      if (!source) {
        return;
      }
      source.onerror = null;
      source.close();
      source = null;
      connected = false;
    };

    const clearExpiryTimer = (): void => {
      clearTimer(expiryTimer);
      expiryTimer = undefined;
    };

    const clearToast = (): void => {
      clearTimer(toastTimer);
      toastTimer = undefined;
      setToast(null);
    };

    const showToast = (notification: PlayerNotification): void => {
      clearTimer(toastTimer);
      setToast(notification);
      toastTimer = window.setTimeout(() => {
        toastTimer = undefined;
        setToast(null);
      }, TOAST_DURATION_MS);
    };

    const refreshPriority = (reason: RefreshReason): number => {
      if (reason === "changed") {
        return 3;
      }
      if (reason === "poll") {
        return 2;
      }
      return reason === "manual" ? 1 : 0;
    };

    function queueRefresh(reason: RefreshReason): void {
      if (!active) {
        return;
      }
      if (refreshTimer !== undefined) {
        if (refreshPriority(reason) > refreshPriority(pendingRefreshReason)) {
          pendingRefreshReason = reason;
        }
        return;
      }
      pendingRefreshReason = reason;
      refreshTimer = window.setTimeout(() => {
        refreshTimer = undefined;
        const nextReason = pendingRefreshReason;
        pendingRefreshReason = "resume";
        void refresh(nextReason);
      }, INVALIDATION_DEBOUNCE_MS);
    }

    const scheduleExpiry = (notifications: readonly PlayerNotification[]): void => {
      clearExpiryTimer();
      if (notifications.length === 0) {
        return;
      }
      const nextExpiry = Math.min(
        ...notifications.map((notification) => Date.parse(notification.expires_at)),
      );
      expiryTimer = window.setTimeout(() => {
        expiryTimer = undefined;
        if (!active) {
          return;
        }
        const remaining = activeNotifications(notifications);
        setState((current) => ({
          ...current,
          notifications: activeNotifications(current.notifications),
        }));
        scheduleExpiry(remaining);
        queueRefresh("expiry");
      }, Math.max(0, nextExpiry - Date.now()));
    };

    const schedulePoll = (force = false): void => {
      clearTimer(pollTimer);
      pollTimer = undefined;
      if (!active || document.visibilityState !== "visible" || (connected && !force)) {
        return;
      }
      pollTimer = window.setTimeout(() => {
        pollTimer = undefined;
        queueRefresh("poll");
      }, POLL_INTERVAL_MS);
    };

    const scheduleReconnect = (): void => {
      if (
        !active ||
        !authenticated ||
        document.visibilityState !== "visible" ||
        retryTimer !== undefined
      ) {
        return;
      }
      const delay = Math.min(30_000, 1_000 * 2 ** Math.min(retryAttempt++, 5));
      retryTimer = window.setTimeout(() => {
        retryTimer = undefined;
        connect(sessionEpoch);
      }, delay);
    };

    const connect = (expectedEpoch: number): void => {
      if (
        !active ||
        !authenticated ||
        source ||
        document.visibilityState !== "visible" ||
        expectedEpoch !== sessionEpoch ||
        getPlayerSessionEpoch() !== expectedEpoch
      ) {
        return;
      }

      let nextSource: EventSource;
      try {
        nextSource = playerNotificationsApi.openEvents();
      } catch {
        schedulePoll();
        scheduleReconnect();
        return;
      }
      source = nextSource;

      nextSource.addEventListener("ready", () => {
        if (
          !active ||
          source !== nextSource ||
          expectedEpoch !== sessionEpoch ||
          getPlayerSessionEpoch() !== expectedEpoch
        ) {
          return;
        }
        connected = true;
        retryAttempt = 0;
        clearTimer(pollTimer);
        pollTimer = undefined;
        clearTimer(retryTimer);
        retryTimer = undefined;
        queueRefresh("resume");
      });

      nextSource.addEventListener("changed", () => {
        if (
          !active ||
          source !== nextSource ||
          expectedEpoch !== sessionEpoch ||
          getPlayerSessionEpoch() !== expectedEpoch
        ) {
          return;
        }
        queueRefresh("changed");
      });

      nextSource.onerror = () => {
        if (
          !active ||
          source !== nextSource ||
          expectedEpoch !== sessionEpoch ||
          getPlayerSessionEpoch() !== expectedEpoch
        ) {
          return;
        }
        closeSource();
        schedulePoll();
        scheduleReconnect();
      };
    };

    async function refresh(reason: RefreshReason): Promise<void> {
      if (!active || getPlayerSessionEpoch() !== sessionEpoch) {
        return;
      }

      const requestGeneration = ++generation;
      controller?.abort();
      const nextController = new AbortController();
      controller = nextController;

      if (!authenticated) {
        setState((current) => ({ ...current, status: "checking" }));
      }

      try {
        const response = await playerNotificationsApi.list(nextController.signal);
        if (
          !active ||
          requestGeneration !== generation ||
          getPlayerSessionEpoch() !== sessionEpoch
        ) {
          return;
        }
        authenticated = true;
        everAuthenticated = true;
        const notifications = activeNotifications(response.notifications);
        const now = Date.now();
        for (const [id, expiresAt] of seenNotifications) {
          if (expiresAt <= now) {
            seenNotifications.delete(id);
          }
        }
        const newNotifications = hasSnapshotBaseline
          ? notifications.filter((notification) => !seenNotifications.has(notification.id))
          : [];
        for (const notification of notifications) {
          seenNotifications.set(notification.id, Date.parse(notification.expires_at));
        }
        hasSnapshotBaseline = true;
        if (reason === "changed" || reason === "poll") {
          const newRemoval = newNotifications.find(
            (notification) => notification.type === "tournament_player_removed",
          );
          if (newRemoval) {
            showToast(newRemoval);
          }
        }
        setState({ status: "ready", notifications });
        scheduleExpiry(notifications);
        connect(sessionEpoch);
        schedulePoll();
      } catch (error) {
        if (
          !active ||
          requestGeneration !== generation ||
          nextController.signal.aborted ||
          getPlayerSessionEpoch() !== sessionEpoch
        ) {
          return;
        }

        if (error instanceof ApiError && error.status === 401) {
          authenticated = false;
          closeSource();
          clearExpiryTimer();
          seenNotifications.clear();
          hasSnapshotBaseline = false;
          clearToast();
          setState({ status: "signed_out", notifications: [] });
          if (Date.now() - lastSessionChangeAt < SESSION_RETRY_WINDOW_MS) {
            schedulePoll();
          }
          return;
        }

        clearExpiryTimer();
        if (error instanceof ApiError && error.status === 403) {
          authenticated = false;
          closeSource();
          seenNotifications.clear();
          hasSnapshotBaseline = false;
          clearToast();
          setState({ status: "forbidden", notifications: [] });
          return;
        }

        setState((current) => ({
          status: everAuthenticated ? "error" : current.status,
          notifications: everAuthenticated ? current.notifications : [],
        }));
        schedulePoll(true);
      }
    }

    refreshRef.current = () => {
      queueRefresh("manual");
    };

    const handleSessionChange = (): void => {
      const nextEpoch = getPlayerSessionEpoch();
      if (nextEpoch === sessionEpoch) {
        return;
      }

      sessionEpoch = nextEpoch;
      lastSessionChangeAt = Date.now();
      generation += 1;
      controller?.abort();
      controller = null;
      closeSource();
      clearExpiryTimer();
      clearTimer(pollTimer);
      pollTimer = undefined;
      clearTimer(retryTimer);
      retryTimer = undefined;
      clearTimer(refreshTimer);
      refreshTimer = undefined;
      pendingRefreshReason = "resume";
      authenticated = false;
      everAuthenticated = false;
      hasSnapshotBaseline = false;
      seenNotifications.clear();
      clearToast();
      retryAttempt = 0;
      setState({ status: "checking", notifications: [] });
      refreshTimer = window.setTimeout(() => {
        refreshTimer = undefined;
        pendingRefreshReason = "resume";
        void refresh("initial");
      }, SESSION_CHANGE_SETTLE_MS);
    };

    const handleVisibilityChange = (): void => {
      if (document.visibilityState !== "visible") {
        closeSource();
        clearTimer(pollTimer);
        pollTimer = undefined;
        clearTimer(retryTimer);
        retryTimer = undefined;
        return;
      }
      handleSessionChange();
      queueRefresh("resume");
      if (authenticated) {
        connect(sessionEpoch);
      }
    };

    const handleFocus = (): void => {
      if (document.visibilityState !== "visible") {
        return;
      }
      handleSessionChange();
      queueRefresh("resume");
      if (authenticated) {
        connect(sessionEpoch);
      }
    };

    void refresh("initial");
    sessionTimer = window.setInterval(handleSessionChange, 1_000);
    window.addEventListener("focus", handleFocus);
    window.addEventListener("online", handleFocus);
    document.addEventListener("visibilitychange", handleVisibilityChange);

    return () => {
      active = false;
      generation += 1;
      refreshRef.current = () => undefined;
      controller?.abort();
      closeSource();
      clearExpiryTimer();
      clearTimer(pollTimer);
      clearTimer(retryTimer);
      clearTimer(refreshTimer);
      clearTimer(toastTimer);
      if (sessionTimer !== undefined) {
        window.clearInterval(sessionTimer);
      }
      window.removeEventListener("focus", handleFocus);
      window.removeEventListener("online", handleFocus);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [enabled]);

  const refresh = useCallback(() => {
    refreshRef.current();
  }, []);

  return {
    ...state,
    visible,
    toast,
    refresh,
  };
}

export type PlayerNotificationsState = ReturnType<typeof usePlayerNotifications>;

type PlayerNotificationsContextValue = PlayerNotificationsState & Readonly<{
  setEnabled: Dispatch<SetStateAction<boolean>>;
}>;

const PlayerNotificationsContext = createContext<PlayerNotificationsContextValue | null>(null);

export function PlayerNotificationsProvider({ children }: Readonly<{ children: ReactNode }>) {
  const [enabled, setEnabled] = useState(false);
  const notifications = usePlayerNotifications(enabled);
  const value = useMemo(
    () => ({
      ...notifications,
      status: enabled ? notifications.status : "checking",
      notifications: enabled ? notifications.notifications : [],
      visible: enabled && notifications.visible,
      toast: enabled ? notifications.toast : null,
      refresh: enabled ? notifications.refresh : noNotificationRefresh,
      setEnabled,
    }),
    [enabled, notifications, setEnabled],
  );

  return (
    createElement(PlayerNotificationsContext.Provider, { value }, children)
  );
}

export function usePlayerNotificationsCenter(): PlayerNotificationsContextValue {
  const context = useContext(PlayerNotificationsContext);
  if (!context) {
    throw new Error("PlayerNotificationsProvider is missing");
  }
  return context;
}
