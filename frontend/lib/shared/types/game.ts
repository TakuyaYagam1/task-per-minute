export type GameState = "playing" | "won" | "lost" | "timeup";

export type FlagStatus = "idle" | "correct" | "incorrect";

export interface GameData {
  session_id: number;
  task_url: string;
  task_title: string;
  description: string;
  time_limit: number;
  opponent_id1?: string;
  opponent_id2?: string;
}

export interface GameResult {
  state: GameState;
  reason?: string;
  winner_id?: number;
  loser_id?: number;
  session_id?: number;
}
