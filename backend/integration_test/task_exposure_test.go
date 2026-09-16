//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

type taskExposureRosterFixture struct {
	tournamentID       uuid.UUID
	rosterID           uuid.UUID
	projectionRevision int64
	playerIDs          []uuid.UUID
	workflow           *tournamentadmin.RosterWorkflow
}

type taskExposureReceiptSnapshot struct {
	id            uuid.UUID
	assignmentID  uuid.UUID
	attemptID     uuid.UUID
	rosterID      uuid.UUID
	participantID uuid.UUID
	instanceID    uuid.UUID
	snapshotID    uuid.UUID
	taskID        uuid.UUID
	taskVersion   int32
	deliveredAt   time.Time
	createdAt     time.Time
}

type taskExposureSnapshot struct {
	id            uuid.UUID
	reservationID uuid.UUID
	taskID        uuid.UUID
	taskVersion   int32
	kind          string
	title         string
	description   string
	category      string
	difficulty    string
	timeLimit     int32
	flag          string
	hints         []byte
	contentDigest []byte
	createdAt     time.Time
}

func TestPrivateTaskReceiptDoesNotExposeSameVersionAcrossTournament(t *testing.T) {
	ctx := context.Background()
	TruncateTables(t, sharedPool)
	t.Cleanup(func() { TruncateTables(t, sharedPool) })

	// Seed enough capacity first. The result fixture publishes a later pool
	// revision containing these tasks and its own assignment task, so both
	// tournaments bind the exact same published pool.
	preparePreflightCapacityContent(ctx, t, 4)
	first := createResultAuditMigrationFixture(ctx, t)
	deliveredAt := first.lockedAt.Add(time.Second)
	createPrivateTaskReceipts(ctx, t, first, deliveredAt)

	receipt := loadTaskExposureReceiptSnapshot(ctx, t, first.draft.rosterID, first.draft.participantIDs[0])
	snapshot := loadTaskExposureSnapshot(ctx, t, receipt.snapshotID)
	receiptCount := countTaskExposureReceipts(ctx, t, first.draft.rosterID)
	publicationRevision := currentTaskPoolPublicationRevision(ctx, t)
	poolCount := countTaskExposurePoolMembers(ctx, t, publicationRevision)
	historySeriesID := createMigrationSeries(
		ctx, t, first.draft.tournamentID, first.draft.rosterID, first.draft.participantIDs, "bo1",
	)

	var exposed bool
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM task_public_exposures
			WHERE task_id = $1 AND task_version = $2
		)`, receipt.taskID, receipt.taskVersion).Scan(&exposed))
	require.False(t, exposed, "a private delivery receipt must not become public exposure evidence")

	// The second participant can receive the same exact version in the first
	// tournament, while the same participant and version remain unique.
	duplicateErr := insertPrivateTaskReceipt(
		ctx,
		privateTaskReceiptIdentity{
			assignmentID: receipt.assignmentID,
			attemptID:    receipt.attemptID,
			rosterID:     receipt.rosterID,
			snapshotID:   receipt.snapshotID,
			taskID:       receipt.taskID,
			taskVersion:  receipt.taskVersion,
		},
		first.draft.participantIDs[0],
		domain.ParticipantTaskInstanceID(receipt.assignmentID, first.draft.participantIDs[0]),
		receipt.snapshotID,
		receipt.taskID,
		receipt.taskVersion,
		deliveredAt.Add(time.Microsecond),
	)
	require.Error(t, duplicateErr, "the same participant cannot receive one exact task version twice")

	second := createTaskExposureRosterFixture(ctx, t, publicationRevision, "cross-tournament")
	preflightID := uuid.New()
	report, err := second.workflow.RunPreflight(ctx, tournamentadmin.PreflightCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: second.tournamentID,
			CommandID:    preflightID,
		},
		ExpectedProjectionRevision: second.projectionRevision,
	})
	require.NoError(t, err)
	require.True(t, report.Passed(), "cross-tournament preflight checks: %+v", report.Checks)

	locked, err := second.workflow.LockRoster(ctx, tournamentadmin.LockRosterCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: second.tournamentID,
			CommandID:    uuid.New(),
		},
		ExpectedProjectionRevision: second.projectionRevision,
		PreflightRevisionID:        preflightID,
		CheckedInPlayerIDs:         second.playerIDs,
	})
	require.NoError(t, err)
	require.True(t, locked.Locked)

	// Exercise the new tournament scope on live reservations. The exact task
	// version already has a committed reservation in the first tournament;
	// the second tournament may reserve that same version independently.
	insertTaskExposureReservationInTournament(ctx, t, second, receipt.taskID, receipt.taskVersion)
	var reservationCount, tournamentCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT tournament_id)
		FROM task_version_reservations
		WHERE task_id = $1 AND task_version = $2
			AND state IN ('reserved', 'committed')`,
		receipt.taskID, receipt.taskVersion).Scan(&reservationCount, &tournamentCount))
	require.Equal(t, 2, reservationCount)
	require.Equal(t, 2, tournamentCount)
	history, err := sqlc.New(sharedPool).LockExactNormalAssignmentHistory(ctx, sqlc.LockExactNormalAssignmentHistoryParams{
		TournamentID: first.draft.tournamentID,
		RosterID:     first.draft.rosterID,
		SeriesID:     historySeriesID,
	})
	require.NoError(t, err)
	require.Len(t, history, 2,
		"the production history query must include both target participants across another Series")
	for _, row := range history {
		require.Equal(t, receipt.taskID, row.TaskID)
		require.Equal(t, receipt.taskVersion, row.TaskVersion)
		require.Contains(t, first.draft.participantIDs, row.ParticipantID)
	}

	gotReceipt := loadTaskExposureReceiptSnapshot(ctx, t, first.draft.rosterID, first.draft.participantIDs[0])
	require.Equal(t, receipt, gotReceipt)
	require.Equal(t, receiptCount, countTaskExposureReceipts(ctx, t, first.draft.rosterID))
	require.Equal(t, snapshot, loadTaskExposureSnapshot(ctx, t, receipt.snapshotID))
	require.Equal(t, poolCount, countTaskExposurePoolMembers(ctx, t, publicationRevision))
	secondParticipantIDs := loadTaskExposureParticipants(ctx, t, second.rosterID)
	secondHistorySeriesID := createMigrationSeries(
		ctx, t, second.tournamentID, second.rosterID, secondParticipantIDs, "bo1",
	)
	secondHistory, err := sqlc.New(sharedPool).LockExactNormalAssignmentHistory(ctx, sqlc.LockExactNormalAssignmentHistoryParams{
		TournamentID: second.tournamentID,
		RosterID:     second.rosterID,
		SeriesID:     secondHistorySeriesID,
	})
	require.NoError(t, err)
	require.Empty(t, secondHistory,
		"a participant in another tournament must not inherit the private task history")
}

