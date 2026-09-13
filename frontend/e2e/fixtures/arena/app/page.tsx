"use client";

import { useEffect, useState } from "react";

import {
  arenaApi,
  createParticipantCommandIntent,
  participantApi,
  readArenaResponse,
  readArenaVoidResponse,
  type ParticipantMutationResult,
} from "../../../../lib/shared/api/index";
import type { components } from "../../../../lib/shared/api/schema";
import { toPublicArenaView } from "../../../../lib/entities/tournament";

const tournamentId = "00000000-0000-4000-8000-000000000001";
const seriesId = "00000000-0000-4000-8000-000000000010";
const waveId = "00000000-0000-4000-8000-000000000015";
const assignmentId = "00000000-0000-4000-8000-000000000017";
const attemptId = "00000000-0000-4000-8000-000000000018";
const participantId = "00000000-0000-4000-8000-000000000019";
const taskId = "00000000-0000-4000-8000-000000000020";
const taskSnapshotId = "00000000-0000-4000-8000-000000000021";
const receiptId = "00000000-0000-4000-8000-000000000022";
const readyWindowId = "00000000-0000-4000-8000-000000000023";

type PublicSnapshot = components["schemas"]["PublicRecoverySnapshot"];
type NoShowRequest = components["schemas"]["OperatorNoShowRequest"];
type ParticipantReadyRequest = components["schemas"]["ParticipantReadyRequest"];
type ParticipantLobbyResponse = components["schemas"]["ParticipantLobbyResponse"];
type ParticipantAssignmentResponse = components["schemas"]["ParticipantAssignmentResponse"];
type ParticipantRecoverySnapshot = components["schemas"]["ParticipantRecoverySnapshot"];
type ParticipantReadyEvent = components["schemas"]["ReadinessEvent"];
type PublicArenaView = ReturnType<typeof toPublicArenaView>;

type FixtureResult =
  | { state: "idle" | "loading" }
  | { state: "success"; value: unknown }
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
const participantLobbyPath = `/api/v1/tournaments/${tournamentId}/participant/lobby`;
const participantAssignmentPath = `/api/v1/tournaments/${tournamentId}/participant/assignments/${assignmentId}`;
const participantSnapshotPath = `/api/v1/tournaments/${tournamentId}/participant/snapshot`;
const participantReadyPath = `/api/v1/tournaments/${tournamentId}/participant/waves/${waveId}/ready`;
const adminTasksPath = "/api/v1/admin/tasks";

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

const participantReadyBody = (): ParticipantReadyRequest => ({
  expected_projection_revision: 4,
  ready: true,
});

const participantLobby = (): ParticipantLobbyResponse => ({
  tournament_id: tournamentId,
  state: "swiss",
  projection_revision: 4,
  roster_locked: true,
  series: [{
    series_id: seriesId,
    state: "active",
    format: "bo3",
    opponent_display_name: "Боб",
    wave_id: waveId,
  }],
});

const participantAssignment = (): ParticipantAssignmentResponse => ({
  tournament_id: tournamentId,
  projection_revision: 4,
  assignment: {
    id: assignmentId,
    attempt_id: attemptId,
    active_snapshot: {
      snapshot_id: taskSnapshotId,
      task_id: taskId,
      version: 2,
      kind: "normal",
      title: "Проверка контракта",
      description: "Стабильное описание задания",
      category: "web",
      difficulty: "medium",
      time_limit: 900,
      hints: ["Проверьте URL"],
      task_url: null,
      source_file_available: false,
    },
    undisclosed_reserve_count: 1,
    receipt: {
      id: receiptId,
      assignment_id: assignmentId,
      attempt_id: attemptId,
      participant_id: participantId,
      snapshot_id: taskSnapshotId,
      task_id: taskId,
      delivered_at: "2026-09-08T10:01:00Z",
    },
  },
});

const participantRecoverySnapshot = (): ParticipantRecoverySnapshot => ({
  tournament_id: tournamentId,
  projection_revision: 5,
  lobby: {
    ...participantLobby(),
    projection_revision: 5,
  },
  series: null,
  wave: null,
  draft: null,
  assignment: null,
  next_cursor: {
    projection_revision: 5,
    participant_view_revision: 3,
    event_sequence: 8,
  },
});

const participantReadyEvent = (commandId: string): ParticipantReadyEvent => ({
  command_id: commandId,
  wave_id: waveId,
  window_id: readyWindowId,
  participant_id: participantId,
  type: "ready",
  occurred_at: "2026-09-08T10:02:00Z",
});

const participantResultForFixture = <T,>(
  result: ParticipantMutationResult<T>,
): unknown => {
  if (result.status === "success") {
    return result.value;
  }
  if (result.status === "conflict") {
    return {
      status: result.status,
      recovered: result.recovered,
      snapshot: result.snapshot,
    };
  }
  return {
    status: result.status,
    retryAfter: result.retryAfter,
  };
};

