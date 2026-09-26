import { Player } from "../../shared/types";
import { ApiError, playerApi } from "../../shared/api";
import { ApiContractError } from "../../shared/api/guards";
import { playerStorage } from "../../shared/lib/storage";

interface PlayerSessionState {
  player: Player;
}

export type InitializePlayerResult =
  | { kind: "ok"; player: Player }
  | { kind: "rate_limited"; retryAfter?: string | null }
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
  async initializePlayer(
    username: string,
    signal?: AbortSignal,
  ): Promise<InitializePlayerResult> {
    try {
      const data = await playerApi.join(username, signal);

      const player: Player = {
        id: data.player_id,
        username: username,
      };

      playerStorage.clearSession();
      playerStorage.setPlayerId(player.id);
      playerStorage.setUsername(player.username);

      return { kind: "ok", player };
    } catch (error) {
      if (isAbortError(error)) {
        return { kind: "aborted" };
      }
      if (error instanceof ApiError && error.status === 429) {
        return { kind: "rate_limited", retryAfter: error.retryAfter };
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
