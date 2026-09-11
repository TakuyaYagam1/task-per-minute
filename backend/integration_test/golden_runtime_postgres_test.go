//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func TestGoldenRuntimeSurvivesRestartAndProducesPlayoffEvidence(t *testing.T) {
	ctx := context.Background()
	fixture := prepareNativeGoldenFinalSwiss(ctx, t)
	sourceProjectionID, sourceProjectionRevision := currentPublishedProjection(
		ctx, t, fixture.tournamentID, fixture.rosterID,
	)
	require.NoError(t, publishSwissGolden(ctx, fixture, tournamentprogression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: sourceProjectionRevision,
		Action: tournamentprogression.ActionStartGolden,
	}))
	now := time.Now().UTC().Truncate(time.Microsecond)
	ensureGoldenRuntimeTestCapacity(ctx, t, fixture.tournamentID)

	application := goldenusecase.NewRuntimeApplication(
		postgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: now},
	)
	operator, err := application.Open(ctx, usecase.GoldenOpenCommand{
		TournamentID:               fixture.tournamentID,
		CommandID:                  uuid.New(),
		ExpectedProjectionRevision: sourceProjectionRevision,
	})
	require.NoError(t, err)
	require.NotEmpty(t, operator.Groups)
	playersByParticipant := make(map[uuid.UUID]uuid.UUID)
	for _, group := range operator.Groups {
		require.Equal(t, "prepared", group.State)
		for _, member := range group.Members {
			var playerID uuid.UUID
			require.NoError(t, sharedPool.QueryRow(ctx, `
				SELECT player_id FROM participants WHERE id = $1`, member.ParticipantID).Scan(&playerID))
			playersByParticipant[member.ParticipantID] = playerID
			participant, readyErr := application.SetReady(ctx, usecase.GoldenReadyCommand{
				TournamentID: fixture.tournamentID, PlayerID: playerID,
				CommandID: uuid.New(), Ready: true,
			})
			require.NoError(t, readyErr)
			if participant.State == "ready" {
				require.NotNil(t, participant.Task)
				require.Equal(t, 180, participant.Task.TimeLimitSeconds)
			}
		}
	}
	startedAt := now
	application = goldenusecase.NewRuntimeApplication(
		postgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: startedAt},
	)
	for _, group := range operator.Groups {
		_, err = application.Start(ctx, usecase.GoldenStartCommand{
			TournamentID: fixture.tournamentID, AttemptID: group.AttemptID, CommandID: uuid.New(),
		})
		require.NoError(t, err)
	}

	// A fresh repository instance represents process recovery. It reconstructs
	// the active attempt from PostgreSQL and records durable recovery evidence.
	restarted := goldenusecase.NewRuntimeApplication(
		postgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: startedAt},
	)
	require.NoError(t, restarted.Recover(ctx, fixture.tournamentID))
	var recoveryCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM golden_recovery_revisions WHERE tournament_id = $1`, fixture.tournamentID).Scan(&recoveryCount))
	require.Equal(t, len(operator.Groups), recoveryCount)

	for groupIndex, group := range operator.Groups {
		var flag string
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT version.flag
			FROM golden_runtime_assignments AS runtime
			INNER JOIN task_versions AS version
				ON version.task_id = runtime.task_id AND version.version = runtime.task_version
			WHERE runtime.attempt_id = $1`, group.AttemptID).Scan(&flag))
		for memberIndex, member := range group.Members {
			commandID := uuid.New()
			finalView, submitErr := restarted.Submit(ctx, usecase.GoldenSubmissionCommand{
				TournamentID: fixture.tournamentID, PlayerID: playersByParticipant[member.ParticipantID],
				CommandID: commandID, SubmittedFlag: flag,
			})
			require.NoError(t, submitErr)
			if groupIndex == 0 && memberIndex == 0 && len(group.Members) > 1 {
				_, replayErr := restarted.Submit(ctx, usecase.GoldenSubmissionCommand{
					TournamentID: fixture.tournamentID,
					PlayerID:     playersByParticipant[group.Members[1].ParticipantID],
					CommandID:    commandID, SubmittedFlag: flag,
				})
				require.True(t, errors.Is(replayErr, domain.ErrConflict))
			}
			if memberIndex == len(group.Members)-1 {
				require.Equal(t, "completed", finalView.State)
			}
		}
	}

	playoffs, err := publishSwissPlayoffs(ctx, fixture, tournamentprogression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: sourceProjectionRevision,
		Action: tournamentprogression.ActionStartPlayoffs,
	})
	require.NoError(t, err)
	require.Equal(t, domain.TournamentStatePlayoffs, playoffs.State)
	require.NotEqual(t, sourceProjectionID, uuid.Nil)
}

func ensureGoldenRuntimeTestCapacity(ctx context.Context, t *testing.T, tournamentID uuid.UUID) {
	t.Helper()
	var poolID uuid.UUID
	var groupCount, taskCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT golden_pool_revision_id
		FROM tournament_content_configurations
		WHERE tournament_id = $1 AND state = 'published'`, tournamentID).Scan(&poolID))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM golden_group_revisions WHERE tournament_id = $1`, tournamentID).Scan(&groupCount))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM task_pool_version_memberships WHERE task_pool_revision_id = $1`, poolID).Scan(&taskCount))
	for taskCount < groupCount {
		taskID := uuid.New()
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO tasks (id, title, description, category, difficulty, time_limit, flag, kind)
			VALUES ($1, $2, 'Golden runtime integration task', 'web', 'easy', 180, $3, 'golden')`,
			taskID, "golden_runtime_"+uuid.NewString()[:8], "golden-runtime-"+uuid.NewString()[:8])
		require.NoError(t, err)
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
			VALUES ($1, 1, 1, true, 'content_validation')`, taskID)
		require.NoError(t, err)
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO task_pool_version_memberships (task_pool_revision_id, task_id, task_version)
			VALUES ($1, $2, 1)`, poolID, taskID)
		require.NoError(t, err)
		taskCount++
	}
}

type goldenRuntimeClock struct{ now time.Time }

func (clock goldenRuntimeClock) Now() time.Time { return clock.now }
