"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import {
  ApiError,
  createOperatorCommandIntent,
  getTournamentContent,
  operatorApi,
  type Tournament,
  type TournamentContentSelection,
} from "../../shared/api";
import { useAdminLiveRefresh } from "../../features/admin-live";
import { catalogFormatLabel } from "../../entities/tournament";
import { formatTournamentState } from "../../shared/lib";
import {
  Button,
  Dialog,
  Message,
  Panel,
  Status,
  type StatusTone,
} from "../../shared/ui";

import { GoldenPlayoffControlPanel } from "./GoldenPlayoffControlPanel";
import { RosterEditor } from "./RosterEditor";
import { SeriesConfigurationEditor } from "./SeriesConfigurationEditor";
import { SwissPairingEditor } from "./SwissPairingEditor";
import { TournamentAuditPanel } from "./TournamentAuditPanel";
import type { AdminRequestRunner } from "./TournamentContentManager";
import { TournamentStartControls } from "./TournamentStartControls";
import { WaveControlPanel } from "./WaveControlPanel";
import styles from "./TournamentAdminPanel.module.css";

export type TournamentAdminView =
  | "overview"
  | "participants"
  | "bracket"
  | "conduct"
  | "audit";

type TournamentAdminPanelProps = Readonly<{
  selectedTournamentId: string | null;
  activeView: TournamentAdminView;
  onNavigate: (tournamentId: string | null, view: TournamentAdminView) => void;
  onDirtyChange?: (dirty: boolean) => void;
  onSessionExpired?: () => void;
  runAdminRequest?: AdminRequestRunner;
}>;

type LoadState = "loading" | "ready" | "error";
type LoadOptions = Readonly<{ silent?: boolean }>;

const PAGE_SIZE = 200;
const MAX_TOURNAMENT_NAME_LENGTH = 120;
const MAX_PUBLIC_ID_LENGTH = 64;
const DEFAULT_ROSTER_SIZE = 4;

const STATE_TONES: Readonly<Record<Tournament["state"], StatusTone>> = {
  draft: "neutral",
  registration: "info",
  roster_locked: "info",
  swiss: "live",
  golden: "live",
  playoffs: "live",
  technical_pause: "warning",
  completed: "success",
  cancelled: "error",
};

const TOURNAMENT_STATES = [
  "draft",
  "registration",
  "roster_locked",
  "swiss",
  "golden",
  "playoffs",
  "technical_pause",
  "completed",
  "cancelled",
] as const satisfies readonly Tournament["state"][];

type TournamentStateFilter = Tournament["state"] | "";

const CYRILLIC_TO_LATIN: Readonly<Record<string, string>> = {
  а: "a",
  б: "b",
  в: "v",
  г: "g",
  д: "d",
  е: "e",
  ё: "e",
  ж: "zh",
  з: "z",
  и: "i",
  й: "j",
  к: "k",
  л: "l",
  м: "m",
  н: "n",
  о: "o",
  п: "p",
  р: "r",
  с: "s",
  т: "t",
  у: "u",
  ф: "f",
  х: "h",
  ц: "c",
  ч: "ch",
  ш: "sh",
  щ: "sch",
  ъ: "",
  ы: "y",
  ь: "",
  э: "e",
  ю: "yu",
  я: "ya",
};

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const formatDateTime = (value: string | null | undefined): string => {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "Дата недоступна";
  }
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(date);
};

const problemMessage = (error: unknown, fallback: string): string => {
  if (error instanceof ApiError) {
    return error.problem?.detail || error.problem?.title || fallback;
  }
  return fallback;
};

const publicIdSlugFromName = (name: string): string => {
  const transliterated = Array.from(name.toLocaleLowerCase("ru-RU"), (character) =>
    CYRILLIC_TO_LATIN[character] ?? character,
  ).join("");
  return transliterated
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, MAX_PUBLIC_ID_LENGTH - 7) || "demo-tournament";
};

const publicIdFromName = (name: string): string => {
  const slug = publicIdSlugFromName(name);
  const suffix = Math.random().toString(36).slice(2, 8);
  return `${slug || "demo-tournament"}-${suffix}`.slice(0, MAX_PUBLIC_ID_LENGTH);
};

