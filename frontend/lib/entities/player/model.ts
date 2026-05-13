import { Player } from "../../shared/types";
import { ApiError, playerApi, type PlayerMeResponse } from "../../shared/api";
import { ApiContractError } from "../../shared/api/guards";
import { gameStorage } from "../../shared/lib/storage";

interface PlayerSessionState {
  player: Player;
  activeDuel?: PlayerMeResponse["active_duel"];
}

export type InitializePlayerResult =
  | { kind: "ok"; player: Player }
  | { kind: "in_duel" }
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
  (error.name === "AbortError" || error.name === "TimeoutError");

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

      gameStorage.clearPlayerSession();
      gameStorage.clearGameData();
      gameStorage.setPlayerId(player.id);
      gameStorage.setUsername(player.username);

      return { kind: "ok", player };
    } catch (error) {
      if (isAbortError(error)) {
        return { kind: "aborted" };
      }
      if (error instanceof ApiError && error.status === 409) {
        return { kind: "in_duel" };
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

      gameStorage.clearPlayerSession();
      gameStorage.setPlayerId(nextPlayer.id);
      gameStorage.setUsername(nextPlayer.username);

      return {
        kind: "ok",
        state: {
          player: nextPlayer,
          activeDuel: data.active_duel,
        },
      };
    } catch (error) {
      if (isAbortError(error)) {
        return { kind: "aborted" };
      }
      if (error instanceof ApiError && error.status === 401) {
        gameStorage.clearPlayerSession();
        gameStorage.clearGameData();
        return { kind: "expired" };
      }
      if (error instanceof ApiContractError) {
        return { kind: "contract" };
      }
      return { kind: "error" };
    }
  },

  async clearCurrentPlayer(): Promise<void> {
    gameStorage.clearPlayerSession();
    try {
      await playerApi.logout();
    } catch {
      // Local state is already cleared; logout is best-effort when offline.
    }
  },

  getCurrentPlayer(): Player | null {
    const id = gameStorage.getPlayerId();
    const username = gameStorage.getUsername();

    if (!id || !username) return null;

    return {
      id,
      username,
    };
  },
};
