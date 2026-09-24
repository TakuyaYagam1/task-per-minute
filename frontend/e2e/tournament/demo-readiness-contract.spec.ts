import { expect, test, type Page, type Route, type WebSocketRoute } from "@playwright/test";

import {
  assertTournamentFixtureSet,
  createTournamentFixtureSet,
  participantBo3Draft,
  participantRecoveryWithDraft,
  publicRecoveryWithRoster,
  tournamentFixtureIds,
} from "./fixtures";

type FixtureSet = ReturnType<typeof createTournamentFixtureSet>;
type PublicSnapshot = ReturnType<typeof publicRecoveryWithRoster>;

const tournamentId = tournamentFixtureIds.tournament;
const publicPath = `/api/v1/tournaments/${tournamentId}`;
const publicSnapshotPath = `${publicPath}/snapshot`;
const participantLobbyPath = `${publicPath}/participant/lobby`;
const participantSnapshotPath = `${publicPath}/participant/snapshot`;
const participantGoldenPath = `${publicPath}/participant/golden`;
const participantRealtimePath = `${publicPath}/participant/realtime`;
const operatorSnapshotPath = `/api/v1/admin/tournaments/${tournamentId}/snapshot`;
const operatorRealtimePath = `/api/v1/admin/tournaments/${tournamentId}/realtime`;
const participantURL = `/arena/participant/${tournamentId}`;
const operatorURL = `/arena/operator/${tournamentId}`;
const spectatorURL = `/arena/spectator/${tournamentId}`;

const fulfillJSON = async (route: Route, body: unknown): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify(body),
    headers: {
      "content-type": "application/json",
      date: new Date("2026-09-15T10:00:00Z").toUTCString(),
    },
    status: 200,
  });
};

const installPublicAccess = async (
  page: Page,
  fixtureSet: FixtureSet,
  snapshot: PublicSnapshot | (() => PublicSnapshot),
): Promise<void> => {
  await page.route(`**${publicPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.public.tournament);
  });
  await page.route(`**${publicSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, typeof snapshot === "function" ? snapshot() : snapshot);
  });
};

const installParticipantAccess = async (
  page: Page,
  fixtureSet: FixtureSet,
  snapshot: ReturnType<typeof participantRecoveryWithDraft>,
): Promise<void> => {
  await page.addInitScript(() => {
    document.cookie = "tpm_player_csrf=demo-player-csrf; Path=/";
  });
  await page.route(`**${publicPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, { ...fixtureSet.public.tournament, state: "golden" });
  });
  await page.route(`**${participantLobbyPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, snapshot.lobby);
  });
  await page.route(`**${participantSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, snapshot);
  });
  await page.route(`**${participantGoldenPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.golden.participant);
  });
};

const participantRealtimeFrame = (sequence: number): string => JSON.stringify({
  type: "tournament.participant",
  payload: {
    envelope: {
      schema_version: 1,
      tournament_id: tournamentId,
      sequence,
      event_id: sequence === 1 ? tournamentFixtureIds.pauseRevision : tournamentFixtureIds.scoreRevision,
      occurred_at: "2026-09-15T10:00:00Z",
      projection_revision: 9,
      resume_id: tournamentFixtureIds.resume,
      participant: {},
    },
  },
});

const installPassiveWebSocket = async (page: Page): Promise<void> => {
  await page.addInitScript(() => {
    const NativeWebSocket = window.WebSocket;
    class PassiveSocket {
      static readonly CLOSED = 3;
      static readonly CLOSING = 2;
      static readonly CONNECTING = 0;
      static readonly OPEN = 1;
      onclose: ((event: { code: number }) => void) | null = null;
      onerror: (() => void) | null = null;
      onmessage: ((event: { data: string }) => void) | null = null;
      onopen: (() => void) | null = null;
      readyState = PassiveSocket.CONNECTING;

      constructor(readonly url: string) {
        queueMicrotask(() => {
          this.readyState = PassiveSocket.OPEN;
          this.onopen?.();
        });
      }

      close(code = 1000): void {
        this.readyState = PassiveSocket.CLOSED;
        this.onclose?.({ code });
      }

      send(_value: string): void {}
    }

    const WebSocketProxy = new Proxy(NativeWebSocket, {
      construct(target, args) {
        const url = String(args[0] ?? "");
        if (url.includes("/api/v1/") && url.endsWith("/realtime")) {
          return new PassiveSocket(url);
        }
        return Reflect.construct(target, args);
      },
    });
    Object.defineProperty(window, "WebSocket", { configurable: true, value: WebSocketProxy });
  });
};

const cutoffSnapshot = (fixtureSet: FixtureSet): PublicSnapshot => {
  const base = publicRecoveryWithRoster(16, 9, 14);
  return {
    ...base,
    scoreboard: {
      ...base.scoreboard,
      entries: base.scoreboard.entries.map((entry, index) => index === 3 || index === 4
        ? { ...entry, losses: 1, points: 2, provisional_tie: true, wins: 2 }
        : index === 5
          ? { ...entry, bye_count: 1, losses: 1, points: 1 }
          : entry),
    },
    tournament: { ...fixtureSet.public.tournament, state: "swiss", projection_revision: 9 },
  };
};

