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
  createStatus?: number;
  tasks?: MockAdminTask[];
  sourceStatus?: number;
  updateStatus?: number;
}>;

type SetupState = {
  createCalls: number;
  createPayloads: Partial<MockAdminTask>[];
  listCalls: number;
  sourceCalls: number;
  updateCalls: number;
  updatePayloads: Partial<MockAdminTask>[];
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
    createPayloads: [],
    listCalls: 0,
    sourceCalls: 0,
    updateCalls: 0,
    updatePayloads: [],
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
      if ((options.createStatus ?? 200) !== 200) {
        await fulfillJSON(
          route,
          options.createStatus ?? 422,
          problem(options.createStatus ?? 422, "Категория недоступна для BO1"),
        );
        return;
      }
      const body = request.postDataJSON() as Partial<MockAdminTask>;
      state.createPayloads.push(body);
      const created = taskResponse({
        id: "61000000-0000-4000-8000-000000000001",
        title: String(body.title),
        description: String(body.description),
        category: String(body.category),
        difficulty: String(body.difficulty),
        kind: body.kind === "golden" ? "golden" : "normal",
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
      if ((options.updateStatus ?? 200) !== 200) {
        await fulfillJSON(
          route,
          options.updateStatus ?? 422,
          problem(options.updateStatus ?? 422, "Категория недоступна для BO1"),
        );
        return;
      }
      const body = request.postDataJSON() as Partial<MockAdminTask>;
      state.updatePayloads.push(body);
      const current = tasks.find((task) => task.id === taskID);
      expect(current).toBeDefined();
      if (!current) {
        await fulfillJSON(route, 409, problem(409, "Задача не найдена"));
        return;
      }
      const updated = taskResponse({
        ...current,
        ...body,
        id: taskID,
        version: current.version + 1,
      });
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
        task.id === sourceTaskID
          ? {
              ...task,
              source_file_url: sourceURL,
              version: task.version + 1,
            }
          : task,
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
  await expect(page.getByRole("button", { name: "Задачи" })).toBeVisible();
  await page.getByRole("button", { name: "Задачи" }).click();
  await expect(page.getByRole("button", { name: "Создать задачу", exact: true })).toBeVisible();
};

const openCreateTaskEditor = async (page: Page) => {
  const trigger = page.getByRole("button", { name: "Создать задачу", exact: true });
  await expect(trigger).toBeVisible();
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Создать задачу", exact: true });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByPlaceholder("Введите название...")).toBeFocused();
  return { dialog, trigger };
};

const fillTaskForm = async (
  page: Page,
  values: Readonly<{
    title: string;
    description: string;
    category?: string;
    kind?: "normal" | "golden";
    difficulty?: string;
    timeLimit?: string;
    flag?: string;
    taskURL?: string;
  }>,
): Promise<void> => {
  const form = page.getByRole("dialog").locator("form");
  await form.getByPlaceholder("Введите название...").fill(values.title);
  await form.getByPlaceholder("Опишите задачу...").fill(values.description);
  await form.locator("select").first().selectOption(values.category ?? "forensics");
  await form.getByLabel("Пул задания").selectOption(values.kind ?? "normal");
  await form.locator("select").nth(2).selectOption(values.difficulty ?? "easy");
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

  await expect(page.getByText(`Ревизия ${contentRevision}`)).toHaveCount(0);
  await expect(page.getByText(publicationID, { exact: true })).toHaveCount(0);
  await expect(page.getByText(normalPoolRevisionID, { exact: true })).toHaveCount(0);
  await expect(page.getByText(goldenPoolRevisionID, { exact: true })).toHaveCount(0);
  await expect(page.getByPlaceholder("Введите название...")).toBeHidden();
  await expect(page.getByText("Пока нет созданных задач")).toBeVisible();

  const { dialog: createDialog, trigger: createTrigger } = await openCreateTaskEditor(page);
  await expect(createDialog.getByLabel("Название задачи")).toBeVisible();
  await expect(createDialog.getByLabel("Описание")).toBeVisible();
  await expect(createDialog.getByLabel("Категория")).toBeVisible();
  await expect(createDialog.getByLabel("Сложность")).toBeVisible();
  await expect(createDialog.getByLabel("Пул задания")).toHaveValue("normal");
  await expect(createDialog.getByLabel("Лимит времени (сек)")).toBeVisible();
  await expect(createDialog.getByLabel("Флаг")).toBeVisible();
  await fillTaskForm(page, {
    title: "FE-028 Synthetic Task",
    description: "Задача создана через каталог турниров.",
    kind: "golden",
  });
  await page.locator('input[type="file"]').setInputFiles({
    name: "fe028-source.zip",
    mimeType: "application/zip",
    buffer: Buffer.from("PK\u0005\u0006fe028"),
  });

  await createDialog.getByRole("button", { name: "Создать задачу", exact: true }).click();
  await expect(page.getByText("Задача успешно создана!")).toBeVisible();
  await expect(createDialog).toBeHidden();
  await expect(createTrigger).toBeFocused();
  await expect(page.getByText("Исходники загружены", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: /Скачать.*ZIP/ })).toHaveAttribute(
    "href",
    "https://files.example/fe028-source.zip",
  );
  await expect(page.getByText("FE-028 Synthetic Task", { exact: true })).toBeVisible();
  await expect(page.getByText("Пул: Для дополнительного отбора", { exact: true })).toBeVisible();
  await expect.poll(() => state.listCalls).toBeGreaterThanOrEqual(2);
  await expect(page.getByText("Версия: 2", { exact: true })).toBeVisible();
  expect(state.createPayloads[0]?.kind).toBe("golden");

  await page.getByTitle("Редактировать задачу").click();
  const editDialog = page.getByRole("dialog", { name: "Редактировать задачу", exact: true });
  await expect(editDialog).toBeVisible();
  await expect(editDialog.getByPlaceholder("Введите название...")).toHaveValue("FE-028 Synthetic Task");
  await expect(editDialog.getByLabel("Пул задания")).toHaveValue("golden");
  await editDialog.getByPlaceholder("Введите название...").fill("FE-028 Synthetic Task Updated");
  await editDialog.getByLabel("Пул задания").selectOption("normal");
  await editDialog.getByRole("button", { name: "Сохранить задачу", exact: true }).click();

  await expect(page.getByText("Задача успешно обновлена!")).toBeVisible();
  await expect(editDialog).toBeHidden();
  await expect(page.getByText("FE-028 Synthetic Task Updated", { exact: true })).toBeVisible();
  expect(state.updatePayloads[0]?.kind).toBe("normal");
  await expect.poll(() => state.listCalls).toBeGreaterThanOrEqual(3);
  await expect(page.getByText("Версия: 3", { exact: true })).toBeVisible();
  await expect(page.getByPlaceholder("Введите название...")).toBeHidden();
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
  const { dialog } = await openCreateTaskEditor(page);
  await fillTaskForm(page, { title, description, kind: "golden" });
  await dialog.locator('input[type="file"]').setInputFiles({
    name: "failed-source.zip",
    mimeType: "application/zip",
    buffer: Buffer.from("PK\u0005\u0006failed"),
  });

  await dialog.getByRole("button", { name: "Создать задачу", exact: true }).click();
  await expect(
    page.getByRole("alert").filter({ hasText: "Задача создана, но файл не загрузился" }),
  ).toBeVisible();
  const failedEditor = page.getByRole("dialog");
  await expect(failedEditor.getByPlaceholder("Введите название...")).toHaveValue(title);
  await expect(failedEditor.getByPlaceholder("Опишите задачу...")).toHaveValue(description);
  await expect(failedEditor.getByPlaceholder("flag{...}")).toHaveValue("flag{fe028}");
  await expect(failedEditor.getByPlaceholder("Подсказка 1")).toHaveValue("первая подсказка");
  await expect(failedEditor.getByLabel("Пул задания")).toHaveValue("golden");
  await expect(page.getByText("failed-source.zip")).toBeVisible();
  expect(state.createCalls).toBe(1);
  expect(state.sourceCalls).toBe(1);
});

