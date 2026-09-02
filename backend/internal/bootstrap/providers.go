package bootstrap

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	arenaws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/arena"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	arenausecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

type rawWebSocketServer struct {
	*websocket.Server
}

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time {
	return f().UTC()
}

func provideRuntimeContext(runtime *RuntimeContext) context.Context {
	return runtime.Context()
}

func provideClock() clockFunc {
	return time.Now
}

type arenaCore struct {
	tournaments *arenausecase.TournamentUseCase
	attendance  *arenausecase.AttendanceUseCase
	rosters     *arenausecase.RosterLockUseCase
	lifecycle   *arenausecase.TournamentLifecycleUseCase
}

type arenaObservability struct {
	metrics  *observability.ArenaMetrics
	observer observability.ArenaEventObserver
}

type arenaHealthProbe struct {
	runtime context.Context
	pool    *pgxpool.Pool
	clock   clockFunc
	metrics *observability.ArenaMetrics
}

const (
	arenaPublicMaxConnections  = 128
	arenaPublicMaxReplayEvents = 128
)

type arenaWebSocketOptions struct {
	participant      websocket.ArenaParticipantConnectionFlow
	public           websocket.ArenaPublicConnectionFlow
	operator         websocket.ArenaOperatorConnectionFlow
	terminal         websocket.ArenaTerminalSubscriptionFlow
	operatorResolver websocket.ArenaOperatorPrincipalResolver
}

func provideArenaCore(
	tournaments arenausecase.TournamentRepository,
	attendance arenausecase.AttendanceRepository,
	rosters arenausecase.RosterLockRepository,
	lifecycle arenausecase.TournamentLifecycleRepository,
	clk arenausecase.Clock,
	telemetry arenaObservability,
) *arenaCore {
	return &arenaCore{
		tournaments: arenausecase.NewTournamentUseCase(tournaments, clk),
		attendance:  arenausecase.NewAttendanceUseCase(attendance, clk),
		rosters:     arenausecase.NewRosterLockUseCase(rosters, clk),
		lifecycle:   arenausecase.NewTournamentLifecycleUseCase(lifecycle, clk, telemetry.observer),
	}
}

func provideArenaObservability(log logkit.Logger) arenaObservability {
	metrics := observability.NewArenaMetrics()
	return arenaObservability{
		metrics: metrics,
		observer: observability.NewArenaEventFanout(
			observability.NewArenaStructuredLogger(log),
			metrics,
		),
	}
}

func provideArenaHealthSource(
	runtime context.Context,
	pool *pgxpool.Pool,
	clk clockFunc,
	telemetry arenaObservability,
) *arenaHealthProbe {
	return &arenaHealthProbe{
		runtime: runtime,
		pool:    pool,
		clock:   clk,
		metrics: telemetry.metrics,
	}
}

func (probe *arenaHealthProbe) ArenaHealth(ctx context.Context) observability.ArenaHealthSnapshot {
	snapshot := observability.HealthyArenaHealthSnapshot()
	if probe == nil || probe.pool == nil || postgres.HealthCheck(ctx, probe.pool) != nil {
		failed := observability.ArenaDependencyStatus{
			Health:    observability.ArenaHealthStateFailed,
			Readiness: observability.ArenaReadinessStateNotReady,
		}
		snapshot.Authority = failed
		snapshot.Submission = failed
		snapshot.TaskDelivery = failed
		snapshot.Outbox = failed
		snapshot.Clock = failed
		snapshot.Recovery = failed
	} else {
		probe.measureClockDrift(ctx, &snapshot)
	}
	if probe == nil || probe.runtime == nil {
		snapshot.Realtime = observability.ArenaDependencyStatus{
			Health:    observability.ArenaHealthStateFailed,
			Readiness: observability.ArenaReadinessStateNotReady,
		}
	} else {
		select {
		case <-probe.runtime.Done():
			snapshot.Realtime = observability.ArenaDependencyStatus{
				Health:    observability.ArenaHealthStateFailed,
				Readiness: observability.ArenaReadinessStateNotReady,
			}
		default:
		}
	}
	return snapshot
}