const runParticipantLobbyAndAssignment = async (): Promise<FixtureResult> => {
  try {
    const lobby = await participantApi.getLobby(tournamentId);
    const assignment = await participantApi.getAssignment(tournamentId, assignmentId);
    return { state: "success", value: { lobby, assignment } };
  } catch (error) {
    return { state: "error", error: serializeError(error) };
  }
};

const runParticipantLobby = async (): Promise<FixtureResult> => {
  try {
    return { state: "success", value: await participantApi.getLobby(tournamentId) };
  } catch (error) {
    return { state: "error", error: serializeError(error) };
  }
};

const runParticipantReady = async (): Promise<FixtureResult> => {
  const body = participantReadyBody();
  try {
    const result = await participantApi.ready(
      tournamentId,
      waveId,
      body,
      createParticipantCommandIntent(),
    );
    return { state: "success", value: result.status === "success" ? null : participantResultForFixture(result) };
  } catch (error) {
    return { state: "error", error: serializeError(error) };
  }
};

const runParticipantReadyTwice = async (): Promise<FixtureResult> => {
  const body = participantReadyBody();
  const intent = createParticipantCommandIntent();
  try {
    const first = await participantApi.ready(tournamentId, waveId, body, intent);
    const second = await participantApi.ready(tournamentId, waveId, body, intent);
    return {
      state: "success",
      value: {
        first: participantResultForFixture(first),
        second: participantResultForFixture(second),
      },
    };
  } catch (error) {
    return { state: "error", error: serializeError(error) };
  }
};

const runParticipantReadyWithNewIntent = async (): Promise<FixtureResult> => {
  const body = participantReadyBody();
  try {
    const first = await participantApi.ready(
      tournamentId,
      waveId,
      body,
      createParticipantCommandIntent(),
    );
    const second = await participantApi.ready(
      tournamentId,
      waveId,
      body,
      createParticipantCommandIntent(),
    );
    return {
      state: "success",
      value: {
        first: participantResultForFixture(first),
        second: participantResultForFixture(second),
      },
    };
  } catch (error) {
    return { state: "error", error: serializeError(error) };
  }
};

const runParticipantConflict = async (): Promise<FixtureResult> => {
  try {
    const result = await participantApi.ready(
      tournamentId,
      waveId,
      participantReadyBody(),
      createParticipantCommandIntent(),
    );
    return { state: "success", value: participantResultForFixture(result) };
  } catch (error) {
    return { state: "error", error: serializeError(error) };
  }
};

const runParticipantRateLimited = async (): Promise<FixtureResult> => {
  try {
    const result = await participantApi.ready(
      tournamentId,
      waveId,
      participantReadyBody(),
      createParticipantCommandIntent(),
    );
    return { state: "success", value: participantResultForFixture(result) };
  } catch (error) {
    return { state: "error", error: serializeError(error) };
  }
};

const runConcurrentAdminTasks = async (): Promise<FixtureResult> => {
  try {
    await Promise.all([
      readArenaResponse(
        arenaApi.clients.operator.GET("/api/v1/admin/tasks"),
        "operator tasks first",
      ),
      readArenaResponse(
        arenaApi.clients.operator.GET("/api/v1/admin/tasks"),
        "operator tasks second",
      ),
    ]);
    return { state: "success", value: null };
  } catch (error) {
    return { state: "error", error: serializeError(error) };
  }
};

const runOperatorTask = async (): Promise<FixtureResult> => {
  try {
    await readArenaResponse(
      arenaApi.clients.operator.GET("/api/v1/admin/tasks"),
      "operator tasks",
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
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runParticipantReady); }}>
        Отметить готовность участника
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runParticipantLobbyAndAssignment); }}>
        Получить лобби и назначение
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runParticipantLobby); }}>
        Получить лобби участника
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runParticipantReadyTwice); }}>
        Повторить ready intent
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runParticipantReadyWithNewIntent); }}>
        Создать новый ready intent
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runParticipantConflict); }}>
        Проверить конфликт участника
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runParticipantRateLimited); }}>
        Проверить rate limit участника
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runConcurrentAdminTasks); }}>
        Проверить параллельный admin refresh
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runOperatorTask); }}>
        Проверить admin 403
      </button>
      <p>Snapshot endpoint: {publicSnapshotPath}</p>
      <p>Participant lobby endpoint: {participantLobbyPath}</p>
      <p>Participant assignment endpoint: {participantAssignmentPath}</p>
      <p>Participant recovery endpoint: {participantSnapshotPath}</p>
      <p>Participant ready endpoint: {participantReadyPath}</p>
      <p>Admin tasks endpoint: {adminTasksPath}</p>
      <output aria-label="Результат вызова API" aria-live="polite">
        {result.state === "idle" && "Ожидание"}
        {result.state === "loading" && "Загрузка"}
        {result.state !== "idle" && result.state !== "loading" && JSON.stringify(result)}
      </output>
    </main>
  );
}
