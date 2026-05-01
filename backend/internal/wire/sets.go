package wire

import (
	"github.com/google/wire"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/inmem"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent"
	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/storage"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

var ConfigSet = wire.NewSet(
	provideAuthConfig,
)

var PostgresSet = wire.NewSet(
	providePostgresConfig,
	providePostgres,
)

var RedisSet = wire.NewSet(
	provideRedisConfig,
	provideRedis,
)

var SeaweedFSSet = wire.NewSet(
	provideSeaweedConfig,
	provideSeaweedStorage,
	wire.Bind(new(usecase.SourceFileStorage), new(*storage.SeaweedStorage)),
)

var ReposSet = wire.NewSet(
	persistent.NewTxManager,
	wire.Bind(new(usecase.TxManager), new(*persistent.TxManager)),
	persistent.NewSchemaVersionPostgres,
	wire.Bind(new(usecase.SchemaVersionReader), new(*persistent.SchemaVersionPostgres)),
	persistent.NewPlayerPostgres,
	wire.Bind(new(usecase.PlayerRepo), new(*persistent.PlayerPostgres)),
	wire.Bind(new(usecase.PlayerStatusRepo), new(*persistent.PlayerPostgres)),
	persistent.NewDuelPostgres,
	wire.Bind(new(usecase.DuelRepo), new(*persistent.DuelPostgres)),
	wire.Bind(new(usecase.ActiveDuelRepo), new(*persistent.DuelPostgres)),
	persistent.NewTaskPostgres,
	wire.Bind(new(usecase.TaskRepo), new(*persistent.TaskPostgres)),
	persistent.NewHistoryPostgres,
	wire.Bind(new(usecase.HistoryRepo), new(*persistent.HistoryPostgres)),
	persistent.NewLeaderboardPostgres,
	wire.Bind(new(usecase.LeaderboardRepo), new(*persistent.LeaderboardPostgres)),
	provideLeaderboardRedis,
	wire.Bind(new(usecase.LeaderboardStore), new(*redisrepo.LeaderboardRedis)),
	provideMatchmakingRedis,
	wire.Bind(new(usecase.MatchmakingQueue), new(*redisrepo.MatchmakingRedis)),
)

var UsecasesSet = wire.NewSet(
	provideClock,
	inmem.NewRevocation,
	wire.Bind(new(usecase.RevocationStore), new(*inmem.Revocation)),
	adminusecase.NewAuthUsecase,
	wire.Bind(new(usecase.AdminAuth), new(*adminusecase.AuthUsecase)),
	adminusecase.NewTaskUsecase,
	wire.Bind(new(usecase.AdminTask), new(*adminusecase.TaskUsecase)),
	adminusecase.NewUploadUsecase,
	wire.Bind(new(usecase.Upload), new(*adminusecase.UploadUsecase)),
	playerusecase.NewPlayerUsecase,
	wire.Bind(new(usecase.Player), new(*playerusecase.PlayerUsecase)),
	duelusecase.NewMatchmakingUsecase,
	wire.Bind(new(websocket.Matchmaking), new(*duelusecase.MatchmakingUsecase)),
	duelusecase.NewTimerRegistry,
	provideHintScheduler,
	provideDuelTimers,
	provideFlagSubmitUsecase,
	wire.Bind(new(websocket.FlagSubmitter), new(*duelusecase.FlagSubmitUsecase)),
	duelusecase.NewReadUsecase,
	wire.Bind(new(usecase.Duel), new(*duelusecase.ReadUsecase)),
	leaderboardusecase.NewLeaderboardUsecase,
	wire.Bind(new(usecase.Leaderboard), new(*leaderboardusecase.LeaderboardUsecase)),
)

var MiddlewareSet = wire.NewSet(
	provideRESTMiddlewares,
)

var WebSocketSet = wire.NewSet(
	provideHubRegistry,
	provideRawWebSocketServer,
	provideDuelBroadcaster,
	provideReconnectManager,
	provideWebSocketServer,
)

var HTTPSet = wire.NewSet(
	provideHealthChecks,
	provideRESTServer,
	provideHTTPHandler,
	provideHTTPServer,
)

var AppSet = wire.NewSet(
	provideApp,
)
