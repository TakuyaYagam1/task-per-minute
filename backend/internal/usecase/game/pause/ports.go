package pause

import (
	"context"

	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	enterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/enter"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

type PauseClock = enterusecase.PauseClock
type NormalPauseRepository = enterusecase.NormalPauseRepository
type TransactionManager = enterusecase.TransactionManager

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