func (probe *arenaHealthProbe) measureClockDrift(
	ctx context.Context,
	snapshot *observability.ArenaHealthSnapshot,
) {
	if probe.clock == nil {
		snapshot.Clock = observability.ArenaDependencyStatus{
			Health:    observability.ArenaHealthStateFailed,
			Readiness: observability.ArenaReadinessStateNotReady,
		}
		return
	}
	var databaseNow time.Time
	if err := probe.pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&databaseNow); err != nil {
		snapshot.Clock = observability.ArenaDependencyStatus{
			Health:    observability.ArenaHealthStateFailed,
			Readiness: observability.ArenaReadinessStateNotReady,
		}
		return
	}
	drift := probe.clock.Now().Sub(databaseNow.UTC())
	if probe.metrics != nil {
		_ = probe.metrics.SetClockDrift("database", drift)
	}
	absoluteDrift := drift
	if absoluteDrift < 0 {
		absoluteDrift = -absoluteDrift
	}
	switch {
	case absoluteDrift > 30*time.Second:
		snapshot.Clock = observability.ArenaDependencyStatus{
			Health:    observability.ArenaHealthStateFailed,
			Readiness: observability.ArenaReadinessStateNotReady,
		}
	case absoluteDrift > 2*time.Second:
		snapshot.Clock = observability.ArenaDependencyStatus{
			Health:    observability.ArenaHealthStateDegraded,
			Readiness: observability.ArenaReadinessStateNotReady,
		}
	}
}

func provideArenaPublicRealtimeConfig() *arenaws.PublicRealtimeConfig {
	// Arena MVP runs one 16-player event per process. This leaves bounded room
	// for spectators and matches the existing operator replay limit.
	return &arenaws.PublicRealtimeConfig{
		MaxConnections:  arenaPublicMaxConnections,
		MaxReplayEvents: arenaPublicMaxReplayEvents,
	}
}

func provideArenaCancellationCoordinators() map[uuid.UUID]*arenaws.CancellationCoordinator {
	return make(map[uuid.UUID]*arenaws.CancellationCoordinator)
}

func provideArenaWebSocketOptions(
	participant websocket.ArenaParticipantConnectionFlow,
	public websocket.ArenaPublicConnectionFlow,
	operator websocket.ArenaOperatorConnectionFlow,
	terminal websocket.ArenaTerminalSubscriptionFlow,
	operatorResolver websocket.ArenaOperatorPrincipalResolver,
) arenaWebSocketOptions {
	return arenaWebSocketOptions{
		participant:      participant,
		public:           public,
		operator:         operator,
		terminal:         terminal,
		operatorResolver: operatorResolver,
	}
}

func provideArenaOperatorPrincipalResolver(
	auth *adminusecase.AuthUseCase,
) websocket.ArenaOperatorPrincipalResolver {
	return func(
		r *http.Request,
		_ *domain.Player,
		tournamentID uuid.UUID,
	) (arenaws.OperatorRealtimePrincipal, bool) {
		if auth == nil || r == nil || tournamentID == uuid.Nil {
			return arenaws.OperatorRealtimePrincipal{}, false
		}
		token, ok := middleware.AdminAccessTokenFromRequest(r)
		if !ok {
			return arenaws.OperatorRealtimePrincipal{}, false
		}
		claims, err := auth.VerifyAccess(r.Context(), token)
		if err != nil || claims == nil {
			return arenaws.OperatorRealtimePrincipal{}, false
		}
		subject := strings.TrimSpace(claims.Subject)
		if subject == "" {
			return arenaws.OperatorRealtimePrincipal{}, false
		}
		principalID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("arena-operator:"+subject))
		if principalID == uuid.Nil {
			return arenaws.OperatorRealtimePrincipal{}, false
		}
		return arenaws.OperatorRealtimePrincipal{
			Authenticated: true,
			PrincipalID:   principalID,
			Role:          arenaws.OperatorRealtimeRole,
			TournamentID:  tournamentID,
		}, true
	}
}

func providePostgresConfig(cfg *config.Config) postgres.Config {
	return postgres.Config{
		DSN:      cfg.DB.DSN,
		MaxConns: cfg.DB.MaxConns,
	}
}

func providePostgres(ctx context.Context, cfg postgres.Config) (*pgxpool.Pool, func(), error) {
	pool, err := postgres.New(ctx, cfg)
	if err != nil {
		return nil, func() {}, err
	}
	return pool, pool.Close, nil
}

func provideRedisConfig(cfg *config.Config) redisadapter.Config {
	return redisadapter.Config{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	}
}

func provideRedis(ctx context.Context, cfg redisadapter.Config) (*goredis.Client, func(), error) {
	client, err := redisadapter.New(ctx, cfg)
	if err != nil {
		return nil, func() {}, err
	}
	return client, func() { _ = client.Close() }, nil
}

