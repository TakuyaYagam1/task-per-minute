"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import {
  ApiError,
  listPublicTournaments,
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

  return { ...state, retry };
};
