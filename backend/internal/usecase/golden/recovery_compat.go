package golden

import goldenrecovery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/recovery"

var (
	ErrInvalidGoldenRecovery   = goldenrecovery.ErrInvalidGoldenRecovery
	ErrAmbiguousGoldenRecovery = goldenrecovery.ErrAmbiguousGoldenRecovery
)

type GoldenRecoverySelection = goldenrecovery.GoldenRecoverySelection

const (
	GoldenRecoverySelectionDirect   = goldenrecovery.GoldenRecoverySelectionDirect
	GoldenRecoverySelectionReserve  = goldenrecovery.GoldenRecoverySelectionReserve
	GoldenRecoverySelectionExcluded = goldenrecovery.GoldenRecoverySelectionExcluded
)

type GoldenRecoveryGroup = goldenrecovery.GoldenRecoveryGroup
type GoldenRecoveryMembership = goldenrecovery.GoldenRecoveryMembership
type GoldenRecoverySubmission = goldenrecovery.GoldenRecoverySubmission
type GoldenRecoveryPositionCommit = goldenrecovery.GoldenRecoveryPositionCommit
type GoldenRecoveryAttempt = goldenrecovery.GoldenRecoveryAttempt
type GoldenRecoveryInput = goldenrecovery.GoldenRecoveryInput
type GoldenRecoveryCommittedAttempt = goldenrecovery.GoldenRecoveryCommittedAttempt
type GoldenRecoveryReserve = goldenrecovery.GoldenRecoveryReserve
type GoldenRecoveryProvisional = goldenrecovery.GoldenRecoveryProvisional
type GoldenRecoveryDeadlines = goldenrecovery.GoldenRecoveryDeadlines
type GoldenRecoveryLiveAttempt = goldenrecovery.GoldenRecoveryLiveAttempt
type GoldenRecoveryResult = goldenrecovery.GoldenRecoveryResult

func RecoverGoldenGroup(input GoldenRecoveryInput) (GoldenRecoveryResult, error) {
	return goldenrecovery.RecoverGoldenGroup(input)
}
