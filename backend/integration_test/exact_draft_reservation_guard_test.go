//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

type exactDraftReservationFixture struct {
	tournamentID       uuid.UUID
	rosterID           uuid.UUID
	seriesID           uuid.UUID
	draftID            uuid.UUID
	draftRevisionID    uuid.UUID
	categoryRevisionID uuid.UUID
	normalPoolID       uuid.UUID
	createdAt          time.Time
	taskIDsByCategory  map[string][]uuid.UUID
}

type exactDraftReservationGroup struct {
	id         uuid.UUID
	categories []string
	childIDs   []uuid.UUID
}

type exactDraftReservationIntent struct {
	id          uuid.UUID
	edgeID      uuid.UUID
	planID      uuid.UUID
	branchID    uuid.UUID
	groupID     *uuid.UUID
	taskID      uuid.UUID
	taskVersion int
}

func TestExactDraftContingencyReservationGuards(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createExactDraftReservationFixture(ctx, t)
	planID, groups := createFullExactDraftReservationPlan(ctx, t, fixture)

	var (
		groupCount       int
		childCount       int
		reservationCount int
		versionCount     int
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM exact_draft_assignment_branches WHERE plan_id = $1),
			(SELECT COUNT(*) FROM assignment_branches WHERE plan_id = $1),
			(SELECT COUNT(*) FROM task_version_reservations WHERE plan_id = $1),
			(SELECT COUNT(DISTINCT (task_id, task_version)) FROM task_version_reservations WHERE plan_id = $1)`, planID,
	).Scan(&groupCount, &childCount, &reservationCount, &versionCount)
	require.NoError(t, err)
	require.Equal(t, 120, groupCount)
	require.Equal(t, 360, childCount)
	require.Equal(t, 1080, reservationCount)
	require.Equal(t, 15, versionCount)

	t.Run("one child per category position and no duplicate version in a group", func(t *testing.T) {
		duplicatePlanID := createExactDraftReservationPlan(ctx, t, fixture, 1, "duplicate")
		group := createExactDraftReservationGroup(ctx, t, fixture, duplicatePlanID, "duplicate", []string{"web", "crypto", "forensics"})
		firstChildID := createExactDraftReservationChild(ctx, t, fixture, duplicatePlanID, group, 1)
		secondChildID := createExactDraftReservationChild(ctx, t, fixture, duplicatePlanID, group, 2)

		err := insertExactDraftReservationChild(
			ctx, fixture, uuid.New(), duplicatePlanID, group, 1,
			"duplicate-position", "web",
		)
		require.Error(t, err)

		spareTaskID := fixture.taskIDsByCategory["web"][3]
		first := createExactDraftReservationIntent(
			ctx, t, fixture, duplicatePlanID, firstChildID, &group.id, spareTaskID, 1,
		)
		require.NoError(t, insertExactDraftReservation(ctx, sharedPool, first))

		second := createExactDraftReservationIntent(
			ctx, t, fixture, duplicatePlanID, secondChildID, &group.id, spareTaskID, 2,
		)
		err = insertExactDraftReservation(ctx, sharedPool, second)
		require.Error(t, err)
		require.NoError(t, releaseExactDraftReservation(ctx, sharedPool, first.id, fixture.createdAt.Add(time.Minute)))
	})

	t.Run("ordinary and contingent writers serialize", func(t *testing.T) {
		spareTaskID := fixture.taskIDsByCategory["web"][3]
		ordinary := createOrdinaryExactReservationIntent(ctx, t, fixture, "ordinary", spareTaskID)
		contingent := createContingentExactReservationIntent(ctx, t, fixture, "contingent", spareTaskID)

		results := writeExactDraftReservationsConcurrently(ctx, t, ordinary, contingent)
		winner := requireExactlyOneExactDraftReservationWinner(t, results)
		assertExactDraftLiveReservationCount(ctx, t, spareTaskID, 1)
		require.NoError(t, releaseExactDraftReservation(ctx, sharedPool, winner.id, fixture.createdAt.Add(2*time.Minute)))
	})

	t.Run("separate exact plans cannot share a contingent version", func(t *testing.T) {
		spareTaskID := fixture.taskIDsByCategory["web"][3]
		first := createContingentExactReservationIntent(ctx, t, fixture, "first-plan", spareTaskID)
		second := createContingentExactReservationIntent(ctx, t, fixture, "second-plan", spareTaskID)

		results := writeExactDraftReservationsConcurrently(ctx, t, first, second)
		winner := requireExactlyOneExactDraftReservationWinner(t, results)
		assertExactDraftLiveReservationCount(ctx, t, spareTaskID, 1)
		require.NoError(t, releaseExactDraftReservation(ctx, sharedPool, winner.id, fixture.createdAt.Add(3*time.Minute)))
	})

	t.Run("failed activation leaves every source reservation reserved", func(t *testing.T) {
		selected := groups[0]
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		at := fixture.createdAt.Add(4 * time.Minute)
		_, err = tx.Exec(ctx, `
			UPDATE task_version_reservations AS reservation
			SET state = 'released', released_at = $3,
				release_reason = 'integration losing branch', revision = reservation.revision + 1
			FROM assignment_branches AS child
			WHERE child.id = reservation.branch_id
				AND child.plan_id = reservation.plan_id
				AND child.plan_id = $1
				AND child.exact_draft_branch_id <> $2`, planID, selected.id, at)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE assignment_branches
			SET state = 'released', released_at = $3, release_reason = 'integration losing branch'
			WHERE plan_id = $1 AND exact_draft_branch_id <> $2`, planID, selected.id, at)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE exact_draft_assignment_branches
			SET state = 'released', released_at = $3, release_reason = 'integration losing branch'
			WHERE plan_id = $1 AND id <> $2`, planID, selected.id, at)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE task_version_reservations AS reservation
			SET state = 'committed', committed_at = $3, revision = reservation.revision + 1
			FROM assignment_branches AS child
			WHERE child.id = reservation.branch_id
				AND child.plan_id = reservation.plan_id
				AND child.plan_id = $1
				AND child.exact_draft_branch_id = $2`, planID, selected.id, at)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE assignment_branches
			SET state = 'active', activated_at = $3
			WHERE plan_id = $1 AND exact_draft_branch_id = $2`, planID, selected.id, at)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE exact_draft_assignment_branches
			SET state = 'active', activated_at = $3
			WHERE plan_id = $1 AND id = $2`, planID, selected.id, at)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE assignment_plans
			SET state = 'committed', active_branch_id = $2, active_draft_branch_id = $3,
				completion_draft_revision_id = $4, completion_draft_revision = 1,
				activation_command_id = $5, completed_categories = $6::JSONB, committed_at = $7
			WHERE id = $1`,
			planID, selected.childIDs[0], selected.id, fixture.draftRevisionID,
			uuid.New(), exactDraftCategoriesJSON(selected.categories), at)
		require.Error(t, err)
		require.NoError(t, tx.Rollback(ctx))

		var (
			reserved int
			children int
			parent   int
		)
		err = sharedPool.QueryRow(ctx, `
			SELECT
				COUNT(*) FILTER (WHERE state = 'reserved'),
				COUNT(DISTINCT branch_id) FILTER (WHERE state = 'reserved'),
				COUNT(DISTINCT contingency_draft_branch_id) FILTER (WHERE state = 'reserved')
			FROM task_version_reservations
			WHERE plan_id = $1`, planID).Scan(&reserved, &children, &parent)
		require.NoError(t, err)
		require.Equal(t, 1080, reserved)
		require.Equal(t, 360, children)
		require.Equal(t, 120, parent)
	})
}

func createExactDraftReservationFixture(ctx context.Context, t *testing.T) exactDraftReservationFixture {
	t.Helper()
	fixture := exactDraftReservationFixture{
		createdAt:         time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond),
		taskIDsByCategory: make(map[string][]uuid.UUID),
	}
	fixture.tournamentID = createMigrationTournament(ctx, t)
	fixture.rosterID = createMigrationRoster(ctx, t, fixture.tournamentID)
	players := createMigrationPlayers(ctx, t, 2)
	participants := createSwissMigrationParticipants(ctx, t, fixture.rosterID, players)
	fixture.seriesID = createMigrationSeries(ctx, t, fixture.tournamentID, fixture.rosterID, participants, "bo3")

	categories := []struct {
		name  string
		count int
	}{
		{name: "web", count: 4},
		{name: "crypto", count: 3},
		{name: "forensics", count: 3},
		{name: "reverse", count: 3},
		{name: "pwn", count: 3},
	}
	for _, category := range categories {
		for ordinal := 1; ordinal <= category.count; ordinal++ {
			var taskID uuid.UUID
			err := sharedPool.QueryRow(ctx, `
				INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'exact draft integration task', $2, 'easy', 60, $3, 'normal')
				RETURNING id`,
				fmt.Sprintf("exact_draft_%s_%d_%s", category.name, ordinal, uuid.NewString()[:8]),
				category.name,
				fmt.Sprintf("FLAG{%s_%d}", category.name, ordinal),
			).Scan(&taskID)
			require.NoError(t, err)
			fixture.taskIDsByCategory[category.name] = append(fixture.taskIDsByCategory[category.name], taskID)
		}
	}
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
		VALUES ($1, 'exact draft integration golden task', 'web', 'easy', 60, 'FLAG{golden}', 'golden')`,
		"exact_draft_golden_"+uuid.NewString()[:8])
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT task_id, version, 1, true, 'integration'
		FROM task_versions`)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `SELECT publish_task_pool_heads()`)
	require.NoError(t, err)

	var publicationID, goldenPoolID uuid.UUID
	err = sharedPool.QueryRow(ctx, `
		SELECT publication.id, normal_pool.id, golden_pool.id
		FROM task_pool_publications AS publication
		INNER JOIN task_pool_revisions AS normal_pool
			ON normal_pool.publication_id = publication.id AND normal_pool.kind = 'normal'
		INNER JOIN task_pool_revisions AS golden_pool
			ON golden_pool.publication_id = publication.id AND golden_pool.kind = 'golden'
		ORDER BY publication.revision DESC
		LIMIT 1`).Scan(&publicationID, &fixture.normalPoolID, &goldenPoolID)
	require.NoError(t, err)

	configurationID := uuid.New()
	bo1PoolID := uuid.New()
	bo3PoolID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_content_configurations (
			id, tournament_id, revision, pool_publication_id, normal_pool_revision_id, golden_pool_revision_id, created_at
		)
		VALUES ($1, $2, 1, $3, $4, $5, $6)`,
		configurationID, fixture.tournamentID, publicationID, fixture.normalPoolID, goldenPoolID, fixture.createdAt)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_category_pool_revisions (id, configuration_id, format, revision, created_at)
		VALUES ($1, $3, 'bo1', 1, $4), ($2, $3, 'bo3', 1, $4)`,
		bo1PoolID, bo3PoolID, configurationID, fixture.createdAt)
	require.NoError(t, err)
	for _, category := range []string{"web", "crypto", "forensics"} {
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO tournament_category_pool_memberships (category_pool_revision_id, category, created_at)
			VALUES ($1, $2, $3)`, bo1PoolID, category, fixture.createdAt)
		require.NoError(t, err)
	}
	for _, category := range []string{"web", "crypto", "forensics", "reverse", "pwn"} {
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO tournament_category_pool_memberships (category_pool_revision_id, category, created_at)
			VALUES ($1, $2, $3)`, bo3PoolID, category, fixture.createdAt)
		require.NoError(t, err)
	}
	for _, stage := range []struct {
		name, format, mode, kind string
		pool                     uuid.UUID
	}{
		{name: "swiss", format: "bo1", mode: "random", kind: "normal", pool: bo1PoolID},
		{name: "golden", format: "bo1", mode: "random", kind: "golden", pool: bo1PoolID},
		{name: "semifinal", format: "bo1", mode: "draft", kind: "normal", pool: bo1PoolID},
		{name: "final", format: "bo3", mode: "draft", kind: "normal", pool: bo3PoolID},
	} {
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO tournament_content_stage_defaults (
				configuration_id, stage, format, category_mode, category_pool_revision_id, task_pool_kind, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			configurationID, stage.name, stage.format, stage.mode, stage.pool, stage.kind, fixture.createdAt)
		require.NoError(t, err)
	}
	_, err = sharedPool.Exec(ctx, `
		UPDATE tournament_content_configurations
		SET state = 'published', published_at = $2
		WHERE id = $1`, configurationID, fixture.createdAt.Add(time.Second))
	require.NoError(t, err)

	categoryRevisionID := uuid.New()
	fixture.categoryRevisionID = categoryRevisionID
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO category_revisions (
			id, series_id, roster_id, revision, source_pool_revision_id, mode, category_pool, created_at
		)
		VALUES ($1, $2, $3, 1, $4, 'draft', '["web","crypto","forensics","reverse","pwn"]'::JSONB, $5)`,
		categoryRevisionID, fixture.seriesID, fixture.rosterID, fixture.normalPoolID, fixture.createdAt)
	require.NoError(t, err)
	fixture.draftID = uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO drafts (
			id, series_id, roster_id, category_revision_id, first_participant_id, second_participant_id, format, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'bo3', $7)`,
		fixture.draftID, fixture.seriesID, fixture.rosterID, categoryRevisionID, participants[0], participants[1], fixture.createdAt)
	require.NoError(t, err)
	fixture.draftRevisionID = uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO draft_revisions (
			id, draft_id, series_id, roster_id, revision, command_id, service_epoch,
			state, turn_number, current_actor_id, current_action, absolute_deadline,
			decision_evidence_id, decision_purpose, decision_algorithm_version, decision_inputs,
			decision_seed, decision_result, decision_replay_digest, decision_owner_id, decided_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 1, $5, $6,
			'active', 1, $7, 'ban', $8,
			$9, 'draft_order', 'hmac-sha256-order-v1', '["first","second"]'::JSONB,
			$10, '["first","second"]'::JSONB, $11, $2, $12, $13
		)`,
		fixture.draftRevisionID, fixture.draftID, fixture.seriesID, fixture.rosterID,
		uuid.New(), uuid.New(), participants[0], fixture.createdAt.Add(time.Hour), uuid.New(),
		exactDraftDigest(1), exactDraftDigest(2), fixture.createdAt.Add(-time.Second), fixture.createdAt)
	require.NoError(t, err)
	return fixture
}

