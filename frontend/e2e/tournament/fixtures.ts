import {
  isGoldenOperatorResponse,
  isGoldenParticipantResponse,
  isOperatorRecoverySnapshot,
  isParticipantAssignmentResponse,
  isParticipantLobbyResponse,
  isParticipantRecoverySnapshot,
  isPublicBracketResponse,
  isPublicScoreboardResponse,
  isPublicTournamentResponse,
} from "../../lib/shared/api/guards";
import { isPublicRecoverySnapshot } from "../../lib/shared/api/tournament-recovery";
import type { components } from "../../lib/shared/api/schema";

type Schema = components["schemas"];

export const tournamentFixtureIds = {
  tournament: "00000000-0000-4000-8000-000000000101",
  roster: "00000000-0000-4000-8000-000000000102",
  firstParticipant: "00000000-0000-4000-8000-000000000110",
  secondParticipant: "00000000-0000-4000-8000-000000000111",
  thirdParticipant: "00000000-0000-4000-8000-000000000112",
  fourthParticipant: "00000000-0000-4000-8000-000000000113",
  bo1Series: "00000000-0000-4000-8000-000000000120",
  bo3Series: "00000000-0000-4000-8000-000000000121",
  bo1Wave: "00000000-0000-4000-8000-000000000130",
  bo3Wave: "00000000-0000-4000-8000-000000000131",
  bo1Slot: "00000000-0000-4000-8000-000000000140",
  bo3SlotOne: "00000000-0000-4000-8000-000000000141",
  bo3SlotTwo: "00000000-0000-4000-8000-000000000142",
  bo3SlotThree: "00000000-0000-4000-8000-000000000143",
  bo1Game: "00000000-0000-4000-8000-000000000150",
  bo3GameOne: "00000000-0000-4000-8000-000000000151",
  bo3GameTwo: "00000000-0000-4000-8000-000000000152",
  bo3GameThree: "00000000-0000-4000-8000-000000000153",
  activePause: "00000000-0000-4000-8000-000000000160",
  pauseRevision: "00000000-0000-4000-8000-000000000161",
  scoreRevision: "00000000-0000-4000-8000-000000000162",
  group: "00000000-0000-4000-8000-000000000170",
  groupRevision: "00000000-0000-4000-8000-000000000171",
  readyWindow: "00000000-0000-4000-8000-000000000172",
  attempt: "00000000-0000-4000-8000-000000000173",
  assignment: "00000000-0000-4000-8000-000000000180",
  snapshot: "00000000-0000-4000-8000-000000000181",
  task: "00000000-0000-4000-8000-000000000182",
  event: "00000000-0000-4000-8000-000000000190",
  resume: "00000000-0000-4000-8000-000000000191",
  draft: "00000000-0000-4000-8000-000000000192",
  draftEvidence: "00000000-0000-4000-8000-000000000193",
  draftCommand: "00000000-0000-4000-8000-000000000194",
} as const;

const dates = {
  created: "2026-09-13T10:00:00Z",
  started: "2026-09-13T10:05:00Z",
  paused: "2026-09-13T10:07:00Z",
  deadline: "2026-09-13T10:12:00Z",
  updated: "2026-09-13T10:08:00Z",
} as const;

export type TournamentFixtureSet = Readonly<{
  public: Readonly<{
    tournament: Schema["PublicTournamentResponse"];
    scoreboard: Schema["PublicScoreboardResponse"];
    bracket: Schema["PublicBracketResponse"];
    recovery: Schema["PublicRecoverySnapshot"];
  }>;
  participant: Readonly<{
    lobby: Schema["ParticipantLobbyResponse"];
    assignment: Schema["ParticipantAssignmentResponse"];
    recovery: Schema["ParticipantRecoverySnapshot"];
  }>;
  operator: Readonly<{
    snapshot: Schema["OperatorRecoverySnapshot"];
  }>;
  golden: Readonly<{
    operator: Schema["GoldenOperatorResponse"];
    participant: Schema["GoldenParticipantResponse"];
  }>;
}>;

