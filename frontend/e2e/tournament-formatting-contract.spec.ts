import { spawn, type ChildProcessByStdio } from "node:child_process";
import { createServer } from "node:net";
import { resolve } from "node:path";
import { type Readable } from "node:stream";

import { expect, test, type Page } from "@playwright/test";

const frontendRoot = process.cwd();
const fixtureRoot = resolve(frontendRoot, "e2e/fixtures/tournament-formatting");
const nextBinary = resolve(frontendRoot, "node_modules/.bin/next");
const fixtureHost = "127.0.0.1";
const readinessTimeoutMs = 60_000;

let fixtureProcess: ChildProcessByStdio<null, Readable, Readable> | undefined;
let fixtureURL = "";
let fixtureOutput = "";

const wait = (durationMs: number) => new Promise<void>((resolvePromise) => {
  setTimeout(resolvePromise, durationMs);
});

const reservePort = async (): Promise<number> => new Promise((resolvePort, reject) => {
  const server = createServer();
  server.once("error", reject);
  server.listen(0, fixtureHost, () => {
    const address = server.address();
    if (!address || typeof address === "string") {
      server.close();
      reject(new Error("Could not reserve a loopback port for the tournament formatting fixture"));
      return;
    }

    const port = address.port;
    server.close((error) => {
      if (error) {
        reject(error);
        return;
      }
      resolvePort(port);
    });
  });
});

const appendFixtureOutput = (chunk: Buffer | string): void => {
  fixtureOutput = `${fixtureOutput}${chunk.toString()}`.slice(-6_000);
};

const isFixtureReady = async (): Promise<boolean> => {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 1_000);

  try {
    const response = await fetch(fixtureURL, { signal: controller.signal });
    return response.ok;
  } catch {
    return false;
  } finally {
    clearTimeout(timeout);
  }
};

