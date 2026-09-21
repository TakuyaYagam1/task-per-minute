import { spawn, type ChildProcessByStdio } from "node:child_process";
import { createServer } from "node:net";
import { resolve } from "node:path";
import { type Readable } from "node:stream";

import { expect, test, type Page, type Request, type Route } from "@playwright/test";
import type { components } from "../lib/shared/api/schema";

const frontendRoot = process.cwd();
const fixtureRoot = resolve(frontendRoot, "e2e/fixtures/public-api");
const nextBinary = resolve(frontendRoot, "node_modules/.bin/next");
const fixtureHost = "127.0.0.1";
const readinessTimeoutMs = 60_000;
const tournamentId = "00000000-0000-4000-8000-000000000001";
const tournamentPath = `/api/v1/tournaments/${tournamentId}`;
const scoreboardPath = `${tournamentPath}/scoreboard`;
const bracketPath = `${tournamentPath}/bracket`;

type PublicTournamentResponse = components["schemas"]["PublicTournamentResponse"];
type PublicScoreboardResponse = components["schemas"]["PublicScoreboardResponse"];
type PublicBracketResponse = components["schemas"]["PublicBracketResponse"];

const endpointPaths = {
  tournament: tournamentPath,
  scoreboard: scoreboardPath,
  bracket: bracketPath,
} as const;
const endpointNames = ["tournament", "scoreboard", "bracket"] as const;
const endpointPathSet: ReadonlySet<string> = new Set(Object.values(endpointPaths));

type Endpoint = keyof typeof endpointPaths;
type EndpointResponse = {
  status: number;
  body: unknown;
  headers?: Record<string, string>;
};

type ResponseResolver = (endpoint: Endpoint) => EndpointResponse;

let fixtureProcess: ChildProcessByStdio<null, Readable, Readable> | undefined;
let fixtureURL = "";
let fixtureOutput = "";

const wait = (durationMs: number): Promise<void> => new Promise((resolvePromise) => {
  setTimeout(resolvePromise, durationMs);
});

