import { Player } from "../../shared/types";
import { generateUsername } from "../../shared/lib";
import { playerApi } from "../../shared/api";
import { gameStorage } from "../../shared/lib/storage";

export const playerModel = {
  // Инициализирует нового игрока и сохраняет данные в локальное хранилище
  async initializePlayer(): Promise<Player | null> {
    try {
      const username = generateUsername();
      const data = await playerApi.join(username);
      
      const player: Player = {
        id: data.player_id,
        username: data.username || username,
        session_id: data.session_id,
      };

      gameStorage.setPlayerId(player.id);
      gameStorage.setSessionId(player.session_id || "");
      gameStorage.setUsername(player.username);

      return player;
    } catch (error) {
      console.error("Failed to initialize player:", error);
      return null;
    }
  },

  // Возвращает текущего игрока из локального хранилища
  getCurrentPlayer(): Player | null {
    const id = gameStorage.getPlayerId();
    const username = gameStorage.getUsername();
    const session_id = gameStorage.getSessionId();

    if (!id || !username) return null;

    return {
      id,
      username,
      session_id: session_id || undefined,
    };
  },
};
