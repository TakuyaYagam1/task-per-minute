import {
  expect,
  test,
  type Page,
  type Route,
  type WebSocketRoute,
} from "@playwright/test";

import { createTournamentFixtureSet, tournamentFixtureIds } from "./tournament/fixtures";

type FixtureSet = ReturnType<typeof createTournamentFixtureSet>;
type GoldenParticipant = FixtureSet["golden"]["participant"];

const tournamentId = tournamentFixtureIds.tournament;
const publicPath = `/api/v1/tournaments/${tournamentId}`;
const goldenPath = `${publicPath}/participant/golden`;
const participantRealtimePath = `${publicPath}/participant/realtime`;
const assignmentPath = `${publicPath}/participant/assignments/${tournamentFixtureIds.assignment}`;
const sourceFilePath = `${assignmentPath}/source-file`;
const participantURL = `/arena/participant/${tournamentId}`;
const nextAttemptId = "00000000-0000-4000-8000-000000000251";
const nextGroupRevisionId = "00000000-0000-4000-8000-000000000252";

const fulfillJSON = async (route: Route, body: unknown, status = 200): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify(body),
    headers: {
      "content-type": status >= 400 ? "application/problem+json" : "application/json",
      date: new Date("2026-09-21T10:00:00Z").toUTCString(),
    },
    status,
  });
};

const installParticipantShell = async (page: Page, fixtureSet: FixtureSet): Promise<void> => {
  await page.addInitScript(() => {
    document.cookie = "tpm_player_csrf=golden-player-csrf; Path=/";
  });
  await page.route(`**${publicPath}`, (route) => fulfillJSON(route, {
    ...fixtureSet.public.tournament,
    state: "golden",
  }));
  await page.route(`**${publicPath}/participant/lobby`, (route) =>
    fulfillJSON(route, fixtureSet.participant.lobby));
  await page.route(`**${publicPath}/participant/snapshot*`, (route) =>
    fulfillJSON(route, fixtureSet.participant.recovery));
};

const activeGolden = (base: GoldenParticipant): GoldenParticipant => ({
  ...base,
  deadline: "2026-09-21T10:03:00Z",
  ready: true,
  runtime_revision: 3,
  started_at: "2026-09-21T10:00:00Z",
  state: "active",
  submitted: false,
});

const participantRealtimeFrame = (sequence: number): string => JSON.stringify({
  type: "tournament.participant",
  payload: {
    envelope: {
      schema_version: 1,
      tournament_id: tournamentId,
      sequence,
      event_id: sequence === 1
        ? tournamentFixtureIds.pauseRevision
        : tournamentFixtureIds.scoreRevision,
      occurred_at: "2026-09-21T10:00:00Z",
      projection_revision: 9,
      resume_id: tournamentFixtureIds.resume,
      participant: {},
    },
  },
});

