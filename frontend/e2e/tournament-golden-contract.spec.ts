import { createServer } from "node:net";
import { spawn, type ChildProcessByStdio } from "node:child_process";
import { resolve } from "node:path";
import { type Readable } from "node:stream";

import { expect, test, type Page, type Route } from "@playwright/test";

import {
  isGoldenOperatorResponse,
  isGoldenParticipantResponse,
  isGoldenRuntimeConflictProblem,
} from "../lib/shared/api/guards";
import { getSafeTaskHref } from "../lib/shared/lib/navigation";

const frontendRoot = process.cwd();
const fixtureRoot = resolve(frontendRoot, "e2e/fixtures/golden-api");
const nextBinary = resolve(frontendRoot, "node_modules/.bin/next");
const fixtureHost = "127.0.0.1";

const tournamentId = "00000000-0000-4000-8000-000000000001";
const groupId = "00000000-0000-4000-8000-000000000002";
const groupRevisionId = "00000000-0000-4000-8000-000000000003";
const attemptId = "00000000-0000-4000-8000-000000000004";
const freshAttemptId = "00000000-0000-4000-8000-000000000005";
const readyWindowId = "00000000-0000-4000-8000-000000000006";
const participantId = "00000000-0000-4000-8000-000000000007";
const secondParticipantId = "00000000-0000-4000-8000-000000000008";
const assignmentId = "00000000-0000-4000-8000-000000000009";
const snapshotId = "00000000-0000-4000-8000-000000000010";
const taskId = "00000000-0000-4000-8000-000000000011";

const operatorPath = `/api/v1/admin/tournaments/${tournamentId}/golden`;
const participantPath = `/api/v1/tournaments/${tournamentId}/participant/golden`;

let fixtureProcess: ChildProcessByStdio<null, Readable, Readable> | undefined;
let fixtureURL = "";

