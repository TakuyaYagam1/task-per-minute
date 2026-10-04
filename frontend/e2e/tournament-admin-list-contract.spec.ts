import { expect, test, type Page, type Route } from "@playwright/test";

import { jsonHeaders, openAccountMenu } from "./support/common";

const accessCSRF = "tournament-admin-access-csrf";
const refreshCSRF = "tournament-admin-refresh-csrf";
const contentRevision = 42;
const publicationID = "10000000-0000-4000-8000-000000000001";
const normalPoolRevisionID = "10000000-0000-4000-8000-000000000002";
const goldenPoolRevisionID = "10000000-0000-4000-8000-000000000003";

type TournamentState =
  | "draft"
  | "registration"
  | "roster_locked"
  | "swiss"
  | "golden"
  | "playoffs"
  | "technical_pause"
  | "completed"
  | "cancelled";

type TournamentOverrides = Partial<{
  id: string;
  name: string;
  public_id: string;
  state: TournamentState;
  roster_size: number;
  planned_roster_size: number;
  started_at: string | null;
  finished_at: string | null;
  paused_from_state: TournamentState | null;
}>;

type CreateRouteHandler = (route: Route) => Promise<void>;

const baseDate = "2026-09-13T10:00:00Z";
const startedDate = "2026-09-13T10:15:00Z";
const finishedDate = "2026-09-13T11:00:00Z";

const tournament = (overrides: TournamentOverrides = {}) => {
  const state = overrides.state ?? "draft";
  const id = overrides.id ?? "10000000-0000-4000-8000-000000000010";
  return {
    content_revision: contentRevision,
    created_at: baseDate,
    finished_at: null,
    id,
    name: overrides.name ?? "Демо турнир",
    paused_from_state: null,
    planned_roster_size: 4,
    preset: "tournament_v1",
    public_id: overrides.public_id ?? "demo-tournament",
    revision: 1,
    roster_id: "10000000-0000-4000-8000-000000000011",
    roster_size: 0,
    started_at: null,
    state,
    updated_at: baseDate,
    ...overrides,
  };
};

const contentSelection = () => ({
  content_revision: contentRevision,
  publication_id: publicationID,
  published_at: baseDate,
  normal_pool_revision_id: normalPoolRevisionID,
  golden_pool_revision_id: goldenPoolRevisionID,
});

const problem = (status: number, detail: string) => ({
  type: "about:blank",
  title: status === 409 ? "conflict" : "validation failed",
  status,
  detail,
});

const fulfillJSON = async (
  route: Route,
  status: number,
  body: unknown,
  headers: Record<string, string> = {},
): Promise<void> => {
  await route.fulfill({
    status,
    headers: { ...jsonHeaders, ...headers },
    body: JSON.stringify(body),
  });
};

const setupAdminRoutes = async (
  page: Page,
  options: {
    listBody?: unknown;
    listStatus?: number;
    contentBody?: unknown;
    contentStatus?: number;
    create?: CreateRouteHandler;
  } = {},
): Promise<void> => {
  await page.route("**/api/v1/admin/login", async (route) => {
    expect(route.request().method()).toBe("POST");
    await fulfillJSON(route, 200, { expires_in: 900 }, {
      "X-CSRF-Token": accessCSRF,
      "X-Admin-Refresh-CSRF-Token": refreshCSRF,
      "Set-Cookie": `tpm_admin_refresh_csrf=${refreshCSRF}; Path=/`,
    });
  });

  await page.route("**/api/v1/admin/refresh", async (route) => {
    expect(route.request().method()).toBe("POST");
    await fulfillJSON(route, 200, { expires_in: 900 }, {
      "X-CSRF-Token": accessCSRF,
      "X-Admin-Refresh-CSRF-Token": refreshCSRF,
      "Set-Cookie": `tpm_admin_refresh_csrf=${refreshCSRF}; Path=/`,
    });
  });

  await page.route("**/api/v1/admin/tasks**", async (route) => {
    const request = route.request();
    if (request.method() === "GET") {
      await fulfillJSON(route, 200, []);
      return;
    }
    await fulfillJSON(route, 404, {});
  });

  await page.route("**/api/v1/admin/tournament-content**", async (route) => {
    await fulfillJSON(
      route,
      options.contentStatus ?? 200,
      options.contentBody ?? contentSelection(),
    );
  });

  await page.route("**/api/v1/admin/tournaments**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path !== "/api/v1/admin/tournaments") {
      await fulfillJSON(route, 404, {});
      return;
    }
    if (request.method() === "GET") {
      await fulfillJSON(
        route,
        options.listStatus ?? 200,
        options.listBody ?? { items: [], next_cursor: null },
      );
      return;
    }
    if (request.method() === "POST" && options.create) {
      await options.create(route);
      return;
    }
    await fulfillJSON(route, 404, {});
  });
};

