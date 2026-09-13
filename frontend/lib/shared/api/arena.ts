import type { Client } from "openapi-fetch";

import {
  adminClient,
  unwrapApi,
  unwrapApiVoid,
  publicClient,
  type ApiResult,
} from "./client";
import type { paths } from "./schema";

/**
 * The Arena API has one generated contract and three authorization scopes.
 * Endpoint methods stay with the feature that owns the operation; this module
 * only exposes the typed transports and common response boundary.
 */
export type ArenaClient = Client<paths>;
export type ArenaRole = "public" | "participant" | "operator";

export const arenaClients: Readonly<Record<ArenaRole, ArenaClient>> = {
  public: publicClient,
  participant: publicClient,
  operator: adminClient,
};

export const readArenaResponse = <T>(
  result: ApiResult<T> | Promise<ApiResult<T>>,
  contract: string,
): Promise<T> => unwrapApi(result, contract);

export const readArenaVoidResponse = (
  result: ApiResult<unknown> | Promise<ApiResult<unknown>>,
  contract: string,
): Promise<void> => unwrapApiVoid(result, contract);

export const arenaApi = {
  clients: arenaClients,
  public: publicClient,
  participant: publicClient,
  operator: adminClient,
  read: readArenaResponse,
  readVoid: readArenaVoidResponse,
} as const;