const bo1Game = (): Schema["Game"] => ({
  attempt_no: 1,
  id: tournamentFixtureIds.bo1Game,
  result_reason: null,
  result_revision_id: null,
  slot_id: tournamentFixtureIds.bo1Slot,
  state: "planned",
  winner_id: null,
});

const bo3GameOne = (): Schema["Game"] => ({
  attempt_no: 1,
  id: tournamentFixtureIds.bo3GameOne,
  result_reason: "solved",
  result_revision_id: tournamentFixtureIds.scoreRevision,
  slot_id: tournamentFixtureIds.bo3SlotOne,
  state: "completed",
  winner_id: tournamentFixtureIds.firstParticipant,
});

const bo3GameTwo = (): Schema["Game"] => ({
  attempt_no: 1,
  id: tournamentFixtureIds.bo3GameTwo,
  result_reason: null,
  result_revision_id: null,
  slot_id: tournamentFixtureIds.bo3SlotTwo,
  state: "paused",
  winner_id: null,
});

const bo3GameThree = (): Schema["Game"] => ({
  attempt_no: 1,
  id: tournamentFixtureIds.bo3GameThree,
  result_reason: null,
  result_revision_id: null,
  slot_id: tournamentFixtureIds.bo3SlotThree,
  state: "planned",
  winner_id: null,
});

export const publicTournament = (projectionRevision = 9): Schema["PublicTournamentResponse"] => ({
  finished_at: null,
  preset: "tournament_v1",
  projection_revision: projectionRevision,
  roster_size: 4,
  started_at: dates.started,
  state: "technical_pause",
  tournament_id: tournamentFixtureIds.tournament,
});

export const publicScoreboard = (projectionRevision = 9): Schema["PublicScoreboardResponse"] => ({
  entries: [
    {
      buchholz: 3,
      display_name: "Алиса",
      effective_time_ms: 38_500,
      points: 3,
      rank: 1,
    },
    {
      buchholz: 2,
      display_name: "Боб",
      effective_time_ms: 42_000,
      points: 2,
      rank: 2,
    },
  ],
  projection_revision: projectionRevision,
  tournament_id: tournamentFixtureIds.tournament,
});

export const publicBracket = (projectionRevision = 9): Schema["PublicBracketResponse"] => ({
  matches: [{
    first_display_name: "Алиса",
    second_display_name: "Боб",
    position: 1,
    score: { first_participant_wins: 1, second_participant_wins: 0 },
    scheduled_at: null,
    stage: "semifinal",
    state: "technical_pause",
  }],
  projection_revision: projectionRevision,
  tournament_id: tournamentFixtureIds.tournament,
});

export const publicRecovery = (projectionRevision = 9, eventSequence = 14): Schema["PublicRecoverySnapshot"] => ({
  bracket: publicBracket(projectionRevision),
  live_draft: null,
  live_series: [{
    current_game_position: 2,
    first_display_name: "Алиса",
    format: "bo3",
    score: { first_wins: 1, second_wins: 0 },
    round_number: 1,
    scheduled_at: null,
    second_display_name: "Боб",
    series_id: tournamentFixtureIds.bo3Series,
    stage: "swiss",
    state: "technical_pause",
  }],
  next_cursor: {
    event_sequence: eventSequence,
    projection_revision: projectionRevision,
  },
  official_results: [],
  scoreboard: publicScoreboard(projectionRevision),
  tournament: publicTournament(projectionRevision),
});

export const participantLobby = (projectionRevision = 9): Schema["ParticipantLobbyResponse"] => ({
  attendance: "checked_in",
  current_swiss_round: 1,
  participant_id: tournamentFixtureIds.firstParticipant,
  projection_revision: projectionRevision,
  required_action: "wait",
  roster_locked: true,
  series: [{
    format: "bo3",
    opponent_display_name: "Боб",
    series_id: tournamentFixtureIds.bo3Series,
    state: "technical_pause",
    wave_id: tournamentFixtureIds.bo3Wave,
  }],
  state: "technical_pause",
  status: "assigned",
  swiss_points: 3,
  tournament_id: tournamentFixtureIds.tournament,
});

