package playoff

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
)

const maxTop4Payload = 64 << 10

var (
	ErrInvalidTop4Snapshot         = errors.New("invalid Top 4 snapshot")
	ErrTop4GoldenZeroParticipation = errors.New("terminal Golden fallback has zero participation")
)

type Top4Participant struct {
	Seed          int       `json:"seed"`
	ParticipantID uuid.UUID `json:"participant_id"`
}

type Top4GoldenSettlement struct {
	RevisionID  domain.DerivedRevisionID
	RevisionNo  int
	Positions   *GoldenPositionEvidence
	State       *goldenusecase.GoldenState
	FinalizedAt time.Time
}

type Top4TerminalSeriesReference struct {
	SeriesID                 uuid.UUID
	OfficialResultRevisionID domain.OfficialResultRevisionID
	ProjectionRevisionID     domain.DerivedRevisionID
	ProjectionPayloadDigest  [sha256.Size]byte
}

type Top4SnapshotCommand struct {
	TournamentID          uuid.UUID
	RevisionID            domain.DerivedRevisionID
	RevisionNo            int
	Previous              *Top4Snapshot
	Source                FinalSwissProjection
	CurrentTerminalSeries []TerminalSeriesEvidence
	GoldenSettlements     []Top4GoldenSettlement
	CreatedAt             time.Time
}

type top4Authority struct {
	TournamentID          uuid.UUID
	RevisionID            domain.DerivedRevisionID
	RevisionNo            int
	Previous              *top4PredecessorReceipt
	Source                FinalSwissProjection
	CurrentTerminalSeries []terminalSeriesRecord
	GoldenSettlements     []Top4GoldenSettlement
	CreatedAt             time.Time
}

type top4PredecessorReceipt struct {
	Projection domain.ProjectionRevision
	Reserved   []uuid.UUID
}

type top4SnapshotState struct {
	Authority                 top4Authority
	Projection                domain.ProjectionRevision
	Participants              []Top4Participant
	Dependencies              []domain.RevisionDependency
	QualificationDependencies []domain.RevisionDependency
	QualificationProjections  []domain.ProjectionRevision
	TerminalReferences        []Top4TerminalSeriesReference
}

type Top4Snapshot struct {
	state top4SnapshotState
}

// The persistence adapter must publish this plan with one CAS over the final
// standings, terminal Series audit heads, Golden states, and predecessor Top 4.
func PlanTop4Snapshot(command Top4SnapshotCommand) (Top4Snapshot, error) {
	authority, err := canonicalTop4Authority(command)
	if err != nil {
		return Top4Snapshot{}, err
	}
	snapshot, err := buildTop4Snapshot(authority)
	if err != nil {
		return Top4Snapshot{}, err
	}
	return snapshot.Snapshot(), nil
}

func (s Top4Snapshot) Validate() error {
	if s.state.Authority.TournamentID == uuid.Nil {
		return top4Error("missing snapshot state")
	}
	rebuilt, err := buildTop4Snapshot(cloneTop4Authority(s.state.Authority))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(s.state, rebuilt.state) {
		return top4Error("snapshot evidence changed")
	}
	return nil
}

func (s Top4Snapshot) Snapshot() Top4Snapshot {
	return Top4Snapshot{state: cloneTop4State(s.state)}
}

func (s Top4Snapshot) Projection() domain.ProjectionRevision {
	return cloneFinalSwissDomainProjection(s.state.Projection)
}

func (s Top4Snapshot) Participants() []Top4Participant {
	return append([]Top4Participant(nil), s.state.Participants...)
}

func (s Top4Snapshot) Dependencies() []domain.RevisionDependency {
	return append([]domain.RevisionDependency(nil), s.state.Dependencies...)
}

func (s Top4Snapshot) QualificationDependencies() []domain.RevisionDependency {
	return append([]domain.RevisionDependency(nil), s.state.QualificationDependencies...)
}

func (s Top4Snapshot) QualificationProjections() []domain.ProjectionRevision {
	return cloneTop4Projections(s.state.QualificationProjections)
}

func (s Top4Snapshot) TerminalSeriesReferences() []Top4TerminalSeriesReference {
	return append([]Top4TerminalSeriesReference(nil), s.state.TerminalReferences...)
}

// GoldenPositionCommitIDs returns the sealed commit identities used to
// qualify this snapshot. The caller owns the returned slice.
func (s Top4Snapshot) GoldenPositionCommitIDs() []uuid.UUID {
	ids := make([]uuid.UUID, 0)
	for _, settlement := range s.state.Authority.GoldenSettlements {
		if settlement.Positions == nil {
			continue
		}
		for _, position := range settlement.Positions.Positions() {
			ids = append(ids, position.CommitID)
		}
	}
	return ids
}

func top4Error(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidTop4Snapshot, fmt.Sprintf(format, arguments...))
}

func top4ZeroParticipationError() error {
	return fmt.Errorf("%w: %w", ErrInvalidTop4Snapshot, ErrTop4GoldenZeroParticipation)
}
