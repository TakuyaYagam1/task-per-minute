import type { TaskPayload } from "./websocket";

export type GameState = "playing" | "won" | "lost" | "timeup";

export type FlagStatus = "idle" | "correct" | "incorrect";

export type GameResultSource = "server" | "local_timer";

export interface GameData {
  duel_id: string;
  deadline: string;
  time_limit_seconds: number;
  task: TaskPayload;
  opponent_username?: string;
  opponent_id?: string;
  opponent_disconnected?: boolean;
  opponent_reconnect_deadline?: string;
}

export interface GameResult {
  state: GameState;
  source?: GameResultSource;
  reason?: string;
  duel_id?: string;
  winner_id?: string | null;
  winner_username?: string | null;
}
