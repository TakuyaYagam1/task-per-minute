package wire

import (
	"context"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

type BucketEnsurer interface {
	EnsureBucket(ctx context.Context) error
}

type WebSocketShutdowner interface {
	Shutdown(ctx context.Context)
}

// RevocationJanitor evicts expired entries from the JWT revocation store.
// Redis-backed implementations can no-op it because Redis evicts via TTL
// natively.
type RevocationJanitor interface {
	Cleanup()
}

type App struct {
	Storage     BucketEnsurer
	Server      *http.Server
	WebSocket   WebSocketShutdowner
	Tx          usecase.TxManager
	Duels       usecase.ActiveDuelRepo
	Players     usecase.PlayerStatusRepo
	Queued      usecase.QueuedPlayerResetter
	Queue       usecase.MatchmakingQueueCleaner
	Broadcaster usecase.DuelBroadcaster
	Clock       clock.Clock
	Revocation  RevocationJanitor
}
