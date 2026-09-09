//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

type goldenExactPlanAuthorityFixture struct {
	golden                   goldenMigrationFixture
	draft                    draftMigrationFixture
	exactAssignmentPlanID    uuid.UUID
	assignmentBranchID       uuid.UUID
	sourceProjectionID       uuid.UUID
	sourceProjectionRevision int64
	sourceArtifactID         uuid.UUID
	sourceArtifactDigest     []byte
	groupID                  uuid.UUID
	groupRevisionID          uuid.UUID
	reservations             []assignmentReservationFixture
}

func TestGoldenExactPlanAuthoritySealsCompleteAggregate(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenExactPlanAuthorityFixture(ctx, t)
	planID := createSealedGoldenExactPlan(ctx, t, fixture)

	t.Run("sealed children reject mutation and late insert", func(t *testing.T) {
		for _, table := range []string{
			"golden_exact_plan_snapshot_groups",
			"golden_exact_plan_snapshot_members",
			"golden_exact_plan_snapshot_edges",
			"golden_exact_plan_snapshot_candidates",
			"golden_exact_plan_snapshot_participant_reservations",
			"golden_exact_plan_snapshot_reservations",
		} {
			t.Run(table, func(t *testing.T) {
				_, err := sharedPool.Exec(ctx, `UPDATE `+table+` SET created_at = created_at WHERE plan_id = $1`, planID)
				require.ErrorContains(t, err, "immutable")

				_, err = sharedPool.Exec(ctx, `DELETE FROM `+table+` WHERE plan_id = $1`, planID)
				require.ErrorContains(t, err, "immutable")
			})
		}

		_, err := sharedPool.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_candidates (
				plan_id, tournament_id, roster_id, pool_revision_id,
				task_id, task_version, exists_in_source, enabled, healthy,
				mutation_locked, publicly_exposed, artifact_digest, created_at
			)
			SELECT plan_id, tournament_id, roster_id, pool_revision_id,
				task_id, task_version, exists_in_source, enabled, healthy,
				mutation_locked, publicly_exposed, artifact_digest, $2
			FROM golden_exact_plan_snapshot_candidates
			WHERE plan_id = $1
			LIMIT 1`, planID, fixture.golden.createdAt.Add(time.Hour))
		require.ErrorContains(t, err, "sealed")

		_, err = sharedPool.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_history (
				plan_id, tournament_id, roster_id, participant_id,
				task_id, task_version, created_at
			)
			SELECT $1, $2, $3, $4, candidate.task_id, candidate.task_version, $5
			FROM golden_exact_plan_snapshot_candidates AS candidate
			WHERE candidate.plan_id = $1
			LIMIT 1`,
			planID,
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			fixture.golden.participantIDs[0],
			fixture.golden.createdAt.Add(time.Hour),
		)
		require.ErrorContains(t, err, "sealed")
	})

	t.Run("unsealed member scope is composite", func(t *testing.T) {
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { require.NoError(t, tx.Rollback(ctx)) }()

		unsealedPlanID := insertGoldenExactPlanRoot(ctx, t, tx, fixture)
		insertGoldenExactPlanGroup(ctx, t, tx, fixture, unsealedPlanID)

		_, err = tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_members (
				plan_id, group_revision_id, tournament_id, roster_id,
				participant_id, position, created_at
			)
			VALUES ($1, $2, $3, $4, $5, 1, $6)`,
			unsealedPlanID,
			fixture.groupRevisionID,
			uuid.New(),
			uuid.New(),
			fixture.golden.participantIDs[0],
			fixture.golden.createdAt,
		)
		require.Error(t, err)
	})

	t.Run("unsealed edge scope is composite", func(t *testing.T) {
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { require.NoError(t, tx.Rollback(ctx)) }()

		unsealedPlanID := insertGoldenExactPlanRoot(ctx, t, tx, fixture)
		insertGoldenExactPlanGroup(ctx, t, tx, fixture, unsealedPlanID)

		_, err = tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_edges (
				plan_id, group_revision_id, tournament_id, roster_id,
				edge_id, reservation_id, snapshot_id, task_id, task_version,
				position, content_digest, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, 1, $9, $10)`,
			unsealedPlanID,
			fixture.groupRevisionID,
			uuid.New(),
			uuid.New(),
			uuid.New(),
			fixture.reservations[0].reservationID,
			fixture.reservations[0].snapshotID,
			fixture.reservations[0].taskID,
			goldenAuthorityDigest(71),
			fixture.golden.createdAt,
		)
		require.Error(t, err)
	})

	t.Run("zero group aggregate cannot seal", func(t *testing.T) {
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)

		unsealedPlanID := insertGoldenExactPlanRoot(ctx, t, tx, fixture)
		sealGoldenExactPlan(ctx, t, tx, fixture, unsealedPlanID)
		err = tx.Commit(ctx)
		require.ErrorContains(t, err, "missing canonical authority children")
	})
}