func createFullExactDraftReservationPlan(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
) (uuid.UUID, []exactDraftReservationGroup) {
	t.Helper()
	planID := createExactDraftReservationPlan(ctx, t, fixture, 120, "full")
	categories := []string{"web", "crypto", "forensics", "reverse", "pwn"}
	groups := make([]exactDraftReservationGroup, 0, 120)
	for number := 0; number < 120; number++ {
		sequence := []string{
			categories[number%len(categories)],
			categories[(number+1)%len(categories)],
			categories[(number+2)%len(categories)],
		}
		group := createExactDraftReservationGroup(
			ctx, t, fixture, planID, fmt.Sprintf("path-%03d", number+1), sequence,
		)
		for position := 1; position <= len(sequence); position++ {
			childID := createExactDraftReservationChild(ctx, t, fixture, planID, group, position)
			group.childIDs = append(group.childIDs, childID)
			for reservePosition, taskID := range fixture.taskIDsByCategory[sequence[position-1]][:3] {
				intent := createExactDraftReservationIntent(
					ctx, t, fixture, planID, childID, &group.id, taskID, reservePosition+1,
				)
				require.NoError(t, insertExactDraftReservation(ctx, sharedPool, intent))
			}
		}
		groups = append(groups, group)
	}
	return planID, groups
}

