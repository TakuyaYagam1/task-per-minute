//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	runtimepostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/golden/runtime"
	adminlifecyclerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/lifecycle"
	cancellationrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/cancellation"
	lifecyclepostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/lifecycle"
	progressionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/progression"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	goldenruntime "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/runtime"
	adminlifecycle "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	adminoperation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
	tournamentlifecycle "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func TestTournamentCancellationPostgresAfterGoldenNoShow(t *testing.T) {
	ctx := context.Background()
	fixture := prepareNativeGoldenFinalSwiss(ctx, t)
	projectionID, projectionRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	require.NoError(t, publishSwissGolden(ctx, fixture, tournamentprogression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: projectionRevision,
		Action: tournamentprogression.ActionStartGolden,
	}))

	now := time.Now().UTC().Truncate(time.Microsecond)
	ensureGoldenRuntimeTestCapacity(ctx, t, fixture.tournamentID)
	createGoldenRuntimeTestPlan(
		ctx, t, fixture.tournamentID, fixture.rosterID, projectionID, projectionRevision, now.Add(-time.Second),
	)
	runtime := goldenruntime.NewRuntimeApplication(
		runtimepostgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)),
		goldenRuntimeClock{now: now},
	)
	opened, err := runtime.Open(ctx, inbound.GoldenOpenCommand{
		TournamentID: fixture.tournamentID, CommandID: uuid.New(),
		ExpectedProjectionRevision: projectionRevision,
		GoldenMutationScope:        inbound.GoldenMutationScope{ActorID: uuid.New()},
	})
	require.NoError(t, err)
	require.Len(t, opened.Groups, 2)
	targetGroup := opened.Groups[len(opened.Groups)-1]
	require.EqualValues(t, 3, targetGroup.PositionFrom)
	require.EqualValues(t, 4, targetGroup.PositionTo)
	require.Len(t, targetGroup.Members, 2)
	noShowParticipantID := targetGroup.Members[1].ParticipantID
	players := make(map[uuid.UUID]uuid.UUID)
	for _, candidate := range opened.Groups {
		for participantID, playerID := range goldenRuntimePlayers(ctx, t, candidate) {
			players[participantID] = playerID
		}
		for _, member := range candidate.Members {
			if member.ParticipantID == noShowParticipantID {
				continue
			}
			_, err = runtime.SetReady(ctx, goldenRuntimeReadyCommand(
				ctx, t, runtime, fixture.tournamentID, players[member.ParticipantID], uuid.New(),
			))
			require.NoError(t, err)
		}
	}

	var readyDeadline time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT ready_window_deadline
		FROM golden_runtime_assignments
		WHERE attempt_id = $1`, targetGroup.AttemptID,
	).Scan(&readyDeadline))
	require.True(t, readyDeadline.After(now))
	noShowClock := goldenRuntimeClock{now: readyDeadline.Add(time.Second)}
	recovered := goldenruntime.NewRuntimeApplication(
		runtimepostgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)), noShowClock,
	)
	require.NoError(t, recovered.Recover(ctx, fixture.tournamentID))
	operatorView, err := recovered.OperatorView(ctx, inbound.GoldenOperatorQuery{
		TournamentID: fixture.tournamentID, OperatorID: uuid.New(),
	})
	require.NoError(t, err)
	readyGroup := goldenRuntimeGroupView(t, operatorView, targetGroup.GroupRevisionID)
	require.Equal(t, "ready", readyGroup.State)
	require.Greater(t, readyGroup.RuntimeRevision, targetGroup.RuntimeRevision)

	for _, initialGroup := range opened.Groups {
		currentGroup := goldenRuntimeGroupView(t, operatorView, initialGroup.GroupRevisionID)
		if initialGroup.GroupRevisionID == targetGroup.GroupRevisionID {
			require.Equal(t, "ready", currentGroup.State)
		}
		_, err = recovered.Start(ctx, goldenRuntimeStartCommand(
			ctx, t, recovered, fixture.tournamentID, currentGroup.AttemptID, uuid.New(),
		))
		require.NoError(t, err)
		flag := goldenRuntimeAttemptFlag(ctx, t, currentGroup.AttemptID)
		activeMembers := make([]uuid.UUID, 0, len(initialGroup.Members))
		for _, member := range initialGroup.Members {
			if member.ParticipantID != noShowParticipantID {
				activeMembers = append(activeMembers, member.ParticipantID)
			}
		}
		for index, participantID := range activeMembers {
			finalView, submitErr := recovered.Submit(ctx, goldenRuntimeSubmitCommand(
				ctx, t, recovered, fixture.tournamentID, players[participantID], uuid.New(), flag,
			))
			require.NoError(t, submitErr)
			if index == len(activeMembers)-1 {
				require.Equal(t, "completed", finalView.State)
			}
		}
	}
	completedView, err := recovered.OperatorView(ctx, inbound.GoldenOperatorQuery{
		TournamentID: fixture.tournamentID, OperatorID: uuid.New(),
	})
	require.NoError(t, err)
	for _, completedGroup := range opened.Groups {
		require.Equal(t, "completed", goldenRuntimeGroupView(t, completedView, completedGroup.GroupRevisionID).State)
	}
	completedTarget := goldenRuntimeGroupView(t, completedView, targetGroup.GroupRevisionID)
	noShowPositionFound := false
	for _, member := range completedTarget.Members {
		if member.ParticipantID != noShowParticipantID {
			continue
		}
		require.NotNil(t, member.Position)
		require.EqualValues(t, 4, *member.Position)
		noShowPositionFound = true
	}
	require.True(t, noShowPositionFound, "completed Golden projection must retain the no-show position")

	_, playoffSourceRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	playoffs, err := publishSwissPlayoffsWithClock(ctx, fixture, tournamentprogression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: playoffSourceRevision,
		Action: tournamentprogression.ActionStartPlayoffs,
	}, playoffPublicationClock{now: readyDeadline.Add(2 * time.Second)})
	require.NoError(t, err)
	require.Equal(t, domain.TournamentStatePlayoffs, playoffs.State)
	_, cancellationProjectionRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	snapshotHistory := readGoldenSnapshotParticipantReservations(ctx, t, sharedPool, fixture.tournamentID)
	require.NotEmpty(t, snapshotHistory, "Golden snapshot must retain participant reservation evidence")

	stage := &cancellationTestStage{}
	tx := cancellationStageTransactionManager{delegate: fixture.tx, stage: stage}
	lifecycleRepository := adminlifecyclerepo.NewTournamentAdminLifecyclePostgres(fixture.tx)
	cancellationClock := playoffPublicationClock{now: readyDeadline.Add(3 * time.Second)}
	progressionRepository := progressionrepo.NewTournamentProgressionPostgres(fixture.tx)
	lifecycle := adminlifecycle.NewLifecycleWorkflow(adminlifecycle.LifecycleWorkflowDependencies{
		Transactions: tx,
		Repository: cancellationStageLifecycleRepository{
			LifecycleWorkflowRepository: lifecycleRepository, stage: stage,
		},
		Transitions: tournamentlifecycle.NewTournamentLifecycleUseCase(
			lifecyclepostgres.NewTournamentLifecyclePostgres(fixture.tx), cancellationClock,
		),
		Pauses: tournamentpause.NewTournamentPauseUseCase(fixture.tx, lifecycleRepository, cancellationClock),
		Cancellations: tournamentcancellation.NewTournamentCancellationUseCase(
			cancellationStageCancellationRepository{
				TournamentCancellationRepository: cancellationrepo.NewTournamentCancellationPostgres(fixture.tx),
				stage:                            stage,
			}, cancellationClock,
		),
		Progressions: tournamentprogression.NewWorkflow(tournamentprogression.ProgressionDependencies{
			Repository: progressionRepository, TerminalEvidence: progressionRepository,
			Transitioner: progressionRepository, Publisher: progressionRepository,
			ProgressionClock: cancellationClock,
		}),
		Clock: cancellationClock,
	})
	cancelled, err := lifecycle.ApplyTournamentAction(ctx, adminlifecycle.TournamentActionCommand{
		CommandScope: adminlifecycle.CommandScope{
			Operator:     adminoperation.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: fixture.tournamentID, CommandID: uuid.New(),
		},
		ExpectedProjectionRevision: cancellationProjectionRevision,
		Action:                     adminlifecycle.TournamentActionCancel, Confirmed: true,
		Reason: "operator cancellation after Golden no-show",
	})
	if err != nil {
		var databaseError *pgconn.PgError
		state, constraint, message := "", "", ""
		if errors.As(err, &databaseError) {
			state, constraint, message = databaseError.Code, databaseError.ConstraintName, databaseError.Message
		}
		t.Fatalf("admin cancellation failed at stage %q: SQLSTATE=%q constraint=%q database_message=%q error=%+v",
			stage.name, state, constraint, message, err)
	}
	require.Equal(t, domain.TournamentStateCancelled, cancelled.State)
	require.Equal(t, "outer transaction commit", stage.name)
	var cancellationCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM tournament_cancellations WHERE tournament_id = $1`, fixture.tournamentID,
	).Scan(&cancellationCount))
	require.Equal(t, 1, cancellationCount)
	require.Equal(t, snapshotHistory,
		readGoldenSnapshotParticipantReservations(ctx, t, sharedPool, fixture.tournamentID),
		"cancellation must preserve sealed Golden reservation history")

	var cancelledTournamentReservations int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM participant_reservations WHERE tournament_id = $1`, fixture.tournamentID,
	).Scan(&cancelledTournamentReservations))
	require.Zero(t, cancelledTournamentReservations, "cancellation must release live player reservations")

	reusedTournamentID := createMigrationTournament(ctx, t)
	var reusedReservationID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		INSERT INTO participant_reservations (player_id, tournament_id)
		VALUES ($1, $2)
		RETURNING reservation_id`, snapshotHistory[0].PlayerID, reusedTournamentID,
	).Scan(&reusedReservationID))
	require.NotEqual(t, snapshotHistory[0].ReservationID, reusedReservationID,
		"a released player must receive a new reservation in another tournament")
	require.Equal(t, snapshotHistory,
		readGoldenSnapshotParticipantReservations(ctx, t, sharedPool, fixture.tournamentID),
		"reusing a player reservation must not mutate prior Golden history")
}

