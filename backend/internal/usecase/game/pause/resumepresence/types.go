package resumepresence

import (
	"context"
	"time"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
	resumeusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/resume"
	"github.com/google/uuid"
)

type PauseReason = model.PauseReason
type PauseState = model.PauseState
type PauseDeadlineKind = model.PauseDeadlineKind
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

type PauseResumeExpectation = resumeusecase.PauseResumeExpectation
type PauseResumeCommand = resumeusecase.PauseResumeCommand
type PauseResumeAuthority = resumeusecase.PauseResumeAuthority
type PauseResumeRecord = resumeusecase.PauseResumeRecord
type PauseResumeGameClock = pausedomain.PauseResumeGameClock

const (
	PauseStateActive    = model.PauseStateActive
	PauseStateResumed   = model.PauseStateResumed
	PauseStateCancelled = model.PauseStateCancelled
)

type PauseClock interface {
	Now() time.Time
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

// PauseResumePresenceRepository owns the locked authority read and atomic
// command-result write for the coupled normal/series/game pause decision.
type PauseResumePresenceRepository interface {
	FindPauseResumePresenceCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*PauseResumePresenceRecord, error)
	LoadPauseResumePresenceAuthority(ctx context.Context, scope pausedomain.GraphScope, normalPauseID, seriesPauseID, gamePauseID uuid.UUID) (PauseResumePresenceAuthority, error)
	CommitPauseResumePresence(ctx context.Context, expected PauseResumePresenceExpectation, record PauseResumePresenceRecord) (*PauseResumePresenceRecord, bool, error)
}

var (
	ErrInvalidPauseResume      = resumeusecase.ErrInvalidPauseResume
	ErrPauseResumeConflict     = resumeusecase.ErrPauseResumeConflict
	ErrPauseResumeCommandReuse = resumeusecase.ErrPauseResumeCommandReuse
	ErrPauseResumeIncomplete   = resumeusecase.ErrPauseResumeIncomplete
	ErrPauseResumePresence     = resumeusecase.ErrPauseResumePresence
	ErrPauseResumeOverflow     = resumeusecase.ErrPauseResumeOverflow
)
