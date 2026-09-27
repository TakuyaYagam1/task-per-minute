import { expect, test, type Page } from "@playwright/test";

const assertHeaderLayout = async (
  page: Page,
  compact: boolean,
  expectLogout = false,
): Promise<void> => {
  const header = page.getByTestId("site-header");
  await expect(header).toHaveCount(1);
  await expect(header.getByRole("link", { name: "Arena", exact: true })).toBeVisible();
  await expect(
    header.getByRole("link", { name: compact ? "Рейтинг" : "Общий рейтинг", exact: true }),
  ).toBeVisible();
  await expect(header.getByLabel("Тема интерфейса")).toHaveCount(1);
  if (expectLogout) {
    await expect(header.getByRole("button", { name: "Выйти" })).toBeVisible();
  } else {
    await expect(header.getByRole("button", { name: "Выйти" })).toHaveCount(0);
  }

  const metrics = await header.evaluate((element) => {
    const brand = element.querySelector("a")?.getBoundingClientRect();
    const actions = Array.from(element.querySelectorAll("nav > a, nav > button, nav > [role='group']"))
      .map((action) => action.getBoundingClientRect())
      .map((rect) => ({ left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom }));
    const rect = element.getBoundingClientRect();
    return {
      actions,
      brandHeight: brand?.height ?? 0,
      height: rect.height,
      left: rect.left,
      right: rect.right,
      top: rect.top,
      viewportWidth: document.documentElement.clientWidth,
    };
  });

  expect(metrics.top).toBe(0);
  expect(metrics.left).toBeGreaterThanOrEqual(0);
  expect(metrics.right).toBeLessThanOrEqual(metrics.viewportWidth);
  expect(metrics.height).toBeLessThan(120);
  if (compact) {
    expect(metrics.brandHeight).toBeGreaterThanOrEqual(44);
    expect(metrics.actions.every((action) => action.bottom - action.top >= 44)).toBe(true);
  }
  for (let index = 1; index < metrics.actions.length; index += 1) {
    expect(metrics.actions[index - 1].right).toBeLessThanOrEqual(metrics.actions[index].left + 1);
  }

  await page.evaluate(() => {
    document.body.style.minHeight = "220vh";
    window.scrollTo({ top: document.body.scrollHeight, behavior: "auto" });
  });
  await expect.poll(async () => page.evaluate(() => window.scrollY)).toBeGreaterThan(0);
  await expect.poll(async () => header.evaluate((element) => element.getBoundingClientRect().top)).toBeLessThanOrEqual(1);
  await expect.poll(async () => header.evaluate((element) => element.getBoundingClientRect().top)).toBeGreaterThanOrEqual(-1);
  await expect.poll(async () => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
}

test.describe("site header", () => {
  test("stays single, pinned, and usable on home at desktop and mobile widths", async ({ page }) => {
    for (const viewport of [
      { width: 1440, height: 900, compact: false },
      { width: 320, height: 844, compact: true },
    ]) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto("/");
      await assertHeaderLayout(page, viewport.compact);
    }
  });

  test("is present on the administrator login without a duplicate logout action", async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 844 });
    await page.goto("/admin");

    await expect(page.getByPlaceholder("Введите пароль...")).toBeVisible();
    await assertHeaderLayout(page, true);
  });

  test("uses the shared admin logout callback and shows pending state on mobile", async ({ page }) => {
    let releaseLogout: (() => void) | undefined;
    const logoutGate = new Promise<void>((resolve) => {
      releaseLogout = resolve;
    });

    await page.route("**/api/v1/admin/login", async (route) => {
      await route.fulfill({
        status: 200,
        headers: {
          "content-type": "application/json",
          "X-CSRF-Token": "header-admin-csrf",
        },
        body: JSON.stringify({ expires_in: 900 }),
      });
    });
    await page.route("**/api/v1/admin/tournaments**", async (route) => {
      await route.fulfill({
        status: 200,
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ items: [], next_cursor: null }),
      });
    });
    await page.route("**/api/v1/admin/tournament-content", async (route) => {
      await route.fulfill({
        status: 200,
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          content_revision: 42,
          publication_id: "10000000-0000-4000-8000-000000000001",
          published_at: "2026-09-13T10:00:00Z",
          normal_pool_revision_id: "10000000-0000-4000-8000-000000000002",
          golden_pool_revision_id: "10000000-0000-4000-8000-000000000003",
        }),
      });
    });
    await page.route("**/api/v1/admin/logout", async (route) => {
      await logoutGate;
      await route.fulfill({ status: 204 });
    });

    await page.setViewportSize({ width: 320, height: 844 });
    await page.goto("/admin");
    await page.getByPlaceholder("Введите пароль...").fill("correct-password");
    await page.getByRole("button", { name: "Войти" }).click();

    await expect(page.getByTestId("site-header").getByRole("button", { name: "Выйти" })).toBeVisible();
    await assertHeaderLayout(page, true, true);
    await page.getByTestId("site-header").getByRole("button", { name: "Выйти" }).click();
    await expect(page.getByTestId("site-header").locator('button[aria-busy="true"]')).toBeVisible();
    releaseLogout?.();
    await expect(page.getByPlaceholder("Введите пароль...")).toBeVisible();
  });
});
