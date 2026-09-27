import { expect, test, type Page, type Route, type TestInfo } from "@playwright/test";

import { createTournamentFixtureSet, tournamentFixtureIds } from "./tournament/fixtures";

test.use({ trace: "off" });

const widths = [375, 768, 1440] as const;
const themes = ["dark", "light"] as const;
type Theme = (typeof themes)[number];
type Role = "participant" | "operator" | "spectator";

const tournamentId = tournamentFixtureIds.tournament;
const tournamentPath = `/api/v1/tournaments/${tournamentId}`;
const catalogPath = "/api/v1/public/tournaments";
const serverDateHeader = "Sun, 13 Sep 2026 10:00:00 GMT";

const catalogItem = {
  created_at: "2026-09-13T10:00:00Z",
  finished_at: null,
  group: "live",
  name: "Сентябрьский контур",
  planned_roster_size: 16,
  preset: "tournament_v1",
  public_id: "september-contour",
  roster_size: 4,
  scheduled_at: null,
  stage: "swiss",
  started_at: "2026-09-13T10:05:00Z",
  state: "swiss",
  tournament_id: tournamentId,
} as const;

const fulfillJSON = async (route: Route, body: unknown, status = 200): Promise<void> => {
  await route.fulfill({
    status,
    headers: {
      "content-type": "application/json",
      date: serverDateHeader,
    },
    body: JSON.stringify(body),
  });
};

const fulfillProblem = async (route: Route, status: number, detail: string): Promise<void> => {
  await route.fulfill({
    status,
    headers: { "content-type": "application/problem+json" },
    body: JSON.stringify({
      detail,
      status,
      title: "Service unavailable",
      type: "about:blank",
    }),
  });
};

const installRoleFixtures = async (page: Page, role: Role): Promise<void> => {
  const fixtureSet = createTournamentFixtureSet();
  await page.route(`**${tournamentPath}`, (route) => fulfillJSON(route, fixtureSet.public.tournament));

  if (role === "participant") {
    await page.route(
      `**${tournamentPath}/participant/lobby*`,
      (route) => fulfillJSON(route, fixtureSet.participant.lobby),
    );
    await page.route(
      `**${tournamentPath}/participant/snapshot*`,
      (route) => fulfillJSON(route, fixtureSet.participant.recovery),
    );
    return;
  }

  if (role === "operator") {
    await page.route(
      `**/api/v1/admin/tournaments/${tournamentId}/snapshot*`,
      (route) => fulfillJSON(route, fixtureSet.operator.snapshot),
    );
    return;
  }

  await page.route(
    `**${tournamentPath}/snapshot*`,
    (route) => fulfillJSON(route, fixtureSet.public.recovery),
  );
};

const installLeaderboardFixture = async (page: Page): Promise<void> => {
  await page.route("**/api/v1/leaderboard*", (route) => fulfillJSON(route, {
    entries: [
      {
        rank: 1,
        username: "alice",
        wins: 4,
        average_solve_time_ms: 42_100,
      },
      {
        rank: 2,
        username: "bob",
        wins: 2,
        average_solve_time_ms: 65_000,
      },
    ],
  }));
};

const installCatalogFixture = async (page: Page): Promise<void> => {
  await page.route(`**${catalogPath}*`, (route) => fulfillJSON(route, {
    items: [catalogItem],
    next_cursor: null,
  }));
};