export const participantAssignment = (projectionRevision = 9): Schema["ParticipantAssignmentResponse"] => ({
  assignment: {
    active_snapshot: {
      category: "web",
      description: "Стабильное описание задания для изолированного smoke test.",
      difficulty: "medium",
      hints: ["Проверьте только контракт"],
      kind: "normal",
      snapshot_id: tournamentFixtureIds.snapshot,
      source_file_available: false,
      task_id: tournamentFixtureIds.task,
      task_url: null,
      time_limit: 900,
      title: "Проверка контракта",
      version: 2,
    },
    attempt_id: tournamentFixtureIds.attempt,
    context: {
      effective_deadline: null,
      game_id: tournamentFixtureIds.bo3GameTwo,
      game_number: 2,
      game_state: "paused",
      series_id: tournamentFixtureIds.bo3Series,
      series_score: { first_participant_wins: 1, second_participant_wins: 0 },
      slot_id: tournamentFixtureIds.bo3SlotTwo,
      stage: "swiss",
      started_at: dates.started,
      swiss_round: 1,
      wave_id: tournamentFixtureIds.bo3Wave,
    },
    id: tournamentFixtureIds.assignment,
    receipt: {
      assignment_id: tournamentFixtureIds.assignment,
      attempt_id: tournamentFixtureIds.attempt,
      delivered_at: dates.created,
      id: tournamentFixtureIds.event,
      participant_id: tournamentFixtureIds.firstParticipant,
      snapshot_id: tournamentFixtureIds.snapshot,
      task_id: tournamentFixtureIds.task,
    },
    undisclosed_reserve_count: 1,
  },
  projection_revision: projectionRevision,
  tournament_id: tournamentFixtureIds.tournament,
});

const operatorParticipant = (
  id: string,
  seed: number,
): Schema["Participant"] => ({
  attendance: "checked_in",
  created_at: dates.created,
  id,
  player_id: id,
  roster_id: tournamentFixtureIds.roster,
  seed,
  tournament_id: tournamentFixtureIds.tournament,
  updated_at: dates.updated,
});

const operatorTournament = (revision = 9): Schema["Tournament"] => ({
  content_revision: 4,
  created_at: dates.created,
  finished_at: null,
  id: tournamentFixtureIds.tournament,
  name: "Сентябрьский контур",
  paused_from_state: "swiss",
  planned_roster_size: 8,
  preset: "tournament_v1",
  public_id: "september-contour",
  revision,
  roster_id: tournamentFixtureIds.roster,
  roster_size: 4,
  started_at: dates.started,
  state: "technical_pause",
  updated_at: dates.updated,
});

const operatorRoster = (): Schema["Roster"] => ({
  created_at: dates.created,
  execution_started: true,
  execution_started_at: dates.started,
  id: tournamentFixtureIds.roster,
  locked: true,
  locked_at: dates.created,
  participants: [
    operatorParticipant(tournamentFixtureIds.firstParticipant, 1),
    operatorParticipant(tournamentFixtureIds.secondParticipant, 2),
    operatorParticipant(tournamentFixtureIds.thirdParticipant, 3),
    operatorParticipant(tournamentFixtureIds.fourthParticipant, 4),
  ],
  revision: 3,
  tournament_id: tournamentFixtureIds.tournament,
  updated_at: dates.updated,
});

