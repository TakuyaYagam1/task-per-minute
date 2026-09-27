"use client";

import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import {
  ApiError,
  createOperatorCommandIntent,
  operatorApi,
  type CorrectionEvidence,
  type OperatorCorrectionDraftRequest,
  type OperatorRecoverySnapshot,
} from "../../shared/api";
import { Button, Message, Status, TechnicalDetails } from "../../shared/ui";

import styles from "./TournamentResultCorrection.module.css";

type CorrectionCandidate = Readonly<{
  series: OperatorRecoverySnapshot["series"][number];
  game: OperatorRecoverySnapshot["series"][number]["slots"][number]["attempts"][number];
  slotPosition: number;
}>;

type CorrectionMessage = Readonly<{
  tone: "success" | "error" | "warning";
  title: string;
  body: string;
}>;

type TournamentResultCorrectionProps = Readonly<{
  snapshot: OperatorRecoverySnapshot;
  snapshotLoading: boolean;
  onAccepted: () => Promise<OperatorRecoverySnapshot>;
  participantName: (id: string) => string;
  matchName: (series: OperatorRecoverySnapshot["series"][number]) => string;
}>;

type GameResultReason = OperatorCorrectionDraftRequest["patch"]["reason"];
type CorrectionReason = OperatorCorrectionDraftRequest["reason"];

const terminalGameStates = new Set(["completed", "void", "cancelled"]);
const terminalTournamentStates = new Set(["completed", "cancelled"]);
const permanentRejectionCodes = new Set([
  "cutoff",
  "cutoff_wave_started",
  "cutoff_task_delivered",
  "cutoff_no_show_recorded",
  "cutoff_forfeit_recorded",
  "cutoff_golden_direct_allocated",
  "tournament_terminal",
]);
const digestPattern = /^[0-9a-f]{64}$/u;

const correctionCandidates = (snapshot: OperatorRecoverySnapshot): CorrectionCandidate[] =>
  snapshot.series.flatMap((series) => series.slots.flatMap((slot) => slot.attempts.flatMap((game) =>
    terminalGameStates.has(game.state) && game.result_revision_id !== null
      ? [{ series, game, slotPosition: slot.position }]
      : []
  )));

const candidateKey = (candidate: CorrectionCandidate): string =>
  `${candidate.series.id}:${candidate.game.id}`;

const resultReasonsFor = (state: CorrectionCandidate["game"]["state"]): readonly GameResultReason[] => {
  switch (state) {
    case "completed":
      return ["solved", "surrender", "operator_forfeit"];
    case "void":
      return ["no_solve", "task_failure", "common_platform_failure", "disconnect", "execution_epoch_break"];
    case "cancelled":
      return ["no_show", "series_cancelled", "tournament_cancelled"];
    default:
      return [];
  }
};

const defaultResultReason = (candidate: CorrectionCandidate): GameResultReason => {
  const options = resultReasonsFor(candidate.game.state);
  return options.find((reason) => reason !== candidate.game.result_reason) ?? options[0] ?? "operator_forfeit";
};

const defaultWinner = (candidate: CorrectionCandidate): string => {
  if (candidate.game.state !== "completed") {
    return "";
  }
  return candidate.game.winner_id === candidate.series.first_participant_id
    ? candidate.series.second_participant_id
    : candidate.series.first_participant_id;
};

const resultReasonLabel = (reason: GameResultReason): string => {
  switch (reason) {
    case "solved": return "Решение принято";
    case "surrender": return "Сдача участника";
    case "operator_forfeit": return "Техническое поражение";
    case "no_solve": return "Решение не найдено";
    case "task_failure": return "Ошибка задания";
    case "common_platform_failure": return "Сбой платформы";
    case "disconnect": return "Разрыв соединения";
    case "execution_epoch_break": return "Перезапуск игрового сервиса";
    case "no_show": return "Неявка";
    case "series_cancelled": return "Серия отменена";
    case "tournament_cancelled": return "Турнир отменен";
    case "derived_revision_superseded": return "Результат заменен";
  }
};

