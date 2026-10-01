import { expect, test, type Page, type Route } from "@playwright/test";

import {
  createTournamentFixtureSet,
  tournamentFixtureIds,
} from "./tournament/fixtures";
import type { components } from "../lib/shared/api/schema";
import { selectTheme } from "./support/common";

const tournamentId = tournamentFixtureIds.tournament;
const publicPath = `/api/v1/tournaments/${tournamentId}`;
const participantLobbyPath = `${publicPath}/participant/lobby`;
const participantSnapshotPath = `${publicPath}/participant/snapshot`;
const participantSubmissionPath = `${publicPath}/participant/series/${tournamentFixtureIds.bo3Series}/games/${tournamentFixtureIds.bo3GameTwo}/submissions`;
const participantSurrenderPath = `${publicPath}/participant/series/${tournamentFixtureIds.bo3Series}/surrender`;
const participantURL = `/arena/participant/${tournamentId}`;
const serverTimestamp = "2026-09-15T10:00:00Z";
const activeDeadline = "2099-09-15T10:15:00Z";
const submittedFlag = "flag{participant-answer-must-not-leak}";

type FixtureSet = ReturnType<typeof createTournamentFixtureSet>;
type ParticipantSnapshot = FixtureSet["participant"]["recovery"];
type SubmissionResponse = components["schemas"]["ParticipantSubmissionResponse"];
type OfficialResultRevision = components["schemas"]["OfficialResultRevision"];

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

const fulfillProblem = async (
  route: Route,
  status: number,
  title: string,
  headers: Record<string, string> = {},
): Promise<void> => {
  await route.fulfill({
    status,
    headers: {
      "content-type": "application/problem+json",
      ...headers,
    },
    body: JSON.stringify({
      detail: title,
      status,
      title,
      type: "about:blank",
    }),
  });
};

const withDate = { date: new Date(serverTimestamp).toUTCString() };

const playSnapshotFor = (
  fixtureSet: FixtureSet,
  projectionRevision = 9,
): ParticipantSnapshot => {
  const base = fixtureSet.participant.recovery;
  if (base.assignment === null || base.wave === null || base.series === null) {
    throw new Error("participant fixture requires an active assignment");
  }

  return {
    ...base,
    assignment: {
      ...base.assignment,
      active_snapshot: {
        ...base.assignment.active_snapshot,
        hints: [],
        source_file_available: false,
      },
      context: {
        ...base.assignment.context,
        effective_deadline: activeDeadline,
        game_state: "active",
      },
    },
    lobby: {
      ...base.lobby,
      projection_revision: projectionRevision,
      required_action: "play",
      series: base.lobby.series.map((series) => ({ ...series, state: "active" })),
      status: "assigned",
    },
    next_cursor: {
      ...base.next_cursor,
      projection_revision: projectionRevision,
    },
    projection_revision: projectionRevision,
    series: {
      ...base.series,
      state: "active",
    },
    wave: {
      ...base.wave,
      paused_at: null,
      state: "active",
    },
  };
};

const completedSnapshotFor = (
  fixtureSet: FixtureSet,
  projectionRevision: number,
): ParticipantSnapshot => {
  const active = playSnapshotFor(fixtureSet, projectionRevision);
  return {
    ...active,
    assignment: null,
    lobby: {
      ...active.lobby,
      required_action: "review_result",
      series: active.lobby.series.map((series) => ({ ...series, state: "completed" })),
      status: "completed",
    },
    series: active.series === null
      ? null
      : { ...active.series, state: "completed", winner_id: tournamentFixtureIds.secondParticipant },
    wave: active.wave === null ? null : { ...active.wave, state: "completed" },
  };
};

const submissionResponseFor = (
  snapshot: ParticipantSnapshot,
  correct: boolean,
  projectionRevision: number,
): SubmissionResponse => {
  if (snapshot.assignment === null) {
    throw new Error("participant fixture requires an assignment");
  }
  return {
    projection_revision: projectionRevision,
    submission: {
      command_id: tournamentFixtureIds.event,
      committed_at: serverTimestamp,
      content_digest: "a".repeat(64),
      correct,
      participant_id: snapshot.lobby.participant_id,
      scope: {
        assignment_id: snapshot.assignment.id,
        game_id: snapshot.assignment.context.game_id,
        series_id: snapshot.assignment.context.series_id,
        slot_id: snapshot.assignment.context.slot_id,
        tournament_id: snapshot.tournament_id,
        wave_id: snapshot.assignment.context.wave_id,
      },
      sequence: 1,
      snapshot_id: snapshot.assignment.active_snapshot.snapshot_id,
      task_id: snapshot.assignment.active_snapshot.task_id,
    },
  };
};

