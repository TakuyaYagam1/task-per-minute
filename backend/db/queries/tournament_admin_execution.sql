-- name: LockTournamentPairingAuthority :one
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    tournament.revision AS tournament_revision,
    roster.id AS roster_id,
    roster.revision AS roster_revision,
    roster.locked_at AS roster_locked_at,
    projection.id AS projection_revision_id,
    projection.revision_number AS projection_revision,
    standings.id AS standings_artifact_id,
    standings.payload AS standings_payload,
    producer.revision_number AS standings_artifact_revision
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
JOIN LATERAL (
    SELECT revision.id, revision.revision_number
    FROM projection_revisions AS revision
    WHERE revision.tournament_id = tournament.id
        AND revision.roster_id = roster.id
        AND revision.state = 'published'
    ORDER BY revision.revision_number DESC, revision.id DESC
    LIMIT 1
    FOR UPDATE
) AS projection ON TRUE
JOIN projection_revision_artifacts AS revision_artifact
    ON revision_artifact.revision_id = projection.id
    AND revision_artifact.artifact_kind = 'standings'
JOIN projection_artifacts AS standings
    ON standings.id = revision_artifact.artifact_id
    AND standings.tournament_id = tournament.id
    AND standings.roster_id = roster.id
JOIN projection_revisions AS producer ON producer.id = standings.produced_by_revision_id
WHERE tournament.id = sqlc.arg(tournament_id)
FOR UPDATE OF tournament, roster, standings, producer;

-- name: LockTournamentPairingParticipants :many
SELECT participant.id,
    participant.seed,
    participant.attendance
FROM participants AS participant
WHERE participant.roster_id = sqlc.arg(roster_id)
ORDER BY participant.seed, participant.id
FOR UPDATE;

-- name: LockTournamentPairingHistory :many
SELECT history.pairing_id,
    history.round_id,
    first_member.participant_id AS first_participant_id,
    second_member.participant_id AS second_participant_id,
    history.prior_meeting_count,
    history.recorded_at
FROM swiss_opponent_history AS history
JOIN swiss_pairing_members AS first_member
    ON first_member.pairing_id = history.pairing_id
    AND first_member.seat = 1
JOIN swiss_pairing_members AS second_member
    ON second_member.pairing_id = history.pairing_id
    AND second_member.seat = 2
WHERE history.roster_id = sqlc.arg(roster_id)
ORDER BY history.recorded_at, history.pairing_id
FOR UPDATE OF history, first_member, second_member;

-- name: LockTournamentPairingByes :many
SELECT round_id,
    participant_id,
    decision_evidence_id
FROM swiss_byes
WHERE roster_id = sqlc.arg(roster_id)
ORDER BY round_id
FOR UPDATE;

-- name: LockTournamentPairingRounds :many
SELECT round.id,
    round.round_number,
    round.revision,
    round.locked_at
FROM swiss_rounds AS round
WHERE round.roster_id = sqlc.arg(roster_id)
ORDER BY round.round_number, round.id
FOR UPDATE OF round;

-- name: LockTournamentPairingWaves :many
SELECT link.round_id,
    wave.id AS wave_id,
    wave.state
FROM swiss_wave_links AS link
JOIN waves AS wave ON wave.id = link.wave_id
WHERE link.roster_id = sqlc.arg(roster_id)
ORDER BY link.round_id, wave.id
FOR UPDATE OF wave;

-- name: FindSwissPairingCommand :one
SELECT command_id,
    tournament_id,
    roster_id,
    round_id,
    actor_id,
    round_number,
    pairing_mode,
    category_mode,
    categories,
    source_projection_revision_id,
    source_projection_revision,
    source_tournament_revision,
    source_roster_revision,
    source_history_revision,
    request_digest,
    result_document,
    executed_at,
    created_at
FROM swiss_pairing_commands
WHERE tournament_id = sqlc.arg(tournament_id)
    AND command_id = sqlc.arg(command_id);

-- name: CreateSwissPairingCommand :one
INSERT INTO swiss_pairing_commands (
    command_id,
    tournament_id,
    roster_id,
    round_id,
    actor_id,
    round_number,
    pairing_mode,
    category_mode,
    categories,
    source_projection_revision_id,
    source_projection_revision,
    source_tournament_revision,
    source_roster_revision,
    source_history_revision,
    request_digest,
    result_document,
    executed_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(round_id),
    sqlc.arg(actor_id),
    sqlc.arg(round_number),
    sqlc.arg(pairing_mode),
    sqlc.arg(category_mode),
    sqlc.arg(categories),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(source_tournament_revision),
    sqlc.arg(source_roster_revision),
    sqlc.arg(source_history_revision),
    sqlc.arg(request_digest),
    sqlc.arg(result_document),
    sqlc.arg(executed_at),
    sqlc.arg(executed_at)
)
RETURNING command_id;

