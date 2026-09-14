import { expect, test, type Page, type Route } from "@playwright/test";

import {
  adminSessionResponse,
  type MockAdminTask,
  taskResponse,
} from "./support/admin";
import { jsonHeaders } from "./support/common";

const accessCSRF = "fe028-admin-access-csrf";
const refreshCSRF = "fe028-admin-refresh-csrf";
const contentRevision = 73;
const publicationID = "60000000-0000-4000-8000-000000000001";
const normalPoolRevisionID = "60000000-0000-4000-8000-000000000002";
const goldenPoolRevisionID = "60000000-0000-4000-8000-000000000003";

type SetupOptions = Readonly<{
  contentStatus?: number;
  tasks?: MockAdminTask[];
  sourceStatus?: number;
}>;

type SetupState = {
  createCalls: number;
  listCalls: number;
  sourceCalls: number;
  updateCalls: number;
};

const contentSelection = () => ({
  content_revision: contentRevision,
  publication_id: publicationID,
  published_at: "2026-09-14T08:00:00Z",
  normal_pool_revision_id: normalPoolRevisionID,
  golden_pool_revision_id: goldenPoolRevisionID,
});

const problem = (status: number, detail: string) => ({
  type: "about:blank",
  title: status === 503 ? "Service Unavailable" : "Unprocessable Content",
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
  options: SetupOptions = {},
): Promise<SetupState> => {
  const state: SetupState = {
    createCalls: 0,
    listCalls: 0,
    sourceCalls: 0,
    updateCalls: 0,
  };
  let tasks = [...(options.tasks ?? [])];

  await page.route("**/api/v1/admin/login", async (route) => {
    expect(route.request().method()).toBe("POST");
    await fulfillJSON(route, 200, adminSessionResponse(), {
      "X-CSRF-Token": accessCSRF,
      "X-Admin-Refresh-CSRF-Token": refreshCSRF,
    });
  });

  await page.route("**/api/v1/admin/refresh", async (route) => {
    await fulfillJSON(route, 200, adminSessionResponse(), {
      "X-CSRF-Token": accessCSRF,
      "X-Admin-Refresh-CSRF-Token": refreshCSRF,
    });
  });

  await page.route("**/api/v1/admin/tournament-content", async (route) => {
    if ((options.contentStatus ?? 200) !== 200) {
      await fulfillJSON(
        route,
        options.contentStatus ?? 422,
        problem(options.contentStatus ?? 422, "Нет доступной опубликованной ревизии контента"),
      );
      return;
    }
    await fulfillJSON(route, 200, contentSelection());
  });

  await page.route("**/api/v1/admin/tournaments**", async (route) => {
    if (route.request().method() !== "GET") {
      await fulfillJSON(route, 404, {});
      return;
    }
    await fulfillJSON(route, 200, { items: [], next_cursor: null });
  });

  await page.route("**/api/v1/admin/tasks**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();

    if (path === "/api/v1/admin/tasks" && method === "GET") {
      state.listCalls += 1;
      await fulfillJSON(route, 200, tasks);
      return;
    }

    if (path === "/api/v1/admin/tasks" && method === "POST") {
      state.createCalls += 1;
      const body = request.postDataJSON() as Partial<MockAdminTask>;
      const created = taskResponse({
        id: "61000000-0000-4000-8000-000000000001",
        title: String(body.title),
        description: String(body.description),
        category: String(body.category),
        difficulty: String(body.difficulty),
        time_limit: Number(body.time_limit),
        flag: String(body.flag),
        hints: Array.isArray(body.hints) ? body.hints : [],
        task_url: typeof body.task_url === "string" ? body.task_url : null,
      });
      tasks = [created, ...tasks];
      await fulfillJSON(route, 200, created);
      return;
    }

    const taskID = path.match(/^\/api\/v1\/admin\/tasks\/([^/]+)$/)?.[1];
    if (taskID && method === "PUT") {
      state.updateCalls += 1;
      const body = request.postDataJSON() as Partial<MockAdminTask>;
      const current = tasks.find((task) => task.id === taskID);
      expect(current).toBeDefined();
      if (!current) {
        await fulfillJSON(route, 409, problem(409, "Задача не найдена"));
        return;
      }
      const updated = taskResponse({ ...current, ...body, id: taskID });
      tasks = tasks.map((task) => (task.id === taskID ? updated : task));
      await fulfillJSON(route, 200, updated);
      return;
    }

    const sourceTaskID = path.match(/^\/api\/v1\/admin\/tasks\/([^/]+)\/source$/)?.[1];
    if (sourceTaskID && method === "POST") {
      state.sourceCalls += 1;
      if ((options.sourceStatus ?? 200) !== 200) {
        await fulfillJSON(
          route,
          options.sourceStatus ?? 503,
          problem(options.sourceStatus ?? 503, "Хранилище исходников временно недоступно"),
        );
        return;
      }
      const sourceURL = "https://files.example/fe028-source.zip";
      tasks = tasks.map((task) =>
        task.id === sourceTaskID ? { ...task, source_file_url: sourceURL } : task,
      );
      await fulfillJSON(route, 200, { source_file_url: sourceURL });
      return;
    }

    await fulfillJSON(route, 404, {});
  });

  return state;
};

