import {
  publicClient,
  unwrapApi,
  type ApiResult,
} from "./client";
import {
  ApiContractError,
  assertApiResponse,
  isPublicTournamentCatalogItem,
  isPublicTournamentCatalogResponse,
} from "./guards";
import type { components, paths } from "./schema";

export type TournamentCatalogFilterGroup =
  components["schemas"]["TournamentCatalogFilterGroup"];
export type TournamentCatalogSort = components["schemas"]["TournamentCatalogSort"];
export type PublicTournamentCatalogItem =
  components["schemas"]["PublicTournamentCatalogItem"];
export type PublicTournamentCatalogResponse =
  components["schemas"]["PublicTournamentCatalogResponse"];

export type PublicTournamentCatalogQuery = Readonly<{
  q?: string;
  group?: TournamentCatalogFilterGroup;
  sort?: TournamentCatalogSort;
  limit?: number;
  cursor?: string;
}>;

export type PublicTournamentCatalogPage = Readonly<{
  items: readonly PublicTournamentCatalogItem[];
  nextCursor: string | null;
}>;

type CatalogQueryParams = NonNullable<
  paths["/api/v1/public/tournaments"]["get"]["parameters"]["query"]
>;

const readPublicCatalogResponse = async <T>(
  result: ApiResult<T> | Promise<ApiResult<T>>,
  guard: (value: unknown) => value is T,
  contract: string,
): Promise<T> => {
  const data = await unwrapApi(result, contract);
  return assertApiResponse(data, guard, contract);
};

const catalogQueryParams = (
  query: PublicTournamentCatalogQuery,
): CatalogQueryParams => {
  const params: CatalogQueryParams = {
    group: query.group ?? "all",
    sort: query.sort ?? "activity",
    limit: query.limit ?? 20,
  };
  const search = query.q?.trim();
  if (search) {
    params.q = search;
  }
  if (query.cursor) {
    params.cursor = query.cursor;
  }
  return params;
};

export const listPublicTournaments = async (
  query: PublicTournamentCatalogQuery = {},
  signal?: AbortSignal,
): Promise<PublicTournamentCatalogPage> => {
  const response = await readPublicCatalogResponse(
    publicClient.GET("/api/v1/public/tournaments", {
      params: { query: catalogQueryParams(query) },
      signal,
    }),
    isPublicTournamentCatalogResponse,
    "public tournament catalog",
  );
  return {
    items: response.items,
    nextCursor: response.next_cursor,
  };
};

export const getPublicTournamentByPublicId = async (
  publicId: string,
  signal?: AbortSignal,
): Promise<PublicTournamentCatalogItem> => {
  const response = await readPublicCatalogResponse(
    publicClient.GET("/api/v1/public/tournaments/{public_id}", {
      params: { path: { public_id: publicId } },
      signal,
    }),
    isPublicTournamentCatalogItem,
    "public tournament catalog item",
  );
  if (response.public_id !== publicId) {
    throw new ApiContractError("public tournament catalog item identity");
  }
  return response;
};

export const publicTournamentCatalogApi = {
  listPublicTournaments,
  getPublicTournamentByPublicId,
} as const;

export const tournamentCatalogApi = publicTournamentCatalogApi;
