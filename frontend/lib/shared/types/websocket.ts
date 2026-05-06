export type ClientMessageType =
  | "join_queue"
  | "leave_queue"
  | "flag_submit"
  | "surrender"
  | "ping";

export type ServerMessageType =
  | "queue_joined"
  | "queue_left"
  | "match_found"
  | "task_assigned"
  | "flag_result"
  | "hint_unlocked"
  | "duel_expired"
  | "duel_finished"
  | "opponent_solved"
  | "opponent_disconnected"
  | "opponent_reconnected"
  | "duel_resume"
  | "pong"
  | "error";

export type MessageType = ClientMessageType | ServerMessageType;

export interface DuelPayload {
  id: string;
  player1_id: string;
  player2_id: string;
  status: "active" | "finished";
  winner_id?: string;
  deadline: string;
  started_at: string;
  finished_at?: string;
}

export interface HintScheduleEntry {
  hint_index: number;
  unlock_at: string;
}

export interface UnlockedHint {
  hint_index: number;
  hint: string;
  unlocked_at: string;
}

export interface TaskPayload {
  id: string;
  title: string;
  description: string;
  category:
    | "web"
    | "crypto"
    | "forensics"
    | "reverse"
    | "pwn"
    | "steganography"
    | "ppc"
    | "osint"
    | "mobile"
    | "hardware"
    | "misc";
  difficulty: "easy" | "medium" | "hard";
  time_limit: number;
  time_limit_seconds: number;
  task_url?: string | null;
  source_url?: string | null;
  source_file_url?: string | null;
  hint_schedule?: HintScheduleEntry[];
  unlocked_hints?: UnlockedHint[];
}

export interface MatchFoundPayload {
  duel_id: string;
  opponent_username: string;
  duel: DuelPayload;
}

export interface TaskAssignedPayload {
  duel_id: string;
  deadline: string;
  time_limit_seconds: number;
  task: TaskPayload;
}

export interface FlagResultPayload {
  duel_id: string;
  correct: boolean;
  message?: string;
}

export interface HintUnlockedPayload {
  duel_id: string;
  task_id: string;
  hint_index: number;
  hint: string;
  unlocked_at: string;
}

export interface OpponentSolvedPayload {
  duel_id: string;
  player_id: string;
}

export interface OpponentDisconnectedPayload {
  duel_id: string;
  player_id: string;
  reconnect_deadline: string;
}

export interface OpponentReconnectedPayload {
  duel_id: string;
  player_id: string;
  deadline: string;
}

export interface DuelResumePayload {
  duel_id: string;
  opponent_id: string;
  deadline: string;
  opponent_disconnected?: boolean;
  opponent_reconnect_deadline?: string;
  task?: TaskPayload;
}

export interface DuelFinishedPayload {
  duel_id: string;
  winner_id?: string;
  winner_username?: string;
  your_solved: boolean;
  opponent_solved: boolean;
  duel: DuelPayload;
}

export type ServerPayloadByType = {
  queue_joined: undefined;
  queue_left: undefined;
  match_found: MatchFoundPayload;
  task_assigned: TaskAssignedPayload;
  flag_result: FlagResultPayload;
  hint_unlocked: HintUnlockedPayload;
  duel_expired: { duel_id: string };
  duel_finished: DuelFinishedPayload;
  opponent_solved: OpponentSolvedPayload;
  opponent_disconnected: OpponentDisconnectedPayload;
  opponent_reconnected: OpponentReconnectedPayload;
  duel_resume: DuelResumePayload;
  pong: undefined;
  error: undefined;
};

type ServerPayloadMessageType = Exclude<ServerMessageType, "error">;

type ServerPayloadMessage = {
  [TType in ServerPayloadMessageType]: {
    type: TType;
    payload?: ServerPayloadByType[TType];
  };
}[ServerPayloadMessageType];

export type WebSocketMessage =
  | ServerPayloadMessage
  | {
      type: "error";
      payload?: undefined;
      code?: string;
      message?: string;
    };

export type ClientPayloadByType = {
  join_queue: undefined;
  leave_queue: undefined;
  flag_submit: {
    duel_id: string;
    flag: string;
  };
  surrender: {
    duel_id: string;
  };
  ping: undefined;
};

export type ClientMessageWithoutPayload = {
  [TType in ClientMessageType]: ClientPayloadByType[TType] extends undefined
    ? TType
    : never;
}[ClientMessageType];

export type ClientMessageWithPayload = Exclude<
  ClientMessageType,
  ClientMessageWithoutPayload
>;

export type ClientWebSocketMessage =
  | {
      [TType in ClientMessageWithoutPayload]: {
        type: TType;
      };
    }[ClientMessageWithoutPayload]
  | {
      [TType in ClientMessageWithPayload]: {
        type: TType;
        payload: ClientPayloadByType[TType];
      };
    }[ClientMessageWithPayload];
