import type {
  DuelPayload,
  ServerMessageType,
  TaskPayload,
  WebSocketMessage,
} from "../types";
import { isOptionalUUIDOrNull, isUUID } from "./validation";

const SERVER_MESSAGE_TYPES = new Set<ServerMessageType>([
  "queue_joined",
  "queue_left",
  "match_found",
  "task_assigned",
  "flag_result",
  "hint_unlocked",
  "duel_expired",
  "duel_finished",
  "opponent_solved",
  "opponent_disconnected",
  "opponent_reconnected",
  "duel_resume",
  "pong",
  "error",
]);

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === "object";

const isServerMessageType = (value: unknown): value is ServerMessageType =>
  typeof value === "string" && SERVER_MESSAGE_TYPES.has(value as ServerMessageType);

const TASK_CATEGORIES = new Set<TaskPayload["category"]>([
  "web",
  "crypto",
  "forensics",
  "reverse",
  "pwn",
  "steganography",
  "ppc",
  "osint",
  "mobile",
  "hardware",
  "misc",
]);

const TASK_DIFFICULTIES = new Set<TaskPayload["difficulty"]>([
  "easy",
  "medium",
  "hard",
]);

const isString = (value: unknown): value is string => typeof value === "string";

const isDateString = (value: unknown): value is string =>
  isString(value) && Number.isFinite(Date.parse(value));

const isOptionalDateStringOrNull = (value: unknown): value is string | null | undefined =>
  value === undefined || value === null || isDateString(value);

const isOptionalStringOrNull = (value: unknown): value is string | null | undefined =>
  value === undefined || value === null || isString(value);

const isInteger = (value: unknown): value is number =>
  typeof value === "number" && Number.isInteger(value);

const isPositiveInteger = (value: unknown): value is number => isInteger(value) && value > 0;

const isHintIndex = (value: unknown): value is number =>
  isInteger(value) && value >= 1 && value <= 3;

const isBoolean = (value: unknown): value is boolean => typeof value === "boolean";

const isHttpURL = (value: unknown): value is string => {
  if (!isString(value)) {
    return false;
  }
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false;
  }
};

const isOptionalHttpURLOrNull = (value: unknown): value is string | null | undefined =>
  value === undefined || value === null || isHttpURL(value);

const isHintScheduleEntry = (value: unknown): boolean =>
  isRecord(value) && isHintIndex(value.hint_index) && isDateString(value.unlock_at);

const isUnlockedHint = (value: unknown): boolean =>
  isRecord(value) &&
  isHintIndex(value.hint_index) &&
  isString(value.hint) &&
  isDateString(value.unlocked_at);

const isOptionalHintArray = (
  value: unknown,
  itemGuard: (item: unknown) => boolean,
): boolean => {
  if (value === undefined) {
    return true;
  }
  if (!Array.isArray(value) || value.length > 3) {
    return false;
  }
  const seen = new Set<number>();
  return value.every((item) => {
    if (!itemGuard(item) || !isRecord(item) || !isHintIndex(item.hint_index)) {
      return false;
    }
    if (seen.has(item.hint_index)) {
      return false;
    }
    seen.add(item.hint_index);
    return true;
  });
};

export const isTaskPayload = (value: unknown): value is TaskPayload =>
  isRecord(value) &&
  isUUID(value.id) &&
  isString(value.title) &&
  isString(value.description) &&
  isString(value.category) &&
  TASK_CATEGORIES.has(value.category as TaskPayload["category"]) &&
  isString(value.difficulty) &&
  TASK_DIFFICULTIES.has(value.difficulty as TaskPayload["difficulty"]) &&
  isPositiveInteger(value.time_limit) &&
  isPositiveInteger(value.time_limit_seconds) &&
  isOptionalStringOrNull(value.task_url) &&
  isOptionalHttpURLOrNull(value.source_url) &&
  isOptionalHttpURLOrNull(value.source_file_url) &&
  isOptionalHintArray(value.hint_schedule, isHintScheduleEntry) &&
  isOptionalHintArray(value.unlocked_hints, isUnlockedHint);

const isDuelPayload = (value: unknown): value is DuelPayload =>
  isRecord(value) &&
  isUUID(value.id) &&
  isUUID(value.player1_id) &&
  isUUID(value.player2_id) &&
  (value.status === "active" || value.status === "finished") &&
  isOptionalUUIDOrNull(value.winner_id) &&
  isDateString(value.deadline) &&
  isDateString(value.started_at) &&
  isOptionalDateStringOrNull(value.finished_at);

const hasMatchingDuelID = (duelID: unknown, duel: unknown): duel is DuelPayload =>
  isUUID(duelID) && isDuelPayload(duel) && duel.id === duelID;

