package golden

import (
	"crypto/sha256"

	"github.com/google/uuid"

	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	goldenwave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave"
)

type WaveClock = goldenwave.WaveClock
type WaveRepository = goldenwave.WaveRepository
type GoldenPrivateAssignmentCommand = goldenwave.GoldenPrivateAssignmentCommand
type GoldenWaveAuthorityCondition = goldenwave.GoldenWaveAuthorityCondition
type GoldenWaveExecutionCommit = goldenwave.GoldenWaveExecutionCommit
type OpenGoldenReadyWindowCommand = goldenwave.OpenGoldenReadyWindowCommand
type GoldenMarkReadyCommand = goldenwave.GoldenMarkReadyCommand
type GoldenReadyDisconnectCommand = goldenwave.GoldenReadyDisconnectCommand
type GoldenReconnectCommand = goldenwave.GoldenReconnectCommand
type GoldenStartCommand = goldenwave.GoldenStartCommand
type GoldenReadyWindowUseCase = goldenwave.GoldenReadyWindowUseCase
type GoldenReadinessUseCase = goldenwave.GoldenReadinessUseCase
type GoldenStartUseCase = goldenwave.GoldenStartUseCase

var (
	ErrGoldenReadyWindowClosed     = goldenwave.ErrGoldenReadyWindowClosed
	ErrGoldenReadyWindowIneligible = goldenwave.ErrGoldenReadyWindowIneligible
	ErrGoldenWaveAuthorityConflict = goldenwave.ErrGoldenWaveAuthorityConflict
	ErrGoldenWaveCommitConflict    = goldenwave.ErrGoldenWaveCommitConflict
	ErrGoldenWaveCommandReuse      = goldenwave.ErrGoldenWaveCommandReuse
	ErrGoldenWaveIdentityConflict  = goldenwave.ErrGoldenWaveIdentityConflict
	ErrGoldenWaveRevisionOverflow  = goldenwave.ErrGoldenWaveRevisionOverflow
	ErrGoldenWaveExecutionNotFound = goldenwave.ErrGoldenWaveExecutionNotFound
	ErrGoldenWaveAuthorityNotLive  = goldenwave.ErrGoldenWaveAuthorityNotLive
)

func NewGoldenReadyWindowUseCase(repository WaveRepository, clock WaveClock) *GoldenReadyWindowUseCase {
	return goldenwave.NewGoldenReadyWindowUseCase(repository, clock)
}

func NewGoldenReadinessUseCase(repository WaveRepository, clock WaveClock) *GoldenReadinessUseCase {
	return goldenwave.NewGoldenReadinessUseCase(repository, clock)
}

func NewGoldenStartUseCase(repository WaveRepository, clock WaveClock) *GoldenStartUseCase {
	return goldenwave.NewGoldenStartUseCase(repository, clock)
}

func SealExecution(execution *GoldenWaveExecution) error {
	return goldenwave.SealExecution(execution)
}

func ValidateFreshIdentityIDs(state GoldenState, identities ...uuid.UUID) error {
	return goldenwave.ValidateFreshIdentityIDs(state, identities...)
}

func GoldenExecutionAuthorityDigest(lease authoritydomain.Lease) [sha256.Size]byte {
	return goldenwave.GoldenExecutionAuthorityDigest(lease)
}