func createExactDraftReservationPlan(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
	reachableBranches int,
	key string,
) uuid.UUID {
	t.Helper()
	planID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO assignment_plans (
			id, tournament_id, roster_id, kind, revision_id, source_roster_revision,
			source_pool_revision_id, source_draft_revision_id, reachable_branch_count,
			constraint_graph, proof_evidence, proof_hash, created_at
		)
		VALUES ($1, $2, $3, 'exact_draft', $4, 1, $5, $6, $7,
			$8::JSONB, $8::JSONB, $9, $10)`,
		planID, fixture.tournamentID, fixture.rosterID, uuid.New(), fixture.normalPoolID,
		fixture.draftRevisionID, reachableBranches, fmt.Sprintf(`{"key":%q}`, key), strings.Repeat("a", 64), fixture.createdAt)
	require.NoError(t, err)
	return planID
}

func createExactDraftReservationGroup(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
	planID uuid.UUID,
	key string,
	categories []string,
) exactDraftReservationGroup {
	t.Helper()
	group := exactDraftReservationGroup{id: uuid.New(), categories: append([]string(nil), categories...)}
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO exact_draft_assignment_branches (
			id, plan_id, draft_id, draft_revision_id, branch_key, category_sequence, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6::JSONB, $7)`,
		group.id, planID, fixture.draftID, fixture.draftRevisionID, key,
		exactDraftCategoriesJSON(categories), fixture.createdAt)
	require.NoError(t, err)
	return group
}

