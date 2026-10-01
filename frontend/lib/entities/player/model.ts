import { Player } from "../../shared/types";
import {
  beginPlayerSessionRotation,
  finishPlayerSessionRotation,
  ApiError,
  getPlayerSessionEpoch,
  isCurrentPlayerSessionEpoch,
  playerApi,
} from "../../shared/api";
import { ApiContractError } from "../../shared/api/guards";
import type {
  PlayerAccountEmailChangeConfirmedResponse,
  PlayerAccountSettingsResponse,
} from "../../shared/api/player";
import { playerStorage } from "../../shared/lib/storage";

interface PlayerSessionState {
  player: Player;
}

export type PlayerRegistrationResult =
  | { kind: "accepted" }
  | { kind: "username_taken" }
  | { kind: "rate_limited"; retryAfter?: string | null }
  | { kind: "unavailable" }
  | { kind: "aborted" }
  | { kind: "error" };

export type PlayerLoginResult =
  | { kind: "ok"; player: Player }
  | { kind: "invalid_credentials" }
  | { kind: "email_unverified" }
  | { kind: "rate_limited"; retryAfter?: string | null }
  | { kind: "unavailable" }
  | { kind: "aborted" }
  | { kind: "error" };

export type PlayerLoginResendVerificationResult =
  | { kind: "sent" }
  | { kind: "invalid_credentials" }
  | { kind: "already_verified" }
  | { kind: "rate_limited"; retryAfter?: string | null }
  | { kind: "unavailable"; retryAfter?: string | null }
  | { kind: "aborted" }
  | { kind: "error" };

export type PlayerVerificationResult =
  | { kind: "ok" }
  | { kind: "invalid" }
  | { kind: "rate_limited"; retryAfter?: string | null }
  | { kind: "aborted" }
  | { kind: "error" };

export type PlayerResendVerificationResult =
  | { kind: "accepted" }
  | { kind: "rate_limited"; retryAfter?: string | null }
  | { kind: "unavailable" }
  | { kind: "aborted" }
  | { kind: "error" };

export type RefreshPlayerResult =
  | { kind: "ok"; state: PlayerSessionState }
  | { kind: "expired" }
  | { kind: "contract" }
  | { kind: "aborted" }
  | { kind: "error" };

export type AccountSettingsResult =
  | { kind: "ok"; settings: PlayerAccountSettingsResponse }
  | { kind: "expired" }
  | { kind: "contract" }
  | { kind: "aborted" }
  | { kind: "error" };

export type AccountMutationResult =
  | { kind: "ok"; player?: Player }
  | { kind: "current_password_invalid" }
  | { kind: "password_invalid" }
  | { kind: "username_taken" }
  | { kind: "email_taken" }
  | { kind: "code_invalid" }
  | { kind: "rate_limited"; retryAfter?: string | null }
  | { kind: "unavailable" }
  | { kind: "expired" }
  | { kind: "aborted" }
  | { kind: "error" };

export type PlayerAvatarResult =
  | { kind: "ok"; avatar?: Blob | null }
  | { kind: "expired" }
  | { kind: "too_large" }
  | { kind: "unsupported" }
  | { kind: "invalid" }
  | { kind: "video_unavailable" }
  | { kind: "busy"; retryAfter?: string | null }
  | { kind: "aborted" }
  | { kind: "error" };

export type EmailConfirmationResult =
  | { kind: "ok"; confirmation: PlayerAccountEmailChangeConfirmedResponse }
  | Exclude<AccountMutationResult, { kind: "ok" }>;

export type PlayerAvatarChange = "refresh" | "clear";
type PlayerAvatarChangeListener = (change: PlayerAvatarChange) => void;

const playerAvatarChangeListeners = new Set<PlayerAvatarChangeListener>();

const notifyPlayerAvatarChange = (change: PlayerAvatarChange): void => {
  for (const listener of playerAvatarChangeListeners) {
    listener(change);
  }
};

