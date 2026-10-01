import { expect, test, type Page } from "@playwright/test";
import { jsonHeaders, openAccountMenu } from "./support/common";

const mockSignedInPlayer = async (page: Page): Promise<void> => {
  await page.route("**/api/v1/players/notifications", async (route) => {
    await route.fulfill({ json: { notifications: [] } });
  });
  await page.route("**/api/v1/players/notifications/events", async (route) => {
    await route.fulfill({ status: 204 });
  });
  await page.route("**/api/v1/players/me", async (route) => {
    await route.fulfill({ status: 200, json: { player: {
      id: "77777777-7777-7777-7777-777777777777",
      username: "demo", created_at: "2026-10-01T00:00:00Z",
    } } });
  });
};

const assertHeaderLayout = async (
  page: Page,
  compact: boolean,
  expectLogout = false,
  expectRating = true,
  brandLabel = "Arena",
): Promise<void> => {
  const header = page.getByTestId("site-header");
  await expect(header).toHaveCount(1);
  const brand = header.getByRole("link", { name: brandLabel, exact: true });
  await expect(brand).toBeVisible();
  await expect(brand).toHaveAttribute(
    "href",
    brandLabel === "Панель управления - на главную" ? "/" : "/arena",
  );
  if (expectRating) {
    await expect(
      header.getByRole("link", { name: compact ? "Рейтинг" : "Общий рейтинг", exact: true }),
    ).toBeVisible();
  } else {
    await expect(header.getByRole("link", { name: "Рейтинг", exact: true })).toHaveCount(0);
    await expect(header.getByRole("link", { name: "Общий рейтинг", exact: true })).toHaveCount(0);
  }
  const accountTrigger = header.getByRole("button", { name: "Меню аккаунта", exact: true });
  if (expectLogout) {
    await expect(accountTrigger).toBeVisible();
    await expect(accountTrigger).toHaveAttribute("aria-expanded", "false");
    const accountMenu = await openAccountMenu(page);
    await expect(accountMenu.getByRole("group", { name: "Тема интерфейса" })).toBeVisible();
    await expect(accountMenu.getByRole("button", { name: "Выйти", exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(accountMenu).toBeHidden();
    await expect(accountTrigger).toHaveAttribute("aria-expanded", "false");
  } else {
    await expect(accountTrigger).toHaveCount(0);
    await expect(header.getByRole("switch", { name: "Светлая тема", exact: true })).toBeVisible();
    await expect(header.getByRole("button", { name: "EN", exact: true })).toBeDisabled();
  }

  const metrics = await header.evaluate((element) => {
    const brand = element.querySelector("a")?.getBoundingClientRect();
    const actions = Array.from(element.querySelectorAll("nav > a, nav > button, nav > [role='group'], nav > div"))
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
    if (Math.abs(metrics.actions[index - 1].top - metrics.actions[index].top) < 1) {
      expect(metrics.actions[index - 1].right).toBeLessThanOrEqual(metrics.actions[index].left + 1);
    }
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
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/v1/players/account/avatar", async (route) => {
      await route.fulfill({ status: 404, body: "" });
    });
    await page.route("**/api/v1/players/me", async (route) => {
      await route.fulfill({
        status: 401,
        headers: { "content-type": "application/problem+json" },
        body: JSON.stringify({ type: "about:blank", title: "Unauthorized", status: 401 }),
      });
    });
  });

  for (const [format, contentType, base64] of [
    ["PNG", "image/png", "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j9pAAAAAASUVORK5CYII="],
    ["GIF", "image/gif", "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"],
  ] as const) {
    test(`shows the saved ${format} avatar in the circular account button on public pages`, async ({ page }) => {
      await mockSignedInPlayer(page);
      await page.route("**/api/v1/players/account/avatar", async (route) => {
        await route.fulfill({ status: 200, contentType, body: Buffer.from(base64, "base64") });
      });
      await page.goto("/leaderboard");
      const trigger = page.getByRole("button", { name: "Меню аккаунта", exact: true });
      const avatar = trigger.locator("img");
      await expect(avatar).toBeVisible();
      await expect.poll(() => avatar.evaluate((element: HTMLImageElement) => element.complete && element.naturalWidth > 0)).toBe(true);
      const shape = await trigger.evaluate((element) => {
        const rect = element.getBoundingClientRect();
        return { width: rect.width, height: rect.height, radius: getComputedStyle(element).borderRadius };
      });
      expect(shape.width).toBe(shape.height);
      expect(shape.radius === "50%" || parseFloat(shape.radius) >= shape.width / 2).toBe(true);
      await trigger.click();
      await expect(page.getByRole("region", { name: "Аккаунт", exact: true })).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(trigger).toBeFocused();
    });
  }

  test("keeps the account menu usable when the avatar cannot be decoded", async ({ page }) => {
    await mockSignedInPlayer(page);
    await page.addInitScript(() => {
      window.addEventListener("error", (event) => {
        if (event.target instanceof HTMLImageElement && event.target.closest('button[aria-label="Меню аккаунта"]')) {
          document.documentElement.dataset.avatarError = "true";
        }
      }, true);
    });
    await page.route("**/api/v1/players/account/avatar", async (route) => {
      await route.fulfill({ status: 200, contentType: "image/png", body: "invalid image" });
    });
    const response = page.waitForResponse("**/api/v1/players/account/avatar");
    await page.goto("/leaderboard");
    await response;
    const trigger = page.getByRole("button", { name: "Меню аккаунта", exact: true });
    await expect(page.locator("html")).toHaveAttribute("data-avatar-error", "true");
    await expect(trigger.locator("img")).toHaveCount(0);
    await expect(trigger.locator("svg")).toBeVisible();
    await trigger.click();
    await expect(page.getByRole("region", { name: "Аккаунт", exact: true })).toBeVisible();
  });

  test("clears the player avatar after logout", async ({ page }) => {
    let signedOut = false;
    await mockSignedInPlayer(page);
    await page.route("**/api/v1/public/tournaments**", async (route) => {
      await route.fulfill({ status: 200, json: { items: [], next_cursor: null } });
    });
    await page.route("**/api/v1/players/me", async (route) => {
      await route.fulfill({ status: 200, json: { player: {
        id: "77777777-7777-7777-7777-777777777777",
        username: "demo",
        created_at: "2026-10-01T00:00:00Z",
      } } });
    });
    await page.route("**/api/v1/players/account/avatar", async (route) => {
      await route.fulfill(signedOut
        ? { status: 401, contentType: "application/problem+json", body: JSON.stringify({ title: "Unauthorized", status: 401 }) }
        : { status: 200, contentType: "image/gif", body: Buffer.from("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7", "base64") });
    });
    await page.route("**/api/v1/players/logout", async (route) => {
      signedOut = true;
      await route.fulfill({ status: 204 });
    });
    await page.goto("/arena");
    const trigger = page.getByRole("button", { name: "Меню аккаунта", exact: true });
    await expect(trigger.locator("img")).toBeVisible();
    const menu = await openAccountMenu(page);
    await menu.getByRole("button", { name: "Выйти", exact: true }).click();
    await expect(page).toHaveURL(/\/login$/);
    await expect(trigger).toHaveCount(0);
    await expect(page.getByRole("switch", { name: "Светлая тема", exact: true })).toBeVisible();
  });

  test("guests can switch theme and use auth links without an account menu", async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 844 });
    await page.goto("/");
    await expect(page).toHaveURL(/\/arena$/);

    const trigger = page.getByRole("button", { name: "Меню аккаунта", exact: true });
    await expect(trigger).toHaveCount(0);
    await expect(page.getByRole("region", { name: "Аккаунт", exact: true })).toHaveCount(0);
    const header = page.getByTestId("site-header");
    await expect(header.getByRole("link", { name: "Войти", exact: true })).toBeVisible();
    await expect(header.getByRole("link", { name: "Регистрация", exact: true })).toBeVisible();
    await expect(header.getByRole("button", { name: "RU", exact: true })).toHaveAttribute("aria-pressed", "true");
    await expect(header.getByRole("button", { name: "EN", exact: true })).toBeDisabled();
    const themeSwitch = header.getByRole("switch", { name: "Светлая тема", exact: true });
    await themeSwitch.focus();
    await themeSwitch.press("Space");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await expect(themeSwitch).toHaveAttribute("aria-checked", "true");

    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await themeSwitch.click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
    await expect(themeSwitch).toHaveAttribute("aria-checked", "false");
  });

  test("stays single, pinned, and usable on the guest arena at desktop and mobile widths", async ({ page }) => {
    for (const viewport of [
      { width: 1440, height: 900, compact: false },
      { width: 320, height: 844, compact: true },
    ]) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto("/");
      await expect(page).toHaveURL(/\/arena$/);
      await assertHeaderLayout(page, viewport.compact);
    }
  });

  test("is present on the administrator login without a duplicate logout action", async ({ page }) => {
    let avatarRequests = 0;
    await page.route("**/api/v1/players/account/avatar", async (route) => {
      avatarRequests += 1;
      await route.fulfill({ status: 404, body: "" });
    });
    await page.setViewportSize({ width: 320, height: 844 });
    await page.goto("/admin");

    await expect(page.getByPlaceholder("Введите пароль...")).toBeVisible();
    await assertHeaderLayout(page, true, false, false, "Панель управления - на главную");
    expect(avatarRequests).toBe(0);

    await expect(page.getByTestId("site-header").getByRole("link", { name: "Регистрация", exact: true })).toHaveCount(0);
  });

  test("auth pages show theme and language controls even with an existing player session", async ({ page }) => {
    await mockSignedInPlayer(page);
    let avatarRequests = 0;
    await page.route("**/api/v1/players/account/avatar", async (route) => {
      avatarRequests += 1;
      await route.fulfill({ status: 404, body: "" });
    });
    for (const path of ["/login", "/register", "/verify-email"]) {
      await page.goto(path);
      const header = page.getByTestId("site-header");
      await expect(header.getByRole("switch", { name: "Светлая тема", exact: true })).toBeVisible();
      await expect(header.getByRole("button", { name: "EN", exact: true })).toBeDisabled();
      await expect(header.getByRole("button", { name: "Меню аккаунта", exact: true })).toHaveCount(0);
      await expect(header.getByRole("link", { name: "Войти", exact: true })).toHaveCount(0);
      await expect(header.getByRole("link", { name: "Регистрация", exact: true })).toHaveCount(0);
    }
    expect(avatarRequests).toBe(0);
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
    await page.route("**/api/v1/admin/refresh", async (route) => {
      await route.fulfill({
        status: 200,
        headers: {
          ...jsonHeaders,
          "X-CSRF-Token": "header-admin-csrf",
        },
        body: JSON.stringify({ expires_in: 900 }),
      });
    });
    await page.route("**/api/v1/admin/players", async (route) => {
      await route.fulfill({ status: 200, headers: jsonHeaders, body: "[]" });
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
    let logoutCalls = 0;
    await page.route("**/api/v1/admin/logout", async (route) => {
      logoutCalls += 1;
      await logoutGate;
      await route.fulfill({ status: 204 });
    });

    await page.addInitScript(() => {
      const nativeEventSource = window.EventSource;
      class QuietAdminEventSource extends EventTarget {
        static readonly CONNECTING = 0;
        static readonly OPEN = 1;
        static readonly CLOSED = 2;

        readonly url: string;
        readonly withCredentials: boolean;
        readyState = QuietAdminEventSource.OPEN;
        onopen: ((this: EventSource, event: Event) => unknown) | null = null;
        onmessage: ((this: EventSource, event: MessageEvent) => unknown) | null = null;
        onerror: ((this: EventSource, event: Event) => unknown) | null = null;

        constructor(url: string | URL, init?: EventSourceInit) {
          super();
          this.url = String(url);
          this.withCredentials = Boolean(init?.withCredentials);
        }

        close(): void {
          this.readyState = QuietAdminEventSource.CLOSED;
        }
      }

      const eventSource = new Proxy(nativeEventSource, {
        construct(target, args) {
          const url = String(args[0] ?? "");
          const path = new URL(url, window.location.href).pathname;
          if (path !== "/api/v1/admin/events") {
            return Reflect.construct(target, args);
          }
          return new QuietAdminEventSource(url, args[1] as EventSourceInit | undefined);
        },
      });
      Object.defineProperty(window, "EventSource", { configurable: true, value: eventSource });
    });

    await page.setViewportSize({ width: 320, height: 844 });
    await page.goto("/admin");
    await page.getByPlaceholder("Введите пароль...").fill("correct-password");
    await page.getByRole("button", { name: "Войти" }).click();

    await assertHeaderLayout(page, true, true, true, "Панель управления - на главную");
    const accountMenu = await openAccountMenu(page);
    await expect(accountMenu.getByRole("link", { name: "Настройки", exact: true })).toHaveCount(0);
    await expect(accountMenu.getByRole("button", { name: "Настройки", exact: true })).toHaveCount(0);
    const logout = accountMenu.getByRole("button", { name: "Выйти", exact: true });
    await logout.click();
    await expect(accountMenu.locator('button[aria-busy="true"]')).toBeDisabled();
    await expect(accountMenu.getByText("Выход...", { exact: true })).toBeVisible();
    await expect.poll(() => logoutCalls).toBe(1);
    releaseLogout?.();
    await expect(page.getByPlaceholder("Введите пароль...")).toBeVisible();
    expect(logoutCalls).toBe(1);
  });
});
