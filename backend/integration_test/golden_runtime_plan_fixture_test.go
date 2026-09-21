//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type goldenRuntimePlanGroup struct {
	id               uuid.UUID
	revisionID       uuid.UUID
	definitionDigest []byte
	positionFrom     int16
	positionTo       int16
	members          []goldenRuntimePlanMember
	edges            []assignmentrepo.AssignmentEdgeInput
}

type goldenRuntimePlanMember struct {
	participantID uuid.UUID
	position      int16
}

type goldenRuntimePlanTask struct {
	id            uuid.UUID
	version       int
	title         string
	description   string
	category      domain.Category
	difficulty    domain.Difficulty
	timeLimit     int
	flag          string
	hints         []string
	taskURL       *string
	sourceFileURL *string
	digest        [sha256.Size]byte
}

func createGoldenRuntimeTestPlan(
	ctx context.Context,
	t testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	sourceProjectionID uuid.UUID,
	sourceProjectionRevision int64,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()
	groups := loadGoldenRuntimePlanGroups(ctx, t, tournamentID)
	tasks, poolID, poolRevision := loadGoldenRuntimePlanTasks(ctx, t, tournamentID, len(groups)*3)
	draftID, draftRevisionID := loadGoldenRuntimePlanDraft(ctx, t, rosterID)
	var rosterRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT revision FROM rosters WHERE id = $1`, rosterID).Scan(&rosterRevision))

	txManager := postgres.NewTxManager(sharedPool)
	assignmentRepository := assignmentrepo.NewAssignmentPostgres(txManager)
	parentPlanID := uuid.New()
	_, err := assignmentRepository.CreateConservativePlan(ctx, assignmentrepo.ConservativePlanInput{
		ID: parentPlanID, TournamentID: tournamentID, RosterID: rosterID, RevisionID: uuid.New(),
		ReserveCount:         domain.AssignmentReserveCount,
		SourceRosterRevision: rosterRevision, SourcePoolRevisionID: poolID,
		ConstraintGraph: map[string]any{"kind": "golden_capacity"},
		ProofEvidence:   map[string]any{"group_count": len(groups), "tasks_per_group": 3},
		CreatedAt:       createdAt,
	})
	require.NoError(t, err)

	planID := uuid.New()
	planRevisionID := uuid.New()
	branches := make([]assignmentrepo.AssignmentBranchInput, len(groups))
	decisionInputs := make([]string, len(groups))
	for groupIndex := range groups {
		branchID := uuid.New()
		branches[groupIndex] = assignmentrepo.AssignmentBranchInput{
			ID: branchID, DraftID: draftID, DraftRevisionID: draftRevisionID,
			Key: fmt.Sprintf("golden-group-%02d", groupIndex+1),
		}
		decisionInputs[groupIndex] = branches[groupIndex].Key
		for edgeIndex := range 3 {
			task := tasks[groupIndex*3+edgeIndex]
			edge := assignmentrepo.AssignmentEdgeInput{
				ID: uuid.New(), ReservationID: uuid.New(), Position: edgeIndex + 1,
				Snapshot: domain.AssignmentTaskSnapshot{
					SnapshotID: uuid.New(), TaskID: task.id, Version: task.version,
					Kind: domain.AssignmentTaskKindGolden, Title: task.title, Description: task.description,
					Category: task.category, Difficulty: task.difficulty, TimeLimit: task.timeLimit,
					Flag: task.flag, Hints: append([]string(nil), task.hints...),
					TaskURL: task.taskURL, SourceFileURL: task.sourceFileURL,
				},
				ContentDigest: task.digest,
				SelectionEvidence: map[string]any{
					"eligible": true, "group_revision_id": groups[groupIndex].revisionID.String(),
				},
			}
			branches[groupIndex].Edges = append(branches[groupIndex].Edges, edge)
			branches[groupIndex].Categories = append(branches[groupIndex].Categories, task.category)
		}
		groups[groupIndex].edges = append([]assignmentrepo.AssignmentEdgeInput(nil), branches[groupIndex].Edges...)
	}
	decision, err := domain.NewDecisionEvidence(
		uuid.New(), domain.DecisionPurposeTask, domain.DecisionAlgorithmV1,
		decisionInputs, planID, createdAt,
	)
	require.NoError(t, err)
	require.NoError(t, decision.Validate())
	seenTasks := make(map[uuid.UUID]struct{}, len(tasks))
	for _, branch := range branches {
		require.Len(t, branch.Edges, domain.AssignmentReserveCount+1)
		require.NotEmpty(t, branch.Categories)
		for _, edge := range branch.Edges {
			require.NoError(t, edge.Snapshot.Validate())
			require.NotEqual(t, [sha256.Size]byte{}, edge.ContentDigest)
			_, duplicate := seenTasks[edge.Snapshot.TaskID]
			require.False(t, duplicate)
			seenTasks[edge.Snapshot.TaskID] = struct{}{}
		}
	}
	writeGoldenRuntimeAssignmentPlan(
		ctx, t, planID, planRevisionID, parentPlanID, tournamentID, rosterID,
		rosterRevision, poolID, draftRevisionID, decision, branches, createdAt,
	)

	writeGoldenRuntimePlanSnapshot(
		ctx, t, planID, planRevisionID, tournamentID, rosterID, sourceProjectionID,
		sourceProjectionRevision, poolID, poolRevision, groups, createdAt,
	)
	return planID
}

//nolint:funlen // The fixture writes the exact assignment graph in one transaction.
func writeGoldenRuntimeAssignmentPlan(
	ctx context.Context,
	t testing.TB,
	planID uuid.UUID,
	planRevisionID uuid.UUID,
	parentPlanID uuid.UUID,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	rosterRevision int64,
	poolID uuid.UUID,
	draftRevisionID uuid.UUID,
	decision domain.DecisionEvidence,
	branches []assignmentrepo.AssignmentBranchInput,
	createdAt time.Time,
) {
	t.Helper()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	decisionInputs, err := json.Marshal(decision.NormalizedInputs)
	require.NoError(t, err)
	decisionResult, err := json.Marshal(decision.Result)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO assignment_plans (
			id, tournament_id, roster_id, kind, parent_plan_id, revision_id,
			source_roster_revision, source_pool_revision_id, source_draft_revision_id,
			reachable_branch_count, constraint_graph, proof_evidence,
			decision_evidence_id, decision_algorithm_version, decision_inputs,
			decision_seed, decision_result, decision_replay_digest, decision_owner_id,
			decided_at, created_at
		) VALUES (
			$1,$2,$3,'exact',$4,$5,$6,$7,$8,$9,
			'{"kind":"golden_exact"}'::jsonb,'{"primary":1,"automatic_reserves":2}'::jsonb,
			$10,$11,$12::jsonb,$13,$14::jsonb,$15,$1,$16,$16
		)`,
		planID, tournamentID, rosterID, parentPlanID, planRevisionID, rosterRevision,
		poolID, draftRevisionID, len(branches), decision.ID, decision.AlgorithmVersion,
		decisionInputs, decision.Seed[:], decisionResult, decision.ReplayDigest[:], createdAt,
	)
	require.NoError(t, err)
	for _, branch := range branches {
		categories, marshalErr := json.Marshal(branch.Categories)
		require.NoError(t, marshalErr)
		_, err = tx.Exec(ctx, `
			INSERT INTO assignment_branches (
				id, plan_id, draft_id, draft_revision_id, branch_key, category_sequence, created_at
			) VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7)`,
			branch.ID, planID, branch.DraftID, branch.DraftRevisionID, branch.Key, categories, createdAt,
		)
		require.NoError(t, err)
		for _, edge := range branch.Edges {
			selection, marshalErr := json.Marshal(edge.SelectionEvidence)
			require.NoError(t, marshalErr)
			_, err = tx.Exec(ctx, `
				INSERT INTO assignment_plan_edges (
					id, plan_id, branch_id, position, task_id, task_version, selection_evidence, created_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8)`,
				edge.ID, planID, branch.ID, edge.Position, edge.Snapshot.TaskID,
				edge.Snapshot.Version, selection, createdAt,
			)
			require.NoError(t, err)
			_, err = tx.Exec(ctx, `
				INSERT INTO task_version_reservations (
					id, edge_id, plan_id, branch_id, task_id, task_version, created_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				edge.ReservationID, edge.ID, planID, branch.ID, edge.Snapshot.TaskID,
				edge.Snapshot.Version, createdAt,
			)
			require.NoError(t, err)
			_, err = tx.Exec(ctx, `
				INSERT INTO task_snapshots (
					id, reservation_id, task_id, task_version, kind, title, description,
					category, difficulty, time_limit, flag, hints, task_url, source_file_url,
					content_digest, created_at
				)
				SELECT $1, $2, version.task_id, version.version, task.kind, version.title,
					version.description, version.category, version.difficulty, version.time_limit,
					version.flag, jsonb_build_array(version.hint_1, version.hint_2, version.hint_3),
					version.task_url, version.source_file_url, version.content_digest, $3
				FROM task_versions AS version
				INNER JOIN tasks AS task ON task.id = version.task_id
				WHERE version.task_id = $4 AND version.version = $5`,
				edge.Snapshot.SnapshotID, edge.ReservationID, createdAt,
				edge.Snapshot.TaskID, edge.Snapshot.Version,
			)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tx.Commit(ctx))
}

func loadGoldenRuntimePlanGroups(
	ctx context.Context,
	t testing.TB,
	tournamentID uuid.UUID,
) []goldenRuntimePlanGroup {
	t.Helper()
	rows, err := sharedPool.Query(ctx, `
		SELECT revision.revision_id, revision.group_id, revision.definition_digest,
			revision.position_from, revision.position_to,
			member.participant_id, member.standing_position
		FROM golden_group_revisions AS revision
		INNER JOIN tournament_stage_tie_group_members AS member
			ON member.command_id = revision.stage_progression_command_id
			AND member.tournament_id = revision.tournament_id
			AND member.roster_id = revision.roster_id
			AND member.group_id = revision.group_id
		WHERE revision.tournament_id = $1
		ORDER BY revision.position_from, member.standing_position`, tournamentID)
	require.NoError(t, err)
	defer rows.Close()
	groups := make([]goldenRuntimePlanGroup, 0)
	for rows.Next() {
		var group goldenRuntimePlanGroup
		var member goldenRuntimePlanMember
		require.NoError(t, rows.Scan(
			&group.revisionID, &group.id, &group.definitionDigest, &group.positionFrom, &group.positionTo,
			&member.participantID, &member.position,
		))
		if len(groups) == 0 || groups[len(groups)-1].revisionID != group.revisionID {
			groups = append(groups, group)
		}
		groups[len(groups)-1].members = append(groups[len(groups)-1].members, member)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, groups)
	return groups
}

func loadGoldenRuntimePlanTasks(
	ctx context.Context,
	t testing.TB,
	tournamentID uuid.UUID,
	required int,
) ([]goldenRuntimePlanTask, uuid.UUID, int64) {
	t.Helper()
	var poolID uuid.UUID
	var poolRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT pool.id, pool.revision
		FROM tournament_content_configurations AS content
		INNER JOIN task_pool_revisions AS pool ON pool.id = content.golden_pool_revision_id
		WHERE content.tournament_id = $1 AND content.state = 'published'`, tournamentID).Scan(&poolID, &poolRevision))
	rows, err := sharedPool.Query(ctx, `
		SELECT version.task_id, version.version, version.title, version.description,
			version.category, version.difficulty, version.time_limit, version.flag,
			version.hint_1, version.hint_2, version.hint_3,
			version.task_url, version.source_file_url, version.content_digest
		FROM task_pool_version_memberships AS membership
		INNER JOIN task_versions AS version
			ON version.task_id = membership.task_id AND version.version = membership.task_version
		INNER JOIN tasks AS task
			ON task.id = version.task_id AND task.current_version = version.version
		LEFT JOIN LATERAL (
			SELECT attestation.healthy
			FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = version.task_id AND attestation.task_version = version.version
			ORDER BY attestation.revision DESC LIMIT 1
		) AS health ON true
		WHERE membership.task_pool_revision_id = $1
			AND task.kind = 'golden' AND task.enabled AND task.deleted_at IS NULL
			AND version.time_limit = 180 AND health.healthy
		ORDER BY version.task_id
		LIMIT $2`, poolID, required)
	require.NoError(t, err)
	defer rows.Close()
	tasks := make([]goldenRuntimePlanTask, 0, required)
	for rows.Next() {
		task := goldenRuntimePlanTask{hints: make([]string, 0, 3)}
		var hint1, hint2, hint3, taskURL, sourceFileURL pgtype.Text
		var digest []byte
		require.NoError(t, rows.Scan(
			&task.id, &task.version, &task.title, &task.description, &task.category, &task.difficulty,
			&task.timeLimit, &task.flag, &hint1, &hint2, &hint3, &taskURL, &sourceFileURL, &digest,
		))
		for _, hint := range []pgtype.Text{hint1, hint2, hint3} {
			if hint.Valid {
				task.hints = append(task.hints, hint.String)
			}
		}
		if taskURL.Valid {
			value := taskURL.String
			task.taskURL = &value
		}
		if sourceFileURL.Valid {
			value := sourceFileURL.String
			task.sourceFileURL = &value
		}
		require.Len(t, digest, sha256.Size)
		copy(task.digest[:], digest)
		tasks = append(tasks, task)
	}
	require.NoError(t, rows.Err())
	require.Len(t, tasks, required)
	return tasks, poolID, poolRevision
}

