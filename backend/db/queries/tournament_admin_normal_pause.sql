-- name: LockTournamentAdminNormalPausePresence :many
SELECT presence.id, presence.tournament_id, presence.roster_id, presence.series_id,
    presence.participant_id, presence.state, presence.presence_epoch, presence.revision,
    presence.connected_at, presence.disconnected_at, presence.updated_at
FROM presence_states AS presence
JOIN wave_series AS membership ON membership.series_id = presence.series_id
WHERE membership.wave_id = sqlc.arg(wave_id)
ORDER BY presence.series_id, presence.participant_id
FOR UPDATE OF presence;

-- name: LockTournamentAdminNormalPauseRows :many
SELECT pause.id, pause.tournament_id, pause.roster_id, pause.scope_kind, pause.scope_id,
    pause.wave_id, pause.series_id, pause.game_attempt_id, pause.parent_pause_id,
    pause.depth, pause.reason, pause.paused_from_state, pause.state,
    pause.current_revision_id, pause.revision, pause.started_at, pause.resolved_at,
    pause.created_at, pause.updated_at
FROM pauses AS pause
WHERE pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND (
        pause.wave_id = sqlc.arg(wave_id)
        OR pause.series_id IN (SELECT series_id FROM wave_series WHERE wave_id = sqlc.arg(wave_id))
    )
ORDER BY pause.depth, pause.started_at, pause.id
FOR UPDATE OF pause;

-- name: LockTournamentAdminNormalPauseReconnect :many
SELECT reconnect.id, reconnect.pause_id, reconnect.roster_id, reconnect.series_id,
    reconnect.game_attempt_id, reconnect.participant_id, reconnect.presence_epoch,
    reconnect.interval_number, reconnect.state, reconnect.opened_at,
    reconnect.deadline_at, reconnect.closed_at, reconnect.revision,
    reconnect.created_at, reconnect.updated_at, reconnect.continuation_number,
    reconnect.continued_from_id, reconnect.suspended_by_pause_id
FROM reconnect_intervals AS reconnect
JOIN wave_series AS membership ON membership.series_id = reconnect.series_id
WHERE membership.wave_id = sqlc.arg(wave_id)
ORDER BY reconnect.series_id, reconnect.participant_id, reconnect.interval_number,
    reconnect.continuation_number, reconnect.id
FOR UPDATE OF reconnect;

-- name: LockTournamentAdminNormalPauseCounters :many
SELECT counter.pause_id, counter.roster_id, counter.participant_id,
    counter.slot_limit, counter.slots_used, counter.revision,
    counter.created_at, counter.updated_at
FROM reconnect_slot_counters AS counter
JOIN pauses AS pause ON pause.id = counter.pause_id
JOIN wave_series AS membership ON membership.series_id = pause.series_id
WHERE membership.wave_id = sqlc.arg(wave_id)
ORDER BY counter.pause_id, counter.participant_id
FOR UPDATE OF counter;

-- name: LockTournamentAdminNormalPauseClocks :many
SELECT clock.pause_id, clock.game_attempt_id, clock.original_deadline,
    clock.frozen_at, clock.frozen_remaining_ms, clock.resumed_at,
    clock.resumed_deadline, clock.revision, clock.created_at, clock.updated_at
FROM pause_clocks AS clock
JOIN pauses AS pause ON pause.id = clock.pause_id
JOIN wave_series AS membership ON membership.series_id = pause.series_id
WHERE membership.wave_id = sqlc.arg(wave_id)
ORDER BY clock.game_attempt_id, clock.pause_id
FOR UPDATE OF clock;

-- name: LockTournamentAdminNormalPauseReadyWindowClocks :many
SELECT clock.pause_id, clock.ready_window_id, clock.wave_id, clock.roster_id,
    clock.original_deadline, clock.frozen_at, clock.frozen_remaining,
    clock.resumed_at, clock.resumed_deadline, clock.revision,
    clock.created_at, clock.updated_at
FROM ready_window_pause_clocks AS clock
WHERE clock.wave_id = sqlc.arg(wave_id)
ORDER BY clock.frozen_at, clock.pause_id
FOR UPDATE OF clock;

-- name: ListTournamentAdminNormalPauseGameDeadlines :many
SELECT attempt.id AS game_attempt_id,
    COALESCE(latest_resume.resumed_deadline,
        attempt.started_at + INTERVAL '180 seconds')::TIMESTAMPTZ AS deadline
