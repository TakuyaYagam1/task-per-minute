package wire

import (
	"context"
	"net"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/storage"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
	pgclient "github.com/TakuyaYagam1/task-per-minute/pkg/postgres"
	redisclient "github.com/TakuyaYagam1/task-per-minute/pkg/redis"
)

type rawWebSocketServer struct {
	*websocket.Server
}

func provideClock() clock.Clock {
	return clock.Real{}
}

func providePostgresConfig(cfg *config.Config) pgclient.Config {
	return pgclient.Config{
		DSN:      cfg.DB.DSN,
		MaxConns: cfg.DB.MaxConns,
	}
}

func providePostgres(ctx context.Context, cfg pgclient.Config) (*pgxpool.Pool, func(), error) {
	pool, err := pgclient.New(ctx, cfg)
	if err != nil {
		return nil, func() {}, err
	}
	return pool, pool.Close, nil
}

func provideRedisConfig(cfg *config.Config) redisclient.Config {
	return redisclient.Config{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	}
}

func provideRedis(ctx context.Context, cfg redisclient.Config) (*goredis.Client, func(), error) {
	client, err := redisclient.New(ctx, cfg)
	if err != nil {
		return nil, func() {}, err
	}
	return client, func() { _ = client.Close() }, nil
}

func provideSeaweedConfig(cfg *config.Config) storage.Config {
	return storage.Config{
		Endpoint:  cfg.SeaweedFS.Endpoint,
		AccessKey: cfg.SeaweedFS.AccessKey,
		SecretKey: cfg.SeaweedFS.SecretKey,
		Bucket:    cfg.SeaweedFS.Bucket,
		Secure:    cfg.SeaweedFS.Secure,
	}
}

func provideSeaweedStorage(cfg storage.Config) (*storage.SeaweedStorage, error) {
	return storage.New(cfg)
}

func provideLeaderboardRedis(client *goredis.Client) *redisrepo.LeaderboardRedis {
	return redisrepo.NewLeaderboardRedis(client, redisrepo.DefaultLeaderboardKey)
}

func provideMatchmakingRedis(client *goredis.Client) *redisrepo.MatchmakingRedis {
	return redisrepo.NewMatchmakingRedis(client, redisrepo.DefaultMatchmakingQueueKey)
}

func provideAuthConfig(cfg *config.Config) adminusecase.AuthConfig {
	return adminusecase.AuthConfig{
		Secret:        []byte(cfg.JWT.Secret),
		AccessTTL:     cfg.JWT.AccessTTL,
		RefreshTTL:    cfg.JWT.RefreshTTL,
		AdminPassword: []byte(cfg.Admin.Password),
	}
}

func provideHealthChecks(
	pool *pgxpool.Pool,
	redis *goredis.Client,
	seaweed *storage.SeaweedStorage,
	schemaVersion usecase.SchemaVersionReader,
) restv1.HealthChecks {
	return restv1.HealthChecks{
		DB: usecase.HealthCheckerFunc(func(ctx context.Context) error {
			return pgclient.HealthCheck(ctx, pool)
		}),
		Redis: usecase.HealthCheckerFunc(func(ctx context.Context) error {
			return redisclient.HealthCheck(ctx, redis)
		}),
		SeaweedFS: usecase.HealthCheckerFunc(func(ctx context.Context) error {
			return seaweed.EnsureBucket(ctx)
		}),
		SchemaVersion: schemaVersion,
	}
}

func provideFlagSubmitUsecase(
	tx usecase.TxManager,
	duels usecase.DuelRepo,
	players usecase.PlayerRepo,
	history usecase.HistoryRepo,
	board usecase.LeaderboardStore,
	clk clock.Clock,
	timers *duelusecase.TimerRegistry,
) *duelusecase.FlagSubmitUsecase {
	return duelusecase.NewFlagSubmitUsecase(tx, duels, players, history, board, clk, timers)
}

