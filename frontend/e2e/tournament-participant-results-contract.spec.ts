import { expect, test, type Page, type Route } from "@playwright/test";

import { createTournamentFixtureSet, tournamentFixtureIds } from "./tournament/fixtures";
import { selectTheme } from "./support/common";

type FixtureSet = ReturnType<typeof createTournamentFixtureSet>;
type ParticipantSnapshot = FixtureSet["participant"]["recovery"];
type Series = NonNullable<ParticipantSnapshot["series"]>;

const tournamentId = tournamentFixtureIds.tournament;
const publicPath = `/api/v1/tournaments/${tournamentId}`;
const participantLobbyPath = `${publicPath}/participant/lobby`;
const participantSnapshotPath = `${publicPath}/participant/snapshot`;
const participantURL = `/arena/participant/${tournamentId}`;

const ids = {
  bo1Result: "00000000-0000-4000-8000-000000000201",
  bo3Result: "00000000-0000-4000-8000-000000000202",
  correctedResult: "00000000-0000-4000-8000-000000000203",
  correctedScore: "00000000-0000-4000-8000-000000000204",
  gameTwoResult: "00000000-0000-4000-8000-000000000205",
  gameThreeResult: "00000000-0000-4000-8000-000000000206",
} as const;

const fulfillJSON = async (route: Route, body: unknown): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify(body),
    headers: {
      "content-type": "application/json",
      date: new Date("2026-09-20T10:00:00Z").toUTCString(),
    },
    status: 200,
  });
};

const installParticipantAPI = async (
  page: Page,
  fixtureSet: FixtureSet,
  snapshot: () => ParticipantSnapshot,
): Promise<void> => {
  await page.route(`**${publicPath}`, (route) => fulfillJSON(route, fixtureSet.public.tournament));
  await page.route(`**${participantLobbyPath}`, (route) => fulfillJSON(route, snapshot().lobby));
  await page.route(`**${participantSnapshotPath}*`, (route) => fulfillJSON(route, snapshot()));
};

const recoveryWithSeries = (
  base: ParticipantSnapshot,
  series: Series,
  revision: number,
): ParticipantSnapshot => ({
  ...base,
  assignment: null,
  lobby: {
    ...base.lobby,
    projection_revision: revision,
    required_action: "review_result",
    series: [{
      format: series.format,
      opponent_display_name: "Боб",
      series_id: series.id,
      state: series.state,
      wave_id: tournamentFixtureIds.bo3Wave,
    }],
    status: "assigned",
  },
  next_cursor: {
    ...base.next_cursor,
    projection_revision: revision,
  },
  projection_revision: revision,
  runtime: null,
  series,
  wave: null,
});

const completedAttempt = (
  game: Series["slots"][number]["attempts"][number],
  winnerId: string,
  resultRevisionId: string,
): Series["slots"][number]["attempts"][number] => ({
  ...game,
  result_reason: "solved",
  result_revision_id: resultRevisionId,
  state: "completed",
  winner_id: winnerId,
});

test("participant sees authoritative BO1 and BO3 2:0 results without a phantom third game", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const baseSeries = fixtureSet.participant.recovery.series;
  if (baseSeries === null) {
    throw new Error("participant fixture requires series details");
  }

  const bo1Series: Series = {
    ...baseSeries,
    current_result_revision_id: ids.bo1Result,
    current_score_revision_id: ids.bo1Result,
    format: "bo1",
    score: { first_participant_wins: 1, second_participant_wins: 0 },
    slots: [{
      ...baseSeries.slots[0],
      attempts: [completedAttempt(
        baseSeries.slots[0].attempts[0],
        tournamentFixtureIds.firstParticipant,
        ids.bo1Result,
      )],
    }],
    state: "completed",
    winner_id: tournamentFixtureIds.firstParticipant,
  };
  let current = recoveryWithSeries(fixtureSet.participant.recovery, bo1Series, 20);
  await installParticipantAPI(page, fixtureSet, () => current);
  await page.goto(participantURL);

  const result = page.getByTestId("participant-series-result");
  await expect(result).toContainText("Серия BO1");
  await expect(page.getByTestId("participant-series-score")).toHaveText("1:0");
  await expect(result).toHaveAttribute("data-result-revision", ids.bo1Result);
  await expect(page.getByTestId("participant-series-game-1-1")).toContainText("Вы");

  const slots = baseSeries.slots;
  const bo3Series: Series = {
    ...baseSeries,
    current_result_revision_id: ids.bo3Result,
    current_score_revision_id: ids.gameTwoResult,
    score: { first_participant_wins: 2, second_participant_wins: 0 },
    slots: [
      {
        ...slots[0],
        attempts: [completedAttempt(
          slots[0].attempts[0],
          tournamentFixtureIds.firstParticipant,
          ids.bo1Result,
        )],
      },
      {
        ...slots[1],
        attempts: [completedAttempt(
          slots[1].attempts[0],
          tournamentFixtureIds.firstParticipant,
          ids.gameTwoResult,
        )],
      },
      slots[2],
    ],
    state: "completed",
    winner_id: tournamentFixtureIds.firstParticipant,
  };
  current = recoveryWithSeries(fixtureSet.participant.recovery, bo3Series, 21);
  await page.reload({ waitUntil: "domcontentloaded" });

  await expect(result).toContainText("Серия BO3");
  await expect(page.getByTestId("participant-series-score")).toHaveText("2:0");
  await expect(page.getByTestId("participant-series-game-1-1")).toBeVisible();
  await expect(page.getByTestId("participant-series-game-2-1")).toBeVisible();
  await expect(page.getByTestId("participant-series-game-3-1")).toHaveCount(0);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await selectTheme(page, "light");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  const noOverflow = await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
  expect(noOverflow).toBe(true);
});

