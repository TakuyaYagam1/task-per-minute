//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func TestTournamentPreflightCertifiesCapacityAndLocksRoster(t *testing.T) {
	ctx := context.Background()
	TruncateTables(t, sharedPool)
	t.Cleanup(func() { TruncateTables(t, sharedPool) })

	contentRevision := preparePreflightCapacityContent(ctx, t, 4)
	createdAt := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	tournamentID := uuid.New()
	rosterID := uuid.New()
	tx := postgres.NewTxManager(sharedPool)
	tournaments := postgres.NewTournamentPostgres(tx)
	_, _, err := tournaments.Create(ctx, postgres.TournamentCreateInput{
		ID: tournamentID, RosterID: rosterID, Name: "Preflight Tournament",
		PublicID: tournamentID.String(), PlannedRosterSize: 4,
		ContentRevision: contentRevision, CreatedAt: createdAt,
	})
	require.NoError(t, err)
	projectionRevision := publishInitialPreflightProjection(ctx, t, tournamentID, rosterID, createdAt)
	_, changed, err := tournaments.Transition(ctx, postgres.TournamentTransitionInput{
		ID: tournamentID, ExpectedRevision: 1, ExpectedState: domain.TournamentStateDraft,
		NextState: domain.TournamentStateRegistration, UpdatedAt: createdAt.Add(time.Millisecond),
	})
	require.NoError(t, err)
	require.True(t, changed)

	playerIDs := createMigrationPlayers(ctx, t, 4)
	createSwissMigrationParticipants(ctx, t, rosterID, playerIDs)
	repository := rosterrepo.NewTournamentAdminRosterPostgres(tx)
	workflow := tournamentadmin.NewRosterWorkflow(tournamentadmin.RosterWorkflowDependencies{
		Transactions: tx,
		Repository:   repository,
		RuntimeHealth: tournamentadmin.PreflightRuntimeHealthSourceFunc(func(context.Context) tournamentpreflight.RuntimeHealth {
			observedAt := time.Now().UTC()
			return tournamentpreflight.RuntimeHealth{
				TaskDelivery: tournamentpreflight.ComponentHealth{Healthy: true, Revision: "task_delivery:ready"},
				Realtime:     tournamentpreflight.ComponentHealth{Healthy: true, Revision: "realtime:ready"},
				Clock: tournamentpreflight.ClockHealth{
					ObservedAt: observedAt, ReferenceAt: observedAt, MaxSkew: time.Second,
				},
				ClockSampled: true,
				Dependencies: []tournamentpreflight.DependencyHealth{
					{Name: tournamentpreflight.DependencyRedis, Healthy: true, Revision: "redis:ready"},
					{Name: tournamentpreflight.DependencyObjectStorage, Healthy: true, Revision: "object_storage:ready"},
				},
			}
		}),
	})
	preflightID := uuid.New()
	report, err := workflow.RunPreflight(ctx, tournamentadmin.PreflightCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: tournamentID, CommandID: preflightID,
		},
		ExpectedProjectionRevision: projectionRevision,
	})
	require.NoError(t, err)
	require.True(t, report.Passed(), "preflight checks: %+v", report.Checks)
	requirePreflightCheckPassed(t, report, tournamentpreflight.CodeRuntimeCapacity)
	requirePreflightCheckPassed(t, report, tournamentpreflight.CodeRuntimeSchedule)
	require.Contains(t, preflightCheckEvidence(report, tournamentpreflight.CodeRuntimeConfiguration),
		fmt.Sprintf("content_revision:%d", contentRevision))

	locked, err := workflow.LockRoster(ctx, tournamentadmin.LockRosterCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: tournamentID, CommandID: uuid.New(),
		},
		ExpectedProjectionRevision: projectionRevision,
		PreflightRevisionID:        preflightID,
		CheckedInPlayerIDs:         playerIDs,
	})
	require.NoError(t, err)
	require.True(t, locked.Locked)
}

func preparePreflightCapacityContent(ctx context.Context, t *testing.T, rosterSize int) int64 {
	t.Helper()

	rounds, err := domain.TournamentPresetV1.SwissRounds(rosterSize)
	require.NoError(t, err)
	chainSize := domain.AssignmentReserveCount + 1
	sharedCount := (rosterSize/2*rounds + 3) * chainSize
	for _, fixture := range []struct {
		kind     string
		category string
		count    int
	}{
		{kind: "normal", category: "web", count: sharedCount},
		{kind: "normal", category: "crypto", count: sharedCount},
		{kind: "normal", category: "reverse", count: sharedCount},
		{kind: "normal", category: "pwn", count: chainSize},
		{kind: "normal", category: "forensics", count: chainSize},
		{kind: "golden", category: "misc", count: rosterSize / 2 * chainSize},
	} {
		for index := range fixture.count {
			_, err = sharedPool.Exec(ctx, `
				INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'preflight capacity fixture', $2, 'easy', $3, $4, $5)`,
				fmt.Sprintf("preflight_%s_%s_%d", fixture.kind, fixture.category, index),
				fixture.category, int(domain.TournamentTaskDuration/time.Second),
				fmt.Sprintf("preflight-%s-%s-%d", fixture.kind, fixture.category, index), fixture.kind,
			)
			require.NoError(t, err)
		}
	}
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT version.task_id, version.version, 1, true, 'content_validation'
		FROM task_versions AS version`)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `SELECT publish_task_pool_heads()`)
	require.NoError(t, err)
	return currentTaskPoolPublicationRevision(ctx, t)
}

func publishInitialPreflightProjection(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) int64 {
	t.Helper()

	cutoffID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO projection_cutoffs (
			id, tournament_id, roster_id, sequence_number, source_kind, reason, cutoff_at, created_at
		)
		VALUES ($1, $2, $3, 1, 'initial', 'initial roster projection', $4, $4)`,
		cutoffID, tournamentID, rosterID, createdAt)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO projection_revisions (
			id, tournament_id, roster_id, revision_number, cutoff_id, state, published_at, created_at
		)
		VALUES ($1, $2, $3, 1, $4, 'published', $5, $5)`,
		uuid.New(), tournamentID, rosterID, cutoffID, createdAt)
	require.NoError(t, err)
	return 1
}

func requirePreflightCheckPassed(
	t *testing.T,
	report tournamentpreflight.ReportRevision,
	code tournamentpreflight.Code,
) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Code == code {
			require.True(t, check.Passed, "preflight check %s: %v", code, check.Evidence)
			return
		}
	}
	require.Fail(t, "preflight check missing", "code: %s", code)
}

func preflightCheckEvidence(report tournamentpreflight.ReportRevision, code tournamentpreflight.Code) []string {
	for _, check := range report.Checks {
		if check.Code == code {
			return check.Evidence
		}
	}
	return nil
}