const startFixture = async (): Promise<void> => {
  const port = await reservePort();
  fixtureURL = `http://${fixtureHost}:${port}`;
  fixtureOutput = "";

  const fixtureChild = spawn(
    nextBinary,
    ["dev", "--hostname", fixtureHost, "--port", String(port)],
    {
      cwd: fixtureRoot,
      detached: true,
      env: {
        NEXT_TELEMETRY_DISABLED: "1",
        NODE_ENV: "development",
        PATH: process.env.PATH ?? "/usr/bin:/bin",
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  fixtureProcess = fixtureChild;
  fixtureChild.stdout.on("data", appendFixtureOutput);
  fixtureChild.stderr.on("data", appendFixtureOutput);

  const deadline = Date.now() + readinessTimeoutMs;
  while (Date.now() < deadline) {
    if (fixtureChild.exitCode !== null) {
      throw new Error(`The tournament formatting fixture exited before readiness.\n${fixtureOutput}`);
    }
    if (await isFixtureReady()) {
      return;
    }
    await wait(250);
  }

  throw new Error(`The tournament formatting fixture did not become ready.\n${fixtureOutput}`);
};

const stopFixture = async (): Promise<void> => {
  const processToStop = fixtureProcess;
  fixtureProcess = undefined;
  if (!processToStop || processToStop.pid === undefined) {
    return;
  }

  const exited = new Promise<void>((resolveExit) => {
    if (processToStop.exitCode !== null) {
      resolveExit();
      return;
    }
    processToStop.once("exit", () => resolveExit());
  });

  try {
    process.kill(-processToStop.pid, "SIGTERM");
  } catch {
    if (processToStop.exitCode === null) {
      processToStop.kill("SIGTERM");
    }
  }

  await Promise.race([exited, wait(5_000)]);
  if (processToStop.exitCode === null) {
    try {
      process.kill(-processToStop.pid, "SIGKILL");
    } catch {
      processToStop.kill("SIGKILL");
    }
  }
};

const openFixture = async (page: Page): Promise<void> => {
  await page.goto(fixtureURL, { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("heading", { name: "Общий формат турнира" })).toBeVisible();
  await expect(page.locator("main[data-hydrated='true']")).toBeVisible();
};

test.describe("tournament presentation formatting", () => {
  test.beforeAll(async () => {
    await startFixture();
  });

  test.afterAll(async () => {
    await stopFixture();
  });

  test("renders Russian presentation values and safe fallbacks", async ({ page }) => {
    await openFixture(page);

    await expect(page.locator("html")).toHaveAttribute("lang", "ru");
    await expect(
      page.getByRole("heading", {
        name: "Международный чемпионат по кибербезопасности: зимний кубок операторов и участников",
      }),
    ).toBeVisible();
    await expect(page.getByTestId("participant-count")).toHaveText([
      "1 участник",
      "2 участника",
      "5 участников",
      "16 участников",
    ]);
    await expect(page.getByTestId("arena-date")).toHaveText("14.09.2026, 15:45");
    await expect(page.getByTestId("arena-duration")).toHaveText("03:00");
    await expect(page.getByTestId("arena-score")).toHaveText("BO3: 2:1");
    await expect(page.getByTestId("arena-points")).toHaveText("1 250");

    for (const category of ["Web", "Crypto", "Reverse", "Forensics", "Pwn"]) {
      await expect(page.getByText(category, { exact: true })).toBeVisible();
    }
    for (const action of ["Открыть регистрацию", "Начать швейцарский этап", "Отправить флаг"]) {
      await expect(page.getByText(action, { exact: true })).toBeVisible();
    }
  });

  test("switches state and result controls without exposing server codes", async ({ page }) => {
    await openFixture(page);

    const tournamentGroup = page.getByRole("group", { name: "Состояние турнира" });
    const registrationButton = tournamentGroup.getByRole("button", { name: "Регистрация", exact: true });
    await registrationButton.click();
    await expect(page.getByTestId("tournament-status")).toContainText("Турнир: Регистрация");
    await tournamentGroup.getByRole("button", { name: "Проверить неизвестное состояние" }).click();
    await expect(page.getByTestId("tournament-status")).toContainText("Турнир: Состояние недоступно");

    const gameGroup = page.getByRole("group", { name: "Состояние игры" });
    await gameGroup.getByRole("button", { name: "Приостановлена", exact: true }).click();
    await expect(page.getByTestId("game-status")).toContainText("Игра: Приостановлена");

    const connectionGroup = page.getByRole("group", { name: "Состояние соединения" });
    await connectionGroup.getByRole("button", { name: "Связь потеряна", exact: true }).click();
    await expect(page.getByTestId("connection-status")).toContainText("Связь: Связь потеряна");

    const reasonGroup = page.getByRole("group", { name: "Причина результата" });
    await reasonGroup.getByRole("button", { name: "Не решено", exact: true }).click();
    await expect(page.getByTestId("result-reason")).toContainText("Не решено");
    await reasonGroup.getByRole("button", { name: "Проверить неизвестную причину" }).click();
    await expect(page.getByTestId("result-reason")).toContainText("Причина результата недоступна");

    const errorGroup = page.getByRole("group", { name: "Ошибка сервера" });
    await errorGroup.getByRole("button", { name: "Слишком много запросов. Повторите позже" }).click();
    await expect(page.getByTestId("server-error")).toContainText("Слишком много запросов. Повторите позже");
    await errorGroup.getByRole("button", { name: "Проверить неизвестную ошибку" }).click();
    await expect(page.getByTestId("server-error")).toContainText("Неизвестная ошибка сервера");

    const text = await page.locator("body").innerText();
    for (const rawCode of ["draft", "registration", "active", "rate_limited", "unknown"]) {
      expect(text).not.toContain(rawCode);
    }
  });

  test("supports both themes and a narrow viewport without overflow", async ({ page }) => {
    await openFixture(page);

    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-theme", "dark");
    const darkBackground = await html.evaluate((element) => getComputedStyle(element).getPropertyValue("--bg").trim());

    await page.getByRole("button", { name: "Светлая тема" }).click();
    await expect(html).toHaveAttribute("data-theme", "light");
    await expect(page.getByTestId("arena-date")).toHaveText("14.09.2026, 15:45");
    await expect(page.getByTestId("arena-duration")).toHaveText("03:00");
    await expect(page.getByTestId("arena-score")).toHaveText("BO3: 2:1");
    const lightBackground = await html.evaluate((element) => getComputedStyle(element).getPropertyValue("--bg").trim());
    expect(lightBackground).not.toBe(darkBackground);

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload({ waitUntil: "domcontentloaded" });
    await expect(page.locator("html")).toHaveAttribute("lang", "ru");
    await expect(page.getByRole("heading", { name: "Общий формат турнира" })).toBeVisible();
    await expect(
      page.getByRole("heading", {
        name: "Международный чемпионат по кибербезопасности: зимний кубок операторов и участников",
      }),
    ).toBeVisible();
    const pageWidths = await page.evaluate(() => ({
      body: document.body.scrollWidth,
      document: document.documentElement.scrollWidth,
      viewport: window.innerWidth,
    }));
    expect(pageWidths.document).toBeLessThanOrEqual(pageWidths.viewport);
    expect(pageWidths.body).toBeLessThanOrEqual(pageWidths.viewport);
  });
});