const reservePort = async (): Promise<number> => new Promise((resolvePort, reject) => {
  const server = createServer();
  server.once("error", reject);
  server.listen(0, fixtureHost, () => {
    const address = server.address();
    if (!address || typeof address === "string") {
      server.close();
      reject(new Error("Could not reserve a loopback port for the public API fixture"));
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
        NEXT_PUBLIC_API_URL: "",
        NEXT_PUBLIC_ADMIN_API_URL: "",
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
      throw new Error(`The public API fixture exited before readiness.\n${fixtureOutput}`);
    }
    if (await isFixtureReady()) {
      return;
    }
    await wait(250);
  }

  throw new Error(`The public API fixture did not become ready.\n${fixtureOutput}`);
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

test.beforeAll(async () => {
  await startFixture();
});

test.afterAll(async () => {
  await stopFixture();
});

const publicTournament = (
  projectionRevision = 17,
  rosterSize = 8,
): PublicTournamentResponse => ({
  tournament_id: tournamentId,
  preset: "tournament_v1",
  state: "registration",
  roster_size: rosterSize,
  started_at: null,
  finished_at: null,
  projection_revision: projectionRevision,
});

const publicScoreboard = (
  projectionRevision = 17,
  entries: PublicScoreboardResponse["entries"] = [
    {
      rank: 1,
      display_name: "Алиса",
      points: 3,
      buchholz: 2,
      effective_time_ms: 42_000,
    },
  ],
): PublicScoreboardResponse => ({
  tournament_id: tournamentId,
  projection_revision: projectionRevision,
  entries,
});

const publicBracket = (
  projectionRevision = 17,
  matches: PublicBracketResponse["matches"] = [
    {
      stage: "semifinal",
      position: 1,
      first_display_name: "Алиса",
      second_display_name: "Боб",
      score: { first_participant_wins: 1, second_participant_wins: 0 },
      scheduled_at: null,
      state: "active",
    },
  ],
): PublicBracketResponse => ({
  tournament_id: tournamentId,
  projection_revision: projectionRevision,
  matches,
});

const validResponses = (rosterSize = 8): Record<Endpoint, EndpointResponse> => ({
  tournament: { status: 200, body: publicTournament(17, rosterSize) },
  scoreboard: { status: 200, body: publicScoreboard() },
  bracket: { status: 200, body: publicBracket() },
});

const problemBody = (status: number, title: string): Record<string, unknown> => ({
  type: "about:blank",
  title,
  status,
  detail: `Публичный запрос завершился с кодом ${status}`,
  request_id: `public-contract-${status}`,
});

const fulfillJSON = async (
  route: Route,
  response: EndpointResponse,
): Promise<void> => {
  await route.fulfill({
    status: response.status,
    headers: {
      "content-type": "application/json",
      ...response.headers,
    },
    body: JSON.stringify(response.body),
  });
};

const installProjectionRoutes = async (
  page: Page,
  resolveResponse: ResponseResolver = (endpoint) => validResponses()[endpoint],
): Promise<void> => {
  for (const endpoint of endpointNames) {
    await page.route(`**${endpointPaths[endpoint]}`, async (route) => {
      expect(route.request().method()).toBe("GET");
      await fulfillJSON(route, resolveResponse(endpoint));
    });
  }
};

const openFixture = async (page: Page): Promise<void> => {
  await page.goto(fixtureURL, { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("heading", { name: "Контракт публичного API" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Получить публичное состояние" })).toBeEnabled();
  await expect(page.getByLabel("Результат вызова API")).toContainText("Ожидание");
};

const readResult = async (page: Page, state: "success" | "error"): Promise<void> => {
  await expect(page.getByLabel("Результат вызова API")).toContainText(`"state":"${state}"`);
};

const expectLastValidProjection = async (
  page: Page,
  rosterSize = 8,
): Promise<void> => {
  await expect(page.getByLabel("Ревизия сервера")).toHaveText("17");
  await expect(page.getByLabel("Размер ростера")).toHaveText(String(rosterSize));
  await expect(page.getByLabel("Записей таблицы")).toHaveText("1");
  await expect(page.getByLabel("Матчей сетки")).toHaveText("1");
};

test("читает три public endpoint анонимно и сохраняет server projection revision", async ({ page }) => {
  const requests: Request[] = [];
  page.on("request", (request) => {
    if (endpointPathSet.has(new URL(request.url()).pathname)) {
      requests.push(request);
    }
  });
  await installProjectionRoutes(page);

  await openFixture(page);
  await page.getByRole("button", { name: "Получить публичное состояние" }).click();
  await readResult(page, "success");
  await expect(page.getByLabel("Результат вызова API")).toContainText('"projectionRevision":17');
  await expectLastValidProjection(page);

  expect(requests).toHaveLength(3);
  expect(new Set(requests.map((request) => new URL(request.url()).pathname))).toEqual(
    new Set(Object.values(endpointPaths)),
  );
  for (const request of requests) {
    expect(request.method()).toBe("GET");
    expect(request.headers().authorization ?? null).toBeNull();
    expect(request.headers()["x-csrf-token"] ?? null).toBeNull();
  }
});

test("принимает пустой турнир с нулевым roster_size и пустыми public коллекциями", async ({ page }) => {
  await installProjectionRoutes(page, (endpoint) => {
    const responses = validResponses(0);
    if (endpoint === "scoreboard") {
      return { status: 200, body: publicScoreboard(17, []) };
    }
    if (endpoint === "bracket") {
      return { status: 200, body: publicBracket(17, []) };
    }
    return responses[endpoint];
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Получить публичное состояние" }).click();
  await readResult(page, "success");
  await expect(page.getByLabel("Ревизия сервера")).toHaveText("17");
  await expect(page.getByLabel("Размер ростера")).toHaveText("0");
  await expect(page.getByLabel("Записей таблицы")).toHaveText("0");
  await expect(page.getByLabel("Матчей сетки")).toHaveText("0");
});

test("различает 404 и 503 и не заменяет последний валидный aggregate state", async ({ page }) => {
  let failure: { endpoint: Endpoint; status: 404 | 503 } | null = null;
  await installProjectionRoutes(page, (endpoint) => {
    if (failure?.endpoint === endpoint) {
      return {
        status: failure.status,
        body: problemBody(failure.status, failure.status === 404 ? "Not Found" : "Service Unavailable"),
        headers: { "content-type": "application/problem+json" },
      };
    }
    return validResponses()[endpoint];
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Получить публичное состояние" }).click();
  await readResult(page, "success");
  await expectLastValidProjection(page);

  failure = { endpoint: "scoreboard", status: 404 };
  await page.getByRole("button", { name: "Получить публичное состояние" }).click();
  await readResult(page, "error");
  await expect(page.getByLabel("Результат вызова API")).toContainText('"status":404');
  await expect(page.getByLabel("Результат вызова API")).toContainText('"kind":"not_found"');
  await expectLastValidProjection(page);

  failure = { endpoint: "bracket", status: 503 };
  await page.getByRole("button", { name: "Получить публичное состояние" }).click();
  await readResult(page, "error");
  await expect(page.getByLabel("Результат вызова API")).toContainText('"status":503');
  await expectLastValidProjection(page);
});

test("отклоняет private, admin, participant поля и неизвестную schema version атомарно", async ({ page }) => {
  let invalid: { endpoint: Endpoint; body: unknown } | null = null;
  await installProjectionRoutes(page, (endpoint) => {
    if (invalid?.endpoint === endpoint) {
      return { status: 200, body: invalid.body };
    }
    return validResponses()[endpoint];
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Получить публичное состояние" }).click();
  await readResult(page, "success");
  await expectLastValidProjection(page);

  const privateScoreboardEntry = {
    ...publicScoreboard().entries[0],
    participant_id: "00000000-0000-4000-8000-000000000009",
  };
  const privateBracketMatch = {
    ...publicBracket().matches[0],
    winner_id: "00000000-0000-4000-8000-000000000010",
  };
  const variants: Array<{ endpoint: Endpoint; body: unknown; leaked: string }> = [
    {
      endpoint: "tournament",
      body: { ...publicTournament(), admin_only: true },
      leaked: "admin_only",
    },
    {
      endpoint: "scoreboard",
      body: { ...publicScoreboard(), entries: [privateScoreboardEntry] },
      leaked: "participant_id",
    },
    {
      endpoint: "bracket",
      body: { ...publicBracket(), matches: [privateBracketMatch] },
      leaked: "winner_id",
    },
    {
      endpoint: "tournament",
      body: { ...publicTournament(), schema_version: 2 },
      leaked: "schema_version",
    },
  ];

  for (const variant of variants) {
    invalid = variant;
    await page.getByRole("button", { name: "Получить публичное состояние" }).click();
    await readResult(page, "error");
    await expect(page.getByLabel("Результат вызова API")).toContainText("ApiContractError");
    await expectLastValidProjection(page);
    await expect(page.getByLabel("Результат вызова API")).not.toContainText(variant.leaked);
  }
});

test("не принимает несогласованные tournament_id или projection_revision из позднего ответа", async ({ page }) => {
  let invalid: { endpoint: Endpoint; body: unknown } | null = null;
  await installProjectionRoutes(page, (endpoint) => {
    if (invalid?.endpoint === endpoint) {
      return { status: 200, body: invalid.body };
    }
    return validResponses()[endpoint];
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Получить публичное состояние" }).click();
  await readResult(page, "success");
  await expectLastValidProjection(page);

  const mismatchedRevision = publicScoreboard(18);
  invalid = { endpoint: "scoreboard", body: mismatchedRevision };
  await page.getByRole("button", { name: "Получить публичное состояние" }).click();
  await readResult(page, "error");
  await expect(page.getByLabel("Результат вызова API")).toContainText("public tournament projection");
  await expectLastValidProjection(page);

  const mismatchedTournament = {
    ...publicBracket(),
    tournament_id: "00000000-0000-4000-8000-000000000002",
  };
  invalid = { endpoint: "bracket", body: mismatchedTournament };
  await page.getByRole("button", { name: "Получить публичное состояние" }).click();
  await readResult(page, "error");
  await expect(page.getByLabel("Результат вызова API")).toContainText("public tournament projection");
  await expectLastValidProjection(page);
});
