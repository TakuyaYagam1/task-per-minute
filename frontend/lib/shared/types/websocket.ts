export type MessageType =
  | "join_queue"
  | "leave_queue"
  | "match_found"
  | "game_start"
  | "flag_submit"
  | "flag_correct"
  | "game_end"
  | "game_won"
  | "game_lost"
  | "error"
  | "opponent_solved"
  | "ping"
  | "pong";

export interface WebSocketMessage {
  type: MessageType;
  data?: any;
  player_id?: number;
}

export interface GameWebSocket extends WebSocket {
  isGameSocket?: boolean;
}

declare global {
  interface Window {
    gameWebSocket?: GameWebSocket;
  }
}