func provideRESTServer(
	players usecase.Player,
	auth usecase.AdminAuth,
	tasks usecase.AdminTask,
	upload usecase.Upload,
	leaderboard usecase.Leaderboard,
	duels usecase.Duel,
	health restv1.HealthChecks,
) *restv1.Server {
	return restv1.New(restv1.Dependencies{
		Players:     players,
		AdminAuth:   auth,
		Tasks:       tasks,
		Upload:      upload,
		Leaderboard: leaderboard,
		Duels:       duels,
		Health:      health,
	})
}

func provideRESTMiddlewares(log logkit.Logger, cfg *config.Config) []openapi.MiddlewareFunc {
	return []openapi.MiddlewareFunc{
		middleware.Build(log, middleware.WithTimeout(cfg.HTTP.WriteTimeout)),
	}
}

func provideHubRegistry() *websocket.HubRegistry {
	return websocket.NewHubRegistry()
}

func provideHintScheduler(clk clock.Clock) *duelusecase.HintScheduler {
	return duelusecase.NewHintScheduler(clk, nil)
}

func provideDuelTimers(
	timers *duelusecase.TimerRegistry,
	hints *duelusecase.HintScheduler,
) duelusecase.DuelTimer {
	return websocket.NewPauseableDuelTimers(timers, hints)
}

func provideRawWebSocketServer(
	ctx context.Context,
	players usecase.PlayerRepo,
	matchmaking websocket.Matchmaking,
	flags websocket.FlagSubmitter,
	hubs *websocket.HubRegistry,
	hints *duelusecase.HintScheduler,
) rawWebSocketServer {
	return rawWebSocketServer{
		Server: websocket.NewServer(
			players,
			matchmaking,
			flags,
			hubs,
			websocket.WithContext(ctx),
			websocket.WithHintScheduler(hints),
		),
	}
}

func provideDuelBroadcaster(server rawWebSocketServer) usecase.DuelBroadcaster {
	return server.Broadcaster()
}

func provideReconnectManager(
	tx usecase.TxManager,
	duels usecase.DuelRepo,
	players usecase.PlayerRepo,
	timers duelusecase.DuelTimer,
	broadcaster usecase.DuelBroadcaster,
	clk clock.Clock,
) *duelusecase.ReconnectManager {
	return duelusecase.NewReconnectManager(tx, duels, players, timers, broadcaster, clk)
}

func provideWebSocketServer(
	server rawWebSocketServer,
	reconnect *duelusecase.ReconnectManager,
) *websocket.Server {
	server.SetReconnectManager(reconnect)
	return server.Server
}

func provideHTTPHandler(
	rest *restv1.Server,
	ws *websocket.Server,
	auth *adminusecase.AuthUsecase,
	players usecase.PlayerRepo,
	middlewares []openapi.MiddlewareFunc,
) http.Handler {
	router := chi.NewRouter()
	router.Handle("/ws", ws)
	return restv1.NewHandler(rest, restv1.HandlerOptions{
		Router:      router,
		AdminAuth:   auth,
		PlayerRepo:  players,
		Middlewares: middlewares,
	})
}

func provideHTTPServer(cfg *config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         net.JoinHostPort(cfg.HTTP.Host, strconv.Itoa(cfg.HTTP.Port)),
		Handler:      handler,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
	}
}

func provideApp(
	seaweed *storage.SeaweedStorage,
	tx usecase.TxManager,
	duels usecase.ActiveDuelRepo,
	players usecase.PlayerStatusRepo,
	broadcaster usecase.DuelBroadcaster,
	clk clock.Clock,
	server *http.Server,
	ws *websocket.Server,
) *App {
	return &App{
		Storage:     seaweed,
		Server:      server,
		WebSocket:   ws,
		Tx:          tx,
		Duels:       duels,
		Players:     players,
		Broadcaster: broadcaster,
		Clock:       clk,
	}
}
