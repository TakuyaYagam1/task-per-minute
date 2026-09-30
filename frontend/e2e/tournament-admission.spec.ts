import { expect, test, type Page, type Route } from "@playwright/test";

import type { components } from "../lib/shared/api/schema";
import { createTournamentFixtureSet, tournamentFixtureIds } from "./tournament/fixtures";

test.use({ trace: "off" });

const tournamentId = tournamentFixtureIds.tournament;
const publicId = "september-contour";
const detailPath = `/api/v1/public/tournaments/${publicId}`;
const playerMePath = "/api/v1/players/me";
const queuePath = `/api/v1/tournaments/${tournamentId}/participant/queue`;
const playerId = "00000000-0000-4000-8000-000000000210";
const participantId = tournamentFixtureIds.firstParticipant;

type AdmissionStatus = "not_registered" | "invited" | "registered" | "checked_in" | "withdrawn";
type CatalogItem = components["schemas"]["PublicTournamentCatalogItem"];
type ConflictReason = "full" | "closed" | "conflicting_reservation";

const jsonHeaders = { "content-type": "application/json" };

const catalogItem: CatalogItem = {
  created_at: "2026-09-13T10:00:00Z",
  finished_at: null,
  group: "upcoming",
  name: "Сентябрьский контур",
  planned_roster_size: 16,
  preset: "tournament_v1",
  public_id: publicId,
  roster_size: 4,
  scheduled_at: null,
  stage: "registration",
  started_at: null,
  state: "registration",
  tournament_id: tournamentId,
};

const liveCatalogItem: CatalogItem = {
  ...catalogItem,
  group: "live",
  stage: "swiss",
  started_at: "2026-09-13T10:05:00Z",
  state: "swiss",
};

const admissionView = (
  status: AdmissionStatus,
  overrides: Partial<{
    participant_id: string | null;
    player_id: string;
    roster_locked: boolean;
    roster_size: number;
    seed: number | null;
  }> = {},
): components["schemas"]["TournamentAdmissionView"] => {
  const registered = status !== "not_registered";
  return {
    attendance: registered ? status : null,
    participant_id: registered ? participantId : null,
    planned_roster_size: 16,
    player_id: playerId,
    roster_locked: false,
    roster_revision: 1,
    roster_size: 4,
    seed: registered ? 3 : null,
    status,
    tournament_id: tournamentId,
    tournament_state: "registration" as const,
    ...overrides,
  } as components["schemas"]["TournamentAdmissionView"];
};

const playerMe = {
  player: {
    created_at: "2026-09-13T09:00:00Z",
    id: playerId,
    username: "alice_01",
  },
};

const fulfillJSON = async (
  route: Route,
  body: unknown,
  status = 200,
  extraHeaders = {},
): Promise<void> => {
  await route.fulfill({
    status,
    headers: { ...jsonHeaders, ...extraHeaders },
    body: JSON.stringify(body),
  });
};

const fulfillProblem = async (
  route: Route,
  status: number,
  detail: string,
  extra: Record<string, unknown> = {},
): Promise<void> => {
  await route.fulfill({
    status,
    headers: { "content-type": "application/problem+json" },
    body: JSON.stringify({
      detail,
      status,
      title: status === 429 ? "Too Many Requests" : "Request rejected",
      type: "about:blank",
      ...extra,
    }),
  });
};

const installDetail = async (page: Page, item = catalogItem): Promise<void> => {
  await page.route(`**${detailPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, item);
  });
  if (item.group === "live") {
    const fixtureSet = createTournamentFixtureSet();
    await page.route(`**/api/v1/tournaments/${tournamentId}/snapshot*`, async (route) => {
      await fulfillJSON(route, fixtureSet.public.recovery);
    });
  }
};

const installStoredSession = async (page: Page): Promise<void> => {
  await page.addInitScript(({ id }) => {
    window.sessionStorage.setItem("player_id", id);
    window.sessionStorage.setItem("username", "alice_01");
    document.cookie = "tpm_player_csrf=admission-csrf; Path=/";
  }, { id: playerId });
};

const installCookieSession = async (page: Page): Promise<void> => {
  await page.addInitScript(() => {
    document.cookie = "tpm_player_csrf=admission-csrf; Path=/";
  });
};

const installPlayerMe = async (page: Page, status = 200): Promise<void> => {
  await page.route(`**${playerMePath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    if (status !== 200) {
      await fulfillProblem(route, status, status === 401 ? "Session expired" : "Access denied");
      return;
    }
    await fulfillJSON(route, playerMe, 200, { "X-CSRF-Token": "admission-csrf" });
  });
};

