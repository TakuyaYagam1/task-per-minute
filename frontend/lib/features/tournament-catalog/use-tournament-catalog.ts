"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import {
  ApiError,
  listPublicTournaments,
  openPublicTournamentEvents,
} from "../../shared/api";
import {
  catalogQueryKey,
  initialCatalogLoadState,
  toCatalogPage,
  toCatalogApiQuery,
  type CatalogLoadState,
  type CatalogQuery,
} from "./model";

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  error.name === "AbortError";

const errorText = (error: unknown): string => {
  if (error instanceof ApiError) {
    if (error.kind === "rate_limited") {
      return "Каталог временно ограничил частоту запросов. Повторите позже.";
    }
    if (error.kind === "not_found") {
      return "Каталог не найден.";
    }
    if (error.kind === "transport") {
      return "Не удалось загрузить каталог. Проверьте соединение.";
    }
  }
  return "Не удалось загрузить каталог. Повторите попытку.";
};

const errorStatus = (error: unknown): CatalogLoadState["status"] =>
  error instanceof ApiError && error.kind === "rate_limited"
    ? "rate_limited"
    : "error";

export type TournamentCatalogState = CatalogLoadState & Readonly<{
  retry: () => void;
}>;

export const useTournamentCatalog = (query: CatalogQuery): TournamentCatalogState => {
  const [state, setState] = useState<CatalogLoadState>(initialCatalogLoadState);
  const [retryVersion, setRetryVersion] = useState(0);
  const requestRef = useRef(0);
  const loadedQueryKeyRef = useRef<string | null>(null);

  const retry = useCallback(() => {
    setRetryVersion((current) => current + 1);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const requestId = ++requestRef.current;
    const queryKey = catalogQueryKey(query);
    const sameQuery = loadedQueryKeyRef.current === queryKey;

    setState((current) => ({
      status: "loading",
      items: sameQuery ? current.items : [],
      nextCursor: sameQuery ? current.nextCursor : null,
      error: null,
    }));

    const timer = window.setTimeout(() => {
      void listPublicTournaments(toCatalogApiQuery(query), controller.signal)
        .then((response) => {
          if (controller.signal.aborted || requestId !== requestRef.current) {
            return;
          }
          const page = toCatalogPage(response);
          loadedQueryKeyRef.current = queryKey;
          setState({
            status: page.items.length > 0 ? "ready" : "empty",
            items: page.items,
            nextCursor: page.nextCursor,
            error: null,
          });
        })
        .catch((error: unknown) => {
          if (controller.signal.aborted || requestId !== requestRef.current || isAbortError(error)) {
            return;
          }
          setState((current) => ({
            status: current.items.length > 0 ? "stale" : errorStatus(error),
            items: current.items,
            nextCursor: current.nextCursor,
            error: errorText(error),
          }));
        });
    }, 180);

    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [query, retryVersion]);

  useEffect(() => {
    if (typeof document === "undefined") {
      return undefined;
    }

    let active = true;
    let source: EventSource | null = null;
    let retryTimer: number | undefined;
    let invalidationTimer: number | undefined;
    let retryAttempt = 0;

    const closeSource = (): void => {
      if (source === null) {
        return;
      }
      source.onerror = null;
      source.close();
      source = null;
    };

    const scheduleRetry = (): void => {
      if (!active || document.visibilityState !== "visible" || retryTimer !== undefined) {
        return;
      }
      const delay = Math.min(30_000, 1_000 * 2 ** Math.min(retryAttempt, 5));
      retryAttempt += 1;
      retryTimer = window.setTimeout(() => {
        retryTimer = undefined;
        connect();
      }, delay);
    };

    const connect = (): void => {
      if (!active || source !== null || document.visibilityState !== "visible") {
        return;
      }
      try {
        const nextSource = openPublicTournamentEvents();
        source = nextSource;
        nextSource.addEventListener("ready", () => {
          if (!active) {
            return;
          }
          retryAttempt = 0;
          setRetryVersion((current) => current + 1);
        });
        nextSource.addEventListener("changed", (event: Event) => {
          if (!active) {
            return;
          }
          let payload: unknown;
          try {
            payload = JSON.parse((event as MessageEvent<string>).data) as unknown;
          } catch {
            return;
          }
          if (
            typeof payload !== "object" ||
            payload === null ||
            !("topic" in payload) ||
            payload.topic !== "tournaments" ||
            invalidationTimer !== undefined
          ) {
            return;
          }
          invalidationTimer = window.setTimeout(() => {
            invalidationTimer = undefined;
            if (active && document.visibilityState === "visible") {
              setRetryVersion((current) => current + 1);
            }
          }, 150);
        });
        nextSource.onerror = () => {
          closeSource();
          scheduleRetry();
        };
      } catch {
        scheduleRetry();
      }
    };

    const handleVisibilityChange = (): void => {
      if (document.visibilityState !== "visible") {
        if (retryTimer !== undefined) {
          window.clearTimeout(retryTimer);
          retryTimer = undefined;
        }
        if (invalidationTimer !== undefined) {
          window.clearTimeout(invalidationTimer);
          invalidationTimer = undefined;
        }
        closeSource();
        return;
      }
      if (retryTimer !== undefined) {
        window.clearTimeout(retryTimer);
        retryTimer = undefined;
      }
      connect();
    };

    connect();
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      active = false;
      if (retryTimer !== undefined) {
        window.clearTimeout(retryTimer);
      }
      if (invalidationTimer !== undefined) {
        window.clearTimeout(invalidationTimer);
      }
      closeSource();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, []);

  return { ...state, retry };
};
