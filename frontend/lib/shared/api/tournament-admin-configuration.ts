import { adminClient, unwrapApi } from "./client";
import { assertApiResponse } from "./guards";
import type { components } from "./schema";
import { isUUID } from "../lib/validation";

export type TournamentConfiguration = components["schemas"]["TournamentConfiguration"];
export type TournamentConfigurationCategoryPool =
  components["schemas"]["TournamentConfigurationCategoryPool"];
export type TournamentConfigurationStageDefault =
  components["schemas"]["TournamentConfigurationStageDefault"];
export type TournamentConfigurationFinalDefault =
  components["schemas"]["TournamentConfigurationFinalDefault"];
export type TournamentConfigurationSeries =
  components["schemas"]["TournamentConfigurationSeries"];
export type TournamentConfigurationRound =
  components["schemas"]["TournamentConfigurationRound"];
export type TournamentConfigurationMutationEvidence =
  components["schemas"]["TournamentConfigurationMutationEvidence"];
export type UpdateTournamentConfigurationRequest =
  components["schemas"]["UpdateTournamentConfigurationRequest"];
export type UpdateTournamentSeriesConfigurationRequest =
  components["schemas"]["UpdateTournamentSeriesConfigurationRequest"];
export type ReplaceTournamentSwissRoundConfigurationRequest =
  components["schemas"]["ReplaceTournamentSwissRoundConfigurationRequest"];

const requiredCSRFHeader = { "X-CSRF-Token": "" } as const;
const NIL_UUID = "00000000-0000-0000-0000-000000000000";
const HEX_DIGEST = /^[0-9a-f]{64}$/;
const CATEGORIES = new Set([
  "web",
  "crypto",
  "forensics",
  "reverse",
  "pwn",
  "steganography",
  "ppc",
  "osint",
  "mobile",
  "hardware",
  "misc",
]);
const MODES = new Set(["random", "admin", "draft"]);
const FORMATS = new Set(["bo1", "bo3"]);
const STAGES = new Set(["swiss", "golden", "semifinal", "final"]);

type RecordValue = Record<string, unknown>;

const isRecord = (value: unknown): value is RecordValue =>
  Boolean(value) && typeof value === "object" && !Array.isArray(value);

const hasExactKeys = (value: RecordValue, keys: readonly string[]): boolean => {
  const actual = Object.keys(value);
  return actual.length === keys.length && actual.every((key) => keys.includes(key));
};

const isSafePositiveInteger = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value > 0;

const isReserveCount = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 0 && value <= 2;

const isNonNilUUID = (value: unknown): value is string =>
  isUUID(value) && value.toLowerCase() !== NIL_UUID;

const isCategory = (value: unknown): boolean =>
  typeof value === "string" && CATEGORIES.has(value);

const isMode = (value: unknown): boolean =>
  typeof value === "string" && MODES.has(value);

const isFormat = (value: unknown): boolean =>
  typeof value === "string" && FORMATS.has(value);

const isCategoryArray = (value: unknown, min: number, max: number): value is string[] => {
  if (!Array.isArray(value) || value.length < min || value.length > max) {
    return false;
  }
  return value.every(isCategory) && new Set(value).size === value.length;
};

const isStageDefault = (value: unknown): value is TournamentConfigurationStageDefault =>
  isRecord(value) &&
  hasExactKeys(value, ["mode", "categories"]) &&
  isMode(value.mode) &&
  isCategoryArray(value.categories, 1, 5);

const isFinalDefault = (value: unknown): value is TournamentConfigurationFinalDefault =>
  isRecord(value) &&
  hasExactKeys(value, ["mode", "categories"]) &&
  value.mode === "draft" &&
  isCategoryArray(value.categories, 5, 5);

const isCategoryPool = (value: unknown): value is TournamentConfigurationCategoryPool => {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["id", "revision", "format", "categories"]) ||
    !isNonNilUUID(value.id) ||
    !isSafePositiveInteger(value.revision) ||
    !isFormat(value.format) ||
    !isCategoryArray(value.categories, value.format === "bo1" ? 3 : 5, value.format === "bo1" ? 3 : 5)
  ) {
    return false;
  }
  return true;
};

const isCategoryPoolList = (
  value: unknown,
): value is TournamentConfigurationCategoryPool[] =>
  Array.isArray(value) &&
  value.length === 2 &&
  value.every(isCategoryPool) &&
  new Set(value.map((pool) => pool.format)).size === 2;