const surrenderResultFor = (): OfficialResultRevision => ({
  actor_id: null,
  actor_kind: "server",
  command_id: tournamentFixtureIds.event,
  game_id: tournamentFixtureIds.bo3GameTwo,
  game_reason: "surrender",
  game_state: "completed",
  id: tournamentFixtureIds.resume,
  ordinal: 1,
  previous_revision_id: null,
  recorded_at: serverTimestamp,
  score_revision_id: null,
  series_id: tournamentFixtureIds.bo3Series,
  series_reason: null,
  series_state: "completed",
  source_projection_revision_id: tournamentFixtureIds.pauseRevision,
  subject_kind: "game",
  tournament_id: tournamentId,
  winner_id: tournamentFixtureIds.secondParticipant,
});

const installParticipantRoutes = async (
  page: Page,
  fixtureSet: FixtureSet,
  snapshotForRequest: () => ParticipantSnapshot,
  onSnapshot?: () => void,
): Promise<void> => {
  await page.addInitScript(() => {
    document.cookie = "tpm_player_csrf=participant-contract-csrf; Path=/";
  });
  await page.route(`**${publicPath}`, async (route) => {
    await fulfillJSON(route, fixtureSet.public.tournament, 200);
  });
  await page.route(`**${participantLobbyPath}`, async (route) => {
    await fulfillJSON(route, snapshotForRequest().lobby, 200);
  });
  await page.route(`**${participantSnapshotPath}*`, async (route) => {
    onSnapshot?.();
    await fulfillJSON(route, snapshotForRequest(), 200, withDate);
  });
};

const openParticipantPlay = async (
  page: Page,
  fixtureSet: FixtureSet,
  snapshotForRequest: () => ParticipantSnapshot,
  onSnapshot?: () => void,
): Promise<void> => {
  await installParticipantRoutes(page, fixtureSet, snapshotForRequest, onSnapshot);
  await page.goto(participantURL);
  await expect(page.getByTestId("participant-player-panel")).toBeVisible();
  await expect(page.getByTestId("participant-assignment-state")).toHaveAttribute(
    "data-assignment-state",
    "delivered",
  );
};

const answerInput = (page: Page) => page.getByRole("textbox", { name: "Ответ", exact: true });

const submitButton = (page: Page) => page.getByRole("button", { name: "Отправить ответ", exact: true });

const surrenderButton = (page: Page) => page.getByRole("button", { name: "Сдаться", exact: true });

const assertFlagIsNotExposed = async (
  page: Page,
  consoleMessages: readonly string[],
): Promise<void> => {
  expect(page.url()).not.toContain(encodeURIComponent(submittedFlag));
  expect(page.url()).not.toContain(submittedFlag);
  const storage = await page.evaluate(() => ({
    local: Object.values(localStorage),
    session: Object.values(sessionStorage),
  }));
  expect(storage.local.join("\n")).not.toContain(submittedFlag);
  expect(storage.session.join("\n")).not.toContain(submittedFlag);
  expect(await page.locator("body").innerText()).not.toContain(submittedFlag);
  const formValues = await page.locator("input, textarea").evaluateAll((elements) =>
    elements.map((element) => (element as HTMLInputElement | HTMLTextAreaElement).value).join("\n"),
  );
  expect(formValues).not.toContain(submittedFlag);
  expect(consoleMessages.join("\n")).not.toContain(submittedFlag);
};

test("empty participant answer is rejected without a submission request", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const current = playSnapshotFor(fixtureSet);
  let submissions = 0;

  await openParticipantPlay(page, fixtureSet, () => current);
  await page.route(`**${participantSubmissionPath}`, async (route) => {
    submissions += 1;
    await fulfillJSON(route, submissionResponseFor(current, false, current.projection_revision));
  });

  const input = answerInput(page);
  await expect(input).toHaveCount(1);
  await expect(input).toHaveValue("");
  await submitButton(page).focus();
  await expect(submitButton(page)).toBeFocused();
  await submitButton(page).press("Enter");

  await expect.poll(() => submissions).toBe(0);
  await expect(page.locator("body")).toContainText(/введите|обязатель|пуст/i);
  await expect(input).toHaveAttribute(
    "aria-describedby",
    "participant-submission-help participant-submission-status",
  );
  await expect(input).toHaveAttribute("aria-invalid", "true");
  const emptyStatus = page.getByTestId("participant-submission-status");
  await expect(emptyStatus).toContainText(/введите|обязатель|пуст/i);
  await expect(emptyStatus).toHaveAttribute("role", "status");
  await expect(emptyStatus).toHaveAttribute("aria-live", "polite");
});