const fulfillJSON = async (route: Route, status: number, body: unknown): Promise<void> => {
  await route.fulfill({
    status,
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
};

const reservePort = async (): Promise<number> => new Promise((resolvePort, reject) => {
  const server = createServer();
  server.once("error", reject);
  server.listen(0, fixtureHost, () => {
    const address = server.address();
    if (!address || typeof address === "string") {
      server.close();
      reject(new Error("Не удалось зарезервировать порт Golden fixture"));
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

const waitForFixture = async (url: string): Promise<void> => {
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    try {
      if ((await fetch(url)).ok) {
        return;
      }
    } catch {
      // Next.js еще не занял loopback-порт.
    }
    await new Promise((resolveWait) => { setTimeout(resolveWait, 250); });
  }
  throw new Error("Golden API fixture не запустился");
};

const startFixture = async (): Promise<void> => {
  const port = await reservePort();
  fixtureURL = `http://${fixtureHost}:${port}`;
  fixtureProcess = spawn(
    nextBinary,
    ["dev", "--hostname", fixtureHost, "--port", String(port)],
    {
      cwd: fixtureRoot,
      detached: true,
      env: {
        NEXT_TELEMETRY_DISABLED: "1",
        NODE_ENV: "development",
        NEXT_PUBLIC_API_URL: "",
        NEXT_PUBLIC_ADMIN_API_URL: "",
        PATH: process.env.PATH ?? "/usr/bin:/bin",
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  await waitForFixture(fixtureURL);
};

const stopFixture = async (): Promise<void> => {
  const processToStop = fixtureProcess;
  fixtureProcess = undefined;
  if (!processToStop?.pid) {
    return;
  }
  try {
    process.kill(-processToStop.pid, "SIGTERM");
  } catch {
    processToStop.kill("SIGTERM");
  }
};

const openFixture = async (page: Page): Promise<void> => {
  await page.goto(fixtureURL, { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("heading", { name: "Контракт Golden API" })).toBeVisible();
  await expect(page.getByLabel("Результат вызова API")).toContainText("idle");
};

const installSessionCSRF = async (page: Page): Promise<void> => {
  await page.addInitScript(() => {
    document.cookie = "tpm_admin_access_csrf=operator-csrf; Path=/";
    document.cookie = "tpm_player_csrf=player-csrf; Path=/";
  });
};

const goldenTask = (task = taskId) => ({
  assignment_id: assignmentId,
  snapshot_id: snapshotId,
  task_id: task,
  version: 4,
  title: "Золотой след",
  description: "Найдите значение в опубликованном материале.",
  category: "web",
  difficulty: "hard",
  time_limit_seconds: 180,
  source_file_available: false,
});

const operatorGroup = (state: "prepared" | "ready" | "active", runtimeRevision: number) => ({
  group_id: groupId,
  group_revision_id: groupRevisionId,
  attempt_id: attemptId,
  state,
  runtime_revision: runtimeRevision,
  ready_window_id: readyWindowId,
  position_from: 1,
  position_to: 2,
  started_at: state === "active" ? "2026-09-13T10:00:00Z" : null,
  deadline: state === "active" ? "2026-09-13T10:03:00Z" : null,
  members: [
    { participant_id: participantId, ready: state !== "prepared", submitted: false, position: 1 },
    { participant_id: secondParticipantId, ready: state !== "prepared", submitted: false, position: 2 },
  ],
});

const operatorResponse = (
  state: "prepared" | "ready" | "active",
  runtimeRevision: number,
) => ({
  tournament_id: tournamentId,
  groups: [operatorGroup(state, runtimeRevision)],
  observed_at: "2026-09-13T10:00:00Z",
});

const participantResponse = ({
  active = false,
  ready = false,
  submitted = false,
  runtimeRevision = 1,
  currentAttemptId = attemptId,
  task = taskId,
}: {
  active?: boolean;
  ready?: boolean;
  submitted?: boolean;
  runtimeRevision?: number;
  currentAttemptId?: string;
  task?: string;
} = {}) => ({
  tournament_id: tournamentId,
  participant_id: participantId,
  group_id: groupId,
  group_revision_id: groupRevisionId,
  attempt_id: currentAttemptId,
  state: active ? "active" : ready ? "ready" : "prepared",
  runtime_revision: runtimeRevision,
  ready_window_id: readyWindowId,
  ready,
  submitted,
  position: 1,
  started_at: active ? "2026-09-13T10:00:00Z" : null,
  deadline: active ? "2026-09-13T10:03:00Z" : null,
  task: active ? goldenTask(task) : null,
});

const problemBody = (status: number, title: string) => ({
  type: "about:blank",
  title,
  status,
  detail: title,
  expected_runtime_revision: 8,
  current_runtime_revision: 9,
  expected_attempt_id: attemptId,
  current_attempt_id: freshAttemptId,
  expected_ready_window_id: readyWindowId,
  current_ready_window_id: readyWindowId,
});

test.beforeAll(async () => {
  await startFixture();
});

test.afterAll(async () => {
  await stopFixture();
});

test("Golden lifecycle keeps operator and participant identities separate", async ({ page }) => {
  await installSessionCSRF(page);
  let runtimeRevision = 1;
  let state: "prepared" | "ready" | "active" = "prepared";
  let submitted = false;
  const methods: string[] = [];

  await page.route("**/api/v1/admin/tournaments/**/golden**", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    methods.push(`operator:${request.method()}:${pathname}`);
    expect(pathname === operatorPath || pathname === `${operatorPath}/open` || pathname.includes("/attempts/")).toBe(true);
    if (pathname === operatorPath) {
      expect(request.method()).toBe("GET");
      await fulfillJSON(route, 200, operatorResponse(state, runtimeRevision));
      return;
    }
    expect(request.method()).toBe("POST");
    expect(request.headers()["x-csrf-token"]).toBe("operator-csrf");
    expect(request.headers()["idempotency-key"]).toBeTruthy();
    if (pathname.endsWith("/open")) {
      expect(request.postDataJSON()).toEqual({
        expected_projection_revision: 7,
        expected_runtime_revision: 0,
      });
      state = "ready";
      runtimeRevision = 2;
      await fulfillJSON(route, 200, operatorResponse(state, runtimeRevision));
      return;
    }
    expect(pathname).toBe(`${operatorPath}/attempts/${attemptId}/start`);
    expect(request.postDataJSON()).toEqual({
      expected_runtime_revision: 2,
      ready_window_id: readyWindowId,
    });
    state = "active";
    runtimeRevision = 3;
    await fulfillJSON(route, 200, operatorResponse(state, runtimeRevision));
  });

  await page.route("**/api/v1/tournaments/**/participant/golden**", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    methods.push(`participant:${request.method()}`);
    if (request.method() === "GET") {
      expect(pathname).toBe(participantPath);
      await fulfillJSON(route, 200, participantResponse({
        active: state === "active",
        ready: state !== "prepared",
        submitted,
        runtimeRevision,
      }));
      return;
    }
    expect(request.method()).toBe("POST");
    expect([`${participantPath}/ready`, `${participantPath}/submissions`]).toContain(pathname);
    expect(request.headers()["x-csrf-token"]).toBe("player-csrf");
    expect(request.headers()["idempotency-key"]).toBeTruthy();
    const body = request.postDataJSON() as Record<string, unknown>;
    expect(body.attempt_id).toBe(attemptId);
    expect(body.ready_window_id).toBe(readyWindowId);
    expect(body.expected_runtime_revision).toBe(runtimeRevision);
    if (body.ready === true) {
      expect(body).toEqual({
        attempt_id: attemptId,
        expected_runtime_revision: 1,
        ready: true,
        ready_window_id: readyWindowId,
      });
      state = "ready";
      runtimeRevision = 2;
      await fulfillJSON(route, 200, participantResponse({ ready: true, runtimeRevision }));
      return;
    }
    expect(body).toHaveProperty("submitted_flag", "flag{fixture-only}");
    expect(Object.keys(body)).toEqual([
      "attempt_id",
      "expected_runtime_revision",
      "ready_window_id",
      "submitted_flag",
    ]);
    submitted = true;
    await fulfillJSON(route, 200, participantResponse({
      active: true,
      ready: true,
      submitted,
      runtimeRevision,
    }));
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Прочитать назначение" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"task": null');
  await page.getByRole("button", { name: "Подтвердить готовность" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"ready": true');

  await page.getByRole("button", { name: "Открыть Golden" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"state": "ready"');
  await page.getByRole("button", { name: "Запустить попытку" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"state": "active"');

  await page.getByRole("button", { name: "Прочитать назначение" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"title": "Золотой след"');
  await expect(page.getByLabel("Результат вызова API")).toContainText('"time_limit_seconds": 180');
  await page.getByRole("button", { name: "Отправить флаг" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"submitted": true');
  await expect(page.getByLabel("Результат вызова API")).not.toContainText("flag{fixture-only}");

  expect(methods).toEqual([
    `participant:GET`,
    `participant:POST`,
    `operator:POST:${operatorPath}/open`,
    `operator:POST:${operatorPath}/attempts/${attemptId}/start`,
    `participant:GET`,
    `participant:POST`,
  ]);
});

test("stale Golden attempt returns one recovery state without replaying submit", async ({ page }) => {
  await installSessionCSRF(page);
  let operatorGets = 0;
  let participantGets = 0;
  let submits = 0;

  await page.route("**/api/v1/admin/tournaments/**/golden", async (route) => {
    operatorGets += 1;
    expect(route.request().method()).toBe("GET");
    expect(new URL(route.request().url()).pathname).toBe(operatorPath);
    await fulfillJSON(route, 200, operatorResponse("active", 8));
  });

  await page.route("**/api/v1/tournaments/**/participant/golden**", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (request.method() === "GET") {
      expect(pathname).toBe(participantPath);
      participantGets += 1;
      await fulfillJSON(route, 200, participantGets === 1
        ? participantResponse({ active: true, ready: true, runtimeRevision: 8 })
        : participantResponse({
          active: true,
          ready: true,
          runtimeRevision: 9,
          currentAttemptId: freshAttemptId,
          task: taskId,
        }));
      return;
    }
    submits += 1;
    expect(request.method()).toBe("POST");
    expect(pathname).toBe(`${participantPath}/submissions`);
    expect(request.headers()["x-csrf-token"]).toBe("player-csrf");
    expect(request.headers()["idempotency-key"]).toBeTruthy();
    expect(request.postDataJSON()).toMatchObject({
      attempt_id: attemptId,
      expected_runtime_revision: 8,
      ready_window_id: readyWindowId,
    });
    await fulfillJSON(route, 409, problemBody(409, "Устаревшая попытка Golden"));
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Прочитать состояние" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"state": "active"');
  await page.getByRole("button", { name: "Прочитать назначение" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"attempt_id": "' + attemptId + '"');
  await page.getByRole("button", { name: "Отправить флаг" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"recovered": true');
  await expect(page.getByLabel("Результат вызова API")).toContainText('"attempt_id": "' + freshAttemptId + '"');
  await expect(page.getByLabel("Результат вызова API")).not.toContainText("flag{fixture-only}");
  expect(operatorGets).toBe(1);
  expect(submits).toBe(1);
  expect(participantGets).toBe(2);
});

test("strict Golden guards reject metadata-only task and extra private fields", () => {
  const active = participantResponse({ active: true, ready: true, runtimeRevision: 3 });
  expect(isGoldenParticipantResponse(active)).toBe(true);
  expect(isGoldenParticipantResponse({
    ...active,
    started_at: null,
    deadline: null,
    task: null,
  })).toBe(true);
  expect(isGoldenParticipantResponse({
    ...active,
    task: {
      assignment_id: assignmentId,
      snapshot_id: snapshotId,
      task_id: taskId,
      version: 4,
    },
  })).toBe(false);
  expect(isGoldenParticipantResponse({
    ...active,
    task: { ...active.task, flag: "secret" },
  })).toBe(false);
  expect(isGoldenOperatorResponse(operatorResponse("active", 3))).toBe(true);
  expect(isGoldenOperatorResponse({
    ...operatorResponse("active", 3),
    private_task_material: "secret",
  })).toBe(false);
  expect(isGoldenRuntimeConflictProblem(problemBody(409, "Конфликт"))).toBe(true);
});

test("Golden task links reject unsafe server URLs", () => {
  expect(getSafeTaskHref("/tasks/golden", "https://arena.local")).toBe("/tasks/golden");
  expect(getSafeTaskHref("https://tasks.example/golden", "https://arena.local")).toBe(
    "https://tasks.example/golden",
  );

  for (const value of [
    "//evil.example/golden",
    "https://user:password@evil.example/golden",
    "https://evil.example/golden#fragment",
    "ftp://evil.example/golden",
    "javascript:alert(1)",
    "https:\\\\evil.example\\\\golden",
    "http://evil.example/golden",
  ]) {
    expect(getSafeTaskHref(value, "https://arena.local")).toBeNull();
  }
});

test("Golden fixture switches themes and remains usable at mobile width", async ({ page }) => {
  await openFixture(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator("main")).toHaveClass(/dark/);
  await page.getByRole("button", { name: "Переключить тему" }).click();
  await expect(page.locator("main")).toHaveClass(/light/);
  await expect(page.getByRole("button", { name: "Переключить тему" })).toContainText("Темная тема");
  const hasHorizontalOverflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
  expect(hasHorizontalOverflow).toBe(false);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole("button", { name: "Переключить тему" }).click();
  await expect(page.locator("main")).toHaveClass(/dark/);
});
