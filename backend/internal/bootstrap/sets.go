package bootstrap

import (
	"github.com/google/wire"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	clockadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/clock"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

var ConfigSet = wire.NewSet(
	provideAuthConfig,
)

var RuntimeSet = wire.NewSet(
	provideRuntimeContext,
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
	wire.Bind(new(adminusecase.SourceFileStorage), new(*objectstorage.SeaweedStorage)),
	wire.Bind(new(duelusecase.SourceFileURLSigner), new(*objectstorage.SeaweedStorage)),
	wire.Bind(new(websocket.SourceFileURLSigner), new(*objectstorage.SeaweedStorage)),
)

var ReposSet = wire.NewSet(
	postgres.NewTxManager,
	wire.Bind(new(adminusecase.TransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(duelusecase.TransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(playerusecase.TransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(recovery.TransactionManager), new(*postgres.TxManager)),

	postgres.NewSchemaVersionPostgres,
	wire.Bind(new(restv1.SchemaVersionReader), new(*postgres.SchemaVersionPostgres)),
	postgres.NewAdminPlayerEventsPostgres,
	wire.Bind(new(restv1.AdminPlayerEventSubscriber), new(*postgres.AdminPlayerEventsPostgres)),

	postgres.NewPlayerPostgres,
	wire.Bind(new(adminusecase.PlayerRepository), new(*postgres.PlayerPostgres)),
	wire.Bind(new(duelusecase.MatchmakingPlayerRepository), new(*postgres.PlayerPostgres)),
	wire.Bind(new(duelusecase.FinalizationPlayerRepository), new(*postgres.PlayerPostgres)),
	wire.Bind(new(playerusecase.Repository), new(*postgres.PlayerPostgres)),
	wire.Bind(new(recovery.PlayerStatusRepository), new(*postgres.PlayerPostgres)),
	wire.Bind(new(recovery.QueuedPlayerResetter), new(*postgres.PlayerPostgres)),
	wire.Bind(new(middleware.PlayerSessionReader), new(*postgres.PlayerPostgres)),
	wire.Bind(new(websocket.PlayerReader), new(*postgres.PlayerPostgres)),

	postgres.NewDuelPostgres,
	wire.Bind(new(duelusecase.MatchmakingDuelRepository), new(*postgres.DuelPostgres)),
	wire.Bind(new(duelusecase.FinalizationDuelRepository), new(*postgres.DuelPostgres)),
	wire.Bind(new(duelusecase.FlagDuelRepository), new(*postgres.DuelPostgres)),
	wire.Bind(new(duelusecase.ReconnectDuelRepository), new(*postgres.DuelPostgres)),
	wire.Bind(new(duelusecase.ReadDuelRepository), new(*postgres.DuelPostgres)),
	wire.Bind(new(playerusecase.ActiveDuelReader), new(*postgres.DuelPostgres)),
	wire.Bind(new(recovery.ActiveDuelRepository), new(*postgres.DuelPostgres)),
	wire.Bind(new(recovery.DuelTaskReader), new(*postgres.DuelPostgres)),
	wire.Bind(new(websocket.DuelTaskReader), new(*postgres.DuelPostgres)),

	postgres.NewTaskPostgres,
	wire.Bind(new(adminusecase.TaskRepository), new(*postgres.TaskPostgres)),
	wire.Bind(new(adminusecase.UploadTaskRepository), new(*postgres.TaskPostgres)),
	wire.Bind(new(duelusecase.MatchmakingTaskRepository), new(*postgres.TaskPostgres)),

	postgres.NewHistoryPostgres,
	wire.Bind(new(duelusecase.MatchmakingHistoryRepository), new(*postgres.HistoryPostgres)),
	wire.Bind(new(duelusecase.SolvedHistoryWriter), new(*postgres.HistoryPostgres)),

	postgres.NewLeaderboardPostgres,
	wire.Bind(new(leaderboardusecase.StatsRepository), new(*postgres.LeaderboardPostgres)),
	provideLeaderboardRedis,
	wire.Bind(new(leaderboardusecase.WinStore), new(*redisadapter.LeaderboardRedis)),
	provideMatchmakingRedis,
	wire.Bind(new(duelusecase.MatchmakingQueue), new(*redisadapter.MatchmakingRedis)),
	wire.Bind(new(recovery.QueueCleaner), new(*redisadapter.MatchmakingRedis)),
)

var UseCasesSet = wire.NewSet(
	provideClock,
	wire.Bind(new(adminusecase.Clock), new(*clockadapter.Real)),
	wire.Bind(new(duelusecase.Clock), new(*clockadapter.Real)),
	wire.Bind(new(leaderboardusecase.Clock), new(*clockadapter.Real)),
	wire.Bind(new(playerusecase.Clock), new(*clockadapter.Real)),
	wire.Bind(new(recovery.Clock), new(*clockadapter.Real)),
	provideRevocationRedis,
	wire.Bind(new(adminusecase.RevocationStore), new(*redisadapter.RevocationRedis)),
	wire.Bind(new(RevocationJanitor), new(*redisadapter.RevocationRedis)),

	provideAuthUseCase,
	wire.Bind(new(restv1.AdminAuthService), new(*adminusecase.AuthUseCase)),
	wire.Bind(new(middleware.AdminAccessVerifier), new(*adminusecase.AuthUseCase)),
	provideAdminTaskUseCase,
	wire.Bind(new(restv1.AdminTaskService), new(*adminusecase.TaskUseCase)),
	provideAdminPlayerUseCase,
	wire.Bind(new(restv1.AdminPlayerService), new(*adminusecase.PlayerUseCase)),
	provideUploadUseCase,
	wire.Bind(new(restv1.UploadService), new(*adminusecase.UploadUseCase)),

	providePlayerUseCase,
	wire.Bind(new(restv1.PlayerService), new(*playerusecase.UseCase)),

	provideMatchmakingUseCase,
	wire.Bind(new(websocket.Matchmaking), new(*duelusecase.MatchmakingUseCase)),
	provideTimerRegistry,
	provideHintScheduler,
	provideDuelTimers,
	provideFlagSubmitUseCase,
	wire.Bind(new(websocket.FlagSubmitter), new(*duelusecase.FlagSubmitUseCase)),
	provideReadUseCase,
	wire.Bind(new(restv1.DuelService), new(*duelusecase.ReadUseCase)),

	provideLeaderboardUseCase,
	wire.Bind(new(restv1.LeaderboardService), new(*leaderboardusecase.UseCase)),
	wire.Bind(new(adminusecase.LeaderboardInvalidator), new(*leaderboardusecase.UseCase)),
	wire.Bind(new(duelusecase.LeaderboardBumper), new(*leaderboardusecase.UseCase)),
)

var MiddlewareSet = wire.NewSet(
	provideRESTMiddlewares,
)

var WebSocketSet = wire.NewSet(
	provideHubRegistry,
	provideHandshakeRateLimiter,
	provideRawWebSocketServer,
	provideDuelBroadcaster,
	provideReconnectManager,
	provideWebSocketServer,
)

var HTTPSet = wire.NewSet(
	provideHealthChecks,
	provideLoginRateLimiter,
	provideRefreshRateLimiter,
	provideJoinRateLimiter,
	provideLeaderboardRateLimiter,
	provideRESTServer,
	provideHTTPHandler,
	provideHTTPServer,
)

var AppSet = wire.NewSet(
	provideMigrator,
	provideStartupRecoverer,
	provideApplication,
)
