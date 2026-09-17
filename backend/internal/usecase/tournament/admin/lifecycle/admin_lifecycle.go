package lifecycle

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

type TournamentAction string

const (
	TournamentActionOpenRegistration TournamentAction = "open_registration"
	TournamentActionStartSwiss       TournamentAction = "start_swiss"
	TournamentActionStartGolden      TournamentAction = "start_golden"
	TournamentActionStartPlayoffs    TournamentAction = "start_playoffs"
	TournamentActionPause            TournamentAction = "pause"
	TournamentActionResume           TournamentAction = "resume"
	TournamentActionComplete         TournamentAction = "complete"
	TournamentActionCancel           TournamentAction = "cancel"
)

type TournamentActionCommand struct {
	CommandScope

	ExpectedProjectionRevision int64
	Action                     TournamentAction
	Confirmed                  bool
	Reason                     string
}

type LifecyclePort interface {
	ApplyTournamentAction(ctx context.Context, command TournamentActionCommand) (usecase.TournamentView, error)
}

func validTournamentActionCommand(command TournamentActionCommand) bool {
	return validCommandScope(command.CommandScope) && command.ExpectedProjectionRevision >= 1 &&
		command.Action.valid() && command.Confirmed && validOptionalText(command.Reason, maxReasonRunes)
}

func (action TournamentAction) valid() bool {
	switch action {
	case TournamentActionOpenRegistration, TournamentActionStartSwiss, TournamentActionStartGolden,
		TournamentActionStartPlayoffs, TournamentActionPause, TournamentActionResume,
		TournamentActionComplete, TournamentActionCancel:
		return true
	default:
		return false
	}
}

func validTournamentView(view usecase.TournamentView, tournamentID uuid.UUID) bool {
	if !validTournamentViewHeader(view, tournamentID) ||
		(domain.Tournament{State: view.State, PausedFromState: view.PausedFromState}).Validate() != nil {
		return false
	}
	return validEventTime(view.StartedAt, view.CreatedAt, view.UpdatedAt) &&
		validEventTime(view.FinishedAt, view.CreatedAt, view.UpdatedAt) &&
		(view.StartedAt == nil || view.FinishedAt == nil || !view.FinishedAt.Before(*view.StartedAt))
}

func validTournamentViewHeader(view usecase.TournamentView, tournamentID uuid.UUID) bool {
	return view.ID == tournamentID && view.RosterID != uuid.Nil && view.Preset.IsValid() &&
		view.Revision >= 1 && view.RosterSize >= 0 && view.RosterSize <= domain.TournamentMaxParticipants &&
		domain.IsValidServerTime(view.CreatedAt) && domain.IsValidServerTime(view.UpdatedAt) &&
		!view.UpdatedAt.Before(view.CreatedAt)
}
