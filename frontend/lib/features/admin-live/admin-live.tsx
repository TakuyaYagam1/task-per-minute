"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  type ReactNode,
} from "react";

import {
  adminApi,
  ADMIN_PLAYERS_CHANGED_EVENT,
  ADMIN_TOURNAMENTS_CHANGED_EVENT,
} from "../../shared/api";
import { log } from "../../shared/lib";

const TOPICS = ["players", "tasks", "tournaments"] as const;
export type AdminLiveTopic = (typeof TOPICS)[number];
type Listener = (topics: ReadonlySet<AdminLiveTopic>) => void;
type Subscribe = (listener: Listener) => () => void;
type RequestRunner = <T>(request: () => Promise<T>) => Promise<T>;

const AdminLiveContext = createContext<Subscribe | null>(null);

export function AdminLiveProvider({
  children,
  runAdminRequest,
  onChange,
}: Readonly<{
  children: ReactNode;
  runAdminRequest: RequestRunner;
  onChange?: (topics: ReadonlySet<AdminLiveTopic>) => void;
}>) {
  const listenersRef = useRef(new Set<Listener>());
  const onChangeRef = useRef(onChange);
  useEffect(() => {
    onChangeRef.current = onChange;
  }, [onChange]);

  const subscribe = useCallback<Subscribe>((listener) => {
    listenersRef.current.add(listener);
    return () => {
      listenersRef.current.delete(listener);
    };
  }, []);

  useEffect(() => {
    let active = true;
    let source: EventSource | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let flushTimer: ReturnType<typeof setTimeout> | undefined;
    let retryAttempt = 0;
    let opening = false;
    const pending = new Set<AdminLiveTopic>();

    const invalidate = (topics: readonly AdminLiveTopic[]) => {
      for (const topic of topics) pending.add(topic);
      if (flushTimer !== undefined) return;
      flushTimer = setTimeout(() => {
        flushTimer = undefined;
        if (!active) return;
        const changed = new Set(pending);
        pending.clear();
        if (changed.has("players")) {
          window.dispatchEvent(new Event(ADMIN_PLAYERS_CHANGED_EVENT));
        }
        if (changed.has("tournaments")) {
          window.dispatchEvent(new Event(ADMIN_TOURNAMENTS_CHANGED_EVENT));
        }
        onChangeRef.current?.(changed);
        for (const listener of listenersRef.current) listener(changed);
      }, 150);
    };

    const closeSource = () => {
      if (!source) return;
      source.onerror = null;
      adminApi.closeEvents(source);
      source = null;
    };

    const scheduleReconnect = () => {
      if (!active || retryTimer !== undefined) return;
      const delay = Math.min(30_000, 1_000 * 2 ** Math.min(retryAttempt++, 5));
      retryTimer = setTimeout(() => {
        retryTimer = undefined;
        void connect();
      }, delay);
    };

    const connect = async () => {
      if (!active || opening || source) return;
      opening = true;
      try {
        await runAdminRequest(() => adminApi.ensureFreshSession());
        if (!active) return;
        const next = adminApi.openEvents();
        source = next;
        next.addEventListener("ready", () => {
          if (!active || source !== next) return;
          retryAttempt = 0;
          // Notifications are hints; recovering a stream always reloads current data.
          invalidate(TOPICS);
        });
        next.addEventListener("changed", (event: MessageEvent<string>) => {
          if (!active || source !== next) return;
          try {
            const payload: unknown = JSON.parse(event.data);
            if (
              payload &&
              typeof payload === "object" &&
              "topic" in payload &&
              TOPICS.includes(payload.topic as AdminLiveTopic)
            ) {
              invalidate([payload.topic as AdminLiveTopic]);
            }
          } catch {
            log.warn("admin event payload is invalid");
          }
        });
        next.onerror = () => {
          if (!active || source !== next) return;
          closeSource();
          scheduleReconnect();
        };
      } catch {
        scheduleReconnect();
      } finally {
        opening = false;
      }
    };

    const resume = () => {
      if (document.visibilityState !== "visible") return;
      invalidate(TOPICS);
      if (!source) {
        clearTimeout(retryTimer);
        retryTimer = undefined;
        void connect();
      }
    };
    void connect();
    window.addEventListener("online", resume);
    window.addEventListener("focus", resume);
    document.addEventListener("visibilitychange", resume);
    return () => {
      active = false;
      clearTimeout(retryTimer);
      clearTimeout(flushTimer);
      closeSource();
      window.removeEventListener("online", resume);
      window.removeEventListener("focus", resume);
      document.removeEventListener("visibilitychange", resume);
    };
  }, [runAdminRequest]);

  return (
    <AdminLiveContext.Provider value={subscribe}>
      {children}
    </AdminLiveContext.Provider>
  );
}

export function useAdminLiveRefresh(
  topics: AdminLiveTopic | readonly AdminLiveTopic[],
  refresh: () => void | Promise<void>,
  enabled = true,
): void {
  const subscribe = useContext(AdminLiveContext);
  const latest = useRef({ topics, refresh, enabled });
  const flushRef = useRef<(() => void) | null>(null);
  useEffect(() => {
    latest.current = { topics, refresh, enabled };
    if (enabled) flushRef.current?.();
  }, [topics, refresh, enabled]);

  useEffect(() => {
    if (!subscribe) return;
    let active = true;
    let pending = false;
    let running = false;
    const flush = async () => {
      if (!active || running || !pending || !latest.current.enabled) return;
      pending = false;
      running = true;
      try {
        await latest.current.refresh();
      } catch {
        log.warn("admin background refresh failed");
      } finally {
        running = false;
        if (active && pending) void flush();
      }
    };
    flushRef.current = () => {
      void flush();
    };
    const unsubscribe = subscribe((changed) => {
      const selected = typeof latest.current.topics === "string"
        ? [latest.current.topics] : latest.current.topics;
      if (!selected.some((topic) => changed.has(topic))) return;
      pending = true;
      void flush();
    });
    return () => {
      active = false;
      flushRef.current = null;
      unsubscribe();
    };
  }, [subscribe]);
}