const resolvedSnapshot = (): PublicSnapshot => {
  const base = publicRecoveryWithRoster(16, 10, 15);
  const entries = base.scoreboard.entries.map((entry, index) => {
    if (index < 3) return { ...entry, qualification_status: "qualified" as const };
    if (index === 3) {
      return {
        ...entry,
        losses: 1,
        points: 2,
        provisional_tie: false,
        qualification_status: "eliminated" as const,
        rank: 5,
        wins: 2,
      };
    }
    if (index === 4) {
      return {
        ...entry,
        losses: 1,
        points: 2,
        provisional_tie: false,
        qualification_status: "qualified" as const,
        rank: 4,
        wins: 2,
      };
    }
    return { ...entry, qualification_status: "eliminated" as const };
  });
  return {
    ...base,
    scoreboard: {
      ...base.scoreboard,
      entries: [...entries].sort((first, second) => first.rank - second.rank),
    },
    tournament: { ...base.tournament, state: "playoffs", projection_revision: 10 },
  };
};

const finalSnapshot = (): PublicSnapshot => {
  const base = resolvedSnapshot();
  return {
    ...base,
    bracket: {
      ...base.bracket,
      matches: [
        {
          first_display_name: "Алиса",
          second_display_name: "Боб",
          format: "bo1",
          position: 1,
          score: { first_participant_wins: 1, second_participant_wins: 0 },
          scheduled_at: null,
          stage: "semifinal",
          state: "completed",
          winner_display_name: "Алиса",
        },
        {
          first_display_name: "Чарли",
          second_display_name: "Дана",
          format: "bo1",
          position: 2,
          score: { first_participant_wins: 1, second_participant_wins: 0 },
          scheduled_at: null,
          stage: "semifinal",
          state: "completed",
          winner_display_name: "Чарли",
        },
        {
          first_display_name: "Алиса",
          second_display_name: "Чарли",
          format: "bo3",
          position: 1,
          score: { first_participant_wins: 2, second_participant_wins: 1 },
          scheduled_at: null,
          stage: "final",
          state: "completed",
          winner_display_name: "Алиса",
        },
      ],
      projection_revision: 11,
    },
    scoreboard: {
      ...base.scoreboard,
      entries: base.scoreboard.entries.map((entry, index) => index === 0
        ? { ...entry, display_name: "Алиса", points: 6, qualification_status: "qualified" as const, rank: 1 }
        : entry),
      projection_revision: 11,
    },
    next_cursor: { event_sequence: 16, projection_revision: 11 },
    tournament: {
      ...base.tournament,
      finished_at: "2026-09-15T10:20:00Z",
      projection_revision: 11,
      state: "completed",
    },
  };
};

test("player demo renders BO3, Golden, and a confirmed reconnect on the real Arena page", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const snapshot = participantRecoveryWithDraft(participantBo3Draft());
  const resumedSnapshot = {
    ...participantRecoveryWithDraft(participantBo3Draft({
      current_actor_id: tournamentFixtureIds.secondParticipant,
      revision: 2,
      turn: 2,
    }), 10),
    lobby: { ...snapshot.lobby, projection_revision: 10, required_action: "wait" as const },
    next_cursor: { event_sequence: 16, participant_view_revision: 6, projection_revision: 10 },
  };
  let currentSnapshot = snapshot;
  const connections: URL[] = [];
  const snapshotRequests: string[] = [];
  expect(() => assertTournamentFixtureSet(fixtureSet)).not.toThrow();
  await installParticipantAccess(page, fixtureSet, snapshot);
  await page.unroute(`**${participantSnapshotPath}*`);
  await page.route(`**${participantSnapshotPath}*`, async (route) => {
    snapshotRequests.push(route.request().url());
    await fulfillJSON(route, currentSnapshot);
  });
  await page.context().routeWebSocket(
    (url) => url.pathname === participantRealtimePath,
    async (socket: WebSocketRoute) => {
      connections.push(new URL(socket.url()));
      const sequence = connections.length;
      if (sequence === 2) currentSnapshot = resumedSnapshot;
      socket.send(participantRealtimeFrame(sequence));
      if (sequence === 1) await socket.close({ code: 1013, reason: "demo reconnect" });
    },
  );

  await page.goto(participantURL, { waitUntil: "domcontentloaded" });
  const player = page.getByTestId("participant-player-panel");
  const draft = page.getByTestId("participant-draft-panel");
  const golden = page.getByTestId("participant-golden-panel");
  await expect(player).toBeVisible();
  await expect(player).toHaveAttribute("data-projection-revision", "9");
  await expect(draft).toHaveAttribute("data-draft-format", "bo3");
  await expect(golden).toHaveAttribute("data-golden-state", "active");
  await expect(golden).toHaveAttribute("data-runtime-revision", "4");
  await expect.poll(() => connections.length).toBe(2);
  expect(connections[0]?.searchParams.has("resume_id")).toBe(false);
  expect(connections[1]?.searchParams.get("resume_id")).toBe(tournamentFixtureIds.resume);
  await expect.poll(() => snapshotRequests.length).toBeGreaterThanOrEqual(3);
  await expect(player).toHaveAttribute("data-projection-revision", "10");
  await expect(draft).toHaveAttribute("data-draft-revision", "2");
  await expect(draft.getByText("Ход соперника", { exact: true })).toBeVisible();

  for (const [width, height] of [[390, 844], [768, 1024], [1440, 900]] as const) {
    await page.setViewportSize({ width, height });
    for (const theme of ["Светлая тема", "Темная тема"] as const) {
      await page.getByRole("button", { name: theme }).click();
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme === "Светлая тема" ? "light" : "dark");
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      await expect(player).toBeVisible();
      await expect(golden).toBeVisible();
    }
  }
});

