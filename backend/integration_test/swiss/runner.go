//go:build integration

package swiss

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/swissseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/tournamentseed"
)

var (
	sharedPool   *pgxpool.Pool
	sharedPoolMu sync.Mutex
)

// RunSwissMigration runs the moved Swiss migration assertions against the
// caller-owned integration pool. The temporary pool binding keeps the
// existing assertion helpers small while serializing callers in one process.
func RunSwissMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	require.NotNil(t, pool)
	sharedPoolMu.Lock()
	previousPool := sharedPool
	sharedPool = pool
	t.Cleanup(func() {
		sharedPool = previousPool
		sharedPoolMu.Unlock()
	})

	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	playerIDs := createMigrationPlayers(ctx, t, 6)
	participantIDs := createSwissMigrationParticipants(ctx, t, rosterID, playerIDs[:5])
	createdAt := time.Now().UTC().Truncate(time.Microsecond)

	automaticRoundID := createAutomaticSwissMigrationRound(ctx, t, tournamentID, rosterID, createdAt)
	assertSwissRoundLockRevision(ctx, t, automaticRoundID, createdAt.Add(time.Minute))

	pairingID := createSwissMigrationPairing(ctx, t, automaticRoundID, rosterID, 1, uuid.Nil)
	addSwissMigrationPairingMember(ctx, t, pairingID, automaticRoundID, rosterID, 1, participantIDs[0])
	addSwissMigrationPairingMember(ctx, t, pairingID, automaticRoundID, rosterID, 2, participantIDs[1])
	assertSwissPairingConstraints(ctx, t, automaticRoundID, rosterID, pairingID, participantIDs)

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO swiss_opponent_history (
			pairing_id, round_id, roster_id, prior_meeting_count
		)
		VALUES ($1, $2, $3, 0)`, pairingID, automaticRoundID, rosterID)
	require.NoError(t, err)

	manualRoundID := createManualSwissMigrationRound(ctx, t, tournamentID, rosterID, createdAt.Add(2*time.Minute))
	overrideID := createSwissMigrationRepeatOverride(
		ctx, t, manualRoundID, rosterID, participantIDs, createdAt.Add(3*time.Minute),
	)
	repeatedPairingID := createSwissMigrationPairing(ctx, t, manualRoundID, rosterID, 1, overrideID)
	addSwissMigrationPairingMember(ctx, t, repeatedPairingID, manualRoundID, rosterID, 1, participantIDs[0])
	addSwissMigrationPairingMember(ctx, t, repeatedPairingID, manualRoundID, rosterID, 2, participantIDs[1])

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO swiss_opponent_history (
			pairing_id, round_id, roster_id, prior_meeting_count, repeat_override_id
		)
		VALUES ($1, $2, $3, 1, $4)`, repeatedPairingID, manualRoundID, rosterID, overrideID)
	require.NoError(t, err)

	withoutOverrideID := createSwissMigrationPairing(ctx, t, manualRoundID, rosterID, 2, uuid.Nil)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO swiss_opponent_history (
			pairing_id, round_id, roster_id, prior_meeting_count
		)
		VALUES ($1, $2, $3, 1)`, withoutOverrideID, manualRoundID, rosterID)
	require.Error(t, err)

	assertSwissByeConstraints(
		ctx, t, automaticRoundID, manualRoundID, rosterID, participantIDs[4], createdAt.Add(4*time.Minute),
	)
	assertSwissForeignRosterConstraints(ctx, t, automaticRoundID, rosterID, tournamentID, playerIDs[5], createdAt)
}

func createSwissMigrationParticipants(
	ctx context.Context, tb testing.TB,
	rosterID uuid.UUID,
	playerIDs []uuid.UUID,
) []uuid.UUID {
	tb.Helper()
	participantIDs, err := swissseed.CreateParticipants(ctx, sharedPool, rosterID, playerIDs)
	require.NoError(tb, err)
	return participantIDs
}

func createAutomaticSwissMigrationRound(
	ctx context.Context, tb testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()
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
		INSERT INTO swiss_rounds (
			id, tournament_id, roster_id, round_number, source_roster_revision, source_history_revision,
			generation_kind, pairing_inputs, decision_evidence_id,
			decision_algorithm_version, decision_seed, decision_result,
			decision_replay_digest, decision_owner_id, generated_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, 1, 1, 0, 'automatic', $4::jsonb, $5,
			'hmac-sha256-order-v1', $6, $7::jsonb, $8, $1, $9, $9, $9
		)
		RETURNING revision, pairing_inputs::text, decision_algorithm_version`,
		roundID, tournamentID, rosterID, inputs, uuid.New(), seed, result, digest, createdAt,
	).Scan(&revision, &storedInputs, &storedAlgorithmVersion)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, revision)
	require.JSONEq(tb, inputs, storedInputs)
	require.Equal(tb, "hmac-sha256-order-v1", storedAlgorithmVersion)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO swiss_rounds (
			id, tournament_id, roster_id, round_number, source_roster_revision, source_history_revision,
			generation_kind, pairing_inputs, decision_evidence_id,
			decision_algorithm_version, decision_seed, decision_result,
			decision_replay_digest, decision_owner_id, generated_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, 2, 1, 0, 'automatic', $4::jsonb, $5,
			'hmac-sha256-order-v1', $6, $4::jsonb, $7, $1, $8, $8, $8
		)`, uuid.New(), tournamentID, rosterID, inputs, uuid.New(), []byte{1}, digest, createdAt)
	require.Error(tb, err)

	return roundID
}

func createManualSwissMigrationRound(
	ctx context.Context, tb testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()
	roundID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO swiss_rounds (
			id, tournament_id, roster_id, round_number, source_roster_revision, source_history_revision,
			generation_kind, pairing_inputs, generated_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, 2, 1, 1, 'manual', $4::jsonb, $5, $5, $5)`,
		roundID, tournamentID, rosterID, `["manual-roster-snapshot","prior-meeting-snapshot"]`, createdAt)
	require.NoError(tb, err)
	return roundID
}

