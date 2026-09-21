import { expect, test } from "@playwright/test";

import {
  getTournamentConfiguration,
  isTournamentConfiguration,
  replaceTournamentSwissRoundConfiguration,
  updateTournamentSeriesConfiguration,
  updateTournamentConfiguration,
} from "../lib/shared/api";
import type { components } from "../lib/shared/api/schema";

type TournamentConfiguration = components["schemas"]["TournamentConfiguration"];
type TournamentConfigurationMutationEvidence =
  components["schemas"]["TournamentConfigurationMutationEvidence"];
type UpdateTournamentConfigurationRequest =
  components["schemas"]["UpdateTournamentConfigurationRequest"];
type UpdateTournamentSeriesConfigurationRequest =
  components["schemas"]["UpdateTournamentSeriesConfigurationRequest"];
type ReplaceTournamentSwissRoundConfigurationRequest =
  components["schemas"]["ReplaceTournamentSwissRoundConfigurationRequest"];
type FetchHandler = (request: Request, calls: readonly Request[]) => Response | Promise<Response>;

const tournamentId = "11111111-1111-4111-8111-111111111111";
const seriesId = "22222222-2222-4222-8222-222222222222";
const poolBo1Id = "33333333-3333-4333-8333-333333333333";
const poolBo3Id = "44444444-4444-4444-8444-444444444444";

const jsonResponse = (
  status: number,
  body: unknown,
  headers: Record<string, string> = {},
): Response => new Response(JSON.stringify(body), {
  status,
  headers: { "content-type": "application/json", ...headers },
});

const withFetchStub = async <T>(
  handler: FetchHandler,
  run: (calls: Request[]) => Promise<T>,
): Promise<T> => {
  const originalFetch = globalThis.fetch;
  const calls: Request[] = [];
  globalThis.fetch = async (input, init) => {
    const request = new Request(input, init);
    calls.push(request);
    return handler(request, calls);
  };
  try {
    return await run(calls);
  } finally {
    globalThis.fetch = originalFetch;
  }
};

const configuration = (): TournamentConfiguration => ({
  tournament_id: tournamentId,
  projection_revision_id: "55555555-5555-4555-8555-555555555555",
  projection_revision: 9,
  configuration_revision: 4,
  reserve_count: 2,
  category_pools: [
    { id: poolBo1Id, revision: 2, format: "bo1", categories: ["web", "crypto", "pwn"] },
    {
      id: poolBo3Id,
      revision: 3,
      format: "bo3",
      categories: ["web", "crypto", "pwn", "reverse", "osint"],
    },
  ],
  swiss_default: { mode: "random", categories: ["web"] },
  golden_default: { mode: "random", categories: ["crypto"] },
  semifinal_default: { mode: "admin", categories: ["web", "crypto"] },
  final_default: {
    mode: "draft",
    categories: ["web", "crypto", "pwn", "reverse", "osint"],
  },
  series: [{
    id: seriesId,
    stage: "swiss",
    round_number: 1,
    revision: 6,
    mode: "admin",
    categories: ["web"],
    category_pool_revision_id: poolBo1Id,
    category_pool_revision: 2,
    locked: false,
    started: false,
    consumed: false,
    disclosed: false,
    unlock_intents: [],
  }],
  rounds: [{
    id: "88888888-8888-4888-8888-888888888888",
    round_number: 1,
    revision: 6,
    mode: "admin",
    categories: ["web"],
    pairings: [{
      first_participant_id: "99999999-9999-4999-8999-999999999999",
      second_participant_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    }],
    bye_participant_id: null,
    locked: false,
    started: false,
    consumed: false,
    disclosed: false,
    unlock_intents: [],
  }],
  updated_at: "2026-09-13T01:00:00Z",
});

const evidence = (): TournamentConfigurationMutationEvidence => ({
  command_id: "66666666-6666-4666-8666-666666666666",
  tournament_id: tournamentId,
  operator_id: "77777777-7777-4777-8777-777777777777",
  reason: "Update defaults",
  requested_at: "2026-09-13T01:00:00Z",
  validation_digest: "ab".repeat(32),
  previous_configuration_revision: 4,
  next_configuration_revision: 5,
  affected_artifact_ids: [],
  superseded_artifact_ids: [],
  rebuilt_artifact_ids: [],
  affected_artifacts: [],
  unlock_intents: [],
});