const operatorSeries = (
  format: Schema["SeriesFormat"],
  state: Schema["SeriesState"],
): Schema["Series"] => {
  if (format === "bo1") {
    return {
      current_result_revision_id: null,
      current_score_revision_id: null,
      first_participant_id: tournamentFixtureIds.thirdParticipant,
      format,
      id: tournamentFixtureIds.bo1Series,
      score: { first_participant_wins: 0, second_participant_wins: 0 },
      second_participant_id: tournamentFixtureIds.fourthParticipant,
      slots: [{
        attempts: [bo1Game()],
        category: "crypto",
        id: tournamentFixtureIds.bo1Slot,
        position: 1,
        score_before: { first_participant_wins: 0, second_participant_wins: 0 },
        series_id: tournamentFixtureIds.bo1Series,
      }],
      state,
      tournament_id: tournamentFixtureIds.tournament,
      winner_id: null,
    };
  }

  return {
    current_result_revision_id: null,
    current_score_revision_id: tournamentFixtureIds.scoreRevision,
    first_participant_id: tournamentFixtureIds.firstParticipant,
    format,
    id: tournamentFixtureIds.bo3Series,
    score: { first_participant_wins: 1, second_participant_wins: 0 },
    second_participant_id: tournamentFixtureIds.secondParticipant,
    slots: [
      {
        attempts: [bo3GameOne()],
        category: "web",
        id: tournamentFixtureIds.bo3SlotOne,
        position: 1,
        score_before: { first_participant_wins: 0, second_participant_wins: 0 },
        series_id: tournamentFixtureIds.bo3Series,
      },
      {
        attempts: [bo3GameTwo()],
        category: "forensics",
        id: tournamentFixtureIds.bo3SlotTwo,
        position: 2,
        score_before: { first_participant_wins: 1, second_participant_wins: 0 },
        series_id: tournamentFixtureIds.bo3Series,
      },
      {
        attempts: [bo3GameThree()],
        category: "reverse",
        id: tournamentFixtureIds.bo3SlotThree,
        position: 3,
        score_before: { first_participant_wins: 1, second_participant_wins: 0 },
        series_id: tournamentFixtureIds.bo3Series,
      },
    ],
    state,
    tournament_id: tournamentFixtureIds.tournament,
    winner_id: null,
  };
};

const operatorWave = (
  id: string,
  state: Schema["WaveState"],
  seriesId: string,
  pausedAt: string | null,
): Schema["Wave"] => ({
  id,
  members: [
    {
      participant_id: tournamentFixtureIds.firstParticipant,
      readiness_revision: 2,
      ready: state === "paused",
      series_id: seriesId,
    },
    {
      participant_id: tournamentFixtureIds.secondParticipant,
      readiness_revision: 2,
      ready: state === "paused",
      series_id: seriesId,
    },
  ],
  paused_at: pausedAt,
  ready_window: null,
  revision: pausedAt === null ? 2 : 4,
  revision_id: pausedAt === null ? tournamentFixtureIds.pauseRevision : tournamentFixtureIds.scoreRevision,
  started_at: pausedAt === null ? dates.started : dates.started,
  state,
  tournament_id: tournamentFixtureIds.tournament,
});

