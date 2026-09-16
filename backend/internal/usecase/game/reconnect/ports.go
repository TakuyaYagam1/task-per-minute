package reconnect

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

type ReconnectClock interface {
	Now() time.Time
}

type Observer interface {
	Observe(ctx context.Context, event ReconnectEvent)
}

// ReconnectRepository owns command receipts and the aggregate compare-and-set.
// CommitMutation stores every state change and the receipt atomically.
type ReconnectRepository interface {
	FindCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*ReconnectRecord, error)
	LoadAuthority(ctx context.Context, scope pause.GraphScope, participantID uuid.UUID) (ReconnectAuthority, error)
	CommitMutation(ctx context.Context, expectedRevision int64, record ReconnectRecord) (*ReconnectRecord, bool, error)
}
