import { expect, test, type Page, type Route } from "@playwright/test";

import { tournamentFixturePage } from "./fixture-page";
import {
  assertTournamentFixtureSet,
  createTournamentFixtureSet,
  tournamentFixtureIds,
} from "./fixtures";

const frontendPort = process.env.E2E_FRONTEND_PORT || "3101";
const baseURL = process.env.E2E_FRONTEND_URL || `http://127.0.0.1:${frontendPort}`;
const baseOrigin = new URL(baseURL).origin;

type NetworkEvidence = {
  apiPaths: string[];
  authorizationHeaders: string[];
  externalRequests: string[];
  unhandledApi: string[];
};

const endpoint = (pathname: string): string => new URL(pathname, `${baseOrigin}/`).toString();

const observeNetwork = async (page: Page): Promise<NetworkEvidence> => {
  const evidence: NetworkEvidence = {
    apiPaths: [],
    authorizationHeaders: [],
    externalRequests: [],
    unhandledApi: [],
  };

  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname.startsWith("/api/")) {
      evidence.apiPaths.push(url.pathname);
      const authorization = request.headers().authorization;
      if (authorization !== undefined) {
        evidence.authorizationHeaders.push(authorization);
      }
      return;
    }
    if ((url.protocol === "http:" || url.protocol === "https:") && url.origin !== baseOrigin) {
      evidence.externalRequests.push(request.url());
    }
  });

  await page.route("**/api/**", async (route) => {
    evidence.unhandledApi.push(route.request().url());
    await route.abort("blockedbyclient");
  });
  return evidence;
};

const fulfillJSON = async (route: Route, body: unknown, status = 200): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify(body),
    headers: { "content-type": "application/json" },
    status,
  });
};

const fulfillProblem = async (route: Route, status: number, title: string): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify({
      detail: title,
      status,
      title,
      type: "about:blank",
    }),
    headers: {
      "content-type": "application/problem+json",
      "retry-after": "3",
    },
    status,
  });
};

const openFixture = async (page: Page): Promise<void> => {
  await page.route(baseURL, async (route) => {
    await route.fulfill({
      body: "<!doctype html><html lang=\"ru\"><body></body></html>",
      contentType: "text/html",
      status: 200,
    });
  });
  await page.goto(baseURL, { waitUntil: "domcontentloaded" });
  await page.setContent(tournamentFixturePage(baseURL), { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("heading", { name: "Один контракт. Три границы." })).toBeVisible();
};

const expectNoLeakage = (evidence: NetworkEvidence): void => {
  expect(evidence.authorizationHeaders).toEqual([]);
  expect(evidence.externalRequests).toEqual([]);
  expect(evidence.unhandledApi).toEqual([]);
};

test("public role keeps the projection public and all REST calls intercepted", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const evidence = await observeNetwork(page);
  await page.route(endpoint(`/api/v1/tournaments/${tournamentFixtureIds.tournament}`), async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.public.tournament);
  });
  await page.route(endpoint(`/api/v1/tournaments/${tournamentFixtureIds.tournament}/scoreboard`), async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.public.scoreboard);
  });
  await page.route(endpoint(`/api/v1/tournaments/${tournamentFixtureIds.tournament}/bracket`), async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.public.bracket);
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Показать проекцию" }).click();
  await expect(page.getByLabel("Результат вызова")).toContainText("Public projection: 3 ответ(а), revision 9");

  expect(evidence.apiPaths).toEqual([
    `/api/v1/tournaments/${tournamentFixtureIds.tournament}`,
    `/api/v1/tournaments/${tournamentFixtureIds.tournament}/scoreboard`,
    `/api/v1/tournaments/${tournamentFixtureIds.tournament}/bracket`,
  ]);
  expect(evidence.apiPaths.every((path) => !path.includes("/admin/") && !path.includes("/participant/"))).toBe(true);
  expect(JSON.stringify(fixtureSet.public.recovery)).not.toMatch(/participant_id|task_id|flag|secret/i);
  expectNoLeakage(evidence);
});

test("participant role keeps its API boundary and can read the generated Golden DTO", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const evidence = await observeNetwork(page);
  await page.route(endpoint(`/api/v1/tournaments/${tournamentFixtureIds.tournament}/participant/lobby`), async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.participant.lobby);
  });
  await page.route(endpoint(`/api/v1/tournaments/${tournamentFixtureIds.tournament}/participant/assignments/${tournamentFixtureIds.assignment}`), async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.participant.assignment);
  });
  await page.route(endpoint(`/api/v1/tournaments/${tournamentFixtureIds.tournament}/participant/snapshot`), async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.participant.recovery);
  });
  await page.route(endpoint(`/api/v1/tournaments/${tournamentFixtureIds.tournament}/participant/golden`), async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.golden.participant);
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Показать сессию" }).click();
  await expect(page.getByLabel("Результат вызова")).toContainText("Participant session: 3 ответ(а), revision 9");
  await page.getByRole("button", { name: "Показать Golden" }).click();
  await expect(page.getByLabel("Результат вызова")).toContainText("Participant Golden: 1 ответ(а), revision 9");

  expect(evidence.apiPaths).toEqual(expect.arrayContaining([
    `/api/v1/tournaments/${tournamentFixtureIds.tournament}/participant/lobby`,
    `/api/v1/tournaments/${tournamentFixtureIds.tournament}/participant/assignments/${tournamentFixtureIds.assignment}`,
    `/api/v1/tournaments/${tournamentFixtureIds.tournament}/participant/snapshot`,
    `/api/v1/tournaments/${tournamentFixtureIds.tournament}/participant/golden`,
  ]));
  expect(evidence.apiPaths).toHaveLength(4);
  expect(evidence.apiPaths.every((path) => path.includes("/participant/"))).toBe(true);
  expectNoLeakage(evidence);
});