test("spectator demo shows the tie-to-Golden result and the completed BO3 bracket", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let currentSnapshot = cutoffSnapshot(fixtureSet);
  const snapshotFetches: number[] = [];
  const publicAccess = {
    ...fixtureSet,
    public: {
      ...fixtureSet.public,
      tournament: { ...fixtureSet.public.tournament, state: "swiss" as const },
    },
  };
  await installPublicAccess(page, publicAccess, () => {
    snapshotFetches.push(currentSnapshot.next_cursor.projection_revision);
    return currentSnapshot;
  });
  await installPassiveWebSocket(page);

  await page.goto(spectatorURL, { waitUntil: "domcontentloaded" });
  const broadcast = page.getByTestId("tournament-broadcast");
  const table = broadcast.getByRole("table", { name: "Публичная таблица турнира" });
  await expect(table.getByRole("row").filter({ hasText: "Участник 04" })).toContainText("Тай-брейк не решен");
  await expect(table.getByRole("row").filter({ hasText: "Участник 05" })).toContainText("Тай-брейк не решен");
  await broadcast.getByRole("tab", { name: "Swiss" }).click();
  await expect(broadcast.getByTestId("swiss-round-1")).toBeVisible();
  const initialFetchCount = snapshotFetches.length;

  currentSnapshot = resolvedSnapshot();
  await page.getByRole("button", { name: "Повторить синхронизацию" }).click();
  await expect.poll(() => snapshotFetches[snapshotFetches.length - 1] ?? 0).toBe(10);
  expect(snapshotFetches.length).toBeGreaterThan(initialFetchCount);
  await broadcast.getByRole("tab", { name: "Таблица" }).click();
  await expect(table.getByRole("row").filter({ hasText: "Участник 05" })).toContainText("Прошел дальше");
  await expect(table.getByRole("row").filter({ hasText: "Участник 04" })).toContainText("Выбыл");
  await expect(page.getByRole("region", { name: "Состояние турнира" })).toContainText("Ревизия сервера: 10");
  const resolvedFetchCount = snapshotFetches.length;

  currentSnapshot = finalSnapshot();
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect.poll(() => snapshotFetches[snapshotFetches.length - 1] ?? 0).toBe(11);
  expect(snapshotFetches.length).toBeGreaterThan(resolvedFetchCount);
  await expect(page.getByRole("region", { name: "Состояние турнира" })).toContainText("Ревизия сервера: 11");
  await broadcast.getByRole("tab", { name: "Плей-офф" }).click();
  const playoff = broadcast.getByRole("tabpanel");
  await expect(playoff.getByTestId("playoff-final")).toContainText("BO3");
  await expect(playoff.getByTestId("playoff-final")).toContainText("2:1");
  await expect(playoff.getByTestId("playoff-final")).toContainText("Чемпион: Алиса");
  await page.getByRole("button", { name: "Светлая тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await expect((await page.request.get("/__tournament-fixtures")).status()).toBe(404);
});

test("operator demo keeps the operator boundary and server pause state visible", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const operatorSnapshot = {
    ...fixtureSet.operator.snapshot,
    tournament: { ...fixtureSet.operator.snapshot.tournament, revision: 9 },
  };
  await page.route(`**${publicPath}`, async (route) => {
    await fulfillJSON(route, fixtureSet.public.tournament);
  });
  await page.route(`**${operatorSnapshotPath}`, async (route) => {
    await fulfillJSON(route, operatorSnapshot);
  });
  await page.context().routeWebSocket(
    (url) => url.pathname === operatorRealtimePath,
    async () => undefined,
  );

  await page.goto(operatorURL, { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("heading", { name: /Сентябрьский контур.*Оператор/ })).toBeVisible();
  const summary = page.getByTestId("operator-realtime-summary");
  await expect(summary).toBeVisible();
  await expect(summary).toContainText("Ревизия");
  await expect(summary).toContainText("Пауза");
  await expect(page.getByRole("region", { name: "Управление турниром" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Турнирная позиция" })).toHaveCount(0);
});