const correctionReasonLabel = (reason: CorrectionReason): string => {
  switch (reason) {
    case "scorekeeping_error": return "Ошибка подсчета";
    case "verified_submission": return "Проверенная отправка";
    case "operator_ruling": return "Решение оператора";
  }
};

const rejectionCode = (error: ApiError): string | null => {
  const problem = error.problem as (Record<string, unknown> & { code?: unknown }) | undefined;
  return typeof problem?.code === "string" ? problem.code : null;
};

const rejectionText = (code: string | null, status: number): string => {
  switch (code) {
    case "stale_projection":
      return "Данные изменились. Проверьте обновленный результат и подтвердите исправление еще раз.";
    case "stale_result":
      return "Этот результат уже исправлен. Показаны актуальные данные.";
    case "incomplete_projection":
      return "Не удалось получить все связанные результаты. Изменения не сохранены. Обновите данные.";
    case "incomplete_unlock":
      return "Не удалось подготовить связанные матчи к исправлению. Прежний результат сохранен.";
    case "cutoff_wave_started":
      return "Следующие матчи уже начались. Исправление недоступно. Приостановите турнир и изучите историю.";
    case "cutoff_task_delivered":
      return "Следующая задача уже выдана. Исправление недоступно. Приостановите турнир и изучите историю.";
    case "cutoff_no_show_recorded":
      return "Неявка уже зафиксирована. Исправление недоступно. Подробности есть в истории турнира.";
    case "cutoff_forfeit_recorded":
      return "Техническое поражение уже зафиксировано. Исправление недоступно. Подробности есть в истории турнира.";
    case "cutoff_golden_direct_allocated":
      return "Места золотого этапа уже распределены. Исправление недоступно. Подробности есть в истории турнира.";
    case "cutoff":
      return "Турнир уже перешел к следующим действиям. Исправление недоступно. Изучите историю турнира.";
    case "tournament_terminal":
      return "Завершенный или отмененный турнир нельзя исправить. История остается доступна.";
    default:
      return status === 409
        ? "Команда конфликтует с текущим состоянием. Сервер не применил частичных изменений."
        : `Сервер отклонил коррекцию (HTTP ${status}).`;
  }
};

const changedFields = (
  candidate: CorrectionCandidate,
  reason: GameResultReason,
  winnerId: string,
): OperatorCorrectionDraftRequest["fields"] => {
  const fields: OperatorCorrectionDraftRequest["fields"] = [];
  const nextWinner = candidate.game.state === "completed" ? winnerId : null;
  if (nextWinner !== candidate.game.winner_id) {
    fields.push("winner");
  }
  if (reason !== candidate.game.result_reason) {
    fields.push("result_reason");
  }
  if (reason === "solved" || candidate.game.result_reason === "solved") {
    fields.push("solve_metadata");
  }
  return fields;
};

