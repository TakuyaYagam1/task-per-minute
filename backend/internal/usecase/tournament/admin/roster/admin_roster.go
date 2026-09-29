package roster

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

type RosterQuery struct {
	Operator     OperatorIdentity
	TournamentID uuid.UUID
}

type RosterParticipantInput struct {
	PlayerID   uuid.UUID
	Seed       int
	Attendance domain.AttendanceState
}

type ReplaceRosterCommand struct {
	CommandScope

	ExpectedProjectionRevision int64
	Participants               []RosterParticipantInput
}

func NewReplaceRosterCommand(
	operator OperatorIdentity,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
	expectedRevision int64,
	participants []RosterParticipantInput,
) ReplaceRosterCommand {
	return ReplaceRosterCommand{
		CommandScope:               CommandScope{Operator: operator, TournamentID: tournamentID, CommandID: commandID},
		ExpectedProjectionRevision: expectedRevision,
		Participants:               append([]RosterParticipantInput(nil), participants...),
	}
}

type PreflightCommand struct {
	CommandScope

	ExpectedProjectionRevision int64
}

type LockRosterCommand struct {
	CommandScope

	ExpectedProjectionRevision int64
	PreflightRevisionID        uuid.UUID
	CheckedInPlayerIDs         []uuid.UUID
}

type UnlockRosterCommand struct {
	CommandScope

	ExpectedProjectionRevision int64
	Confirmed                  bool
	Reason                     string
}

type RosterParticipantView struct {
	ID           uuid.UUID
	RosterID     uuid.UUID
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	Seed         int
	Attendance   domain.AttendanceState
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type RosterView struct {
	ID                 uuid.UUID
	TournamentID       uuid.UUID
	Revision           int64
	Participants       []RosterParticipantView
	Locked             bool
	ExecutionStarted   bool
	LockedAt           *time.Time
	ExecutionStartedAt *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type RosterPort interface {
	GetRoster(ctx context.Context, query RosterQuery) (RosterView, error)
	ReplaceRoster(ctx context.Context, command ReplaceRosterCommand) (RosterView, error)
	LockRoster(ctx context.Context, command LockRosterCommand) (RosterView, error)
	UnlockRoster(ctx context.Context, command UnlockRosterCommand) (RosterView, error)
}

type PreflightPort interface {
	RunPreflight(ctx context.Context, command PreflightCommand) (tournamentpreflight.ReportRevision, error)
}

func validRosterQuery(query RosterQuery) bool {
	return validOperator(query.Operator) && query.TournamentID != uuid.Nil
}

func validReplaceRosterCommand(command ReplaceRosterCommand) bool {
	if !validCommandScope(command.CommandScope) || command.ExpectedProjectionRevision < 1 ||
		len(command.Participants) > domain.TournamentMaxParticipants {
		return false
	}
	players := make(map[uuid.UUID]struct{}, len(command.Participants))
	seeds := make(map[int]struct{}, len(command.Participants))
	for _, participant := range command.Participants {
		if participant.PlayerID == uuid.Nil || participant.Seed < 1 ||
			participant.Seed > len(command.Participants) || !participant.Attendance.IsValid() {
			return false
		}
		if _, duplicate := players[participant.PlayerID]; duplicate {
			return false
		}
		if _, duplicate := seeds[participant.Seed]; duplicate {
			return false
		}
		players[participant.PlayerID] = struct{}{}
		seeds[participant.Seed] = struct{}{}
	}
	return true
}

func validPreflightCommand(command PreflightCommand) bool {
	return validCommandScope(command.CommandScope) && command.ExpectedProjectionRevision >= 1
}

func validLockRosterCommand(command LockRosterCommand) bool {
	return validCommandScope(command.CommandScope) && command.ExpectedProjectionRevision >= 1 &&
		command.PreflightRevisionID != uuid.Nil &&
		validUniqueIDs(command.CheckedInPlayerIDs, domain.TournamentMinParticipants, domain.TournamentMaxParticipants)
}

func validUnlockRosterCommand(command UnlockRosterCommand) bool {
	return validCommandScope(command.CommandScope) && command.ExpectedProjectionRevision >= 1 &&
		command.Confirmed && validText(command.Reason, maxReasonRunes)
}

func validRosterView(view RosterView, tournamentID uuid.UUID) bool {
	if !validRosterViewHeader(view, tournamentID) || !validRosterTimeline(view) {
		return false
	}
	return validRosterParticipants(view, tournamentID)
}

func validRosterViewHeader(view RosterView, tournamentID uuid.UUID) bool {
	return view.ID != uuid.Nil && view.TournamentID == tournamentID && view.Revision >= 1 &&
		view.Participants != nil && len(view.Participants) <= domain.TournamentMaxParticipants &&
		domain.IsValidServerTime(view.CreatedAt) && domain.IsValidServerTime(view.UpdatedAt) &&
		!view.UpdatedAt.Before(view.CreatedAt)
}

func validRosterTimeline(view RosterView) bool {
	if view.Locked != (view.LockedAt != nil) || view.ExecutionStarted != (view.ExecutionStartedAt != nil) ||
		view.ExecutionStarted && !view.Locked {
		return false
	}
	return validEventTime(view.LockedAt, view.CreatedAt, view.UpdatedAt) &&
		validEventTime(view.ExecutionStartedAt, view.CreatedAt, view.UpdatedAt)
}

func validRosterParticipants(view RosterView, tournamentID uuid.UUID) bool {
	participantIDs := make(map[uuid.UUID]struct{}, len(view.Participants))
	players := make(map[uuid.UUID]struct{}, len(view.Participants))
	seeds := make(map[int]struct{}, len(view.Participants))
	for _, participant := range view.Participants {
		if !validRosterParticipant(participant, view.ID, tournamentID) ||
			!addUniqueID(participantIDs, participant.ID) || !addUniqueID(players, participant.PlayerID) ||
			!addUniqueInt(seeds, participant.Seed) {
			return false
		}
	}
	return !view.Locked || len(view.Participants) >= domain.TournamentMinParticipants
}

func validRosterParticipant(participant RosterParticipantView, rosterID, tournamentID uuid.UUID) bool {
	return participant.ID != uuid.Nil && participant.RosterID == rosterID &&
		participant.TournamentID == tournamentID && participant.PlayerID != uuid.Nil &&
		participant.Seed >= 1 && participant.Seed <= domain.TournamentMaxParticipants &&
		participant.Attendance.IsValid() && domain.IsValidServerTime(participant.CreatedAt) &&
		domain.IsValidServerTime(participant.UpdatedAt) && !participant.UpdatedAt.Before(participant.CreatedAt)
}
