-- The lifecycle coordinator locks tournament and roster first. Progression
-- then locks these sources in the fixed order below: Swiss rounds and Waves,
-- Series and result heads, Golden attempts and commits, projection artifacts.

-- A semifinal has no Wave or client assignment authority at stage creation.
-- It is nevertheless born with the same immutable zero-score genesis as a
-- normal Series. The CTE binds the score head and the Series pointer before
-- the deferred revision guards run, so an incomplete locked semifinal rolls
-- back with the outer projection publication.
-- name: CreateTournamentProgressionLockedSemifinalSeries :one
WITH created_series AS (
    INSERT INTO series (
        id,
        tournament_id,
        roster_id,
        first_participant_id,
        second_participant_id,
        format,
        state,
        current_score_revision_id,
        revision,
        created_at,
        updated_at
    )
    VALUES (
        sqlc.arg(series_id),
        sqlc.arg(tournament_id),
        sqlc.arg(roster_id),
        sqlc.arg(first_participant_id),
        sqlc.arg(second_participant_id),
        'bo1',
        'locked',
        sqlc.arg(initial_score_revision_id),
        1,
        sqlc.arg(created_at),
        sqlc.arg(created_at)
    )
    RETURNING id,
        tournament_id,
        roster_id
),
created_score AS (
    INSERT INTO series_score_revisions (
        id,
        tournament_id,
        roster_id,
        series_id,
        result_event_id,
        previous_revision_id,
        revision_number,
        operation,
        command_id,
        actor_kind,
        actor_id,
        command_attempt_id,
        source_projection_revision_id,
        source_projection_revision,
        first_participant_wins,
        second_participant_wins,
        created_at
    )
    SELECT sqlc.arg(initial_score_revision_id),
        created_series.tournament_id,
        created_series.roster_id,
        created_series.id,
        NULL,
        NULL,
        1,
        'initialize',
        sqlc.arg(command_id),
        'server',
        NULL,
        NULL,
        sqlc.arg(source_projection_revision_id),
        sqlc.arg(source_projection_revision),
        0,
        0,
        sqlc.arg(created_at)
    FROM created_series
    RETURNING series_id,
        roster_id,
        id
),
created_head AS (
    INSERT INTO series_score_heads (
        series_id,
        roster_id,
        current_revision_id,
        revision,
        updated_at
    )
    SELECT series_id,
        roster_id,
        id,
        1,
        sqlc.arg(created_at)
    FROM created_score
    RETURNING series_id
)
SELECT id
FROM created_series;

-- name: FindTournamentStageProgression :one
SELECT lifecycle.command_id,
    lifecycle.tournament_id,
    lifecycle.roster_id,
    lifecycle.actor_id,
    lifecycle.action,
    lifecycle.source_projection_revision_id,
    lifecycle.source_projection_revision,
    lifecycle.source_tournament_revision,
    lifecycle.source_tournament_state,
    lifecycle.resulting_tournament_revision,
    lifecycle.resulting_tournament_state,
    lifecycle.preset,
    lifecycle.roster_size,
    lifecycle.tournament_created_at,
    lifecycle.tournament_updated_at,
    lifecycle.tournament_started_at,
    lifecycle.tournament_finished_at,
    lifecycle.executed_at,
    progression.proof,
    progression.proof_digest
FROM tournament_stage_progressions AS progression
INNER JOIN tournament_lifecycle_commands AS lifecycle
    ON lifecycle.command_id = progression.command_id
    AND lifecycle.tournament_id = progression.tournament_id
WHERE progression.tournament_id = sqlc.arg(tournament_id)
    AND progression.command_id = sqlc.arg(command_id);

-- Read our own stage proof before the outer coordinator writes its deferred
-- lifecycle receipt. Replay lookup remains bound to the lifecycle receipt.
-- name: GetTournamentStageProgressionProof :one
SELECT command_id,
    tournament_id,
    roster_id,
    actor_id,
    action,
    source_tournament_revision,
    source_tournament_state,
    source_projection_revision_id,
    source_projection_revision,
    resulting_projection_revision_id,
    resulting_projection_revision,
    resulting_tournament_revision,
    resulting_tournament_state,
    proof,
    proof_digest,
    executed_at,
    created_at
FROM tournament_stage_progressions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND command_id = sqlc.arg(command_id);

-- name: LockTournamentProgressionSwissRounds :many
SELECT swiss_round.id,
    swiss_round.roster_id,
    swiss_round.round_number,
    swiss_round.revision,
    swiss_round.lock_revision,
    swiss_round.locked_at,
    swiss_link.wave_id,
    swiss_link.bye_participant_id,
    swiss_link.bye_revision_id,
    wave.revision_id AS wave_revision_id,
    wave.revision AS wave_revision,
    wave.state AS wave_state,
    wave.closed_at AS wave_closed_at
FROM swiss_rounds AS swiss_round
INNER JOIN swiss_wave_links AS swiss_link
    ON swiss_link.round_id = swiss_round.id
    AND swiss_link.roster_id = swiss_round.roster_id
INNER JOIN waves AS wave
    ON wave.id = swiss_link.wave_id
    AND wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = swiss_round.roster_id
WHERE swiss_round.roster_id = sqlc.arg(roster_id)
ORDER BY swiss_round.round_number, swiss_round.id
FOR UPDATE OF swiss_round, swiss_link, wave;

-- Locked proof roots are loaded after their Swiss round and Wave. They are
-- normalized durable authority, not a reconstructed pairing document.
-- name: LockTournamentProgressionSwissRoundLockProofs :many
SELECT proof.round_id,
    proof.tournament_id,
    proof.roster_id,
    proof.preset,
    proof.round_number,
    proof.source_projection_revision_id,
    proof.preflight_revision_id,
    proof.normal_pool_revision_id,
    proof.wave_id,
    proof.wave_revision_id,
    proof.round_revision,
    proof.source_projection_revision,
    proof.roster_revision,
    proof.history_revision,
    proof.normal_pool_revision,
    proof.wave_revision,
    proof.bye_participant_id,
    proof.proof_hash,
    proof.proof_mode,
    proof.terminal_command_id,
    proof.locked_at,
    proof.created_at
FROM swiss_round_lock_proofs AS proof
WHERE proof.tournament_id = sqlc.arg(tournament_id)
    AND proof.roster_id = sqlc.arg(roster_id)
ORDER BY proof.round_number, proof.round_id
FOR UPDATE OF proof;

-- name: LockTournamentProgressionSwissRoundLockProofMembers :many
SELECT member.round_id,
    member.roster_id,
    member.participant_id,
    member.created_at
FROM swiss_round_lock_proof_members AS member
INNER JOIN swiss_round_lock_proofs AS proof
    ON proof.round_id = member.round_id
    AND proof.roster_id = member.roster_id
WHERE proof.tournament_id = sqlc.arg(tournament_id)
    AND member.roster_id = sqlc.arg(roster_id)
ORDER BY member.round_id, member.participant_id
FOR UPDATE OF member;

-- name: LockTournamentProgressionSwissRoundLockProofSeries :many
SELECT proof_series.round_id,
    proof_series.roster_id,
    proof_series.pairing_id,
    proof_series.series_id,
    proof_series.first_participant_id,
    proof_series.second_participant_id,
    proof_series.category_revision_id,
    proof_series.category_revision,
    proof_series.assignment_id,
    proof_series.assignment_revision,
    proof_series.assignment_plan_id,
    proof_series.assignment_plan_revision_id,
    proof_series.reservation_id,
    proof_series.reservation_revision,
    proof_series.created_at
FROM swiss_round_lock_proof_series AS proof_series
INNER JOIN swiss_round_lock_proofs AS proof
    ON proof.round_id = proof_series.round_id
    AND proof.roster_id = proof_series.roster_id
WHERE proof.tournament_id = sqlc.arg(tournament_id)
    AND proof_series.roster_id = sqlc.arg(roster_id)
ORDER BY proof_series.round_id, proof_series.pairing_id
FOR UPDATE OF proof_series;

-- name: LockTournamentProgressionSwissSeries :many
SELECT swiss_round.id AS round_id,
    swiss_round.round_number,
    wave.id AS wave_id,
    series.id AS series_id,
    series.first_participant_id,
    series.second_participant_id,
    series.format,
    series.state,
    series.current_score_revision_id,
    series.current_result_revision_id,
    series.started_at,
    series.finished_at,
    result_revision.id AS result_revision_id,
    result_revision.result_state,
    result_revision.result_reason,
    result_revision.winner_id,
    score_revision.id AS score_revision_id
FROM swiss_rounds AS swiss_round
INNER JOIN swiss_wave_links AS swiss_link
    ON swiss_link.round_id = swiss_round.id
    AND swiss_link.roster_id = swiss_round.roster_id
INNER JOIN waves AS wave
    ON wave.id = swiss_link.wave_id
    AND wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = swiss_round.roster_id
INNER JOIN wave_series AS wave_series
    ON wave_series.wave_id = wave.id
    AND wave_series.tournament_id = wave.tournament_id
    AND wave_series.roster_id = wave.roster_id
INNER JOIN series
    ON series.id = wave_series.series_id
    AND series.tournament_id = wave_series.tournament_id
    AND series.roster_id = wave_series.roster_id
LEFT JOIN official_result_revisions AS result_revision
    ON result_revision.id = series.current_result_revision_id
    AND result_revision.series_id = series.id
    AND result_revision.roster_id = series.roster_id
LEFT JOIN series_score_revisions AS score_revision
    ON score_revision.id = series.current_score_revision_id
    AND score_revision.series_id = series.id
    AND score_revision.roster_id = series.roster_id
WHERE swiss_round.roster_id = sqlc.arg(roster_id)
ORDER BY swiss_round.round_number, wave.id, series.id
FOR UPDATE OF series;

-- Result heads are locked after their Series. The INNER JOIN intentionally
-- excludes Series without a result head, which remain invalid progression
-- evidence rather than becoming nullable lock targets.
-- name: LockTournamentProgressionSwissResultHeads :many
SELECT result_head.current_revision_id AS result_revision_id,
    result_head.revision AS result_head_revision,
    result_head.updated_at AS result_head_updated_at,
    result_revision.series_id,
    result_revision.roster_id,
    result_revision.result_event_id,
    result_revision.previous_revision_id,
    result_revision.revision_number,
    result_revision.result_state,
    result_revision.result_reason,
    result_revision.winner_id,
    result_revision.created_at AS result_revision_created_at,
    result_revision.command_id AS result_command_id,
    result_revision.actor_kind AS result_actor_kind,
    result_revision.actor_id AS result_actor_id,
    result_revision.source_projection_revision_id AS result_source_projection_revision_id,
    result_revision.source_projection_revision AS result_source_projection_revision,
    result_event.occurred_at AS result_occurred_at
FROM swiss_rounds AS swiss_round
INNER JOIN swiss_wave_links AS swiss_link
    ON swiss_link.round_id = swiss_round.id
    AND swiss_link.roster_id = swiss_round.roster_id
INNER JOIN waves AS wave
    ON wave.id = swiss_link.wave_id
    AND wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = swiss_round.roster_id
INNER JOIN wave_series AS wave_series
    ON wave_series.wave_id = wave.id
    AND wave_series.tournament_id = wave.tournament_id
    AND wave_series.roster_id = wave.roster_id
INNER JOIN series
    ON series.id = wave_series.series_id
    AND series.tournament_id = wave_series.tournament_id
    AND series.roster_id = wave_series.roster_id
INNER JOIN official_result_heads AS result_head
    ON result_head.entity_kind = 'series'
    AND result_head.entity_id = series.id
    AND result_head.series_id = series.id
    AND result_head.roster_id = series.roster_id
INNER JOIN official_result_revisions AS result_revision
    ON result_revision.id = result_head.current_revision_id
    AND result_revision.entity_kind = result_head.entity_kind
    AND result_revision.entity_id = result_head.entity_id
    AND result_revision.series_id = result_head.series_id
    AND result_revision.roster_id = result_head.roster_id
INNER JOIN result_events AS result_event
    ON result_event.id = result_revision.result_event_id
    AND result_event.tournament_id = result_revision.tournament_id
    AND result_event.roster_id = result_revision.roster_id
    AND result_event.series_id = result_revision.series_id
WHERE swiss_round.roster_id = sqlc.arg(roster_id)
ORDER BY swiss_round.round_number, wave.id, series.id
FOR UPDATE OF result_head, result_revision;

-- Score heads are locked after official result heads for the same Series.
-- name: LockTournamentProgressionSwissScoreHeads :many
SELECT score_head.current_revision_id AS score_revision_id,
    score_head.revision AS score_head_revision,
    score_head.updated_at AS score_head_updated_at,
    score_revision.series_id,
    score_revision.roster_id,
    score_revision.result_event_id,
    score_revision.previous_revision_id,
    score_revision.revision_number,
    score_revision.operation AS score_operation,
    score_revision.command_id AS score_command_id,
    score_revision.actor_kind AS score_actor_kind,
    score_revision.actor_id AS score_actor_id,
    score_revision.command_attempt_id AS score_command_attempt_id,
    score_revision.source_projection_revision_id AS score_source_projection_revision_id,
    score_revision.source_projection_revision AS score_source_projection_revision,
    score_revision.first_participant_wins,
    score_revision.second_participant_wins,
    score_revision.created_at AS score_revision_created_at,
    result_event.occurred_at AS score_occurred_at
FROM swiss_rounds AS swiss_round
INNER JOIN swiss_wave_links AS swiss_link
    ON swiss_link.round_id = swiss_round.id
    AND swiss_link.roster_id = swiss_round.roster_id
INNER JOIN waves AS wave
    ON wave.id = swiss_link.wave_id
    AND wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = swiss_round.roster_id
INNER JOIN wave_series AS wave_series
    ON wave_series.wave_id = wave.id
    AND wave_series.tournament_id = wave.tournament_id
    AND wave_series.roster_id = wave.roster_id
INNER JOIN series
    ON series.id = wave_series.series_id
    AND series.tournament_id = wave_series.tournament_id
    AND series.roster_id = wave_series.roster_id
INNER JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
    AND score_head.roster_id = series.roster_id
INNER JOIN series_score_revisions AS score_revision
    ON score_revision.id = score_head.current_revision_id
    AND score_revision.series_id = score_head.series_id
    AND score_revision.roster_id = score_head.roster_id
INNER JOIN result_events AS result_event
    ON result_event.id = score_revision.result_event_id
    AND result_event.tournament_id = score_revision.tournament_id
    AND result_event.roster_id = score_revision.roster_id
    AND result_event.series_id = score_revision.series_id
WHERE swiss_round.roster_id = sqlc.arg(roster_id)
ORDER BY swiss_round.round_number, wave.id, series.id
FOR UPDATE OF score_head, score_revision;

-- Score attempt rows are immutable copies of the exact current score head.
-- They are locked after the score head and are never reconstructed from game
-- identifiers by the progression reader.
-- name: LockTournamentProgressionSwissScoreRevisionAttempts :many
SELECT swiss_round.id AS round_id,
    series.id AS series_id,
    score_revision.id AS score_revision_id,
    attempt.position,
    attempt.slot_id,
    attempt.slot_position,
    attempt.game_attempt_id,
    attempt.attempt_number,
    attempt.game_result_revision_id,
    attempt.result_event_id,
    attempt.result_state,
    attempt.result_reason,
    attempt.winner_id,
    attempt.occurred_at,
    attempt.created_at
FROM swiss_rounds AS swiss_round
INNER JOIN swiss_wave_links AS swiss_link
    ON swiss_link.round_id = swiss_round.id
    AND swiss_link.roster_id = swiss_round.roster_id
INNER JOIN waves AS wave
    ON wave.id = swiss_link.wave_id
    AND wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = swiss_round.roster_id
INNER JOIN wave_series AS wave_series
    ON wave_series.wave_id = wave.id
    AND wave_series.tournament_id = wave.tournament_id
    AND wave_series.roster_id = wave.roster_id
INNER JOIN series
    ON series.id = wave_series.series_id
    AND series.tournament_id = wave_series.tournament_id
    AND series.roster_id = wave_series.roster_id
INNER JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
    AND score_head.roster_id = series.roster_id
INNER JOIN series_score_revisions AS score_revision
    ON score_revision.id = score_head.current_revision_id
    AND score_revision.series_id = score_head.series_id
    AND score_revision.roster_id = score_head.roster_id
