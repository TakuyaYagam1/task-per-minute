"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  ApiError,
  operatorApi,
  type Tournament,
} from "../../shared/api";
import { useAdminLiveRefresh } from "../../features/admin-live";
import { Message, Panel } from "../../shared/ui";

import { TournamentAuditPanel } from "./TournamentAuditPanel";
import styles from "./TournamentJournalSection.module.css";

type LoadState = "loading" | "ready" | "error";
type LoadOptions = Readonly<{ silent?: boolean }>;

export type TournamentJournalSectionProps = Readonly<{
  selectedTournamentId: string | null;
  onSelectTournament: (tournamentId: string | null) => void;
  onSessionExpired?: () => void;
}>;

const PAGE_SIZE = 200;

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const problemMessage = (error: unknown, fallback: string): string => {
  if (error instanceof ApiError) {
    return error.problem?.detail || error.problem?.title || fallback;
  }
  return fallback;
};

const readAllTournaments = async (signal: AbortSignal): Promise<Tournament[]> => {
  const result: Tournament[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  while (true) {
    const page = await operatorApi.listTournaments(
      cursor ? { cursor, page_size: PAGE_SIZE } : { page_size: PAGE_SIZE },
      signal,
    );
    result.push(...page.items);
    if (!page.next_cursor || seenCursors.has(page.next_cursor)) {
      return result;
    }
    seenCursors.add(page.next_cursor);
    cursor = page.next_cursor;
  }
};

export const TournamentJournalSection = ({
  onSelectTournament,
  onSessionExpired,
  selectedTournamentId,
}: TournamentJournalSectionProps) => {
  const [tournaments, setTournaments] = useState<Tournament[]>([]);
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [loadError, setLoadError] = useState<string | null>(null);
  const controllerRef = useRef<AbortController | null>(null);
  const requestGenerationRef = useRef(0);
  const loadStateRef = useRef(loadState);
  loadStateRef.current = loadState;
  const selectedTournament = useMemo(
    () =>
      tournaments.find((tournament) => tournament.id === selectedTournamentId) ??
      null,
    [selectedTournamentId, tournaments],
  );

  const loadTournaments = useCallback(async (options: LoadOptions = {}): Promise<void> => {
    const previousLoadState = loadStateRef.current;
    controllerRef.current?.abort();
    const controller = new AbortController();
    const requestGeneration = requestGenerationRef.current + 1;
    requestGenerationRef.current = requestGeneration;
    controllerRef.current = controller;
    if (!options.silent) {
      loadStateRef.current = "loading";
      setLoadState("loading");
      setLoadError(null);
    }
    try {
      const items = await readAllTournaments(controller.signal);
      if (
        controller.signal.aborted ||
        requestGenerationRef.current !== requestGeneration
      ) {
        return;
      }
      setTournaments(items);
      loadStateRef.current = "ready";
      setLoadState("ready");
      setLoadError(null);
    } catch (error) {
      if (
        controller.signal.aborted ||
        requestGenerationRef.current !== requestGeneration ||
        isAbortError(error)
      ) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (options.silent) {
        if (previousLoadState === "loading") {
          loadStateRef.current = "error";
          setLoadState("error");
          setLoadError(problemMessage(error, "Не удалось загрузить список соревнований"));
        }
        return;
      }
      loadStateRef.current = "error";
      setLoadState("error");
      setLoadError(problemMessage(error, "Не удалось загрузить список соревнований"));
    } finally {
      if (controllerRef.current === controller) {
        controllerRef.current = null;
      }
    }
  }, [onSessionExpired]);

  useAdminLiveRefresh("tournaments", () => loadTournaments({ silent: true }));

  useEffect(() => {
    void loadTournaments();
    return () => {
      controllerRef.current?.abort();
      requestGenerationRef.current += 1;
    };
  }, [loadTournaments]);

  return (
    <section className={styles.root} aria-label="Журнал соревнования">
      <Panel
        title="Журнал соревнования"
        description="События выбранного соревнования и материалы для разбора инцидентов."
        className={styles.panel}
      >
        <div className={styles.toolbar}>
          <div className={styles.selector}>
            <label htmlFor="tournament-journal-select">Соревнование</label>
            <select
              id="tournament-journal-select"
              value={selectedTournamentId ?? ""}
              onChange={(event) =>
                onSelectTournament(event.target.value || null)
              }
              disabled={loadState !== "ready"}
            >
              <option value="">Выберите соревнование</option>
              {tournaments.map((tournament) => (
                <option key={tournament.id} value={tournament.id}>
                  {tournament.name} - {tournament.public_id}
                </option>
              ))}
            </select>
          </div>
        </div>

        {loadState === "error" ? (
          <Message tone="error" title="Список соревнований недоступен">
            {loadError}
          </Message>
        ) : null}
        {loadState === "ready" && !selectedTournament ? (
          <Message
            tone={selectedTournamentId ? "error" : "empty"}
            title={selectedTournamentId ? "Соревнование недоступно" : "Соревнование не выбрано"}
          >
            {selectedTournamentId ? (
              <>
                Выбранное соревнование не найдено. Выберите другое соревнование.
              </>
            ) : tournaments.length === 0 ? (
              "Журнал появится после создания соревнования."
            ) : (
              "Выберите соревнование, чтобы открыть его журнал."
            )}
          </Message>
        ) : null}
      </Panel>

      {selectedTournament ? (
        <TournamentAuditPanel
          tournaments={tournaments}
          selectedTournamentId={selectedTournament.id}
          onSelectTournament={(tournamentId) =>
            onSelectTournament(tournamentId || null)
          }
          onSessionExpired={onSessionExpired}
          showTournamentChooser={false}
        />
      ) : null}
    </section>
  );
};

TournamentJournalSection.displayName = "TournamentJournalSection";
