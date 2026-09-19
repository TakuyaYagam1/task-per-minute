import { adminClient, unwrapApi, unwrapApiVoid } from "./client";
import {
  assertApiResponse,
  isAuditPage,
  isIncidentBundle,
  isOperatorRecoverySnapshot,
  isOperatorWave,
  isPreflightReport,
  isRoster,
  isSwissRound,
  isTournamentListResponse,
  isTournamentResponse,
  type OperatorAuditEvent,
  type OperatorAuditPage,
  type OperatorAuditRedactedPayload,
} from "./guards";
import type { components } from "./schema";

export { isOperatorWave } from "./guards";

export type Tournament = components["schemas"]["Tournament"];
export type TournamentListResponse = components["schemas"]["TournamentListResponse"];
export type CreateTournamentRequest = components["schemas"]["CreateTournamentRequest"];
export type TournamentStateFilter = components["parameters"]["TournamentStateFilter"];
export type TournamentListCursor = components["parameters"]["TournamentListCursor"];
export type TournamentPageSize = components["parameters"]["TournamentPageSize"];
export type Roster = components["schemas"]["Roster"];
export type ReplaceRosterRequest = components["schemas"]["ReplaceRosterRequest"];
export type PreflightRequest = components["schemas"]["PreflightRequest"];
export type PreflightReport = components["schemas"]["PreflightReport"];
export type LockRosterRequest = components["schemas"]["LockRosterRequest"];
export type UnlockRosterRequest = components["schemas"]["UnlockRosterRequest"];
export type PairingConfigurationRequest = components["schemas"]["PairingConfigurationRequest"];
export type SwissRound = components["schemas"]["SwissRound"];
export type AuditCursor = components["schemas"]["AuditCursor"];
export type AuditRedactedPayload = OperatorAuditRedactedPayload;
export type AuditEvent = OperatorAuditEvent;
export type AuditPage = OperatorAuditPage;
export type IncidentBundle = components["schemas"]["IncidentBundle"];
export type OperatorRecoveryCursor = components["schemas"]["OperatorRecoveryCursor"];
export type OperatorRecoverySnapshot = components["schemas"]["OperatorRecoverySnapshot"];
export type Wave = components["schemas"]["Wave"];
export type WaveControlRequest = components["schemas"]["WaveControlRequest"];
export type TournamentActionRequest = components["schemas"]["TournamentActionRequest"];
export type OperatorNoShowRequest = components["schemas"]["OperatorNoShowRequest"];
export type OperatorForfeitRequest = components["schemas"]["OperatorForfeitRequest"];
export type OperatorForfeitGameExpectation = components["schemas"]["OperatorForfeitGameExpectation"];
export type AuditEntityKind = components["parameters"]["AuditEntityKind"];
export type AuditActorKind = components["parameters"]["AuditActorKind"];

export type TournamentListQuery = {
  state?: TournamentStateFilter;
  cursor?: TournamentListCursor;
  page_size?: TournamentPageSize;
};

export type TournamentAuditQuery = {
  tournament_id: components["parameters"]["AuditTournamentId"];
  entity_kind?: AuditEntityKind;
  entity_id?: components["parameters"]["AuditEntityId"];
  event_type?: components["parameters"]["AuditEventType"];
  actor_kind?: AuditActorKind;
  actor_id?: components["parameters"]["AuditActorId"];
  result_reason?: components["parameters"]["AuditResultReason"];
  occurred_from?: components["parameters"]["AuditOccurredFrom"];
  occurred_to?: components["parameters"]["AuditOccurredTo"];
  cursor?: components["parameters"]["AuditCursor"];
  page_size?: components["parameters"]["AuditPageSize"];
};

/** One in-memory intent. The key is never persisted or copied to a URL. */
export type OperatorCommandIntent = Readonly<{
  idempotencyKey: string;
}>;

export type OperatorIdempotencyKey = string | OperatorCommandIntent;

/** The common evidence required by future operator live commands. */
export type OperatorMutationEvidence = Readonly<{
  expectedProjectionRevision: number;
  reason: string;
}>;

export type OperatorMutationContext = OperatorMutationEvidence & Readonly<{
  idempotencyKey: OperatorIdempotencyKey;
}>;

type OperatorMutationHeaders = {
  "Idempotency-Key": string;
  "X-CSRF-Token": "";
};

const UUID_VERSION_MASK = 0x0f;
const UUID_VARIANT_MASK = 0x3f;

