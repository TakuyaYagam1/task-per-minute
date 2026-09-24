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
	executionrecoveryrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/recovery"
	runtimepostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/golden/runtime"
	recoveryrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery"
	recoveryterminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	goldenruntime "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/runtime"
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

	application := goldenruntime.NewRuntimeApplication(
		runtimepostgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: now},
	)
	operator, err := application.Open(ctx, usecase.GoldenOpenCommand{
		TournamentID:               fixture.tournamentID,
		CommandID:                  uuid.New(),
		ExpectedProjectionRevision: sourceProjectionRevision,
		GoldenMutationScope:        usecase.GoldenMutationScope{ActorID: uuid.New()},
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
			participant, readyErr := application.SetReady(ctx, goldenRuntimeReadyCommand(ctx, t, application, fixture.tournamentID, playerID, uuid.New()))
			require.NoError(t, readyErr)
			if participant.State == "ready" {
				require.Nil(t, participant.Task, "ready snapshots must not disclose the task before start commits")
			}
		}
	}
	startedAt := now
	application = goldenruntime.NewRuntimeApplication(
		runtimepostgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: startedAt},
	)
	for _, group := range operator.Groups {
		_, err = application.Start(ctx, goldenRuntimeStartCommand(ctx, t, application, fixture.tournamentID, group.AttemptID, uuid.New()))
		require.NoError(t, err)
		for _, member := range group.Members {
			participant, viewErr := application.ParticipantView(ctx, usecase.GoldenParticipantQuery{
				TournamentID: fixture.tournamentID,
				PlayerID:     playersByParticipant[member.ParticipantID],
			})
			require.NoError(t, viewErr)
			require.NotNil(t, participant.Task, "start must disclose the task after deadline commits")
			require.Equal(t, 180, participant.Task.TimeLimitSeconds)
		}
	}

	recoveryRepository := executionrecoveryrepo.NewExecutionRecoveryPostgresWithDependencies(
		postgres.NewTxManager(sharedPool),
		recoveryrepo.NewRecoveryPostgres(postgres.NewTxManager(sharedPool), nil),
		recoveryterminalrepo.NewRecoveryTerminalPostgresWithDependencies(
			postgres.NewTxManager(sharedPool), nil, goldenRuntimeClock{now: now},
			wavestartrepo.EnsurePreStartSwissRoundProofForCommand, resultauthority.FinalizeProjection,
		),
		resultauthority.FinalizeProjection,
	)
	recoveryTournaments, err := recoveryRepository.ListRecoveryTournaments(ctx)
	require.NoError(t, err)
	require.Contains(t, recoveryTournaments, fixture.tournamentID)

	// A fresh repository instance represents process recovery. It reconstructs
	// the active attempt from PostgreSQL and records durable recovery evidence.
	restarted := goldenruntime.NewRuntimeApplication(
		runtimepostgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: startedAt},
	)
	require.NoError(t, restarted.Recover(ctx, fixture.tournamentID))
	var recoveryCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM golden_recovery_revisions WHERE tournament_id = $1`, fixture.tournamentID).Scan(&recoveryCount))
	require.Zero(t, recoveryCount, "a recovery before any elapsed boundary is a no-op")

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
			submitCommand := goldenRuntimeSubmitCommand(ctx, t, restarted, fixture.tournamentID, playersByParticipant[member.ParticipantID], commandID, flag)
			finalView, submitErr := restarted.Submit(ctx, submitCommand)
			require.NoError(t, submitErr)
			replayedView, replayErr := restarted.Submit(ctx, submitCommand)
			require.NoError(t, replayErr)
			require.Equal(t, finalView, replayedView)
			if groupIndex == 0 && memberIndex == 0 && len(group.Members) > 1 {
				replayCommand := goldenRuntimeSubmitCommand(ctx, t, restarted, fixture.tournamentID, playersByParticipant[group.Members[1].ParticipantID], commandID, flag)
				_, reuseErr := restarted.Submit(ctx, replayCommand)
				require.True(t, errors.Is(reuseErr, domain.ErrConflict))
			}
			if memberIndex == len(group.Members)-1 {
				require.Equal(t, "completed", finalView.State)
			}
		}
	}
	var headRevision, commandCount, auditCount, outboxCount int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT head.revision,
			(SELECT COUNT(*) FROM golden_runtime_commands WHERE tournament_id = head.tournament_id),
			(SELECT COUNT(*) FROM audit_events WHERE tournament_id = head.tournament_id AND action LIKE 'golden.runtime.%'),
			(SELECT COUNT(*) FROM outbox_golden_runtime_sources WHERE tournament_id = head.tournament_id)
		FROM golden_runtime_heads AS head
		WHERE head.tournament_id = $1`, fixture.tournamentID,
	).Scan(&headRevision, &commandCount, &auditCount, &outboxCount))
	require.Positive(t, headRevision)
	require.Equal(t, headRevision, commandCount)
	require.Equal(t, commandCount, auditCount)
	require.Equal(t, commandCount, outboxCount)

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
	fixture := prepareGoldenThreeMemberSwiss(t)
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

	withoutPlan := goldenruntime.NewRuntimeApplication(
		runtimepostgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: now},
	)
	_, err := withoutPlan.Open(ctx, usecase.GoldenOpenCommand{
		TournamentID: fixture.tournamentID, CommandID: uuid.New(),
		ExpectedProjectionRevision: sourceProjectionRevision,
		GoldenMutationScope:        usecase.GoldenMutationScope{ActorID: uuid.New()},
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
	application := goldenruntime.NewRuntimeApplication(
		runtimepostgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: now},
	)
	operator, err := application.Open(ctx, usecase.GoldenOpenCommand{
		TournamentID: fixture.tournamentID, CommandID: uuid.New(),
		ExpectedProjectionRevision: sourceProjectionRevision,
		GoldenMutationScope:        usecase.GoldenMutationScope{ActorID: uuid.New()},
	})
	require.NoError(t, err)
	require.Len(t, operator.Groups, 1)
	primary := operator.Groups[0]
	require.Len(t, primary.Members, 3, "one solve must leave two participants for the reserve branch")
	players := goldenRuntimePlayers(ctx, t, primary)
	for _, member := range primary.Members {
		_, err = application.SetReady(ctx, goldenRuntimeReadyCommand(ctx, t, application, fixture.tournamentID, players[member.ParticipantID], uuid.New()))
		require.NoError(t, err)
	}
	_, err = application.Start(ctx, goldenRuntimeStartCommand(ctx, t, application, fixture.tournamentID, primary.AttemptID, uuid.New()))
	require.NoError(t, err)

	first := primary.Members[0]
	require.NoError(t, application.SetConnected(ctx, usecase.GoldenConnectionCommand{
		TournamentID: fixture.tournamentID, PlayerID: players[first.ParticipantID],
		CommandID: uuid.New(), Connected: false,
		GoldenMutationScope: goldenRuntimeParticipantScope(ctx, t, application, fixture.tournamentID, players[first.ParticipantID]),
	}))
	flag := goldenRuntimeAttemptFlag(ctx, t, primary.AttemptID)
	_, err = application.Submit(ctx, goldenRuntimeSubmitCommand(ctx, t, application, fixture.tournamentID, players[first.ParticipantID], uuid.New(), flag))
	require.ErrorIs(t, err, domain.ErrConflict)
	require.NoError(t, application.SetConnected(ctx, usecase.GoldenConnectionCommand{
		TournamentID: fixture.tournamentID, PlayerID: players[first.ParticipantID],
		CommandID: uuid.New(), Connected: true,
		GoldenMutationScope: goldenRuntimeParticipantScope(ctx, t, application, fixture.tournamentID, players[first.ParticipantID]),
	}))
	_, err = application.Submit(ctx, goldenRuntimeSubmitCommand(ctx, t, application, fixture.tournamentID, players[first.ParticipantID], uuid.New(), flag))
	require.NoError(t, err)

	deadline := now.Add(goldenRuntimeDurationForTest)
	recovered := goldenruntime.NewRuntimeApplication(
		runtimepostgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: deadline},
	)
	late := primary.Members[1]
	_, err = recovered.Submit(ctx, goldenRuntimeSubmitCommand(
		ctx, t, recovered, fixture.tournamentID, players[late.ParticipantID], uuid.New(), flag,
	))
	require.ErrorIs(t, err, domain.ErrConflict, "submission at the exact deadline must lose to closure")
	require.NoError(t, recovered.Recover(ctx, fixture.tournamentID))
	view, err := recovered.OperatorView(ctx, usecase.GoldenOperatorQuery{
		TournamentID: fixture.tournamentID, OperatorID: uuid.New(),
	})
	require.NoError(t, err)
	reserve := goldenRuntimeGroupView(t, view, primary.GroupRevisionID)
	require.Equal(t, "prepared", reserve.State)
	require.NotEqual(t, primary.AttemptID, reserve.AttemptID)
	require.Len(t, reserve.Members, 2)
	var reservePosition int16
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT edge_position FROM golden_runtime_assignments WHERE attempt_id = $1`,
		reserve.AttemptID,
	).Scan(&reservePosition))
	require.EqualValues(t, 2, reservePosition)

	for _, member := range reserve.Members {
		_, err = recovered.SetReady(ctx, goldenRuntimeReadyCommand(ctx, t, recovered, fixture.tournamentID, players[member.ParticipantID], uuid.New()))
		require.NoError(t, err)
	}
	_, err = recovered.Start(ctx, goldenRuntimeStartCommand(ctx, t, recovered, fixture.tournamentID, reserve.AttemptID, uuid.New()))
	require.NoError(t, err)
	reserveFlag := goldenRuntimeAttemptFlag(ctx, t, reserve.AttemptID)
	for _, member := range reserve.Members {
		_, err = recovered.Submit(ctx, goldenRuntimeSubmitCommand(ctx, t, recovered, fixture.tournamentID, players[member.ParticipantID], uuid.New(), reserveFlag))
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
	application := goldenruntime.NewRuntimeApplication(
		runtimepostgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)), goldenRuntimeClock{now: now},
	)
	operator, err := application.Open(ctx, usecase.GoldenOpenCommand{
		TournamentID: fixture.tournamentID, CommandID: uuid.New(), ExpectedProjectionRevision: sourceProjectionRevision,
		GoldenMutationScope: usecase.GoldenMutationScope{ActorID: uuid.New()},
	})
	require.NoError(t, err)
	primary := operator.Groups[0]
	players := goldenRuntimePlayers(ctx, t, primary)
	for _, member := range primary.Members {
		_, err = application.SetReady(ctx, goldenRuntimeReadyCommand(ctx, t, application, fixture.tournamentID, players[member.ParticipantID], uuid.New()))
		require.NoError(t, err)
	}
	_, err = application.Start(ctx, goldenRuntimeStartCommand(ctx, t, application, fixture.tournamentID, primary.AttemptID, uuid.New()))
	require.NoError(t, err)

	afterDeadline := now.Add(goldenRuntimeDurationForTest + time.Second)
	recovery := goldenruntime.NewRuntimeApplication(
		runtimepostgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)), goldenRuntimeClock{now: afterDeadline},
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

	operatorResume := goldenruntime.NewRuntimeApplication(
		runtimepostgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: afterDeadline.Add(time.Second)},
	)
	_, err = operatorResume.Start(ctx, goldenRuntimeStartCommand(ctx, t, operatorResume, fixture.tournamentID, primary.AttemptID, uuid.New()))
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

func goldenRuntimeParticipantScope(
	ctx context.Context,
	t testing.TB,
	application usecase.GoldenUseCase,
	tournamentID, playerID uuid.UUID,
) usecase.GoldenMutationScope {
	t.Helper()
	view, err := application.ParticipantView(ctx, usecase.GoldenParticipantQuery{
		TournamentID: tournamentID, PlayerID: playerID,
	})
	require.NoError(t, err)
	return usecase.GoldenMutationScope{
		ActorID: playerID, ExpectedRuntimeRevision: view.RuntimeRevision,
		ExpectedAttemptID: view.AttemptID, ExpectedReadyWindowID: view.ReadyWindowID,
	}
}

func goldenRuntimeReadyCommand(
	ctx context.Context,
	t testing.TB,
	application usecase.GoldenUseCase,
	tournamentID, playerID, commandID uuid.UUID,
) usecase.GoldenReadyCommand {
	t.Helper()
	return usecase.GoldenReadyCommand{
		TournamentID: tournamentID, PlayerID: playerID, CommandID: commandID, Ready: true,
		GoldenMutationScope: goldenRuntimeParticipantScope(ctx, t, application, tournamentID, playerID),
	}
}

func goldenRuntimeSubmitCommand(
	ctx context.Context,
	t testing.TB,
	application usecase.GoldenUseCase,
	tournamentID, playerID, commandID uuid.UUID,
	flag string,
) usecase.GoldenSubmissionCommand {
	t.Helper()
	return usecase.GoldenSubmissionCommand{
		TournamentID: tournamentID, PlayerID: playerID, CommandID: commandID, SubmittedFlag: flag,
		GoldenMutationScope: goldenRuntimeParticipantScope(ctx, t, application, tournamentID, playerID),
	}
}

func goldenRuntimeStartCommand(
	ctx context.Context,
	t testing.TB,
	application usecase.GoldenUseCase,
	tournamentID, attemptID, commandID uuid.UUID,
) usecase.GoldenStartCommand {
	t.Helper()
	actorID := uuid.New()
	view, err := application.OperatorView(ctx, usecase.GoldenOperatorQuery{
		TournamentID: tournamentID, OperatorID: actorID,
	})
	require.NoError(t, err)
	for _, group := range view.Groups {
		if group.AttemptID == attemptID {
			return usecase.GoldenStartCommand{
				TournamentID: tournamentID, AttemptID: attemptID, CommandID: commandID,
				GoldenMutationScope: usecase.GoldenMutationScope{
					ActorID: actorID, ExpectedRuntimeRevision: group.RuntimeRevision,
					ExpectedAttemptID: attemptID, ExpectedReadyWindowID: group.ReadyWindowID,
				},
			}
		}
	}
	require.FailNow(t, "Golden attempt not found", attemptID)
	return usecase.GoldenStartCommand{}
}

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