func TestGoldenStateAuthoritySealsScopeAndLineage(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenExactPlanAuthorityFixture(ctx, t)
	planID := createSealedGoldenExactPlan(ctx, t, fixture)
	initialStateID := createInitialSealedGoldenState(ctx, t, fixture, planID)

	t.Run("sealed state children reject mutation and late insert", func(t *testing.T) {
		for _, table := range []string{
			"golden_state_transitions",
			"golden_state_members",
		} {
			t.Run(table+" mutation", func(t *testing.T) {
				_, err := sharedPool.Exec(ctx, `UPDATE `+table+` SET created_at = created_at WHERE state_revision_id = $1`, initialStateID)
				require.ErrorContains(t, err, "immutable")

				_, err = sharedPool.Exec(ctx, `DELETE FROM `+table+` WHERE state_revision_id = $1`, initialStateID)
				require.ErrorContains(t, err, "immutable")
			})
		}

		for _, table := range []string{
			"golden_state_transitions",
			"golden_state_members",
			"golden_state_attempts",
			"golden_state_attempt_members",
			"golden_state_ready_windows",
			"golden_state_ready_window_participants",
			"golden_state_ready_events",
			"golden_state_no_show_resolutions",
			"golden_state_no_show_participants",
			"golden_state_allocations",
			"golden_state_allocation_inputs",
			"golden_state_allocation_positions",
		} {
			t.Run(table, func(t *testing.T) {
				_, err := sharedPool.Exec(ctx, `INSERT INTO `+table+` (state_revision_id) VALUES ($1)`, initialStateID)
				require.ErrorContains(t, err, "sealed")
			})
		}
	})

	t.Run("transition predecessor must equal its root predecessor", func(t *testing.T) {
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { require.NoError(t, tx.Rollback(ctx)) }()

		stateID := insertGoldenStateRevision(
			ctx,
			t,
			tx,
			fixture,
			planID,
			2,
			initialStateID,
		)
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_state_transitions (
				state_revision_id, tournament_id, roster_id, group_id, group_revision_id,
				transition_kind, command_id, previous_state_revision_id, occurred_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, 'ready', $6, $7, $8, $8)`,
			stateID,
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			fixture.groupID,
			fixture.groupRevisionID,
			uuid.New(),
			uuid.New(),
			fixture.golden.createdAt.Add(2*time.Hour),
		)
		require.Error(t, err)
	})

	other := createGoldenMigrationFixture(ctx, t, 2)
	for _, testCase := range []struct {
		name   string
		insert func(t *testing.T, tx pgx.Tx, state goldenUnsealedStateFixture)
	}{
		{
			name: "attempt member",
			insert: func(t *testing.T, tx pgx.Tx, state goldenUnsealedStateFixture) {
				_, err := tx.Exec(ctx, `
					INSERT INTO golden_state_attempt_members (
						state_revision_id, attempt_id, tournament_id, roster_id,
						participant_id, position, created_at
					)
					VALUES ($1, $2, $3, $4, $5, 1, $6)`,
					state.stateID, state.attemptID, other.tournamentID, other.rosterID,
					other.participantIDs[0], fixture.golden.createdAt)
				require.Error(t, err)
			},
		},
		{
			name: "ready window participant",
			insert: func(t *testing.T, tx pgx.Tx, state goldenUnsealedStateFixture) {
				_, err := tx.Exec(ctx, `
					INSERT INTO golden_state_ready_window_participants (
						state_revision_id, window_id, tournament_id, roster_id,
						participant_id, membership_kind, position, created_at
					)
					VALUES ($1, $2, $3, $4, $5, 'ready', 1, $6)`,
					state.stateID, state.windowID, other.tournamentID, other.rosterID,
					other.participantIDs[0], fixture.golden.createdAt)
				require.Error(t, err)
			},
		},
		{
			name: "no show participant",
			insert: func(t *testing.T, tx pgx.Tx, state goldenUnsealedStateFixture) {
				_, err := tx.Exec(ctx, `
					INSERT INTO golden_state_no_show_participants (
						state_revision_id, command_id, tournament_id, roster_id,
						participant_id, membership_kind, position, created_at
					)
					VALUES ($1, $2, $3, $4, $5, 'excluded', 1, $6)`,
					state.stateID, state.noShowCommandID, other.tournamentID, other.rosterID,
					other.participantIDs[0], fixture.golden.createdAt)
				require.Error(t, err)
			},
		},
		{
			name: "allocation input",
			insert: func(t *testing.T, tx pgx.Tx, state goldenUnsealedStateFixture) {
				_, err := tx.Exec(ctx, `
					INSERT INTO golden_state_allocation_inputs (
						allocation_id, state_revision_id, tournament_id, roster_id,
						participant_id, points, buchholz, head_to_head_points,
						head_to_head_applied, effective_time_milliseconds,
						accepted_solve_time_milliseconds, stable_seed, position, created_at
					)
					VALUES ($1, $2, $3, $4, $5, 0, 0, 0, false, 0, NULL, 1, 1, $6)`,
					state.allocationID, state.stateID, other.tournamentID, other.rosterID,
					other.participantIDs[0], fixture.golden.createdAt)
				require.Error(t, err)
			},
		},
		{
			name: "allocation position",
			insert: func(t *testing.T, tx pgx.Tx, state goldenUnsealedStateFixture) {
				_, err := tx.Exec(ctx, `
					INSERT INTO golden_state_allocation_positions (
						allocation_id, state_revision_id, tournament_id, roster_id,
						position, participant_id, position_kind, created_at
					)
					VALUES ($1, $2, $3, $4, 1, $5, 'direct', $6)`,
					state.allocationID, state.stateID, other.tournamentID, other.rosterID,
					other.participantIDs[0], fixture.golden.createdAt)
				require.Error(t, err)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tx, err := sharedPool.Begin(ctx)
			require.NoError(t, err)
			defer func() { require.NoError(t, tx.Rollback(ctx)) }()
			state := insertUnsealedGoldenStateScaffold(ctx, t, tx, fixture, planID, initialStateID)
			testCase.insert(t, tx, state)
		})
	}
}

func TestGoldenPositionLedgerSealsExactAttemptEvidence(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenExactPlanAuthorityFixture(ctx, t)
	authority := createGoldenLedgerAttemptAuthority(ctx, t, fixture)
	ledgerID := createSealedGoldenPositionLedger(ctx, t, fixture, authority)

	for _, table := range []string{
		"golden_position_ledger_attempts",
		"golden_position_ledger_commit_bindings",
	} {
		t.Run(table+" mutation", func(t *testing.T) {
			_, err := sharedPool.Exec(ctx, `UPDATE `+table+` SET created_at = created_at WHERE ledger_revision_id = $1`, ledgerID)
			require.ErrorContains(t, err, "immutable")

			_, err = sharedPool.Exec(ctx, `DELETE FROM `+table+` WHERE ledger_revision_id = $1`, ledgerID)
			require.ErrorContains(t, err, "immutable")
		})
	}

	t.Run("late ledger binding is rejected", func(t *testing.T) {
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO golden_position_ledger_commit_bindings (
				ledger_revision_id, attempt_id, position_commit_id,
				tournament_id, roster_id, participant_id,
				position, evidence_digest, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, 2, $7, $8)`,
			ledgerID,
			authority.goldenAttemptID,
			uuid.New(),
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			authority.participantID,
			authority.payloadDigest,
			fixture.golden.createdAt.Add(time.Hour),
		)
		require.ErrorContains(t, err, "sealed")
	})

	t.Run("ledger attempt cannot bind a different Golden group", func(t *testing.T) {
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { require.NoError(t, tx.Rollback(ctx)) }()

		nextLedgerID := uuid.New()
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_position_ledger_revisions (
				revision_id, tournament_id, roster_id, group_revision_id,
				revision_number, previous_revision_id, payload_digest, finalized_at, created_at
			)
			VALUES ($1, $2, $3, $4, 2, $5, $6, $7, $7)`,
			nextLedgerID,
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			fixture.groupRevisionID,
			ledgerID,
			authority.payloadDigest,
			fixture.golden.createdAt.Add(time.Hour),
		)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_position_ledger_attempts (
				ledger_revision_id, attempt_id, submission_revision_id,
				tournament_id, roster_id, group_revision_id,
				attempt_number, order_count, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8)`,
			nextLedgerID,
			authority.goldenAttemptID,
			authority.submissionRevisionID,
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			uuid.New(),
			authority.attemptNumber,
			fixture.golden.createdAt.Add(time.Hour),
		)
		require.Error(t, err)
	})

	t.Run("submission revision is append only", func(t *testing.T) {
		_, err := sharedPool.Exec(ctx, `
			UPDATE golden_attempt_submission_revisions
			SET created_at = created_at
			WHERE revision_id = $1`, authority.submissionRevisionID)
		require.ErrorContains(t, err, "append-only")

		_, err = sharedPool.Exec(ctx, `
			DELETE FROM golden_attempt_submission_revisions
			WHERE revision_id = $1`, authority.submissionRevisionID)
		require.ErrorContains(t, err, "append-only")
	})
}

