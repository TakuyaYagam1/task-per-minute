package presence

import (
	"errors"
	"time"

	"github.com/google/uuid"

	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

var (
	ErrInvalidPausedPresence      = errors.New("invalid paused presence update")
	ErrPausedPresenceConflict     = errors.New("paused presence conflict")
	ErrPausedPresenceCommandReuse = errors.New("paused presence command reuse")
	ErrPausedPresenceSuppression  = errors.New("paused presence suppression is inactive")
	ErrPausedPresenceState        = errors.New("paused presence state does not change")
	ErrPausedPresenceOverflow     = errors.New("paused presence revision overflow")
)

type PausePresenceRevision = model.PausePresenceRevision
type PauseFrozenDeadline = model.PauseFrozenDeadline
type NormalPauseRecord = model.NormalPauseRecord

type PausedPresenceExpectation struct {
	PauseID       uuid.UUID
	GraphRevision int64
	PauseRevision int64
	Presence      PausePresenceRevision
	Authority     authoritydomain.Identity
}

type PausedPresenceCommand struct {
	Scope                    pausedomain.GraphScope
	PauseID                  uuid.UUID
	CommandID                uuid.UUID
	ParticipantID            uuid.UUID
	ExpectedGraphRevision    int64
	ExpectedPauseRevision    int64
	ExpectedPresenceEpoch    int64
	ExpectedPresenceRevision int64
	NextState                pausedomain.PresenceState
}

type PausedPresenceAuthority struct {
	Pause                  NormalPauseRecord
	Presence               pausedomain.PausePresence
	Reconnect              []pausedomain.PauseReconnectInterval
	Counters               []pausedomain.PauseReconnectCounter
	FrozenDeadlines        []PauseFrozenDeadline
	TerminalActionRevision int64
}

type PausedPresenceRecord struct {
	Command   PausedPresenceCommand
	Authority PausedPresenceAuthority
	ChangedAt time.Time
}
