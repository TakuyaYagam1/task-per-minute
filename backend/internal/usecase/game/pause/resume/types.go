package resume

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

var (
	ErrInvalidPauseResume      = errors.New("invalid pause resume")
	ErrPauseResumeConflict     = errors.New("pause resume conflict")
	ErrPauseResumeCommandReuse = errors.New("pause resume command reuse")
	ErrPauseResumeIncomplete   = errors.New("pause resume evidence incomplete")
	ErrPauseResumePresence     = errors.New("pause resume requires connected participants")
	ErrPauseResumeOverflow     = errors.New("pause resume revision overflow")
)

type PauseChildRevision = model.PauseChildRevision
type PausePresenceRevision = model.PausePresenceRevision
type PauseReconnectCounterRevision = model.PauseReconnectCounterRevision
type PauseFrozenDeadlineRevision = model.PauseFrozenDeadlineRevision
type PauseSourcePauseRevision = model.PauseSourcePauseRevision
type PauseWave = model.PauseWave
type PauseSeries = model.PauseSeries
type PauseGame = model.PauseGame
type PauseFrozenDeadline = model.PauseFrozenDeadline
type PauseGraph = model.PauseGraph
type PauseGraphRevisions = model.PauseGraphRevisions
type NormalPauseRecord = model.NormalPauseRecord
type PauseState = model.PauseState
type PauseDeadlineKind = model.PauseDeadlineKind

const (
	PauseStateActive    = model.PauseStateActive
	PauseStateResumed   = model.PauseStateResumed
	PauseStateCancelled = model.PauseStateCancelled

	PauseDeadlineReadyWindow = model.PauseDeadlineReadyWindow
	PauseDeadlineGame        = model.PauseDeadlineGame
	PauseDeadlineDraft       = model.PauseDeadlineDraft
)

type PauseResumeExpectation struct {
	PauseID                 uuid.UUID
	GraphRevision           int64
	PauseRevision           int64
	Authority               authoritydomain.Identity
	TournamentState         domain.TournamentState
	TournamentRevision      int64
	WaveRevision            int64
	Series                  []PauseChildRevision
	Games                   []PauseChildRevision
	SourcePauses            []PauseSourcePauseRevision
	Draft                   *draftusecase.RevisionExpectation
	DraftPreviousRevisionID uuid.UUID
	Presence                []PausePresenceRevision
	Reconnect               []PauseChildRevision
	Counters                []PauseReconnectCounterRevision
	FrozenDeadlines         []PauseFrozenDeadlineRevision
	TerminalActionRevision  int64
}

type PauseResumeCommand struct {
	Scope                 pausedomain.GraphScope
	PauseID               uuid.UUID
	CommandID             uuid.UUID
	ActorID               uuid.UUID
	DraftResultRevisionID uuid.UUID
	Expected              PauseResumeExpectation
}

type PauseResumeAuthority struct {
	Pause                  NormalPauseRecord
	Presence               []pausedomain.PausePresence
	Reconnect              []pausedomain.PauseReconnectInterval
	Counters               []pausedomain.PauseReconnectCounter
	FrozenDeadlines        []PauseFrozenDeadline
	TerminalActionRevision int64
}

type PauseResumeRecord struct {
	Scope                 pausedomain.GraphScope
	PauseID               uuid.UUID
	CommandID             uuid.UUID
	ActorID               uuid.UUID
	DraftResultRevisionID uuid.UUID
	State                 PauseState
	Revision              int64
	Expected              PauseResumeExpectation
	Graph                 PauseGraph
	ResumedAt             time.Time
}