INNER JOIN series_score_revision_attempts AS attempt
    ON attempt.score_revision_id = score_revision.id
    AND attempt.tournament_id = score_revision.tournament_id
    AND attempt.roster_id = score_revision.roster_id
    AND attempt.series_id = score_revision.series_id
WHERE swiss_round.roster_id = sqlc.arg(roster_id)
ORDER BY swiss_round.round_number, series.id, attempt.position
FOR UPDATE OF attempt;

-- Pre-start operator forfeits are the only score source without a game
-- attempt ledger. The normalized adjudication child is therefore mandatory.
-- name: LockTournamentProgressionSwissScoreRevisionAdjudications :many
SELECT swiss_round.id AS round_id,
    series.id AS series_id,
    score_revision.id AS score_revision_id,
    adjudication.operator_forfeit_commit_id,
    adjudication.command_id,
    adjudication.actor_id,
    adjudication.anchor_attempt_id,
    adjudication.forfeiting_participant_id,
    adjudication.winner_id,
    adjudication.source_projection_revision_id,
    adjudication.source_projection_revision,
    adjudication.created_at
FROM swiss_rounds AS swiss_round
INNER JOIN swiss_wave_links AS swiss_link
    ON swiss_link.round_id = swiss_round.id
    AND swiss_link.roster_id = swiss_round.roster_id
INNER JOIN waves AS wave
    ON wave.id = swiss_link.wave_id
    AND wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = swiss_round.roster_id
INNER JOIN wave_series AS wave_series
    ON wave_series.wave_id = wave.id
    AND wave_series.tournament_id = wave.tournament_id
    AND wave_series.roster_id = wave.roster_id
INNER JOIN series
    ON series.id = wave_series.series_id
    AND series.tournament_id = wave_series.tournament_id
    AND series.roster_id = wave_series.roster_id
INNER JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
    AND score_head.roster_id = series.roster_id
INNER JOIN series_score_revisions AS score_revision
    ON score_revision.id = score_head.current_revision_id
    AND score_revision.series_id = score_head.series_id
    AND score_revision.roster_id = score_head.roster_id
INNER JOIN series_score_revision_adjudications AS adjudication
    ON adjudication.score_revision_id = score_revision.id
    AND adjudication.tournament_id = score_revision.tournament_id
    AND adjudication.roster_id = score_revision.roster_id
    AND adjudication.series_id = score_revision.series_id
WHERE swiss_round.roster_id = sqlc.arg(roster_id)
ORDER BY swiss_round.round_number, series.id, adjudication.score_revision_id
FOR UPDATE OF adjudication;

-- Terminal game heads supply the full, immutable game evidence referenced by
-- a Series score. The progression adapter passes these normalized rows to the
-- correction-owned SwissTerminalEvidenceReader; it must reject missing or
-- non-terminal rows instead of filling them from an identifier.
-- name: LockTournamentProgressionSwissGameHeads :many
SELECT swiss_round.id AS round_id,
    swiss_round.round_number,
    series.id AS series_id,
    game_slot.id AS slot_id,
    game_slot.slot_number,
    game_slot.category,
    game_slot.first_participant_wins_before,
    game_slot.second_participant_wins_before,
    game_attempt.id AS game_attempt_id,
    game_attempt.attempt_number,
    game_attempt.state AS game_state,
    game_attempt.result_reason AS game_reason,
    game_attempt.winner_id AS game_winner_id,
    game_attempt.result_revision_id AS game_result_revision_id,
    game_attempt.finished_at AS game_finished_at,
    game_result_head.current_revision_id AS result_revision_id,
    game_result_head.revision AS result_head_revision,
    game_result_revision.previous_revision_id,
    game_result_revision.revision_number,
    game_result_revision.result_state,
    game_result_revision.result_reason,
    game_result_revision.winner_id AS result_winner_id,
    game_result_revision.created_at AS result_revision_created_at,
    game_result_revision.command_id AS result_command_id,
    game_result_revision.actor_kind AS result_actor_kind,
    game_result_revision.actor_id AS result_actor_id,
    game_result_revision.source_projection_revision_id AS result_source_projection_revision_id,
    game_result_revision.source_projection_revision AS result_source_projection_revision,
    result_event.submission_event_id,
    result_event.occurred_at AS result_occurred_at,
    submission_event.payload_digest AS submission_evidence_digest
FROM swiss_rounds AS swiss_round
INNER JOIN swiss_wave_links AS swiss_link
    ON swiss_link.round_id = swiss_round.id
    AND swiss_link.roster_id = swiss_round.roster_id
INNER JOIN waves AS wave
    ON wave.id = swiss_link.wave_id
    AND wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = swiss_round.roster_id
INNER JOIN wave_series AS wave_series
    ON wave_series.wave_id = wave.id
    AND wave_series.tournament_id = wave.tournament_id
    AND wave_series.roster_id = wave.roster_id
INNER JOIN series
    ON series.id = wave_series.series_id
    AND series.tournament_id = wave_series.tournament_id
    AND series.roster_id = wave_series.roster_id
INNER JOIN game_slots AS game_slot
    ON game_slot.series_id = series.id
    AND game_slot.roster_id = series.roster_id
INNER JOIN game_attempts AS game_attempt
    ON game_attempt.slot_id = game_slot.id
    AND game_attempt.series_id = game_slot.series_id
    AND game_attempt.roster_id = game_slot.roster_id
INNER JOIN official_result_heads AS game_result_head
    ON game_result_head.entity_kind = 'game_attempt'
    AND game_result_head.entity_id = game_attempt.id
    AND game_result_head.series_id = game_attempt.series_id
    AND game_result_head.roster_id = game_attempt.roster_id
    AND game_result_head.current_revision_id = game_attempt.result_revision_id
INNER JOIN official_result_revisions AS game_result_revision
    ON game_result_revision.id = game_result_head.current_revision_id
    AND game_result_revision.entity_kind = game_result_head.entity_kind
    AND game_result_revision.entity_id = game_result_head.entity_id
    AND game_result_revision.series_id = game_result_head.series_id
    AND game_result_revision.roster_id = game_result_head.roster_id
INNER JOIN result_events AS result_event
    ON result_event.id = game_result_revision.result_event_id
    AND result_event.tournament_id = game_result_revision.tournament_id
    AND result_event.roster_id = game_result_revision.roster_id
    AND result_event.series_id = game_result_revision.series_id
    AND result_event.attempt_id = game_attempt.id
LEFT JOIN submission_events AS submission_event
    ON submission_event.id = result_event.submission_event_id
    AND submission_event.tournament_id = result_event.tournament_id
    AND submission_event.roster_id = result_event.roster_id
    AND submission_event.series_id = result_event.series_id
    AND submission_event.attempt_id = result_event.attempt_id
WHERE swiss_round.roster_id = sqlc.arg(roster_id)
ORDER BY swiss_round.round_number, series.id, game_slot.slot_number, game_attempt.attempt_number
FOR UPDATE OF game_slot, game_attempt, game_result_head, game_result_revision;

-- Normal no-show evidence is a distinct exhaustive terminal path. Its commit
-- and game list are loaded separately from ordinary game heads so a missing
-- game row cannot be mistaken for proof of a no-show.
-- name: LockTournamentProgressionSwissNormalNoShowCommits :many
SELECT swiss_round.id AS round_id,
    normal_commit.id AS commit_id,
    normal_commit.wave_id,
    normal_commit.ready_window_id,
    normal_commit.ready_window_revision_id,
    normal_commit.series_id,
    normal_commit.result_event_id,
    normal_commit.series_score_revision_id,
    normal_commit.series_result_revision_id,
    normal_commit.command_id,
    normal_commit.action,
    normal_commit.resolved_at,
    normal_game.game_attempt_id,
    normal_game.game_result_revision_id,
    normal_game.position,
    projection_evidence.artifact_kinds,
    projection_evidence.payload_digest
FROM swiss_rounds AS swiss_round
INNER JOIN swiss_wave_links AS swiss_link
    ON swiss_link.round_id = swiss_round.id
    AND swiss_link.roster_id = swiss_round.roster_id
INNER JOIN waves AS wave
    ON wave.id = swiss_link.wave_id
    AND wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = swiss_round.roster_id
INNER JOIN normal_no_show_commits AS normal_commit
    ON normal_commit.wave_id = wave.id
    AND normal_commit.tournament_id = wave.tournament_id
    AND normal_commit.roster_id = wave.roster_id
INNER JOIN normal_no_show_commit_games AS normal_game
    ON normal_game.commit_id = normal_commit.id
INNER JOIN result_projection_evidence AS projection_evidence
    ON projection_evidence.id = normal_commit.projection_evidence_id
    AND projection_evidence.tournament_id = normal_commit.tournament_id
    AND projection_evidence.roster_id = normal_commit.roster_id
    AND projection_evidence.series_id = normal_commit.series_id
    AND projection_evidence.result_event_id = normal_commit.result_event_id
WHERE swiss_round.roster_id = sqlc.arg(roster_id)
ORDER BY swiss_round.round_number, normal_commit.series_id, normal_game.position
FOR UPDATE OF normal_commit;

-- name: LockTournamentProgressionSwissOperatorForfeitCommits :many
SELECT swiss_round.id AS round_id,
    forfeit_commit.id AS commit_id,
    forfeit_commit.series_id,
    forfeit_commit.anchor_attempt_id,
    forfeit_commit.result_event_id,
    forfeit_commit.series_score_revision_id,
    forfeit_commit.series_result_revision_id,
    forfeit_commit.command_id,
    forfeit_commit.actor_id,
    forfeit_commit.forfeiting_participant_id,
    forfeit_commit.source_projection_revision_id,
    forfeit_commit.source_projection_revision,
    forfeit_commit.rule_id,
    forfeit_commit.reason,
    forfeit_commit.evidence_ids,
    forfeit_commit.resolved_at,
    projection_evidence.artifact_kinds,
    projection_evidence.payload_digest
FROM swiss_rounds AS swiss_round
INNER JOIN swiss_wave_links AS swiss_link
    ON swiss_link.round_id = swiss_round.id
    AND swiss_link.roster_id = swiss_round.roster_id
INNER JOIN waves AS wave
    ON wave.id = swiss_link.wave_id
    AND wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = swiss_round.roster_id
INNER JOIN wave_series AS wave_series
    ON wave_series.wave_id = wave.id
    AND wave_series.tournament_id = wave.tournament_id
    AND wave_series.roster_id = wave.roster_id
INNER JOIN operator_forfeit_commits AS forfeit_commit
    ON forfeit_commit.series_id = wave_series.series_id
    AND forfeit_commit.tournament_id = wave_series.tournament_id
    AND forfeit_commit.roster_id = wave_series.roster_id
INNER JOIN result_projection_evidence AS projection_evidence
    ON projection_evidence.id = forfeit_commit.projection_evidence_id
    AND projection_evidence.tournament_id = forfeit_commit.tournament_id
    AND projection_evidence.roster_id = forfeit_commit.roster_id
    AND projection_evidence.series_id = forfeit_commit.series_id
    AND projection_evidence.result_event_id = forfeit_commit.result_event_id
WHERE swiss_round.roster_id = sqlc.arg(roster_id)
ORDER BY swiss_round.round_number, forfeit_commit.series_id
FOR UPDATE OF forfeit_commit;

-- Logical correction nodes retain the exact projection payload and digest for
-- every game, score, and Series result. The reader selects only bindings whose
-- source_id equals the already locked current result or score head.
-- name: LockTournamentProgressionSwissProjectionBindings :many
SELECT binding.command_id,
    binding.artifact_kind,
    binding.entity_id,
    binding.source_id,
    binding.node_id,
    binding.created_at,
    node.previous_node_id,
    node.revision_number,
    node.payload,
    node.payload_digest,
    node.created_at AS node_created_at
FROM correction_projection_bindings AS binding
INNER JOIN result_projection_nodes AS node
    ON node.id = binding.node_id
    AND node.tournament_id = binding.tournament_id
    AND node.roster_id = binding.roster_id
WHERE binding.tournament_id = sqlc.arg(tournament_id)
    AND binding.roster_id = sqlc.arg(roster_id)
    AND binding.artifact_kind IN ('game_result', 'series_score', 'series_result')
ORDER BY binding.artifact_kind, binding.entity_id, binding.source_id, binding.node_id
FOR KEY SHARE OF binding, node;

-- name: LockTournamentProgressionSwissProjectionNodes :many
SELECT node.id,
    node.authority_id,
    node.artifact_kind,
    node.entity_id,
    node.revision_number,
    node.previous_node_id,
    node.payload,
    node.payload_digest,
    node.created_at
FROM result_projection_nodes AS node
WHERE node.tournament_id = sqlc.arg(tournament_id)
    AND node.roster_id = sqlc.arg(roster_id)
    AND node.artifact_kind IN ('game_result', 'series_score', 'series_result')
ORDER BY node.artifact_kind, node.entity_id, node.revision_number, node.id
FOR KEY SHARE;

-- Historical result and score revisions retain the exact physical published
-- projection present when the receipt was accepted. This read is anchored to
-- receipt IDs, never mutable current heads, so a correction cannot rewrite a
-- prior Final Swiss predecessor.
-- name: LockTournamentProgressionFinalSwissReceiptSourceProjections :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
), source_references AS (
    SELECT receipt_series.projection_revision_id AS receipt_projection_revision_id,
        'series_result'::text AS result_kind,
        receipt_series.series_id AS entity_id,
        receipt_series.series_result_revision_id AS result_revision_id,
        result_revision.source_projection_revision_id,
        result_revision.source_projection_revision
    FROM final_swiss_projection_receipt_series AS receipt_series
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
        AND receipt_chain.tournament_id = receipt_series.tournament_id
        AND receipt_chain.roster_id = receipt_series.roster_id
    INNER JOIN official_result_revisions AS result_revision
        ON result_revision.id = receipt_series.series_result_revision_id
        AND result_revision.tournament_id = receipt_series.tournament_id
        AND result_revision.roster_id = receipt_series.roster_id
        AND result_revision.series_id = receipt_series.series_id
        AND result_revision.entity_kind = 'series'

    UNION ALL

    SELECT receipt_series.projection_revision_id,
        'series_score'::text,
        receipt_series.series_id,
        receipt_series.score_revision_id,
        score_revision.source_projection_revision_id,
        score_revision.source_projection_revision
    FROM final_swiss_projection_receipt_series AS receipt_series
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
        AND receipt_chain.tournament_id = receipt_series.tournament_id
        AND receipt_chain.roster_id = receipt_series.roster_id
    INNER JOIN series_score_revisions AS score_revision
        ON score_revision.id = receipt_series.score_revision_id
        AND score_revision.tournament_id = receipt_series.tournament_id
        AND score_revision.roster_id = receipt_series.roster_id
        AND score_revision.series_id = receipt_series.series_id

    UNION ALL

    SELECT receipt_game.projection_revision_id,
        'game_result'::text,
        receipt_game.game_attempt_id,
        receipt_game.game_result_revision_id,
        result_revision.source_projection_revision_id,
        result_revision.source_projection_revision
    FROM final_swiss_projection_receipt_games AS receipt_game
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_game.projection_revision_id
        AND receipt_chain.tournament_id = receipt_game.tournament_id
        AND receipt_chain.roster_id = receipt_game.roster_id
    INNER JOIN official_result_revisions AS result_revision
        ON result_revision.id = receipt_game.game_result_revision_id
        AND result_revision.tournament_id = receipt_game.tournament_id
        AND result_revision.roster_id = receipt_game.roster_id
        AND result_revision.entity_id = receipt_game.game_attempt_id
        AND result_revision.entity_kind = 'game_attempt'
)
SELECT source_references.receipt_projection_revision_id,
    source_references.result_kind,
    source_references.entity_id,
    source_references.result_revision_id,
    source_references.source_projection_revision_id,
    source_references.source_projection_revision,
    projection_revision.id AS physical_projection_revision_id,
    projection_revision.revision_number AS physical_projection_revision,
    projection_revision.previous_revision_id AS physical_previous_projection_revision_id,
    projection_revision.state AS physical_projection_state,
    projection_revision.created_at AS physical_projection_created_at,
    artifact.id AS artifact_id,
    artifact.artifact_kind,
    artifact.payload,
    artifact.payload_digest,
    artifact.created_at AS artifact_created_at