FROM wave_series AS membership
JOIN series ON series.id = membership.series_id
JOIN game_slots AS slot ON slot.series_id = series.id
JOIN LATERAL (
    SELECT game.id, game.started_at
    FROM game_attempts AS game
    WHERE game.slot_id = slot.id
    ORDER BY game.attempt_number DESC, game.id DESC
    LIMIT 1
) AS attempt ON TRUE
JOIN assignments AS assignment ON assignment.attempt_id = attempt.id AND assignment.state = 'active'
JOIN task_snapshots AS snapshot ON snapshot.id = assignment.snapshot_id
LEFT JOIN LATERAL (
    SELECT clock.resumed_at + (clock.original_deadline - clock.frozen_at) AS resumed_deadline
    FROM pause_clocks AS clock
    JOIN pauses AS pause ON pause.id = clock.pause_id AND pause.state = 'resumed'
    WHERE clock.game_attempt_id = attempt.id
        AND clock.resumed_at IS NOT NULL
        AND clock.resumed_deadline IS NOT NULL
    ORDER BY clock.resumed_at DESC, clock.pause_id DESC
    LIMIT 1
) AS latest_resume ON TRUE
WHERE membership.wave_id = sqlc.arg(wave_id)
ORDER BY attempt.id;

-- name: LockTournamentAdminNormalPauseActiveDraftIDs :many
SELECT draft.id
FROM drafts AS draft
JOIN series
    ON series.id = draft.series_id
    AND series.roster_id = draft.roster_id
JOIN LATERAL (
    SELECT revision.state
    FROM draft_revisions AS revision
    WHERE revision.draft_id = draft.id
    ORDER BY revision.revision DESC, revision.id DESC
    LIMIT 1
    FOR UPDATE
) AS current_revision ON TRUE
JOIN wave_series AS membership ON membership.series_id = series.id
WHERE membership.wave_id = sqlc.arg(wave_id)
    AND current_revision.state IN ('active', 'paused', 'recovery_required')
ORDER BY draft.created_at, draft.id;

-- name: CreateTournamentAdminNormalPause :one
INSERT INTO pauses (id, tournament_id, roster_id, scope_kind, scope_id, wave_id,
    series_id, game_attempt_id, parent_pause_id, depth, reason, paused_from_state,
    state, current_revision_id, revision, started_at, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(tournament_id), sqlc.arg(roster_id), sqlc.arg(scope_kind),
    sqlc.arg(scope_id), sqlc.narg(wave_id), sqlc.narg(series_id),
    sqlc.narg(game_attempt_id), sqlc.narg(parent_pause_id), sqlc.arg(depth),
    sqlc.arg(reason), sqlc.arg(paused_from_state), 'active',
    sqlc.arg(current_revision_id), 1, sqlc.arg(started_at), sqlc.arg(started_at),
    sqlc.arg(started_at))
RETURNING id;

-- name: CreateTournamentAdminNormalPauseRevision :one
INSERT INTO pause_revisions (id, pause_id, previous_revision_id, revision_number,
    state, transition_reason, created_at)
VALUES (sqlc.arg(id), sqlc.arg(pause_id), sqlc.narg(previous_revision_id),
    sqlc.arg(revision_number), sqlc.arg(state), sqlc.narg(transition_reason),
    sqlc.arg(created_at))
RETURNING id;

-- name: CreateTournamentAdminNormalPausePresenceSnapshot :one
INSERT INTO pause_presence_snapshots (pause_id, roster_id, series_id, participant_id,
    presence_state, presence_epoch, presence_revision, captured_at, created_at)
VALUES (sqlc.arg(pause_id), sqlc.arg(roster_id), sqlc.arg(series_id),
    sqlc.arg(participant_id), sqlc.arg(presence_state), sqlc.arg(presence_epoch),
    sqlc.arg(presence_revision), sqlc.arg(captured_at), sqlc.arg(captured_at))
RETURNING participant_id;

-- name: SuspendTournamentAdminNormalPauseReconnectCAS :one
UPDATE reconnect_intervals
SET state = 'cancelled', closed_at = sqlc.arg(paused_at), revision = revision + 1,
    updated_at = sqlc.arg(paused_at), suspended_by_pause_id = sqlc.arg(normal_pause_id)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision)
    AND state = 'open' AND opened_at < sqlc.arg(paused_at)
    AND deadline_at > sqlc.arg(paused_at) AND suspended_by_pause_id IS NULL
