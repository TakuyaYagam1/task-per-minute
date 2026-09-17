package semifinal

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	top4usecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff/top4"
)

const (
	maxSemifinalBracketPayload   = 32 << 10
	finalSwissTop4Cutoff         = 4
	maxPlayoffReservedIdentities = 65536
)

var ErrInvalidSemifinalBracket = errors.New("invalid strength-matched semifinal bracket")

type Top4Snapshot = top4usecase.Top4Snapshot
type Top4Participant = top4usecase.Top4Participant

type SemifinalWinnerPath string

const SemifinalWinnerToFinal SemifinalWinnerPath = "final"

type SemifinalLoserPath string

const SemifinalLoserEliminated SemifinalLoserPath = "eliminated"

type SemifinalMatch struct {
	Position   int
	Series     domain.Series
	WinnerPath SemifinalWinnerPath
	LoserPath  SemifinalLoserPath
}

type SemifinalBracketCommand struct {
	TournamentID uuid.UUID
	RevisionID   domain.DerivedRevisionID
	RevisionNo   int
	Previous     *SemifinalBracket
	Top4         Top4Snapshot
	SeriesIDs    [2]uuid.UUID
	CreatedAt    time.Time
}

type semifinalBracketAuthority struct {
	TournamentID uuid.UUID
	RevisionID   domain.DerivedRevisionID
	RevisionNo   int
	Previous     *semifinalBracketPredecessorReceipt
	Top4         Top4Snapshot
	SeriesIDs    [2]uuid.UUID
	CreatedAt    time.Time
}

type semifinalBracketPredecessorReceipt struct {
	Projection     domain.ProjectionRevision
	Top4RevisionID domain.DerivedRevisionID
	Reserved       []uuid.UUID
	Semifinals     []SemifinalMatch
	LockedAt       time.Time
}

type semifinalBracketState struct {
	Authority    semifinalBracketAuthority
	Projection   domain.ProjectionRevision
	Dependencies []domain.RevisionDependency
	Semifinals   []SemifinalMatch
	LockedAt     time.Time
}

type SemifinalBracket struct {
	state semifinalBracketState
}

// The persistence adapter must publish this plan with one CAS over the current
// Top 4 and predecessor bracket heads.
func PlanStrengthMatchedSemifinals(command SemifinalBracketCommand) (SemifinalBracket, error) {
	authority, err := canonicalSemifinalBracketAuthority(command)
	if err != nil {
		return SemifinalBracket{}, err
	}
	bracket, err := buildSemifinalBracket(authority)
	if err != nil {
		return SemifinalBracket{}, err
	}
	return bracket.Snapshot(), nil
}

func (b SemifinalBracket) Validate() error {
	if b.state.Authority.TournamentID == uuid.Nil {
		return semifinalBracketError("missing bracket state")
	}
	rebuilt, err := buildSemifinalBracket(cloneSemifinalBracketAuthority(b.state.Authority))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(b.state, rebuilt.state) {
		return semifinalBracketError("bracket evidence changed")
	}
	return nil
}

func (b SemifinalBracket) Snapshot() SemifinalBracket {
	return SemifinalBracket{state: cloneSemifinalBracketState(b.state)}
}

func (b SemifinalBracket) Projection() domain.ProjectionRevision {
	return cloneSemifinalProjection(b.state.Projection)
}

func (b SemifinalBracket) Dependencies() []domain.RevisionDependency {
	return append([]domain.RevisionDependency(nil), b.state.Dependencies...)
}

func (b SemifinalBracket) Semifinals() []SemifinalMatch {
	return cloneSemifinalMatches(b.state.Semifinals)
}

func (b SemifinalBracket) Locked() bool {
	return !b.state.LockedAt.IsZero()
}

func (b SemifinalBracket) LockedAt() time.Time {
	return b.state.LockedAt
}

func (b SemifinalBracket) HasLowerBracket() bool {
	return false
}

// CloneSemifinalMatches returns detached match evidence for neighboring
// semifinal workflows without exposing bracket authority internals.
func CloneSemifinalMatches(input []SemifinalMatch) []SemifinalMatch {
	return cloneSemifinalMatches(input)
}

func semifinalBracketError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSemifinalBracket, fmt.Sprintf(format, arguments...))
}

func validSemifinalTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Equal(value.Round(0))
}

func reservePlayoffIdentity(reserved map[uuid.UUID]struct{}, id uuid.UUID) bool {
	if id == uuid.Nil {
		return false
	}
	if _, exists := reserved[id]; exists {
		return true
	}
	if len(reserved) >= maxPlayoffReservedIdentities {
		return false
	}
	reserved[id] = struct{}{}
	return true
}

func mergePlayoffReservedIdentities(reserved map[uuid.UUID]struct{}, retained []uuid.UUID) bool {
	if len(reserved) > maxPlayoffReservedIdentities || len(retained) > maxPlayoffReservedIdentities {
		return false
	}
	for _, id := range retained {
		if !reservePlayoffIdentity(reserved, id) {
			return false
		}
	}
	return true
}

func cloneSemifinalProjection(input domain.ProjectionRevision) domain.ProjectionRevision {
	revision := input.Revision()
	clone, err := domain.NewProjectionRevision(
		revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
		revision.PreviousRevisionID(), revision.CreatedAt(), input.Payload(),
	)
	if err != nil {
		return domain.ProjectionRevision{}
	}
	return clone
}

func cloneTerminalSeries(value domain.Series) domain.Series {
	clone := value
	clone.WinnerID = cloneTerminalSeriesUUIDPointer(value.WinnerID)
	clone.CurrentScoreRevisionID = cloneTerminalSeriesScoreRevisionID(value.CurrentScoreRevisionID)
	clone.CurrentResultRevisionID = cloneTerminalSeriesResultRevisionID(value.CurrentResultRevisionID)
	clone.Slots = make([]domain.GameSlot, len(value.Slots))
	for index := range value.Slots {
		clone.Slots[index] = cloneTerminalSeriesGameSlot(value.Slots[index])
	}
	return clone
}

func cloneTerminalSeriesGameSlot(value domain.GameSlot) domain.GameSlot {
	clone := value
	clone.Attempts = make([]domain.Game, len(value.Attempts))
	for index, attempt := range value.Attempts {
		clone.Attempts[index] = attempt
		clone.Attempts[index].WinnerID = cloneTerminalSeriesUUIDPointer(attempt.WinnerID)
		clone.Attempts[index].ResultRevisionID = cloneTerminalSeriesResultRevisionID(attempt.ResultRevisionID)
	}
	return clone
}

func cloneTerminalSeriesUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTerminalSeriesScoreRevisionID(value *domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTerminalSeriesResultRevisionID(value *domain.OfficialResultRevisionID) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