func createExactDraftReservationChild(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
	planID uuid.UUID,
	group exactDraftReservationGroup,
	position int,
) uuid.UUID {
	t.Helper()
	childID := uuid.New()
	err := insertExactDraftReservationChild(
		ctx, fixture, childID, planID, group, position,
		fmt.Sprintf("%s-category-%d", group.id, position), group.categories[position-1],
	)
	require.NoError(t, err)
	return childID
}

func insertExactDraftReservationChild(
	ctx context.Context,
	fixture exactDraftReservationFixture,
	childID uuid.UUID,
	planID uuid.UUID,
	group exactDraftReservationGroup,
	position int,
	key string,
	category string,
) error {
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO assignment_branches (
			id, plan_id, draft_id, draft_revision_id, branch_key, category_sequence,
			exact_draft_branch_id, exact_draft_position, decision_evidence_id,
			decision_algorithm_version, decision_inputs, decision_seed, decision_result,
			decision_replay_digest, decision_owner_id, decided_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6::JSONB, $7, $8, $9,
			'hmac-sha256-order-v1', '["candidate"]'::JSONB, $10, '["selected"]'::JSONB,
			$11, $2, $12, $13
		)`,
		childID, planID, fixture.draftID, fixture.draftRevisionID, key,
		exactDraftCategoriesJSON([]string{category}), group.id, position, uuid.New(),
		exactDraftDigest(3), exactDraftDigest(4), fixture.createdAt.Add(-time.Second), fixture.createdAt)
	return err
}

func createExactDraftReservationIntent(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
	planID uuid.UUID,
	branchID uuid.UUID,
	groupID *uuid.UUID,
	taskID uuid.UUID,
	position int,
) exactDraftReservationIntent {
	t.Helper()
	intent := exactDraftReservationIntent{
		id: uuid.New(), edgeID: uuid.New(), planID: planID, branchID: branchID,
		groupID: groupID, taskID: taskID, taskVersion: 1,
	}
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO assignment_plan_edges (
			id, plan_id, branch_id, position, task_id, task_version, selection_evidence, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 1, '{"integration":true}'::JSONB, $6)`,
		intent.edgeID, planID, branchID, position, taskID, fixture.createdAt)
	require.NoError(t, err)
	return intent
}

