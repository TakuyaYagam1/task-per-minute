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
import { formatTournamentState } from "../../shared/lib";
import {
  Button,
  Message,
  Panel,
  Status,
  Table,
  type StatusTone,
  type TableColumn,
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
  const [content, setContent] = useState<TournamentContentSelection | null>(null);
  const [contentState, setContentState] = useState<LoadState>("loading");
  const [contentError, setContentError] = useState<string | null>(null);
  const [contentEmpty, setContentEmpty] = useState(false);
  const [name, setName] = useState("");
  const [plannedRosterSize, setPlannedRosterSize] = useState(
    String(DEFAULT_ROSTER_SIZE),
  );
  const [formError, setFormError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const creatingRef = useRef(false);
  const mountedRef = useRef(false);
  const createRunRef = useRef(0);
  const navigationContextRef = useRef({ activeView, selectedTournamentId });
  const tournamentsControllerRef = useRef<AbortController | null>(null);
  const contentControllerRef = useRef<AbortController | null>(null);
  const requestGenerationRef = useRef(0);
  const dirtySourcesRef = useRef<Record<string, boolean>>({});
  const previousViewRef = useRef(activeView);
  const previousTournamentIdRef = useRef(selectedTournamentId);

  const selectedTournament = useMemo(
    () =>
      tournaments.find((tournament) => tournament.id === selectedTournamentId) ??
      null,
    [selectedTournamentId, tournaments],
  );

  const createFormDirty =
    Boolean(name.trim()) || plannedRosterSize !== String(DEFAULT_ROSTER_SIZE);

  const publishDirty = useCallback((): void => {
    const editorDirty = Object.values(dirtySourcesRef.current).some(Boolean);
    onDirtyChange?.(createFormDirty || editorDirty);
  }, [createFormDirty, onDirtyChange]);

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

  const loadTournaments = useCallback(async (): Promise<void> => {
    tournamentsControllerRef.current?.abort();
    const controller = new AbortController();
    tournamentsControllerRef.current = controller;
    setTournamentsState("loading");
    setTournamentsError(null);
    try {
      const items = await readAllTournaments(controller.signal);
      if (!controller.signal.aborted) {
        setTournaments(items);
        setTournamentsState("ready");
      }
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      setTournamentsState("error");
      setTournamentsError(problemMessage(error, "Не удалось загрузить список турниров"));
    } finally {
      if (tournamentsControllerRef.current === controller) {
        tournamentsControllerRef.current = null;
      }
    }
  }, [onSessionExpired]);

  const loadContent = useCallback(async (): Promise<void> => {
    contentControllerRef.current?.abort();
    const controller = new AbortController();
    const requestGeneration = requestGenerationRef.current + 1;
    requestGenerationRef.current = requestGeneration;
    contentControllerRef.current = controller;
    setContentState("loading");
    setContentError(null);
    setContentEmpty(false);
    try {
      const selection = await getTournamentContent(controller.signal);
      if (
        controller.signal.aborted ||
        requestGenerationRef.current !== requestGeneration
      ) {
        return;
      }
      setContent(selection);
      setContentState("ready");
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
      const unavailable = error instanceof ApiError && error.status === 422;
      setContent(null);
      setContentEmpty(unavailable);
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

  const handleCreate = async (): Promise<void> => {
    if (creatingRef.current) {
      return;
    }
    const trimmedName = name.trim();
    if (!trimmedName) {
      setFormError("Введите название турнира");
      return;
    }
    if (Array.from(trimmedName).length > MAX_TOURNAMENT_NAME_LENGTH) {
      setFormError("Название турнира не должно быть длиннее 120 символов");
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
            "Список турниров изменился. Обновите данные и повторите создание.",
        );
      } else if (error instanceof ApiError && error.status === 422) {
        setFormError(
          error.problem?.detail ||
            "Выбранная публикация больше недоступна. Обновите данные и повторите.",
        );
        void loadContent();
      } else {
        setFormError(problemMessage(error, "Не удалось создать турнир"));
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

  const columns = useMemo<readonly TableColumn<Tournament>[]>(
    () => [
      {
        key: "name",
        header: "Турнир",
        cell: (tournament) => (
          <div className={styles.tournamentNameCell}>
            <strong>{tournament.name}</strong>
          </div>
        ),
      },
      {
        key: "state",
        header: "Состояние",
        cell: (tournament) => (
          <Status tone={STATE_TONES[tournament.state]}>
            {formatTournamentState(tournament.state)}
          </Status>
        ),
      },
      {
        key: "roster_size",
        header: "Состав",
        align: "center",
        cell: (tournament) => (
          <span>
            {tournament.roster_size} / {tournament.planned_roster_size}
          </span>
        ),
      },
      {
        key: "created_at",
        header: "Создан",
        cell: (tournament) => formatDateTime(tournament.created_at),
      },
      {
        key: "open",
        header: "Действие",
        cell: (tournament) => (
          <Button
            variant="secondary"
            size="small"
            onClick={() => onNavigate(tournament.id, "overview")}
          >
            Открыть
          </Button>
        ),
      },
    ],
    [onNavigate],
  );

  const renderCreateForm = (): ReactNode => (
    <Panel
      title="Новый турнир"
      description="Создайте турнир на выбранной публикации контента."
      className={styles.panel}
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
          <label htmlFor="tournament-name">Название турнира</label>
          <input
            id="tournament-name"
            name="name"
            type="text"
            value={name}
            required
            maxLength={MAX_TOURNAMENT_NAME_LENGTH}
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
        <div className={styles.revisionBlock}>
          <div className={styles.revisionHeading}>
            <span>Текущая публикация</span>
            {contentState === "ready" && content ? (
              <Status tone="success">Задачи опубликованы</Status>
            ) : null}
          </div>
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
                onClick={() => void loadContent()}
              >
                Обновить данные
              </button>
            </Message>
          ) : null}
          {contentState === "ready" && content ? (
            <p className={styles.revisionDescription}>
              Опубликовано {formatDateTime(content.published_at)}.
            </p>
          ) : null}
        </div>
        {formError ? (
          <Message id="tournament-form-error" tone="error" title="Не удалось создать турнир">
            {formError}
          </Message>
        ) : null}
        <Button
          type="submit"
          size="large"
          loading={creating}
          loadingLabel="Создаем турнир"
          disabled={contentState !== "ready" || !content}
          className={styles.submitButton}
        >
          Создать турнир
        </Button>
      </form>
    </Panel>
  );

  const renderTournamentList = (): ReactNode => (
    <Panel
      title="Турниры"
      description="Выберите турнир, чтобы открыть его рабочую область."
      className={`${styles.panel} ${styles.listPanel}`}
    >
      <div className={styles.listToolbar}>
        <span className={styles.listCount}>
          {tournamentsState === "ready"
            ? `${tournaments.length} ${tournaments.length === 1 ? "турнир" : "турниров"}`
            : ""}
        </span>
        <Button
          variant="secondary"
          size="small"
          onClick={() => void loadTournaments()}
          loading={tournamentsState === "loading"}
          loadingLabel="Обновляем"
        >
          Обновить список
        </Button>
      </div>
      <Table
        columns={columns}
        rows={tournaments}
        rowKey="id"
        ariaLabel="Список турниров"
        caption="Турниры"
        loading={tournamentsState === "loading"}
        loadingMessage="Загружаем турниры"
        error={
          tournamentsState === "error"
            ? tournamentsError || "Неизвестная ошибка списка"
            : undefined
        }
        empty={
          tournamentsState === "ready" && tournaments.length === 0
            ? "Турниров пока нет"
            : undefined
        }
        wrapperClassName={styles.tableRegion}
      />
    </Panel>
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
              Назад к турнирам
            </button>
            <p className={styles.breadcrumb}>Турниры / рабочая область</p>
            <h2 id="tournament-detail-title">Турнир недоступен</h2>
          </div>
          {tournamentsState === "loading" ? (
            <Message tone="loading" title="Загружаем турнир">
              Проверяем данные выбранного турнира.
            </Message>
          ) : (
            <Message tone="error" title="Турнир не найден">
              {tournamentsError || "Список не содержит выбранный турнир."}
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
            Назад к турнирам
          </button>
          <p className={styles.breadcrumb}>Турниры / рабочая область</p>
          <h2 id="tournament-detail-title">{selectedTournament.name}</h2>
          <p className={styles.detailMeta}>
            {formatTournamentState(selectedTournament.state)}
          </p>
        </div>
        <nav className={styles.viewNav} aria-label="Разделы турнира">
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
                onReloadTournaments={loadTournaments}
                onSessionExpired={onSessionExpired}
                onTournamentUpdated={handleTournamentUpdated}
                tournament={selectedTournament}
              />
              <Panel
                title="Обзор турнира"
                description="Основные сведения о выбранном турнире и его текущем состоянии."
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
                    <dd>{selectedTournament.preset}</dd>
                  </div>
                  <div className={styles.overviewItem}>
                    <dt>Создан</dt>
                    <dd>{formatDateTime(selectedTournament.created_at)}</dd>
                  </div>
                  <div className={styles.overviewItem}>
                    <dt>Начат</dt>
                    <dd>{formatDateTime(selectedTournament.started_at)}</dd>
                  </div>
                  <div className={styles.overviewItem}>
                    <dt>Завершен</dt>
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
              onReloadTournaments={loadTournaments}
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
                onReloadTournaments={loadTournaments}
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
    <section className={styles.root} aria-label="Управление турнирами">
      <div className={styles.heading}>
        <p className={styles.eyebrow}>Турниры</p>
        <h2>
          {selectedTournament ? "Рабочая область турнира" : "Турниры"}
        </h2>
      </div>
      {selectedTournamentId ? (
        renderDetail()
      ) : (
        <div className={styles.layout}>
          {renderCreateForm()}
          {renderTournamentList()}
        </div>
      )}
    </section>
  );
};

TournamentAdminPanel.displayName = "TournamentAdminPanel";