const avatarError = (
  error: unknown,
  epoch: number,
  signal?: AbortSignal,
): Exclude<PlayerAvatarResult, { kind: "ok" }> => {
  if (isAbortError(error) || !sessionEpochStillCurrent(epoch, signal)) {
    return { kind: "aborted" };
  }
  if (error instanceof ApiError && error.status === 401) return { kind: "expired" };
  if (error instanceof ApiError && error.status === 413) return { kind: "too_large" };
  if (error instanceof ApiError && error.status === 415) return { kind: "unsupported" };
  if (error instanceof ApiError && error.status === 429) {
    return { kind: "busy", retryAfter: error.retryAfter };
  }
  if (apiCodeIs(error, "player.avatar_invalid")) return { kind: "invalid" };
  if (apiCodeIs(error, "player.avatar_video_unavailable")) return { kind: "video_unavailable" };
  return { kind: "error" };
};

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  error.name === "AbortError";

const apiCodeIs = (error: unknown, code: string): boolean =>
  error instanceof ApiError && error.problem?.code === code;

const sessionEpochStillCurrent = (epoch: number, signal?: AbortSignal): boolean =>
  !signal?.aborted && isCurrentPlayerSessionEpoch(epoch);

const accountMutationError = (
  error: unknown,
  epoch: number,
  signal?: AbortSignal,
): Exclude<AccountMutationResult, { kind: "ok" }> => {
  if (isAbortError(error) || !sessionEpochStillCurrent(epoch, signal)) {
    return { kind: "aborted" };
  }
  if (error instanceof ApiError && error.status === 401) {
    return { kind: "expired" };
  }
  if (apiCodeIs(error, "player.current_password_invalid")) {
    return { kind: "current_password_invalid" };
  }
  if (apiCodeIs(error, "player.username_taken")) {
    return { kind: "username_taken" };
  }
  if (apiCodeIs(error, "player_account.email_taken")) {
    return { kind: "email_taken" };
  }
  if (apiCodeIs(error, "player.email_change_code_invalid")) {
    return { kind: "code_invalid" };
  }
  if (error instanceof ApiError && error.status === 429) {
    return { kind: "rate_limited", retryAfter: error.retryAfter };
  }
  if (error instanceof ApiError && error.status === 503) {
    return { kind: "unavailable" };
  }
  return { kind: "error" };
};

