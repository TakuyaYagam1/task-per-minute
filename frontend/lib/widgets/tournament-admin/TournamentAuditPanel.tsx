"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  ApiError,
  operatorApi,
  type AuditActorKind,
  type AuditCursor,
  type AuditEntityKind,
  type AuditEvent,
  type AuditPage,
  type IncidentBundle,
  type Tournament,
  type TournamentAuditQuery,
} from "../../shared/api";
import { Button, Message, Panel, Status } from "../../shared/ui";

import styles from "./TournamentAuditPanel.module.css";

type TournamentAuditPanelProps = Readonly<{
  tournaments: readonly Tournament[];
  selectedTournamentId: string;
  onSelectTournament: (tournamentId: string) => void;
  onSessionExpired?: () => void;
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
    return "Entity ID должен быть UUID.";
  }
  if (filters.actorId && !UUID_PATTERN.test(filters.actorId.trim())) {
    return "Actor ID должен быть UUID.";
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

const eventTypeLabel = (eventType: string): string => {
  const known: Readonly<Record<string, string>> = {
    result_recorded: "Результат записан",
    result_corrected: "Результат исправлен",
    replay_requested: "Запрошен replay",
    correction_committed: "Коррекция подтверждена",
  };
  return known[eventType] ?? eventType.replaceAll("_", " ").replaceAll(".", " ");
};

const actorLabel = (event: AuditEvent): string => {
  const actor = event.actor_kind === "operator" ? "Оператор" : "Сервер";
  return event.actor_id ? `${actor}: ${event.actor_id}` : actor;
};

const payloadLabel = (key: string): string => {
  const labels: Readonly<Record<string, string>> = {
    attempt_id: "Attempt",
    entity_id: "Entity",
    entity_kind: "Тип entity",
    previous_revision_id: "Предыдущая revision",
    projection_revision_id: "Projection revision",
    reason: "Обоснование",
    result_reason: "Причина результата",
    revision_number: "Номер revision",
    series_id: "Series",
    source_projection_revision_id: "Исходная projection",
    state: "Состояние",
    tournament_id: "Турнир",
    winner_id: "Победитель",
  };
  return labels[key] ?? key;
};

const downloadBundle = (bundle: IncidentBundle): void => {
  const payload = JSON.stringify(bundle, null, 2);
  const url = URL.createObjectURL(new Blob([payload], { type: "application/json" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = `incident-${bundle.tournament_id}-r${bundle.projection_revision}.json`;
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
      setLoadError(problemMessage(error, "Не удалось загрузить аудит турнира."));
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
      downloadBundle(nextBundle);
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
      setBundleError(problemMessage(error, "Не удалось скачать incident bundle."));
    } finally {
      if (bundleControllerRef.current === controller) {
        bundleControllerRef.current = null;
      }
    }
  };

  return (
    <Panel
      title="Аудит и incident bundle"
      description="Ищите только redacted события сервера и сохраняйте подписанный снимок инцидента."
      className={styles.panel}
    >
      <div className={styles.toolbar}>
        <div className={styles.fieldWide}>
          <label htmlFor="audit-tournament">Турнир для аудита</label>
          <select
            id="audit-tournament"
            value={selectedTournamentId}
            onChange={(event) => onSelectTournament(event.target.value)}
          >
            <option value="">Выберите турнир</option>
            {tournaments.map((tournament) => (
              <option key={tournament.id} value={tournament.id}>
                {tournament.name} - {tournament.state}
              </option>
            ))}
          </select>
        </div>
        <div className={styles.bundleAction}>
          <Button
            type="button"
            variant="secondary"
            disabled={!selectedTournamentId}
            loading={bundleState === "loading"}
            loadingLabel="Готовим bundle"
            onClick={() => void exportIncident()}
          >
            Скачать incident bundle
          </Button>
          <span>JSON envelope сохраняется целиком, содержимое не открывается в браузере.</span>
        </div>
      </div>

      {bundleError ? (
        <Message tone="error" title="Incident bundle недоступен">
          {bundleError}
        </Message>
      ) : null}
      {bundle ? (
        <div className={styles.bundleReceipt} aria-live="polite">
          <div>
            <span>Турнир</span>
            <code>{bundle.tournament_id}</code>
          </div>
          <div>
            <span>Projection revision</span>
            <strong>{bundle.projection_revision}</strong>
          </div>
          <div>
            <span>Сформирован сервером</span>
            <strong>{formatDateTime(bundle.generated_at)} UTC</strong>
          </div>
          <div className={styles.bundleHash}>
            <span>SHA-256</span>
            <code>{bundle.sha256}</code>
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
          <label htmlFor="audit-entity-kind">Тип entity</label>
          <select
            id="audit-entity-kind"
            value={filters.entityKind}
            onChange={(event) => setFilters((current) => ({
              ...current,
              entityKind: event.target.value as AuditFilters["entityKind"],
            }))}
          >
            <option value="">Все</option>
            <option value="series">Series</option>
            <option value="game_attempt">Game attempt</option>
          </select>
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-entity-id">Entity ID</label>
          <input
            id="audit-entity-id"
            value={filters.entityId}
            onChange={(event) => setFilters((current) => ({ ...current, entityId: event.target.value }))}
            placeholder="UUID entity"
            autoComplete="off"
          />
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-event-type">Событие</label>
          <input
            id="audit-event-type"
            value={filters.eventType}
            onChange={(event) => setFilters((current) => ({ ...current, eventType: event.target.value }))}
            placeholder="result_recorded"
            autoComplete="off"
          />
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-actor-kind">Actor</label>
          <select
            id="audit-actor-kind"
            value={filters.actorKind}
            onChange={(event) => setFilters((current) => ({
              ...current,
              actorKind: event.target.value as AuditFilters["actorKind"],
            }))}
          >
            <option value="">Все</option>
            <option value="server">Сервер</option>
            <option value="operator">Оператор</option>
          </select>
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-actor-id">Actor ID</label>
          <input
            id="audit-actor-id"
            value={filters.actorId}
            onChange={(event) => setFilters((current) => ({ ...current, actorId: event.target.value }))}
            placeholder="UUID оператора"
            autoComplete="off"
          />
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-result-reason">Причина результата</label>
          <input
            id="audit-result-reason"
            value={filters.resultReason}
            onChange={(event) => setFilters((current) => ({ ...current, resultReason: event.target.value }))}
            placeholder="score_complete"
            autoComplete="off"
          />
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-occurred-from">От, серверное время</label>
          <input
            id="audit-occurred-from"
            type="datetime-local"
            value={filters.occurredFrom}
            onChange={(event) => setFilters((current) => ({ ...current, occurredFrom: event.target.value }))}
          />
        </div>
        <div className={styles.field}>
          <label htmlFor="audit-occurred-to">До, серверное время</label>
          <input
            id="audit-occurred-to"
            type="datetime-local"
            value={filters.occurredTo}
            onChange={(event) => setFilters((current) => ({ ...current, occurredTo: event.target.value }))}
          />
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
        <Message tone="error" title="Аудит недоступен">
          {loadError}
        </Message>
      ) : null}
      {!selectedTournamentId ? (
        <Message tone="info" title="Выберите турнир">
          История и экспорт загружаются только для выбранного турнира.
        </Message>
      ) : null}
      {selectedTournamentId && loadState === "loading" && !currentPage ? (
        <Message tone="loading" title="Загружаем аудит">
          Читаем redacted projection с сервера.
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
              const lineage = event.redacted_payload.previous_revision_id;
              return (
                <li key={event.audit_event_id}>
                  <article className={styles.event}>
                    <header className={styles.eventHeader}>
                      <div>
                        <strong>{eventTypeLabel(event.event_type)}</strong>
                        <code>{event.event_type}</code>
                      </div>
                      <Status tone={event.is_current ? "success" : "warning"}>
                        {event.is_current ? "Текущая revision" : "Заменена"}
                      </Status>
                    </header>
                    <dl className={styles.eventFacts}>
                      <div>
                        <dt>Server time</dt>
                        <dd>{formatDateTime(event.occurred_at)} UTC</dd>
                      </div>
                      <div>
                        <dt>Entity</dt>
                        <dd><code>{event.entity_kind}: {event.entity_id}</code></dd>
                      </div>
                      <div>
                        <dt>Actor</dt>
                        <dd>{actorLabel(event)}</dd>
                      </div>
                      <div>
                        <dt>Результат</dt>
                        <dd>{event.result_state} / {event.result_reason}</dd>
                      </div>
                      <div>
                        <dt>Official revision</dt>
                        <dd><code>{event.official_result_revision_id}</code></dd>
                      </div>
                      <div>
                        <dt>Номер revision</dt>
                        <dd>{event.revision_number}</dd>
                      </div>
                      {lineage ? (
                        <div className={styles.lineage}>
                          <dt>Предыдущая revision</dt>
                          <dd><code>{lineage}</code></dd>
                        </div>
                      ) : null}
                    </dl>
                    {payloadEntries.length ? (
                      <details className={styles.payload}>
                        <summary>Redacted payload</summary>
                        <dl>
                          {payloadEntries.map(([key, value]) => (
                            <div key={key}>
                              <dt>{payloadLabel(key)}</dt>
                              <dd><code>{String(value)}</code></dd>
                            </div>
                          ))}
                        </dl>
                      </details>
                    ) : null}
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