const detailURL = (): string =>
  `/arena/tournaments/${publicId}?view=overview&return=${encodeURIComponent(
    "/arena?group=live",
  )}`;

test.beforeEach(async ({ page }) => {
  await page.route("**/api/v1/players/notifications", async (route) => {
    await fulfillJSON(route, { notifications: [] });
  });
  await page.route("**/api/v1/players/notifications/events", async (route) => {
    await route.fulfill({ status: 204, body: "" });
  });
});

test("anonymous detail sends players to account pages and keeps queue idle", async ({ page }) => {
  await installPlayerMe(page, 401);
  await installDetail(page);
  let queueGets = 0;
  let queuePosts = 0;
  await page.route(`**${queuePath}`, async (route) => {
    expect(route.request().headers().authorization).toBeUndefined();
    if (route.request().method() === "GET") {
      queueGets += 1;
    } else {
      queuePosts += 1;
    }
    await route.abort();
  });

  await page.goto(detailURL());
  await expect(page.getByRole("heading", { name: "Сентябрьский контур", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Участие в соревновании", exact: true })).toBeVisible();
  const catalogLinks = page.getByRole("link", { name: "К списку соревнований", exact: true });
  await expect(catalogLinks).toHaveCount(2);
  await expect(catalogLinks.nth(0)).toHaveAttribute("href", "/arena?group=live");
  await expect(catalogLinks.nth(1)).toHaveAttribute("href", "/arena?group=live");
  await expect(page.getByRole("link", { name: "Войти и участвовать", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Создать аккаунт", exact: true })).toBeVisible();
  const loginHref = await page.getByRole("link", { name: "Войти и участвовать", exact: true }).getAttribute("href");
  expect(new URL(loginHref ?? "", page.url()).pathname).toBe("/login");
  expect(new URL(loginHref ?? "", page.url()).searchParams.get("next")).toBe(detailURL());
  expect(queueGets).toBe(0);
  expect(queuePosts).toBe(0);
});

test("stored player restores durable registration without a join command", async ({ page }) => {
  await installStoredSession(page);
  await installDetail(page);
  await installPlayerMe(page);
  let queueGets = 0;
  let queuePosts = 0;
  await page.route(`**${queuePath}`, async (route) => {
    if (route.request().method() === "GET") {
      queueGets += 1;
      await fulfillJSON(route, admissionView("registered"));
      return;
    }
    queuePosts += 1;
    await route.continue();
  });

  await page.goto(detailURL());
  await expect(page.getByTestId("tournament-admission-status")).toContainText("Зарегистрирован");
  await expect(page.getByText("alice_01", { exact: true })).toBeVisible();
  expect(queueGets).toBe(1);
  expect(queuePosts).toBe(0);
  await expect(page.getByRole("button", { name: "Участвовать", exact: true })).toHaveCount(0);
});

test("cookie-backed player restores admission when the local session cache is empty", async ({ page }) => {
  await installCookieSession(page);
  await installDetail(page);
  await installPlayerMe(page);
  await page.route(`**${queuePath}`, async (route) => {
    await fulfillJSON(route, admissionView("registered"));
  });

  await page.goto(detailURL());
  await expect(page.getByTestId("tournament-admission-status")).toContainText("Зарегистрирован");
  await expect(page.getByText("alice_01", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Никнейм")).toHaveCount(0);
});

test(
  "checked in status exposes a return-preserving participant link without using seed as queue position",
  async ({ page }) => {
  await installStoredSession(page);
  await installDetail(page, liveCatalogItem);
  await installPlayerMe(page);
  await page.route(`**${queuePath}`, async (route) => {
    await fulfillJSON(route, admissionView("checked_in", { seed: 16 }));
  });

  await page.goto(detailURL());
  await expect(page.getByTestId("tournament-admission-status")).toContainText("Готов");
  await expect(page.getByRole("button", { name: "Проверить матч", exact: true })).toBeVisible();
  const fixtureSet = createTournamentFixtureSet();
  let lobbyCalls = 0;
  await page.route(`**${tournamentId}/participant/lobby`, async (route) => {
    lobbyCalls += 1;
    await fulfillJSON(route, {
      ...fixtureSet.participant.lobby,
      participant_id: participantId,
      tournament_id: tournamentId,
    });
  });
  await page.getByRole("button", { name: "Проверить матч", exact: true }).click();
  const workspaceLink = page.getByRole("link", { name: "Открыть мой матч" });
  await expect(workspaceLink).toHaveAttribute(
    "href",
    new RegExp(`/arena/participant/${tournamentId}\\?return=`),
  );
  const workspaceHref = await workspaceLink.getAttribute("href");
  expect(workspaceHref).not.toBeNull();
  const workspaceURL = new URL(workspaceHref ?? "", "https://arena.local");
  expect(workspaceURL.searchParams.get("return")).toBe(
    `/arena/tournaments/${publicId}?view=overview&return=%2Farena%3Fgroup%3Dlive`,
  );
  await expect(page.getByText(/место в очереди/i)).toHaveCount(0);
  await expect(page.getByText(/seed|позиция в очереди/i)).toHaveCount(0);
  expect(lobbyCalls).toBe(1);
  },
);

test("checked in prestart status waits for publication before offering participant workspace", async ({ page }) => {
  await installStoredSession(page);
  await installDetail(page);
  await installPlayerMe(page);
  await page.route(`**${queuePath}`, async (route) => {
    await fulfillJSON(route, admissionView("checked_in"));
  });

  await page.goto(detailURL());
  await expect(page.getByText("Вы подтвердили участие. Страница матча откроется после старта соревнования.")).toBeVisible();
  await expect(page.getByRole("link", { name: "Открыть мой матч" })).toHaveCount(0);
});

test("checked in player can withdraw before the registration roster is locked", async ({ page }) => {
  await installStoredSession(page);
  await installDetail(page);
  await installPlayerMe(page);
  let deleteCalls = 0;
  await page.route(`**${queuePath}`, async (route) => {
    if (route.request().method() === "GET") {
      await fulfillJSON(route, admissionView("checked_in"));
      return;
    }
    expect(route.request().method()).toBe("DELETE");
    deleteCalls += 1;
    await fulfillJSON(route, { changed: true, view: admissionView("withdrawn") });
  });

  await page.goto(detailURL());
  await expect(page.getByRole("button", { name: "Отменить регистрацию", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Отменить регистрацию", exact: true }).click();
  await expect(page.getByTestId("tournament-admission-status")).toContainText("Регистрация отменена");
  await expect(page.getByRole("button", { name: "Отменить регистрацию", exact: true })).toHaveCount(0);
  expect(deleteCalls).toBe(1);
});

test("checked in status requires a confirmed participant lobby before opening workspace", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  await installStoredSession(page);
  await installDetail(page, liveCatalogItem);
  await installPlayerMe(page);
  await page.route(`**${queuePath}`, async (route) => {
    await fulfillJSON(route, admissionView("checked_in"));
  });
  let lobbyCalls = 0;
  await page.route(`**${tournamentId}/participant/lobby`, async (route) => {
    lobbyCalls += 1;
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, {
      ...fixtureSet.participant.lobby,
      participant_id: participantId,
      tournament_id: tournamentId,
    });
  });

  await page.goto(detailURL());
  await expect(page.getByRole("button", { name: "Проверить матч", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Открыть мой матч" })).toHaveCount(0);
  await page.getByRole("button", { name: "Проверить матч", exact: true }).click();
  await expect(page.getByRole("link", { name: "Открыть мой матч" })).toBeVisible();
  expect(lobbyCalls).toBe(1);
});

test("registered player can cancel and sees the rejoin action", async ({ page }) => {
  await installStoredSession(page);
  await installDetail(page);
  await installPlayerMe(page);
  let deleteCalls = 0;
  let deleteKey = "";
  await page.route(`**${queuePath}`, async (route) => {
    if (route.request().method() === "GET") {
      await fulfillJSON(route, admissionView("registered"));
      return;
    }
    expect(route.request().method()).toBe("DELETE");
    deleteCalls += 1;
    deleteKey = route.request().headers()["idempotency-key"] ?? "";
    expect(route.request().headers()["x-csrf-token"]).toBe("admission-csrf");
    await fulfillJSON(route, { changed: true, view: admissionView("withdrawn") });
  });

  await page.goto(detailURL());
  await expect(page.getByTestId("tournament-admission-status")).toContainText("Зарегистрирован");
  await page.getByRole("button", { name: "Отменить регистрацию", exact: true }).click();
  await expect(page.getByTestId("tournament-admission-status")).toContainText("Регистрация отменена");
  await expect(page.getByText("Регистрация отменена. Пока набор открыт, можно подать заявку повторно.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Подать заявку повторно", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Отменить регистрацию", exact: true })).toHaveCount(0);
  expect(deleteCalls).toBe(1);
  expect(deleteKey).toMatch(/^[0-9a-f-]{36}$/i);
});

test("admission conflict reasons remain distinct and readable", async ({ page }) => {
  await installStoredSession(page);
  await installDetail(page);
  await installPlayerMe(page);
  let reason: ConflictReason = "full";
  await page.route(`**${queuePath}`, async (route) => {
    if (route.request().method() === "GET") {
      await fulfillJSON(route, admissionView("not_registered"));
      return;
    }
    await fulfillProblem(route, 409, "Admission rejected", { reason });
  });

  const expectedTitles: Record<ConflictReason, string> = {
    full: "Состав заполнен",
    closed: "Регистрация закрыта",
    conflicting_reservation: "Участие уже зарезервировано",
  };
  for (const nextReason of ["full", "closed", "conflicting_reservation"] as const) {
    reason = nextReason;
    await page.goto(detailURL());
    await expect(page.getByTestId("tournament-admission-status")).toContainText("Регистрация не оформлена");
    await page.getByRole("button", { name: "Участвовать", exact: true }).click();
    await expect(page.getByTestId("tournament-entry-message")).toContainText(expectedTitles[nextReason]);
  }
});

test("admission rate limiting stays visible and does not fabricate a registration", async ({ page }) => {
  await installStoredSession(page);
  await installDetail(page);
  await installPlayerMe(page);
  await page.route(`**${queuePath}`, async (route) => {
    if (route.request().method() === "GET") {
      await fulfillJSON(route, admissionView("not_registered"));
      return;
    }
    await fulfillProblem(route, 429, "Admission rate limited");
  });

  await page.goto(detailURL());
  await page.getByRole("button", { name: "Участвовать", exact: true }).click();
  await expect(page.getByTestId("tournament-entry-message")).toContainText("Запрос ограничен");
  await expect(page.getByTestId("tournament-admission-status")).toHaveCount(0);
});

test("unauthorized and forbidden admission status remain distinct", async ({ page }) => {
  await installStoredSession(page);
  await installDetail(page);
  await installPlayerMe(page);
  let status: 401 | 403 = 401;
  await page.route(`**${queuePath}`, async (route) => {
    await fulfillProblem(route, status, status === 401 ? "Session expired" : "Access denied");
  });

  await page.goto(detailURL());
  await expect(page.getByTestId("tournament-entry-message")).toContainText("Сессия истекла");
  await expect(page.getByRole("link", { name: "Войти и участвовать", exact: true })).toBeVisible();
  status = 403;
  await page.reload();
  await expect(page.getByTestId("tournament-entry-message")).toContainText("Доступ запрещен");
});

test("wrong admission identity is rejected without exposing a workspace link", async ({ page }) => {
  await installStoredSession(page);
  await installDetail(page);
  await installPlayerMe(page);
  await page.route(`**${queuePath}`, async (route) => {
    await fulfillJSON(route, {
      ...admissionView("checked_in"),
      player_id: "00000000-0000-4000-8000-000000000211",
    });
  });

  await page.goto(detailURL());
  await expect(page.getByTestId("tournament-entry-message")).toContainText("Ошибка данных");
  await expect(page.getByRole("link", { name: "Открыть мой матч" })).toHaveCount(0);
});

test("a stale admission response cannot replace a newer status after navigation", async ({ page }) => {
  await installStoredSession(page);
  await installDetail(page);
  await installPlayerMe(page);
  let queueGets = 0;
  let firstQueueStartedResolve = (): void => {};
  const firstQueueStarted = new Promise<void>((resolve) => {
    firstQueueStartedResolve = resolve;
  });
  let releaseFirstResponse = (): void => {};
  const firstResponseRelease = new Promise<void>((resolve) => {
    releaseFirstResponse = resolve;
  });
  let secondQueueStartedResolve = (): void => {};
  const secondQueueStarted = new Promise<void>((resolve) => {
    secondQueueStartedResolve = resolve;
  });
  await page.route(`**${queuePath}`, async (route) => {
    queueGets += 1;
    if (queueGets === 1) {
      firstQueueStartedResolve();
      await firstResponseRelease;
      try {
        await fulfillJSON(route, admissionView("registered"));
      } catch {
        // The first request is intentionally stale after reload.
      }
      return;
    }
    secondQueueStartedResolve();
    await fulfillJSON(route, admissionView("checked_in"));
  });

  await page.goto(detailURL(), { waitUntil: "domcontentloaded" });
  await firstQueueStarted;
  const reload = page.reload({ waitUntil: "domcontentloaded" });
  await secondQueueStarted;
  releaseFirstResponse();
  await reload;
  await expect(page.getByTestId("tournament-admission-status")).toContainText("Готов");
  expect(queueGets).toBe(2);
});