const readAllTournaments = async (signal: AbortSignal): Promise<Tournament[]> => {
  const tournaments: Tournament[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  while (true) {
    const page = await operatorApi.listTournaments(
      cursor ? { cursor, page_size: PAGE_SIZE } : { page_size: PAGE_SIZE },
      signal,
    );
    tournaments.push(...page.items);
    if (!page.next_cursor || seenCursors.has(page.next_cursor)) {
      return tournaments;
    }
    seenCursors.add(page.next_cursor);
    cursor = page.next_cursor;
  }
};

const viewLabels: Readonly<Record<TournamentAdminView, string>> = {
  overview: "Обзор",
  participants: "Участники",
  bracket: "Сетка и серии",
  conduct: "Проведение",
  audit: "Журнал",
};

export const TournamentAdminPanel = ({
  activeView,
  onDirtyChange,
  onNavigate,
  onSessionExpired,
  selectedTournamentId,
}: TournamentAdminPanelProps) => {
  const [tournaments, setTournaments] = useState<Tournament[]>([]);
  const [tournamentsState, setTournamentsState] = useState<LoadState>("loading");
  const [tournamentsError, setTournamentsError] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [stateFilter, setStateFilter] = useState<TournamentStateFilter>("");
  const [content, setContent] = useState<TournamentContentSelection | null>(null);
  const [contentState, setContentState] = useState<LoadState>("loading");
  const [contentError, setContentError] = useState<string | null>(null);
  const [contentEmpty, setContentEmpty] = useState(false);
  const [name, setName] = useState("");
  const [plannedRosterSize, setPlannedRosterSize] = useState(
    String(DEFAULT_ROSTER_SIZE),
  );
  const [formError, setFormError] = useState<string | null>(null);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const creatingRef = useRef(false);
  const createNameInputRef = useRef<HTMLInputElement | null>(null);
  const createTriggerRef = useRef<HTMLButtonElement | null>(null);
  const mountedRef = useRef(false);
  const createRunRef = useRef(0);
  const navigationContextRef = useRef({ activeView, selectedTournamentId });
  const tournamentsControllerRef = useRef<AbortController | null>(null);
  const contentControllerRef = useRef<AbortController | null>(null);
  const tournamentsStateRef = useRef(tournamentsState);
  const contentStateRef = useRef(contentState);
  const requestGenerationRef = useRef(0);
  const dirtySourcesRef = useRef<Record<string, boolean>>({});
  const previousViewRef = useRef(activeView);
  const previousTournamentIdRef = useRef(selectedTournamentId);
  tournamentsStateRef.current = tournamentsState;
  contentStateRef.current = contentState;

  const selectedTournament = useMemo(
    () =>
      tournaments.find((tournament) => tournament.id === selectedTournamentId) ??
      null,
    [selectedTournamentId, tournaments],
  );

  const filteredTournaments = useMemo(() => {
    const normalizedQuery = searchQuery.trim().toLocaleLowerCase("ru-RU");
    return tournaments.filter((tournament) => {
      const matchesName =
        normalizedQuery.length === 0 ||
        tournament.name.toLocaleLowerCase("ru-RU").includes(normalizedQuery);
      const matchesState = stateFilter.length === 0 || tournament.state === stateFilter;
      return matchesName && matchesState;
    });
  }, [searchQuery, stateFilter, tournaments]);

  const hasTournamentFilters = searchQuery.trim().length > 0 || stateFilter.length > 0;

  const resetFilters = useCallback((): void => {
    setSearchQuery("");
    setStateFilter("");
  }, []);

  const createFormDirty =
    Boolean(name.trim()) || plannedRosterSize !== String(DEFAULT_ROSTER_SIZE);

  const publishDirty = useCallback((): void => {
    const editorDirty = Object.values(dirtySourcesRef.current).some(Boolean);
    onDirtyChange?.((createDialogOpen && createFormDirty) || editorDirty);
  }, [createDialogOpen, createFormDirty, onDirtyChange]);

  const reportChildDirty = useCallback(
    (source: string, dirty: boolean): void => {
      dirtySourcesRef.current[source] = dirty;
      publishDirty();
    },
    [publishDirty],
  );

  useEffect(() => {
    publishDirty();
  }, [publishDirty]);

  useEffect(() => {
    if (previousViewRef.current !== activeView) {
      previousViewRef.current = activeView;
      dirtySourcesRef.current = {};
      publishDirty();
    }
  }, [activeView, publishDirty]);

  useEffect(() => {
    if (previousTournamentIdRef.current !== selectedTournamentId) {
      previousTournamentIdRef.current = selectedTournamentId;
      dirtySourcesRef.current = {};
      publishDirty();
    }
  }, [publishDirty, selectedTournamentId]);

  useEffect(() => {
    const previous = navigationContextRef.current;
    if (
      previous.activeView !== activeView ||
      previous.selectedTournamentId !== selectedTournamentId
    ) {
      navigationContextRef.current = { activeView, selectedTournamentId };
      createRunRef.current += 1;
    }
  }, [activeView, selectedTournamentId]);

  const reportRosterDirty = useCallback(
    (dirty: boolean): void => {
      reportChildDirty("roster", dirty);
    },
    [reportChildDirty],
  );

  const reportSeriesDirty = useCallback(
    (dirty: boolean): void => {
      reportChildDirty("series", dirty);
    },
    [reportChildDirty],
  );

  const reportSwissDirty = useCallback(
    (dirty: boolean): void => {
      reportChildDirty("swiss", dirty);
    },
    [reportChildDirty],
  );

  const loadTournaments = useCallback(async (options: LoadOptions = {}): Promise<void> => {
    const previousTournamentsState = tournamentsStateRef.current;
    tournamentsControllerRef.current?.abort();
    const controller = new AbortController();
    tournamentsControllerRef.current = controller;
    if (!options.silent) {
      tournamentsStateRef.current = "loading";
      setTournamentsState("loading");
      setTournamentsError(null);
    }
    try {
      const items = await readAllTournaments(controller.signal);
      if (!controller.signal.aborted) {
        setTournaments(items);
        tournamentsStateRef.current = "ready";
        setTournamentsState("ready");
        setTournamentsError(null);
      }
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (options.silent) {
        if (previousTournamentsState === "loading") {
          tournamentsStateRef.current = "error";
          setTournamentsState("error");
          setTournamentsError(problemMessage(error, "Не удалось загрузить список соревнований"));
        }
        return;
      }
      tournamentsStateRef.current = "error";
      setTournamentsState("error");
      setTournamentsError(problemMessage(error, "Не удалось загрузить список соревнований"));
    } finally {
      if (tournamentsControllerRef.current === controller) {
        tournamentsControllerRef.current = null;
      }
    }
  }, [onSessionExpired]);

  const loadContent = useCallback(async (options: LoadOptions = {}): Promise<void> => {
    const previousContentState = contentStateRef.current;
    contentControllerRef.current?.abort();
    const controller = new AbortController();
    const requestGeneration = requestGenerationRef.current + 1;
    requestGenerationRef.current = requestGeneration;
    contentControllerRef.current = controller;
    if (!options.silent) {
      contentStateRef.current = "loading";
      setContentState("loading");
      setContentError(null);
      setContentEmpty(false);
    }
    try {
      const selection = await getTournamentContent(controller.signal);
      if (
        controller.signal.aborted ||
        requestGenerationRef.current !== requestGeneration
      ) {
        return;
      }
      setContent(selection);
      contentStateRef.current = "ready";
      setContentState("ready");
      setContentError(null);
      setContentEmpty(false);
    } catch (error) {
      if (
        controller.signal.aborted ||
        requestGenerationRef.current !== requestGeneration ||
        isAbortError(error)
      ) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (options.silent) {
        if (previousContentState === "loading") {
          const unavailable = error instanceof ApiError && error.status === 422;
          contentStateRef.current = "error";
          setContentEmpty(unavailable);
          setContentState("error");
          setContentError(
            unavailable
              ? "Опубликованных задач пока нет. Сначала опубликуйте задачи."
              : problemMessage(error, "Не удалось получить текущую публикацию контента"),
          );
        }
        return;
      }
      const unavailable = error instanceof ApiError && error.status === 422;
      setContent(null);
      setContentEmpty(unavailable);
      contentStateRef.current = "error";
      setContentState("error");
      setContentError(
        unavailable
          ? "Опубликованных задач пока нет. Сначала опубликуйте задачи."
          : problemMessage(error, "Не удалось получить текущую публикацию контента"),
      );
    } finally {
      if (contentControllerRef.current === controller) {
        contentControllerRef.current = null;
      }
    }
  }, [onSessionExpired]);

  const refreshLiveAdminData = useCallback(async (): Promise<void> => {
    await Promise.all([
      loadTournaments({ silent: true }),
      loadContent({ silent: true }),
    ]);
  }, [loadContent, loadTournaments]);

  useAdminLiveRefresh(["tournaments", "tasks"] as const, refreshLiveAdminData);

  const reloadTournamentsSilently = useCallback(async (): Promise<void> => {
    await loadTournaments({ silent: true });
  }, [loadTournaments]);

  useEffect(() => {
    mountedRef.current = true;
    void Promise.all([loadTournaments(), loadContent()]);
    return () => {
      mountedRef.current = false;
      createRunRef.current += 1;
      tournamentsControllerRef.current?.abort();
      contentControllerRef.current?.abort();
      requestGenerationRef.current += 1;
    };
  }, [loadContent, loadTournaments]);


  const handleTournamentUpdated = useCallback((updatedTournament: Tournament): void => {
    setTournaments((current) =>
      current.map((currentTournament) =>
        currentTournament.id === updatedTournament.id
          ? updatedTournament
          : currentTournament,
      ),
    );
  }, []);

  const resetCreateForm = useCallback((): void => {
    setName("");
    setPlannedRosterSize(String(DEFAULT_ROSTER_SIZE));
    setFormError(null);
  }, []);

  const closeCreateDialog = useCallback((): void => {
    if (creatingRef.current) {
      return;
    }
    if (
      createFormDirty &&
      !window.confirm("Есть несохраненные изменения. Закрыть без сохранения?")
    ) {
      return;
    }
    setCreateDialogOpen(false);
    resetCreateForm();
  }, [createFormDirty, resetCreateForm]);

  const openCreateDialog = useCallback(
    (trigger: HTMLButtonElement): void => {
      if (creatingRef.current) {
        return;
      }
      resetCreateForm();
      createTriggerRef.current = trigger;
      setCreateDialogOpen(true);
    },
    [resetCreateForm],
  );

  useEffect(() => {
    if (selectedTournamentId && createDialogOpen && !creatingRef.current) {
      setCreateDialogOpen(false);
      resetCreateForm();
    }
  }, [createDialogOpen, resetCreateForm, selectedTournamentId]);

  const handleCreate = async (): Promise<void> => {
    if (creatingRef.current) {
      return;
    }
    const trimmedName = name.trim();
    if (!trimmedName) {
      setFormError("Введите название соревнования");
      return;
    }
    if (Array.from(trimmedName).length > MAX_TOURNAMENT_NAME_LENGTH) {
      setFormError("Название соревнования не должно быть длиннее 120 символов");
      return;
    }
    if (!content) {
      setFormError("Сначала дождитесь доступной публикации");
      return;
    }
    const rosterSize = Number(plannedRosterSize);
    if (![4, 8, 16].includes(rosterSize)) {
      setFormError("Выберите размер состава от 4 до 16 участников");
      return;
    }

    creatingRef.current = true;
    const createRunId = createRunRef.current + 1;
    createRunRef.current = createRunId;
    const canApply = (): boolean =>
      mountedRef.current && createRunRef.current === createRunId;
    setCreating(true);
    setFormError(null);
    try {
      const tournament = await operatorApi.createTournament(
        {
          expected_revision: 0,
          preset: "tournament_v1",
          name: trimmedName,
          public_id: publicIdFromName(trimmedName),
          planned_roster_size: rosterSize,
          content_revision: content.content_revision,
        },
        createOperatorCommandIntent(),
      );
      if (!canApply()) {
        return;
      }
      setTournaments((current) => [tournament, ...current]);
      setName("");
      setPlannedRosterSize(String(DEFAULT_ROSTER_SIZE));
      setCreateDialogOpen(false);
      dirtySourcesRef.current = {};
      onDirtyChange?.(false);
      window.setTimeout(() => {
        if (canApply()) {
          onNavigate(tournament.id, "overview");
        }
      }, 0);
    } catch (error) {
      if (!canApply()) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
        setFormError(
          error.problem?.detail ||
            "Список соревнований изменился. Обновите данные и повторите создание.",
        );
      } else if (error instanceof ApiError && error.status === 422) {
        setFormError(
          error.problem?.detail ||
            "Выбранная публикация больше недоступна. Обновите данные и повторите.",
        );
        void loadContent();
      } else {
        setFormError(problemMessage(error, "Не удалось создать соревнование"));
      }
    } finally {
      if (createRunRef.current === createRunId) {
        creatingRef.current = false;
        if (mountedRef.current) {
          setCreating(false);
        }
      }
    }
  };

  const renderCreateDialog = (): ReactNode => (
    <Dialog
      open={createDialogOpen}
      title="Создать соревнование"
      size="small"
      initialFocusRef={createNameInputRef}
      returnFocusRef={createTriggerRef}
      closeLabel="Закрыть форму создания соревнования"
      closeOnEscape={!creating}
      showCloseButton={!creating}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) {
          closeCreateDialog();
        }
      }}
    >
      <form
        className={styles.form}
        onSubmit={(event) => {
          event.preventDefault();
          void handleCreate();
        }}
        noValidate
      >
        <div className={styles.field}>
          <label htmlFor="tournament-name">Название соревнования</label>
          <input
            ref={createNameInputRef}
            id="tournament-name"
            name="name"
            type="text"
            value={name}
            required
            maxLength={MAX_TOURNAMENT_NAME_LENGTH}
            disabled={creating}
            onChange={(event) => {
              setName(event.target.value);
              setFormError(null);
            }}
            placeholder="Например, Осенний кубок"
            aria-invalid={Boolean(formError)}
            aria-describedby={formError ? "tournament-form-error" : undefined}
          />
        </div>
        <div className={styles.field}>
          <label htmlFor="tournament-roster-size">Плановый размер состава</label>
          <select
            id="tournament-roster-size"
            name="planned_roster_size"
            value={plannedRosterSize}
            disabled={creating}
            onChange={(event) => {
              setPlannedRosterSize(event.target.value);
              setFormError(null);
            }}
          >
            <option value="4">4 участника</option>
            <option value="8">8 участников</option>
            <option value="16">16 участников</option>
          </select>
        </div>
        {contentState === "loading" || contentState === "error" ? (
          <div className={styles.revisionBlock}>
            {contentState === "loading" ? (
              <Message tone="loading" title="Проверяем публикацию">
                Получаем опубликованные задачи.
              </Message>
            ) : null}
            {contentState === "error" ? (
              <Message
                tone={contentEmpty ? "empty" : "error"}
                title={contentEmpty ? "Публикации пока нет" : "Публикация недоступна"}
              >
                {contentError}
                <button
                  className={styles.inlineAction}
                  type="button"
                  disabled={creating}
                  onClick={() => void loadContent()}
                >
                  Повторить
                </button>
              </Message>
            ) : null}
          </div>
        ) : null}
        {formError ? (
          <Message id="tournament-form-error" tone="error" title="Не удалось создать соревнование">
            {formError}
          </Message>
        ) : null}
        <div className={styles.formActions}>
          <Button
            type="submit"
            loading={creating}
            loadingLabel="Создаем соревнование"
            disabled={contentState !== "ready" || !content}
          >
            Создать соревнование
          </Button>
          <Button
            type="button"
            variant="secondary"
            onClick={closeCreateDialog}
            disabled={creating}
          >
            Отменить
          </Button>
        </div>
      </form>
    </Dialog>
  );

  const renderTournamentList = (): ReactNode => (
    <>
      {renderCreateDialog()}
      <Panel
        as="article"
        aria-labelledby="tournament-list-title"
        header={
          <div className={styles.listHeader}>
            <div>
              <h2 id="tournament-list-title" className={styles.sectionTitle}>
                Список соревнований
              </h2>
              {tournamentsState === "ready" && (
                <p className={styles.listDescription} aria-live="polite">
                  Показано: {filteredTournaments.length} из {tournaments.length}
                </p>
              )}
            </div>
            <Button onClick={(event) => openCreateDialog(event.currentTarget)}>
              Создать соревнование
            </Button>
          </div>
        }
        className={styles.listPanel}
      >
        {tournamentsState === "ready" && tournaments.length > 0 ? (
          <div className={styles.listToolbar} aria-label="Фильтры соревнований">
            <div className={styles.filterField}>
              <label htmlFor="admin-tournament-filter-search">Поиск по названию</label>
              <input
                id="admin-tournament-filter-search"
                className={styles.filterInput}
                type="search"
                value={searchQuery}
                onChange={(event) => setSearchQuery(event.target.value)}
                placeholder="Название соревнования"
              />
            </div>
            <div className={styles.filterField}>
              <label htmlFor="admin-tournament-filter-state">Статус</label>
              <select
                id="admin-tournament-filter-state"
                className={styles.filterSelect}
                value={stateFilter}
                onChange={(event) =>
                  setStateFilter(event.target.value as TournamentStateFilter)
                }
              >
                <option value="">Все статусы</option>
                {TOURNAMENT_STATES.map((state) => (
                  <option key={state} value={state}>
                    {formatTournamentState(state)}
                  </option>
                ))}
              </select>
            </div>
            {hasTournamentFilters && (
              <div className={styles.filterActions}>
                <Button
                  type="button"
                  variant="secondary"
                  size="small"
                  onClick={resetFilters}
                >
                  Сбросить фильтры
                </Button>
              </div>
            )}
          </div>
        ) : null}
        {tournamentsState === "loading" && (
          <Message tone="loading" title="Загрузка соревнований">
            Загружаем актуальный список соревнований.
          </Message>
        )}
        {tournamentsState === "error" && (
          <Message tone="error" title="Не удалось загрузить соревнования">
            {tournamentsError}
            <Button
              variant="secondary"
              size="small"
              className={styles.inlineButton}
              onClick={() => void loadTournaments()}
            >
              Повторить
            </Button>
          </Message>
        )}
        {tournamentsState === "ready" && tournaments.length === 0 && (
          <Message tone="empty" title="Каталог пуст">
            Пока нет созданных соревнований
          </Message>
        )}
        {tournamentsState === "ready" && tournaments.length > 0 && (
          filteredTournaments.length === 0 ? (
            <Message tone="empty" title="Ничего не найдено">
              Измените условия поиска или сбросьте фильтры.
            </Message>
          ) : (
            <div className={styles.tournamentList} aria-label="Каталог соревнований">
              {filteredTournaments.map((tournament) => (
                <article key={tournament.id} className={styles.tournamentCard}>
                  <div className={styles.tournamentCardHeader}>
                    <div className={styles.tournamentCardTitle}>
                      <h3 className={styles.tournamentName}>{tournament.name}</h3>
                    </div>
                    <Status
                      className={styles.tournamentStatus}
                      tone={STATE_TONES[tournament.state]}
                      size="small"
                    >
                      {formatTournamentState(tournament.state)}
                    </Status>
                  </div>
                  <dl className={styles.tournamentMeta}>
                    <div className={styles.tournamentMetaItem}>
                      <dt>Состав</dt>
                      <dd>
                        {tournament.roster_size} / {tournament.planned_roster_size}
                      </dd>
                    </div>
                    <div className={styles.tournamentMetaItem}>
                      <dt>Создано</dt>
                      <dd>{formatDateTime(tournament.created_at)}</dd>
                    </div>
                  </dl>
                  <div className={styles.tournamentCardActions}>
                    <Button
                      variant="secondary"
                      size="small"
                      aria-label={`Открыть соревнование ${tournament.name}`}
                      onClick={() => onNavigate(tournament.id, "overview")}
                    >
                      Открыть
                    </Button>
                  </div>
                </article>
              ))}
            </div>
          )
        )}
      </Panel>
    </>
  );

  const renderDetail = (): ReactNode => {
    if (!selectedTournament) {
      return (
        <section className={styles.detail} aria-labelledby="tournament-detail-title">
          <div className={styles.detailHeader}>
            <button
              className={styles.backButton}
              type="button"
              onClick={() => onNavigate(null, "overview")}
            >
              К списку соревнований
            </button>
            <h2 id="tournament-detail-title">Соревнование недоступно</h2>
          </div>
          {tournamentsState === "loading" ? (
            <Message tone="loading" title="Загружаем соревнование">
              Проверяем данные выбранного соревнования.
            </Message>
          ) : (
            <Message tone="error" title="Соревнование не найдено">
              {tournamentsError || "Список не содержит выбранное соревнование."}
            </Message>
          )}
        </section>
      );
    }
    return (
      <section className={styles.detail} aria-labelledby="tournament-detail-title">
        <div className={styles.detailHeader}>
          <button
            className={styles.backButton}
            type="button"
            onClick={() => {
              onNavigate(null, "overview");
            }}
          >
            К списку соревнований
          </button>
          <h2 id="tournament-detail-title">{selectedTournament.name}</h2>
        </div>
        <nav className={styles.viewNav} aria-label="Разделы соревнования">
          {(Object.keys(viewLabels) as TournamentAdminView[]).map((view) => (
            <button
              key={view}
              className={`${styles.viewTab} ${activeView === view ? styles.viewTabActive : ""}`}
              type="button"
              aria-current={activeView === view ? "page" : undefined}
              onClick={() => onNavigate(selectedTournament.id, view)}
            >
              {viewLabels[view]}
            </button>
          ))}
        </nav>
        <div className={styles.viewContent}>
          {activeView === "overview" ? (
            <div className={styles.editorStack}>
              <TournamentStartControls
                onNavigate={(view) => onNavigate(selectedTournament.id, view)}
                onReloadTournaments={reloadTournamentsSilently}
                onSessionExpired={onSessionExpired}
                onTournamentUpdated={handleTournamentUpdated}
                tournament={selectedTournament}
              />
              <Panel
                title="Обзор соревнования"
                description="Основные сведения о выбранном соревновании и его текущем состоянии."
              >
                <dl className={styles.overviewGrid}>
                  <div className={styles.overviewItem}>
                    <dt>Состав</dt>
                    <dd>
                      {selectedTournament.roster_size} / {selectedTournament.planned_roster_size}
                    </dd>
                  </div>
                  <div className={styles.overviewItem}>
                    <dt>Формат</dt>
                    <dd>{catalogFormatLabel(selectedTournament.preset)}</dd>
                  </div>
                  <div className={styles.overviewItem}>
                    <dt>Создание</dt>
                    <dd>{formatDateTime(selectedTournament.created_at)}</dd>
                  </div>
                  <div className={styles.overviewItem}>
                    <dt>Начало</dt>
                    <dd>{formatDateTime(selectedTournament.started_at)}</dd>
                  </div>
                  <div className={styles.overviewItem}>
                    <dt>Завершение</dt>
                    <dd>{formatDateTime(selectedTournament.finished_at)}</dd>
                  </div>
                </dl>
              </Panel>
            </div>
          ) : null}
          {activeView === "participants" ? (
            <RosterEditor
              tournaments={tournaments}
              selectedTournament={selectedTournament}
              selectedTournamentId={selectedTournament.id}
              onSelectTournament={(id) => onNavigate(id || null, "participants")}
              onNavigateToOverview={() => onNavigate(selectedTournament.id, "overview")}
              onReloadTournaments={reloadTournamentsSilently}
              onSessionExpired={onSessionExpired}
              onDirtyChange={reportRosterDirty}
              showTournamentChooser={false}
            />
          ) : null}
          {activeView === "bracket" ? (
            <div className={styles.editorStack}>
              <SeriesConfigurationEditor
                tournaments={tournaments}
                selectedTournament={selectedTournament}
                selectedTournamentId={selectedTournament.id}
                onSelectTournament={(id) => onNavigate(id || null, "bracket")}
                onSessionExpired={onSessionExpired}
                onDirtyChange={reportSeriesDirty}
                showTournamentChooser={false}
              />
              <SwissPairingEditor
                tournaments={tournaments}
                selectedTournament={selectedTournament}
                selectedTournamentId={selectedTournament.id}
                onSelectTournament={(id) => onNavigate(id || null, "bracket")}
                onNavigateToConduct={() => onNavigate(selectedTournament.id, "conduct")}
                onReloadTournaments={reloadTournamentsSilently}
                onSessionExpired={onSessionExpired}
                onDirtyChange={reportSwissDirty}
                showTournamentChooser={false}
              />
            </div>
          ) : null}
          {activeView === "conduct" ? (
            <div className={styles.editorStack}>
              <WaveControlPanel
                tournamentId={selectedTournament.id}
                onSessionExpired={onSessionExpired}
              />
              <GoldenPlayoffControlPanel
                tournament={selectedTournament}
                onSessionExpired={onSessionExpired}
                onTournamentUpdated={handleTournamentUpdated}
              />
            </div>
          ) : null}
          {activeView === "audit" ? (
            <TournamentAuditPanel
              tournaments={tournaments}
              selectedTournamentId={selectedTournament.id}
              onSelectTournament={(id) => onNavigate(id || null, "audit")}
              onSessionExpired={onSessionExpired}
              showTournamentChooser={false}
            />
          ) : null}
        </div>
      </section>
    );
  };

  return (
    <section className={styles.root} aria-label="Управление соревнованиями">
      {selectedTournamentId ? (
        renderDetail()
      ) : (
        renderTournamentList()
      )}
    </section>
  );
};

TournamentAdminPanel.displayName = "TournamentAdminPanel";