test("participant sees BO3 2:1 game history and corrected current revision", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const baseSeries = fixtureSet.participant.recovery.series;
  if (baseSeries === null) {
    throw new Error("participant fixture requires series details");
  }
  const slots = baseSeries.slots;
  const completedSlots: Series["slots"] = [
    {
      ...slots[0],
      attempts: [completedAttempt(slots[0].attempts[0], tournamentFixtureIds.firstParticipant, ids.bo1Result)],
    },
    {
      ...slots[1],
      attempts: [completedAttempt(slots[1].attempts[0], tournamentFixtureIds.secondParticipant, ids.gameTwoResult)],
    },
    {
      ...slots[2],
      attempts: [completedAttempt(slots[2].attempts[0], tournamentFixtureIds.firstParticipant, ids.gameThreeResult)],
      score_before: { first_participant_wins: 1, second_participant_wins: 1 },
    },
  ];
  let current = recoveryWithSeries(fixtureSet.participant.recovery, {
    ...baseSeries,
    current_result_revision_id: ids.bo3Result,
    current_score_revision_id: ids.gameThreeResult,
    score: { first_participant_wins: 2, second_participant_wins: 1 },
    slots: completedSlots,
    state: "completed",
    winner_id: tournamentFixtureIds.firstParticipant,
  }, 30);
  await installParticipantAPI(page, fixtureSet, () => current);
  await page.goto(participantURL);

  const result = page.getByTestId("participant-series-result");
  await expect(page.getByTestId("participant-series-score")).toHaveText("2:1");
  await expect(page.getByTestId("participant-series-game-3-1")).toContainText("1:1");
  await expect(result).toHaveAttribute("data-result-revision", ids.bo3Result);

  current = recoveryWithSeries(fixtureSet.participant.recovery, {
    ...baseSeries,
    current_result_revision_id: ids.correctedResult,
    current_score_revision_id: ids.correctedScore,
    score: { first_participant_wins: 1, second_participant_wins: 2 },
    slots: completedSlots,
    state: "completed",
    winner_id: tournamentFixtureIds.secondParticipant,
  }, 31);
  await page.reload({ waitUntil: "domcontentloaded" });

  await expect(page.getByTestId("participant-series-score")).toHaveText("1:2");
  await expect(result).toHaveAttribute("data-result-revision", ids.correctedResult);
  await expect(result).toHaveAttribute("data-score-revision", ids.correctedScore);
  await expect(result.getByText(ids.bo3Result)).toHaveCount(0);
  await expect(result).toContainText("Боб");
});

test("participant keeps pre-game no-show and cancellation nullable", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const baseSeries = fixtureSet.participant.recovery.series;
  if (baseSeries === null) {
    throw new Error("participant fixture requires series details");
  }
  let current = recoveryWithSeries(fixtureSet.participant.recovery, {
    ...baseSeries,
    current_result_revision_id: ids.bo1Result,
    current_score_revision_id: null,
    format: "bo1",
    score: { first_participant_wins: 0, second_participant_wins: 0 },
    slots: [{
      ...baseSeries.slots[0],
      attempts: [{
        ...baseSeries.slots[0].attempts[0],
        result_reason: "no_show",
        result_revision_id: ids.bo1Result,
        state: "cancelled",
        winner_id: null,
      }],
    }],
    state: "cancelled",
    winner_id: null,
  }, 40);
  await installParticipantAPI(page, fixtureSet, () => current);
  await page.goto(participantURL);

  const result = page.getByTestId("participant-series-result");
  await expect(result).toContainText("Неявка");
  await expect(page.getByTestId("participant-series-game-1-1")).toContainText("Победитель");
  await expect(page.getByTestId("participant-series-game-1-1")).toContainText("Не опубликован");
  await expect(page.getByTestId("participant-assignment-state")).toHaveAttribute("data-assignment-state", "superseded");

  current = recoveryWithSeries(fixtureSet.participant.recovery, {
    ...baseSeries,
    current_result_revision_id: ids.correctedResult,
    current_score_revision_id: null,
    format: "bo1",
    score: { first_participant_wins: 0, second_participant_wins: 0 },
    slots: [{ ...baseSeries.slots[0], attempts: [] }],
    state: "cancelled",
    winner_id: null,
  }, 41);
  await page.reload({ waitUntil: "domcontentloaded" });

  await expect(page.getByTestId("participant-series-empty-history")).toBeVisible();
  await expect(result).toContainText("Сыгранных игр пока нет");
  await expect(result).toContainText("Победитель");
  await expect(result).toContainText("Не опубликован");
});