RETURNING id;

-- name: PauseTournamentAdminNormalTournamentCAS :one
UPDATE tournaments
SET state = 'technical_pause', paused_from_state = sqlc.arg(expected_state),
    revision = revision + 1, updated_at = sqlc.arg(paused_at)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state) AND paused_from_state IS NULL
RETURNING id;

-- name: PauseTournamentAdminNormalWaveCAS :one
UPDATE waves
SET state = sqlc.arg(next_state), revision = revision + 1,
    paused_at = sqlc.narg(paused_at),
    updated_at = COALESCE(sqlc.narg(paused_at)::TIMESTAMPTZ, transaction_timestamp())
WHERE id = sqlc.arg(id) AND tournament_id = sqlc.arg(tournament_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state) AND paused_at IS NULL
    AND (
        (sqlc.arg(expected_state) = 'active' AND sqlc.arg(next_state) = 'paused'
            AND sqlc.narg(paused_at)::TIMESTAMPTZ IS NOT NULL)
        OR (sqlc.arg(expected_state) IN ('ready_window_open', 'ready')
            AND sqlc.arg(next_state) = sqlc.arg(expected_state)
            AND sqlc.narg(paused_at)::TIMESTAMPTZ IS NULL)
    )
RETURNING id;

-- name: PauseTournamentAdminNormalSeriesCAS :one
UPDATE series
SET state = 'technical_pause', revision = revision + 1, updated_at = sqlc.arg(paused_at)
WHERE id = sqlc.arg(id) AND tournament_id = sqlc.arg(tournament_id)
    AND revision = sqlc.arg(expected_revision) AND state = sqlc.arg(expected_state)
    AND sqlc.arg(expected_state) IN ('draft', 'ready', 'active', 'replay_required')
RETURNING id;

-- name: PauseTournamentAdminNormalGameCAS :one
UPDATE game_attempts
SET state = 'paused', revision = revision + 1, updated_at = sqlc.arg(paused_at)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision) AND state = 'active'
RETURNING id;

-- name: CreateTournamentAdminNormalPauseClock :one
INSERT INTO pause_clocks (pause_id, game_attempt_id, original_deadline, frozen_at,
    frozen_remaining_ms, revision, created_at, updated_at)
VALUES (sqlc.arg(pause_id), sqlc.arg(game_attempt_id), sqlc.arg(original_deadline),
    sqlc.arg(frozen_at), sqlc.arg(frozen_remaining_ms), 1,
    sqlc.arg(frozen_at), sqlc.arg(frozen_at))
RETURNING pause_id;

-- name: CreateTournamentAdminNormalPauseReadyWindowClock :one
INSERT INTO ready_window_pause_clocks (pause_id, ready_window_id, wave_id,
    roster_id, original_deadline, frozen_at, frozen_remaining, revision,
    created_at, updated_at)
VALUES (sqlc.arg(pause_id), sqlc.arg(ready_window_id), sqlc.arg(wave_id),
    sqlc.arg(roster_id), sqlc.arg(original_deadline), sqlc.arg(frozen_at),
    sqlc.arg(original_deadline)::TIMESTAMPTZ - sqlc.arg(frozen_at)::TIMESTAMPTZ,
    1, sqlc.arg(frozen_at), sqlc.arg(frozen_at))
RETURNING pause_id;

-- name: CreateTournamentAdminNormalPauseCounter :one
INSERT INTO reconnect_slot_counters (pause_id, roster_id, participant_id,
    slot_limit, slots_used, revision, created_at, updated_at)
VALUES (sqlc.arg(pause_id), sqlc.arg(roster_id), sqlc.arg(participant_id),
    sqlc.arg(slot_limit), sqlc.arg(slots_used), sqlc.arg(revision),
    sqlc.arg(created_at), sqlc.arg(created_at))
RETURNING participant_id;

-- name: GetTournamentAdminNormalPauseReceipt :one
SELECT command.result_document
FROM wave_control_commands AS command
JOIN pauses AS pause ON pause.wave_id = command.wave_id
    AND pause.scope_kind = 'wave' AND pause.parent_pause_id IS NULL
    AND pause.started_at = command.executed_at
