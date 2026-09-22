import { expect, test, type Page } from "@playwright/test";

const tournamentId = "00000000-0000-4000-8000-000000000401";
const firstSeriesId = "00000000-0000-4000-8000-000000000410";
const secondSeriesId = "00000000-0000-4000-8000-000000000411";
const firstRevisionId = "00000000-0000-4000-8000-000000000420";
const serverTimestamp = "2026-09-15T10:00:00Z";
const snapshotPath = `/api/v1/tournaments/${tournamentId}/snapshot`;
const tournamentPath = `/api/v1/tournaments/${tournamentId}`;

const jsonHeaders = {
  "content-type": "application/json",
  date: new Date(serverTimestamp).toUTCString(),
};

const installSocketStub = async (
  page: Page,
  initialFrame: Record<string, unknown> | null = null,
): Promise<void> => {
  await page.addInitScript((serializedFrame: string | null) => {
    const NativeWebSocket = window.WebSocket;
    const frame = serializedFrame === null ? null : JSON.parse(serializedFrame) as Record<string, unknown>;
    class PublicSocket {
      static readonly CLOSED = 3;
      static readonly CLOSING = 2;
      static readonly CONNECTING = 0;
      static readonly OPEN = 1;
      onclose: ((event: { code: number }) => void) | null = null;
      onerror: (() => void) | null = null;
      onmessage: ((event: { data: string }) => void) | null = null;
      onopen: (() => void) | null = null;
      readyState = PublicSocket.CONNECTING;

      constructor(readonly url: string) {
        queueMicrotask(() => {
          this.readyState = PublicSocket.OPEN;
          this.onopen?.();
          if (frame !== null) {
            this.onmessage?.({ data: JSON.stringify(frame) });
          }
        });
      }

      close(code = 1000): void {
        this.readyState = PublicSocket.CLOSED;
        this.onclose?.({ code });
      }
    }

    const WebSocketProxy = new Proxy(NativeWebSocket, {
      construct(target, args) {
        const url = String(args[0] ?? "");
        if (url.includes("/api/v1/tournaments/") && url.includes("/realtime")) {
          return new PublicSocket(url);
        }
        return Reflect.construct(target, args);
      },
    });
    Object.defineProperty(window, "WebSocket", {
      configurable: true,
      value: WebSocketProxy,
    });
  }, initialFrame === null ? null : JSON.stringify(initialFrame));
};

const installFullscreenMock = async (page: Page): Promise<void> => {
  await page.addInitScript(() => {
    let activeElement: Element | null = null;
    Object.defineProperty(document, "fullscreenElement", {
      configurable: true,
      get: () => activeElement,
    });
    Element.prototype.requestFullscreen = function requestFullscreen(): Promise<void> {
      activeElement = this;
      document.dispatchEvent(new Event("fullscreenchange"));
      return Promise.resolve();
    };
    Object.defineProperty(document, "exitFullscreen", {
      configurable: true,
      value: (): Promise<void> => {
        activeElement = null;
        document.dispatchEvent(new Event("fullscreenchange"));
        return Promise.resolve();
      },
    });
  });
};

type SnapshotMode = "live" | "bo1" | "completed-bo1" | "completed-bo3" | "result";

