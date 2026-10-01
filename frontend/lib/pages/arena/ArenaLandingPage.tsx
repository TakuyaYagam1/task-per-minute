"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";

import { playerModel } from "../../entities/player";
import {
  DEFAULT_CATALOG_QUERY,
  catalogQueryKey,
  catalogQueryFromSearch,
  catalogSearchFromQuery,
  useTournamentCatalog,
  type CatalogQuery,
} from "../../features/tournament-catalog";
import { buildArenaPublicTournamentPath } from "../../shared/lib";
import type { Player } from "../../shared/types";
import { ArenaLanding, ArenaShell } from "../../widgets/arena";
import { TournamentCatalog } from "../../widgets/tournament-catalog";

export const ArenaLandingPage = () => {
  const router = useRouter();
  const [query, setQuery] = useState<CatalogQuery>(DEFAULT_CATALOG_QUERY);
  const [currentPlayer, setCurrentPlayer] = useState<Player | null>(null);
  const [logoutPending, setLogoutPending] = useState(false);
  const queryRef = useRef(query);
  const catalog = useTournamentCatalog(query);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    void playerModel.restoreCurrentPlayer(controller.signal).then((result) => {
      if (!active || result.kind === "aborted") return;
      setCurrentPlayer(result.kind === "ok" ? result.state.player : null);
    });
    return () => {
      active = false;
      controller.abort();
    };
  }, []);

  const handleLogout = useCallback(async (): Promise<void> => {
    if (!currentPlayer || logoutPending) return;
    setLogoutPending(true);
    try {
      await playerModel.clearCurrentPlayer();
    } finally {
      setCurrentPlayer(null);
      setLogoutPending(false);
      router.replace("/login");
    }
  }, [currentPlayer, logoutPending, router]);

  useEffect(() => {
    const currentQuery = catalogQueryFromSearch(window.location.search);
    const previousQuery = queryRef.current;
    queryRef.current = currentQuery;
    if (catalogQueryKey(currentQuery) !== catalogQueryKey(previousQuery)) {
      setQuery(currentQuery);
    }

    const handlePopState = (): void => {
      const nextQuery = catalogQueryFromSearch(window.location.search);
      queryRef.current = nextQuery;
      setQuery(nextQuery);
    };
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  const handleQueryChange = useCallback((nextQuery: CatalogQuery): void => {
    const normalizedQuery: CatalogQuery = {
      ...nextQuery,
      q: nextQuery.q.slice(0, 80),
      cursor: nextQuery.cursor ?? null,
    };
    const previousQuery = queryRef.current;
    const search = catalogSearchFromQuery(normalizedQuery);
    const href = `/arena${search}`;
    const historyMethod = previousQuery.q !== normalizedQuery.q ? "replaceState" : "pushState";
    window.history[historyMethod](window.history.state, "", href);
    queryRef.current = normalizedQuery;
    setQuery(normalizedQuery);
  }, []);

  const buildTournamentHref = useCallback((publicId: string): string => {
    const returnPath = `/arena${catalogSearchFromQuery(queryRef.current)}`;
    return buildArenaPublicTournamentPath(publicId, "overview", returnPath);
  }, []);

  return (
    <ArenaShell
      accessStatus="ready"
      logoutPending={logoutPending}
      onLogout={currentPlayer ? handleLogout : undefined}
    >
      <ArenaLanding>
        <TournamentCatalog
          buildTournamentHref={(item) => buildTournamentHref(item.publicId)}
          onQueryChange={handleQueryChange}
          query={query}
          state={catalog}
        />
      </ArenaLanding>
    </ArenaShell>
  );
};

ArenaLandingPage.displayName = "ArenaLandingPage";