const isNoPayload = (value: unknown): boolean => value === undefined || value === null;

const isValidPayload = (type: ServerMessageType, payload: unknown): boolean => {
  switch (type) {
    case "queue_joined":
    case "queue_left":
    case "pong":
    case "error":
      return isNoPayload(payload);

    case "match_found":
      return (
        isRecord(payload) &&
        isUUID(payload.duel_id) &&
        isString(payload.opponent_username) &&
        hasMatchingDuelID(payload.duel_id, payload.duel)
      );

    case "task_assigned":
      return (
        isRecord(payload) &&
        isUUID(payload.duel_id) &&
        isDateString(payload.deadline) &&
        isPositiveInteger(payload.time_limit_seconds) &&
        isTaskPayload(payload.task)
      );

    case "flag_result":
      return (
        isRecord(payload) &&
        isUUID(payload.duel_id) &&
        isBoolean(payload.correct) &&
        (payload.message === undefined || isString(payload.message))
      );

    case "hint_unlocked":
      return (
        isRecord(payload) &&
        isUUID(payload.duel_id) &&
        isUUID(payload.task_id) &&
        isHintIndex(payload.hint_index) &&
        isString(payload.hint) &&
        isDateString(payload.unlocked_at)
      );

    case "duel_expired":
      return isRecord(payload) && isUUID(payload.duel_id);

    case "duel_finished":
      return (
        isRecord(payload) &&
        isUUID(payload.duel_id) &&
        isOptionalUUIDOrNull(payload.winner_id) &&
        isOptionalStringOrNull(payload.winner_username) &&
        isBoolean(payload.your_solved) &&
        isBoolean(payload.opponent_solved) &&
        hasMatchingDuelID(payload.duel_id, payload.duel)
      );

    case "opponent_solved":
      return (
        isRecord(payload) &&
        isUUID(payload.duel_id) &&
        isUUID(payload.player_id)
      );

    case "opponent_disconnected":
      return (
        isRecord(payload) &&
        isUUID(payload.duel_id) &&
        isUUID(payload.player_id) &&
        isDateString(payload.reconnect_deadline)
      );

    case "opponent_reconnected":
      return (
        isRecord(payload) &&
        isUUID(payload.duel_id) &&
        isUUID(payload.player_id) &&
        isDateString(payload.deadline)
      );

    case "duel_resume":
      return (
        isRecord(payload) &&
        isUUID(payload.duel_id) &&
        isUUID(payload.opponent_id) &&
        isDateString(payload.deadline) &&
        (payload.opponent_disconnected === undefined ||
          isBoolean(payload.opponent_disconnected)) &&
        isOptionalDateStringOrNull(payload.opponent_reconnect_deadline) &&
        (payload.task === undefined || isTaskPayload(payload.task))
      );

    default:
      return false;
  }
};

export const openExternalUrl = (url: string): Window | null => {
  return window.open(url, "_blank", "noopener,noreferrer");
};

const LOCAL_HOSTNAMES = new Set(["localhost", "127.0.0.1", "::1"]);
const INVALID_SESSION_WS_CODE = "player.invalid_session";

const currentLocationProtocol = (): string => {
  const override = (window as unknown as { __taskPerMinuteLocationProtocol?: unknown })
    .__taskPerMinuteLocationProtocol;
  if (override === "http:" || override === "https:") {
    return override;
  }
  return window.location.protocol;
};

export const isSafeNavigationURL = (rawValue: unknown): rawValue is string => {
  if (typeof rawValue !== "string") {
    return false;
  }
  let parsed: URL;
  try {
    parsed = new URL(rawValue);
  } catch {
    return false;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return false;
  }
  if (typeof window === "undefined") {
    return true;
  }
  if (currentLocationProtocol() !== "https:") {
    return true;
  }
  if (parsed.protocol === "https:") {
    return true;
  }
  return LOCAL_HOSTNAMES.has(parsed.hostname);
};

export const parseWebSocketMessage = (raw: string): WebSocketMessage | null => {
  try {
    const value: unknown = JSON.parse(raw);
    if (!isRecord(value) || !isServerMessageType(value.type)) {
      return null;
    }
    if (!isValidPayload(value.type, value.payload)) {
      return null;
    }
    if (
      value.type === "error" &&
      ((value.code !== undefined && !isString(value.code)) ||
        (value.message !== undefined && !isString(value.message)))
    ) {
      return null;
    }
    return value as WebSocketMessage;
  } catch {
    return null;
  }
};

export const isInvalidSessionWebSocketMessage = (message: WebSocketMessage): boolean =>
  message.type === "error" && message.code === INVALID_SESSION_WS_CODE;