const configurationUpdate = (): UpdateTournamentConfigurationRequest => ({
  expected_projection_revision: 9,
  expected_configuration_revision: 4,
  reserve_count: 1,
  confirmed: true,
  reason: "Update defaults",
  unlock_intents: [],
  swiss_default: { mode: "admin", categories: ["web", "crypto"] },
  semifinal_default: { mode: "draft", categories: ["web", "crypto", "pwn"] },
});

const seriesUpdate = (): UpdateTournamentSeriesConfigurationRequest => ({
  expected_projection_revision: 9,
  expected_series_revision: 6,
  confirmed: true,
  reason: "Update Series",
  unlock_intents: [],
  mode: "admin",
  categories: ["web", "crypto"],
});

const roundUpdate = (): ReplaceTournamentSwissRoundConfigurationRequest => ({
  expected_projection_revision: 9,
  expected_round_revision: 6,
  confirmed: true,
  reason: "Revise round",
  unlock_intents: [],
  mode: "admin",
  categories: ["web", "crypto"],
  manual_pairings: configuration().rounds[0].pairings,
  manual_bye_participant_id: null,
});

test("reads strict operator tournament configuration through the admin client", async () => {
  const expected = configuration();
  const calls = await withFetchStub(
    (request) => {
      expect(request.method).toBe("GET");
      expect(new URL(request.url).pathname).toBe(
        `/api/v1/admin/tournaments/${tournamentId}/configuration`,
      );
      expect(request.credentials).toBe("include");
      expect(request.headers.get("authorization")).toBeNull();
      return jsonResponse(200, expected, { "cache-control": "no-store" });
    },
    async (requests) => {
      await expect(getTournamentConfiguration(tournamentId)).resolves.toEqual(expected);
      return requests;
    },
  );
  expect(calls).toHaveLength(1);
});

test("rejects malformed configuration readback", async () => {
  const malformed = {
    ...configuration(),
    final_default: {
      ...configuration().final_default,
      categories: ["web", "crypto", "pwn", "reverse"],
    },
  };
  expect(isTournamentConfiguration(malformed)).toBe(false);
  expect(isTournamentConfiguration({ ...configuration(), reserve_count: 3 })).toBe(false);

  await withFetchStub(
    () => jsonResponse(200, { ...configuration(), extra: true }),
    async () => {
      await expect(getTournamentConfiguration(tournamentId)).rejects.toThrow(
        "Invalid API response: admin/tournament configuration",
      );
    },
  );
});

test("sends CSRF and idempotency headers for configuration mutations", async () => {
  const expected = evidence();
  const calls = await withFetchStub(
    (request, requests) => {
      expect(request.credentials).toBe("include");
      expect(request.headers.get("authorization")).toBeNull();
      expect(request.headers.get("x-csrf-token")).not.toBeNull();
      expect(request.headers.get("idempotency-key")).toBe(`command-configuration-${requests.length}`);
      return jsonResponse(200, expected);
    },
    async (requests) => {
      await expect(
        updateTournamentConfiguration(tournamentId, configurationUpdate(), "command-configuration-1"),
      ).resolves.toEqual(expected);
      await expect(
        updateTournamentSeriesConfiguration(tournamentId, seriesId, seriesUpdate(), "command-configuration-2"),
      ).resolves.toEqual(expected);
      await expect(
        replaceTournamentSwissRoundConfiguration(tournamentId, 1, roundUpdate(), "command-configuration-3"),
      ).resolves.toEqual(expected);
      return requests;
    },
  );
  expect(calls).toHaveLength(3);
  expect(new URL(calls[1].url).pathname).toContain(`/series/${seriesId}/configuration`);
  expect(new URL(calls[2].url).pathname).toContain("/swiss/rounds/1");
});