func assertSwissRoundLockRevision(
	ctx context.Context, tb testing.TB,
	roundID uuid.UUID,
	lockedAt time.Time,
) {
	tb.Helper()
	var lockRevision int64
	err := sharedPool.QueryRow(ctx, `
		UPDATE swiss_rounds
		SET lock_revision = revision, locked_at = $2, updated_at = $2
		WHERE id = $1
		RETURNING lock_revision`, roundID, lockedAt).Scan(&lockRevision)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, lockRevision)

	_, err = sharedPool.Exec(ctx, `
		UPDATE swiss_rounds
		SET revision = revision + 1, updated_at = $2
		WHERE id = $1`, roundID, lockedAt.Add(time.Minute))
	require.Error(tb, err)
}

func createSwissMigrationRepeatOverride(
	ctx context.Context, tb testing.TB,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	confirmedAt time.Time,
) uuid.UUID {
	tb.Helper()
	overrideID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO swiss_repeat_overrides (
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
	require.NoError(tb, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO swiss_repeat_overrides (
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
	require.Error(tb, err)

	return overrideID
}

func createSwissMigrationPairing(
	ctx context.Context, tb testing.TB,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	slotNumber int,
	overrideID uuid.UUID,
) uuid.UUID {
	tb.Helper()
	var override *uuid.UUID
	if overrideID != uuid.Nil {
		override = &overrideID
	}
	pairingID, err := swissseed.CreatePairing(
		ctx,
		sharedPool,
		roundID,
		rosterID,
		slotNumber,
		override,
	)
	require.NoError(tb, err)
	return pairingID
}

func addSwissMigrationPairingMember(
	ctx context.Context, tb testing.TB,
	pairingID uuid.UUID,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	seat int,
	participantID uuid.UUID,
) {
	tb.Helper()
	err := swissseed.AddPairingMember(
		ctx,
		sharedPool,
		pairingID,
		roundID,
		rosterID,
		seat,
		participantID,
	)
	require.NoError(tb, err)
}

func assertSwissPairingConstraints(
	ctx context.Context, tb testing.TB,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	pairingID uuid.UUID,
	participantIDs []uuid.UUID,
) {
	tb.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO swiss_pairing_members (
			pairing_id, round_id, roster_id, seat, participant_id
		)
		VALUES ($1, $2, $3, 2, $4)`, pairingID, roundID, rosterID, participantIDs[0])
	require.Error(tb, err)

	secondPairingID := createSwissMigrationPairing(ctx, tb, roundID, rosterID, 2, uuid.Nil)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO swiss_pairing_members (
			pairing_id, round_id, roster_id, seat, participant_id
		)
		VALUES ($1, $2, $3, 1, $4)`, secondPairingID, roundID, rosterID, participantIDs[0])
	require.Error(tb, err)
}

func assertSwissByeConstraints(
	ctx context.Context, tb testing.TB,
	firstRoundID uuid.UUID,
	secondRoundID uuid.UUID,
	rosterID uuid.UUID,
	participantID uuid.UUID,
	decidedAt time.Time,
) {
	tb.Helper()
	inputs := `["participant:p1|points:0|buchholz:0|head-to-head:na|effective-time-ns:0|received-bye:false"]`
	seed := bytes.Repeat([]byte{3}, 32)
	digest := bytes.Repeat([]byte{4}, 32)
	var points int
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO swiss_byes (
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
	require.NoError(tb, err)
	require.Equal(tb, 1, points)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO swiss_byes (
			round_id, roster_id, participant_id, decision_evidence_id,
			decision_algorithm_version, decision_inputs, decision_seed,
			decision_result, decision_replay_digest, decision_owner_id,
			decided_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 'hmac-sha256-order-v1', $5::jsonb, $6,
			$5::jsonb, $7, $1, $8, $8
		)`, secondRoundID, rosterID, participantID, uuid.New(), inputs, seed, digest, decidedAt)
	require.Error(tb, err)
}

func assertSwissForeignRosterConstraints(
	ctx context.Context, tb testing.TB,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	tournamentID uuid.UUID,
	playerID uuid.UUID,
	createdAt time.Time,
) {
	tb.Helper()
	otherTournamentID := createMigrationTournament(ctx, tb)
	require.NotEqual(tb, tournamentID, otherTournamentID)
	otherRosterID := createMigrationRoster(ctx, tb, otherTournamentID)
	foreignParticipantID := createSwissMigrationParticipants(ctx, tb, otherRosterID, []uuid.UUID{playerID})[0]
	pairingID := createSwissMigrationPairing(ctx, tb, roundID, rosterID, 3, uuid.Nil)

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO swiss_pairing_members (
			pairing_id, round_id, roster_id, seat, participant_id
		)
		VALUES ($1, $2, $3, 1, $4)`, pairingID, roundID, rosterID, foreignParticipantID)
	require.Error(tb, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO swiss_byes (
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
	require.Error(tb, err)
}

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	_, err := sharedPool.Exec(ctx, `
		TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	require.NoError(tb, err)
}

func createMigrationTournament(ctx context.Context, tb testing.TB) uuid.UUID {
	tb.Helper()
	id, err := tournamentseed.CreateTournament(ctx, sharedPool)
	require.NoError(tb, err)
	return id
}

func createMigrationRoster(ctx context.Context, tb testing.TB, tournamentID uuid.UUID) uuid.UUID {
	tb.Helper()
	seed, err := tournamentseed.CreateRoster(ctx, sharedPool, tournamentID)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, seed.Revision)
	return seed.ID
}

func createMigrationPlayers(ctx context.Context, tb testing.TB, count int) []uuid.UUID {
	tb.Helper()
	ids, err := tournamentseed.CreatePlayers(ctx, sharedPool, "swiss_migration", count)
	require.NoError(tb, err)
	return ids
}