const publicSnapshot = (mode: SnapshotMode): Record<string, unknown> => {
  const resultMode = mode === "result";
  const secondDraftMode = mode === "bo1" || mode === "completed-bo1";
  const completedDraftMode = mode === "completed-bo1" || mode === "completed-bo3";
  const firstDeadline = "2026-09-15T10:05:00Z";
  const secondDeadline = "2026-09-15T10:08:00Z";
  const projectionRevision = resultMode ? 11 : completedDraftMode ? 12 : secondDraftMode ? 10 : 9;
  const eventSequence = projectionRevision;
  const draftActions = completedDraftMode
    ? secondDraftMode
      ? [
        {
          action: "ban",
          actor_display_name: "Чарли",
          automatic: false,
          category: "web",
          occurred_at: "2026-09-15T10:01:00Z",
          turn: 1,
        },
        {
          action: "pick",
          actor_display_name: "Дана",
          automatic: true,
          category: "crypto",
          occurred_at: "2026-09-15T10:01:30Z",
          turn: 2,
        },
      ]
      : [
        {
          action: "ban",
          actor_display_name: "Алиса",
          automatic: false,
          category: "web",
          occurred_at: "2026-09-15T10:00:30Z",
          turn: 1,
        },
        {
          action: "pick",
          actor_display_name: "Боб",
          automatic: true,
          category: "crypto",
          occurred_at: "2026-09-15T10:01:00Z",
          turn: 2,
        },
        {
          action: "ban",
          actor_display_name: "Алиса",
          automatic: false,
          category: "pwn",
          occurred_at: "2026-09-15T10:01:30Z",
          turn: 3,
        },
        {
          action: "pick",
          actor_display_name: "Боб",
          automatic: true,
          category: "forensics",
          occurred_at: "2026-09-15T10:02:00Z",
          turn: 4,
        },
      ]
    : secondDraftMode
      ? [{
        action: "ban",
        actor_display_name: "Чарли",
        automatic: true,
        category: "web",
        occurred_at: "2026-09-15T10:01:00Z",
        turn: 1,
      }]
      : [{
        action: "ban",
        actor_display_name: "Алиса",
        automatic: false,
        category: "web",
        occurred_at: "2026-09-15T10:00:30Z",
        turn: 1,
      }];
  const draftSeriesId = secondDraftMode ? secondSeriesId : firstSeriesId;
  const draftFormat = secondDraftMode ? "bo1" : "bo3";
  return {
    bracket: {
      matches: [],
      projection_revision: projectionRevision,
      tournament_id: tournamentId,
    },
    live_draft: resultMode ? null : {
      actions: draftActions,
      auto_action_pending: completedDraftMode ? false : secondDraftMode,
      current_action: completedDraftMode ? null : "pick",
      current_actor_display_name: completedDraftMode ? null : secondDraftMode ? "Дана" : "Боб",
      current_turn: completedDraftMode ? null : 2,
      first_actor_display_name: secondDraftMode ? "Чарли" : "Алиса",
      format: draftFormat,
      pool: secondDraftMode ? ["web", "crypto", "pwn"] : ["web", "crypto", "pwn", "forensics", "reverse"],
      projection_revision: projectionRevision,
      selected_categories: completedDraftMode ? (secondDraftMode ? ["web"] : ["web", "crypto", "pwn"]) : [],
      series_id: draftSeriesId,
      state: completedDraftMode ? "completed" : "active",
      tournament_id: tournamentId,
      turn_deadline: completedDraftMode ? null : secondDraftMode ? "2026-09-15T09:59:00Z" : "2026-09-15T10:03:00Z",
    },
    live_series: [
      {
        current_game: {
          category: "web",
          effective_deadline: resultMode ? null : firstDeadline,
          finished_at: resultMode ? "2026-09-15T10:05:00Z" : null,
          first_connection_status: "connected",
          position: 1,
          result_reason: resultMode ? "solved" : null,
          second_connection_status: "disconnected",
          started_at: "2026-09-15T10:00:00Z",
          state: resultMode ? "completed" : "active",
          winner_display_name: resultMode ? "Алиса" : null,
        },
        current_game_position: 1,
        first_display_name: "Алиса",
        format: "bo3",
        round_number: 1,
        scheduled_at: null,
        score: { first_wins: resultMode ? 2 : 1, second_wins: resultMode ? 1 : 0 },
        second_display_name: "Боб",
        series_id: firstSeriesId,
        stage: "swiss",
        state: resultMode ? "completed" : "active",
      },
      {
        current_game: {
          category: "crypto",
          effective_deadline: secondDeadline,
          finished_at: null,
          first_connection_status: "connected",
          position: 2,
          result_reason: null,
          second_connection_status: "unknown",
          started_at: "2026-09-15T10:00:00Z",
          state: "active",
          winner_display_name: null,
        },
        current_game_position: 2,
        first_display_name: "Чарли",
        format: "bo1",
        round_number: 1,
        scheduled_at: null,
        score: { first_wins: 0, second_wins: 0 },
        second_display_name: "Дана",
        series_id: secondSeriesId,
        stage: "swiss",
        state: "active",
      },
    ],
    next_cursor: {
      event_sequence: eventSequence,
      projection_revision: projectionRevision,
    },
    official_results: resultMode ? [{
      recorded_at: "2026-09-15T10:06:00Z",
      revision_id: firstRevisionId,
      score: { first_wins: 2, second_wins: 1 },
      series_id: firstSeriesId,
      state: "completed",
      winner_display_name: "Алиса",
    }] : [],
    scoreboard: {
      entries: [
        {
          buchholz: 2,
          bye_count: 0,
          display_name: "Алиса",
          effective_time_ms: 42_000,
          losses: 0,
          points: 3,
          provisional_tie: false,
          qualification_status: "pending",
          rank: 1,
          wins: 1,
        },
        {
          buchholz: 1,
          bye_count: 0,
          display_name: "Боб",
          effective_time_ms: 45_000,
          losses: 1,
          points: 0,
          provisional_tie: false,
          qualification_status: "pending",
          rank: 2,
          wins: 0,
        },
      ],
      projection_revision: projectionRevision,
      tournament_id: tournamentId,
    },
    swiss_rounds: [{ bye: null, round_number: 1, state: "active" }],
    tournament: {
      finished_at: resultMode ? "2026-09-15T10:06:00Z" : null,
      preset: "tournament_v1",
      projection_revision: projectionRevision,
      roster_size: 4,
      started_at: serverTimestamp,
      state: resultMode ? "completed" : "swiss",
      tournament_id: tournamentId,
    },
  };
};

