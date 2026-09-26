"use client";

import Link from "next/link";

import {
  catalogCreatedLabel,
  catalogFormatLabel,
  catalogGroupLabel,
  catalogRosterLabel,
  catalogScheduleLabel,
  catalogStageLabel,
  catalogStateLabel,
  type PublicTournamentCatalogItemView,
} from "../../entities/tournament";
import { Button, Message, Status } from "../../shared/ui";
import {
  CATALOG_PAGE_SIZE,
  type CatalogQuery,
  type TournamentCatalogState,
} from "../../features/tournament-catalog";
import type {
  TournamentCatalogFilterGroup,
  TournamentCatalogSort,
} from "../../shared/api";
import styles from "./TournamentCatalog.module.css";

const GROUP_OPTIONS: readonly Readonly<{
  value: TournamentCatalogFilterGroup;
  label: string;
}>[] = [
  { value: "all", label: "Все" },
  { value: "live", label: "Идут" },
  { value: "upcoming", label: "Предстоящие" },
  { value: "completed", label: "Завершенные" },
];

const SORT_OPTIONS: readonly Readonly<{
  value: TournamentCatalogSort;
  label: string;
}>[] = [
  { value: "activity", label: "По активности" },
  { value: "name", label: "По имени" },
  { value: "newest", label: "Сначала новые" },
];

type TournamentCatalogProps = Readonly<{
  query: CatalogQuery;
  state: TournamentCatalogState;
  buildTournamentHref: (item: PublicTournamentCatalogItemView) => string;
  onQueryChange: (query: CatalogQuery) => void;
}>;

const statusTone = (
  group: PublicTournamentCatalogItemView["group"],
): "live" | "info" | "success" => {
  switch (group) {
    case "live":
      return "live";
    case "upcoming":
      return "info";
    case "completed":
      return "success";
  }
};

const TournamentCard = ({
  item,
  href,
}: Readonly<{
  item: PublicTournamentCatalogItemView;
  href: string;
}>) => (
  <li>
    <Link className={styles.card} href={href}>
      <div className={styles.cardHeader}>
        <div className={styles.cardTitleBlock}>
          <h2 className={styles.cardTitle}>{item.name}</h2>
        </div>
        <Status size="small" tone={statusTone(item.group)}>
          {catalogGroupLabel(item.group)}
        </Status>
      </div>
      <dl className={styles.cardFacts}>
        <div>
          <dt>Этап</dt>
          <dd>{catalogStageLabel(item.stage)}</dd>
        </div>
        <div>
          <dt>Состояние</dt>
          <dd>{catalogStateLabel(item.state)}</dd>
        </div>
        <div>
          <dt>Участники</dt>
          <dd>{catalogRosterLabel(item)}</dd>
        </div>
        <div>
          <dt>Формат</dt>
          <dd>{catalogFormatLabel(item.preset)}</dd>
        </div>
      </dl>
      <div className={styles.cardDates}>
        <time dateTime={item.scheduledAt ?? item.startedAt ?? item.finishedAt ?? item.createdAt}>
          {catalogScheduleLabel(item)}
        </time>
        <span>{catalogCreatedLabel(item.createdAt)}</span>
      </div>
      <span className={styles.cardAction}>Открыть обзор</span>
    </Link>
  </li>
);

export const TournamentCatalog = ({
  buildTournamentHref,
  onQueryChange,
  query,
  state,
}: TournamentCatalogProps) => {
  const isInitialLoading = state.status === "loading" && state.items.length === 0;
  const hasItems = state.items.length > 0;

  return (
    <section className={styles.catalog} aria-labelledby="arena-catalog-title">
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>Публичный каталог</p>
          <h1 className={styles.title} id="arena-catalog-title">Турниры</h1>
          <p className={styles.lead}>
            Выберите соревнование для просмотра. Публичный просмотр доступен без входа.
          </p>
        </div>
        <span className={styles.pageSize}>До {CATALOG_PAGE_SIZE} турниров на странице</span>
      </header>

      <form
        className={styles.filters}
        onSubmit={(event) => {
          event.preventDefault();
        }}
        role="search"
        aria-label="Поиск турниров"
      >
        <div className={styles.searchField}>
          <label htmlFor="tournament-catalog-search">Поиск</label>
          <input
            id="tournament-catalog-search"
            type="search"
            value={query.q}
            maxLength={80}
            placeholder="Поиск по названию"
            onChange={(event) => onQueryChange({ ...query, q: event.target.value, cursor: null })}
          />
        </div>
        <div className={styles.selectField}>
          <label htmlFor="tournament-catalog-group">Состояние</label>
          <select
            id="tournament-catalog-group"
            value={query.group}
            onChange={(event) => onQueryChange({
              ...query,
              group: event.target.value as TournamentCatalogFilterGroup,
              cursor: null,
            })}
          >
            {GROUP_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>{option.label}</option>
            ))}
          </select>
        </div>
        <div className={styles.selectField}>
          <label htmlFor="tournament-catalog-sort">Сортировка</label>
          <select
            id="tournament-catalog-sort"
            value={query.sort}
            onChange={(event) => onQueryChange({
              ...query,
              sort: event.target.value as TournamentCatalogSort,
              cursor: null,
            })}
          >
            {SORT_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>{option.label}</option>
            ))}
          </select>
        </div>
      </form>

      {isInitialLoading && (
        <Message tone="loading" title="Загрузка каталога">
          <p>Получаем публичный список турниров.</p>
        </Message>
      )}

      {state.error && (
        <Message
          tone={state.status === "stale" ? "warning" : "error"}
          title={state.status === "stale" ? "Показаны последние данные" : "Каталог недоступен"}
        >
          <p>{state.error}</p>
          <Button type="button" size="small" variant="secondary" onClick={state.retry}>
            Повторить
          </Button>
        </Message>
      )}

      {state.status === "empty" && (
        <Message tone="empty" title="Турниры не найдены">
          <p>Измените поиск или фильтры.</p>
        </Message>
      )}

      {hasItems && (
        <div className={styles.results} aria-busy={state.status === "loading"}>
          <div className={styles.resultsHeader}>
            <h2 className={styles.resultsTitle}>Найденные турниры</h2>
            <span>{state.items.length}</span>
          </div>
          <ul className={styles.grid}>
            {state.items.map((item) => (
              <TournamentCard
                href={buildTournamentHref(item)}
                item={item}
                key={item.publicId}
              />
            ))}
          </ul>
          {state.nextCursor && (
            <div className={styles.pagination}>
              <Button
                type="button"
                variant="secondary"
                loading={state.status === "loading"}
                loadingLabel="Загрузка"
                onClick={() => onQueryChange({ ...query, cursor: state.nextCursor })}
              >
                Следующая страница
              </Button>
            </div>
          )}
        </div>
      )}
    </section>
  );
};

TournamentCatalog.displayName = "TournamentCatalog";
