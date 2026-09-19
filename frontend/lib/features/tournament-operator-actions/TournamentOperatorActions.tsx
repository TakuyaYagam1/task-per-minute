"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import {
  ApiError,
  createOperatorCommandIntent,
  operatorApi,
  type OperatorForfeitGameExpectation,
  type OperatorForfeitRequest,
  type OperatorNoShowRequest,
  type OperatorRecoverySnapshot,
  type TournamentActionRequest,
  type WaveControlRequest,
} from "../../shared/api";
import { Button, Dialog, Message, Panel, Status } from "../../shared/ui";

import styles from "./TournamentOperatorActions.module.css";

type OperatorAction = "pause" | "resume" | "cancel" | "no-show" | "operator-forfeit";

type TournamentOperatorActionsProps = Readonly<{
  tournamentId: string;
}>;

type NoShowCandidate = Readonly<{
  series: OperatorRecoverySnapshot["series"][number];
  wave: OperatorRecoverySnapshot["waves"][number];
  window: NonNullable<OperatorRecoverySnapshot["waves"][number]["ready_window"]>;
}>;

type ActionMessage = Readonly<{
  tone: "success" | "error" | "warning";
  title: string;
  body: string;
}>;

const TERMINAL_TOURNAMENT_STATES = new Set(["completed", "cancelled"]);
const PAUSABLE_TOURNAMENT_STATES = new Set(["swiss", "golden", "playoffs"]);
const PAUSABLE_WAVE_STATES = new Set(["ready_window_open", "ready", "active"]);
const NO_SHOW_SERIES_STATES = new Set(["ready", "active", "replay_required"]);
const FORFEIT_SERIES_STATES = new Set(["active", "technical_pause"]);
const LIVE_GAME_STATES = new Set(["active", "paused"]);

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const stateLabel = (value: OperatorRecoverySnapshot["tournament"]["state"]): string => {
  switch (value) {
    case "draft":
      return "Черновик";
    case "registration":
      return "Регистрация";
    case "roster_locked":
      return "Состав закрыт";
    case "swiss":
      return "Швейцарка";
    case "golden":
      return "Golden";
    case "playoffs":
      return "Плей-офф";
    case "technical_pause":
      return "Техническая пауза";
    case "completed":
      return "Завершен";
    case "cancelled":
      return "Отменен";
  }
};

const errorText = (error: unknown): string => {
  if (error instanceof ApiError) {
    if (error.status === 401) {
      return "Сессия оператора истекла. Войдите снова.";
    }
    if (error.status === 403) {
      return "Эта сессия не может изменять выбранный турнир.";
    }
    if (error.status === 404) {
      return "Турнир или выбранный объект больше недоступен.";
    }
    if (error.status === 409) {
      return "Снимок устарел. Состояние обновлено, проверьте команду еще раз.";
    }
    if (error.status > 0) {
      return `Сервер отклонил команду (HTTP ${error.status}).`;
    }
  }
  return "Не удалось выполнить команду. Проверьте соединение и повторите попытку.";
};

const newUUID = (): string => createOperatorCommandIntent().idempotencyKey;

const latestLiveAttempt = (
  series: OperatorRecoverySnapshot["series"][number],
): OperatorRecoverySnapshot["series"][number]["slots"][number]["attempts"][number] | null => {
  let selected: {
    attempt: OperatorRecoverySnapshot["series"][number]["slots"][number]["attempts"][number];
    slotPosition: number;
  } | null = null;
  for (const slot of series.slots) {
    for (const attempt of slot.attempts) {
      if (!LIVE_GAME_STATES.has(attempt.state)) {
        continue;
      }
      if (
        selected === null ||
        slot.position > selected.slotPosition ||
        (slot.position === selected.slotPosition && attempt.attempt_no > selected.attempt.attempt_no)
      ) {
        selected = { attempt, slotPosition: slot.position };
      }
    }
  }
  return selected?.attempt ?? null;
};

const operatorForfeitExpectation = (
  series: OperatorRecoverySnapshot["series"][number],
): OperatorForfeitGameExpectation | null => {
  const attempt = latestLiveAttempt(series);
  if (attempt === null) {
    return null;
  }
  return {
    attempt_no: attempt.attempt_no,
    game_id: attempt.id,
    slot_id: attempt.slot_id,
    state: attempt.state,
  };
};

