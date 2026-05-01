//go:build wireinject
// +build wireinject

package wire

import (
	"context"

	"github.com/google/wire"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
)

func InitializeApp(ctx context.Context, cfg *config.Config, log logkit.Logger) (*App, func(), error) {
	wire.Build(
		ConfigSet,
		PostgresSet,
		RedisSet,
		SeaweedFSSet,
		ReposSet,
		UsecasesSet,
		MiddlewareSet,
		WebSocketSet,
		HTTPSet,
		AppSet,
	)
	return nil, nil, nil
}
