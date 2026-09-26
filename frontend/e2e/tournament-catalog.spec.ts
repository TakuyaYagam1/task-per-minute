import { expect, test, type Page, type Route } from "@playwright/test";

import { createTournamentFixtureSet, tournamentFixtureIds } from "./tournament/fixtures";

const tournamentId = tournamentFixtureIds.tournament;
const publicId = "september-contour";
const catalogPath = "/api/v1/public/tournaments";
const detailPath = `${catalogPath}/${publicId}`;
const snapshotPath = `/api/v1/tournaments/${tournamentId}/snapshot`;
const serverDateHeader = "Sun, 13 Sep 2026 10:00:00 GMT";

type CatalogItem = Readonly<{
  created_at: string;
  finished_at: string | null;
  group: "live" | "upcoming" | "completed";
  name: string;
  planned_roster_size: number;
  preset: "tournament_v1";
  public_id: string;
  roster_size: number;
  scheduled_at: string | null;
  stage: "registration" | "roster_locked" | "swiss" | "golden" | "playoffs" | "completed" | "cancelled";
  started_at: string | null;
  state: "registration" | "roster_locked" | "swiss" | "golden" | "playoffs" | "technical_pause" | "completed" | "cancelled";
  tournament_id: string;
}>;

const item = (overrides: Partial<CatalogItem> = {}): CatalogItem => ({
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
  ...overrides,
});

const catalogItems: readonly CatalogItem[] = [
  item(),
  ...Array.from({ length: 19 }, (_, index) => item({
    name: `Контур ${String(index + 2).padStart(2, "0")}`,
    public_id: `contour-${index + 2}`,
    tournament_id: `00000000-0000-4000-8000-${String(index + 201).padStart(12, "0")}`,
  })),
];

const liveItem = item({
  group: "live",
  name: "Живой швейцарский этап",
  public_id: "live-swiss",
  roster_size: 10,
  stage: "swiss",
  started_at: "2026-09-13T10:05:00Z",
  state: "swiss",
  tournament_id: "00000000-0000-4000-8000-000000000202",
});

const completedItem = item({
  finished_at: "2026-09-13T11:05:00Z",
  group: "completed",
  name: "Завершенный контур",
  public_id: "finished-contour",
  roster_size: 12,
  stage: "completed",
  started_at: "2026-09-13T10:05:00Z",
  state: "completed",
  tournament_id: "00000000-0000-4000-8000-000000000203",
});

const fulfillJSON = async (
  route: Route,
  body: unknown,
  status = 200,
  headers: Record<string, string> = {},
): Promise<void> => {
  await route.fulfill({
    status,
    headers: {
      "content-type": "application/json",
      ...headers,
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
      title: status === 429 ? "Too Many Requests" : "Service unavailable",
      type: "about:blank",
    }),
  });
};

const installCatalogRoute = async (
  page: Page,
  handler: (url: URL, route: Route) => Promise<void>,
): Promise<void> => {
  await page.route(`**${catalogPath}**`, async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname !== catalogPath) {
      await route.fallback();
      return;
    }
    await handler(url, route);
  });
};

const installDetailAndSnapshotRoutes = async (page: Page): Promise<void> => {
  const fixtureSet = createTournamentFixtureSet();
  await page.route("**/api/v1/players/me", async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillProblem(route, 401, "Требуется вход");
  });
  await page.route(`**${detailPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, item({
      group: "live",
      name: "Сентябрьский контур",
      public_id: publicId,
      roster_size: 4,
      stage: "swiss",
      started_at: "2026-09-13T10:05:00Z",
      state: "technical_pause",
    }));
  });
  await page.route(`**${snapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.public.recovery, 200, { date: serverDateHeader });
  });
};

test("catalog renders 20 items, sends server filters, and preserves a multiword search", async ({ page }) => {
  const requests: URL[] = [];
  await installCatalogRoute(page, async (url, route) => {
    requests.push(url);
    const query = url.searchParams.get("q") ?? "";
    const items = query.length > 0
      ? [item({ name: "Контур для командной игры" })]
      : catalogItems;
    await fulfillJSON(route, { items, next_cursor: query.length > 0 ? null : "page-2" });
  });

  await page.goto("/arena?group=live&sort=name");
  await expect(page.getByRole("heading", { name: "Турниры", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: /Контур 19/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "Следующая страница" })).toBeVisible();

  const search = page.getByRole("searchbox", { name: "Поиск", exact: true });
  await search.click();
  await page.keyboard.type("Контур для командной игры");
  await expect(search).toHaveValue("Контур для командной игры");
  await expect(page.getByRole("link", { name: /Контур для командной игры/ })).toBeVisible();
  await expect.poll(() => requests.some((url) => url.searchParams.get("q") === "Контур для командной игры")).toBe(true);
  expect(page.url()).toContain("q=%D0%9A%D0%BE%D0%BD%D1%82%D1%83%D1%80+%D0%B4%D0%BB%D1%8F");
  expect(requests.some((url) => url.searchParams.get("limit") === "20")).toBe(true);
});

