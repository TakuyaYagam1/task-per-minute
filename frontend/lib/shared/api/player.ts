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
  isPlayerAccountAcceptedResponse,
} from "./guards";
import type { components } from "./schema";

export type JoinPlayerResponse = components["schemas"]["JoinPlayerResponse"];
export type CurrentPlayerResponse = components["schemas"]["CurrentPlayerResponse"];
export type PlayerAccountAcceptedResponse = components["schemas"]["PlayerAccountAcceptedResponse"];

export const playerApi = {
  async register(
    username: string,
    email: string,
    password: string,
    signal?: AbortSignal,
  ): Promise<PlayerAccountAcceptedResponse> {
    const data = await unwrapApi(
      await publicClient.POST("/api/v1/players/register", {
        body: { username, email, password },
        signal,
      }),
      "players/register",
    );
    return assertApiResponse(data, isPlayerAccountAcceptedResponse, "players/register");
  },

  async login(login: string, password: string, signal?: AbortSignal): Promise<JoinPlayerResponse> {
    advancePlayerSessionEpoch();
    const data = await unwrapApi(
      await publicClient.POST("/api/v1/players/login", {
        body: { login, password },
        signal,
      }),
      "players/login",
    );
    return assertApiResponse(data, isJoinPlayerResponse, "players/login");
  },

  async verifyEmail(token: string, signal?: AbortSignal): Promise<void> {
    await unwrapApiVoid(
      await publicClient.POST("/api/v1/players/verify-email", {
        body: { token },
        signal,
      }),
      "players/verify-email",
    );
  },

  async resendVerification(email: string, signal?: AbortSignal): Promise<PlayerAccountAcceptedResponse> {
    const data = await unwrapApi(
      await publicClient.POST("/api/v1/players/resend-verification", {
        body: { email },
        signal,
      }),
      "players/resend-verification",
    );
    return assertApiResponse(data, isPlayerAccountAcceptedResponse, "players/resend-verification");
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
