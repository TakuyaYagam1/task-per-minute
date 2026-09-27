"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useParticipantNames } from "../../entities/tournament";

import {
  ApiError,
  operatorApi,
  type AuditActorKind,
  type AuditCursor,
  type AuditEntityKind,
  type AuditEvent,
  type AuditPage,
  type IncidentBundle,
  type OperatorRecoverySnapshot,
  type Tournament,
  type TournamentAuditQuery,
} from "../../shared/api";
import { formatGameState, formatResultReason, formatTournamentState, RESULT_REASON_LABELS } from "../../shared/lib";
import { Button, Message, Panel, Status, TechnicalDetails } from "../../shared/ui";

import styles from "./TournamentAuditPanel.module.css";

type TournamentAuditPanelProps = Readonly<{
  tournaments: readonly Tournament[];
  selectedTournamentId: string;
  onSelectTournament: (tournamentId: string) => void;
  onSessionExpired?: () => void;
  showTournamentChooser?: boolean;
}>;

type AuditFilters = Readonly<{
  entityKind: "" | AuditEntityKind;
  entityId: string;
  eventType: string;
  actorKind: "" | AuditActorKind;
  actorId: string;
  resultReason: string;
  occurredFrom: string;
  occurredTo: string;
}>;

type AppliedAuditQuery = Omit<TournamentAuditQuery, "tournament_id" | "cursor">;
type LoadState = "idle" | "loading" | "ready" | "error";
type BundleReceipt = Pick<
  IncidentBundle,
  "generated_at" | "projection_revision" | "sha256" | "tournament_id"
>;

const PAGE_SIZE = 25;
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

const emptyFilters = (): AuditFilters => ({
  entityKind: "",
  entityId: "",
  eventType: "",
  actorKind: "",
  actorId: "",
  resultReason: "",
  occurredFrom: "",
  occurredTo: "",
});

const defaultQuery = (): AppliedAuditQuery => ({ page_size: PAGE_SIZE });

const formatDateTime = (value: string): string => {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "Дата недоступна";
  }
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "medium",
    timeZone: "UTC",
  }).format(date);
};

const problemMessage = (error: unknown, fallback: string): string => {
  if (error instanceof ApiError) {
    if (error.status === 403) {
      return "У этой сессии нет доступа к операторскому аудиту.";
    }
    return error.problem?.detail || error.problem?.title || fallback;
  }
  return fallback;
};

