package bootstrap

import (
	"context"
	"net/http"
	"strings"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

type rawWebSocketServer struct {
	*websocket.Server
}

const publicRealtimeMaxConnections = 128

type tournamentRealtimeOptions struct {
	participant      websocket.TournamentParticipantConnectionFlow
	participantState websocket.TournamentParticipantLifecycleFlow
	public           websocket.TournamentPublicConnectionFlow
	operator         websocket.TournamentOperatorConnectionFlow
	operatorResolver websocket.TournamentOperatorSessionResolver
	goldenConnection websocket.GoldenConnectionFlow
}

func providePublicRealtimeConfig() *tournamentws.PublicRealtimeConfig {
	return &tournamentws.PublicRealtimeConfig{MaxConnections: publicRealtimeMaxConnections}
}

func provideTournamentRealtimeOptions(
	participant websocket.TournamentParticipantConnectionFlow,
	participantState inbound.TournamentParticipantConnectionUseCase,
	public websocket.TournamentPublicConnectionFlow,
	operator websocket.TournamentOperatorConnectionFlow,
	operatorResolver websocket.TournamentOperatorSessionResolver,
	goldenConnection inbound.GoldenConnectionUseCase,
) tournamentRealtimeOptions {
	return tournamentRealtimeOptions{
		participant:      participant,
		participantState: participantState,
		public:           public,
		operator:         operator,
		operatorResolver: operatorResolver,
		goldenConnection: goldenConnection,
	}
}

func provideOperatorSessionResolver(
	auth *authusecase.UseCase,
) websocket.TournamentOperatorSessionResolver {
	return func(r *http.Request, tournamentID uuid.UUID) (websocket.TournamentOperatorSession, bool) {
		if auth == nil || r == nil || tournamentID == uuid.Nil {
			return websocket.TournamentOperatorSession{}, false
		}
		token, ok := middleware.AdminAccessTokenFromRequest(r)
		if !ok {
			return websocket.TournamentOperatorSession{}, false
		}
		claims, err := auth.VerifyAccess(r.Context(), token)
		if err != nil || claims == nil {
			return websocket.TournamentOperatorSession{}, false
		}
		subject := strings.TrimSpace(claims.Subject)
		if subject == "" {
			return websocket.TournamentOperatorSession{}, false
		}
		principalID, err := inbound.OperatorActorID(subject)
		if err != nil {
			return websocket.TournamentOperatorSession{}, false
		}
		principal := tournamentws.OperatorRealtimePrincipal{
			Authenticated: true,
			PrincipalID:   principalID,
			Role:          tournamentws.OperatorRealtimeRole,
			TournamentID:  tournamentID,
		}
		expiresAt := claims.ExpiresAt
		jti := claims.JTI
		return websocket.TournamentOperatorSession{
			Principal: principal,
			ExpiresAt: expiresAt,
			Validate: func(ctx context.Context) bool {
				current, err := auth.VerifyAccess(ctx, token)
				if err != nil || current == nil || current.JTI != jti ||
					strings.TrimSpace(current.Subject) != subject || !current.ExpiresAt.Equal(expiresAt) {
					return false
				}
				currentPrincipalID, err := inbound.OperatorActorID(current.Subject)
				return err == nil && currentPrincipalID == principalID
			},
		}, true
	}
}

// wsHandshakeRateLimiter adapts the shared bounded per-IP limiter to the
// WebSocket handshake port without exposing HTTP middleware internals.
type wsHandshakeRateLimiter struct {
	Inner middleware.RateLimiter
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
	players websocket.PlayerSessionReader,
	handshakeLimiter *wsHandshakeRateLimiter,
	realtime tournamentRealtimeOptions,
	delivery *websocket.RealtimeDelivery,
	telemetry eventTelemetry,
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
		websocket.WithClientIPResolver(clientIPResolver),
		websocket.WithConnectionLimits(cfg.WS.MaxConnections, cfg.WS.MaxConnectionsPerPrincipal),
		websocket.WithRequireOrigin(cfg.WS.RequireOrigin),
		websocket.WithLogger(log),
		websocket.WithTournamentEventObserver(telemetry.observer),
	}
	if delivery != nil {
		options = append(options, websocket.WithRealtimeDelivery(delivery))
	}
	if accept := provideWSAcceptOptions(cfg); accept != nil {
		options = append(options, websocket.WithAcceptOptions(accept))
	}
	if handshakeLimiter != nil && handshakeLimiter.Inner != nil {
		options = append(options, websocket.WithHandshakeRateLimiter(handshakeLimiter))
	}
	if realtime.participant != nil {
		options = append(options, websocket.WithTournamentParticipantFlow(realtime.participant))
	}
	if realtime.participantState != nil {
		options = append(options, websocket.WithTournamentParticipantLifecycleFlow(realtime.participantState))
	}
	if realtime.public != nil {
		options = append(options, websocket.WithTournamentPublicFlow(realtime.public))
	}
	if realtime.operator != nil {
		options = append(options, websocket.WithTournamentOperatorFlow(realtime.operator))
	}
	if realtime.operatorResolver != nil {
		options = append(options, websocket.WithTournamentOperatorSessionResolver(realtime.operatorResolver))
	}
	if realtime.goldenConnection != nil {
		options = append(options, websocket.WithGoldenConnectionFlow(realtime.goldenConnection))
	}
	return rawWebSocketServer{Server: websocket.NewServer(players, options...)}
}

func provideHandshakeRateLimiter(
	client *goredis.Client,
	cfg *config.Config,
) *wsHandshakeRateLimiter {
	if cfg == nil {
		return &wsHandshakeRateLimiter{}
	}
	return &wsHandshakeRateLimiter{Inner: redisadapter.NewRateLimiter(
		client, "websocket-handshake", cfg.WS.HandshakeRateAttempts, cfg.WS.HandshakeRateWindow,
	)}
}

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

func provideWebSocketServer(server rawWebSocketServer) *websocket.Server {
	return server.Server
}
