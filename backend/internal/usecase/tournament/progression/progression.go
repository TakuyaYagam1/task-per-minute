// Stage progression owns the atomic admission path from final Swiss evidence
// into Golden or playoffs. The caller must already hold the lifecycle authority
// lock. Repository implementations then acquire mutable evidence in this order:
// Swiss waves, Series, result heads, Golden attempts, Golden position commits,
// and projection artifacts.
package progression

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

type Action string

const (
	ActionStartGolden   Action = "start_golden"
	ActionStartPlayoffs Action = "start_playoffs"
)

// Command is server-scoped. CommandID is both the lifecycle idempotency key
// and the namespace for all newly allocated stage identities.
type Command struct {
	CommandID                  uuid.UUID
	TournamentID               uuid.UUID
	RosterID                   uuid.UUID
	ActorID                    uuid.UUID
	ExpectedProjectionRevision int64
	Action                     Action
}

// Authority is the already locked lifecycle row. It deliberately contains no
// client supplied stage proof.
type Authority struct {
	Tournament           usecase.TournamentView
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
}

type ProjectionReference struct {
	ArtifactID uuid.UUID
	RevisionID uuid.UUID
	Revision   int64
	Kind       domain.ArtifactKind
	Digest     [sha256.Size]byte
	// Payload contains server-rebuilt canonical bytes matching Digest.
	Payload []byte
	Members []ProjectionMember
}

type ProjectionMember struct {
	ParticipantID uuid.UUID
	Position      int
	ScoreMilli    *int64
}

// SwissEvidence is loaded only under the lifecycle lock. Counts are retained
// separately from the planner input so an adapter cannot conceal an omitted
// wave or Series behind a syntactically valid partial ledger.
type SwissEvidence struct {
	Current        ProjectionReference
	ExpectedRounds int
	TerminalRounds int
	ExpectedWaves  int
	TerminalWaves  int
	ExpectedSeries int
	TerminalSeries int
	FinalSwiss     playoff.ProgressionSwissInput
	Canonical      resultprojection.CanonicalMaterializationInput
}

// GoldenEvidence extends final Swiss evidence with only terminal, normalized
// Golden settlement authority. The existing playoff planner validates exact
// group membership and position allocation.
type GoldenEvidence struct {
	Swiss                 SwissEvidence
	CurrentTerminalSeries []playoff.TerminalSeriesEvidence
	Settlements           []playoff.Top4GoldenSettlement
}

type TieGroupRecord struct {
	GroupID      uuid.UUID
	RevisionID   uuid.UUID
	PositionFrom int
	PositionTo   int
	Participants []uuid.UUID
	Proof        json.RawMessage
	ProofDigest  [sha256.Size]byte
}

type Record struct {
	Command     Command
	Source      ProjectionReference
	SourceState domain.TournamentState
	Proof       json.RawMessage
	ProofDigest [sha256.Size]byte
	TieGroups   []TieGroupRecord
}

// Plan is persisted before the tournament row is transitioned. For
// StartPlayoffs, Top4 and Bracket are published by the same repository call.
// The deferred FK from Record to the lifecycle command is satisfied by the
// outer lifecycle coordinator before transaction commit.
type Plan struct {
	Record         Record
	FinalSwiss     playoff.FinalSwissProjection
	GoldenGroups   []playoff.FinalSwissGoldenGroup
	Top4           *playoff.Top4Snapshot
	Bracket        *playoff.SemifinalBracket
	PublicationIDs PlayoffPublicationIDs
}

// PlayoffPublication is the normalized projection-side receipt returned by
// the correction-owned publisher. Artifact IDs are database identities, while
// the semifinal IDs must equal the deterministic planner identities in Plan.
type PlayoffPublication struct {
	PublishedProjectionID uuid.UUID
	PublishedRevision     int64
	Top4ArtifactID        uuid.UUID
	BracketArtifactID     uuid.UUID
	SemifinalSeriesIDs    [2]uuid.UUID
}

type PersistedArtifact struct {
	ID     uuid.UUID
	Kind   domain.ArtifactKind
	Digest [sha256.Size]byte
}