func provideSeaweedConfig(cfg *config.Config) objectstorage.Config {
	return objectstorage.Config{
		Endpoint:       cfg.SeaweedFS.Endpoint,
		PublicEndpoint: cfg.SeaweedFS.PublicEndpoint,
		AccessKey:      cfg.SeaweedFS.AccessKey,
		SecretKey:      cfg.SeaweedFS.SecretKey,
		Bucket:         cfg.SeaweedFS.Bucket,
		Secure:         cfg.SeaweedFS.Secure,
		PublicSecure:   cfg.SeaweedFS.PublicSecure,
	}
}

func provideSeaweedStorage(cfg objectstorage.Config) (*objectstorage.SeaweedStorage, error) {
	return objectstorage.New(cfg)
}

func provideLeaderboardRedis(client *goredis.Client) *redisadapter.LeaderboardRedis {
	return redisadapter.NewLeaderboardRedis(client, redisadapter.DefaultLeaderboardKey)
}

func provideMatchmakingRedis(client *goredis.Client) *redisadapter.MatchmakingRedis {
	return redisadapter.NewMatchmakingRedis(client, redisadapter.DefaultMatchmakingQueueKey)
}

func provideRevocationRedis(client *goredis.Client) *redisadapter.RevocationRedis {
	return redisadapter.NewRevocationRedis(client, redisadapter.DefaultRevocationKeyPrefix)
}

func provideAuthConfig(cfg *config.Config) adminusecase.AuthConfig {
	return adminusecase.AuthConfig{
		Secret:        []byte(cfg.JWT.Secret),
		AccessTTL:     cfg.JWT.AccessTTL,
		RefreshTTL:    cfg.JWT.RefreshTTL,
		AdminPassword: []byte(cfg.Admin.Password),
	}
}

func provideAuthUseCase(
	cfg adminusecase.AuthConfig,
	clk adminusecase.Clock,
	revocation adminusecase.RevocationStore,
) *adminusecase.AuthUseCase {
	return adminusecase.NewAuthUseCase(cfg, clk, revocation)
}

func provideAdminTaskUseCase(tasks adminusecase.TaskRepository) *adminusecase.TaskUseCase {
	return adminusecase.NewTaskUseCase(tasks)
}

func provideAdminPlayerUseCase(
	tx adminusecase.TransactionManager,
	players adminusecase.PlayerRepository,
	leaderboard adminusecase.LeaderboardInvalidator,
	clk adminusecase.Clock,
) *adminusecase.PlayerUseCase {
	return adminusecase.NewPlayerUseCase(tx, players, leaderboard, clk)
}

func provideReadUseCase(duels duelusecase.ReadDuelRepository) *duelusecase.ReadUseCase {
	return duelusecase.NewReadUseCase(duels)
}

func provideLeaderboardUseCase(
	store leaderboardusecase.WinStore,
	repo leaderboardusecase.StatsRepository,
	clk leaderboardusecase.Clock,
) *leaderboardusecase.UseCase {
	return leaderboardusecase.NewUseCase(store, repo, clk)
}

func provideHealthChecks(
	pool *pgxpool.Pool,
	redis *goredis.Client,
	seaweed *objectstorage.SeaweedStorage,
	schemaVersion restv1.SchemaVersionReader,
	arena *arenaHealthProbe,
) restv1.HealthChecks {
	return restv1.HealthChecks{
		DB: restv1.HealthCheckerFunc(func(ctx context.Context) error {
			return postgres.HealthCheck(ctx, pool)
		}),
		Redis: restv1.HealthCheckerFunc(func(ctx context.Context) error {
			return redisadapter.HealthCheck(ctx, redis)
		}),
		SeaweedFS: restv1.HealthCheckerFunc(func(ctx context.Context) error {
			return seaweed.EnsureBucket(ctx)
		}),
		SchemaVersion: schemaVersion,
		Arena:         arena,
	}
}

func provideFlagSubmitUseCase(
	tx duelusecase.TransactionManager,
	duels duelusecase.FlagDuelRepository,
	players duelusecase.FinalizationPlayerRepository,
	history duelusecase.SolvedHistoryWriter,
	board duelusecase.LeaderboardBumper,
	clk duelusecase.Clock,
	timers *duelusecase.TimerRegistry,
	log logkit.Logger,
) *duelusecase.FlagSubmitUseCase {
	return duelusecase.NewFlagSubmitUseCase(tx, duels, players, history, board, clk, timers).
		Configure(duelusecase.WithFlagSubmitLogger(log))
}

func providePlayerUseCase(
	cfg *config.Config,
	tx playerusecase.TransactionManager,
	players playerusecase.Repository,
	duels playerusecase.ActiveDuelReader,
	clk playerusecase.Clock,
) *playerusecase.UseCase {
	return playerusecase.NewUseCase(
		tx,
		players,
		duels,
		clk,
		playerusecase.WithSessionTTL(cfg.Player.SessionTTL),
	)
}

