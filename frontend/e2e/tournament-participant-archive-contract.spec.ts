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
const participantAssignmentPath = `${publicPath}/participant/assignments/${tournamentFixtureIds.assignment}`;
const participantSourceFilePath = `${participantAssignmentPath}/source-file`;
const participantURL = `/arena/participant/${tournamentId}`;
const serverTimestamp = "2026-09-15T10:00:00Z";
const sourceExpiry = "2099-09-15T10:05:00Z";
const expiredSourceExpiry = "2026-01-15T10:05:00Z";

type FixtureSet = ReturnType<typeof createTournamentFixtureSet>;
type ParticipantSnapshot = FixtureSet["participant"]["recovery"];
type ParticipantAssignmentResponse = components["schemas"]["ParticipantAssignmentResponse"];

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
): Promise<void> => {
  await route.fulfill({
    status,
    headers: { "content-type": "application/problem+json" },
    body: JSON.stringify({
      detail: title,
      status,
      title,
      type: "about:blank",
    }),
  });
};

const withDate = { date: new Date(serverTimestamp).toUTCString() };

const sourceOriginFor = (page: Page): string => {
  const configuredOrigin = process.env.NEXT_PUBLIC_SOURCE_FILE_ORIGINS
    ?.split(",")
    .map((value) => value.trim())
    .find((value) => value.length > 0);
  return configuredOrigin === undefined
    ? new URL(page.url()).origin
    : new URL(configuredOrigin).origin;
};

const participantSnapshotFor = (
  base: ParticipantSnapshot,
  participantId: string,
  options: Readonly<{
    hints?: string[];
    projectionRevision?: number;
    sourceFileAvailable?: boolean;
  }> = {},
): ParticipantSnapshot => {
  if (base.assignment === null) {
    throw new Error("participant fixture requires an assignment");
  }

  const projectionRevision = options.projectionRevision ?? base.projection_revision;
  return {
    ...base,
    assignment: {
      ...base.assignment,
      active_snapshot: {
        ...base.assignment.active_snapshot,
        hints: options.hints ?? base.assignment.active_snapshot.hints,
        source_file_available: options.sourceFileAvailable ?? true,
      },
      receipt: {
        ...base.assignment.receipt,
        participant_id: participantId,
      },
    },
    lobby: {
      ...base.lobby,
      participant_id: participantId,
      projection_revision: projectionRevision,
    },
    next_cursor: {
      ...base.next_cursor,
      projection_revision: projectionRevision,
    },
    projection_revision: projectionRevision,
  };
};

const assignmentResponseFor = (
  snapshot: ParticipantSnapshot,
): ParticipantAssignmentResponse => {
  if (snapshot.assignment === null) {
    throw new Error("participant fixture requires an assignment");
  }
  return {
    assignment: snapshot.assignment,
    projection_revision: snapshot.projection_revision,
    tournament_id: snapshot.tournament_id,
  };
};

const installParticipantRoutes = async (
  page: Page,
  fixtureSet: FixtureSet,
  response: ParticipantSnapshot | (() => ParticipantSnapshot),
  assignmentResponse?: ParticipantSnapshot | (() => ParticipantSnapshot),
): Promise<void> => {
  await page.route(`**${publicPath}`, async (route) => {
    await fulfillJSON(route, fixtureSet.public.tournament, 200);
  });
  await page.route(`**${participantLobbyPath}`, async (route) => {
    await fulfillJSON(route, fixtureSet.participant.lobby, 200);
  });
  await page.route(`**${participantSnapshotPath}*`, async (route) => {
    const snapshot = typeof response === "function" ? response() : response;
    await fulfillJSON(route, snapshot, 200, withDate);
  });
  if (assignmentResponse !== undefined) {
    await page.route(`**${participantAssignmentPath}`, async (route) => {
      const snapshot = typeof assignmentResponse === "function"
        ? assignmentResponse()
        : assignmentResponse;
      await fulfillJSON(route, assignmentResponseFor(snapshot));
    });
  }
};

const archiveRequestButton = (page: Page) => page
  .getByRole("button", { name: /архив|исход|ссыл|получ|скач|файл/i })
  .first();

const readBodyText = async (page: Page): Promise<string> => page.locator("body").innerText();

const assertNoTextInDOM = async (page: Page, text: string): Promise<void> => {
  const present = await page.locator("body").evaluate(
    (body, marker) => body.textContent?.includes(marker) ?? false,
    text,
  );
  expect(present).toBe(false);
};

const readArchiveBytes = async (page: Page, url: string): Promise<number[]> =>
  page.evaluate(async (sourceURL) => {
    const response = await fetch(sourceURL);
    return Array.from(new Uint8Array(await response.arrayBuffer()));
  }, url);

