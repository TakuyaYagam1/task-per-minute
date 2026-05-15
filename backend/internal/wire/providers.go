package wire

import (
	"context"
	"net"
	"net/http"
	"strconv"

	coderws "github.com/coder/websocket"
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
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
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
		Endpoint:       cfg.SeaweedFS.Endpoint,
		PublicEndpoint: cfg.SeaweedFS.PublicEndpoint,
		AccessKey:      cfg.SeaweedFS.AccessKey,
		SecretKey:      cfg.SeaweedFS.SecretKey,
		Bucket:         cfg.SeaweedFS.Bucket,
		Secure:         cfg.SeaweedFS.Secure,
		PublicSecure:   cfg.SeaweedFS.PublicSecure,
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

func provideRevocationRedis(client *goredis.Client) *redisrepo.RevocationRedis {
	return redisrepo.NewRevocationRedis(client, redisrepo.DefaultRevocationKeyPrefix)
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
	board usecase.LeaderboardBumper,
	clk clock.Clock,
	timers *duelusecase.TimerRegistry,
	log logkit.Logger,
) *duelusecase.FlagSubmitUsecase {
	return duelusecase.NewFlagSubmitUsecase(tx, duels, players, history, board, clk, timers).
		Configure(duelusecase.WithFlagSubmitLogger(log))
}

func providePlayerUsecase(
	cfg *config.Config,
	tx usecase.TxManager,
	players usecase.PlayerRepo,
	duels usecase.DuelRepo,
	clk clock.Clock,
) *playerusecase.PlayerUsecase {
	return playerusecase.NewPlayerUsecase(
		tx,
		players,
		duels,
		playerusecase.WithSessionTTL(cfg.Player.SessionTTL),
		playerusecase.WithClock(clk),
	)
}

func provideMatchmakingUsecase(
	tx usecase.TxManager,
	queue usecase.MatchmakingQueue,
	players usecase.PlayerRepo,
	tasks usecase.TaskRepo,
	history usecase.HistoryRepo,
	duels usecase.DuelRepo,
	storage usecase.SourceFileStorage,
	clk clock.Clock,
	log logkit.Logger,
) *duelusecase.MatchmakingUsecase {
	return duelusecase.NewMatchmakingUsecase(tx, queue, players, tasks, history, duels, storage, clk).
		Configure(duelusecase.WithMatchmakingLogger(log))
}

func provideUploadUsecase(
	tasks usecase.TaskRepo,
	storage usecase.SourceFileStorage,
	log logkit.Logger,
) *adminusecase.UploadUsecase {
	return adminusecase.NewUploadUsecase(tasks, storage).
		Configure(adminusecase.WithUploadLogger(log))
}

func provideTimerRegistry(
	ctx context.Context,
	tx usecase.TxManager,
	duels usecase.DuelRepo,
	players usecase.PlayerRepo,
	clk clock.Clock,
	log logkit.Logger,
) *duelusecase.TimerRegistry {
	return duelusecase.NewTimerRegistry(tx, duels, players, clk,
		duelusecase.WithTimerRegistryContext(ctx),
		duelusecase.WithTimerRegistryLogger(log),
	)
}

func provideRESTServer(
	players usecase.Player,
	auth usecase.AdminAuth,
	tasks usecase.AdminTask,
	adminPlayers usecase.AdminPlayer,
	adminPlayerEvents usecase.AdminPlayerEvents,
	upload usecase.Upload,
	leaderboard usecase.Leaderboard,
	duels usecase.Duel,
	health restv1.HealthChecks,
	loginLimiter *middleware.LoginRateLimiter,
	refreshLimiter adminRefreshRateLimiter,
	joinLimiter *middleware.JoinRateLimiter,
	leaderboardLimiter leaderboardRateLimiter,
	log logkit.Logger,
) *restv1.Server {
	return restv1.New(restv1.Dependencies{
		Players:            players,
		AdminAuth:          auth,
		Tasks:              tasks,
		AdminPlayers:       adminPlayers,
		AdminPlayerEvents:  adminPlayerEvents,
		Upload:             upload,
		Leaderboard:        leaderboard,
		Duels:              duels,
		Health:             health,
		LoginLimiter:       loginLimiter,
		RefreshLimiter:     refreshLimiter.Inner,
		JoinLimiter:        joinLimiter,
		LeaderboardLimiter: leaderboardLimiter.Inner,
		Log:                log,
	})
}

func provideLoginRateLimiter(ctx context.Context, cfg *config.Config) *middleware.LoginRateLimiter {
	return middleware.NewLoginRateLimiter(
		ctx,
		cfg.Admin.LoginRateAttempts,
		cfg.Admin.LoginRateWindow,
		cfg.Admin.LoginRateBucketTTL,
	)
}

type adminRefreshRateLimiter struct {
	Inner *middleware.LoginRateLimiter
}

func provideRefreshRateLimiter(ctx context.Context, cfg *config.Config) adminRefreshRateLimiter {
	return adminRefreshRateLimiter{
		Inner: middleware.NewLoginRateLimiter(
			ctx,
			cfg.Admin.RefreshRateAttempts,
			cfg.Admin.RefreshRateWindow,
			cfg.Admin.RefreshRateBucketTTL,
		),
	}
}

func provideJoinRateLimiter(ctx context.Context, cfg *config.Config) *middleware.JoinRateLimiter {
	return middleware.NewJoinRateLimiter(
		ctx,
		cfg.Player.JoinRateAttempts,
		cfg.Player.JoinRateWindow,
		cfg.Player.JoinRateBucketTTL,
	)
}

type leaderboardRateLimiter struct {
	Inner *middleware.LoginRateLimiter
}

func provideLeaderboardRateLimiter(ctx context.Context, cfg *config.Config) leaderboardRateLimiter {
	return leaderboardRateLimiter{
		Inner: middleware.NewLoginRateLimiter(
			ctx,
			cfg.Leaderboard.RateAttempts,
			cfg.Leaderboard.RateWindow,
			cfg.Leaderboard.RateBucketTTL,
		),
	}
}

func provideRESTMiddlewares(log logkit.Logger, cfg *config.Config) []openapi.MiddlewareFunc {
	return []openapi.MiddlewareFunc{
		middleware.Build(
			log,
			middleware.WithTimeout(cfg.HTTP.WriteTimeout),
			middleware.WithTrustedProxyCIDRs(cfg.HTTP.TrustedProxyCIDRs),
			middleware.WithAllowedOrigins(cfg.HTTP.AllowedOrigins),
		),
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

// wsHandshakeRateLimiter is what provideRawWebSocketServer accepts; using a
// concrete type (a thin wrapper around middleware.LoginRateLimiter) keeps the
// wire graph free of nil-interface ambiguity. The wrapper field is exported
// so wire can build it via a struct literal in wire_gen.go without a setter.
type wsHandshakeRateLimiter struct {
	Inner *middleware.LoginRateLimiter
}

func (l *wsHandshakeRateLimiter) Allow(ip string) bool {
	if l == nil || l.Inner == nil {
		return true
	}
	return l.Inner.Allow(ip)
}

func (l *wsHandshakeRateLimiter) RetryAfter() string {
	if l == nil || l.Inner == nil {
		return ""
	}
	return l.Inner.RetryAfter()
}

func provideRawWebSocketServer(
	ctx context.Context,
	cfg *config.Config,
	log logkit.Logger,
	players usecase.PlayerRepo,
	matchmaking websocket.Matchmaking,
	flags websocket.FlagSubmitter,
	hubs *websocket.HubRegistry,
	hints *duelusecase.HintScheduler,
	timers *duelusecase.TimerRegistry,
	duels usecase.DuelRepo,
	storage usecase.SourceFileStorage,
	handshakeLimiter *wsHandshakeRateLimiter,
) rawWebSocketServer {
	clientIPResolver, err := middleware.NewClientIPResolver(cfg.HTTP.TrustedProxyCIDRs)
	if err != nil {
		if log != nil {
			log.Warn("invalid trusted proxy CIDRs for websocket, using RemoteAddr only", logkit.Fields{"error": err.Error()})
		}
		clientIPResolver = middleware.ClientIPFromRequest
	}
	options := []websocket.Option{
		websocket.WithContext(ctx),
		websocket.WithHintScheduler(hints),
		websocket.WithTimerStopper(timers),
		websocket.WithTaskResolver(duels, storage),
		websocket.WithClientIPResolver(clientIPResolver),
		websocket.WithRequireOrigin(cfg.WS.RequireOrigin),
		websocket.WithLogger(log),
		websocket.WithInboundRateLimits(websocket.InboundRateLimits{
			MessageAttempts: cfg.WS.MessageRateAttempts,
			MessageWindow:   cfg.WS.MessageRateWindow,
			ActionAttempts:  cfg.WS.ActionRateAttempts,
			ActionWindow:    cfg.WS.ActionRateWindow,
		}),
	}
	if accept := provideWSAcceptOptions(cfg); accept != nil {
		options = append(options, websocket.WithAcceptOptions(accept))
	}
	if handshakeLimiter != nil && handshakeLimiter.Inner != nil {
		options = append(options, websocket.WithHandshakeRateLimiter(handshakeLimiter))
	}
	return rawWebSocketServer{
		Server: websocket.NewServer(players, matchmaking, flags, hubs, options...),
	}
}

// provideHandshakeRateLimiter creates a per-IP gate for /ws handshakes.
// The reuse of LoginRateLimiter is intentional - it already provides token
// bucket semantics with the right TTL and IP-keyed eviction.
func provideHandshakeRateLimiter(
	ctx context.Context,
	cfg *config.Config,
) *wsHandshakeRateLimiter {
	if cfg == nil {
		return &wsHandshakeRateLimiter{}
	}
	inner := middleware.NewLoginRateLimiter(
		ctx,
		cfg.WS.HandshakeRateAttempts,
		cfg.WS.HandshakeRateWindow,
		cfg.WS.HandshakeRateBucketTTL,
	)
	return &wsHandshakeRateLimiter{Inner: inner}
}

// provideWSAcceptOptions returns nil when no allowed origins are configured -
// coder/websocket then applies its same-origin default, which is the right
// behaviour for a single-domain deploy. When WS_ALLOWED_ORIGINS is supplied
// (e.g. the frontend lives on a different host) it becomes the explicit
// allowlist passed to coderws.Accept.
func provideWSAcceptOptions(cfg *config.Config) *coderws.AcceptOptions {
	if cfg == nil || len(cfg.WS.AllowedOrigins) == 0 {
		return nil
	}
	patterns := make([]string, 0, len(cfg.WS.AllowedOrigins))
	for _, raw := range cfg.WS.AllowedOrigins {
		if raw != "" {
			patterns = append(patterns, raw)
		}
	}
	if len(patterns) == 0 {
		return nil
	}
	return &coderws.AcceptOptions{OriginPatterns: patterns}
}

func provideDuelBroadcaster(server rawWebSocketServer) usecase.DuelBroadcaster {
	return server.Broadcaster()
}

func provideReconnectManager(
	ctx context.Context,
	tx usecase.TxManager,
	duels usecase.DuelRepo,
	players usecase.PlayerRepo,
	timers duelusecase.DuelTimer,
	broadcaster usecase.DuelBroadcaster,
	clk clock.Clock,
	board usecase.LeaderboardBumper,
	log logkit.Logger,
) *duelusecase.ReconnectManager {
	return duelusecase.NewReconnectManager(tx, duels, players, timers, broadcaster, clk,
		duelusecase.WithLeaderboardStore(board),
		duelusecase.WithReconnectContext(ctx),
		duelusecase.WithReconnectLogger(log),
	)
}

func provideWebSocketServer(
	server rawWebSocketServer,
	reconnect *duelusecase.ReconnectManager,
) *websocket.Server {
	server.SetReconnectManager(reconnect)
	return server.Server
}

func provideHTTPHandler(
	cfg *config.Config,
	rest *restv1.Server,
	ws *websocket.Server,
	auth *adminusecase.AuthUsecase,
	players usecase.PlayerRepo,
	middlewares []openapi.MiddlewareFunc,
	log logkit.Logger,
) http.Handler {
	generatedRouter := chi.NewRouter()
	generatedRouter.Handle("/ws", ws)
	handler := restv1.NewHandler(rest, restv1.HandlerOptions{
		Router:      generatedRouter,
		AdminAuth:   auth,
		PlayerRepo:  players,
		Middlewares: middlewares,
	})

	router := chi.NewRouter()
	router.Method(http.MethodGet, "/api/v1/admin/players/events", adminPlayerEventsHandler(rest, auth, log, cfg))
	router.Mount("/", handler)

	return middleware.CORS(cfg.HTTP.AllowedOrigins)(router)
}

func adminPlayerEventsHandler(rest *restv1.Server, auth *adminusecase.AuthUsecase, log logkit.Logger, cfg *config.Config) http.Handler {
	var handler http.Handler = http.HandlerFunc(rest.StreamAdminPlayerEvents)
	if auth != nil {
		handler = middleware.AdminJWT(auth)(handler)
	}
	handler = middleware.NoStoreSensitiveResponses()(handler)
	if cfg != nil {
		handler = middleware.BuildStreaming(
			log,
			middleware.WithTrustedProxyCIDRs(cfg.HTTP.TrustedProxyCIDRs),
			middleware.WithAllowedOrigins(cfg.HTTP.AllowedOrigins),
		)(handler)
	}
	return handler
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
	duelTasks usecase.DuelRepo,
	players usecase.PlayerStatusRepo,
	queued usecase.QueuedPlayerResetter,
	queue usecase.MatchmakingQueueCleaner,
	broadcaster usecase.DuelBroadcaster,
	reconnect *duelusecase.ReconnectManager,
	hints *duelusecase.HintScheduler,
	clk clock.Clock,
	server *http.Server,
	ws *websocket.Server,
	revocation RevocationJanitor,
) *App {
	return &App{
		Storage:     seaweed,
		Server:      server,
		WebSocket:   ws,
		Tx:          tx,
		Duels:       duels,
		DuelTasks:   duelTasks,
		Players:     players,
		Queued:      queued,
		Queue:       queue,
		Broadcaster: broadcaster,
		Reconnect:   reconnect,
		Hints:       hints,
		Clock:       clk,
		Revocation:  revocation,
	}
}
