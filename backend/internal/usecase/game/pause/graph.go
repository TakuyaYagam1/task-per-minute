package pause

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
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

const (
	PauseReasonOperator       = model.PauseReasonOperator
	PauseReasonDisconnect     = model.PauseReasonDisconnect
	PauseReasonPlatform       = model.PauseReasonPlatform
	PauseReasonExecutionEpoch = model.PauseReasonExecutionEpoch
)

type PauseState = model.PauseState

const (
	PauseStateActive    = model.PauseStateActive
	PauseStateResumed   = model.PauseStateResumed
	PauseStateCancelled = model.PauseStateCancelled
)

type PauseDeadlineKind = model.PauseDeadlineKind

const (
	PauseDeadlineReadyWindow = model.PauseDeadlineReadyWindow
	PauseDeadlineGame        = model.PauseDeadlineGame
	PauseDeadlineDraft       = model.PauseDeadlineDraft
)

type PauseChildRevision = model.PauseChildRevision
type PausePresenceRevision = model.PausePresenceRevision
type PauseReconnectCounterRevision = model.PauseReconnectCounterRevision
type PauseFrozenDeadlineRevision = model.PauseFrozenDeadlineRevision
type PauseGraphRevisions = model.PauseGraphRevisions
type PauseWave = model.PauseWave
type PauseSeries = model.PauseSeries
type PauseGame = model.PauseGame
type PauseFrozenDeadline = model.PauseFrozenDeadline
type TournamentRecord = model.TournamentRecord
type PauseGraph = model.PauseGraph
type NormalPauseAuthority = model.NormalPauseAuthority
type NormalPauseCommand = model.NormalPauseCommand
type NormalPauseRecord = model.NormalPauseRecord