type cancellationTestStage struct{ name string }

type cancellationStageTransactionManager struct {
	delegate *postgres.TxManager
	stage    *cancellationTestStage
}

func (manager cancellationStageTransactionManager) Do(ctx context.Context, fn func(context.Context) error) error {
	return manager.delegate.Do(ctx, func(txCtx context.Context) error {
		if err := fn(txCtx); err != nil {
			return err
		}
		manager.stage.name = "outer transaction commit"
		return nil
	})
}

type cancellationStageLifecycleRepository struct {
	adminlifecycle.LifecycleWorkflowRepository
	stage *cancellationTestStage
}

func (repository cancellationStageLifecycleRepository) LockLifecycleAuthority(
	ctx context.Context,
	tournamentID uuid.UUID,
) (adminlifecycle.LifecycleAuthority, error) {
	repository.stage.name = "lifecycle authority lock"
	return repository.LifecycleWorkflowRepository.LockLifecycleAuthority(ctx, tournamentID)
}

func (repository cancellationStageLifecycleRepository) FindLifecycleCommand(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*adminlifecycle.LifecycleCommandRecord, error) {
	repository.stage.name = "lifecycle idempotency lookup"
	return repository.LifecycleWorkflowRepository.FindLifecycleCommand(ctx, tournamentID, commandID)
}

