package websocket

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	coderws "github.com/coder/websocket"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/wirelimits"
	appobservability "github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

const (
	TournamentPublicWebSocketPath      = "/api/v1/tournaments/{tournament_id}/realtime"
	TournamentParticipantWebSocketPath = "/api/v1/tournaments/{tournament_id}/participant/realtime"
	TournamentOperatorWebSocketPath    = "/api/v1/admin/tournaments/{tournament_id}/realtime"
	defaultWriteWait                   = 10 * time.Second
	defaultPingInterval                = 20 * time.Second
	defaultSessionCheckInterval        = 20 * time.Second
	defaultSessionCheckTimeout         = 2 * time.Second
	defaultReadLimit                   = wirelimits.MaxMessageBytes
	defaultMaxConnections              = 512
	defaultMaxConnectionsPerPrincipal  = 4
	serverShutdownCloseText            = "server shutdown"
)

type HandshakeRateLimiter interface {
	Allow(ip string) bool
	RetryAfter() string
}

type ClientIPResolver func(r *http.Request) string

type Server struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	players              PlayerSessionReader
	acceptOptions        *coderws.AcceptOptions
	handshakeLimiter     HandshakeRateLimiter
	clientIP             ClientIPResolver
	participant          TournamentParticipantConnectionFlow
	public               TournamentPublicConnectionFlow
	operator             TournamentOperatorConnectionFlow
	operatorResolve      TournamentOperatorSessionResolver
	realtimeDelivery     *RealtimeDelivery
	tournamentObserver   appobservability.TournamentEventObserver
	sessionCheckInterval time.Duration
	sessionCheckTimeout  time.Duration
	maxConnections       int
	maxPerPrincipal      int
	requireOrigin        bool
	log                  logkit.Logger

	lifecycleMu sync.Mutex
	closing     bool
	connections map[*coderws.Conn]struct{}
	active      int
	byPrincipal map[string]int
	wg          sync.WaitGroup
}

type Option func(*Server)

func WithContext(ctx context.Context) Option {
	return func(server *Server) {
		if ctx != nil {
			server.ctx = ctx
		}
	}
}

func WithAcceptOptions(options *coderws.AcceptOptions) Option {
	return func(server *Server) {
		server.acceptOptions = options
	}
}

func WithHandshakeRateLimiter(limiter HandshakeRateLimiter) Option {
	return func(server *Server) {
		server.handshakeLimiter = limiter
	}
}

func WithClientIPResolver(resolver ClientIPResolver) Option {
	return func(server *Server) {
		if resolver != nil {
			server.clientIP = resolver
		}
	}
}

func WithSessionMonitor(interval, timeout time.Duration) Option {
	return func(server *Server) {
		server.sessionCheckInterval = interval
		server.sessionCheckTimeout = timeout
	}
}

func WithConnectionLimits(maxConnections, maxPerPrincipal int) Option {
	return func(server *Server) {
		if maxConnections > 0 {
			server.maxConnections = maxConnections
		}
		if maxPerPrincipal > 0 {
			server.maxPerPrincipal = maxPerPrincipal
		}
	}
}

func WithTournamentParticipantFlow(flow TournamentParticipantConnectionFlow) Option {
	return func(server *Server) {
		server.participant = flow
	}
}

func WithTournamentPublicFlow(flow TournamentPublicConnectionFlow) Option {
	return func(server *Server) {
		server.public = flow
	}
}

func WithTournamentOperatorFlow(flow TournamentOperatorConnectionFlow) Option {
	return func(server *Server) {
		server.operator = flow
	}
}

func WithTournamentOperatorSessionResolver(resolver TournamentOperatorSessionResolver) Option {
	return func(server *Server) {
		server.operatorResolve = resolver
	}
}

func WithRealtimeDelivery(delivery *RealtimeDelivery) Option {
	return func(server *Server) {
		server.realtimeDelivery = delivery
	}
}

func WithTournamentEventObserver(observer appobservability.TournamentEventObserver) Option {
	return func(server *Server) {
		server.tournamentObserver = observer
	}
}

func WithRequireOrigin(require bool) Option {
	return func(server *Server) {
		server.requireOrigin = require
	}
}

