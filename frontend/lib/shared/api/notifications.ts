import { CONFIG } from "../config";
import { isUUID } from "../lib/validation";
import { publicClient, unwrapApi } from "./client";
import { assertApiResponse } from "./guards";
import type { components } from "./schema";

export type PlayerNotification = components["schemas"]["PlayerNotification"];
export type PlayerNotificationsResponse = components["schemas"]["PlayerNotificationsResponse"];

const DATE_TIME_PATTERN =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object";

const isDateTime = (value: unknown): value is string =>
  typeof value === "string" &&
  DATE_TIME_PATTERN.test(value) &&
  Number.isFinite(Date.parse(value));

const isPlayerNotification = (value: unknown): value is PlayerNotification =>
  isRecord(value) &&
  isUUID(value.id) &&
  value.type === "tournament_player_removed" &&
  isUUID(value.tournament_id) &&
  typeof value.tournament_name === "string" &&
  isDateTime(value.created_at) &&
  isDateTime(value.expires_at) &&
  Date.parse(value.expires_at) > Date.parse(value.created_at);

const isPlayerNotificationsResponse = (
  value: unknown,
): value is PlayerNotificationsResponse =>
  isRecord(value) &&
  Array.isArray(value.notifications) &&
  value.notifications.every(isPlayerNotification);

const readNotifications = async (
  signal?: AbortSignal,
): Promise<PlayerNotificationsResponse> => {
  const data = await unwrapApi(
    await publicClient.GET("/api/v1/players/notifications", { signal }),
    "player notifications",
  );
  return assertApiResponse(data, isPlayerNotificationsResponse, "player notifications");
};

const openNotificationEvents = (): EventSource => {
  const baseUrl = CONFIG.apiUrl || window.location.origin;
  const url = new URL("/api/v1/players/notifications/events", baseUrl);
  return new EventSource(url, { withCredentials: true });
};

export const playerNotificationsApi = {
  list: readNotifications,
  openEvents: openNotificationEvents,
} as const;