const pauseGraph = (): Schema["PauseGraph"] => ({
  active_pause_id: tournamentFixtureIds.activePause,
  counters: [
    {
      limit: 3,
      participant_id: tournamentFixtureIds.firstParticipant,
      pause_id: tournamentFixtureIds.activePause,
      revision: 2,
      roster_id: tournamentFixtureIds.roster,
      used: 1,
    },
    {
      limit: 3,
      participant_id: tournamentFixtureIds.secondParticipant,
      pause_id: tournamentFixtureIds.activePause,
      revision: 2,
      roster_id: tournamentFixtureIds.roster,
      used: 1,
    },
  ],
  deadlines_suppressed: true,
  draft: null,
  frozen_deadlines: [{
    frozen_at: dates.paused,
    kind: "game",
    original_deadline: dates.deadline,
    owner_id: tournamentFixtureIds.bo3GameTwo,
    remaining_ms: 180_000,
    resumed_at: null,
    resumed_deadline: null,
    revision: 1,
  }],
  games: [{
    deadline: dates.deadline,
    game: bo3GameTwo(),
    resume_state: "active",
    revision: 2,
    series_id: tournamentFixtureIds.bo3Series,
  }],
  graph_revision: 3,
  paused_at: dates.paused,
  presence: [
    {
      connected_at: dates.started,
      disconnected_at: null,
      id: tournamentFixtureIds.firstParticipant,
      participant_id: tournamentFixtureIds.firstParticipant,
      presence_epoch: 2,
      revision: 2,
      roster_id: tournamentFixtureIds.roster,
      series_id: tournamentFixtureIds.bo3Series,
      state: "connected",
      tournament_id: tournamentFixtureIds.tournament,
      updated_at: dates.updated,
    },
    {
      connected_at: dates.started,
      disconnected_at: dates.paused,
      id: tournamentFixtureIds.secondParticipant,
      participant_id: tournamentFixtureIds.secondParticipant,
      presence_epoch: 2,
      revision: 2,
      roster_id: tournamentFixtureIds.roster,
      series_id: tournamentFixtureIds.bo3Series,
      state: "disconnected",
      tournament_id: tournamentFixtureIds.tournament,
      updated_at: dates.updated,
    },
  ],
  reconnect: [{
    closed_at: null,
    continuation_number: 0,
    continued_from_id: null,
    deadline: dates.deadline,
    game_id: tournamentFixtureIds.bo3GameTwo,
    id: tournamentFixtureIds.pauseRevision,
    number: 1,
    opened_at: dates.paused,
    participant_id: tournamentFixtureIds.secondParticipant,
    pause_id: tournamentFixtureIds.activePause,
    presence_epoch: 2,
    revision: 1,
    roster_id: tournamentFixtureIds.roster,
    series_id: tournamentFixtureIds.bo3Series,
    state: "open",
    suspended_by_pause_id: null,
    updated_at: dates.updated,
  }],
  roster_id: tournamentFixtureIds.roster,
  series: [{
    current_game_id: tournamentFixtureIds.bo3GameTwo,
    resume_state: "active",
    revision: 2,
    series: operatorSeries("bo3", "technical_pause"),
  }],
  terminal_action_revision: 2,
  tournament_id: tournamentFixtureIds.tournament,
  wave: operatorWave(
    tournamentFixtureIds.bo3Wave,
    "paused",
    tournamentFixtureIds.bo3Series,
    dates.paused,
  ),
});

export const operatorSnapshot = (projectionRevision = 9): Schema["OperatorRecoverySnapshot"] => ({
  next_cursor: {
    audit_sequence: 14,
    authority_revision: projectionRevision,
    projection_revision: projectionRevision,
  },
  pause_graph: pauseGraph(),
  recovery_controls: [],
  roster: operatorRoster(),
  series: [operatorSeries("bo1", "planned"), operatorSeries("bo3", "technical_pause")],
  tournament: operatorTournament(projectionRevision),
  waves: [
    operatorWave(tournamentFixtureIds.bo1Wave, "active", tournamentFixtureIds.bo1Series, null),
    operatorWave(tournamentFixtureIds.bo3Wave, "paused", tournamentFixtureIds.bo3Series, dates.paused),
  ],
});

export const participantRecovery = (projectionRevision = 9): Schema["ParticipantRecoverySnapshot"] => ({
  assignment: participantAssignment(projectionRevision).assignment,
  draft: null,
  lobby: participantLobby(projectionRevision),
  next_cursor: {
    event_sequence: 14,
    participant_view_revision: 5,
    projection_revision: projectionRevision,
  },
  projection_revision: projectionRevision,
  runtime: null,
  series: operatorSeries("bo3", "technical_pause"),
  tournament_id: tournamentFixtureIds.tournament,
  wave: operatorWave(
    tournamentFixtureIds.bo3Wave,
    "paused",
    tournamentFixtureIds.bo3Series,
    dates.paused,
  ),
});

