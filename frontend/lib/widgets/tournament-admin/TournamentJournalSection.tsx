"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  ApiError,
  operatorApi,
  type Tournament,
} from "../../shared/api";
import { Button, Message, Panel } from "../../shared/ui";

import { TournamentAuditPanel } from "./TournamentAuditPanel";
import styles from "./TournamentJournalSection.module.css";

type LoadState = "loading" | "ready" | "error";

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
  const selectedTournament = useMemo(
    () =>
      tournaments.find((tournament) => tournament.id === selectedTournamentId) ??
      null,
    [selectedTournamentId, tournaments],
  );

  const loadTournaments = useCallback(async (): Promise<void> => {
    controllerRef.current?.abort();
    const controller = new AbortController();
    const requestGeneration = requestGenerationRef.current + 1;
    requestGenerationRef.current = requestGeneration;
    controllerRef.current = controller;
    setLoadState("loading");
    setLoadError(null);
    try {
      const items = await readAllTournaments(controller.signal);
      if (
        controller.signal.aborted ||
        requestGenerationRef.current !== requestGeneration
      ) {
        return;
      }
      setTournaments(items);
      setLoadState("ready");
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
      setLoadState("error");
      setLoadError(problemMessage(error, "Не удалось загрузить список турниров"));
    } finally {
      if (controllerRef.current === controller) {
        controllerRef.current = null;
      }
    }
  }, [onSessionExpired]);

  useEffect(() => {
    void loadTournaments();
    return () => {
      controllerRef.current?.abort();
      requestGenerationRef.current += 1;
    };
  }, [loadTournaments]);

  return (
    <section className={styles.root} aria-label="Журнал турнира">
      <Panel
        title="Журнал турнира"
        description="События выбранного турнира и материалы для разбора инцидентов."
        className={styles.panel}
      >
        <div className={styles.toolbar}>
          <div className={styles.selector}>
            <label htmlFor="tournament-journal-select">Турнир</label>
            <select
              id="tournament-journal-select"
              value={selectedTournamentId ?? ""}
              onChange={(event) =>
                onSelectTournament(event.target.value || null)
              }
              disabled={loadState !== "ready"}
            >
              <option value="">Выберите турнир</option>
              {tournaments.map((tournament) => (
                <option key={tournament.id} value={tournament.id}>
                  {tournament.name} - {tournament.public_id}
                </option>
              ))}
            </select>
          </div>
          <Button
            variant="secondary"
            size="small"
            onClick={() => void loadTournaments()}
            loading={loadState === "loading"}
            loadingLabel="Обновляем"
          >
            Обновить список
          </Button>
        </div>

        {loadState === "error" ? (
          <Message tone="error" title="Список турниров недоступен">
            {loadError}
            <Button
              variant="secondary"
              size="small"
              onClick={() => void loadTournaments()}
            >
              Повторить
            </Button>
          </Message>
        ) : null}
        {loadState === "ready" && !selectedTournament ? (
          <Message
            tone={selectedTournamentId ? "error" : "empty"}
            title={selectedTournamentId ? "Турнир недоступен" : "Турнир не выбран"}
          >
            {selectedTournamentId ? (
              <>
                Выбранный турнир не найден. Обновите список или выберите другой турнир.
              </>
            ) : tournaments.length === 0 ? (
              "Журнал появится после создания турнира."
            ) : (
              "Выберите турнир, чтобы открыть его журнал."
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