func (repository cancellationStageLifecycleRepository) SaveLifecycleCommand(
	ctx context.Context,
	record adminlifecycle.LifecycleCommandRecord,
) error {
	repository.stage.name = "lifecycle command insert"
	return repository.LifecycleWorkflowRepository.SaveLifecycleCommand(ctx, record)
}

type cancellationStageCancellationRepository struct {
	tournamentcancellation.TournamentCancellationRepository
	stage *cancellationTestStage
}

func (repository cancellationStageCancellationRepository) GetTournament(
	ctx context.Context,
	id uuid.UUID,
) (*tournamentcancellation.CancellationTournamentRecord, error) {
	repository.stage.name = "cancellation authority read"
	return repository.TournamentCancellationRepository.GetTournament(ctx, id)
}

func (repository cancellationStageCancellationRepository) GetTournamentCancellation(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*tournamentcancellation.TournamentCancellationRecord, error) {
	repository.stage.name = "cancellation replay lookup"
	return repository.TournamentCancellationRepository.GetTournamentCancellation(ctx, tournamentID, commandID)
}

func (repository cancellationStageCancellationRepository) CancelTournament(
	ctx context.Context,
	input tournamentcancellation.TournamentCancellationInput,
) (*tournamentcancellation.TournamentCancellationRecord, bool, error) {
	repository.stage.name = "cancellation SQL mutation"
	return repository.TournamentCancellationRepository.CancelTournament(ctx, input)
}