export const playerModel = {
  subscribeAvatarChanges(listener: PlayerAvatarChangeListener): () => void {
    playerAvatarChangeListeners.add(listener);
    return () => playerAvatarChangeListeners.delete(listener);
  },

  async registerPlayer(
    username: string,
    email: string,
    password: string,
    signal?: AbortSignal,
  ): Promise<PlayerRegistrationResult> {
    try {
      await playerApi.register(username, email, password, signal);
      return { kind: "accepted" };
    } catch (error) {
      if (isAbortError(error)) {
        return { kind: "aborted" };
      }
      if (error instanceof ApiError && error.status === 429) {
        return { kind: "rate_limited", retryAfter: error.retryAfter };
      }
      if (error instanceof ApiError && error.status === 409) {
        return { kind: "username_taken" };
      }
      if (error instanceof ApiError && error.status === 503) {
        return { kind: "unavailable" };
      }
      return { kind: "error" };
    }
  },

  async loginPlayer(
    login: string,
    password: string,
    signal?: AbortSignal,
  ): Promise<PlayerLoginResult> {
    let sessionCreated = false;
    const attemptEpoch = getPlayerSessionEpoch() + 1;
    try {
      const identity = await playerApi.login(login, password, signal);
      sessionCreated = true;
      if (signal?.aborted || !isCurrentPlayerSessionEpoch(attemptEpoch)) {
        throw new DOMException("Player login was superseded", "AbortError");
      }
      const current = await playerApi.me(signal);
      if (signal?.aborted || !isCurrentPlayerSessionEpoch(attemptEpoch)) {
        throw new DOMException("Player login was superseded", "AbortError");
      }
      if (current.player.id !== identity.player_id) {
        throw new ApiContractError("players/login identity");
      }

      const player: Player = {
        id: current.player.id,
        username: current.player.username,
      };
      playerStorage.clearSession();
      playerStorage.setPlayerId(player.id);
      playerStorage.setUsername(player.username);
      notifyPlayerAvatarChange("refresh");
      return { kind: "ok", player };
    } catch (error) {
      if (
        (sessionCreated || error instanceof ApiContractError) &&
        isCurrentPlayerSessionEpoch(attemptEpoch)
      ) {
        playerStorage.clearSession();
        try {
          await playerApi.logout();
        } catch {
          // The login screen must not retain an unverified local session cache.
        }
      }
      if (isAbortError(error)) {
        return { kind: "aborted" };
      }
      if (
        error instanceof ApiError &&
        error.status === 401 &&
        error.problem?.code === "player.email_unverified" &&
        !sessionCreated &&
        isCurrentPlayerSessionEpoch(attemptEpoch)
      ) {
        return { kind: "email_unverified" };
      }
      if (
        error instanceof ApiError &&
        error.status === 401 &&
        !sessionCreated &&
        isCurrentPlayerSessionEpoch(attemptEpoch)
      ) {
        return { kind: "invalid_credentials" };
      }
      if (
        error instanceof ApiError &&
        error.status === 429 &&
        !sessionCreated &&
        isCurrentPlayerSessionEpoch(attemptEpoch)
      ) {
        return { kind: "rate_limited", retryAfter: error.retryAfter };
      }
      if (
        error instanceof ApiError &&
        error.status === 503 &&
        !sessionCreated &&
        isCurrentPlayerSessionEpoch(attemptEpoch)
      ) {
        return { kind: "unavailable" };
      }
      return { kind: "error" };
    }
  },

  async resendLoginVerification(
    login: string,
    password: string,
    signal?: AbortSignal,
  ): Promise<PlayerLoginResendVerificationResult> {
    try {
      await playerApi.resendLoginVerification(login, password, signal);
      return { kind: "sent" };
    } catch (error) {
      if (isAbortError(error)) {
        return { kind: "aborted" };
      }
      if (error instanceof ApiError && error.status === 401) {
        return { kind: "invalid_credentials" };
      }
      if (
        error instanceof ApiError &&
        error.status === 409 &&
        error.problem?.code === "player.email_already_verified"
      ) {
        return { kind: "already_verified" };
      }
      if (error instanceof ApiError && error.status === 429) {
        return { kind: "rate_limited", retryAfter: error.retryAfter };
      }
      if (error instanceof ApiError && error.status === 503) {
        return { kind: "unavailable", retryAfter: error.retryAfter };
      }
      return { kind: "error" };
    }
  },

  async verifyEmail(token: string, signal?: AbortSignal): Promise<PlayerVerificationResult> {
    try {
      await playerApi.verifyEmail(token, signal);
      return { kind: "ok" };
    } catch (error) {
      if (isAbortError(error)) {
        return { kind: "aborted" };
      }
      if (error instanceof ApiError && error.status === 400) {
        return { kind: "invalid" };
      }
      if (error instanceof ApiError && error.status === 429) {
        return { kind: "rate_limited", retryAfter: error.retryAfter };
      }
      return { kind: "error" };
    }
  },

  async resendVerification(
    email: string,
    signal?: AbortSignal,
  ): Promise<PlayerResendVerificationResult> {
    try {
      await playerApi.resendVerification(email, signal);
      return { kind: "accepted" };
    } catch (error) {
      if (isAbortError(error)) {
        return { kind: "aborted" };
      }
      if (error instanceof ApiError && error.status === 429) {
        return { kind: "rate_limited", retryAfter: error.retryAfter };
      }
      if (error instanceof ApiError && error.status === 503) {
        return { kind: "unavailable" };
      }
      return { kind: "error" };
    }
  },

  async refreshCurrentPlayer(
    player: Player,
    signal?: AbortSignal,
  ): Promise<RefreshPlayerResult> {
    const requestEpoch = getPlayerSessionEpoch();
    try {
      const data = await playerApi.me(signal);
      if (signal?.aborted || !isCurrentPlayerSessionEpoch(requestEpoch)) {
        return { kind: "aborted" };
      }
      const nextPlayer: Player = {
        id: data.player.id,
        username: data.player.username,
      };

      playerStorage.clearSession();
      playerStorage.setPlayerId(nextPlayer.id);
      playerStorage.setUsername(nextPlayer.username);

      return {
        kind: "ok",
        state: {
          player: nextPlayer,
        },
      };
    } catch (error) {
      if (signal?.aborted || !isCurrentPlayerSessionEpoch(requestEpoch)) {
        return { kind: "aborted" };
      }
      if (isAbortError(error)) {
        return { kind: "aborted" };
      }
      if (error instanceof ApiError && error.status === 401) {
        playerStorage.clearSession();
        return { kind: "expired" };
      }
      if (error instanceof ApiContractError) {
        return { kind: "contract" };
      }
      return { kind: "error" };
    }
  },

  async restoreCurrentPlayer(signal?: AbortSignal): Promise<RefreshPlayerResult> {
    const requestEpoch = getPlayerSessionEpoch();
    try {
      const data = await playerApi.me(signal);
      if (signal?.aborted || !isCurrentPlayerSessionEpoch(requestEpoch)) {
        return { kind: "aborted" };
      }
      const nextPlayer: Player = {
        id: data.player.id,
        username: data.player.username,
      };

      playerStorage.clearSession();
      playerStorage.setPlayerId(nextPlayer.id);
      playerStorage.setUsername(nextPlayer.username);

      return {
        kind: "ok",
        state: {
          player: nextPlayer,
        },
      };
    } catch (error) {
      if (signal?.aborted || !isCurrentPlayerSessionEpoch(requestEpoch)) {
        return { kind: "aborted" };
      }
      if (isAbortError(error)) {
        return { kind: "aborted" };
      }
      if (error instanceof ApiError && error.status === 401) {
        playerStorage.clearSession();
        return { kind: "expired" };
      }
      if (error instanceof ApiContractError) {
        return { kind: "contract" };
      }
      return { kind: "error" };
    }
  },

  async getAccountSettings(signal?: AbortSignal): Promise<AccountSettingsResult> {
    const requestEpoch = getPlayerSessionEpoch();
    try {
      const settings = await playerApi.getAccountSettings(signal);
      if (!sessionEpochStillCurrent(requestEpoch, signal)) {
        return { kind: "aborted" };
      }
      return { kind: "ok", settings };
    } catch (error) {
      if (isAbortError(error) || !sessionEpochStillCurrent(requestEpoch, signal)) {
        return { kind: "aborted" };
      }
      if (error instanceof ApiError && error.status === 401) {
        playerStorage.clearSession();
        return { kind: "expired" };
      }
      if (error instanceof ApiContractError) {
        return { kind: "contract" };
      }
      return { kind: "error" };
    }
  },

  async changeUsername(
    currentPassword: string,
    username: string,
    signal?: AbortSignal,
  ): Promise<AccountMutationResult> {
    const requestEpoch = getPlayerSessionEpoch();
    try {
      const response = await playerApi.changeUsername(currentPassword, username, signal);
      if (!sessionEpochStillCurrent(requestEpoch, signal)) {
        return { kind: "aborted" };
      }
      const player: Player = { id: response.id, username: response.username };
      playerStorage.clearSession();
      playerStorage.setPlayerId(player.id);
      playerStorage.setUsername(player.username);
      return { kind: "ok", player };
    } catch (error) {
      return accountMutationError(error, requestEpoch, signal);
    }
  },

  async changePassword(
    currentPassword: string,
    newPassword: string,
    signal?: AbortSignal,
  ): Promise<AccountMutationResult> {
    const requestEpoch = beginPlayerSessionRotation();
    let rotated = false;
    try {
      await playerApi.changePassword(currentPassword, newPassword, signal);
      if (!sessionEpochStillCurrent(requestEpoch, signal)) {
        return { kind: "aborted" };
      }
      rotated = true;
      return { kind: "ok" };
    } catch (error) {
      if (error instanceof ApiError && error.status === 400 && sessionEpochStillCurrent(requestEpoch, signal)) {
        return { kind: "password_invalid" };
      }
      return accountMutationError(error, requestEpoch, signal);
    } finally {
      finishPlayerSessionRotation(requestEpoch);
      if (rotated) notifyPlayerAvatarChange("refresh");
    }
  },

  async beginEmailChange(
    currentPassword: string,
    newEmail: string,
    signal?: AbortSignal,
  ): Promise<AccountMutationResult & { settings?: PlayerAccountSettingsResponse }> {
    const requestEpoch = getPlayerSessionEpoch();
    try {
      const settings = await playerApi.beginEmailChange(currentPassword, newEmail, signal);
      if (!sessionEpochStillCurrent(requestEpoch, signal)) {
        return { kind: "aborted" };
      }
      return { kind: "ok", settings };
    } catch (error) {
      return accountMutationError(error, requestEpoch, signal);
    }
  },

  async resendEmailChange(
    signal?: AbortSignal,
  ): Promise<AccountMutationResult & { settings?: PlayerAccountSettingsResponse }> {
    const requestEpoch = getPlayerSessionEpoch();
    try {
      const settings = await playerApi.resendEmailChange(signal);
      if (!sessionEpochStillCurrent(requestEpoch, signal)) {
        return { kind: "aborted" };
      }
      return { kind: "ok", settings };
    } catch (error) {
      return accountMutationError(error, requestEpoch, signal);
    }
  },

  async cancelEmailChange(
    signal?: AbortSignal,
  ): Promise<AccountMutationResult & { settings?: PlayerAccountSettingsResponse }> {
    const requestEpoch = getPlayerSessionEpoch();
    try {
      const settings = await playerApi.cancelEmailChange(signal);
      if (!sessionEpochStillCurrent(requestEpoch, signal)) {
        return { kind: "aborted" };
      }
      return { kind: "ok", settings };
    } catch (error) {
      return accountMutationError(error, requestEpoch, signal);
    }
  },

  async confirmEmailChange(code: string, signal?: AbortSignal): Promise<EmailConfirmationResult> {
    const requestEpoch = beginPlayerSessionRotation();
    let rotated = false;
    try {
      const confirmation = await playerApi.confirmEmailChange(code, signal);
      if (!sessionEpochStillCurrent(requestEpoch, signal)) {
        return { kind: "aborted" };
      }
      rotated = true;
      return { kind: "ok", confirmation };
    } catch (error) {
      return accountMutationError(error, requestEpoch, signal);
    } finally {
      finishPlayerSessionRotation(requestEpoch);
      if (rotated) notifyPlayerAvatarChange("refresh");
    }
  },

  async getAvatar(signal?: AbortSignal): Promise<PlayerAvatarResult> {
    const requestEpoch = getPlayerSessionEpoch();
    try {
      const avatar = await playerApi.getAvatar(signal);
      if (!sessionEpochStillCurrent(requestEpoch, signal)) return { kind: "aborted" };
      return { kind: "ok", avatar };
    } catch (error) {
      return avatarError(error, requestEpoch, signal);
    }
  },

  async replaceAvatar(file: File, signal?: AbortSignal): Promise<PlayerAvatarResult> {
    const requestEpoch = getPlayerSessionEpoch();
    try {
      await playerApi.replaceAvatar(file, signal);
      if (!sessionEpochStillCurrent(requestEpoch, signal)) return { kind: "aborted" };
      notifyPlayerAvatarChange("refresh");
      return { kind: "ok" };
    } catch (error) {
      return avatarError(error, requestEpoch, signal);
    }
  },

  async deleteAvatar(signal?: AbortSignal): Promise<PlayerAvatarResult> {
    const requestEpoch = getPlayerSessionEpoch();
    try {
      await playerApi.deleteAvatar(signal);
      if (!sessionEpochStillCurrent(requestEpoch, signal)) return { kind: "aborted" };
      notifyPlayerAvatarChange("refresh");
      return { kind: "ok" };
    } catch (error) {
      return avatarError(error, requestEpoch, signal);
    }
  },

  async clearCurrentPlayer(): Promise<void> {
    playerStorage.clearSession();
    notifyPlayerAvatarChange("clear");
    try {
      await playerApi.logout();
    } catch {
      // Local state is already cleared; logout is best-effort when offline.
    }
  },

  getCurrentPlayer(): Player | null {
    const id = playerStorage.getPlayerId();
    const username = playerStorage.getUsername();

    if (!id || !username) return null;

    return {
      id,
      username,
    };
  },
};
