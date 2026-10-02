import { expect, test, type Page } from "@playwright/test";
import type { BotView } from "../lib/shared/api/test-bots";

test.use({ trace: "off" });
const id = "11111111-1111-4111-8111-111111111111";
const player = "22222222-2222-4222-8222-222222222222";
const root = `/api/v1/players/test-bots/${id}`;
const idle: BotView = { available: true, tournament_id: id, status: "idle", message: "Можно подключить ботов", size: 0, scenario: "", next: { outcome: "human_wins", delay: 15 }, bots: [] };

async function installTournament(page: Page) {
  await page.route("**/api/**", route => route.fulfill({ status: 404, json: {} }));
  await page.route("**/api/v1/players/me", route => route.fulfill({ json: { player: { id: player, username: "tester", created_at: "2026-10-01T00:00:00Z" } } }));
  await page.route("**/api/v1/players/notifications", route => route.fulfill({ json: { notifications: [] } }));
  await page.route("**/api/v1/players/notifications/events", route => route.fulfill({ status: 204, body: "" }));
  await page.route("**/api/v1/public/tournaments/bot-test", route => route.fulfill({ json: {
    created_at: "2026-10-01T00:00:00Z", finished_at: null, group: "upcoming", name: "Тестовый турнир", planned_roster_size: 8,
    preset: "tournament_v1", public_id: "bot-test", roster_size: 1, scheduled_at: null, stage: "registration", started_at: null, state: "registration", tournament_id: id,
  } }));
  await page.route(`**/api/v1/tournaments/${id}/participant/queue`, route => route.fulfill({ json: {
    attendance: "checked_in", participant_id: player, planned_roster_size: 8, player_id: player, roster_locked: false,
    roster_revision: 1, roster_size: 1, seed: 1, status: "checked_in", tournament_id: id, tournament_state: "registration",
  } }));
  await page.addInitScript(() => { document.cookie = "tpm_player_csrf=test-csrf; Path=/"; });
}

test("test controls stay hidden when the backend denies access", async ({ page }) => {
  await installTournament(page);
  await page.route(`**${root}/state`, route => route.fulfill({ status: 403, json: {} }));
  await page.goto("/arena/tournaments/bot-test");
  await expect(page.getByRole("heading", { name: "Тестовый турнир", exact: true })).toBeVisible();
  await expect(page.getByText("Тестирование", { exact: true })).toHaveCount(0);
});

test("tester controls bots and shows an answer without storing it", async ({ page }) => {
  await installTournament(page);
  let state = idle;
  const actions: Record<string, unknown>[] = [];
  await page.route(`**${root}/state`, route => route.fulfill({ json: state }));
  await page.route(`**${root}/actions`, async route => {
    const command = route.request().postDataJSON() as Record<string, unknown>;
    actions.push(command);
    expect(route.request().headers()["x-csrf-token"]).toBeTruthy();
    state = { ...state, status: command.action === "pause" ? "paused" : "running", size: 8, scenario: "free", message: "Боты готовы" };
    await route.fulfill({ json: state });
  });
  await page.route(`**${root}/answer`, route => route.fulfill({ headers: { "cache-control": "no-store" }, json: { answer: "fixture-answer" } }));
  await page.goto("/arena/tournaments/bot-test");
  await page.getByText("Тестирование", { exact: true }).click();
  await page.getByRole("button", { name: "Заполнить ботами" }).click();
  await expect(page.getByRole("button", { name: "Приостановить" })).toBeVisible();
  await page.getByLabel("Следующая серия").selectOption("bot_wins");
  await page.getByLabel("Задержка, с").fill("20");
  await page.getByRole("button", { name: "Применить к следующей серии" }).click();
  await page.getByRole("button", { name: "Показать тестовый ответ" }).click();
  await expect(page.getByText("fixture-answer", { exact: true })).toBeVisible();
  const stored = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
  expect(stored).not.toContain("fixture-answer");
  await page.getByRole("button", { name: "Приостановить" }).click();
  await expect(page.getByText("fixture-answer", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Продолжить" }).click();
  expect(actions).toEqual([
    { action: "start", scenario: "free" }, { action: "configure", outcome: "bot_wins", delay: 20 }, { action: "pause" }, { action: "resume" },
  ]);
});