test("catalog cursor and filter changes are server driven and reversible with browser history", async ({ page }) => {
  const requests: URL[] = [];
  await installCatalogRoute(page, async (url, route) => {
    requests.push(url);
    if (url.searchParams.get("cursor") === "page-2") {
      await fulfillJSON(route, { items: [completedItem], next_cursor: null });
      return;
    }
    const group = url.searchParams.get("group");
    await fulfillJSON(route, {
      items: group === "completed" ? [completedItem] : [liveItem],
      next_cursor: group === "completed" ? null : "page-2",
    });
  });

  await page.goto("/arena?group=live&sort=activity");
  await expect(page.getByRole("link", { name: /Живой швейцарский этап/ })).toBeVisible();
  await page.getByRole("button", { name: "Следующая страница" }).click();
  {
    const url = new URL(page.url());
    expect(url.pathname).toBe("/arena");
    expect(url.searchParams.get("group")).toBe("live");
    expect(url.searchParams.get("sort") ?? "activity").toBe("activity");
    expect(url.searchParams.get("cursor")).toBe("page-2");
  }
  await expect(page.getByRole("link", { name: /Завершенный контур/ })).toBeVisible();

  await page.goto("/arena?group=live&sort=activity&cursor=page-2");
  await expect(page.getByRole("link", { name: /Завершенный контур/ })).toBeVisible();
  await page.getByLabel("Состояние").selectOption("completed");
  await expect(page).toHaveURL(/\/arena\?group=completed$/);
  await expect(page.getByRole("link", { name: /Завершенный контур/ })).toBeVisible();
  await page.goBack();
  {
    const url = new URL(page.url());
    expect(url.pathname).toBe("/arena");
    expect(url.searchParams.get("group")).toBe("live");
    expect(url.searchParams.get("sort") ?? "activity").toBe("activity");
    expect(url.searchParams.get("cursor")).toBe("page-2");
  }
  await expect(page.getByRole("link", { name: /Завершенный контур/ })).toBeVisible();
  await page.goForward();
  await expect(page).toHaveURL(/\/arena\?group=completed$/);
  expect(requests.some((url) => url.searchParams.get("cursor") === "page-2")).toBe(true);
});

test("catalog empty state is explicit and does not invent tournament data", async ({ page }) => {
  await installCatalogRoute(page, async (_url, route) => {
    await fulfillJSON(route, { items: [], next_cursor: null });
  });

  await page.goto("/arena?group=completed");
  await expect(page.getByRole("status").filter({ hasText: "Турниры не найдены" })).toBeVisible();
  await expect(page.getByRole("link", { name: /Открыть обзор/ })).toHaveCount(0);
});

test("catalog rate limits expose retry and retain a recoverable error state", async ({ page }) => {
  let attempts = 0;
  let allowSuccess = false;
  await installCatalogRoute(page, async (_url, route) => {
    attempts += 1;
    if (!allowSuccess) {
      await fulfillProblem(route, 429, "synthetic catalog rate limit");
      return;
    }
    await fulfillJSON(route, { items: [item()], next_cursor: null });
  });

  await page.goto("/arena");
  await expect(page.getByText("Каталог временно ограничил частоту запросов")).toBeVisible();
  allowSuccess = true;
  await page.getByRole("button", { name: "Повторить" }).click();
  await expect(page.getByRole("link", { name: /Сентябрьский контур/ })).toBeVisible();
  expect(attempts).toBeGreaterThanOrEqual(2);
});