const openParticipantAssignment = async (
  page: Page,
  fixtureSet: FixtureSet,
  snapshot: ParticipantSnapshot | (() => ParticipantSnapshot),
  assignmentResponse?: ParticipantSnapshot | (() => ParticipantSnapshot),
): Promise<void> => {
  await installParticipantRoutes(page, fixtureSet, snapshot, assignmentResponse);
  await page.goto(participantURL);
  await expect(page.getByTestId("participant-player-panel")).toBeVisible();
  await expect(page.getByTestId("participant-assignment-state")).toHaveAttribute(
    "data-assignment-state",
    "delivered",
  );
};

const participants = [
  { id: tournamentFixtureIds.firstParticipant, label: "first participant" },
  { id: tournamentFixtureIds.secondParticipant, label: "second participant" },
] as const;

for (const participant of participants) {
  test(`authorized ${participant.label} can request a fresh source archive`, async ({ page }) => {
    const fixtureSet = createTournamentFixtureSet();
    const initial = participantSnapshotFor(fixtureSet.participant.recovery, participant.id, {
      hints: [],
      sourceFileAvailable: true,
    });
    let sourceFileRequests = 0;
    let assignmentReads = 0;
    let archiveRequests = 0;

    await openParticipantAssignment(page, fixtureSet, initial, () => {
      assignmentReads += 1;
      return initial;
    });
    const sourceURL = `${sourceOriginFor(page)}/source-download/${participant.id}.zip`;
    await page.route(`**${participantSourceFilePath}`, async (route) => {
      sourceFileRequests += 1;
      expect(route.request().method()).toBe("GET");
      await fulfillJSON(route, {
        expires_at: sourceExpiry,
        source_file_url: sourceURL,
      });
    });
    await page.context().route("**/source-download/*.zip", async (route) => {
      archiveRequests += 1;
      await route.fulfill({
        body: "PK\x03\x04 participant source archive",
        headers: {
          "access-control-allow-origin": "*",
          "content-disposition": 'attachment; filename="task-source.zip"',
          "content-type": "application/zip",
        },
        status: 200,
      });
    });

    await assertNoTextInDOM(page, sourceURL);
    await expect(archiveRequestButton(page)).toBeVisible();
    const downloadPromise = page.waitForEvent("download");
    await archiveRequestButton(page).click();
    await expect.poll(() => assignmentReads).toBe(1);
    await expect.poll(() => sourceFileRequests).toBe(1);
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe("task-source.zip");
    const downloadedBody = await readArchiveBytes(page, sourceURL);
    await expect.poll(() => archiveRequests).toBeGreaterThan(1);
    expect(String.fromCharCode(...downloadedBody.slice(0, 2))).toBe("PK");
    await assertNoTextInDOM(page, sourceURL);
    expect(page.url()).toContain(participantURL);
  });
}

for (const rejectedURL of [
  "ftp://files.example.test/source.zip",
  "https://untrusted.example.test/source.zip",
] as const) {
  test(`rejects a source archive URL outside the browser contract (${new URL(rejectedURL).protocol})`, async ({ page }) => {
    const fixtureSet = createTournamentFixtureSet();
    const initial = participantSnapshotFor(
      fixtureSet.participant.recovery,
      tournamentFixtureIds.firstParticipant,
      { hints: [], sourceFileAvailable: true },
    );
    let sourceFileRequests = 0;
    const initialURL = participantURL;

    await openParticipantAssignment(page, fixtureSet, initial, initial);
    await page.route(`**${participantSourceFilePath}`, async (route) => {
      sourceFileRequests += 1;
      await fulfillJSON(route, {
        expires_at: sourceExpiry,
        source_file_url: rejectedURL,
      });
    });

    await archiveRequestButton(page).click();
    await expect.poll(() => sourceFileRequests).toBe(1);
    await expect(page).toHaveURL(initialURL);
    await expect(page.getByRole("alert").filter({ hasText: /архив|ссыл|url|доступ|ошиб/i })).toBeVisible();
    await assertNoTextInDOM(page, rejectedURL);
  });
}

