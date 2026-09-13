"use client";

import { useEffect, useState } from "react";

import {
  arenaApi,
  readArenaResponse,
  readArenaVoidResponse,
} from "../../../../lib/shared/api/index";
import type { components } from "../../../../lib/shared/api/schema";
import { toPublicArenaView } from "../../../../lib/entities/tournament";

const tournamentId = "00000000-0000-4000-8000-000000000001";
const seriesId = "00000000-0000-4000-8000-000000000010";

type PublicSnapshot = components["schemas"]["PublicRecoverySnapshot"];
type NoShowRequest = components["schemas"]["OperatorNoShowRequest"];
type PublicArenaView = ReturnType<typeof toPublicArenaView>;

type FixtureResult =
  | { state: "idle" | "loading" }
  | { state: "success"; value: PublicArenaView | null }
  | { state: "error"; error: SerializedError };

type SerializedError = {
  name: string;
  message: string;
  status?: number;
  kind?: string;
  retryAfter?: string | null;
  problem?: { status: number; title: string };
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === "object";

const serializeError = (value: unknown): SerializedError => {
  const candidate = isRecord(value) ? value : {};
  const problem = isRecord(candidate.problem)
    && typeof candidate.problem.status === "number"
    && typeof candidate.problem.title === "string"
    ? { status: candidate.problem.status, title: candidate.problem.title }
    : undefined;

  return {
    name: typeof candidate.name === "string" ? candidate.name : "Error",
    message: typeof candidate.message === "string" ? candidate.message : String(value),
    ...(typeof candidate.status === "number" ? { status: candidate.status } : {}),
    ...(typeof candidate.kind === "string" ? { kind: candidate.kind } : {}),
    ...(candidate.retryAfter === null || typeof candidate.retryAfter === "string"
      ? { retryAfter: candidate.retryAfter }
      : {}),
    ...(problem ? { problem } : {}),
  };
};

const publicSnapshotPath = `/api/v1/tournaments/${tournamentId}/snapshot`;

const readPublicSnapshot = (): Promise<PublicSnapshot> =>
  readArenaResponse(
    arenaApi.clients.public.GET(
      "/api/v1/tournaments/{tournament_id}/snapshot",
      { params: { path: { tournament_id: tournamentId } } },
    ),
    "public tournament snapshot",
  );

const noShowBody = (): NoShowRequest => ({
  confirmed: true,
  expected_authority_revision: 4,
  expected_series_state: "active",
  expected_wave_revision_id: "00000000-0000-4000-8000-000000000011",
  expected_window_revision_id: "00000000-0000-4000-8000-000000000012",
  game_result_revision_ids: [],
  reason: "confirmed no-show",
  score_revision_id: "00000000-0000-4000-8000-000000000013",
  series_id: seriesId,
  series_result_revision_id: "00000000-0000-4000-8000-000000000014",
  tournament_id: tournamentId,
  wave_id: "00000000-0000-4000-8000-000000000015",
  window_id: "00000000-0000-4000-8000-000000000016",
});

const runSnapshot = async (): Promise<FixtureResult> => {
  try {
    const response = await readPublicSnapshot();
    return { state: "success", value: toPublicArenaView(response) };
  } catch (error) {
    return { state: "error", error: serializeError(error) };
  }
};

const runNoShow = async (): Promise<FixtureResult> => {
  const body = noShowBody();
  try {
    await readArenaVoidResponse(
      arenaApi.clients.operator.POST(
        "/api/v1/admin/tournaments/{tournament_id}/waves/{wave_id}/no-shows",
        {
          params: {
            path: { tournament_id: tournamentId, wave_id: body.wave_id },
            header: { "Idempotency-Key": "contract-no-show", "X-CSRF-Token": "" },
          },
          body,
        },
      ),
      "operator no-show",
    );
    return { state: "success", value: null };
  } catch (error) {
    return { state: "error", error: serializeError(error) };
  }
};

export default function ArenaApiFixture() {
  const [hydrated, setHydrated] = useState(false);
  const [result, setResult] = useState<FixtureResult>({ state: "idle" });

  useEffect(() => {
    setHydrated(true);
  }, []);

  const invoke = async (operation: () => Promise<FixtureResult>): Promise<void> => {
    setResult({ state: "loading" });
    setResult(await operation());
  };

  return (
    <main>
      <h1>Контракт API Arena</h1>
      <p>Изолированный экран вызывает production adapter из браузерного runtime.</p>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runSnapshot); }}>
        Получить снимок
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runNoShow); }}>
        Выполнить no-show
      </button>
      <p>Snapshot endpoint: {publicSnapshotPath}</p>
      <output aria-label="Результат вызова API" aria-live="polite">
        {result.state === "idle" && "Ожидание"}
        {result.state === "loading" && "Загрузка"}
        {result.state !== "idle" && result.state !== "loading" && JSON.stringify(result)}
      </output>
    </main>
  );
}
