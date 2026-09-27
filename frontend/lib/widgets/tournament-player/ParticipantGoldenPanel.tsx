"use client";

import { useEffect, useMemo, useState, type FormEvent } from "react";

import {
  useParticipantAssignmentSourceFile,
  useParticipantGolden,
  type ParticipantGoldenActionStatus,
} from "../../features/tournament-player";
import { formatCategory, formatGoldenState, getSafeTaskHref } from "../../shared/lib";
import { Button, Message, Status } from "../../shared/ui";

import styles from "./ParticipantGoldenPanel.module.css";

type ParticipantGoldenPanelProps = Readonly<{
  refreshToken: string;
  tournamentId: string;
}>;

const timerLabel = (deadline: string | null, now: number, paused: boolean): string => {
  if (paused) {
    return "Остановлен";
  }
  if (deadline === null) {
    return "Не запущен";
  }
  const deadlineMs = Date.parse(deadline);
  if (!Number.isFinite(deadlineMs)) {
    return "Не опубликован";
  }
  const remainingSeconds = Math.max(0, Math.ceil((deadlineMs - now) / 1_000));
  const minutes = Math.floor(remainingSeconds / 60);
  const seconds = remainingSeconds % 60;
  return `${minutes}:${seconds.toString().padStart(2, "0")}`;
};

const formatTimestamp = (value: string | null): string => {
  if (value === null) {
    return "Не опубликован";
  }
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) {
    return "Не опубликован";
  }
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "medium",
  }).format(timestamp);
};

const stateTone = (
  state: NonNullable<ReturnType<typeof useParticipantGolden>["snapshot"]>["state"],
): "neutral" | "info" | "success" | "warning" | "error" => {
  switch (state) {
    case "prepared":
      return "neutral";
    case "ready":
    case "active":
      return "info";
    case "technical_pause":
      return "warning";
    case "completed":
      return "success";
    case "cancelled":
      return "error";
    case "superseded":
      return "neutral";
  }
};

const actionTone = (
  status: ParticipantGoldenActionStatus,
): "info" | "success" | "warning" | "error" => {
  switch (status) {
    case "accepted":
      return "success";
    case "pending":
      return "info";
    case "conflict":
    case "incorrect":
    case "rate_limited":
      return "warning";
    case "error":
    case "idle":
      return "error";
  }
};

