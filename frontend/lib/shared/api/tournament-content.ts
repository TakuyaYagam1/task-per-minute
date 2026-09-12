import { adminClient, unwrapApi } from "./client";
import { assertApiResponse, isTournamentContentSelection } from "./guards";
import type { components } from "./schema";

export type TournamentContentSelection = components["schemas"]["TournamentContentSelection"];

export const getTournamentContent = async (
  signal?: AbortSignal,
): Promise<TournamentContentSelection> => {
  const data = await unwrapApi(
    await adminClient.GET("/api/v1/admin/tournament-content", { signal }),
  );
  return assertApiResponse(data, isTournamentContentSelection, "admin/tournament-content");
};
