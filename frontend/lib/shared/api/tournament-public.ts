import { CONFIG } from "../config";
import { publicClient, unwrapApi, type ApiResult } from "./client";
import {
  ApiContractError,
  assertApiResponse,
  isPublicBracketResponse,
  isPublicScoreboardResponse,
  isPublicTournamentResponse,
} from "./guards";
import type { components } from "./schema";

export type PublicTournamentResponse = components["schemas"]["PublicTournamentResponse"];
export type PublicScoreboardResponse = components["schemas"]["PublicScoreboardResponse"];
export type PublicBracketResponse = components["schemas"]["PublicBracketResponse"];

export type PublicTournamentProjection = Readonly<{
  tournament: PublicTournamentResponse;
  scoreboard: PublicScoreboardResponse;
  bracket: PublicBracketResponse;
  projectionRevision: number;
}>;

export const openPublicTournamentEvents = (): EventSource => {
  const baseUrl = CONFIG.apiUrl || window.location.origin;
  const url = new URL("/api/v1/arena/events", baseUrl);
  return new EventSource(url, { withCredentials: true });
};

const readPublicResponse = async <T>(
  result: ApiResult<T> | Promise<ApiResult<T>>,
  guard: (value: unknown) => value is T,
  contract: string,
): Promise<T> => {
  const data = await unwrapApi(result, contract);
  return assertApiResponse(data, guard, contract);
};

const publicTournamentPath = (tournamentId: string): { tournament_id: string } => ({
  tournament_id: tournamentId,
});

export const getPublicTournament = async (
  tournamentId: string,
  signal?: AbortSignal,
): Promise<PublicTournamentResponse> =>
  readPublicResponse(
    publicClient.GET("/api/v1/tournaments/{tournament_id}", {
      params: { path: publicTournamentPath(tournamentId) },
      signal,
    }),
    isPublicTournamentResponse,
    "public tournament",
  );

export const getPublicScoreboard = async (
  tournamentId: string,
  signal?: AbortSignal,
): Promise<PublicScoreboardResponse> =>
  readPublicResponse(
    publicClient.GET("/api/v1/tournaments/{tournament_id}/scoreboard", {
      params: { path: publicTournamentPath(tournamentId) },
      signal,
    }),
    isPublicScoreboardResponse,
    "public scoreboard",
  );

export const getPublicBracket = async (
  tournamentId: string,
  signal?: AbortSignal,
): Promise<PublicBracketResponse> =>
  readPublicResponse(
    publicClient.GET("/api/v1/tournaments/{tournament_id}/bracket", {
      params: { path: publicTournamentPath(tournamentId) },
      signal,
    }),
    isPublicBracketResponse,
    "public bracket",
  );

/** Read and validate the three public projections before replacing a view state. */
export const getPublicTournamentProjection = async (
  tournamentId: string,
  signal?: AbortSignal,
): Promise<PublicTournamentProjection> => {
  const [tournament, scoreboard, bracket] = await Promise.all([
    getPublicTournament(tournamentId, signal),
    getPublicScoreboard(tournamentId, signal),
    getPublicBracket(tournamentId, signal),
  ]);

  if (
    scoreboard.tournament_id !== tournament.tournament_id ||
    bracket.tournament_id !== tournament.tournament_id ||
    scoreboard.projection_revision !== tournament.projection_revision ||
    bracket.projection_revision !== tournament.projection_revision
  ) {
    throw new ApiContractError("public tournament projection");
  }

  return {
    tournament,
    scoreboard,
    bracket,
    projectionRevision: tournament.projection_revision,
  };
};

export const publicTournamentApi = {
  getPublicTournament,
  getPublicScoreboard,
  getPublicBracket,
  getPublicTournamentProjection,
} as const;

export const tournamentPublicApi = publicTournamentApi;