FROM source_references
INNER JOIN projection_revisions AS projection_revision
    ON projection_revision.id = source_references.source_projection_revision_id
    AND projection_revision.tournament_id = sqlc.arg(tournament_id)
    AND projection_revision.roster_id = sqlc.arg(roster_id)
    AND projection_revision.revision_number = source_references.source_projection_revision
INNER JOIN projection_revision_artifacts AS membership
    ON membership.revision_id = projection_revision.id
    AND membership.tournament_id = projection_revision.tournament_id
    AND membership.roster_id = projection_revision.roster_id
INNER JOIN projection_artifacts AS artifact
    ON artifact.id = membership.artifact_id
    AND artifact.tournament_id = membership.tournament_id
    AND artifact.roster_id = membership.roster_id
    AND artifact.artifact_kind = membership.artifact_kind
ORDER BY source_references.receipt_projection_revision_id,
    source_references.result_kind,
    source_references.entity_id,
    source_references.result_revision_id,
    artifact.artifact_kind,
    artifact.id
FOR KEY SHARE OF projection_revision, membership, artifact;

-- Logical result nodes are anchored by the exact IDs persisted in each
-- receipt. Ordinary origins retain their revision identity; correction
-- origins require their normalized binding. No current Series or Game head is
-- consulted by this historical reader.
-- name: LockTournamentProgressionFinalSwissReceiptLogicalResultNodes :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
), heads AS (
    SELECT receipt_series.projection_revision_id AS receipt_projection_revision_id,
        'series_result'::text AS head_kind,
        receipt_series.series_id AS entity_id,
        receipt_series.series_result_revision_id AS source_id,
        receipt_series.series_result_node_id AS node_id
    FROM final_swiss_projection_receipt_series AS receipt_series
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
        AND receipt_chain.tournament_id = receipt_series.tournament_id
        AND receipt_chain.roster_id = receipt_series.roster_id

    UNION ALL

    SELECT receipt_series.projection_revision_id,
        'series_score'::text,
        receipt_series.series_id,
        receipt_series.score_revision_id,
        receipt_series.score_node_id
    FROM final_swiss_projection_receipt_series AS receipt_series
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
        AND receipt_chain.tournament_id = receipt_series.tournament_id
        AND receipt_chain.roster_id = receipt_series.roster_id

    UNION ALL

    SELECT receipt_game.projection_revision_id,
        'game_result'::text,
        receipt_game.game_attempt_id,
        receipt_game.game_result_revision_id,
        receipt_game.game_result_node_id
    FROM final_swiss_projection_receipt_games AS receipt_game
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_game.projection_revision_id
        AND receipt_chain.tournament_id = receipt_game.tournament_id
        AND receipt_chain.roster_id = receipt_game.roster_id
)
SELECT head.receipt_projection_revision_id,
    head.head_kind,
    head.entity_id,
    head.source_id,
    node.id AS node_id,
    node.authority_id,
    authority.source_kind,
    node.artifact_kind,
    node.revision_number,
    node.previous_node_id,
    node.payload,
    node.payload_digest,
    node.created_at AS node_created_at,
    binding.command_id AS correction_command_id,
    binding.created_at AS binding_created_at
FROM heads AS head
LEFT JOIN correction_projection_bindings AS binding
    ON binding.tournament_id = sqlc.arg(tournament_id)
    AND binding.roster_id = sqlc.arg(roster_id)
    AND binding.artifact_kind = head.head_kind
    AND binding.entity_id = head.entity_id
    AND binding.source_id = head.source_id
INNER JOIN result_projection_nodes AS node
    ON node.id = head.node_id
    AND node.tournament_id = sqlc.arg(tournament_id)
    AND node.roster_id = sqlc.arg(roster_id)
    AND node.artifact_kind = head.head_kind
    AND node.entity_id = head.entity_id
INNER JOIN result_projection_node_authorities AS authority
    ON authority.id = node.authority_id
    AND authority.tournament_id = node.tournament_id
    AND authority.roster_id = node.roster_id
WHERE (
    binding.node_id IS NULL
    AND node.id = head.source_id
    AND authority.source_kind IN ('result_commit', 'wave_initialization', 'normal_no_show_commit', 'operator_forfeit_commit')
) OR (
    binding.node_id = head.node_id
    AND authority.source_kind = 'correction_commit'
)
ORDER BY head.receipt_projection_revision_id,
    head.head_kind,
    head.entity_id,
    head.source_id,
    node.id
FOR KEY SHARE OF node, authority;

-- Projection dependencies are immutable correction graph edges. A normal
-- no-show needs these persisted edges to prove each cancelled Game feeds the
-- score node and the score feeds the Series result node; the reader must not
-- manufacture that lineage from node identifiers alone.
-- name: LockTournamentProgressionSwissProjectionDependencies :many
SELECT dependency.source_node_id,
    dependency.derived_node_id
FROM result_projection_dependencies AS dependency
INNER JOIN result_projection_nodes AS source_node
    ON source_node.id = dependency.source_node_id
INNER JOIN result_projection_nodes AS derived_node
    ON derived_node.id = dependency.derived_node_id
WHERE source_node.tournament_id = sqlc.arg(tournament_id)
    AND source_node.roster_id = sqlc.arg(roster_id)
    AND derived_node.tournament_id = source_node.tournament_id
    AND derived_node.roster_id = source_node.roster_id
    AND source_node.artifact_kind IN ('game_result', 'series_score', 'series_result')
    AND derived_node.artifact_kind IN ('game_result', 'series_score', 'series_result')
ORDER BY dependency.source_node_id, dependency.derived_node_id
FOR KEY SHARE OF dependency, source_node, derived_node;

-- name: LockTournamentProgressionSwissLedger :many
SELECT ledger.id,
    ledger.round_id,
    ledger.round_number,
    ledger.source_kind,
    ledger.source_series_id,
    ledger.series_result_revision_id,
    ledger.bye_revision_id,
    ledger.result_label,
    ledger.participant_id,
    ledger.opponent_id,
    ledger.points,
    ledger.effective_time_ns,
    ledger.accepted_solve_time_ns,
    ledger.stable_seed
FROM swiss_point_ledger_entries AS ledger
WHERE ledger.tournament_id = sqlc.arg(tournament_id)
    AND ledger.roster_id = sqlc.arg(roster_id)
    AND (
        ledger.source_kind = 'bye'
        OR (
            ledger.source_kind = 'series'
            AND EXISTS (
                SELECT 1
                FROM series
                WHERE series.id = ledger.source_series_id
                    AND series.tournament_id = ledger.tournament_id
                    AND series.roster_id = ledger.roster_id
                    AND series.current_result_revision_id = ledger.series_result_revision_id
            )
        )
    )
ORDER BY ledger.round_number,
    ledger.round_id,
    ledger.source_series_id NULLS LAST,
    ledger.participant_id
FOR UPDATE;

-- The Final Swiss receipt is an immutable incident snapshot. Unlike canonical
-- materialization above, it must retain historical ledger pairs after a
-- correction moves the Series head to its successor result revision.
-- name: LockTournamentProgressionAllSwissLedger :many
SELECT ledger.id,
    ledger.round_id,
    ledger.round_number,
    ledger.source_kind,
    ledger.source_series_id,
    ledger.series_result_revision_id,
    ledger.bye_revision_id,
    ledger.result_label,
    ledger.participant_id,
    ledger.opponent_id,
    ledger.points,
    ledger.effective_time_ns,
    ledger.accepted_solve_time_ns,
    ledger.stable_seed
FROM swiss_point_ledger_entries AS ledger
WHERE ledger.tournament_id = sqlc.arg(tournament_id)
    AND ledger.roster_id = sqlc.arg(roster_id)
ORDER BY ledger.round_number,
    ledger.round_id,
    ledger.source_series_id NULLS LAST,
    ledger.participant_id,
    ledger.series_result_revision_id NULLS LAST
FOR UPDATE;

-- Final Swiss receipts retain the canonical logical projection identity even
-- when a correction supersedes the physical standings artifact. The recursive
-- read returns every exact predecessor for revalidation by the application
-- planner; a cycle is retained in the result so the adapter can fail closed.
-- A writer holds the projection authority lock before selecting the latest
-- immutable receipt. Physical revisions may have gaps between receipts.
-- name: CreateStageProjectionOutboxEvent :one
WITH target AS MATERIALIZED (
    SELECT stage.command_id, stage.tournament_id, stage.roster_id,
        stage.source_projection_revision_id, revision.id AS projection_revision_id,
        revision.revision_number, evidence.top4_artifact_id, evidence.bracket_artifact_id,
        stage.executed_at,
        jsonb_build_object(
            'schema', 'playoffs-publication-v1', 'stage_command_id', stage.command_id,
            'projection_revision_id', revision.id, 'projection_revision', revision.revision_number,
            'top4_artifact_id', top4.id, 'top4_digest', encode(top4.payload_digest, 'hex'),
            'bracket_artifact_id', bracket.id, 'bracket_digest', encode(bracket.payload_digest, 'hex')
        ) AS payload
    FROM tournament_stage_progressions AS stage
    JOIN tournament_stage_playoff_evidence AS evidence
        ON evidence.command_id = stage.command_id AND evidence.tournament_id = stage.tournament_id
    JOIN projection_revisions AS revision ON revision.id = stage.resulting_projection_revision_id
    JOIN projection_artifacts AS top4 ON top4.id = evidence.top4_artifact_id
    JOIN projection_artifacts AS bracket ON bracket.id = evidence.bracket_artifact_id
    JOIN final_swiss_projection_receipts AS receipt
        ON receipt.projection_revision_id = stage.source_projection_revision_id
    WHERE stage.command_id = sqlc.arg(stage_command_id)::UUID
        AND stage.tournament_id = sqlc.arg(tournament_id)::UUID
        AND stage.roster_id = sqlc.arg(roster_id)::UUID
        AND revision.id = sqlc.arg(projection_revision_id)::UUID
        AND revision.revision_number = sqlc.arg(projection_revision)::BIGINT
        AND revision.state = 'published'
        AND stage.action = 'start_playoffs'
    FOR SHARE OF stage, evidence, revision, receipt, top4, bracket
), allocated_sequence AS (
    INSERT INTO tournament_outbox_cursors (tournament_id, next_sequence, updated_at)
    SELECT tournament_id, 2, executed_at FROM target
    ON CONFLICT (tournament_id) DO UPDATE
    SET next_sequence = tournament_outbox_cursors.next_sequence + 1, updated_at = EXCLUDED.updated_at
    RETURNING next_sequence - 1 AS sequence
), allocated_ordinal AS (
    INSERT INTO projection_outbox_cursors (projection_revision_id, tournament_id, roster_id, next_ordinal, updated_at)
    SELECT projection_revision_id, tournament_id, roster_id, 2, executed_at
    FROM target JOIN allocated_sequence ON true
    ON CONFLICT (projection_revision_id) DO UPDATE
    SET next_ordinal = projection_outbox_cursors.next_ordinal + 1, updated_at = EXCLUDED.updated_at
    WHERE projection_outbox_cursors.next_ordinal < 32768
        AND projection_outbox_cursors.tournament_id = EXCLUDED.tournament_id
        AND projection_outbox_cursors.roster_id = EXCLUDED.roster_id
    RETURNING next_ordinal - 1 AS ordinal
), event AS (
    INSERT INTO outbox_events (id, tournament_id, roster_id, projection_revision_id, projection_revision,
        sequence, projection_ordinal, terminal, idempotency_key, audience, topic, payload, created_at, available_at)
    SELECT sqlc.arg(outbox_event_id)::UUID, target.tournament_id, target.roster_id,
        target.projection_revision_id, target.revision_number, allocated_sequence.sequence, allocated_ordinal.ordinal,
        true, sqlc.arg(outbox_event_id)::UUID, 'all', 'tournament.playoffs.published', target.payload,
        target.executed_at AS created_at, target.executed_at AS available_at
    FROM target JOIN allocated_sequence ON true JOIN allocated_ordinal ON true
    RETURNING id, tournament_id, roster_id, projection_revision_id, projection_revision, projection_ordinal, created_at
)
INSERT INTO outbox_stage_projection_sources (outbox_event_id, tournament_id, roster_id, stage_command_id,
    projection_revision_id, projection_revision, projection_ordinal, swiss_receipt_projection_revision_id,
    top4_artifact_id, bracket_artifact_id, created_at)
SELECT event.id, event.tournament_id, event.roster_id, target.command_id,
    event.projection_revision_id, event.projection_revision, event.projection_ordinal,
    target.source_projection_revision_id, target.top4_artifact_id, target.bracket_artifact_id, event.created_at
FROM event JOIN target ON target.projection_revision_id = event.projection_revision_id
RETURNING outbox_event_id;

-- name: LockLatestFinalSwissReceipt :one
SELECT receipt.projection_revision_id, revision.revision_number
FROM final_swiss_projection_receipts AS receipt
INNER JOIN projection_revisions AS revision
    ON revision.id = receipt.projection_revision_id
    AND revision.tournament_id = receipt.tournament_id AND revision.roster_id = receipt.roster_id
WHERE receipt.tournament_id = sqlc.arg(tournament_id) AND receipt.roster_id = sqlc.arg(roster_id)
ORDER BY receipt.receipt_revision DESC
LIMIT 1
FOR KEY SHARE OF receipt, revision;

-- name: LockTournamentProgressionFinalSwissReceiptChain :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.receipt_revision,
        receipt.canonical_projection_id,
        receipt.previous_receipt_projection_revision_id,
        receipt.source_standings_artifact_id,
        receipt.source_standings_payload_digest,
        receipt.canonical_payload_digest,
        receipt.created_at,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.receipt_revision,
        predecessor.canonical_projection_id,
        predecessor.previous_receipt_projection_revision_id,
        predecessor.source_standings_artifact_id,
        predecessor.source_standings_payload_digest,
        predecessor.canonical_payload_digest,
        predecessor.created_at,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt.projection_revision_id,
    receipt.tournament_id,
    receipt.roster_id,
    receipt.receipt_revision,
    receipt.canonical_projection_id,
    receipt.previous_receipt_projection_revision_id,
    receipt.source_standings_artifact_id,
    receipt.source_standings_payload_digest,
    receipt.canonical_payload_digest,
    receipt.created_at,
    projection_revision.revision_number AS physical_projection_revision,
    artifact.payload AS source_standings_payload,
    artifact.payload_digest AS source_standings_artifact_digest,
    receipt_chain.lineage_cycle
FROM final_swiss_projection_receipts AS receipt
INNER JOIN receipt_chain
    ON receipt.projection_revision_id = receipt_chain.projection_revision_id
    AND receipt.tournament_id = receipt_chain.tournament_id
    AND receipt.roster_id = receipt_chain.roster_id
INNER JOIN projection_revisions AS projection_revision
    ON projection_revision.id = receipt.projection_revision_id
    AND projection_revision.tournament_id = receipt.tournament_id
    AND projection_revision.roster_id = receipt.roster_id
INNER JOIN projection_revision_artifacts AS membership
    ON membership.revision_id = projection_revision.id
    AND membership.tournament_id = projection_revision.tournament_id
    AND membership.roster_id = projection_revision.roster_id
    AND membership.artifact_kind = 'standings'
    AND membership.artifact_id = receipt.source_standings_artifact_id
INNER JOIN projection_artifacts AS artifact
    ON artifact.id = membership.artifact_id
    AND artifact.tournament_id = membership.tournament_id
    AND artifact.roster_id = membership.roster_id
    AND artifact.artifact_kind = membership.artifact_kind
ORDER BY receipt.receipt_revision
FOR KEY SHARE OF receipt, projection_revision, membership, artifact;

-- Every child read is scoped through the same canonical receipt chain. This
-- makes a missing predecessor child observable instead of silently using the
-- current mutable heads for an older canonical revision.
-- name: LockTournamentProgressionFinalSwissReceiptParticipants :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT participant.projection_revision_id,
    participant.participant_id,
    participant.stable_seed,
    participant.created_at
FROM final_swiss_projection_receipt_participants AS participant
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = participant.projection_revision_id
    AND receipt_chain.tournament_id = participant.tournament_id
    AND receipt_chain.roster_id = participant.roster_id
ORDER BY participant.projection_revision_id, participant.stable_seed, participant.participant_id
FOR KEY SHARE OF participant;

-- name: LockTournamentProgressionFinalSwissReceiptRounds :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt_round.projection_revision_id,
    receipt_round.round_id,
    receipt_round.round_number,
    receipt_round.created_at