export const participantDraft = (
  overrides: Partial<Schema["Draft"]> = {},
): Schema["Draft"] => ({
  actions: [],
  current_action: "ban",
  current_actor_id: tournamentFixtureIds.firstParticipant,
  first_participant_id: tournamentFixtureIds.firstParticipant,
  format: "bo1",
  id: tournamentFixtureIds.draft,
  legal_categories: ["web", "crypto", "reverse"],
  pool: ["web", "crypto", "reverse"],
  revision: 1,
  second_participant_id: tournamentFixtureIds.secondParticipant,
  selected_categories: [],
  series_id: tournamentFixtureIds.bo1Series,
  state: "active",
  turn: 1,
  turn_deadline: dates.deadline,
  ...overrides,
});

export const participantBo3Draft = (
  overrides: Partial<Schema["Draft"]> = {},
): Schema["Draft"] => participantDraft({
  format: "bo3",
  legal_categories: ["crypto", "forensics", "pwn", "reverse", "web"],
  pool: ["crypto", "forensics", "pwn", "reverse", "web"],
  series_id: tournamentFixtureIds.bo3Series,
  ...overrides,
});

export const participantRecoveryWithDraft = (
  draft: Schema["Draft"] = participantDraft(),
  projectionRevision = 9,
): Schema["ParticipantRecoverySnapshot"] => {
  const base = participantRecovery(projectionRevision);
  const series: Schema["Series"] | null = base.series === null
    ? null
    : {
        ...base.series,
        format: draft.format,
        id: draft.series_id,
        state: (draft.state === "completed" || draft.state === "superseded" ? "completed" : "draft") as Schema["SeriesState"],
        winner_id: null,
      };
  const wave = base.wave === null
    ? null
    : {
        ...base.wave,
        id: tournamentFixtureIds.bo1Wave,
        members: base.wave.members.map((member) => ({
          ...member,
          series_id: draft.series_id,
        })),
        paused_at: null,
        ready_window: null,
        state: "active" as const,
      };

  return {
    ...base,
    assignment: null,
    draft,
    lobby: {
      ...base.lobby,
      current_swiss_round: 1,
      participant_id: draft.current_actor_id ?? tournamentFixtureIds.firstParticipant,
      projection_revision: projectionRevision,
      required_action: draft.state === "active" ? "draft" : "wait",
      series: [{
        format: draft.format,
        opponent_display_name: "Боб",
        series_id: draft.series_id,
        state: series?.state ?? "completed",
        wave_id: tournamentFixtureIds.bo1Wave,
      }],
      state: "swiss",
      status: draft.state === "completed" || draft.state === "superseded" ? "completed" : "assigned",
    },
    next_cursor: {
      ...base.next_cursor,
      projection_revision: projectionRevision,
    },
    projection_revision: projectionRevision,
    series,
    tournament_id: base.tournament_id,
    wave,
  };
};

const goldenTask = (): Schema["GoldenRuntimeTask"] => ({
  assignment_id: tournamentFixtureIds.assignment,
  category: "web",
  description: "Безопасное описание Golden задания.",
  difficulty: "hard",
  snapshot_id: tournamentFixtureIds.snapshot,
  source_file_available: false,
  task_id: tournamentFixtureIds.task,
  time_limit_seconds: 180,
  title: "Golden проверка",
  version: 1,
});

export const goldenOperator = (runtimeRevision = 4): Schema["GoldenOperatorResponse"] => ({
  groups: [{
    attempt_id: tournamentFixtureIds.attempt,
    deadline: dates.deadline,
    group_id: tournamentFixtureIds.group,
    group_revision_id: tournamentFixtureIds.groupRevision,
    members: [
      {
        participant_id: tournamentFixtureIds.firstParticipant,
        position: 1,
        ready: true,
        submitted: false,
      },
      {
        participant_id: tournamentFixtureIds.secondParticipant,
        position: 2,
        ready: true,
        submitted: false,
      },
    ],
    position_from: 1,
    position_to: 2,
    ready_window_id: tournamentFixtureIds.readyWindow,
    runtime_revision: runtimeRevision,
    started_at: dates.started,
    state: "active",
  }],
  observed_at: dates.updated,
  tournament_id: tournamentFixtureIds.tournament,
});

