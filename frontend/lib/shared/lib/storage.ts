import { isUUID } from "./validation";

const PLAYER_ID_KEY = "player_id";
const USERNAME_KEY = "username";
const sessionStorageFallback = new Map<string, string>();

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

const readItem = (key: string): string | null => {
  const storage = browserSessionStorage();
  if (!storage) {
    return sessionStorageFallback.get(key) || null;
  }
  try {
    return storage.getItem(key) || null;
  } catch {
    return sessionStorageFallback.get(key) || null;
  }
};

const writeItem = (key: string, value: string): void => {
  const storage = browserSessionStorage();
  if (!storage) {
    sessionStorageFallback.set(key, value);
    return;
  }
  try {
    storage.setItem(key, value);
    sessionStorageFallback.delete(key);
  } catch {
    sessionStorageFallback.set(key, value);
  }
};

const removeItem = (key: string): void => {
  sessionStorageFallback.delete(key);
  try {
    browserSessionStorage()?.removeItem(key);
  } catch {
    // Browser storage cleanup is best effort.
  }
};

export const playerStorage = {
  setPlayerId: (id: string): void => {
    writeItem(PLAYER_ID_KEY, id);
  },

  getPlayerId: (): string | null => {
    const value = readItem(PLAYER_ID_KEY);
    if (!value) {
      return null;
    }
    if (isUUID(value)) {
      return value;
    }
    removeItem(PLAYER_ID_KEY);
    return null;
  },

  setUsername: (username: string): void => {
    writeItem(USERNAME_KEY, username);
  },

  getUsername: (): string | null => readItem(USERNAME_KEY),

  clearSession: (): void => {
    removeItem(PLAYER_ID_KEY);
    removeItem(USERNAME_KEY);
  },
};