// PersistenceReceipt is read back by the progression repository after its
// writes. For playoffs it proves that the source was superseded by this exact
// published revision, and that the materialized artifact rows equal planner
// output byte-for-byte by digest.
type PersistenceReceipt struct {
	CommandID                    uuid.UUID
	SourceProjectionState        string
	SourceSupersededByRevisionID uuid.UUID
	PublishedProjectionID        uuid.UUID
	PublishedRevision            int64
	Top4                         PersistedArtifact
	Bracket                      PersistedArtifact
}

type Receipt struct {
	CommandID uuid.UUID
	Result    usecase.TournamentView
}

type TransitionCommand struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	NextState        domain.TournamentState
}

// Repository methods participate in the caller's existing transaction. Find
// must be performed before any currentness CAS. Persist must CAS the locked
// tournament and source projection before writing proof, artifacts, and stage
// evidence, in that order.
type Repository interface {
	FindStageProgression(ctx context.Context, tournamentID, commandID uuid.UUID) (*Receipt, error)
	LoadSwissEvidence(ctx context.Context, authority Authority) (SwissEvidence, error)
	LoadGoldenEvidence(ctx context.Context, authority Authority) (GoldenEvidence, error)
	PersistStageProgression(
		ctx context.Context,
		plan Plan,
		publication *PlayoffPublication,
	) (PersistenceReceipt, error)
}

// SwissTerminalEvidenceReader is implemented by the correction owner. It
// turns the already locked normalized result, score, game-head, and logical
// projection rows into the existing playoff input. It must never manufacture
// terminal proof from revision identifiers alone.
//
// The reader is invoked after Repository.LoadSwissEvidence has locked Swiss
// rounds, Waves, Series, and their result heads. It must retain that lock
// order and use the full sources named in tournament_progression.sql.
type SwissTerminalEvidenceReader interface {
	LoadLockedSwissTerminalEvidence(
		ctx context.Context,
		command Command,
		authority Authority,
	) (playoff.ProgressionSwissInput, error)
}

// PlayoffProjectionPublisher is implemented by the correction owner. It
// writes the current Top4 and bracket projection evidence and the two locked
// semifinal Series, but never changes the tournament lifecycle state.
type PlayoffProjectionPublisher interface {
	PublishPlayoffStage(ctx context.Context, plan Plan) (PlayoffPublication, error)
}

type Transitioner interface {
	TransitionStage(ctx context.Context, command TransitionCommand) (usecase.TournamentView, bool, error)
}

type ProgressionClock interface {
	Now() time.Time
}

// Service is the lifecycle-facing progression port.
type ProgressionService interface {
	Advance(ctx context.Context, command Command, authority Authority) (Receipt, error)
}

type ProgressionDependencies struct {
	Repository       Repository
	TerminalEvidence SwissTerminalEvidenceReader
	Transitioner     Transitioner
	Publisher        PlayoffProjectionPublisher
	ProgressionClock ProgressionClock
}

type Workflow struct {
	repository       Repository
	terminalEvidence SwissTerminalEvidenceReader
	transitioner     Transitioner
	publisher        PlayoffProjectionPublisher
	clock            ProgressionClock
}

func NewWorkflow(dependencies ProgressionDependencies) *Workflow {
	return &Workflow{
		repository:       dependencies.Repository,
		terminalEvidence: dependencies.TerminalEvidence,
		transitioner:     dependencies.Transitioner,
		publisher:        dependencies.Publisher,
		clock:            dependencies.ProgressionClock,
	}
}

