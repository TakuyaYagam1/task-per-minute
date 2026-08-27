//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestArenaSwissMigration(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	tournamentID := createArenaMigrationTournament(t, ctx)
	rosterID := createArenaMigrationRoster(t, ctx, tournamentID)
	playerIDs := createArenaMigrationPlayers(t, ctx, 6)
	participantIDs := createSwissMigrationParticipants(t, ctx, rosterID, playerIDs[:5])
	createdAt := time.Now().UTC().Truncate(time.Microsecond)

	automaticRoundID := createAutomaticSwissMigrationRound(t, ctx, rosterID, createdAt)
	assertSwissRoundLockRevision(t, ctx, automaticRoundID, createdAt.Add(time.Minute))

	pairingID := createSwissMigrationPairing(t, ctx, automaticRoundID, rosterID, 1, uuid.Nil)
	addSwissMigrationPairingMember(t, ctx, pairingID, automaticRoundID, rosterID, 1, participantIDs[0])
	addSwissMigrationPairingMember(t, ctx, pairingID, automaticRoundID, rosterID, 2, participantIDs[1])
	assertSwissPairingConstraints(t, ctx, automaticRoundID, rosterID, pairingID, participantIDs)

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_opponent_history (
			pairing_id, round_id, roster_id, prior_meeting_count
		)
		VALUES ($1, $2, $3, 0)`, pairingID, automaticRoundID, rosterID)
	require.NoError(t, err)

	manualRoundID := createManualSwissMigrationRound(t, ctx, rosterID, createdAt.Add(2*time.Minute))
	overrideID := createSwissMigrationRepeatOverride(
		t, ctx, manualRoundID, rosterID, participantIDs, createdAt.Add(3*time.Minute),
	)
	repeatedPairingID := createSwissMigrationPairing(t, ctx, manualRoundID, rosterID, 1, overrideID)
	addSwissMigrationPairingMember(t, ctx, repeatedPairingID, manualRoundID, rosterID, 1, participantIDs[0])
	addSwissMigrationPairingMember(t, ctx, repeatedPairingID, manualRoundID, rosterID, 2, participantIDs[1])

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_opponent_history (
			pairing_id, round_id, roster_id, prior_meeting_count, repeat_override_id
		)
		VALUES ($1, $2, $3, 1, $4)`, repeatedPairingID, manualRoundID, rosterID, overrideID)
	require.NoError(t, err)

	withoutOverrideID := createSwissMigrationPairing(t, ctx, manualRoundID, rosterID, 2, uuid.Nil)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_opponent_history (
			pairing_id, round_id, roster_id, prior_meeting_count
		)
		VALUES ($1, $2, $3, 1)`, withoutOverrideID, manualRoundID, rosterID)
	require.Error(t, err)

	assertSwissByeConstraints(
		t, ctx, automaticRoundID, manualRoundID, rosterID, participantIDs[4], createdAt.Add(4*time.Minute),
	)
	assertSwissForeignRosterConstraints(t, ctx, automaticRoundID, rosterID, tournamentID, playerIDs[5], createdAt)
}

func createSwissMigrationParticipants(
	t testing.TB,
	ctx context.Context,
	rosterID uuid.UUID,
	playerIDs []uuid.UUID,
) []uuid.UUID {
	t.Helper()
	participantIDs := make([]uuid.UUID, len(playerIDs))
	for i, playerID := range playerIDs {
		err := sharedPool.QueryRow(ctx, `
			INSERT INTO arena_participants (roster_id, player_id, seed, attendance)
			VALUES ($1, $2, $3, 'checked_in')
			RETURNING id`, rosterID, playerID, i+1).Scan(&participantIDs[i])
		require.NoError(t, err)
	}
	return participantIDs
}

func createAutomaticSwissMigrationRound(
	t testing.TB,
	ctx context.Context,
	rosterID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()
	roundID := uuid.New()
	inputs := `[
		"meeting:00000000-0000-0000-0000-000000000001:00000000-0000-0000-0000-000000000002",
		"participant:00000000-0000-0000-0000-000000000001",
		"participant:00000000-0000-0000-0000-000000000002"
	]`
	result := `[
		"participant:00000000-0000-0000-0000-000000000002",
		"participant:00000000-0000-0000-0000-000000000001",
		"meeting:00000000-0000-0000-0000-000000000001:00000000-0000-0000-0000-000000000002"
	]`
	seed := bytes.Repeat([]byte{1}, 32)
	digest := bytes.Repeat([]byte{2}, 32)
	var (
		revision               int64
		storedInputs           string
		storedAlgorithmVersion string
	)
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO arena_swiss_rounds (
			id, roster_id, round_number, source_roster_revision, source_history_revision,
			generation_kind, pairing_inputs, decision_evidence_id,
			decision_algorithm_version, decision_seed, decision_result,
			decision_replay_digest, decision_owner_id, generated_at, created_at, updated_at
		)
		VALUES (
			$1, $2, 1, 1, 0, 'automatic', $3::jsonb, $4,
			'hmac-sha256-order-v1', $5, $6::jsonb, $7, $1, $8, $8, $8
		)
		RETURNING revision, pairing_inputs::text, decision_algorithm_version`,
		roundID, rosterID, inputs, uuid.New(), seed, result, digest, createdAt,
	).Scan(&revision, &storedInputs, &storedAlgorithmVersion)
	require.NoError(t, err)
	require.EqualValues(t, 1, revision)
	require.JSONEq(t, inputs, storedInputs)
	require.Equal(t, "hmac-sha256-order-v1", storedAlgorithmVersion)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_rounds (
			id, roster_id, round_number, source_roster_revision, source_history_revision,
			generation_kind, pairing_inputs, decision_evidence_id,
			decision_algorithm_version, decision_seed, decision_result,
			decision_replay_digest, decision_owner_id, generated_at, created_at, updated_at
		)
		VALUES (
			$1, $2, 2, 1, 0, 'automatic', $3::jsonb, $4,
			'hmac-sha256-order-v1', $5, $3::jsonb, $6, $1, $7, $7, $7
		)`, uuid.New(), rosterID, inputs, uuid.New(), []byte{1}, digest, createdAt)
	require.Error(t, err)

	return roundID
}

func createManualSwissMigrationRound(
	t testing.TB,
	ctx context.Context,
	rosterID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()
	roundID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_rounds (
			id, roster_id, round_number, source_roster_revision, source_history_revision,
			generation_kind, pairing_inputs, generated_at, created_at, updated_at
		)
		VALUES ($1, $2, 2, 1, 1, 'manual', $3::jsonb, $4, $4, $4)`,
		roundID, rosterID, `["manual-roster-snapshot","prior-meeting-snapshot"]`, createdAt)
	require.NoError(t, err)
	return roundID
}

