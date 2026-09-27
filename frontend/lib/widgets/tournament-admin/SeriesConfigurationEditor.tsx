"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  ApiError,
  createOperatorCommandIntent,
  getTournamentConfiguration,
  updateTournamentConfiguration,
  updateTournamentSeriesConfiguration,
  type Tournament,
  type TournamentConfiguration,
  type TournamentConfigurationCategoryPool,
  type TournamentConfigurationSeries,
  type UpdateTournamentConfigurationRequest,
  type UpdateTournamentSeriesConfigurationRequest,
} from "../../shared/api";
import { formatCategory, formatSeriesFormat } from "../../shared/lib";
import { Button, Message, Panel, Status } from "../../shared/ui";

import styles from "./SeriesConfigurationEditor.module.css";

type SeriesConfigurationEditorProps = Readonly<{
  tournaments: readonly Tournament[];
  selectedTournament: Tournament | null;
  selectedTournamentId: string;
  onSelectTournament: (id: string) => void;
  onSessionExpired?: () => void;
  onDirtyChange?: (dirty: boolean) => void;
  showTournamentChooser?: boolean;
}>;

type LoadState = "ready" | "loading" | "error";
type ReserveCount = 0 | 1 | 2;
type SeriesMode = TournamentConfigurationSeries["mode"];
type Category = TournamentConfigurationCategoryPool["categories"][number];
type SeriesDraft = Readonly<{
  mode: SeriesMode;
  category: Category;
}>;

const SERIES_MODES: readonly SeriesMode[] = ["random", "admin", "draft"];
const RESERVE_COUNTS: readonly ReserveCount[] = [0, 1, 2];

const SERIES_MODE_LABELS: Readonly<Record<SeriesMode, string>> = {
  random: "Случайный выбор",
  admin: "Выбор оператора",
  draft: "Полный драфт",
};

const STAGE_LABELS: Readonly<Record<TournamentConfigurationSeries["stage"], string>> = {
  swiss: "Квалификация",
  golden: "Дополнительный отбор",
  semifinal: "Полуфинал",
  final: "Финал",
};

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const problemMessage = (error: unknown, fallback: string): string => {
  if (error instanceof ApiError) {
    return error.problem?.detail || error.problem?.title || fallback;
  }
  return fallback;
};

const draftsFromConfiguration = (
  configuration: TournamentConfiguration,
): Record<string, SeriesDraft> => {
  const pools = new Map(
    configuration.category_pools.map((pool) => [pool.id, pool]),
  );
  return Object.fromEntries(
    configuration.series.flatMap((series) => {
      const pool = pools.get(series.category_pool_revision_id);
      const category = pool?.categories.find((item) =>
        series.categories.includes(item),
      ) ?? pool?.categories[0];
      if (!pool || !category) {
        return [];
      }
      return [[series.id, { mode: series.mode, category } satisfies SeriesDraft]];
    }),
  );
};

const stageLabel = (series: TournamentConfigurationSeries): string => {
  const label = STAGE_LABELS[series.stage];
  return series.stage === "swiss"
    ? `${label}, раунд ${series.round_number}`
    : label;
};

const lockedReason = (
  series: TournamentConfigurationSeries,
  isFinalSeries: boolean,
): string => {
  if (isFinalSeries) {
    return "Финальная серия BO3 доступна только для просмотра.";
  }
  if (series.locked) {
    return "Серия уже заблокирована.";
  }
  if (series.started) {
    return "Серия уже началась и больше не принимает изменения.";
  }
  if (series.consumed) {
    return "Резерв серии уже использован, изменения закрыты.";
  }
  if (series.disclosed) {
    return "Категории серии уже раскрыты, изменения закрыты.";
  }
  return "Серия больше не доступна для изменения.";
};

const staleMessage =
  "Состояние конфигурации устарело. Перезагрузите данные перед повторной отправкой.";

const cutoffMessage =
  "Изменение недоступно: серия уже заблокирована, начата, использована или раскрыта.";

const reserveCutoffMessage =
  "Резерв больше нельзя менять: соревнование уже началось или настройки уже используются.";

const reserveCountFromValue = (value: string): ReserveCount | null => {
  if (value === "0") {
    return 0;
  }
  if (value === "1") {
    return 1;
  }
  if (value === "2") {
    return 2;
  }
  return null;
};

