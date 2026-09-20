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
import { formatCategory, formatGameStatus } from "../../shared/lib";

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
    () => safeTaskHref(view?.assignment?.taskUrl ?? null),
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
    readiness.status === "submitting" ||
    readiness.ready ||
    view.state === "bye" ||
    view.state === "eliminated" ||
    view.state === "completed";
  const showReadinessAction = view.state !== "bye" && view.state !== "eliminated" && view.state !== "completed";
  const showDraft = view.draft !== null && (view.draft.format === "bo1" || view.draft.format === "bo3");

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
              <dd>{formatDeadline(view.draft.turnDeadline)}</dd>
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
              {draft.allowed && view.draft.currentAction !== null
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
          <div className={styles.gameActions}>
            <form className={styles.submissionForm} onSubmit={submitAnswer}>
              <label className={styles.submissionLabel} htmlFor="participant-answer-input">
                Ответ
              </label>
              <div className={styles.submissionControls}>
                <input
                  aria-describedby="participant-submission-help"
                  className={styles.submissionInput}
                  data-testid="participant-answer-input"
                  disabled={submission.status === "pending"}
                  id="participant-answer-input"
                  onChange={(event) => setSubmittedFlag(event.target.value)}
                  value={submittedFlag}
                />
                <Button
                  data-testid="participant-submit-button"
                  disabled={!submission.allowed || submission.status === "pending"}
                  loading={submission.status === "pending"}
                  loadingLabel="Проверяем"
                  type="submit"
                  variant="primary"
                >
                  Отправить ответ
                </Button>
              </div>
              <p className={styles.submissionHelp} id="participant-submission-help">
                {submission.allowed
                  ? "Сервер проверит ответ. Победитель определяется только официальным результатом."
                  : "Отправка откроется, когда сервер активирует текущую игру."}
              </p>
              {submission.message !== null && (
                <Message
                  data-status={submission.status}
                  data-testid="participant-submission-status"
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