const loginAndOpenTaskCatalog = async (page: Page): Promise<void> => {
  await page.goto("/admin");
  await page.getByPlaceholder("Введите пароль...").fill("correct-password");
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("button", { name: "Турниры" })).toBeVisible();
  await page.getByRole("button", { name: "Турниры" }).click();
  await expect(page.getByPlaceholder("Введите название...")).toBeVisible();
};

const fillTaskForm = async (
  page: Page,
  values: Readonly<{
    title: string;
    description: string;
    category?: string;
    difficulty?: string;
    timeLimit?: string;
    flag?: string;
    taskURL?: string;
  }>,
): Promise<void> => {
  const form = page.locator("form").filter({ has: page.getByPlaceholder("Введите название...") }).first();
  await form.getByPlaceholder("Введите название...").fill(values.title);
  await form.getByPlaceholder("Опишите задачу...").fill(values.description);
  await form.locator("select").first().selectOption(values.category ?? "forensics");
  await form.locator("select").nth(1).selectOption(values.difficulty ?? "easy");
  await form.getByPlaceholder("60").fill(values.timeLimit ?? "120");
  await form.getByPlaceholder("flag{...}").fill(values.flag ?? "flag{fe028}");
  await form.getByPlaceholder("https://example.com/task").fill(values.taskURL ?? "");
  await form.getByPlaceholder("Подсказка 1").fill("первая подсказка");
  await form.getByPlaceholder("Подсказка 2").fill("вторая подсказка");
  await form.getByPlaceholder("Подсказка 3").fill("третья подсказка");
};