FROM final_swiss_projection_receipt_rounds AS receipt_round
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = receipt_round.projection_revision_id
    AND receipt_chain.tournament_id = receipt_round.tournament_id
    AND receipt_chain.roster_id = receipt_round.roster_id
ORDER BY receipt_round.projection_revision_id, receipt_round.round_number, receipt_round.round_id
FOR KEY SHARE OF receipt_round;

-- name: LockTournamentProgressionFinalSwissReceiptSeries :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt_series.projection_revision_id,
    receipt_series.round_id,
    receipt_series.series_id,
    receipt_series.terminal_source,
    receipt_series.series_result_revision_id,
    receipt_series.score_revision_id,
    receipt_series.series_result_node_id,
    receipt_series.score_node_id,
    receipt_series.normal_no_show_commit_id,
    receipt_series.operator_forfeit_commit_id,
    receipt_series.created_at
FROM final_swiss_projection_receipt_series AS receipt_series
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
    AND receipt_chain.tournament_id = receipt_series.tournament_id
    AND receipt_chain.roster_id = receipt_series.roster_id
ORDER BY receipt_series.projection_revision_id, receipt_series.round_id, receipt_series.series_id
FOR KEY SHARE OF receipt_series;

-- name: LockTournamentProgressionFinalSwissReceiptGames :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt_game.projection_revision_id,
    receipt_game.series_id,
    receipt_game.game_attempt_id,
    receipt_game.game_result_revision_id,
    receipt_game.game_result_node_id,
    receipt_game.created_at
FROM final_swiss_projection_receipt_games AS receipt_game
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = receipt_game.projection_revision_id
    AND receipt_chain.tournament_id = receipt_game.tournament_id
    AND receipt_chain.roster_id = receipt_game.roster_id
ORDER BY receipt_game.projection_revision_id, receipt_game.series_id, receipt_game.game_attempt_id
FOR KEY SHARE OF receipt_game;

-- name: LockTournamentProgressionFinalSwissReceiptLedgerEntries :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt_ledger.projection_revision_id,
    receipt_ledger.ledger_entry_id,
    receipt_ledger.created_at
FROM final_swiss_projection_receipt_ledger_entries AS receipt_ledger
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = receipt_ledger.projection_revision_id
    AND receipt_chain.tournament_id = receipt_ledger.tournament_id
    AND receipt_chain.roster_id = receipt_ledger.roster_id
ORDER BY receipt_ledger.projection_revision_id, receipt_ledger.ledger_entry_id
FOR KEY SHARE OF receipt_ledger;

-- Historical receipt hydration deliberately joins the immutable IDs persisted
-- by a receipt. It never follows a mutable Series, score, result, or
-- correction head, so a corrected current projection cannot rewrite a prior
-- Final Swiss predecessor.
-- name: LockTournamentProgressionFinalSwissReceiptSeriesEvidence :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt_series.projection_revision_id,
    receipt_series.round_id,
    receipt_series.series_id,
    receipt_series.terminal_source,
    receipt_series.series_result_revision_id,
    receipt_series.score_revision_id,
    receipt_series.series_result_node_id,
    receipt_series.score_node_id,
    receipt_series.normal_no_show_commit_id,
    receipt_series.operator_forfeit_commit_id,
    series.first_participant_id,
    series.second_participant_id,
    series.format AS series_format,
    series_result.result_event_id AS series_result_event_id,
    series_result.previous_revision_id AS series_result_previous_revision_id,
    series_result.revision_number AS series_result_revision_number,
    series_result.result_state AS series_result_state,
    series_result.result_reason AS series_result_reason,
    series_result.winner_id AS series_winner_id,
    series_result.command_id AS series_result_command_id,
    series_result.actor_kind AS series_result_actor_kind,
    series_result.actor_id AS series_result_actor_id,
    series_result.source_projection_revision_id AS series_result_source_projection_revision_id,
    series_result.source_projection_revision AS series_result_source_projection_revision,
    series_result.created_at AS series_result_created_at,
    series_result_event.occurred_at AS series_result_occurred_at,
    score.result_event_id AS score_result_event_id,
    score.previous_revision_id AS score_previous_revision_id,
    score.revision_number AS score_revision_number,
    score.operation AS score_operation,
    score.command_id AS score_command_id,
    score.actor_kind AS score_actor_kind,
    score.actor_id AS score_actor_id,
    score.command_attempt_id AS score_command_attempt_id,
    score.source_projection_revision_id AS score_source_projection_revision_id,
    score.source_projection_revision AS score_source_projection_revision,
    score.first_participant_wins,
    score.second_participant_wins,
    score.created_at AS score_created_at,
    score_result_event.occurred_at AS score_occurred_at,
    series_node.previous_node_id AS series_result_previous_node_id,
    series_node.revision_number AS series_result_node_revision,
    series_node.payload AS series_result_payload,
    series_node.payload_digest AS series_result_payload_digest,
    score_node.previous_node_id AS score_previous_node_id,
    score_node.revision_number AS score_node_revision,
    score_node.payload AS score_payload,
    score_node.payload_digest AS score_payload_digest
FROM final_swiss_projection_receipt_series AS receipt_series
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
    AND receipt_chain.tournament_id = receipt_series.tournament_id
    AND receipt_chain.roster_id = receipt_series.roster_id
INNER JOIN series
    ON series.id = receipt_series.series_id
    AND series.tournament_id = receipt_series.tournament_id
    AND series.roster_id = receipt_series.roster_id
INNER JOIN official_result_revisions AS series_result
    ON series_result.id = receipt_series.series_result_revision_id
    AND series_result.series_id = receipt_series.series_id
    AND series_result.roster_id = receipt_series.roster_id
INNER JOIN result_events AS series_result_event
    ON series_result_event.id = series_result.result_event_id
    AND series_result_event.tournament_id = series_result.tournament_id
    AND series_result_event.roster_id = series_result.roster_id
    AND series_result_event.series_id = series_result.series_id
INNER JOIN series_score_revisions AS score
    ON score.id = receipt_series.score_revision_id
    AND score.series_id = receipt_series.series_id
    AND score.roster_id = receipt_series.roster_id
INNER JOIN result_events AS score_result_event
    ON score_result_event.id = score.result_event_id
    AND score_result_event.tournament_id = score.tournament_id
    AND score_result_event.roster_id = score.roster_id
    AND score_result_event.series_id = score.series_id
INNER JOIN result_projection_nodes AS series_node
    ON series_node.id = receipt_series.series_result_node_id
    AND series_node.tournament_id = receipt_series.tournament_id
    AND series_node.roster_id = receipt_series.roster_id
    AND series_node.artifact_kind = 'series_result'
    AND series_node.entity_id = receipt_series.series_id
    AND series_node.revision_number = series_result.revision_number
INNER JOIN result_projection_nodes AS score_node
    ON score_node.id = receipt_series.score_node_id
    AND score_node.tournament_id = receipt_series.tournament_id
    AND score_node.roster_id = receipt_series.roster_id
    AND score_node.artifact_kind = 'series_score'
    AND score_node.entity_id = receipt_series.series_id
    AND score_node.revision_number = score.revision_number
ORDER BY receipt_series.projection_revision_id, receipt_series.round_id, receipt_series.series_id
FOR KEY SHARE OF receipt_series,
    series,
    series_result,
    series_result_event,
    score,
    score_result_event,
    series_node,
    score_node;

-- name: LockTournamentProgressionFinalSwissReceiptScoreRevisionAttempts :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt_series.projection_revision_id,
    receipt_series.round_id,
    receipt_series.series_id,
    receipt_series.score_revision_id,
    attempt.position,
    attempt.slot_id,
    attempt.slot_position,
    attempt.game_attempt_id,
    attempt.attempt_number,
    attempt.game_result_revision_id,
    attempt.result_event_id,
    attempt.result_state,
    attempt.result_reason,
    attempt.winner_id,
    attempt.occurred_at,
    attempt.created_at
FROM final_swiss_projection_receipt_series AS receipt_series
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
    AND receipt_chain.tournament_id = receipt_series.tournament_id
    AND receipt_chain.roster_id = receipt_series.roster_id
INNER JOIN series_score_revision_attempts AS attempt
    ON attempt.score_revision_id = receipt_series.score_revision_id
    AND attempt.tournament_id = receipt_series.tournament_id
    AND attempt.roster_id = receipt_series.roster_id
    AND attempt.series_id = receipt_series.series_id
ORDER BY receipt_series.projection_revision_id, receipt_series.series_id, attempt.position
FOR KEY SHARE OF receipt_series, attempt;

-- name: LockTournamentProgressionFinalSwissReceiptScoreRevisionAdjudications :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt_series.projection_revision_id,
    receipt_series.round_id,
    receipt_series.series_id,
    receipt_series.score_revision_id,
    adjudication.operator_forfeit_commit_id,
    adjudication.command_id,
    adjudication.actor_id,
    adjudication.anchor_attempt_id,
    adjudication.forfeiting_participant_id,
    adjudication.winner_id,
    adjudication.source_projection_revision_id,
    adjudication.source_projection_revision,
    adjudication.created_at
FROM final_swiss_projection_receipt_series AS receipt_series
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
    AND receipt_chain.tournament_id = receipt_series.tournament_id
    AND receipt_chain.roster_id = receipt_series.roster_id
INNER JOIN series_score_revision_adjudications AS adjudication
    ON adjudication.score_revision_id = receipt_series.score_revision_id
    AND adjudication.tournament_id = receipt_series.tournament_id
    AND adjudication.roster_id = receipt_series.roster_id
    AND adjudication.series_id = receipt_series.series_id
WHERE receipt_series.terminal_source = 'pre_start_forfeit'
ORDER BY receipt_series.projection_revision_id, receipt_series.series_id, adjudication.score_revision_id
FOR KEY SHARE OF receipt_series, adjudication;

-- name: LockTournamentProgressionFinalSwissReceiptGameEvidence :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt_game.projection_revision_id,
    receipt_game.series_id,
    receipt_game.game_attempt_id,
    receipt_game.game_result_revision_id,
    receipt_game.game_result_node_id,
    game_slot.id AS slot_id,
    game_slot.slot_number,
    game_slot.category,
    game_slot.first_participant_wins_before,
    game_slot.second_participant_wins_before,
    game_attempt.attempt_number,
    game_attempt.state AS game_state,
    game_attempt.result_reason AS game_reason,
    game_attempt.winner_id AS game_winner_id,
    game_attempt.finished_at AS game_finished_at,
    game_result.previous_revision_id AS game_result_previous_revision_id,
    game_result.revision_number AS game_result_revision_number,
    game_result.result_state,
    game_result.result_reason,
    game_result.winner_id AS result_winner_id,
    game_result.created_at AS game_result_created_at,
    game_result.command_id AS game_result_command_id,
    game_result.actor_kind AS game_result_actor_kind,
    game_result.actor_id AS game_result_actor_id,
    game_result.source_projection_revision_id AS game_result_source_projection_revision_id,
    game_result.source_projection_revision AS game_result_source_projection_revision,
    result_event.submission_event_id,
    result_event.occurred_at AS result_occurred_at,
    submission_event.payload_digest AS submission_evidence_digest,
    game_node.previous_node_id AS game_result_previous_node_id,
    game_node.revision_number AS game_result_node_revision,
    game_node.payload AS game_result_payload,
    game_node.payload_digest AS game_result_payload_digest
FROM final_swiss_projection_receipt_games AS receipt_game
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = receipt_game.projection_revision_id
    AND receipt_chain.tournament_id = receipt_game.tournament_id
    AND receipt_chain.roster_id = receipt_game.roster_id
INNER JOIN game_attempts AS game_attempt
    ON game_attempt.id = receipt_game.game_attempt_id
    AND game_attempt.series_id = receipt_game.series_id
    AND game_attempt.roster_id = receipt_game.roster_id
INNER JOIN game_slots AS game_slot
    ON game_slot.id = game_attempt.slot_id
    AND game_slot.series_id = game_attempt.series_id
    AND game_slot.roster_id = game_attempt.roster_id
INNER JOIN official_result_revisions AS game_result
    ON game_result.id = receipt_game.game_result_revision_id
    AND game_result.entity_kind = 'game_attempt'
    AND game_result.entity_id = receipt_game.game_attempt_id
    AND game_result.series_id = receipt_game.series_id
    AND game_result.roster_id = receipt_game.roster_id
INNER JOIN result_events AS result_event
    ON result_event.id = game_result.result_event_id
    AND result_event.tournament_id = game_result.tournament_id
    AND result_event.roster_id = game_result.roster_id
    AND result_event.series_id = game_result.series_id
    AND result_event.attempt_id = receipt_game.game_attempt_id
LEFT JOIN submission_events AS submission_event
    ON submission_event.id = result_event.submission_event_id
    AND submission_event.tournament_id = result_event.tournament_id
    AND submission_event.roster_id = result_event.roster_id
    AND submission_event.series_id = result_event.series_id
    AND submission_event.attempt_id = result_event.attempt_id
INNER JOIN result_projection_nodes AS game_node
    ON game_node.id = receipt_game.game_result_node_id
    AND game_node.tournament_id = receipt_game.tournament_id
    AND game_node.roster_id = receipt_game.roster_id
    AND game_node.artifact_kind = 'game_result'
    AND game_node.entity_id = receipt_game.game_attempt_id
    AND game_node.revision_number = game_result.revision_number
ORDER BY receipt_game.projection_revision_id,
    receipt_game.series_id,
    game_slot.slot_number,
    game_attempt.attempt_number
FOR KEY SHARE OF receipt_game,
    game_attempt,
    game_slot,
    game_result,
    result_event,
    game_node;

-- name: LockTournamentProgressionFinalSwissReceiptNormalNoShowCommits :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt_series.projection_revision_id,
    receipt_series.round_id,
    receipt_series.series_id,
    normal_commit.id AS commit_id,
    normal_commit.wave_id,
    normal_commit.ready_window_id,
    normal_commit.ready_window_revision_id,
    normal_commit.result_event_id,
    normal_commit.series_score_revision_id,
    normal_commit.series_result_revision_id,
    normal_commit.command_id,
    normal_commit.action,
    normal_commit.resolved_at,
    normal_game.game_attempt_id,
    normal_game.game_result_revision_id,
    normal_game.position,
    projection_evidence.artifact_kinds,
    projection_evidence.payload_digest
FROM final_swiss_projection_receipt_series AS receipt_series
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
    AND receipt_chain.tournament_id = receipt_series.tournament_id
    AND receipt_chain.roster_id = receipt_series.roster_id
INNER JOIN normal_no_show_commits AS normal_commit
    ON normal_commit.id = receipt_series.normal_no_show_commit_id
    AND normal_commit.tournament_id = receipt_series.tournament_id
    AND normal_commit.roster_id = receipt_series.roster_id
    AND normal_commit.series_id = receipt_series.series_id
INNER JOIN normal_no_show_commit_games AS normal_game
    ON normal_game.commit_id = normal_commit.id
INNER JOIN result_projection_evidence AS projection_evidence
    ON projection_evidence.id = normal_commit.projection_evidence_id
    AND projection_evidence.tournament_id = normal_commit.tournament_id
    AND projection_evidence.roster_id = normal_commit.roster_id
    AND projection_evidence.series_id = normal_commit.series_id
    AND projection_evidence.result_event_id = normal_commit.result_event_id
WHERE receipt_series.terminal_source = 'normal_no_show'
ORDER BY receipt_series.projection_revision_id, receipt_series.series_id, normal_game.position
FOR KEY SHARE OF receipt_series, normal_commit, normal_game, projection_evidence;

-- name: LockTournamentProgressionFinalSwissReceiptOperatorForfeitCommits :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt_series.projection_revision_id,
    receipt_series.round_id,
    receipt_series.series_id,
    forfeit_commit.id AS commit_id,
    forfeit_commit.anchor_attempt_id,
    forfeit_commit.result_event_id,
    forfeit_commit.series_score_revision_id,
    forfeit_commit.series_result_revision_id,
    forfeit_commit.command_id,
    forfeit_commit.actor_id,
    forfeit_commit.forfeiting_participant_id,
    forfeit_commit.source_projection_revision_id,
    forfeit_commit.source_projection_revision,
    forfeit_commit.rule_id,
    forfeit_commit.reason,
    forfeit_commit.evidence_ids,
    forfeit_commit.resolved_at,
    projection_evidence.artifact_kinds,
    projection_evidence.payload_digest