func TestGoldenCorrectionTombstoneRequiresCommittedScopedCorrection(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenExactPlanAuthorityFixture(ctx, t)

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO golden_correction_group_tombstones (
			command_id, tournament_id, roster_id,
			group_id, group_revision_id, successor_revision_id,
			superseded_at, proof_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, now(), $7, now())`,
		uuid.New(),
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupID,
		fixture.groupRevisionID,
		uuid.New(),
		goldenAuthorityDigest(91),
	)
	require.ErrorContains(t, err, "golden_correction_group_tombstones_correction_fk")
}

type goldenLedgerAttemptAuthority struct {
	goldenAttemptID      uuid.UUID
	submissionRevisionID uuid.UUID
	positionCommitID     uuid.UUID
	participantID        uuid.UUID
	payloadDigest        []byte
	attemptNumber        int
}

func createGoldenLedgerAttemptAuthority(
	ctx context.Context,
	t testing.TB,
	fixture goldenExactPlanAuthorityFixture,
) goldenLedgerAttemptAuthority {
	t.Helper()

	releasedBranchID := createAssignmentBranch(
		ctx,
		t,
		fixture.exactAssignmentPlanID,
		fixture.draft,
		"golden-ledger-reserve",
		`["web"]`,
		fixture.golden.createdAt.Add(20*time.Minute),
	)
	releasedReservations := createAssignmentBranchReservations(
		ctx,
		t,
		fixture.exactAssignmentPlanID,
		releasedBranchID,
		"web",
		fixture.golden.createdAt.Add(20*time.Minute),
	)
	var err error
	for _, reservation := range fixture.reservations {
		_, err = sharedPool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'committed', revision = revision + 1, committed_at = $2
			WHERE id = $1`, reservation.reservationID, fixture.golden.createdAt.Add(20*time.Minute))
		require.NoError(t, err)
	}
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'active', activated_at = $2
		WHERE id = $1`, fixture.assignmentBranchID, fixture.golden.createdAt.Add(20*time.Minute))
	require.NoError(t, err)
	for _, reservation := range releasedReservations {
		_, err = sharedPool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'released', revision = revision + 1, released_at = $2, release_reason = 'unused Golden ledger branch'
			WHERE id = $1`, reservation.reservationID, fixture.golden.createdAt.Add(20*time.Minute))
		require.NoError(t, err)
	}
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'released', released_at = $2, release_reason = 'unused Golden ledger branch'
		WHERE id = $1`, releasedBranchID, fixture.golden.createdAt.Add(20*time.Minute))
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_plans
		SET state = 'committed', active_branch_id = $2, committed_at = $3
		WHERE id = $1`,
		fixture.exactAssignmentPlanID,
		fixture.assignmentBranchID,
		fixture.golden.createdAt.Add(20*time.Minute),
	)
	require.NoError(t, err)

	waveID, _ := createMigrationWave(
		ctx,
		t,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.golden.participantIDs,
		fixture.golden.createdAt.Add(21*time.Minute),
	)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO wave_series (wave_id, tournament_id, roster_id, series_id, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		waveID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.draft.seriesID,
		fixture.golden.createdAt.Add(21*time.Minute),
	)
	require.NoError(t, err)

	slotID := createMigrationGameSlot(ctx, t, fixture.draft.seriesID, fixture.golden.rosterID, 1, "web")
	var gameAttemptID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		INSERT INTO game_attempts (
			slot_id, series_id, roster_id, attempt_number, state, created_at, updated_at
		)
		VALUES ($1, $2, $3, 1, 'planned', $4, $4)
		RETURNING id`,
		slotID,
		fixture.draft.seriesID,
		fixture.golden.rosterID,
		fixture.golden.createdAt.Add(21*time.Minute),
	).Scan(&gameAttemptID))
	assignmentID := createActiveMigrationAssignment(
		ctx,
		t,
		gameAttemptID,
		fixture.draft,
		fixture.exactAssignmentPlanID,
		fixture.assignmentBranchID,
		fixture.reservations[0],
		nil,
		fixture.golden.createdAt.Add(21*time.Minute),
	)

	authority := goldenLedgerAttemptAuthority{}
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT position_commit.id,
			position_commit.attempt_id,
			position_commit.participant_id,
			submission.id,
			submission.payload_digest,
			attempt.attempt_number
		FROM golden_position_commits AS position_commit
		INNER JOIN golden_provisional_submissions AS submission
			ON submission.id = position_commit.provisional_submission_id
			AND submission.attempt_id = position_commit.attempt_id
			AND submission.membership_id = position_commit.membership_id
			AND submission.participant_id = position_commit.participant_id
		INNER JOIN golden_attempts AS attempt ON attempt.id = position_commit.attempt_id
		WHERE position_commit.tournament_id = $1
			AND position_commit.roster_id = $2
		ORDER BY position_commit.committed_at
		LIMIT 1`, fixture.golden.tournamentID, fixture.golden.rosterID).Scan(
		&authority.positionCommitID,
		&authority.goldenAttemptID,
		&authority.participantID,
		&authority.submissionRevisionID,
		&authority.payloadDigest,
		&authority.attemptNumber,
	))

	var membershipID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT membership_id
		FROM golden_position_commits
		WHERE id = $1`, authority.positionCommitID).Scan(&membershipID))

	stageAt := fixture.golden.createdAt.Add(22 * time.Minute)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO golden_attempt_stage_groups (
			attempt_id, tournament_id, roster_id, group_revision_id, bound_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		authority.goldenAttemptID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupRevisionID,
		stageAt,
	)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO golden_attempt_authorities (
			attempt_id, tournament_id, roster_id, group_revision_id,
			wave_id, assignment_id, snapshot_id, task_id, task_version,
			source_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		authority.goldenAttemptID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupRevisionID,
		waveID,
		assignmentID,
		fixture.reservations[0].snapshotID,
		fixture.reservations[0].taskID,
		fixture.reservations[0].taskVersion,
		authority.payloadDigest,
		stageAt,
	)
	require.NoError(t, err)

	authority.submissionRevisionID = uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO golden_attempt_submission_revisions (
			revision_id, attempt_id, tournament_id, roster_id,
			membership_id, participant_id, revision_number, previous_revision_id,
			provisional_submission_id, payload_digest, committed_at, created_at
		)
		SELECT $1, $2, $3, $4, $5, $6, 1, NULL, submission.id,
			submission.payload_digest, submission.received_at, $7
		FROM golden_provisional_submissions AS submission
		WHERE submission.attempt_id = $2
			AND submission.membership_id = $5
			AND submission.participant_id = $6`,
		authority.submissionRevisionID,
		authority.goldenAttemptID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		membershipID,
		authority.participantID,
		stageAt,
	)
	require.NoError(t, err)
	return authority
}