test("FE-028 server validation keeps create and update form input", async ({ page }) => {
  const existingTask = taskResponse({
    id: "62000000-0000-4000-8000-000000000001",
    title: "FE-028 Existing Task",
    description: "Исходная задача для проверки ошибки обновления.",
    category: "forensics",
    kind: "normal",
    version: 3,
  });
  const state = await setupAdminRoutes(page, {
    createStatus: 422,
    tasks: [existingTask],
    updateStatus: 422,
  });
  await loginAndOpenTaskCatalog(page);

  const { dialog: createDialog } = await openCreateTaskEditor(page);
  await fillTaskForm(page, {
    title: "FE-028 Rejected Create",
    description: "Значения create должны остаться после 422.",
    category: "forensics",
    kind: "golden",
  });
  await createDialog.locator('input[type="file"]').setInputFiles({
    name: "rejected-create.zip",
    mimeType: "application/zip",
    buffer: Buffer.from("PK\u0005\u0006create-422"),
  });
  await createDialog.getByRole("button", { name: "Создать задачу", exact: true }).click();

  await expect(page.getByText("Категория недоступна для BO1")).toBeVisible();
  await expect(createDialog.getByPlaceholder("Введите название...")).toHaveValue(
    "FE-028 Rejected Create",
  );
  await expect(createDialog.getByLabel("Категория")).toHaveValue("forensics");
  await expect(createDialog.getByLabel("Пул задания")).toHaveValue("golden");
  await expect(page.getByText("rejected-create.zip")).toBeVisible();
  expect(state.createCalls).toBe(1);
  expect(state.sourceCalls).toBe(0);

  const closeConfirmation = page.waitForEvent("dialog").then(async (dialog) => {
    expect(dialog.message()).toContain("несохраненные изменения");
    await dialog.accept();
  });
  await createDialog.getByRole("button", { name: "Закрыть редактор задачи", exact: true }).click();
  await closeConfirmation;
  await expect(createDialog).toBeHidden();

  await page.getByTitle("Редактировать задачу").click();
  const editDialog = page.getByRole("dialog", { name: "Редактировать задачу", exact: true });
  await expect(editDialog).toBeVisible();
  await expect(editDialog.getByPlaceholder("Введите название...")).toHaveValue(existingTask.title);
  await editDialog.getByPlaceholder("Введите название...").fill("FE-028 Rejected Update");
  await editDialog.getByLabel("Пул задания").selectOption("golden");
  await editDialog.locator('input[type="file"]').setInputFiles({
    name: "rejected-update.zip",
    mimeType: "application/zip",
    buffer: Buffer.from("PK\u0005\u0006update-422"),
  });
  await editDialog.getByRole("button", { name: "Сохранить задачу", exact: true }).click();

  await expect(page.getByText("Категория недоступна для BO1")).toBeVisible();
  await expect(editDialog.getByPlaceholder("Введите название...")).toHaveValue(
    "FE-028 Rejected Update",
  );
  await expect(editDialog.getByLabel("Категория")).toHaveValue("forensics");
  await expect(editDialog.getByLabel("Пул задания")).toHaveValue("golden");
  await expect(page.getByText("rejected-update.zip")).toBeVisible();
  expect(state.updateCalls).toBe(1);
  expect(state.sourceCalls).toBe(0);
});