func loadGoldenRuntimePlanDraft(ctx context.Context, t testing.TB, rosterID uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var draftID, revisionID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT draft.id, revision.id
		FROM draft_revisions AS revision
		INNER JOIN drafts AS draft ON draft.id = revision.draft_id
		WHERE draft.roster_id = $1
		ORDER BY revision.created_at DESC, revision.revision DESC
		LIMIT 1`, rosterID).Scan(&draftID, &revisionID))
	return draftID, revisionID
}

//nolint:gocyclo,funlen // The fixture mirrors one atomic immutable authority aggregate.
func writeGoldenRuntimePlanSnapshot(
	ctx context.Context,
	t testing.TB,
	planID uuid.UUID,
	planRevisionID uuid.UUID,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	sourceProjectionID uuid.UUID,
	sourceProjectionRevision int64,
	poolID uuid.UUID,
	poolRevision int64,
	groups []goldenRuntimePlanGroup,
	createdAt time.Time,
) {
	t.Helper()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var sourceArtifactID uuid.UUID
	var sourceDigest []byte
	var sourcePreviousRevisionID pgtype.UUID
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT previous_revision_id FROM projection_revisions WHERE id = $1`,
		sourceProjectionID,
	).Scan(&sourcePreviousRevisionID))
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT artifact.id, artifact.payload_digest
		FROM projection_revision_artifacts AS binding
		INNER JOIN projection_artifacts AS artifact ON artifact.id = binding.artifact_id
		WHERE binding.revision_id = $1 AND binding.artifact_kind = 'standings'`, sourceProjectionID).Scan(
		&sourceArtifactID, &sourceDigest,
	))
	proofDigest := sha256.Sum256([]byte(planID.String() + sourceProjectionID.String()))
	proof := hex.EncodeToString(proofDigest[:])
	digest := sha256.Sum256([]byte("golden-runtime-plan-" + planID.String()))
	_, err = tx.Exec(ctx, `
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
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, 1, $12, $13, $14, 1, $15, 1, $16, 1, $17, 1, $18, 1,
			$10, $19, $20, $21, $22, $23, $24, $25, $26, $27
		)`,
		planID, planRevisionID, tournamentID, rosterID, uuid.New(), sourceProjectionID,
		sourceProjectionRevision, sourcePreviousRevisionID, sourceArtifactID, sourceDigest,
		uuid.New(), poolID, poolRevision,
		uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), digest[:], digest[:], digest[:],
		digest[:], digest[:], digest[:], digest[:], proof, createdAt,
	)
	require.NoError(t, err)

	for groupIndex, group := range groups {
		insertGoldenRuntimePlanGroup(ctx, t, tx, planID, tournamentID, rosterID,
			sourceProjectionID, sourceProjectionRevision, poolID, planRevisionID,
			groupIndex, group, createdAt)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_exact_plan_snapshot_seals (
			plan_id, tournament_id, roster_id, proof_hash, sealed_at
		) VALUES ($1,$2,$3,$4,$5)`, planID, tournamentID, rosterID, proof, createdAt)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