const publicRealtimeFrame = (
  mode: SnapshotMode,
  occurredAt: string,
): Record<string, unknown> => {
  const snapshot = publicSnapshot(mode);
  const cursor = snapshot.next_cursor as Record<string, unknown>;
  const scoreboard = snapshot.scoreboard as Record<string, unknown>;
  const bracket = snapshot.bracket as Record<string, unknown>;
  const tournament = snapshot.tournament as Record<string, unknown>;
  const publicProjection: Record<string, unknown> = {
    bracket: bracket.matches,
    last_sequence: cursor.event_sequence,
    live_series: snapshot.live_series,
    official_results: snapshot.official_results,
    revision: cursor.projection_revision,
    scoreboard: scoreboard.entries,
    swiss_rounds: snapshot.swiss_rounds,
    tournament: Object.fromEntries(
      Object.entries(tournament).filter(([key]) => key !== "projection_revision"),
    ),
  };
  if (snapshot.live_draft !== null) {
    const draft = snapshot.live_draft as Record<string, unknown>;
    publicProjection.draft = Object.fromEntries(
      Object.entries(draft).filter(([key]) => key !== "projection_revision" && key !== "tournament_id"),
    );
  }
  return {
    payload: {
      envelope: {
        event_id: firstRevisionId,
        occurred_at: occurredAt,
        projection_revision: cursor.projection_revision,
        public: publicProjection,
        schema_version: 1,
        sequence: cursor.event_sequence,
        tournament_id: tournamentId,
      },
    },
    type: "tournament.public",
  };
};

const installArenaRoutes = async (page: Page, mode: () => SnapshotMode): Promise<void> => {
  await page.route(`**${tournamentPath}`, async (route) => {
    await route.fulfill({
      body: JSON.stringify(publicSnapshot(mode()).tournament),
      headers: jsonHeaders,
      status: 200,
    });
  });
  await page.route(`**${snapshotPath}*`, async (route) => {
    await route.fulfill({
      body: JSON.stringify(publicSnapshot(mode())),
      headers: jsonHeaders,
      status: 200,
    });
  });
};