const noShowCandidatesFor = (snapshot: OperatorRecoverySnapshot): NoShowCandidate[] => {
  const seriesById = new Map(snapshot.series.map((series) => [series.id, series]));
  return snapshot.waves.flatMap((wave) => {
    const readyWindow = wave.ready_window;
    if (readyWindow?.state !== "open") {
      return [];
    }
    const deadline = Date.parse(readyWindow.deadline);
    if (!Number.isFinite(deadline) || Date.now() < deadline) {
      return [];
    }
    const membersBySeries = new Map<string, typeof wave.members>();
    for (const member of wave.members) {
      if (member.series_id === undefined || member.series_id === null) {
        continue;
      }
      const members = membersBySeries.get(member.series_id) ?? [];
      members.push(member);
      membersBySeries.set(member.series_id, members);
    }
    return Array.from(membersBySeries.entries()).flatMap(([seriesId, members]) => {
      const series = seriesById.get(seriesId);
      const readyCount = members.filter((member) => member.ready).length;
      if (
        !series ||
        members.length !== 2 ||
        !NO_SHOW_SERIES_STATES.has(series.state) ||
        readyCount >= 2
      ) {
        return [];
      }
      return [{ series, wave, window: readyWindow }];
    });
  });
};

const noShowGameResultIDs = (
  series: OperatorRecoverySnapshot["series"][number],
): string[] => series.slots.flatMap((slot) => {
  const attempt = slot.attempts.at(-1);
  return attempt !== undefined && (attempt.state === "planned" || attempt.state === "ready")
    ? [newUUID()]
    : [];
});

const parseEvidenceIDs = (value: string): string[] =>
  value
    .split(/[\n,]/u)
    .map((item) => item.trim())
    .filter((item) => item.length > 0);

const actionLabel = (action: OperatorAction): string => {
  switch (action) {
    case "pause":
      return "Пауза турнира";
    case "resume":
      return "Возобновить турнир";
    case "cancel":
      return "Отменить турнир";
    case "no-show":
      return "Неявка пары";
    case "operator-forfeit":
      return "Операторский форфейт";
  }
};

const actionAvailable = (
  action: OperatorAction,
  snapshot: OperatorRecoverySnapshot,
  noShowCandidates: readonly NoShowCandidate[],
  forfeitAvailable: boolean,
): boolean => {
  const state = snapshot.tournament.state;
  switch (action) {
    case "pause":
      return PAUSABLE_TOURNAMENT_STATES.has(state);
    case "resume":
      return state === "technical_pause";
    case "cancel":
      return !TERMINAL_TOURNAMENT_STATES.has(state);
    case "no-show":
      return noShowCandidates.length > 0;
    case "operator-forfeit":
      return forfeitAvailable;
  }
};

const waveForTournamentAction = (
  action: "pause" | "resume",
  snapshot: OperatorRecoverySnapshot,
): OperatorRecoverySnapshot["waves"][number] | null => {
  if (action === "pause") {
    return snapshot.tournament.state === "swiss"
      ? snapshot.waves.find((wave) => PAUSABLE_WAVE_STATES.has(wave.state)) ?? null
      : null;
  }
  const pausedWaveId = snapshot.pause_graph?.wave.id;
  if (pausedWaveId === undefined) {
    return null;
  }
  return snapshot.waves.find((wave) => wave.id === pausedWaveId && wave.state === "paused") ?? null;
};