FROM final_swiss_projection_receipt_series AS receipt_series
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
    AND receipt_chain.tournament_id = receipt_series.tournament_id
    AND receipt_chain.roster_id = receipt_series.roster_id
INNER JOIN operator_forfeit_commits AS forfeit_commit
    ON forfeit_commit.id = receipt_series.operator_forfeit_commit_id
    AND forfeit_commit.tournament_id = receipt_series.tournament_id
    AND forfeit_commit.roster_id = receipt_series.roster_id
    AND forfeit_commit.series_id = receipt_series.series_id
INNER JOIN result_projection_evidence AS projection_evidence
    ON projection_evidence.id = forfeit_commit.projection_evidence_id
    AND projection_evidence.tournament_id = forfeit_commit.tournament_id
    AND projection_evidence.roster_id = forfeit_commit.roster_id
    AND projection_evidence.series_id = forfeit_commit.series_id
    AND projection_evidence.result_event_id = forfeit_commit.result_event_id
WHERE receipt_series.terminal_source = 'pre_start_forfeit'
ORDER BY receipt_series.projection_revision_id, receipt_series.series_id
FOR KEY SHARE OF receipt_series, forfeit_commit, projection_evidence;

-- name: LockTournamentProgressionFinalSwissReceiptProjectionNodes :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
), receipt_nodes AS (
    SELECT receipt_series.projection_revision_id,
        receipt_series.tournament_id,
        receipt_series.roster_id,
        receipt_series.series_result_node_id AS node_id
    FROM final_swiss_projection_receipt_series AS receipt_series
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
        AND receipt_chain.tournament_id = receipt_series.tournament_id
        AND receipt_chain.roster_id = receipt_series.roster_id

    UNION

    SELECT receipt_series.projection_revision_id,
        receipt_series.tournament_id,
        receipt_series.roster_id,
        receipt_series.score_node_id AS node_id
    FROM final_swiss_projection_receipt_series AS receipt_series
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
        AND receipt_chain.tournament_id = receipt_series.tournament_id
        AND receipt_chain.roster_id = receipt_series.roster_id

    UNION

    SELECT receipt_game.projection_revision_id,
        receipt_game.tournament_id,
        receipt_game.roster_id,
        receipt_game.game_result_node_id AS node_id
    FROM final_swiss_projection_receipt_games AS receipt_game
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_game.projection_revision_id
        AND receipt_chain.tournament_id = receipt_game.tournament_id
        AND receipt_chain.roster_id = receipt_game.roster_id
)
SELECT receipt_nodes.projection_revision_id,
    node.id,
    node.authority_id,
    node.artifact_kind,
    node.entity_id,
    node.revision_number,
    node.previous_node_id,
    node.payload,
    node.payload_digest,
    node.created_at
FROM receipt_nodes
INNER JOIN result_projection_nodes AS node
    ON node.id = receipt_nodes.node_id
    AND node.tournament_id = receipt_nodes.tournament_id
    AND node.roster_id = receipt_nodes.roster_id
ORDER BY receipt_nodes.projection_revision_id,
    node.artifact_kind,
    node.entity_id,
    node.revision_number,
    node.id
FOR KEY SHARE OF node;

-- name: LockTournamentProgressionFinalSwissReceiptProjectionDependencies :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
), receipt_nodes AS (
    SELECT receipt_series.tournament_id,
        receipt_series.roster_id,
        receipt_series.series_result_node_id AS node_id
    FROM final_swiss_projection_receipt_series AS receipt_series
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
        AND receipt_chain.tournament_id = receipt_series.tournament_id
        AND receipt_chain.roster_id = receipt_series.roster_id

    UNION

    SELECT receipt_series.tournament_id,
        receipt_series.roster_id,
        receipt_series.score_node_id AS node_id
    FROM final_swiss_projection_receipt_series AS receipt_series
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_series.projection_revision_id
        AND receipt_chain.tournament_id = receipt_series.tournament_id
        AND receipt_chain.roster_id = receipt_series.roster_id

    UNION

    SELECT receipt_game.tournament_id,
        receipt_game.roster_id,
        receipt_game.game_result_node_id AS node_id
    FROM final_swiss_projection_receipt_games AS receipt_game
    INNER JOIN receipt_chain
        ON receipt_chain.projection_revision_id = receipt_game.projection_revision_id
        AND receipt_chain.tournament_id = receipt_game.tournament_id
        AND receipt_chain.roster_id = receipt_game.roster_id
)
SELECT dependency.source_node_id,
    dependency.derived_node_id
FROM result_projection_dependencies AS dependency
INNER JOIN receipt_nodes AS source_receipt_node
    ON source_receipt_node.node_id = dependency.source_node_id
INNER JOIN receipt_nodes AS derived_receipt_node
    ON derived_receipt_node.node_id = dependency.derived_node_id
    AND derived_receipt_node.tournament_id = source_receipt_node.tournament_id
    AND derived_receipt_node.roster_id = source_receipt_node.roster_id
INNER JOIN result_projection_nodes AS source_node
    ON source_node.id = dependency.source_node_id
    AND source_node.tournament_id = source_receipt_node.tournament_id
    AND source_node.roster_id = source_receipt_node.roster_id
INNER JOIN result_projection_nodes AS derived_node
    ON derived_node.id = dependency.derived_node_id
    AND derived_node.tournament_id = derived_receipt_node.tournament_id
    AND derived_node.roster_id = derived_receipt_node.roster_id
ORDER BY dependency.source_node_id, dependency.derived_node_id
FOR KEY SHARE OF dependency, source_node, derived_node;

-- name: LockTournamentProgressionFinalSwissReceiptLedger :many
WITH RECURSIVE receipt_chain AS (
    SELECT receipt.projection_revision_id,
        receipt.tournament_id,
        receipt.roster_id,
        receipt.previous_receipt_projection_revision_id,
        ARRAY[receipt.projection_revision_id]::uuid[] AS lineage,
        FALSE AS lineage_cycle
    FROM final_swiss_projection_receipts AS receipt
    WHERE receipt.projection_revision_id = sqlc.arg(projection_revision_id)
        AND receipt.tournament_id = sqlc.arg(tournament_id)
        AND receipt.roster_id = sqlc.arg(roster_id)

    UNION ALL

    SELECT predecessor.projection_revision_id,
        predecessor.tournament_id,
        predecessor.roster_id,
        predecessor.previous_receipt_projection_revision_id,
        receipt_chain.lineage || predecessor.projection_revision_id,
        predecessor.projection_revision_id = ANY(receipt_chain.lineage)
    FROM receipt_chain
    INNER JOIN final_swiss_projection_receipts AS predecessor
        ON predecessor.projection_revision_id = receipt_chain.previous_receipt_projection_revision_id
        AND predecessor.tournament_id = receipt_chain.tournament_id
        AND predecessor.roster_id = receipt_chain.roster_id
    WHERE NOT receipt_chain.lineage_cycle
)
SELECT receipt_ledger.projection_revision_id,
    ledger.id AS ledger_entry_id,
    ledger.round_id,
    ledger.round_number,
    ledger.source_kind,
    ledger.source_series_id,
    ledger.series_result_revision_id,
    ledger.bye_revision_id,
    ledger.result_label,
    ledger.participant_id,
    ledger.opponent_id,
    ledger.points,
    ledger.effective_time_ns,
    ledger.accepted_solve_time_ns,
    ledger.stable_seed,
    ledger.created_at
FROM final_swiss_projection_receipt_ledger_entries AS receipt_ledger
INNER JOIN receipt_chain
    ON receipt_chain.projection_revision_id = receipt_ledger.projection_revision_id
    AND receipt_chain.tournament_id = receipt_ledger.tournament_id
    AND receipt_chain.roster_id = receipt_ledger.roster_id
INNER JOIN swiss_point_ledger_entries AS ledger
    ON ledger.id = receipt_ledger.ledger_entry_id
    AND ledger.tournament_id = receipt_ledger.tournament_id
    AND ledger.roster_id = receipt_ledger.roster_id
ORDER BY receipt_ledger.projection_revision_id,
    ledger.round_number,
    ledger.round_id,
    ledger.source_series_id NULLS LAST,
    ledger.participant_id
FOR KEY SHARE OF receipt_ledger, ledger;

-- name: LockTournamentProgressionParticipants :many
SELECT participant.id,
    participant.seed
FROM participants AS participant
WHERE participant.roster_id = sqlc.arg(roster_id)
ORDER BY participant.seed, participant.id
FOR UPDATE;

-- name: LockTournamentProgressionCurrentStandings :many
SELECT revision.id AS projection_revision_id,
    revision.revision_number AS projection_revision,
    revision.state AS projection_state,
    revision.superseded_by_revision_id,
    artifact.id AS artifact_id,
    artifact.payload,
    artifact.payload_digest,
    member.participant_id,
    member.position,
    member.score
FROM projection_revisions AS revision
INNER JOIN projection_revision_artifacts AS revision_artifact
    ON revision_artifact.revision_id = revision.id
    AND revision_artifact.tournament_id = revision.tournament_id
    AND revision_artifact.roster_id = revision.roster_id
    AND revision_artifact.artifact_kind = 'standings'
INNER JOIN projection_artifacts AS artifact
    ON artifact.id = revision_artifact.artifact_id
    AND artifact.tournament_id = revision.tournament_id
    AND artifact.roster_id = revision.roster_id
    AND artifact.artifact_kind = 'standings'
INNER JOIN projection_artifact_members AS member
    ON member.artifact_id = artifact.id
    AND member.tournament_id = artifact.tournament_id
    AND member.roster_id = artifact.roster_id
    AND member.artifact_kind = artifact.artifact_kind
WHERE revision.id = sqlc.arg(projection_revision_id)
    AND revision.tournament_id = sqlc.arg(tournament_id)
    AND revision.roster_id = sqlc.arg(roster_id)
    AND revision.revision_number = sqlc.arg(projection_revision)
ORDER BY member.position, member.participant_id
FOR UPDATE OF revision, revision_artifact, artifact, member;

-- An unchanged correction publishes a new projection without rewriting the
-- immutable Golden group revisions. Follow correction predecessors until the
-- nearest projection that owns the active Golden authority is found.
-- name: ResolveTournamentProgressionGoldenSource :one
WITH RECURSIVE projection_lineage AS (
    SELECT sqlc.arg(projection_revision_id)::UUID AS projection_revision_id,
        sqlc.arg(projection_revision)::BIGINT AS projection_revision,
        0::BIGINT AS depth

    UNION ALL

    SELECT correction.source_projection_revision_id,
        correction.source_projection_revision,
        lineage.depth + 1
    FROM projection_lineage AS lineage
    INNER JOIN result_correction_commits AS correction
        ON correction.tournament_id = sqlc.arg(tournament_id)
        AND correction.roster_id = sqlc.arg(roster_id)
        AND correction.resulting_projection_revision_id = lineage.projection_revision_id
        AND correction.resulting_projection_revision = lineage.projection_revision
)
SELECT lineage.projection_revision_id,
    lineage.projection_revision
FROM projection_lineage AS lineage
WHERE EXISTS (
    SELECT 1
    FROM golden_group_revisions AS group_revision
    WHERE group_revision.tournament_id = sqlc.arg(tournament_id)
        AND group_revision.roster_id = sqlc.arg(roster_id)
        AND group_revision.source_projection_revision_id = lineage.projection_revision_id
        AND group_revision.source_projection_revision = lineage.projection_revision
)
ORDER BY lineage.depth
LIMIT 1;

-- name: LockTournamentProgressionGoldenSettlements :many
SELECT group_revision.revision_id AS group_revision_id,
    group_revision.group_id,
    group_revision.source_projection_revision_id,
    group_revision.source_projection_revision,
    COALESCE((group_revision.definition ->> 'revision_no')::integer, 1)::integer AS group_revision_number,
    group_revision.position_from,
    group_revision.position_to,
    attempt_group.attempt_id,
    attempt.state AS attempt_state,
    position_commit.id AS position_commit_id,
    position_commit.participant_id,
    position_commit.position,
    runtime.settlement_revision_id,
    runtime.finalized_at AS runtime_finalized_at
FROM golden_group_revisions AS group_revision
LEFT JOIN golden_attempt_stage_groups AS attempt_group
    ON attempt_group.group_revision_id = group_revision.revision_id
    AND attempt_group.tournament_id = group_revision.tournament_id
    AND attempt_group.roster_id = group_revision.roster_id
LEFT JOIN golden_attempts AS attempt
    ON attempt.id = attempt_group.attempt_id
    AND attempt.tournament_id = attempt_group.tournament_id
    AND attempt.roster_id = attempt_group.roster_id
LEFT JOIN golden_position_commits AS position_commit
    ON position_commit.attempt_id = attempt.id
    AND position_commit.tournament_id = attempt.tournament_id
    AND position_commit.roster_id = attempt.roster_id
LEFT JOIN golden_runtime_assignments AS runtime
    ON runtime.attempt_id = attempt_group.attempt_id
    AND runtime.tournament_id = attempt_group.tournament_id
    AND runtime.roster_id = attempt_group.roster_id
    AND runtime.group_revision_id = group_revision.revision_id
WHERE group_revision.tournament_id = sqlc.arg(tournament_id)
    AND group_revision.roster_id = sqlc.arg(roster_id)
    AND group_revision.source_projection_revision_id = sqlc.arg(source_projection_revision_id)
    AND group_revision.source_projection_revision = sqlc.arg(source_projection_revision)
ORDER BY group_revision.position_from,
    group_revision.position_to,
    attempt_group.attempt_id,
    position_commit.position
FOR UPDATE OF group_revision;

-- Golden attempts are locked after their group revisions. A missing attempt
-- remains observable in the preceding query and cannot be converted to a
-- terminal settlement by this query.
-- name: LockTournamentProgressionGoldenAttempts :many
SELECT group_revision.revision_id AS group_revision_id,
    attempt_group.attempt_id,
    attempt.state AS attempt_state
FROM golden_group_revisions AS group_revision
INNER JOIN golden_attempt_stage_groups AS attempt_group
    ON attempt_group.group_revision_id = group_revision.revision_id
    AND attempt_group.tournament_id = group_revision.tournament_id
    AND attempt_group.roster_id = group_revision.roster_id
INNER JOIN golden_attempts AS attempt
    ON attempt.id = attempt_group.attempt_id
    AND attempt.tournament_id = attempt_group.tournament_id
    AND attempt.roster_id = attempt_group.roster_id
WHERE group_revision.tournament_id = sqlc.arg(tournament_id)
    AND group_revision.roster_id = sqlc.arg(roster_id)
    AND group_revision.source_projection_revision_id = sqlc.arg(source_projection_revision_id)
    AND group_revision.source_projection_revision = sqlc.arg(source_projection_revision)
ORDER BY group_revision.position_from, group_revision.position_to, attempt_group.attempt_id
FOR UPDATE OF attempt_group, attempt;

-- Position commits are locked after their bound Golden attempts.
-- name: LockTournamentProgressionGoldenPositionCommits :many
SELECT group_revision.revision_id AS group_revision_id,
    position_commit.id AS position_commit_id,
    position_commit.attempt_id,
    position_commit.participant_id,
    position_commit.position
FROM golden_group_revisions AS group_revision
INNER JOIN golden_attempt_stage_groups AS attempt_group
    ON attempt_group.group_revision_id = group_revision.revision_id
    AND attempt_group.tournament_id = group_revision.tournament_id
    AND attempt_group.roster_id = group_revision.roster_id
INNER JOIN golden_position_commits AS position_commit
    ON position_commit.attempt_id = attempt_group.attempt_id
    AND position_commit.tournament_id = attempt_group.tournament_id
    AND position_commit.roster_id = attempt_group.roster_id
WHERE group_revision.tournament_id = sqlc.arg(tournament_id)
    AND group_revision.roster_id = sqlc.arg(roster_id)
    AND group_revision.source_projection_revision_id = sqlc.arg(source_projection_revision_id)
    AND group_revision.source_projection_revision = sqlc.arg(source_projection_revision)
ORDER BY group_revision.position_from, group_revision.position_to, position_commit.position
FOR UPDATE OF position_commit;

