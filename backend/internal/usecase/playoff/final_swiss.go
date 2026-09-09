package playoff

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

const (
	finalSwissTop4Cutoff         = 4
	maxFinalSwissPayload         = 64 << 10
	finalSwissRevisionRole       = "projection revision"
	maxPlayoffReservedIdentities = 65536
)

var ErrInvalidFinalSwissProjection = errors.New("invalid final Swiss projection")

type SwissBuchholzStatus string

const (
	SwissBuchholzProvisional SwissBuchholzStatus = "provisional"
	SwissBuchholzFinal       SwissBuchholzStatus = "final"
)

type finalSwissRound struct {
	RoundID     uuid.UUID
	RoundNumber int
	RevisionID  uuid.UUID
	LockProof   swissusecase.RoundLockProof
	Series      []terminalSeriesRecord
	Bye         *swissusecase.ByePointResult
}

type FinalSwissGoldenGroupIdentity struct {
	PositionFrom int                      `json:"position_from"`
	PositionTo   int                      `json:"position_to"`
	GroupID      uuid.UUID                `json:"group_id"`
	RevisionID   domain.DerivedRevisionID `json:"revision_id"`
}

type FinalSwissProjectionCommand struct {
	TournamentID               uuid.UUID
	Preset                     domain.TournamentPreset
	ProjectionID               uuid.UUID
	RevisionID                 domain.DerivedRevisionID
	RevisionNo                 int
	PhysicalProjectionRevision int
	Previous                   *FinalSwissProjection
	ParticipantIDs             []uuid.UUID
	Seeds                      []swissusecase.ParticipantSeed
	Rounds                     []FinalSwissRound
	GoldenGroups               []FinalSwissGoldenGroupIdentity
	CreatedAt                  time.Time
}

type FinalSwissStanding struct {
	ParticipantID     uuid.UUID                `json:"participant_id"`
	Position          int                      `json:"position"`
	Points            int                      `json:"points"`
	PointsLabel       swissusecase.PointsLabel `json:"points_label"`
	Buchholz          int                      `json:"buchholz"`
	BuchholzStatus    SwissBuchholzStatus      `json:"buchholz_status"`
	HeadToHeadPoints  int                      `json:"head_to_head_points"`
	HeadToHeadApplied bool                     `json:"head_to_head_applied"`
	EffectiveTime     time.Duration            `json:"effective_time"`
	AcceptedSolveTime *time.Duration           `json:"accepted_solve_time,omitempty"`
	StableSeed        int                      `json:"stable_seed"`
}

type FinalSwissTieGroup struct {
	PositionFrom   int         `json:"position_from"`
	PositionTo     int         `json:"position_to"`
	Impactful      bool        `json:"impactful"`
	ParticipantIDs []uuid.UUID `json:"participant_ids"`
}

type FinalSwissGoldenGroup struct {
	State      domain.GoldenGroupState
	Revision   goldenusecase.GroupRevision
	Projection domain.ProjectionRevision
	Dependency domain.RevisionDependency
}

type finalSwissAuthority struct {
	ReceiptOnly                bool
	TournamentID               uuid.UUID
	Preset                     domain.TournamentPreset
	ProjectionID               uuid.UUID
	RevisionID                 domain.DerivedRevisionID
	RevisionNo                 int
	PhysicalProjectionRevision int
	Previous                   *finalSwissPredecessorReceipt
	ParticipantIDs             []uuid.UUID
	Seeds                      []swissusecase.ParticipantSeed
	Rounds                     []finalSwissRound
	GoldenGroups               []FinalSwissGoldenGroupIdentity
	CreatedAt                  time.Time
}

type finalSwissReservedIdentity struct {
	ID   uuid.UUID
	Role string
}

type finalSwissPredecessorReceipt struct {
	Projection                 domain.ProjectionRevision
	ProjectionID               uuid.UUID
	PhysicalProjectionRevision int
	Reserved                   []finalSwissReservedIdentity
}

