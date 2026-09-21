//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestReplayReserveAuthorityProducerGuards(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture, plan := createCommittedReplayAuthorityFixture(ctx, t)

	t.Run("missing authority rolls back the assignment", func(t *testing.T) {
		assignmentID := uuid.New()
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertReplayAuthorityAssignment(ctx, tx, fixture, plan, assignmentID))
		require.Error(t, tx.Commit(ctx))

		var count int
		require.NoError(t, sharedPool.QueryRow(ctx,
			`SELECT COUNT(*) FROM assignments WHERE id = $1`, assignmentID).Scan(&count))
		require.Zero(t, count)
	})

	t.Run("partial authority pool rolls back the assignment", func(t *testing.T) {
		assignmentID := uuid.New()
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertReplayAuthorityAssignment(ctx, tx, fixture, plan, assignmentID))
		require.NoError(t, insertReplayAuthorityHead(ctx, tx, assignmentID))
		require.Error(t, tx.Commit(ctx))

		var count int
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM replay_reserve_authorities
			WHERE assignment_id = $1`, assignmentID).Scan(&count))
		require.Zero(t, count)
	})

	t.Run("complete immutable pool is accepted and dynamic eligibility is enforced", func(t *testing.T) {
		assignmentID := uuid.New()
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertReplayAuthorityAssignment(ctx, tx, fixture, plan, assignmentID))
		require.NoError(t, insertReplayAuthorityHead(ctx, tx, assignmentID))
		require.NoError(t, insertReplayAuthorityPool(ctx, tx, assignmentID))
		require.NoError(t, tx.Commit(ctx))

		var authorityPoolCount, sourcePoolCount int
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT
				(SELECT COUNT(*) FROM replay_reserve_authority_pool_versions WHERE assignment_id = $1),
				(SELECT COUNT(*) FROM exact_draft_assignment_child_candidates
					WHERE plan_id = $2 AND child_branch_id = $3)`,
			assignmentID, plan.id, plan.branchID,
		).Scan(&authorityPoolCount, &sourcePoolCount))
		require.Equal(t, sourcePoolCount, authorityPoolCount)

		candidateID, eligible := findReplayEligibleCandidate(ctx, t, assignmentID)
		require.NotEqual(t, uuid.Nil, candidateID)
		require.True(t, eligible)

		tx, err = sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `
			UPDATE tasks
			SET enabled = false
			WHERE id IN (
				SELECT pool_version.task_id
				FROM replay_reserve_authorities AS authority
				INNER JOIN replay_reserve_authority_pool_versions AS pool_version
					ON pool_version.assignment_id = authority.assignment_id
				INNER JOIN task_versions AS version
					ON version.task_id = pool_version.task_id
					AND version.version = pool_version.task_version
				WHERE authority.assignment_id = $1
					AND version.category = authority.required_category
			)`, assignmentID)
		require.NoError(t, err)
		_, eligible = findReplayEligibleCandidateWith(ctx, t, tx, assignmentID)
		require.False(t, eligible)
		require.NoError(t, tx.Rollback(ctx))
	})
}

type replayAuthorityPlanFixture struct {
	id            uuid.UUID
	groupID       uuid.UUID
	branchID      uuid.UUID
	reservationID uuid.UUID
	snapshotID    uuid.UUID
	attemptID     uuid.UUID
}

