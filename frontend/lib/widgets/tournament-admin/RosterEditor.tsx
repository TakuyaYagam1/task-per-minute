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
import { useAdminLiveRefresh } from "../../features/admin-live";
import { formatTournamentState, PREFLIGHT_CODE_LABELS } from "../../shared/lib";
import { Button, Message, Panel, Status, TechnicalDetails } from "../../shared/ui";

import { PlayerPicker } from "./PlayerPicker";
import styles from "./RosterEditor.module.css";

type RosterEditorProps = Readonly<{
  tournaments: readonly Tournament[];
  selectedTournament: Tournament | null;
  selectedTournamentId: string;
  onSelectTournament: (id: string) => void;
  onNavigateToOverview?: () => void;
  onReloadTournaments: () => Promise<void>;
  onSessionExpired?: () => void;
  onDirtyChange?: (dirty: boolean) => void;
  showTournamentChooser?: boolean;
}>;

type LoadState = "loading" | "ready" | "error";
type LoadOptions = Readonly<{ silent?: boolean }>;
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
  onNavigateToOverview,
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
  const playersControllerRef = useRef<AbortController | null>(null);
  const preflightControllerRef = useRef<AbortController | null>(null);
  const rosterLoadRef = useRef(0);
  const playersLoadRef = useRef(0);
  const playersSessionExpiredRef = useRef(false);
  const playersHasSnapshotRef = useRef(false);
  const rosterRef = useRef<Roster | null>(null);
  const rosterDraftDirtyRef = useRef(false);
  const rosterMutationInFlightRef = useRef(false);
  const rosterRefreshPendingRef = useRef(false);
  const preflightRunRef = useRef(0);
  const draftIdRef = useRef(0);
  const [players, setPlayers] = useState<AdminPlayer[]>([]);
  const [playersHasSnapshot, setPlayersHasSnapshot] = useState(false);
  const [playersState, setPlayersState] = useState<LoadState>("ready");
  const [playersError, setPlayersError] = useState<string | null>(null);
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

  rosterRef.current = roster;
  playersHasSnapshotRef.current = playersHasSnapshot;
  const rosterDraftDirty = Boolean(selectedTournamentId && roster) && (
    JSON.stringify(roster ? draftFromRoster(roster) : []) !== JSON.stringify(draftParticipants) ||
    unlockConfirmed ||
    Boolean(unlockReason.trim())
  );
  rosterDraftDirtyRef.current = rosterDraftDirty;
  const rosterMutationInFlight = savingRoster || lockingRoster || unlockingRoster;
  rosterMutationInFlightRef.current = rosterMutationInFlight;

  useEffect(() => {
    onDirtyChange?.(rosterDraftDirty);
  }, [onDirtyChange, rosterDraftDirty]);

  const resetPreflight = useCallback((): void => {
    preflightControllerRef.current?.abort();
    preflightControllerRef.current = null;
    preflightRunRef.current += 1;
    setPreflightState("idle");
    setPreflightReport(null);
    setPreflightProjectionRevision(null);
    setPreflightError(null);
  }, []);

  const cancelPlayersLoad = useCallback((): void => {
    playersControllerRef.current?.abort();
    playersControllerRef.current = null;
    playersLoadRef.current += 1;
  }, []);

  const loadPlayers = useCallback(async (id: string, options: LoadOptions = {}): Promise<void> => {
    const silent = options.silent === true;
    if (!id || playersSessionExpiredRef.current) {
      return;
    }
    playersControllerRef.current?.abort();
    const controller = new AbortController();
    const loadId = playersLoadRef.current + 1;
    playersLoadRef.current = loadId;
    playersControllerRef.current = controller;
    if (!silent || !playersHasSnapshotRef.current) {
      setPlayersState("loading");
    }
    setPlayersError(null);

    try {
      const activePlayers = await adminApi.listPlayers(false, controller.signal);
      if (
        controller.signal.aborted ||
        playersLoadRef.current !== loadId
      ) {
        return;
      }
      setPlayers(activePlayers);
      playersHasSnapshotRef.current = true;
      setPlayersHasSnapshot(true);
      setPlayersState("ready");
    } catch (error) {
      if (
        controller.signal.aborted ||
        playersLoadRef.current !== loadId ||
        isAbortError(error)
      ) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        playersSessionExpiredRef.current = true;
        onSessionExpired?.();
      }
      if (silent && playersHasSnapshotRef.current) {
        setPlayersState("ready");
        return;
      }
      setPlayersState("error");
      setPlayersError(
        rosterErrorMessage(error, "Не удалось загрузить список активных игроков"),
      );
    } finally {
      if (playersControllerRef.current === controller) {
        playersControllerRef.current = null;
      }
    }
  }, [onSessionExpired]);

  const loadRoster = useCallback(async (id: string, options: LoadOptions = {}): Promise<void> => {
    const silent = options.silent === true;
    rosterControllerRef.current?.abort();
    rosterRefreshPendingRef.current = false;
    const controller = new AbortController();
    const loadId = rosterLoadRef.current + 1;
    rosterLoadRef.current = loadId;
    rosterControllerRef.current = controller;
    if (!silent || rosterRef.current === null) {
      setRosterState("loading");
    }
    setRosterError(null);
    setRosterNotice(null);
    setControlError(null);
    if (!silent || rosterRef.current === null) {
      setUnlockConfirmed(false);
      setUnlockReason("");
    }

    try {
      const currentRoster = await operatorApi.getRoster(id, controller.signal);
      if (
        controller.signal.aborted ||
        rosterLoadRef.current !== loadId
      ) {
        return;
      }
      if (
        silent &&
        (rosterDraftDirtyRef.current || rosterMutationInFlightRef.current)
      ) {
        rosterRefreshPendingRef.current = true;
        return;
      }
      const previousRoster = rosterRef.current;
      const rosterChanged = previousRoster === null ||
        previousRoster.tournament_id !== currentRoster.tournament_id ||
        previousRoster.revision !== currentRoster.revision;
      if (rosterChanged) {
        resetPreflight();
      }
      rosterRef.current = currentRoster;
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
        playersSessionExpiredRef.current = true;
        onSessionExpired?.();
      }
      if (silent && rosterRef.current !== null) {
        return;
      }
      setRosterState("error");
      setRosterError(
        rosterErrorMessage(error, "Не удалось загрузить состав соревнования"),
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
      cancelPlayersLoad();
      playersSessionExpiredRef.current = false;
      playersHasSnapshotRef.current = false;
      rosterRefreshPendingRef.current = false;
      resetPreflight();
      rosterLoadRef.current += 1;
      setPlayers([]);
      setPlayersHasSnapshot(false);
      setPlayersState("ready");
      setPlayersError(null);
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
    playersSessionExpiredRef.current = false;
    void loadRoster(tournamentId);
    return () => {
      rosterControllerRef.current?.abort();
      cancelPlayersLoad();
    };
  }, [cancelPlayersLoad, loadRoster, resetPreflight, tournamentId]);

  useEffect(() => {
    if (!tournamentId) {
      return;
    }

    void loadPlayers(tournamentId);
    return () => {
      cancelPlayersLoad();
    };
  }, [cancelPlayersLoad, loadPlayers, tournamentId]);

  useEffect(() => {
    if (
      !rosterRefreshPendingRef.current ||
      !tournamentId ||
      rosterDraftDirty ||
      rosterMutationInFlight ||
      rosterState === "loading"
    ) {
      return;
    }
    rosterRefreshPendingRef.current = false;
    void loadRoster(tournamentId, { silent: true });
  }, [loadRoster, rosterDraftDirty, rosterMutationInFlight, rosterState, tournamentId]);

  const refreshPlayersLive = useCallback(async (): Promise<void> => {
    if (!tournamentId) {
      return;
    }
    await loadPlayers(tournamentId, { silent: true });
  }, [loadPlayers, tournamentId]);

  const refreshRosterLive = useCallback(async (): Promise<void> => {
    if (
      !tournamentId ||
      rosterDraftDirtyRef.current ||
      rosterMutationInFlightRef.current ||
      rosterState === "loading"
    ) {
      rosterRefreshPendingRef.current = true;
      return;
    }
    await loadRoster(tournamentId, { silent: true });
  }, [loadRoster, rosterState, tournamentId]);

  useAdminLiveRefresh(
    "players",
    refreshPlayersLive,
    Boolean(tournamentId),
  );
  useAdminLiveRefresh(
    "tournaments",
    refreshRosterLive,
    Boolean(tournamentId) &&
      !rosterDraftDirty &&
      !rosterMutationInFlight &&
      rosterState !== "loading",
  );

  useEffect(() => () => {
    rosterControllerRef.current?.abort();
    cancelPlayersLoad();
    preflightControllerRef.current?.abort();
  }, [cancelPlayersLoad]);

  useEffect(() => {
    resetPreflight();
    setControlError(null);
    setUnlockConfirmed(false);
    setUnlockReason("");
  }, [resetPreflight, selectedTournamentId]);

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
      setRosterNotice("Состав сохранен.");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
        resetPreflight();
        setRosterError(
          `${error.problem?.detail || "Данные соревнования изменились."} Ваши изменения сохранены в форме. Перезагрузите данные перед новой попыткой.`,
        );
      } else {
        setRosterError(
          rosterErrorMessage(error, "Не удалось сохранить состав соревнования"),
        );
      }
    } finally {
      setSavingRoster(false);
    }
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
          ? `${rosterErrorMessage(error, "Данные соревнования изменились.")} Обновите данные и повторите проверку.`
          : rosterErrorMessage(error, "Не удалось проверить готовность к старту"),
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
      setControlError("Для фиксации состава отметьте минимум 4 игроков как присутствующих.");
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
      setRosterNotice(null);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
        resetPreflight();
      }
      setControlError(
        error instanceof ApiError && error.status === 409
          ? `${rosterErrorMessage(error, "Данные соревнования изменились.")} Перезагрузите данные и запустите проверку заново.`
          : rosterErrorMessage(error, "Не удалось зафиксировать состав соревнования"),
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
      setRosterNotice("Состав разблокирован. Перед новой фиксацией потребуется повторная проверка.");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      setControlError(
        error instanceof ApiError && error.status === 409
          ? `${rosterErrorMessage(error, "Данные соревнования изменились.")} Обновите данные перед повторной попыткой.`
          : rosterErrorMessage(error, "Не удалось разблокировать состав соревнования"),
      );
    } finally {
      setUnlockingRoster(false);
    }
  };

  return (
    <Panel
      title="Состав соревнования"
      description="Выберите соревнование, чтобы загрузить его состав и активных игроков."
      className={styles.panel}
    >
      {showTournamentChooser ? <div className={styles.chooser}>
        <label htmlFor="roster-tournament-select">
          Соревнование для редактирования состава
        </label>
        <select
          id="roster-tournament-select"
          name="roster_tournament"
          value={selectedTournamentId}
          onChange={(event) => onSelectTournament(event.target.value)}
        >
          <option value="">Выберите соревнование</option>
          {tournaments.map((tournament) => (
            <option key={tournament.id} value={tournament.id}>
              {tournament.name} - {formatTournamentState(tournament.state)}
            </option>
          ))}
        </select>
      </div> : null}

      {!selectedTournament && (
        <Message tone="empty" title="Соревнование не выбрано">
          Выберите соревнование из списка выше, чтобы просмотреть и изменить его состав.
        </Message>
      )}

      {selectedTournament && rosterState === "loading" && (
        <Message tone="loading" title="Загружаем состав">
          Получаем состав соревнования.
        </Message>
      )}

      {selectedTournament && rosterState === "error" && (
        <Message tone="error" title="Не удалось загрузить состав">
          {rosterError || "Состав временно недоступен."}
        </Message>
      )}

      {selectedTournament && rosterState === "ready" && roster && (
        <div className={styles.content}>
          <div className={styles.summary}>
            <div className={styles.summaryHeading}>
              <div>
                <h3 className={styles.title}>{selectedTournament.name}</h3>
                <p className={styles.subtitle}>
                  {formatTournamentState(selectedTournament.state)}
                </p>
              </div>
              <Status tone={rosterEditingLocked ? "disabled" : "success"}>
                {rosterEditingLocked ? "Только просмотр" : "Можно редактировать"}
              </Status>
            </div>
            <dl className={styles.meta}>
              <div>
                <dt>Участников</dt>
                <dd>{draftParticipants.length} / 16</dd>
              </div>
              <div>
                <dt>Плановый размер</dt>
                <dd>{selectedTournament.planned_roster_size}</dd>
              </div>
              <div>
                <dt>Состав зафиксирован</dt>
                <dd>{roster.locked ? "Да" : "Нет"}</dd>
              </div>
              <div>
                <dt>Соревнование началось</dt>
                <dd>{roster.execution_started ? "Да" : "Нет"}</dd>
              </div>
            </dl>
          </div>

          {playersState === "loading" && players.length === 0 && (
            <p className={styles.fieldHint}>Загружаем список активных игроков.</p>
          )}

          {playersState === "error" && (
            <Message tone="error" title="Не удалось загрузить список игроков">
              {playersError || "Список активных игроков временно недоступен."}
            </Message>
          )}

          {rosterEditingLocked && (
            <Message
              tone={roster.execution_started ? "warning" : "success"}
              title={roster.execution_started ? "Соревнование уже началось" : "Состав зафиксирован"}
            >
              {roster.execution_started
                ? "Состав больше нельзя менять после начала соревнования."
                : "Перейдите в раздел \"Обзор\", чтобы запустить квалификацию."
              }
              {!roster.execution_started && onNavigateToOverview ? (
                <Button
                  className={styles.nextStepButton}
                  type="button"
                  size="small"
                  onClick={onNavigateToOverview}
                >
                  Перейти к запуску
                </Button>
              ) : null}
            </Message>
          )}

          {roster.locked && !roster.execution_started && (
            <details className={styles.unlockPanel}>
              <summary className={styles.unlockSummary}>Изменить зафиксированный состав</summary>
              <div className={styles.unlockContent}>
                <p className={styles.sectionDescription}>
                  Откройте состав для изменений. После этого его потребуется проверить и зафиксировать заново.
                </p>
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
                    Подтверждаю открытие состава для изменений
                  </label>
                </div>
                <div className={styles.unlockField}>
                  <label htmlFor="roster-unlock-reason">Причина изменения</label>
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
                  loadingLabel="Открываем состав"
                  disabled={
                    !unlockConfirmed ||
                    unlockReason.trim().length === 0 ||
                    unlockingRoster ||
                    lockingRoster ||
                    savingRoster
                  }
                >
                  Открыть состав для изменений
                </Button>
              </div>
            </details>
          )}

          {rosterNotice && (
            <Message tone="success" title="Состав сохранен">
              {rosterNotice}
            </Message>
          )}

          {controlError && (
            <Message tone="error" title="Операция не выполнена">
              {controlError}
            </Message>
          )}

          {rosterError && !rosterEditingLocked && (
            <Message tone="error" title="Состав не сохранен">
              {rosterError}
            </Message>
          )}

          {!rosterEditingLocked ? <>
            <div className={styles.actions}>
              <Button
                type="button"
                variant="secondary"
                onClick={handleAddParticipant}
                disabled={savingRoster}
              >
                Добавить участника
              </Button>
              <Button
                type="button"
                onClick={() => void handleSaveRoster()}
                loading={savingRoster}
                loadingLabel="Сохраняем состав"
                disabled={rosterState !== "ready"}
              >
                Сохранить состав
              </Button>
              <span className={styles.hint}>
                Максимум 16 участников. После изменений сохраните состав.
              </span>
            </div>

            <section
              className={styles.preflight}
              aria-labelledby="roster-preflight-title"
            >
            <div className={styles.preflightHeader}>
              <div>
                <h4 id="roster-preflight-title" className={styles.sectionTitle}>
                  Проверка перед стартом
                </h4>
                <p className={styles.sectionDescription}>
                  Проверьте, что состав и задачи готовы к запуску соревнования.
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
              <Message tone="error" title="Проверка не выполнена">
                {preflightError || "Проверка не вернула результат."}
              </Message>
            )}

            {preflightState === "ready" && preflightReport && (
              <div
                className={styles.preflightReport}
                aria-label="Результат проверки перед стартом"
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
                        <span>{PREFLIGHT_CODE_LABELS[check.code] ?? `Проверка ${index + 1}`}</span>
                      </div>
                      <p className={styles.preflightExplanation}>
                        {check.passed
                          ? "Готово. Это условие выполнено."
                          : "Исправьте этот пункт и запустите проверку повторно."}
                      </p>
                      <TechnicalDetails>
                        <p>Подробности: {check.explanation}</p>
                        <p>Код проверки: <code>{check.code}</code></p>
                        {check.evidence.length > 0 && (
                          <ul className={styles.preflightEvidence}>
                            {check.evidence.map((evidence, evidenceIndex) => (
                              <li key={`${check.code}-evidence-${evidenceIndex}`}>
                                {evidence}
                              </li>
                            ))}
                          </ul>
                        )}
                      </TechnicalDetails>
                    </li>
                  ))}
                </ol>
                {!preflightReport.passed && (
                  <Message tone="warning" title="Фиксация недоступна">
                    Исправьте указанные проблемы и запустите проверку повторно.
                  </Message>
                )}
              </div>
            )}

            <div className={styles.lifecycleActions}>
              <Button
                type="button"
                onClick={() => void handleLockRoster()}
                loading={lockingRoster}
                loadingLabel="Фиксируем состав"
                disabled={!canLockRoster}
              >
                Зафиксировать состав
              </Button>
              <span className={styles.hint}>
                Фиксация доступна только после успешной проверки и при наличии минимум 4 присутствующих игроков.
              </span>
            </div>
            </section>
          </> : null}

          {draftParticipants.length === 0 ? (
            <Message tone="empty" title="Состав пуст">
              Добавьте активного игрока, чтобы сформировать состав соревнования.
            </Message>
          ) : rosterEditingLocked ? (
            <div className={styles.list} aria-label="Состав соревнования">
              {draftParticipants.map((participant, index) => {
                const player = playerById.get(participant.playerId);
                const participantName = player?.username || `Участник ${index + 1}`;
                return (
                  <div
                    className={styles.readOnlyRow}
                    key={participant.id}
                    role="group"
                    aria-label={participantName}
                  >
                    <strong className={styles.readOnlyName}>{participantName}</strong>
                    <dl className={styles.readOnlyMeta}>
                      <div>
                        <dt>Позиция</dt>
                        <dd>{participant.seed}</dd>
                      </div>
                      <div>
                        <dt>Участие</dt>
                        <dd>{ATTENDANCE_LABELS[participant.attendance]}</dd>
                      </div>
                    </dl>
                  </div>
                );
              })}
            </div>
          ) : (
            <div className={styles.list} aria-label="Редактируемый состав">
              {draftParticipants.map((participant, index) => {
                const player = playerById.get(participant.playerId);
                const unknownPlayer =
                  participant.playerId && playersHasSnapshot && !player
                    ? {
                        id: participant.playerId,
                        username: "Игрок недоступен",
                      }
                    : null;
                const selectedPlayer = player ||
                  (participant.playerId
                    ? {
                        id: participant.playerId,
                        username: unknownPlayer?.username || "Игрок выбран",
                      }
                    : null);
                const excludedPlayerIds = new Set(
                  draftParticipants
                    .filter((candidate) => candidate.id !== participant.id)
                    .map((candidate) => candidate.playerId)
                    .filter((playerId): playerId is string => Boolean(playerId)),
                );
                return (
                  <fieldset
                    className={styles.row}
                    key={participant.id}
                    disabled={rosterEditingLocked || savingRoster}
                  >
                    <legend>{player?.username || `Участник ${index + 1}`}</legend>
                    <div className={styles.fields}>
                      <div className={styles.field}>
                        <label htmlFor={`roster-player-${participant.id}`}>
                          Игрок
                        </label>
                        <PlayerPicker
                          id={`roster-player-${participant.id}`}
                          players={players}
                          value={participant.playerId}
                          selectedPlayer={selectedPlayer}
                          excludedPlayerIds={excludedPlayerIds}
                          describedBy={unknownPlayer ? `roster-player-hint-${participant.id}` : undefined}
                          onOpen={() => void loadPlayers(tournamentId)}
                          onChange={(playerId) =>
                            updateDraftPlayer(participant.id, playerId)
                          }
                          disabled={rosterEditingLocked || savingRoster}
                        />
                      </div>
                      <div className={styles.field}>
                        <label htmlFor={`roster-seed-${participant.id}`}>
                          Позиция
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
                          Участие
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
                        aria-label={`Удалить из состава: ${player?.username || `участник ${index + 1}`}`}
                        onClick={() => handleRemoveParticipant(participant.id)}
                        disabled={rosterEditingLocked || savingRoster}
                      >
                        Удалить
                      </Button>
                    </div>
                    {unknownPlayer ? (
                      <p className={styles.fieldHint} id={`roster-player-hint-${participant.id}`}>
                        Этот игрок больше не входит в список активных. Выберите другого игрока или удалите его из состава.
                      </p>
                    ) : null}
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
