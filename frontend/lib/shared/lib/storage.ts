export const gameStorage = {
  setPlayerId: (id: number): void => {
    localStorage.setItem("player_id", id.toString());
  },

  getPlayerId: (): number | null => {
    const id = localStorage.getItem("player_id");
    return id ? parseInt(id) : null;
  },

  setSessionId: (id: string): void => {
    localStorage.setItem("session_id", id);
  },

  getSessionId: (): string | null => {
    return localStorage.getItem("session_id");
  },

  setUsername: (username: string): void => {
    localStorage.setItem("username", username);
  },

  getUsername: (): string | null => {
    return localStorage.getItem("username");
  },

  setGameData: (data: any): void => {
    localStorage.setItem("currentGame", JSON.stringify(data));
    localStorage.setItem("game_session_id", data.session_id?.toString() || "");
    localStorage.setItem("task_url", data.task_url || "");
    localStorage.setItem("task_title", data.task_title || "");
    localStorage.setItem("task_description", data.description || "");
    localStorage.setItem("time_limit", data.time_limit?.toString() || "300");
    localStorage.setItem("game_start_time", Date.now().toString());
    localStorage.setItem("opponent_id", data.opponent_id1 || data.opponent_id2 || "");
  },

  getGameData: () => {
    return {
      currentGame: localStorage.getItem("currentGame"),
      gameSessionId: localStorage.getItem("game_session_id"),
      taskUrl: localStorage.getItem("task_url"),
      taskTitle: localStorage.getItem("task_title"),
      taskDescription: localStorage.getItem("task_description"),
      timeLimit: localStorage.getItem("time_limit"),
      gameStartTime: localStorage.getItem("game_start_time"),
      opponentId: localStorage.getItem("opponent_id"),
    };
  },

  setGameResult: (result: any): void => {
    localStorage.setItem("game_result", JSON.stringify(result));
  },

  getGameResult: (): any => {
    const result = localStorage.getItem("game_result");
    if (!result) return null;
    
    try {
      return JSON.parse(result);
    } catch {
      return result;
    }
  },

  clearGameData: (): void => {
    localStorage.removeItem("game_result");
    localStorage.removeItem("currentGame");
    localStorage.removeItem("game_session_id");
    localStorage.removeItem("game_start_time");
    localStorage.removeItem("task_url");
    localStorage.removeItem("task_title");
    localStorage.removeItem("task_description");
    localStorage.removeItem("time_limit");
    localStorage.removeItem("opponent_id");
  },
};
