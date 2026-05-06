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
        session_token: data.session_token,
      };

      gameStorage.clearGameData();
      gameStorage.setPlayerId(player.id);
      gameStorage.setSessionToken(player.session_token);
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
      const data = await playerApi.me(player.session_token, signal);
      const nextPlayer: Player = {
        id: data.player.id,
        username: data.player.username,
        session_token: player.session_token,
      };

      gameStorage.setPlayerId(nextPlayer.id);
      gameStorage.setSessionToken(nextPlayer.session_token);
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

  clearCurrentPlayer(): void {
    gameStorage.clearPlayerSession();
  },

  getCurrentPlayer(): Player | null {
    const id = gameStorage.getPlayerId();
    const username = gameStorage.getUsername();
    const sessionToken = gameStorage.getSessionToken();

    if (!id || !username || !sessionToken) return null;

    return {
      id,
      username,
      session_token: sessionToken,
    };
  },
};