test("FE-041 public match center binds selected series, game timers, and passive draft", async ({ page }) => {
  let mode: SnapshotMode = "live";
  await page.clock.install({ time: serverTimestamp });
  await installSocketStub(page);
  await installFullscreenMock(page);
  await installArenaRoutes(page, () => mode);

  await page.goto(`/arena/spectator/${tournamentId}?match=series:${firstSeriesId}`, {
    waitUntil: "domcontentloaded",
  });

  const broadcast = page.getByTestId("tournament-broadcast");
  await expect(broadcast).toBeVisible();
  await expect(broadcast.getByTestId("broadcast-connection")).toBeVisible();
  await expect(broadcast.getByTestId("broadcast-selected-series")).toContainText("Алиса - Боб");
  await expect(broadcast.getByTestId("broadcast-selected-game")).toContainText("Игра 1");
  await expect(broadcast.getByTestId("broadcast-selected-game")).toHaveAttribute("data-series-id", firstSeriesId);
  await expect(broadcast.getByTestId("broadcast-selected-game")).toHaveAttribute("data-deadline", "2026-09-15T10:05:00Z");
  await expect(broadcast.getByTestId("broadcast-game-category")).toHaveText("web");
  await expect(broadcast.getByTestId("broadcast-game-countdown")).toHaveText("5:00");
  await expect(broadcast.getByTestId("broadcast-draft-first-actor")).toHaveText("Алиса");
  await expect(broadcast.getByTestId("broadcast-draft-turn")).toHaveText("2");
  await expect(broadcast.getByTestId("broadcast-draft-current-actor")).toHaveText("Боб");
  await expect(broadcast.getByTestId("broadcast-draft-current-action")).toHaveText("Выбор");
  await expect(broadcast.getByTestId("broadcast-draft-action-1")).toHaveAttribute("data-automatic", "false");
  await expect(broadcast).not.toContainText(/flag|task|hint|seed|подсказ|оператор/i);
  await expect(broadcast).not.toContainText(firstSeriesId);

  mode = "bo1";
  await page.reload({ waitUntil: "domcontentloaded" });
  const secondMatch = broadcast.locator(`[data-match-key="series:${secondSeriesId}"]`);
  await secondMatch.focus();
  await secondMatch.press("Enter");
  await expect.poll(() => new URL(page.url()).searchParams.get("match")).toBe(`series:${secondSeriesId}`);
  await expect(broadcast.getByTestId("broadcast-selected-series")).toContainText("Чарли - Дана");
  await expect(broadcast.getByTestId("broadcast-selected-game")).toContainText("Игра 2");
  await expect(broadcast.getByTestId("broadcast-game-category")).toHaveText("crypto");
  await expect(broadcast.getByTestId("broadcast-game-countdown")).toHaveText("8:00");
  await expect(broadcast.getByTestId("broadcast-draft")).toHaveAttribute("data-series-id", secondSeriesId);
  await expect(broadcast.getByTestId("broadcast-draft")).toHaveAttribute("data-deadline", "2026-09-15T09:59:00Z");
  await expect(broadcast.getByTestId("broadcast-draft-first-actor")).toHaveText("Чарли");
  await expect(broadcast.getByTestId("broadcast-draft-turn")).toHaveText("2");
  await expect(broadcast.getByTestId("broadcast-draft-current-actor")).toHaveText("Дана");
  await expect(broadcast.getByTestId("broadcast-draft-current-action")).toHaveText("Выбор");
  await expect(broadcast.getByTestId("broadcast-draft")).toContainText("автоматическое действие");
  await expect(broadcast.getByTestId("broadcast-draft-action-1")).toHaveAttribute("data-automatic", "true");

  mode = "completed-bo1";
  await page.reload({ waitUntil: "domcontentloaded" });
  const completedBo1Draft = broadcast.getByTestId("broadcast-draft");
  await expect(completedBo1Draft).toContainText("Завершена");
  await expect(completedBo1Draft.getByTestId("broadcast-draft-turn")).toHaveText("-");
  await expect(completedBo1Draft.getByTestId("broadcast-draft-current-action")).toHaveText("Не объявлено");
  await expect(completedBo1Draft.getByTestId("broadcast-draft-deadline")).toHaveText("Не объявлен");
  await expect(completedBo1Draft.getByTestId("broadcast-draft-action-1")).toContainText("web");
  await expect(completedBo1Draft.getByTestId("broadcast-draft-action-2")).toContainText("crypto");
  await expect(completedBo1Draft.getByTestId("broadcast-draft-action-1")).toHaveAttribute("data-automatic", "false");
  await expect(completedBo1Draft.getByTestId("broadcast-draft-action-2")).toHaveAttribute("data-automatic", "true");

  mode = "completed-bo3";
  await page.goto(`/arena/spectator/${tournamentId}?match=series:${firstSeriesId}`, {
    waitUntil: "domcontentloaded",
  });
  const completedBo3Draft = broadcast.getByTestId("broadcast-draft");
  await expect(completedBo3Draft).toContainText("Завершена");
  await expect(completedBo3Draft.getByTestId("broadcast-draft-first-actor")).toHaveText("Алиса");
  await expect(completedBo3Draft.getByTestId("broadcast-draft-action-1")).toContainText("web");
  await expect(completedBo3Draft.getByTestId("broadcast-draft-action-2")).toContainText("crypto");
  await expect(completedBo3Draft.getByTestId("broadcast-draft-action-3")).toContainText("pwn");
  await expect(completedBo3Draft.getByTestId("broadcast-draft-action-4")).toContainText("forensics");
  await expect(completedBo3Draft.getByTestId("broadcast-draft-action-1")).toHaveAttribute("data-automatic", "false");
  await expect(completedBo3Draft.getByTestId("broadcast-draft-action-2")).toHaveAttribute("data-automatic", "true");
  await expect(completedBo3Draft.getByTestId("broadcast-draft-action-3")).toHaveAttribute("data-automatic", "false");
  await expect(completedBo3Draft.getByTestId("broadcast-draft-action-4")).toHaveAttribute("data-automatic", "true");
});

