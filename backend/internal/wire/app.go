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

type App struct {
	Storage     BucketEnsurer
	Server      *http.Server
	WebSocket   WebSocketShutdowner
	Tx          usecase.TxManager
	Duels       usecase.ActiveDuelRepo
	Players     usecase.PlayerStatusRepo
	Broadcaster usecase.DuelBroadcaster
	Clock       clock.Clock
}