const isConfigurationSeries = (value: unknown): value is TournamentConfigurationSeries =>
  isRecord(value) &&
  hasExactKeys(value, [
    "id",
    "stage",
    "round_number",
    "revision",
    "mode",
    "categories",
    "category_pool_revision_id",
    "category_pool_revision",
    "locked",
    "started",
    "consumed",
    "disclosed",
    "unlock_intents",
  ]) &&
  isNonNilUUID(value.id) &&
  typeof value.stage === "string" &&
  STAGES.has(value.stage) &&
  typeof value.round_number === "number" &&
  Number.isInteger(value.round_number) &&
  value.round_number >= 0 &&
  value.round_number <= 4 &&
  isSafePositiveInteger(value.revision) &&
  isMode(value.mode) &&
  isCategoryArray(value.categories, 1, 5) &&
  isNonNilUUID(value.category_pool_revision_id) &&
  isSafePositiveInteger(value.category_pool_revision) &&
  typeof value.locked === "boolean" &&
  typeof value.started === "boolean" &&
  typeof value.consumed === "boolean" &&
  typeof value.disclosed === "boolean" &&
  Array.isArray(value.unlock_intents) &&
  value.unlock_intents.every(isUnlockIntent);

const isParticipantPair = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["first_participant_id", "second_participant_id"]) &&
  isNonNilUUID(value.first_participant_id) &&
  isNonNilUUID(value.second_participant_id) &&
  value.first_participant_id !== value.second_participant_id;

const isConfigurationRound = (value: unknown): value is TournamentConfigurationRound =>
  isRecord(value) &&
  hasExactKeys(value, [
    "id",
    "round_number",
    "revision",
    "mode",
    "categories",
    "pairings",
    "bye_participant_id",
    "locked",
    "started",
    "consumed",
    "disclosed",
    "unlock_intents",
  ]) &&
  isNonNilUUID(value.id) &&
  isSafePositiveInteger(value.round_number) &&
  value.round_number <= 4 &&
  isSafePositiveInteger(value.revision) &&
  isMode(value.mode) &&
  isCategoryArray(value.categories, 1, 5) &&
  Array.isArray(value.pairings) &&
  value.pairings.every(isParticipantPair) &&
  (value.bye_participant_id === null || isNonNilUUID(value.bye_participant_id)) &&
  typeof value.locked === "boolean" &&
  typeof value.started === "boolean" &&
  typeof value.consumed === "boolean" &&
  typeof value.disclosed === "boolean" &&
  Array.isArray(value.unlock_intents) &&
  value.unlock_intents.every(isUnlockIntent);

export const isTournamentConfiguration = (value: unknown): value is TournamentConfiguration =>
  isRecord(value) &&
  hasExactKeys(value, [
    "tournament_id",
    "projection_revision_id",
    "projection_revision",
    "configuration_revision",
    "reserve_count",
    "category_pools",
    "swiss_default",
    "golden_default",
    "semifinal_default",
    "final_default",
    "series",
    "rounds",
    "updated_at",
  ]) &&
  isNonNilUUID(value.tournament_id) &&
  isNonNilUUID(value.projection_revision_id) &&
  isSafePositiveInteger(value.projection_revision) &&
  isSafePositiveInteger(value.configuration_revision) &&
  isReserveCount(value.reserve_count) &&
  isCategoryPoolList(value.category_pools) &&
  isStageDefault(value.swiss_default) &&
  isStageDefault(value.golden_default) &&
  isStageDefault(value.semifinal_default) &&
  isFinalDefault(value.final_default) &&
  Array.isArray(value.series) &&
  value.series.every(isConfigurationSeries) &&
  Array.isArray(value.rounds) &&
  value.rounds.every(isConfigurationRound) &&
  typeof value.updated_at === "string" &&
  Number.isFinite(Date.parse(value.updated_at));

const isDigest = (value: unknown): value is string =>
  typeof value === "string" && HEX_DIGEST.test(value);

const isUnlockIntent = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, [
    "reservation_id",
    "owner_id",
    "source_revision_id",
    "expected_revision",
    "expected_used",
    "expected_disclosed",
    "evidence_digest",
    "binding_digest",
  ]) &&
  isNonNilUUID(value.reservation_id) &&
  isNonNilUUID(value.owner_id) &&
  isNonNilUUID(value.source_revision_id) &&
  isSafePositiveInteger(value.expected_revision) &&
  typeof value.expected_used === "boolean" &&
  typeof value.expected_disclosed === "boolean" &&
  isDigest(value.evidence_digest) &&
  isDigest(value.binding_digest);