test("защищает переход между задачами после изменения только select и лимита времени", async ({ page }) => {
  const firstTask = taskResponse({
    id: "62000000-0000-4000-8000-000000000010",
    title: "FE-028 First Task",
    description: "Первая задача для проверки dirty состояния.",
    category: "forensics",
    difficulty: "easy",
    kind: "normal",
    time_limit: 120,
  });
  const secondTask = taskResponse({
    id: "62000000-0000-4000-8000-000000000011",
    title: "FE-028 Second Task",
    description: "Вторая задача для проверки перехода.",
    category: "web",
    difficulty: "medium",
    kind: "golden",
    time_limit: 90,
  });
  await setupAdminRoutes(page, { tasks: [firstTask, secondTask] });
  await loginAndOpenTaskCatalog(page);
  await page.getByTitle("Редактировать задачу").first().click();
  const editor = page.getByRole("dialog", { name: "Редактировать задачу", exact: true });
  await expect(editor).toBeVisible();
  await editor.getByLabel("Категория").selectOption("crypto");
  await editor.getByLabel("Лимит времени (сек)").fill("180");

  const closeDialogMessage = new Promise<string>((resolve) => {
    page.once("dialog", async (dialog) => {
      resolve(dialog.message());
      await dialog.dismiss();
    });
  });
  await editor.getByRole("button", { name: "Закрыть редактор задачи", exact: true }).click();
  await expect(closeDialogMessage).resolves.toContain("несохраненные изменения");
  await expect(editor).toBeVisible();
  await expect(editor.getByLabel("Категория")).toHaveValue("crypto");
  await expect(editor.getByLabel("Лимит времени (сек)")).toHaveValue("180");

  const closeAccepted = page.waitForEvent("dialog").then(async (dialog) => {
    expect(dialog.message()).toContain("несохраненные изменения");
    await dialog.accept();
  });
  await editor.getByRole("button", { name: "Закрыть редактор задачи", exact: true }).click();
  await closeAccepted;
  await expect(editor).toBeHidden();

  await page.getByTitle("Редактировать задачу").nth(1).click();
  await expect(editor).toBeVisible();
  await expect(editor.getByPlaceholder("Введите название...")).toHaveValue("FE-028 Second Task");
});

