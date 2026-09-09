package playoff

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

// ProgressionSwissRound is the locked, normalized input for one completed
// Swiss round. The bridge converts it to the package-private final Swiss
// authority consumed by the existing planner.
type ProgressionSwissRound struct {
	RoundID     uuid.UUID
	RoundNumber int
	RevisionID  uuid.UUID
	LockProof   swissusecase.RoundLockProof
	Series      []TerminalSeriesEvidence
	Bye         *swissusecase.ByePointResult
}

// ProgressionSwissInput contains only server-owned normalized authority. A
// persistence adapter must load it while holding the stage lock.
type ProgressionSwissInput struct {
	TournamentID               uuid.UUID
	Preset                     domain.TournamentPreset
	ProjectionID               uuid.UUID
	RevisionID                 domain.DerivedRevisionID
	RevisionNo                 int
	PhysicalProjectionRevision int
	Previous                   *FinalSwissProjection
	ParticipantIDs             []uuid.UUID
	Seeds                      []swissusecase.ParticipantSeed
	Rounds                     []ProgressionSwissRound
	GoldenGroups               []FinalSwissGoldenGroupIdentity
	CreatedAt                  time.Time
}

// ImpactfulGoldenTieRange is an identity-free, canonical tie range from
// locked Swiss authority. The use case assigns new Golden identities
// only after this range has been derived.
type ImpactfulGoldenTieRange struct {
	PositionFrom int
	PositionTo   int
}

// DeriveImpactfulGoldenTieRanges derives only the exact canonical ranges. It
// rejects identities so a persistence adapter cannot choose Golden IDs before
// the lifecycle use case allocates them.
func DeriveImpactfulGoldenTieRanges(input ProgressionSwissInput) ([]ImpactfulGoldenTieRange, error) {
	if len(input.GoldenGroups) != 0 {
		return nil, finalSwissError("identity-free Swiss input contains Golden identities")
	}
	authority, err := canonicalFinalSwissAuthority(progressionFinalSwissCommand(input))
	if err != nil {
		return nil, err
	}
	canonical, err := deriveFinalSwissCanonicalMaterialization(authority)
	if err != nil {
		return nil, err
	}
	ranges := make([]ImpactfulGoldenTieRange, 0, len(canonical.TieGroups))
	for _, tie := range canonical.TieGroups {
		if tie.Impactful {
			ranges = append(ranges, ImpactfulGoldenTieRange{
				PositionFrom: tie.PositionFrom,
				PositionTo:   tie.PositionTo,
			})
		}
	}
	return ranges, nil
}

// PlanFinalSwissProgression delegates canonical standings, tie partitioning,
// and Golden topology construction to PlanFinalSwissProjection. It exists so
// progression adapters cannot construct or depend on private planner records.
func PlanFinalSwissProgression(input ProgressionSwissInput) (FinalSwissProjection, error) {
	return PlanFinalSwissProjection(progressionFinalSwissCommand(input))
}

func progressionFinalSwissCommand(input ProgressionSwissInput) FinalSwissProjectionCommand {
	command := FinalSwissProjectionCommand{
		TournamentID:               input.TournamentID,
		Preset:                     input.Preset,
		ProjectionID:               input.ProjectionID,
		RevisionID:                 input.RevisionID,
		RevisionNo:                 input.RevisionNo,
		PhysicalProjectionRevision: input.PhysicalProjectionRevision,
		ParticipantIDs:             append([]uuid.UUID(nil), input.ParticipantIDs...),
		Seeds:                      append([]swissusecase.ParticipantSeed(nil), input.Seeds...),
		GoldenGroups:               append([]FinalSwissGoldenGroupIdentity(nil), input.GoldenGroups...),
		CreatedAt:                  input.CreatedAt,
		Rounds:                     make([]FinalSwissRound, len(input.Rounds)),
	}
	if input.Previous != nil {
		previous := input.Previous.Snapshot()
		command.Previous = &previous
	}
	for index, round := range input.Rounds {
		command.Rounds[index] = FinalSwissRound{
			RoundID:     round.RoundID,
			RoundNumber: round.RoundNumber,
			RevisionID:  round.RevisionID,
			LockProof:   swissusecase.CloneRoundLockProof(round.LockProof),
			Series:      cloneTerminalSeriesEvidence(round.Series),
			Bye:         cloneProgressionBye(round.Bye),
		}
	}
	return command
}

func cloneTerminalSeriesEvidence(input []TerminalSeriesEvidence) []TerminalSeriesEvidence {
	result := make([]TerminalSeriesEvidence, len(input))
	for index, evidence := range input {
		result[index] = evidence.Snapshot()
	}
	return result
}

func cloneProgressionBye(input *swissusecase.ByePointResult) *swissusecase.ByePointResult {
	if input == nil {
		return nil
	}
	clone := *input
	return &clone
}