-- name: CreateSwissWaveLink :one
INSERT INTO swiss_wave_links (
    wave_id,
    tournament_id,
    roster_id,
    round_id,
    bye_participant_id,
    bye_revision_id,
    created_at
)
VALUES (
    sqlc.arg(wave_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(round_id),
    sqlc.narg(bye_participant_id)::UUID,
    sqlc.narg(bye_revision_id)::UUID,
    sqlc.arg(created_at)
)
RETURNING wave_id;

-- name: ReadTournamentExecutionTime :one
SELECT clock_timestamp()::TIMESTAMPTZ AS observed_at;

-- name: LockTournamentAdminWaveAuthority :one
SELECT tournament.state AS tournament_state,
    tournament.revision AS tournament_revision,
    roster.id AS roster_id,
    roster.revision AS roster_revision,
    projection.id AS projection_revision_id,
    projection.revision_number AS projection_revision,
    wave.id,
    wave.tournament_id,
    wave.revision_id,
    wave.revision,
    wave.state,
    wave.created_at,
    wave.updated_at,
    wave.started_at,
    wave.paused_at,
    wave.closed_at,
    ready_window.id AS ready_window_id,
    ready_window.revision_id AS ready_window_revision_id,
    ready_window.state AS ready_window_state,
    ready_window.opened_at,
    ready_window.deadline,
    ready_window.consumed_at,
    link.bye_participant_id,
    standings.id AS artifact_revision_id,
    producer.revision_number AS artifact_revision
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
JOIN LATERAL (
    SELECT revision.id, revision.revision_number
    FROM projection_revisions AS revision
    WHERE revision.tournament_id = tournament.id
        AND revision.roster_id = roster.id
        AND revision.state = 'published'
    ORDER BY revision.revision_number DESC, revision.id DESC
    LIMIT 1
    FOR UPDATE
) AS projection ON TRUE
JOIN waves AS wave
    ON wave.tournament_id = tournament.id
    AND wave.roster_id = roster.id
LEFT JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id
LEFT JOIN swiss_wave_links AS link ON link.wave_id = wave.id
LEFT JOIN projection_revision_artifacts AS revision_artifact
    ON revision_artifact.revision_id = projection.id
    AND revision_artifact.artifact_kind = 'standings'
LEFT JOIN projection_artifacts AS standings ON standings.id = revision_artifact.artifact_id
LEFT JOIN projection_revisions AS producer ON producer.id = standings.produced_by_revision_id
WHERE tournament.id = sqlc.arg(tournament_id)
    AND wave.id = sqlc.arg(wave_id)
FOR UPDATE OF tournament, roster, wave;

-- name: LockTournamentAdminWaveMembers :many
SELECT member.participant_id,
    readiness.ready,
    readiness.revision AS readiness_revision,
    readiness.ready_at
FROM wave_members AS member
JOIN wave_readiness AS readiness
    ON readiness.wave_id = member.wave_id
    AND readiness.participant_id = member.participant_id
WHERE member.wave_id = sqlc.arg(wave_id)
ORDER BY member.participant_id
FOR UPDATE OF member, readiness;

-- name: LockTournamentAdminWaveSeries :many
SELECT series.id,
    series.first_participant_id,
    series.second_participant_id,
    series.state,
    series.revision
FROM wave_series AS membership
JOIN series ON series.id = membership.series_id
WHERE membership.wave_id = sqlc.arg(wave_id)
ORDER BY series.id
FOR UPDATE OF membership, series;

-- name: LockTournamentAdminWaveGames :many
SELECT series.id AS series_id,
    attempt.id AS game_id,
    attempt.state,
    attempt.revision
FROM wave_series AS membership
JOIN series ON series.id = membership.series_id
JOIN game_slots AS slot ON slot.series_id = series.id
JOIN LATERAL (
    SELECT game.id, game.state, game.revision
    FROM game_attempts AS game
    WHERE game.slot_id = slot.id
    ORDER BY game.attempt_number DESC, game.id DESC
    LIMIT 1
    FOR UPDATE
) AS attempt ON TRUE
WHERE membership.wave_id = sqlc.arg(wave_id)
ORDER BY series.id, slot.slot_number;

-- name: LockTournamentAdminWaveAssignments :many
SELECT series.id AS series_id,
    game_slot.id AS slot_id,
    attempt.id AS game_id,
    assignment.id,
    assignment.attempt_id,
    assignment.revision,
    plan.revision_id AS plan_revision_id
FROM wave_series AS membership
JOIN series ON series.id = membership.series_id
JOIN game_slots AS game_slot
    ON game_slot.series_id = series.id
    AND game_slot.roster_id = series.roster_id
JOIN LATERAL (
    SELECT game.id,
        game.state,
        game.revision
    FROM game_attempts AS game
    WHERE game.slot_id = game_slot.id
        AND game.series_id = series.id
        AND game.roster_id = series.roster_id
    ORDER BY game.attempt_number DESC, game.id DESC
    LIMIT 1
    FOR UPDATE
) AS attempt ON TRUE
JOIN assignments AS assignment
    ON assignment.attempt_id = attempt.id
    AND assignment.series_id = series.id
    AND assignment.roster_id = series.roster_id
    AND assignment.state = 'active'
JOIN assignment_plans AS plan
    ON plan.id = assignment.plan_id
    AND plan.roster_id = series.roster_id
    AND plan.state = 'committed'
    AND plan.active_branch_id = assignment.branch_id
WHERE membership.wave_id = sqlc.arg(wave_id)
ORDER BY series.id, game_slot.slot_number, attempt.id
FOR UPDATE OF membership, series, game_slot, assignment, plan;

-- name: LockTournamentAdminWaveDeliveries :many
SELECT receipt.id,
    receipt.attempt_id,
    receipt.participant_id
FROM task_delivery_receipts AS receipt
JOIN game_attempts AS attempt ON attempt.id = receipt.attempt_id
JOIN wave_series AS membership ON membership.series_id = attempt.series_id
WHERE membership.wave_id = sqlc.arg(wave_id)
ORDER BY receipt.id
FOR UPDATE OF receipt;

-- name: FindWaveControlCommand :one
SELECT command_id,
    tournament_id,
    roster_id,
    wave_id,
    actor_id,
    action,
    source_projection_revision_id,
    source_projection_revision,
    source_tournament_revision,
    source_roster_revision,
    source_wave_revision,
    resulting_wave_revision,
    source_revisions,
    source_graph,
    request_digest,
    reason,
    result_document,
    executed_at,
    created_at
FROM wave_control_commands
WHERE tournament_id = sqlc.arg(tournament_id)
    AND command_id = sqlc.arg(command_id);

-- name: CreateWaveControlCommand :one
INSERT INTO wave_control_commands (
    command_id,
    tournament_id,
    roster_id,
    wave_id,
    actor_id,
    action,
    source_projection_revision_id,
    source_projection_revision,
    source_tournament_revision,
    source_roster_revision,
    source_wave_revision,
    resulting_wave_revision,
    source_revisions,
    source_graph,
    request_digest,
    reason,
    result_document,
    executed_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(wave_id),
    sqlc.arg(actor_id),
    sqlc.arg(action),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(source_tournament_revision),
    sqlc.arg(source_roster_revision),
    sqlc.arg(source_wave_revision),
    sqlc.arg(resulting_wave_revision),
    sqlc.arg(source_revisions),
    sqlc.arg(source_graph),
    sqlc.arg(request_digest),
    sqlc.narg(reason)::TEXT,
    sqlc.arg(result_document),
    sqlc.arg(executed_at),
    sqlc.arg(executed_at)
)
RETURNING command_id;

-- name: CompleteTournamentAdminWaveCAS :one
UPDATE waves
SET state = 'completed',
    revision_id = sqlc.arg(revision_id),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at),
    paused_at = NULL,
    closed_at = sqlc.arg(closed_at)
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND revision_id = sqlc.arg(expected_revision_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = 'active'
    AND sqlc.arg(revision_id) <> sqlc.arg(expected_revision_id)
RETURNING id,
    revision_id,
    revision,
    state;

-- name: StartTournamentAdminWaveSeries :many
UPDATE series
SET state = 'active',
    revision = revision + 1,
    started_at = COALESCE(started_at, sqlc.arg(started_at)),
    updated_at = sqlc.arg(started_at)
WHERE id IN (
        SELECT membership.series_id
        FROM wave_series AS membership
        WHERE membership.wave_id = sqlc.arg(wave_id)
    )
    AND state = 'ready'
RETURNING id;

-- name: StartTournamentAdminWaveGames :many
UPDATE game_attempts AS attempt
SET state = 'active',
    revision = attempt.revision + 1,
    started_at = COALESCE(attempt.started_at, sqlc.arg(started_at)),
    updated_at = sqlc.arg(started_at)
WHERE attempt.id IN (
        SELECT current_attempt.id
        FROM wave_series AS membership
        JOIN game_slots AS slot ON slot.series_id = membership.series_id
        JOIN LATERAL (
            SELECT game.id
            FROM game_attempts AS game
            WHERE game.slot_id = slot.id
            ORDER BY game.attempt_number DESC, game.id DESC
            LIMIT 1
        ) AS current_attempt ON TRUE
        WHERE membership.wave_id = sqlc.arg(wave_id)
    )
    AND attempt.state = 'ready'
RETURNING attempt.id;

-- name: LockWaveStartAuthority :one
SELECT tournament.state AS tournament_state,
    tournament.revision AS tournament_revision,
    roster.id AS roster_id,
    roster.revision AS roster_revision,
    projection.id AS projection_revision_id,
    projection.revision_number AS projection_revision,
    wave.id,
    wave.tournament_id,
    wave.revision_id,
    wave.revision,
    wave.state,
    wave.created_at,
    wave.updated_at,
    wave.started_at,
    wave.paused_at,
    wave.closed_at,
    ready_window.id AS ready_window_id,
    ready_window.revision_id AS ready_window_revision_id,
    ready_window.state AS ready_window_state,
    ready_window.opened_at,
    ready_window.deadline,
    ready_window.consumed_at,
    link.round_id AS swiss_round_id,
    link.bye_participant_id,
    standings.id AS artifact_revision_id,
    producer.revision_number AS artifact_revision
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
JOIN LATERAL (
    SELECT revision.id, revision.revision_number
    FROM projection_revisions AS revision
    WHERE revision.tournament_id = tournament.id
        AND revision.roster_id = roster.id
        AND revision.state = 'published'
    ORDER BY revision.revision_number DESC, revision.id DESC
    LIMIT 1
    FOR UPDATE
) AS projection ON TRUE
JOIN projection_revision_artifacts AS revision_artifact
    ON revision_artifact.revision_id = projection.id
    AND revision_artifact.artifact_kind = 'standings'
JOIN projection_artifacts AS standings
    ON standings.id = revision_artifact.artifact_id
    AND standings.tournament_id = tournament.id
    AND standings.roster_id = roster.id
JOIN projection_revisions AS producer ON producer.id = standings.produced_by_revision_id
JOIN waves AS wave
    ON wave.tournament_id = tournament.id
    AND wave.roster_id = roster.id
JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id
LEFT JOIN swiss_wave_links AS link ON link.wave_id = wave.id
WHERE tournament.id = sqlc.arg(tournament_id)
    AND wave.id = sqlc.arg(wave_id)
FOR UPDATE OF tournament, roster, revision_artifact, standings, producer, wave, ready_window;

-- The pairing command creates this lineage without locking the round. Wave
-- start or a pre-start terminal action captures all immutable source heads,
-- persists the proof, then CAS-locks the round in the same transaction.
-- name: LockWaveStartSwissRoundProof :one
SELECT tournament.preset,
    swiss_round.id AS round_id,
    swiss_round.round_number,
    swiss_round.revision AS round_revision,
    swiss_round.source_history_revision AS history_revision,
    roster_lock.preflight_revision_id,
    content.normal_pool_revision_id,
    content.normal_pool_revision
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
JOIN waves AS wave
    ON wave.tournament_id = tournament.id
    AND wave.roster_id = roster.id
JOIN swiss_wave_links AS swiss_link
    ON swiss_link.wave_id = wave.id
    AND swiss_link.tournament_id = tournament.id
    AND swiss_link.roster_id = roster.id
JOIN swiss_rounds AS swiss_round
    ON swiss_round.id = swiss_link.round_id
    AND swiss_round.roster_id = roster.id
JOIN LATERAL (
    SELECT operation.preflight_revision_id
    FROM tournament_roster_operations AS operation
    WHERE operation.tournament_id = tournament.id
        AND operation.roster_id = roster.id
        AND operation.action = 'lock'
        AND operation.resulting_roster_revision = roster.revision
        AND operation.preflight_revision_id IS NOT NULL
    ORDER BY operation.created_at DESC, operation.command_id DESC
    LIMIT 1
    FOR UPDATE
) AS roster_lock ON TRUE
JOIN LATERAL (
    SELECT configuration.normal_pool_revision_id,
        normal_pool.revision AS normal_pool_revision
    FROM tournament_content_configurations AS configuration
    JOIN task_pool_revisions AS normal_pool
        ON normal_pool.id = configuration.normal_pool_revision_id
        AND normal_pool.kind = 'normal'
    WHERE configuration.tournament_id = tournament.id
        AND configuration.state = 'published'
    ORDER BY configuration.revision DESC, configuration.id DESC
    LIMIT 1
    FOR UPDATE OF configuration, normal_pool
) AS content ON TRUE
WHERE tournament.id = sqlc.arg(tournament_id)
    AND roster.id = sqlc.arg(roster_id)
    AND wave.id = sqlc.arg(wave_id)
    AND swiss_round.id = sqlc.arg(round_id)
FOR UPDATE OF tournament, roster, wave, swiss_link, swiss_round;

-- name: LockPreStartSwissSeriesWave :many
SELECT wave.id AS wave_id, wave.roster_id
FROM wave_series AS membership
JOIN waves AS wave ON wave.id = membership.wave_id
    AND wave.tournament_id = membership.tournament_id AND wave.roster_id = membership.roster_id
JOIN swiss_wave_links AS link ON link.wave_id = wave.id
    AND link.tournament_id = wave.tournament_id AND link.roster_id = wave.roster_id
WHERE membership.tournament_id = sqlc.arg(tournament_id)
    AND membership.series_id = sqlc.arg(series_id)
    AND wave.started_at IS NULL
ORDER BY wave.id
FOR UPDATE OF wave, membership, link;

-- name: GetSwissRoundProofForUpdate :one
SELECT round_id, tournament_id, roster_id, preset, round_number,
    source_projection_revision_id, preflight_revision_id, normal_pool_revision_id,
    wave_id, wave_revision_id, round_revision, source_projection_revision,
    roster_revision, history_revision, normal_pool_revision, wave_revision,
    bye_participant_id, proof_hash, proof_mode, terminal_command_id, locked_at, created_at
FROM swiss_round_lock_proofs
WHERE round_id = sqlc.arg(round_id)
    AND tournament_id = sqlc.arg(tournament_id) AND roster_id = sqlc.arg(roster_id)
FOR UPDATE;

-- name: ListSwissRoundProofMembers :many
SELECT round_id, roster_id, participant_id, created_at
FROM swiss_round_lock_proof_members
WHERE round_id = sqlc.arg(round_id) AND roster_id = sqlc.arg(roster_id)
ORDER BY participant_id
FOR KEY SHARE;

-- name: ListSwissRoundProofSeries :many
SELECT round_id, roster_id, pairing_id, series_id, first_participant_id,
    second_participant_id, category_revision_id, category_revision, assignment_id,
    assignment_revision, assignment_plan_id, assignment_plan_revision_id,
    reservation_id, reservation_revision, created_at
FROM swiss_round_lock_proof_series
WHERE round_id = sqlc.arg(round_id) AND roster_id = sqlc.arg(roster_id)
ORDER BY pairing_id
FOR KEY SHARE;

-- Lock every expected Series before resolving the per-Series BO1 binding.
-- The caller rejects absent or duplicate game bindings instead of silently
-- reducing the proof to the rows an inner join happened to return.
-- name: LockWaveStartSeriesMemberships :many
SELECT membership.wave_id,
    membership.tournament_id,
    membership.roster_id,
    membership.series_id
FROM wave_series AS membership
JOIN waves AS wave
    ON wave.id = membership.wave_id
    AND wave.tournament_id = membership.tournament_id
    AND wave.roster_id = membership.roster_id
WHERE membership.wave_id = sqlc.arg(wave_id)
ORDER BY membership.series_id
FOR UPDATE OF membership, wave;

-- name: LockWaveStartReadiness :many
SELECT member.participant_id,
    readiness.ready,
    readiness.revision AS readiness_revision,
    readiness.ready_at
FROM wave_members AS member
JOIN wave_readiness AS readiness
    ON readiness.wave_id = member.wave_id
    AND readiness.roster_id = member.roster_id
    AND readiness.participant_id = member.participant_id
WHERE member.wave_id = sqlc.arg(wave_id)
    AND readiness.ready_window_id = sqlc.arg(ready_window_id)
ORDER BY member.participant_id
FOR UPDATE OF member, readiness;

-- name: LockWaveStartGames :many
SELECT series.id AS series_id,
    series.tournament_id,
    series.roster_id,
    series.first_participant_id,
    series.second_participant_id,
    series.format AS series_format,
    series.state AS series_state,
    series.first_participant_wins,
    series.second_participant_wins,
    series.revision AS series_revision,
    swiss_pairing.id AS pairing_id,
    game_slot.id AS slot_id,
    game_slot.slot_number,
    game_slot.category,
    game_slot.first_participant_wins_before,
    game_slot.second_participant_wins_before,
    category_revision.id AS category_revision_id,
    category_revision.revision AS category_revision,
    attempt.id AS game_id,
    attempt.attempt_number,
    attempt.state AS game_state,
    attempt.revision AS game_revision,
    assignment.id AS assignment_id,
    assignment.revision AS assignment_revision,
    plan.id AS assignment_plan_id,
    plan.revision_id AS plan_revision_id,
    reservation.id AS reservation_id,
    reservation.revision AS reservation_revision,
    reservation.state AS reservation_state,
    reservation.disclosed_at,
    snapshot.id AS snapshot_id,
    snapshot.task_id,
    snapshot.task_version,
    snapshot.content_digest,
    snapshot.time_limit
FROM wave_series AS membership
JOIN series ON series.id = membership.series_id
JOIN game_slots AS game_slot
    ON game_slot.series_id = series.id
    AND game_slot.roster_id = series.roster_id
JOIN LATERAL (
    SELECT game.id,
        game.attempt_number,
        game.state,
        game.revision
    FROM game_attempts AS game
    WHERE game.slot_id = game_slot.id
        AND game.series_id = series.id
        AND game.roster_id = series.roster_id
    ORDER BY game.attempt_number DESC, game.id DESC
    LIMIT 1
    FOR UPDATE
) AS attempt ON TRUE
JOIN assignments AS assignment
    ON assignment.attempt_id = attempt.id
    AND assignment.series_id = series.id
    AND assignment.roster_id = series.roster_id
    AND assignment.state = 'active'
JOIN assignment_plans AS plan
    ON plan.id = assignment.plan_id
    AND plan.roster_id = series.roster_id
    AND plan.state = 'committed'
    AND plan.active_branch_id = assignment.branch_id
JOIN LATERAL (
    SELECT revision.id,
        revision.revision
    FROM category_revisions AS revision
    WHERE revision.series_id = series.id
        AND revision.roster_id = series.roster_id
        AND revision.selected_categories @> jsonb_build_array(game_slot.category)
    ORDER BY revision.revision DESC, revision.id DESC
    LIMIT 1
    FOR UPDATE
) AS category_revision ON TRUE
JOIN task_version_reservations AS reservation
    ON reservation.id = assignment.reservation_id
    AND reservation.plan_id = assignment.plan_id
    AND reservation.branch_id = assignment.branch_id
    AND reservation.state = 'committed'
JOIN task_snapshots AS snapshot
    ON snapshot.id = assignment.snapshot_id
    AND snapshot.reservation_id = reservation.id
    AND snapshot.task_id = assignment.task_id
    AND snapshot.task_version = assignment.task_version
LEFT JOIN LATERAL (
    SELECT pairing.id
    FROM swiss_wave_links AS swiss_link
    JOIN swiss_pairings AS pairing
        ON pairing.round_id = swiss_link.round_id
        AND pairing.roster_id = series.roster_id
    JOIN swiss_pairing_members AS first_member
        ON first_member.pairing_id = pairing.id
        AND first_member.round_id = pairing.round_id
        AND first_member.roster_id = pairing.roster_id
        AND first_member.seat = 1
        AND first_member.participant_id = series.first_participant_id
    JOIN swiss_pairing_members AS second_member
        ON second_member.pairing_id = pairing.id
        AND second_member.round_id = pairing.round_id
        AND second_member.roster_id = pairing.roster_id
        AND second_member.seat = 2
        AND second_member.participant_id = series.second_participant_id
    WHERE swiss_link.wave_id = membership.wave_id
        AND swiss_link.tournament_id = membership.tournament_id
        AND swiss_link.roster_id = membership.roster_id
    FOR UPDATE OF pairing, first_member, second_member
) AS swiss_pairing ON TRUE
WHERE membership.wave_id = sqlc.arg(wave_id)
ORDER BY series.id, game_slot.slot_number, attempt.id
FOR UPDATE OF membership, series, game_slot, assignment, plan, reservation, snapshot;

-- name: FindWaveStartCommandByID :one
SELECT command_id,
    tournament_id,
    roster_id,
    wave_id,
    actor_id,
    action,
    source_projection_revision_id,
    source_projection_revision,
    source_tournament_revision,
    source_roster_revision,
    source_wave_revision,
    resulting_wave_revision,
    source_revisions,
    source_graph,
    request_digest,
    reason,
    result_document,
    executed_at,
    created_at
FROM wave_control_commands
WHERE command_id = sqlc.arg(command_id)
    AND action = 'start';

-- name: FindLatestWaveStartCommand :one
SELECT command_id,
    tournament_id,
    roster_id,
    wave_id,
    actor_id,
    action,
    source_projection_revision_id,
    source_projection_revision,
    source_tournament_revision,
    source_roster_revision,
    source_wave_revision,
    resulting_wave_revision,
    source_revisions,
    source_graph,
    request_digest,
    reason,
    result_document,
    executed_at,
    created_at
FROM wave_control_commands
WHERE tournament_id = sqlc.arg(tournament_id)
    AND wave_id = sqlc.arg(wave_id)
    AND action = 'start'
ORDER BY created_at DESC, command_id DESC
LIMIT 1;

-- name: StartWaveSeriesCAS :one
UPDATE series
SET state = 'active',
    revision = revision + 1,
    started_at = sqlc.arg(started_at),
    updated_at = sqlc.arg(started_at)
WHERE id = sqlc.arg(id)
    AND roster_id = sqlc.arg(roster_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = 'ready'
    AND started_at IS NULL
RETURNING id,
    revision,
    state,
    started_at;

-- name: StartWaveGameCAS :one
UPDATE game_attempts
SET state = 'active',
    revision = revision + 1,
    started_at = sqlc.arg(started_at),
    updated_at = sqlc.arg(started_at)
WHERE id = sqlc.arg(id)
    AND series_id = sqlc.arg(series_id)
    AND roster_id = sqlc.arg(roster_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state)
    AND state IN ('planned', 'ready')
    AND started_at IS NULL
RETURNING id,
    revision,
    state,
    started_at;

-- BindExecutionGameEpoch fences WaveStart to the exact service-owned
-- authority identity supplied before the roster transaction. The latest lease
-- must still be that identity and live according to PostgreSQL time.
-- name: BindExecutionGameEpoch :one
INSERT INTO execution_game_epochs (
    game_attempt_id,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    slot_id,
    authority_holder_id,
    authority_lease_id,
    authority_epoch,
    authority_revision,
    bound_at,
    created_at
)
SELECT attempt.id,
    current_authority.tournament_id,
    attempt.roster_id,
    sqlc.arg(wave_id),
    attempt.series_id,
    attempt.slot_id,
    current_authority.holder_id,
    current_authority.lease_id,
    current_authority.epoch,
    current_authority.revision,
    sqlc.arg(bound_at),
    sqlc.arg(bound_at)
FROM game_attempts AS attempt
JOIN series
    ON series.id = attempt.series_id
    AND series.roster_id = attempt.roster_id
CROSS JOIN execution_authority_leases AS current_authority
WHERE attempt.id = sqlc.arg(game_attempt_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND attempt.roster_id = sqlc.arg(roster_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND current_authority.tournament_id = sqlc.arg(tournament_id)
    AND current_authority.revision = (
        SELECT MAX(latest.revision)
        FROM execution_authority_leases AS latest
        WHERE latest.tournament_id = sqlc.arg(tournament_id)
    )
    AND attempt.state = 'active'
    AND attempt.started_at = sqlc.arg(bound_at)::TIMESTAMPTZ
    AND current_authority.holder_id = sqlc.arg(authority_holder_id)
    AND current_authority.lease_id = sqlc.arg(authority_lease_id)
    AND current_authority.epoch = sqlc.arg(authority_epoch)
    AND clock_timestamp() >= current_authority.renewed_at
    AND clock_timestamp() < current_authority.expires_at
RETURNING game_attempt_id,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    slot_id,
    authority_holder_id,
    authority_lease_id,
    authority_epoch,
    authority_revision,
    bound_at,
    created_at;

-- name: DiscloseWaveStartReservationCAS :one
UPDATE task_version_reservations
SET disclosed_at = sqlc.arg(disclosed_at),
    revision = revision + 1
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND state = 'committed'
    AND disclosed_at IS NULL
RETURNING id,
    revision,
    disclosed_at;

-- name: PauseTournamentAdminWaveSeries :many
UPDATE series
SET state = 'technical_pause',
    revision = revision + 1,
    updated_at = sqlc.arg(paused_at)
WHERE id IN (
        SELECT membership.series_id
        FROM wave_series AS membership
        WHERE membership.wave_id = sqlc.arg(wave_id)
    )
    AND state = 'active'
RETURNING id;

-- name: PauseTournamentAdminWaveGames :many
UPDATE game_attempts AS attempt
SET state = 'paused',
    revision = attempt.revision + 1,
    updated_at = sqlc.arg(paused_at)
WHERE attempt.id IN (
        SELECT current_attempt.id
        FROM wave_series AS membership
        JOIN game_slots AS slot ON slot.series_id = membership.series_id
        JOIN LATERAL (
            SELECT game.id
            FROM game_attempts AS game
            WHERE game.slot_id = slot.id
            ORDER BY game.attempt_number DESC, game.id DESC
            LIMIT 1
        ) AS current_attempt ON TRUE
        WHERE membership.wave_id = sqlc.arg(wave_id)
    )
    AND attempt.state = 'active'
RETURNING attempt.id;

-- name: ResumeTournamentAdminWaveSeries :many
UPDATE series
SET state = 'active',
    revision = revision + 1,
    updated_at = sqlc.arg(resumed_at)
WHERE id IN (
        SELECT membership.series_id
        FROM wave_series AS membership
        WHERE membership.wave_id = sqlc.arg(wave_id)
    )
    AND state = 'technical_pause'
RETURNING id;

-- RebindPausedExecutionGameEpochs appends successor authority evidence before
-- any paused Game becomes active. The supplied service-owned identity must be
-- the exact latest live PostgreSQL lease; prior epoch rows are never updated.
-- name: RebindPausedExecutionGameEpochs :many
WITH current_authority AS (
    SELECT lease.tournament_id,
        lease.holder_id,
        lease.lease_id,
        lease.epoch,
        lease.revision,
        lease.renewed_at,
        lease.expires_at
    FROM execution_authority_leases AS lease
    WHERE lease.tournament_id = sqlc.arg(tournament_id)
        AND lease.holder_id = sqlc.arg(authority_holder_id)
        AND lease.lease_id = sqlc.arg(authority_lease_id)
        AND lease.epoch = sqlc.arg(authority_epoch)
        AND lease.revision = (
            SELECT MAX(latest.revision)
            FROM execution_authority_leases AS latest
            WHERE latest.tournament_id = sqlc.arg(tournament_id)
        )
        AND clock_timestamp() >= lease.renewed_at
        AND clock_timestamp() < lease.expires_at
    ORDER BY lease.revision DESC
    LIMIT 1
    FOR UPDATE
), paused_games AS (
    SELECT attempt.id AS game_attempt_id,
        epoch.tournament_id,
        epoch.roster_id,
        epoch.wave_id,
        epoch.series_id,
        epoch.slot_id,
        COALESCE(rebind.authority_holder_id, epoch.authority_holder_id) AS previous_authority_holder_id,
        COALESCE(rebind.authority_lease_id, epoch.authority_lease_id) AS previous_authority_lease_id,
        COALESCE(rebind.authority_epoch, epoch.authority_epoch) AS previous_authority_epoch,
        COALESCE(rebind.authority_revision, epoch.authority_revision) AS previous_authority_revision,
        COALESCE(rebind.rebind_sequence, 0) AS previous_rebind_sequence
    FROM waves AS wave
    JOIN tournaments AS tournament ON tournament.id = wave.tournament_id
    JOIN wave_series AS membership
        ON membership.wave_id = wave.id
        AND membership.tournament_id = wave.tournament_id
        AND membership.roster_id = wave.roster_id
    JOIN series
        ON series.id = membership.series_id
        AND series.tournament_id = wave.tournament_id
        AND series.roster_id = wave.roster_id
    JOIN game_slots AS slot
        ON slot.series_id = series.id
        AND slot.roster_id = series.roster_id
    JOIN LATERAL (
        SELECT game.id
        FROM game_attempts AS game
        WHERE game.slot_id = slot.id
            AND game.series_id = series.id
            AND game.roster_id = series.roster_id
        ORDER BY game.attempt_number DESC, game.id DESC
        LIMIT 1
    ) AS current_attempt ON TRUE
    JOIN game_attempts AS attempt
        ON attempt.id = current_attempt.id
        AND attempt.series_id = series.id
        AND attempt.roster_id = series.roster_id
    JOIN execution_game_epochs AS epoch
        ON epoch.game_attempt_id = attempt.id
        AND epoch.tournament_id = wave.tournament_id
        AND epoch.roster_id = wave.roster_id
        AND epoch.wave_id = wave.id
        AND epoch.series_id = series.id
        AND epoch.slot_id = slot.id
    LEFT JOIN LATERAL (
        SELECT successor.authority_holder_id,
            successor.authority_lease_id,
            successor.authority_epoch,
            successor.authority_revision,
            successor.rebind_sequence
        FROM execution_game_epoch_rebinds AS successor
        WHERE successor.game_attempt_id = attempt.id
        ORDER BY successor.rebind_sequence DESC
        LIMIT 1
    ) AS rebind ON TRUE
    WHERE wave.id = sqlc.arg(wave_id)
        AND wave.tournament_id = sqlc.arg(tournament_id)
        AND wave.state = 'paused'
        AND tournament.state = 'swiss'
        AND series.state = 'technical_pause'
        AND attempt.state = 'paused'
    FOR UPDATE OF attempt, epoch
), inserted AS (
    INSERT INTO execution_game_epoch_rebinds (
        game_attempt_id,
        rebind_sequence,
        command_id,
        tournament_id,
        roster_id,
        wave_id,
        series_id,
        slot_id,
        previous_authority_holder_id,
        previous_authority_lease_id,
        previous_authority_epoch,
        previous_authority_revision,
        authority_holder_id,
        authority_lease_id,
        authority_epoch,
        authority_revision,
        rebound_at,
        created_at
    )
    SELECT paused.game_attempt_id,
        paused.previous_rebind_sequence + 1,
        sqlc.arg(command_id),
        paused.tournament_id,
        paused.roster_id,
        paused.wave_id,
        paused.series_id,
        paused.slot_id,
        paused.previous_authority_holder_id,
        paused.previous_authority_lease_id,
        paused.previous_authority_epoch,
        paused.previous_authority_revision,
        current_authority.holder_id,
        current_authority.lease_id,
        current_authority.epoch,
        current_authority.revision,
        sqlc.arg(rebound_at),
        sqlc.arg(rebound_at)
    FROM paused_games AS paused
    JOIN current_authority ON current_authority.tournament_id = paused.tournament_id
    RETURNING game_attempt_id
)
SELECT game_attempt_id
FROM inserted
ORDER BY game_attempt_id;

-- name: ResumeTournamentAdminWaveGames :many
UPDATE game_attempts AS attempt
SET state = 'active',
    revision = attempt.revision + 1,
    updated_at = sqlc.arg(resumed_at)
WHERE attempt.id IN (
        SELECT current_attempt.id
        FROM wave_series AS membership
        JOIN game_slots AS slot ON slot.series_id = membership.series_id
        JOIN LATERAL (
            SELECT game.id
            FROM game_attempts AS game
            WHERE game.slot_id = slot.id
            ORDER BY game.attempt_number DESC, game.id DESC
            LIMIT 1
        ) AS current_attempt ON TRUE
        WHERE membership.wave_id = sqlc.arg(wave_id)
    )
    AND attempt.state = 'paused'
RETURNING attempt.id;