func TestTournamentCancellationPostgresUsesLockedProjectionTargetAndReplaysExactly(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createDraftMigrationFixture(ctx, t)
	input := cancellationRepositoryInput(ctx, t, fixture, uuid.New())
	var expectedProjectionID uuid.UUID
	var expectedProjectionRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id, revision_number
		FROM projection_revisions
		WHERE tournament_id = $1
			AND roster_id = $2
			AND state = 'published'
		FOR KEY SHARE`, fixture.tournamentID, fixture.rosterID,
	).Scan(&expectedProjectionID, &expectedProjectionRevision))

	repositories := []*cancellationrepo.TournamentCancellationPostgres{
		cancellationrepo.NewTournamentCancellationPostgres(postgres.NewTxManager(sharedPool)),
		cancellationrepo.NewTournamentCancellationPostgres(postgres.NewTxManager(sharedPool)),
	}
	type outcome struct {
		record  *tournamentcancellation.TournamentCancellationRecord
		changed bool
		err     error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, len(repositories))
	var workers sync.WaitGroup
	for _, repository := range repositories {
		workers.Add(1)
		go func(repository *cancellationrepo.TournamentCancellationPostgres) {
			defer workers.Done()
			<-start
			record, changed, err := repository.CancelTournament(ctx, input)
			outcomes <- outcome{record: record, changed: changed, err: err}
		}(repository)
	}
	close(start)
	workers.Wait()
	close(outcomes)

	changedCount := 0
	replayedCount := 0
	for outcome := range outcomes {
		require.NoError(t, outcome.err)
		require.NotNil(t, outcome.record)
		require.Equal(t, input.CommandID, outcome.record.CommandID)
		if outcome.changed {
			changedCount++
		} else {
			replayedCount++
		}
	}
	require.Equal(t, 1, changedCount)
	require.Equal(t, 1, replayedCount)

	var (
		eventProjectionID        uuid.UUID
		eventProjectionRevision  int64
		eventProjectionOrdinal   int16
		sourceProjectionID       uuid.UUID
		sourceProjectionRevision int64
		sourceProjectionOrdinal  int16
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT event.projection_revision_id,
			event.projection_revision,
			event.projection_ordinal,
			source.projection_revision_id,
			source.projection_revision,
			source.projection_ordinal
		FROM outbox_events AS event
		INNER JOIN outbox_tournament_cancellation_sources AS source
			ON source.outbox_event_id = event.id
			AND source.tournament_id = event.tournament_id
			AND source.roster_id = event.roster_id
		WHERE source.cancellation_command_id = $1`, input.CommandID,
	).Scan(
		&eventProjectionID,
		&eventProjectionRevision,
		&eventProjectionOrdinal,
		&sourceProjectionID,
		&sourceProjectionRevision,
		&sourceProjectionOrdinal,
	))
	require.Equal(t, expectedProjectionID, eventProjectionID)
	require.Equal(t, expectedProjectionRevision, eventProjectionRevision)
	require.GreaterOrEqual(t, eventProjectionOrdinal, int16(1))
	require.Equal(t, eventProjectionID, sourceProjectionID)
	require.Equal(t, eventProjectionRevision, sourceProjectionRevision)
	require.Equal(t, eventProjectionOrdinal, sourceProjectionOrdinal)
}