test("FE-041 keeps official result server-only and covers fullscreen, themes, and mobile layout", async ({ page }) => {
  let mode: SnapshotMode = "live";
  await page.clock.install({ time: serverTimestamp });
  await installSocketStub(page);
  await installFullscreenMock(page);
  await installArenaRoutes(page, () => mode);

  await page.goto(`/arena/spectator/${tournamentId}?match=series:${firstSeriesId}`, {
    waitUntil: "domcontentloaded",
  });
  const broadcast = page.getByTestId("tournament-broadcast");
  await expect(broadcast.getByTestId("broadcast-official-result")).toHaveCount(0);

  await page.clock.fastForward(301_000);
  await expect(broadcast.getByTestId("broadcast-game-countdown")).toContainText("Ожидает подтверждения");
  await expect(broadcast.getByTestId("broadcast-official-result")).toHaveCount(0);

  const fullscreenToggle = broadcast.getByTestId("broadcast-fullscreen-toggle");
  await expect(fullscreenToggle).toBeEnabled();
  await fullscreenToggle.click();
  await expect(fullscreenToggle).toHaveAttribute("aria-pressed", "true");
  await fullscreenToggle.click();
  await expect(fullscreenToggle).toHaveAttribute("aria-pressed", "false");

  for (const theme of ["Темная тема", "Светлая тема"] as const) {
    await page.getByRole("button", { name: theme }).click();
    await expect(page.locator("html")).toHaveAttribute(
      "data-theme",
      theme === "Темная тема" ? "dark" : "light",
    );
  }

  mode = "result";
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.getByTestId("broadcast-official-result")).toContainText("2:1");
  await expect(page.getByTestId("broadcast-official-result")).toContainText("Алиса");

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const box = await page.getByTestId("tournament-broadcast").boundingBox();
  expect(box).not.toBeNull();
  if (box !== null) {
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(390);
  }
});

test("FE-041 anchors countdown to the latest public websocket timestamp", async ({ page }) => {
  await page.clock.install({ time: serverTimestamp });
  await installSocketStub(page, publicRealtimeFrame("live", "2026-09-15T10:01:00Z"));
  await installArenaRoutes(page, () => "live");

  await page.goto(`/arena/spectator/${tournamentId}?match=series:${firstSeriesId}`, {
    waitUntil: "domcontentloaded",
  });

  const broadcast = page.getByTestId("tournament-broadcast");
  await expect(broadcast.getByTestId("broadcast-selected-game")).toHaveAttribute(
    "data-deadline",
    "2026-09-15T10:05:00Z",
  );
  await expect(broadcast.getByTestId("broadcast-game-countdown")).toHaveText("4:00");
});
