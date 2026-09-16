"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  adminApi,
  ApiError,
  createOperatorCommandIntent,
  getTournamentConfiguration,
  operatorApi,
  type AdminPlayer,
  type PairingConfigurationRequest,
  type Roster,
  type SwissRound,
  type Tournament,
} from "../../shared/api";
import { Button, Message, Panel, Status } from "../../shared/ui";

import styles from "./SwissPairingEditor.module.css";

type SwissPairingEditorProps = Readonly<{
  tournaments: readonly Tournament[];
  selectedTournament: Tournament | null;
  selectedTournamentId: string;
  onSelectTournament: (id: string) => void;
  onReloadTournaments: () => Promise<void>;
  onSessionExpired?: () => void;
}>;

type LoadState = "loading" | "ready" | "error";
type PairingDraft = Readonly<{
  firstParticipantId: string;
  secondParticipantId: string;
}>;

type EligibleParticipant = Roster["participants"][number];
type PairingMode = PairingConfigurationRequest["pairing_mode"];
type CategoryMode = PairingConfigurationRequest["category_mode"];
type Category = PairingConfigurationRequest["categories"][number];

const CATEGORY_LABELS: Readonly<Record<Category, string>> = {
  web: "Web",
  crypto: "Crypto",
  forensics: "Forensics",
  reverse: "Reverse",
  pwn: "Pwn",
  steganography: "Steganography",
  ppc: "PPC",
  osint: "OSINT",
  mobile: "Mobile",
  hardware: "Hardware",
  misc: "Misc",
};

const CATEGORY_MODES: readonly CategoryMode[] = ["random", "admin", "draft"];
const CATEGORY_MODE_LABELS: Readonly<Record<CategoryMode, string>> = {
  random: "Случайная политика",
  admin: "Политика оператора",
  draft: "Драфт",
};

const CATEGORIES = Object.keys(CATEGORY_LABELS) as Category[];

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const problemMessage = (error: unknown, fallback: string): string => {
  if (error instanceof ApiError) {
    return error.problem?.detail || error.problem?.title || fallback;
  }
  return fallback;
};

const participantSort = (first: EligibleParticipant, second: EligibleParticipant): number =>
  first.seed - second.seed || first.id.localeCompare(second.id);

const blankPairings = (participantCount: number): PairingDraft[] =>
  Array.from({ length: Math.floor(participantCount / 2) }, () => ({
    firstParticipantId: "",
    secondParticipantId: "",
  }));

const nextRoundFromConfiguration = (
  rounds: ReadonlyArray<{
    round_number: number;
    consumed: boolean;
    disclosed: boolean;
    started: boolean;
  }>,
): number => {
  const pending = rounds
    .filter((round) => !round.consumed && !round.disclosed && !round.started)
    .sort((first, second) => first.round_number - second.round_number)[0];
  if (pending) {
    return pending.round_number;
  }
  const highest = rounds.reduce(
    (value, round) => Math.max(value, round.round_number),
    0,
  );
  return Math.min(4, highest + 1);
};

const participantLabel = (
  participantId: string,
  participantById: ReadonlyMap<string, EligibleParticipant>,
  playerById: ReadonlyMap<string, AdminPlayer>,
): string => {
  const participant = participantById.get(participantId);
  if (!participant) {
    return "Участник недоступен";
  }
  const player = playerById.get(participant.player_id);
  return player?.username || `Участник ${participant.seed}`;
};