test("participant completes server-owned Golden readiness, task and placement flow", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const longCopy = "ОченьДлинноеНазваниеGoldenЗаданияБезПробелов".repeat(8);
  let current: GoldenParticipant = {
    ...fixtureSet.golden.participant,
    deadline: null,
    position: null,
    ready: false,
    runtime_revision: 1,
    started_at: null,
    state: "prepared",
    submitted: false,
    task: null,
  };
  const mutationBodies: unknown[] = [];
  const realtimeSockets: WebSocketRoute[] = [];
  await installParticipantShell(page, fixtureSet);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.context().routeWebSocket(
    (url) => url.pathname === participantRealtimePath,
    (socket: WebSocketRoute) => {
      realtimeSockets.push(socket);
      socket.send(participantRealtimeFrame(1));
    },
  );
  await page.route(`**${goldenPath}**`, async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === "GET") {
      await fulfillJSON(route, current);
      return;
    }
    expect(request.headers()["x-csrf-token"]).toBe("golden-player-csrf");
    expect(request.headers()["idempotency-key"]).toBeTruthy();
    mutationBodies.push(request.postDataJSON());
    if (path.endsWith("/ready")) {
      current = { ...current, ready: true, runtime_revision: 2, state: "ready" };
      await fulfillJSON(route, current);
      return;
    }
    expect(path).toBe(`${goldenPath}/submissions`);
    current = { ...current, submitted: true };
    await fulfillJSON(route, current);
  });

  await page.goto(participantURL);
  const panel = page.getByTestId("participant-golden-panel");
  await expect(panel).toHaveAttribute("data-golden-state", "prepared");
  await expect(panel).toContainText("Это не BO1-серия");
  await expect(panel.getByText("Материалы попытки")).toHaveCount(0);

  await panel.getByRole("button", { name: "Готов к Golden", exact: true }).click();
  await expect(panel).toHaveAttribute("data-golden-state", "ready");
  expect(mutationBodies[0]).toEqual({
    attempt_id: tournamentFixtureIds.attempt,
    expected_runtime_revision: 1,
    ready: true,
    ready_window_id: tournamentFixtureIds.readyWindow,
  });

  current = activeGolden(fixtureSet.golden.participant);
  if (current.task === null) {
    throw new Error("Golden fixture requires a task");
  }
  current = {
    ...current,
    task: {
      ...current.task,
      description: `${longCopy}\n${longCopy}`,
      title: longCopy,
    },
  };
  await expect.poll(() => realtimeSockets.length).toBe(1);
  realtimeSockets[0]?.send(participantRealtimeFrame(2));
  await expect(panel).toHaveAttribute("data-golden-state", "active");
  await expect(panel.getByRole("heading", { name: longCopy, exact: true })).toBeVisible();
  await expect(panel.getByTestId("participant-golden-timer")).toHaveText(/^\d+:\d{2}$/);

  const goldenTimer = panel.getByTestId("participant-golden-timer");
  const timerSemantics = await goldenTimer.evaluate((element) => ({
    ancestorLive: element.closest("[aria-live]")?.getAttribute("aria-live") ?? null,
    ancestorRole: element.closest('[role="status"], [role="alert"]')?.getAttribute("role") ?? null,
    ariaLive: element.getAttribute("aria-live"),
    role: element.getAttribute("role"),
  }));
  expect(timerSemantics).toEqual({
    ancestorLive: null,
    ancestorRole: null,
    ariaLive: null,
    role: null,
  });

  const goldenInput = panel.getByRole("textbox", { name: "Ответ Golden", exact: true });
  await goldenInput.focus();
  await expect(goldenInput).toBeFocused();
  const focusState = await goldenInput.evaluate((element) => {
    const styles = getComputedStyle(element);
    return {
      outlineColor: styles.outlineColor,
      outlineStyle: styles.outlineStyle,
      outlineWidth: styles.outlineWidth,
    };
  });
  expect(focusState.outlineStyle).toBe("solid");
  expect(focusState.outlineWidth).toBe("2px");
  expect(focusState.outlineColor).not.toMatch(/rgba?\([^)]*,\s*0\)?$/);
  await goldenInput.press("Enter");
  await expect(goldenInput).toHaveAttribute("aria-describedby", "participant-golden-action-status");
  await expect(goldenInput).toHaveAttribute("aria-invalid", "true");
  const goldenActionStatus = panel.getByTestId("participant-golden-action-status");
  await expect(goldenActionStatus).toBeVisible();
  await expect(goldenActionStatus).toHaveAttribute("role", "alert");
  await expect(goldenActionStatus).toHaveAttribute("aria-live", "assertive");
  await goldenInput.fill("flag{golden_acceptance}");
  await goldenInput.press("Enter");
  await expect(panel).toContainText("Решение Golden принято сервером");
  expect(mutationBodies[1]).toEqual({
    attempt_id: tournamentFixtureIds.attempt,
    expected_runtime_revision: 3,
    ready_window_id: tournamentFixtureIds.readyWindow,
    submitted_flag: "flag{golden_acceptance}",
  });

  current = { ...current, position: 2, runtime_revision: 4, state: "completed" };
  await panel.getByRole("button", { name: "Обновить Golden" }).click();
  await expect(panel).toHaveAttribute("data-golden-state", "completed");
  await expect(panel.getByTestId("participant-golden-position")).toContainText("2");
  await expect(panel.getByTestId("participant-golden-position")).toContainText(
    "Следующий этап определяет только серверный lobby",
  );

  await page.getByRole("button", { name: "Светлая тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "Темная тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  for (const [width, height] of [[390, 844], [768, 1024], [1440, 900]] as const) {
    await page.setViewportSize({ width, height });
    await page.evaluate(() => {
      document.documentElement.style.fontSize = "200%";
    });
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
});

test("Golden loading error keeps a named landmark and an announced message", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  await installParticipantShell(page, fixtureSet);
  await page.route(`**${goldenPath}**`, (route) => fulfillJSON(route, fixtureSet.golden.participant, 503));

  await page.goto(participantURL);
  const panel = page.getByTestId("participant-golden-panel");
  await expect(panel).toHaveAttribute("data-state", "error");
  await expect(page.getByRole("region", { name: "Golden Task", exact: true })).toBeVisible();
  await expect(panel.getByRole("heading", { name: "Golden Task", exact: true })).toBeVisible();
  const goldenError = panel.getByRole("alert").filter({ hasText: "Golden недоступен" });
  await expect(goldenError).toContainText("Golden недоступен");
  await expect(goldenError).toHaveAttribute("role", "alert");
  await expect(goldenError).toHaveAttribute("aria-live", "assertive");
});