func insertExactDraftReservation(
	ctx context.Context,
	execer interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	},
	intent exactDraftReservationIntent,
) error {
	var contingencyDraftBranchID any
	if intent.groupID != nil {
		contingencyDraftBranchID = *intent.groupID
	}
	_, err := execer.Exec(ctx, `
		INSERT INTO task_version_reservations (
			id, edge_id, plan_id, branch_id, contingency_draft_branch_id, task_id, task_version
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		intent.id, intent.edgeID, intent.planID, intent.branchID, contingencyDraftBranchID,
		intent.taskID, intent.taskVersion)
	return err
}

func createContingentExactReservationIntent(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
	key string,
	taskID uuid.UUID,
) exactDraftReservationIntent {
	t.Helper()
	planID := createExactDraftReservationPlan(ctx, t, fixture, 1, key)
	group := createExactDraftReservationGroup(ctx, t, fixture, planID, key, []string{"web", "crypto", "forensics"})
	childID := createExactDraftReservationChild(ctx, t, fixture, planID, group, 1)
	return createExactDraftReservationIntent(ctx, t, fixture, planID, childID, &group.id, taskID, 1)
}

func createOrdinaryExactReservationIntent(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
	key string,
	taskID uuid.UUID,
) exactDraftReservationIntent {
	t.Helper()
	parentPlanID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO assignment_plans (
			id, tournament_id, roster_id, kind, revision_id, source_roster_revision,
			source_pool_revision_id, reachable_branch_count, constraint_graph, proof_evidence, created_at
		)
		VALUES ($1, $2, $3, 'conservative', $4, 1, $5, 0, '{"integration":true}'::JSONB, '{"integration":true}'::JSONB, $6)`,
		parentPlanID, fixture.tournamentID, fixture.rosterID, uuid.New(), fixture.normalPoolID, fixture.createdAt)
	require.NoError(t, err)
	planID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO assignment_plans (
			id, tournament_id, roster_id, kind, parent_plan_id, revision_id, source_roster_revision,
			source_pool_revision_id, source_draft_revision_id, reachable_branch_count,
			constraint_graph, proof_evidence, decision_evidence_id, decision_algorithm_version,
			decision_inputs, decision_seed, decision_result, decision_replay_digest,
			decision_owner_id, decided_at, created_at
		)
		VALUES (
			$1, $2, $3, 'exact', $4, $5, 1, $6, $7, 1,
			'{"integration":true}'::JSONB, '{"integration":true}'::JSONB, $8, 'hmac-sha256-order-v1',
			'["candidate"]'::JSONB, $9, '["selected"]'::JSONB, $10, $1, $11, $12
		)`,
		planID, fixture.tournamentID, fixture.rosterID, parentPlanID, uuid.New(), fixture.normalPoolID,
		fixture.draftRevisionID, uuid.New(), exactDraftDigest(5), exactDraftDigest(6),
		fixture.createdAt.Add(-time.Second), fixture.createdAt)
	require.NoError(t, err)
	branchID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO assignment_branches (
			id, plan_id, draft_id, draft_revision_id, branch_key, category_sequence, created_at
		)
		VALUES ($1, $2, $3, $4, $5, '["web"]'::JSONB, $6)`,
		branchID, planID, fixture.draftID, fixture.draftRevisionID, key, fixture.createdAt)
	require.NoError(t, err)
	return createExactDraftReservationIntent(ctx, t, fixture, planID, branchID, nil, taskID, 1)
}