func provideMatchmakingUseCase(
	tx duelusecase.TransactionManager,
	queue duelusecase.MatchmakingQueue,
	players duelusecase.MatchmakingPlayerRepository,
	tasks duelusecase.MatchmakingTaskRepository,
	history duelusecase.MatchmakingHistoryRepository,
	duels duelusecase.MatchmakingDuelRepository,
	storage duelusecase.SourceFileURLSigner,
	clk duelusecase.Clock,
	log logkit.Logger,
) *duelusecase.MatchmakingUseCase {
	return duelusecase.NewMatchmakingUseCase(tx, queue, players, tasks, history, duels, storage, clk).
		Configure(duelusecase.WithMatchmakingLogger(log))
}

func provideUploadUseCase(
	tasks adminusecase.UploadTaskRepository,
	storage adminusecase.SourceFileStorage,
	log logkit.Logger,
) *adminusecase.UploadUseCase {
	return adminusecase.NewUploadUseCase(tasks, storage).
		Configure(adminusecase.WithUploadLogger(log))
}

func provideTimerRegistry(
	ctx context.Context,
	tx duelusecase.TransactionManager,
	duels duelusecase.FinalizationDuelRepository,
	players duelusecase.FinalizationPlayerRepository,
	clk duelusecase.Clock,
	log logkit.Logger,
) *duelusecase.TimerRegistry {
	return duelusecase.NewTimerRegistry(tx, duels, players, clk,
		duelusecase.WithTimerRegistryContext(ctx),
		duelusecase.WithTimerRegistryLogger(log),
	)
}