func (w *Workflow) Advance(ctx context.Context, command Command, authority Authority) (Receipt, error) {
	if ctx == nil || !validCommand(command) || !validAuthority(authority, command) {
		return Receipt{}, domain.ErrValidation
	}
	if w == nil || w.repository == nil {
		return Receipt{}, domain.ErrInternal
	}

	recorded, err := w.repository.FindStageProgression(ctx, command.TournamentID, command.CommandID)
	if err != nil {
		return Receipt{}, err
	}
	if recorded != nil {
		if recorded.CommandID != command.CommandID || !validTournamentResult(recorded.Result, authority, command.Action) {
			return Receipt{}, domain.ErrConflict
		}
		return Receipt{CommandID: recorded.CommandID, Result: progressionCloneTournamentView(recorded.Result)}, nil
	}
	if w.terminalEvidence == nil || w.transitioner == nil || w.clock == nil {
		return Receipt{}, domain.ErrInternal
	}

	now := w.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(now) || now.Before(authority.Tournament.UpdatedAt) {
		return Receipt{}, domain.ErrValidation
	}

	var plan Plan
	switch command.Action {
	case ActionStartGolden:
		evidence, loadErr := w.loadSwissEvidence(ctx, command, authority)
		if loadErr != nil {
			return Receipt{}, loadErr
		}
		plan, err = planStartGolden(command, authority, evidence, now)
	case ActionStartPlayoffs:
		if authority.Tournament.State == domain.TournamentStateSwiss {
			evidence, loadErr := w.loadSwissEvidence(ctx, command, authority)
			if loadErr != nil {
				return Receipt{}, loadErr
			}
			plan, err = planStartPlayoffsFromSwiss(command, authority, evidence, now)
		} else {
			evidence, loadErr := w.repository.LoadGoldenEvidence(ctx, authority)
			if loadErr != nil {
				return Receipt{}, loadErr
			}
			evidence.Swiss, loadErr = w.hydrateSwissEvidence(ctx, command, authority, evidence.Swiss)
			if loadErr != nil {
				return Receipt{}, loadErr
			}
			plan, err = planStartPlayoffsFromGolden(command, authority, evidence, now)
		}
	default:
		return Receipt{}, domain.ErrValidation
	}
	if err != nil {
		return Receipt{}, err
	}
	var publication *PlayoffPublication
	if command.Action == ActionStartPlayoffs {
		if w.publisher == nil {
			return Receipt{}, domain.ErrInternal
		}
		published, publishErr := w.publisher.PublishPlayoffStage(ctx, plan)
		if publishErr != nil {
			return Receipt{}, publishErr
		}
		if !matchesPlayoffPublication(plan, published) {
			return Receipt{}, domain.ErrConflict
		}
		publication = &published
	}
	persisted, err := w.repository.PersistStageProgression(ctx, plan, publication)
	if err != nil {
		return Receipt{}, err
	}
	if !matchesPersistence(plan, publication, persisted) {
		return Receipt{}, domain.ErrConflict
	}

	next, _ := actionState(command.Action)
	result, changed, err := w.transitioner.TransitionStage(ctx, TransitionCommand{
		TournamentID: command.TournamentID, ExpectedRevision: authority.Tournament.Revision, NextState: next,
	})
	if err != nil {
		return Receipt{}, err
	}
	if !changed || !validTournamentResult(result, authority, command.Action) {
		return Receipt{}, domain.ErrConflict
	}
	return Receipt{CommandID: command.CommandID, Result: progressionCloneTournamentView(result)}, nil
}

func (w *Workflow) loadSwissEvidence(
	ctx context.Context,
	command Command,
	authority Authority,
) (SwissEvidence, error) {
	evidence, err := w.repository.LoadSwissEvidence(ctx, authority)
	if err != nil {
		return SwissEvidence{}, err
	}
	return w.hydrateSwissEvidence(ctx, command, authority, evidence)
}

func (w *Workflow) hydrateSwissEvidence(
	ctx context.Context,
	command Command,
	authority Authority,
	evidence SwissEvidence,
) (SwissEvidence, error) {
	terminal, err := w.terminalEvidence.LoadLockedSwissTerminalEvidence(ctx, command, authority)
	if err != nil {
		return SwissEvidence{}, err
	}
	terminal, err = prepareSwissTerminalInput(command, authority, terminal)
	if err != nil {
		return SwissEvidence{}, err
	}
	evidence.FinalSwiss = terminal
	return evidence, nil
}

// prepareSwissTerminalInput keeps Golden identity allocation in the
// use case. A PostgreSQL reader supplies only locked normalized
// Swiss authority for the first transition. Once Golden exists, the reader
// must instead return the persisted group identities, which are checked
// against the canonical ranges before the final planner sees them.
func prepareSwissTerminalInput(
	command Command,
	authority Authority,
	terminal playoff.ProgressionSwissInput,
) (playoff.ProgressionSwissInput, error) {
	switch authority.Tournament.State {
	case domain.TournamentStateSwiss:
		if len(terminal.GoldenGroups) != 0 {
			return playoff.ProgressionSwissInput{}, progressionConflict(
				"Swiss terminal reader supplied Golden identities before stage creation",
			)
		}
		ranges, err := playoff.DeriveImpactfulGoldenTieRanges(terminal)
		if err != nil {
			return playoff.ProgressionSwissInput{}, progressionConflict(
				"derive exact impactful Swiss ties: %v", err,
			)
		}
		return prepareSwissTerminalIdentityRanges(command, authority.Tournament.State, terminal, ranges)
	case domain.TournamentStateGolden:
		identityFree := terminal
		identityFree.GoldenGroups = nil
		ranges, err := playoff.DeriveImpactfulGoldenTieRanges(identityFree)
		if err != nil {
			return playoff.ProgressionSwissInput{}, progressionConflict(
				"derive persisted Golden tie ranges: %v", err,
			)
		}
		return prepareSwissTerminalIdentityRanges(command, authority.Tournament.State, terminal, ranges)
	default:
		return playoff.ProgressionSwissInput{}, domain.ErrValidation
	}
}