-- A writer persists the normalized receipt only after the replacement
-- projection is published and the exact predecessor has been superseded. The
-- deferred schema guard validates the complete child evidence at commit.
-- name: CreateFinalSwissProjectionReceipt :one
INSERT INTO final_swiss_projection_receipts (
    projection_revision_id,
    tournament_id,
    roster_id,
    receipt_revision,
    canonical_projection_id,
    previous_receipt_projection_revision_id,
    source_standings_artifact_id,
    source_standings_payload_digest,
    canonical_payload_digest,
    created_at
)
VALUES (
    sqlc.arg(projection_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(receipt_revision),
    sqlc.arg(canonical_projection_id),
    sqlc.narg(previous_receipt_projection_revision_id)::uuid,
    sqlc.arg(source_standings_artifact_id),
    sqlc.arg(source_standings_payload_digest),
    sqlc.arg(canonical_payload_digest),
    sqlc.arg(created_at)
)
RETURNING projection_revision_id;

-- name: CreateFinalSwissProjectionReceiptParticipant :one
INSERT INTO final_swiss_projection_receipt_participants (
    projection_revision_id,
    tournament_id,
    roster_id,
    participant_id,
    stable_seed,
    created_at
)
VALUES (
    sqlc.arg(projection_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(stable_seed),
    sqlc.arg(created_at)
)
RETURNING participant_id;

-- name: CreateFinalSwissProjectionReceiptRound :one
INSERT INTO final_swiss_projection_receipt_rounds (
    projection_revision_id,
    tournament_id,
    roster_id,
    round_id,
    round_number,
    created_at
)
VALUES (
    sqlc.arg(projection_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(round_id),
    sqlc.arg(round_number),
    sqlc.arg(created_at)
)
RETURNING round_id;

-- name: CreateFinalSwissProjectionReceiptSeries :one
INSERT INTO final_swiss_projection_receipt_series (
    projection_revision_id,
    tournament_id,
    roster_id,
    round_id,
    series_id,
    terminal_source,
    series_result_revision_id,
    score_revision_id,
    series_result_node_id,
    score_node_id,
    normal_no_show_commit_id,
    operator_forfeit_commit_id,
    created_at
)
VALUES (
    sqlc.arg(projection_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(round_id),
    sqlc.arg(series_id),
    sqlc.arg(terminal_source),
    sqlc.arg(series_result_revision_id),
    sqlc.arg(score_revision_id),
    sqlc.arg(series_result_node_id),
    sqlc.arg(score_node_id),
    sqlc.narg(normal_no_show_commit_id)::uuid,
    sqlc.narg(operator_forfeit_commit_id)::uuid,
    sqlc.arg(created_at)
)
RETURNING series_id;

-- name: LockFinalSwissPublicationSeries :many
WITH receipt_series AS (
    SELECT sqlc.arg(projection_revision_id)::uuid AS projection_revision_id,
        series.tournament_id,
        series.roster_id,
        proof.round_id,
        series.id AS series_id,
        CASE
            WHEN normal_commit.id IS NOT NULL THEN 'normal_no_show'
            WHEN forfeit.id IS NOT NULL THEN 'pre_start_forfeit'
            ELSE 'played'
        END::text AS terminal_source,
        result_head.current_revision_id AS series_result_revision_id,
        score_head.current_revision_id AS score_revision_id,
        COALESCE(result_binding.node_id, result_head.current_revision_id)::uuid AS series_result_node_id,
        COALESCE(score_binding.node_id, score_head.current_revision_id)::uuid AS score_node_id,
        normal_commit.id AS normal_no_show_commit_id,
        forfeit.id AS operator_forfeit_commit_id
    FROM swiss_round_lock_proof_series AS proof
    INNER JOIN series ON series.id = proof.series_id AND series.roster_id = proof.roster_id
    INNER JOIN official_result_heads AS result_head
        ON result_head.entity_kind = 'series' AND result_head.entity_id = series.id
        AND result_head.roster_id = series.roster_id
        AND result_head.current_revision_id = series.current_result_revision_id
    INNER JOIN series_score_heads AS score_head
        ON score_head.series_id = series.id AND score_head.roster_id = series.roster_id
        AND score_head.current_revision_id = series.current_score_revision_id
    LEFT JOIN correction_projection_bindings AS result_binding
        ON result_binding.tournament_id = series.tournament_id AND result_binding.roster_id = series.roster_id
        AND result_binding.artifact_kind = 'series_result' AND result_binding.entity_id = series.id
        AND result_binding.source_id = result_head.current_revision_id
    LEFT JOIN correction_projection_bindings AS score_binding
        ON score_binding.tournament_id = series.tournament_id AND score_binding.roster_id = series.roster_id
        AND score_binding.artifact_kind = 'series_score' AND score_binding.entity_id = series.id
        AND score_binding.source_id = score_head.current_revision_id
    LEFT JOIN normal_no_show_commits AS normal_commit
        ON normal_commit.series_id = series.id AND normal_commit.roster_id = series.roster_id
        AND normal_commit.series_result_revision_id = result_head.current_revision_id
    LEFT JOIN operator_forfeit_commits AS forfeit
        ON forfeit.series_id = series.id AND forfeit.roster_id = series.roster_id
        AND forfeit.series_result_revision_id = result_head.current_revision_id
    WHERE series.tournament_id = sqlc.arg(tournament_id) AND series.roster_id = sqlc.arg(roster_id)
)
SELECT receipt_series.projection_revision_id,
    receipt_series.round_id,
    receipt_series.series_id,
    receipt_series.terminal_source,
    receipt_series.series_result_revision_id,
    receipt_series.score_revision_id,
    receipt_series.series_result_node_id,
    receipt_series.score_node_id,
    receipt_series.normal_no_show_commit_id,
    receipt_series.operator_forfeit_commit_id,
    series.first_participant_id,
    series.second_participant_id,
    series.format AS series_format,
    series_result.result_event_id AS series_result_event_id,
    series_result.previous_revision_id AS series_result_previous_revision_id,
    series_result.revision_number AS series_result_revision_number,
    series_result.result_state AS series_result_state,
    series_result.result_reason AS series_result_reason,
    series_result.winner_id AS series_winner_id,
    series_result.command_id AS series_result_command_id,
    series_result.actor_kind AS series_result_actor_kind,
    series_result.actor_id AS series_result_actor_id,
    series_result.source_projection_revision_id AS series_result_source_projection_revision_id,
    series_result.source_projection_revision AS series_result_source_projection_revision,
    series_result.created_at AS series_result_created_at,
    series_result_event.occurred_at AS series_result_occurred_at,
    score.result_event_id AS score_result_event_id,
    score.previous_revision_id AS score_previous_revision_id,
    score.revision_number AS score_revision_number,
    score.operation AS score_operation,
    score.command_id AS score_command_id,
    score.actor_kind AS score_actor_kind,
    score.actor_id AS score_actor_id,
    score.command_attempt_id AS score_command_attempt_id,
    score.source_projection_revision_id AS score_source_projection_revision_id,
    score.source_projection_revision AS score_source_projection_revision,
    score.first_participant_wins,
    score.second_participant_wins,
    score.created_at AS score_created_at,
    score_result_event.occurred_at AS score_occurred_at,
    series_node.previous_node_id AS series_result_previous_node_id,
    series_node.revision_number AS series_result_node_revision,
    series_node.payload AS series_result_payload,
    series_node.payload_digest AS series_result_payload_digest,
    score_node.previous_node_id AS score_previous_node_id,
    score_node.revision_number AS score_node_revision,
    score_node.payload AS score_payload,
    score_node.payload_digest AS score_payload_digest
FROM receipt_series
INNER JOIN series
    ON series.id = receipt_series.series_id
    AND series.tournament_id = receipt_series.tournament_id
    AND series.roster_id = receipt_series.roster_id
INNER JOIN official_result_revisions AS series_result
    ON series_result.id = receipt_series.series_result_revision_id
    AND series_result.series_id = receipt_series.series_id
    AND series_result.roster_id = receipt_series.roster_id
INNER JOIN result_events AS series_result_event
    ON series_result_event.id = series_result.result_event_id
    AND series_result_event.tournament_id = series_result.tournament_id
    AND series_result_event.roster_id = series_result.roster_id
    AND series_result_event.series_id = series_result.series_id
INNER JOIN series_score_revisions AS score
    ON score.id = receipt_series.score_revision_id
    AND score.series_id = receipt_series.series_id
    AND score.roster_id = receipt_series.roster_id
INNER JOIN result_events AS score_result_event
    ON score_result_event.id = score.result_event_id
    AND score_result_event.tournament_id = score.tournament_id
    AND score_result_event.roster_id = score.roster_id
    AND score_result_event.series_id = score.series_id
INNER JOIN result_projection_nodes AS series_node
    ON series_node.id = receipt_series.series_result_node_id
    AND series_node.tournament_id = receipt_series.tournament_id
    AND series_node.roster_id = receipt_series.roster_id
    AND series_node.artifact_kind = 'series_result'
    AND series_node.entity_id = receipt_series.series_id
    AND series_node.revision_number = series_result.revision_number
INNER JOIN result_projection_nodes AS score_node
    ON score_node.id = receipt_series.score_node_id
    AND score_node.tournament_id = receipt_series.tournament_id
    AND score_node.roster_id = receipt_series.roster_id
    AND score_node.artifact_kind = 'series_score'
    AND score_node.entity_id = receipt_series.series_id
    AND score_node.revision_number = score.revision_number
ORDER BY receipt_series.projection_revision_id, receipt_series.round_id, receipt_series.series_id
FOR KEY SHARE OF series,
    series_result,
    series_result_event,
    score,
    score_result_event,
    series_node,
    score_node;

-- name: LockFinalSwissPublicationGames :many
WITH receipt_game AS (
    SELECT sqlc.arg(projection_revision_id)::uuid AS projection_revision_id,
        series.tournament_id,
        series.roster_id,
        series.id AS series_id,
        attempt.id AS game_attempt_id,
        head.current_revision_id AS game_result_revision_id,
        COALESCE(binding.node_id, head.current_revision_id)::uuid AS game_result_node_id
    FROM swiss_round_lock_proof_series AS proof
    INNER JOIN series ON series.id = proof.series_id AND series.roster_id = proof.roster_id
    INNER JOIN game_attempts AS attempt ON attempt.series_id = series.id AND attempt.roster_id = series.roster_id
    INNER JOIN official_result_heads AS head
        ON head.entity_kind = 'game_attempt' AND head.entity_id = attempt.id
        AND head.series_id = series.id AND head.roster_id = series.roster_id
        AND head.current_revision_id = attempt.result_revision_id
    LEFT JOIN correction_projection_bindings AS binding
        ON binding.tournament_id = series.tournament_id AND binding.roster_id = series.roster_id
        AND binding.artifact_kind = 'game_result' AND binding.entity_id = attempt.id
        AND binding.source_id = head.current_revision_id
    WHERE series.tournament_id = sqlc.arg(tournament_id) AND series.roster_id = sqlc.arg(roster_id)
)
SELECT receipt_game.projection_revision_id,
    receipt_game.series_id,
    receipt_game.game_attempt_id,
    receipt_game.game_result_revision_id,
    receipt_game.game_result_node_id,
    game_slot.id AS slot_id,
    game_slot.slot_number,
    game_slot.category,
    game_slot.first_participant_wins_before,
    game_slot.second_participant_wins_before,
    game_attempt.attempt_number,
    game_attempt.state AS game_state,
    game_attempt.result_reason AS game_reason,
    game_attempt.winner_id AS game_winner_id,
    game_attempt.finished_at AS game_finished_at,
    game_result.previous_revision_id AS game_result_previous_revision_id,
    game_result.revision_number AS game_result_revision_number,
    game_result.result_state,
    game_result.result_reason,
    game_result.winner_id AS result_winner_id,
    game_result.created_at AS game_result_created_at,
    game_result.command_id AS game_result_command_id,
    game_result.actor_kind AS game_result_actor_kind,
    game_result.actor_id AS game_result_actor_id,
    game_result.source_projection_revision_id AS game_result_source_projection_revision_id,
    game_result.source_projection_revision AS game_result_source_projection_revision,
    result_event.submission_event_id,
    result_event.occurred_at AS result_occurred_at,
    submission_event.payload_digest AS submission_evidence_digest,
    game_node.previous_node_id AS game_result_previous_node_id,
    game_node.revision_number AS game_result_node_revision,
    game_node.payload AS game_result_payload,
    game_node.payload_digest AS game_result_payload_digest
FROM receipt_game
INNER JOIN game_attempts AS game_attempt
    ON game_attempt.id = receipt_game.game_attempt_id
    AND game_attempt.series_id = receipt_game.series_id
    AND game_attempt.roster_id = receipt_game.roster_id
INNER JOIN game_slots AS game_slot
    ON game_slot.id = game_attempt.slot_id
    AND game_slot.series_id = game_attempt.series_id
    AND game_slot.roster_id = game_attempt.roster_id
INNER JOIN official_result_revisions AS game_result
    ON game_result.id = receipt_game.game_result_revision_id
    AND game_result.entity_kind = 'game_attempt'
    AND game_result.entity_id = receipt_game.game_attempt_id
    AND game_result.series_id = receipt_game.series_id
    AND game_result.roster_id = receipt_game.roster_id
INNER JOIN result_events AS result_event
    ON result_event.id = game_result.result_event_id
    AND result_event.tournament_id = game_result.tournament_id
    AND result_event.roster_id = game_result.roster_id
    AND result_event.series_id = game_result.series_id
    AND result_event.attempt_id = receipt_game.game_attempt_id
LEFT JOIN submission_events AS submission_event
    ON submission_event.id = result_event.submission_event_id
    AND submission_event.tournament_id = result_event.tournament_id
    AND submission_event.roster_id = result_event.roster_id
    AND submission_event.series_id = result_event.series_id
    AND submission_event.attempt_id = result_event.attempt_id
INNER JOIN result_projection_nodes AS game_node
    ON game_node.id = receipt_game.game_result_node_id
    AND game_node.tournament_id = receipt_game.tournament_id
    AND game_node.roster_id = receipt_game.roster_id
    AND game_node.artifact_kind = 'game_result'
    AND game_node.entity_id = receipt_game.game_attempt_id
    AND game_node.revision_number = game_result.revision_number
ORDER BY receipt_game.projection_revision_id,
    receipt_game.series_id,
    game_slot.slot_number,
    game_attempt.attempt_number
FOR KEY SHARE OF game_attempt,
    game_slot,
    game_result,
    result_event,
    game_node;

-- name: LockFinalSwissPublicationNodes :many
WITH selected_series AS (
    SELECT series.*
    FROM swiss_round_lock_proof_series AS proof
    INNER JOIN series ON series.id = proof.series_id AND series.roster_id = proof.roster_id
    WHERE series.tournament_id = sqlc.arg(tournament_id) AND series.roster_id = sqlc.arg(roster_id)
), heads AS (
    SELECT 'series_result'::text AS head_kind, series.id AS entity_id, head.current_revision_id AS source_id
    FROM selected_series AS series
    INNER JOIN official_result_heads AS head
        ON head.entity_kind = 'series' AND head.entity_id = series.id
        AND head.series_id = series.id AND head.roster_id = series.roster_id
        AND head.current_revision_id = series.current_result_revision_id
    UNION ALL
    SELECT 'series_score'::text, series.id, head.current_revision_id
    FROM selected_series AS series
    INNER JOIN series_score_heads AS head
        ON head.series_id = series.id AND head.roster_id = series.roster_id
        AND head.current_revision_id = series.current_score_revision_id
    UNION ALL
    SELECT 'game_result'::text, attempt.id, head.current_revision_id
    FROM selected_series AS series
    INNER JOIN game_attempts AS attempt ON attempt.series_id = series.id AND attempt.roster_id = series.roster_id
    INNER JOIN official_result_heads AS head
        ON head.entity_kind = 'game_attempt' AND head.entity_id = attempt.id
        AND head.series_id = series.id AND head.roster_id = series.roster_id
        AND head.current_revision_id = attempt.result_revision_id
)
SELECT sqlc.arg(projection_revision_id)::uuid AS receipt_projection_revision_id,
    heads.head_kind,
    node.entity_id,
    heads.source_id,
    node.id AS node_id,
    node.authority_id,
    authority.source_kind,
    node.artifact_kind,
    node.revision_number,
    node.previous_node_id,
    node.payload,
    node.payload_digest,
    node.created_at AS node_created_at,
    binding.command_id AS correction_command_id,
    binding.created_at AS binding_created_at
FROM heads
LEFT JOIN correction_projection_bindings AS binding
    ON binding.source_id = heads.source_id AND binding.artifact_kind = heads.head_kind
    AND binding.entity_id = heads.entity_id AND binding.tournament_id = sqlc.arg(tournament_id)
    AND binding.roster_id = sqlc.arg(roster_id)
INNER JOIN result_projection_nodes AS node
    ON node.id = COALESCE(binding.node_id, heads.source_id)
    AND node.artifact_kind = heads.head_kind AND node.entity_id = heads.entity_id
INNER JOIN result_projection_node_authorities AS authority
    ON authority.id = node.authority_id AND authority.tournament_id = node.tournament_id
    AND authority.roster_id = node.roster_id
WHERE node.tournament_id = sqlc.arg(tournament_id) AND node.roster_id = sqlc.arg(roster_id)
ORDER BY node.artifact_kind, node.entity_id, node.revision_number, node.id
FOR KEY SHARE OF node, authority;

-- Golden progression is hydrated from immutable plan, state, and position
-- ledger rows. These locks follow the established Swiss result-head locks.

-- name: LockTournamentProgressionGoldenExactPlanSeal :one
SELECT plan_id,
    tournament_id,
    roster_id,
    proof_hash,
    sealed_at
FROM golden_exact_plan_snapshot_seals
WHERE plan_id = sqlc.arg(plan_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
FOR UPDATE;

-- name: LockTournamentProgressionGoldenExactPlan :one
SELECT plan.plan_id,
    plan.plan_revision_id,
    plan.tournament_id,
    plan.roster_id,
    plan.plan_set_id,
    plan.source_projection_revision_id,
    plan.source_projection_revision,
    plan.source_projection_previous_revision_id,
    plan.source_standings_artifact_id,
    plan.source_standings_payload_digest,
    plan.group_set_revision_id,
    plan.group_set_revision,
    plan.pool_revision_id,
    plan.pool_revision,
    plan.history_revision_id,
    plan.history_revision,
    plan.task_health_revision_id,
    plan.task_health_revision,
    plan.artifact_revision_id,
    plan.artifact_revision,
    plan.reservation_revision_id,
    plan.reservation_revision,
    plan.membership_revision_id,
    plan.membership_revision,
    plan.source_payload_digest,
    plan.group_digest,
    plan.pool_digest,
    plan.history_digest,
    plan.task_health_digest,
    plan.artifact_digest,
    plan.reservation_digest,
    plan.membership_digest,
    plan.proof_hash,
    plan.created_at,
    source_revision.id AS persisted_source_projection_revision_id,
    source_revision.revision_number AS persisted_source_projection_revision,
    source_revision.previous_revision_id AS persisted_source_projection_previous_revision_id,
    source_revision.state AS persisted_source_projection_state,
    source_standings.payload AS source_standings_payload,
    source_standings.payload_digest AS persisted_source_standings_payload_digest
FROM golden_exact_plan_snapshots AS plan
INNER JOIN projection_revisions AS source_revision
    ON source_revision.id = plan.source_projection_revision_id
    AND source_revision.tournament_id = plan.tournament_id
    AND source_revision.roster_id = plan.roster_id
    AND source_revision.revision_number = plan.source_projection_revision
INNER JOIN projection_artifacts AS source_standings
    ON source_standings.id = plan.source_standings_artifact_id
    AND source_standings.tournament_id = plan.tournament_id
    AND source_standings.roster_id = plan.roster_id
    AND source_standings.artifact_kind = 'standings'
WHERE plan.tournament_id = sqlc.arg(tournament_id)
    AND plan.roster_id = sqlc.arg(roster_id)
    AND plan.source_projection_revision_id = sqlc.arg(source_projection_revision_id)
    AND plan.source_projection_revision = sqlc.arg(source_projection_revision)
FOR UPDATE OF plan, source_revision, source_standings;

-- name: LockTournamentProgressionGoldenExactPlanGroups :many
SELECT plan_group.plan_id,
    plan_group.group_id,
    plan_group.group_revision_id,
    plan_group.source_projection_revision_id,
    plan_group.source_projection_revision,
    plan_group.position_from,
    plan_group.position_to,
    plan_group.group_ordinal,
    plan_group.definition_digest,
    group_revision.definition,
    group_revision.definition_digest AS group_definition_digest
FROM golden_exact_plan_snapshot_groups AS plan_group
INNER JOIN golden_group_revisions AS group_revision
    ON group_revision.revision_id = plan_group.group_revision_id
    AND group_revision.tournament_id = plan_group.tournament_id
    AND group_revision.roster_id = plan_group.roster_id
WHERE plan_group.plan_id = sqlc.arg(plan_id)
    AND plan_group.tournament_id = sqlc.arg(tournament_id)
    AND plan_group.roster_id = sqlc.arg(roster_id)
ORDER BY plan_group.group_ordinal
FOR UPDATE OF plan_group, group_revision;

-- name: LockTournamentProgressionGoldenExactPlanMembers :many
SELECT plan_id,
    group_revision_id,
    participant_id,
    position
FROM golden_exact_plan_snapshot_members
WHERE plan_id = sqlc.arg(plan_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY group_revision_id, position
FOR UPDATE;

-- name: LockTournamentProgressionGoldenExactPlanEdges :many
SELECT edge.plan_id,
    edge.group_revision_id,
    edge.edge_id,
    edge.reservation_id,
    edge.snapshot_id,
    edge.task_id,
    edge.task_version,
    edge.position,
    edge.content_digest,
    snapshot.kind,
    snapshot.title,
    snapshot.description,
    snapshot.category,
    snapshot.difficulty,
    snapshot.time_limit,
    snapshot.flag,
    snapshot.hints,
    snapshot.task_url,
    snapshot.source_file_url,
    snapshot.content_digest AS snapshot_content_digest
FROM golden_exact_plan_snapshot_edges AS edge
INNER JOIN task_snapshots AS snapshot
    ON snapshot.id = edge.snapshot_id
    AND snapshot.reservation_id = edge.reservation_id
    AND snapshot.task_id = edge.task_id
    AND snapshot.task_version = edge.task_version
WHERE edge.plan_id = sqlc.arg(plan_id)
    AND edge.tournament_id = sqlc.arg(tournament_id)
    AND edge.roster_id = sqlc.arg(roster_id)
ORDER BY edge.group_revision_id, edge.position
FOR UPDATE OF edge, snapshot;

-- name: LockTournamentProgressionGoldenExactPlanCandidates :many
SELECT candidate.plan_id,
    candidate.pool_revision_id,
    candidate.task_id,
    candidate.task_version,
    candidate.exists_in_source,
    candidate.enabled,
    candidate.healthy,
    candidate.mutation_locked,
    candidate.publicly_exposed,
    candidate.artifact_digest,
    task.title,
    task.description,
    task.category,
    task.difficulty,
    task.time_limit,
    task.flag,
    task.hint_1,
    task.hint_2,
    task.hint_3,
    task.task_url,
    task.source_file_url,
    task.content_digest AS task_content_digest
FROM golden_exact_plan_snapshot_candidates AS candidate
INNER JOIN task_versions AS task
    ON task.task_id = candidate.task_id
    AND task.version = candidate.task_version
WHERE candidate.plan_id = sqlc.arg(plan_id)
    AND candidate.tournament_id = sqlc.arg(tournament_id)
    AND candidate.roster_id = sqlc.arg(roster_id)
ORDER BY candidate.task_id, candidate.task_version
FOR UPDATE OF candidate, task;

-- name: LockTournamentProgressionGoldenExactPlanHistory :many
SELECT plan_id,
    participant_id,
    task_id,
    task_version
FROM golden_exact_plan_snapshot_history
WHERE plan_id = sqlc.arg(plan_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY participant_id, task_id, task_version
FOR UPDATE;

-- name: LockTournamentProgressionGoldenExactPlanParticipantReservations :many
SELECT plan_id,
    participant_id,
    player_id,
    reservation_id,
    revision,
    acquired_at,
    updated_at
FROM golden_exact_plan_snapshot_participant_reservations
WHERE plan_id = sqlc.arg(plan_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY participant_id
FOR UPDATE;

-- name: LockTournamentProgressionGoldenExactPlanReservations :many
SELECT plan_id,
    task_id,
    task_version,
    reservation_id,
    owner_plan_id,
    owner_plan_revision_id
FROM golden_exact_plan_snapshot_reservations
WHERE plan_id = sqlc.arg(plan_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY task_id, task_version
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateRevisions :many
SELECT revision_id,
    tournament_id,
    roster_id,
    group_id,
    group_revision_id,
    revision_number,
    previous_revision_id,
    plan_id,
    membership_revision_id,
    membership_revision,
    membership_previous_revision_id,
    membership_digest,
    payload_digest,
    created_at
FROM golden_state_revisions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY group_revision_id, revision_number
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateTransitions :many
SELECT state_revision_id,
    group_id,
    group_revision_id,
    transition_kind,
    command_id,
    previous_state_revision_id,
    occurred_at
FROM golden_state_transitions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY state_revision_id
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateRevisionSeals :many
SELECT state_revision_id,
    tournament_id,
    roster_id,
    payload_digest,
    sealed_at
FROM golden_state_revision_seals
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY state_revision_id
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateMembers :many
SELECT state_revision_id,
    participant_id,
    excluded,
    position
FROM golden_state_members
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY state_revision_id, position
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateAttempts :many
SELECT state_revision_id,
    group_revision_id,
    attempt_id,
    attempt_number,
    previous_attempt_id,
    state,
    retained_at,
    started_at,
    finished_at
FROM golden_state_attempts
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY state_revision_id, attempt_number
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateAttemptMembers :many
SELECT state_revision_id,
    attempt_id,
    participant_id,
    position
FROM golden_state_attempt_members
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY state_revision_id, attempt_id, position
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateReadyWindows :many
SELECT state_revision_id,
    window_id,
    window_revision_id,
    window_revision,
    window_previous_revision_id,
    attempt_id,
    attempt_number,
    opened_at,
    deadline,
    state,
    readiness_revision_id,
    readiness_revision,
    readiness_previous_revision_id,
    readiness_digest,
    presence_revision_id,
    presence_revision,
    presence_previous_revision_id,
    presence_digest
FROM golden_state_ready_windows
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY state_revision_id, opened_at, window_id
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateReadyWindowParticipants :many
SELECT state_revision_id,
    window_id,
    participant_id,
    membership_kind,
    position
FROM golden_state_ready_window_participants
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY state_revision_id, window_id, membership_kind, position
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateReadyEvents :many
SELECT command_id,
    state_revision_id,
    group_id,
    group_revision_id,
    participant_id,
    event_type,
    attempt_id,
    window_id,
    expected_state_revision_id,
    expected_state_revision,
    expected_state_payload_digest,
    expected_window_revision_id,
    expected_window_revision,
    expected_readiness_revision_id,
    expected_readiness_revision,
    expected_readiness_digest,
    expected_presence_revision_id,
    expected_presence_revision,
    expected_presence_digest,
    command_digest,
    result_window_revision_id,
    result_readiness_revision_id,
    result_presence_revision_id,
    occurred_at
FROM golden_state_ready_events
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY occurred_at, command_id
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateNoShowResolutions :many
SELECT command_id,
    state_revision_id,
    group_id,
    group_revision_id,
    attempt_id,
    attempt_number,
    window_id,
    expected_state_revision_id,
    expected_state_revision,
    expected_state_payload_digest,
    expected_window_revision_id,
    expected_window_revision,
    expected_readiness_revision_id,
    expected_readiness_revision,
    expected_readiness_digest,
    expected_presence_revision_id,
    expected_presence_revision,
    expected_presence_digest,
    result_window_revision_id,
    result_membership_revision_id,
    deadline,
    resolved_at,
    readiness_revision_id,
    readiness_revision,
    readiness_digest,
    presence_revision_id,
    presence_revision,
    presence_digest
FROM golden_state_no_show_resolutions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY resolved_at, command_id
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateNoShowParticipants :many
SELECT state_revision_id,
    command_id,
    participant_id,
    membership_kind,
    position
FROM golden_state_no_show_participants
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY state_revision_id, command_id, membership_kind, position
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateAllocations :many
SELECT allocation_id,
    command_id,
    state_revision_id,
    group_id,
    group_revision_id,
    expected_state_revision_id,
    expected_state_revision,
    expected_state_payload_digest,
    allocated_at,
    payload_digest
FROM golden_state_allocations
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY allocated_at, allocation_id
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateAllocationInputs :many
SELECT allocation_id,
    state_revision_id,
    participant_id,
    points,
    buchholz,
    head_to_head_points,
    head_to_head_applied,
    effective_time_milliseconds,
    accepted_solve_time_milliseconds,
    stable_seed,
    position
FROM golden_state_allocation_inputs
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY allocation_id, position
FOR UPDATE;

-- name: LockTournamentProgressionGoldenStateAllocationPositions :many
SELECT allocation_id,
    state_revision_id,
    position,
    participant_id,
    position_kind
FROM golden_state_allocation_positions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY allocation_id, position
FOR UPDATE;

-- name: LockTournamentProgressionGoldenPositionLedger :many
SELECT ledger.revision_id AS ledger_revision_id,
    ledger.group_revision_id,
    ledger.revision_number,
    ledger.previous_revision_id,
    ledger.payload_digest,
    ledger.finalized_at,
    attempt.attempt_id,
    attempt.submission_revision_id,
    submission.revision_number AS submission_revision,
    attempt.attempt_number,
    attempt.order_count,
    COALESCE(attempt_authority.wave_id, runtime_authority.wave_id,
        '00000000-0000-0000-0000-000000000000'::uuid) AS wave_id,
    COALESCE(attempt_authority.assignment_id, runtime_authority.assignment_id,
        '00000000-0000-0000-0000-000000000000'::uuid) AS assignment_id,
    COALESCE(attempt_authority.snapshot_id, runtime_authority.snapshot_id,
        '00000000-0000-0000-0000-000000000000'::uuid) AS snapshot_id,
    COALESCE(attempt_authority.task_id, runtime_authority.task_id,
        '00000000-0000-0000-0000-000000000000'::uuid) AS task_id,
    binding.position_commit_id,
    binding.participant_id,
    binding.position,
    binding.evidence_digest,
    provisional_submission.server_sequence AS submission_id,
    terminal_evidence.id AS terminal_evidence_id,
    terminal_evidence.payload_digest AS terminal_payload_digest
FROM golden_position_ledger_revisions AS ledger
LEFT JOIN golden_position_ledger_attempts AS attempt
    ON attempt.ledger_revision_id = ledger.revision_id
    AND attempt.tournament_id = ledger.tournament_id
    AND attempt.roster_id = ledger.roster_id
LEFT JOIN golden_attempt_submission_revisions AS submission
    ON submission.revision_id = attempt.submission_revision_id
    AND submission.attempt_id = attempt.attempt_id
    AND submission.tournament_id = ledger.tournament_id
    AND submission.roster_id = ledger.roster_id
LEFT JOIN golden_attempt_authorities AS attempt_authority
    ON attempt_authority.attempt_id = attempt.attempt_id
    AND attempt_authority.tournament_id = ledger.tournament_id
    AND attempt_authority.roster_id = ledger.roster_id
    AND attempt_authority.group_revision_id = ledger.group_revision_id
LEFT JOIN golden_runtime_assignments AS runtime_authority
    ON runtime_authority.attempt_id = attempt.attempt_id
    AND runtime_authority.tournament_id = ledger.tournament_id
    AND runtime_authority.roster_id = ledger.roster_id
    AND runtime_authority.group_revision_id = ledger.group_revision_id
LEFT JOIN golden_position_ledger_commit_bindings AS binding
    ON binding.ledger_revision_id = ledger.revision_id
    AND binding.attempt_id = attempt.attempt_id
    AND binding.tournament_id = ledger.tournament_id
    AND binding.roster_id = ledger.roster_id
LEFT JOIN golden_position_commits AS position_commit
    ON position_commit.id = binding.position_commit_id
    AND position_commit.attempt_id = binding.attempt_id
    AND position_commit.tournament_id = binding.tournament_id
    AND position_commit.roster_id = binding.roster_id
LEFT JOIN golden_provisional_submissions AS provisional_submission
    ON provisional_submission.id = position_commit.provisional_submission_id
    AND provisional_submission.attempt_id = position_commit.attempt_id
    AND provisional_submission.tournament_id = position_commit.tournament_id
    AND provisional_submission.roster_id = position_commit.roster_id
LEFT JOIN golden_terminal_position_evidence AS terminal_evidence
    ON terminal_evidence.id = position_commit.terminal_evidence_id
    AND terminal_evidence.attempt_id = position_commit.attempt_id
    AND terminal_evidence.membership_id = position_commit.membership_id
    AND terminal_evidence.participant_id = binding.participant_id
    AND terminal_evidence.tournament_id = ledger.tournament_id
    AND terminal_evidence.roster_id = ledger.roster_id
    AND terminal_evidence.group_revision_id = ledger.group_revision_id
    AND terminal_evidence.position = binding.position
WHERE ledger.tournament_id = sqlc.arg(tournament_id)
    AND ledger.roster_id = sqlc.arg(roster_id)
ORDER BY ledger.group_revision_id, ledger.revision_number, attempt.attempt_number, binding.position
FOR UPDATE OF ledger;

-- name: LockTournamentProgressionGoldenPositionLedgerRevisionSeals :many
SELECT ledger_revision_id,
    tournament_id,
    roster_id,
    payload_digest,
    sealed_at
FROM golden_position_ledger_revision_seals
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY ledger_revision_id
FOR UPDATE;

-- name: CreateFinalSwissProjectionReceiptGame :one
INSERT INTO final_swiss_projection_receipt_games (
    projection_revision_id,
    tournament_id,
    roster_id,
    series_id,
    game_attempt_id,
    game_result_revision_id,
    game_result_node_id,
    created_at
)
VALUES (
    sqlc.arg(projection_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(game_attempt_id),
    sqlc.arg(game_result_revision_id),
    sqlc.arg(game_result_node_id),
    sqlc.arg(created_at)
)
RETURNING game_attempt_id;

-- name: CreateFinalSwissProjectionReceiptLedgerEntry :one
INSERT INTO final_swiss_projection_receipt_ledger_entries (
    projection_revision_id,
    tournament_id,
    roster_id,
    ledger_entry_id,
    created_at
)
VALUES (
    sqlc.arg(projection_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(ledger_entry_id),
    sqlc.arg(created_at)
)
RETURNING ledger_entry_id;

-- name: CreateTournamentStageProgressionCAS :one
WITH authority AS MATERIALIZED (
    SELECT tournament.id,
        roster.id AS roster_id
    FROM tournaments AS tournament
    INNER JOIN rosters AS roster ON roster.tournament_id = tournament.id
    INNER JOIN projection_revisions AS source_revision
        ON source_revision.id = sqlc.arg(source_projection_revision_id)
        AND source_revision.tournament_id = tournament.id
        AND source_revision.roster_id = roster.id
        AND source_revision.revision_number = sqlc.arg(source_projection_revision)
    INNER JOIN projection_revisions AS resulting_revision
        ON resulting_revision.id = COALESCE(
            sqlc.narg(resulting_projection_revision_id)::UUID,
            source_revision.id
        )
        AND resulting_revision.tournament_id = tournament.id
        AND resulting_revision.roster_id = roster.id
        AND resulting_revision.revision_number = COALESCE(
            sqlc.narg(resulting_projection_revision)::BIGINT,
            source_revision.revision_number
        )
    WHERE tournament.id = sqlc.arg(tournament_id)
        AND roster.id = sqlc.arg(roster_id)
        AND tournament.revision = sqlc.arg(source_tournament_revision)
        AND tournament.state = sqlc.arg(source_tournament_state)
        AND (
            (
                sqlc.arg(action) = 'start_golden'
                AND source_revision.state = 'published'
                AND resulting_revision.id = source_revision.id
            )
            OR (
                sqlc.arg(action) = 'start_playoffs'
                AND source_revision.state = 'superseded'
                AND source_revision.superseded_by_revision_id = resulting_revision.id
                AND resulting_revision.state = 'published'
            )
        )
    FOR UPDATE OF tournament, roster, source_revision, resulting_revision
)
INSERT INTO tournament_stage_progressions (
    command_id,
    tournament_id,
    roster_id,
    actor_id,
    action,
    source_tournament_revision,
    source_tournament_state,
    source_projection_revision_id,
    source_projection_revision,
    resulting_projection_revision_id,
    resulting_projection_revision,
    resulting_tournament_revision,
    resulting_tournament_state,
    proof,
    proof_digest,
    executed_at,
    created_at
)
SELECT sqlc.arg(command_id),
    authority.id,
    authority.roster_id,
    sqlc.arg(actor_id),
    sqlc.arg(action),
    sqlc.arg(source_tournament_revision),
    sqlc.arg(source_tournament_state),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.narg(resulting_projection_revision_id)::UUID,
    sqlc.narg(resulting_projection_revision)::BIGINT,
    sqlc.arg(source_tournament_revision) + 1,
    sqlc.arg(resulting_tournament_state),
    sqlc.arg(proof)::JSONB,
    sqlc.arg(proof_digest),
    sqlc.arg(executed_at),
    sqlc.arg(executed_at)
FROM authority
RETURNING command_id;

-- name: CreateTournamentStageTieGroup :one
INSERT INTO tournament_stage_tie_groups (
    command_id,
    tournament_id,
    roster_id,
    group_id,
    group_revision_id,
    source_projection_revision_id,
    source_projection_revision,
    position_from,
    position_to,
    proof,
    proof_digest,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(position_from),
    sqlc.arg(position_to),
    sqlc.arg(proof)::JSONB,
    sqlc.arg(proof_digest),
    sqlc.arg(created_at)
)
RETURNING group_id;

-- name: CreateTournamentStageTieGroupMember :one
INSERT INTO tournament_stage_tie_group_members (
    command_id,
    tournament_id,
    roster_id,
    group_id,
    participant_id,
    standing_position,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_id),
    sqlc.arg(participant_id),
    sqlc.arg(standing_position),
    sqlc.arg(created_at)
)
RETURNING participant_id;

-- name: CreateGoldenGroupRevision :one
INSERT INTO golden_group_revisions (
    revision_id,
    group_id,
    stage_progression_command_id,
    tournament_id,
    roster_id,
    source_projection_revision_id,
    source_projection_revision,
    position_from,
    position_to,
    definition,
    definition_digest,
    created_at
)
VALUES (
    sqlc.arg(revision_id),
    sqlc.arg(group_id),
    sqlc.arg(stage_progression_command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(position_from),
    sqlc.arg(position_to),
    sqlc.arg(definition)::JSONB,
    sqlc.arg(definition_digest),
    sqlc.arg(created_at)
)
RETURNING revision_id;

-- name: CreateGoldenAttemptStageGroup :one
INSERT INTO golden_attempt_stage_groups (
    attempt_id,
    tournament_id,
    roster_id,
    group_revision_id,
    bound_at
)
VALUES (
    sqlc.arg(attempt_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(bound_at)
)
RETURNING attempt_id;

-- name: CreateTournamentStagePlayoffEvidence :one
WITH inserted AS (
    INSERT INTO tournament_stage_playoff_evidence (
        command_id,
        tournament_id,
        roster_id,
        source_projection_revision_id,
        source_projection_revision,
        published_projection_revision_id,
        published_projection_revision,
        top4_artifact_id,
        bracket_artifact_id,
        top4_node_id,
        bracket_node_id,
        first_semifinal_series_id,
        second_semifinal_series_id,
        proof_digest,
        created_at
    )
    SELECT
        sqlc.arg(command_id),
        sqlc.arg(tournament_id),
        sqlc.arg(roster_id),
        sqlc.arg(source_projection_revision_id),
        sqlc.arg(source_projection_revision),
        sqlc.arg(published_projection_revision_id),
        sqlc.arg(published_projection_revision),
        sqlc.arg(top4_artifact_id),
        sqlc.arg(bracket_artifact_id),
        sqlc.arg(top4_node_id),
        sqlc.arg(bracket_node_id),
        sqlc.arg(first_semifinal_series_id),
        sqlc.arg(second_semifinal_series_id),
        sqlc.arg(proof_digest),
        sqlc.arg(created_at)
    WHERE NOT EXISTS (
        SELECT 1
        FROM tournament_stage_playoff_evidence
        WHERE command_id = sqlc.arg(command_id)
            AND tournament_id = sqlc.arg(tournament_id)
    )
    ON CONFLICT (command_id, tournament_id) DO NOTHING
    RETURNING command_id
)
SELECT command_id
FROM inserted
UNION ALL
SELECT command_id
FROM tournament_stage_playoff_evidence
WHERE command_id = sqlc.arg(command_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND source_projection_revision_id = sqlc.arg(source_projection_revision_id)
    AND source_projection_revision = sqlc.arg(source_projection_revision)
    AND published_projection_revision_id = sqlc.arg(published_projection_revision_id)
    AND published_projection_revision = sqlc.arg(published_projection_revision)
    AND top4_artifact_id = sqlc.arg(top4_artifact_id)
    AND bracket_artifact_id = sqlc.arg(bracket_artifact_id)
    AND top4_node_id = sqlc.arg(top4_node_id)
    AND bracket_node_id = sqlc.arg(bracket_node_id)
    AND first_semifinal_series_id = sqlc.arg(first_semifinal_series_id)
    AND second_semifinal_series_id = sqlc.arg(second_semifinal_series_id)
    AND proof_digest = sqlc.arg(proof_digest)
    AND created_at = sqlc.arg(created_at)
    AND NOT EXISTS (SELECT 1 FROM inserted)
LIMIT 1;

-- The stage evidence row is written before this authority. Its node foreign
-- keys are deferred so the ensuing four-node graph can prove the exact Top4,
-- bracket, and semifinal score genesis at transaction commit.
-- name: CreateTournamentProgressionStageProjectionNodeAuthority :one
INSERT INTO result_projection_node_authorities (
    id,
    tournament_id,
    roster_id,
    source_kind,
    result_commit_id,
    correction_command_id,
    wave_id,
    stage_command_id,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    'stage_initialization',
    NULL,
    NULL,
    NULL,
    sqlc.arg(stage_command_id),
    sqlc.arg(created_at)
)
RETURNING id;

-- name: CreateTournamentProgressionStageProjectionNode :one
INSERT INTO result_projection_nodes (
    id,
    authority_id,
    tournament_id,
    roster_id,
    artifact_kind,
    entity_id,
    revision_number,
    previous_node_id,
    payload,
    payload_digest,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(authority_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(artifact_kind),
    sqlc.arg(entity_id),
    1,
    NULL,
    sqlc.arg(payload),
    sqlc.arg(payload_digest),
    sqlc.arg(created_at)
)
RETURNING id;

-- name: CreateTournamentProgressionStageProjectionDependency :one
INSERT INTO result_projection_dependencies (
    authority_id,
    source_node_id,
    derived_node_id,
    created_at
)
VALUES (
    sqlc.arg(authority_id),
    sqlc.arg(source_node_id),
    sqlc.arg(derived_node_id),
    sqlc.arg(created_at)
)
RETURNING authority_id;

-- name: CreateTournamentStagePlayoffGoldenSettlement :one
INSERT INTO tournament_stage_playoff_golden_settlements (
    command_id,
    tournament_id,
    roster_id,
    group_revision_id,
    attempt_id,
    position_commit_id,
    participant_id,
    position,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(attempt_id),
    sqlc.arg(position_commit_id),
    sqlc.arg(participant_id),
    sqlc.arg(position),
    sqlc.arg(created_at)
)
RETURNING position_commit_id;

-- name: CreateTournamentStagePlayoffSemifinal :one
WITH inserted AS (
    INSERT INTO tournament_stage_playoff_semifinals (
        command_id,
        tournament_id,
        roster_id,
        bracket_artifact_id,
        position,
        series_id,
        created_at
    )
    SELECT
        sqlc.arg(command_id),
        sqlc.arg(tournament_id),
        sqlc.arg(roster_id),
        sqlc.arg(bracket_artifact_id),
        sqlc.arg(position),
        sqlc.arg(series_id),
        sqlc.arg(created_at)
    WHERE NOT EXISTS (
        SELECT 1
        FROM tournament_stage_playoff_semifinals
        WHERE command_id = sqlc.arg(command_id)
            AND tournament_id = sqlc.arg(tournament_id)
            AND position = sqlc.arg(position)
    )
    ON CONFLICT DO NOTHING
    RETURNING series_id
)
SELECT series_id
FROM inserted
UNION ALL
SELECT series_id
FROM tournament_stage_playoff_semifinals
WHERE command_id = sqlc.arg(command_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND bracket_artifact_id = sqlc.arg(bracket_artifact_id)
    AND position = sqlc.arg(position)
    AND series_id = sqlc.arg(series_id)
    AND created_at = sqlc.arg(created_at)
    AND NOT EXISTS (SELECT 1 FROM inserted)
LIMIT 1;

-- A published bracket creates locked, empty BO1 Series. The materializer
-- locks each row again before reading it so category, assignment, and graph
-- writes all use one current database authority.
-- name: LockPlayoffSemifinalSeriesForMaterialization :one
SELECT id,
    tournament_id,
    roster_id,
    first_participant_id,
    second_participant_id,
    format,
    state,
    current_score_revision_id,
    current_result_revision_id,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM series
WHERE id = sqlc.arg(series_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND format = 'bo1'
    AND state = 'locked'
    AND current_score_revision_id IS NOT NULL
    AND current_result_revision_id IS NULL
    AND started_at IS NULL
    AND finished_at IS NULL
FOR UPDATE;

-- Semifinal category authority is persisted separately from the Swiss
-- materializer, while retaining the same immutable random decision shape.
-- name: CreatePlayoffSemifinalCategoryRevision :one
INSERT INTO category_revisions (
    id,
    series_id,
    roster_id,
    revision,
    source_pool_revision_id,
    mode,
    category_pool,
    selected_categories,
    selector_actor_id,
    selection_reason,
    decision_evidence_id,
    decision_algorithm_version,
    decision_inputs,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    decided_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(series_id),
    sqlc.arg(roster_id),
    sqlc.arg(revision),
    sqlc.arg(source_pool_revision_id),
    sqlc.arg(mode),
    sqlc.arg(category_pool),
    sqlc.arg(selected_categories),
    sqlc.narg(selector_actor_id)::UUID,
    sqlc.narg(selection_reason)::TEXT,
    sqlc.narg(decision_evidence_id)::UUID,
    sqlc.narg(decision_algorithm_version)::VARCHAR,
    sqlc.narg(decision_inputs)::JSONB,
    sqlc.narg(decision_seed)::BYTEA,
    sqlc.narg(decision_result)::JSONB,
    sqlc.narg(decision_replay_digest)::BYTEA,
    sqlc.narg(decision_owner_id)::UUID,
    sqlc.narg(decided_at)::TIMESTAMPTZ,
    sqlc.arg(created_at)
)
RETURNING id,
    series_id,
    roster_id,
    revision,
    source_pool_revision_id,
    mode,
    category_pool,
    selected_categories,
    selector_actor_id,
    selection_reason,
    decision_evidence_id,
    decision_algorithm_version,
    decision_inputs,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    decided_at,
    created_at;

-- A semifinal has one independent planned execution Wave. It is linked to
-- its Series before the ready state transition and before a ready window is
-- opened by the existing wave control workflow.
-- name: CreatePlayoffSemifinalWave :exec
INSERT INTO waves (
    id,
    tournament_id,
    roster_id,
    revision_id,
    revision,
    state,
    replaces_wave_id,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(revision_id),
    1,
    'planned',
    NULL,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
);

-- name: ReadyPlayoffSemifinalGameForMaterialization :one
UPDATE game_attempts
SET state = 'ready',
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(game_id)
    AND slot_id = sqlc.arg(slot_id)
    AND series_id = sqlc.arg(series_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'planned'
    AND started_at IS NULL
    AND finished_at IS NULL
RETURNING id,
    slot_id,
    series_id,
    roster_id,
    attempt_number,
    revision,
    state;

-- name: ReadyPlayoffSemifinalSeriesForMaterialization :one
UPDATE series
SET state = 'ready',
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(series_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'locked'
    AND started_at IS NULL
    AND finished_at IS NULL
    AND current_result_revision_id IS NULL
RETURNING id,
    revision,
    state;
