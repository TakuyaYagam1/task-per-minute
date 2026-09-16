"use client";

import { useEffect, useState } from "react";

import {
  Button,
  Panel,
  Status,
} from "../../../../lib/shared/ui";
import {
  arenaActionLabel,
  formatArenaCategory,
  formatArenaDateTime,
  formatArenaError,
  formatBestOfScore,
  formatConnectionStatus,
  formatDurationClock,
  formatGameStatus,
  formatParticipantCount,
  formatPoints,
  formatResultReason,
  formatTournamentStatus,
} from "../../../../lib/shared/lib";

type OptionButtonProps = Readonly<{
  label: string;
  selected: boolean;
  onClick: () => void;
}>;

const tournamentStatusOptions = [
  "draft",
  "registration",
  "roster_locked",
  "swiss",
  "technical_pause",
  "completed",
  "cancelled",
] as const;

const gameStatusOptions = [
  "planned",
  "ready",
  "active",
  "paused",
  "completed",
  "void",
  "cancelled",
] as const;

const connectionStatusOptions = [
  "connecting",
  "live",
  "recovering",
  "stale",
  "disconnected",
] as const;

const reasonOptions = [
  "solved",
  "surrender",
  "no_solve",
  "operator_forfeit",
  "tournament_cancelled",
] as const;

const errorOptions = ["unauthorized", "conflict", "rate_limited"] as const;

const actionOptions = [
  "open_registration",
  "start_swiss",
  "open_ready_window",
  "submit",
  "surrender",
  "acknowledge_result",
] as const;

const categoryOptions = ["web", "crypto", "reverse", "forensics", "pwn"] as const;

const OptionButton = ({ label, selected, onClick }: OptionButtonProps) => (
  <Button
    aria-pressed={selected}
    onClick={onClick}
    size="small"
    variant={selected ? "primary" : "ghost"}
  >
    {label}
  </Button>
);

