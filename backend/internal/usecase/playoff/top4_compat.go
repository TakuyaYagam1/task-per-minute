package playoff

import top4usecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff/top4"

var (
	ErrInvalidTop4Snapshot           = top4usecase.ErrInvalidTop4Snapshot
	ErrTop4GoldenZeroParticipation   = top4usecase.ErrTop4GoldenZeroParticipation
	ErrInvalidFinalSwissProjection   = top4usecase.ErrInvalidFinalSwissProjection
	ErrInvalidTerminalSeriesEvidence = top4usecase.ErrInvalidTerminalSeriesEvidence
	ErrInvalidGoldenPositionEvidence = top4usecase.ErrInvalidGoldenPositionEvidence
)

type Top4Participant = top4usecase.Top4Participant
type Top4GoldenSettlement = top4usecase.Top4GoldenSettlement
type Top4TerminalSeriesReference = top4usecase.Top4TerminalSeriesReference
type Top4SnapshotCommand = top4usecase.Top4SnapshotCommand
type Top4Snapshot = top4usecase.Top4Snapshot

type SwissBuchholzStatus = top4usecase.SwissBuchholzStatus

const (
	SwissBuchholzProvisional = top4usecase.SwissBuchholzProvisional
	SwissBuchholzFinal       = top4usecase.SwissBuchholzFinal
)

type FinalSwissGoldenGroupIdentity = top4usecase.FinalSwissGoldenGroupIdentity
type FinalSwissProjectionCommand = top4usecase.FinalSwissProjectionCommand
type FinalSwissStanding = top4usecase.FinalSwissStanding
type FinalSwissTieGroup = top4usecase.FinalSwissTieGroup
type FinalSwissGoldenGroup = top4usecase.FinalSwissGoldenGroup
type FinalSwissProjection = top4usecase.FinalSwissProjection

type TerminalSeriesEvidenceInput = top4usecase.TerminalSeriesEvidenceInput
type TerminalSeriesEvidence = top4usecase.TerminalSeriesEvidence
type FinalSwissRound = top4usecase.FinalSwissRound

type GoldenPositionCommitEvidence = top4usecase.GoldenPositionCommitEvidence
type GoldenPositionAttemptEvidence = top4usecase.GoldenPositionAttemptEvidence
type GoldenPositionEvidenceInput = top4usecase.GoldenPositionEvidenceInput
type GoldenPositionEvidence = top4usecase.GoldenPositionEvidence

type ProgressionSwissRound = top4usecase.ProgressionSwissRound
type ProgressionSwissInput = top4usecase.ProgressionSwissInput
type ImpactfulGoldenTieRange = top4usecase.ImpactfulGoldenTieRange

func PlanTop4Snapshot(command Top4SnapshotCommand) (Top4Snapshot, error) {
	return top4usecase.PlanTop4Snapshot(command)
}

func PlanFinalSwissReceipt(input ProgressionSwissInput) (FinalSwissProjection, error) {
	return top4usecase.PlanFinalSwissReceipt(input)
}

func PlanFinalSwissProjection(command FinalSwissProjectionCommand) (FinalSwissProjection, error) {
	return top4usecase.PlanFinalSwissProjection(command)
}

func NewTerminalSeriesEvidence(input TerminalSeriesEvidenceInput) (TerminalSeriesEvidence, error) {
	return top4usecase.NewTerminalSeriesEvidence(input)
}

func NewGoldenPositionEvidence(input GoldenPositionEvidenceInput) (GoldenPositionEvidence, error) {
	return top4usecase.NewGoldenPositionEvidence(input)
}

func DeriveImpactfulGoldenTieRanges(input ProgressionSwissInput) ([]ImpactfulGoldenTieRange, error) {
	return top4usecase.DeriveImpactfulGoldenTieRanges(input)
}

func PlanFinalSwissProgression(input ProgressionSwissInput) (FinalSwissProjection, error) {
	return top4usecase.PlanFinalSwissProgression(input)
}
