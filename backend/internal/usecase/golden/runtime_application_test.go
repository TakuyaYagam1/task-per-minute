package golden_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
)

func TestRuntimeApplicationRoutesGoldenLifecycleAndRecovery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	repository := &runtimeRepositoryStub{view: goldenRuntimeView(now)}
	application := goldenusecase.NewRuntimeApplication(repository, runtimeClock{now: now})

	operator, err := application.Open(context.Background(), usecase.GoldenOpenCommand{
		TournamentID: uuid.New(), CommandID: uuid.New(), ExpectedProjectionRevision: 7,
	})
	require.NoError(t, err)
	require.Equal(t, repository.view, operator)
	require.Equal(t, 1, repository.openCalls)

	participant, err := application.SetReady(context.Background(), usecase.GoldenReadyCommand{
		TournamentID: repository.view.TournamentID, PlayerID: uuid.New(), CommandID: uuid.New(), Ready: true,
	})
	require.NoError(t, err)
	require.Equal(t, repository.participant, participant)

	_, err = application.Start(context.Background(), usecase.GoldenStartCommand{
		TournamentID: repository.view.TournamentID, AttemptID: repository.view.Groups[0].AttemptID,
		CommandID: uuid.New(),
	})
	require.NoError(t, err)

	_, err = application.Submit(context.Background(), usecase.GoldenSubmissionCommand{
		TournamentID: repository.view.TournamentID, PlayerID: uuid.New(), CommandID: uuid.New(),
		SubmittedFlag: "TPM{correct}",
	})
	require.NoError(t, err)
	require.NoError(t, application.SetConnected(context.Background(), usecase.GoldenConnectionCommand{
		TournamentID: repository.view.TournamentID, PlayerID: uuid.New(), CommandID: uuid.New(), Connected: true,
	}))

	require.NoError(t, application.Recover(context.Background(), repository.view.TournamentID))
	require.Equal(t, []string{"open", "ready", "start", "submit", "connect", "recover"}, repository.calls)
}

type runtimeClock struct{ now time.Time }

func (clock runtimeClock) Now() time.Time { return clock.now }

type runtimeRepositoryStub struct {
	view        usecase.GoldenOperatorView
	participant usecase.GoldenParticipantView
	calls       []string
	openCalls   int
}

func (repository *runtimeRepositoryStub) Open(
	_ context.Context,
	_ usecase.GoldenOpenCommand,
	_ time.Time,
) (usecase.GoldenOperatorView, error) {
	repository.calls = append(repository.calls, "open")
	repository.openCalls++
	return repository.view, nil
}

func (repository *runtimeRepositoryStub) SetReady(
	_ context.Context,
	_ usecase.GoldenReadyCommand,
	_ time.Time,
) (usecase.GoldenParticipantView, error) {
	repository.calls = append(repository.calls, "ready")
	return repository.participant, nil
}

func (repository *runtimeRepositoryStub) Start(
	_ context.Context,
	_ usecase.GoldenStartCommand,
	_ time.Time,
) (usecase.GoldenOperatorView, error) {
	repository.calls = append(repository.calls, "start")
	return repository.view, nil
}

func (repository *runtimeRepositoryStub) Submit(
	_ context.Context,
	_ usecase.GoldenSubmissionCommand,
	_ time.Time,
) (usecase.GoldenParticipantView, error) {
	repository.calls = append(repository.calls, "submit")
	return repository.participant, nil
}

func (repository *runtimeRepositoryStub) OperatorView(
	_ context.Context,
	_ usecase.GoldenOperatorQuery,
) (usecase.GoldenOperatorView, error) {
	return repository.view, nil
}

func (repository *runtimeRepositoryStub) ParticipantView(
	_ context.Context,
	_ usecase.GoldenParticipantQuery,
) (usecase.GoldenParticipantView, error) {
	return repository.participant, nil
}

func (repository *runtimeRepositoryStub) Recover(
	_ context.Context,
	_ uuid.UUID,
	_ time.Time,
) error {
	repository.calls = append(repository.calls, "recover")
	return nil
}

func (repository *runtimeRepositoryStub) SetConnected(
	_ context.Context,
	_ usecase.GoldenConnectionCommand,
	_ time.Time,
) error {
	repository.calls = append(repository.calls, "connect")
	return nil
}

func goldenRuntimeView(now time.Time) usecase.GoldenOperatorView {
	tournamentID := uuid.New()
	view := usecase.GoldenOperatorView{
		TournamentID: tournamentID,
		Groups: []usecase.GoldenOperatorGroupView{{
			GroupID: uuid.New(), GroupRevisionID: uuid.New(), AttemptID: uuid.New(),
			State: "prepared", PositionFrom: 1, PositionTo: 2,
		}},
		ObservedAt: now,
	}
	return view
}