func createCommittedReplayAuthorityFixture(
	ctx context.Context,
	t *testing.T,
) (exactDraftReservationFixture, replayAuthorityPlanFixture) {
	t.Helper()
	fixture := createExactDraftReservationFixture(ctx, t)
	participants := loadReplayAuthorityParticipants(ctx, t, fixture)
	completionID := completeReplayAuthorityDraft(ctx, t, fixture, participants)

	planID := createExactDraftReservationPlan(ctx, t, fixture, 1, "replay-authority")
	group := createExactDraftReservationGroup(
		ctx, t, fixture, planID, "replay-authority", []string{"web", "crypto", "forensics"},
	)
	plan := replayAuthorityPlanFixture{id: planID, groupID: group.id}
	slotID := createMigrationGameSlot(ctx, t, fixture.seriesID, fixture.rosterID, 1, "web")
	plan.attemptID = createActiveMigrationAttempt(
		ctx, t, slotID, fixture.seriesID, fixture.rosterID, fixture.createdAt,
	)
	for position, category := range group.categories {
		childID := createExactDraftReservationChild(ctx, t, fixture, planID, group, position+1)
		if position == 0 {
			plan.branchID = childID
		}
		for reservePosition, taskID := range fixture.taskIDsByCategory[category][:3] {
			intent := createExactDraftReservationIntent(
				ctx, t, fixture, planID, childID, &group.id, taskID, reservePosition+1,
			)
			require.NoError(t, insertExactDraftReservation(ctx, sharedPool, intent))
			if position == 0 && reservePosition == 0 {
				plan.reservationID = intent.id
				plan.snapshotID = createReplayAuthoritySnapshot(ctx, t, intent, fixture.createdAt)
			} else {
				createReplayAuthoritySnapshot(ctx, t, intent, fixture.createdAt)
			}
		}
		insertReplayAuthorityChildSource(ctx, t, fixture, planID, childID, slotID, participants)
	}

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	activatedAt := fixture.createdAt.Add(2 * time.Minute)
	_, err = tx.Exec(ctx, `
		UPDATE task_version_reservations
		SET state = 'committed', committed_at = $2, revision = revision + 1
		WHERE plan_id = $1`, planID, activatedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'active', activated_at = $2
		WHERE plan_id = $1`, planID, activatedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE exact_draft_assignment_branches
		SET state = 'active', activated_at = $2
		WHERE id = $1 AND plan_id = $3`, group.id, activatedAt, planID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE assignment_plans
		SET state = 'committed', active_branch_id = $2, active_draft_branch_id = $3,
			completion_draft_revision_id = $4, completion_draft_revision = 5,
			activation_command_id = $5, completed_categories = $6::JSONB, committed_at = $7
		WHERE id = $1`,
		planID,
		plan.branchID,
		group.id,
		completionID,
		uuid.New(),
		exactDraftCategoriesJSON(group.categories),
		activatedAt,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return fixture, plan
}

type replayAuthorityParticipant struct {
	id            uuid.UUID
	playerID      uuid.UUID
	reservationID uuid.UUID
}

func loadReplayAuthorityParticipants(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
) []replayAuthorityParticipant {
	t.Helper()
	rows, err := sharedPool.Query(ctx, `
		SELECT participant.id, participant.player_id
		FROM participants AS participant
		WHERE participant.roster_id = $1
		ORDER BY participant.seed`, fixture.rosterID)
	require.NoError(t, err)
	defer rows.Close()

	participants := make([]replayAuthorityParticipant, 0, 2)
	for rows.Next() {
		participant := replayAuthorityParticipant{}
		require.NoError(t, rows.Scan(&participant.id, &participant.playerID))
		require.NoError(t, sharedPool.QueryRow(ctx, `
			INSERT INTO participant_reservations (player_id, tournament_id, acquired_at, updated_at)
			VALUES ($1, $2, $3, $3)
			RETURNING reservation_id`,
			participant.playerID, fixture.tournamentID, fixture.createdAt,
		).Scan(&participant.reservationID))
		participants = append(participants, participant)
	}
	require.NoError(t, rows.Err())
	require.Len(t, participants, 2)
	return participants
}

func completeReplayAuthorityDraft(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
	participants []replayAuthorityParticipant,
) uuid.UUID {
	t.Helper()
	previousID := fixture.draftRevisionID
	deadline := fixture.createdAt.Add(time.Hour)
	steps := []struct {
		state, action, category string
		turn, actionTurn        int
		actor                   uuid.UUID
		currentActor            any
		currentAction           any
		absoluteDeadline        any
	}{
		{"active", "ban", "reverse", 2, 1, participants[0].id, participants[1].id, "ban", deadline},
		{"active", "ban", "pwn", 3, 2, participants[1].id, participants[0].id, "pick", deadline},
		{"active", "pick", "web", 4, 3, participants[0].id, participants[1].id, "pick", deadline},
		{"completed", "pick", "crypto", 4, 4, participants[1].id, nil, nil, nil},
	}
	for index, step := range steps {
		revisionID := uuid.New()
		commandID := uuid.New()
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		createdAt := fixture.createdAt.Add(time.Duration(index+1) * time.Second)
		selectedCategories := "[]"
		if step.state == "completed" {
			selectedCategories = `["web","crypto","forensics"]`
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO draft_revisions (
				id, draft_id, series_id, roster_id, revision, previous_revision_id,
				command_id, service_epoch, state, turn_number, current_actor_id,
				current_action, absolute_deadline, selected_categories, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::JSONB, $15)`,
			revisionID, fixture.draftID, fixture.seriesID, fixture.rosterID, index+2,
			previousID, commandID, uuid.New(), step.state, step.turn, step.currentActor,
			step.currentAction, step.absoluteDeadline, selectedCategories, createdAt,
		)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO draft_actions (
				draft_id, result_revision_id, command_id, turn_number, actor_id, action,
				category, scheduled_deadline, occurred_at, automatic, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, false, $10)`,
			fixture.draftID, revisionID, commandID, step.actionTurn, step.actor,
			step.action, step.category, deadline, createdAt, createdAt,
		)
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))
		previousID = revisionID
	}
	return previousID
}

func createReplayAuthoritySnapshot(
	ctx context.Context,
	t *testing.T,
	intent exactDraftReservationIntent,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()
	snapshotID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO task_snapshots (
			id, reservation_id, task_id, task_version, kind, title, description, category,
			difficulty, time_limit, flag, hints, task_url, source_file_url, content_digest, created_at
		)
		SELECT $1, $2, version.task_id, version.version, task.kind, version.title,
			version.description, version.category, version.difficulty, version.time_limit,
			version.flag, '[]'::JSONB, version.task_url, version.source_file_url,
			version.content_digest, $3
		FROM task_versions AS version
		JOIN tasks AS task ON task.id = version.task_id
		WHERE version.task_id = $4 AND version.version = $5`,
		snapshotID, intent.id, createdAt, intent.taskID, intent.taskVersion,
	)
	require.NoError(t, err)
	return snapshotID
}