func provideRESTServerWithClock(
	players restv1.PlayerService,
	auth restv1.AdminAuthService,
	tasks restv1.AdminTaskService,
	adminPlayers restv1.AdminPlayerService,
	adminPlayerEvents restv1.AdminPlayerEventSubscriber,
	upload restv1.UploadService,
	leaderboard restv1.LeaderboardService,
	duels restv1.DuelService,
	health restv1.HealthChecks,
	clk clockFunc,
	loginLimiter *middleware.LoginRateLimiter,
	refreshLimiter adminRefreshRateLimiter,
	joinLimiter *middleware.JoinRateLimiter,
	leaderboardLimiter leaderboardRateLimiter,
	log logkit.Logger,
	telemetry arenaObservability,
) *restv1.Server {
	var now func() time.Time
	if clk != nil {
		now = clk.Now
	}
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
		ArenaMetrics:       telemetry.metrics.Gatherer(),
		Now:                now,
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

type restMiddlewareStack struct {
	RequestValidator api.MiddlewareFunc
	Outer            []api.MiddlewareFunc
}

func provideRESTMiddlewares(
	ctx context.Context,
	log logkit.Logger,
	cfg *config.Config,
	telemetry arenaObservability,
) (restMiddlewareStack, error) {
	openAPIValidator, err := middleware.OpenAPIRequestValidator(ctx, log)
	if err != nil {
		return restMiddlewareStack{}, err
	}

	return restMiddlewareStack{
		RequestValidator: openAPIValidator,
		Outer: []api.MiddlewareFunc{
			middleware.Build(
				log,
				middleware.WithTimeout(cfg.HTTP.WriteTimeout),
				middleware.WithTrustedProxyCIDRs(cfg.HTTP.TrustedProxyCIDRs),
				middleware.WithAllowedOrigins(cfg.HTTP.AllowedOrigins),
				middleware.WithArenaEventObserver(telemetry.observer),
			),
		},
	}, nil
}

func provideHubRegistry() *websocket.HubRegistry {
	return websocket.NewHubRegistry()
}

func provideHintScheduler(clk duelusecase.Clock) *duelusecase.HintScheduler {
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

func provideRawWebSocketServerWithArena(
	ctx context.Context,
	cfg *config.Config,
	log logkit.Logger,
	players websocket.PlayerReader,
	matchmaking websocket.Matchmaking,
	flags websocket.FlagSubmitter,
	hubs *websocket.HubRegistry,
	hints *duelusecase.HintScheduler,
	timers *duelusecase.TimerRegistry,
	duels websocket.DuelTaskReader,
	storage websocket.SourceFileURLSigner,
	handshakeLimiter *wsHandshakeRateLimiter,
	arena arenaWebSocketOptions,
	telemetry arenaObservability,
) rawWebSocketServer {
	return provideRawWebSocketServerOptions(
		ctx,
		cfg,
		log,
		players,
		matchmaking,
		flags,
		hubs,
		hints,
		timers,
		duels,
		storage,
		handshakeLimiter,
		arena,
		telemetry,
	)
}

func provideRawWebSocketServerOptions(
	ctx context.Context,
	cfg *config.Config,
	log logkit.Logger,
	players websocket.PlayerReader,
	matchmaking websocket.Matchmaking,
	flags websocket.FlagSubmitter,
	hubs *websocket.HubRegistry,
	hints *duelusecase.HintScheduler,
	timers *duelusecase.TimerRegistry,
	duels websocket.DuelTaskReader,
	storage websocket.SourceFileURLSigner,
	handshakeLimiter *wsHandshakeRateLimiter,
	arena arenaWebSocketOptions,
	telemetry arenaObservability,
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
		websocket.WithArenaEventObserver(telemetry.observer),
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
	if arena.participant != nil {
		options = append(options, websocket.WithArenaParticipantFlow(arena.participant))
	}
	if arena.public != nil {
		options = append(options, websocket.WithArenaPublicFlow(arena.public))
	}
	if arena.operator != nil {
		options = append(options, websocket.WithArenaOperatorFlow(arena.operator))
	}
	if arena.terminal != nil {
		options = append(options, websocket.WithArenaTerminalFlow(arena.terminal))
	}
	if arena.operatorResolver != nil {
		options = append(options, websocket.WithArenaOperatorPrincipalResolver(arena.operatorResolver))
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

func provideDuelBroadcaster(server rawWebSocketServer) *websocket.Broadcaster {
	return server.Broadcaster()
}

func provideReconnectManager(
	ctx context.Context,
	tx duelusecase.TransactionManager,
	duels duelusecase.ReconnectDuelRepository,
	players duelusecase.FinalizationPlayerRepository,
	timers duelusecase.DuelTimer,
	broadcaster *websocket.Broadcaster,
	clk duelusecase.Clock,
	board duelusecase.LeaderboardBumper,
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
	auth *adminusecase.AuthUseCase,
	players middleware.PlayerSessionReader,
	middlewares restMiddlewareStack,
	log logkit.Logger,
) http.Handler {
	generatedRouter := chi.NewRouter()
	generatedRouter.Handle("/ws", ws)
	handler := restv1.NewHandler(rest, restv1.HandlerOptions{
		Router:           generatedRouter,
		AdminAuth:        auth,
		PlayerRepo:       players,
		RequestValidator: middlewares.RequestValidator,
		Middlewares:      middlewares.Outer,
	})

	router := chi.NewRouter()
	router.Method(http.MethodGet, "/internal/metrics", rest.ArenaPrivateMetricsHandler())
	router.Method(http.MethodGet, "/api/v1/admin/players/events", adminPlayerEventsHandler(rest, auth, log, cfg))
	router.Mount("/", handler)

	return middleware.CORS(cfg.HTTP.AllowedOrigins)(router)
}

func adminPlayerEventsHandler(rest *restv1.Server, auth *adminusecase.AuthUseCase, log logkit.Logger, cfg *config.Config) http.Handler {
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

func provideMigrator(cfg *config.Config) *Migrator {
	return NewMigrator(cfg.DB.DSN, ResolveMigrationsDir(migrationsDir))
}

func provideStartupRecoverer(
	tx recovery.TransactionManager,
	duels recovery.ActiveDuelRepository,
	duelTasks recovery.DuelTaskReader,
	players recovery.PlayerStatusRepository,
	queued recovery.QueuedPlayerResetter,
	queue recovery.QueueCleaner,
	broadcaster *websocket.Broadcaster,
	reconnect *duelusecase.ReconnectManager,
	hints *duelusecase.HintScheduler,
	clk recovery.Clock,
	log logkit.Logger,
) *recovery.StartupRecoverer {
	return recovery.NewStartupRecoverer(
		tx,
		duels,
		duelTasks,
		players,
		queued,
		queue,
		broadcaster,
		reconnect,
		hints,
		clk,
		log,
	)
}

func provideArenaApplication(
	cfg *config.Config,
	log logkit.Logger,
	runtime *RuntimeContext,
	seaweed *objectstorage.SeaweedStorage,
	migrator *Migrator,
	recoverer *recovery.StartupRecoverer,
	arena *arenaCore,
	server *http.Server,
	ws *websocket.Server,
	revocation RevocationJanitor,
) *App {
	return newArenaApplication(cfg, log, runtime, seaweed, migrator, recoverer, arena, server, ws, revocation)
}