export const goldenParticipant = (runtimeRevision = 4): Schema["GoldenParticipantResponse"] => ({
  attempt_id: tournamentFixtureIds.attempt,
  deadline: dates.deadline,
  group_id: tournamentFixtureIds.group,
  group_revision_id: tournamentFixtureIds.groupRevision,
  participant_id: tournamentFixtureIds.firstParticipant,
  position: 1,
  ready: true,
  ready_window_id: tournamentFixtureIds.readyWindow,
  runtime_revision: runtimeRevision,
  started_at: dates.started,
  state: "active",
  submitted: false,
  task: goldenTask(),
  tournament_id: tournamentFixtureIds.tournament,
});

export const createTournamentFixtureSet = (): TournamentFixtureSet => ({
  public: {
    bracket: publicBracket(),
    recovery: publicRecovery(),
    scoreboard: publicScoreboard(),
    tournament: publicTournament(),
  },
  participant: {
    assignment: participantAssignment(),
    lobby: participantLobby(),
    recovery: participantRecovery(),
  },
  operator: { snapshot: operatorSnapshot() },
  golden: {
    operator: goldenOperator(),
    participant: goldenParticipant(),
  },
});

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === "object" && !Array.isArray(value);

const hasExactKeys = (value: Record<string, unknown>, keys: readonly string[]): boolean => {
  const actual = Object.keys(value).sort();
  const expected = [...keys].sort();
  return actual.length === expected.length && actual.every((key, index) => key === expected[index]);
};

const fail = (reason: string): never => {
  throw new Error(`Invalid tournament fixture set: ${reason}`);
};

