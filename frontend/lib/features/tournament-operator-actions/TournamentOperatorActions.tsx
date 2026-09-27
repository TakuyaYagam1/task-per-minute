"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useParticipantNames } from "../../entities/tournament";

import {
  ApiError,
  adminApi,
  createOperatorCommandIntent,
  operatorApi,
  type OperatorForfeitGameExpectation,
  type OperatorForfeitRequest,
  type OperatorReplayRequest,
  type OperatorReserveRequest,
  type OperatorNoShowRequest,
  type OperatorRecoverySnapshot,
  type TournamentActionRequest,
  type WaveControlRequest,
} from "../../shared/api";
import { formatCategory, formatGameState, formatResultReason, formatSeriesState } from "../../shared/lib";
import { Button, Dialog, Message, Panel, Status } from "../../shared/ui";

import styles from "./TournamentOperatorActions.module.css";
import { TournamentResultCorrection } from "./TournamentResultCorrection";

type OperatorAction = "pause" | "resume" | "cancel" | "no-show" | "operator-forfeit";

type TournamentOperatorActionsProps = Readonly<{
  tournamentId: string;
}>;

type NoShowCandidate = Readonly<{
  series: OperatorRecoverySnapshot["series"][number];
  wave: OperatorRecoverySnapshot["waves"][number];
  window: NonNullable<OperatorRecoverySnapshot["waves"][number]["ready_window"]>;
}>;

type RecoveryControl = OperatorRecoverySnapshot["recovery_controls"][number];
type RecoveryReserveCandidate = NonNullable<RecoveryControl["reserve_exhausted"]>["candidates"][number];

const noShowCandidateKey = (candidate: NoShowCandidate): string =>
  `${candidate.wave.id}:${candidate.series.id}`;

const recoveryControlKey = (control: RecoveryControl): string =>
  `${control.kind}:${control.assignment_id}:${control.series_id}:${control.slot_id}`;

const recoveryCandidateKey = (candidate: RecoveryReserveCandidate): string =>
  `${candidate.task_id}:${candidate.version}`;

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

const recoveryReasonLabel = (value: string | null): string => {
  switch (value) {
    case "operator":
      return "Операторская пауза";
    case "disconnect":
      return "Разрыв соединения";
    case "platform":
      return "Сбой платформы";
    case "execution_epoch":
      return "Перезапуск игрового сервиса";
    default:
      return "Причина не указана";
  }
};

const gameReasonLabel = (value: string | null): string => {
  switch (value) {
    case "no_solve":
      return "Никто не решил задачу";
    case "task_failure":
      return "Ошибка задания";
    case "common_platform_failure":
      return "Сбой платформы";
    case "disconnect":
      return "Потеря соединения";
    case "execution_epoch_break":
      return "Перезапуск игрового сервиса";
    case null:
      return "Причина еще не зафиксирована";
    default:
      return formatResultReason(value);
  }
};

