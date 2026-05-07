import type { components } from "./schema";
import { isUUID } from "../lib/validation";

type ActiveDuelInfo = components["schemas"]["ActiveDuelInfo"];
type AdminPlayer = components["schemas"]["AdminPlayerResponse"];
type AdminPlayerAuditEvent = components["schemas"]["AdminPlayerAuditEventResponse"];
type AdminPlayerAuditState = components["schemas"]["AdminPlayerAuditState"];
type AdminTask = components["schemas"]["TaskResponse"];
type AdminTokenResponse = components["schemas"]["AdminTokenResponse"];
type JoinResponse = components["schemas"]["JoinResponse"];
type LeaderboardEntry = components["schemas"]["LeaderboardEntry"];
type LeaderboardResponse = components["schemas"]["LeaderboardResponse"];
type PlayerMeResponse = components["schemas"]["PlayerMeResponse"];
type PlayerResponse = components["schemas"]["PlayerResponse"];
type UploadSourceResponse = components["schemas"]["UploadSourceResponse"];

type Guard<T> = (value: unknown) => value is T;

const TASK_CATEGORIES = new Set<AdminTask["category"]>([
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

const TASK_DIFFICULTIES = new Set<AdminTask["difficulty"]>([
  "easy",
  "medium",
  "hard",
]);

const PLAYER_STATUSES = new Set<PlayerResponse["status"]>([
  "idle",
  "queued",
  "in_duel",
]);

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === "object";

const isString = (value: unknown): value is string => typeof value === "string";

const isDateString = (value: unknown): value is string =>
  isString(value) && Number.isFinite(Date.parse(value));

const isOptionalDateStringOrNull = (value: unknown): value is string | null | undefined =>
  value === undefined || value === null || isDateString(value);

const isNumber = (value: unknown): value is number =>
  typeof value === "number" && Number.isFinite(value);

const isInteger = (value: unknown): value is number =>
  isNumber(value) && Number.isInteger(value);

const isPositiveInteger = (value: unknown): value is number => isInteger(value) && value > 0;

const isNonNegativeInteger = (value: unknown): value is number =>
  isInteger(value) && value >= 0;

const isOptionalStringOrNull = (value: unknown): value is string | null | undefined =>
  value === undefined || value === null || isString(value);

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

const isThreeNonEmptyStringArray = (value: unknown): value is [string, string, string] =>
  Array.isArray(value) &&
  value.length === 3 &&
  value.every((item) => isString(item) && item.trim().length > 0);

export class ApiContractError extends Error {
  constructor(contract: string) {
    super(`Invalid API response: ${contract}`);
    this.name = "ApiContractError";
  }
}

export const assertApiResponse = <T>(
  value: unknown,
  guard: Guard<T>,
  contract: string,
): T => {
  if (!guard(value)) {
    throw new ApiContractError(contract);
  }
  return value;
};

export const isJoinResponse = (value: unknown): value is JoinResponse =>
  isRecord(value) && isUUID(value.player_id) && isUUID(value.session_token);

const isActiveDuelInfo = (value: unknown): value is ActiveDuelInfo =>
  isRecord(value) &&
  isUUID(value.id) &&
  value.status === "active" &&
  isDateString(value.deadline) &&
  isDateString(value.started_at);

const isPlayerResponse = (value: unknown): value is PlayerResponse =>
  isRecord(value) &&
  isUUID(value.id) &&
  isString(value.username) &&
  isString(value.status) &&
  PLAYER_STATUSES.has(value.status as PlayerResponse["status"]) &&
  isDateString(value.created_at);

export const isPlayerMeResponse = (value: unknown): value is PlayerMeResponse =>
  isRecord(value) &&
  isPlayerResponse(value.player) &&
  (value.active_duel === undefined || isActiveDuelInfo(value.active_duel));

const isLeaderboardEntry = (value: unknown): value is LeaderboardEntry =>
  isRecord(value) &&
  isPositiveInteger(value.rank) &&
  isString(value.username) &&
  isNonNegativeInteger(value.wins) &&
  isNonNegativeInteger(value.average_solve_time_ms);

export const isLeaderboardResponse = (value: unknown): value is LeaderboardResponse =>
  isRecord(value) && Array.isArray(value.entries) && value.entries.every(isLeaderboardEntry);

export const isAdminTokenResponse = (value: unknown): value is AdminTokenResponse =>
  isRecord(value) &&
  isString(value.access_token) &&
  isString(value.refresh_token) &&
  value.token_type === "Bearer" &&
  isPositiveInteger(value.expires_in);

export const isAdminTask = (value: unknown): value is AdminTask =>
  isRecord(value) &&
  isUUID(value.id) &&
  isString(value.title) &&
  isString(value.description) &&
  isString(value.category) &&
  TASK_CATEGORIES.has(value.category as AdminTask["category"]) &&
  isString(value.difficulty) &&
  TASK_DIFFICULTIES.has(value.difficulty as AdminTask["difficulty"]) &&
  isPositiveInteger(value.time_limit) &&
  isString(value.flag) &&
  isThreeNonEmptyStringArray(value.hints) &&
  isDateString(value.created_at) &&
  isOptionalStringOrNull(value.task_url) &&
  isOptionalHttpURLOrNull(value.source_file_url);

export const isAdminTaskArray = (value: unknown): value is AdminTask[] =>
  Array.isArray(value) && value.every(isAdminTask);

export const isAdminPlayer = (value: unknown): value is AdminPlayer =>
  isRecord(value) &&
  isUUID(value.id) &&
  isString(value.username) &&
  isString(value.status) &&
  PLAYER_STATUSES.has(value.status as AdminPlayer["status"]) &&
  isDateString(value.created_at) &&
  isOptionalDateStringOrNull(value.deleted_at) &&
  isNonNegativeInteger(value.wins) &&
  isNonNegativeInteger(value.average_solve_time_ms) &&
  typeof value.stats_overridden === "boolean";

export const isAdminPlayerArray = (value: unknown): value is AdminPlayer[] =>
  Array.isArray(value) && value.every(isAdminPlayer);

const isAdminPlayerAuditState = (value: unknown): value is AdminPlayerAuditState =>
  isRecord(value) &&
  isString(value.username) &&
  isString(value.status) &&
  PLAYER_STATUSES.has(value.status as AdminPlayerAuditState["status"]) &&
  isNonNegativeInteger(value.wins) &&
  isNonNegativeInteger(value.average_solve_time_ms) &&
  typeof value.stats_overridden === "boolean" &&
  typeof value.deleted === "boolean";

const isAdminPlayerAuditEvent = (value: unknown): value is AdminPlayerAuditEvent =>
  isRecord(value) &&
  isUUID(value.id) &&
  isString(value.actor_subject) &&
  isString(value.actor_jti) &&
  (value.action === "update" || value.action === "delete") &&
  isUUID(value.player_id) &&
  isAdminPlayerAuditState(value.before_state) &&
  isAdminPlayerAuditState(value.after_state) &&
  isDateString(value.created_at);

export const isAdminPlayerAuditEventArray = (value: unknown): value is AdminPlayerAuditEvent[] =>
  Array.isArray(value) && value.every(isAdminPlayerAuditEvent);

export const isUploadSourceResponse = (value: unknown): value is UploadSourceResponse =>
  isRecord(value) && isHttpURL(value.source_file_url);
