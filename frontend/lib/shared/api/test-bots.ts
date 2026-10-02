import { credentialedFetch, adminCredentialedFetch } from "./client";
import { CONFIG } from "../config";
import { isUUID } from "../lib/validation";

export type BotPolicy = Readonly<{ outcome: "human_wins" | "bot_wins"; delay: number }>;
export type BotView = Readonly<{
  available: true; tournament_id: string; status: string; message: string;
  size: number; scenario: string; next: BotPolicy;
  bots: readonly { username: string; player_id: string; participant_id: string; slot: number; status: string; message: string }[];
}>;
export type BotAction = Readonly<{ action: "start" | "pause" | "resume" | "configure"; scenario?: "free" | "golden"; outcome?: BotPolicy["outcome"]; delay?: number }>;
export type BotPair = Readonly<{ first_participant_id: string; second_participant_id: string }>;
const record = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value);
function parseView(value: unknown, id: string): BotView {
  if (!record(value) || value.available !== true || value.tournament_id !== id || typeof value.status !== "string" || typeof value.message !== "string" || !Number.isInteger(value.size) || typeof value.scenario !== "string" || !record(value.next) || !["human_wins", "bot_wins"].includes(String(value.next.outcome)) || !Number.isInteger(value.next.delay) || !Array.isArray(value.bots) || value.bots.length > 15 || !value.bots.every((bot: unknown) => record(bot) && typeof bot.username === "string" && typeof bot.player_id === "string" && isUUID(bot.player_id) && typeof bot.participant_id === "string" && Number.isInteger(bot.slot) && typeof bot.status === "string" && typeof bot.message === "string")) throw new Error("Некорректный ответ тестового сервиса.");
  return value as BotView;
}
function path(id: string, route: string): string {
  if (!isUUID(id)) throw new Error("Некорректное соревнование.");
  return `${CONFIG.apiUrl}/api/v1/players/test-bots/${id}/${route}`;
}
async function result(response: Response): Promise<unknown> {
  const value: unknown = await response.json().catch(() => null);
  if (!response.ok) throw new Error(record(value) && typeof value.message === "string" ? value.message : "Тестовый сервис недоступен.");
  return value;
}
export async function getBotState(id: string, signal: AbortSignal): Promise<BotView | null> {
  const response = await credentialedFetch(path(id, "state"), { signal, cache: "no-store" });
  if ([401, 403, 404].includes(response.status)) return null;
  return parseView(await result(response), id);
}
export async function botAction(id: string, action: BotAction, signal: AbortSignal): Promise<BotView> {
  return parseView(await result(await credentialedFetch(path(id, "actions"), { method: "POST", signal, headers: { "Content-Type": "application/json" }, body: JSON.stringify(action) })), id);
}
export async function getBotAnswer(id: string, signal: AbortSignal): Promise<string> {
  const value = await result(await credentialedFetch(path(id, "answer"), { method: "POST", signal, cache: "no-store" }));
  if (!record(value) || typeof value.answer !== "string" || value.answer.length > 4096) throw new Error("Ответ недоступен.");
  return value.answer;
}
export async function getBotPairings(id: string, round: number, signal: AbortSignal): Promise<BotPair[] | null> {
  if (!isUUID(id)) return null;
  const response = await adminCredentialedFetch(`${CONFIG.adminApiUrl}/api/v1/admin/test-bots/${id}/pairings?round=${round}`, { signal, cache: "no-store" });
  if ([401, 403, 404, 409].includes(response.status)) return null;
  const value = await result(response);
  if (!record(value) || !Array.isArray(value.pairs) || !value.pairs.every((pair: unknown) => record(pair) && typeof pair.first_participant_id === "string" && isUUID(pair.first_participant_id) && typeof pair.second_participant_id === "string" && isUUID(pair.second_participant_id))) throw new Error("Некорректные пары сценария.");
  return value.pairs as BotPair[];
}