func insertReplayAuthorityChildSource(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
	planID uuid.UUID,
	childID uuid.UUID,
	slotID uuid.UUID,
	participants []replayAuthorityParticipant,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO exact_draft_assignment_child_sources (
			child_branch_id, plan_id, tournament_id, roster_id, series_id, slot_id,
			category_lock_id, series_revision, pool_revision_id, pool_revision,
			history_revision_id, history_revision, roster_revision, artifact_revision_id,
			artifact_revision, category_revision_id, category_revision, graph_digest,
			artifact_digest, proof_hash, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, 1, $9, 1, 1, $10, 1, $7, 1,
			$11, $12, $13, $14)`,
		childID, planID, fixture.tournamentID, fixture.rosterID, fixture.seriesID, slotID,
		fixture.categoryRevisionID, fixture.normalPoolID, uuid.New(), uuid.New(),
		exactDraftDigest(19), exactDraftDigest(20), strings.Repeat("f", 64), fixture.createdAt,
	)
	require.NoError(t, err)
	for _, participant := range participants {
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO exact_draft_assignment_child_participants (
				child_branch_id, plan_id, participant_id, player_id, reservation_id,
				tournament_id, reservation_revision, acquired_at, updated_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7, $7)`,
			childID, planID, participant.id, participant.playerID, participant.reservationID,
			fixture.tournamentID, fixture.createdAt,
		)
		require.NoError(t, err)
	}
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO exact_draft_assignment_child_candidates (
			child_branch_id, plan_id, task_id, task_version, pool_revision_id, created_at
		)
		SELECT $1, $2, membership.task_id, membership.task_version, membership.task_pool_revision_id, $3
		FROM task_pool_version_memberships AS membership
		WHERE membership.task_pool_revision_id = $4`,
		childID, planID, fixture.createdAt, fixture.normalPoolID,
	)
	require.NoError(t, err)
}

func insertReplayAuthorityAssignment(
	ctx context.Context,
	tx pgx.Tx,
	fixture exactDraftReservationFixture,
	plan replayAuthorityPlanFixture,
	assignmentID uuid.UUID,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO assignments (
			id, attempt_id, series_id, roster_id, plan_id, branch_id, reservation_id,
			snapshot_id, task_id, task_version, created_at, updated_at
		)
		SELECT $1, $2, $3, $4, $5, $6, reservation.id, snapshot.id,
			reservation.task_id, reservation.task_version, $7, $7
		FROM task_version_reservations AS reservation
		JOIN task_snapshots AS snapshot ON snapshot.reservation_id = reservation.id
		WHERE reservation.id = $8`,
		assignmentID, plan.attemptID, fixture.seriesID, fixture.rosterID, plan.id,
		plan.branchID, fixture.createdAt.Add(3*time.Minute), plan.reservationID,
	)
	return err
}