const validateManualDraft = (
  pairings: readonly PairingDraft[],
  byeParticipantId: string,
  eligibleParticipants: readonly EligibleParticipant[],
): string | null => {
  const eligibleIds = new Set(eligibleParticipants.map((participant) => participant.id));
  const expectedPairingCount = Math.floor(eligibleParticipants.length / 2);
  if (pairings.length !== expectedPairingCount) {
    return "Количество пар не соответствует числу присутствующих участников.";
  }

  const seen = new Set<string>();
  for (const [index, pairing] of pairings.entries()) {
    if (!pairing.firstParticipantId || !pairing.secondParticipantId) {
      return `Заполните обоих участников пары ${index + 1}.`;
    }
    if (!eligibleIds.has(pairing.firstParticipantId) || !eligibleIds.has(pairing.secondParticipantId)) {
      return "В ручной сетке есть участник, которого нет среди присутствующих.";
    }
    if (pairing.firstParticipantId === pairing.secondParticipantId) {
      return `В паре ${index + 1} нельзя указать одного и того же участника дважды.`;
    }
    if (seen.has(pairing.firstParticipantId) || seen.has(pairing.secondParticipantId)) {
      return "Участник не может встречаться более одного раза в этом раунде.";
    }
    seen.add(pairing.firstParticipantId);
    seen.add(pairing.secondParticipantId);
  }

  if (eligibleParticipants.length % 2 === 1) {
    if (!byeParticipantId) {
      return "Для нечетного состава выберите ровно одного участника с bye.";
    }
    if (!eligibleIds.has(byeParticipantId)) {
      return "Участник с bye должен входить в список присутствующих.";
    }
    if (seen.has(byeParticipantId)) {
      return "Участник с bye не должен одновременно входить в пару.";
    }
    seen.add(byeParticipantId);
  } else if (byeParticipantId) {
    return "Для четного состава bye не используется.";
  }

  if (seen.size !== eligibleParticipants.length) {
    return "Каждый присутствующий участник должен быть указан ровно один раз: в паре или как bye.";
  }
  return null;
};

const isRepeatProblem = (error: ApiError): boolean => {
  const detail = `${error.problem?.detail || ""} ${error.problem?.title || ""}`.toLocaleLowerCase("ru-RU");
  return /повтор|повторн|repeat|встречал|already/.test(detail);
};

