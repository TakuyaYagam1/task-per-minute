package pairing

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
)

type PairingMode string

const (
	PairingModeAutomatic PairingMode = "automatic"
	PairingModeManual    PairingMode = "manual"
)

type ParticipantPair struct {
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
}

type PairingCommand struct {
	operationusecase.CommandScope

	ExpectedProjectionRevision int64
	RoundNumber                int
	PairingMode                PairingMode
	CategoryMode               domain.CategoryMode
	Categories                 []domain.Category
	ManualPairings             []ParticipantPair
	ManualPairingsProvided     bool
	ManualByeParticipantID     *uuid.UUID
}

// PairingParticipant is the stable roster identity consumed by pairing and
// configuration projections.
type PairingParticipant struct {
	ID         uuid.UUID
	StableSeed int
}

type SwissStandingView struct {
	ParticipantID       uuid.UUID
	Position            int
	Points              int
	PointsLabel         string
	Buchholz            int
	BuchholzStatus      string
	HeadToHeadPoints    int
	HeadToHeadApplied   bool
	EffectiveTimeMS     int64
	AcceptedSolveTimeMS *int64
	StableSeed          int
}

type PairingAuthority struct {
	TournamentID         uuid.UUID
	TournamentState      domain.TournamentState
	TournamentRevision   int64
	RosterID             uuid.UUID
	RosterRevision       int64
	RosterLockedAt       time.Time
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
	HistoryRevision      int64
	Participants         []PairingParticipant
	Standings            []SwissStandingView
	PreviousMeetings     []swissusecase.Pair
	PriorMeetingCounts   map[swissusecase.PairKey]int
	ReceivedBye          map[uuid.UUID]bool
	RoundCount           int
	CompletedRoundCount  int
}

type PairingPlan struct {
	Command                 PairingCommand
	Authority               PairingAuthority
	RoundID                 uuid.UUID
	PairingEvidenceID       uuid.UUID
	WaveID                  uuid.UUID
	WaveRevisionID          domain.WaveRevisionID
	PairingIDs              []uuid.UUID
	SeriesIDs               []uuid.UUID
	InitialScoreRevisionIDs []domain.SeriesScoreRevisionID
	Pairs                   []swissusecase.Pair
	Automatic               *swissusecase.AutomaticPairing
	Bye                     *swissusecase.ByeSelection
	DecidedAt               time.Time
}

var ErrManualByeMismatch = errors.New("manual Swiss bye does not match the deterministic selection")

// ManualByeMismatchError preserves both the manual-bye error identity and the
// projection revision conflict details used by admin callers.
type ManualByeMismatchError struct {
	RequestedParticipantID uuid.UUID
	SelectedParticipantID  uuid.UUID
	ExpectedRevision       int64
	CurrentRevision        int64
	CurrentState           domain.TournamentState
}

func (e *ManualByeMismatchError) Error() string {
	return ErrManualByeMismatch.Error()
}

func (e *ManualByeMismatchError) Unwrap() []error {
	return []error{
		ErrManualByeMismatch,
		&operationusecase.RevisionConflictError{
			ExpectedRevision: e.ExpectedRevision,
			CurrentRevision:  e.CurrentRevision,
			CurrentState:     e.CurrentState,
		},
	}
}

func ValidPairingCommand(command PairingCommand) bool {
	return validPairingCommand(command)
}

func ValidPairingCommandHeader(command PairingCommand) bool {
	return validPairingCommandHeader(command)
}

func ValidManualPairingCommand(command PairingCommand) bool {
	return validManualPairingCommand(command)
}

func ValidParticipantPair(pair ParticipantPair) bool {
	return validParticipantPair(pair)
}

func ValidManualBye(byeParticipantID *uuid.UUID, participants map[uuid.UUID]struct{}) bool {
	return validManualBye(byeParticipantID, participants)
}

func ValidCategories(categories []domain.Category) bool {
	return validCategories(categories)
}

func ValidSwissStanding(standing SwissStandingView, total int) bool {
	return validSwissStanding(standing, total)
}

func validPairingCommand(command PairingCommand) bool {
	if !validPairingCommandHeader(command) {
		return false
	}
	if command.PairingMode == PairingModeAutomatic {
		return !command.ManualPairingsProvided && command.ManualByeParticipantID == nil
	}
	return validManualPairingCommand(command)
}

func validPairingCommandHeader(command PairingCommand) bool {
	return validCommandScope(command.CommandScope) && command.ExpectedProjectionRevision >= 1 &&
		command.RoundNumber >= 1 && command.RoundNumber <= 4 && command.PairingMode.valid() &&
		command.CategoryMode.IsValid() && validCategories(command.Categories)
}

func validManualPairingCommand(command PairingCommand) bool {
	if !command.ManualPairingsProvided || len(command.ManualPairings) < 2 || len(command.ManualPairings) > 8 {
		return false
	}
	participants := make(map[uuid.UUID]struct{}, len(command.ManualPairings)*2+1)
	for _, pair := range command.ManualPairings {
		if !validParticipantPair(pair) || !addUniqueID(participants, pair.FirstParticipantID) ||
			!addUniqueID(participants, pair.SecondParticipantID) {
			return false
		}
	}
	return validManualBye(command.ManualByeParticipantID, participants)
}

func validParticipantPair(pair ParticipantPair) bool {
	return pair.FirstParticipantID != uuid.Nil && pair.SecondParticipantID != uuid.Nil &&
		pair.FirstParticipantID != pair.SecondParticipantID
}

func validManualBye(byeParticipantID *uuid.UUID, participants map[uuid.UUID]struct{}) bool {
	if byeParticipantID == nil {
		return true
	}
	return *byeParticipantID != uuid.Nil && addUniqueID(participants, *byeParticipantID)
}

func (mode PairingMode) valid() bool {
	return mode == PairingModeAutomatic || mode == PairingModeManual
}

func validCategories(categories []domain.Category) bool {
	if len(categories) == 0 {
		return false
	}
	seen := make(map[domain.Category]struct{}, len(categories))
	for _, category := range categories {
		if !category.IsValid() {
			return false
		}
		if _, duplicate := seen[category]; duplicate {
			return false
		}
		seen[category] = struct{}{}
	}
	return true
}

func validSwissStanding(standing SwissStandingView, total int) bool {
	return standing.ParticipantID != uuid.Nil && standing.Position >= 1 && standing.Position <= total &&
		standing.Points >= 0 && standing.Buchholz >= 0 && standing.HeadToHeadPoints >= 0 &&
		standing.EffectiveTimeMS >= 0 && standing.StableSeed >= 1 && standing.StableSeed <= total &&
		(standing.PointsLabel == "provisional" || standing.PointsLabel == "final") &&
		(standing.BuchholzStatus == "provisional" || standing.BuchholzStatus == "final") &&
		(standing.AcceptedSolveTimeMS == nil || *standing.AcceptedSolveTimeMS >= 0)
}

func validCommandScope(scope operationusecase.CommandScope) bool {
	return scope.Operator.ActorID != uuid.Nil && scope.TournamentID != uuid.Nil && scope.CommandID != uuid.Nil
}

func addUniqueID(values map[uuid.UUID]struct{}, value uuid.UUID) bool {
	if _, exists := values[value]; exists {
		return false
	}
	values[value] = struct{}{}
	return true
}