test("incorrect answer shows a server result and never exposes the submitted flag", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const current = playSnapshotFor(fixtureSet);
  const consoleMessages: string[] = [];
  const requests: Array<{ body: unknown; key: string | undefined; url: string }> = [];
  page.on("console", (message) => consoleMessages.push(message.text()));

  await openParticipantPlay(page, fixtureSet, () => current);
  await page.route(`**${participantSubmissionPath}`, async (route) => {
    const request = route.request();
    requests.push({
      body: request.postDataJSON(),
      key: request.headers()["idempotency-key"],
      url: request.url(),
    });
    await fulfillJSON(route, submissionResponseFor(current, false, current.projection_revision));
  });

  await answerInput(page).fill(submittedFlag);
  await answerInput(page).press("Enter");
  await expect(page.locator("body")).toContainText(/неверн|неправ|отклон|ошиб/i);
  await expect(answerInput(page)).toHaveAttribute(
    "aria-describedby",
    "participant-submission-help participant-submission-status",
  );
  await expect(answerInput(page)).toHaveAttribute("aria-invalid", "true");
  const incorrectStatus = page.getByTestId("participant-submission-status");
  await expect(incorrectStatus).toContainText(/неверн|неправ|отклон|ошиб/i);
  await expect(incorrectStatus).toHaveAttribute("role", "status");
  await expect(incorrectStatus).toHaveAttribute("aria-live", "polite");

  expect(requests).toHaveLength(1);
  expect(requests[0]?.body).toEqual({
    expected_projection_revision: current.projection_revision,
    submitted_flag: submittedFlag,
  });
  expect(requests[0]?.key).toMatch(/^[0-9a-f-]{36}$/i);
  expect(requests[0]?.url).not.toContain(submittedFlag);
  await assertFlagIsNotExposed(page, consoleMessages);
  await expect(page.locator("body")).not.toContainText(/winner_id/i);
});

test("slow submission ignores a double click, keeps one idempotency key, and recovers a conflict", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = playSnapshotFor(fixtureSet);
  let snapshotReads = 0;
  let submissions = 0;
  const keys: string[] = [];
  let releaseSubmission: (() => void) | undefined;

  await openParticipantPlay(page, fixtureSet, () => current, () => {
    snapshotReads += 1;
  });
  await page.route(`**${participantSubmissionPath}`, async (route) => {
    submissions += 1;
    keys.push(route.request().headers()["idempotency-key"] ?? "");
    await new Promise<void>((resolve) => {
      releaseSubmission = resolve;
    });
    current = playSnapshotFor(fixtureSet, 10);
    await fulfillProblem(route, 409, "Состояние игры изменилось");
  });

  await answerInput(page).fill("flag{slow-answer}");
  await submitButton(page).click();
  const pendingSubmit = page.getByTestId("participant-submit-button");
  await expect(pendingSubmit).toBeDisabled();
  await pendingSubmit.click({ force: true }).catch(() => undefined);
  await expect.poll(() => submissions).toBe(1);
  releaseSubmission?.();

  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute(
    "data-projection-revision",
    "10",
  );
  await expect.poll(() => snapshotReads).toBeGreaterThan(1);
  expect(keys).toEqual([expect.stringMatching(/^[0-9a-f-]{36}$/i)]);
});

test("accepted answer reflects only the server boolean and never assigns a client winner", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const current = playSnapshotFor(fixtureSet);
  const consoleMessages: string[] = [];
  let submissions = 0;
  page.on("console", (message) => consoleMessages.push(message.text()));

  await openParticipantPlay(page, fixtureSet, () => current);
  await page.route(`**${participantSubmissionPath}`, async (route) => {
    submissions += 1;
    expect(route.request().postDataJSON()).toEqual({
      expected_projection_revision: current.projection_revision,
      submitted_flag: submittedFlag,
    });
    await fulfillJSON(route, submissionResponseFor(current, true, current.projection_revision + 1));
  });

  await answerInput(page).fill(submittedFlag);
  await submitButton(page).focus();
  await expect(submitButton(page)).toBeFocused();
  await submitButton(page).press("Enter");
  await expect.poll(() => submissions).toBe(1);
  await expect(page.locator("body")).toContainText(/принят|верн|правил|подтвержд/i);
  await expect(page.locator("body")).not.toContainText(/winner_id/i);
  await assertFlagIsNotExposed(page, consoleMessages);
});