export const assertTournamentFixtureSet = (
  value: unknown,
): asserts value is TournamentFixtureSet => {
  const root = isRecord(value) ? value : fail("top-level value is not an object");
  if (!hasExactKeys(root, ["golden", "operator", "participant", "public"])) {
    fail("top-level roles are incomplete");
  }
  const publicValue = isRecord(root.public) ? root.public : fail("public role is not an object");
  if (!hasExactKeys(publicValue, ["bracket", "recovery", "scoreboard", "tournament"])) {
    fail("public role shape is invalid");
  }
  const participantValue = isRecord(root.participant)
    ? root.participant
    : fail("participant role is not an object");
  if (!hasExactKeys(participantValue, ["assignment", "lobby", "recovery"])) {
    fail("participant role shape is invalid");
  }
  const operatorValue = isRecord(root.operator) ? root.operator : fail("operator role is not an object");
  if (!hasExactKeys(operatorValue, ["snapshot"])) {
    fail("operator role shape is invalid");
  }
  const goldenValue = isRecord(root.golden) ? root.golden : fail("Golden role is not an object");
  if (!hasExactKeys(goldenValue, ["operator", "participant"])) {
    fail("Golden role shape is invalid");
  }
  const tournament = isPublicTournamentResponse(publicValue.tournament)
    ? publicValue.tournament
    : fail("public tournament DTO failed its runtime guard");
  const scoreboard = isPublicScoreboardResponse(publicValue.scoreboard)
    ? publicValue.scoreboard
    : fail("public scoreboard DTO failed its runtime guard");
  const bracket = isPublicBracketResponse(publicValue.bracket)
    ? publicValue.bracket
    : fail("public bracket DTO failed its runtime guard");
  const publicSnapshot = isPublicRecoverySnapshot(publicValue.recovery)
    ? publicValue.recovery
    : fail("public recovery DTO failed its runtime guard");
  const lobby = isParticipantLobbyResponse(participantValue.lobby)
    ? participantValue.lobby
    : fail("participant lobby DTO failed its runtime guard");
  const assignment = isParticipantAssignmentResponse(participantValue.assignment)
    ? participantValue.assignment
    : fail("participant assignment DTO failed its runtime guard");
  const participantSnapshot = isParticipantRecoverySnapshot(participantValue.recovery)
    ? participantValue.recovery
    : fail("participant recovery DTO failed its runtime guard");
  const operatorSnapshotValue = isOperatorRecoverySnapshot(operatorValue.snapshot)
    ? operatorValue.snapshot
    : fail("operator recovery DTO failed its runtime guard");
  const goldenOperatorValue = isGoldenOperatorResponse(goldenValue.operator)
    ? goldenValue.operator
    : fail("Golden operator DTO failed its runtime guard");
  const goldenParticipantValue = isGoldenParticipantResponse(goldenValue.participant)
    ? goldenValue.participant
    : fail("Golden participant DTO failed its runtime guard");

  const tournamentId = tournament.tournament_id;
  const projectionRevision = tournament.projection_revision;
  if (
    scoreboard.tournament_id !== tournamentId ||
    bracket.tournament_id !== tournamentId ||
    publicSnapshot.tournament.tournament_id !== tournamentId ||
    publicSnapshot.next_cursor.projection_revision !== projectionRevision ||
    lobby.tournament_id !== tournamentId ||
    lobby.projection_revision !== projectionRevision ||
    assignment.tournament_id !== tournamentId ||
    participantSnapshot.tournament_id !== tournamentId ||
    participantSnapshot.projection_revision !== projectionRevision ||
    operatorSnapshotValue.tournament.id !== tournamentId ||
    operatorSnapshotValue.next_cursor.projection_revision !== projectionRevision ||
    goldenOperatorValue.tournament_id !== tournamentId ||
    goldenParticipantValue.tournament_id !== tournamentId
  ) {
    fail("cross-role tournament or projection identity differs");
  }
  if (
    scoreboard.projection_revision !== projectionRevision ||
    bracket.projection_revision !== projectionRevision ||
    operatorSnapshotValue.tournament.revision !== projectionRevision ||
    operatorSnapshotValue.next_cursor.authority_revision !== projectionRevision
  ) {
    fail("server revisions are not aligned");
  }

  const operatorSeriesFormats = new Set(operatorSnapshotValue.series.map((series) => series.format));
  if (!operatorSeriesFormats.has("bo1") || !operatorSeriesFormats.has("bo3")) {
    fail("operator snapshot must contain both BO1 and BO3");
  }
  const pause = operatorSnapshotValue.pause_graph;
  if (
    pause === null ||
    pause.tournament_id !== tournamentId ||
    pause.paused_at === null ||
    !pause.deadlines_suppressed ||
    pause.wave.state !== "paused" ||
    pause.series.length !== 1 ||
    pause.series[0].series.id !== tournamentFixtureIds.bo3Series ||
    pause.games.length !== 1 ||
    pause.games[0].game.state !== "paused"
  ) {
    fail("pause graph is incomplete or detached from the BO3 series");
  }

  const goldenGroup = goldenOperatorValue.groups[0];
  if (
    goldenGroup === undefined ||
    goldenParticipantValue.group_id !== goldenGroup.group_id ||
    goldenParticipantValue.attempt_id !== goldenGroup.attempt_id ||
    goldenParticipantValue.ready_window_id !== goldenGroup.ready_window_id ||
    goldenParticipantValue.runtime_revision > goldenGroup.runtime_revision ||
    goldenParticipantValue.task?.time_limit_seconds !== 180
  ) {
    fail("Golden participant and operator identities are inconsistent");
  }

  const publicJSON = JSON.stringify(publicSnapshot).toLowerCase();
  if (
    publicJSON.includes("participant_id") ||
    publicJSON.includes("task_id") ||
    publicJSON.includes("flag") ||
    publicJSON.includes("secret")
  ) {
    fail("public DTO contains private material");
  }
};
