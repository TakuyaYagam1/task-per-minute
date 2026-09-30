//go:build wireinject
// +build wireinject

package bootstrap

import (
	"github.com/google/wire"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
)

func initializeApp(runtime *RuntimeContext, cfg *config.Config, log logkit.Logger) (*App, func(), error) {
	wire.Build(
		ConfigSet,
		RuntimeSet,
		PostgresSet,
		RedisSet,
		SeaweedFSSet,
		ReposSet,
		UseCasesSet,
		PlayerNotificationsSet,
		PlayerAccountsSet,
		MiddlewareSet,
		WebSocketSet,
		HTTPSet,
		AppSet,
	)
	return nil, nil, nil
}
