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
	createGoldenRuntimeTestPlan(
		ctx, t, fixture.tournamentID, fixture.rosterID,
		sourceProjectionID, sourceProjectionRevision, now.Add(-time.Second),
	)

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

	recoveryRepository := postgres.NewExecutionRecoveryPostgres(
		postgres.NewTxManager(sharedPool),
		postgres.NewRecoveryPostgres(postgres.NewTxManager(sharedPool), nil),
		postgres.NewRecoveryTerminalPostgres(postgres.NewTxManager(sharedPool), nil, goldenRuntimeClock{now: now}),
	)
	recoveryTournaments, err := recoveryRepository.ListRecoveryTournaments(ctx)
	require.NoError(t, err)
	require.Contains(t, recoveryTournaments, fixture.tournamentID)

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

func TestGoldenRuntimeDeadlineReserveAndConnectionStateMachine(t *testing.T) {
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

	withoutPlan := goldenusecase.NewRuntimeApplication(
		postgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: now},
	)
	_, err := withoutPlan.Open(ctx, usecase.GoldenOpenCommand{
		TournamentID: fixture.tournamentID, CommandID: uuid.New(),
		ExpectedProjectionRevision: sourceProjectionRevision,
	})
	require.Error(t, err)
	var attemptCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM golden_runtime_assignments WHERE tournament_id = $1`, fixture.tournamentID).Scan(&attemptCount))
	require.Zero(t, attemptCount)

	createGoldenRuntimeTestPlan(
		ctx, t, fixture.tournamentID, fixture.rosterID,
		sourceProjectionID, sourceProjectionRevision, now.Add(-time.Second),
	)
	application := goldenusecase.NewRuntimeApplication(
		postgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: now},
	)
	operator, err := application.Open(ctx, usecase.GoldenOpenCommand{
		TournamentID: fixture.tournamentID, CommandID: uuid.New(),
		ExpectedProjectionRevision: sourceProjectionRevision,
	})
	require.NoError(t, err)
	require.NotEmpty(t, operator.Groups)
	primary := operator.Groups[0]
	players := goldenRuntimePlayers(ctx, t, primary)
	for _, member := range primary.Members {
		_, err = application.SetReady(ctx, usecase.GoldenReadyCommand{
			TournamentID: fixture.tournamentID, PlayerID: players[member.ParticipantID],
			CommandID: uuid.New(), Ready: true,
		})
		require.NoError(t, err)
	}
	_, err = application.Start(ctx, usecase.GoldenStartCommand{
		TournamentID: fixture.tournamentID, AttemptID: primary.AttemptID, CommandID: uuid.New(),
	})
	require.NoError(t, err)

	first := primary.Members[0]
	require.NoError(t, application.SetConnected(ctx, usecase.GoldenConnectionCommand{
		TournamentID: fixture.tournamentID, PlayerID: players[first.ParticipantID],
		CommandID: uuid.New(), Connected: false,
	}))
	flag := goldenRuntimeAttemptFlag(ctx, t, primary.AttemptID)
	_, err = application.Submit(ctx, usecase.GoldenSubmissionCommand{
		TournamentID: fixture.tournamentID, PlayerID: players[first.ParticipantID],
		CommandID: uuid.New(), SubmittedFlag: flag,
	})
	require.ErrorIs(t, err, domain.ErrConflict)
	require.NoError(t, application.SetConnected(ctx, usecase.GoldenConnectionCommand{
		TournamentID: fixture.tournamentID, PlayerID: players[first.ParticipantID],
		CommandID: uuid.New(), Connected: true,
	}))
	_, err = application.Submit(ctx, usecase.GoldenSubmissionCommand{
		TournamentID: fixture.tournamentID, PlayerID: players[first.ParticipantID],
		CommandID: uuid.New(), SubmittedFlag: flag,
	})
	require.NoError(t, err)

	deadline := now.Add(goldenRuntimeDurationForTest + time.Second)
	recovered := goldenusecase.NewRuntimeApplication(
		postgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: deadline},
	)
	require.NoError(t, recovered.Recover(ctx, fixture.tournamentID))
	view, err := recovered.OperatorView(ctx, usecase.GoldenOperatorQuery{
		TournamentID: fixture.tournamentID, OperatorID: uuid.New(),
	})
	require.NoError(t, err)
	reserve := goldenRuntimeGroupView(t, view, primary.GroupRevisionID)
	require.Equal(t, "prepared", reserve.State)
	require.NotEqual(t, primary.AttemptID, reserve.AttemptID)
	require.Len(t, reserve.Members, len(primary.Members)-1)
	var reservePosition int16
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT edge_position FROM golden_runtime_assignments WHERE attempt_id = $1`,
		reserve.AttemptID,
	).Scan(&reservePosition))
	require.EqualValues(t, 2, reservePosition)

	for _, member := range reserve.Members {
		_, err = recovered.SetReady(ctx, usecase.GoldenReadyCommand{
			TournamentID: fixture.tournamentID, PlayerID: players[member.ParticipantID],
			CommandID: uuid.New(), Ready: true,
		})
		require.NoError(t, err)
	}
	_, err = recovered.Start(ctx, usecase.GoldenStartCommand{
		TournamentID: fixture.tournamentID, AttemptID: reserve.AttemptID, CommandID: uuid.New(),
	})
	require.NoError(t, err)
	reserveFlag := goldenRuntimeAttemptFlag(ctx, t, reserve.AttemptID)
	for _, member := range reserve.Members {
		_, err = recovered.Submit(ctx, usecase.GoldenSubmissionCommand{
			TournamentID: fixture.tournamentID, PlayerID: players[member.ParticipantID],
			CommandID: uuid.New(), SubmittedFlag: reserveFlag,
		})
		require.NoError(t, err)
	}
	var ledgerRevisionCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM golden_position_ledger_revisions WHERE tournament_id = $1`,
		fixture.tournamentID,
	).Scan(&ledgerRevisionCount))
	require.NoError(t, recovered.Recover(ctx, fixture.tournamentID))
	var replayedLedgerRevisionCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM golden_position_ledger_revisions WHERE tournament_id = $1`,
		fixture.tournamentID,
	).Scan(&replayedLedgerRevisionCount))
	require.Equal(t, ledgerRevisionCount, replayedLedgerRevisionCount)

	command := tournamentprogression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: sourceProjectionRevision,
		Action: tournamentprogression.ActionStartPlayoffs,
	}
	playoffs, err := publishSwissPlayoffsWithClock(
		ctx, fixture, command, playoffPublicationClock{now: deadline.Add(time.Second)},
	)
	require.NoError(t, err)
	require.Equal(t, domain.TournamentStatePlayoffs, playoffs.State)
}

