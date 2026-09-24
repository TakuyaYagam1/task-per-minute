"use client";

import { useEffect, useMemo, useState, type FormEvent } from "react";

import {
  useParticipantReadiness,
  useParticipantDraft,
  useParticipantSourceFile,
  useParticipantSubmission,
  useParticipantSurrender,
  type ParticipantSubmissionIntent,
  type ParticipantSubmissionResult,
  type ParticipantDraftIntent,
  type ParticipantDraftResult,
  type ParticipantSurrenderIntent,
  type ParticipantSurrenderResult,
  type ParticipantPlayerView,
  type ParticipantReadyIntent,
  type ParticipantReadyResult,
} from "../../features/tournament-player";
import { Button, Message, Status } from "../../shared/ui";
import {
  formatCategory,
  formatGameStatus,
  formatResultReason,
  formatSeriesFormat,
  formatSeriesState,
  getSafeTaskHref,
} from "../../shared/lib";

import styles from "./TournamentPlayerPanel.module.css";

type TournamentPlayerPanelProps = Readonly<{
  onSubmit: (intent: ParticipantSubmissionIntent) => Promise<ParticipantSubmissionResult>;
  onSurrender: (intent: ParticipantSurrenderIntent) => Promise<ParticipantSurrenderResult>;
  onDraft: (intent: ParticipantDraftIntent) => Promise<ParticipantDraftResult>;
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

const submissionTone = (
  status: ReturnType<typeof useParticipantSubmission>["status"],
): "info" | "success" | "warning" | "error" | "loading" => {
  switch (status) {
    case "pending":
      return "loading";
    case "accepted":
      return "success";
    case "incorrect":
    case "empty":
      return "warning";
    case "conflict":
    case "rate_limited":
    case "error":
      return "error";
    case "idle":
      return "info";
  }
};

const surrenderTone = (
  status: ReturnType<typeof useParticipantSurrender>["status"],
): "success" | "error" | "loading" | "warning" => {
  switch (status) {
    case "accepted":
      return "success";
    case "pending":
      return "loading";
    case "confirming":
      return "warning";
    case "conflict":
    case "rate_limited":
    case "error":
      return "error";
    case "idle":
      return "warning";
  }
};

const draftStatusTone = (
  status: ReturnType<typeof useParticipantDraft>["status"],
): "info" | "success" | "warning" | "error" | "loading" => {
  switch (status) {
    case "submitting":
      return "loading";
    case "accepted":
      return "success";
    case "conflict":
    case "error":
      return "error";
    case "rate_limited":
      return "warning";
    case "idle":
      return "info";
  }
};

const draftStateLabel = (state: NonNullable<ParticipantPlayerView["draft"]>["state"]): string => {
  switch (state) {
    case "active":
      return "Активен";
    case "paused":
      return "На паузе";
    case "recovery_required":
      return "Ожидает восстановления";
    case "completed":
      return "Завершен";
    case "superseded":
      return "Заменен сервером";
  }
};

const draftActionLabel = (
  action: NonNullable<ParticipantPlayerView["draft"]>["currentAction"],
): string => {
  switch (action) {
    case "ban":
      return "Бан категории";
    case "pick":
      return "Выбор категории";
    case null:
      return "Действие не требуется";
  }
};

const pauseSourceLabel = (source: ParticipantPlayerView["pause"]["source"]): string => {
  switch (source) {
    case "wave":
      return "Раунд приостановлен";
    case "series":
      return "Серия на технической паузе";
    case "game":
      return "Игра приостановлена";
    case "draft":
      return "Драфт приостановлен";
    case null:
      return "Пауза не активна";
  }
};

const pauseReasonLabel = (reason: ParticipantPlayerView["pause"]["reason"]): string => {
  switch (reason) {
    case "operator":
      return "Операторская пауза";
    case "disconnect":
      return "Отключение участника";
    case "platform":
      return "Проблема платформы";
    case "execution_epoch":
      return "Перезапуск выполнения";
    case null:
      return "Причина не опубликована сервером";
  }
};

const presenceStateLabel = (
  state: NonNullable<ParticipantPlayerView["runtime"]>["presence"][number]["state"],
): string => state === "connected" ? "На связи" : "Связь потеряна";

const reconnectStateLabel = (
  state: NonNullable<ParticipantPlayerView["runtime"]>["reconnect"][number]["state"],
): string => {
  switch (state) {
    case "open":
      return "Ожидает переподключения";
    case "reconnected":
      return "Переподключение подтверждено";
    case "expired":
      return "Срок истек";
    case "cancelled":
      return "Отменено сервером";
  }
};

const participantLabelFor = (
  participantId: string,
  view: ParticipantPlayerView,
): string => participantId === view.participantId ? "Вы" : view.opponentName ?? "Соперник";

const runtimeStatusTone = (
  view: ParticipantPlayerView,
): "info" | "success" | "warning" => {
  if (view.pause.active) {
    return "warning";
  }

  return view.officialOutcome === null ? "info" : "success";
};

const runtimeStatusLabel = (view: ParticipantPlayerView): string => {
  if (view.pause.active) {
    return "Пауза сервера";
  }

  return view.officialOutcome === null ? "Синхронизировано" : "Итог зафиксирован";
};

const officialOutcomeStateLabel = (
  outcome: NonNullable<ParticipantPlayerView["officialOutcome"]>,
): string => outcome.subject === "game"
  ? formatGameStatus(outcome.state)
  : formatSeriesState(outcome.state);

const officialOutcomeSubjectLabel = (
  outcome: NonNullable<ParticipantPlayerView["officialOutcome"]>,
): string => outcome.subject === "game" ? "Игры" : "Серии";

const officialOutcomeReasonLabel = (
  outcome: NonNullable<ParticipantPlayerView["officialOutcome"]>,
): string => outcome.reason === null
  ? "Причина не опубликована сервером"
  : formatResultReason(outcome.reason);

const officialWinnerLabel = (
  outcome: NonNullable<ParticipantPlayerView["officialOutcome"]>,
  participantId: string,
  opponentName: string | null,
): string => outcome.winnerId === null
  ? "Победитель не опубликован сервером"
  : outcome.winnerId === participantId
    ? "Вы"
    : opponentName ?? "Соперник";

const deadlineLabel = (value: string | null, suppressed: boolean): string => {
  if (suppressed) {
    return "Приостановлен сервером";
  }

  return formatDeadline(value);
};

const seriesResultTone = (
  state: NonNullable<ParticipantPlayerView["seriesResult"]>["state"],
): "info" | "success" | "warning" | "error" => {
  switch (state) {
    case "completed":
      return "success";
    case "cancelled":
      return "error";
    case "technical_pause":
    case "replay_required":
      return "warning";
    case "active":
    case "draft":
    case "locked":
    case "planned":
    case "ready":
      return "info";
  }
};

const resultRevisionLabel = (value: string | null): string => value ?? "Не опубликована";

export const TournamentPlayerPanel = ({
  onDraft,
  onReady,
  onSubmit,
  onSurrender,
  view,
}: TournamentPlayerPanelProps) => {
  const draft = useParticipantDraft({ onDraft, view });
  const readiness = useParticipantReadiness({ onReady, view });
  const sourceFile = useParticipantSourceFile(view);
  const submission = useParticipantSubmission({ onSubmit, view });
  const surrender = useParticipantSurrender({ onSurrender, view });
  const [hintsOpen, setHintsOpen] = useState(false);
  const [submittedFlag, setSubmittedFlag] = useState("");
  const taskHref = useMemo(
    () => getSafeTaskHref(view?.assignment?.taskUrl ?? null),
    [view?.assignment?.taskUrl],
  );

  useEffect(() => {
    setHintsOpen(false);
    setSubmittedFlag("");
  }, [view?.assignment?.assignmentId, view?.projectionRevision]);

  const submitAnswer = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const value = submittedFlag;
    setSubmittedFlag("");
    submission.submit(value);
  };

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
    view.pause.active ||
    readiness.status === "submitting" ||
    readiness.ready ||
    view.state === "bye" ||
    view.state === "eliminated" ||
    view.state === "completed";
  const terminalParticipant = view.state === "eliminated" || view.state === "completed";
  const showReadinessAction =
    !terminalParticipant &&
    view.state !== "bye";
  const showDraft = view.draft !== null && (view.draft.format === "bo1" || view.draft.format === "bo3");
  const submissionHasError =
    submission.status === "empty" ||
    submission.status === "incorrect" ||
    submission.status === "conflict" ||
    submission.status === "rate_limited" ||
    submission.status === "error";
  const submissionDescribedBy = submission.message === null
    ? "participant-submission-help"
    : "participant-submission-help participant-submission-status";

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

      {(view.pause.active ||
        view.officialOutcome !== null ||
        (view.runtime?.presence.length ?? 0) > 0 ||
        (view.runtime?.reconnect.length ?? 0) > 0) && (
        <section
          aria-labelledby="participant-runtime-title"
          className={styles.runtimeStatus}
          data-deadlines-suppressed={view.pause.deadlinesSuppressed ? "true" : "false"}
          data-game-revision={view.runtime?.gameRevision ?? ""}
          data-game-state={view.runtime?.gameState ?? view.assignment?.gameState ?? "unknown"}
          data-paused={view.pause.active ? "true" : "false"}
          data-pause-reason={view.pause.reason ?? "none"}
          data-pause-state={view.pause.state ?? "none"}
          data-testid="participant-runtime-status"
        >
          <div className={styles.runtimeHeader}>
            <div>
              <h3 className={styles.runtimeTitle} id="participant-runtime-title">
                Состояние матча
              </h3>
              <p className={styles.runtimeCopy}>
                Состояние и доступность действий определяются последним снимком сервера.
              </p>
            </div>
            <Status tone={runtimeStatusTone(view)}>{runtimeStatusLabel(view)}</Status>
          </div>

          {view.pause.active && (
            <div className={styles.runtimePause} role="status">
              <p>Матч приостановлен сервером. Локальное продолжение недоступно.</p>
              <dl className={styles.runtimeFacts} aria-label="Состояние паузы">
                <div>
                  <dt>Серверное состояние</dt>
                  <dd>{pauseSourceLabel(view.pause.source)}</dd>
                </div>
                <div>
                  <dt>Пауза с</dt>
                  <dd>
                    {view.pause.pausedAt === null
                      ? "Время не опубликовано сервером"
                      : (
                        <time dateTime={view.pause.pausedAt}>
                          {formatServerTimestamp(view.pause.pausedAt)}
                        </time>
                    )}
                  </dd>
                </div>
                <div>
                  <dt>Причина</dt>
                  <dd>{pauseReasonLabel(view.pause.reason)}</dd>
                </div>
                <div>
                  <dt>Дедлайны</dt>
                  <dd>
                    {view.pause.deadlinesSuppressed
                      ? "Подавлены сервером"
                      : "Не подавлены сервером"}
                  </dd>
                </div>
                <div>
                  <dt>Переподключение до</dt>
                  <dd>
                    {view.pause.reconnectDeadline === null
                      ? "Срок не опубликован сервером"
                      : formatDeadline(view.pause.reconnectDeadline)}
                  </dd>
                </div>
              </dl>
              <p className={styles.runtimeHint}>
                Следующее состояние и момент продолжения будут опубликованы сервером.
              </p>
            </div>
          )}

          {view.runtime !== null && view.runtime.presence.length > 0 && (
            <div className={styles.runtimeParticipants}>
              <h4 className={styles.runtimeSubtitle}>Присутствие участников</h4>
              <ul className={styles.runtimeList} data-testid="participant-runtime-presence">
                {view.runtime.presence.map((presence) => (
                  <li
                    data-state={presence.state}
                    data-testid={`participant-runtime-presence-${presence.participantId}`}
                    key={presence.participantId}
                  >
                    <div>
                      <strong>{participantLabelFor(presence.participantId, view)}</strong>
                      <span>{presenceStateLabel(presence.state)}</span>
                    </div>
                    <small>
                      Обновлено {formatServerTimestamp(presence.updatedAt)}
                    </small>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {view.runtime !== null && view.runtime.reconnect.length > 0 && (
            <div className={styles.runtimeParticipants}>
              <h4 className={styles.runtimeSubtitle}>Переподключение</h4>
              <ul className={styles.runtimeList} data-testid="participant-runtime-reconnect">
                {view.runtime.reconnect.map((reconnect) => (
                  <li
                    data-state={reconnect.state}
                    data-testid={`participant-runtime-reconnect-${reconnect.id}`}
                    key={reconnect.id}
                  >
                    <div>
                      <strong>{participantLabelFor(reconnect.participantId, view)}</strong>
                      <span>{reconnectStateLabel(reconnect.state)}</span>
                    </div>
                    <small>
                      До {formatDeadline(reconnect.deadline)}
                      {reconnect.closedAt === null
                        ? ""
                        : `; закрыто ${formatServerTimestamp(reconnect.closedAt)}`}
                    </small>
                  </li>
                ))}
              </ul>
              <p className={styles.runtimeHint}>
                Состояние переподключения сохраняется сервером. Локальное возобновление не создается.
              </p>
            </div>
          )}

          {!view.pause.active && view.officialOutcome !== null && (
            <div className={styles.runtimeOutcome} role="status">
              <p>Официальный итог опубликован сервером.</p>
              <dl className={styles.runtimeFacts} aria-label="Официальный итог">
                <div>
                  <dt>Объект</dt>
                  <dd>{officialOutcomeSubjectLabel(view.officialOutcome)}</dd>
                </div>
                <div>
                  <dt>Состояние</dt>
                  <dd>{officialOutcomeStateLabel(view.officialOutcome)}</dd>
                </div>
                <div>
                  <dt>Причина</dt>
                  <dd>{officialOutcomeReasonLabel(view.officialOutcome)}</dd>
                </div>
                <div>
                  <dt>Победитель</dt>
                  <dd>
                    {officialWinnerLabel(
                      view.officialOutcome,
                      view.participantId,
                      view.opponentName,
                    )}
                  </dd>
                </div>
                <div>
                  <dt>Ревизия результата</dt>
                  <dd>{view.officialOutcome.revisionId}</dd>
                </div>
              </dl>
            </div>
          )}
        </section>
      )}

      {view.seriesResult !== null && (
        <section
          aria-labelledby="participant-series-result-title"
          className={styles.seriesResult}
          data-result-revision={view.seriesResult.currentResultRevisionId ?? "none"}
          data-score-revision={view.seriesResult.currentScoreRevisionId ?? "none"}
          data-series-state={view.seriesResult.state}
          data-testid="participant-series-result"
        >
          <div className={styles.seriesResultHeader}>
            <div>
              <p className={styles.seriesResultEyebrow}>Официальный результат</p>
              <h3 className={styles.seriesResultTitle} id="participant-series-result-title">
                Серия {formatSeriesFormat(view.seriesResult.format)}
              </h3>
            </div>
            <Status tone={seriesResultTone(view.seriesResult.state)}>
              {formatSeriesState(view.seriesResult.state)}
            </Status>
          </div>

          <div className={styles.seriesScore} aria-label="Текущий счет серии">
            <span>Вы</span>
            <strong data-testid="participant-series-score">
              {view.seriesResult.score.own}:{view.seriesResult.score.opponent}
            </strong>
            <span>{view.opponentName ?? "Соперник"}</span>
          </div>

          <dl className={styles.seriesFacts} aria-label="Ревизии официального результата">
            <div>
              <dt>Победитель</dt>
              <dd>
                {view.seriesResult.winnerId === null
                  ? "Не опубликован"
                  : participantLabelFor(view.seriesResult.winnerId, view)}
              </dd>
            </div>
            <div>
              <dt>Ревизия счета</dt>
              <dd>{resultRevisionLabel(view.seriesResult.currentScoreRevisionId)}</dd>
            </div>
            <div>
              <dt>Ревизия результата</dt>
              <dd>{resultRevisionLabel(view.seriesResult.currentResultRevisionId)}</dd>
            </div>
            <div>
              <dt>Следующий этап</dt>
              <dd>{view.requiredAction}</dd>
            </div>
          </dl>

          <div className={styles.seriesHistory}>
            <h4>История игр</h4>
            {view.seriesResult.games.length === 0 ? (
              <p className={styles.seriesEmpty} data-testid="participant-series-empty-history">
                Сыгранных игр нет. Сервер не публиковал назначение или победителя.
              </p>
            ) : (
              <ol className={styles.seriesGames}>
                {view.seriesResult.games.map((game) => (
                  <li
                    data-game-state={game.state}
                    data-testid={`participant-series-game-${game.position}-${game.attemptNo}`}
                    key={game.gameId}
                  >
                    <div className={styles.seriesGameHeader}>
                      <strong>
                        Игра {game.position}{game.attemptNo > 1 ? `, попытка ${game.attemptNo}` : ""}
                      </strong>
                      <Status tone={game.state === "completed" ? "success" : game.state === "active" ? "info" : "warning"}>
                        {formatGameStatus(game.state)}
                      </Status>
                    </div>
                    <dl className={styles.seriesGameFacts}>
                      <div>
                        <dt>Категория</dt>
                        <dd>{game.category}</dd>
                      </div>
                      <div>
                        <dt>Счет до игры</dt>
                        <dd>{game.scoreBefore.own}:{game.scoreBefore.opponent}</dd>
                      </div>
                      <div>
                        <dt>Победитель</dt>
                        <dd>{game.winnerId === null ? "Не опубликован" : participantLabelFor(game.winnerId, view)}</dd>
                      </div>
                      <div>
                        <dt>Причина</dt>
                        <dd>{game.reason === null ? "Не опубликована" : formatResultReason(game.reason)}</dd>
                      </div>
                      <div>
                        <dt>Ревизия</dt>
                        <dd>{resultRevisionLabel(game.resultRevisionId)}</dd>
                      </div>
                    </dl>
                  </li>
                ))}
              </ol>
            )}
          </div>
        </section>
      )}

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

      {showDraft && view.draft !== null && (
        <section
          aria-labelledby="participant-draft-title"
          className={styles.draft}
          data-draft-revision={view.draft.revision}
          data-draft-state={view.draft.state}
          data-draft-format={view.draft.format}
          data-testid="participant-draft-panel"
        >
          <div className={styles.draftHeader}>
            <div>
              <h3 className={styles.draftTitle} id="participant-draft-title">
                Драфт категории {view.draft.format.toUpperCase()}
              </h3>
              <p className={styles.draftCopy}>
                Порядок хода, дедлайн и доступные категории подтверждены сервером.
              </p>
            </div>
            <Status tone={view.draft.state === "active" ? "info" : view.draft.state === "completed" ? "success" : "neutral"}>
              {draftStateLabel(view.draft.state)}
            </Status>
          </div>

          <dl className={styles.draftFacts} aria-label="Состояние драфта">
            <div>
              <dt>Раунд хода</dt>
              <dd>{view.draft.turn}</dd>
            </div>
            <div>
              <dt>Действие</dt>
              <dd>{draftActionLabel(view.draft.currentAction)}</dd>
            </div>
            <div>
              <dt>Срок хода</dt>
              <dd>{deadlineLabel(view.draft.turnDeadline, view.pause.deadlinesSuppressed)}</dd>
            </div>
            <div>
              <dt>Владелец хода</dt>
              <dd>
                {view.draft.currentActorId === null
                  ? "Не задан сервером"
                  : view.draft.currentActorId === view.participantId
                    ? "Ваш ход"
                    : "Ход соперника"}
              </dd>
            </div>
          </dl>

          <div className={styles.draftPool} data-testid="participant-draft-pool">
            <h4 className={styles.draftSubtitle}>Категории в пуле</h4>
            <div className={styles.draftCategories}>
              {draft.allowed && !terminalParticipant && view.draft.currentAction !== null
                ? view.draft.pool.map((category) => {
                    const legal = view.draft?.legalCategories.includes(category) ?? false;
                    const action = view.draft?.currentAction;
                    return action === "ban" ? (
                      <Button
                        data-testid={`participant-draft-ban-${category}`}
                        disabled={!legal || draft.status === "submitting"}
                        key={category}
                        loading={draft.status === "submitting" && legal}
                        loadingLabel="Передаем"
                        onClick={() => draft.ban(category)}
                        type="button"
                        variant="secondary"
                      >
                        Забанить {formatCategory(category)}
                      </Button>
                    ) : (
                      <Button
                        data-testid={`participant-draft-pick-${category}`}
                        disabled={!legal || draft.status === "submitting"}
                        key={category}
                        loading={draft.status === "submitting" && legal}
                        loadingLabel="Передаем"
                        onClick={() => draft.pick(category)}
                        type="button"
                        variant="secondary"
                      >
                        Выбрать {formatCategory(category)}
                      </Button>
                    );
                  })
                : view.draft.pool.map((category) => (
                    <span className={styles.draftCategory} key={category}>
                      {formatCategory(category)}
                    </span>
                  ))}
            </div>
            <p className={styles.draftHint}>
              {draft.allowed
                ? view.draft.currentAction === "ban"
                  ? "Выберите категорию для бана. Подтверждение и следующий ход вернет сервер."
                  : "Выберите категорию для игры. Подтверждение и следующий ход вернет сервер."
                : view.draft.state === "paused"
                  ? "Драфт на паузе. Ходы возобновятся только после решения сервера."
                  : view.draft.currentActorId !== null && view.draft.currentActorId !== view.participantId
                    ? "Сейчас ход соперника. Ожидайте обновления сервера."
                    : "Ход недоступен в текущем серверном состоянии."}
            </p>
          </div>

          {view.draft.selectedCategories.length > 0 && (
            <div className={styles.draftSelection} data-testid="participant-draft-selected">
              <h4 className={styles.draftSubtitle}>Категории игр</h4>
              <ol className={styles.draftSelectedGames}>
                {view.draft.selectedCategories.map((category, index) => (
                  <li key={`${index + 1}-${category}`} data-testid={`participant-draft-game-${index + 1}`}>
                    <span>Игра {index + 1}</span>
                    <strong>{formatCategory(category)}</strong>
                  </li>
                ))}
              </ol>
            </div>
          )}

          {view.draft.actions.length > 0 && (
            <div className={styles.draftHistory} aria-label="История драфта">
              <h4 className={styles.draftSubtitle}>История ходов</h4>
              <ol className={styles.draftActions}>
                {view.draft.actions.map((action) => (
                  <li
                    className={styles.draftAction}
                    data-automatic={action.automatic ? "true" : "false"}
                    data-testid={`participant-draft-action-${action.turn}`}
                    key={`${action.turn}-${action.actorId}-${action.category}`}
                  >
                    <div>
                      <strong>{formatCategory(action.category)}</strong>
                      <span>{action.action === "ban" ? "Бан" : "Выбор"}</span>
                    </div>
                    <span className={styles.draftActionMeta}>
                      {action.automatic ? "Автоматически сервером" : "Ход участника"}
                    </span>
                    {action.decisionEvidence !== null && (
                      <span className={styles.draftEvidence}>
                        Подтверждено доказательством решения сервера
                      </span>
                    )}
                  </li>
                ))}
              </ol>
            </div>
          )}

          {draft.message !== null && (
            <Message
              data-status={draft.status}
              data-testid="participant-draft-status"
              tone={draftStatusTone(draft.status)}
              title={draft.status === "accepted" ? "Ход принят" : "Драфт"}
            >
              <p>{draft.message}</p>
            </Message>
          )}
        </section>
      )}

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
          {view.assignment.sourceFileAvailable && (
            <>
              <Button
                className={styles.assignmentLink}
                disabled={sourceFile.status === "loading"}
                loading={sourceFile.status === "loading"}
                loadingLabel="Получаем архив"
                onClick={sourceFile.request}
                type="button"
                variant="secondary"
              >
                Скачать архив
              </Button>
              {sourceFile.message !== null && (
                <Message
                  tone={sourceFile.status === "ready" ? "success" : "error"}
                  title={sourceFile.status === "ready" ? "Готово" : "Архив недоступен"}
                >
                  <p>{sourceFile.message}</p>
                </Message>
              )}
            </>
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
                {view.pause.deadlinesSuppressed
                  ? "Приостановлен сервером"
                  : view.taskDeadlineAt === null
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
            <details
              className={styles.assignmentHints}
              onToggle={(event) => setHintsOpen(event.currentTarget.open)}
              open={hintsOpen}
            >
              <summary>Подсказки ({view.assignment.hints.length})</summary>
              {hintsOpen && (
                <ul>
                  {view.assignment.hints.map((hint) => <li key={hint}>{hint}</li>)}
                </ul>
              )}
            </details>
          )}
          {!terminalParticipant && (
            <div className={styles.gameActions}>
            <form className={styles.submissionForm} onSubmit={submitAnswer}>
              <label className={styles.submissionLabel} htmlFor="participant-answer-input">
                Ответ
              </label>
              <div className={styles.submissionControls}>
                <input
                  aria-describedby={submissionDescribedBy}
                  aria-invalid={submissionHasError}
                  className={styles.submissionInput}
                  data-testid="participant-answer-input"
                  disabled={submission.status === "pending" || view.pause.active}
                  id="participant-answer-input"
                  onChange={(event) => setSubmittedFlag(event.target.value)}
                  value={submittedFlag}
                />
                <Button
                  data-testid="participant-submit-button"
                  disabled={view.pause.active || !submission.allowed || submission.status === "pending"}
                  loading={submission.status === "pending"}
                  loadingLabel="Проверяем"
                  type="submit"
                  variant="primary"
                >
                  Отправить ответ
                </Button>
              </div>
              <p className={styles.submissionHelp} id="participant-submission-help">
                {view.pause.active
                  ? "Отправка приостановлена сервером. Ожидайте нового снимка состояния."
                  : submission.allowed
                    ? "Сервер проверит ответ. Победитель определяется только официальным результатом."
                    : "Отправка откроется, когда сервер активирует текущую игру."}
              </p>
              {submission.message !== null && (
                <Message
                  data-status={submission.status}
                  data-testid="participant-submission-status"
                  id="participant-submission-status"
                  tone={submissionTone(submission.status)}
                  title={submission.status === "accepted" ? "Ответ принят" : "Отправка ответа"}
                >
                  <p>{submission.message}</p>
                </Message>
              )}
            </form>

            {surrender.allowed && (
              <div className={styles.surrender} data-testid="participant-surrender">
                {surrender.status === "confirming" ? (
                  <div
                    aria-labelledby="participant-surrender-title"
                    className={styles.surrenderConfirm}
                    data-testid="participant-surrender-confirmation"
                    role="alert"
                  >
                    <h4 id="participant-surrender-title">Подтвердить сдачу?</h4>
                    <p>Сдача завершит текущую серию для этого участника.</p>
                    <div className={styles.surrenderActions}>
                      <Button
                        data-testid="participant-surrender-cancel"
                        onClick={surrender.cancel}
                        variant="ghost"
                      >
                        Отменить
                      </Button>
                      <Button
                        data-testid="participant-surrender-confirm"
                        onClick={surrender.confirm}
                        variant="danger"
                      >
                        Подтвердить сдачу
                      </Button>
                    </div>
                  </div>
                ) : (
                  <Button
                    data-testid="participant-surrender-button"
                    disabled={surrender.status === "pending"}
                    loading={surrender.status === "pending"}
                    loadingLabel="Отправляем"
                    onClick={surrender.requestConfirmation}
                    variant="danger"
                  >
                    Сдаться
                  </Button>
                )}
                {surrender.message !== null && (
                  <Message
                    data-status={surrender.status}
                    data-testid="participant-surrender-status"
                    tone={surrenderTone(surrender.status)}
                    title={surrender.status === "accepted" ? "Сдача принята" : "Сдача"}
                  >
                    <p>{surrender.message}</p>
                  </Message>
                )}
              </div>
            )}
            </div>
          )}
          </>
        )}
      </section>

      {showReadinessAction && (
        <section className={styles.readiness} aria-labelledby="participant-readiness-title">
          <div className={styles.readinessHeader}>
            <div>
              <h3 className={styles.readinessTitle} id="participant-readiness-title">
                Готовность к раунду
              </h3>
              <p className={styles.readinessCopy} id="participant-readiness-help">
                {view.readyWindowOpen
                  ? "Подтверждение действует только для текущего окна и назначения."
                  : "Действие откроется только в активном окне готовности."}
              </p>
            </div>
            <span className={styles.deadline} data-ready-window-state={view.readyWindowState ?? "closed"}>
              {view.pause.deadlinesSuppressed
                ? "Срок приостановлен сервером"
                : view.readyWindowDeadline === null
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
        </section>
      )}

      <p className={styles.revision}>
        Подтвержденная ревизия сервера: {view.projectionRevision}
      </p>
    </section>
  );
};

TournamentPlayerPanel.displayName = "TournamentPlayerPanel";
