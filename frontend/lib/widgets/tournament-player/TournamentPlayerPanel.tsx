"use client";

import { useMemo } from "react";

import {
  useParticipantReadiness,
  type ParticipantPlayerView,
  type ParticipantReadyIntent,
  type ParticipantReadyResult,
} from "../../features/tournament-player";
import { Button, Message, Status } from "../../shared/ui";
import { formatGameStatus } from "../../shared/lib";

import styles from "./TournamentPlayerPanel.module.css";

type TournamentPlayerPanelProps = Readonly<{
  view: ParticipantPlayerView | null;
  onReady: (intent: ParticipantReadyIntent) => Promise<ParticipantReadyResult>;
}>;

const statusTone = (state: ParticipantPlayerView["state"]): "neutral" | "info" | "success" | "warning" | "error" => {
  switch (state) {
    case "assigned":
      return "info";
    case "ready":
      return "success";
    case "bye":
      return "warning";
    case "eliminated":
      return "error";
    case "completed":
      return "neutral";
    case "waiting":
      return "warning";
  }
};

const assignmentStatusTone = (
  state: ParticipantPlayerView["assignmentDeliveryState"],
): "neutral" | "success" | "warning" | "error" => {
  switch (state) {
    case "delivered":
      return "success";
    case "superseded":
      return "error";
    case "waiting":
      return "warning";
  }
};

const assignmentStatusLabel = (
  state: ParticipantPlayerView["assignmentDeliveryState"],
): string => {
  switch (state) {
    case "delivered":
      return "Доставлено сервером";
    case "superseded":
      return "Назначение устарело";
    case "waiting":
      return "Ожидается доставка";
  }
};

const checkInLabel = (checkIn: ParticipantPlayerView["checkIn"]): string => {
  switch (checkIn) {
    case "confirmed":
      return "Подтверждена";
    case "pending":
      return "Ожидает подтверждения";
    case "unknown":
      return "Нет данных";
  }
};

const safeTaskHref = (value: string | null): string | null => {
  if (value === null || value.trim().length === 0) {
    return null;
  }

  if (value.startsWith("/")) {
    return value;
  }

  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:" ? value : null;
  } catch {
    return null;
  }
};

const formatDeadline = (value: string | null): string => {
  if (value === null) {
    return "Не задано";
  }

  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) {
    return "Серверное время недоступно";
  }

  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(timestamp);
};

const formatServerTimestamp = (value: string): string => {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) {
    return "Серверное время недоступно";
  }

  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "medium",
  }).format(timestamp);
};