export const TournamentResultCorrection = ({
  snapshot,
  snapshotLoading,
  onAccepted,
  participantName,
  matchName,
}: TournamentResultCorrectionProps) => {
  const candidates = useMemo(() => correctionCandidates(snapshot), [snapshot]);
  const [selectedKey, setSelectedKey] = useState("");
  const selected = candidates.find((candidate) => candidateKey(candidate) === selectedKey) ?? candidates[0] ?? null;
  const [resultReason, setResultReason] = useState<GameResultReason>("operator_forfeit");
  const [winnerId, setWinnerId] = useState("");
  const [correctionReason, setCorrectionReason] = useState<CorrectionReason>("scorekeeping_error");
  const [explanation, setExplanation] = useState("");
  const [solvedAt, setSolvedAt] = useState("");
  const [submissionId, setSubmissionId] = useState("");
  const [evidenceDigest, setEvidenceDigest] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [blockedCode, setBlockedCode] = useState<string | null>(null);
  const [message, setMessage] = useState<CorrectionMessage | null>(null);
  const submittingRef = useRef(false);

  useEffect(() => {
    if (selectedKey === "" || !candidates.some((candidate) => candidateKey(candidate) === selectedKey)) {
      setSelectedKey(candidates[0] === undefined ? "" : candidateKey(candidates[0]));
    }
  }, [candidates, selectedKey]);

  useEffect(() => {
    if (selected === null) {
      return;
    }
    setResultReason(defaultResultReason(selected));
    setWinnerId(defaultWinner(selected));
    setSolvedAt("");
    setSubmissionId("");
    setEvidenceDigest("");
    setConfirmed(false);
    setBlockedCode(null);
    setMessage(null);
  }, [selectedKey]); // eslint-disable-line react-hooks/exhaustive-deps -- reset only when the selected authority changes

  const fields = selected === null ? [] : changedFields(selected, resultReason, winnerId);
  const solveMetadataValid = resultReason !== "solved" || (
    solvedAt.length > 0 && submissionId.length > 0 && digestPattern.test(evidenceDigest)
  );
  const terminalTournament = terminalTournamentStates.has(snapshot.tournament.state);
  const permanentBlock = blockedCode !== null && permanentRejectionCodes.has(blockedCode);
  const clearTransientBlock = (): void => {
    setBlockedCode((code) => code !== null && permanentRejectionCodes.has(code) ? code : null);
  };
  const canSubmit = selected !== null && !snapshotLoading && !submitting && !terminalTournament &&
    blockedCode === null && confirmed && explanation.trim().length > 0 && fields.length > 0 &&
    (selected.game.state !== "completed" || winnerId.length > 0) && solveMetadataValid;

  const submit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    if (!canSubmit || selected === null || selected.game.result_revision_id === null || submittingRef.current) {
      return;
    }
    submittingRef.current = true;
    setSubmitting(true);
    setMessage(null);
    const intent = createOperatorCommandIntent();
    const solvedAtISO = resultReason === "solved" ? new Date(solvedAt).toISOString() : null;
    const draft: OperatorCorrectionDraftRequest = {
      confirmed: true,
      expected_projection_revision: snapshot.next_cursor.projection_revision,
      explanation: explanation.trim(),
      fields,
      patch: {
        reason: resultReason,
        solve_metadata: {
          evidence_digest: resultReason === "solved" ? evidenceDigest : "0".repeat(64),
          solved_at: solvedAtISO,
          submission_id: resultReason === "solved" ? submissionId : null,
        },
        state: selected.game.state,
        winner_id: selected.game.state === "completed" ? winnerId : null,
      },
      reason: correctionReason,
      source_result_revision: selected.game.result_revision_id,
    };

    try {
      const prepared = await operatorApi.preflightGameResultCorrection(
        snapshot.tournament.id,
        selected.series.id,
        selected.game.id,
        draft,
        intent,
      );
      const evidence: CorrectionEvidence = await operatorApi.correctGameResult(
        snapshot.tournament.id,
        selected.series.id,
        selected.game.id,
        prepared,
        intent,
      );
      await onAccepted();
      setConfirmed(false);
      setMessage({
        tone: "success",
        title: "Коррекция применена",
        body: `Новая проекция подтверждена. Заменено проекций: ${evidence.supersessions.length}, освобождено резервов: ${evidence.unlock_intents.length}.`,
      });
    } catch (error) {
      if (error instanceof ApiError) {
        const code = rejectionCode(error);
        if (error.status === 409) {
          setBlockedCode(code ?? "conflict");
          try {
            await onAccepted();
          } catch {
            // Preserve the rejection explanation if the follow-up snapshot is unavailable.
          }
          setMessage({ tone: "warning", title: "Коррекция отклонена", body: rejectionText(code, error.status) });
        } else {
          setMessage({ tone: "error", title: "Коррекция не выполнена", body: rejectionText(code, error.status) });
        }
      } else {
        setMessage({
          tone: "error",
          title: "Коррекция не выполнена",
          body: "Не удалось подтвердить команду. Проверьте соединение и повторите попытку.",
        });
      }
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  };

  return (
    <section className={styles.section} aria-labelledby="operator-correction-heading">
      <div className={styles.header}>
        <div>
          <h3 className={styles.title} id="operator-correction-heading">Исправить результат</h3>
          <p className={styles.hint}>
            Выберите матч, укажите правильный результат и объясните причину. Исправление сохранится в истории турнира.
          </p>
        </div>
        {candidates.length > 0 && (
          <Status tone={terminalTournament ? "warning" : snapshotLoading ? "loading" : "live"}>
            {terminalTournament ? "Турнир завершен" : snapshotLoading ? "Обновляем" : `Результаты: ${candidates.length}`}
          </Status>
        )}
      </div>

      {message && !permanentBlock && (
        <Message tone={message.tone} title={message.title}>
          <p className={styles.messageText}>{message.body}</p>
        </Message>
      )}

      {terminalTournament || permanentBlock ? (
        <Message tone="warning" title="Коррекция закрыта">
          <p className={styles.messageText}>
            {terminalTournament
              ? "В terminal tournament результат не меняется. Используйте журнал аудита и разрешенные terminal-команды."
              : rejectionText(blockedCode, 409)}
          </p>
        </Message>
      ) : candidates.length === 0 ? (
        <Message tone="info" title="Нет результата для коррекции">
          <p className={styles.messageText}>В текущем снимке нет завершенной попытки с официальной ревизией.</p>
        </Message>
      ) : selected !== null && (
        <form className={styles.form} onSubmit={(event) => void submit(event)}>
          <div className={styles.grid}>
            <div className={`${styles.field} ${styles.wide}`}>
              <label className={styles.label} htmlFor="operator-correction-game">Официальный результат</label>
              <select
                className={styles.select}
                id="operator-correction-game"
                value={candidateKey(selected)}
                onChange={(event) => setSelectedKey(event.target.value)}
                disabled={submitting}
              >
                {candidates.map((candidate) => (
                  <option key={candidateKey(candidate)} value={candidateKey(candidate)}>
                    {matchName(candidate.series)} - игра {candidate.slotPosition}, попытка {candidate.game.attempt_no}
                  </option>
                ))}
              </select>
            </div>

            <div className={styles.field}>
              <label className={styles.label} htmlFor="operator-correction-winner">Победитель</label>
              <select
                className={styles.select}
                id="operator-correction-winner"
                value={winnerId}
                onChange={(event) => { setWinnerId(event.target.value); clearTransientBlock(); }}
                disabled={submitting || selected.game.state !== "completed"}
              >
                {selected.game.state !== "completed" && <option value="">Победителя нет</option>}
                {[selected.series.first_participant_id, selected.series.second_participant_id].map((participantId) => (
                  <option key={participantId} value={participantId}>{participantName(participantId)}</option>
                ))}
              </select>
            </div>

            <div className={styles.field}>
              <label className={styles.label} htmlFor="operator-correction-result-reason">Новая причина результата</label>
              <select
                className={styles.select}
                id="operator-correction-result-reason"
                value={resultReason}
                onChange={(event) => { setResultReason(event.target.value as GameResultReason); clearTransientBlock(); }}
                disabled={submitting}
              >
                {resultReasonsFor(selected.game.state).map((reason) => (
                  <option key={reason} value={reason}>{resultReasonLabel(reason)}</option>
                ))}
              </select>
            </div>

            <div className={styles.field}>
              <label className={styles.label} htmlFor="operator-correction-reason">Основание коррекции</label>
              <select
                className={styles.select}
                id="operator-correction-reason"
                value={correctionReason}
                onChange={(event) => { setCorrectionReason(event.target.value as CorrectionReason); clearTransientBlock(); }}
                disabled={submitting}
              >
                {(["scorekeeping_error", "verified_submission", "operator_ruling"] as const).map((reason) => (
                  <option key={reason} value={reason}>{correctionReasonLabel(reason)}</option>
                ))}
              </select>
            </div>

            <div className={`${styles.field} ${styles.wide}`}>
              <label className={styles.label} htmlFor="operator-correction-explanation">Объяснение</label>
              <textarea
                className={styles.textarea}
                id="operator-correction-explanation"
                value={explanation}
                onChange={(event) => { setExplanation(event.target.value); clearTransientBlock(); }}
                disabled={submitting}
                maxLength={512}
                required
                placeholder="Укажите проверенный источник и причину исправления"
              />
            </div>

            {resultReason === "solved" && (
              <>
                <div className={styles.field}>
                  <label className={styles.label} htmlFor="operator-correction-solved-at">Время решения</label>
                  <input
                    className={styles.input}
                    id="operator-correction-solved-at"
                    type="datetime-local"
                    value={solvedAt}
                    onChange={(event) => { setSolvedAt(event.target.value); clearTransientBlock(); }}
                    disabled={submitting}
                    required
                  />
                </div>
                <div className={styles.field}>
                  <label className={styles.label} htmlFor="operator-correction-submission">ID подтвержденной отправки из журнала</label>
                  <input
                    className={styles.input}
                    id="operator-correction-submission"
                    value={submissionId}
                    onChange={(event) => { setSubmissionId(event.target.value.trim()); clearTransientBlock(); }}
                    disabled={submitting}
                    required
                    placeholder="Скопируйте из технических данных записи"
                  />
                </div>
                <div className={`${styles.field} ${styles.wide}`}>
                  <label className={styles.label} htmlFor="operator-correction-digest">SHA-256 доказательства</label>
                  <input
                    className={styles.input}
                    id="operator-correction-digest"
                    value={evidenceDigest}
                    onChange={(event) => { setEvidenceDigest(event.target.value.trim().toLowerCase()); clearTransientBlock(); }}
                    disabled={submitting}
                    minLength={64}
                    maxLength={64}
                    required
                    placeholder="Контрольная сумма из записи, 64 символа"
                  />
                </div>
              </>
            )}
          </div>

          <TechnicalDetails>
            <dl className={styles.evidence} aria-label="Данные исправляемого результата">
              <div className={styles.evidenceItem}>
                <dt className={styles.evidenceLabel}>ID исходного результата</dt>
                <dd className={styles.evidenceValue}>{selected.game.result_revision_id}</dd>
              </div>
              <div className={styles.evidenceItem}>
                <dt className={styles.evidenceLabel}>Версия данных</dt>
                <dd className={styles.evidenceValue}>{snapshot.next_cursor.projection_revision}</dd>
              </div>
              <div className={styles.evidenceItem}>
                <dt className={styles.evidenceLabel}>Изменяемые поля</dt>
                <dd className={styles.evidenceValue}>{fields.join(", ") || "Нет изменений"}</dd>
              </div>
            </dl>
          </TechnicalDetails>

          <label className={styles.checkRow} htmlFor="operator-correction-confirmed">
            <input
              className={styles.checkbox}
              id="operator-correction-confirmed"
              type="checkbox"
              checked={confirmed}
              onChange={(event) => setConfirmed(event.target.checked)}
              disabled={submitting}
            />
            <span>Подтверждаю коррекцию результата и атомарную перестройку зависимых проекций.</span>
          </label>

          <div className={styles.submitRow}>
            <Button type="submit" variant="danger" loading={submitting} loadingLabel="Проверяем" disabled={!canSubmit}>
              Подтвердить коррекцию
            </Button>
            <p className={styles.submitHint}>
              {blockedCode === null
                ? "Preflight не изменяет состояние. Финальная команда отправляет полный набор intents одной транзакцией."
                : "Измените форму или дождитесь нового авторитетного снимка перед повтором."}
            </p>
          </div>
        </form>
      )}
    </section>
  );
};
