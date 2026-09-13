import {
  advancePlayerSessionEpoch,
  clearPlayerCSRFTokens,
  isCurrentPlayerSessionEpoch,
  publicClient,
  unwrapApi,
  unwrapApiVoid,
} from "./client";
import {
  assertApiResponse,
  isCurrentPlayerResponse,
  isJoinPlayerResponse,
} from "./guards";
import type { components } from "./schema";

export type JoinPlayerResponse = components["schemas"]["JoinPlayerResponse"];
export type CurrentPlayerResponse = components["schemas"]["CurrentPlayerResponse"];

export const playerApi = {
  async join(username: string, signal?: AbortSignal): Promise<JoinPlayerResponse> {
    const sessionEpoch = advancePlayerSessionEpoch();
    clearPlayerCSRFTokens();
    try {
      const data = await unwrapApi(
        await publicClient.POST("/api/v1/players/join", {
          body: { username },
          signal,
        }),
      );
      return assertApiResponse(data, isJoinPlayerResponse, "players/join");
    } catch (error) {
      if (isCurrentPlayerSessionEpoch(sessionEpoch)) {
        clearPlayerCSRFTokens();
      }
      throw error;
    }
  },

  async me(signal?: AbortSignal): Promise<CurrentPlayerResponse> {
    const data = await unwrapApi(
      await publicClient.GET("/api/v1/players/me", {
        signal,
      }),
    );
    return assertApiResponse(data, isCurrentPlayerResponse, "players/me");
  },

  async logout(signal?: AbortSignal): Promise<void> {
    const logoutEpoch = advancePlayerSessionEpoch();
    try {
      await unwrapApiVoid(
        await publicClient.POST("/api/v1/players/logout", {
          signal,
        }),
      );
    } finally {
      if (isCurrentPlayerSessionEpoch(logoutEpoch)) {
        clearPlayerCSRFTokens();
      }
    }
  },
};