const createRandomUUID = (): string => {
  const bytes = new Uint8Array(16);
  const cryptoSource = globalThis.crypto;
  if (cryptoSource?.getRandomValues) {
    cryptoSource.getRandomValues(bytes);
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256);
    }
  }
  bytes[6] = (bytes[6] & UUID_VERSION_MASK) | 0x40;
  bytes[8] = (bytes[8] & UUID_VARIANT_MASK) | 0x80;
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
};

export const createOperatorCommandIntent = (): OperatorCommandIntent =>
  Object.freeze({ idempotencyKey: createRandomUUID() });

const idempotencyKeyFrom = (value: OperatorIdempotencyKey): string => {
  const key = typeof value === "string" ? value : value.idempotencyKey;
  if (typeof key !== "string" || key.trim().length === 0) {
    throw new TypeError("Operator command requires a non-empty idempotency key");
  }
  return key;
};

const mutationHeaders = (value: OperatorIdempotencyKey): OperatorMutationHeaders => ({
  "Idempotency-Key": idempotencyKeyFrom(value),
  "X-CSRF-Token": "",
});

/** Keep revision and reason together when a future live command is assembled. */
export const operatorMutationEvidence = (
  evidence: OperatorMutationEvidence,
): { expected_projection_revision: number; reason: string } => {
  if (!Number.isSafeInteger(evidence.expectedProjectionRevision) || evidence.expectedProjectionRevision <= 0) {
    throw new TypeError("Operator mutation requires a valid expected projection revision");
  }
  if (evidence.reason.trim().length === 0) {
    throw new TypeError("Operator mutation requires a non-empty reason");
  }
  return {
    expected_projection_revision: evidence.expectedProjectionRevision,
    reason: evidence.reason,
  };
};

const readOperatorResponse = async <T>(
  result: ReturnType<typeof adminClient.GET> | ReturnType<typeof adminClient.POST>,
  guard: (value: unknown) => value is T,
  contract: string,
): Promise<T> => {
  const data = await unwrapApi(await result, contract);
  return assertApiResponse(data, guard, contract);
};

const readOperatorGetResponse = async <T>(
  result: ReturnType<typeof adminClient.GET>,
  guard: (value: unknown) => value is T,
  contract: string,
): Promise<T> => {
  const data = await unwrapApi(await result, contract);
  return assertApiResponse(data, guard, contract);
};

