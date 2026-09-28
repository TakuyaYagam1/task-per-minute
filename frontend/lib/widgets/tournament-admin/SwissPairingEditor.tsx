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

import { useAdminLiveRefresh } from "../../features/admin-live";
import styles from "./SwissPairingEditor.module.css";

type SwissPairingEditorProps = Readonly<{
  tournaments: readonly Tournament[];
  selectedTournament: Tournament | null;
  selectedTournamentId: string;
  onSelectTournament: (id: string) => void;
  onNavigateToConduct?: () => void;
  onReloadTournaments: () => Promise<void>;
  onSessionExpired?: () => void;
  onDirtyChange?: (dirty: boolean) => void;
  showTournamentChooser?: boolean;
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

const categoryModeDescription = (mode: CategoryMode): string => {
  switch (mode) {
    case "admin":
      return "Выберите одну категорию для всех матчей раунда.";
    case "draft":
      return "Участники по очереди исключают категории, пока не останется одна.";
    default:
      return "Категория каждого матча выбирается случайно из списка.";
  }
};

const genericValidationMessage = (
  pairingMode: PairingMode,
  categoryMode: CategoryMode,
): string => {
  if (pairingMode === "manual") {
    return "Сервер отклонил настройки. Проверьте ручные пары, bye и политику категорий.";
  }
  return `Сервер отклонил настройки. ${categoryModeDescription(categoryMode)}`;
};

const isGenericValidationDetail = (detail: string): boolean =>
  /validation failed|bad request|invalid request|ошибк[аи] валидации/i.test(detail);

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

type PairingLifecycleMessage = Readonly<{
  statusLabel: string;
  title: string;
  detail: string;
}>;

const pairingLifecycleMessage = (
  state: Tournament["state"],
  pausedFromState: Tournament["paused_from_state"],
): PairingLifecycleMessage => {
  switch (state) {
    case "draft":
      return {
        statusLabel: "Соревнование не запущено",
        title: "Подготовьте соревнование к запуску",
        detail: 'Откройте регистрацию, отметьте присутствующих участников, затем нажмите "Зафиксировать состав" и "Начать квалификацию".',
      };
    case "registration":
      return {
        statusLabel: "Соревнование не запущено",
        title: "Сначала запустите соревнование",
        detail: 'Проверьте состав, затем нажмите "Зафиксировать состав" и "Начать квалификацию". После этого можно формировать пары.',
      };
    case "roster_locked":
      return {
        statusLabel: "Соревнование не запущено",
        title: "Сначала запустите соревнование",
        detail: 'Нажмите "Начать квалификацию". После этого можно формировать пары.',
      };
    case "swiss":
      return {
        statusLabel: "Раунд доступен",
        title: "Раунд доступен",
        detail: "Можно сформировать следующий раунд.",
      };
    case "technical_pause":
      if (pausedFromState === "swiss") {
        return {
          statusLabel: "Соревнование приостановлено",
          title: "Возобновите соревнование",
          detail: "Возобновите соревнование, чтобы продолжить формирование пар.",
        };
      }
      return {
        statusLabel: "Этап приостановлен",
        title: "Возобновите соревнование",
        detail: "Сейчас соревнование приостановлено. Новые пары можно формировать только во время квалификации.",
      };
    case "golden":
    case "playoffs":
      return {
        statusLabel: "Этап завершен",
        title: "Квалификация завершена",
        detail: "Соревнование уже перешло к следующему этапу. Формировать новые пары здесь больше не нужно.",
      };
    case "completed":
      return {
        statusLabel: "Соревнование завершено",
        title: "Соревнование завершено",
        detail: "Формирование пар недоступно для завершенного соревнования.",
      };
    case "cancelled":
      return {
        statusLabel: "Соревнование отменено",
        title: "Соревнование отменено",
        detail: "Формирование пар недоступно для отмененного соревнования.",
      };
    default:
      return {
        statusLabel: "Действие недоступно",
        title: "Состояние соревнования изменилось",
        detail: "Состояние соревнования изменилось. Повторите попытку.",
      };
  }
};

export const SwissPairingEditor = ({
  onNavigateToConduct,
  onReloadTournaments,
  onSelectTournament,
  onSessionExpired,
  selectedTournament,
  selectedTournamentId,
  tournaments,
  onDirtyChange,
  showTournamentChooser = true,
}: SwissPairingEditorProps) => {
  const tournamentId = selectedTournament?.id ?? "";
  const loadControllerRef = useRef<AbortController | null>(null);
  const submitControllerRef = useRef<AbortController | null>(null);
  const loadRunRef = useRef(0);
  const submittingRef = useRef(false);
  const draftDirtyRef = useRef(false);
  const reportDirty = useCallback((dirty: boolean) => {
    draftDirtyRef.current = dirty;
    onDirtyChange?.(dirty);
  }, [onDirtyChange]);
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

  useEffect(() => {
    if (!selectedTournamentId) {
      reportDirty(false);
    }
  }, [reportDirty, selectedTournamentId]);

  const loadTournamentData = useCallback(async (id: string, silent = false): Promise<void> => {
    loadControllerRef.current?.abort();
    if (!silent) submitControllerRef.current?.abort();
    const controller = new AbortController();
    const runId = loadRunRef.current + 1;
    loadRunRef.current = runId;
    loadControllerRef.current = controller;
    if (!silent) {
      setLoadState("loading");
      setLoadError(null);
      setFormError(null);
      setNotice(null);
      setSavedRound(null);
    }
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
      if (!silent || !draftDirtyRef.current) {
        setRoundNumber(nextRound);
        setPairingMode("automatic");
        const officialCategories = currentConfiguration.category_pools.find(
          (pool) => pool.format === "bo1",
        )?.categories ?? [];
        const configuredCategoryMode = currentConfiguration.swiss_default.mode;
        const configuredAdminCategory = currentConfiguration.swiss_default.categories.find(
          (category) => officialCategories.includes(category),
        ) ?? officialCategories[0];
        setCategoryMode(configuredCategoryMode);
        setCategories(
          configuredCategoryMode === "admin" && configuredAdminCategory
            ? [configuredAdminCategory]
            : [...officialCategories],
        );
        setDraftPairings(blankPairings(eligible.length));
        setByeParticipantId("");
        reportDirty(false);
      }
      setLoadState("ready");
    } catch (error) {
      if (controller.signal.aborted || loadRunRef.current !== runId || isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (!silent) {
        setLoadState("error");
        setLoadError(problemMessage(error, "Не удалось загрузить данные для формирования пар"));
      }
    } finally {
      if (loadControllerRef.current === controller) {
        loadControllerRef.current = null;
      }
    }
  }, [reportDirty, onSessionExpired]);

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
      setPairingMode("automatic");
      setCategoryMode("random");
      setCategories([]);
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

  useAdminLiveRefresh(["tournaments", "players"], async () => {
    if (tournamentId) await loadTournamentData(tournamentId, true);
  }, Boolean(tournamentId) && !submitting && loadState !== "loading");

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
  const swissPoolCategories = useMemo(
    () => configuration?.category_pools.find((pool) => pool.format === "bo1")?.categories ?? [],
    [configuration],
  );
  const editingLocked = Boolean(
    savedRound?.locked ||
      activeConfigurationRound?.locked ||
      activeConfigurationRound?.started ||
      activeConfigurationRound?.consumed ||
      activeConfigurationRound?.disclosed,
  );
  const currentTournament = snapshot?.tournament ?? selectedTournament;
  const pairingLifecycle = currentTournament
    ? pairingLifecycleMessage(
        currentTournament.state,
        currentTournament.paused_from_state,
      )
    : null;
  const pairingStateAllowed = currentTournament?.state === "swiss";
  const pairingFormDisabled = !pairingStateAllowed || editingLocked || submitting;

  const updatePairingMode = (nextMode: PairingMode): void => {
    reportDirty(true);
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

  const updateCategoryMode = (nextMode: CategoryMode): void => {
    reportDirty(true);
    const selectedCategory = swissPoolCategories.find((category) => categories.includes(category)) ?? swissPoolCategories[0];
    setCategoryMode(nextMode);
    setCategories(
      nextMode === "admin" && selectedCategory
        ? [selectedCategory]
        : [...swissPoolCategories],
    );
    setFormError(null);
    setNotice(null);
  };

  const updateAdminCategory = (category: Category): void => {
    if (!swissPoolCategories.includes(category)) {
      return;
    }
    reportDirty(true);
    setCategories([category]);
    setFormError(null);
    setNotice(null);
  };

  const updatePairing = (
    index: number,
    field: keyof PairingDraft,
    value: string,
  ): void => {
    reportDirty(true);
    setDraftPairings((current) =>
      current.map((pairing, pairingIndex) =>
        pairingIndex === index ? { ...pairing, [field]: value } : pairing,
      ),
    );
    setFormError(null);
    setNotice(null);
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
    if (!pairingStateAllowed) {
      setFormError(
        pairingLifecycle?.detail ||
          "Обновите состояние соревнования перед формированием пар.",
      );
      void onReloadTournaments();
      return;
    }
    const officialCategories = configuration.category_pools.find(
      (pool) => pool.format === "bo1",
    )?.categories ?? [];
    if (officialCategories.length === 0) {
      setFormError("Для этого соревнования пока нет доступных категорий.");
      return;
    }
    const selectedCategory = categories.length === 1 && officialCategories.includes(categories[0])
      ? categories[0]
      : null;
    if (categoryMode === "admin" && !selectedCategory) {
      setFormError("Выберите одну из доступных категорий раунда.");
      return;
    }
    const requestCategories: Category[] = categoryMode === "admin" && selectedCategory
      ? [selectedCategory]
      : [...officialCategories];
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
    const submitLoadRun = loadRunRef.current;
    const canApply = (): boolean =>
      !controller.signal.aborted && loadRunRef.current === submitLoadRun;
    try {
      const freshSnapshot = await operatorApi.getSnapshot(
        selectedTournament.id,
        undefined,
        controller.signal,
      );
      if (!canApply()) {
        return;
      }
      if (freshSnapshot.tournament.id !== selectedTournament.id) {
        setFormError(
          "Данные относятся к другому соревнованию. Обновите данные и повторите попытку.",
        );
        void onReloadTournaments();
        return;
      }
      setSnapshot(freshSnapshot);
      if (freshSnapshot.tournament.state !== "swiss") {
        const freshLifecycle = pairingLifecycleMessage(
          freshSnapshot.tournament.state,
          freshSnapshot.tournament.paused_from_state,
        );
        setFormError(freshLifecycle.detail);
        void onReloadTournaments();
        return;
      }
      const body: PairingConfigurationRequest = {
        expected_projection_revision: freshSnapshot.next_cursor.projection_revision,
        round_number: roundNumber,
        pairing_mode: pairingMode,
        category_mode: categoryMode,
        categories: requestCategories,
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
      if (!canApply()) {
        return;
      }
      setSavedRound(configuredRound);
      setNotice(`Раунд ${configuredRound.round_number} сформирован. Следующий шаг - в разделе "Проведение" открыть готовность и дождаться участников.`);
      reportDirty(false);
      await onReloadTournaments();
    } catch (error) {
      if (!canApply() || isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 400) {
        const detail = problemMessage(error, "");
        setFormError(
          isGenericValidationDetail(detail)
            ? genericValidationMessage(pairingMode, categoryMode)
            : detail || genericValidationMessage(pairingMode, categoryMode),
        );
      } else if (error instanceof ApiError && error.status === 409) {
        setFormError(
          `${problemMessage(error, "Состояние соревнования изменилось.")} Настройки сохранены в форме. Повторите отправку после автоматического обновления.`,
        );
        await loadTournamentData(tournamentId, true);
      } else if (error instanceof ApiError && error.status === 422) {
        const detail = problemMessage(error, "");
        if (pairingMode === "manual") {
          const repeatMessage = isRepeatProblem(error)
            ? "Повторные пары запрещены. Выберите участников, которые еще не встречались."
            : "Не удалось принять ручную сетку. Проверьте пары, bye и состав участников.";
          setFormError(`${repeatMessage} ${detail}`.trim());
        } else {
          const automaticMessage = categoryMode === "admin"
            ? "Не удалось сохранить пары. Выберите одну из доступных категорий раунда."
            : `Не удалось сохранить автоматические пары: ${categoryModeDescription(categoryMode)}`;
          setFormError(`${automaticMessage} ${detail}`.trim());
        }
      } else {
        setFormError(problemMessage(error, "Не удалось сформировать раунд квалификации"));
      }
    } finally {
      if (submitControllerRef.current === controller) {
        submitControllerRef.current = null;
        submittingRef.current = false;
        setSubmitting(false);
      }
    }
  };

  const roundParticipantLabel = useCallback(
    (participantId: string): string =>
      participantLabel(participantId, participantById, playerById),
    [participantById, playerById],
  );

  return (
    <Panel
      title="Пары квалификации"
      description="Сформируйте следующий раунд на основе актуальных данных соревнования."
      className={styles.panel}
    >
      {showTournamentChooser ? <div className={styles.chooser}>
        <label htmlFor="pairing-tournament-select">Соревнование для формирования пар</label>
        <select
          id="pairing-tournament-select"
          name="pairing_tournament"
          value={selectedTournamentId}
          onChange={(event) => onSelectTournament(event.target.value)}
          disabled={submitting}
        >
          <option value="">Выберите соревнование</option>
          {tournaments.map((tournament) => (
            <option key={tournament.id} value={tournament.id}>
              {tournament.name}
            </option>
          ))}
        </select>
      </div> : null}

      {!selectedTournament && (
        <Message tone="empty" title="Соревнование не выбрано">
          Выберите соревнование, чтобы загрузить состав и сформировать пары.
        </Message>
      )}

      {selectedTournament && loadState === "loading" && (
        <Message tone="loading" title="Загружаем состояние квалификации">
          Получаем состав участников и настройки раунда.
        </Message>
      )}

      {selectedTournament && loadState === "error" && (
        <Message tone="error" title="Данные квалификации недоступны">
          {loadError || "Не удалось получить данные для формирования пар."}
        </Message>
      )}

      {selectedTournament && loadState === "ready" && roster && configuration && snapshot && (
        <div className={styles.content}>
          <div className={styles.summary}>
            <div className={styles.summaryHeading}>
              <div>
                <h3 className={styles.title}>{selectedTournament.name}</h3>
                <p className={styles.subtitle}>
                  Следующий раунд определяется правилами соревнования и не редактируется здесь.
                </p>
              </div>
              <Status tone={editingLocked || !pairingStateAllowed ? "disabled" : "info"}>
                {editingLocked
                  ? "Раунд заблокирован"
                  : pairingLifecycle?.statusLabel || "Состояние недоступно"}
              </Status>
            </div>
            <dl className={styles.meta}>
              <div>
                <dt>Следующий раунд</dt>
                <dd>{roundNumber}</dd>
              </div>
              <div>
                <dt>Присутствуют</dt>
                <dd>{eligibleParticipants.length}</dd>
              </div>
            </dl>
          </div>

          {editingLocked && (
            <Message tone="warning" title="Раунд доступен только для просмотра">
              Раунд уже заблокирован, начат или использован.
            </Message>
          )}

          {!pairingStateAllowed && pairingLifecycle && (
            <Message tone="warning" title={pairingLifecycle.title}>
              {pairingLifecycle.detail}
            </Message>
          )}

          <section className={styles.editor} aria-labelledby="swiss-pairing-editor-title">
            <div className={styles.sectionHeading}>
              <div>
                <h4 id="swiss-pairing-editor-title" className={styles.sectionTitle}>
                  Настройки раунда {roundNumber}
                </h4>
                <p className={styles.sectionDescription}>
                  Выберите, как определяется категория каждого матча.
                </p>
              </div>
              <Status tone="info" size="small">
                Текущая политика: {CATEGORY_MODE_LABELS[categoryMode]}
              </Status>
            </div>

            <fieldset className={styles.modeFieldset} disabled={pairingFormDisabled}>
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
                  <small>Пары и bye подбираются автоматически.</small>
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
                    updateCategoryMode(event.target.value as CategoryMode);
                  }}
                  disabled={pairingFormDisabled}
                >
                  {CATEGORY_MODES.map((mode) => (
                    <option key={mode} value={mode}>
                      {CATEGORY_MODE_LABELS[mode]}
                    </option>
                  ))}
                </select>
              </div>
              <div className={styles.categoryField}>
                {categoryMode === "admin" ? (
                  <>
                    <label className={styles.fieldLabel} htmlFor="swiss-admin-category">
                      Категория раунда
                    </label>
                    <select
                      id="swiss-admin-category"
                      value={categories[0] ?? ""}
                      onChange={(event) => updateAdminCategory(event.target.value as Category)}
                      disabled={pairingFormDisabled}
                    >
                      {swissPoolCategories.map((category) => (
                        <option key={category} value={category}>
                          {CATEGORY_LABELS[category]}
                        </option>
                      ))}
                    </select>
                    <span className={styles.fieldHint}>
                      Эта категория будет использована во всех матчах раунда.
                    </span>
                  </>
                ) : (
                  <>
                    <span className={styles.fieldLabel}>Категории раунда</span>
                    <div className={styles.categoryReadonly}>
                      <span className={styles.fieldHint}>{categoryModeDescription(categoryMode)}</span>
                      {swissPoolCategories.length > 0 ? (
                        <ul className={styles.categoryList} aria-label="Категории раунда">
                          {swissPoolCategories.map((category) => (
                            <li key={category}>{CATEGORY_LABELS[category]}</li>
                          ))}
                        </ul>
                      ) : (
                        <span className={styles.fieldHint}>
                          Доступных категорий пока нет.
                        </span>
                      )}
                    </div>
                  </>
                )}
              </div>
            </div>

            {pairingMode === "automatic" ? (
              <Message tone="info" title="Автоматический план">
                Пары появятся здесь после отправки запроса.
              </Message>
            ) : (
              <section className={styles.manualEditor} aria-labelledby="manual-pairing-title">
                <div>
                  <h5 id="manual-pairing-title" className={styles.sectionTitle}>
                    Ручная сетка
                  </h5>
                  <p className={styles.sectionDescription}>
                    Доступны только участники со статусом &quot;На месте&quot;. Повторные встречи проверяются при сохранении.
                  </p>
                </div>
                {eligibleParticipants.length === 0 ? (
                  <Message tone="empty" title="Нет присутствующих участников">
                    Отметьте участников как &quot;На месте&quot; в редакторе состава.
                  </Message>
                ) : (
                  <>
                    <div className={styles.pairList} aria-label="Ручные пары">
                      {draftPairings.map((pairing, index) => (
                        <fieldset className={styles.pairRow} key={`pair-${index + 1}`} disabled={pairingFormDisabled}>
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
                            reportDirty(true);
                          }}
                          disabled={pairingFormDisabled}
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
              </Message>
            )}
            {notice && (
              <Message tone="success" title="Раунд готов">
                {notice}
                {onNavigateToConduct ? (
                  <Button
                    className={styles.nextStepButton}
                    type="button"
                    size="small"
                    onClick={onNavigateToConduct}
                  >
                    Перейти к проведению
                  </Button>
                ) : null}
              </Message>
            )}

            <div className={styles.actions}>
              <Button
                type="button"
                onClick={() => void handleSubmit()}
                loading={submitting}
                loadingLabel="Формируем пары"
                disabled={pairingFormDisabled || eligibleParticipants.length < 2}
              >
                Сформировать пары
              </Button>
            </div>
          </section>

          {savedRound && (
            <section className={styles.result} aria-labelledby="swiss-result-title">
              <div className={styles.sectionHeading}>
                <div>
                  <h4 id="swiss-result-title" className={styles.sectionTitle}>
                    План раунда {savedRound.round_number}
                  </h4>
                  <p className={styles.sectionDescription}>
                    Показаны пары, bye и таблица раунда.
                  </p>
                </div>
                <Status tone={savedRound.locked ? "disabled" : "success"}>
                  {savedRound.locked ? "Заблокирован" : "Сохранен"}
                </Status>
              </div>
              <dl className={styles.resultMeta}>
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
                <h5 className={styles.subheading}>Таблица раунда</h5>
                <div className={styles.tableWrap}>
                  <table>
                    <caption className={styles.srOnly}>Таблица раунда {savedRound.round_number}</caption>
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
