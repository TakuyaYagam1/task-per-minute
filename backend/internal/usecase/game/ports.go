package game

import (
	"time"

	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/recovery"
)

type ExecutionClock = recoveryusecase.ExecutionClock
type AuthorityTimeSource = recoveryusecase.AuthorityTimeSource
type EpochReplayRepository = recoveryusecase.EpochReplayRepository
type RecoveryCommand = recoveryusecase.RecoveryCommand
type RecoveryCandidate = recoveryusecase.RecoveryCandidate
type DeadlineArm = recoveryusecase.DeadlineArm
type RecoveryReport = recoveryusecase.RecoveryReport
type RecoveryAuthorityReader = recoveryusecase.RecoveryAuthorityReader
type RecoverySource = recoveryusecase.RecoverySource
type DeadlineRearmer = recoveryusecase.DeadlineRearmer
type RecoveryEpochReplayer = recoveryusecase.RecoveryEpochReplayer
type RecoveryTournamentSource = recoveryusecase.RecoveryTournamentSource
type RecoveryAuthorityProvider = recoveryusecase.RecoveryAuthorityProvider
type AttemptClock = attemptusecase.AttemptClock
type AttemptRepository = attemptusecase.AttemptRepository
type ReconnectClock = reconnectusecase.ReconnectClock
type Observer = reconnectusecase.Observer
type ReconnectRepository = reconnectusecase.ReconnectRepository
type WaveClock interface {
	Now() time.Time
}