func TestTournamentCancellationPostgresRejectsCrossScopeCommandReuse(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	first := createDraftMigrationFixture(ctx, t)
	second := createDraftMigrationFixture(ctx, t)
	commandID := uuid.New()
	inputs := []tournamentcancellation.TournamentCancellationInput{
		cancellationRepositoryInput(ctx, t, first, commandID),
		cancellationRepositoryInput(ctx, t, second, commandID),
	}
	type outcome struct {
		input   tournamentcancellation.TournamentCancellationInput
		changed bool
		err     error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, len(inputs))
	var workers sync.WaitGroup
	for _, input := range inputs {
		workers.Add(1)
		go func(input tournamentcancellation.TournamentCancellationInput) {
			defer workers.Done()
			<-start
			_, changed, err := cancellationrepo.NewTournamentCancellationPostgres(
				postgres.NewTxManager(sharedPool),
			).CancelTournament(ctx, input)
			outcomes <- outcome{input: input, changed: changed, err: err}
		}(input)
	}
	close(start)
	workers.Wait()
	close(outcomes)

	changedCount := 0
	conflictedCount := 0
	var conflicted tournamentcancellation.TournamentCancellationInput
	for outcome := range outcomes {
		switch {
		case outcome.err == nil && outcome.changed:
			changedCount++
		case errors.Is(outcome.err, domain.ErrConflict):
			conflictedCount++
			conflicted = outcome.input
		default:
			require.NoError(t, outcome.err)
		}
	}
	require.Equal(t, 1, changedCount)
	require.Equal(t, 1, conflictedCount)

	var (
		state    string
		revision int64
		ledger   int
		outbox   int
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, revision
		FROM tournaments
		WHERE id = $1`, conflicted.TournamentID,
	).Scan(&state, &revision))
	require.Equal(t, string(domain.TournamentStateDraft), state)
	require.Equal(t, conflicted.ExpectedRevision, revision)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM tournament_cancellations
		WHERE tournament_id = $1`, conflicted.TournamentID,
	).Scan(&ledger))
	require.Zero(t, ledger)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM outbox_events
		WHERE tournament_id = $1`, conflicted.TournamentID,
	).Scan(&outbox))
	require.Zero(t, outbox)
}

func cancellationRepositoryInput(
	ctx context.Context,
	tb testing.TB,
	fixture draftMigrationFixture,
	commandID uuid.UUID,
) tournamentcancellation.TournamentCancellationInput {
	tb.Helper()

	var (
		revision int64
		state    string
	)
	require.NoError(tb, sharedPool.QueryRow(ctx, `
		SELECT revision, state
		FROM tournaments
		WHERE id = $1`, fixture.tournamentID,
	).Scan(&revision, &state))
	return tournamentcancellation.TournamentCancellationInput{
		TournamentID:     fixture.tournamentID,
		ExpectedRevision: revision,
		ExpectedState:    domain.TournamentState(state),
		CommandID:        commandID,
		ActorID:          uuid.New(),
		Reason:           "operator cancellation request",
		CancelledAt:      time.Now().UTC().Truncate(time.Microsecond),
	}
}
