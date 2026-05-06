type Level = "error" | "warn" | "info" | "debug";

const enabled = process.env.NODE_ENV !== "production";

const emit = (level: Level, args: unknown[]): void => {
  if (!enabled) {
    return;
  }
  // eslint-disable-next-line no-console
  console[level](...args);
};

export const log = {
  error: (...args: unknown[]): void => emit("error", args),
  warn: (...args: unknown[]): void => emit("warn", args),
  info: (...args: unknown[]): void => emit("info", args),
  debug: (...args: unknown[]): void => emit("debug", args),
};