func TestExplicitPublicAndSpectatorExposureBlocksPreflightAndSavedLock(t *testing.T) {
	ctx := context.Background()
	TruncateTables(t, sharedPool)
	t.Cleanup(func() { TruncateTables(t, sharedPool) })

	contentRevision := preparePreflightCapacityContent(ctx, t, 4)
	fixture := createTaskExposureRosterFixture(ctx, t, contentRevision, "exposure-fence")
	preflightID := uuid.New()
	passed, err := fixture.workflow.RunPreflight(ctx, tournamentadmin.PreflightCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: fixture.tournamentID,
			CommandID:    preflightID,
		},
		ExpectedProjectionRevision: fixture.projectionRevision,
	})
	require.NoError(t, err)
	require.True(t, passed.Passed(), "preflight checks before disclosure: %+v", passed.Checks)

	normalTaskID, normalVersion := loadTaskExposureTarget(ctx, t, fixture.tournamentID, "normal")
	goldenTaskID, goldenVersion := loadTaskExposureTarget(ctx, t, fixture.tournamentID, "golden")
	adjacentVersion := createTaskExposureAdjacentVersion(ctx, t, normalTaskID)
	require.Equal(t, normalVersion+1, adjacentVersion)
	recordTaskExposure(ctx, t, normalTaskID, normalVersion, "public")
	recordTaskExposure(ctx, t, goldenTaskID, goldenVersion, "spectator")

	normalPoolRevisionID, goldenPoolRevisionID := loadTaskExposurePoolRevisions(ctx, t, fixture.tournamentID)
	healthRows, err := sqlc.New(sharedPool).ListTaskPoolVersionHealth(
		ctx, []uuid.UUID{normalPoolRevisionID, goldenPoolRevisionID},
	)
	require.NoError(t, err)
	requireTaskExposureHealth(t, healthRows, normalPoolRevisionID, normalTaskID, normalVersion, true)
	requireTaskExposureHealth(t, healthRows, goldenPoolRevisionID, goldenTaskID, goldenVersion, true)

	latestPools, err := sqlc.New(sharedPool).GetLatestTaskPoolPublication(ctx)
	require.NoError(t, err)
	var latestNormalPoolID uuid.UUID
	for _, pool := range latestPools {
		if pool.Kind == "normal" {
			latestNormalPoolID = pool.PoolRevisionID
		}
	}
	require.NotEqual(t, uuid.Nil, latestNormalPoolID)
	latestHealth, err := sqlc.New(sharedPool).ListTaskPoolVersionHealth(ctx, []uuid.UUID{latestNormalPoolID})
	require.NoError(t, err)
	requireTaskExposureHealth(t, latestHealth, latestNormalPoolID, normalTaskID, adjacentVersion, false)

	var normalAudience, goldenAudience string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT audience
		FROM task_public_exposures
		WHERE task_id = $1 AND task_version = $2`, normalTaskID, normalVersion).Scan(&normalAudience))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT audience
		FROM task_public_exposures
		WHERE task_id = $1 AND task_version = $2`, goldenTaskID, goldenVersion).Scan(&goldenAudience))
	require.Equal(t, "public", normalAudience)
	require.Equal(t, "spectator", goldenAudience)

	_, err = sharedPool.Exec(ctx, `
		UPDATE task_public_exposures
		SET audience = 'spectator'
		WHERE task_id = $1 AND task_version = $2`, normalTaskID, normalVersion)
	require.Error(t, err, "public exposure evidence must be append-only")
	_, err = sharedPool.Exec(ctx, `
		DELETE FROM task_public_exposures
		WHERE task_id = $1 AND task_version = $2`, goldenTaskID, goldenVersion)
	require.Error(t, err, "spectator exposure evidence must be append-only")
	var exposureCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM task_public_exposures
		WHERE (task_id, task_version) IN (($1, $2), ($3, $4))`,
		normalTaskID, normalVersion, goldenTaskID, goldenVersion).Scan(&exposureCount))
	require.Equal(t, 2, exposureCount)

	_, err = fixture.workflow.LockRoster(ctx, tournamentadmin.LockRosterCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: fixture.tournamentID,
			CommandID:    uuid.New(),
		},
		ExpectedProjectionRevision: fixture.projectionRevision,
		PreflightRevisionID:        preflightID,
		CheckedInPlayerIDs:         fixture.playerIDs,
	})
	require.ErrorIs(t, err, domain.ErrConflict)

	var lockedAt *time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT roster.locked_at
		FROM rosters AS roster
		WHERE roster.id = $1`, fixture.rosterID).Scan(&lockedAt))
	require.Nil(t, lockedAt)
	var reservationCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM participant_reservations
		WHERE tournament_id = $1`, fixture.tournamentID).Scan(&reservationCount))
	require.Zero(t, reservationCount)

	fresh, err := fixture.workflow.RunPreflight(ctx, tournamentadmin.PreflightCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: fixture.tournamentID,
			CommandID:    uuid.New(),
		},
		ExpectedProjectionRevision: fixture.projectionRevision,
	})
	require.NoError(t, err)
	require.False(t, fresh.Passed(), "fresh preflight must reject disclosed task versions")
	requirePreflightCheckFailed(t, fresh, tournamentpreflight.CodeTaskExposed)
}

func TestExplicitExposureBlocksExactNormalCandidateAssignment(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(ctx, t) })

	fixture := createTournamentAdminSwissProofFixture(ctx, t)
	binding := fixture.binding[0]
	_, err := sharedPool.Exec(ctx, `
		UPDATE series
		SET state = 'locked', revision = revision + 1, updated_at = clock_timestamp()
		WHERE id = $1 AND state = 'ready'`, binding.SeriesID)
	require.NoError(t, err)
	reservationAt := time.Now().UTC().Truncate(time.Microsecond)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO participant_reservations (player_id, tournament_id, acquired_at, updated_at)
		SELECT participant.player_id, roster.tournament_id, $2, $2
		FROM participants AS participant
		JOIN rosters AS roster ON roster.id = participant.roster_id
		WHERE participant.id IN ($1, $3)`,
		binding.FirstParticipantID, reservationAt, binding.SecondParticipantID)
	require.NoError(t, err)

	var slotID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id
		FROM game_slots
		WHERE series_id = $1 AND roster_id = $2 AND slot_number = 1`,
		binding.SeriesID, fixture.rosterID).Scan(&slotID))
	scope := assignmentusecase.ExactNormalAssignmentScope{
		TournamentID:   fixture.tournamentID,
		RosterID:       fixture.rosterID,
		SeriesID:       binding.SeriesID,
		SlotID:         slotID,
		CategoryLockID: binding.CategoryRevisionID,
	}
	repository := postgres.NewExactNormalAssignmentPostgres(fixture.tx)
	_, err = repository.LoadExactNormalAssignmentAuthority(ctx, scope)
	require.NoError(t, err, "the exact-normal authority should have candidates before exposure")

	rows, err := sharedPool.Query(ctx, `
		SELECT membership.task_id, membership.task_version
		FROM task_pool_version_memberships AS membership
		WHERE membership.task_pool_revision_id = $1
		ORDER BY membership.task_id, membership.task_version`, fixture.normalPoolRevisionID)
	require.NoError(t, err)
	for rows.Next() {
		var taskID uuid.UUID
		var taskVersion int32
		require.NoError(t, rows.Scan(&taskID, &taskVersion))
		recordTaskExposure(ctx, t, taskID, taskVersion, "public")
	}
	require.NoError(t, rows.Err())
	rows.Close()

	_, err = repository.LoadExactNormalAssignmentAuthority(ctx, scope)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func createTaskExposureRosterFixture(
	ctx context.Context,
	t *testing.T,
	contentRevision int64,
	name string,
) taskExposureRosterFixture {
	t.Helper()
	createdAt := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	tournamentID := uuid.New()
	rosterID := uuid.New()
	tx := postgres.NewTxManager(sharedPool)
	_, _, err := postgres.NewTournamentPostgres(tx).Create(ctx, postgres.TournamentCreateInput{
		ID: tournamentID, RosterID: rosterID, Name: name,
		PublicID: tournamentID.String(), PlannedRosterSize: 4,
		ContentRevision: contentRevision, CreatedAt: createdAt,
	})
	require.NoError(t, err)
	projectionRevision := publishInitialPreflightProjection(ctx, t, tournamentID, rosterID, createdAt)
	_, changed, err := postgres.NewTournamentPostgres(tx).Transition(ctx, postgres.TournamentTransitionInput{
		ID: tournamentID, ExpectedRevision: 1, ExpectedState: domain.TournamentStateDraft,
		NextState: domain.TournamentStateRegistration, UpdatedAt: createdAt.Add(time.Millisecond),
	})
	require.NoError(t, err)
	require.True(t, changed)
	playerIDs := createMigrationPlayers(ctx, t, 4)
	createSwissMigrationParticipants(ctx, t, rosterID, playerIDs)
	return taskExposureRosterFixture{
		tournamentID: tournamentID, rosterID: rosterID,
		projectionRevision: projectionRevision, playerIDs: playerIDs,
		workflow: tournamentadmin.NewRosterWorkflow(tournamentadmin.RosterWorkflowDependencies{
			Transactions:  tx,
			Repository:    rosterrepo.NewTournamentAdminRosterPostgres(tx),
			RuntimeHealth: taskExposureHealthyRuntime{},
		}),
	}
}

type taskExposureHealthyRuntime struct{}

func (taskExposureHealthyRuntime) RuntimeHealth(context.Context) tournamentpreflight.RuntimeHealth {
	now := time.Now().UTC()
	return tournamentpreflight.RuntimeHealth{
		TaskDelivery: tournamentpreflight.ComponentHealth{Healthy: true, Revision: "task_delivery:ready"},
		Realtime:     tournamentpreflight.ComponentHealth{Healthy: true, Revision: "realtime:ready"},
		Clock:        tournamentpreflight.ClockHealth{ObservedAt: now, ReferenceAt: now, MaxSkew: time.Second},
		ClockSampled: true,
		Dependencies: []tournamentpreflight.DependencyHealth{
			{Name: tournamentpreflight.DependencyRedis, Healthy: true, Revision: "redis:ready"},
			{Name: tournamentpreflight.DependencyObjectStorage, Healthy: true, Revision: "object_storage:ready"},
		},
	}
}

func loadTaskExposureReceiptSnapshot(
	ctx context.Context,
	t *testing.T,
	rosterID uuid.UUID,
	participantID uuid.UUID,
) taskExposureReceiptSnapshot {
	t.Helper()
	var snapshot taskExposureReceiptSnapshot
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id, assignment_id, attempt_id, roster_id, participant_id, instance_id,
			snapshot_id, task_id, task_version, delivered_at, created_at
		FROM task_delivery_receipts
		WHERE roster_id = $1 AND participant_id = $2
		ORDER BY id
		LIMIT 1`, rosterID, participantID).Scan(
		&snapshot.id, &snapshot.assignmentID, &snapshot.attemptID, &snapshot.rosterID,
		&snapshot.participantID, &snapshot.instanceID, &snapshot.snapshotID,
		&snapshot.taskID, &snapshot.taskVersion, &snapshot.deliveredAt, &snapshot.createdAt,
	))
	return snapshot
}