const loginAndOpenTournamentList = async (page: Page): Promise<void> => {
  await page.goto("/admin");
  await page.getByPlaceholder("Введите пароль...").fill("correct-password");
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("button", { name: "Соревнования" })).toBeVisible();
  await page.getByRole("button", { name: "Соревнования" }).click();
  await expect(page.getByRole("heading", { name: "Новое соревнование" })).toBeVisible();
};

test("показывает состояния загрузки списка турниров и контента", async ({ page }) => {
  let releaseList: (() => void) | undefined;
  let releaseContent: (() => void) | undefined;
  const listGate = new Promise<void>((resolve) => {
    releaseList = resolve;
  });
  const contentGate = new Promise<void>((resolve) => {
    releaseContent = resolve;
  });

  await page.route("**/api/v1/admin/login", async (route) => {
    await fulfillJSON(route, 200, { expires_in: 900 }, {
      "X-CSRF-Token": accessCSRF,
      "X-Admin-Refresh-CSRF-Token": refreshCSRF,
    });
  });
  await page.route("**/api/v1/admin/tasks**", async (route) => {
    await fulfillJSON(route, 200, []);
  });
  await page.route("**/api/v1/admin/tournament-content**", async (route) => {
    await contentGate;
    await fulfillJSON(route, 200, contentSelection());
  });
  await page.route("**/api/v1/admin/tournaments**", async (route) => {
    await listGate;
    await fulfillJSON(route, 200, { items: [], next_cursor: null });
  });

  await loginAndOpenTournamentList(page);
  await expect(
    page.getByRole("region", { name: "Новое соревнование" }).getByText("Проверяем публикацию"),
  ).toBeVisible();
  await expect(
    page.getByRole("region", { name: "Список соревнований" }).getByText("Загружаем соревнования"),
  ).toBeVisible();

  releaseContent?.();
  releaseList?.();
  await expect(page.getByText("Соревнований пока нет")).toBeVisible();
});

test("показывает пустой список турниров", async ({ page }) => {
  await setupAdminRoutes(page);
  await loginAndOpenTournamentList(page);

  await expect(page.getByText("Всего: 0")).toBeVisible();
  await expect(page.getByText("Соревнований пока нет")).toBeVisible();
});