export const operatorApi = {
  async listTournaments(
    query?: TournamentListQuery,
    signal?: AbortSignal,
  ): Promise<TournamentListResponse> {
    return readOperatorGetResponse(
      adminClient.GET("/api/v1/admin/tournaments", {
        params: query === undefined ? undefined : { query },
        signal,
      }),
      isTournamentListResponse,
      "admin/tournaments list",
    );
  },

  async createTournament(
    body: CreateTournamentRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<Tournament> {
    return readOperatorResponse(
      adminClient.POST("/api/v1/admin/tournaments", {
        params: { header: mutationHeaders(intent) },
        body,
        signal,
      }),
      isTournamentResponse,
      "admin/tournament create",
    );
  },

  async getRoster(tournamentId: string, signal?: AbortSignal): Promise<Roster> {
    return readOperatorGetResponse(
      adminClient.GET("/api/v1/admin/tournaments/{tournament_id}/roster", {
        params: { path: { tournament_id: tournamentId } },
        signal,
      }),
      isRoster,
      "admin/tournament roster",
    );
  },

  async replaceRoster(
    tournamentId: string,
    body: ReplaceRosterRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<Roster> {
    return readOperatorResponse(
      adminClient.PUT("/api/v1/admin/tournaments/{tournament_id}/roster", {
        params: {
          path: { tournament_id: tournamentId },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      isRoster,
      "admin/tournament roster replace",
    );
  },

  async runRosterPreflight(
    tournamentId: string,
    body: PreflightRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<PreflightReport> {
    return readOperatorResponse(
      adminClient.POST("/api/v1/admin/tournaments/{tournament_id}/roster/preflight", {
        params: {
          path: { tournament_id: tournamentId },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      isPreflightReport,
      "admin/tournament roster preflight",
    );
  },

  async lockRoster(
    tournamentId: string,
    body: LockRosterRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<Roster> {
    return readOperatorResponse(
      adminClient.POST("/api/v1/admin/tournaments/{tournament_id}/roster/lock", {
        params: {
          path: { tournament_id: tournamentId },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      isRoster,
      "admin/tournament roster lock",
    );
  },

  async unlockRoster(
    tournamentId: string,
    body: UnlockRosterRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<Roster> {
    return readOperatorResponse(
      adminClient.POST("/api/v1/admin/tournaments/{tournament_id}/roster/unlock", {
        params: {
          path: { tournament_id: tournamentId },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      isRoster,
      "admin/tournament roster unlock",
    );
  },

  async configurePairings(
    tournamentId: string,
    body: PairingConfigurationRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<SwissRound> {
    return readOperatorResponse(
      adminClient.POST("/api/v1/admin/tournaments/{tournament_id}/pairings", {
        params: {
          path: { tournament_id: tournamentId },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      isSwissRound,
      "admin/tournament pairings configure",
    );
  },

  async controlWave(
    tournamentId: string,
    waveId: string,
    body: WaveControlRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<Wave> {
    return readOperatorResponse(
      adminClient.POST("/api/v1/admin/tournaments/{tournament_id}/waves/{wave_id}/actions", {
        params: {
          path: { tournament_id: tournamentId, wave_id: waveId },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      isOperatorWave,
      "admin/tournament wave control",
    );
  },

  async applyTournamentAction(
    tournamentId: string,
    body: TournamentActionRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<Tournament> {
    return readOperatorResponse(
      adminClient.POST("/api/v1/admin/tournaments/{tournament_id}/actions", {
        params: {
          path: { tournament_id: tournamentId },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      isTournamentResponse,
      "admin/tournament action",
    );
  },

  async resolveNoShow(
    tournamentId: string,
    waveId: string,
    body: OperatorNoShowRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<void> {
    await unwrapApiVoid(
      adminClient.POST("/api/v1/admin/tournaments/{tournament_id}/waves/{wave_id}/no-shows", {
        params: {
          path: { tournament_id: tournamentId, wave_id: waveId },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      "admin/tournament no-show",
    );
  },

  async recordForfeit(
    tournamentId: string,
    seriesId: string,
    body: OperatorForfeitRequest,
    intent: OperatorIdempotencyKey,
    signal?: AbortSignal,
  ): Promise<void> {
    await unwrapApiVoid(
      adminClient.POST("/api/v1/admin/tournaments/{tournament_id}/series/{series_id}/operator-forfeits", {
        params: {
          path: { tournament_id: tournamentId, series_id: seriesId },
          header: mutationHeaders(intent),
        },
        body,
        signal,
      }),
      "admin/tournament operator forfeit",
    );
  },

  async getSnapshot(
    tournamentId: string,
    cursor?: OperatorRecoveryCursor,
    signal?: AbortSignal,
  ): Promise<OperatorRecoverySnapshot> {
    const params = cursor === undefined
      ? { path: { tournament_id: tournamentId } }
      : { path: { tournament_id: tournamentId }, query: { cursor } };
    return readOperatorGetResponse(
      adminClient.GET("/api/v1/admin/tournaments/{tournament_id}/snapshot", {
        params,
        signal,
      }),
      isOperatorRecoverySnapshot,
      "admin/operator snapshot",
    );
  },

  async listAudit(
    query: TournamentAuditQuery,
    signal?: AbortSignal,
  ): Promise<AuditPage> {
    return readOperatorGetResponse(
      adminClient.GET("/api/v1/admin/tournament-audit", {
        params: { query },
        signal,
      }),
      isAuditPage,
      "admin/tournament audit",
    );
  },

  async exportIncident(
    tournamentId: string,
    signal?: AbortSignal,
  ): Promise<IncidentBundle> {
    return readOperatorGetResponse(
      adminClient.GET("/api/v1/admin/tournaments/{tournament_id}/incident-export", {
        params: { path: { tournament_id: tournamentId } },
        signal,
      }),
      isIncidentBundle,
      "admin/tournament incident export",
    );
  },
} as const;

export const listTournaments = operatorApi.listTournaments;
export const createTournament = operatorApi.createTournament;
export const getTournamentRoster = operatorApi.getRoster;
export const replaceTournamentRoster = operatorApi.replaceRoster;
export const runTournamentRosterPreflight = operatorApi.runRosterPreflight;
export const lockTournamentRoster = operatorApi.lockRoster;
export const unlockTournamentRoster = operatorApi.unlockRoster;
export const configureTournamentPairings = operatorApi.configurePairings;
export const controlTournamentWave = operatorApi.controlWave;
export const applyTournamentAction = operatorApi.applyTournamentAction;
export const resolveTournamentNoShow = operatorApi.resolveNoShow;
export const recordTournamentForfeit = operatorApi.recordForfeit;
export const getOperatorSnapshot = operatorApi.getSnapshot;
export const listTournamentAudit = operatorApi.listAudit;
export const exportTournamentIncident = operatorApi.exportIncident;
