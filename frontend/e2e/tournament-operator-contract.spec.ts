import { spawn, type ChildProcessByStdio } from "node:child_process";
import { createServer } from "node:net";
import { resolve } from "node:path";
import { type Readable } from "node:stream";

import { expect, test, type Page, type Request as PlaywrightRequest, type Route } from "@playwright/test";
import type { AuditPage } from "../lib/shared/api";
import type { components } from "../lib/shared/api/schema";

const frontendRoot = process.cwd();
const fixtureRoot = resolve(frontendRoot, "e2e/fixtures/operator-api");
const nextBinary = resolve(frontendRoot, "node_modules/.bin/next");
const fixtureHost = "127.0.0.1";
const tournamentId = "00000000-0000-4000-8000-000000000001";
const rosterId = "00000000-0000-4000-8000-000000000002";
const participantId = "00000000-0000-4000-8000-000000000003";
const playerId = "00000000-0000-4000-8000-000000000004";
const secondParticipantId = "00000000-0000-4000-8000-000000000012";
const seriesId = "00000000-0000-4000-8000-000000000005";
const waveId = "00000000-0000-4000-8000-000000000006";
const revisionId = "00000000-0000-4000-8000-000000000007";
const gameId = "00000000-0000-4000-8000-000000000008";
const slotId = "00000000-0000-4000-8000-000000000009";
const auditEventId = "00000000-0000-4000-8000-000000000010";
const resultEventId = "00000000-0000-4000-8000-000000000011";
const snapshotBound = "operator-snapshot-1";
const tournamentPath = "/api/v1/admin/tournaments";
const snapshotPath = `${tournamentPath}/${tournamentId}/snapshot`;
const auditPath = "/api/v1/admin/tournament-audit";
const incidentPath = `${tournamentPath}/${tournamentId}/incident-export`;

type Tournament = components["schemas"]["Tournament"];
type OperatorRecoverySnapshot = components["schemas"]["OperatorRecoverySnapshot"];

let fixtureProcess: ChildProcessByStdio<null, Readable, Readable> | undefined;
let fixtureURL = "";

const problemBody = (status: number, title: string) => ({
  type: "about:blank",
  title,
  status,
  detail: title,
});

const tournament = (revision: number): Tournament => ({
  content_revision: 7,
  created_at: "2026-09-13T10:00:00Z",
  finished_at: null,
  id: tournamentId,
  name: "Демо турнир",
  paused_from_state: null,
  planned_roster_size: 4,
  preset: "tournament_v1",
  public_id: "demo-tournament",
  revision,
  roster_id: rosterId,
  roster_size: 1,
  started_at: null,
  state: "draft",
  updated_at: "2026-09-13T10:00:00Z",
});

const participant = (): components["schemas"]["Participant"] => ({
  attendance: "checked_in",
  created_at: "2026-09-13T10:00:00Z",
  id: participantId,
  player_id: playerId,
  roster_id: rosterId,
  seed: 1,
  tournament_id: tournamentId,
  updated_at: "2026-09-13T10:00:00Z",
});

const roster = (): components["schemas"]["Roster"] => ({
  created_at: "2026-09-13T10:00:00Z",
  execution_started: false,
  execution_started_at: null,
  id: rosterId,
  locked: true,
  locked_at: "2026-09-13T10:01:00Z",
  participants: [participant()],
  revision: 2,
  tournament_id: tournamentId,
  updated_at: "2026-09-13T10:01:00Z",
});

const series = (): components["schemas"]["Series"] => ({
  current_result_revision_id: null,
  current_score_revision_id: null,
  first_participant_id: participantId,
  format: "bo1",
  id: seriesId,
  score: { first_participant_wins: 0, second_participant_wins: 0 },
  second_participant_id: secondParticipantId,
  slots: [{
    attempts: [{
      attempt_no: 1,
      id: gameId,
      result_reason: null,
      result_revision_id: null,
      slot_id: slotId,
      state: "planned",
      winner_id: null,
    }],
    category: "web",
    id: slotId,
    position: 1,
    score_before: { first_participant_wins: 0, second_participant_wins: 0 },
    series_id: seriesId,
  }],
  state: "planned",
  tournament_id: tournamentId,
  winner_id: null,
});

