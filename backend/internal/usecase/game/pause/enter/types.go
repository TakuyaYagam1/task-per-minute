package enter

import "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"

const (
	normalPauseGraphAttempts = model.NormalPauseGraphAttempts
	maxFrozenPauseDuration   = model.MaxFrozenPauseDuration
)

var (
	ErrInvalidNormalPauseGraph    = model.ErrInvalidNormalPauseGraph
	ErrNormalPauseGraphConflict   = model.ErrNormalPauseGraphConflict
	ErrNormalPauseCommandReuse    = model.ErrNormalPauseCommandReuse
	ErrNormalPauseGraphIncomplete = model.ErrNormalPauseGraphIncomplete
	ErrNormalPauseGoldenActive    = model.ErrNormalPauseGoldenActive
	ErrNormalPauseDeadline        = model.ErrNormalPauseDeadline
	ErrNormalPauseOverflow        = model.ErrNormalPauseOverflow
)

type PauseReason = model.PauseReason
type PauseState = model.PauseState
type PauseDeadlineKind = model.PauseDeadlineKind
type PauseChildRevision = model.PauseChildRevision
type PauseGraphRevisions = model.PauseGraphRevisions
type PauseWave = model.PauseWave
type PauseSeries = model.PauseSeries
type PauseGame = model.PauseGame
type PauseFrozenDeadline = model.PauseFrozenDeadline
type PauseGraph = model.PauseGraph
type NormalPauseAuthority = model.NormalPauseAuthority
type NormalPauseCommand = model.NormalPauseCommand
type NormalPauseRecord = model.NormalPauseRecord

const (
	PauseReasonOperator       = model.PauseReasonOperator
	PauseReasonDisconnect     = model.PauseReasonDisconnect
	PauseReasonPlatform       = model.PauseReasonPlatform
	PauseReasonExecutionEpoch = model.PauseReasonExecutionEpoch

	PauseStateActive    = model.PauseStateActive
	PauseStateResumed   = model.PauseStateResumed
	PauseStateCancelled = model.PauseStateCancelled

	PauseDeadlineReadyWindow = model.PauseDeadlineReadyWindow
	PauseDeadlineGame        = model.PauseDeadlineGame
	PauseDeadlineDraft       = model.PauseDeadlineDraft
)
