import type {
  GameData,
  GameResult,
  GameResultSource,
  GameState,
} from "../types";
import { isTaskPayload } from "./utils";
import { isOptionalUUIDOrNull, isUUID } from "./validation";

const PLAYER_ID_KEY = "player_id";
const SESSION_TOKEN_KEY = "session_token";
const USERNAME_KEY = "username";
const CURRENT_GAME_KEY = "currentGame";
const GAME_RESULT_KEY = "game_result";
const REDIRECT_NOTIFICATION_KEY = "redirect_notification";

const localStorageFallback = new Map<string, string>();
const sessionStorageFallback = new Map<string, string>();

const browserLocalStorage = (): Storage | null => {
  if (typeof window === "undefined") {
    return null;
  }
  try {
    return window.localStorage;
  } catch {
    return null;
  }
};

const browserSessionStorage = (): Storage | null => {
  if (typeof window === "undefined") {
    return null;
  }
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
};

const browserGameStorage = (): Storage | null => browserSessionStorage();

const readItem = (
  storage: Storage | null,
  fallback: Map<string, string>,
  key: string,
): string | null => {
  if (!storage) {
    return fallback.get(key) || null;
  }
  try {
    return storage.getItem(key) || null;
  } catch {
    return fallback.get(key) || null;
  }
};

const writeItem = (
  storage: Storage | null,
  fallback: Map<string, string>,
  key: string,
  value: string,
): void => {
  if (!storage) {
    fallback.set(key, value);
    return;
  }
  try {
    storage.setItem(key, value);
    fallback.delete(key);
  } catch {
    // Storage can be blocked in private or restricted browser contexts.
    fallback.set(key, value);
  }
};

const removeItem = (
  storage: Storage | null,
  fallback: Map<string, string>,
  key: string,
): void => {
  fallback.delete(key);
  try {
    storage?.removeItem(key);
  } catch {
    // Best-effort cleanup only.
  }
};

const readGameItem = (key: string): string | null => {
  const storage = browserGameStorage();
  const value = readItem(storage, sessionStorageFallback, key);
  if (value) {
    return value;
  }

  const legacyStorage = browserLocalStorage();
  const legacyValue = readItem(legacyStorage, localStorageFallback, key);
  if (!legacyValue) {
    return null;
  }

  writeItem(storage, sessionStorageFallback, key, legacyValue);
  removeItem(legacyStorage, localStorageFallback, key);
  return legacyValue;
};

const writeGameItem = (key: string, value: string): void => {
  writeItem(browserGameStorage(), sessionStorageFallback, key, value);
  removeItem(browserLocalStorage(), localStorageFallback, key);
};

const removeGameItem = (key: string): void => {
  removeItem(browserGameStorage(), sessionStorageFallback, key);
  removeItem(browserLocalStorage(), localStorageFallback, key);
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === "object";

const isString = (value: unknown): value is string => typeof value === "string";

const isOptionalString = (value: unknown): value is string | undefined =>
  value === undefined || isString(value);

const isValidDateString = (value: unknown): value is string =>
  isString(value) && Number.isFinite(Date.parse(value));

const isOptionalDateString = (value: unknown): value is string | undefined =>
  value === undefined || isValidDateString(value);

const isPositiveInteger = (value: unknown): value is number =>
  typeof value === "number" && Number.isInteger(value) && value > 0;

const GAME_STATES = new Set<GameState>(["playing", "won", "lost", "timeup"]);
const GAME_RESULT_SOURCES = new Set<GameResultSource>([
  "server",
  "local_timer",
]);

const isOptionalGameResultSource = (
  value: unknown,
): value is GameResultSource | undefined =>
  value === undefined ||
  (isString(value) && GAME_RESULT_SOURCES.has(value as GameResultSource));

const isGameData = (value: unknown): value is GameData =>
  isRecord(value) &&
  isUUID(value.duel_id) &&
  isValidDateString(value.deadline) &&
  isPositiveInteger(value.time_limit_seconds) &&
  isTaskPayload(value.task) &&
  isOptionalString(value.opponent_username) &&
  (value.opponent_id === undefined || isUUID(value.opponent_id)) &&
  (value.opponent_disconnected === undefined ||
    typeof value.opponent_disconnected === "boolean") &&
  isOptionalDateString(value.opponent_reconnect_deadline);

const isGameResult = (value: unknown): value is GameResult =>
  isRecord(value) &&
  isString(value.state) &&
  GAME_STATES.has(value.state as GameState) &&
  isOptionalGameResultSource(value.source) &&
  isOptionalString(value.reason) &&
  (value.duel_id === undefined || isUUID(value.duel_id)) &&
  isOptionalUUIDOrNull(value.winner_id) &&
  (value.winner_username === undefined ||
    value.winner_username === null ||
    isString(value.winner_username));

const getUUIDItem = (key: string): string | null => {
  const value = readGameItem(key);
  if (!value) {
    return null;
  }
  if (isUUID(value)) {
    return value;
  }
  removeGameItem(key);
  return null;
};

const parseStoredJSON = <T>(
  key: string,
  guard: (value: unknown) => value is T,
): T | null => {
  const raw = readGameItem(key);
  if (!raw) {
    return null;
  }
  try {
    const value: unknown = JSON.parse(raw);
    if (guard(value)) {
      return value;
    }
  } catch {
    removeGameItem(key);
    return null;
  }
  removeGameItem(key);
  return null;
};

export const gameStorage = {
  setPlayerId: (id: string): void => {
    writeGameItem(PLAYER_ID_KEY, id);
  },

  getPlayerId: (): string | null => getUUIDItem(PLAYER_ID_KEY),

  setUsername: (username: string): void => {
    writeGameItem(USERNAME_KEY, username);
  },

  getUsername: (): string | null =>
    readGameItem(USERNAME_KEY),

  clearPlayerSession: (): void => {
    removeGameItem(PLAYER_ID_KEY);
    removeGameItem(SESSION_TOKEN_KEY);
    removeGameItem(USERNAME_KEY);
  },

  setGameData: (data: GameData): void => {
    writeGameItem(CURRENT_GAME_KEY, JSON.stringify(data));
  },

  getGameData: (): GameData | null =>
    parseStoredJSON(CURRENT_GAME_KEY, isGameData),

  setGameResult: (result: GameResult): void => {
    writeGameItem(GAME_RESULT_KEY, JSON.stringify(result));
  },

  getGameResult: (): GameResult | null =>
    parseStoredJSON(GAME_RESULT_KEY, isGameResult),

  clearGameResult: (): void => {
    removeGameItem(GAME_RESULT_KEY);
  },

  clearCurrentGame: (): void => {
    removeGameItem(CURRENT_GAME_KEY);
  },

  clearGameData: (): void => {
    removeGameItem(GAME_RESULT_KEY);
    removeGameItem(CURRENT_GAME_KEY);
  },
};

export const redirectNotificationStorage = {
  set: (message: string): void => {
    writeItem(
      browserSessionStorage(),
      sessionStorageFallback,
      REDIRECT_NOTIFICATION_KEY,
      message,
    );
  },

  consume: (): string | null => {
    const storage = browserSessionStorage();
    const message = readItem(
      storage,
      sessionStorageFallback,
      REDIRECT_NOTIFICATION_KEY,
    );
    removeItem(storage, sessionStorageFallback, REDIRECT_NOTIFICATION_KEY);
    return message;
  },
};