func writeExactDraftReservationsConcurrently(
	ctx context.Context,
	t *testing.T,
	intents ...exactDraftReservationIntent,
) map[uuid.UUID]error {
	t.Helper()
	start := make(chan struct{})
	results := make(chan struct {
		id  uuid.UUID
		err error
	}, len(intents))
	var workers sync.WaitGroup
	for _, intent := range intents {
		workers.Add(1)
		go func(intent exactDraftReservationIntent) {
			defer workers.Done()
			tx, err := sharedPool.Begin(ctx)
			if err == nil {
				<-start
				err = insertExactDraftReservation(ctx, tx, intent)
				if err == nil {
					err = tx.Commit(ctx)
				} else {
					_ = tx.Rollback(ctx)
				}
			}
			results <- struct {
				id  uuid.UUID
				err error
			}{id: intent.id, err: err}
		}(intent)
	}
	close(start)
	workers.Wait()
	close(results)
	resultByID := make(map[uuid.UUID]error, len(intents))
	for result := range results {
		resultByID[result.id] = result.err
	}
	return resultByID
}

func requireExactlyOneExactDraftReservationWinner(
	t *testing.T,
	results map[uuid.UUID]error,
) exactDraftReservationIntent {
	t.Helper()
	var winnerID uuid.UUID
	for id, err := range results {
		if err == nil {
			require.Equal(t, uuid.Nil, winnerID)
			winnerID = id
		}
	}
	require.NotEqual(t, uuid.Nil, winnerID)
	require.Len(t, results, 2)
	for id, err := range results {
		if id != winnerID {
			require.Error(t, err)
		}
	}
	return exactDraftReservationIntent{id: winnerID}
}

func releaseExactDraftReservation(
	ctx context.Context,
	execer interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	},
	reservationID uuid.UUID,
	at time.Time,
) error {
	_, err := execer.Exec(ctx, `
		UPDATE task_version_reservations
		SET state = 'released', released_at = $2, release_reason = 'integration cleanup', revision = revision + 1
		WHERE id = $1`, reservationID, at)
	return err
}

func assertExactDraftLiveReservationCount(
	ctx context.Context,
	t *testing.T,
	taskID uuid.UUID,
	want int,
) {
	t.Helper()
	var got int
	err := sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM task_version_reservations
		WHERE task_id = $1 AND task_version = 1 AND state IN ('reserved', 'committed')`, taskID).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func exactDraftCategoriesJSON(categories []string) string {
	parts := make([]string, len(categories))
	for index, category := range categories {
		parts[index] = fmt.Sprintf("%q", category)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func exactDraftDigest(value byte) []byte {
	return bytes.Repeat([]byte{value}, 32)
}
