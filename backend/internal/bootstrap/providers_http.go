package bootstrap

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	goredis "github.com/redis/go-redis/v9"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

func provideRESTServerWithClock(
	players restv1.PlayerService,
	auth restv1.AdminAuthService,
	tasks restv1.AdminTaskService,
	adminPlayers restv1.AdminPlayerService,
	adminPlayerEvents restv1.AdminPlayerEventSubscriber,
	upload restv1.UploadService,
	leaderboard restv1.LeaderboardService,
	tournaments inbound.TournamentUseCase,
	tournamentAdmin inbound.TournamentAdminUseCase,
	participants inbound.TournamentParticipantUseCase,
	snapshots inbound.TournamentSnapshotUseCase,
	health restv1.HealthChecks,
	clk clockFunc,
	loginLimiter loginRateLimiter,
	refreshLimiter adminRefreshRateLimiter,
	joinLimiter joinRateLimiter,
	leaderboardLimiter leaderboardRateLimiter,
	publicTournamentReadLimiter publicTournamentReadRateLimiter,
	operatorTournamentReadLimiter operatorTournamentReadRateLimiter,
	operatorTournamentMutationLimiter operatorTournamentMutationRateLimiter,
	participantTournamentReadLimiter participantTournamentReadRateLimiter,
	participantTournamentMutationLimiter participantTournamentMutationRateLimiter,
	log logkit.Logger,
) *restv1.Server {
	var now func() time.Time
	if clk != nil {
		now = clk.Now
	}
	return restv1.New(restv1.Dependencies{
		Players:                              players,
		AdminAuth:                            auth,
		Tasks:                                tasks,
		AdminPlayers:                         adminPlayers,
		AdminPlayerEvents:                    adminPlayerEvents,
		Upload:                               upload,
		Leaderboard:                          leaderboard,
		Tournaments:                          tournaments,
		TournamentAdmin:                      tournamentAdmin,
		TournamentParticipant:                participants,
		TournamentSnapshots:                  snapshots,
		Health:                               health,
		Now:                                  now,
		LoginLimiter:                         loginLimiter.Inner,
		RefreshLimiter:                       refreshLimiter.Inner,
		JoinLimiter:                          joinLimiter.Inner,
		LeaderboardLimiter:                   leaderboardLimiter.Inner,
		PublicTournamentReadLimiter:          publicTournamentReadLimiter.Inner,
		OperatorTournamentReadLimiter:        operatorTournamentReadLimiter.Inner,
		OperatorTournamentMutationLimiter:    operatorTournamentMutationLimiter.Inner,
		ParticipantTournamentReadLimiter:     participantTournamentReadLimiter.Inner,
		ParticipantTournamentMutationLimiter: participantTournamentMutationLimiter.Inner,
		Log:                                  log,
	})
}

type loginRateLimiter struct {
	Inner middleware.RateLimiter
}

func provideLoginRateLimiter(client *goredis.Client, cfg *config.Config) loginRateLimiter {
	return loginRateLimiter{Inner: redisadapter.NewRateLimiter(
		client, "admin-login", cfg.Admin.LoginRateAttempts, cfg.Admin.LoginRateWindow,
	)}
}

type adminRefreshRateLimiter struct {
	Inner middleware.RateLimiter
}

func provideRefreshRateLimiter(client *goredis.Client, cfg *config.Config) adminRefreshRateLimiter {
	return adminRefreshRateLimiter{Inner: redisadapter.NewRateLimiter(
		client, "admin-refresh", cfg.Admin.RefreshRateAttempts, cfg.Admin.RefreshRateWindow,
	)}
}

type joinRateLimiter struct {
	Inner middleware.RateLimiter
}

func provideJoinRateLimiter(client *goredis.Client, cfg *config.Config) joinRateLimiter {
	return joinRateLimiter{Inner: redisadapter.NewRateLimiter(
		client, "player-join", cfg.Player.JoinRateAttempts, cfg.Player.JoinRateWindow,
	)}
}

type leaderboardRateLimiter struct {
	Inner middleware.RateLimiter
}

func provideLeaderboardRateLimiter(client *goredis.Client, cfg *config.Config) leaderboardRateLimiter {
	return leaderboardRateLimiter{Inner: redisadapter.NewRateLimiter(
		client, "leaderboard", cfg.Leaderboard.RateAttempts, cfg.Leaderboard.RateWindow,
	)}
}

type publicTournamentReadRateLimiter struct {
	Inner middleware.RateLimiter
}