export const TournamentPlayerPanel = ({ onReady, view }: TournamentPlayerPanelProps) => {
  const readiness = useParticipantReadiness({ onReady, view });
  const taskHref = useMemo(
    () => safeTaskHref(view?.assignment?.taskUrl ?? null),
    [view?.assignment?.taskUrl],
  );

  if (view === null) {
    return (
      <section className={styles.panel} aria-labelledby="participant-player-title" data-state="loading">
        <div className={styles.header}>
          <h2 className={styles.title} id="participant-player-title">Загружаем состояние</h2>
          <Status tone="loading">Получаем снимок</Status>
        </div>
        <p className={styles.description}>Сверяем текущий турнир с сервером.</p>
      </section>
    );
  }

  const readinessDisabled =
    !view.readyWindowOpen ||
    readiness.status === "submitting" ||
    readiness.ready ||
    view.state === "bye" ||
    view.state === "eliminated" ||
    view.state === "completed";
  const showReadinessAction = view.state !== "bye" && view.state !== "eliminated" && view.state !== "completed";

  return (
    <section
      className={styles.panel}
      aria-labelledby="participant-player-title"
      data-projection-revision={view.projectionRevision}
      data-ready-key={[
        view.readyKey.tournamentId,
        view.readyKey.projectionRevision,
        view.readyKey.waveId ?? "none",
        view.readyKey.readyWindowId ?? "none",
        view.readyKey.readyWindowRevisionId ?? "none",
        view.readyKey.assignmentAttemptId ?? "none",
      ].join(":")}
      data-state={view.state}
      data-testid="participant-player-panel"
    >
      <div className={styles.header}>
        <h2 className={styles.title} id="participant-player-title">Турнирная позиция</h2>
        <Status tone={statusTone(view.state)} data-testid="participant-player-state">
          {view.stateLabel}
        </Status>
      </div>

      <p className={styles.description}>{view.stateDescription}</p>

      <dl className={styles.details} aria-label="Данные участника">
        <div className={styles.detailRow}>
          <dt>Турнир</dt>
          <dd>{view.tournamentLabel}</dd>
        </div>
        <div className={styles.detailRow}>
          <dt>Стадия</dt>
          <dd>{view.stageLabel}</dd>
        </div>
        <div className={styles.detailRow}>
          <dt>Раунд</dt>
          <dd>{view.roundLabel}</dd>
        </div>
        <div className={styles.detailRow}>
          <dt>Игра</dt>
          <dd>{view.gameNumber === null ? "Номер не опубликован сервером" : `Игра ${view.gameNumber}`}</dd>
        </div>
        <div className={styles.detailRow}>
          <dt>Счет серии</dt>
          <dd>
            {view.seriesScore === null
              ? "Счет не опубликован сервером"
              : `${view.seriesScore.own}:${view.seriesScore.opponent}`}
          </dd>
        </div>
        <div className={styles.detailRow}>
          <dt>Очки</dt>
          <dd>{view.ownPoints === null ? "Пока не опубликованы" : view.ownPoints}</dd>
        </div>
        <div className={styles.detailRow}>
          <dt>Проверка участия</dt>
          <dd>{checkInLabel(view.checkIn)}</dd>
        </div>
        <div className={styles.detailRow}>
          <dt>Соперник</dt>
          <dd>{view.opponentName ?? "Соперник не назначен"}</dd>
        </div>
        <div className={styles.detailRow}>
          <dt>Категория</dt>
          <dd>{view.category ?? "Категория будет объявлена сервером"}</dd>
        </div>
        <div className={styles.detailRow}>
          <dt>Требуемое действие</dt>
          <dd>{view.requiredAction}</dd>
        </div>
      </dl>

      <section
        className={styles.assignment}
        aria-labelledby="participant-assignment-title"
        data-assignment-state={view.assignmentDeliveryState}
        data-testid="participant-assignment-state"
      >
        <div className={styles.assignmentHeader}>
          <div>
            <h3 className={styles.assignmentTitle} id="participant-assignment-title">Задание для игры</h3>
            <p className={styles.assignmentMeta}>
              Контент отображается только после подтвержденной доставки сервером.
            </p>
          </div>
          <Status tone={assignmentStatusTone(view.assignmentDeliveryState)}>
            {assignmentStatusLabel(view.assignmentDeliveryState)}
          </Status>
        </div>

        {view.assignment === null && view.assignmentDeliveryState === "waiting" && (
          <p className={styles.assignmentUnavailable} role="status">
            Задание пока не доставлено. Ожидайте обновления состояния турнира.
          </p>
        )}

        {view.assignment === null && view.assignmentDeliveryState === "superseded" && (
          <p className={styles.assignmentUnavailable} role="status">
            Предыдущее назначение устарело. Новый контент будет показан только после доставки сервером.
          </p>
        )}

        {view.assignment !== null && (
          <>
          <div className={styles.assignmentHeader}>
            <div>
              <p className={styles.assignmentMeta}>
                {view.assignment.category} - {view.assignment.difficulty} - {view.assignment.timeLimitSeconds} с
              </p>
            </div>
            <Status tone={view.assignment.sourceFileAvailable ? "info" : "neutral"}>
              {view.assignment.sourceFileAvailable ? "Файл доступен" : "Текст задания"}
            </Status>
          </div>
          <p className={styles.assignmentName}>{view.assignment.title}</p>
          {taskHref !== null ? (
            <a className={styles.assignmentLink} href={taskHref}>
              Открыть назначенное задание
            </a>
          ) : (
            <span className={styles.assignmentUnavailable} role="status">
              Ссылка на задание появится после подтверждения сервером.
            </span>
          )}
          <p className={styles.assignmentDescription}>{view.assignment.description}</p>
          <dl className={styles.assignmentFacts} aria-label="Параметры назначения">
            <div>
              <dt>Версия задания</dt>
              <dd>{view.assignment.version}</dd>
            </div>
            <div>
              <dt>Доставлено сервером</dt>
              <dd>
                <time dateTime={view.assignment.deliveredAt}>
                  {formatServerTimestamp(view.assignment.deliveredAt)}
                </time>
              </dd>
            </div>
            <div>
              <dt>Дедлайн задания</dt>
              <dd>
                {view.taskDeadlineAt === null
                  ? "Не опубликован сервером"
                  : formatDeadline(view.taskDeadlineAt)}
              </dd>
            </div>
            <div>
              <dt>Состояние игры</dt>
              <dd>{formatGameStatus(view.assignment.gameState)}</dd>
            </div>
            <div>
              <dt>Старт игры</dt>
              <dd>
                {view.assignment.startedAt === null
                  ? "Еще не началась"
                  : (
                    <time dateTime={view.assignment.startedAt}>
                      {formatServerTimestamp(view.assignment.startedAt)}
                    </time>
                  )}
              </dd>
            </div>
          </dl>
          {view.assignment.hints.length > 0 && (
            <details className={styles.assignmentHints}>
              <summary>Подсказки ({view.assignment.hints.length})</summary>
              <ul>
                {view.assignment.hints.map((hint) => <li key={hint}>{hint}</li>)}
              </ul>
            </details>
          )}
          </>
        )}
      </section>

      {showReadinessAction && (
        <div className={styles.readiness}>
          <div className={styles.readinessHeader}>
            <div>
              <h3 className={styles.readinessTitle}>Готовность к раунду</h3>
              <p className={styles.readinessCopy} id="participant-readiness-help">
                {view.readyWindowOpen
                  ? "Подтверждение действует только для текущего окна и назначения."
                  : "Действие откроется только в активном окне готовности."}
              </p>
            </div>
            <span className={styles.deadline} data-ready-window-state={view.readyWindowState ?? "closed"}>
              {view.readyWindowDeadline === null
                ? "Срок не задан"
                : `До ${formatDeadline(view.readyWindowDeadline)}`}
            </span>
          </div>
          <Button
            aria-describedby="participant-readiness-help"
            aria-disabled={readinessDisabled}
            data-testid="participant-ready-button"
            disabled={readinessDisabled}
            loading={readiness.status === "submitting"}
            loadingLabel="Сохраняем"
            onClick={readiness.submitReady}
            variant="primary"
          >
            {readiness.ready ? "Готовность подтверждена" : "Подтвердить готовность"}
          </Button>
          {readiness.message !== null && (
            <Message
              tone={readiness.status === "accepted" ? "success" : "error"}
              title={readiness.status === "accepted" ? "Готово" : "Состояние обновилось"}
            >
              <p>{readiness.message}</p>
            </Message>
          )}
        </div>
      )}

      <p className={styles.revision}>
        Подтвержденная ревизия сервера: {view.projectionRevision}
      </p>
    </section>
  );
};

TournamentPlayerPanel.displayName = "TournamentPlayerPanel";
