import {
  advancePlayerSessionEpoch,
  clearPlayerCSRFTokens,
  credentialedFetch,
  isCurrentPlayerSessionEpoch,
  publicClient,
  ApiError,
  unwrapApi,
  unwrapApiVoid,
} from "./client";
import {
  assertApiResponse,
  isCurrentPlayerResponse,
  isPlayerAccountEmailChangeConfirmedResponse,
  isPlayerAccountSettingsResponse,
  isJoinPlayerResponse,
  isPlayerResponse,
  isPlayerAccountAcceptedResponse,
  ApiContractError,
} from "./guards";
import type { components } from "./schema";

export type JoinPlayerResponse = components["schemas"]["JoinPlayerResponse"];
export type CurrentPlayerResponse = components["schemas"]["CurrentPlayerResponse"];
export type PlayerAccountAcceptedResponse = components["schemas"]["PlayerAccountAcceptedResponse"];
export type PlayerAccountSettingsResponse = components["schemas"]["PlayerAccountSettingsResponse"];
export type PlayerAccountEmailChangeConfirmedResponse = components["schemas"]["PlayerAccountEmailChangeConfirmedResponse"];
export type PlayerResponse = components["schemas"]["PlayerResponse"];

const requiredPlayerCSRFHeader = { "X-CSRF-Token": "" } as const;

const playerProblemFromResponse = async (response: Response): Promise<components["schemas"]["ProblemDetails"] | undefined> => {
  const body: unknown = await response.clone().json().catch(() => null);
  if (
    body &&
    typeof body === "object" &&
    !Array.isArray(body) &&
    typeof (body as { status?: unknown }).status === "number" &&
    typeof (body as { title?: unknown }).title === "string"
  ) {
    return body as components["schemas"]["ProblemDetails"];
  }
  return undefined;
};

const requireAvatarResponse = async (response: Response, contract: string): Promise<void> => {
  if (!response.ok) {
    throw new ApiError(response, await playerProblemFromResponse(response));
  }
  if (response.status !== 204) {
    throw new ApiContractError(contract);
  }
};

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

  async resendLoginVerification(
    login: string,
    password: string,
    signal?: AbortSignal,
  ): Promise<PlayerAccountAcceptedResponse> {
    const data = await unwrapApi(
      await publicClient.POST("/api/v1/players/login/resend-verification", {
        body: { login, password },
        signal,
      }),
      "players/login/resend-verification",
    );
    return assertApiResponse(
      data,
      isPlayerAccountAcceptedResponse,
      "players/login/resend-verification",
    );
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

  async getAccountSettings(signal?: AbortSignal): Promise<PlayerAccountSettingsResponse> {
    const data = await unwrapApi(
      await publicClient.GET("/api/v1/players/account", { signal }),
      "players/account",
    );
    return assertApiResponse(data, isPlayerAccountSettingsResponse, "players/account");
  },

  async changeUsername(
    currentPassword: string,
    username: string,
    signal?: AbortSignal,
  ): Promise<PlayerResponse> {
    const data = await unwrapApi(
      await publicClient.POST("/api/v1/players/account/username", {
        params: { header: requiredPlayerCSRFHeader },
        body: { current_password: currentPassword, username },
        signal,
      }),
      "players/account/username",
    );
    return assertApiResponse(data, isPlayerResponse, "players/account/username");
  },

  async changePassword(
    currentPassword: string,
    newPassword: string,
    signal?: AbortSignal,
  ): Promise<void> {
    await unwrapApiVoid(
      await publicClient.POST("/api/v1/players/account/password", {
        params: { header: requiredPlayerCSRFHeader },
        body: { current_password: currentPassword, new_password: newPassword },
        signal,
      }),
      "players/account/password",
    );
  },

  async beginEmailChange(
    currentPassword: string,
    newEmail: string,
    signal?: AbortSignal,
  ): Promise<PlayerAccountSettingsResponse> {
    const data = await unwrapApi(
      await publicClient.POST("/api/v1/players/account/email", {
        params: { header: requiredPlayerCSRFHeader },
        body: { current_password: currentPassword, new_email: newEmail },
        signal,
      }),
      "players/account/email",
    );
    return assertApiResponse(data, isPlayerAccountSettingsResponse, "players/account/email");
  },

  async resendEmailChange(signal?: AbortSignal): Promise<PlayerAccountSettingsResponse> {
    const data = await unwrapApi(
      await publicClient.POST("/api/v1/players/account/email/resend", {
        params: { header: requiredPlayerCSRFHeader },
        signal,
      }),
      "players/account/email/resend",
    );
    return assertApiResponse(data, isPlayerAccountSettingsResponse, "players/account/email/resend");
  },

  async cancelEmailChange(signal?: AbortSignal): Promise<PlayerAccountSettingsResponse> {
    const data = await unwrapApi(
      await publicClient.DELETE("/api/v1/players/account/email", {
        params: { header: requiredPlayerCSRFHeader },
        signal,
      }),
      "players/account/email",
    );
    return assertApiResponse(data, isPlayerAccountSettingsResponse, "players/account/email");
  },

  async confirmEmailChange(
    code: string,
    signal?: AbortSignal,
  ): Promise<PlayerAccountEmailChangeConfirmedResponse> {
    const data = await unwrapApi(
      await publicClient.POST("/api/v1/players/account/email/confirm", {
        params: { header: requiredPlayerCSRFHeader },
        body: { code },
        signal,
      }),
      "players/account/email/confirm",
    );
    return assertApiResponse(
      data,
      isPlayerAccountEmailChangeConfirmedResponse,
      "players/account/email/confirm",
    );
  },

  async getAvatar(signal?: AbortSignal): Promise<Blob | null> {
    const response = await credentialedFetch("/api/v1/players/account/avatar", {
      method: "GET",
      signal,
    });
    if (response.status === 404) {
      return null;
    }
    if (!response.ok) {
      throw new ApiError(response, await playerProblemFromResponse(response));
    }
    const contentType = response.headers.get("Content-Type")?.split(";", 1)[0]?.trim().toLowerCase();
    if (
      contentType !== "image/jpeg" &&
      contentType !== "image/png" &&
      contentType !== "image/gif" &&
      contentType !== "video/mp4"
    ) {
      throw new ApiContractError("players/account/avatar media type");
    }
    return response.blob();
  },

  async replaceAvatar(file: File, signal?: AbortSignal): Promise<void> {
    const body = new FormData();
    body.append("file", file, file.name);
    const response = await credentialedFetch("/api/v1/players/account/avatar", {
      method: "PUT",
      body,
      signal,
    });
    await requireAvatarResponse(response, "players/account/avatar");
  },

  async deleteAvatar(signal?: AbortSignal): Promise<void> {
    const response = await credentialedFetch("/api/v1/players/account/avatar", {
      method: "DELETE",
      signal,
    });
    await requireAvatarResponse(response, "players/account/avatar");
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