export default function TournamentFormattingFixture() {
  const [hydrated, setHydrated] = useState(false);
  const [tournamentStatus, setTournamentStatus] = useState<string>("draft");
  const [gameStatus, setGameStatus] = useState<string>("active");
  const [connectionStatus, setConnectionStatus] = useState<string>("live");
  const [resultReason, setResultReason] = useState<string>("solved");
  const [errorCode, setErrorCode] = useState<string>("unauthorized");

  useEffect(() => {
    setHydrated(true);
  }, []);

  return (
    <main className="fixture-shell" data-hydrated={hydrated ? "true" : "false"}>
      <header className="fixture-header">
        <p className="fixture-kicker">Публичное представление данных турнира</p>
        <h1>Общий формат турнира</h1>
        <p className="fixture-lede">
          Синтетический стенд с русскими подписями, серверным временем и безопасными запасными сообщениями.
        </p>
      </header>

      <Panel
        title="Международный чемпионат по кибербезопасности: зимний кубок операторов и участников"
        description="Длинное название должно переноситься на узком экране."
        tone="accent"
      >
        <dl className="fixture-metrics">
          <div>
            <dt>Дата и время</dt>
            <dd data-testid="arena-date">{formatArenaDateTime("2026-09-14T12:45:00Z")}</dd>
          </div>
          <div>
            <dt>Лимит задания</dt>
            <dd data-testid="arena-duration">{formatDurationClock(180_000)}</dd>
          </div>
          <div>
            <dt>Серия</dt>
            <dd data-testid="arena-score">{formatBestOfScore(2, 1, 3)}</dd>
          </div>
          <div>
            <dt>Очки</dt>
            <dd data-testid="arena-points">{formatPoints(1_250)}</dd>
          </div>
        </dl>
      </Panel>

      <div className="fixture-grid">
        <Panel title="Участники" description="Русские формы для размеров состава." tone="muted">
          <div className="participant-list" aria-label="Количество участников">
            {[1, 2, 5, 16].map((count) => (
              <span className="participant-count" data-testid="participant-count" key={count}>
                {formatParticipantCount(count)}
              </span>
            ))}
          </div>
        </Panel>

        <Panel title="Категории" description="Названия категорий сохраняют привычное написание." tone="muted">
          <div className="category-list" aria-label="Категории заданий">
            {categoryOptions.map((category) => (
              <span className="category" key={category}>{formatArenaCategory(category)}</span>
            ))}
          </div>
        </Panel>
      </div>

      <Panel title="Состояния турнира" description="Выберите состояние, чтобы проверить подпись.">
        <div className="control-section">
          <div className="current-values">
            <Status data-testid="tournament-status" tone="info">
              Турнир: {formatTournamentStatus(tournamentStatus)}
            </Status>
            <Status data-testid="game-status" tone="info">
              Игра: {formatGameStatus(gameStatus)}
            </Status>
            <Status data-testid="connection-status" tone="info">
              Связь: {formatConnectionStatus(connectionStatus)}
            </Status>
          </div>
          <fieldset className="option-group">
            <legend>Состояние турнира</legend>
            <div className="option-list">
              {tournamentStatusOptions.map((state) => (
                <OptionButton
                  key={state}
                  label={formatTournamentStatus(state)}
                  selected={state === tournamentStatus}
                  onClick={() => setTournamentStatus(state)}
                />
              ))}
              <OptionButton
                label="Проверить неизвестное состояние"
                selected={tournamentStatus === "unknown"}
                onClick={() => setTournamentStatus("unknown")}
              />
            </div>
          </fieldset>
          <fieldset className="option-group">
            <legend>Состояние игры</legend>
            <div className="option-list">
              {gameStatusOptions.map((state) => (
                <OptionButton
                  key={state}
                  label={formatGameStatus(state)}
                  selected={state === gameStatus}
                  onClick={() => setGameStatus(state)}
                />
              ))}
              <OptionButton
                label="Проверить неизвестную игру"
                selected={gameStatus === "unknown"}
                onClick={() => setGameStatus("unknown")}
              />
            </div>
          </fieldset>
          <fieldset className="option-group">
            <legend>Состояние соединения</legend>
            <div className="option-list">
              {connectionStatusOptions.map((state) => (
                <OptionButton
                  key={state}
                  label={formatConnectionStatus(state)}
                  selected={state === connectionStatus}
                  onClick={() => setConnectionStatus(state)}
                />
              ))}
              <OptionButton
                label="Проверить неизвестную связь"
                selected={connectionStatus === "unknown"}
                onClick={() => setConnectionStatus("unknown")}
              />
            </div>
          </fieldset>
        </div>
      </Panel>

      <div className="fixture-grid">
        <Panel title="Причины результата" description="Подпись не раскрывает внутренний код.">
          <div className="current-values feedback-status">
            <Status data-testid="result-reason" tone="success">
              {formatResultReason(resultReason)}
            </Status>
          </div>
          <div className="option-list" role="group" aria-label="Причина результата">
            {reasonOptions.map((reason) => (
              <OptionButton
                key={reason}
                label={formatResultReason(reason)}
                selected={reason === resultReason}
                onClick={() => setResultReason(reason)}
              />
            ))}
            <OptionButton
              label="Проверить неизвестную причину"
              selected={resultReason === "unknown"}
              onClick={() => setResultReason("unknown")}
            />
          </div>
        </Panel>

        <Panel title="Ошибки сервера" description="Для неизвестного кода показывается общее сообщение.">
          <div className="current-values feedback-status">
            <Status data-testid="server-error" tone="error">
              {formatArenaError(errorCode)}
            </Status>
          </div>
          <div className="option-list" role="group" aria-label="Ошибка сервера">
            {errorOptions.map((error) => (
              <OptionButton
                key={error}
                label={formatArenaError(error)}
                selected={error === errorCode}
                onClick={() => setErrorCode(error)}
              />
            ))}
            <OptionButton
              label="Проверить неизвестную ошибку"
              selected={errorCode === "unknown"}
              onClick={() => setErrorCode("unknown")}
            />
          </div>
        </Panel>
      </div>

      <Panel title="Действия" description="Основные подписи действий остаются понятными на русском.">
        <div className="action-list" aria-label="Действия турнира">
          {actionOptions.map((action) => (
            <span className="action-label" key={action}>{arenaActionLabel(action)}</span>
          ))}
        </div>
      </Panel>
    </main>
  );
}