test("FE-028 catalog creates a task with ZIP, refreshes, and updates it", async ({ page }) => {
  const state = await setupAdminRoutes(page);
  await loginAndOpenTaskCatalog(page);
  const contentCatalog = page.getByRole("region", { name: "Каталог контента" });

  await expect(contentCatalog.getByText(`Ревизия ${contentRevision}`)).toBeVisible();
  await expect(contentCatalog.getByText(publicationID, { exact: true })).toBeVisible();
  await expect(contentCatalog.getByText(normalPoolRevisionID, { exact: true })).toBeVisible();
  await expect(contentCatalog.getByText(goldenPoolRevisionID, { exact: true })).toBeVisible();
  await expect(contentCatalog.getByLabel("Название задачи")).toBeVisible();
  await expect(contentCatalog.getByLabel("Описание")).toBeVisible();
  await expect(contentCatalog.getByLabel("Категория")).toBeVisible();
  await expect(contentCatalog.getByLabel("Сложность")).toBeVisible();
  await expect(contentCatalog.getByLabel("Лимит времени (сек)")).toBeVisible();
  await expect(contentCatalog.getByLabel("Флаг")).toBeVisible();
  await expect(page.getByText("Пока нет созданных задач")).toBeVisible();

  await fillTaskForm(page, {
    title: "FE-028 Synthetic Task",
    description: "Задача создана через каталог турниров.",
  });
  await page.locator('input[type="file"]').setInputFiles({
    name: "fe028-source.zip",
    mimeType: "application/zip",
    buffer: Buffer.from("PK\u0005\u0006fe028"),
  });

  await page.getByRole("button", { name: /Создать задачу/ }).click();
  await expect(page.getByText("Задача успешно создана!")).toBeVisible();
  await expect(page.getByText("Исходники загружены в SeaweedFS")).toBeVisible();
  await expect(page.getByRole("link", { name: /Скачать.*ZIP/ })).toHaveAttribute(
    "href",
    "https://files.example/fe028-source.zip",
  );
  await expect(page.getByText("FE-028 Synthetic Task", { exact: true })).toBeVisible();
  await expect.poll(() => state.listCalls).toBeGreaterThanOrEqual(2);

  await page.getByTitle("Редактировать задачу").click();
  await page.getByPlaceholder("Введите название...").fill("FE-028 Synthetic Task Updated");
  await page.getByRole("button", { name: /Сохранить задачу/ }).click();

  await expect(page.getByText("Задача успешно обновлена!")).toBeVisible();
  await expect(page.getByText("FE-028 Synthetic Task Updated", { exact: true })).toBeVisible();
  expect(state.createCalls).toBe(1);
  expect(state.sourceCalls).toBe(1);
  expect(state.updateCalls).toBe(1);
  await expect.poll(() => state.listCalls).toBeGreaterThanOrEqual(3);
});

test("FE-028 upload failure keeps entered task data in the catalog form", async ({ page }) => {
  const state = await setupAdminRoutes(page, { sourceStatus: 503 });
  await loginAndOpenTaskCatalog(page);

  const title = "FE-028 Upload Retry Task";
  const description = "Данные формы должны остаться после ошибки ZIP.";
  await fillTaskForm(page, { title, description });
  await page.locator('input[type="file"]').setInputFiles({
    name: "failed-source.zip",
    mimeType: "application/zip",
    buffer: Buffer.from("PK\u0005\u0006failed"),
  });

  await page.getByRole("button", { name: /Создать задачу/ }).click();
  await expect(page.getByText("Задача создана, но файл не загрузился")).toBeVisible();
  await expect(page.getByPlaceholder("Введите название...")).toHaveValue(title);
  await expect(page.getByPlaceholder("Опишите задачу...")).toHaveValue(description);
  await expect(page.getByPlaceholder("flag{...}")).toHaveValue("flag{fe028}");
  await expect(page.getByPlaceholder("Подсказка 1")).toHaveValue("первая подсказка");
  await expect(page.getByText("failed-source.zip")).toBeVisible();
  expect(state.createCalls).toBe(1);
  expect(state.sourceCalls).toBe(1);
});

test("FE-028 empty catalog and unavailable content keep Russian responsive UI in both themes", async ({ page }) => {
  await setupAdminRoutes(page, { contentStatus: 422 });
  await loginAndOpenTaskCatalog(page);
  const contentCatalog = page.getByRole("region", { name: "Каталог контента" });

  await expect(contentCatalog.getByText("Контент недоступен")).toBeVisible();
  await expect(contentCatalog.getByText("Нет доступной опубликованной ревизии контента")).toBeVisible();
  await expect(page.getByText("Пока нет созданных задач")).toBeVisible();
  await expect(page.getByRole("button", { name: "Создать демо-турнир" })).toBeDisabled();

  await page.getByRole("button", { name: "Темная тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  const darkBackground = await contentCatalog.evaluate((element) => getComputedStyle(element).backgroundColor);
  await page.getByRole("button", { name: "Светлая тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  const lightBackground = await contentCatalog.evaluate((element) => getComputedStyle(element).backgroundColor);
  expect(darkBackground).not.toBe(lightBackground);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(contentCatalog.getByText("Контент недоступен")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
