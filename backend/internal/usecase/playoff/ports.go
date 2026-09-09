package playoff

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

const maxSemifinalBracketPayload = 32 << 10

var ErrInvalidSemifinalBracket = errors.New("invalid strength-matched semifinal bracket")

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
	return cloneFinalSwissDomainProjection(b.state.Projection)
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

func semifinalBracketError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSemifinalBracket, fmt.Sprintf(format, arguments...))
}

// TerminalSeriesCommand is intentionally transport-neutral. The repository
// resolves the current stage only after the enclosing settlement has written
// the authoritative game, score, and series heads.
type TerminalSeriesCommand struct {
	TournamentID uuid.UUID
	SeriesID     uuid.UUID
}

// TerminalDraftCommand is invoked only after a draft action has been written.
// The repository proves that this is the final draft and that its completion
// revision is still authoritative.
type TerminalDraftCommand struct {
	TournamentID uuid.UUID
	SeriesID     uuid.UUID
	DraftID      uuid.UUID
	CommandID    uuid.UUID
}

// FinalStageIDs are all identities reserved from the immutable playoff stage
// command. They are deterministic, so retries cannot allocate another final
// graph.
type FinalStageIDs struct {
	FinalSeriesID             uuid.UUID
	CategoryRevisionID        uuid.UUID
	DraftID                   uuid.UUID
	DraftInitialRevisionID    uuid.UUID
	DraftOrderDecisionID      uuid.UUID
	DraftServiceEpochID       uuid.UUID
	DraftAssignmentPlanID     uuid.UUID
	DraftAssignmentRevisionID uuid.UUID
	InitialScoreRevisionID    domain.SeriesScoreRevisionID
	FirstSlotID               uuid.UUID
	FirstGameID               uuid.UUID
	FirstWaveID               uuid.UUID
	FirstWaveRevisionID       domain.WaveRevisionID
	SecondSlotID              uuid.UUID
	SecondGameID              uuid.UUID
	SecondWaveID              uuid.UUID
	SecondWaveRevisionID      domain.WaveRevisionID
	ThirdSlotID               uuid.UUID
	ThirdGameID               uuid.UUID
	ThirdWaveID               uuid.UUID
	ThirdWaveRevisionID       domain.WaveRevisionID
}

// FinalPublicationIDs are derived from the immutable terminal Game result
// revision. A retry of the same settled Game therefore addresses the exact
// same champion revision, projection revision, artifacts, and dependencies.
type FinalPublicationIDs struct {
	ChampionRevisionID          domain.DerivedRevisionID
	ProjectionRevisionID        uuid.UUID
	CutoffID                    uuid.UUID
	StandingsArtifactID         uuid.UUID
	BracketArtifactID           uuid.UUID
	TopFourArtifactID           uuid.UUID
	ChampionArtifactID          uuid.UUID
	StandingsDependencyID       uuid.UUID
	BracketDependencyID         uuid.UUID
	TopFourDependencyID         uuid.UUID
	ChampionBracketDependencyID uuid.UUID
	ChampionResultDependencyID  uuid.UUID
}

// FinalGameBinding is the authoritative task binding for one locked final
// category. It is pre-reserved while the draft is active, then selected by the
// completed ordered draft branch. The coordinator never selects a task.
type FinalGameBinding struct {
	GameID             uuid.UUID
	AssignmentID       uuid.UUID
	AssignmentRevision int64
	PlanID             uuid.UUID
	PlanRevisionID     uuid.UUID
	BranchID           uuid.UUID
	ReservationID      uuid.UUID
	SnapshotID         uuid.UUID
	ContentDigest      [sha256.Size]byte
	DeadlineSeconds    int
}

// SemifinalStageAuthority is a normalized and already locked view of exact
// semifinal heads plus the final content configuration. A nil authority means
// that the settled series is not a playoff semifinal.
type SemifinalStageAuthority struct {
	StageCommandID uuid.UUID
	RosterID       uuid.UUID
	Bracket        SemifinalAdvancementAuthority
	Series         []domain.Series
	Configuration  domain.ContentConfiguration
	RecordedAt     time.Time
}

// FinalDraftPlan is the durable planned final and active draft. It contains no
// score head, slot, game, or selected category because those only exist after
// the authoritative draft completes.
type FinalDraftPlan struct {
	StageCommandID uuid.UUID
	RosterID       uuid.UUID
	Advancement    []SemifinalAdvancementResult
	Series         domain.Series
	Category       draftusecase.CategoryRevision
	Draft          draftusecase.Execution
	IDs            FinalStageIDs
	CreatedAt      time.Time
}

