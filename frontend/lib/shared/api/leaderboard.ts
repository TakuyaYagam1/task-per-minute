import { publicClient, unwrapApi } from "./client";
import { assertApiResponse, isLeaderboardResponse } from "./guards";
import type { components } from "./schema";

export type LeaderboardResponse = components["schemas"]["LeaderboardResponse"];

export const leaderboardApi = {
  async top50(signal?: AbortSignal): Promise<LeaderboardResponse> {
    const data = await unwrapApi(
      await publicClient.GET("/api/v1/leaderboard", { signal }),
    );
    return assertApiResponse(data, isLeaderboardResponse, "leaderboard");
  },
};