const isConfigurationArtifact = (value: unknown): boolean =>
  isRecord(value) &&
  hasExactKeys(value, ["kind", "id", "stage", "previous_revision_id", "successor_revision_id"]) &&
  typeof value.kind === "string" &&
  value.kind.length > 0 &&
  isNonNilUUID(value.id) &&
  typeof value.stage === "string" &&
  STAGES.has(value.stage) &&
  isNonNilUUID(value.previous_revision_id) &&
  isNonNilUUID(value.successor_revision_id);

export const isTournamentConfigurationMutationEvidence = (
  value: unknown,
): value is TournamentConfigurationMutationEvidence =>
  isRecord(value) &&
  hasExactKeys(value, [
    "command_id",
    "tournament_id",
    "operator_id",
    "reason",
    "requested_at",
    "validation_digest",
    "previous_configuration_revision",
    "next_configuration_revision",
    "affected_artifact_ids",
    "superseded_artifact_ids",
    "rebuilt_artifact_ids",
    "affected_artifacts",
    "unlock_intents",
  ]) &&
  isNonNilUUID(value.command_id) &&
  isNonNilUUID(value.tournament_id) &&
  isNonNilUUID(value.operator_id) &&
  typeof value.reason === "string" &&
  value.reason.length > 0 &&
  typeof value.requested_at === "string" &&
  Number.isFinite(Date.parse(value.requested_at)) &&
  isDigest(value.validation_digest) &&
  isSafePositiveInteger(value.previous_configuration_revision) &&
  isSafePositiveInteger(value.next_configuration_revision) &&
  value.next_configuration_revision >= value.previous_configuration_revision &&
  Array.isArray(value.affected_artifact_ids) &&
  value.affected_artifact_ids.every(isNonNilUUID) &&
  Array.isArray(value.superseded_artifact_ids) &&
  value.superseded_artifact_ids.every(isNonNilUUID) &&
  Array.isArray(value.rebuilt_artifact_ids) &&
  value.rebuilt_artifact_ids.every(isNonNilUUID) &&
  Array.isArray(value.affected_artifacts) &&
  value.affected_artifacts.every(isConfigurationArtifact) &&
  Array.isArray(value.unlock_intents) &&
  value.unlock_intents.every(isUnlockIntent);

const mutationHeaders = (idempotencyKey: string) => ({
  ...requiredCSRFHeader,
  "Idempotency-Key": idempotencyKey,
});

export const getTournamentConfiguration = async (
  tournamentId: string,
  signal?: AbortSignal,
): Promise<TournamentConfiguration> => {
  const data = await unwrapApi(
    await adminClient.GET("/api/v1/admin/tournaments/{tournament_id}/configuration", {
      params: { path: { tournament_id: tournamentId } },
      signal,
    }),
  );
  return assertApiResponse(data, isTournamentConfiguration, "admin/tournament configuration");
};

export const updateTournamentConfiguration = async (
  tournamentId: string,
  body: UpdateTournamentConfigurationRequest,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<TournamentConfigurationMutationEvidence> => {
  const data = await unwrapApi(
    await adminClient.PATCH("/api/v1/admin/tournaments/{tournament_id}/configuration", {
      params: {
        path: { tournament_id: tournamentId },
        header: mutationHeaders(idempotencyKey),
      },
      body,
      signal,
    }),
  );
  return assertApiResponse(
    data,
    isTournamentConfigurationMutationEvidence,
    "admin/tournament configuration update",
  );
};

export const updateTournamentSeriesConfiguration = async (
  tournamentId: string,
  seriesId: string,
  body: UpdateTournamentSeriesConfigurationRequest,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<TournamentConfigurationMutationEvidence> => {
  const data = await unwrapApi(
    await adminClient.PATCH(
      "/api/v1/admin/tournaments/{tournament_id}/series/{series_id}/configuration",
      {
        params: {
          path: { tournament_id: tournamentId, series_id: seriesId },
          header: mutationHeaders(idempotencyKey),
        },
        body,
        signal,
      },
    ),
  );
  return assertApiResponse(
    data,
    isTournamentConfigurationMutationEvidence,
    "admin/series configuration update",
  );
};

export const replaceTournamentSwissRoundConfiguration = async (
  tournamentId: string,
  roundNumber: number,
  body: ReplaceTournamentSwissRoundConfigurationRequest,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<TournamentConfigurationMutationEvidence> => {
  const data = await unwrapApi(
    await adminClient.PUT("/api/v1/admin/tournaments/{tournament_id}/swiss/rounds/{round_number}", {
      params: {
        path: { tournament_id: tournamentId, round_number: roundNumber },
        header: mutationHeaders(idempotencyKey),
      },
      body,
      signal,
    }),
  );
  return assertApiResponse(
    data,
    isTournamentConfigurationMutationEvidence,
    "admin/swiss round configuration update",
  );
};