WHERE command.tournament_id = sqlc.arg(tournament_id)
    AND command.wave_id = sqlc.arg(wave_id) AND command.action = 'pause'
    AND pause.id = sqlc.arg(pause_id)
ORDER BY command.executed_at DESC, command.command_id DESC
LIMIT 1;

-- name: GetTournamentAdminActiveNormalPause :one
SELECT id
FROM pauses
WHERE tournament_id = sqlc.arg(tournament_id) AND roster_id = sqlc.arg(roster_id)
    AND wave_id = sqlc.arg(wave_id) AND scope_kind = 'wave'
    AND parent_pause_id IS NULL AND state = 'active'
ORDER BY started_at DESC, id DESC
LIMIT 1
FOR UPDATE;

-- name: CreateTournamentAdminNormalResumeDecision :one
INSERT INTO resume_decisions (id, pause_id, decision_number, first_participant_id,
    second_participant_id, first_pre_pause_state, second_pre_pause_state,
    first_live_state, second_live_state, first_presence_epoch, second_presence_epoch,
    first_presence_revision, second_presence_revision, first_reconnect_interval_id,
    second_reconnect_interval_id, action, decided_at, created_at)
VALUES (sqlc.arg(id), sqlc.arg(pause_id), sqlc.arg(decision_number),
    sqlc.arg(first_participant_id), sqlc.arg(second_participant_id),
    sqlc.arg(first_pre_pause_state), sqlc.arg(second_pre_pause_state),
    sqlc.arg(first_live_state), sqlc.arg(second_live_state),
    sqlc.arg(first_presence_epoch), sqlc.arg(second_presence_epoch),
    sqlc.arg(first_presence_revision), sqlc.arg(second_presence_revision),
    sqlc.narg(first_reconnect_interval_id), sqlc.narg(second_reconnect_interval_id),
    sqlc.arg(action), sqlc.arg(decided_at), sqlc.arg(decided_at))
RETURNING id;

-- name: GetTournamentAdminNormalPauseDecisionNumber :one
SELECT COALESCE(MAX(decision_number), 0)::BIGINT
FROM resume_decisions
WHERE pause_id = sqlc.arg(pause_id);

-- name: CreateTournamentAdminNormalPauseReconnectInterval :one
INSERT INTO reconnect_intervals (id, pause_id, roster_id, series_id,
    game_attempt_id, participant_id, presence_epoch, interval_number, state,
    opened_at, deadline_at, revision, created_at, updated_at,
    continuation_number, continued_from_id)
VALUES (sqlc.arg(id), sqlc.arg(pause_id), sqlc.arg(roster_id),
    sqlc.arg(series_id), sqlc.arg(game_attempt_id), sqlc.arg(participant_id),
    sqlc.arg(presence_epoch), sqlc.arg(interval_number), 'open',
    sqlc.arg(opened_at), sqlc.arg(deadline_at), 1,
    sqlc.arg(opened_at), sqlc.arg(opened_at), sqlc.arg(continuation_number),
    sqlc.narg(continued_from_id))
RETURNING id;

-- name: AdvanceTournamentAdminNormalPauseCounterCAS :one
UPDATE reconnect_slot_counters
SET slots_used = slots_used + 1, revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE pause_id = sqlc.arg(pause_id)
    AND participant_id = sqlc.arg(participant_id)
    AND revision = sqlc.arg(expected_revision)
    AND slots_used = sqlc.arg(expected_slots_used)
    AND slots_used < slot_limit
RETURNING participant_id;

-- name: ResumeTournamentAdminNormalPauseClockCAS :one
UPDATE pause_clocks
SET resumed_at = sqlc.arg(resumed_at),
    resumed_deadline = sqlc.arg(resumed_deadline), revision = revision + 1,
    updated_at = sqlc.arg(resumed_at)
WHERE pause_id = sqlc.arg(pause_id) AND revision = sqlc.arg(expected_revision)
    AND resumed_at IS NULL AND resumed_deadline IS NULL
RETURNING pause_id;

-- name: ResumeTournamentAdminNormalPauseReadyWindowClockCAS :one
UPDATE ready_window_pause_clocks
SET resumed_at = sqlc.arg(resumed_at),
    resumed_deadline = sqlc.arg(resumed_deadline), revision = revision + 1,
    updated_at = sqlc.arg(resumed_at)