// FinalDraftAuthority is read after a final draft action. It includes the
// exact completed branch bindings; incomplete drafts are returned as nil.
type FinalDraftAuthority struct {
	StageCommandID         uuid.UUID
	RosterID               uuid.UUID
	Bracket                SemifinalAdvancementAuthority
	Advancement            []SemifinalAdvancementResult
	ExpectedSeriesRevision int64
	Draft                  draftusecase.Execution
	IDs                    FinalStageIDs
	RecordedAt             time.Time
}

// FinalInitialPlan is the initial score head and planned first final game
// graph produced exclusively by NewFinal after a completed draft is
// revalidated. The first Wave is then started by the ordinary readiness flow.
type FinalInitialPlan struct {
	StageCommandID         uuid.UUID
	RosterID               uuid.UUID
	ExpectedSeriesRevision int64
	Execution              seriesdomain.Execution
	InitialScoreRevisionID domain.SeriesScoreRevisionID
	CurrentWave            domain.Wave
	Binding                FinalGameBinding
	ActivatedAt            time.Time
}

// FinalSettlementAuthority rehydrates the live final from its initial graph
// and append-only prior game steps. Progression is the exact current settled
// game/score evidence. Publication is populated only for terminal results.
type FinalSettlementAuthority struct {
	StageCommandID uuid.UUID
	RosterID       uuid.UUID
	Bracket        SemifinalAdvancementAuthority
	Advancement    []SemifinalAdvancementResult
	Draft          draftusecase.Execution
	IDs            FinalStageIDs
	History        []FinalProgressionCommand
	Progression    FinalProgressionCommand
	Bindings       []FinalGameBinding
	Publication    *resultprojection.FinalPublication
	RecordedAt     time.Time
}

// FinalContinuationPlan is append-only evidence keyed by the exact current
// score revision. The next Wave and assignment are already committed before
// any later game can be settled.
type FinalContinuationPlan struct {
	StageCommandID       uuid.UUID
	RosterID             uuid.UUID
	SeriesID             uuid.UUID
	SourceScoreRevision  domain.SeriesScoreRevisionID
	SourceResultRevision domain.OfficialResultRevisionID
	Next                 seriesdomain.NextGameWave
	Slot                 domain.GameSlot
	Wave                 domain.Wave
	Binding              FinalGameBinding
	CreatedAt            time.Time
}

type TerminalReceipt struct {
	FinalSeriesID      uuid.UUID
	ProjectionRevision int64
	Changed            bool
}

// TerminalRepository participates in the caller's outer transaction. Every
// load locks exact current evidence; every persist reconciles deterministic
// identities or returns a conflict. Nil stage authorities are safe no-ops for
// ordinary series and drafts.
type TerminalRepository interface {
	LoadSemifinalStage(
		ctx context.Context,
		command TerminalSeriesCommand,
	) (*SemifinalStageAuthority, error)
	PersistFinalDraft(ctx context.Context, plan FinalDraftPlan) (bool, error)
	LoadFinalDraft(
		ctx context.Context,
		command TerminalDraftCommand,
	) (*FinalDraftAuthority, error)
	PersistFinalInitial(ctx context.Context, plan FinalInitialPlan) (bool, error)
	LoadFinalSettlement(
		ctx context.Context,
		command TerminalSeriesCommand,
	) (*FinalSettlementAuthority, error)
	PersistFinalContinuation(ctx context.Context, plan FinalContinuationPlan) (bool, error)
}

type TerminalCoordinatorDependencies struct {
	Repository   TerminalRepository
	Publisher    resultprojection.FinalPublicationRepository
	DraftPlanner FinalDraftAssignmentPlanner
	Rehydrator   FinalBindingRehydrator
}

// FinalDraftAssignmentPlanner reserves a complete normal-task plan for every
// reachable active final-draft branch, then activates exactly the completed
// branch. It returns only deterministic bindings for the resulting final
// Game graph.
type FinalDraftAssignmentPlanner interface {
	PlanFinalDraft(ctx context.Context, plan FinalDraftPlan) (bool, error)
	ActivateFinalDraft(
		ctx context.Context,
		authority FinalDraftAuthority,
		command TerminalDraftCommand,
	) ([]FinalGameBinding, bool, error)
}

// FinalBindingRehydrator rebuilds all final task bindings from the committed
// active exact-draft branch before any later Game settlement is advanced.
type FinalBindingRehydrator interface {
	RehydrateFinalBindings(
		ctx context.Context,
		authority FinalSettlementAuthority,
	) ([]FinalGameBinding, error)
}

// ExactDraftCommittedPlanReader supplies the immutable committed exact-draft
// plan to the terminal coordinator without coupling it to a persistence
// adapter.
type ExactDraftCommittedPlanReader interface {
	LoadCommittedExactDraftPlan(
		ctx context.Context,
		planID uuid.UUID,
	) (*assignmentusecase.ExactDraftBranchPlan, error)
}
