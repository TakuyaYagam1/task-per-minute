"use client";

import { useEffect, useState } from "react";

import {
  getPublicBracket,
  getPublicScoreboard,
  getPublicTournament,
  getPublicTournamentProjection,
  type PublicBracketResponse,
  type PublicScoreboardResponse,
  type PublicTournamentResponse,
} from "../../../../lib/shared/api";

const tournamentId = "00000000-0000-4000-8000-000000000001";

type PublicState = {
  tournament: PublicTournamentResponse;
  scoreboard: PublicScoreboardResponse;
  bracket: PublicBracketResponse;
};

type SerializedError = {
  name: string;
  message: string;
  status?: number;
  kind?: string;
};

type FixtureResult =
  | { state: "idle" | "loading" }
  | { state: "success"; value: unknown }
  | { state: "error"; error: SerializedError };

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object";

const serializeError = (value: unknown): SerializedError => {
  const candidate = isRecord(value) ? value : {};
  return {
    name: typeof candidate.name === "string" ? candidate.name : "Error",
    message: typeof candidate.message === "string" ? candidate.message : String(value),
    ...(typeof candidate.status === "number" ? { status: candidate.status } : {}),
    ...(typeof candidate.kind === "string" ? { kind: candidate.kind } : {}),
  };
};

export default function PublicApiFixture() {
  const [hydrated, setHydrated] = useState(false);
  const [result, setResult] = useState<FixtureResult>({ state: "idle" });
  const [publicState, setPublicState] = useState<PublicState | null>(null);

  useEffect(() => {
    setHydrated(true);
  }, []);

  const invoke = async (operation: () => Promise<unknown>): Promise<void> => {
    setResult({ state: "loading" });
    try {
      setResult({ state: "success", value: await operation() });
    } catch (error) {
      setResult({ state: "error", error: serializeError(error) });
    }
  };

  const loadPublicState = async (): Promise<unknown> => {
    const projection = await getPublicTournamentProjection(tournamentId);
    const nextState: PublicState = {
      tournament: projection.tournament,
      scoreboard: projection.scoreboard,
      bracket: projection.bracket,
    };
    setPublicState(nextState);
    return {
      projectionRevision: projection.projectionRevision,
      rosterSize: projection.tournament.roster_size,
      scoreboardEntries: projection.scoreboard.entries.length,
      bracketMatches: projection.bracket.matches.length,
    };
  };

  const loadTournament = async (): Promise<unknown> => {
    const tournament = await getPublicTournament(tournamentId);
    setPublicState((current) => current === null
      ? null
      : { ...current, tournament });
    return {
      projectionRevision: tournament.projection_revision,
      rosterSize: tournament.roster_size,
    };
  };

  const loadScoreboard = async (): Promise<unknown> => {
    const scoreboard = await getPublicScoreboard(tournamentId);
    setPublicState((current) => current === null
      ? null
      : { ...current, scoreboard });
    return {
      projectionRevision: scoreboard.projection_revision,
      entries: scoreboard.entries.length,
    };
  };

  const loadBracket = async (): Promise<unknown> => {
    const bracket = await getPublicBracket(tournamentId);
    setPublicState((current) => current === null
      ? null
      : { ...current, bracket });
    return {
      projectionRevision: bracket.projection_revision,
      matches: bracket.matches.length,
    };
  };

  return (
    <main>
      <h1>Контракт публичного API</h1>
      <p>Анонимный экран читает только allowlist DTO турнира, таблицы и сетки.</p>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(loadPublicState); }}>
        Получить публичное состояние
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(loadTournament); }}>
        Получить турнир
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(loadScoreboard); }}>
        Получить таблицу
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(loadBracket); }}>
        Получить сетку
      </button>
      <dl>
        <dt>Ревизия сервера</dt>
        <dd aria-label="Ревизия сервера">{publicState?.tournament.projection_revision ?? "нет"}</dd>
        <dt>Размер ростера</dt>
        <dd aria-label="Размер ростера">{publicState?.tournament.roster_size ?? "нет"}</dd>
        <dt>Записей таблицы</dt>
        <dd aria-label="Записей таблицы">{publicState?.scoreboard.entries.length ?? "нет"}</dd>
        <dt>Матчей сетки</dt>
        <dd aria-label="Матчей сетки">{publicState?.bracket.matches.length ?? "нет"}</dd>
      </dl>
      <output aria-label="Результат вызова API" aria-live="polite">
        {result.state === "idle" && "Ожидание"}
        {result.state === "loading" && "Загрузка"}
        {result.state !== "idle" && result.state !== "loading" && JSON.stringify(result)}
      </output>
    </main>
  );
}
