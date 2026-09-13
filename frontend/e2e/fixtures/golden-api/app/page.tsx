"use client";

import { useEffect, useState } from "react";

import {
  createGoldenOperatorCommandIntent,
  createGoldenParticipantCommandIntent,
  getGoldenOperatorState,
  getGoldenParticipantState,
  openGoldenExecution,
  setGoldenParticipantReady,
  startGoldenAttempt,
  submitGoldenFlag,
  type GoldenOperatorResponse,
  type GoldenParticipantMutationResult,
  type GoldenParticipantResponse,
} from "../../../../lib/shared/api";

const tournamentId = "00000000-0000-4000-8000-000000000001";

type FixtureResult =
  | { state: "idle" | "loading" }
  | { state: "success"; value: unknown }
  | { state: "error"; error: { name: string; message: string; status?: number } };

const serializeError = (value: unknown): { name: string; message: string; status?: number } => {
  const candidate = value !== null && typeof value === "object"
    ? value as Record<string, unknown>
    : {};
  return {
    name: typeof candidate.name === "string" ? candidate.name : "Error",
    message: typeof candidate.message === "string" ? candidate.message : String(value),
    ...(typeof candidate.status === "number" ? { status: candidate.status } : {}),
  };
};

const visibleMutationValue = <T,>(result: GoldenParticipantMutationResult<T>): unknown => {
  if (result.status === "success") {
    return result.value;
  }
  if (result.status === "conflict") {
    return {
      status: result.status,
      recovered: result.recovered,
      error: { status: result.error.status, message: result.error.message },
      snapshot: result.snapshot,
    };
  }
  return {
    status: result.status,
    error: { status: result.error.status, message: result.error.message },
    retryAfter: result.retryAfter,
  };
};