WHERE pause_id = sqlc.arg(pause_id) AND revision = sqlc.arg(expected_revision)
    AND resumed_at IS NULL AND resumed_deadline IS NULL
RETURNING pause_id;

-- name: ShiftTournamentAdminNormalPauseReadyWindowDeadlineCAS :one
UPDATE ready_windows
SET deadline = sqlc.arg(resumed_deadline)
WHERE id = sqlc.arg(ready_window_id) AND wave_id = sqlc.arg(wave_id)
    AND roster_id = sqlc.arg(roster_id)
    AND deadline = sqlc.arg(original_deadline)
RETURNING id;

-- name: ResumeTournamentAdminNormalPauseCAS :one
UPDATE pauses
SET state = 'resumed', current_revision_id = sqlc.arg(current_revision_id),
    revision = revision + 1, resolved_at = sqlc.arg(resumed_at),
    updated_at = sqlc.arg(resumed_at)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision)
    AND current_revision_id = sqlc.arg(expected_revision_id) AND state = 'active'
RETURNING id;

-- name: ResumeTournamentAdminNormalGameCAS :one
UPDATE game_attempts
SET state = 'active', revision = revision + 1, updated_at = sqlc.arg(resumed_at)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision) AND state = 'paused'
RETURNING id;

-- name: ResumeTournamentAdminNormalSeriesCAS :one
UPDATE series
SET state = sqlc.arg(resume_state), revision = revision + 1, updated_at = sqlc.arg(resumed_at)
WHERE id = sqlc.arg(id) AND tournament_id = sqlc.arg(tournament_id)
    AND revision = sqlc.arg(expected_revision) AND state = 'technical_pause'
    AND sqlc.arg(resume_state) IN ('draft', 'ready', 'active', 'replay_required')
RETURNING id;

-- name: ResumeTournamentAdminNormalWaveCAS :one
UPDATE waves
SET state = sqlc.arg(resume_state), revision = revision + 1, paused_at = NULL,
    updated_at = sqlc.arg(resumed_at)
WHERE id = sqlc.arg(id) AND tournament_id = sqlc.arg(tournament_id)
    AND revision = sqlc.arg(expected_revision) AND state = sqlc.arg(expected_state)
    AND (
        (sqlc.arg(expected_state) = 'paused' AND sqlc.arg(resume_state) = 'active')
        OR (sqlc.arg(expected_state) IN ('ready_window_open', 'ready')
            AND sqlc.arg(resume_state) = sqlc.arg(expected_state))
    )
RETURNING id;

-- name: ResumeTournamentAdminNormalTournamentCAS :one
UPDATE tournaments
SET state = sqlc.arg(resume_state), paused_from_state = NULL,
    revision = revision + 1, updated_at = sqlc.arg(resumed_at)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision)
    AND state = 'technical_pause' AND paused_from_state = sqlc.arg(resume_state)
RETURNING id;

-- RebindTournamentAdminNormalPauseGameEpochs appends the successor execution
-- authority before paused Games become active in either executable phase.
-- name: RebindTournamentAdminNormalPauseGameEpochs :many
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
    JOIN presence_states AS first_presence
        ON first_presence.series_id = series.id
        AND first_presence.participant_id = series.first_participant_id
        AND first_presence.state = 'connected'
    JOIN presence_states AS second_presence
        ON second_presence.series_id = series.id
        AND second_presence.participant_id = series.second_participant_id
        AND second_presence.state = 'connected'
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
        AND tournament.state IN ('swiss', 'playoffs')
        AND (series.state = 'technical_pause' OR (
            series.state = 'active'
            AND EXISTS (
                SELECT 1 FROM pauses AS source_pause
                WHERE source_pause.tournament_id = series.tournament_id
                    AND source_pause.roster_id = series.roster_id
                    AND source_pause.series_id = series.id
                    AND source_pause.game_attempt_id = attempt.id
                    AND source_pause.scope_kind = 'game_attempt'
                    AND source_pause.scope_id = attempt.id
                    AND source_pause.parent_pause_id IS NULL
                    AND source_pause.depth = 0
                    AND source_pause.reason = 'disconnect'
                    AND source_pause.state = 'active'
            )
        ))
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