test("operator role keeps admin-only API boundary and receives BO1, BO3, pause and Golden data", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const evidence = await observeNetwork(page);
  await page.route(endpoint(`/api/v1/admin/tournaments/${tournamentFixtureIds.tournament}/snapshot`), async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.operator.snapshot);
  });
  await page.route(endpoint(`/api/v1/admin/tournaments/${tournamentFixtureIds.tournament}/golden`), async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.golden.operator);
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Показать пульт" }).click();
  await expect(page.getByLabel("Результат вызова")).toContainText("Operator console: 2 ответ(а), revision 9");

  expect(evidence.apiPaths).toEqual(expect.arrayContaining([
    `/api/v1/admin/tournaments/${tournamentFixtureIds.tournament}/snapshot`,
    `/api/v1/admin/tournaments/${tournamentFixtureIds.tournament}/golden`,
  ]));
  expect(evidence.apiPaths).toHaveLength(2);
  expect(evidence.apiPaths.every((path) => path.includes("/admin/"))).toBe(true);
  expect(fixtureSet.operator.snapshot.pause_graph?.deadlines_suppressed).toBe(true);
  expect(fixtureSet.golden.operator.groups[0]?.runtime_revision).toBe(4);
  expectNoLeakage(evidence);
});

test("controlled HTTP conflict stays inside the participant fixture", async ({ page }) => {
  const evidence = await observeNetwork(page);
  const snapshotPath = `/api/v1/tournaments/${tournamentFixtureIds.tournament}/participant/snapshot`;
  await page.route(endpoint(snapshotPath), async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillProblem(route, 409, "Снимок устарел");
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Проверить HTTP 409" }).click();
  await expect(page.getByLabel("Результат вызова")).toContainText("Participant conflict: HTTP 409");
  expect(evidence.apiPaths).toEqual([snapshotPath]);
  expectNoLeakage(evidence);
});

test("controlled WebSocket close and malformed frame are observed for every role", async ({ page }) => {
  const evidence = await observeNetwork(page);
  const websocketURLs: string[] = [];
  let routedWebSockets = 0;
  await page.context().routeWebSocket(
    (url) => url.pathname.startsWith("/api/v1/") && url.pathname.endsWith("/realtime"),
    async (socket) => {
      routedWebSockets += 1;
      websocketURLs.push(socket.url());
      socket.send("not-json");
      await socket.close({ code: 1013, reason: "fixture controlled" });
    },
  );

  await openFixture(page);
  await page.locator('[data-role-action="participant-realtime"]').click();
  await expect(page.getByLabel("Результат вызова")).toContainText("WS ошибка: закрытие 1013");
  await expect.poll(() => routedWebSockets).toBe(1);
  await page.locator('[data-role-action="operator-realtime"]').click();
  await expect.poll(() => routedWebSockets).toBe(2);
  await page.locator('[data-role-action="public-realtime"]').click();
  await expect.poll(() => routedWebSockets).toBe(3);
  await expect.poll(() => websocketURLs.length).toBe(3);

  const websocketPaths = websocketURLs.map((value) => new URL(value).pathname);
  expect(websocketPaths).toEqual(expect.arrayContaining([
    `/api/v1/tournaments/${tournamentFixtureIds.tournament}/realtime`,
    `/api/v1/tournaments/${tournamentFixtureIds.tournament}/participant/realtime`,
    `/api/v1/admin/tournaments/${tournamentFixtureIds.tournament}/realtime`,
  ]));
  expect(websocketURLs.every((value) => {
    const url = new URL(value);
    return url.searchParams.has("resume_id") &&
      !url.searchParams.has("token") &&
      !url.searchParams.has("session_id") &&
      !url.searchParams.has("player_id");
  })).toBe(true);
  expect(evidence.apiPaths).toEqual([]);
  expectNoLeakage(evidence);
});

test("invalid fixture data fails closed with an explicit validator error", () => {
  const valid = createTournamentFixtureSet();
  const invalid: unknown = {
    ...valid,
    public: {
      ...valid.public,
      scoreboard: {
        ...valid.public.scoreboard,
        projection_revision: valid.public.scoreboard.projection_revision + 1,
      },
    },
  };

  expect(() => assertTournamentFixtureSet(invalid)).toThrow("Invalid tournament fixture set");
});

test("both themes remain usable without document overflow at 390 and 1440 pixels", async ({ page }) => {
  await openFixture(page);
  for (const width of [390, 1440]) {
    await page.setViewportSize({ height: 900, width });
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.getByRole("button", { name: "Светлая" }).click();
    await expect.poll(() => page.evaluate(() => document.documentElement.dataset.theme)).toBe("light");
    await page.getByRole("button", { name: "Темная" }).click();
    await expect.poll(() => page.evaluate(() => document.documentElement.dataset.theme)).toBe("dark");
    await expect(page.getByRole("heading", { name: "Один контракт. Три границы." })).toBeVisible();
  }
});

test("production app does not expose a tournament fixture management route", async ({ request }) => {
  const response = await request.get(new URL("/__tournament-fixtures", `${baseOrigin}/`).toString());
  expect(response.status()).toBe(404);
});