func createSealedGoldenPositionLedger(
	ctx context.Context,
	t testing.TB,
	fixture goldenExactPlanAuthorityFixture,
	authority goldenLedgerAttemptAuthority,
) uuid.UUID {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	ledgerID := uuid.New()
	sealedAt := fixture.golden.createdAt.Add(23 * time.Minute)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_revisions (
			revision_id, tournament_id, roster_id, group_revision_id,
			revision_number, previous_revision_id, payload_digest, finalized_at, created_at
		)
		VALUES ($1, $2, $3, $4, 1, NULL, $5, $6, $6)`,
		ledgerID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupRevisionID,
		authority.payloadDigest,
		sealedAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_attempts (
			ledger_revision_id, attempt_id, submission_revision_id,
			tournament_id, roster_id, group_revision_id,
			attempt_number, order_count, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8)`,
		ledgerID,
		authority.goldenAttemptID,
		authority.submissionRevisionID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupRevisionID,
		authority.attemptNumber,
		sealedAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_commit_bindings (
			ledger_revision_id, attempt_id, position_commit_id,
			tournament_id, roster_id, participant_id,
			position, evidence_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $8)`,
		ledgerID,
		authority.goldenAttemptID,
		authority.positionCommitID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		authority.participantID,
		authority.payloadDigest,
		sealedAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_revision_seals (
			ledger_revision_id, tournament_id, roster_id, payload_digest, sealed_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		ledgerID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		authority.payloadDigest,
		sealedAt,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return ledgerID
}

type goldenUnsealedStateFixture struct {
	stateID         uuid.UUID
	attemptID       uuid.UUID
	windowID        uuid.UUID
	noShowCommandID uuid.UUID
	allocationID    uuid.UUID
}

func createInitialSealedGoldenState(
	ctx context.Context,
	t testing.TB,
	fixture goldenExactPlanAuthorityFixture,
	planID uuid.UUID,
) uuid.UUID {
	t.Helper()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	stateID := insertGoldenStateRevision(ctx, t, tx, fixture, planID, 1, nil)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_transitions (
			state_revision_id, tournament_id, roster_id, group_id, group_revision_id,
			transition_kind, command_id, previous_state_revision_id, occurred_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 'initial', NULL, NULL, $6, $6)`,
		stateID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupID,
		fixture.groupRevisionID,
		fixture.golden.createdAt.Add(2*time.Minute),
	)
	require.NoError(t, err)
	insertGoldenStateMembers(ctx, t, tx, fixture, stateID)
	sealGoldenState(ctx, t, tx, fixture, stateID)
	require.NoError(t, tx.Commit(ctx))
	return stateID
}

