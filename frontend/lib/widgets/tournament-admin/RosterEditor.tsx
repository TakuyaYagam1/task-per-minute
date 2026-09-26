"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  adminApi,
  ApiError,
  createOperatorCommandIntent,
  operatorApi,
  type AdminPlayer,
  type PreflightReport,
  type Roster,
  type ReplaceRosterRequest,
  type Tournament,
} from "../../shared/api";
import { formatTournamentState } from "../../shared/lib";
import { Button, Message, Panel, Status } from "../../shared/ui";

import styles from "./RosterEditor.module.css";

type RosterEditorProps = Readonly<{
  tournaments: readonly Tournament[];
  selectedTournament: Tournament | null;
  selectedTournamentId: string;
  onSelectTournament: (id: string) => void;
  onReloadTournaments: () => Promise<void>;
  onSessionExpired?: () => void;
  onDirtyChange?: (dirty: boolean) => void;
  showTournamentChooser?: boolean;
}>;

type LoadState = "loading" | "ready" | "error";
type PreflightState = "idle" | "loading" | "ready" | "error";
type RosterParticipant = Roster["participants"][number];
type RosterParticipantInput = ReplaceRosterRequest["participants"][number];
type AttendanceState = RosterParticipantInput["attendance"];

type DraftParticipant = Readonly<{
  id: string;
  participantId: string | null;
  playerId: string;
  seed: string;
  attendance: AttendanceState;
}>;

const MIN_ROSTER_SIZE = 4;
const MAX_ROSTER_SIZE = 16;
const MAX_UNLOCK_REASON_LENGTH = 512;

const ATTENDANCE_LABELS: Readonly<Record<AttendanceState, string>> = {
  invited: "Приглашен",
  registered: "Зарегистрирован",
  checked_in: "На месте",
  withdrawn: "Отказался",
};

const ATTENDANCE_STATES: readonly AttendanceState[] = [
  "invited",
  "registered",
  "checked_in",
  "withdrawn",
];

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const draftFromParticipant = (participant: RosterParticipant): DraftParticipant => ({
  id: `server-${participant.id}`,
  participantId: participant.id,
  playerId: participant.player_id,
  seed: String(participant.seed),
  attendance: participant.attendance,
});

const draftFromRoster = (roster: Roster): DraftParticipant[] =>
  roster.participants.map(draftFromParticipant);

const toRosterInput = (
  participant: DraftParticipant,
): RosterParticipantInput => ({
  attendance: participant.attendance,
  player_id: participant.playerId,
  seed: Number(participant.seed),
});

const rosterErrorMessage = (error: unknown, fallback: string): string => {
  if (error instanceof ApiError) {
    return error.problem?.detail || error.problem?.title || fallback;
  }
  return fallback;
};

