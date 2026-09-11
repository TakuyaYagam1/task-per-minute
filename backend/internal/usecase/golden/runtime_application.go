package golden

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

var ErrInvalidGoldenRuntime = errors.New("invalid Golden runtime command")

type RuntimeRepository interface {
	Open(ctx context.Context, command usecase.GoldenOpenCommand, now time.Time) (usecase.GoldenOperatorView, error)
	Start(ctx context.Context, command usecase.GoldenStartCommand, now time.Time) (usecase.GoldenOperatorView, error)
	SetReady(ctx context.Context, command usecase.GoldenReadyCommand, now time.Time) (usecase.GoldenParticipantView, error)
	Submit(ctx context.Context, command usecase.GoldenSubmissionCommand, now time.Time) (usecase.GoldenParticipantView, error)
	OperatorView(ctx context.Context, query usecase.GoldenOperatorQuery) (usecase.GoldenOperatorView, error)
	ParticipantView(ctx context.Context, query usecase.GoldenParticipantQuery) (usecase.GoldenParticipantView, error)
	SetConnected(ctx context.Context, command usecase.GoldenConnectionCommand, now time.Time) error
	Recover(ctx context.Context, tournamentID uuid.UUID, now time.Time) error
}

type RuntimeApplication struct {
	repository RuntimeRepository
	clock      ConnectionClock
}

var _ usecase.GoldenUseCase = (*RuntimeApplication)(nil)
var _ usecase.GoldenConnectionUseCase = (*RuntimeApplication)(nil)

func NewRuntimeApplication(repository RuntimeRepository, clock ConnectionClock) *RuntimeApplication {
	return &RuntimeApplication{repository: repository, clock: clock}
}

func (application *RuntimeApplication) Open(ctx context.Context, command usecase.GoldenOpenCommand) (usecase.GoldenOperatorView, error) {
	if !application.valid() || ctx == nil || command.TournamentID == uuid.Nil || command.CommandID == uuid.Nil || command.ExpectedProjectionRevision < 1 {
		return usecase.GoldenOperatorView{}, ErrInvalidGoldenRuntime
	}
	now, err := application.now()
	if err != nil {
		return usecase.GoldenOperatorView{}, err
	}
	return application.repository.Open(ctx, command, now)
}

func (application *RuntimeApplication) Start(ctx context.Context, command usecase.GoldenStartCommand) (usecase.GoldenOperatorView, error) {
	if !application.valid() || ctx == nil || command.TournamentID == uuid.Nil || command.AttemptID == uuid.Nil || command.CommandID == uuid.Nil {
		return usecase.GoldenOperatorView{}, ErrInvalidGoldenRuntime
	}
	now, err := application.now()
	if err != nil {
		return usecase.GoldenOperatorView{}, err
	}
	return application.repository.Start(ctx, command, now)
}

func (application *RuntimeApplication) SetReady(ctx context.Context, command usecase.GoldenReadyCommand) (usecase.GoldenParticipantView, error) {
	if !application.valid() || ctx == nil || command.TournamentID == uuid.Nil || command.PlayerID == uuid.Nil || command.CommandID == uuid.Nil || !command.Ready {
		return usecase.GoldenParticipantView{}, ErrInvalidGoldenRuntime
	}
	now, err := application.now()
	if err != nil {
		return usecase.GoldenParticipantView{}, err
	}
	return application.repository.SetReady(ctx, command, now)
}

// Submit validates the public command before passing it to the transactional runtime.
// The explicit checks keep malformed identities and oversized secrets out of storage.
//

func (application *RuntimeApplication) Submit(ctx context.Context, command usecase.GoldenSubmissionCommand) (usecase.GoldenParticipantView, error) {
	if !application.valid() || ctx == nil || command.TournamentID == uuid.Nil || command.PlayerID == uuid.Nil || command.CommandID == uuid.Nil || strings.TrimSpace(command.SubmittedFlag) == "" || len(command.SubmittedFlag) > 255 {
		return usecase.GoldenParticipantView{}, ErrInvalidGoldenRuntime
	}
	now, err := application.now()
	if err != nil {
		return usecase.GoldenParticipantView{}, err
	}
	return application.repository.Submit(ctx, command, now)
}

func (application *RuntimeApplication) OperatorView(ctx context.Context, query usecase.GoldenOperatorQuery) (usecase.GoldenOperatorView, error) {
	if !application.valid() || ctx == nil || query.TournamentID == uuid.Nil || query.OperatorID == uuid.Nil {
		return usecase.GoldenOperatorView{}, ErrInvalidGoldenRuntime
	}
	return application.repository.OperatorView(ctx, query)
}

func (application *RuntimeApplication) ParticipantView(ctx context.Context, query usecase.GoldenParticipantQuery) (usecase.GoldenParticipantView, error) {
	if !application.valid() || ctx == nil || query.TournamentID == uuid.Nil || query.PlayerID == uuid.Nil {
		return usecase.GoldenParticipantView{}, ErrInvalidGoldenRuntime
	}
	return application.repository.ParticipantView(ctx, query)
}

func (application *RuntimeApplication) SetConnected(ctx context.Context, command usecase.GoldenConnectionCommand) error {
	if !application.valid() || ctx == nil || command.TournamentID == uuid.Nil ||
		command.PlayerID == uuid.Nil || command.CommandID == uuid.Nil {
		return ErrInvalidGoldenRuntime
	}
	now, err := application.now()
	if err != nil {
		return err
	}
	return application.repository.SetConnected(ctx, command, now)
}

func (application *RuntimeApplication) Recover(ctx context.Context, tournamentID uuid.UUID) error {
	if !application.valid() || ctx == nil || tournamentID == uuid.Nil {
		return ErrInvalidGoldenRuntime
	}
	now, err := application.now()
	if err != nil {
		return err
	}
	return application.repository.Recover(ctx, tournamentID, now)
}

func (application *RuntimeApplication) valid() bool {
	return application != nil && application.repository != nil && application.clock != nil
}

func (application *RuntimeApplication) now() (time.Time, error) {
	now := application.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(now) {
		return time.Time{}, ErrInvalidGoldenRuntime
	}
	return now, nil
}
