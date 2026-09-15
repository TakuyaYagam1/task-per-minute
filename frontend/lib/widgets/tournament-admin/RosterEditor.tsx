"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  adminApi,
  ApiError,
  createOperatorCommandIntent,
  operatorApi,
  type AdminPlayer,
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
}>;

type LoadState = "loading" | "ready" | "error";
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
}: RosterEditorProps) => {
  const tournamentId = selectedTournament?.id ?? "";
  const rosterControllerRef = useRef<AbortController | null>(null);
  const rosterLoadRef = useRef(0);
  const draftIdRef = useRef(0);
  const [players, setPlayers] = useState<AdminPlayer[]>([]);
  const [roster, setRoster] = useState<Roster | null>(null);
  const [draftParticipants, setDraftParticipants] = useState<DraftParticipant[]>([]);
  const [rosterState, setRosterState] = useState<LoadState>("ready");
  const [rosterError, setRosterError] = useState<string | null>(null);
  const [rosterNotice, setRosterNotice] = useState<string | null>(null);
  const [savingRoster, setSavingRoster] = useState(false);

  const loadRoster = useCallback(async (id: string): Promise<void> => {
    rosterControllerRef.current?.abort();
    const controller = new AbortController();
    const loadId = rosterLoadRef.current + 1;
    rosterLoadRef.current = loadId;
    rosterControllerRef.current = controller;
    setRosterState("loading");
    setRosterError(null);
    setRosterNotice(null);

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
  }, [onSessionExpired]);

  useEffect(() => {
    if (!tournamentId) {
      rosterControllerRef.current?.abort();
      rosterLoadRef.current += 1;
      setPlayers([]);
      setRoster(null);
      setDraftParticipants([]);
      setRosterState("ready");
      setRosterError(null);
      setRosterNotice(null);
      return;
    }
    void loadRoster(tournamentId);
    return () => {
      rosterControllerRef.current?.abort();
    };
  }, [loadRoster, tournamentId]);

  useEffect(() => () => {
    rosterControllerRef.current?.abort();
  }, []);

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
    setRosterError(null);
    setRosterNotice(null);
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
    setRosterError(null);
    setRosterNotice(null);
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
    setRosterError(null);
    setRosterNotice(null);
  };

  const handleRemoveParticipant = (participantId: string): void => {
    if (rosterEditingLocked || savingRoster) {
      return;
    }
    setDraftParticipants((current) =>
      current.filter((participant) => participant.id !== participantId),
    );
    setRosterError(null);
    setRosterNotice(null);
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
    setRosterError(null);
    setRosterNotice(null);
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
      await onReloadTournaments();
      setRosterNotice("Состав сохранен. Серверный порядок и идентификаторы обновлены.");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
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
    if (!tournamentId || savingRoster) {
      return;
    }
    void loadRoster(tournamentId);
    void onReloadTournaments();
  };

  return (
    <Panel
      title="Состав турнира"
      description="Выберите турнир, чтобы загрузить его состав и активных игроков."
      className={styles.panel}
    >
      <div className={styles.chooser}>
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
      </div>

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

          {rosterNotice && (
            <Message tone="success" title="Состав сохранен">
              {rosterNotice}
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