type finalSwissProjectionState struct {
	Authority          finalSwissAuthority
	Projection         domain.ProjectionRevision
	GoldenSource       goldenusecase.StandingsProjection
	Standings          []FinalSwissStanding
	TieGroups          []FinalSwissTieGroup
	GoldenGroups       []FinalSwissGoldenGroup
	SeriesDependencies []domain.RevisionDependency
	Dependencies       []domain.RevisionDependency
	AdvanceDirectly    bool
}

type FinalSwissProjection struct {
	state finalSwissProjectionState
}

// PlanFinalSwissReceipt binds terminal Swiss evidence before Golden identities
// are allocated by the stage command.
func PlanFinalSwissReceipt(input ProgressionSwissInput) (FinalSwissProjection, error) {
	if len(input.GoldenGroups) != 0 {
		return FinalSwissProjection{}, finalSwissError("Swiss receipt contains future Golden identities")
	}
	authority, err := canonicalFinalSwissAuthority(progressionFinalSwissCommand(input))
	if err != nil {
		return FinalSwissProjection{}, err
	}
	authority.ReceiptOnly = true
	receipt, err := buildFinalSwissProjection(authority)
	if err != nil {
		return FinalSwissProjection{}, err
	}
	return receipt.Snapshot(), nil
}

// The persistence adapter must publish this plan with one CAS over the current
// round, bye, terminal Series result, and predecessor standings heads.
func PlanFinalSwissProjection(command FinalSwissProjectionCommand) (FinalSwissProjection, error) {
	authority, err := canonicalFinalSwissAuthority(command)
	if err != nil {
		return FinalSwissProjection{}, err
	}
	projection, err := buildFinalSwissProjection(authority)
	if err != nil {
		return FinalSwissProjection{}, err
	}
	return projection.Snapshot(), nil
}

func (p FinalSwissProjection) Validate() error {
	if p.state.Authority.TournamentID == uuid.Nil {
		return finalSwissError("missing projection state")
	}
	rebuilt, err := buildFinalSwissProjection(cloneFinalSwissAuthority(p.state.Authority))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(p.state, rebuilt.state) {
		return finalSwissError("projection evidence changed")
	}
	return nil
}

func (p FinalSwissProjection) Snapshot() FinalSwissProjection {
	return FinalSwissProjection{state: cloneFinalSwissProjectionState(p.state)}
}

func (p FinalSwissProjection) Projection() domain.ProjectionRevision {
	return cloneFinalSwissDomainProjection(p.state.Projection)
}

// PhysicalProjectionRevision is the exact persisted projection_revisions
// counter. It is distinct from the canonical receipt lineage RevisionNo.
func (p FinalSwissProjection) PhysicalProjectionRevision() int {
	return p.state.Authority.PhysicalProjectionRevision
}

func (p FinalSwissProjection) GoldenSource() goldenusecase.StandingsProjection {
	return p.state.GoldenSource.Snapshot()
}

func (p FinalSwissProjection) Standings() []FinalSwissStanding {
	return cloneFinalSwissStandings(p.state.Standings)
}

func (p FinalSwissProjection) TieGroups() []FinalSwissTieGroup {
	return cloneFinalSwissTieGroups(p.state.TieGroups)
}

func (p FinalSwissProjection) GoldenGroups() []FinalSwissGoldenGroup {
	return cloneFinalSwissGoldenGroups(p.state.GoldenGroups)
}

func (p FinalSwissProjection) TerminalSeriesDependencies() []domain.RevisionDependency {
	return append([]domain.RevisionDependency(nil), p.state.SeriesDependencies...)
}

func (p FinalSwissProjection) Dependencies() []domain.RevisionDependency {
	return append([]domain.RevisionDependency(nil), p.state.Dependencies...)
}

func (p FinalSwissProjection) AdvanceDirectly() bool {
	return p.state.AdvanceDirectly
}

func finalSwissError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidFinalSwissProjection, fmt.Sprintf(format, arguments...))
}