func loadTaskExposureSnapshot(ctx context.Context, t *testing.T, snapshotID uuid.UUID) taskExposureSnapshot {
	t.Helper()
	var snapshot taskExposureSnapshot
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id, reservation_id, task_id, task_version, kind, title, description,
			category, difficulty, time_limit, flag, hints, content_digest, created_at
		FROM task_snapshots
		WHERE id = $1`, snapshotID).Scan(
		&snapshot.id, &snapshot.reservationID, &snapshot.taskID, &snapshot.taskVersion,
		&snapshot.kind, &snapshot.title, &snapshot.description, &snapshot.category,
		&snapshot.difficulty, &snapshot.timeLimit, &snapshot.flag, &snapshot.hints,
		&snapshot.contentDigest, &snapshot.createdAt,
	))
	return snapshot
}

func countTaskExposureReceipts(ctx context.Context, t *testing.T, rosterID uuid.UUID) int {
	t.Helper()
	var count int
	require.NoError(t, sharedPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM task_delivery_receipts WHERE roster_id = $1`, rosterID).Scan(&count))
	return count
}

func countTaskExposurePoolMembers(ctx context.Context, t *testing.T, publicationRevision int64) int {
	t.Helper()
	var count int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM task_pool_version_memberships AS membership
		JOIN task_pool_revisions AS pool ON pool.id = membership.task_pool_revision_id
		JOIN task_pool_publications AS publication ON publication.id = pool.publication_id
		WHERE publication.revision = $1`, publicationRevision).Scan(&count))
	return count
}

func loadTaskExposureTarget(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	kind string,
) (uuid.UUID, int32) {
	t.Helper()
	var taskID uuid.UUID
	var taskVersion int32
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT membership.task_id, membership.task_version
		FROM tournament_content_configurations AS configuration
		JOIN task_pool_revisions AS pool
			ON pool.id = CASE WHEN $2 = 'golden' THEN configuration.golden_pool_revision_id
				ELSE configuration.normal_pool_revision_id END
		JOIN task_pool_version_memberships AS membership
			ON membership.task_pool_revision_id = pool.id
		WHERE configuration.tournament_id = $1
		ORDER BY membership.task_id, membership.task_version
		LIMIT 1`, tournamentID, kind).Scan(&taskID, &taskVersion))
	return taskID, taskVersion
}