func TestGoldenRuntimeCommonFailureRequiresOperatorReserve(t *testing.T) {
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
	createGoldenRuntimeTestPlan(ctx, t, fixture.tournamentID, fixture.rosterID,
		sourceProjectionID, sourceProjectionRevision, now.Add(-time.Second))
	application := goldenusecase.NewRuntimeApplication(
		postgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)), goldenRuntimeClock{now: now},
	)
	operator, err := application.Open(ctx, usecase.GoldenOpenCommand{
		TournamentID: fixture.tournamentID, CommandID: uuid.New(), ExpectedProjectionRevision: sourceProjectionRevision,
	})
	require.NoError(t, err)
	primary := operator.Groups[0]
	players := goldenRuntimePlayers(ctx, t, primary)
	for _, member := range primary.Members {
		_, err = application.SetReady(ctx, usecase.GoldenReadyCommand{
			TournamentID: fixture.tournamentID, PlayerID: players[member.ParticipantID], CommandID: uuid.New(), Ready: true,
		})
		require.NoError(t, err)
	}
	_, err = application.Start(ctx, usecase.GoldenStartCommand{
		TournamentID: fixture.tournamentID, AttemptID: primary.AttemptID, CommandID: uuid.New(),
	})
	require.NoError(t, err)

	afterDeadline := now.Add(goldenRuntimeDurationForTest + time.Second)
	recovery := goldenusecase.NewRuntimeApplication(
		postgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)), goldenRuntimeClock{now: afterDeadline},
	)
	require.NoError(t, recovery.Recover(ctx, fixture.tournamentID))
	view, err := recovery.OperatorView(ctx, usecase.GoldenOperatorQuery{TournamentID: fixture.tournamentID, OperatorID: uuid.New()})
	require.NoError(t, err)
	paused := goldenRuntimeGroupView(t, view, primary.GroupRevisionID)
	require.Equal(t, "technical_pause", paused.State)
	require.Equal(t, primary.AttemptID, paused.AttemptID)
	var failureSubmissionCount, recoveryRevisionCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM golden_provisional_submissions WHERE attempt_id = $1`,
		primary.AttemptID,
	).Scan(&failureSubmissionCount))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM golden_recovery_revisions WHERE attempt_id = $1`,
		primary.AttemptID,
	).Scan(&recoveryRevisionCount))
	require.NoError(t, recovery.Recover(ctx, fixture.tournamentID))
	var replayedFailureSubmissionCount, replayedRecoveryRevisionCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM golden_provisional_submissions WHERE attempt_id = $1`,
		primary.AttemptID,
	).Scan(&replayedFailureSubmissionCount))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM golden_recovery_revisions WHERE attempt_id = $1`,
		primary.AttemptID,
	).Scan(&replayedRecoveryRevisionCount))
	require.Equal(t, failureSubmissionCount, replayedFailureSubmissionCount)
	require.Equal(t, recoveryRevisionCount, replayedRecoveryRevisionCount)

	operatorResume := goldenusecase.NewRuntimeApplication(
		postgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: afterDeadline.Add(time.Second)},
	)
	_, err = operatorResume.Start(ctx, usecase.GoldenStartCommand{
		TournamentID: fixture.tournamentID, AttemptID: primary.AttemptID, CommandID: uuid.New(),
	})
	require.NoError(t, err)
	view, err = operatorResume.OperatorView(ctx, usecase.GoldenOperatorQuery{
		TournamentID: fixture.tournamentID, OperatorID: uuid.New(),
	})
	require.NoError(t, err)
	replacement := goldenRuntimeGroupView(t, view, primary.GroupRevisionID)
	require.Equal(t, "prepared", replacement.State)
	require.NotEqual(t, primary.AttemptID, replacement.AttemptID)
	var edgePosition int16
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT edge_position FROM golden_runtime_assignments WHERE attempt_id = $1`,
		replacement.AttemptID,
	).Scan(&edgePosition))
	require.EqualValues(t, 2, edgePosition)
}

const goldenRuntimeDurationForTest = 180 * time.Second

func goldenRuntimePlayers(
	ctx context.Context,
	t testing.TB,
	group usecase.GoldenOperatorGroupView,
) map[uuid.UUID]uuid.UUID {
	t.Helper()
	players := make(map[uuid.UUID]uuid.UUID, len(group.Members))
	for _, member := range group.Members {
		var playerID uuid.UUID
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT player_id FROM participants WHERE id = $1`, member.ParticipantID).Scan(&playerID))
		players[member.ParticipantID] = playerID
	}
	return players
}

