package game

import replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/replay"

var (
	ErrInvalidReplayReserveExhaustion  = replayusecase.ErrInvalidReplayReserveExhaustion
	ErrReplayReserveExhaustionConflict = replayusecase.ErrReplayReserveExhaustionConflict
	ErrReplayReserveExhaustionReuse    = replayusecase.ErrReplayReserveExhaustionReuse
	ErrInvalidOperatorReserve          = replayusecase.ErrInvalidOperatorReserve
	ErrOperatorReserveConflict         = replayusecase.ErrOperatorReserveConflict
	ErrOperatorReserveReuse            = replayusecase.ErrOperatorReserveReuse
	ErrInvalidReplayReplacement        = replayusecase.ErrInvalidReplayReplacement
	ErrReplayReplacementConflict       = replayusecase.ErrReplayReplacementConflict
	ErrReplayReplacementReuse          = replayusecase.ErrReplayReplacementReuse
	ErrReplayReservesExhausted         = replayusecase.ErrReplayReservesExhausted
)

type ReplayClock = replayusecase.ReplayClock
type ReplayReserveExhaustionCommand = replayusecase.ReplayReserveExhaustionCommand
type ReplayReserveExhaustionAuthority = replayusecase.ReplayReserveExhaustionAuthority
type ReplayReserveExhaustion = replayusecase.ReplayReserveExhaustion
type ReplayReserveExhaustionRepository = replayusecase.ReplayReserveExhaustionRepository
type FailedAttemptTerminalizer = replayusecase.FailedAttemptTerminalizer
type OldWaveCloser = replayusecase.OldWaveCloser
type ReplayReplacementPlanner = replayusecase.ReplayReplacementPlanner
type OperatorReserveCommand = replayusecase.OperatorReserveCommand
type OperatorReserveAuthority = replayusecase.OperatorReserveAuthority
type OperatorReserve = replayusecase.OperatorReserve
type OperatorReserveRepository = replayusecase.OperatorReserveRepository
type ReplayReplacementScope = replayusecase.ReplayReplacementScope
type ReplayReserveChain = replayusecase.ReplayReserveChain
type ReplayReplacementAuthority = replayusecase.ReplayReplacementAuthority
type ReplayReplacementCommand = replayusecase.ReplayReplacementCommand
type ReplayReplacement = replayusecase.ReplayReplacement
type NoSolveReplayCommand = replayusecase.NoSolveReplayCommand
type NoSolveReplayResult = replayusecase.NoSolveReplayResult
type ReplayReplacementRepository = replayusecase.ReplayReplacementRepository

type ReplayReserveExhaustionUseCase = replayusecase.ReplayReserveExhaustionUseCase
type OperatorReserveUseCase = replayusecase.OperatorReserveUseCase
type ReplayReplacementUseCase = replayusecase.ReplayReplacementUseCase
type NoSolveReplayUseCase = replayusecase.NoSolveReplayUseCase

func NewReplayReserveExhaustionUseCase(
	repository ReplayReserveExhaustionRepository,
) *ReplayReserveExhaustionUseCase {
	return replayusecase.NewReplayReserveExhaustionUseCase(repository)
}

func NewOperatorReserveUseCase(repository OperatorReserveRepository) *OperatorReserveUseCase {
	return replayusecase.NewOperatorReserveUseCase(repository)
}

func NewReplayReplacementUseCase(
	repository ReplayReplacementRepository,
	clock ReplayClock,
) *ReplayReplacementUseCase {
	return replayusecase.NewReplayReplacementUseCase(repository, clock)
}

func NewNoSolveReplayUseCase(
	terminalizer FailedAttemptTerminalizer,
	closer OldWaveCloser,
	replacer ReplayReplacementPlanner,
) *NoSolveReplayUseCase {
	return replayusecase.NewNoSolveReplayUseCase(terminalizer, closer, replacer)
}