export const ParticipantGoldenPanel = ({
  refreshToken,
  tournamentId,
}: ParticipantGoldenPanelProps) => {
  const golden = useParticipantGolden(tournamentId, refreshToken);
  const snapshot = golden.snapshot;
  const [submittedFlag, setSubmittedFlag] = useState("");
  const [now, setNow] = useState(() => Date.now());
  const sourceTarget = useMemo(() => snapshot?.task === null || snapshot?.task === undefined
    ? null
    : {
        assignmentId: snapshot.task.assignment_id,
        sourceFileAvailable: snapshot.task.source_file_available,
        tournamentId,
        verifyAssignment: false,
      }, [snapshot?.task, tournamentId]);
  const sourceFile = useParticipantAssignmentSourceFile(sourceTarget);
  const taskHref = useMemo(
    () => getSafeTaskHref(snapshot?.task?.task_url),
    [snapshot?.task?.task_url],
  );

  useEffect(() => {
    setSubmittedFlag("");
  }, [snapshot?.attempt_id]);

  useEffect(() => {
    if (snapshot?.state !== "active" || snapshot.deadline === null) {
      return;
    }
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, [snapshot?.deadline, snapshot?.state]);

  if (golden.loadStatus === "unavailable") {
    return null;
  }

  if (snapshot === null) {
    return (
      <section
        aria-labelledby="participant-golden-title"
        className={styles.panel}
        data-state={golden.loadStatus}
        data-testid="participant-golden-panel"
      >
        <div className={styles.header}>
          <div>
            <p className={styles.eyebrow}>Групповое задание</p>
            <h2 className={styles.title} id="participant-golden-title">Golden Task</h2>
          </div>
          <Status tone={golden.loadStatus === "loading" ? "loading" : "error"}>
            {golden.loadStatus === "loading" ? "Загружаем Golden" : "Нет связи"}
          </Status>
        </div>
        {golden.loadMessage !== null && (
          <Message tone="error" title="Golden недоступен">
            <p>{golden.loadMessage}</p>
            <Button onClick={golden.refresh} type="button" variant="secondary">Повторить</Button>
          </Message>
        )}
      </section>
    );
  }

  const readyAllowed = snapshot.state === "prepared" && !snapshot.ready;
  const submitAllowed = snapshot.state === "active" && snapshot.task !== null && !snapshot.submitted;
  const paused = snapshot.state === "technical_pause";
  const goldenSubmitHasError =
    golden.actionStatus === "incorrect" ||
    golden.actionStatus === "conflict" ||
    golden.actionStatus === "rate_limited" ||
    golden.actionStatus === "error";
  const goldenSubmitDescribedBy = golden.actionMessage === null
    ? undefined
    : "participant-golden-action-status";
  const submitAnswer = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const value = submittedFlag;
    setSubmittedFlag("");
    golden.submit(value);
  };

  return (
    <section
      aria-labelledby="participant-golden-title"
      className={styles.panel}
      data-attempt-id={snapshot.attempt_id}
      data-golden-state={snapshot.state}
      data-runtime-revision={snapshot.runtime_revision}
      data-testid="participant-golden-panel"
    >
      <div className={styles.header}>
        <div>
          <p className={styles.eyebrow}>Групповое задание</p>
          <h2 className={styles.title} id="participant-golden-title">Golden Task</h2>
          <p className={styles.description}>
            Это общая группа с единым распределением мест, а не BO1-серия.
          </p>
        </div>
        <Status tone={stateTone(snapshot.state)}>{formatGoldenState(snapshot.state)}</Status>
      </div>

      <dl className={styles.facts} aria-label="Golden группа">
        <div>
          <dt>Таймер</dt>
          <dd data-testid="participant-golden-timer">
            {timerLabel(snapshot.deadline, now, paused)}
          </dd>
        </div>
      </dl>

      <div className={styles.timeline}>
        <div>
          <span>Старт</span>
          <strong>{formatTimestamp(snapshot.started_at)}</strong>
        </div>
        <div>
            <span>Дедлайн</span>
          <strong>{formatTimestamp(snapshot.deadline)}</strong>
        </div>
      </div>

      {readyAllowed && (
        <div className={styles.actions}>
          <Button
            data-testid="participant-golden-ready"
            disabled={golden.actionStatus === "pending"}
            loading={golden.actionStatus === "pending"}
            loadingLabel="Подтверждаем"
            onClick={golden.ready}
            type="button"
            variant="primary"
          >
            Готов к Golden
          </Button>
          <p>Материалы появятся после старта и допуска участника.</p>
        </div>
      )}

      {snapshot.state === "ready" && (
        <Message tone="info" title="Группа готова">
          <p>Ожидайте запуска. Задание пока недоступно.</p>
        </Message>
      )}

      {paused && (
        <Message tone="warning" title="Техническая пауза">
          <p>Таймер и действия остановлены. Продолжение появится после паузы.</p>
        </Message>
      )}

      {snapshot.task !== null ? (
        <section className={styles.task} aria-labelledby="participant-golden-task-title">
          <div className={styles.taskHeader}>
            <div>
              <p className={styles.eyebrow}>Материалы попытки</p>
              <h3 id="participant-golden-task-title">{snapshot.task.title}</h3>
            </div>
            <Status tone="info">{formatCategory(snapshot.task.category)}</Status>
          </div>
          <p>{snapshot.task.description}</p>
          <dl className={styles.taskFacts}>
            <div><dt>Сложность</dt><dd>{snapshot.task.difficulty}</dd></div>
            <div><dt>Версия</dt><dd>{snapshot.task.version}</dd></div>
            <div><dt>Лимит</dt><dd>{snapshot.task.time_limit_seconds} с</dd></div>
          </dl>
          <div className={styles.taskLinks}>
            {taskHref !== null && <a href={taskHref}>Открыть Golden задание</a>}
            {snapshot.task.source_file_available && (
              <Button
                disabled={sourceFile.status === "loading"}
                loading={sourceFile.status === "loading"}
                loadingLabel="Получаем архив"
                onClick={sourceFile.request}
                type="button"
                variant="secondary"
              >
                Скачать архив
              </Button>
            )}
          </div>
          {sourceFile.message !== null && (
            <Message
              tone={sourceFile.status === "ready" ? "success" : "error"}
              title={sourceFile.status === "ready" ? "Готово" : "Архив недоступен"}
            >
              <p>{sourceFile.message}</p>
            </Message>
          )}
        </section>
      ) : snapshot.state === "active" || snapshot.state === "completed" ? (
        <Message tone="warning" title="Материалы не выданы">
          <p>
            Участие в этой попытке недоступно. Задание не восстанавливается из чужих данных.
          </p>
        </Message>
      ) : null}

      {submitAllowed && (
        <form className={styles.submit} onSubmit={submitAnswer}>
          <label htmlFor="participant-golden-answer">Ответ Golden</label>
          <div className={styles.submitRow}>
            <input
              aria-describedby={goldenSubmitDescribedBy}
              aria-invalid={goldenSubmitHasError}
              autoComplete="off"
              id="participant-golden-answer"
              onChange={(event) => setSubmittedFlag(event.target.value)}
              placeholder="Введите флаг"
              spellCheck={false}
              type="password"
              value={submittedFlag}
            />
            <Button
              data-testid="participant-golden-submit"
              disabled={golden.actionStatus === "pending"}
              loading={golden.actionStatus === "pending"}
              loadingLabel="Проверяем"
              type="submit"
              variant="primary"
            >
              Отправить ответ
            </Button>
          </div>
        </form>
      )}

      {snapshot.submitted && (
        <Message tone="success" title="Решение принято">
          <p>Результат зафиксирован. Ожидайте итоговое распределение мест.</p>
        </Message>
      )}

      {snapshot.state === "completed" && (
        <div className={styles.position} data-testid="participant-golden-position">
          <span>Официальное место в группе</span>
          <strong>{snapshot.position ?? "Не опубликовано"}</strong>
          <p>Следующий этап появится после обновления результата.</p>
        </div>
      )}

      {golden.actionMessage !== null && (
        <Message
          data-status={golden.actionStatus}
          data-testid="participant-golden-action-status"
          id="participant-golden-action-status"
          tone={actionTone(golden.actionStatus)}
          title={golden.actionStatus === "accepted" ? "Действие подтверждено" : "Golden"}
        >
          <p>{golden.actionMessage}</p>
        </Message>
      )}

      <div className={styles.footer}>
        <Button
          disabled={golden.actionStatus === "pending"}
          onClick={golden.refresh}
          type="button"
          variant="ghost"
        >
          Обновить данные
        </Button>
      </div>
    </section>
  );
};

ParticipantGoldenPanel.displayName = "ParticipantGoldenPanel";