func goldenRuntimeAttemptFlag(ctx context.Context, t testing.TB, attemptID uuid.UUID) string {
	t.Helper()
	var flag string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT version.flag
		FROM golden_runtime_assignments AS runtime
		INNER JOIN task_versions AS version
			ON version.task_id = runtime.task_id AND version.version = runtime.task_version
		WHERE runtime.attempt_id = $1`, attemptID).Scan(&flag))
	return flag
}

func goldenRuntimeGroupView(
	t testing.TB,
	view usecase.GoldenOperatorView,
	groupRevisionID uuid.UUID,
) usecase.GoldenOperatorGroupView {
	t.Helper()
	for _, group := range view.Groups {
		if group.GroupRevisionID == groupRevisionID {
			return group
		}
	}
	require.FailNow(t, "Golden group not found", groupRevisionID.String())
	return usecase.GoldenOperatorGroupView{}
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
		SELECT COUNT(*)
		FROM task_pool_version_memberships AS membership
		INNER JOIN task_versions AS version
			ON version.task_id = membership.task_id AND version.version = membership.task_version
		INNER JOIN tasks AS task ON task.id = version.task_id
		LEFT JOIN LATERAL (
			SELECT attestation.healthy
			FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = version.task_id
				AND attestation.task_version = version.version
			ORDER BY attestation.revision DESC
			LIMIT 1
		) AS health ON true
		WHERE membership.task_pool_revision_id = $1
			AND task.kind = 'golden'
			AND task.enabled
			AND task.deleted_at IS NULL
			AND version.time_limit = 180
			AND health.healthy`, poolID).Scan(&taskCount))
	requiredTaskCount := groupCount * (domain.AssignmentReserveCount + 1)
	for taskCount < requiredTaskCount {
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