test("refreshes the assignment and source URL when the first URL is already expired", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const initial = participantSnapshotFor(
    fixtureSet.participant.recovery,
    tournamentFixtureIds.firstParticipant,
    { hints: [], sourceFileAvailable: true },
  );
  const refreshed = participantSnapshotFor(
    fixtureSet.participant.recovery,
    tournamentFixtureIds.firstParticipant,
    { hints: [], projectionRevision: initial.projection_revision + 1, sourceFileAvailable: true },
  );
  let sourceFileRequests = 0;
  let assignmentReads = 0;
  let archiveRequests = 0;

  await openParticipantAssignment(page, fixtureSet, initial, () => {
    assignmentReads += 1;
    return refreshed;
  });
  const assignmentReadsBeforeDownload = assignmentReads;
  const staleURL = `${sourceOriginFor(page)}/source-download/stale.zip`;
  const freshURL = `${sourceOriginFor(page)}/source-download/fresh.zip`;
  await page.route(`**${participantSourceFilePath}`, async (route) => {
    sourceFileRequests += 1;
    await fulfillJSON(route, {
      expires_at: sourceFileRequests === 1 ? expiredSourceExpiry : sourceExpiry,
      source_file_url: sourceFileRequests === 1 ? staleURL : freshURL,
    });
  });
  await page.context().route("**/source-download/*.zip", async (route) => {
    archiveRequests += 1;
    await route.fulfill({
      body: "PK\x03\x04 fresh participant source archive",
      headers: {
        "access-control-allow-origin": "*",
        "content-disposition": 'attachment; filename="task-source.zip"',
        "content-type": "application/zip",
      },
      status: 200,
    });
  });

  const downloadPromise = page.waitForEvent("download");
  await archiveRequestButton(page).click();
  await expect.poll(() => assignmentReads).toBe(assignmentReadsBeforeDownload + 2);
  await expect.poll(() => sourceFileRequests).toBe(2);
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe("task-source.zip");
  const downloadedBody = await readArchiveBytes(page, freshURL);
  await expect.poll(() => archiveRequests).toBeGreaterThan(1);
  expect(String.fromCharCode(...downloadedBody.slice(0, 2))).toBe("PK");
});

test("shows a safe backend error when the participant source archive is unavailable", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const initial = participantSnapshotFor(
    fixtureSet.participant.recovery,
    tournamentFixtureIds.firstParticipant,
    { hints: [], sourceFileAvailable: true },
  );
  let sourceFileRequests = 0;

  await openParticipantAssignment(page, fixtureSet, initial, initial);
  await page.route(`**${participantSourceFilePath}`, async (route) => {
    sourceFileRequests += 1;
    await fulfillProblem(route, 503, "Архив исходников временно недоступен");
  });

  await archiveRequestButton(page).click();
  await expect.poll(() => sourceFileRequests).toBe(1);
  await expect(page.getByRole("alert").filter({ hasText: /архив|недоступ|ошиб/i })).toBeVisible();
  expect(await readBodyText(page)).not.toContain("request_id");
});

test("does not place a locked hint in the DOM and reveals only the refreshed server snapshot", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const lockedHint = "Секретная подсказка до открытия сервером";
  const revealedHint = "Подсказка открыта сервером";
  let current: ParticipantSnapshot = participantSnapshotFor(
    fixtureSet.participant.recovery,
    tournamentFixtureIds.firstParticipant,
    { hints: [], sourceFileAvailable: false },
  );

  await openParticipantAssignment(page, fixtureSet, () => current as ParticipantSnapshot);
  await assertNoTextInDOM(page, lockedHint);
  await expect(page.getByText(/Подсказки/i)).toHaveCount(0);
  await expect(page.getByText(revealedHint, { exact: true })).toHaveCount(0);

  current = participantSnapshotFor(
    fixtureSet.participant.recovery,
    tournamentFixtureIds.firstParticipant,
    { hints: [revealedHint], projectionRevision: current.projection_revision + 1, sourceFileAvailable: false },
  );
  await page.reload({ waitUntil: "domcontentloaded" });
  const hintSummary = page.locator("summary").filter({ hasText: "Подсказки (1)" });
  await expect(hintSummary).toBeVisible();
  await expect(page.getByText(revealedHint, { exact: true })).toHaveCount(0);
  await hintSummary.click();
  await expect(page.getByText(revealedHint, { exact: true })).toBeVisible();
  await assertNoTextInDOM(page, lockedHint);
});

test("keeps the source archive action usable in dark and light desktop themes and on mobile", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const initial = participantSnapshotFor(
    fixtureSet.participant.recovery,
    tournamentFixtureIds.firstParticipant,
    { hints: [], sourceFileAvailable: true },
  );

  await openParticipantAssignment(page, fixtureSet, initial);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(archiveRequestButton(page)).toBeVisible();
  await selectTheme(page, "light");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(archiveRequestButton(page)).toBeVisible();
  await selectTheme(page, "dark");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(archiveRequestButton(page)).toBeVisible();
  await expect.poll(() => page.evaluate(() => ({
    body: document.body.scrollWidth,
    document: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }))).toEqual({ body: 390, document: 390, viewport: 390 });
  await archiveRequestButton(page).focus();
  await expect(archiveRequestButton(page)).toBeFocused();
});