func prepareSwissTerminalIdentityRanges(
	command Command,
	state domain.TournamentState,
	terminal playoff.ProgressionSwissInput,
	ranges []playoff.ImpactfulGoldenTieRange,
) (playoff.ProgressionSwissInput, error) {
	switch state {
	case domain.TournamentStateSwiss:
		if len(terminal.GoldenGroups) != 0 {
			return playoff.ProgressionSwissInput{}, progressionConflict(
				"Swiss terminal reader supplied Golden identities before stage creation",
			)
		}
		switch command.Action {
		case ActionStartGolden:
			terminal.GoldenGroups = serverGoldenGroupIdentities(command.CommandID, ranges)
		case ActionStartPlayoffs:
			// Direct Swiss playoffs deliberately retain no Golden identities.
		default:
			return playoff.ProgressionSwissInput{}, domain.ErrValidation
		}
	case domain.TournamentStateGolden:
		if command.Action != ActionStartPlayoffs ||
			!matchesPersistedGoldenIdentityRanges(terminal.GoldenGroups, ranges) {
			return playoff.ProgressionSwissInput{}, progressionConflict(
				"persisted Golden identities do not match exact Swiss ties",
			)
		}
	default:
		return playoff.ProgressionSwissInput{}, domain.ErrValidation
	}
	return terminal, nil
}

func serverGoldenGroupIdentities(
	commandID uuid.UUID,
	ranges []playoff.ImpactfulGoldenTieRange,
) []playoff.FinalSwissGoldenGroupIdentity {
	identities := make([]playoff.FinalSwissGoldenGroupIdentity, len(ranges))
	for index, tie := range ranges {
		identities[index] = playoff.FinalSwissGoldenGroupIdentity{
			PositionFrom: tie.PositionFrom,
			PositionTo:   tie.PositionTo,
			GroupID:      progressionID(commandID, goldenGroupRole(tie.PositionFrom, tie.PositionTo, "id")),
			RevisionID: domain.DerivedRevisionID(
				progressionID(commandID, goldenGroupRole(tie.PositionFrom, tie.PositionTo, "revision")),
			),
		}
	}
	return identities
}

func matchesPersistedGoldenIdentityRanges(
	identities []playoff.FinalSwissGoldenGroupIdentity,
	ranges []playoff.ImpactfulGoldenTieRange,
) bool {
	if len(identities) != len(ranges) {
		return false
	}
	remaining := make(map[[2]int]struct{}, len(ranges))
	for _, tie := range ranges {
		remaining[[2]int{tie.PositionFrom, tie.PositionTo}] = struct{}{}
	}
	for _, identity := range identities {
		key := [2]int{identity.PositionFrom, identity.PositionTo}
		if identity.GroupID == uuid.Nil || identity.RevisionID.IsZero() {
			return false
		}
		if _, found := remaining[key]; !found {
			return false
		}
		delete(remaining, key)
	}
	return len(remaining) == 0
}

func matchesPlayoffPublication(plan Plan, publication PlayoffPublication) bool {
	if plan.Top4 == nil || plan.Bracket == nil || !plan.PublicationIDs.Valid() ||
		publication.PublishedProjectionID != plan.PublicationIDs.ProjectionRevisionID ||
		publication.PublishedRevision < 1 || publication.Top4ArtifactID == uuid.Nil ||
		publication.BracketArtifactID == uuid.Nil ||
		publication.Top4ArtifactID != plan.PublicationIDs.Top4ArtifactID ||
		publication.BracketArtifactID != plan.PublicationIDs.BracketArtifactID {
		return false
	}
	matches := plan.Bracket.Semifinals()
	return len(matches) == len(publication.SemifinalSeriesIDs) &&
		matches[0].Series.ID == publication.SemifinalSeriesIDs[0] &&
		matches[1].Series.ID == publication.SemifinalSeriesIDs[1]
}