func assertSwissRoundLockRevision(
	t testing.TB,
	ctx context.Context,
	roundID uuid.UUID,
	lockedAt time.Time,
) {
	t.Helper()
	var lockRevision int64
	err := sharedPool.QueryRow(ctx, `
		UPDATE arena_swiss_rounds
		SET lock_revision = revision, locked_at = $2, updated_at = $2
		WHERE id = $1
		RETURNING lock_revision`, roundID, lockedAt).Scan(&lockRevision)
	require.NoError(t, err)
	require.EqualValues(t, 1, lockRevision)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_swiss_rounds
		SET revision = revision + 1, updated_at = $2
		WHERE id = $1`, roundID, lockedAt.Add(time.Minute))
	require.Error(t, err)
}

func createSwissMigrationRepeatOverride(
	t testing.TB,
	ctx context.Context,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	confirmedAt time.Time,
) uuid.UUID {
	t.Helper()
	overrideID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_repeat_overrides (
			id, round_id, roster_id, actor_id, reason, confirmed_at,
			roster_participant_ids, proposed_pairings, bye_participant_id,
			previous_meetings, repeated_pairings, alternative_algorithm_version,
			alternative_search_complete, alternative_pairings, created_at
		)
		VALUES (
			$1, $2, $3, $4, 'no non-repeating complete matching', $5,
			$6::jsonb, $7::jsonb, $8, $9::jsonb, $7::jsonb,
			'complete-backtracking-v1', TRUE, '[]'::jsonb, $5
		)`,
		overrideID, roundID, rosterID, uuid.New(), confirmedAt,
		`["p1","p2","p3","p4","p5"]`, `[["p1","p2"],["p3","p4"]]`, participantIDs[4],
		`[["p1","p2"]]`,
	)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_repeat_overrides (
			id, round_id, roster_id, actor_id, reason, confirmed_at,
			roster_participant_ids, proposed_pairings, previous_meetings,
			repeated_pairings, alternative_algorithm_version,
			alternative_search_complete, alternative_pairings, created_at
		)
		VALUES (
			$1, $2, $3, $4, '  ', $5,
			'["p1"]'::jsonb, '[["p1","p2"]]'::jsonb, '[]'::jsonb,
			'[["p1","p2"]]'::jsonb, 'complete-backtracking-v1', TRUE, '[]'::jsonb, $5
		)`, uuid.New(), roundID, rosterID, uuid.New(), confirmedAt)
	require.Error(t, err)

	return overrideID
}

func createSwissMigrationPairing(
	t testing.TB,
	ctx context.Context,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	slotNumber int,
	overrideID uuid.UUID,
) uuid.UUID {
	t.Helper()
	var pairingID uuid.UUID
	var override any
	if overrideID != uuid.Nil {
		override = overrideID
	}
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO arena_swiss_pairings (round_id, roster_id, slot_number, repeat_override_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, roundID, rosterID, slotNumber, override).Scan(&pairingID)
	require.NoError(t, err)
	return pairingID
}

func addSwissMigrationPairingMember(
	t testing.TB,
	ctx context.Context,
	pairingID uuid.UUID,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	seat int,
	participantID uuid.UUID,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_pairing_members (
			pairing_id, round_id, roster_id, seat, participant_id
		)
		VALUES ($1, $2, $3, $4, $5)`, pairingID, roundID, rosterID, seat, participantID)
	require.NoError(t, err)
}