func insertGoldenStateRevision(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	planID uuid.UUID,
	revisionNumber int64,
	previousRevisionID any,
) uuid.UUID {
	t.Helper()
	stateID := uuid.New()
	_, err := tx.Exec(ctx, `
		INSERT INTO golden_state_revisions (
			revision_id, tournament_id, roster_id, group_id, group_revision_id,
			revision_number, previous_revision_id, plan_id,
			membership_revision_id, membership_revision, membership_previous_revision_id,
			membership_digest, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, NULL, $10, $11, $12)`,
		stateID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupID,
		fixture.groupRevisionID,
		revisionNumber,
		previousRevisionID,
		planID,
		uuid.New(),
		goldenAuthorityDigest(73),
		goldenAuthorityDigest(74),
		fixture.golden.createdAt.Add(2*time.Minute),
	)
	require.NoError(t, err)
	return stateID
}

func insertGoldenStateMembers(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	stateID uuid.UUID,
) {
	t.Helper()
	for index, participantID := range fixture.golden.participantIDs {
		_, err := tx.Exec(ctx, `
			INSERT INTO golden_state_members (
				state_revision_id, tournament_id, roster_id,
				participant_id, excluded, position, created_at
			)
			VALUES ($1, $2, $3, $4, false, $5, $6)`,
			stateID,
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			participantID,
			index+1,
			fixture.golden.createdAt.Add(2*time.Minute),
		)
		require.NoError(t, err)
	}
}

func sealGoldenState(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	stateID uuid.UUID,
) {
	t.Helper()
	_, err := tx.Exec(ctx, `
		INSERT INTO golden_state_revision_seals (
			state_revision_id, tournament_id, roster_id, payload_digest, sealed_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		stateID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		goldenAuthorityDigest(74),
		fixture.golden.createdAt.Add(3*time.Minute),
	)
	require.NoError(t, err)
}

func insertUnsealedGoldenStateScaffold(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	planID uuid.UUID,
	previousStateID uuid.UUID,
) goldenUnsealedStateFixture {
	t.Helper()
	stateID := insertGoldenStateRevision(ctx, t, tx, fixture, planID, 2, previousStateID)
	attemptID := uuid.New()
	var previousAttemptID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT id
		FROM golden_attempts
		WHERE tournament_id = $1 AND roster_id = $2
		ORDER BY attempt_number DESC
		LIMIT 1
		FOR KEY SHARE`, fixture.golden.tournamentID, fixture.golden.rosterID).Scan(&previousAttemptID))
	_, err := tx.Exec(ctx, `
		INSERT INTO golden_attempts (
			id, tournament_id, roster_id, attempt_number, previous_attempt_id, created_at
		)
		VALUES ($1, $2, $3, 2, $4, $5)`,
		attemptID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		previousAttemptID,
		fixture.golden.createdAt.Add(3*time.Minute),
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_attempts (
			state_revision_id, tournament_id, roster_id, group_revision_id,
			attempt_id, attempt_number, previous_attempt_id, state,
			retained_at, started_at, finished_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 2, $6, 'planned', NULL, NULL, NULL, $7)`,
		stateID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupRevisionID,
		attemptID,
		previousAttemptID,
		fixture.golden.createdAt.Add(3*time.Minute),
	)
	require.NoError(t, err)
	windowID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_ready_windows (
			state_revision_id, tournament_id, roster_id, window_id,
			window_revision_id, window_revision, window_previous_revision_id,
			attempt_id, attempt_number, opened_at, deadline, state,
			readiness_revision_id, readiness_revision, readiness_previous_revision_id,
			readiness_digest, presence_revision_id, presence_revision,
			presence_previous_revision_id, presence_digest, created_at
		)
		VALUES (
			$1, $2, $3, $4,
			$5, 1, NULL,
			$6, 2, $7, $8, 'open',
			$9, 1, NULL,
			$10, $11, 1, NULL, $12, $13
		)`,
		stateID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		windowID,
		uuid.New(),
		attemptID,
		fixture.golden.createdAt.Add(3*time.Minute),
		fixture.golden.createdAt.Add(4*time.Minute),
		uuid.New(),
		goldenAuthorityDigest(75),
		uuid.New(),
		goldenAuthorityDigest(76),
		fixture.golden.createdAt.Add(3*time.Minute),
	)
	require.NoError(t, err)
	noShowCommandID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_no_show_resolutions (
			command_id, state_revision_id, tournament_id, roster_id, group_id, group_revision_id,
			attempt_id, attempt_number, window_id, expected_state_revision_id,
			expected_state_revision, expected_state_payload_digest,
			expected_window_revision_id, expected_window_revision,
			expected_readiness_revision_id, expected_readiness_revision, expected_readiness_digest,
			expected_presence_revision_id, expected_presence_revision, expected_presence_digest,
			result_window_revision_id, result_membership_revision_id, deadline, resolved_at,
			readiness_revision_id, readiness_revision, readiness_digest,
			presence_revision_id, presence_revision, presence_digest, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, 2, $8, $2,
			2, $9,
			$10, 1,
			$11, 1, $12,
			$13, 1, $14,
			$15, $16, $17, $17,
			$18, 1, $19,
			$20, 1, $21, $22
		)`,
		noShowCommandID,
		stateID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupID,
		fixture.groupRevisionID,
		attemptID,
		windowID,
		goldenAuthorityDigest(74),
		uuid.New(),
		uuid.New(),
		goldenAuthorityDigest(75),
		uuid.New(),
		goldenAuthorityDigest(76),
		uuid.New(),
		uuid.New(),
		fixture.golden.createdAt.Add(4*time.Minute),
		uuid.New(),
		goldenAuthorityDigest(77),
		uuid.New(),
		goldenAuthorityDigest(78),
		fixture.golden.createdAt.Add(4*time.Minute),
	)
	require.NoError(t, err)
	allocationID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_allocations (
			allocation_id, command_id, state_revision_id, tournament_id, roster_id,
			group_id, group_revision_id, expected_state_revision_id,
			expected_state_revision, expected_state_payload_digest,
			allocated_at, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $3, 2, $8, $9, $10, $9)`,
		allocationID,
		uuid.New(),
		stateID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupID,
		fixture.groupRevisionID,
		goldenAuthorityDigest(74),
		fixture.golden.createdAt.Add(4*time.Minute),
		goldenAuthorityDigest(79),
	)
	require.NoError(t, err)
	return goldenUnsealedStateFixture{
		stateID:         stateID,
		attemptID:       attemptID,
		windowID:        windowID,
		noShowCommandID: noShowCommandID,
		allocationID:    allocationID,
	}
}

