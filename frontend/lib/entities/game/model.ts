import { GameData, GameResult } from "../../shared/types";
import { gameStorage } from "../../shared/lib/storage";

export const gameModel = {
  saveGameData(data: GameData): void {
    gameStorage.setGameData(data);
  },

  getGameData(): GameData | null {
    return gameStorage.getGameData();
  },

  saveGameResult(result: GameResult): void {
    gameStorage.setGameResult(result);
  },

  getGameResult(): GameResult | null {
    return gameStorage.getGameResult();
  },

  clearGameResult(): void {
    gameStorage.clearGameResult();
  },

  calculateRemainingTime(deadline?: string): number {
    if (!deadline) {
      const game = this.getGameData();
      deadline = game?.deadline;
    }
    if (!deadline) {
      return 0;
    }

    const deadlineMs = Date.parse(deadline);
    if (!Number.isFinite(deadlineMs)) {
      return 0;
    }

    const remainingMs = deadlineMs - Date.now();
    return Math.max(0, Math.ceil(remainingMs / 1000));
  },

  clearGameData(): void {
    gameStorage.clearGameData();
  },

  clearCurrentGame(): void {
    gameStorage.clearCurrentGame();
  },
};
