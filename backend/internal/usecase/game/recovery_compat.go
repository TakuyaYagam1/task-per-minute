package game

import recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/recovery"

var (
	ErrInvalidRecovery             = recoveryusecase.ErrInvalidRecovery
	ErrNotAuthoritative            = recoveryusecase.ErrNotAuthoritative
	ErrInvalidRecoveryRunnerConfig = recoveryusecase.ErrInvalidRecoveryRunnerConfig
	ErrRecoveryRunnerRunning       = recoveryusecase.ErrRecoveryRunnerRunning
	ErrInvalidEpochReplay          = recoveryusecase.ErrInvalidEpochReplay
	ErrEpochReplayConflict         = recoveryusecase.ErrEpochReplayConflict
	ErrEpochReplayCommandReuse     = recoveryusecase.ErrEpochReplayCommandReuse
)

const (
	RecoveryOutcomeSuccess = recoveryusecase.RecoveryOutcomeSuccess
	RecoveryOutcomeFailure = recoveryusecase.RecoveryOutcomeFailure
)

type Recoverer = recoveryusecase.Recoverer
type RecoveryRunnerConfig = recoveryusecase.RecoveryRunnerConfig
type GoldenRecovery = recoveryusecase.GoldenRecovery
type RecoveryRunner = recoveryusecase.RecoveryRunner
type RecoveryRunnerHealth = recoveryusecase.RecoveryRunnerHealth
type RecoveryObserver = recoveryusecase.RecoveryObserver
type RecoveryEvent = recoveryusecase.RecoveryEvent
type EpochReplayCommand = recoveryusecase.EpochReplayCommand
type EpochReplayAuthority = recoveryusecase.EpochReplayAuthority
type EpochReplayRecord = recoveryusecase.EpochReplayRecord
type EpochReplayCommitCondition = recoveryusecase.EpochReplayCommitCondition
type EpochReplayUseCase = recoveryusecase.EpochReplayUseCase

func NewRecoverer(
	authority RecoveryAuthorityReader,
	source RecoverySource,
	timers DeadlineRearmer,
	replayer RecoveryEpochReplayer,
	timeSource AuthorityTimeSource,
) *Recoverer {
	return recoveryusecase.NewRecoverer(authority, source, timers, replayer, timeSource)
}

func NewRecoveryRunner(
	source RecoveryTournamentSource,
	authority RecoveryAuthorityProvider,
	recoverer *Recoverer,
	clock ExecutionClock,
	configs ...RecoveryRunnerConfig,
) (*RecoveryRunner, error) {
	return recoveryusecase.NewRecoveryRunner(source, authority, recoverer, clock, configs...)
}

func NewEpochReplayUseCase(
	repository EpochReplayRepository,
	timeSource AuthorityTimeSource,
) *EpochReplayUseCase {
	return recoveryusecase.NewEpochReplayUseCase(repository, timeSource)
}