func assertSwissPairingConstraints(
	t testing.TB,
	ctx context.Context,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	pairingID uuid.UUID,
	participantIDs []uuid.UUID,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_pairing_members (
			pairing_id, round_id, roster_id, seat, participant_id
		)
		VALUES ($1, $2, $3, 2, $4)`, pairingID, roundID, rosterID, participantIDs[0])
	require.Error(t, err)

	secondPairingID := createSwissMigrationPairing(t, ctx, roundID, rosterID, 2, uuid.Nil)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_pairing_members (
			pairing_id, round_id, roster_id, seat, participant_id
		)
		VALUES ($1, $2, $3, 1, $4)`, secondPairingID, roundID, rosterID, participantIDs[0])
	require.Error(t, err)
}

func assertSwissByeConstraints(
	t testing.TB,
	ctx context.Context,
	firstRoundID uuid.UUID,
	secondRoundID uuid.UUID,
	rosterID uuid.UUID,
	participantID uuid.UUID,
	decidedAt time.Time,
) {
	t.Helper()
	inputs := `["participant:p1|points:0|buchholz:0|head-to-head:na|effective-time-ns:0|received-bye:false"]`
	seed := bytes.Repeat([]byte{3}, 32)
	digest := bytes.Repeat([]byte{4}, 32)
	var points int
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO arena_swiss_byes (
			round_id, roster_id, participant_id, decision_evidence_id,
			decision_algorithm_version, decision_inputs, decision_seed,
			decision_result, decision_replay_digest, decision_owner_id,
			decided_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 'hmac-sha256-order-v1', $5::jsonb, $6,
			$5::jsonb, $7, $1, $8, $8
		)
		RETURNING points_awarded`,
		firstRoundID, rosterID, participantID, uuid.New(), inputs, seed, digest, decidedAt,
	).Scan(&points)
	require.NoError(t, err)
	require.Equal(t, 1, points)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_byes (
			round_id, roster_id, participant_id, decision_evidence_id,
			decision_algorithm_version, decision_inputs, decision_seed,
			decision_result, decision_replay_digest, decision_owner_id,
			decided_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 'hmac-sha256-order-v1', $5::jsonb, $6,
			$5::jsonb, $7, $1, $8, $8
		)`, secondRoundID, rosterID, participantID, uuid.New(), inputs, seed, digest, decidedAt)
	require.Error(t, err)
}

func assertSwissForeignRosterConstraints(
	t testing.TB,
	ctx context.Context,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	tournamentID uuid.UUID,
	playerID uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()
	otherTournamentID := createArenaMigrationTournament(t, ctx)
	require.NotEqual(t, tournamentID, otherTournamentID)
	otherRosterID := createArenaMigrationRoster(t, ctx, otherTournamentID)
	foreignParticipantID := createSwissMigrationParticipants(t, ctx, otherRosterID, []uuid.UUID{playerID})[0]
	pairingID := createSwissMigrationPairing(t, ctx, roundID, rosterID, 3, uuid.Nil)

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_pairing_members (
			pairing_id, round_id, roster_id, seat, participant_id
		)
		VALUES ($1, $2, $3, 1, $4)`, pairingID, roundID, rosterID, foreignParticipantID)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_swiss_byes (
			round_id, roster_id, participant_id, decision_evidence_id,
			decision_algorithm_version, decision_inputs, decision_seed,
			decision_result, decision_replay_digest, decision_owner_id,
			decided_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 'hmac-sha256-order-v1', '["foreign"]'::jsonb, $5,
			'["foreign"]'::jsonb, $6, $1, $7, $7
		)`, roundID, rosterID, foreignParticipantID, uuid.New(),
		bytes.Repeat([]byte{5}, 32), bytes.Repeat([]byte{6}, 32), createdAt)
	require.Error(t, err)
}