test("public detail is anonymous, keeps the catalog return path, and exposes all views", async ({ page }) => {
  const apiPaths: string[] = [];
  const authorizationHeaders: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname.startsWith("/api/")) {
      apiPaths.push(url.pathname);
      const authorization = request.headers().authorization;
      if (authorization !== undefined) {
        authorizationHeaders.push(authorization);
      }
    }
  });
  await installDetailAndSnapshotRoutes(page);

  await page.goto(`/arena/tournaments/${publicId}?view=overview&return=${encodeURIComponent("/arena?group=live")}`);
  await expect(page.getByRole("heading", { name: "Сентябрьский контур", exact: true })).toBeVisible();
  await expect(page.getByText("Идет", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("region", { name: "Сведения о турнире" })
      .getByText("Техническая пауза", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "Вернуться к каталогу" })).toHaveAttribute(
    "href",
    "/arena?group=live",
  );
  await expect(page.getByRole("link", { name: "Участник", exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Оператор", exact: true })).toHaveCount(0);

  await page.getByRole("link", { name: "Матчи" }).click();
  await expect(page).toHaveURL(/view=matches/);
  await expect(page.getByRole("heading", { name: "Матчи сервера" })).toBeVisible();

  await page.getByRole("link", { name: "Сетка" }).click();
  await expect(page).toHaveURL(/view=bracket/);
  await expect(page.getByRole("tab", { name: "Swiss" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Плей-офф" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Swiss" })).toHaveAttribute("aria-selected", "false");
  await page.getByRole("tab", { name: "Swiss" }).click();
  await expect(page.getByRole("tabpanel", { name: "Swiss" })).toBeVisible();

  await page.getByRole("link", { name: "Турнирная таблица" }).click();
  await expect(page).toHaveURL(/view=standings/);
  await expect(page.getByRole("table", { name: "Публичная таблица турнира" })).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(/view=bracket/);
  await page.goBack();
  await expect(page).toHaveURL(/view=matches/);

  expect(new Set(apiPaths)).toEqual(new Set([
    detailPath,
    snapshotPath,
    "/api/v1/players/me",
  ]));
  expect(authorizationHeaders).toEqual([]);
});

test("catalog rejects malformed and duplicate responses, then recovers through retry", async ({ page }) => {
  let allowSuccess = false;
  await installCatalogRoute(page, async (_url, route) => {
    if (!allowSuccess) {
      await fulfillJSON(route, {
        items: [item(), item({ name: "Дубликат", public_id: publicId })],
        next_cursor: null,
      });
      return;
    }
    await fulfillJSON(route, { items: [item()], next_cursor: null });
  });

  await page.goto("/arena");
  await expect(page.getByText("Сервер вернул неожиданный ответ каталога")).toBeVisible();
  allowSuccess = true;
  await page.getByRole("button", { name: "Повторить" }).click();
  await expect(page.getByRole("link", { name: /Сентябрьский контур/ })).toBeVisible();
});

test("stale catalog responses cannot replace a newer search result", async ({ page }) => {
  let firstRequest = true;
  await installCatalogRoute(page, async (url, route) => {
    if (firstRequest && !url.searchParams.has("q")) {
      firstRequest = false;
      await new Promise((resolve) => setTimeout(resolve, 350));
      try {
        await fulfillJSON(route, { items: [item({ name: "Старый результат" })], next_cursor: null });
      } catch {
        // The browser may cancel this deliberately stale request.
      }
      return;
    }
    await fulfillJSON(route, {
      items: [item({ name: "Новый результат" })],
      next_cursor: null,
    });
  });

  await page.goto("/arena");
  const search = page.getByRole("searchbox", { name: "Поиск", exact: true });
  await search.fill("новый");
  await expect(page.getByRole("link", { name: /Новый результат/ })).toBeVisible();
  await expect(page.getByRole("link", { name: /Старый результат/ })).toHaveCount(0);
});

test("detail errors distinguish missing, rate limited, and transport states and retry", async ({ page }) => {
  let mode: "not_found" | "rate_limited" | "transport" | "success" = "not_found";
  await page.route(`**${detailPath}`, async (route) => {
    if (mode === "not_found") {
      await fulfillProblem(route, 404, "synthetic tournament missing");
      return;
    }
    if (mode === "rate_limited") {
      await fulfillProblem(route, 429, "synthetic tournament rate limit");
      return;
    }
    if (mode === "transport") {
      await route.abort("failed");
      return;
    }
    await fulfillJSON(route, item());
  });

  await page.goto(`/arena/tournaments/${publicId}`);
  await expect(page.getByText("Турнир не найден или больше не публикуется")).toBeVisible();
  mode = "success";
  await page.getByRole("button", { name: "Повторить" }).click();
  await expect(page.getByRole("heading", { name: "Сентябрьский контур", exact: true })).toBeVisible();

  mode = "rate_limited";
  await page.reload();
  await expect(page.getByText("Слишком много запросов. Повторите попытку позже.")).toBeVisible();
  mode = "transport";
  await page.reload();
  await expect(page.getByText("Не удалось загрузить турнир. Повторите попытку.")).toBeVisible();
});

test("prestart detail does not open the public recovery transport", async ({ page }) => {
  let snapshotRequests = 0;
  await page.route(`**${detailPath}`, async (route) => {
    await fulfillJSON(route, item());
  });
  await page.route(`**${snapshotPath}*`, async (route) => {
    snapshotRequests += 1;
    await route.abort("blockedbyclient");
  });

  await page.goto(`/arena/tournaments/${publicId}?view=overview`);
  await expect(page.getByText("Турнир готовится к старту")).toBeVisible();
  expect(snapshotRequests).toBe(0);
});

test("wrong public slug response is rejected before any recovery request", async ({ page }) => {
  let snapshotRequests = 0;
  await page.route(`**${detailPath}`, async (route) => {
    await fulfillJSON(route, item({ public_id: "different-contour" }));
  });
  await page.route(`**${snapshotPath}*`, async (route) => {
    snapshotRequests += 1;
    await route.abort("blockedbyclient");
  });

  await page.goto(`/arena/tournaments/${publicId}`);
  await expect(page.getByText("Не удалось загрузить турнир. Повторите попытку.")).toBeVisible();
  expect(snapshotRequests).toBe(0);
});

test("public return paths allow one local detail hop and reject external targets", async ({ page }) => {
  await installDetailAndSnapshotRoutes(page);
  const nestedReturn = `/arena/tournaments/${publicId}?view=matches&return=${encodeURIComponent("/arena?group=live")}`;
  await page.goto(`/arena/tournaments/${publicId}?view=overview&return=${encodeURIComponent(nestedReturn)}`);
  await expect(page.getByRole("link", { name: "Вернуться к каталогу" })).toHaveAttribute(
    "href",
    nestedReturn,
  );

  await page.goto(`/arena/tournaments/${publicId}?view=overview&return=${encodeURIComponent("https://evil.example")}`);
  await expect(page.getByRole("link", { name: "Вернуться к каталогу" })).toHaveAttribute("href", "/arena");
});

const setTheme = async (page: Page, theme: "dark" | "light"): Promise<void> => {
  await page.getByRole("button", { name: theme === "light" ? "Светлая тема" : "Темная тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
};

const expectNoHorizontalOverflow = async (page: Page): Promise<void> => {
  const dimensions = await page.evaluate(() => ({
    body: document.body.scrollWidth,
    document: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }));
  expect(dimensions.body).toBeLessThanOrEqual(dimensions.viewport);
  expect(dimensions.document).toBeLessThanOrEqual(dimensions.viewport);
};

const expectReadableCatalogCard = async (page: Page): Promise<void> => {
  const cardContrast = async (): Promise<number> => page
    .getByRole("link", { name: /Сентябрьский контур/ })
    .first()
    .evaluate((element) => {
      const title = element.querySelector("h2");
      const parseRGB = (value: string): [number, number, number] | null => {
        const match = value.match(/rgba?\(\s*([\d.]+)[, ]+\s*([\d.]+)[, ]+\s*([\d.]+)/);
        return match
          ? [Number(match[1]), Number(match[2]), Number(match[3])]
          : null;
      };
      const luminance = (value: string): number => {
        const channels = parseRGB(value);
        if (!channels) {
          return 0;
        }
        const linear = channels.map((channel) => {
          const normalized = channel / 255;
          return normalized <= 0.03928
            ? normalized / 12.92
            : ((normalized + 0.055) / 1.055) ** 2.4;
        });
        return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
      };
      const background = luminance(getComputedStyle(element).backgroundColor);
      const text = luminance(title ? getComputedStyle(title).color : "");
      return (Math.max(background, text) + 0.05) / (Math.min(background, text) + 0.05);
    });

  await expect.poll(cardContrast).toBeGreaterThanOrEqual(4.5);
};

for (const theme of ["dark", "light"] as const) {
  for (const width of [375, 768, 1440] as const) {
    test(`catalog and public detail visual contract ${theme} ${width}px`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await installCatalogRoute(page, async (_url, route) => {
        await fulfillJSON(route, {
          items: [item({
            group: "live",
            stage: "swiss",
            started_at: "2026-09-13T10:05:00Z",
            state: "swiss",
          })],
          next_cursor: null,
        });
      });
      await installDetailAndSnapshotRoutes(page);

      await page.goto("/arena");
      await expect(page.getByRole("link", { name: /Сентябрьский контур/ })).toBeVisible();
      await setTheme(page, theme);
      await expectReadableCatalogCard(page);
      await expectNoHorizontalOverflow(page);
      await page.screenshot({
        animations: "disabled",
        fullPage: true,
        path: testInfo.outputPath("tournament-catalog", `catalog-${theme}-${width}.png`),
      });

      await page.getByRole("link", { name: /Сентябрьский контур/ }).click();
      await expect(page).toHaveURL(/\/arena\/tournaments\/september-contour/);
      await expect(
        page.getByRole("heading", { level: 1, name: "Сентябрьский контур", exact: true }),
      ).toBeVisible();
      await expect(page.getByRole("region", { name: "Матч-центр" })).toBeVisible();
      await expect(page.getByRole("heading", { name: "Участие в турнире", exact: true })).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await page.screenshot({
        animations: "disabled",
        fullPage: true,
        path: testInfo.outputPath("tournament-catalog", `detail-${theme}-${width}.png`),
      });
    });
  }
}
