import {
  toPublicTournamentCatalogItemView,
  type PublicTournamentCatalogItemView,
} from "../../entities/tournament";
import type {
  PublicTournamentCatalogPage,
  PublicTournamentCatalogQuery,
  TournamentCatalogFilterGroup,
  TournamentCatalogSort,
} from "../../shared/api";

export const CATALOG_PAGE_SIZE = 20;

export type CatalogQuery = Readonly<{
  q: string;
  group: TournamentCatalogFilterGroup;
  sort: TournamentCatalogSort;
  cursor: string | null;
  limit: number;
}>;

export const DEFAULT_CATALOG_QUERY: CatalogQuery = {
  q: "",
  group: "all",
  sort: "activity",
  cursor: null,
  limit: CATALOG_PAGE_SIZE,
};

const CATALOG_GROUPS: readonly TournamentCatalogFilterGroup[] = [
  "all",
  "live",
  "upcoming",
  "completed",
];
const CATALOG_SORTS: readonly TournamentCatalogSort[] = [
  "activity",
  "name",
  "newest",
];
const CURSOR_PATTERN = /^[A-Za-z0-9_-]+$/;

const valueIn = <T extends string>(value: string | null, values: readonly T[]): value is T =>
  value !== null && values.includes(value as T);

export const catalogQueryFromSearch = (search: string): CatalogQuery => {
  const params = new URLSearchParams(search);
  const q = params.get("q")?.trim().slice(0, 80) ?? "";
  const group = valueIn(params.get("group"), CATALOG_GROUPS)
    ? params.get("group") as TournamentCatalogFilterGroup
    : DEFAULT_CATALOG_QUERY.group;
  const sort = valueIn(params.get("sort"), CATALOG_SORTS)
    ? params.get("sort") as TournamentCatalogSort
    : DEFAULT_CATALOG_QUERY.sort;
  const rawCursor = params.get("cursor");
  const cursor = rawCursor && rawCursor.length <= 2048 && CURSOR_PATTERN.test(rawCursor)
    ? rawCursor
    : null;
  return {
    q,
    group,
    sort,
    cursor,
    limit: CATALOG_PAGE_SIZE,
  };
};

export const catalogSearchFromQuery = (query: CatalogQuery): string => {
  const params = new URLSearchParams();
  if (query.q) {
    params.set("q", query.q);
  }
  if (query.group !== DEFAULT_CATALOG_QUERY.group) {
    params.set("group", query.group);
  }
  if (query.sort !== DEFAULT_CATALOG_QUERY.sort) {
    params.set("sort", query.sort);
  }
  if (query.cursor) {
    params.set("cursor", query.cursor);
  }
  const search = params.toString();
  return search ? `?${search}` : "";
};

export const catalogQueryKey = (query: CatalogQuery): string =>
  JSON.stringify([query.q, query.group, query.sort, query.cursor, query.limit]);

export const toCatalogApiQuery = (
  query: CatalogQuery,
): PublicTournamentCatalogQuery => ({
  q: query.q || undefined,
  group: query.group,
  sort: query.sort,
  limit: query.limit,
  cursor: query.cursor ?? undefined,
});

export type CatalogLoadStatus =
  | "idle"
  | "loading"
  | "ready"
  | "empty"
  | "stale"
  | "error"
  | "rate_limited";

export type CatalogLoadState = Readonly<{
  status: CatalogLoadStatus;
  items: readonly PublicTournamentCatalogItemView[];
  nextCursor: string | null;
  error: string | null;
}>;

export const initialCatalogLoadState: CatalogLoadState = {
  status: "idle",
  items: [],
  nextCursor: null,
  error: null,
};

export type CatalogPage = Readonly<{
  items: readonly PublicTournamentCatalogItemView[];
  nextCursor: string | null;
}>;

export const toCatalogPage = (page: PublicTournamentCatalogPage): CatalogPage => ({
  items: page.items.map(toPublicTournamentCatalogItemView),
  nextCursor: page.nextCursor,
});
