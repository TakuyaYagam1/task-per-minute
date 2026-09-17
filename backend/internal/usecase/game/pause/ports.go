package pause

import (
	"context"
	"time"

	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

type PauseClock interface {
	Now() time.Time
}

// NormalPauseRepository participates in the caller transaction. Load locks the
// complete authority. The stable lock order is execution-authority, pause and
// graph, Tournament, Wave, Series, Games, Draft, Presence, Reconnect, counters,
// frozen deadlines and terminal actions, with each collection ordered by its
// durable identity. Commit revalidates every expectation and publishes the
// complete graph and command result atomically or makes no write.
type NormalPauseRepository interface {
	FindNormalPauseCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*NormalPauseRecord, error)
	LoadNormalPauseAuthority(ctx context.Context, scope pausedomain.GraphScope) (NormalPauseAuthority, error)
	CommitNormalPause(ctx context.Context, expected PauseGraphRevisions, record NormalPauseRecord) (*NormalPauseRecord, bool, error)
}

func PauseGraphRevisionsFrom(graph PauseGraph) PauseGraphRevisions {
	return model.PauseGraphRevisionsFrom(graph)
}

// PausedPresenceRepository participates in the caller transaction. Load locks
// in this stable order: execution-authority, pause and graph, Tournament, Wave,
// Series, Games, Draft, Presence, Reconnect, counters, frozen deadlines and
// terminal actions, with collection rows ordered by durable identity. Commit
// revalidates the complete expectation, retains the command result atomically
// and changes only the selected Presence row.
type PausedPresenceRepository interface {
	FindPausedPresenceCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*PausedPresenceRecord, error)
	LoadPausedPresenceAuthority(ctx context.Context, scope pausedomain.GraphScope, participantID uuid.UUID) (PausedPresenceAuthority, error)
	CommitPausedPresence(ctx context.Context, expected PausedPresenceExpectation, record PausedPresenceRecord) (*PausedPresenceRecord, bool, error)
}

// PauseResumePresenceRepository commits the normal Wave pause, old Series/Game
// pause heads, Game clock, decision rows and reconnect rows in one transaction.
type PauseResumePresenceRepository interface {
	FindPauseResumePresenceCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*PauseResumePresenceRecord, error)
	LoadPauseResumePresenceAuthority(ctx context.Context, scope pausedomain.GraphScope, normalPauseID, seriesPauseID, gamePauseID uuid.UUID) (PauseResumePresenceAuthority, error)
	CommitPauseResumePresence(ctx context.Context, expected PauseResumePresenceExpectation, record PauseResumePresenceRecord) (*PauseResumePresenceRecord, bool, error)
}

// PauseResumeRepository participates in the caller transaction. Load locks in
// this stable order: execution-authority, pause and graph, Tournament, Wave,
// Series, Games, Draft, Presence, Reconnect, counters, frozen deadlines and
// terminal actions, with collection rows ordered by durable identity. Commit
// revalidates the complete expectation and atomically stores the restored graph
// and command result. A conflict makes no write.
type PauseResumeRepository interface {
	FindPauseResumeCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*PauseResumeRecord, error)
	LoadPauseResumeAuthority(ctx context.Context, scope pausedomain.GraphScope, pauseID uuid.UUID) (PauseResumeAuthority, error)
	CommitPauseResume(ctx context.Context, expected PauseResumeExpectation, record PauseResumeRecord) (*PauseResumeRecord, bool, error)
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}