func loadTaskExposurePoolRevisions(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var normalPoolRevisionID, goldenPoolRevisionID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT normal_pool_revision_id, golden_pool_revision_id
		FROM tournament_content_configurations
		WHERE tournament_id = $1 AND state = 'published'
		ORDER BY revision DESC
		LIMIT 1`, tournamentID).Scan(&normalPoolRevisionID, &goldenPoolRevisionID))
	return normalPoolRevisionID, goldenPoolRevisionID
}

func requireTaskExposureHealth(
	t *testing.T,
	rows []sqlc.ListTaskPoolVersionHealthRow,
	poolRevisionID uuid.UUID,
	taskID uuid.UUID,
	taskVersion int32,
	exposed bool,
) {
	t.Helper()
	matches := 0
	for _, row := range rows {
		if row.PoolRevisionID != poolRevisionID || row.TaskID != taskID || row.TaskVersion != taskVersion {
			continue
		}
		matches++
		require.Equal(t, exposed, row.TaskPubliclyExposed,
			"health row must bind exposure to the exact task version")
	}
	require.Equal(t, 1, matches,
		"health must contain one exact row for pool=%s task=%s version=%d",
		poolRevisionID, taskID, taskVersion)
}

func createTaskExposureAdjacentVersion(
	ctx context.Context,
	t *testing.T,
	taskID uuid.UUID,
) int32 {
	t.Helper()
	task, err := sqlc.New(sharedPool).GetTaskByID(ctx, taskID)
	require.NoError(t, err)
	hints := make([]string, 0, 3)
	for _, hint := range []*string{task.Hint1, task.Hint2, task.Hint3} {
		if hint != nil {
			hints = append(hints, *hint)
		}
	}
	updated, err := newTaskRepo().Update(ctx, taskID, taskusecase.UpdateInput{
		Title:         task.Title + " v2",
		Description:   task.Description + " v2",
		Category:      domain.Category(task.Category),
		Difficulty:    domain.Difficulty(task.Difficulty),
		TimeLimit:     int(task.TimeLimit),
		Flag:          task.Flag + "-v2",
		Kind:          domain.TaskKind(task.Kind),
		Enabled:       task.Enabled,
		Hints:         hints,
		TaskURL:       task.TaskUrl,
		SourceFileURL: task.SourceFileUrl,
	})
	require.NoError(t, err)
	require.Equal(t, int(task.CurrentVersion)+1, updated.CurrentVersion)
	return int32(updated.CurrentVersion)
}

func recordTaskExposure(
	ctx context.Context,
	t *testing.T,
	taskID uuid.UUID,
	taskVersion int32,
	audience string,
) {
	t.Helper()
	at := time.Now().UTC().Truncate(time.Microsecond)
	_, err := sqlc.New(sharedPool).RecordTaskPublicExposure(ctx, sqlc.RecordTaskPublicExposureParams{
		ID:          uuid.New(),
		TaskID:      taskID,
		TaskVersion: taskVersion,
		Audience:    audience,
		EvidenceID:  uuid.New(),
		DisclosedAt: pgtype.Timestamptz{Time: at, Valid: true},
	})
	require.NoError(t, err)
}

func insertTaskExposureReservationInTournament(
	ctx context.Context,
	t *testing.T,
	fixture taskExposureRosterFixture,
	taskID uuid.UUID,
	taskVersion int32,
) {
	t.Helper()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	var poolRevisionID uuid.UUID
	var rosterRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT configuration.normal_pool_revision_id
		FROM tournament_content_configurations AS configuration
		WHERE configuration.tournament_id = $1`, fixture.tournamentID).Scan(&poolRevisionID))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision
		FROM rosters
		WHERE id = $1`, fixture.rosterID).Scan(&rosterRevision))
	participantIDs := loadTaskExposureParticipants(ctx, t, fixture.rosterID)
	seriesID := createMigrationSeries(ctx, t, fixture.tournamentID, fixture.rosterID, participantIDs, "bo1")
	draft := createRoundProofDraft(ctx, t, fixture.tournamentID, fixture.rosterID, poolRevisionID, postgres.WaveSeriesInput{
		ID:                     seriesID,
		FirstParticipantID:     participantIDs[0],
		SecondParticipantID:    participantIDs[1],
		Format:                 domain.SeriesFormatBO1,
		InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()),
	}, createdAt)
	planID := createRoundProofAssignmentPlan(ctx, t, fixture.tournamentID, fixture.rosterID, poolRevisionID, draft, createdAt)
	branchID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO assignment_branches (
			id, plan_id, draft_id, draft_revision_id,
			branch_key, category_sequence, state, created_at
		)
		VALUES ($1, $2, $3, $4, 'cross-tournament', '["web"]'::jsonb, 'reserved', $5)`,
		branchID, planID, draft.draftID, draft.initialRevisionID, createdAt)
	require.NoError(t, err)
	rows, err := sharedPool.Query(ctx, `
		SELECT membership.task_id, membership.task_version
		FROM task_pool_version_memberships AS membership
		JOIN task_versions AS version
			ON version.task_id = membership.task_id
			AND version.version = membership.task_version
		WHERE membership.task_pool_revision_id = $1
			AND version.category = 'web'
			AND (
				(membership.task_id = $2 AND membership.task_version = $3)
				OR NOT EXISTS (
					SELECT 1
					FROM task_version_reservations AS reservation
					WHERE reservation.tournament_id = $4
						AND reservation.task_id = membership.task_id
						AND reservation.task_version = membership.task_version
						AND reservation.state IN ('reserved', 'committed')
				)
			)
		ORDER BY CASE WHEN membership.task_id = $2 AND membership.task_version = $3 THEN 0 ELSE 1 END,
			membership.task_id, membership.task_version
		LIMIT 3`, poolRevisionID, taskID, taskVersion, fixture.tournamentID)
	require.NoError(t, err)
	defer rows.Close()

	reservationIDs := make([]uuid.UUID, 0, domain.AssignmentReserveCount+1)
	for position := 1; rows.Next(); position++ {
		var selectedTaskID uuid.UUID
		var selectedTaskVersion int32
		require.NoError(t, rows.Scan(&selectedTaskID, &selectedTaskVersion))
		edgeID := uuid.New()
		reservationID := uuid.New()
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO assignment_plan_edges (
				id, plan_id, branch_id, position, task_id, task_version,
				selection_evidence, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, '{"eligible":true}'::jsonb, $7)`,
			edgeID, planID, branchID, position, selectedTaskID, selectedTaskVersion, createdAt)
		require.NoError(t, err)
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO task_version_reservations (
				id, edge_id, plan_id, branch_id, task_id, task_version, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			reservationID, edgeID, planID, branchID,
			selectedTaskID, selectedTaskVersion, createdAt)
		require.NoError(t, err)
		digest := sha256.Sum256([]byte(selectedTaskID.String()))
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO task_snapshots (
				id, reservation_id, task_id, task_version, kind,
				title, description, category, difficulty, time_limit, flag,
				hints, content_digest, created_at
			)
			VALUES (
			$1, $2, $3, $4, 'normal',
			'cross tournament task', 'cross tournament task description', 'web', 'easy', 180,
			'FLAG{cross-tournament}', '[]'::jsonb, $5, $6
			)`, uuid.New(), reservationID, selectedTaskID, selectedTaskVersion, digest[:], createdAt)
		require.NoError(t, err)
		reservationIDs = append(reservationIDs, reservationID)
	}
	require.NoError(t, rows.Err())
	require.Len(t, reservationIDs, domain.AssignmentReserveCount+1,
		"the cross-tournament exact branch must retain one primary and two reserves")
	committedAt := createdAt.Add(time.Microsecond)
	for _, id := range reservationIDs {
		_, err = sharedPool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'committed', revision = revision + 1, committed_at = $2
			WHERE id = $1`, id, committedAt)
		require.NoError(t, err)
	}
	activatedAt := committedAt.Add(time.Microsecond)
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'active', activated_at = $2
		WHERE id = $1`, branchID, activatedAt)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_plans
		SET state = 'committed', active_branch_id = $2, committed_at = $3
		WHERE id = $1`, planID, branchID, activatedAt.Add(time.Microsecond))
	require.NoError(t, err)
}

func loadTaskExposureParticipants(ctx context.Context, t *testing.T, rosterID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := sharedPool.Query(ctx, `
		SELECT id
		FROM participants
		WHERE roster_id = $1
		ORDER BY seed, id
		LIMIT 2`, rosterID)
	require.NoError(t, err)
	defer rows.Close()

	participantIDs := make([]uuid.UUID, 0, 2)
	for rows.Next() {
		var participantID uuid.UUID
		require.NoError(t, rows.Scan(&participantID))
		participantIDs = append(participantIDs, participantID)
	}
	require.NoError(t, rows.Err())
	require.Len(t, participantIDs, 2)
	return participantIDs
}

func requirePreflightCheckFailed(
	t *testing.T,
	report tournamentpreflight.ReportRevision,
	code tournamentpreflight.Code,
) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Code == code {
			require.False(t, check.Passed, "preflight check %s unexpectedly passed: %v", code, check.Evidence)
			return
		}
	}
	require.Fail(t, "preflight check missing", "code: %s", code)
}