func createGoldenExactPlanAuthorityFixture(
	ctx context.Context,
	t testing.TB,
) goldenExactPlanAuthorityFixture {
	t.Helper()

	draft := createDraftMigrationFixture(ctx, t)
	createdAt := draft.createdAt.Add(20 * time.Second)
	conservativePlanID := createConservativeAssignmentPlan(ctx, t, draft, createdAt)
	exactPlanID := createExactAssignmentPlan(ctx, t, draft, conservativePlanID, createdAt.Add(time.Second))
	branchID := createAssignmentBranch(ctx, t, exactPlanID, draft, "golden-proof", `[`+`"web"`+`]`, createdAt.Add(2*time.Second))
	reservations := createAssignmentBranchReservations(ctx, t, exactPlanID, branchID, "web", createdAt.Add(3*time.Second))

	golden := goldenMigrationFixture{
		tournamentID:   draft.tournamentID,
		rosterID:       draft.rosterID,
		participantIDs: draft.participantIDs,
		createdAt:      createdAt,
	}
	var (
		sourceProjectionID       uuid.UUID
		sourceProjectionRevision int64
		sourceArtifactID         uuid.UUID
		sourceArtifactDigest     []byte
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision.id, revision.revision_number, artifact.id, artifact.payload_digest
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
		WHERE revision.tournament_id = $1
			AND revision.roster_id = $2
			AND revision.state = 'published'
		ORDER BY revision.revision_number DESC
		LIMIT 1`, golden.tournamentID, golden.rosterID).Scan(
		&sourceProjectionID,
		&sourceProjectionRevision,
		&sourceArtifactID,
		&sourceArtifactDigest,
	))

	stageCommandID := uuid.New()
	groupID := uuid.New()
	groupRevisionID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	stageAt := createdAt.Add(12 * time.Second)
	insertGoldenStageLifecycleCommand(
		ctx,
		t,
		tx,
		golden,
		stageCommandID,
		sourceProjectionID,
		sourceProjectionRevision,
		stageAt,
	)
	_, err = tx.Exec(ctx, `
		INSERT INTO tournament_stage_progressions (
			command_id, tournament_id, roster_id, actor_id, action,
			source_tournament_revision, source_tournament_state,
			source_projection_revision_id, source_projection_revision,
			resulting_tournament_revision, resulting_tournament_state,
			proof, proof_digest, executed_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 'start_golden',
			1, 'swiss', $5, $6,
			2, 'golden', '{"proof":"golden-exact-plan"}'::JSONB, $7, $8, $9
		)`,
		stageCommandID,
		golden.tournamentID,
		golden.rosterID,
		golden.participantIDs[0],
		sourceProjectionID,
		sourceProjectionRevision,
		goldenAuthorityDigest(62),
		stageAt,
		stageAt.Add(time.Microsecond),
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO tournament_stage_tie_groups (
			command_id, tournament_id, roster_id, group_id, group_revision_id,
			source_projection_revision_id, source_projection_revision,
			position_from, position_to, proof, proof_digest, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, 1, 2, '{"range":"1-2"}'::JSONB, $8, $9
		)`,
		stageCommandID,
		golden.tournamentID,
		golden.rosterID,
		groupID,
		groupRevisionID,
		sourceProjectionID,
		sourceProjectionRevision,
		goldenAuthorityDigest(63),
		stageAt,
	)
	require.NoError(t, err)
	for index, participantID := range golden.participantIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO tournament_stage_tie_group_members (
				command_id, tournament_id, roster_id, group_id,
				participant_id, standing_position, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			stageCommandID,
			golden.tournamentID,
			golden.rosterID,
			groupID,
			participantID,
			index+1,
			stageAt,
		)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_group_revisions (
			revision_id, group_id, stage_progression_command_id,
			tournament_id, roster_id,
			source_projection_revision_id, source_projection_revision,
			position_from, position_to, definition, definition_digest, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, 1, 2, '{"range":"1-2"}'::JSONB, $8, $9
		)`,
		groupRevisionID,
		groupID,
		stageCommandID,
		golden.tournamentID,
		golden.rosterID,
		sourceProjectionID,
		sourceProjectionRevision,
		goldenAuthorityDigest(64),
		stageAt,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	return goldenExactPlanAuthorityFixture{
		golden:                   golden,
		draft:                    draft,
		exactAssignmentPlanID:    exactPlanID,
		assignmentBranchID:       branchID,
		sourceProjectionID:       sourceProjectionID,
		sourceProjectionRevision: sourceProjectionRevision,
		sourceArtifactID:         sourceArtifactID,
		sourceArtifactDigest:     sourceArtifactDigest,
		groupID:                  groupID,
		groupRevisionID:          groupRevisionID,
		reservations:             reservations,
	}
}

func createSealedGoldenExactPlan(
	ctx context.Context,
	t testing.TB,
	fixture goldenExactPlanAuthorityFixture,
) uuid.UUID {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	planID := insertGoldenExactPlanRoot(ctx, t, tx, fixture)
	insertGoldenExactPlanGroup(ctx, t, tx, fixture, planID)
	insertGoldenExactPlanMembers(ctx, t, tx, fixture, planID)
	insertGoldenExactPlanEdges(ctx, t, tx, fixture, planID)
	insertGoldenExactPlanCandidates(ctx, t, tx, fixture, planID)
	insertGoldenExactPlanReservations(ctx, t, tx, fixture, planID)
	insertGoldenExactPlanParticipantReservations(ctx, t, tx, fixture, planID)
	sealGoldenExactPlan(ctx, t, tx, fixture, planID)
	require.NoError(t, tx.Commit(ctx))
	return planID
}

func insertGoldenExactPlanRoot(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
) uuid.UUID {
	t.Helper()

	planID := uuid.New()
	_, err := tx.Exec(ctx, `
		INSERT INTO golden_exact_plan_snapshots (
			plan_id, plan_revision_id, tournament_id, roster_id, plan_set_id,
			source_projection_revision_id, source_projection_revision,
			source_projection_previous_revision_id,
			source_standings_artifact_id, source_standings_payload_digest,
			group_set_revision_id, group_set_revision, pool_revision_id, pool_revision,
			history_revision_id, history_revision, task_health_revision_id, task_health_revision,
			artifact_revision_id, artifact_revision, reservation_revision_id, reservation_revision,
			membership_revision_id, membership_revision, source_payload_digest, group_digest,
			pool_digest, history_digest, task_health_digest, artifact_digest, reservation_digest,
			membership_digest, proof_hash, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, NULL,
			$8, $9,
			$10, 1, $11, 1,
			$12, 1, $13, 1,
			$14, 1, $15, 1,
			$16, 1, $9, $17,
			$18, $19, $20, $21, $22,
			$23, 'golden-exact-plan-proof', $24
		)`,
		planID,
		uuid.New(),
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		uuid.New(),
		fixture.sourceProjectionID,
		fixture.sourceProjectionRevision,
		fixture.sourceArtifactID,
		fixture.sourceArtifactDigest,
		uuid.New(),
		uuid.New(),
		uuid.New(),
		uuid.New(),
		uuid.New(),
		uuid.New(),
		uuid.New(),
		goldenAuthorityDigest(66),
		goldenAuthorityDigest(67),
		goldenAuthorityDigest(68),
		goldenAuthorityDigest(69),
		goldenAuthorityDigest(70),
		goldenAuthorityDigest(71),
		goldenAuthorityDigest(72),
		fixture.golden.createdAt.Add(time.Minute),
	)
	require.NoError(t, err)
	return planID
}

func insertGoldenExactPlanGroup(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	planID uuid.UUID,
) {
	t.Helper()
	_, err := tx.Exec(ctx, `
		INSERT INTO golden_exact_plan_snapshot_groups (
			plan_id, tournament_id, roster_id, group_id, group_revision_id,
			source_projection_revision_id, source_projection_revision,
			position_from, position_to, group_ordinal, definition_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, 2, 1, $8, $9)`,
		planID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupID,
		fixture.groupRevisionID,
		fixture.sourceProjectionID,
		fixture.sourceProjectionRevision,
		goldenAuthorityDigest(64),
		fixture.golden.createdAt.Add(time.Minute),
	)
	require.NoError(t, err)
}

func insertGoldenExactPlanMembers(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	planID uuid.UUID,
) {
	t.Helper()
	for index, participantID := range fixture.golden.participantIDs {
		_, err := tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_members (
				plan_id, group_revision_id, tournament_id, roster_id,
				participant_id, position, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			planID,
			fixture.groupRevisionID,
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			participantID,
			index+1,
			fixture.golden.createdAt.Add(time.Minute),
		)
		require.NoError(t, err)
	}
}

func insertGoldenExactPlanEdges(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	planID uuid.UUID,
) {
	t.Helper()
	for index, reservation := range fixture.reservations {
		var edgeID uuid.UUID
		var contentDigest []byte
		err := tx.QueryRow(ctx, `
			SELECT reservation.edge_id, snapshot.content_digest
			FROM task_version_reservations AS reservation
			INNER JOIN task_snapshots AS snapshot
				ON snapshot.id = $2
				AND snapshot.reservation_id = reservation.id
			WHERE reservation.id = $1`, reservation.reservationID, reservation.snapshotID).Scan(&edgeID, &contentDigest)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_edges (
				plan_id, group_revision_id, tournament_id, roster_id,
				edge_id, reservation_id, snapshot_id, task_id, task_version,
				position, content_digest, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			planID,
			fixture.groupRevisionID,
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			edgeID,
			reservation.reservationID,
			reservation.snapshotID,
			reservation.taskID,
			reservation.taskVersion,
			index+1,
			contentDigest,
			fixture.golden.createdAt.Add(time.Minute),
		)
		require.NoError(t, err)
	}
}

func insertGoldenExactPlanCandidates(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	planID uuid.UUID,
) {
	t.Helper()
	for _, reservation := range fixture.reservations {
		_, err := tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_candidates (
				plan_id, tournament_id, roster_id, pool_revision_id,
				task_id, task_version, exists_in_source, enabled, healthy,
				mutation_locked, publicly_exposed, artifact_digest, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, true, true, true, false, false, $7, $8)`,
			planID,
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			uuid.New(),
			reservation.taskID,
			reservation.taskVersion,
			goldenAuthorityDigest(72),
			fixture.golden.createdAt.Add(time.Minute),
		)
		require.NoError(t, err)
	}
}