func providePublicTournamentReadRateLimiter(
	client *goredis.Client,
	cfg *config.Config,
) publicTournamentReadRateLimiter {
	return publicTournamentReadRateLimiter{Inner: redisadapter.NewRateLimiter(
		client,
		"tournament-public-read",
		cfg.Tournament.PublicReadRateAttempts,
		cfg.Tournament.PublicReadRateWindow,
	)}
}

type operatorTournamentReadRateLimiter struct {
	Inner middleware.RateLimiter
}

func provideOperatorTournamentReadRateLimiter(
	client *goredis.Client,
	cfg *config.Config,
) operatorTournamentReadRateLimiter {
	return operatorTournamentReadRateLimiter{Inner: redisadapter.NewRateLimiter(
		client,
		"tournament-operator-read",
		cfg.Tournament.OperatorReadRateAttempts,
		cfg.Tournament.OperatorReadRateWindow,
	)}
}

type operatorTournamentMutationRateLimiter struct {
	Inner middleware.RateLimiter
}

func provideOperatorTournamentMutationRateLimiter(
	client *goredis.Client,
	cfg *config.Config,
) operatorTournamentMutationRateLimiter {
	return operatorTournamentMutationRateLimiter{Inner: redisadapter.NewRateLimiter(
		client,
		"tournament-operator-mutation",
		cfg.Tournament.OperatorMutationRateAttempts,
		cfg.Tournament.OperatorMutationRateWindow,
	)}
}

type participantTournamentReadRateLimiter struct {
	Inner middleware.RateLimiter
}

func provideParticipantTournamentReadRateLimiter(
	client *goredis.Client,
	cfg *config.Config,
) participantTournamentReadRateLimiter {
	return participantTournamentReadRateLimiter{Inner: redisadapter.NewRateLimiter(
		client,
		"tournament-participant-read",
		cfg.Tournament.ParticipantReadRateAttempts,
		cfg.Tournament.ParticipantReadRateWindow,
	)}
}

type participantTournamentMutationRateLimiter struct {
	Inner middleware.RateLimiter
}

func provideParticipantTournamentMutationRateLimiter(
	client *goredis.Client,
	cfg *config.Config,
) participantTournamentMutationRateLimiter {
	return participantTournamentMutationRateLimiter{Inner: redisadapter.NewRateLimiter(
		client,
		"tournament-participant-mutation",
		cfg.Tournament.ParticipantMutationRateAttempts,
		cfg.Tournament.ParticipantMutationRateWindow,
	)}
}

type restMiddlewareStack struct {
	RequestValidator api.MiddlewareFunc
	Outer            []api.MiddlewareFunc
}

func provideRESTMiddlewares(
	ctx context.Context,
	log logkit.Logger,
	cfg *config.Config,
	telemetry eventTelemetry,
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
				middleware.WithTournamentEventObserver(telemetry.observer),
			),
		},
	}, nil
}

func provideHTTPHandler(
	cfg *config.Config,
	rest *restv1.Server,
	ws *websocket.Server,
	auth *authusecase.UseCase,
	players middleware.PlayerSessionReader,
	middlewares restMiddlewareStack,
	metrics privateMetricsHandler,
	log logkit.Logger,
) http.Handler {
	generatedRouter := chi.NewRouter()
	generatedRouter.Handle(websocket.TournamentPublicWebSocketPath, ws)
	generatedRouter.Handle(websocket.TournamentParticipantWebSocketPath, ws)
	generatedRouter.Handle(websocket.TournamentOperatorWebSocketPath, ws)
	handler := restv1.NewHandler(rest, restv1.HandlerOptions{
		Router:           generatedRouter,
		AdminAuth:        auth,
		PlayerRepo:       players,
		RequestValidator: middlewares.RequestValidator,
		Middlewares:      middlewares.Outer,
	})

	router := chi.NewRouter()
	if metrics.Handler != nil {
		router.Method(http.MethodGet, "/internal/metrics", metrics.Handler)
	}
	router.Method(http.MethodGet, "/api/v1/admin/players/events", adminPlayerEventsHandler(rest, auth, log, cfg))
	router.Mount("/", handler)

	return middleware.CORS(cfg.HTTP.AllowedOrigins)(router)
}

func adminPlayerEventsHandler(rest *restv1.Server, auth *authusecase.UseCase, log logkit.Logger, cfg *config.Config) http.Handler {
	var handler http.Handler = http.HandlerFunc(rest.StreamPlayerEvents)
	if auth != nil {
		handler = middleware.AdminSession(auth)(handler)
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
