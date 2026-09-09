-- name: GetTournamentAdminRoster :one
SELECT roster.id,
    roster.tournament_id,
    roster.revision,
    roster.locked_at,
    roster.execution_started_at,
    roster.created_at,
    roster.updated_at
FROM rosters AS roster
WHERE roster.tournament_id = sqlc.arg(tournament_id);

-- name: ListTournamentAdminRosterParticipants :many
SELECT participant.id,
    participant.roster_id,
    roster.tournament_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    participant.created_at,
    participant.updated_at
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
WHERE roster.tournament_id = sqlc.arg(tournament_id)
ORDER BY participant.seed, participant.id;

-- name: LockTournamentRosterAuthority :one
SELECT tournament.state AS tournament_state,
    tournament.preset AS tournament_preset,
    tournament.revision AS tournament_revision,
    roster.id AS roster_id,
    roster.tournament_id,
    roster.revision AS roster_revision,
    roster.locked_at,
    roster.execution_started_at,
    roster.created_at AS roster_created_at,
    roster.updated_at AS roster_updated_at,
    projection.id AS projection_revision_id,
    projection.revision_number AS projection_revision
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
WHERE tournament.id = sqlc.arg(tournament_id)
FOR UPDATE OF tournament, roster;

-- name: FindTournamentRosterOperation :one
SELECT command_id,
    tournament_id,
    roster_id,
    actor_id,
    action,
    preflight_revision_id,
    source_projection_revision_id,
    source_projection_revision,
    source_tournament_revision,
    source_tournament_state,
    resulting_tournament_revision,
    resulting_tournament_state,
    source_roster_revision,
    resulting_roster_revision,
    request_digest,
    checked_in_player_ids,
    result_document,
    executed_at,
    created_at
FROM tournament_roster_operations
WHERE tournament_id = sqlc.arg(tournament_id)
    AND command_id = sqlc.arg(command_id);

-- name: ReadTournamentRosterTime :one
SELECT clock_timestamp()::TIMESTAMPTZ AS observed_at;

-- name: DeleteTournamentAdminRosterParticipants :execrows
DELETE FROM participants AS participant
USING rosters AS roster
WHERE participant.roster_id = roster.id
    AND roster.id = sqlc.arg(roster_id)
    AND roster.tournament_id = sqlc.arg(tournament_id)
    AND roster.revision = sqlc.arg(expected_roster_revision)
    AND roster.locked_at IS NULL
    AND roster.execution_started_at IS NULL
    AND NOT EXISTS (
        SELECT 1
        FROM swiss_rounds AS round
        WHERE round.roster_id = roster.id
    );

-- name: AdvanceTournamentAdminRosterRevision :one
UPDATE rosters
SET revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE rosters.id = sqlc.arg(roster_id)
    AND rosters.tournament_id = sqlc.arg(tournament_id)
    AND rosters.revision = sqlc.arg(expected_roster_revision)
    AND rosters.locked_at IS NULL
    AND rosters.execution_started_at IS NULL
    AND NOT EXISTS (
        SELECT 1
        FROM swiss_rounds AS round
        WHERE round.roster_id = rosters.id
    )
RETURNING rosters.id,
    rosters.tournament_id,
    rosters.revision,
    rosters.locked_at,
    rosters.execution_started_at,
    rosters.created_at,
    rosters.updated_at;

-- name: TransitionTournamentForRosterCAS :one
UPDATE tournaments
SET state = sqlc.arg(next_state),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(tournament_id)
    AND revision = sqlc.arg(expected_tournament_revision)
    AND state = sqlc.arg(expected_state)
    AND paused_from_state IS NULL
    AND started_at IS NULL
    AND finished_at IS NULL
RETURNING id;

-- name: ListTournamentPreflightParticipants :many
SELECT participant.id AS participant_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    COALESCE(reservation.tournament_id, roster.tournament_id) AS reserved_tournament_id
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
LEFT JOIN participant_reservations AS reservation ON reservation.player_id = participant.player_id
WHERE roster.id = sqlc.arg(roster_id)
    AND roster.tournament_id = sqlc.arg(tournament_id)
ORDER BY participant.seed, participant.id;

-- name: GetTournamentPreflightRound :one
SELECT round.id,
    round.revision,
    bye.participant_id AS bye_participant_id
FROM swiss_rounds AS round
LEFT JOIN swiss_byes AS bye ON bye.round_id = round.id
WHERE round.roster_id = sqlc.arg(roster_id)
ORDER BY round.round_number DESC, round.id DESC
LIMIT 1;

-- name: ListTournamentPreflightPairings :many
SELECT first_member.participant_id AS first_participant_id,
    second_member.participant_id AS second_participant_id,
    COALESCE(history.prior_meeting_count, 0)::SMALLINT AS prior_meeting_count,
    override.actor_id AS override_actor_id,
    override.reason AS override_reason,
    override.confirmed_at AS override_confirmed_at
FROM swiss_pairings AS pairing
JOIN swiss_pairing_members AS first_member
    ON first_member.pairing_id = pairing.id
    AND first_member.seat = 1
JOIN swiss_pairing_members AS second_member
    ON second_member.pairing_id = pairing.id
    AND second_member.seat = 2
LEFT JOIN swiss_opponent_history AS history ON history.pairing_id = pairing.id
LEFT JOIN swiss_repeat_overrides AS override ON override.id = pairing.repeat_override_id
WHERE pairing.round_id = sqlc.arg(round_id)
    AND pairing.roster_id = sqlc.arg(roster_id)
ORDER BY pairing.slot_number, pairing.id;

-- name: CreateTournamentRosterOperation :one
INSERT INTO tournament_roster_operations (
    command_id,
    tournament_id,
    roster_id,
    actor_id,
    action,
    preflight_revision_id,
    source_projection_revision_id,
    source_projection_revision,
    source_tournament_revision,
    source_tournament_state,
    resulting_tournament_revision,
    resulting_tournament_state,
    source_roster_revision,
    resulting_roster_revision,
    request_digest,
    checked_in_player_ids,
    result_document,
    executed_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(actor_id),
    sqlc.arg(action),
    sqlc.narg(preflight_revision_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(source_tournament_revision),
    sqlc.arg(source_tournament_state),
    sqlc.arg(resulting_tournament_revision),
    sqlc.arg(resulting_tournament_state),
    sqlc.arg(source_roster_revision),
    sqlc.arg(resulting_roster_revision),
    sqlc.arg(request_digest),
    sqlc.arg(checked_in_player_ids),
    sqlc.arg(result_document),
    sqlc.arg(executed_at),
    sqlc.arg(executed_at)
)
RETURNING command_id;