export const SwissPairingEditor = ({
  onReloadTournaments,
  onSelectTournament,
  onSessionExpired,
  selectedTournament,
  selectedTournamentId,
  tournaments,
}: SwissPairingEditorProps) => {
  const tournamentId = selectedTournament?.id ?? "";
  const loadControllerRef = useRef<AbortController | null>(null);
  const submitControllerRef = useRef<AbortController | null>(null);
  const loadRunRef = useRef(0);
  const submittingRef = useRef(false);
  const [players, setPlayers] = useState<AdminPlayer[]>([]);
  const [roster, setRoster] = useState<Roster | null>(null);
  const [configuration, setConfiguration] = useState<Awaited<ReturnType<typeof getTournamentConfiguration>> | null>(null);
  const [snapshot, setSnapshot] = useState<Awaited<ReturnType<typeof operatorApi.getSnapshot>> | null>(null);
  const [loadState, setLoadState] = useState<LoadState>("ready");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [pairingMode, setPairingMode] = useState<PairingMode>("automatic");
  const [categoryMode, setCategoryMode] = useState<CategoryMode>("random");
  const [categories, setCategories] = useState<Category[]>([]);
  const [roundNumber, setRoundNumber] = useState(1);
  const [draftPairings, setDraftPairings] = useState<PairingDraft[]>([]);
  const [byeParticipantId, setByeParticipantId] = useState("");
  const [savedRound, setSavedRound] = useState<SwissRound | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const loadTournamentData = useCallback(async (id: string): Promise<void> => {
    loadControllerRef.current?.abort();
    submitControllerRef.current?.abort();
    const controller = new AbortController();
    const runId = loadRunRef.current + 1;
    loadRunRef.current = runId;
    loadControllerRef.current = controller;
    setLoadState("loading");
    setLoadError(null);
    setFormError(null);
    setNotice(null);
    setSavedRound(null);
    try {
      const [currentRoster, activePlayers, currentConfiguration, currentSnapshot] = await Promise.all([
        operatorApi.getRoster(id, controller.signal),
        adminApi.listPlayers(false, controller.signal),
        getTournamentConfiguration(id, controller.signal),
        operatorApi.getSnapshot(id, undefined, controller.signal),
      ]);
      if (controller.signal.aborted || loadRunRef.current !== runId) {
        return;
      }
      const eligible = currentRoster.participants
        .filter((participant) => participant.attendance === "checked_in")
        .sort(participantSort);
      const nextRound = nextRoundFromConfiguration(currentConfiguration.rounds);
      setRoster(currentRoster);
      setPlayers(activePlayers);
      setConfiguration(currentConfiguration);
      setSnapshot(currentSnapshot);
      setRoundNumber(nextRound);
      setPairingMode("automatic");
      setCategoryMode(currentConfiguration.swiss_default.mode);
      setCategories([...currentConfiguration.swiss_default.categories]);
      setDraftPairings(blankPairings(eligible.length));
      setByeParticipantId("");
      setLoadState("ready");
    } catch (error) {
      if (controller.signal.aborted || loadRunRef.current !== runId || isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      setLoadState("error");
      setLoadError(problemMessage(error, "Не удалось загрузить данные для формирования пар"));
    } finally {
      if (loadControllerRef.current === controller) {
        loadControllerRef.current = null;
      }
    }
  }, [onSessionExpired]);

  useEffect(() => {
    if (!tournamentId) {
      loadControllerRef.current?.abort();
      submitControllerRef.current?.abort();
      loadRunRef.current += 1;
      setPlayers([]);
      setRoster(null);
      setConfiguration(null);
      setSnapshot(null);
      setSavedRound(null);
      setDraftPairings([]);
      setByeParticipantId("");
      setLoadState("ready");
      setLoadError(null);
      setFormError(null);
      setNotice(null);
      return;
    }
    void loadTournamentData(tournamentId);
    return () => {
      loadControllerRef.current?.abort();
      submitControllerRef.current?.abort();
    };
  }, [loadTournamentData, tournamentId]);

  useEffect(() => () => {
    loadControllerRef.current?.abort();
    submitControllerRef.current?.abort();
  }, []);

  const eligibleParticipants = useMemo(
    () =>
      (roster?.participants ?? [])
        .filter((participant) => participant.attendance === "checked_in")
        .sort(participantSort),
    [roster],
  );
  const participantById = useMemo(
    () => new Map(eligibleParticipants.map((participant) => [participant.id, participant])),
    [eligibleParticipants],
  );
  const playerById = useMemo(
    () => new Map(players.map((player) => [player.id, player])),
    [players],
  );
  const activeConfigurationRound = useMemo(
    () => configuration?.rounds.find((round) => round.round_number === roundNumber) ?? null,
    [configuration, roundNumber],
  );
  const editingLocked = Boolean(
    savedRound?.locked ||
      activeConfigurationRound?.locked ||
      activeConfigurationRound?.started ||
      activeConfigurationRound?.consumed ||
      activeConfigurationRound?.disclosed,
  );

  const updatePairingMode = (nextMode: PairingMode): void => {
    setPairingMode(nextMode);
    setFormError(null);
    setNotice(null);
    if (nextMode === "manual") {
      setDraftPairings((current) => {
        const expected = Math.floor(eligibleParticipants.length / 2);
        return current.length === expected ? current : blankPairings(eligibleParticipants.length);
      });
    }
  };

  const updateCategory = (category: Category, checked: boolean): void => {
    setCategories((current) => {
      if (checked) {
        return current.includes(category) ? current : [...current, category];
      }
      return current.filter((item) => item !== category);
    });
    setFormError(null);
    setNotice(null);
  };

  const updatePairing = (
    index: number,
    field: keyof PairingDraft,
    value: string,
  ): void => {
    setDraftPairings((current) =>
      current.map((pairing, pairingIndex) =>
        pairingIndex === index ? { ...pairing, [field]: value } : pairing,
      ),
    );
    setFormError(null);
    setNotice(null);
  };

  const handleReload = (): void => {
    if (!tournamentId || submittingRef.current) {
      return;
    }
    void loadTournamentData(tournamentId);
  };

  const handleSubmit = async (): Promise<void> => {
    if (
      !selectedTournament ||
      !roster ||
      !configuration ||
      !snapshot ||
      loadState !== "ready" ||
      editingLocked ||
      submittingRef.current
    ) {
      return;
    }
    if (categories.length === 0) {
      setFormError("Выберите хотя бы одну категорию для Swiss раунда.");
      return;
    }
    if (pairingMode === "manual") {
      const validationError = validateManualDraft(
        draftPairings,
        byeParticipantId,
        eligibleParticipants,
      );
      if (validationError) {
        setFormError(validationError);
        return;
      }
    }

    submittingRef.current = true;
    setSubmitting(true);
    setFormError(null);
    setNotice(null);
    submitControllerRef.current?.abort();
    const controller = new AbortController();
    submitControllerRef.current = controller;
    try {
      const freshSnapshot = await operatorApi.getSnapshot(
        selectedTournament.id,
        undefined,
        controller.signal,
      );
      if (controller.signal.aborted) {
        return;
      }
      setSnapshot(freshSnapshot);
      const body: PairingConfigurationRequest = {
        expected_projection_revision: freshSnapshot.next_cursor.projection_revision,
        round_number: roundNumber,
        pairing_mode: pairingMode,
        category_mode: categoryMode,
        categories: [...categories],
        ...(pairingMode === "manual"
          ? {
              manual_pairings: draftPairings.map((pairing) => ({
                first_participant_id: pairing.firstParticipantId,
                second_participant_id: pairing.secondParticipantId,
              })),
              manual_bye_participant_id: byeParticipantId || null,
            }
          : {}),
      };
      const configuredRound = await operatorApi.configurePairings(
        selectedTournament.id,
        body,
        createOperatorCommandIntent(),
        controller.signal,
      );
      if (controller.signal.aborted) {
        return;
      }
      setSavedRound(configuredRound);
      setNotice(`Раунд ${configuredRound.round_number} сформирован сервером.`);
      await onReloadTournaments();
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
        setFormError(
          `${problemMessage(error, "Состояние турнира изменилось.")} Результат предыдущего сохранения оставлен на экране. Перезагрузите данные и повторите попытку.`,
        );
      } else if (error instanceof ApiError && error.status === 422) {
        const repeatMessage = isRepeatProblem(error)
          ? "Повторные пары запрещены сервером. Выберите участников, которые еще не встречались."
          : "Сервер отклонил ручную сетку. Проверьте пары, bye и состав участников.";
        setFormError(`${repeatMessage} ${problemMessage(error, "")}`.trim());
      } else {
        setFormError(problemMessage(error, "Не удалось сформировать Swiss раунд"));
      }
    } finally {
      if (submitControllerRef.current === controller) {
        submitControllerRef.current = null;
      }
      submittingRef.current = false;
      setSubmitting(false);
    }
  };

  const roundParticipantLabel = useCallback(
    (participantId: string): string =>
      participantLabel(participantId, participantById, playerById),
    [participantById, playerById],
  );

  return (
    <Panel
      title="Пары Swiss"
      description="Сформируйте следующий раунд на основе актуального серверного состояния турнира."
      className={styles.panel}
    >
      <div className={styles.chooser}>
        <label htmlFor="pairing-tournament-select">Турнир для формирования пар</label>
        <select
          id="pairing-tournament-select"
          name="pairing_tournament"
          value={selectedTournamentId}
          onChange={(event) => onSelectTournament(event.target.value)}
          disabled={submitting}
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
          Выберите турнир, чтобы загрузить состав и сформировать пары.
        </Message>
      )}

      {selectedTournament && loadState === "loading" && (
        <Message tone="loading" title="Загружаем состояние Swiss">
          Получаем состав, активных игроков, конфигурацию и серверный snapshot.
        </Message>
      )}

      {selectedTournament && loadState === "error" && (
        <Message tone="error" title="Данные Swiss недоступны">
          {loadError || "Сервер не вернул данные для формирования пар."}
          <button className={styles.inlineAction} type="button" onClick={handleReload}>
            Повторить загрузку
          </button>
        </Message>
      )}

      {selectedTournament && loadState === "ready" && roster && configuration && snapshot && (
        <div className={styles.content}>
          <div className={styles.summary}>
            <div className={styles.summaryHeading}>
              <div>
                <h3 className={styles.title}>{selectedTournament.name}</h3>
                <p className={styles.subtitle}>
                  Следующий раунд определяется конфигурацией сервера и не редактируется локально.
                </p>
              </div>
              <Status tone={editingLocked ? "disabled" : "info"}>
                {editingLocked ? "Раунд заблокирован" : "Раунд доступен"}
              </Status>
            </div>
            <dl className={styles.meta}>
              <div>
                <dt>Следующий раунд</dt>
                <dd>{roundNumber}</dd>
              </div>
              <div>
                <dt>Текущая ревизия</dt>
                <dd>{snapshot.next_cursor.projection_revision}</dd>
              </div>
              <div>
                <dt>Присутствуют</dt>
                <dd>{eligibleParticipants.length}</dd>
              </div>
              <div>
                <dt>Конфигурация</dt>
                <dd>{configuration.configuration_revision}</dd>
              </div>
            </dl>
          </div>

          {editingLocked && (
            <Message tone="warning" title="Раунд доступен только для просмотра">
              Конфигурация или серверный раунд уже заблокированы, начаты или использованы.
            </Message>
          )}

          <section className={styles.editor} aria-labelledby="swiss-pairing-editor-title">
            <div className={styles.sectionHeading}>
              <div>
                <h4 id="swiss-pairing-editor-title" className={styles.sectionTitle}>
                  Настройки раунда {roundNumber}
                </h4>
                <p className={styles.sectionDescription}>
                  Категории берутся из политики swiss_default и могут быть уточнены перед отправкой.
                </p>
              </div>
              <Status tone="info" size="small">
                swiss_default: {CATEGORY_MODE_LABELS[configuration.swiss_default.mode]}
              </Status>
            </div>

            <fieldset className={styles.modeFieldset} disabled={editingLocked || submitting}>
              <legend>Способ формирования пар</legend>
              <label className={styles.radioOption}>
                <input
                  type="radio"
                  name="pairing-mode"
                  value="automatic"
                  checked={pairingMode === "automatic"}
                  onChange={() => updatePairingMode("automatic")}
                />
                <span>
                  <strong>Автоматически</strong>
                  <small>Пары и bye полностью выбирает сервер.</small>
                </span>
              </label>
              <label className={styles.radioOption}>
                <input
                  type="radio"
                  name="pairing-mode"
                  value="manual"
                  checked={pairingMode === "manual"}
                  onChange={() => updatePairingMode("manual")}
                />
                <span>
                  <strong>Вручную</strong>
                  <small>Укажите полное покрытие присутствующих участников.</small>
                </span>
              </label>
            </fieldset>

            <div className={styles.fieldsGrid}>
              <div className={styles.field}>
                <label htmlFor="swiss-category-mode">Политика категорий</label>
                <select
                  id="swiss-category-mode"
                  value={categoryMode}
                  onChange={(event) => {
                    setCategoryMode(event.target.value as CategoryMode);
                    setFormError(null);
                    setNotice(null);
                  }}
                  disabled={editingLocked || submitting}
                >
                  {CATEGORY_MODES.map((mode) => (
                    <option key={mode} value={mode}>
                      {CATEGORY_MODE_LABELS[mode]}
                    </option>
                  ))}
                </select>
              </div>
              <div className={styles.categoryField}>
                <span className={styles.fieldLabel}>Категории раунда</span>
                <div className={styles.categoryGrid}>
                  {CATEGORIES.map((category) => (
                    <label className={styles.categoryOption} key={category}>
                      <input
                        type="checkbox"
                        checked={categories.includes(category)}
                        onChange={(event) => updateCategory(category, event.target.checked)}
                        disabled={editingLocked || submitting}
                      />
                      <span>{CATEGORY_LABELS[category]}</span>
                    </label>
                  ))}
                </div>
              </div>
            </div>

            {pairingMode === "automatic" ? (
              <Message tone="info" title="Автоматический план">
                Локальные пары не создаются. После отправки здесь отобразится полный план, который вернул сервер.
              </Message>
            ) : (
              <section className={styles.manualEditor} aria-labelledby="manual-pairing-title">
                <div>
                  <h5 id="manual-pairing-title" className={styles.sectionTitle}>
                    Ручная сетка
                  </h5>
                  <p className={styles.sectionDescription}>
                    Доступны только участники со статусом «На месте». Повторные встречи дополнительно проверяет сервер.
                  </p>
                </div>
                {eligibleParticipants.length === 0 ? (
                  <Message tone="empty" title="Нет присутствующих участников">
                    Отметьте участников как «На месте» в редакторе состава.
                  </Message>
                ) : (
                  <>
                    <div className={styles.pairList} aria-label="Ручные пары">
                      {draftPairings.map((pairing, index) => (
                        <fieldset className={styles.pairRow} key={`pair-${index + 1}`} disabled={editingLocked || submitting}>
                          <legend>Пара {index + 1}</legend>
                          <div className={styles.field}>
                            <label htmlFor={`pair-first-${index}`}>Пара {index + 1} - первый участник</label>
                            <select
                              id={`pair-first-${index}`}
                              value={pairing.firstParticipantId}
                              onChange={(event) => updatePairing(index, "firstParticipantId", event.target.value)}
                            >
                              <option value="">Выберите участника</option>
                              {eligibleParticipants.map((participant) => (
                                <option key={participant.id} value={participant.id}>
                                  {participantLabel(participant.id, participantById, playerById)}
                                </option>
                              ))}
                            </select>
                          </div>
                          <div className={styles.field}>
                            <label htmlFor={`pair-second-${index}`}>Пара {index + 1} - второй участник</label>
                            <select
                              id={`pair-second-${index}`}
                              value={pairing.secondParticipantId}
                              onChange={(event) => updatePairing(index, "secondParticipantId", event.target.value)}
                            >
                              <option value="">Выберите участника</option>
                              {eligibleParticipants.map((participant) => (
                                <option key={participant.id} value={participant.id}>
                                  {participantLabel(participant.id, participantById, playerById)}
                                </option>
                              ))}
                            </select>
                          </div>
                        </fieldset>
                      ))}
                    </div>
                    {eligibleParticipants.length % 2 === 1 && (
                      <div className={styles.field}>
                        <label htmlFor="swiss-bye-participant">Участник с bye</label>
                        <select
                          id="swiss-bye-participant"
                          value={byeParticipantId}
                          onChange={(event) => {
                            setByeParticipantId(event.target.value);
                            setFormError(null);
                            setNotice(null);
                          }}
                          disabled={editingLocked || submitting}
                        >
                          <option value="">Выберите одного участника</option>
                          {eligibleParticipants.map((participant) => (
                            <option key={participant.id} value={participant.id}>
                              {participantLabel(participant.id, participantById, playerById)}
                            </option>
                          ))}
                        </select>
                        <span className={styles.fieldHint}>
                          Bye должен быть выбран ровно один раз и не может входить в пару.
                        </span>
                      </div>
                    )}
                  </>
                )}
              </section>
            )}

            {formError && (
              <Message tone="error" title="Пары не сохранены">
                {formError}
                <button
                  className={styles.inlineAction}
                  type="button"
                  onClick={handleReload}
                  disabled={submitting}
                >
                  Перезагрузить данные
                </button>
              </Message>
            )}
            {notice && (
              <Message tone="success" title="Раунд сохранен">
                {notice}
              </Message>
            )}

            <div className={styles.actions}>
              <Button
                type="button"
                onClick={() => void handleSubmit()}
                loading={submitting}
                loadingLabel="Формируем пары"
                disabled={editingLocked || submitting || eligibleParticipants.length < 2}
              >
                Сформировать пары
              </Button>
              <Button
                type="button"
                variant="secondary"
                onClick={handleReload}
                disabled={submitting}
              >
                Обновить состояние
              </Button>
            </div>
          </section>

          {savedRound && (
            <section className={styles.result} aria-labelledby="swiss-result-title">
              <div className={styles.sectionHeading}>
                <div>
                  <h4 id="swiss-result-title" className={styles.sectionTitle}>
                    Серверный план раунда {savedRound.round_number}
                  </h4>
                  <p className={styles.sectionDescription}>
                    Показаны все пары, bye и standings из последнего принятого ответа сервера.
                  </p>
                </div>
                <Status tone={savedRound.locked ? "disabled" : "success"}>
                  {savedRound.locked ? "Заблокирован" : "Сохранен"}
                </Status>
              </div>
              <dl className={styles.resultMeta}>
                <div>
                  <dt>Ревизия раунда</dt>
                  <dd>{savedRound.revision}</dd>
                </div>
                <div>
                  <dt>Статус блокировки</dt>
                  <dd>{savedRound.locked ? "Да" : "Нет"}</dd>
                </div>
                <div>
                  <dt>Пары</dt>
                  <dd>{savedRound.pairings.length}</dd>
                </div>
              </dl>
              <div className={styles.resultGrid}>
                <div>
                  <h5 className={styles.subheading}>Пары</h5>
                  <ol className={styles.pairResultList}>
                    {savedRound.pairings.map((pairing) => (
                      <li key={pairing.id}>
                        <span>{roundParticipantLabel(pairing.first_participant_id)}</span>
                        <span aria-hidden="true"> vs </span>
                        <span>{roundParticipantLabel(pairing.second_participant_id)}</span>
                        {pairing.repeated && <Status tone="error" size="small">Повтор</Status>}
                      </li>
                    ))}
                  </ol>
                </div>
                <div>
                  <h5 className={styles.subheading}>Bye</h5>
                  {savedRound.bye ? (
                    <p className={styles.byeResult}>
                      <strong>{roundParticipantLabel(savedRound.bye.participant_id)}</strong>
                      <span>{savedRound.bye.points_awarded} очк.</span>
                    </p>
                  ) : (
                    <p className={styles.emptyResult}>Bye нет.</p>
                  )}
                </div>
              </div>
              <div className={styles.standings}>
                <h5 className={styles.subheading}>Standings</h5>
                <div className={styles.tableWrap}>
                  <table>
                    <caption className={styles.srOnly}>Standings раунда {savedRound.round_number}</caption>
                    <thead>
                      <tr>
                        <th scope="col">Место</th>
                        <th scope="col">Участник</th>
                        <th scope="col">Очки</th>
                        <th scope="col">Buchholz</th>
                      </tr>
                    </thead>
                    <tbody>
                      {savedRound.standings.map((standing) => (
                        <tr key={standing.participant_id}>
                          <td>{standing.position}</td>
                          <td>{roundParticipantLabel(standing.participant_id)}</td>
                          <td>{standing.points}</td>
                          <td>{standing.buchholz}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            </section>
          )}
        </div>
      )}
    </Panel>
  );
};

SwissPairingEditor.displayName = "SwissPairingEditor";