const recoveryKindLabel = (kind: RecoveryControl["kind"]): string =>
  kind === "replay" ? "Переиграть" : "Заменить задачу";

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
      return "Данные устарели. Состояние обновлено, проверьте команду еще раз.";
    }
    if (error.status > 0) {
      return `Команда отклонена (HTTP ${error.status}).`;
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
      return "Техническое поражение";
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
  const { participantName, matchName } = useParticipantNames(tournamentId, snapshot?.roster ?? null);
  const [taskNames, setTaskNames] = useState<ReadonlyMap<string, string>>(new Map());
  const hasReserveChoices = snapshot?.recovery_controls.some((control) =>
    (control.reserve_exhausted?.candidates.length ?? 0) > 0) ?? false;

  useEffect(() => {
    if (!hasReserveChoices) return;
    const controller = new AbortController();
    void adminApi.listTasks(controller.signal).then((tasks) => {
      if (!controller.signal.aborted) setTaskNames(new Map(tasks.map((task) => [task.id, task.title])));
    }).catch(() => {
      if (!controller.signal.aborted) setTaskNames(new Map());
    });
    return () => controller.abort();
  }, [hasReserveChoices, tournamentId]);
  const [snapshotLoading, setSnapshotLoading] = useState(true);
  const [snapshotError, setSnapshotError] = useState<string | null>(null);
  const [action, setAction] = useState<OperatorAction>("pause");
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [selectedNoShowKey, setSelectedNoShowKey] = useState("");
  const [selectedSeriesId, setSelectedSeriesId] = useState("");
  const [selectedParticipantId, setSelectedParticipantId] = useState("");
  const [ruleId, setRuleId] = useState("");
  const [evidenceText, setEvidenceText] = useState("");
  const [selectedRecoveryKey, setSelectedRecoveryKey] = useState("");
  const [recoveryReason, setRecoveryReason] = useState("");
  const [recoveryConfirmed, setRecoveryConfirmed] = useState(false);
  const [selectedReserveCandidateKey, setSelectedReserveCandidateKey] = useState("");
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
  const selectedNoShow = noShowCandidates.find((candidate) => noShowCandidateKey(candidate) === selectedNoShowKey) ??
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
  const recoveryControls = useMemo(
    () => snapshot?.recovery_controls ?? [],
    [snapshot],
  );
  const selectedRecoveryControl = recoveryControls.find(
    (control) => recoveryControlKey(control) === selectedRecoveryKey,
  ) ?? recoveryControls[0] ?? null;
  const reserveDetails = selectedRecoveryControl?.kind === "reserve_exhausted"
    ? selectedRecoveryControl.reserve_exhausted
    : null;
  const reserveCandidates = useMemo(
    () => reserveDetails?.candidates ?? [],
    [reserveDetails],
  );
  const selectedReserveCandidate = reserveCandidates.find(
    (candidate) => recoveryCandidateKey(candidate) === selectedReserveCandidateKey,
  ) ?? reserveCandidates[0] ?? null;

  useEffect(() => {
    if (selectedNoShowKey === "" || !noShowCandidates.some((candidate) => noShowCandidateKey(candidate) === selectedNoShowKey)) {
      setSelectedNoShowKey(noShowCandidates[0] === undefined ? "" : noShowCandidateKey(noShowCandidates[0]));
    }
  }, [noShowCandidates, selectedNoShowKey]);

  useEffect(() => {
    if (selectedSeriesId === "" || !forfeitSeries.some((series) => series.id === selectedSeriesId)) {
      setSelectedSeriesId(forfeitSeries[0]?.id ?? "");
    }
  }, [forfeitSeries, selectedSeriesId]);

  useEffect(() => {
    if (
      selectedRecoveryKey === "" ||
      !recoveryControls.some((control) => recoveryControlKey(control) === selectedRecoveryKey)
    ) {
      setSelectedRecoveryKey(
        recoveryControls[0] === undefined ? "" : recoveryControlKey(recoveryControls[0]),
      );
    }
  }, [recoveryControls, selectedRecoveryKey]);

  useEffect(() => {
    if (
      selectedReserveCandidateKey === "" ||
      !reserveCandidates.some((candidate) => recoveryCandidateKey(candidate) === selectedReserveCandidateKey)
    ) {
      setSelectedReserveCandidateKey(
        reserveCandidates[0] === undefined ? "" : recoveryCandidateKey(reserveCandidates[0]),
      );
    }
  }, [reserveCandidates, selectedReserveCandidateKey]);

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
  const recoveryActionAvailable = selectedRecoveryControl !== null && (
    selectedRecoveryControl.kind === "replay"
      ? selectedRecoveryControl.replay?.available === true
      : reserveDetails !== null && selectedReserveCandidate !== null
  );
  const recoverySubmitDisabled = snapshotLoading || snapshot === null || submitting ||
    !recoveryConfirmed || recoveryReason.trim().length === 0 || !recoveryActionAvailable;

  const submitRecovery = async (): Promise<void> => {
    if (
      submittingRef.current ||
      recoverySubmitDisabled ||
      snapshot === null ||
      selectedRecoveryControl === null
    ) {
      return;
    }

    submittingRef.current = true;
    setSubmitting(true);
    setActionMessage(null);
    const trimmedReason = recoveryReason.trim();
    const intent = createOperatorCommandIntent();
    const failedAttempt = selectedRecoveryControl.attempts.at(-1);

    try {
      if (selectedRecoveryControl.kind === "replay") {
        const replay = selectedRecoveryControl.replay;
        if (replay === null || !replay.available || failedAttempt === undefined) {
          setActionMessage({
            tone: "warning",
            title: "Replay недоступен",
            body: "Переигровка этой попытки больше недоступна. Обновите данные.",
          });
          return;
        }
        const body: OperatorReplayRequest = {
          assignment_attempt_id: newUUID(),
          assignment_id: selectedRecoveryControl.assignment_id,
          confirmed: true,
          expected_authority_revision: selectedRecoveryControl.expected_authority_revision,
          expected_closure_revision_id: replay.expected_closure_revision_id,
          failed_game_id: failedAttempt.id,
          old_wave_id: selectedRecoveryControl.old_wave_id,
          ready_window_id: newUUID(),
          ready_window_revision_id: newUUID(),
          reason: trimmedReason,
          replacement_game_id: newUUID(),
          replacement_wave_id: newUUID(),
          replacement_wave_revision_id: newUUID(),
          series_id: selectedRecoveryControl.series_id,
          slot_id: selectedRecoveryControl.slot_id,
          tournament_id: tournamentId,
        };
        await operatorApi.replayGame(
          tournamentId,
          selectedRecoveryControl.series_id,
          failedAttempt.id,
          body,
          intent,
        );
      } else {
        if (reserveDetails === null || selectedReserveCandidate === null || failedAttempt === undefined) {
          setActionMessage({
            tone: "warning",
            title: "Резерв недоступен",
            body: "Для этого назначения нет подходящего кандидата.",
          });
          return;
        }
        const body: OperatorReserveRequest = {
          assignment_attempt_id: failedAttempt.id,
          assignment_id: selectedRecoveryControl.assignment_id,
          confirmed: true,
          evidence_id: newUUID(),
          expected_artifact_revision: reserveDetails.expected_artifact_revision,
          expected_artifact_revision_id: reserveDetails.expected_artifact_revision_id,
          expected_assignment_revision: reserveDetails.expected_assignment_revision,
          expected_authority_revision: selectedRecoveryControl.expected_authority_revision,
          expected_category_revision: reserveDetails.expected_category_revision,
          expected_category_revision_id: reserveDetails.expected_category_revision_id,
          expected_exhaustion_command_id: reserveDetails.expected_exhaustion_command_id,
          expected_history_revision: reserveDetails.expected_history_revision,
          expected_history_revision_id: reserveDetails.expected_history_revision_id,
          expected_pool_revision: reserveDetails.expected_pool_revision,
          expected_pool_revision_id: reserveDetails.expected_pool_revision_id,
          expected_reservation_revision: reserveDetails.expected_reservation_revision,
          expected_reservation_revision_id: reserveDetails.expected_reservation_revision_id,
          expected_snapshot_id: reserveDetails.expected_snapshot_id,
          old_wave_id: selectedRecoveryControl.old_wave_id,
          proposed_snapshot_id: newUUID(),
          proposed_task_id: selectedReserveCandidate.task_id,
          proposed_version: selectedReserveCandidate.version,
          reason: trimmedReason,
          series_id: selectedRecoveryControl.series_id,
          slot_id: selectedRecoveryControl.slot_id,
          tournament_id: tournamentId,
        };
        await operatorApi.assignReserve(
          tournamentId,
          selectedRecoveryControl.series_id,
          selectedRecoveryControl.assignment_id,
          body,
          intent,
        );
      }

      await loadSnapshot();
      setActionMessage({
        tone: "success",
        title: "Восстановление подтверждено",
        body: "Состояние обновлено. Новая попытка появится после подтверждения.",
      });
      setRecoveryConfirmed(false);
      setRecoveryReason("");
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
          title: "Данные устарели",
          body: "Другой оператор изменил восстановление. Данные обновлены, проверьте доступный вариант перед повтором.",
        });
        return;
      }
      setActionMessage({ tone: "error", title: "Восстановление не выполнено", body: errorText(error) });
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  };

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
            body: "Укажите rule_id и хотя бы один evidence ID.",
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
        body: "Действие выполнено. Данные турнира обновлены.",
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
          title: "Данные устарели",
          body: "Другой оператор изменил состояние. Данные обновлены, проверьте команду перед повтором.",
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
      <Panel title="Управление турниром" description="Загружаем состояние турнира.">
        <div className={styles.loadingState} role="status" aria-live="polite">
          <Status tone="loading">Загружаем данные</Status>
          <p className={styles.errorText}>Доступные действия появятся после загрузки.</p>
        </div>
      </Panel>
    );
  }

  if (snapshot === null) {
    return (
      <Panel title="Управление турниром" description="Для управления нужны актуальные данные турнира.">
        <Message tone="error" title="Не удалось загрузить турнир">
          <p className={styles.errorText}>{snapshotError ?? "Не удалось получить состояние турнира."}</p>
          <Button size="small" variant="secondary" onClick={() => void loadSnapshot()}>
            Загрузить еще раз
          </Button>
        </Message>
      </Panel>
    );
  }

  const actionHint = action === "operator-forfeit"
    ? "Засчитайте игроку техническое поражение за нарушение. Укажите правило и подтверждающие записи."
    : action === "no-show"
      ? "Неявку можно зафиксировать, когда время подтверждения готовности истекло и хотя бы один игрок не готов."
      : "Выберите действие и укажите причину. Она сохранится в истории турнира.";

  return (
    <Panel
      title="Управление турниром"
      description="Приостановите турнир, продолжите его или разберите спорную ситуацию."
      className={styles.panel}
    >
      <div className={styles.header}>
        <p className={styles.intro}>
          {snapshot.tournament.name}
        </p>
        <Status tone={snapshotLoading ? "loading" : "live"}>
          {snapshotLoading ? "Обновляем" : "Данные обновлены"}
        </Status>
      </div>

      <dl className={styles.snapshot} aria-label="Состояние турнира">
        <div className={styles.snapshotItem}>
          <dt className={styles.snapshotLabel}>Состояние</dt>
          <dd className={styles.snapshotValue}>{stateLabel(snapshot.tournament.state)}</dd>
        </div>
      </dl>

      {snapshotError && (
        <Message tone="warning" title="Данные требуют внимания">
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
              Действие
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
                Матч с неявкой
              </label>
              <select
                className={styles.select}
                id="operator-no-show-wave"
                value={selectedNoShow === null ? "" : noShowCandidateKey(selectedNoShow)}
                onChange={(event) => setSelectedNoShowKey(event.target.value)}
                disabled={submitting || noShowCandidates.length === 0}
              >
                {noShowCandidates.map((candidate) => (
                  <option key={noShowCandidateKey(candidate)} value={noShowCandidateKey(candidate)}>
                    {matchName(candidate.series)} - волна {snapshot.waves.findIndex((wave) => wave.id === candidate.wave.id) + 1}
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
                  Матч
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
                      {matchName(series)} ({formatSeriesState(series.state)})
                    </option>
                  ))}
                </select>
              </div>
              <div className={styles.field}>
                <label className={styles.label} htmlFor="operator-forfeit-participant">
                  Кому засчитать поражение
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
                      <option key={participantId} value={participantId}>{participantName(participantId)}</option>
                    ))}
                </select>
                <p className={styles.hint}>
                  {selectedForfeitAttempt ? `Попытка ${selectedForfeitAttempt.attempt_no}: ${formatGameState(selectedForfeitAttempt.state)}.` : "Активная попытка не найдена."}
                </p>
              </div>
              <div className={styles.field}>
                <label className={styles.label} htmlFor="operator-rule-id">Нарушенное правило</label>
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
                  ID подтверждающих записей из журнала
                </label>
                <textarea
                  className={styles.textarea}
                  id="operator-evidence-ids"
                  value={evidenceText}
                  onChange={(event) => setEvidenceText(event.target.value)}
                  disabled={submitting}
                  placeholder="Один UUID на строку"
                />
                <p className={styles.hint}>Укажите хотя бы один уникальный UUID доказательства.</p>
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
              ? "Игроку будет засчитано техническое поражение. Проверьте выбранного игрока и причину."
              : "Проверьте выбранное действие и причину перед подтверждением."}
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
          <span>Подтверждаю, что команда соответствует актуальным данным турнира.</span>
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
            {selectedActionAvailable ? "Действие сохранится в истории турнира." : "Действие сейчас недоступно."}
          </p>
        </div>
      </form>

      <TournamentResultCorrection
        participantName={participantName}
        matchName={matchName}
        snapshot={snapshot}
        snapshotLoading={snapshotLoading}
        onAccepted={loadSnapshot}
      />

      <section className={styles.recoverySection} aria-labelledby="operator-recovery-heading">
        <div className={styles.recoveryHeader}>
          <div>
            <h3 className={styles.recoveryTitle} id="operator-recovery-heading">
              Восстановление игр
            </h3>
            <p className={styles.hint}>
              Переиграйте матч после сбоя или выберите резервную задачу. Здесь показаны доступные для текущей ситуации действия.
            </p>
          </div>
          {recoveryControls.length > 0 && (
            <Status tone={snapshotLoading ? "loading" : "warning"}>
              {snapshotLoading ? "Обновляем" : `Доступно действий: ${recoveryControls.length}`}
            </Status>
          )}
        </div>

        {recoveryControls.length === 0 ? (
          <Message tone="info" title="Восстановление не требуется">
            <p className={styles.errorText}>
              Сейчас нет игр, которым нужна переигровка или замена задачи.
            </p>
          </Message>
        ) : (
          <form
            className={styles.recoveryForm}
            onSubmit={(event) => {
              event.preventDefault();
              void submitRecovery();
            }}
          >
            <fieldset className={styles.recoveryChoices}>
              <legend className={styles.label}>Доступные действия</legend>
              <div className={styles.recoveryChoiceList}>
                {recoveryControls.map((control) => {
                  const controlKey = recoveryControlKey(control);
                  return (
                    <label className={styles.recoveryChoice} key={controlKey}>
                      <input
                        className={styles.radio}
                        type="radio"
                        name="operator-recovery-control"
                        value={controlKey}
                        checked={selectedRecoveryControl !== null && recoveryControlKey(selectedRecoveryControl) === controlKey}
                        onChange={() => setSelectedRecoveryKey(controlKey)}
                        disabled={submitting}
                      />
                      <span>
                        <strong>{recoveryKindLabel(control.kind)}</strong>
                        <span className={styles.recoveryChoiceMeta}>
                          {formatCategory(control.category)} - {(() => {
                            const series = snapshot.series.find((item) => item.id === control.series_id);
                            const slot = series?.slots.find((item) => item.id === control.slot_id);
                            return series ? `${matchName(series)}${slot ? `, игра ${slot.position}` : ""}` : "Матч недоступен";
                          })()}
                        </span>
                      </span>
                    </label>
                  );
                })}
              </div>
            </fieldset>

            {selectedRecoveryControl !== null && (
              <div className={styles.recoveryDetails}>
                <dl className={styles.recoveryMeta} aria-label="Доказательства восстановления">
                  <div className={styles.recoveryMetaItem}>
                    <dt className={styles.snapshotLabel}>Причина остановки</dt>
                    <dd className={styles.snapshotValue}>{gameReasonLabel(selectedRecoveryControl.reason)}</dd>
                  </div>
                  {selectedRecoveryControl.pause_reason !== null && (
                    <div className={styles.recoveryMetaItem}>
                      <dt className={styles.snapshotLabel}>Причина паузы</dt>
                      <dd className={styles.snapshotValue}>{recoveryReasonLabel(selectedRecoveryControl.pause_reason)}</dd>
                    </div>
                  )}
                  <div className={styles.recoveryMetaItem}>
                    <dt className={styles.snapshotLabel}>Категория</dt>
                    <dd className={styles.snapshotValue}>{formatCategory(selectedRecoveryControl.category)}</dd>
                  </div>
                </dl>

                <div className={styles.attemptPanel}>
                  <h4 className={styles.attemptTitle}>Попытки по порядку</h4>
                  <ol className={styles.attemptList} aria-label="Упорядоченные попытки игры">
                    {selectedRecoveryControl.attempts.map((attempt) => (
                      <li className={styles.attemptItem} key={attempt.id}>
                        <span className={styles.attemptNumber}>Попытка {attempt.attempt_no}</span>
                        <span>{gameReasonLabel(attempt.result_reason)}</span>
                        <span className={styles.attemptState}>{formatGameState(attempt.state)}</span>
                      </li>
                    ))}
                  </ol>
                </div>

                {selectedRecoveryControl.kind === "replay" && selectedRecoveryControl.replay?.available !== true && (
                  <Message tone="warning" title="Переигровка недоступна">
                    <p className={styles.errorText}>
                      Состояние игры изменилось. Обновите данные, чтобы увидеть доступные действия.
                    </p>
                  </Message>
                )}

                {selectedRecoveryControl.kind === "reserve_exhausted" && reserveDetails !== null && (
                  reserveCandidates.length === 0 ? (
                    <Message tone="warning" title="Резерв исчерпан">
                      <p className={styles.errorText}>
                        Подходящих кандидатов нет. Задачу и категорию нельзя указать вручную.
                      </p>
                    </Message>
                  ) : (
                    <div className={styles.field}>
                      <label className={styles.label} htmlFor="operator-recovery-candidate">
                        Задача для замены
                      </label>
                      <select
                        className={styles.select}
                        id="operator-recovery-candidate"
                        value={selectedReserveCandidate === null ? "" : recoveryCandidateKey(selectedReserveCandidate)}
                        onChange={(event) => setSelectedReserveCandidateKey(event.target.value)}
                        disabled={submitting}
                      >
                        {reserveCandidates.map((candidate, index) => (
                          <option key={recoveryCandidateKey(candidate)} value={recoveryCandidateKey(candidate)}>
                            {taskNames.get(candidate.task_id) ?? `Резервная задача ${index + 1}`} (версия {candidate.version})
                          </option>
                        ))}
                      </select>
                      <p className={styles.hint}>
                        Можно выбрать только доступную резервную задачу. Категория: {formatCategory(selectedRecoveryControl.category)}.
                      </p>
                    </div>
                  )
                )}

                <div className={styles.field}>
                  <label className={styles.label} htmlFor="operator-recovery-reason">
                    Причина восстановления
                  </label>
                  <textarea
                    className={styles.textarea}
                    id="operator-recovery-reason"
                    value={recoveryReason}
                    onChange={(event) => setRecoveryReason(event.target.value)}
                    disabled={submitting}
                    maxLength={512}
                    required
                    placeholder="Коротко опишите подтвержденную причину"
                  />
                </div>

                <label className={styles.checkRow} htmlFor="operator-recovery-confirmed">
                  <input
                    className={styles.checkbox}
                    id="operator-recovery-confirmed"
                    type="checkbox"
                    checked={recoveryConfirmed}
                    onChange={(event) => setRecoveryConfirmed(event.target.checked)}
                    disabled={submitting}
                  />
                  <span>Я проверил причину остановки и подтверждаю выбранное действие.</span>
                </label>

                <div className={styles.submitRow}>
                  <Button
                    type="submit"
                    variant="primary"
                    loading={submitting}
                    loadingLabel="Восстанавливаем"
                    disabled={recoverySubmitDisabled}
                  >
                    {selectedRecoveryControl.kind === "replay" ? "Повторить игру" : "Назначить резерв"}
                  </Button>
                  <p className={styles.submitHint}>
                    {recoveryActionAvailable
                      ? "Результат появится после выполнения действия."
                      : "Действие сейчас недоступно. Обновите данные."}
                  </p>
                </div>
              </div>
            )}
          </form>
        )}
      </section>

      <Dialog
        open={cancelDialogOpen}
        title="Подтвердить отмену турнира"
        description="Отмена завершит турнир для всех игроков. Продолжить отмененный турнир нельзя."
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
          Причина: {reason.trim()}. После выполнения загрузятся свежие данные.
        </p>
      </Dialog>
    </Panel>
  );
};

TournamentOperatorActions.displayName = "TournamentOperatorActions";
