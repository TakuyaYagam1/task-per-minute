"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";

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

import { RosterEditor } from "./RosterEditor";
import { SwissPairingEditor } from "./SwissPairingEditor";
import {
  TournamentContentManager,
  type AdminRequestRunner,
} from "./TournamentContentManager";
import styles from "./TournamentAdminPanel.module.css";

type TournamentAdminPanelProps = Readonly<{
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

export const TournamentAdminPanel = ({
  onSessionExpired,
  runAdminRequest,
}: TournamentAdminPanelProps) => {
  const router = useRouter();
  const [tournaments, setTournaments] = useState<Tournament[]>([]);
  const [tournamentsState, setTournamentsState] = useState<LoadState>("loading");
  const [tournamentsError, setTournamentsError] = useState<string | null>(null);
  const [content, setContent] = useState<TournamentContentSelection | null>(null);
  const [contentState, setContentState] = useState<LoadState>("loading");
  const [contentError, setContentError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [plannedRosterSize, setPlannedRosterSize] = useState(
    String(DEFAULT_ROSTER_SIZE),
  );
  const [formError, setFormError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [selectedTournamentId, setSelectedTournamentId] = useState("");
  const creatingRef = useRef(false);
  const tournamentsControllerRef = useRef<AbortController | null>(null);
  const contentControllerRef = useRef<AbortController | null>(null);

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
      setTournamentsError(
        problemMessage(error, "Не удалось загрузить список турниров"),
      );
    } finally {
      if (tournamentsControllerRef.current === controller) {
        tournamentsControllerRef.current = null;
      }
    }
  }, [onSessionExpired]);

  const loadContent = useCallback(async (): Promise<void> => {
    contentControllerRef.current?.abort();
    const controller = new AbortController();
    contentControllerRef.current = controller;
    setContentState("loading");
    setContentError(null);
    try {
      const selection = await getTournamentContent(controller.signal);
      if (!controller.signal.aborted) {
        setContent(selection);
        setContentState("ready");
      }
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      setContentState("error");
      setContentError(
        error instanceof ApiError && error.status === 422
          ? "Нет доступной опубликованной ревизии контента"
          : problemMessage(error, "Не удалось получить доступную ревизию контента"),
      );
    } finally {
      if (contentControllerRef.current === controller) {
        contentControllerRef.current = null;
      }
    }
  }, [onSessionExpired]);

  useEffect(() => {
    void Promise.all([loadTournaments(), loadContent()]);
    return () => {
      tournamentsControllerRef.current?.abort();
      contentControllerRef.current?.abort();
    };
  }, [loadContent, loadTournaments]);

  const publicIdPreview = useMemo(() => publicIdSlugFromName(name), [name]);
  const selectedTournament = useMemo(
    () => tournaments.find((tournament) => tournament.id === selectedTournamentId) ?? null,
    [selectedTournamentId, tournaments],
  );

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
      setFormError("Сначала дождитесь доступной ревизии контента");
      return;
    }
    const rosterSize = Number(plannedRosterSize);
    if (![4, 8, 16].includes(rosterSize)) {
      setFormError("Выберите размер состава от 4 до 16 участников");
      return;
    }

    creatingRef.current = true;
    setCreating(true);
    setFormError(null);
    const intent = createOperatorCommandIntent();
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
        intent,
      );
      setTournaments((current) => [tournament, ...current]);
      router.push(`/arena/operator/${encodeURIComponent(tournament.id)}`);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
        setFormError(
          error.problem?.detail ||
            "Состояние турниров изменилось. Повторите создание с текущими данными.",
        );
      } else if (error instanceof ApiError && error.status === 422) {
        setFormError(
          error.problem?.detail ||
            "Выбранная ревизия контента больше недоступна. Обновите данные и повторите.",
        );
        void loadContent();
      } else {
        setFormError(problemMessage(error, "Не удалось создать турнир"));
      }
    } finally {
      creatingRef.current = false;
      setCreating(false);
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
            <span className={styles.publicId}>{tournament.public_id}</span>
            <span className={styles.tournamentId}>{tournament.id}</span>
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
        key: "preset",
        header: "Пресет",
        cell: () => <span>Демо (tournament_v1)</span>,
      },
      {
        key: "created_at",
        header: "Создан",
        cell: (tournament) => formatDateTime(tournament.created_at),
      },
      {
        key: "started_at",
        header: "Начат",
        cell: (tournament) => formatDateTime(tournament.started_at),
      },
      {
        key: "finished_at",
        header: "Завершен",
        cell: (tournament) => formatDateTime(tournament.finished_at),
      },
      {
        key: "open",
        header: "Действие",
        cell: (tournament) => (
          <div className={styles.rowActions}>
            <a
              className={styles.openLink}
              href={`/arena/operator/${encodeURIComponent(tournament.id)}`}
            >
              Открыть
            </a>
            <button
              className={styles.inlineAction}
              type="button"
              onClick={() => setSelectedTournamentId(tournament.id)}
            >
              Редактировать состав
            </button>
          </div>
        ),
      },
    ],
    [],
  );

  return (
    <div className={styles.root}>
      <TournamentContentManager
        content={content}
        contentState={contentState}
        contentError={contentError}
        onReloadContent={() => void loadContent()}
        onSessionExpired={onSessionExpired}
        runAdminRequest={runAdminRequest}
      />
      <div className={styles.layout}>
        <Panel
          title="Новый турнир"
          description="Создайте демо-турнир на актуальной публикации контента."
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
                <span>Доступный контент</span>
                {contentState === "ready" && content ? (
                  <Status tone="info">Ревизия {content.content_revision}</Status>
                ) : null}
              </div>
              {contentState === "loading" && (
                <Message tone="loading" title="Загружаем публикацию">
                  Проверяем актуальную ревизию контента.
                </Message>
              )}
              {contentState === "error" && (
                <Message tone="error" title="Контент недоступен">
                  {contentError}
                  <button
                    className={styles.inlineAction}
                    type="button"
                    onClick={() => void loadContent()}
                  >
                    Обновить данные
                  </button>
                </Message>
              )}
              {contentState === "ready" && content && (
                <p className={styles.revisionDescription}>
                  Опубликовано {formatDateTime(content.published_at)}. Будет
                  использовано без изменений при создании турнира.
                </p>
              )}
            </div>

            <p className={styles.publicIdHint}>
              Публичный идентификатор будет создан автоматически:
              <code>{publicIdPreview}</code>
            </p>

            {formError && (
              <Message
                id="tournament-form-error"
                tone="error"
                title="Не удалось создать турнир"
              >
                {formError}
              </Message>
            )}

            <Button
              type="submit"
              size="large"
              loading={creating}
              loadingLabel="Создаем турнир"
              disabled={contentState !== "ready" || !content}
              className={styles.submitButton}
            >
              Создать демо-турнир
            </Button>
          </form>
        </Panel>

        <Panel
          title="Турниры"
          description="Все состояния и актуальный размер состава из операторского API."
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
            caption="Операторские турниры"
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
      </div>

      <RosterEditor
        tournaments={tournaments}
        selectedTournament={selectedTournament}
        selectedTournamentId={selectedTournamentId}
        onSelectTournament={setSelectedTournamentId}
        onReloadTournaments={loadTournaments}
        onSessionExpired={onSessionExpired}
      />
      <SwissPairingEditor
        tournaments={tournaments}
        selectedTournament={selectedTournament}
        selectedTournamentId={selectedTournamentId}
        onSelectTournament={setSelectedTournamentId}
        onReloadTournaments={loadTournaments}
        onSessionExpired={onSessionExpired}
      />
    </div>
  );
};

TournamentAdminPanel.displayName = "TournamentAdminPanel";
