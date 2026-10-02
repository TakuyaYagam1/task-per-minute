import { anonymousClient, publicClient, unwrapApi } from "./client";
import { ApiContractError, assertApiResponse, isLeaderboardResponse } from "./guards";
import type { components } from "./schema";

export type LeaderboardWinsFilter = components["schemas"]["LeaderboardWinsFilter"];
export type LeaderboardAvatarMetadata = components["schemas"]["LeaderboardAvatar"];
export type LeaderboardAvatarContentType = LeaderboardAvatarMetadata["content_type"];
export type LeaderboardEntry = components["schemas"]["LeaderboardEntry"];
export type LeaderboardResponse = components["schemas"]["LeaderboardResponse"];

export type LeaderboardQuery = {
  search: string;
  wins: LeaderboardWinsFilter;
  page: number;
};

const MAX_AVATAR_BYTES = 5 * 1024 * 1024;

const normalizeContentType = (value: string | null): string | null =>
  value?.split(";", 1)[0]?.trim().toLowerCase() ?? null;

export const leaderboardApi = {
  async page(query: LeaderboardQuery, signal?: AbortSignal): Promise<LeaderboardResponse> {
    const data = await unwrapApi(
      await publicClient.GET("/api/v1/leaderboard", {
        params: {
          query: {
            search: query.search,
            wins: query.wins,
            page: query.page,
            per_page: 25,
          },
        },
        signal,
      }),
    );
    return assertApiResponse(data, isLeaderboardResponse, "leaderboard");
  },

  async getAvatar(
    avatar: LeaderboardAvatarMetadata,
    signal?: AbortSignal,
  ): Promise<Blob | null> {
    const result = await anonymousClient.GET("/api/v1/players/{player_id}/avatar", {
      params: {
        path: { player_id: avatar.player_id },
        query: { v: avatar.version },
      },
      parseAs: "blob",
      cache: "no-store",
      signal,
    });
    if (result.response.status === 404) {
      return null;
    }

    const blob = await unwrapApi(result, "players/avatar");
    const responseContentType = normalizeContentType(result.response.headers.get("Content-Type"));
    const blobContentType = normalizeContentType(blob.type);
    if (
      responseContentType !== avatar.content_type ||
      blobContentType !== avatar.content_type ||
      blob.size > MAX_AVATAR_BYTES
    ) {
      throw new ApiContractError("players/avatar media");
    }
    return blob;
  },
};