test("Golden no-show and technical pause stay taskless and server-controlled", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current: GoldenParticipant = {
    ...activeGolden(fixtureSet.golden.participant),
    position: null,
    task: null,
  };
  await installParticipantShell(page, fixtureSet);
  await page.route(`**${goldenPath}**`, (route) => fulfillJSON(route, current));

  await page.goto(participantURL);
  const panel = page.getByTestId("participant-golden-panel");
  await expect(panel).toContainText("зафиксировал no-show");
  await expect(panel.getByLabel("Ответ Golden")).toHaveCount(0);
  await expect(panel.getByRole("heading", { name: "Golden проверка" })).toHaveCount(0);

  current = { ...current, runtime_revision: 4, state: "technical_pause" };
  await panel.getByRole("button", { name: "Обновить Golden" }).click();
  await expect(panel).toContainText("Техническая пауза");
  await expect(panel.getByTestId("participant-golden-timer")).toHaveText("Остановлен сервером");
  await expect(panel.getByRole("button", { name: "Отправить ответ", exact: true })).toHaveCount(0);
});

test("Golden archive uses its immutable assignment without an ordinary assignment lookup", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  if (fixtureSet.golden.participant.task === null) {
    throw new Error("Golden archive fixture requires a task");
  }
  const current: GoldenParticipant = {
    ...activeGolden(fixtureSet.golden.participant),
    task: {
      ...fixtureSet.golden.participant.task,
      source_file_available: true,
    },
  };
  let assignmentReads = 0;
  let sourceFileReads = 0;
  await installParticipantShell(page, fixtureSet);
  await page.route(`**${goldenPath}**`, (route) => fulfillJSON(route, current));
  await page.route(`**${assignmentPath}`, async (route) => {
    assignmentReads += 1;
    await fulfillJSON(route, { title: "unexpected ordinary assignment lookup" }, 500);
  });

  await page.goto(participantURL);
  const sourceURL = new URL("/golden-source.zip", page.url()).toString();
  await page.route(`**${sourceFilePath}`, async (route) => {
    sourceFileReads += 1;
    await fulfillJSON(route, {
      expires_at: "2099-09-21T10:05:00Z",
      source_file_url: sourceURL,
    });
  });
  await page.context().route("**/golden-source.zip", (route) => route.fulfill({
    body: "PK\x03\x04 golden source archive",
    headers: {
      "content-disposition": "attachment; filename=golden-source.zip",
      "content-type": "application/zip",
    },
    status: 200,
  }));

  const downloadPromise = page.waitForEvent("download");
  await page.getByTestId("participant-golden-panel")
    .getByRole("button", { name: "Скачать архив" })
    .click();
  const download = await downloadPromise;

  expect(download.suggestedFilename()).toBe("golden-source.zip");
  expect(sourceFileReads).toBe(1);
  expect(assignmentReads).toBe(0);
});

test("stale Golden submission recovers a continuation attempt without replay", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const original = activeGolden(fixtureSet.golden.participant);
  const continued: GoldenParticipant = {
    ...original,
    attempt_id: nextAttemptId,
    group_revision_id: nextGroupRevisionId,
    runtime_revision: 5,
    submitted: false,
  };
  let current = original;
  let getCount = 0;
  let submitCount = 0;
  await installParticipantShell(page, fixtureSet);
  await page.route(`**${goldenPath}**`, async (route) => {
    const request = route.request();
    if (request.method() === "GET") {
      getCount += 1;
      await fulfillJSON(route, current);
      return;
    }
    submitCount += 1;
    current = continued;
    await fulfillJSON(route, {
      type: "about:blank",
      title: "Golden attempt changed",
      status: 409,
      detail: "Golden continuation was created",
      expected_runtime_revision: 3,
      current_runtime_revision: 5,
      expected_attempt_id: tournamentFixtureIds.attempt,
      current_attempt_id: nextAttemptId,
      expected_ready_window_id: tournamentFixtureIds.readyWindow,
      current_ready_window_id: tournamentFixtureIds.readyWindow,
    }, 409);
  });

  await page.goto(participantURL);
  const panel = page.getByTestId("participant-golden-panel");
  await panel.getByLabel("Ответ Golden").fill("flag{stale}");
  await panel.getByRole("button", { name: "Отправить ответ", exact: true }).click();

  await expect(panel).toHaveAttribute("data-attempt-id", nextAttemptId);
  await expect(panel.getByTestId("participant-golden-action-status")).toHaveAttribute(
    "data-status",
    "conflict",
  );
  await expect(panel.getByLabel("Ответ Golden")).toHaveValue("");
  expect(submitCount).toBe(1);
  expect(getCount).toBeGreaterThanOrEqual(2);
});