func insertGoldenExactPlanReservations(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	planID uuid.UUID,
) {
	t.Helper()
	for _, reservation := range fixture.reservations {
		_, err := tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_reservations (
				plan_id, tournament_id, roster_id, task_id, task_version,
				reservation_id, owner_plan_id, owner_plan_revision_id, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			planID,
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			reservation.taskID,
			reservation.taskVersion,
			reservation.reservationID,
			uuid.New(),
			uuid.New(),
			fixture.golden.createdAt.Add(time.Minute),
		)
		require.NoError(t, err)
	}
}

func insertGoldenExactPlanParticipantReservations(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	planID uuid.UUID,
) {
	t.Helper()
	for _, participantID := range fixture.golden.participantIDs {
		var playerID, reservationID uuid.UUID
		var revision int64
		var acquiredAt, updatedAt time.Time
		err := tx.QueryRow(ctx, `
			SELECT player_id
			FROM participants
			WHERE roster_id = $1 AND id = $2`, fixture.golden.rosterID, participantID).Scan(&playerID)
		require.NoError(t, err)
		err = tx.QueryRow(ctx, `
			INSERT INTO participant_reservations (player_id, tournament_id)
			VALUES ($1, $2)
			RETURNING reservation_id, revision, acquired_at, updated_at`, playerID, fixture.golden.tournamentID).Scan(
			&reservationID,
			&revision,
			&acquiredAt,
			&updatedAt,
		)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_participant_reservations (
				plan_id, tournament_id, roster_id, participant_id, player_id,
				reservation_id, revision, acquired_at, updated_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			planID,
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			participantID,
			playerID,
			reservationID,
			revision,
			acquiredAt,
			updatedAt,
			fixture.golden.createdAt.Add(time.Minute),
		)
		require.NoError(t, err)
	}
}

