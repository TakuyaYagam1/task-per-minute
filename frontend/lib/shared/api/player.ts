import { CONFIG } from '../config';

export const playerApi = {
  async join(username: string) {
    const response = await fetch(`${CONFIG.apiUrl}/api/v1/join`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username }),
    });

    if (!response.ok) {
      throw new Error("Failed to join game");
    }

    return response.json();
  },

  async submitFlag(sessionId: number, playerId: number, flag: string) {
    const response = await fetch(`${CONFIG.apiUrl}/api/v1/submit-flag`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        session_id: sessionId,
        player_id: playerId,
        flag: flag.trim(),
      }),
    });

    if (!response.ok) {
      throw new Error("Failed to submit flag");
    }

    return response.json();
  },
};