func matchesPersistence(plan Plan, publication *PlayoffPublication, persisted PersistenceReceipt) bool {
	if persisted.CommandID != plan.Record.Command.CommandID {
		return false
	}
	if plan.Record.Command.Action == ActionStartGolden {
		return publication == nil && persisted.SourceProjectionState == "published" &&
			persisted.SourceSupersededByRevisionID == uuid.Nil && persisted.PublishedProjectionID == uuid.Nil &&
			persisted.PublishedRevision == 0 && persisted.Top4.ID == uuid.Nil && persisted.Bracket.ID == uuid.Nil
	}
	if plan.Record.Command.Action != ActionStartPlayoffs || publication == nil || plan.Top4 == nil || plan.Bracket == nil ||
		!matchesPlayoffPersistence(*publication, persisted, plan.Top4.Projection(), plan.Bracket.Projection()) {
		return false
	}
	return true
}

func matchesPlayoffPersistence(
	publication PlayoffPublication,
	persisted PersistenceReceipt,
	top4 domain.ProjectionRevision,
	bracket domain.ProjectionRevision,
) bool {
	top4Revision := top4.Revision()
	bracketRevision := bracket.Revision()
	return top4.Validate() == nil && bracket.Validate() == nil &&
		top4Revision.Artifact().Kind == domain.ArtifactKindTopFour &&
		bracketRevision.Artifact().Kind == domain.ArtifactKindBracket &&
		isExactPublishedSupersession(persisted, publication) &&
		persisted.PublishedProjectionID == publication.PublishedProjectionID &&
		persisted.PublishedRevision == publication.PublishedRevision &&
		persisted.Top4.ID == publication.Top4ArtifactID &&
		persisted.Bracket.ID == publication.BracketArtifactID &&
		persisted.Top4.Kind == domain.ArtifactKindTopFour &&
		persisted.Bracket.Kind == domain.ArtifactKindBracket &&
		persisted.Top4.Digest == top4Revision.PayloadDigest() &&
		persisted.Bracket.Digest == bracketRevision.PayloadDigest()
}

func isExactPublishedSupersession(receipt PersistenceReceipt, publication PlayoffPublication) bool {
	return receipt.SourceProjectionState == "superseded" &&
		receipt.SourceSupersededByRevisionID == publication.PublishedProjectionID
}

func actionState(action Action) (domain.TournamentState, bool) {
	switch action {
	case ActionStartGolden:
		return domain.TournamentStateGolden, true
	case ActionStartPlayoffs:
		return domain.TournamentStatePlayoffs, true
	default:
		return "", false
	}
}

func validCommand(command Command) bool {
	_, validAction := actionState(command.Action)
	return command.CommandID != uuid.Nil && command.TournamentID != uuid.Nil && command.RosterID != uuid.Nil &&
		command.ActorID != uuid.Nil && command.ExpectedProjectionRevision >= 1 && validAction
}

func validAuthority(authority Authority, command Command) bool {
	return authority.Tournament.ID == command.TournamentID && authority.Tournament.RosterID == command.RosterID &&
		authority.Tournament.Revision >= 1 && authority.ProjectionRevisionID != uuid.Nil &&
		authority.ProjectionRevision == command.ExpectedProjectionRevision && authority.ProjectionRevision >= 1
}

func validTournamentResult(
	result usecase.TournamentView,
	authority Authority,
	action Action,
) bool {
	next, exists := actionState(action)
	return exists && result.ID == authority.Tournament.ID && result.RosterID == authority.Tournament.RosterID &&
		result.State == next && result.PausedFromState == nil &&
		result.Revision == authority.Tournament.Revision+1 && result.UpdatedAt.After(authority.Tournament.UpdatedAt)
}

func progressionCloneTournamentView(input usecase.TournamentView) usecase.TournamentView {
	clone := input
	if input.PausedFromState != nil {
		value := *input.PausedFromState
		clone.PausedFromState = &value
	}
	if input.StartedAt != nil {
		value := *input.StartedAt
		clone.StartedAt = &value
	}
	if input.FinishedAt != nil {
		value := *input.FinishedAt
		clone.FinishedAt = &value
	}
	return clone
}

var _ ProgressionService = (*Workflow)(nil)
