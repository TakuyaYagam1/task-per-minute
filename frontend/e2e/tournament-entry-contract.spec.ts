import { expect, test, type Page, type Route } from "@playwright/test";

import { adminSessionResponse } from "./support/admin";
import { createTournamentFixtureSet, tournamentFixtureIds } from "./tournament/fixtures";

const tournamentId = tournamentFixtureIds.tournament;
const publicPath = `/api/v1/tournaments/${tournamentId}`;
const participantPath = `${publicPath}/participant/lobby`;
const operatorPath = `/api/v1/admin/tournaments/${tournamentId}/snapshot`;
const adminRefreshPath = "/api/v1/admin/refresh";
const playerLogoutPath = "/api/v1/players/logout";
const adminLogoutPath = "/api/v1/admin/logout";

type Role = "participant" | "operator" | "spectator";

type NetworkEvidence = Readonly<{
  apiPaths: string[];
  authorizationHeaders: string[];
}>;

const roleURL = (role: Role, query = ""): string =>
  `/arena/${role}/${tournamentId}${query}`;

const fulfillJSON = async (
  route: Route,
  body: unknown,
  status = 200,
): Promise<void> => {
  await route.fulfill({
    status,
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
};

const fulfillProblem = async (
  route: Route,
  status: number,
  title: string,
): Promise<void> => {
  await route.fulfill({
    status,
    headers: { "content-type": "application/problem+json" },
    body: JSON.stringify({
      detail: title,
      status,
      title,
      type: "about:blank",
    }),
  });
};

const observeAPI = async (page: Page): Promise<NetworkEvidence> => {
  const evidence: { apiPaths: string[]; authorizationHeaders: string[] } = {
    apiPaths: [],
    authorizationHeaders: [],
  };

  page.on("request", (request) => {
    const url = new URL(request.url());
    if (!url.pathname.startsWith("/api/")) {
      return;
    }
    evidence.apiPaths.push(url.pathname);
    const authorization = request.headers().authorization;
    if (authorization !== undefined) {
      evidence.authorizationHeaders.push(authorization);
    }
  });

  await page.route("**/api/**", async (route) => {
    await route.abort("blockedbyclient");
  });

  return evidence;
};

const installPublicRoute = async (
  page: Page,
  body: unknown,
  status = 200,
): Promise<void> => {
  await page.route(`**${publicPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    if (status === 200) {
      await fulfillJSON(route, body);
      return;
    }
    await fulfillProblem(route, status, "Турнир не найден");
  });
};

const installParticipantRoute = async (
  page: Page,
  body: unknown,
  status = 200,
): Promise<void> => {
  await page.route(`**${participantPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    if (status === 200) {
      await fulfillJSON(route, body);
      return;
    }
    await fulfillProblem(route, status, status === 401 ? "Требуется вход" : "Доступ запрещен");
  });
};

const installOperatorRoute = async (
  page: Page,
  body: unknown,
  status = 200,
): Promise<void> => {
  await page.route(`**${operatorPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    if (status === 200) {
      await fulfillJSON(route, body);
      return;
    }
    await fulfillProblem(route, status, status === 401 ? "Требуется вход" : "Доступ запрещен");
  });
};

const expectNoAuthorization = (evidence: NetworkEvidence): void => {
  expect(evidence.authorizationHeaders).toEqual([]);
};

const expectOnlyPaths = (
  evidence: NetworkEvidence,
  expectedPaths: readonly string[],
): void => {
  expect(new Set(evidence.apiPaths)).toEqual(new Set(expectedPaths));
};

test("Arena landing links expose direct participant, operator, and spectator routes", async ({ page }) => {
  await page.goto("/arena");

  await page.getByLabel("Идентификатор турнира").fill(tournamentId);

  for (const role of ["participant", "operator", "spectator"] as const) {
    await expect(page.getByRole("link", { name: {
      participant: "Участник",
      operator: "Оператор",
      spectator: "Наблюдатель",
    }[role] })).toHaveAttribute("href", roleURL(role));
  }
});

test("direct participant link uses the participant API boundary", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const evidence = await observeAPI(page);
  await installPublicRoute(page, fixtureSet.public.tournament);
  await installParticipantRoute(page, fixtureSet.participant.lobby);

  await page.goto(roleURL("participant", "?source=share&tab=overview"));

  await expect(page.getByTestId("arena-status")).toContainText("Доступ подтвержден");
  await expect(page.getByTestId("arena-tournament-id")).toHaveText(tournamentId);
  await expect(page.getByRole("heading", { name: /Участник/ })).toBeVisible();

  expectOnlyPaths(evidence, [publicPath, participantPath]);
  expect(evidence.apiPaths.some((path) => path.includes("/admin/"))).toBe(false);
  expectNoAuthorization(evidence);
});

test("direct operator link uses the admin API boundary and shows the tournament name", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const evidence = await observeAPI(page);
  await installPublicRoute(page, fixtureSet.public.tournament);
  await installOperatorRoute(page, fixtureSet.operator.snapshot);

  await page.goto(roleURL("operator", "?source=share&tab=overview"));

  await expect(page.getByTestId("arena-status")).toContainText("Доступ подтвержден");
  await expect(page.getByRole("heading", { name: /Сентябрьский контур.*Оператор/ })).toBeVisible();

  expectOnlyPaths(evidence, [publicPath, operatorPath]);
  expect(evidence.apiPaths.some((path) => path.includes("/participant/"))).toBe(false);
  expectNoAuthorization(evidence);
});

test("direct spectator link remains anonymous and does not call protected endpoints", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const evidence = await observeAPI(page);
  await installPublicRoute(page, fixtureSet.public.tournament);

  await page.goto(roleURL("spectator", "?source=share&tab=overview"));

  await expect(page.getByTestId("arena-status")).toContainText("Доступ подтвержден");
  await expect(page.getByRole("heading", { name: /Наблюдатель/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "Выйти" })).toHaveCount(0);

  expectOnlyPaths(evidence, [publicPath]);
  expectNoAuthorization(evidence);
});

test("successful role links preserve path and query parameters across reload", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const evidence = await observeAPI(page);
  await installPublicRoute(page, fixtureSet.public.tournament);
  await installParticipantRoute(page, fixtureSet.participant.lobby);
  await installOperatorRoute(page, fixtureSet.operator.snapshot);

  for (const role of ["participant", "operator", "spectator"] as const) {
    const directURL = roleURL(role, "?source=share&tab=overview");
    await page.goto(directURL);
    await expect(page.getByTestId("arena-status")).toContainText("Доступ подтвержден");
    const loadedURL = page.url();

    await page.reload({ waitUntil: "domcontentloaded" });

    expect(page.url()).toBe(loadedURL);
    await expect(page.getByTestId("arena-status")).toContainText("Доступ подтвержден");
    await expect(page.getByTestId("arena-tournament-id")).toHaveText(tournamentId);
  }

  expectOnlyPaths(evidence, [publicPath, participantPath, operatorPath]);
  expectNoAuthorization(evidence);
});

test("participant access distinguishes 401 from 403", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const evidence = await observeAPI(page);
  let participantStatus: 401 | 403 = 401;
  await installPublicRoute(page, fixtureSet.public.tournament);
  await page.route(`**${participantPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillProblem(
      route,
      participantStatus,
      participantStatus === 401 ? "Требуется вход" : "Доступ запрещен",
    );
  });

  await page.goto(roleURL("participant"));
  await expect(page.getByText("Требуется вход")).toBeVisible();
  await expect(page.getByRole("link", { name: "Войти как участник" })).toBeVisible();

  participantStatus = 403;
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.locator("strong").filter({ hasText: "Доступ запрещен" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Войти как участник" })).toHaveCount(0);

  expectOnlyPaths(evidence, [publicPath, participantPath]);
  expectNoAuthorization(evidence);
});

test("operator access distinguishes 401 from 403", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const evidence = await observeAPI(page);
  let operatorStatus: 401 | 403 = 401;
  let refreshCalls = 0;
  await installPublicRoute(page, fixtureSet.public.tournament);
  await page.route(`**${operatorPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillProblem(
      route,
      operatorStatus,
      operatorStatus === 401 ? "Требуется вход" : "Доступ запрещен",
    );
  });
  await page.route(`**${adminRefreshPath}`, async (route) => {
    refreshCalls += 1;
    await fulfillProblem(route, 401, "Требуется вход");
  });

  await page.goto(roleURL("operator"));
  await expect(page.getByText("Требуется вход")).toBeVisible();
  await expect(page.getByRole("link", { name: "Войти как оператор" })).toBeVisible();
  await expect.poll(() => refreshCalls).toBeGreaterThan(0);

  operatorStatus = 403;
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.locator("strong").filter({ hasText: "Доступ запрещен" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Войти как оператор" })).toHaveCount(0);

  expectOnlyPaths(evidence, [publicPath, operatorPath, adminRefreshPath]);
  expectNoAuthorization(evidence);
});

test("participant login returns to the requested Arena route", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const returnURL = roleURL("participant", "?source=login");
  await installPublicRoute(page, fixtureSet.public.tournament);
  await installParticipantRoute(page, fixtureSet.participant.lobby);
  await page.route("**/api/v1/players/join", async (route) => {
    await route.fulfill({
      status: 200,
      headers: {
        "content-type": "application/json",
        "X-CSRF-Token": "arena-player-csrf",
      },
      body: JSON.stringify({ player_id: "00000000-0000-4000-8000-000000000210" }),
    });
  });

  await page.goto(`/?next=${encodeURIComponent(returnURL)}`);
  await page.getByPlaceholder("Введите никнейм...").fill("arena_player");
  await page.getByRole("button", { name: "ПОДКЛЮЧИТЬСЯ" }).click();

  await expect(page).toHaveURL(new URL(returnURL, page.url()).toString());
  await expect(page.getByTestId("arena-status")).toContainText("Доступ подтвержден");
});

test("operator login returns to the requested Arena route", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const returnURL = roleURL("operator", "?source=login");
  await installPublicRoute(page, fixtureSet.public.tournament);
  await installOperatorRoute(page, fixtureSet.operator.snapshot);
  await page.route("**/api/v1/admin/login", async (route) => {
    await route.fulfill({
      status: 200,
      headers: {
        "content-type": "application/json",
        "X-CSRF-Token": "arena-admin-access-csrf",
        "X-Admin-Refresh-CSRF-Token": "arena-admin-refresh-csrf",
      },
      body: JSON.stringify(adminSessionResponse()),
    });
  });
  await page.route("**/api/v1/admin/tasks**", async (route) => {
    await fulfillJSON(route, []);
  });

  await page.goto(`/admin?next=${encodeURIComponent(returnURL)}`);
  await page.getByPlaceholder("Введите пароль...").fill("correct-password");
  await page.getByRole("button", { name: "Войти" }).click();

  await expect(page).toHaveURL(new URL(returnURL, page.url()).toString());
  await expect(page.getByTestId("arena-status")).toContainText("Доступ подтвержден");
});

test("participant logout calls the player logout API and clears local session state", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const playerId = "00000000-0000-4000-8000-000000000210";
  const csrfToken = "arena-participant-logout-csrf";
  let logoutCSRF: string | undefined;
  const evidence = await observeAPI(page);

  await page.addInitScript(({ playerId, csrfToken: token }) => {
    window.sessionStorage.setItem("player_id", playerId);
    window.sessionStorage.setItem("username", "arena-player");
    document.cookie = `tpm_player_csrf=${encodeURIComponent(token)}; Path=/`;
  }, { playerId, csrfToken });
  await installPublicRoute(page, fixtureSet.public.tournament);
  await installParticipantRoute(page, fixtureSet.participant.lobby);
  await page.route(`**${playerLogoutPath}`, async (route) => {
    expect(route.request().method()).toBe("POST");
    logoutCSRF = route.request().headers()["x-csrf-token"];
    await route.fulfill({ status: 204, body: "" });
  });

  await page.goto(roleURL("participant"));
  await expect(page.getByRole("button", { name: "Выйти" })).toBeVisible();
  await page.getByRole("button", { name: "Выйти" }).click();

  await expect(page.getByText("Требуется вход")).toBeVisible();
  await expect(page.getByRole("heading", { name: /Участник/ })).toHaveCount(0);
  expect(logoutCSRF).toBe(csrfToken);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem("player_id"))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem("username"))).toBeNull();
  await expect.poll(() => page.evaluate(() => document.cookie.includes("tpm_player_csrf="))).toBe(false);

  expectOnlyPaths(evidence, [publicPath, participantPath, playerLogoutPath]);
  expectNoAuthorization(evidence);
});

test("operator logout calls the admin logout API and clears the admin session", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const csrfToken = "arena-operator-logout-csrf";
  let logoutCSRF: string | undefined;
  const evidence = await observeAPI(page);

  await page.addInitScript((token) => {
    document.cookie = `tpm_admin_refresh_csrf=${encodeURIComponent(token)}; Path=/`;
  }, csrfToken);
  await installPublicRoute(page, fixtureSet.public.tournament);
  await installOperatorRoute(page, fixtureSet.operator.snapshot);
  await page.route(`**${adminLogoutPath}`, async (route) => {
    expect(route.request().method()).toBe("POST");
    logoutCSRF = route.request().headers()["x-csrf-token"];
    await route.fulfill({ status: 204, body: "" });
  });

  await page.goto(roleURL("operator"));
  await expect(page.getByRole("button", { name: "Выйти" })).toBeVisible();
  await page.getByRole("button", { name: "Выйти" }).click();

  await expect(page.getByText("Требуется вход")).toBeVisible();
  await expect(page.getByRole("heading", { name: /Оператор/ })).toHaveCount(0);
  expect(logoutCSRF).toBe(csrfToken);
  await expect.poll(() => page.evaluate(() => document.cookie.includes("tpm_admin_refresh_csrf="))).toBe(false);

  expectOnlyPaths(evidence, [publicPath, operatorPath, adminLogoutPath]);
  expectNoAuthorization(evidence);
});

test("unknown tournament shows a controlled missing state without protected requests", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const evidence = await observeAPI(page);
  await installPublicRoute(page, fixtureSet.public.tournament, 404);

  await page.goto(roleURL("spectator"));

  await expect(page.locator("strong").filter({ hasText: "Турнир не найден" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Выбрать другой турнир" })).toBeVisible();
  await expect(page.getByRole("heading", { name: /Наблюдатель/ })).toHaveCount(0);

  expectOnlyPaths(evidence, [publicPath]);
  expectNoAuthorization(evidence);
});

test("completed tournaments are read-only after role authorization", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const completedTournament = {
    ...fixtureSet.public.tournament,
    state: "completed" as const,
    finished_at: "2026-09-13T10:12:00Z",
  };
  const evidence = await observeAPI(page);
  await installPublicRoute(page, completedTournament);
  await installParticipantRoute(page, fixtureSet.participant.lobby);
  await installOperatorRoute(page, fixtureSet.operator.snapshot);

  await page.goto(roleURL("participant"));
  await expect(page.getByTestId("arena-status")).toContainText("Только чтение");
  await expect(page.getByRole("heading", { name: /Участник.*Завершен/ })).toBeVisible();
  await expect(page.getByText("Действия турнира отключены")).toBeVisible();
  await expect(page.getByRole("button", { name: /Отправить|Пауза|Готов/ })).toHaveCount(0);

  await page.goto(roleURL("operator"));
  await expect(page.getByTestId("arena-status")).toContainText("Только чтение");
  await expect(page.getByRole("heading", { name: /Сентябрьский контур.*Оператор.*Завершен/ })).toBeVisible();

  await page.goto(roleURL("spectator"));
  await expect(page.getByTestId("arena-status")).toContainText("Только чтение");
  await expect(page.getByRole("heading", { name: /Наблюдатель.*Завершен/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "Выйти" })).toHaveCount(0);

  expectOnlyPaths(evidence, [publicPath, participantPath, operatorPath]);
  expectNoAuthorization(evidence);
});

test("Arena stays usable in dark and light themes at a narrow viewport", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  await installPublicRoute(page, fixtureSet.public.tournament);

  await page.goto(roleURL("spectator"));
  const html = page.locator("html");
  await expect(html).toHaveAttribute("data-theme", "dark");

  await page.getByRole("button", { name: "Светлая тема" }).click();
  await expect(html).toHaveAttribute("data-theme", "light");

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => ({
    body: document.body.scrollWidth,
    document: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }))).toEqual({ body: 390, document: 390, viewport: 390 });
  await expect(page.getByRole("heading", { name: /Наблюдатель/ })).toBeVisible();
});