//nolint:funlen // Each write is one required child of the sealed fixture aggregate.
func insertGoldenRuntimePlanGroup(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	planID uuid.UUID,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	sourceProjectionID uuid.UUID,
	sourceProjectionRevision int64,
	poolID uuid.UUID,
	planRevisionID uuid.UUID,
	groupIndex int,
	group goldenRuntimePlanGroup,
	createdAt time.Time,
) {
	t.Helper()
	_, err := tx.Exec(ctx, `
		INSERT INTO golden_exact_plan_snapshot_groups (
			plan_id, tournament_id, roster_id, group_id, group_revision_id,
			source_projection_revision_id, source_projection_revision,
			position_from, position_to, group_ordinal, definition_digest, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		planID, tournamentID, rosterID, group.id, group.revisionID, sourceProjectionID,
		sourceProjectionRevision, group.positionFrom, group.positionTo, groupIndex+1,
		group.definitionDigest, createdAt,
	)
	require.NoError(t, err)
	for _, member := range group.members {
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_members (
				plan_id, group_revision_id, tournament_id, roster_id, participant_id, position, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			planID, group.revisionID, tournamentID, rosterID, member.participantID, member.position, createdAt,
		)
		require.NoError(t, err)
		insertGoldenRuntimeParticipantReservation(
			ctx, t, tx, planID, tournamentID, rosterID, member.participantID, createdAt,
		)
	}
	for _, edge := range group.edges {
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_edges (
				plan_id, group_revision_id, tournament_id, roster_id, edge_id, reservation_id,
				snapshot_id, task_id, task_version, position, content_digest, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			planID, group.revisionID, tournamentID, rosterID, edge.ID, edge.ReservationID,
			edge.Snapshot.SnapshotID, edge.Snapshot.TaskID, edge.Snapshot.Version,
			edge.Position, edge.ContentDigest[:], createdAt,
		)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_candidates (
				plan_id, tournament_id, roster_id, pool_revision_id, task_id, task_version,
				exists_in_source, enabled, healthy, mutation_locked, publicly_exposed,
				artifact_digest, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,true,true,true,false,false,$7,$8)`,
			planID, tournamentID, rosterID, poolID, edge.Snapshot.TaskID, edge.Snapshot.Version,
			edge.ContentDigest[:], createdAt,
		)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_reservations (
				plan_id, tournament_id, roster_id, task_id, task_version, reservation_id,
				owner_plan_id, owner_plan_revision_id, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$1,$7,$8)`,
			planID, tournamentID, rosterID, edge.Snapshot.TaskID, edge.Snapshot.Version,
			edge.ReservationID, planRevisionID, createdAt,
		)
		require.NoError(t, err)
	}
}

func insertGoldenRuntimeParticipantReservation(
	ctx context.Context,
	t testing.TB,
	tx pgx.Tx,
	planID uuid.UUID,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantID uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()
	var playerID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT player_id FROM participants WHERE roster_id = $1 AND id = $2`, rosterID, participantID).Scan(&playerID))
	_, err := tx.Exec(ctx, `
		INSERT INTO participant_reservations (player_id, tournament_id)
		VALUES ($1, $2) ON CONFLICT (player_id) DO NOTHING`, playerID, tournamentID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_exact_plan_snapshot_participant_reservations (
			plan_id, tournament_id, roster_id, participant_id, player_id,
			reservation_id, revision, acquired_at, updated_at, created_at
		)
		SELECT $1, $2, $3, $4, reservation.player_id, reservation.reservation_id,
			reservation.revision, reservation.acquired_at, reservation.updated_at, $5
		FROM participant_reservations AS reservation
		WHERE reservation.player_id = $6 AND reservation.tournament_id = $2`,
		planID, tournamentID, rosterID, participantID, createdAt, playerID,
	)
	require.NoError(t, err)
}