const setTheme = async (page: Page, theme: Theme): Promise<void> => {
  await page.getByRole("button", { name: theme === "light" ? "Светлая тема" : "Темная тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
};

const waitForRoleSurface = async (page: Page, role: Role): Promise<void> => {
  const arenaStatus = page.getByTestId("arena-status");
  await expect(arenaStatus).toContainText("Доступ подтвержден");
  await expect(arenaStatus).toHaveAttribute("data-tone", "success");

  if (role === "participant") {
    const participantPanel = page.getByTestId("participant-player-panel");
    await expect(participantPanel).toBeVisible();
    await expect(participantPanel.getByRole("heading", { name: "Позиция в соревновании" })).toBeVisible();
    return;
  }

  if (role === "operator") {
    await expect(page.getByRole("heading", { name: "Управление соревнованием" }).last()).toBeVisible();
    await expect(page.getByText("Данные обновлены", { exact: true })).toBeVisible();
    return;
  }

  const broadcast = page.getByTestId("tournament-broadcast");
  await expect(broadcast).toBeVisible();
  await expect(broadcast.getByTestId("broadcast-selected-series")).toBeVisible();
};

const parseColor = (value: string): [number, number, number] | null => {
  const normalized = value.trim().toLowerCase();
  const hex = normalized.match(/^#([0-9a-f]{6})$/);
  if (hex) {
    return [
      Number.parseInt(hex[1].slice(0, 2), 16),
      Number.parseInt(hex[1].slice(2, 4), 16),
      Number.parseInt(hex[1].slice(4, 6), 16),
    ];
  }
  const rgb = normalized.match(/^rgba?\(\s*([\d.]+)[, ]+\s*([\d.]+)[, ]+\s*([\d.]+)/);
  return rgb
    ? [Number.parseFloat(rgb[1]), Number.parseFloat(rgb[2]), Number.parseFloat(rgb[3])]
    : null;
};

const assertClassicVisualContract = async (page: Page): Promise<void> => {
  const state = await page.evaluate(() => {
    const root = getComputedStyle(document.documentElement);
    const computedStyles = Array.from(document.querySelectorAll<HTMLElement>("body *"))
      .map((element) => getComputedStyle(element));
    return {
      bodyScrollWidth: document.body.scrollWidth,
      documentScrollWidth: document.documentElement.scrollWidth,
      viewportWidth: window.innerWidth,
      tokens: [
        "--bg",
        "--surface",
        "--surface-strong",
        "--surface-muted",
        "--text",
        "--secondary",
        "--subtle",
        "--border",
        "--border-strong",
        "--accent",
        "--accent-strong",
        "--accent-soft",
        "--accent-ring",
        "--overlay-bg",
      ]
        .map((name) => root.getPropertyValue(name).trim()),
      hasGradient: computedStyles.some((styles) => styles.backgroundImage !== "none"),
      hasBackdropFilter: computedStyles.some((styles) => styles.backdropFilter !== "none"),
      text: document.body.innerText,
    };
  });

  expect(state.documentScrollWidth).toBeLessThanOrEqual(state.viewportWidth);
  expect(state.bodyScrollWidth).toBeLessThanOrEqual(state.viewportWidth);
  expect(state.hasGradient).toBe(false);
  expect(state.hasBackdropFilter).toBe(false);
  expect(state.text).not.toMatch(/[\u{1f000}-\u{1faff}]/u);

  for (const token of state.tokens) {
    const color = parseColor(token);
    expect(color, `token ${token} must be a readable color`).not.toBeNull();
    expect(color?.[0]).toBe(color?.[1]);
    expect(color?.[1]).toBe(color?.[2]);
  }

  const themeToggle = await page.getByRole("group", { name: "Тема интерфейса" }).boundingBox();
  if (themeToggle === null) {
    throw new Error("Theme switcher geometry is unavailable");
  }
  const headerGeometry = await page.evaluate(() => {
    const selectors = [
      ["home-card", "main .card"],
      ["leaderboard-back", 'main > a[href="/"]'],
    ] as const;
    return selectors.flatMap(([name, selector]) => {
      const element = document.querySelector<HTMLElement>(selector);
      if (element === null) {
        return [];
      }
      const rect = element.getBoundingClientRect();
      return [{
        bottom: rect.bottom,
        left: rect.left,
        name,
        right: rect.right,
        top: rect.top,
      }];
    });
  });
  for (const element of headerGeometry) {
    const overlaps = element.left < themeToggle.x + themeToggle.width
      && element.right > themeToggle.x
      && element.top < themeToggle.y + themeToggle.height
      && element.bottom > themeToggle.y;
    expect(overlaps, `${element.name} overlaps the theme switcher`).toBe(false);
  }

  await page.locator("body").click({ position: { x: 4, y: 4 } });
  await page.keyboard.press("Tab");
  const focused = page.locator(":focus-visible").first();
  await expect(focused).toBeVisible();
  await expect.poll(() => focused.evaluate((element) => getComputedStyle(element).outlineStyle)).not.toBe("none");
};

const screenshot = async (page: Page, testInfo: TestInfo, name: string): Promise<void> => {
  await page.screenshot({
    fullPage: true,
    path: testInfo.outputPath("classic-ui", `${name}.png`),
  });
};

const surfaces = [
  { name: "home", path: "/" },
  { name: "leaderboard", path: "/leaderboard" },
  { name: "arena", path: "/arena" },
  { name: "participant", path: `/arena/participant/${tournamentId}`, role: "participant" as const },
  { name: "operator", path: `/arena/operator/${tournamentId}`, role: "operator" as const },
  { name: "spectator", path: `/arena/spectator/${tournamentId}`, role: "spectator" as const },
] as const;

for (const surface of surfaces) {
  for (const theme of themes) {
    for (const width of widths) {
      test(`${surface.name} ${theme} ${width}px has the classic visual contract`, async ({ page }, testInfo) => {
        await page.setViewportSize({ width, height: 900 });
        if (surface.name === "leaderboard") {
          await installLeaderboardFixture(page);
        }
        if (surface.name === "arena") {
          await installCatalogFixture(page);
        }
        if ("role" in surface) {
          await installRoleFixtures(page, surface.role);
        }

        await page.goto(surface.path);
        if (surface.name === "leaderboard") {
          await expect(page.getByRole("table")).toBeVisible();
        } else if (surface.name === "arena") {
          await expect(page.getByRole("heading", { name: "Соревнования", exact: true })).toBeVisible();
          await expect(page.getByRole("link", { name: /Сентябрьский контур/ })).toBeVisible();
          const qualificationLabels = page.getByText("Квалификация", { exact: true });
          await expect(qualificationLabels).toHaveCount(2);
          await expect(qualificationLabels.first()).toBeVisible();
        } else if ("role" in surface) {
          await waitForRoleSurface(page, surface.role);
        } else {
          await expect(page.locator("main")).toBeVisible();
          await expect(page.getByRole("heading", { name: "Task Per Minute", exact: true })).toBeVisible();
          await expect(page.getByRole("heading", { name: "CTF Соревнования", exact: true })).toBeVisible();
          await expect(page.getByText("Платформа турниров CTF", { exact: true })).toHaveCount(0);
          await expect(page.getByText("CTF турнир", { exact: true })).toHaveCount(0);
        }

        await setTheme(page, theme);
        await assertClassicVisualContract(page);
        await screenshot(page, testInfo, `${surface.name}-${theme}-${width}`);
      });
    }
  }
}

test("leaderboard empty and error states remain readable", async ({ page }) => {
  let response: { entries: unknown[] } = { entries: [] };
  await page.route("**/api/v1/leaderboard*", (route) => fulfillJSON(route, response));
  await page.goto("/leaderboard");
  await expect(page.getByText("Пока нет данных о игроках")).toBeVisible();

  response = { entries: [] };
  await page.unroute("**/api/v1/leaderboard*");
  await page.route(
    "**/api/v1/leaderboard*",
    (route) => fulfillProblem(route, 503, "synthetic leaderboard unavailable"),
  );
  await page.reload();
  await expect(page.getByText("synthetic leaderboard unavailable")).toBeVisible();
});