export default function GoldenApiFixture() {
  const [hydrated, setHydrated] = useState(false);
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const [result, setResult] = useState<FixtureResult>({ state: "idle" });
  const [operatorState, setOperatorState] = useState<GoldenOperatorResponse | null>(null);
  const [participantState, setParticipantState] = useState<GoldenParticipantResponse | null>(null);

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

  const runOperatorState = async (): Promise<unknown> => {
    const value = await getGoldenOperatorState(tournamentId);
    setOperatorState(value);
    return value;
  };

  const runOpen = async (): Promise<unknown> => {
    const value = await openGoldenExecution(
      tournamentId,
      { expected_projection_revision: 7, expected_runtime_revision: 0 },
      createGoldenOperatorCommandIntent(),
    );
    setOperatorState(value);
    return value;
  };

  const runStart = async (): Promise<unknown> => {
    const group = operatorState?.groups[0];
    if (!group) {
      throw new Error("Сначала откройте Golden");
    }
    const value = await startGoldenAttempt(
      tournamentId,
      group.attempt_id,
      {
        expected_runtime_revision: group.runtime_revision,
        ready_window_id: group.ready_window_id,
      },
      createGoldenOperatorCommandIntent(),
    );
    setOperatorState(value);
    return value;
  };

  const runParticipantState = async (): Promise<unknown> => {
    const value = await getGoldenParticipantState(tournamentId);
    setParticipantState(value);
    return value;
  };

  const runReady = async (): Promise<unknown> => {
    if (!participantState) {
      throw new Error("Сначала прочитайте состояние участника");
    }
    const value = await setGoldenParticipantReady(
      tournamentId,
      {
        attempt_id: participantState.attempt_id,
        expected_runtime_revision: participantState.runtime_revision,
        ready: true,
        ready_window_id: participantState.ready_window_id,
      },
      createGoldenParticipantCommandIntent(),
    );
    if (value.status === "success") {
      setParticipantState(value.value);
    } else if (value.status === "conflict") {
      setParticipantState(value.snapshot);
    }
    return visibleMutationValue(value);
  };

  const runSubmit = async (): Promise<unknown> => {
    if (!participantState) {
      throw new Error("Сначала прочитайте состояние участника");
    }
    const value = await submitGoldenFlag(
      tournamentId,
      {
        attempt_id: participantState.attempt_id,
        expected_runtime_revision: participantState.runtime_revision,
        ready_window_id: participantState.ready_window_id,
        submitted_flag: "flag{fixture-only}",
      },
      createGoldenParticipantCommandIntent(),
    );
    if (value.status === "success") {
      setParticipantState(value.value);
    } else if (value.status === "conflict") {
      setParticipantState(value.snapshot);
    }
    return visibleMutationValue(value);
  };

  const jsonResult = result.state === "error"
    ? result.error
    : result.state === "success"
      ? result.value
      : result.state;

  return (
    <main className={theme}>
      <section className="shell">
        <div className="toolbar">
          <div>
            <h1>Контракт Golden API</h1>
            <p className="lead">Демо MVP проверяет identity, ревизии и server delivery.</p>
          </div>
          <button
            className="theme-toggle"
            type="button"
            aria-label="Переключить тему"
            disabled={!hydrated}
            onClick={() => setTheme((current) => current === "dark" ? "light" : "dark")}
          >
            {theme === "dark" ? "Светлая тема" : "Темная тема"}
          </button>
        </div>
        <div className="grid">
          <section className="card" aria-labelledby="operator-title">
            <h2 id="operator-title">Golden для оператора</h2>
            <div className="actions">
              <button type="button" disabled={!hydrated} onClick={() => { void invoke(runOperatorState); }}>
                Прочитать состояние
              </button>
              <button type="button" disabled={!hydrated} onClick={() => { void invoke(runOpen); }}>
                Открыть Golden
              </button>
              <button type="button" disabled={!hydrated} onClick={() => { void invoke(runStart); }}>
                Запустить попытку
              </button>
            </div>
          </section>
          <section className="card" aria-labelledby="participant-title">
            <h2 id="participant-title">Назначение участника</h2>
            <div className="actions">
              <button type="button" disabled={!hydrated} onClick={() => { void invoke(runParticipantState); }}>
                Прочитать назначение
              </button>
              <button type="button" disabled={!hydrated} onClick={() => { void invoke(runReady); }}>
                Подтвердить готовность
              </button>
              <button type="button" disabled={!hydrated} onClick={() => { void invoke(runSubmit); }}>
                Отправить флаг
              </button>
            </div>
          </section>
        </div>
        <section className="result-card">
          <p className="eyebrow">Ответ сервера</p>
          <pre aria-label="Результат вызова API">{JSON.stringify(jsonResult, null, 2)}</pre>
        </section>
      </section>
      <style jsx>{`
        :global(*) { box-sizing: border-box; }
        :global(html), :global(body) { margin: 0; min-height: 100%; }
        :global(body) { font-family: Inter, ui-sans-serif, system-ui, sans-serif; }
        main { min-height: 100vh; padding: 40px 24px; transition: background .2s ease, color .2s ease; }
        main.dark { --bg: #101419; --panel: #1a2027; --border: #2d3742; --text: #f2f5f7; --muted: #9daab6; --accent: #70b5e8; --accent-ink: #101419; }
        main.light { --bg: #f5f7fa; --panel: #ffffff; --border: #d8e0e8; --text: #18212b; --muted: #58697a; --accent: #175cd3; --accent-ink: #ffffff; }
        main { background: var(--bg); color: var(--text); }
        .shell { width: min(100%, 1040px); margin: 0 auto; }
        .toolbar { display: flex; align-items: flex-start; justify-content: space-between; gap: 24px; margin-bottom: 28px; }
        .eyebrow { color: var(--accent); font-size: 12px; font-weight: 700; letter-spacing: .08em; margin: 0 0 8px; text-transform: uppercase; }
        h1, h2 { margin: 0; letter-spacing: -.02em; }
        h1 { font-size: clamp(28px, 5vw, 44px); }
        h2 { font-size: 20px; }
        .lead { color: var(--muted); margin: 10px 0 0; max-width: 620px; }
        .theme-toggle, button { border: 1px solid var(--border); border-radius: 10px; cursor: pointer; font: inherit; font-weight: 650; }
        .theme-toggle { background: transparent; color: var(--text); padding: 10px 14px; white-space: nowrap; }
        .grid { display: grid; gap: 16px; grid-template-columns: repeat(2, minmax(0, 1fr)); }
        .card, .result-card { background: var(--panel); border: 1px solid var(--border); border-radius: 12px; padding: 20px; }
        .actions { display: flex; flex-wrap: wrap; gap: 10px; margin-top: 20px; }
        .actions button { background: var(--accent); color: var(--accent-ink); padding: 10px 12px; }
        button:disabled { cursor: wait; opacity: .6; }
        .result-card { margin-top: 16px; }
        pre { background: color-mix(in srgb, var(--bg) 85%, var(--panel)); border-radius: 8px; color: var(--text); margin: 12px 0 0; min-height: 170px; overflow: auto; padding: 14px; white-space: pre-wrap; word-break: break-word; }
        @media (max-width: 620px) {
          main { padding: 24px 16px; }
          .toolbar { align-items: stretch; flex-direction: column; }
          .theme-toggle { align-self: flex-start; }
          .grid { grid-template-columns: minmax(0, 1fr); }
        }
      `}</style>
    </main>
  );
}