test("rate limited answer surfaces Retry-After without leaking the value", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const current = playSnapshotFor(fixtureSet);
  let submissions = 0;

  await openParticipantPlay(page, fixtureSet, () => current);
  await page.route(`**${participantSubmissionPath}`, async (route) => {
    submissions += 1;
    await fulfillProblem(route, 429, "Слишком много попыток", { "Retry-After": "17" });
  });

  await answerInput(page).fill("flag{rate-limited-answer}");
  await submitButton(page).focus();
  await expect(submitButton(page)).toBeFocused();
  await submitButton(page).press("Enter");
  await expect.poll(() => submissions).toBe(1);
  await expect(page.locator("body")).toContainText(/слишком|лимит|повтор|17/i);
  const rateLimitStatus = page.getByTestId("participant-submission-status");
  await expect(rateLimitStatus).toContainText(/слишком|лимит|повтор|17/i);
  await expect(rateLimitStatus).toHaveAttribute("role", "alert");
  await expect(rateLimitStatus).toHaveAttribute("aria-live", "assertive");
  await expect(page.locator("body")).not.toContainText(/winner_id/i);
});

test("surrender cancel makes no request, confirmation sends one command, and the official result is refetched", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = playSnapshotFor(fixtureSet);
  let snapshotReads = 0;
  let surrenderRequests = 0;
  const keys: string[] = [];

  await openParticipantPlay(page, fixtureSet, () => current, () => {
    snapshotReads += 1;
  });
  await page.route(`**${participantSurrenderPath}`, async (route) => {
    surrenderRequests += 1;
    const request = route.request();
    keys.push(request.headers()["idempotency-key"] ?? "");
    expect(request.postDataJSON()).toMatchObject({
      confirmed: true,
      expected_projection_revision: 9,
    });
    current = completedSnapshotFor(fixtureSet, 11);
    await fulfillJSON(route, surrenderResultFor());
  });

  await surrenderButton(page).click();
  await expect(page.locator("body")).toContainText(/сдач|подтверд|уверен/i);
  await page.getByTestId("participant-surrender-cancel").click();
  await expect.poll(() => surrenderRequests).toBe(0);

  await surrenderButton(page).click();
  await page.getByTestId("participant-surrender-confirm").click();

  await expect.poll(() => surrenderRequests).toBe(1);
  await expect.poll(() => snapshotReads).toBeGreaterThan(1);
  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute(
    "data-state",
    "completed",
  );
  await expect(page.locator("body")).toContainText(/результат|сдач|заверш/i);
  expect(keys).toEqual([expect.stringMatching(/^[0-9a-f-]{36}$/i)]);
});

test("participant submission controls preserve keyboard access, themes, scaling, and mobile layout", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const current = playSnapshotFor(fixtureSet);
  const longCopy = "ОченьДлинноеИмяУчастникаБезПробелов".repeat(8);
  if (current.assignment === null) {
    throw new Error("participant fixture requires an active assignment");
  }
  current.assignment = {
    ...current.assignment,
    active_snapshot: {
      ...current.assignment.active_snapshot,
      description: `${longCopy}\n${longCopy}`,
      title: longCopy,
    },
  };
  await page.emulateMedia({ reducedMotion: "reduce" });
  await openParticipantPlay(page, fixtureSet, () => current);

  await expect(page.getByTestId("participant-player-panel")).toBeVisible();
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Состояние соревнования" })).toHaveCount(0);

  const keyActions = [answerInput(page), submitButton(page)];
  for (const theme of ["dark", "light"] as const) {
    await selectTheme(page, theme);
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);

    for (const [width, height] of [[390, 844], [768, 1024], [1440, 900]] as const) {
      await page.setViewportSize({ width, height });
      await page.evaluate(() => {
        document.documentElement.style.fontSize = "200%";
      });
      await expect(page.getByRole("main")).toBeVisible();
      await expect.poll(() => page.evaluate(() => (
        document.body.scrollWidth <= window.innerWidth
        && document.documentElement.scrollWidth <= window.innerWidth
      ))).toBe(true);

      for (const control of keyActions) {
        await expect(control).toBeVisible();
        const bounds = await control.boundingBox();
        expect(bounds).not.toBeNull();
        expect(bounds?.x).toBeGreaterThanOrEqual(-1);
        expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(width + 1);
      }
    }
  }
});