test("FE-028 task list uses two columns on desktop and one on mobile with modal focus", async ({ page }) => {
  const firstTask = taskResponse({
    id: "62000000-0000-4000-8000-000000000020",
    title: "FE-028 Grid First",
  });
  const secondTask = taskResponse({
    id: "62000000-0000-4000-8000-000000000021",
    title: "FE-028 Grid Second",
  });
  await setupAdminRoutes(page, { tasks: [firstTask, secondTask] });
  await loginAndOpenTaskCatalog(page);

  const taskList = page.locator('article[aria-labelledby="admin-task-list-title"] [aria-label="Каталог задач"]');
  await expect(taskList).toBeVisible();
  const desktopColumns = await taskList.evaluate((element) =>
    getComputedStyle(element).gridTemplateColumns.split(" ").filter(Boolean).length,
  );
  expect(desktopColumns).toBe(2);
  await page.screenshot({ path: test.info().outputPath("task-list-desktop.png"), fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  const mobileColumns = await taskList.evaluate((element) =>
    getComputedStyle(element).gridTemplateColumns.split(" ").filter(Boolean).length,
  );
  expect(mobileColumns).toBe(1);
  await page.screenshot({ path: test.info().outputPath("task-list-mobile.png"), fullPage: true });

  const { dialog, trigger } = await openCreateTaskEditor(page);
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(trigger).toBeFocused();

  await page.getByTitle("Редактировать задачу").first().click();
  const editDialog = page.getByRole("dialog", { name: "Редактировать задачу", exact: true });
  await expect(editDialog.getByPlaceholder("Введите название...")).toHaveValue("FE-028 Grid First");
  await editDialog.getByRole("button", { name: "Закрыть редактор задачи", exact: true }).click();
  await expect(editDialog).toBeHidden();
  await page.getByTitle("Редактировать задачу").first().click();
  await expect(editDialog).toBeVisible();
  await expect(editDialog.getByPlaceholder("Введите название...")).toHaveValue("FE-028 Grid First");
});

test("FE-028 empty catalog and unavailable content keep Russian responsive UI in both themes", async ({ page }) => {
  await setupAdminRoutes(page, { contentStatus: 422 });
  await loginAndOpenTaskCatalog(page);

  await expect(page.getByText("Публикации пока нет")).toBeVisible();
  await expect(page.getByText(/Опубликованных задач пока нет/)).toBeVisible();
  await expect(page.getByText("Пока нет созданных задач")).toBeVisible();
  await expect(page.getByRole("button", { name: "Создать задачу" })).toBeEnabled();
  const contentRoot = page.locator('article[aria-labelledby="admin-task-list-title"]');

  await page.getByRole("button", { name: "Темная тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  const darkBackground = await contentRoot.evaluate((element) => getComputedStyle(element).backgroundColor);
  await page.getByRole("button", { name: "Светлая тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  const lightBackground = await contentRoot.evaluate((element) => getComputedStyle(element).backgroundColor);
  expect(darkBackground).not.toBe(lightBackground);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByText("Публикации пока нет")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
