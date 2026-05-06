import { publicClient, unwrapApi } from "./client";
import { assertApiResponse, isJoinResponse, isPlayerMeResponse } from "./guards";
import type { components } from "./schema";

export type JoinResponse = components["schemas"]["JoinResponse"];
export type PlayerMeResponse = components["schemas"]["PlayerMeResponse"];

export const playerApi = {
  async join(username: string, signal?: AbortSignal): Promise<JoinResponse> {
    const data = await unwrapApi(
      await publicClient.POST("/api/v1/players/join", {
        body: { username },
        signal,
      }),
    );
    return assertApiResponse(data, isJoinResponse, "players/join");
  },

  async me(sessionToken: string, signal?: AbortSignal): Promise<PlayerMeResponse> {
    const data = await unwrapApi(
      await publicClient.GET("/api/v1/players/me", {
        headers: { "X-Session-Token": sessionToken },
        signal,
      }),
    );
    return assertApiResponse(data, isPlayerMeResponse, "players/me");
  },
};
