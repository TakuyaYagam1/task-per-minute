import { GameState, GameData, GameResult } from "../../shared/types";
import { gameStorage } from "../../shared/lib/storage";

export const gameModel = {
  // Сохраняет данные игры в локальное хранилище
  saveGameData(data: GameData): void {
    gameStorage.setGameData(data);
  },

  // Получает данные игры из локального хранилища
  getGameData(): {
    timeLimit: number;
    taskUrl: string;
    taskTitle: string;
    taskDescription: string;
    sessionId: number | null;
    gameStartTime: number | null;
  } {
    const stored = gameStorage.getGameData();
    
    return {
      timeLimit: stored.timeLimit ? parseInt(stored.timeLimit) : 300,
      taskUrl: stored.taskUrl || "",
      taskTitle: stored.taskTitle || "",
      taskDescription: stored.taskDescription || "",
      sessionId: stored.gameSessionId ? parseInt(stored.gameSessionId) : null,
      gameStartTime: stored.gameStartTime ? parseInt(stored.gameStartTime) : null,
    };
  },

  // Сохраняет результат игры в локальное хранилище
  saveGameResult(result: GameResult): void {
    gameStorage.setGameResult(result);
  },

  // Получает результат игры из локального хранилища
  getGameResult(): GameResult | null {
    const result = gameStorage.getGameResult();
    if (!result) return null;

    if (typeof result === "string") {
      return {
        state: result === "timeout" ? "timeup" : (result as GameState),
      };
    }

    return result;
  },

  // Вычисляет оставшееся время игры
  calculateRemainingTime(): number {
    const { gameStartTime, timeLimit } = this.getGameData();
    
    if (!gameStartTime) return timeLimit;
    
    const elapsed = Math.floor((Date.now() - gameStartTime) / 1000);
    return Math.max(0, timeLimit - elapsed);
  },

  // Очищает все данные игры из локального хранилища
  clearGameData(): void {
    gameStorage.clearGameData();
  },
};