const wave = (): components["schemas"]["Wave"] => ({
  id: waveId,
  members: [{
    participant_id: participantId,
    readiness_revision: 1,
    ready: false,
    series_id: seriesId,
  }, {
    participant_id: secondParticipantId,
    readiness_revision: 1,
    ready: false,
    series_id: seriesId,
  }],
  paused_at: null,
  ready_window: null,
  revision: 1,
  revision_id: revisionId,
  started_at: null,
  state: "planned",
  tournament_id: tournamentId,
});

const operatorSnapshot = (projectionRevision: number): OperatorRecoverySnapshot => ({
  next_cursor: {
    audit_sequence: 1,
    authority_revision: projectionRevision,
    projection_revision: projectionRevision,
  },
  pause_graph: null,
  recovery_controls: [],
  roster: roster(),
  series: [series()],
  tournament: tournament(projectionRevision),
  waves: [wave()],
});

const auditPage = (nextCursor: components["schemas"]["AuditCursor"] | null): AuditPage => ({
  events: [{
    actor_id: null,
    actor_kind: "server",
    audit_event_id: auditEventId,
    created_at: "2026-09-13T10:00:00Z",
    entity_id: seriesId,
    entity_kind: "series",
    event_type: "series.created",
    is_current: true,
    is_superseded: false,
    occurred_at: "2026-09-13T10:00:00Z",
    official_result_revision_id: revisionId,
    redacted_payload: { entity_id: seriesId },
    result_event_id: resultEventId,
    result_reason: "score_complete",
    result_state: "planned",
    revision_number: 1,
    roster_id: rosterId,
    series_id: seriesId,
    tournament_id: tournamentId,
    winner_id: null,
  }],
  next_cursor: nextCursor,
});

const auditCursor = (): components["schemas"]["AuditCursor"] => ({
  audit_event_id: auditEventId,
  occurred_at: "2026-09-13T10:00:00Z",
  revision_id: revisionId,
  snapshot_bound: snapshotBound,
});