test("открывает detail турнира, сохраняет подраздел и выбор при reload", async ({ page }) => {
  const selected = tournament({
    id: "10000000-0000-4000-8000-000000000099",
    name: "Detail турнир",
  });
  await setupAdminRoutes(page, { listBody: { items: [selected], next_cursor: null } });
  await loginAndOpenTournamentList(page);

  await page.getByRole("row").filter({ hasText: "Detail турнир" }).getByRole("button", { name: "Открыть" }).click();
  await expect(page.getByRole("heading", { name: "Detail турнир" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Обзор" })).toHaveAttribute("aria-current", "page");
  await expect(page.getByText("Формат", { exact: true })).toBeVisible();
  await expect(page.getByText("Квалификация и плей-офф", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Участники" }).click();
  await expect(page).toHaveURL(new RegExp(`[?&]tournament=${selected.id}(?:&|$)`));
  await expect(page).toHaveURL(new RegExp(`[?&]view=participants(?:&|$)`));
  await page.reload();
  await expect(page.getByRole("heading", { name: "Detail турнир" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Участники" })).toHaveAttribute("aria-current", "page");
});

test("показывает ошибку списка и доступен повторный запрос", async ({ page }) => {
  let listRequests = 0;
  let listUnavailable = true;
  await page.route("**/api/v1/admin/login", async (route) => {
    await fulfillJSON(route, 200, { expires_in: 900 }, {
      "X-CSRF-Token": accessCSRF,
      "X-Admin-Refresh-CSRF-Token": refreshCSRF,
    });
  });
  await page.route("**/api/v1/admin/tasks**", async (route) => {
    await fulfillJSON(route, 200, []);
  });
  await page.route("**/api/v1/admin/tournament-content**", async (route) => {
    await fulfillJSON(route, 200, contentSelection());
  });
  await page.route("**/api/v1/admin/tournaments**", async (route) => {
    listRequests += 1;
    if (listUnavailable) {
      await fulfillJSON(route, 503, problem(503, "Сервис списка временно недоступен"));
      return;
    }
    await fulfillJSON(route, 200, { items: [], next_cursor: null });
  });

  await loginAndOpenTournamentList(page);
  await expect(page.getByRole("region", { name: "Список соревнований" }).getByRole("alert")).toContainText(
    "Ошибка. Сервис списка временно недоступен",
  );
  const failedRequestCount = listRequests;
  expect(failedRequestCount).toBeGreaterThanOrEqual(1);
  listUnavailable = false;
  await page.getByRole("region", { name: "Список соревнований" }).getByRole("button", { name: "Обновить список" }).click();
  await expect(page.getByText("Соревнований пока нет")).toBeVisible();
  expect(listRequests).toBe(failedRequestCount + 1);
});

test("отображает серверные состояния, состав, пресет и даты", async ({ page }) => {
  const stateCases: Array<{
    state: TournamentState;
    label: string;
    rosterSize: number;
    plannedRosterSize: number;
    startedAt: string | null;
    finishedAt: string | null;
    pausedFromState?: TournamentState | null;
  }> = [
    {
      state: "draft",
      label: "Черновик",
      rosterSize: 0,
      plannedRosterSize: 4,
      startedAt: null,
      finishedAt: null,
    },
    {
      state: "registration",
      label: "Регистрация",
      rosterSize: 2,
      plannedRosterSize: 4,
      startedAt: null,
      finishedAt: null,
    },
    {
      state: "roster_locked",
      label: "Состав зафиксирован",
      rosterSize: 4,
      plannedRosterSize: 4,
      startedAt: startedDate,
      finishedAt: null,
    },
    {
      state: "swiss",
      label: "Квалификация",
      rosterSize: 3,
      plannedRosterSize: 8,
      startedAt: startedDate,
      finishedAt: null,
    },
    {
      state: "golden",
      label: "Дополнительный отбор",
      rosterSize: 8,
      plannedRosterSize: 8,
      startedAt: startedDate,
      finishedAt: null,
    },
    {
      state: "playoffs",
      label: "Плей-офф",
      rosterSize: 8,
      plannedRosterSize: 8,
      startedAt: startedDate,
      finishedAt: null,
    },
    {
      state: "technical_pause",
      label: "Техническая пауза",
      rosterSize: 6,
      plannedRosterSize: 8,
      startedAt: startedDate,
      finishedAt: null,
      pausedFromState: "swiss",
    },
    {
      state: "completed",
      label: "Завершено",
      rosterSize: 4,
      plannedRosterSize: 4,
      startedAt: startedDate,
      finishedAt: finishedDate,
    },
    {
      state: "cancelled",
      label: "Отменено",
      rosterSize: 1,
      plannedRosterSize: 4,
      startedAt: startedDate,
      finishedAt: finishedDate,
    },
  ];
  const items = stateCases.map((item, index) =>
    tournament({
      id: `10000000-0000-4000-8000-${String(index + 20).padStart(12, "0")}`,
      name: `Состояние ${item.state}`,
      public_id: `state-${item.state.replaceAll("_", "-")}`,
      state: item.state,
      roster_size: item.rosterSize,
      planned_roster_size: item.plannedRosterSize,
      started_at: item.startedAt,
      finished_at: item.finishedAt,
      paused_from_state: item.pausedFromState ?? null,
    }),
  );

  await setupAdminRoutes(page, { listBody: { items, next_cursor: null } });
  await loginAndOpenTournamentList(page);

  const table = page
    .getByRole("region", { name: "Список соревнований" })
    .getByRole("table");
  await expect(table.getByRole("row")).toHaveCount(stateCases.length + 1);
  for (const item of stateCases) {
    const row = table.getByRole("row").filter({ hasText: `Состояние ${item.state}` });
    await expect(row).toContainText(item.label);
    await expect(row).toContainText(`${item.rosterSize} / ${item.plannedRosterSize}`);
    await expect(row).toContainText("13.09");
    await expect(row.getByRole("button", { name: "Открыть" })).toBeVisible();
  }
});

test("создает турнир с актуальной ревизией и открывает операторский маршрут", async ({ page }) => {
  const createdID = "10000000-0000-4000-8000-000000000090";
  const createRequests: Array<{ body: Record<string, unknown>; idempotencyKey: string }> = [];
  await setupAdminRoutes(page, {
    create: async (route) => {
      const request = route.request();
      createRequests.push({
        body: request.postDataJSON() as Record<string, unknown>,
        idempotencyKey: request.headers()["idempotency-key"] ?? "",
      });
      await fulfillJSON(route, 201, tournament({
        id: createdID,
        name: "Весенний турнир",
        public_id: "vesennij-turnir-demo",
        planned_roster_size: 8,
      }));
    },
  });

  await loginAndOpenTournamentList(page);
  await page.getByLabel("Название соревнования").fill("Весенний турнир");
  await page.getByLabel("Плановый размер состава").selectOption("8");
  const createPanel = page.getByRole("region", { name: "Новое соревнование" });
  await expect(createPanel.getByText("Задачи опубликованы", { exact: true })).toHaveCount(0);
  await expect(createPanel.getByText(/Опубликовано/)).toHaveCount(0);
  const createButton = page.getByRole("button", { name: "Создать соревнование" });
  await expect(createButton).toBeEnabled();
  await Promise.all([
    page.waitForURL(new RegExp(`[?&]tournament=${createdID}(?:&|$)`)),
    createButton.click(),
  ]);

  expect(createRequests).toHaveLength(1);
  expect(createRequests[0].idempotencyKey).toMatch(/^[0-9a-f-]{36}$/i);
  expect(createRequests[0].body).toMatchObject({
    expected_revision: 0,
    preset: "tournament_v1",
    name: "Весенний турнир",
    planned_roster_size: 8,
    content_revision: contentRevision,
  });
  expect(createRequests[0].body.public_id).toMatch(/^vesennij-turnir-[a-z0-9]+$/);
});

test("двойное нажатие отправляет один запрос с одним ключом идемпотентности", async ({ page }) => {
  const createdID = "10000000-0000-4000-8000-000000000091";
  const createRequests: string[] = [];
  let releaseCreate: (() => void) | undefined;
  const createGate = new Promise<void>((resolve) => {
    releaseCreate = resolve;
  });

  await setupAdminRoutes(page, {
    create: async (route) => {
      createRequests.push(route.request().headers()["idempotency-key"] ?? "");
      await createGate;
      await fulfillJSON(route, 201, tournament({ id: createdID, name: "Двойной клик" }));
    },
  });

  await loginAndOpenTournamentList(page);
  await page.getByLabel("Название соревнования").fill("Двойной клик");
  const createButton = page.getByRole("button", { name: "Создать соревнование" });
  await createButton.click({ clickCount: 2 });
  await expect.poll(() => createRequests.length).toBe(1);
  releaseCreate?.();
  await expect(page).toHaveURL(new RegExp(`[?&]tournament=${createdID}(?:&|$)`));
  expect(createRequests).toHaveLength(1);
  expect(createRequests[0]).toMatch(/^[0-9a-f-]{36}$/i);
});

test("не возвращает пользователя в турнир после ухода во время создания", async ({ page }) => {
  const createdID = "10000000-0000-4000-8000-000000000092";
  const createRequests: string[] = [];
  let releaseCreate: (() => void) | undefined;
  const createGate = new Promise<void>((resolve) => {
    releaseCreate = resolve;
  });

  await setupAdminRoutes(page, {
    create: async (route) => {
      createRequests.push(route.request().headers()["idempotency-key"] ?? "");
      await createGate;
      await fulfillJSON(route, 201, tournament({ id: createdID, name: "Поздний ответ" }));
    },
  });

  await loginAndOpenTournamentList(page);
  await page.getByLabel("Название соревнования").fill("Поздний ответ");
  await page.getByRole("button", { name: "Создать соревнование" }).click();
  await expect.poll(() => createRequests.length).toBe(1);

  const dialogPromise = page.waitForEvent("dialog").then(async (dialog) => {
    expect(dialog.message()).toContain("несохраненные изменения");
    await dialog.accept();
  });
  await page.getByRole("button", { name: "Задачи" }).click();
  await dialogPromise;
  await page.getByRole("button", { name: "Создать задачу", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "Создать задачу" })).toBeVisible();

  releaseCreate?.();
  await expect(page).toHaveURL(/[?&]section=tasks(?:&|$)/);
  await expect(page).not.toHaveURL(new RegExp(`[?&]tournament=${createdID}(?:&|$)`));
});

test("409 показывает восстановимое сообщение и сохраняет введенное название", async ({ page }) => {
  await setupAdminRoutes(page, {
    create: async (route) => {
      await fulfillJSON(route, 409, problem(409, "Турнир уже был создан другим оператором"));
    },
  });

  await loginAndOpenTournamentList(page);
  const name = "Конфликтный турнир";
  await page.getByLabel("Название соревнования").fill(name);
  await page.getByRole("button", { name: "Создать соревнование" }).click();
  await expect(page.getByRole("region", { name: "Новое соревнование" }).getByRole("alert")).toContainText(
    "Турнир уже был создан другим оператором",
  );
  await expect(page.getByLabel("Название соревнования")).toHaveValue(name);
  await expect(page.getByRole("button", { name: "Создать соревнование" })).toBeEnabled();
});

test("422 показывает сообщение о ревизии и сохраняет введенное название", async ({ page }) => {
  await setupAdminRoutes(page, {
    create: async (route) => {
      await fulfillJSON(route, 422, problem(422, "Ревизия контента больше недоступна"));
    },
  });

  await loginAndOpenTournamentList(page);
  const name = "Устаревшая ревизия";
  await page.getByLabel("Название соревнования").fill(name);
  await page.getByRole("button", { name: "Создать соревнование" }).click();
  await expect(page.getByRole("region", { name: "Новое соревнование" }).getByRole("alert")).toContainText(
    "Ревизия контента больше недоступна",
  );
  await expect(page.getByLabel("Название соревнования")).toHaveValue(name);
  await expect(page.getByRole("button", { name: "Создать соревнование" })).toBeEnabled();
});

test("список не создает горизонтальный overflow в темной и светлой теме на мобильной ширине", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await setupAdminRoutes(page, {
    listBody: {
      items: [tournament({
        name: "Мобильный турнир",
        public_id: "mobilnyj-turnir",
        planned_roster_size: 16,
        roster_size: 9,
        state: "technical_pause",
        paused_from_state: "swiss",
      })],
      next_cursor: null,
    },
  });
  await loginAndOpenTournamentList(page);

  for (const theme of ["Темная тема", "Светлая тема"]) {
    const accountMenu = await openAccountMenu(page);
    await accountMenu.getByRole("button", { name: theme }).click();
    await expect(page.locator("html")).toHaveAttribute(
      "data-theme",
      theme === "Темная тема" ? "dark" : "light",
    );
    await page.keyboard.press("Escape");
    await expect(page.getByRole("region", { name: "Аккаунт" })).toBeHidden();
    const dimensions = await page.evaluate(() => ({
      bodyWidth: document.body.scrollWidth,
      documentWidth: document.documentElement.scrollWidth,
      viewportWidth: window.innerWidth,
    }));
    expect(dimensions.bodyWidth).toBeLessThanOrEqual(dimensions.viewportWidth);
    expect(dimensions.documentWidth).toBeLessThanOrEqual(dimensions.viewportWidth);
  }
  await expect(
    page
      .getByRole("region", { name: "Список соревнований" })
      .getByRole("table")
      .getByText("Мобильный турнир", { exact: true }),
  ).toBeVisible();
});