func WithLogger(log logkit.Logger) Option {
	return func(server *Server) {
		server.log = log
	}
}

func NewServer(players PlayerSessionReader, options ...Option) *Server {
	server := &Server{
		ctx:                  context.Background(),
		players:              players,
		sessionCheckInterval: defaultSessionCheckInterval,
		sessionCheckTimeout:  defaultSessionCheckTimeout,
		maxConnections:       defaultMaxConnections,
		maxPerPrincipal:      defaultMaxConnectionsPerPrincipal,
		connections:          make(map[*coderws.Conn]struct{}),
		byPrincipal:          make(map[string]int),
	}
	for _, option := range options {
		option(server)
	}
	if server.tournamentObserver == nil {
		server.tournamentObserver = appobservability.NewTournamentStructuredLogger(server.log)
	}
	server.ctx, server.cancel = context.WithCancel(server.ctx)
	return server
}

func (server *Server) Shutdown(ctx context.Context) {
	if server == nil || ctx == nil {
		return
	}

	server.lifecycleMu.Lock()
	if !server.closing {
		server.closing = true
		if server.cancel != nil {
			server.cancel()
		}
	}
	connections := make([]*coderws.Conn, 0, len(server.connections))
	for connection := range server.connections {
		connections = append(connections, connection)
	}
	server.lifecycleMu.Unlock()

	for _, connection := range connections {
		_ = connection.CloseNow()
	}

	done := make(chan struct{})
	go func() {
		server.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		for _, connection := range connections {
			_ = connection.CloseNow()
		}
		return
	}
}

func (server *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	scope, principal, resume, ok := server.prepareTournamentConnection(w, r)
	if !ok {
		return
	}
	release, limit := server.reserveConnection(scope, principal)
	if limit != connectionAvailable {
		server.writeConnectionLimitProblem(w, r, limit)
		return
	}
	defer release()
	connection, err := coderws.Accept(w, r, server.acceptOptions)
	if err != nil {
		server.logRequestSecurityEvent(r, "ws.handshake", wsSecurityOutcomeFailure, logkit.Fields{
			"error_code": "accept_failed",
			"reason":     "upgrade_failed",
		})
		return
	}
	connection.SetReadLimit(defaultReadLimit)
	if !server.registerConnection(connection) {
		_ = connection.Close(coderws.StatusGoingAway, serverShutdownCloseText)
		return
	}
	defer server.unregisterConnection(connection)
	defer func() { _ = connection.CloseNow() }()

	connectionCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	server.serveTournamentConnection(connectionCtx, connection, scope, principal, resume)
}

func (server *Server) registerConnection(connection *coderws.Conn) bool {
	server.lifecycleMu.Lock()
	defer server.lifecycleMu.Unlock()
	if server.closing {
		return false
	}
	server.connections[connection] = struct{}{}
	server.wg.Add(1)
	return true
}

func (server *Server) unregisterConnection(connection *coderws.Conn) {
	server.lifecycleMu.Lock()
	delete(server.connections, connection)
	server.lifecycleMu.Unlock()
	server.wg.Done()
}

func (server *Server) isClosing() bool {
	server.lifecycleMu.Lock()
	defer server.lifecycleMu.Unlock()
	return server.closing
}

func (server *Server) acceptsOrigin(r *http.Request) bool {
	if server.acceptOptions != nil && server.acceptOptions.InsecureSkipVerify {
		return true
	}
	var patterns []string
	if server.acceptOptions != nil {
		patterns = server.acceptOptions.OriginPatterns
	}
	return websocketOriginAllowed(r, patterns, server.requireOrigin)
}

func websocketOriginAllowed(r *http.Request, originPatterns []string, requireOrigin bool) bool {
	if r == nil {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return !requireOrigin
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if strings.EqualFold(r.Host, parsed.Host) {
		return true
	}
	for _, pattern := range originPatterns {
		target := parsed.Host
		if strings.Contains(pattern, "://") {
			target = parsed.Scheme + "://" + parsed.Host
		}
		if strings.EqualFold(strings.TrimSpace(pattern), target) {
			return true
		}
	}
	return false
}

func (server *Server) resolveClientIP(r *http.Request) string {
	if server.clientIP != nil {
		if ip := server.clientIP(r); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