func sealGoldenExactPlan(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	planID uuid.UUID,
) {
	t.Helper()
	_, err := tx.Exec(ctx, `
		INSERT INTO golden_exact_plan_snapshot_seals (
			plan_id, tournament_id, roster_id, proof_hash, sealed_at
		)
		VALUES ($1, $2, $3, 'golden-exact-plan-proof', $4)`,
		planID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.golden.createdAt.Add(2*time.Minute),
	)
	require.NoError(t, err)
}

func insertGoldenStageLifecycleCommand(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	fixture goldenMigrationFixture,
	commandID uuid.UUID,
	sourceProjectionID uuid.UUID,
	sourceProjectionRevision int64,
	at time.Time,
) {
	t.Helper()
	_, err := tx.Exec(ctx, `
		INSERT INTO tournament_lifecycle_commands (
			command_id, tournament_id, roster_id, actor_id, action,
			source_projection_revision_id, source_projection_revision,
			source_tournament_revision, source_tournament_state,
			resulting_tournament_revision, resulting_tournament_state,
			preset, roster_size, tournament_created_at, tournament_updated_at,
			tournament_started_at, tournament_finished_at, executed_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 'start_golden',
			$5, $6, 1, 'swiss', 2, 'golden',
			'tournament_v1', 2, $7, $7, $7, NULL, $7, $8
		)`,
		commandID,
		fixture.tournamentID,
		fixture.rosterID,
		fixture.participantIDs[0],
		sourceProjectionID,
		sourceProjectionRevision,
		at,
		at.Add(time.Microsecond),
	)
	require.NoError(t, err)
}

func goldenAuthorityDigest(value byte) []byte {
	return bytes.Repeat([]byte{value}, 32)
}
