import { Player } from "../../shared/types";
import {
  ApiError,
  getPlayerSessionEpoch,
  isCurrentPlayerSessionEpoch,
  playerApi,
} from "../../shared/api";
import { ApiContractError } from "../../shared/api/guards";
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
  | { kind: "rate_limited"; retryAfter?: string | null }
  | { kind: "unavailable" }
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

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  error.name === "AbortError";

export const playerModel = {
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
    try {
      const data = await playerApi.me(signal);
      if (signal?.aborted) {
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
    try {
      const data = await playerApi.me(signal);
      if (signal?.aborted) {
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

  async clearCurrentPlayer(): Promise<void> {
    playerStorage.clearSession();
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