const toServerTime = (value: string): string | undefined => {
  if (!value) {
    return undefined;
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString();
};

const validateFilters = (filters: AuditFilters): string | null => {
  if (filters.entityId && !UUID_PATTERN.test(filters.entityId.trim())) {
    return "Проверьте ID записи в технических фильтрах. Нужен полный UUID из журнала.";
  }
  if (filters.actorId && !UUID_PATTERN.test(filters.actorId.trim())) {
    return "Проверьте ID администратора в технических фильтрах. Нужен полный UUID из журнала.";
  }
  const occurredFrom = toServerTime(filters.occurredFrom);
  const occurredTo = toServerTime(filters.occurredTo);
  if (filters.occurredFrom && !occurredFrom) {
    return "Начало диапазона времени заполнено неверно.";
  }
  if (filters.occurredTo && !occurredTo) {
    return "Конец диапазона времени заполнен неверно.";
  }
  if (occurredFrom && occurredTo && occurredFrom > occurredTo) {
    return "Начало диапазона должно быть раньше конца.";
  }
  return null;
};

const queryFromFilters = (filters: AuditFilters): AppliedAuditQuery => ({
  page_size: PAGE_SIZE,
  ...(filters.entityKind ? { entity_kind: filters.entityKind } : {}),
  ...(filters.entityId.trim() ? { entity_id: filters.entityId.trim() } : {}),
  ...(filters.eventType.trim() ? { event_type: filters.eventType.trim() } : {}),
  ...(filters.actorKind ? { actor_kind: filters.actorKind } : {}),
  ...(filters.actorId.trim() ? { actor_id: filters.actorId.trim() } : {}),
  ...(filters.resultReason.trim() ? { result_reason: filters.resultReason.trim() } : {}),
  ...(toServerTime(filters.occurredFrom)
    ? { occurred_from: toServerTime(filters.occurredFrom) }
    : {}),
  ...(toServerTime(filters.occurredTo)
    ? { occurred_to: toServerTime(filters.occurredTo) }
    : {}),
});

const EVENT_LABELS: Readonly<Record<string, string>> = {
  result_recorded: "Результат записан",
  result_corrected: "Результат исправлен",
  result_superseded: "Результат заменен",
  "tournament.result.settled": "Результат подтвержден",
  replay_requested: "Запрошена переигровка",
  correction_committed: "Исправление подтверждено",
};

const eventTypeLabel = (eventType: string): string =>
  EVENT_LABELS[eventType] ?? "Другое событие";

const actorLabel = (event: AuditEvent): string => {
  return event.actor_kind === "operator" ? "Администратор" : "Система";
};

const payloadLabel = (key: string): string => {
  const labels: Readonly<Record<string, string>> = {
    attempt_id: "ID попытки",
    entity_id: "ID записи",
    entity_kind: "Тип записи",
    previous_revision_id: "Предыдущая версия",
    projection_revision_id: "Версия данных",
    reason: "Обоснование",
    result_reason: "Причина результата",
    revision_number: "Номер версии",
    series_id: "ID матча",
    source_projection_revision_id: "Исходная версия данных",
    state: "Состояние",
    tournament_id: "Турнир",
    winner_id: "Победитель",
  };
  return labels[key] ?? key;
};

const downloadBundle = (bundle: IncidentBundle, tournamentName: string): void => {
  const payload = JSON.stringify(bundle, null, 2);
  const url = URL.createObjectURL(new Blob([payload], { type: "application/json" }));
  const link = document.createElement("a");
  link.href = url;
  const filename = tournamentName.replace(/[^\p{L}\p{N}._ -]/gu, "").trim().slice(0, 80) || "Турнир";
  link.download = `Отчет - ${filename} - ${bundle.generated_at.slice(0, 10)}.json`;
  document.body.append(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
};

export const TournamentAuditPanel = ({
  tournaments,
  selectedTournamentId,
  onSelectTournament,
  onSessionExpired,
  showTournamentChooser = true,
}: TournamentAuditPanelProps) => {
  const [filters, setFilters] = useState<AuditFilters>(emptyFilters);
  const [appliedQuery, setAppliedQuery] = useState<AppliedAuditQuery>(defaultQuery);
  const [pages, setPages] = useState<AuditPage[]>([]);
  const [pageIndex, setPageIndex] = useState(0);
  const [loadState, setLoadState] = useState<LoadState>("idle");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [filterError, setFilterError] = useState<string | null>(null);
  const [bundleState, setBundleState] = useState<LoadState>("idle");
  const [bundleError, setBundleError] = useState<string | null>(null);
  const [bundle, setBundle] = useState<BundleReceipt | null>(null);
  const [snapshot, setSnapshot] = useState<OperatorRecoverySnapshot | null>(null);
  const currentSnapshot = snapshot?.tournament.id === selectedTournamentId ? snapshot : null;
  const { matchName } = useParticipantNames(selectedTournamentId, currentSnapshot?.roster ?? null);

  useEffect(() => {
    if (!selectedTournamentId) return;
    const controller = new AbortController();
    void operatorApi.getSnapshot(selectedTournamentId, undefined, controller.signal).then((next) => {
      if (!controller.signal.aborted) setSnapshot(next);
    }).catch(() => {
      if (!controller.signal.aborted) setSnapshot(null);
    });
    return () => controller.abort();
  }, [selectedTournamentId]);

  const eventSubject = (event: AuditEvent): string => {
    const series = currentSnapshot?.series.find((item) =>
      (event.entity_kind === "series" && item.id === event.entity_id) ||
      item.id === event.redacted_payload.series_id ||
      item.slots.some((slot) => slot.attempts.some((attempt) => attempt.id === event.entity_id)),
    );
    if (!series) return event.entity_kind === "series" ? "Матч" : "Попытка решения";
    return matchName(series);
  };
  const auditControllerRef = useRef<AbortController | null>(null);
  const bundleControllerRef = useRef<AbortController | null>(null);

  const currentPage = pages[pageIndex] ?? null;
  const selectedTournament = useMemo(
    () => tournaments.find((tournament) => tournament.id === selectedTournamentId) ?? null,
    [selectedTournamentId, tournaments],
  );

  const fetchPage = useCallback(async (
    tournamentId: string,
    query: AppliedAuditQuery,
    cursor?: AuditCursor,
  ): Promise<AuditPage | null> => {
    auditControllerRef.current?.abort();
    const controller = new AbortController();
    auditControllerRef.current = controller;
    setLoadState("loading");
    setLoadError(null);
    try {
      const page = await operatorApi.listAudit(
        { tournament_id: tournamentId, ...query, ...(cursor ? { cursor } : {}) },
        controller.signal,
      );
      if (controller.signal.aborted) {
        return null;
      }
      setLoadState("ready");
      return page;
    } catch (error) {
      if (controller.signal.aborted) {
        return null;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      setLoadState("error");
      setLoadError(problemMessage(error, "Не удалось загрузить историю турнира."));
      return null;
    } finally {
      if (auditControllerRef.current === controller) {
        auditControllerRef.current = null;
      }
    }
  }, [onSessionExpired]);

  useEffect(() => {
    auditControllerRef.current?.abort();
    bundleControllerRef.current?.abort();
    setFilters(emptyFilters());
    setAppliedQuery(defaultQuery());
    setPages([]);
    setPageIndex(0);
    setFilterError(null);
    setLoadError(null);
    setBundle(null);
    setBundleError(null);
    setBundleState("idle");
    if (!selectedTournamentId) {
      setLoadState("idle");
      return;
    }
    void fetchPage(selectedTournamentId, defaultQuery()).then((page) => {
      if (page) {
        setPages([page]);
      }
    });
    return () => {
      auditControllerRef.current?.abort();
      bundleControllerRef.current?.abort();
    };
  }, [fetchPage, selectedTournamentId]);

  const applyFilters = async (): Promise<void> => {
    if (!selectedTournamentId) {
      setFilterError("Сначала выберите турнир.");
      return;
    }
    const validationError = validateFilters(filters);
    if (validationError) {
      setFilterError(validationError);
      return;
    }
    setFilterError(null);
    const query = queryFromFilters(filters);
    setAppliedQuery(query);
    const page = await fetchPage(selectedTournamentId, query);
    if (page) {
      setPages([page]);
      setPageIndex(0);
    }
  };

  const resetFilters = async (): Promise<void> => {
    const nextFilters = emptyFilters();
    const query = defaultQuery();
    setFilters(nextFilters);
    setFilterError(null);
    setAppliedQuery(query);
    if (!selectedTournamentId) {
      return;
    }
    const page = await fetchPage(selectedTournamentId, query);
    if (page) {
      setPages([page]);
      setPageIndex(0);
    }
  };

  const openNextPage = async (): Promise<void> => {
    if (!selectedTournamentId || !currentPage?.next_cursor) {
      return;
    }
    const cachedPage = pages[pageIndex + 1];
    if (cachedPage) {
      setPageIndex((current) => current + 1);
      return;
    }
    const page = await fetchPage(
      selectedTournamentId,
      appliedQuery,
      currentPage.next_cursor,
    );
    if (page) {
      setPages((current) => [...current.slice(0, pageIndex + 1), page]);
      setPageIndex((current) => current + 1);
    }
  };

  const exportIncident = async (): Promise<void> => {
    if (!selectedTournamentId) {
      return;
    }
    bundleControllerRef.current?.abort();
    const controller = new AbortController();
    bundleControllerRef.current = controller;
    setBundleState("loading");
    setBundleError(null);
    try {
      const nextBundle = await operatorApi.exportIncident(
        selectedTournamentId,
        controller.signal,
      );
      if (controller.signal.aborted) {
        return;
      }
      downloadBundle(nextBundle, selectedTournament?.name ?? "Турнир");
      setBundle({
        generated_at: nextBundle.generated_at,
        projection_revision: nextBundle.projection_revision,
        sha256: nextBundle.sha256,
        tournament_id: nextBundle.tournament_id,
      });
      setBundleState("ready");
    } catch (error) {
      if (controller.signal.aborted) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      setBundleState("error");
      setBundleError(problemMessage(error, "Не удалось скачать отчет. Попробуйте еще раз."));
    } finally {
      if (bundleControllerRef.current === controller) {
        bundleControllerRef.current = null;
      }
    }
  };

  return (
    <Panel
      title="История турнира"
      description="Просматривайте результаты и действия администраторов. Отчет поможет разобрать ошибку или спорный результат."
      className={styles.panel}
    >
      <div className={styles.toolbar}>
        {showTournamentChooser ? (
          <div className={styles.fieldWide}>
            <label htmlFor="audit-tournament">Турнир</label>
            <select
              id="audit-tournament"
              value={selectedTournamentId}
              onChange={(event) => onSelectTournament(event.target.value)}
            >
              <option value="">Выберите турнир</option>
              {tournaments.map((tournament) => (
                <option key={tournament.id} value={tournament.id}>
                  {tournament.name} - {formatTournamentState(tournament.state)}
                </option>
              ))}
            </select>
          </div>
        ) : null}
        <div className={styles.bundleAction}>
          <Button
            type="button"
            variant="secondary"
            disabled={!selectedTournamentId}
            loading={bundleState === "loading"}
            loadingLabel="Готовим отчет"
            onClick={() => void exportIncident()}
          >
            Скачать отчет
          </Button>
          <span>Сохранить историю и результаты турнира в файл.</span>
        </div>
      </div>

      {bundleError ? (
        <Message tone="error" title="Не удалось скачать отчет">
          {bundleError}
        </Message>
      ) : null}
      {bundle ? (
        <div className={styles.bundleReceipt} aria-live="polite">
          <div>
            <span>Турнир</span>
            <strong>{selectedTournament?.name ?? "Выбранный турнир"}</strong>
          </div>
          <div>
            <span>Отчет готов</span>
            <strong>{formatDateTime(bundle.generated_at)} UTC</strong>
          </div>
          <div className={styles.bundleHash}>
            <TechnicalDetails>
              <p>ID турнира: <code>{bundle.tournament_id}</code></p>
              <p>Версия данных: {bundle.projection_revision}</p>
              <span>SHA-256</span>
              <code>{bundle.sha256}</code>
            </TechnicalDetails>
          </div>
        </div>
      ) : null}

      <form
        className={styles.filters}
        onSubmit={(event) => {
          event.preventDefault();
          void applyFilters();
        }}
        noValidate
      >
        <div className={styles.field}>
          <label htmlFor="audit-entity-kind">Что изменилось</label>
          <select
            id="audit-entity-kind"
            value={filters.entityKind}
            onChange={(event) => setFilters((current) => ({
              ...current,
              entityKind: event.target.value as AuditFilters["entityKind"],
            }))}
          >
            <option value="">Все</option>
            <option value="series">Матч</option>
            <option value="game_attempt">Попытка решения</option>
          </select>
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-event-type">Событие</label>
          <select
            id="audit-event-type"
            value={filters.eventType}
            onChange={(event) => setFilters((current) => ({ ...current, eventType: event.target.value }))}
          >
            <option value="">Все события</option>
            {Object.entries(EVENT_LABELS).map(([value, label]) => (
              <option key={value} value={value}>{label}</option>
            ))}
            {filters.eventType && !EVENT_LABELS[filters.eventType] ? (
              <option value={filters.eventType}>Событие из технических фильтров</option>
            ) : null}
          </select>
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-actor-kind">Кто выполнил</label>
          <select
            id="audit-actor-kind"
            value={filters.actorKind}
            onChange={(event) => setFilters((current) => ({
              ...current,
              actorKind: event.target.value as AuditFilters["actorKind"],
            }))}
          >
            <option value="">Все</option>
            <option value="server">Система</option>
            <option value="operator">Администратор</option>
          </select>
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-result-reason">Причина результата</label>
          <select
            id="audit-result-reason"
            value={filters.resultReason}
            onChange={(event) => setFilters((current) => ({ ...current, resultReason: event.target.value }))}
          >
            <option value="">Все причины</option>
            {Object.entries(RESULT_REASON_LABELS).map(([value, label]) => (
              <option key={value} value={value}>{label}</option>
            ))}
            {filters.resultReason && !Object.hasOwn(RESULT_REASON_LABELS, filters.resultReason) ? (
              <option value={filters.resultReason}>Причина из технических фильтров</option>
            ) : null}
          </select>
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-occurred-from">С даты, ваше время</label>
          <input
            id="audit-occurred-from"
            type="datetime-local"
            value={filters.occurredFrom}
            onChange={(event) => setFilters((current) => ({ ...current, occurredFrom: event.target.value }))}
          />
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-occurred-to">По дату, ваше время</label>
          <input
            id="audit-occurred-to"
            type="datetime-local"
            value={filters.occurredTo}
            onChange={(event) => setFilters((current) => ({ ...current, occurredTo: event.target.value }))}
          />
        </div>
        <div className={styles.advancedFilters}>
          <TechnicalDetails summary={`Технические фильтры${filters.entityId || filters.actorId ? " (заданы ID)" : ""}`}>
            <p>Для поиска конкретной записи при разборе ошибки. Обычно эти поля можно оставить пустыми.</p>
            <div className={styles.advancedFields}>
              <div className={styles.field}>
                <label htmlFor="audit-entity-id">ID записи</label>
                <input id="audit-entity-id" value={filters.entityId} autoComplete="off"
                  onChange={(event) => setFilters((current) => ({ ...current, entityId: event.target.value }))} />
              </div>
              <div className={styles.field}>
                <label htmlFor="audit-actor-id">ID администратора</label>
                <input id="audit-actor-id" value={filters.actorId} autoComplete="off"
                  onChange={(event) => setFilters((current) => ({ ...current, actorId: event.target.value }))} />
              </div>
              <div className={styles.field}>
                <label htmlFor="audit-event-code">Код события</label>
                <input id="audit-event-code" value={filters.eventType} autoComplete="off"
                  onChange={(event) => setFilters((current) => ({ ...current, eventType: event.target.value }))} />
              </div>
              <div className={styles.field}>
                <label htmlFor="audit-reason-code">Код причины</label>
                <input id="audit-reason-code" value={filters.resultReason} autoComplete="off"
                  onChange={(event) => setFilters((current) => ({ ...current, resultReason: event.target.value }))} />
              </div>
            </div>
          </TechnicalDetails>
        </div>
        <div className={styles.filterActions}>
          <Button type="submit" disabled={!selectedTournamentId} loading={loadState === "loading"}>
            Применить фильтры
          </Button>
          <Button
            type="button"
            variant="secondary"
            disabled={!selectedTournamentId || loadState === "loading"}
            onClick={() => void resetFilters()}
          >
            Сбросить
          </Button>
        </div>
      </form>

      {filterError ? (
        <Message tone="error" title="Проверьте фильтры">
          {filterError}
        </Message>
      ) : null}
      {loadError ? (
        <Message tone="error" title="История недоступна">
          {loadError}
        </Message>
      ) : null}
      {!selectedTournamentId ? (
        <Message tone="info" title="Выберите турнир">
          История и экспорт загружаются только для выбранного турнира.
        </Message>
      ) : null}
      {selectedTournamentId && loadState === "loading" && !currentPage ? (
        <Message tone="loading" title="Загружаем историю">
          Получаем события выбранного турнира.
        </Message>
      ) : null}
      {selectedTournamentId && loadState === "ready" && currentPage?.events.length === 0 ? (
        <Message tone="info" title="Событий не найдено">
          Измените фильтры или выберите другой турнир.
        </Message>
      ) : null}

      {currentPage?.events.length ? (
        <div className={styles.results}>
          <div className={styles.resultsHeading} aria-live="polite">
            <div>
              <strong>{selectedTournament?.name ?? "Выбранный турнир"}</strong>
              <span>Страница {pageIndex + 1}, событий: {currentPage.events.length}</span>
            </div>
            <span>Время показано в UTC</span>
          </div>
          <ol className={styles.eventList} aria-label="События аудита">
            {currentPage.events.map((event) => {
              const payloadEntries = Object.entries(event.redacted_payload);
              return (
                <li key={event.audit_event_id}>
                  <article className={styles.event}>
                    <header className={styles.eventHeader}>
                      <div>
                        <strong>{eventTypeLabel(event.event_type)}</strong>
                      </div>
                      <Status tone={event.is_current ? "success" : "warning"}>
                        {event.is_current ? "Действует" : "Исправлено"}
                      </Status>
                    </header>
                    <dl className={styles.eventFacts}>
                      <div>
                        <dt>Время</dt>
                        <dd>{formatDateTime(event.occurred_at)} UTC</dd>
                      </div>
                      <div>
                        <dt>Что изменилось</dt>
                        <dd>{eventSubject(event)}</dd>
                      </div>
                      <div>
                        <dt>Кто выполнил</dt>
                        <dd>{actorLabel(event)}</dd>
                      </div>
                      <div>
                        <dt>Результат</dt>
                        <dd>{formatGameState(event.result_state)}. {formatResultReason(event.result_reason)}</dd>
                      </div>
                    </dl>
                    <div className={styles.payload}>
                      <TechnicalDetails>
                        <dl>
                          <div><dt>ID записи</dt><dd><code>{event.entity_id}</code></dd></div>
                          <div><dt>Код события</dt><dd><code>{event.event_type}</code></dd></div>
                          <div><dt>ID администратора</dt><dd><code>{event.actor_id || "Не указан"}</code></dd></div>
                          <div><dt>ID результата</dt><dd><code>{event.official_result_revision_id}</code></dd></div>
                          <div><dt>Версия результата</dt><dd>{event.revision_number}</dd></div>
                          {payloadEntries.map(([key, value]) => (
                            <div key={key}>
                              <dt>{payloadLabel(key)}</dt>
                              <dd><code>{String(value)}</code></dd>
                            </div>
                          ))}
                        </dl>
                      </TechnicalDetails>
                    </div>
                  </article>
                </li>
              );
            })}
          </ol>
          <div className={styles.pagination}>
            <Button
              type="button"
              variant="secondary"
              disabled={pageIndex === 0 || loadState === "loading"}
              onClick={() => setPageIndex((current) => Math.max(0, current - 1))}
            >
              Предыдущая страница
            </Button>
            <Button
              type="button"
              variant="secondary"
              disabled={!currentPage.next_cursor || loadState === "loading"}
              loading={loadState === "loading"}
              loadingLabel="Загружаем"
              onClick={() => void openNextPage()}
            >
              Следующая страница
            </Button>
          </div>
        </div>
      ) : null}
    </Panel>
  );
};

TournamentAuditPanel.displayName = "TournamentAuditPanel";