export const TournamentOperatorActions = ({
  tournamentId,
}: TournamentOperatorActionsProps) => {
  const [snapshot, setSnapshot] = useState<OperatorRecoverySnapshot | null>(null);
  const [snapshotLoading, setSnapshotLoading] = useState(true);
  const [snapshotError, setSnapshotError] = useState<string | null>(null);
  const [action, setAction] = useState<OperatorAction>("pause");
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [selectedWaveId, setSelectedWaveId] = useState("");
  const [selectedSeriesId, setSelectedSeriesId] = useState("");
  const [selectedParticipantId, setSelectedParticipantId] = useState("");
  const [ruleId, setRuleId] = useState("");
  const [evidenceText, setEvidenceText] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [cancelDialogOpen, setCancelDialogOpen] = useState(false);
  const [actionMessage, setActionMessage] = useState<ActionMessage | null>(null);
  const requestGenerationRef = useRef(0);
  const submittingRef = useRef(false);

  const loadSnapshot = useCallback(async (signal?: AbortSignal): Promise<OperatorRecoverySnapshot> => {
    const requestGeneration = requestGenerationRef.current + 1;
    requestGenerationRef.current = requestGeneration;
    setSnapshotLoading(true);
    try {
      const nextSnapshot = await operatorApi.getSnapshot(tournamentId, undefined, signal);
      if (requestGeneration === requestGenerationRef.current) {
        setSnapshot(nextSnapshot);
        setSnapshotError(null);
      }
      return nextSnapshot;
    } catch (error) {
      if (requestGeneration === requestGenerationRef.current && !isAbortError(error)) {
        setSnapshotError(errorText(error));
      }
      throw error;
    } finally {
      if (requestGeneration === requestGenerationRef.current) {
        setSnapshotLoading(false);
      }
    }
  }, [tournamentId]);

  useEffect(() => {
    const controller = new AbortController();
    void loadSnapshot(controller.signal).catch(() => undefined);
    return () => {
      controller.abort();
      requestGenerationRef.current += 1;
    };
  }, [loadSnapshot]);

  const noShowCandidates = useMemo(
    () => snapshot === null ? [] : noShowCandidatesFor(snapshot),
    [snapshot],
  );
  const selectedNoShow = noShowCandidates.find((candidate) => candidate.wave.id === selectedWaveId) ??
    noShowCandidates[0] ?? null;
  const forfeitSeries = useMemo(
    () => snapshot?.series.filter((series) => FORFEIT_SERIES_STATES.has(series.state)) ?? [],
    [snapshot],
  );
  const selectedForfeitSeries = forfeitSeries.find((series) => series.id === selectedSeriesId) ??
    forfeitSeries[0] ?? null;
  const selectedForfeitAttempt = selectedForfeitSeries ? latestLiveAttempt(selectedForfeitSeries) : null;
  const forfeitAvailable = selectedForfeitSeries !== null &&
    selectedForfeitAttempt !== null &&
    LIVE_GAME_STATES.has(selectedForfeitAttempt.state);

  useEffect(() => {
    if (selectedWaveId === "" || !noShowCandidates.some((candidate) => candidate.wave.id === selectedWaveId)) {
      setSelectedWaveId(noShowCandidates[0]?.wave.id ?? "");
    }
  }, [noShowCandidates, selectedWaveId]);

  useEffect(() => {
    if (selectedSeriesId === "" || !forfeitSeries.some((series) => series.id === selectedSeriesId)) {
      setSelectedSeriesId(forfeitSeries[0]?.id ?? "");
    }
  }, [forfeitSeries, selectedSeriesId]);

  useEffect(() => {
    const participants = selectedForfeitSeries === null
      ? []
      : [selectedForfeitSeries.first_participant_id, selectedForfeitSeries.second_participant_id];
    if (!participants.includes(selectedParticipantId)) {
      setSelectedParticipantId(participants[0] ?? "");
    }
  }, [selectedForfeitSeries, selectedParticipantId]);

  const selectedActionAvailable = snapshot !== null &&
    actionAvailable(action, snapshot, noShowCandidates, forfeitAvailable);
  const submitDisabled = snapshotLoading || snapshot === null || submitting ||
    !confirmed || reason.trim().length === 0 || !selectedActionAvailable;

  const submit = async (): Promise<void> => {
    if (submittingRef.current || submitDisabled || snapshot === null) {
      return;
    }
    submittingRef.current = true;
    setSubmitting(true);
    setActionMessage(null);
    const intent = createOperatorCommandIntent();
    const trimmedReason = reason.trim();
    try {
      if (action === "pause" || action === "resume") {
        const wave = waveForTournamentAction(action, snapshot);
        if (wave !== null) {
          const body: WaveControlRequest = {
            action,
            confirmed: true,
            expected_projection_revision: snapshot.next_cursor.projection_revision,
            reason: trimmedReason,
          };
          await operatorApi.controlWave(tournamentId, wave.id, body, intent);
        } else {
          const body: TournamentActionRequest = {
            action,
            confirmed: true,
            expected_projection_revision: snapshot.next_cursor.projection_revision,
            reason: trimmedReason,
          };
          await operatorApi.applyTournamentAction(tournamentId, body, intent);
        }
      } else if (action === "cancel") {
        const body: TournamentActionRequest = {
          action,
          confirmed: true,
          expected_projection_revision: snapshot.next_cursor.projection_revision,
          reason: trimmedReason,
        };
        await operatorApi.applyTournamentAction(tournamentId, body, intent);
      } else if (action === "no-show") {
        if (selectedNoShow === null) {
          throw new Error("No-show authority is unavailable");
        }
        const body: OperatorNoShowRequest = {
          confirmed: true,
          expected_authority_revision: snapshot.next_cursor.authority_revision,
          expected_series_state: selectedNoShow.series.state,
          expected_wave_revision_id: selectedNoShow.wave.revision_id,
          expected_window_revision_id: selectedNoShow.window.revision_id,
          game_result_revision_ids: noShowGameResultIDs(selectedNoShow.series),
          reason: trimmedReason,
          score_revision_id: newUUID(),
          series_id: selectedNoShow.series.id,
          series_result_revision_id: newUUID(),
          tournament_id: tournamentId,
          wave_id: selectedNoShow.wave.id,
          window_id: selectedNoShow.window.id,
        };
        await operatorApi.resolveNoShow(tournamentId, selectedNoShow.wave.id, body, intent);
      } else {
        if (selectedForfeitSeries === null || selectedParticipantId === "" || !forfeitAvailable) {
          throw new Error("Operator forfeit authority is unavailable");
        }
        const evidenceIds = parseEvidenceIDs(evidenceText);
        if (ruleId.trim().length === 0 || evidenceIds.length === 0) {
          setActionMessage({
            tone: "warning",
            title: "Нужно добавить доказательства",
            body: "Укажите rule_id и хотя бы один evidence ID. Сервер не принимает пустой список evidence IDs.",
          });
          return;
        }
        const expectedGame = operatorForfeitExpectation(selectedForfeitSeries);
        const body: OperatorForfeitRequest = {
          audit_event_id: newUUID(),
          basis: "rule_violation",
          confirmed: true,
          evidence_ids: evidenceIds,
          expected_authority_revision: snapshot.next_cursor.authority_revision,
          expected_game: expectedGame,
          forfeiting_participant_id: selectedParticipantId,
          game_result_revision_id: expectedGame === null ? null : newUUID(),
          outbox_event_id: newUUID(),
          projection_revision_id: newUUID(),
          reason: trimmedReason,
          rule_id: ruleId.trim(),
          score_revision_id: newUUID(),
          series_id: selectedForfeitSeries.id,
          series_result_revision_id: newUUID(),
          tournament_id: tournamentId,
        };
        await operatorApi.recordForfeit(tournamentId, selectedForfeitSeries.id, body, intent);
      }

      await loadSnapshot();
      setActionMessage({
        tone: "success",
        title: "Команда подтверждена",
        body: `${actionLabel(action)} выполнено. Отображается новый авторитетный снимок.`,
      });
      setConfirmed(false);
    } catch (error) {
      if (isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 409) {
        try {
          await loadSnapshot();
        } catch {
          // The stale message remains the actionable state for the operator.
        }
        setActionMessage({
          tone: "warning",
          title: "Снимок устарел",
          body: "Другой оператор изменил состояние. Снимок обновлен, проверьте команду перед повтором.",
        });
        return;
      }
      setActionMessage({ tone: "error", title: "Команда не выполнена", body: errorText(error) });
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  };

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    if (action === "cancel") {
      setCancelDialogOpen(true);
      return;
    }
    void submit();
  };

  if (snapshotLoading && snapshot === null) {
    return (
      <Panel title="Управление турниром" description="Получаем авторитетный снимок перед показом команд.">
        <div className={styles.loadingState} role="status" aria-live="polite">
          <Status tone="loading">Загружаем снимок оператора</Status>
          <p className={styles.errorText}>Команды появятся после проверки ревизии сервера.</p>
        </div>
      </Panel>
    );
  }

  if (snapshot === null) {
    return (
      <Panel title="Управление турниром" description="Команды доступны только после авторитетной синхронизации.">
        <Message tone="error" title="Снимок недоступен">
          <p className={styles.errorText}>{snapshotError ?? "Не удалось получить состояние турнира."}</p>
          <Button size="small" variant="secondary" onClick={() => void loadSnapshot()}>
            Повторить синхронизацию
          </Button>
        </Message>
      </Panel>
    );
  }

  const actionHint = action === "operator-forfeit"
    ? "Операторский форфейт фиксирует нарушение правила и официальный результат. Это не действие участника."
    : action === "no-show"
      ? "Неявка появляется после дедлайна открытого окна для каждой связанной пары, если готовы не оба участника."
      : "Команда использует текущую ревизию снимка и применяется только при допустимом состоянии турнира.";

  return (
    <Panel
      title="Управление турниром"
      description="Команды отправляются на сервер с ревизией авторитетного снимка."
      className={styles.panel}
    >
      <div className={styles.header}>
        <p className={styles.intro}>
          После каждой успешной команды состояние перечитывается с сервера. Данные ниже не являются локальным прогнозом.
        </p>
        <Status tone={snapshotLoading ? "loading" : "live"}>
          {snapshotLoading ? "Обновляем" : "Снимок подтвержден"}
        </Status>
      </div>

      <dl className={styles.snapshot} aria-label="Авторитетный снимок оператора">
        <div className={styles.snapshotItem}>
          <dt className={styles.snapshotLabel}>Состояние</dt>
          <dd className={styles.snapshotValue}>{stateLabel(snapshot.tournament.state)}</dd>
        </div>
        <div className={styles.snapshotItem}>
          <dt className={styles.snapshotLabel}>Ревизия проекции</dt>
          <dd className={styles.snapshotValue}>{snapshot.next_cursor.projection_revision}</dd>
        </div>
        <div className={styles.snapshotItem}>
          <dt className={styles.snapshotLabel}>Ревизия authority</dt>
          <dd className={styles.snapshotValue}>{snapshot.next_cursor.authority_revision}</dd>
        </div>
      </dl>

      {snapshotError && (
        <Message tone="warning" title="Снимок требует внимания">
          <p className={styles.errorText}>{snapshotError}</p>
        </Message>
      )}
      {actionMessage && (
        <Message tone={actionMessage.tone} title={actionMessage.title}>
          <p className={styles.errorText}>{actionMessage.body}</p>
        </Message>
      )}

      <form className={styles.form} onSubmit={handleSubmit}>
        <div className={styles.formGrid}>
          <div className={`${styles.field} ${styles.fieldWide}`}>
            <label className={styles.label} htmlFor="operator-action">
              Команда оператора
            </label>
            <select
              className={styles.select}
              id="operator-action"
              value={action}
              onChange={(event) => setAction(event.target.value as OperatorAction)}
              disabled={submitting}
            >
              {(["pause", "resume", "cancel", "no-show", "operator-forfeit"] as const).map((candidate) => (
                <option
                  key={candidate}
                  value={candidate}
                  disabled={!actionAvailable(candidate, snapshot, noShowCandidates, forfeitAvailable)}
                >
                  {actionLabel(candidate)}
                </option>
              ))}
            </select>
            <p className={styles.hint}>{actionHint}</p>
          </div>

          {action === "no-show" && (
            <div className={`${styles.field} ${styles.fieldWide}`}>
              <label className={styles.label} htmlFor="operator-no-show-wave">
                Открытое окно неявки
              </label>
              <select
                className={styles.select}
                id="operator-no-show-wave"
                value={selectedNoShow?.wave.id ?? ""}
                onChange={(event) => setSelectedWaveId(event.target.value)}
                disabled={submitting || noShowCandidates.length === 0}
              >
                {noShowCandidates.map((candidate) => (
                  <option key={candidate.wave.id} value={candidate.wave.id}>
                    Волна {candidate.wave.id} - серия {candidate.series.id}
                  </option>
                ))}
              </select>
              {selectedNoShow === null && (
                <p className={styles.hint}>До окончания окна или при полной готовности пары неявка недоступна.</p>
              )}
            </div>
          )}

          {action === "operator-forfeit" && (
            <>
              <div className={styles.field}>
                <label className={styles.label} htmlFor="operator-forfeit-series">
                  Активная серия
                </label>
                <select
                  className={styles.select}
                  id="operator-forfeit-series"
                  value={selectedForfeitSeries?.id ?? ""}
                  onChange={(event) => setSelectedSeriesId(event.target.value)}
                  disabled={submitting || forfeitSeries.length === 0}
                >
                  {forfeitSeries.map((series) => (
                    <option key={series.id} value={series.id}>
                      Серия {series.id} ({series.state})
                    </option>
                  ))}
                </select>
              </div>
              <div className={styles.field}>
                <label className={styles.label} htmlFor="operator-forfeit-participant">
                  Участник, которому засчитать форфейт
                </label>
                <select
                  className={styles.select}
                  id="operator-forfeit-participant"
                  value={selectedParticipantId}
                  onChange={(event) => setSelectedParticipantId(event.target.value)}
                  disabled={submitting || selectedForfeitSeries === null}
                >
                  {[selectedForfeitSeries?.first_participant_id, selectedForfeitSeries?.second_participant_id]
                    .filter((value): value is string => value !== undefined)
                    .map((participantId) => (
                      <option key={participantId} value={participantId}>{participantId}</option>
                    ))}
                </select>
                <p className={styles.hint}>
                  Текущая попытка: {selectedForfeitAttempt?.id ?? "не найдена"} ({selectedForfeitAttempt?.state ?? "нет"}).
                </p>
              </div>
              <div className={styles.field}>
                <label className={styles.label} htmlFor="operator-rule-id">rule_id</label>
                <input
                  className={styles.input}
                  id="operator-rule-id"
                  value={ruleId}
                  onChange={(event) => setRuleId(event.target.value)}
                  disabled={submitting}
                  maxLength={64}
                  placeholder="rule.match.integrity"
                />
              </div>
              <div className={styles.field}>
                <label className={styles.label} htmlFor="operator-evidence-ids">
                  Evidence IDs
                </label>
                <textarea
                  className={styles.textarea}
                  id="operator-evidence-ids"
                  value={evidenceText}
                  onChange={(event) => setEvidenceText(event.target.value)}
                  disabled={submitting}
                  placeholder="Один UUID на строку"
                />
                <p className={styles.hint}>Сервер требует хотя бы один уникальный UUID доказательства.</p>
              </div>
            </>
          )}

          <div className={`${styles.field} ${styles.fieldWide}`}>
            <label className={styles.label} htmlFor="operator-action-reason">
              Причина
            </label>
            <textarea
              className={styles.textarea}
              id="operator-action-reason"
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              disabled={submitting}
              maxLength={256}
              required
              placeholder="Коротко опишите решение и его основание"
            />
          </div>
        </div>

        <p className={styles.actionNote}>
          {action === "no-show"
            ? "Это запись неявки пары, а не сдача участника."
            : action === "operator-forfeit"
              ? "Это операторский форфейт по правилу турнира, а не surrender участника."
              : "Проверьте состояние, ревизию и причину перед подтверждением."}
        </p>

        <label className={styles.checkRow} htmlFor="operator-action-confirmed">
          <input
            className={styles.checkbox}
            id="operator-action-confirmed"
            type="checkbox"
            checked={confirmed}
            onChange={(event) => setConfirmed(event.target.checked)}
            disabled={submitting}
          />
          <span>Подтверждаю, что команда соответствует текущему авторитетному снимку.</span>
        </label>

        <div className={styles.submitRow}>
          <Button
            type="submit"
            variant={action === "cancel" || action === "operator-forfeit" ? "danger" : "primary"}
            loading={submitting}
            loadingLabel="Отправляем"
            disabled={submitDisabled || !selectedActionAvailable}
          >
            Выполнить: {actionLabel(action)}
          </Button>
          <p className={styles.submitHint}>
            {selectedActionAvailable ? "Сервер проверит ревизию повторно." : "Команда недоступна в текущем состоянии."}
          </p>
        </div>
      </form>

      <Dialog
        open={cancelDialogOpen}
        title="Подтвердить отмену турнира"
        description="Отмена завершит турнир для всех экранов. Сервер повторно проверит текущую ревизию."
        closeLabel="Закрыть подтверждение отмены"
        onOpenChange={setCancelDialogOpen}
        footer={
          <>
            <Button
              type="button"
              variant="secondary"
              disabled={submitting}
              onClick={() => setCancelDialogOpen(false)}
            >
              Не отменять
            </Button>
            <Button
              type="button"
              variant="danger"
              loading={submitting}
              loadingLabel="Отменяем"
              disabled={submitDisabled}
              onClick={() => {
                setCancelDialogOpen(false);
                void submit();
              }}
            >
              Подтвердить отмену
            </Button>
          </>
        }
      >
        <p className={styles.errorText}>
          Причина: {reason.trim()}. После ответа сервера будет загружен новый авторитетный снимок.
        </p>
      </Dialog>
    </Panel>
  );
};

TournamentOperatorActions.displayName = "TournamentOperatorActions";
