"use client";

import { useState } from "react";

import {
  TournamentLivePanel,
  type TournamentLiveConnectionStatus,
  useServerCountdown,
} from "../../../../lib/features/tournament-live";

const initialServerTimestamp = "2026-09-13T10:00:00Z";
const initialDeadline = "2026-09-13T10:00:30Z";
const staleServerTimestamp = "2026-09-13T09:59:59Z";

export default function Page() {
  const [serverTimestamp, setServerTimestamp] = useState(initialServerTimestamp);
  const [deadline, setDeadline] = useState(initialDeadline);
  const [actionCount, setActionCount] = useState(0);
  const [connectionStatus, setConnectionStatus] = useState<TournamentLiveConnectionStatus>("live");
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const countdown = useServerCountdown({ serverTimestamp, deadline });
  const status = countdown.status === "awaiting_server" ? "awaiting_server" : connectionStatus;

  const selectTheme = (nextTheme: "dark" | "light"): void => {
    document.documentElement.dataset.theme = nextTheme;
    setTheme(nextTheme);
  };

  return (
    <main>
      <div className="theme-switch" role="group" aria-label="Тема интерфейса">
        <button
          aria-pressed={theme === "dark"}
          className="fixture-button"
          onClick={() => selectTheme("dark")}
          type="button"
        >
          Темная
        </button>
        <button
          aria-pressed={theme === "light"}
          className="fixture-button"
          onClick={() => selectTheme("light")}
          type="button"
        >
          Светлая
        </button>
      </div>
      <TournamentLivePanel
        actions={[{
          label: "Отправить команду",
          onClick: () => setActionCount((count) => count + 1),
        }]}
        countdown={countdown}
        onRetry={() => setDeadline(initialDeadline)}
        revision={7}
        role="participant"
        status={status}
        title="Живой турнир"
        tournamentId="00000000-0000-4000-8000-000000000001"
      >
        <p data-testid="action-count">Команд отправлено: {actionCount}</p>
        <button className="fixture-button" type="button" onClick={() => setDeadline(serverTimestamp)}>
          Завершить отсчет
        </button>
        <button
          className="fixture-button"
          type="button"
          onClick={() => {
            setServerTimestamp(staleServerTimestamp);
            setDeadline(initialDeadline);
          }}
        >
          Повторить устаревший ответ
        </button>
        <button className="fixture-button" type="button" onClick={() => setConnectionStatus("stale")}>
          Показать устаревшее состояние
        </button>
      </TournamentLivePanel>
    </main>
  );
}