export const RosterEditor = ({
  onReloadTournaments,
  onSessionExpired,
  onSelectTournament,
  selectedTournament,
  selectedTournamentId,
  tournaments,
  onDirtyChange,
  showTournamentChooser = true,
}: RosterEditorProps) => {
  const tournamentId = selectedTournament?.id ?? "";
  const rosterControllerRef = useRef<AbortController | null>(null);
  const preflightControllerRef = useRef<AbortController | null>(null);
  const rosterLoadRef = useRef(0);
  const preflightRunRef = useRef(0);
  const draftIdRef = useRef(0);
  const [players, setPlayers] = useState<AdminPlayer[]>([]);
  const [roster, setRoster] = useState<Roster | null>(null);
  const [draftParticipants, setDraftParticipants] = useState<DraftParticipant[]>([]);
  const [rosterState, setRosterState] = useState<LoadState>("ready");
  const [rosterError, setRosterError] = useState<string | null>(null);
  const [rosterNotice, setRosterNotice] = useState<string | null>(null);
  const [savingRoster, setSavingRoster] = useState(false);
  const [preflightState, setPreflightState] = useState<PreflightState>("idle");
  const [preflightReport, setPreflightReport] = useState<PreflightReport | null>(null);
  const [preflightProjectionRevision, setPreflightProjectionRevision] = useState<number | null>(null);
  const [preflightError, setPreflightError] = useState<string | null>(null);
  const [controlError, setControlError] = useState<string | null>(null);
  const [lockingRoster, setLockingRoster] = useState(false);
  const [unlockingRoster, setUnlockingRoster] = useState(false);
  const [unlockConfirmed, setUnlockConfirmed] = useState(false);
  const [unlockReason, setUnlockReason] = useState("");

  useEffect(() => {
    if (!selectedTournamentId || !roster) {
      onDirtyChange?.(false);
      return;
    }
    const draftChanged =
      JSON.stringify(draftFromRoster(roster)) !== JSON.stringify(draftParticipants);
    onDirtyChange?.(draftChanged || unlockConfirmed || Boolean(unlockReason.trim()));
  }, [
    draftParticipants,
    onDirtyChange,
    roster,
    selectedTournamentId,
    unlockConfirmed,
    unlockReason,
  ]);

  const resetPreflight = useCallback((): void => {
    preflightControllerRef.current?.abort();
    preflightControllerRef.current = null;
    preflightRunRef.current += 1;
    setPreflightState("idle");
    setPreflightReport(null);
    setPreflightProjectionRevision(null);
    setPreflightError(null);
  }, []);

  const loadRoster = useCallback(async (id: string): Promise<void> => {
    rosterControllerRef.current?.abort();
    resetPreflight();
    const controller = new AbortController();
    const loadId = rosterLoadRef.current + 1;
    rosterLoadRef.current = loadId;
    rosterControllerRef.current = controller;
    setRosterState("loading");
    setRosterError(null);
    setRosterNotice(null);
    setControlError(null);
    setUnlockConfirmed(false);
    setUnlockReason("");

    try {
      const [activePlayers, currentRoster] = await Promise.all([
        adminApi.listPlayers(false, controller.signal),
        operatorApi.getRoster(id, controller.signal),
    ]);
      if (
        controller.signal.aborted ||
        rosterLoadRef.current !== loadId
      ) {
        return;
      }
      setPlayers(activePlayers);
      setRoster(currentRoster);
      setDraftParticipants(draftFromRoster(currentRoster));
      setRosterState("ready");
    } catch (error) {
      if (
        controller.signal.aborted ||
        rosterLoadRef.current !== loadId ||
        isAbortError(error)
      ) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      setRosterState("error");
      setRosterError(
        rosterErrorMessage(error, "Не удалось загрузить состав турнира"),
      );
    } finally {
      if (rosterControllerRef.current === controller) {
        rosterControllerRef.current = null;
      }
    }
  }, [onSessionExpired, resetPreflight]);

  useEffect(() => {
    if (!tournamentId) {
      rosterControllerRef.current?.abort();
      resetPreflight();
      rosterLoadRef.current += 1;
      setPlayers([]);
      setRoster(null);
      setDraftParticipants([]);
      setRosterState("ready");
      setRosterError(null);
      setRosterNotice(null);
      setControlError(null);
      setUnlockConfirmed(false);
      setUnlockReason("");
      return;
    }
    void loadRoster(tournamentId);
    return () => {
      rosterControllerRef.current?.abort();
    };
  }, [loadRoster, resetPreflight, tournamentId]);

  useEffect(() => () => {
    rosterControllerRef.current?.abort();
    preflightControllerRef.current?.abort();
  }, []);

  useEffect(() => {
    resetPreflight();
    setControlError(null);
    setUnlockConfirmed(false);
    setUnlockReason("");
  }, [resetPreflight, selectedTournament?.revision, selectedTournamentId]);

  const rosterEditingLocked = Boolean(
    roster?.locked || roster?.execution_started,
  );

  const playerById = useMemo(
    () => new Map(players.map((player) => [player.id, player])),
    [players],
  );
  const participantCount = draftParticipants.length;

  const updateDraftParticipant = (
    participantId: string,
    patch: Partial<Pick<DraftParticipant, "attendance" | "seed">>,
  ): void => {
    if (rosterEditingLocked) {
      return;
    }
    setDraftParticipants((current) =>
      current.map((participant) =>
        participant.id === participantId ? { ...participant, ...patch } : participant,
      ),
    );
    resetPreflight();
    setRosterError(null);
    setRosterNotice(null);
    setControlError(null);
  };

  const updateDraftPlayer = (participantId: string, playerId: string): void => {
    if (rosterEditingLocked) {
      return;
    }
    if (
      draftParticipants.some(
        (participant) =>
          participant.id !== participantId && participant.playerId === playerId,
      )
    ) {
      setRosterError("Этот игрок уже добавлен в состав. Выберите другого.");
      return;
    }
    setDraftParticipants((current) =>
      current.map((participant) =>
        participant.id === participantId
          ? { ...participant, playerId }
          : participant,
      ),
    );
    resetPreflight();
    setRosterError(null);
    setRosterNotice(null);
    setControlError(null);
  };

  const handleAddParticipant = (): void => {
    if (rosterEditingLocked || savingRoster) {
      return;
    }
    if (draftParticipants.length >= MAX_ROSTER_SIZE) {
      setRosterError("Нельзя добавить больше 16 участников в один состав.");
      return;
    }
    const selectedPlayerIds = new Set(
      draftParticipants
        .map((participant) => participant.playerId)
        .filter(Boolean),
    );
    const availablePlayer = players.find(
      (player) => !selectedPlayerIds.has(player.id),
    );
    if (!availablePlayer) {
      setRosterError("Нет свободных активных игроков для добавления.");
      return;
    }
    const selectedSeeds = new Set(
      draftParticipants
        .map((participant) => Number(participant.seed))
        .filter((seed) => Number.isSafeInteger(seed) && seed > 0),
    );
    const nextParticipantCount = draftParticipants.length + 1;
    const nextSeed = Array.from(
      { length: nextParticipantCount },
      (_, index) => index + 1,
    ).find((seed) => !selectedSeeds.has(seed)) ?? nextParticipantCount;
    draftIdRef.current += 1;
    setDraftParticipants((current) => [
      ...current,
      {
        id: `new-${draftIdRef.current}`,
        participantId: null,
        playerId: availablePlayer.id,
        seed: String(nextSeed),
        attendance: "invited",
      },
      ]);
    resetPreflight();
    setRosterError(null);
    setRosterNotice(null);
    setControlError(null);
  };

  const handleRemoveParticipant = (participantId: string): void => {
    if (rosterEditingLocked || savingRoster) {
      return;
    }
    setDraftParticipants((current) =>
      current.filter((participant) => participant.id !== participantId),
    );
    resetPreflight();
    setRosterError(null);
    setRosterNotice(null);
    setControlError(null);
  };

  const handleSaveRoster = async (): Promise<void> => {
    if (
      !selectedTournament ||
      !roster ||
      rosterEditingLocked ||
      savingRoster
    ) {
      return;
    }
    if (draftParticipants.length > MAX_ROSTER_SIZE) {
      setRosterError("Нельзя сохранить больше 16 участников в одном составе.");
      return;
    }
    if (draftParticipants.length < MIN_ROSTER_SIZE) {
      setRosterError("В составе должно быть не менее 4 участников.");
      return;
    }
    if (draftParticipants.some((participant) => !participant.playerId)) {
      setRosterError("Выберите игрока для каждой строки состава.");
      return;
    }
    const playerIds = draftParticipants.map((participant) => participant.playerId);
    if (new Set(playerIds).size !== playerIds.length) {
      setRosterError("В составе не должно быть повторяющихся игроков.");
      return;
    }
    const seeds = draftParticipants.map((participant) => Number(participant.seed));
    if (
      seeds.some(
        (seed) =>
          !Number.isSafeInteger(seed) ||
          seed < 1 ||
          seed > participantCount,
      )
    ) {
      setRosterError(
        `Позиция должна быть целым числом от 1 до ${participantCount}.`,
      );
      return;
    }
    if (new Set(seeds).size !== seeds.length) {
      setRosterError("Позиции участников не должны повторяться.");
      return;
    }

    setSavingRoster(true);
    resetPreflight();
    setRosterError(null);
    setRosterNotice(null);
    setControlError(null);
    try {
      const savedRoster = await operatorApi.replaceRoster(
        selectedTournament.id,
        {
          expected_projection_revision: selectedTournament.revision,
          participants: draftParticipants.map(toRosterInput),
        },
        createOperatorCommandIntent(),
      );
      setRoster(savedRoster);
      setDraftParticipants(draftFromRoster(savedRoster));
      resetPreflight();
      await onReloadTournaments();
      setRosterNotice("Состав сохранен. Серверный порядок и идентификаторы обновлены.");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
        resetPreflight();
        setRosterError(
          `${error.problem?.detail || "Серверное состояние изменилось."} Ваши изменения сохранены в форме. Перезагрузите данные перед новой попыткой.`,
        );
      } else {
        setRosterError(
          rosterErrorMessage(error, "Не удалось сохранить состав турнира"),
        );
      }
    } finally {
      setSavingRoster(false);
    }
  };

  const handleReloadRoster = (): void => {
    if (!tournamentId || savingRoster || lockingRoster || unlockingRoster) {
      return;
    }
    void loadRoster(tournamentId);
    void onReloadTournaments();
  };

  const checkedInPlayerIds = useMemo(
    () =>
      draftParticipants
        .filter((participant) => participant.attendance === "checked_in")
        .map((participant) => participant.playerId),
    [draftParticipants],
  );
  const canLockRoster = Boolean(
    selectedTournament &&
      roster &&
      !rosterEditingLocked &&
      preflightState === "ready" &&
      preflightReport?.passed &&
      preflightReport.tournament_id === selectedTournament.id &&
      preflightProjectionRevision !== null &&
      checkedInPlayerIds.length >= MIN_ROSTER_SIZE &&
      !savingRoster &&
      !lockingRoster &&
      !unlockingRoster,
  );

  const handleRunPreflight = async (): Promise<void> => {
    if (
      !selectedTournament ||
      !roster ||
      rosterEditingLocked ||
      savingRoster ||
      lockingRoster ||
      unlockingRoster ||
      preflightState === "loading"
    ) {
      return;
    }

    preflightControllerRef.current?.abort();
    const controller = new AbortController();
    const runId = preflightRunRef.current + 1;
    preflightRunRef.current = runId;
    preflightControllerRef.current = controller;
    setPreflightState("loading");
    setPreflightReport(null);
    setPreflightProjectionRevision(null);
    setPreflightError(null);
    setControlError(null);
    try {
      const snapshot = await operatorApi.getSnapshot(
        selectedTournament.id,
        undefined,
        controller.signal,
      );
      const projectionRevision = snapshot.next_cursor.projection_revision;
      const report = await operatorApi.runRosterPreflight(
        selectedTournament.id,
        { expected_projection_revision: projectionRevision },
        createOperatorCommandIntent(),
        controller.signal,
      );
      if (
        controller.signal.aborted ||
        preflightRunRef.current !== runId
      ) {
        return;
      }
      setPreflightReport(report);
      setPreflightProjectionRevision(projectionRevision);
      setPreflightState("ready");
    } catch (error) {
      if (
        controller.signal.aborted ||
        preflightRunRef.current !== runId ||
        isAbortError(error)
      ) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      setPreflightReport(null);
      setPreflightProjectionRevision(null);
      setPreflightState("error");
      setPreflightError(
        error instanceof ApiError && error.status === 409
          ? `${rosterErrorMessage(error, "Серверное состояние изменилось.")} Обновите данные и повторите проверку.`
          : rosterErrorMessage(error, "Не удалось выполнить предполетную проверку состава"),
      );
    } finally {
      if (preflightControllerRef.current === controller) {
        preflightControllerRef.current = null;
      }
    }
  };

  const handleLockRoster = async (): Promise<void> => {
    if (
      !selectedTournament ||
      !roster ||
      rosterEditingLocked ||
      savingRoster ||
      lockingRoster ||
      unlockingRoster ||
      preflightState !== "ready" ||
      !preflightReport?.passed ||
      preflightProjectionRevision === null
    ) {
      return;
    }
    if (checkedInPlayerIds.length < MIN_ROSTER_SIZE) {
      setControlError("Для блокировки состава отметьте минимум 4 игроков как присутствующих.");
      return;
    }

    setLockingRoster(true);
    setControlError(null);
    setRosterNotice(null);
    try {
      const lockedRoster = await operatorApi.lockRoster(
        selectedTournament.id,
        {
          expected_projection_revision: preflightProjectionRevision,
          preflight_revision_id: preflightReport.id,
          checked_in_player_ids: checkedInPlayerIds,
        },
        createOperatorCommandIntent(),
      );
      setRoster(lockedRoster);
      setDraftParticipants(draftFromRoster(lockedRoster));
      resetPreflight();
      setUnlockConfirmed(false);
      setUnlockReason("");
      await onReloadTournaments();
      setRosterNotice("Состав заблокирован. Исполнение можно начинать только после успешной проверки.");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
        resetPreflight();
      }
      setControlError(
        error instanceof ApiError && error.status === 409
          ? `${rosterErrorMessage(error, "Серверное состояние изменилось.")} Перезагрузите данные и запустите проверку заново.`
          : rosterErrorMessage(error, "Не удалось заблокировать состав турнира"),
      );
    } finally {
      setLockingRoster(false);
    }
  };

  const handleUnlockRoster = async (): Promise<void> => {
    if (
      !selectedTournament ||
      !roster ||
      !roster.locked ||
      roster.execution_started ||
      savingRoster ||
      lockingRoster ||
      unlockingRoster ||
      !unlockConfirmed
    ) {
      return;
    }
    const reason = unlockReason.trim();
    if (!reason) {
      setControlError("Укажите причину разблокировки состава.");
      return;
    }

    setUnlockingRoster(true);
    setControlError(null);
    setRosterNotice(null);
    try {
      const snapshot = await operatorApi.getSnapshot(selectedTournament.id);
      const unlockedRoster = await operatorApi.unlockRoster(
        selectedTournament.id,
        {
          expected_projection_revision: snapshot.next_cursor.projection_revision,
          confirmed: true,
          reason,
        },
        createOperatorCommandIntent(),
      );
      setRoster(unlockedRoster);
      setDraftParticipants(draftFromRoster(unlockedRoster));
      resetPreflight();
      setUnlockConfirmed(false);
      setUnlockReason("");
      await onReloadTournaments();
      setRosterNotice("Состав разблокирован. Перед новой блокировкой потребуется повторная проверка.");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      setControlError(
        error instanceof ApiError && error.status === 409
          ? `${rosterErrorMessage(error, "Серверное состояние изменилось.")} Обновите данные перед повторной попыткой.`
          : rosterErrorMessage(error, "Не удалось разблокировать состав турнира"),
      );
    } finally {
      setUnlockingRoster(false);
    }
  };

  return (
    <Panel
      title="Состав турнира"
      description="Выберите турнир, чтобы загрузить его состав и активных игроков."
      className={styles.panel}
    >
      {showTournamentChooser ? <div className={styles.chooser}>
        <label htmlFor="roster-tournament-select">
          Турнир для редактирования состава
        </label>
        <select
          id="roster-tournament-select"
          name="roster_tournament"
          value={selectedTournamentId}
          onChange={(event) => onSelectTournament(event.target.value)}
        >
          <option value="">Выберите турнир</option>
          {tournaments.map((tournament) => (
            <option key={tournament.id} value={tournament.id}>
              {tournament.name} - {tournament.public_id}
            </option>
          ))}
        </select>
      </div> : null}

      {!selectedTournament && (
        <Message tone="empty" title="Турнир не выбран">
          Выберите турнир из списка выше, чтобы просмотреть и изменить его состав.
        </Message>
      )}

      {selectedTournament && rosterState === "loading" && (
        <Message tone="loading" title="Загружаем состав">
          Получаем состав турнира и список активных игроков.
        </Message>
      )}

      {selectedTournament && rosterState === "error" && (
        <Message tone="error" title="Не удалось загрузить состав">
          {rosterError || "Состав временно недоступен."}
          <button
            className={styles.inlineAction}
            type="button"
            onClick={handleReloadRoster}
          >
            Повторить загрузку
          </button>
        </Message>
      )}

      {selectedTournament && rosterState === "ready" && roster && (
        <div className={styles.content}>
          <div className={styles.summary}>
            <div className={styles.summaryHeading}>
              <div>
                <h3 className={styles.title}>{selectedTournament.name}</h3>
                <p className={styles.subtitle}>
                  {selectedTournament.public_id} | {formatTournamentState(selectedTournament.state)}
                </p>
              </div>
              <Status tone={rosterEditingLocked ? "disabled" : "success"}>
                {rosterEditingLocked ? "Только просмотр" : "Можно редактировать"}
              </Status>
            </div>
            <dl className={styles.meta}>
              <div>
                <dt>Ревизия турнира</dt>
                <dd>{selectedTournament.revision}</dd>
              </div>
              <div>
                <dt>Ревизия состава</dt>
                <dd>{roster.revision}</dd>
              </div>
              <div>
                <dt>Участников</dt>
                <dd>{draftParticipants.length} / 16</dd>
              </div>
              <div>
                <dt>Плановый размер</dt>
                <dd>{selectedTournament.planned_roster_size}</dd>
              </div>
              <div>
                <dt>Серверная блокировка</dt>
                <dd>{roster.locked ? "Да" : "Нет"}</dd>
              </div>
              <div>
                <dt>Исполнение началось</dt>
                <dd>{roster.execution_started ? "Да" : "Нет"}</dd>
              </div>
            </dl>
          </div>

          {rosterEditingLocked && (
            <Message tone="warning" title="Состав недоступен для изменений">
              {roster.execution_started
                ? "Исполнение турнира уже началось. Изменение и сохранение состава отключены."
                : "Состав зафиксирован на сервере. Изменение и сохранение состава отключены."}
            </Message>
          )}

          {roster.locked && !roster.execution_started && (
            <section
              className={styles.unlockPanel}
              aria-labelledby="roster-unlock-title"
            >
              <div>
                <h4 id="roster-unlock-title" className={styles.sectionTitle}>
                  Разблокировать состав
                </h4>
                <p className={styles.sectionDescription}>
                  Разблокировка отменит серверную фиксацию. Перед новой блокировкой потребуется повторная проверка.
                </p>
              </div>
              <div className={styles.checkboxField}>
                <input
                  id="roster-unlock-confirmed"
                  type="checkbox"
                  checked={unlockConfirmed}
                  disabled={unlockingRoster || lockingRoster}
                  onChange={(event) => {
                    setUnlockConfirmed(event.target.checked);
                    setControlError(null);
                  }}
                />
                <label htmlFor="roster-unlock-confirmed">
                  Подтверждаю разблокировку состава
                </label>
              </div>
              <div className={styles.unlockField}>
                <label htmlFor="roster-unlock-reason">Причина разблокировки</label>
                <textarea
                  id="roster-unlock-reason"
                  value={unlockReason}
                  maxLength={MAX_UNLOCK_REASON_LENGTH}
                  onChange={(event) => {
                    setUnlockReason(event.target.value);
                    setControlError(null);
                  }}
                  placeholder="Укажите причину"
                  disabled={unlockingRoster || lockingRoster}
                  required
                />
                <span className={styles.fieldHint}>
                  {unlockReason.length} / {MAX_UNLOCK_REASON_LENGTH}
                </span>
              </div>
              <Button
                type="button"
                variant="danger"
                onClick={() => void handleUnlockRoster()}
                loading={unlockingRoster}
                loadingLabel="Разблокируем состав"
                disabled={
                  !unlockConfirmed ||
                  unlockReason.trim().length === 0 ||
                  unlockingRoster ||
                  lockingRoster ||
                  savingRoster
                }
              >
                Разблокировать состав
              </Button>
            </section>
          )}

          {rosterNotice && (
            <Message tone="success" title="Состав сохранен">
              {rosterNotice}
            </Message>
          )}

          {controlError && (
            <Message tone="error" title="Операция не выполнена">
              {controlError}
              <button
                className={styles.inlineAction}
                type="button"
                onClick={handleReloadRoster}
                disabled={savingRoster || lockingRoster || unlockingRoster}
              >
                Перезагрузить данные
              </button>
            </Message>
          )}

          {rosterError && !rosterEditingLocked && (
            <Message tone="error" title="Состав не сохранен">
              {rosterError}
              <button
                className={styles.inlineAction}
                type="button"
                onClick={handleReloadRoster}
                disabled={savingRoster}
              >
                Перезагрузить данные
              </button>
            </Message>
          )}

          <div className={styles.actions}>
            <Button
              type="button"
              variant="secondary"
              onClick={handleAddParticipant}
              disabled={rosterEditingLocked || savingRoster}
            >
              Добавить участника
            </Button>
            <Button
              type="button"
              onClick={() => void handleSaveRoster()}
              loading={savingRoster}
              loadingLabel="Сохраняем состав"
              disabled={rosterEditingLocked || rosterState !== "ready"}
            >
              Сохранить состав
            </Button>
            <span className={styles.hint}>
              Максимум 16 участников. Сохранение отправляет весь состав целиком.
            </span>
          </div>

          <section
            className={styles.preflight}
            aria-labelledby="roster-preflight-title"
          >
            <div className={styles.preflightHeader}>
              <div>
                <h4 id="roster-preflight-title" className={styles.sectionTitle}>
                  Предполетная проверка
                </h4>
                <p className={styles.sectionDescription}>
                  Сервер проверит состав, задачи, категории, ресурсы и доступную емкость перед блокировкой.
                </p>
              </div>
              <Button
                type="button"
                variant="secondary"
                onClick={() => void handleRunPreflight()}
                loading={preflightState === "loading"}
                loadingLabel="Проверяем состав"
                disabled={
                  rosterEditingLocked ||
                  savingRoster ||
                  lockingRoster ||
                  unlockingRoster ||
                  preflightState === "loading"
                }
              >
                Запустить проверку
              </Button>
            </div>

            {preflightState === "idle" && (
              <p className={styles.preflightEmpty}>
                Проверка еще не запускалась. Измените состав перед новой проверкой, если это необходимо.
              </p>
            )}

            {preflightState === "error" && (
              <Message tone="error" title="Предполетная проверка не выполнена">
                {preflightError || "Сервер не вернул отчет проверки."}
                <button
                  className={styles.inlineAction}
                  type="button"
                  onClick={handleReloadRoster}
                  disabled={savingRoster || lockingRoster || unlockingRoster}
                >
                  Перезагрузить данные
                </button>
              </Message>
            )}

            {preflightState === "ready" && preflightReport && (
              <div
                className={styles.preflightReport}
                aria-label="Результат предполетной проверки"
              >
                <div className={styles.preflightSummary}>
                  <div>
                    <span className={styles.preflightLabel}>Общий результат</span>
                    <Status
                      tone={preflightReport.passed ? "success" : "error"}
                      size="small"
                    >
                      {preflightReport.passed
                        ? "Проверка пройдена"
                        : "Проверка не пройдена"}
                    </Status>
                  </div>
                  <div>
                    <span className={styles.preflightLabel}>Идентификатор отчета</span>
                    <code className={styles.preflightReportId}>{preflightReport.id}</code>
                  </div>
                </div>
                <ol className={styles.preflightChecks}>
                  {preflightReport.checks.map((check, index) => (
                    <li
                      className={check.passed ? styles.preflightCheckPassed : styles.preflightCheckFailed}
                      key={`${check.code}-${index}`}
                    >
                      <div className={styles.preflightCheckHeading}>
                        <Status tone={check.passed ? "success" : "error"} size="small">
                          {check.passed ? "Пройдено" : "Ошибка"}
                        </Status>
                        <code>{check.code}</code>
                      </div>
                      <p className={styles.preflightExplanation}>{check.explanation}</p>
                      {check.evidence.length > 0 && (
                        <ul className={styles.preflightEvidence}>
                          {check.evidence.map((evidence, evidenceIndex) => (
                            <li key={`${check.code}-evidence-${evidenceIndex}`}>
                              {evidence}
                            </li>
                          ))}
                        </ul>
                      )}
                    </li>
                  ))}
                </ol>
                {!preflightReport.passed && (
                  <Message tone="warning" title="Блокировка недоступна">
                    Исправьте указанные проблемы и запустите предполетную проверку повторно.
                  </Message>
                )}
              </div>
            )}

            <div className={styles.lifecycleActions}>
              <Button
                type="button"
                onClick={() => void handleLockRoster()}
                loading={lockingRoster}
                loadingLabel="Блокируем состав"
                disabled={!canLockRoster}
              >
                Заблокировать состав
              </Button>
              <span className={styles.hint}>
                Блокировка доступна только после успешной проверки и при наличии минимум 4 присутствующих игроков.
              </span>
            </div>
          </section>

          {draftParticipants.length === 0 ? (
            <Message tone="empty" title="Состав пуст">
              Добавьте активного игрока, чтобы сформировать состав турнира.
            </Message>
          ) : (
            <div className={styles.list} aria-label="Редактируемый состав">
              {draftParticipants.map((participant, index) => {
                const player = playerById.get(participant.playerId);
                const unknownPlayer =
                  participant.playerId && !player
                    ? {
                        id: participant.playerId,
                        username: "Игрок недоступен",
                      }
                    : null;
                return (
                  <fieldset
                    className={styles.row}
                    key={participant.id}
                    disabled={rosterEditingLocked || savingRoster}
                  >
                    <legend>Участник {index + 1}</legend>
                    <div className={styles.rowDetails}>
                      <div className={styles.serverOrder}>
                        <span>Порядок на сервере</span>
                        <strong>{index + 1}</strong>
                      </div>
                      <div className={styles.identity}>
                        <span>Идентификатор участника</span>
                        <code>{participant.participantId || "Будет присвоен сервером"}</code>
                      </div>
                    </div>
                    <div className={styles.fields}>
                      <div className={styles.field}>
                        <label htmlFor={`roster-player-${participant.id}`}>
                          Игрок
                        </label>
                        <select
                          id={`roster-player-${participant.id}`}
                          value={participant.playerId}
                          onChange={(event) =>
                            updateDraftPlayer(participant.id, event.target.value)
                          }
                        >
                          <option value="">Выберите игрока</option>
                          {unknownPlayer && (
                            <option value={unknownPlayer.id}>
                              {unknownPlayer.username} - {unknownPlayer.id}
                            </option>
                          )}
                          {players.map((activePlayer) => (
                            <option key={activePlayer.id} value={activePlayer.id}>
                              {activePlayer.username} - {activePlayer.id}
                            </option>
                          ))}
                        </select>
                        <span className={styles.fieldHint}>
                          {player
                            ? `Имя: ${player.username} | ID игрока: ${player.id}`
                            : participant.playerId
                              ? `Игрок больше не входит в список активных | ID игрока: ${participant.playerId}`
                              : "Выберите активного игрока"}
                        </span>
                      </div>
                      <div className={styles.field}>
                        <label htmlFor={`roster-seed-${participant.id}`}>
                          Seed / позиция
                        </label>
                        <input
                          id={`roster-seed-${participant.id}`}
                          type="number"
                          min="1"
                          max={participantCount}
                          step="1"
                          value={participant.seed}
                          onChange={(event) =>
                            updateDraftParticipant(participant.id, {
                              seed: event.target.value,
                            })
                          }
                        />
                      </div>
                      <div className={styles.field}>
                        <label htmlFor={`roster-attendance-${participant.id}`}>
                          Посещаемость
                        </label>
                        <select
                          id={`roster-attendance-${participant.id}`}
                          value={participant.attendance}
                          onChange={(event) =>
                            updateDraftParticipant(participant.id, {
                              attendance: event.target.value as AttendanceState,
                            })
                          }
                        >
                          {ATTENDANCE_STATES.map((attendance) => (
                            <option key={attendance} value={attendance}>
                              {ATTENDANCE_LABELS[attendance]}
                            </option>
                          ))}
                        </select>
                      </div>
                      <Button
                        type="button"
                        variant="danger"
                        size="small"
                        onClick={() => handleRemoveParticipant(participant.id)}
                        disabled={rosterEditingLocked || savingRoster}
                      >
                        Удалить
                      </Button>
                    </div>
                  </fieldset>
                );
              })}
            </div>
          )}
        </div>
      )}
    </Panel>
  );
};

RosterEditor.displayName = "RosterEditor";
