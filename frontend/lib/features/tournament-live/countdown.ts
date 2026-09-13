export type MonotonicClock = () => number;

export type CountdownStatus = "running" | "awaiting_server";

export type ServerCountdown = Readonly<{
  serverTimestampMs: number;
  deadlineMs: number;
  receivedAtMonotonicMs: number;
  initialRemainingMs: number;
  status: CountdownStatus;
}>;

export type CountdownView = Readonly<{
  remainingMs: number;
  status: CountdownStatus;
  commandsEnabled: boolean;
}>;

export type ServerCountdownInput = Readonly<{
  serverTimestamp: string | number | Date;
  deadline: string | number | Date;
  receivedAtMonotonicMs?: number;
}>;

const finiteNumber = (value: number, label: string): number => {
  if (!Number.isFinite(value)) {
    throw new TypeError(`${label} must be finite`);
  }
  return value;
};

const timestampMs = (value: string | number | Date, label: string): number => {
  if (value instanceof Date) {
    return finiteNumber(value.getTime(), label);
  }
  if (typeof value === "number") {
    return finiteNumber(value, label);
  }
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) {
    throw new TypeError(`${label} must be a valid timestamp`);
  }
  return parsed;
};

/** Read the browser monotonic clock. It is intentionally not Date.now(). */
export const readMonotonicNow: MonotonicClock = (): number => {
  if (typeof performance === "undefined") {
    return 0;
  }
  return performance.now();
};

export const createServerCountdown = (
  input: ServerCountdownInput,
  monotonicNow: MonotonicClock = readMonotonicNow,
): ServerCountdown => {
  const serverTimestampMs = timestampMs(input.serverTimestamp, "serverTimestamp");
  const deadlineMs = timestampMs(input.deadline, "deadline");
  const receivedAtMonotonicMs = finiteNumber(
    input.receivedAtMonotonicMs ?? monotonicNow(),
    "receivedAtMonotonicMs",
  );
  const initialRemainingMs = Math.max(0, deadlineMs - serverTimestampMs);

  return Object.freeze({
    serverTimestampMs,
    deadlineMs,
    receivedAtMonotonicMs,
    initialRemainingMs,
    status: initialRemainingMs === 0 ? "awaiting_server" : "running",
  });
};

export const remainingMsAt = (
  countdown: ServerCountdown,
  monotonicTime: number,
): number => {
  if (countdown.status === "awaiting_server") {
    return 0;
  }
  const elapsed = Math.max(0, finiteNumber(monotonicTime, "monotonicTime") - countdown.receivedAtMonotonicMs);
  return Math.max(0, countdown.initialRemainingMs - elapsed);
};

export const viewServerCountdown = (
  countdown: ServerCountdown,
  monotonicTime: number,
): CountdownView => {
  const remainingMs = remainingMsAt(countdown, monotonicTime);
  const status: CountdownStatus = remainingMs === 0 ? "awaiting_server" : "running";
  return Object.freeze({
    remainingMs,
    status,
    commandsEnabled: status === "running",
  });
};

export const formatCountdown = (remainingMs: number): string => {
  const seconds = Math.ceil(Math.max(0, remainingMs) / 1000);
  const minutesPart = Math.floor(seconds / 60);
  const secondsPart = seconds % 60;
  return `${minutesPart}:${String(secondsPart).padStart(2, "0")}`;
};