const reserveCountFromNumber = (value: number): ReserveCount | null =>
  reserveCountFromValue(String(value));

export const SeriesConfigurationEditor = ({
  onSelectTournament,
  onSessionExpired,
  selectedTournament,
  selectedTournamentId,
  tournaments,
  onDirtyChange,
  showTournamentChooser = true,
}: SeriesConfigurationEditorProps) => {
  const tournamentId = selectedTournament?.id ?? "";
  const mountedRef = useRef(false);
  const loadControllerRef = useRef<AbortController | null>(null);
  const submitControllerRef = useRef<AbortController | null>(null);
  const loadRunRef = useRef(0);
  const submitRunRef = useRef(0);
  const submittingRef = useRef(false);
  const dirtySeriesIdsRef = useRef(new Set<string>());
  const dirtyReserveRef = useRef(false);
  const [configuration, setConfiguration] =
    useState<TournamentConfiguration | null>(null);
  const [drafts, setDrafts] = useState<Record<string, SeriesDraft>>({});
  const [loadState, setLoadState] = useState<LoadState>("ready");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [formErrors, setFormErrors] = useState<Record<string, string>>({});
  const [notices, setNotices] = useState<Record<string, string>>({});
  const [reserveCountDraft, setReserveCountDraft] = useState<ReserveCount | null>(null);
  const [reserveError, setReserveError] = useState<string | null>(null);
  const [reserveNotice, setReserveNotice] = useState<string | null>(null);
  const [reserveServerCutoff, setReserveServerCutoff] = useState(false);
  const [submittingConfiguration, setSubmittingConfiguration] = useState(false);
  const [submittingSeriesId, setSubmittingSeriesId] = useState<string | null>(
    null,
  );

  useEffect(() => {
    if (!selectedTournamentId) {
      onDirtyChange?.(false);
    }
  }, [onDirtyChange, selectedTournamentId]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      loadControllerRef.current?.abort();
      submitControllerRef.current?.abort();
    };
  }, []);

  const loadConfiguration = useCallback(
    async (
      id: string,
      options: Readonly<{
        preserveDirtyDrafts?: boolean;
        replaceReserveCount?: boolean;
        replaceSeriesId?: string;
      }> = {},
    ): Promise<TournamentConfiguration | null> => {
      loadControllerRef.current?.abort();
      const controller = new AbortController();
      const runId = loadRunRef.current + 1;
      loadRunRef.current = runId;
      loadControllerRef.current = controller;
      setLoadState("loading");
      setLoadError(null);
      setFormErrors({});
      setReserveNotice(null);

      try {
        const nextConfiguration = await getTournamentConfiguration(
          id,
          controller.signal,
        );
        if (
          controller.signal.aborted ||
          loadRunRef.current !== runId ||
          !mountedRef.current
        ) {
          return null;
        }

        const nextDrafts = draftsFromConfiguration(nextConfiguration);
        const nextReserveCount = reserveCountFromNumber(nextConfiguration.reserve_count);
        if (nextReserveCount === null) {
          setLoadState("error");
          setLoadError("Получено недопустимое количество резервов.");
          return null;
        }
        setConfiguration(nextConfiguration);
        setReserveCountDraft((current) => {
          if (!options.preserveDirtyDrafts || options.replaceReserveCount || current === null) {
            return nextReserveCount;
          }
          return current;
        });
        setReserveServerCutoff(false);
        setReserveError(null);
        setDrafts((current) => {
          if (!options.preserveDirtyDrafts) {
            dirtySeriesIdsRef.current.clear();
            dirtyReserveRef.current = false;
            return nextDrafts;
          }
          const merged = { ...nextDrafts };
          for (const seriesId of dirtySeriesIdsRef.current) {
            if (
              seriesId !== options.replaceSeriesId &&
              current[seriesId] !== undefined &&
              merged[seriesId] !== undefined
            ) {
              merged[seriesId] = current[seriesId];
            }
          }
          if (options.replaceSeriesId) {
            dirtySeriesIdsRef.current.delete(options.replaceSeriesId);
          }
          return merged;
        });
        setLoadState("ready");
        if (!options.preserveDirtyDrafts) {
          onDirtyChange?.(false);
        }
        return nextConfiguration;
      } catch (error) {
        if (
          controller.signal.aborted ||
          loadRunRef.current !== runId ||
          !mountedRef.current ||
          isAbortError(error)
        ) {
          return null;
        }
        if (error instanceof ApiError && error.status === 401) {
          onSessionExpired?.();
        }
        setLoadState("error");
        setLoadError(
          problemMessage(error, "Не удалось загрузить конфигурацию серий"),
        );
        return null;
      } finally {
        if (loadControllerRef.current === controller) {
          loadControllerRef.current = null;
        }
      }
    },
    [onDirtyChange, onSessionExpired],
  );

  useEffect(() => {
    if (!tournamentId) {
      loadControllerRef.current?.abort();
      submitControllerRef.current?.abort();
      loadRunRef.current += 1;
      submitRunRef.current += 1;
      dirtySeriesIdsRef.current.clear();
      dirtyReserveRef.current = false;
      setConfiguration(null);
      setDrafts({});
      setLoadState("ready");
      setLoadError(null);
      setFormErrors({});
      setNotices({});
      setReserveCountDraft(null);
      setReserveError(null);
      setReserveNotice(null);
      setReserveServerCutoff(false);
      setSubmittingConfiguration(false);
      setSubmittingSeriesId(null);
      submittingRef.current = false;
      return;
    }

    submitControllerRef.current?.abort();
    submitRunRef.current += 1;
    submittingRef.current = false;
    setSubmittingConfiguration(false);
    setSubmittingSeriesId(null);
    setConfiguration(null);
    setDrafts({});
    setLoadState("loading");
    setLoadError(null);
    setFormErrors({});
    setNotices({});
    setReserveCountDraft(null);
    setReserveError(null);
    setReserveNotice(null);
    setReserveServerCutoff(false);
    void loadConfiguration(tournamentId);

    return () => {
      loadControllerRef.current?.abort();
      submitControllerRef.current?.abort();
    };
  }, [loadConfiguration, tournamentId]);

  const poolById = useMemo(
    () =>
      new Map(
        (configuration?.category_pools ?? []).map((pool) => [pool.id, pool]),
      ),
    [configuration],
  );

  const clearFeedback = useCallback((seriesId: string): void => {
    setFormErrors((current) => {
      if (current[seriesId] === undefined) {
        return current;
      }
      const next = { ...current };
      delete next[seriesId];
      return next;
    });
    setNotices((current) => {
      if (current[seriesId] === undefined) {
        return current;
      }
      const next = { ...current };
      delete next[seriesId];
      return next;
    });
  }, []);

  const updateMode = useCallback(
    (seriesId: string, mode: SeriesMode): void => {
      onDirtyChange?.(true);
      dirtySeriesIdsRef.current.add(seriesId);
      setDrafts((current) => {
        const draft = current[seriesId];
        if (!draft) {
          return current;
        }
        return { ...current, [seriesId]: { ...draft, mode } };
      });
      clearFeedback(seriesId);
    },
    [clearFeedback, onDirtyChange],
  );

  const updateCategory = useCallback(
    (seriesId: string, category: Category): void => {
      onDirtyChange?.(true);
      dirtySeriesIdsRef.current.add(seriesId);
      setDrafts((current) => {
        const draft = current[seriesId];
        if (!draft) {
          return current;
        }
        return { ...current, [seriesId]: { ...draft, category } };
      });
      clearFeedback(seriesId);
    },
    [clearFeedback, onDirtyChange],
  );

  const updateReserveCount = useCallback((value: string): void => {
    const nextReserveCount = reserveCountFromValue(value);
    if (nextReserveCount === null) {
      return;
    }
    onDirtyChange?.(true);
    setReserveCountDraft(nextReserveCount);
    dirtyReserveRef.current = true;
    setReserveError(null);
    setReserveNotice(null);
  }, [onDirtyChange]);

  const reserveEditingClosed = Boolean(
    reserveServerCutoff ||
      selectedTournament?.started_at,
  );
  const anySubmitting = Boolean(submittingSeriesId) || submittingConfiguration;

  const handleReserveSubmit = useCallback(async (): Promise<void> => {
    if (
      !configuration ||
      !tournamentId ||
      reserveCountDraft === null ||
      loadState !== "ready" ||
      reserveEditingClosed ||
      submittingRef.current
    ) {
      return;
    }

    const body: UpdateTournamentConfigurationRequest = {
      expected_projection_revision: configuration.projection_revision,
      expected_configuration_revision: configuration.configuration_revision,
      reserve_count: reserveCountDraft,
      confirmed: true,
      reason: "Настройка резерва перед стартом",
      unlock_intents: [],
      swiss_default: configuration.swiss_default,
      semifinal_default: configuration.semifinal_default,
    };

    submittingRef.current = true;
    const submitRunId = submitRunRef.current + 1;
    submitRunRef.current = submitRunId;
    setSubmittingConfiguration(true);
    setReserveError(null);
    setReserveNotice(null);
    submitControllerRef.current?.abort();
    const controller = new AbortController();
    submitControllerRef.current = controller;

    try {
      await updateTournamentConfiguration(
        tournamentId,
        body,
        createOperatorCommandIntent().idempotencyKey,
        controller.signal,
      );
      if (
        controller.signal.aborted ||
        !mountedRef.current ||
        submitRunRef.current !== submitRunId
      ) {
        return;
      }
      const reloaded = await loadConfiguration(tournamentId, {
        preserveDirtyDrafts: true,
        replaceReserveCount: true,
      });
      if (reloaded && mountedRef.current) {
        dirtyReserveRef.current = false;
        onDirtyChange?.(dirtySeriesIdsRef.current.size > 0);
        setReserveNotice("Резерв сохранен.");
      }
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
        setReserveError("Сессия оператора истекла. Войдите снова.");
      } else if (error instanceof ApiError && error.status === 409) {
        setReserveError(staleMessage);
      } else if (error instanceof ApiError && error.status === 422) {
        const detail = problemMessage(error, "");
        setReserveServerCutoff(true);
        setReserveError(detail ? `${reserveCutoffMessage} ${detail}` : reserveCutoffMessage);
      } else {
        setReserveError(
          problemMessage(error, "Не удалось сохранить количество резервов"),
        );
      }
    } finally {
      if (submitControllerRef.current === controller) {
        submitControllerRef.current = null;
      }
      if (mountedRef.current && submitRunRef.current === submitRunId) {
        submittingRef.current = false;
        setSubmittingConfiguration(false);
      }
    }
  }, [
    configuration,
    loadConfiguration,
    loadState,
    onDirtyChange,
    onSessionExpired,
    reserveCountDraft,
    reserveEditingClosed,
    tournamentId,
  ]);

  const handleReload = useCallback((): void => {
    if (!tournamentId || submittingRef.current) {
      return;
    }
    void loadConfiguration(tournamentId);
  }, [loadConfiguration, tournamentId]);

  const handleSubmit = useCallback(
    async (
      series: TournamentConfigurationSeries,
      pool: TournamentConfigurationCategoryPool,
      isFinalSeries: boolean,
    ): Promise<void> => {
      if (
        !configuration ||
        !tournamentId ||
        loadState !== "ready" ||
        isFinalSeries ||
        (series.locked && series.unlock_intents.length === 0) ||
        series.started ||
        series.consumed ||
        series.disclosed ||
        submittingRef.current
      ) {
        return;
      }

      const draft = drafts[series.id];
      if (!draft || pool.categories.length === 0) {
        setFormErrors((current) => ({
          ...current,
          [series.id]: "Не удалось определить официальный пул серии. Обновите данные.",
        }));
        return;
      }
      if (
        draft.mode !== "draft" &&
        !pool.categories.includes(draft.category)
      ) {
        setFormErrors((current) => ({
          ...current,
          [series.id]: "Выберите категорию из официального пула серии.",
        }));
        return;
      }

      const categories = draft.mode === "draft"
        ? [...pool.categories]
        : [draft.category];
      const body: UpdateTournamentSeriesConfigurationRequest = {
        expected_projection_revision: configuration.projection_revision,
        expected_series_revision: series.revision,
        confirmed: true,
        reason: "Настройка категорий серии",
        unlock_intents: series.unlock_intents,
        mode: draft.mode,
        categories,
      };

      submittingRef.current = true;
      const submitRunId = submitRunRef.current + 1;
      submitRunRef.current = submitRunId;
      setSubmittingSeriesId(series.id);
      setFormErrors((current) => {
        const next = { ...current };
        delete next[series.id];
        return next;
      });
      setNotices((current) => {
        const next = { ...current };
        delete next[series.id];
        return next;
      });
      submitControllerRef.current?.abort();
      const controller = new AbortController();
      submitControllerRef.current = controller;

      try {
        await updateTournamentSeriesConfiguration(
          tournamentId,
          series.id,
          body,
          createOperatorCommandIntent().idempotencyKey,
          controller.signal,
        );
        if (
          controller.signal.aborted ||
          !mountedRef.current ||
          submitRunRef.current !== submitRunId
        ) {
          return;
        }
        const reloaded = await loadConfiguration(tournamentId, {
          preserveDirtyDrafts: true,
          replaceSeriesId: series.id,
        });
        if (reloaded && mountedRef.current) {
          dirtySeriesIdsRef.current.delete(series.id);
          onDirtyChange?.(dirtySeriesIdsRef.current.size > 0 || dirtyReserveRef.current);
          setNotices((current) => ({
            ...current,
            [series.id]: "Настройки серии сохранены.",
          }));
        }
      } catch (error) {
        if (controller.signal.aborted || isAbortError(error)) {
          return;
        }
        if (error instanceof ApiError && error.status === 401) {
          onSessionExpired?.();
          setFormErrors((current) => ({
            ...current,
            [series.id]: "Сессия оператора истекла. Войдите снова.",
          }));
        } else if (error instanceof ApiError && error.status === 409) {
          setFormErrors((current) => ({
            ...current,
            [series.id]: staleMessage,
          }));
        } else if (error instanceof ApiError && error.status === 422) {
          const detail = problemMessage(error, "");
          setFormErrors((current) => ({
            ...current,
            [series.id]: detail ? `${cutoffMessage} ${detail}` : cutoffMessage,
          }));
        } else {
          setFormErrors((current) => ({
            ...current,
            [series.id]: problemMessage(
              error,
              "Не удалось сохранить настройки серии",
            ),
          }));
        }
      } finally {
        if (submitControllerRef.current === controller) {
          submitControllerRef.current = null;
        }
        if (mountedRef.current && submitRunRef.current === submitRunId) {
          submittingRef.current = false;
          setSubmittingSeriesId(null);
        }
      }
    },
    [
      configuration,
      drafts,
      loadConfiguration,
      loadState,
      onDirtyChange,
      onSessionExpired,
      tournamentId,
    ],
  );

  return (
    <Panel
      title="Конфигурация серий"
      description="Настройте режим и категории каждой серии по официальным пулам соревнования."
      className={styles.panel}
    >
      {showTournamentChooser ? <div className={styles.chooser}>
        <label htmlFor="series-configuration-tournament-select">
          Соревнование для настройки серий
        </label>
        <select
          id="series-configuration-tournament-select"
          name="series_configuration_tournament"
          value={selectedTournamentId}
          onChange={(event) => onSelectTournament(event.target.value)}
          disabled={anySubmitting}
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
          Выберите соревнование, чтобы загрузить конфигурацию его серий.
        </Message>
      )}

      {selectedTournament && loadState === "loading" && (
        <Message tone="loading" title="Загружаем конфигурацию серий">
          Загружаем серии и официальные пулы категорий.
        </Message>
      )}

      {selectedTournament && loadState === "error" && (
        <Message tone="error" title="Конфигурация серий недоступна">
          {loadError || "Не удалось получить конфигурацию серий."}
          <button
            className={styles.inlineAction}
            type="button"
            onClick={handleReload}
          >
            Повторить загрузку
          </button>
        </Message>
      )}

      {selectedTournament &&
        loadState === "ready" &&
        configuration &&
        (
          <div className={styles.content}>
            <div className={styles.summary}>
              <div>
                <h3 className={styles.title}>{selectedTournament.name}</h3>
                <p className={styles.subtitle}>
                  Настройте формат и категории задач. Сохраните изменения для каждого матча.
                </p>
              </div>
              <dl className={styles.meta}>
                <div>
                  <dt>Матчей</dt>
                  <dd>{configuration.series.length}</dd>
                </div>
                <div>
                  <dt>Резерв обычных задач и задач дополнительного отбора</dt>
                  <dd>{configuration.reserve_count}</dd>
                </div>
              </dl>
            </div>

            <section className={styles.reservePanel} aria-labelledby="reserve-count-title">
              <div className={styles.reserveHeader}>
                <div>
                  <h3 id="reserve-count-title" className={styles.reserveTitle}>
                    Резерв заданий до старта
                  </h3>
                  <p className={styles.reserveDescription}>
                    Одно значение действует одновременно для обычного режима и дополнительного отбора. При нуле после сбоя основной попытки оператор принимает решение вручную.
                  </p>
                </div>
                <Status tone={reserveEditingClosed ? "disabled" : "info"} size="small">
                  {reserveEditingClosed ? "Только просмотр" : "Доступно до старта"}
                </Status>
              </div>

              {reserveEditingClosed && !reserveError && (
                <Message tone="warning" title="Изменение резерва закрыто">
                  {selectedTournament.started_at
                    ? "Соревнование уже началось. Количество резервов доступно только для просмотра."
                    : "Настройки уже используются. Количество резервов доступно только для просмотра."}
                </Message>
              )}

              <div className={styles.reserveFields}>
                <div className={`${styles.field} ${styles.reserveField}`}>
                  <label htmlFor="reserve-count-select">
                    Количество резервов для обычного режима и дополнительного отбора
                  </label>
                  <select
                    id="reserve-count-select"
                    value={reserveCountDraft === null ? "" : String(reserveCountDraft)}
                    onChange={(event) => updateReserveCount(event.target.value)}
                    disabled={reserveEditingClosed || anySubmitting || reserveCountDraft === null}
                    aria-describedby={reserveError ? "reserve-count-error" : "reserve-count-help"}
                  >
                    <option value="">Загрузка значения</option>
                    {RESERVE_COUNTS.map((count) => (
                      <option key={count} value={count}>
                        {count === 0
                          ? "0 - без автоматического резерва"
                          : `${count} - ${count === 1 ? "один резерв" : "два резерва"}`}
                      </option>
                    ))}
                  </select>
                  <span id="reserve-count-help" className={styles.fieldHint}>
                    Резерв расходуется только после сбоя основной попытки; значение 0 означает вмешательство оператора.
                  </span>
                  <div className={styles.reserveActions}>
                    <Button
                      type="button"
                      onClick={() => void handleReserveSubmit()}
                      loading={submittingConfiguration}
                      loadingLabel="Сохраняем"
                      disabled={reserveEditingClosed || reserveCountDraft === null || anySubmitting}
                      aria-describedby={reserveError ? "reserve-count-error" : undefined}
                    >
                      Сохранить резерв
                    </Button>
                  </div>
                </div>
              </div>

              {reserveError && (
                <Message id="reserve-count-error" tone="error" title="Резерв не сохранен">
                  {reserveError}
                  <button
                    className={styles.inlineAction}
                    type="button"
                    onClick={handleReload}
                    disabled={anySubmitting}
                  >
                    Повторить загрузку
                  </button>
                </Message>
              )}
              {reserveNotice && (
                <Message tone="success" title="Резерв сохранен">
                  {reserveNotice}
                </Message>
              )}
            </section>

            {configuration.series.length > 0 ? (
              <>
                <div className={styles.seriesGrid}>
              {configuration.series.map((series, index) => {
                const pool = poolById.get(series.category_pool_revision_id);
                const isFinalSeries = series.stage === "final";
                const canUnlock = series.locked && series.unlock_intents.length > 0;
                const isLocked =
                  isFinalSeries ||
                  (series.locked && !canUnlock) ||
                  series.started ||
                  series.consumed ||
                  series.disclosed ||
                  !pool;
                const draft = drafts[series.id];
                const seriesTitleId = `series-configuration-title-${series.id}`;
                const seriesStatusId = `series-configuration-status-${series.id}`;
                const modeId = `series-${index + 1}-mode`;
                const categoryId = `series-${index + 1}-category`;
                const categoryPlan = pool?.categories ?? [];

                return (
                  <article
                    className={styles.seriesCard}
                    key={series.id}
                    data-series-id={series.id}
                    data-series-index={index + 1}
                    aria-labelledby={seriesTitleId}
                  >
                    <header className={styles.seriesHeader}>
                      <div>
                        <h4 id={seriesTitleId} className={styles.seriesTitle}>
                          Серия {index + 1} - {stageLabel(series)}
                          {pool ? ` (${formatSeriesFormat(pool.format)})` : ""}
                        </h4>
                        <p className={styles.seriesSubtitle}>
                          Выберите категории задач и формат матча.
                        </p>
                      </div>
                      <Status
                        id={seriesStatusId}
                        tone={isLocked ? "disabled" : "info"}
                      >
                        {isLocked ? "Только просмотр" : "Доступна для изменения"}
                      </Status>
                    </header>

                    <div className={styles.plan}>
                      <span className={styles.planLabel}>
                        План {isFinalSeries ? "финальной BO3 серии" : "серии"}
                      </span>
                      {pool ? (
                        <ul className={styles.planList} aria-label={`План серии ${index + 1}`}>
                          {categoryPlan.map((category) => (
                            <li key={category}>{formatCategory(category)}</li>
                          ))}
                        </ul>
                      ) : (
                        <span className={styles.planEmpty}>
                          Официальный пул не найден. Данные пула недоступны.
                        </span>
                      )}
                    </div>

                    {isLocked && (
                      <Message
                        tone={isFinalSeries ? "info" : "warning"}
                        title={isFinalSeries ? "Финальная серия неизменяема" : "Редактирование закрыто"}
                      >
                        {lockedReason(series, isFinalSeries)}
                      </Message>
                    )}

                    {!isLocked && canUnlock && (
                      <Message tone="warning" title="Сохранение с разблокировкой">
                        При сохранении серия откроется для изменений и будет настроена заново по указанным данным.
                      </Message>
                    )}

                    {!isLocked && pool && draft && (
                      <fieldset
                        className={styles.fields}
                        disabled={anySubmitting}
                        aria-describedby={seriesStatusId}
                      >
                        <legend>Настройки серии {index + 1}</legend>
                        <div className={styles.field}>
                          <label htmlFor={modeId}>Режим серии {index + 1}</label>
                          <select
                            id={modeId}
                            value={draft.mode}
                            onChange={(event) => {
                              const mode = SERIES_MODES.find(
                                (item) => item === event.target.value,
                              );
                              if (mode) {
                                updateMode(series.id, mode);
                              }
                            }}
                          >
                            {SERIES_MODES.map((mode) => (
                              <option key={mode} value={mode}>
                                {SERIES_MODE_LABELS[mode]}
                              </option>
                            ))}
                          </select>
                        </div>

                        {draft.mode === "draft" ? (
                          <Message tone="info" title="Полный драфт">
                            При сохранении будет использован весь официальный пул {pool.format.toUpperCase()}.
                          </Message>
                        ) : (
                          <div className={styles.field}>
                            <label htmlFor={categoryId}>
                              Категория серии {index + 1}
                            </label>
                            <select
                              id={categoryId}
                              value={draft.category}
                              onChange={(event) => {
                                const category = pool.categories.find(
                                  (item) => item === event.target.value,
                                );
                                if (category) {
                                  updateCategory(series.id, category);
                                }
                              }}
                            >
                              {pool.categories.map((category) => (
                                <option key={category} value={category}>
                                  {formatCategory(category)}
                                </option>
                              ))}
                            </select>
                            <span className={styles.fieldHint}>
                              Для режима «{SERIES_MODE_LABELS[draft.mode]}» отправляется ровно одна категория из пула.
                            </span>
                          </div>
                        )}
                      </fieldset>
                    )}

                    {formErrors[series.id] && (
                      <Message
                        id={`series-${index + 1}-error`}
                        tone="error"
                        title="Серия не сохранена"
                      >
                        {formErrors[series.id]}
                        <button
                          className={styles.inlineAction}
                          type="button"
                          onClick={handleReload}
                          disabled={anySubmitting}
                        >
                          Повторить загрузку
                        </button>
                      </Message>
                    )}
                    {notices[series.id] && (
                      <Message tone="success" title="Серия сохранена">
                        {notices[series.id]}
                      </Message>
                    )}

                    <div className={styles.actions}>
                      <Button
                        type="button"
                        onClick={() => {
                          if (pool) {
                            void handleSubmit(series, pool, isFinalSeries);
                          }
                        }}
                        loading={submittingSeriesId === series.id}
                        loadingLabel="Сохраняем"
                        disabled={isLocked || !pool || !draft || anySubmitting}
                        aria-describedby={formErrors[series.id] ? `series-${index + 1}-error` : undefined}
                      >
                        Сохранить серию {index + 1}
                      </Button>
                    </div>
                  </article>
                );
              })}
                </div>
              </>
            ) : (
              <Message tone="empty" title="Серии не найдены">
                Для выбранного соревнования нет серий.
              </Message>
            )}
          </div>
        )}
    </Panel>
  );
};

SeriesConfigurationEditor.displayName = "SeriesConfigurationEditor";
