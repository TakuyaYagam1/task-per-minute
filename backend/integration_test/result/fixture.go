//go:build integration

package result

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/correctionseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/draftseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/resultaudit"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type correctionFixture struct {
	draft      draftseed.PreparedScope
	audit      resultaudit.PreparedScope
	correction correctionseed.Scope
}

type assignmentReservation struct {
	reservationID uuid.UUID
	snapshotID    uuid.UUID
	taskID        uuid.UUID
	taskVersion   int
}

func resetResultTables(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("result fixture: nil pool")
	}
	_, err := pool.Exec(ctx, `TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	return err
}

func createCorrectionFixture(
	ctx context.Context,
	pool *pgxpool.Pool,
) (correctionFixture, error) {
	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	draft, err := draftseed.Prepare(ctx, pool, draftseed.PrepareInput{
		CreatedAt: createdAt,
		Content:   createCorrectionContent,
	})
	if err != nil {
		return correctionFixture{}, err
	}

	planAt := draft.CreatedAt.Add(5 * time.Second)
	conservativePlanID, err := createConservativePlan(ctx, pool, draft, planAt)
	if err != nil {
		return correctionFixture{}, err
	}
	exactPlanID, err := createExactPlan(ctx, pool, draft, conservativePlanID, planAt.Add(time.Second))
	if err != nil {
		return correctionFixture{}, err
	}
	branchID, err := createBranch(ctx, pool, draft, exactPlanID, "web-final", planAt.Add(2*time.Second))
	if err != nil {
		return correctionFixture{}, err
	}
	releasedBranchID, err := createBranch(
		ctx, pool, draft, exactPlanID, "web-fallback", planAt.Add(2*time.Second),
	)
	if err != nil {
		return correctionFixture{}, err
	}
	reservations, err := createReservations(ctx, pool, draft, exactPlanID, branchID, planAt.Add(3*time.Second))
	if err != nil {
		return correctionFixture{}, err
	}
	releasedReservations, err := createReservations(
		ctx, pool, draft, exactPlanID, releasedBranchID, planAt.Add(3*time.Second),
	)
	if err != nil {
		return correctionFixture{}, err
	}
	committedAt := planAt.Add(4 * time.Second)
	if err := updateAssignmentState(ctx, pool, exactPlanID, branchID, releasedBranchID, reservations, releasedReservations, committedAt); err != nil {
		return correctionFixture{}, err
	}

	lockedAt := time.Now().UTC().Add(5 * time.Second).Truncate(time.Microsecond)
	audit, err := resultaudit.PrepareScope(ctx, pool, resultaudit.PrepareInput{
		TournamentID: draft.TournamentID,
		RosterID:     draft.RosterID,
		SeriesID:     draft.SeriesID,
		ParticipantIDs: [2]uuid.UUID{
			draft.ParticipantIDs[0], draft.ParticipantIDs[1],
		},
		Assignment: resultaudit.AssignmentInput{
			ID:            uuid.New(),
			PlanID:        exactPlanID,
			BranchID:      branchID,
			ReservationID: reservations[0].reservationID,
			SnapshotID:    reservations[0].snapshotID,
			TaskID:        reservations[0].taskID,
			TaskVersion:   reservations[0].taskVersion,
		},
		GameCategory:        domain.CategoryWeb,
		GameCreatedAt:       committedAt.Add(3 * time.Second),
		AssignmentCreatedAt: committedAt.Add(4 * time.Second),
		LockedAt:            lockedAt,
	})
	if err != nil {
		return correctionFixture{}, err
	}

	correction, err := correctionseed.Build(ctx, pool, correctionseed.Input{
		ResultScope:  resultauditScope(audit.Fixture.Scope),
		AssignmentID: audit.Fixture.Scope.AssignmentID,
		ParticipantIDs: []uuid.UUID{
			audit.Fixture.Scope.ParticipantIDs[0], audit.Fixture.Scope.ParticipantIDs[1],
		},
		LockedAt: audit.Fixture.LockedAt,
	})
	if err != nil {
		return correctionFixture{}, err
	}
	return correctionFixture{draft: draft, audit: audit, correction: correction}, nil
}

func resultauditScope(scope resultaudit.Scope) resultrepo.ResultScope {
	return resultrepo.ResultScope{
		TournamentID: scope.TournamentID,
		RosterID:     scope.RosterID,
		SeriesID:     scope.SeriesID,
		AttemptID:    scope.AttemptID,
	}
}

func createCorrectionContent(
	ctx context.Context,
	pool *pgxpool.Pool,
	tournamentID uuid.UUID,
	at time.Time,
) (draftseed.ContentSeed, error) {
	taskIDs := make([]uuid.UUID, 0, 24)
	for _, category := range []string{"web", "crypto", "pwn"} {
		for range 8 {
			var taskID uuid.UUID
			err := pool.QueryRow(ctx, `
				INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'draft fixture task', $2, 'easy', 60, $3, 'normal')
				RETURNING id`,
				"draft_fixture_"+category+"_"+uuid.NewString()[:8],
				category,
				"FLAG{draft-"+category+"-"+uuid.NewString()[:8]+"}",
			).Scan(&taskID)
			if err != nil {
				return draftseed.ContentSeed{}, fmt.Errorf("result fixture: create task: %w", err)
			}
			taskIDs = append(taskIDs, taskID)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
		VALUES ($1, 'draft fixture golden task', 'web', 'easy', 60, $2, 'golden')`,
		"draft_fixture_golden_"+uuid.NewString()[:8],
		"FLAG{draft-golden-"+uuid.NewString()[:8]+"}",
	); err != nil {
		return draftseed.ContentSeed{}, fmt.Errorf("result fixture: create golden task: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT version.task_id, version.version, 1, true, 'content_validation'
		FROM task_versions AS version
		WHERE NOT EXISTS (
			SELECT 1
			FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = version.task_id
				AND attestation.task_version = version.version
		)`); err != nil {
		return draftseed.ContentSeed{}, fmt.Errorf("result fixture: attest task versions: %w", err)
	}
	if _, err := pool.Exec(ctx, `SELECT publish_task_pool_heads()`); err != nil {
		return draftseed.ContentSeed{}, fmt.Errorf("result fixture: publish task pools: %w", err)
	}

	var publicationID, normalPoolID, goldenPoolID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT publication.id, normal_pool.id, golden_pool.id
		FROM task_pool_publications AS publication
		INNER JOIN task_pool_revisions AS normal_pool
			ON normal_pool.publication_id = publication.id AND normal_pool.kind = 'normal'
		INNER JOIN task_pool_revisions AS golden_pool
			ON golden_pool.publication_id = publication.id AND golden_pool.kind = 'golden'
		WHERE (
			SELECT COUNT(*)
			FROM task_pool_version_memberships AS membership
			INNER JOIN tasks AS task
				ON task.id = membership.task_id
				AND task.current_version = membership.task_version
			WHERE membership.task_pool_revision_id = normal_pool.id
				AND membership.task_id = ANY($1::UUID[])
		) = cardinality($1::UUID[])
		AND EXISTS (
			SELECT 1
			FROM task_pool_version_memberships AS membership
			INNER JOIN tasks AS task ON task.id = membership.task_id
			LEFT JOIN LATERAL (
				SELECT attestation.healthy
				FROM task_version_health_attestations AS attestation
				WHERE attestation.task_id = membership.task_id
					AND attestation.task_version = membership.task_version
				ORDER BY attestation.revision DESC
				LIMIT 1
			) AS health ON true
			WHERE membership.task_pool_revision_id = golden_pool.id
				AND task.kind = 'golden'
				AND task.enabled
				AND task.deleted_at IS NULL
				AND health.healthy
		)
		ORDER BY publication.revision ASC
		LIMIT 1`, taskIDs).Scan(&publicationID, &normalPoolID, &goldenPoolID); err != nil {
		return draftseed.ContentSeed{}, fmt.Errorf("result fixture: find published task pools: %w", err)
	}

	configurationID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tournament_content_configurations (
			id, tournament_id, revision, state, pool_publication_id,
			normal_pool_revision_id, golden_pool_revision_id, created_at
		)
		VALUES ($1, $2, 1, 'draft', $3, $4, $5, $6)`,
		configurationID, tournamentID, publicationID, normalPoolID, goldenPoolID, at); err != nil {
		return draftseed.ContentSeed{}, fmt.Errorf("result fixture: create content configuration: %w", err)
	}
	bo1PoolID, bo3PoolID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tournament_category_pool_revisions (
			id, configuration_id, format, revision, created_at
		)
		VALUES ($1, $2, 'bo1', 1, $3), ($4, $2, 'bo3', 1, $3)`,
		bo1PoolID, configurationID, at, bo3PoolID); err != nil {
		return draftseed.ContentSeed{}, fmt.Errorf("result fixture: create category pools: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO tournament_category_pool_memberships (category_pool_revision_id, category, created_at)
		VALUES
			($1, 'web', $3), ($1, 'crypto', $3), ($1, 'forensics', $3),
			($2, 'web', $3), ($2, 'crypto', $3), ($2, 'forensics', $3),
			($2, 'reverse', $3), ($2, 'pwn', $3)`, bo1PoolID, bo3PoolID, at); err != nil {
		return draftseed.ContentSeed{}, fmt.Errorf("result fixture: create category memberships: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO tournament_content_stage_defaults (
			configuration_id, stage, format, category_mode, categories,
			category_pool_revision_id, task_pool_kind, created_at
		)
		VALUES
			($1, 'swiss', 'bo1', 'random', '["web"]'::jsonb, $2, 'normal', $4),
			($1, 'golden', 'bo1', 'random', '["web"]'::jsonb, $2, 'golden', $4),
			($1, 'semifinal', 'bo1', 'draft', '["web", "crypto", "forensics"]'::jsonb, $2, 'normal', $4),
			($1, 'final', 'bo3', 'draft', '["web", "crypto", "forensics", "reverse", "pwn"]'::jsonb, $3, 'normal', $4)`,
		configurationID, bo1PoolID, bo3PoolID, at); err != nil {
		return draftseed.ContentSeed{}, fmt.Errorf("result fixture: create stage defaults: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE tournament_content_configurations
		SET state = 'published', published_at = $2
		WHERE id = $1`, configurationID, at); err != nil {
		return draftseed.ContentSeed{}, fmt.Errorf("result fixture: publish content configuration: %w", err)
	}
	return draftseed.ContentSeed{TaskIDs: taskIDs, NormalPoolRevisionID: normalPoolID}, nil
}

func createConservativePlan(
	ctx context.Context,
	pool *pgxpool.Pool,
	draft draftseed.PreparedScope,
	createdAt time.Time,
) (uuid.UUID, error) {
	planID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO assignment_plans (
			id, tournament_id, roster_id, kind, revision_id,
			source_roster_revision, source_pool_revision_id,
			constraint_graph, proof_evidence, created_at
		)
		VALUES (
			$1, $2, $3, 'conservative', $4,
			1, $5,
			'{"type":"capacity"}'::JSONB,
			'{"covers":"full_roster"}'::JSONB,
			$6
		)`, planID, draft.TournamentID, draft.RosterID, uuid.New(),
		draft.Content.NormalPoolRevisionID, createdAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("result fixture: create conservative plan: %w", err)
	}
	return planID, nil
}

func createExactPlan(
	ctx context.Context,
	pool *pgxpool.Pool,
	draft draftseed.PreparedScope,
	parentPlanID uuid.UUID,
	createdAt time.Time,
) (uuid.UUID, error) {
	planID := uuid.New()
	seed := bytes.Repeat([]byte{5}, 32)
	digest := bytes.Repeat([]byte{6}, 32)
	_, err := pool.Exec(ctx, `
		INSERT INTO assignment_plans (
			id, tournament_id, roster_id, kind, parent_plan_id, revision_id,
			source_roster_revision, source_pool_revision_id,
			source_draft_revision_id, reachable_branch_count,
			constraint_graph, proof_evidence,
			decision_evidence_id, decision_algorithm_version,
			decision_inputs, decision_seed, decision_result,
			decision_replay_digest, decision_owner_id, decided_at, created_at
		)
		VALUES (
			$1, $2, $3, 'exact', $4, $5,
			1, $6,
			$7, 2,
			'{"nodes":6,"edges":6}'::JSONB,
			'{"eligible":true}'::JSONB,
			$8, 'hmac-sha256-order-v1',
			'["crypto-final","pwn-final"]'::JSONB,
			$9,
			'["crypto-final","pwn-final"]'::JSONB,
			$10, $1, $11, $11
		)`, planID, draft.TournamentID, draft.RosterID, parentPlanID, uuid.New(),
		draft.Content.NormalPoolRevisionID, draft.Draft.InitialRevisionID, uuid.New(), seed, digest, createdAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("result fixture: create exact plan: %w", err)
	}
	return planID, nil
}

func createBranch(
	ctx context.Context,
	pool *pgxpool.Pool,
	draft draftseed.PreparedScope,
	planID uuid.UUID,
	branchKey string,
	createdAt time.Time,
) (uuid.UUID, error) {
	branchID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO assignment_branches (
			id, plan_id, draft_id, draft_revision_id,
			branch_key, category_sequence, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6::JSONB, $7)`,
		branchID, planID, draft.Draft.DraftID, draft.Draft.InitialRevisionID,
		branchKey, `["web"]`, createdAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("result fixture: create assignment branch: %w", err)
	}
	return branchID, nil
}

func createReservations(
	ctx context.Context,
	pool *pgxpool.Pool,
	draft draftseed.PreparedScope,
	planID uuid.UUID,
	branchID uuid.UUID,
	createdAt time.Time,
) ([]assignmentReservation, error) {
	reservations := make([]assignmentReservation, 3)
	for i := range reservations {
		taskID, taskVersion, err := selectTask(ctx, pool, planID, i)
		if err != nil {
			return nil, err
		}
		edgeID := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO assignment_plan_edges (
				id, plan_id, branch_id, position,
				task_id, task_version, selection_evidence, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, '{"eligible":true}'::JSONB, $7)`,
			edgeID, planID, branchID, i+1, taskID, taskVersion, createdAt); err != nil {
			return nil, fmt.Errorf("result fixture: create assignment edge: %w", err)
		}
		reservationID := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO task_version_reservations (
				id, edge_id, plan_id, branch_id,
				task_id, task_version, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			reservationID, edgeID, planID, branchID, taskID, taskVersion, createdAt); err != nil {
			return nil, fmt.Errorf("result fixture: create reservation: %w", err)
		}
		snapshotID := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO task_snapshots (
				id, reservation_id, task_id, task_version, kind,
				title, description, category, difficulty,
				time_limit, flag, hints, content_digest, created_at
			)
			VALUES (
				$1, $2, $3, $4, 'normal',
				$5, 'immutable description', 'web', 'medium',
				180, 'FLAG{snapshot}', '["hint"]'::JSONB, $6, $7
			)`, snapshotID, reservationID, taskID, taskVersion,
			fmt.Sprintf("snapshot %d", i+1), bytes.Repeat([]byte{byte(i + 10)}, 32), createdAt); err != nil {
			return nil, fmt.Errorf("result fixture: create task snapshot: %w", err)
		}
		reservations[i] = assignmentReservation{
			reservationID: reservationID, snapshotID: snapshotID,
			taskID: taskID, taskVersion: taskVersion,
		}
	}
	return reservations, nil
}

func selectTask(
	ctx context.Context,
	pool *pgxpool.Pool,
	planID uuid.UUID,
	position int,
) (uuid.UUID, int, error) {
	var taskID uuid.UUID
	var taskVersion int
	err := pool.QueryRow(ctx, `
		WITH pinned_normal_pool AS (
			SELECT pool.id
			FROM assignment_plans AS plan
			INNER JOIN task_pool_revisions AS pool
				ON pool.id = plan.source_pool_revision_id
				AND pool.kind = 'normal'
			WHERE plan.id = $3::UUID
		)
		SELECT task.id, membership.task_version
		FROM pinned_normal_pool AS pool
		INNER JOIN task_pool_version_memberships AS membership
			ON membership.task_pool_revision_id = pool.id
		INNER JOIN tasks AS task
			ON task.id = membership.task_id
		INNER JOIN task_versions AS version
			ON version.task_id = membership.task_id
			AND version.version = membership.task_version
		LEFT JOIN LATERAL (
			SELECT attestation.healthy
			FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = membership.task_id
				AND attestation.task_version = membership.task_version
			ORDER BY attestation.revision DESC
			LIMIT 1
		) AS health ON true
		WHERE task.kind = 'normal'
			AND task.category = $1
			AND task.enabled
			AND task.deleted_at IS NULL
			AND health.healthy
			AND NOT EXISTS (
				SELECT 1
				FROM task_version_reservations AS reservation
				WHERE reservation.plan_id = $3::UUID
					AND reservation.task_id = membership.task_id
					AND reservation.task_version = membership.task_version
			)
		ORDER BY task.id
		OFFSET $2
		LIMIT 1`, "web", position, planID).Scan(&taskID, &taskVersion)
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("result fixture: select task: %w", err)
	}
	return taskID, taskVersion, nil
}

func updateAssignmentState(
	ctx context.Context,
	pool *pgxpool.Pool,
	planID, branchID, releasedBranchID uuid.UUID,
	reservations, releasedReservations []assignmentReservation,
	committedAt time.Time,
) error {
	for _, reservation := range reservations {
		if _, err := pool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'committed', revision = revision + 1, committed_at = $2
			WHERE id = $1`, reservation.reservationID, committedAt); err != nil {
			return fmt.Errorf("result fixture: commit reservation: %w", err)
		}
	}
	for _, reservation := range releasedReservations {
		if _, err := pool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'released', revision = revision + 1,
				released_at = $2, release_reason = 'unused fixture branch'
			WHERE id = $1`, reservation.reservationID, committedAt); err != nil {
			return fmt.Errorf("result fixture: release reservation: %w", err)
		}
	}
	if _, err := pool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'active', activated_at = $2
		WHERE id = $1`, branchID, committedAt.Add(time.Second)); err != nil {
		return fmt.Errorf("result fixture: activate branch: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'released', released_at = $2,
			release_reason = 'unused fixture branch'
		WHERE id = $1`, releasedBranchID, committedAt.Add(time.Second)); err != nil {
		return fmt.Errorf("result fixture: release branch: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE assignment_plans
		SET state = 'committed', active_branch_id = $2, committed_at = $3
		WHERE id = $1`, planID, branchID, committedAt.Add(2*time.Second)); err != nil {
		return fmt.Errorf("result fixture: commit assignment plan: %w", err)
	}
	return nil
}