const reservePort = async (): Promise<number> => new Promise((resolvePort, reject) => {
  const server = createServer();
  server.once("error", reject);
  server.listen(0, fixtureHost, () => {
    const address = server.address();
    if (!address || typeof address === "string") {
      server.close();
      reject(new Error("Could not reserve a loopback port for the operator fixture"));
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
      const response = await fetch(url);
      if (response.ok) {
        return;
      }
    } catch {
      // Next has not bound the loopback port yet.
    }
    await new Promise((resolveWait) => { setTimeout(resolveWait, 250); });
  }
  throw new Error("The operator API fixture did not become ready");
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
  await expect(page.getByRole("heading", { name: "Контракт API оператора" })).toBeVisible();
  await expect(page.getByLabel("Результат вызова API")).toContainText("Ожидание");
};

const fulfillJSON = async (route: Route, status: number, body: unknown): Promise<void> => {
  await route.fulfill({
    status,
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
};

const installAdminCSRF = async (page: Page): Promise<void> => {
  await page.addInitScript(() => {
    document.cookie = "tpm_admin_access_csrf=operator-access-csrf; Path=/";
  });
};

test.beforeAll(async () => {
  await startFixture();
});

test.afterAll(async () => {
  await stopFixture();
});

test("creates a tournament and reuses the returned server revision", async ({ page }) => {
  await installAdminCSRF(page);
  const requests: PlaywrightRequest[] = [];
  await page.route(`**${tournamentPath}`, async (route) => {
    const request = route.request();
    requests.push(request);
    expect(request.method()).toBe("POST");
    expect(request.headers()["x-csrf-token"]).toBe("operator-access-csrf");
    expect(request.headers()["idempotency-key"]).toBeTruthy();
    expect(request.postDataJSON().expected_revision).toBe(requests.length === 1 ? 0 : 7);
    await fulfillJSON(route, 201, tournament(7));
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Создать соревнование" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"revision":7');
  await expect(page.getByText("Созданная ревизия: 7")).toBeVisible();
  await page.getByRole("button", { name: "Создать соревнование" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"revision":7');
  expect(requests).toHaveLength(2);
  expect(requests[0].headers()["idempotency-key"]).not.toBe(requests[1].headers()["idempotency-key"]);
});

test("reads operator snapshot and walks the audit cursor", async ({ page }) => {
  await installAdminCSRF(page);
  await page.route(`**${snapshotPath}**`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, 200, operatorSnapshot(4));
  });
  const auditRequests: PlaywrightRequest[] = [];
  await page.route(`**${auditPath}**`, async (route) => {
    auditRequests.push(route.request());
    expect(route.request().method()).toBe("GET");
    const hasCursor = auditRequests.length > 1;
    await fulfillJSON(route, 200, auditPage(hasCursor ? null : auditCursor()));
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Получить снимок оператора" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"revision":4');
  await expect(page.getByText("Снимок ревизия: 4")).toBeVisible();
  await page.getByRole("button", { name: "Получить аудит" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"events":1');
  await page.getByRole("button", { name: "Следующая страница аудита" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"events":1');
  expect(auditRequests).toHaveLength(2);
  const secondQuery = new URL(auditRequests[1].url()).searchParams;
  expect(secondQuery.get("cursor[audit_event_id]")).toBe(auditEventId);
  expect(secondQuery.get("cursor[snapshot_bound]")).toBe(snapshotBound);
});

test("keeps stale 409 and forbidden 403 as errors without optimistic snapshot state", async ({ page }) => {
  await installAdminCSRF(page);
  await page.route(`**${snapshotPath}**`, async (route) => {
    const requestURL = new URL(route.request().url());
    if (requestURL.searchParams.has("cursor[projection_revision]")) {
      await fulfillJSON(route, 409, problemBody(409, "Stale operator revision"));
      return;
    }
    await fulfillJSON(route, 200, operatorSnapshot(4));
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Получить снимок оператора" }).click();
  await expect(page.getByText("Снимок ревизия: 4")).toBeVisible();
  await page.getByRole("button", { name: "Проверить stale revision" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"status":409');
  await expect(page.getByText("Снимок ревизия: 4")).toBeVisible();

  await page.unroute(`**${snapshotPath}**`);
  await page.route(`**${snapshotPath}**`, async (route) => {
    await fulfillJSON(route, 403, problemBody(403, "Forbidden operator request"));
  });
  await page.getByRole("button", { name: "Проверить доступ оператора" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"status":403');
  await expect(page.getByText("Снимок ревизия: 4")).toBeVisible();
});

test("rejects malformed successful operator responses", async ({ page }) => {
  await installAdminCSRF(page);
  await page.route(`**${tournamentPath}`, async (route) => {
    await fulfillJSON(route, 201, { ...tournament(7), unexpected: true });
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Создать соревнование" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText("ApiContractError");
  await expect(page.getByLabel("Результат вызова API")).toContainText(
    "Invalid API response: admin/tournament create",
  );
});

test("parses incident export independently and rejects a malformed envelope", async ({ page }) => {
  const incident = {
    algorithm: "hmac-sha256-v1",
    canonical_content: "e30=",
    canonical_content_encoding: "base64",
    canonical_content_length: 2,
    canonical_content_type: "application/json",
    generated_at: "2026-09-13T10:00:00Z",
    key_id: "operator-contract",
    mac: "cd".repeat(32),
    projection_revision: 4,
    sha256: "ab".repeat(32),
    tournament_id: tournamentId,
  };
  let requestCount = 0;
  await page.route(`**${incidentPath}`, async (route) => {
    requestCount += 1;
    await fulfillJSON(route, 200, requestCount === 1 ? incident : { ...incident, mac: "invalid" });
  });

  await openFixture(page);
  await page.getByRole("button", { name: "Экспортировать инцидент" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText('"revision":4');
  await expect(page.getByLabel("Результат вызова API")).toContainText('"contentType":"application/json"');
  await page.getByRole("button", { name: "Экспортировать инцидент" }).click();
  await expect(page.getByLabel("Результат вызова API")).toContainText("ApiContractError");
  await expect(page.getByLabel("Результат вызова API")).toContainText(
    "Invalid API response: admin/tournament incident export",
  );
});