func insertReplayAuthorityHead(ctx context.Context, tx pgx.Tx, assignmentID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO replay_reserve_authorities (
			assignment_id, tournament_id, roster_id, series_id, slot_id,
			assignment_attempt_id, active_snapshot_id, required_category,
			assignment_revision, pool_revision_id, pool_revision, history_revision_id,
			history_revision, artifact_revision_id, artifact_revision, reservation_revision_id,
			reservation_revision, category_revision_id, category_revision, revision, created_at, updated_at
		)
		SELECT assignment.id, source.tournament_id, source.roster_id, source.series_id,
			source.slot_id, assignment.attempt_id, assignment.snapshot_id,
			branch.category_sequence ->> 0, assignment.revision, source.pool_revision_id,
			source.pool_revision, source.history_revision_id, source.history_revision,
			source.artifact_revision_id, source.artifact_revision, reservation.id,
			reservation.revision, source.category_revision_id, source.category_revision,
			1,
			LEAST(assignment.created_at, clock_timestamp() - INTERVAL '1 second'),
			LEAST(assignment.created_at, clock_timestamp() - INTERVAL '1 second')
		FROM assignments AS assignment
		JOIN assignment_plans AS plan ON plan.id = assignment.plan_id
		JOIN assignment_branches AS branch ON branch.id = assignment.branch_id AND branch.plan_id = plan.id
		JOIN exact_draft_assignment_child_sources AS source
			ON source.child_branch_id = branch.id AND source.plan_id = plan.id
		JOIN task_version_reservations AS reservation
			ON reservation.id = assignment.reservation_id AND reservation.plan_id = plan.id
			AND reservation.branch_id = branch.id
		JOIN game_attempts AS attempt
			ON attempt.id = assignment.attempt_id AND attempt.series_id = assignment.series_id
			AND attempt.roster_id = assignment.roster_id
		WHERE assignment.id = $1 AND assignment.state = 'active' AND plan.kind = 'exact_draft'
			AND plan.state = 'committed' AND branch.state = 'active'
			AND reservation.state = 'committed' AND attempt.slot_id = source.slot_id`, assignmentID)
	return err
}

func insertReplayAuthorityPool(ctx context.Context, tx pgx.Tx, assignmentID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO replay_reserve_authority_pool_versions (assignment_id, task_id, task_version, created_at)
		SELECT authority.assignment_id, candidate.task_id, candidate.task_version, authority.created_at
		FROM replay_reserve_authorities AS authority
		JOIN assignments AS assignment ON assignment.id = authority.assignment_id
		JOIN exact_draft_assignment_child_candidates AS candidate
			ON candidate.plan_id = assignment.plan_id AND candidate.child_branch_id = assignment.branch_id
		WHERE authority.assignment_id = $1`, assignmentID)
	return err
}

func findReplayEligibleCandidate(
	ctx context.Context,
	t *testing.T,
	assignmentID uuid.UUID,
) (uuid.UUID, bool) {
	t.Helper()
	return findReplayEligibleCandidateWith(ctx, t, sharedPool, assignmentID)
}

func findReplayEligibleCandidateWith(
	ctx context.Context,
	t *testing.T,
	querier interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	assignmentID uuid.UUID,
) (uuid.UUID, bool) {
	t.Helper()
	var candidateID uuid.UUID
	err := querier.QueryRow(ctx, `
		SELECT candidate.task_id
		FROM replay_reserve_authorities AS authority
		JOIN assignments AS assignment ON assignment.id = authority.assignment_id
		JOIN replay_reserve_authority_pool_versions AS pool_version
			ON pool_version.assignment_id = authority.assignment_id
		JOIN task_versions AS candidate
			ON candidate.task_id = pool_version.task_id AND candidate.version = pool_version.task_version
		JOIN tasks AS task ON task.id = candidate.task_id
		JOIN LATERAL (
			SELECT attestation.healthy
			FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = candidate.task_id AND attestation.task_version = candidate.version
			ORDER BY attestation.revision DESC
			LIMIT 1
		) AS health ON true
		WHERE authority.assignment_id = $1
			AND candidate.category = authority.required_category
			AND task.enabled AND task.deleted_at IS NULL AND health.healthy
			AND NOT EXISTS (
				SELECT 1 FROM task_delivery_receipts AS receipt
				WHERE receipt.task_id = candidate.task_id AND receipt.task_version = candidate.version
			)
			AND NOT EXISTS (
				SELECT 1 FROM task_version_reservations AS reservation
				WHERE reservation.plan_id = assignment.plan_id AND reservation.branch_id = assignment.branch_id
					AND reservation.task_id = candidate.task_id AND reservation.task_version = candidate.version
					AND reservation.state = 'committed'
			)
		ORDER BY candidate.task_id
		LIMIT 1`, assignmentID).Scan(&candidateID)
	if err == nil {
		return candidateID, true
	}
	require.ErrorIs(t, err, pgx.ErrNoRows)
	return uuid.Nil, false
}
