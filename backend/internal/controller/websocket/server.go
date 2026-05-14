package websocket

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	restmw "github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

const (
	defaultHubCloseDelay   = 100 * time.Millisecond
	defaultDisconnectGrace = time.Second
)

type Matchmaking interface {
	JoinQueue(ctx context.Context, playerID uuid.UUID) (*duelusecase.MatchResult, error)
	LeaveQueue(ctx context.Context, playerID uuid.UUID) error
}

type FlagSubmitter interface {
	SubmitFlag(ctx context.Context, duelID, playerID uuid.UUID, flag string) (duelusecase.Result, error)
}

type ReconnectManager interface {
	StartDuelTimer(duel *domain.Duel)
	HandleDisconnect(ctx context.Context, duelID, playerID uuid.UUID)
	ConsumeReconnect(ctx context.Context, playerID uuid.UUID) (*duelusecase.ReconnectDecision, error)
	ActiveDuel(ctx context.Context, playerID uuid.UUID) (*duelusecase.ReconnectDecision, error)
	DuelPaused(duelID uuid.UUID) bool
	FinalizeDraw(ctx context.Context, duelID uuid.UUID) (*domain.Duel, error)
	FinalizePlayerForfeit(ctx context.Context, duelID, loserID uuid.UUID) (*domain.Duel, error)
	CloseDuel(duelID uuid.UUID)
	StopAll()
}

type HandshakeRateLimiter interface {
	Allow(ip string) bool
	RetryAfter() string
}

type ClientIPResolver func(r *http.Request) string

type TimerStopper interface {
	StopAll()
}

type Server struct {
	ctx              context.Context
	cancel           context.CancelFunc
	players          usecase.PlayerRepo
	matchmaking      Matchmaking
	flags            FlagSubmitter
	hubs             *HubRegistry
	broadcaster      *Broadcaster
	hints            *duelusecase.HintScheduler
	duels            usecase.DuelRepo
	storage          usecase.SourceFileStorage
	timers           TimerStopper
	acceptOptions    *coderws.AcceptOptions
	reconnect        ReconnectManager
	closeDelay       time.Duration
	disconnectGrace  time.Duration
	handshakeLimiter HandshakeRateLimiter
	clientIP         ClientIPResolver
	requireOrigin    bool
	log              logkit.Logger

	wg      sync.WaitGroup
	clients sync.Map
}

type Option func(*Server)

func WithContext(ctx context.Context) Option {
	return func(s *Server) {
		if ctx != nil {
			s.ctx = ctx
		}
	}
}

func WithAcceptOptions(opts *coderws.AcceptOptions) Option {
	return func(s *Server) {
		s.acceptOptions = opts
	}
}

func WithReconnectManager(manager ReconnectManager) Option {
	return func(s *Server) {
		s.reconnect = manager
	}
}

func WithHintScheduler(hints *duelusecase.HintScheduler) Option {
	return func(s *Server) {
		s.hints = hints
	}
}

func WithTaskResolver(duels usecase.DuelRepo, storage usecase.SourceFileStorage) Option {
	return func(s *Server) {
		s.duels = duels
		s.storage = storage
	}
}

func WithTimerStopper(timers TimerStopper) Option {
	return func(s *Server) {
		s.timers = timers
	}
}

func WithHubCloseDelay(delay time.Duration) Option {
	return func(s *Server) {
		s.closeDelay = delay
	}
}

func WithDisconnectGrace(delay time.Duration) Option {
	return func(s *Server) {
		s.disconnectGrace = delay
	}
}

func WithHandshakeRateLimiter(rl HandshakeRateLimiter) Option {
	return func(s *Server) {
		s.handshakeLimiter = rl
	}
}

func WithClientIPResolver(resolver ClientIPResolver) Option {
	return func(s *Server) {
		if resolver != nil {
			s.clientIP = resolver
		}
	}
}

func WithRequireOrigin(require bool) Option {
	return func(s *Server) {
		s.requireOrigin = require
	}
}

func WithLogger(log logkit.Logger) Option {
	return func(s *Server) {
		s.log = log
	}
}

func NewServer(
	players usecase.PlayerRepo,
	matchmaking Matchmaking,
	flags FlagSubmitter,
	hubs *HubRegistry,
	options ...Option,
) *Server {
	if hubs == nil {
		hubs = NewHubRegistry()
	}
	s := &Server{
		ctx:             context.Background(),
		players:         players,
		matchmaking:     matchmaking,
		flags:           flags,
		hubs:            hubs,
		closeDelay:      defaultHubCloseDelay,
		disconnectGrace: defaultDisconnectGrace,
	}
	for _, opt := range options {
		opt(s)
	}

	s.ctx, s.cancel = context.WithCancel(s.ctx) //nolint:gosec,nolintlint // G118 in older gosec: cancel is stored on Server and invoked by Shutdown.
	s.broadcaster = newBroadcaster(s.ctx, s.hubs, s.clientByPlayer, s.closeDelay)
	if s.hints != nil {
		s.hints.SetSender(s.sendHintUnlocked)
	}
	return s
}

func (s *Server) Broadcaster() *Broadcaster {
	return s.broadcaster
}

func (s *Server) SetReconnectManager(manager ReconnectManager) {
	s.reconnect = manager
}

func (s *Server) Shutdown(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		//nolint:contextcheck // Shutdown is a lifecycle boundary; nil input falls back to a root context.
		ctx = context.Background()
	}
	s.clients.Range(func(_, raw any) bool {
		c := raw.(*client)
		_ = c.sendError(ErrorServerShutdown, "server is shutting down")
		return true
	})

	drain := time.NewTimer(50 * time.Millisecond)
	select {
	case <-ctx.Done():
		if !drain.Stop() {
			<-drain.C
		}
	case <-drain.C:
	}

	s.stopBackgroundTimers()

	s.clients.Range(func(key, raw any) bool {
		c := raw.(*client)
		if c.isQueued() && s.matchmaking != nil {
			_ = s.matchmaking.LeaveQueue(ctx, c.player.ID)
			c.setQueued(false)
		}
		if duelID, ok := c.currentDuel(); ok {
			s.hubs.Unregister(duelID, c)
			if s.hints != nil {
				s.hints.StopDuel(duelID)
			}
			if s.reconnect != nil {
				s.reconnect.CloseDuel(duelID)
			}
		}
		s.clients.Delete(key)
		c.Close()
		return true
	})

	if s.cancel != nil {
		s.cancel()
	}

	waitDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-ctx.Done():
	}

	s.hubs.CloseAll()
}

func (s *Server) stopBackgroundTimers() {
	if s.reconnect != nil {
		s.reconnect.StopAll()
	}
	if s.timers != nil {
		s.timers.StopAll()
	}
	if s.hints != nil {
		s.hints.StopAll()
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeHandshakeProblem(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.Path != "/ws" {
		writeHandshakeProblem(w, r, http.StatusNotFound, "websocket endpoint not found")
		return
	}

	if s.handshakeLimiter != nil {
		ip := s.resolveClientIP(r)
		if !s.handshakeLimiter.Allow(ip) {
			if retry := s.handshakeLimiter.RetryAfter(); retry != "" {
				w.Header().Set("Retry-After", retry)
			}
			s.logRequestSecurityEvent(r, "ws.handshake", wsSecurityOutcomeRateLimited, logkit.Fields{
				"error_code": "rate_limited",
				"reason":     "handshake_rate_limit",
			})
			writeHandshakeProblem(w, r, http.StatusTooManyRequests, "too many handshake attempts")
			return
		}
	}

	if !s.acceptsOrigin(r) {
		s.logRequestSecurityEvent(r, "ws.handshake", wsSecurityOutcomeFailure, logkit.Fields{
			"error_code": "origin_not_allowed",
			"reason":     "origin_not_allowed",
		})
		writeHandshakeProblem(w, r, http.StatusForbidden, "origin not allowed")
		return
	}

	player, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	acceptOpts := s.acceptOptions
	conn, err := coderws.Accept(w, r, acceptOpts)
	if err != nil {
		s.logRequestSecurityEvent(r, "ws.handshake", wsSecurityOutcomeFailure, logkit.Fields{
			"error_code": "accept_failed",
			"reason":     "upgrade_failed",
		})
		return
	}
	s.logRequestSecurityEvent(r, "ws.auth", wsSecurityOutcomeSuccess, logkit.Fields{
		"player_id": player.ID.String(),
	})

	c := newClient(player, conn)
	var oldClient *client
	if old, loaded := s.clients.Swap(player.ID, c); loaded {
		oldClient = old.(*client)
		oldClient.markDisplaced()
	}

	connCtx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	//nolint:contextcheck // WebSocket connections also stop on server shutdown, not only request lifetime.
	stopOnServerShutdown := context.AfterFunc(s.ctx, cancel)
	defer cancel()
	defer stopOnServerShutdown()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		c.writePump(connCtx)
	}()

	handled := false
	if oldClient != nil {
		handled = s.handleConnectionReplacement(connCtx, c, oldClient)
	}
	if !handled {
		handled = s.handleReconnect(connCtx, c)
	}
	if !handled {
		s.handleActiveDuelRestore(connCtx, c)
	}
	s.readPump(connCtx, c)
	s.cleanupClient(connCtx, c)
}

func (s *Server) handleConnectionReplacement(ctx context.Context, c, old *client) bool {
	queued, duelID, inDuel := old.stateSnapshot()
	if s.reconnect != nil {
		decision, err := s.reconnect.ActiveDuel(ctx, c.player.ID)
		if err != nil {
			go old.Close()
			s.sendAppError(c, err)
			return true
		}
		if decision != nil {
			if inDuel {
				s.hubs.Unregister(duelID, old)
			}
			go old.Close()
			if !s.attachToDuelHub(ctx, c, decision.Duel.ID) {
				return true
			}
			s.sendDuelResume(ctx, c, decision, false)
			return true
		}
	}

	if inDuel {
		s.hubs.Unregister(duelID, old)
		go old.Close()
		if s.reconnect == nil {
			return s.attachToDuelHub(ctx, c, duelID)
		}
		decision, err := s.reconnect.ActiveDuel(ctx, c.player.ID)
		if err != nil {
			s.sendAppError(c, err)
			return true
		}
		if decision == nil {
			return true
		}
		if !s.attachToDuelHub(ctx, c, decision.Duel.ID) {
			return true
		}
		s.sendDuelResume(ctx, c, decision, false)
		return true
	}

	if queued {
		c.setQueued(true)
		go old.Close()
		return false
	}

	go old.Close()
	return false
}

func (s *Server) handleReconnect(ctx context.Context, c *client) bool {
	if s.reconnect == nil {
		return false
	}

	decision, err := s.reconnect.ConsumeReconnect(ctx, c.player.ID)
	if err != nil {
		s.sendAppError(c, err)
		return true
	}
	if decision == nil {
		return false
	}
	if decision.WindowExpired {
		_ = c.sendError(ErrorInvalidPayload, "reconnect window expired")
		return true
	}

	if !s.attachToDuelHub(ctx, c, decision.Duel.ID) {
		return true
	}

	if decision.OpponentExpired {
		if _, err := s.reconnect.FinalizeDraw(ctx, decision.Duel.ID); err != nil {
			s.sendAppError(c, err)
		}
		return true
	}

	if decision.Resume {
		s.sendDuelResume(ctx, c, decision, true)
		return true
	}

	if decision.OpponentDisconnected {
		s.sendDuelResume(ctx, c, decision, false)
		deadline := decision.OpponentReconnectDeadline
		_ = c.sendEvent(EventOpponentDisconnected, OpponentDisconnectedPayload{
			DuelID:            decision.Duel.ID,
			PlayerID:          decision.OpponentID,
			ReconnectDeadline: deadline,
		})
	}
	return true
}

func (s *Server) handleActiveDuelRestore(ctx context.Context, c *client) bool {
	if s.reconnect == nil {
		return false
	}
	decision, err := s.reconnect.ActiveDuel(ctx, c.player.ID)
	if err != nil {
		s.sendAppError(c, err)
		return true
	}
	if decision == nil {
		return false
	}
	if !s.attachToDuelHub(ctx, c, decision.Duel.ID) {
		return true
	}
	s.sendDuelResume(ctx, c, decision, false)
	return true
}

func (s *Server) sendDuelResume(ctx context.Context, c *client, decision *duelusecase.ReconnectDecision, notifyOpponent bool) {
	payload := DuelResumePayload{
		DuelID:     decision.Duel.ID,
		OpponentID: decision.OpponentID,
		Deadline:   decision.NewDeadline,
	}
	if decision.OpponentDisconnected {
		payload.OpponentDisconnected = true
		payload.OpponentReconnectDeadline = &decision.OpponentReconnectDeadline
	}
	task, err := s.taskPayloadForPlayer(ctx, decision.Duel, c.player.ID)
	if err != nil {
		s.sendAppError(c, err)
		return
	}
	if task != nil {
		payload.Task = task
	}
	_ = c.sendEvent(EventDuelResume, payload)
	if notifyOpponent {
		if opponent, ok := s.clientByPlayer(decision.OpponentID); ok {
			_ = opponent.sendEvent(EventOpponentReconnected, OpponentReconnectedPayload{
				DuelID:   decision.Duel.ID,
				PlayerID: c.player.ID,
				Deadline: decision.NewDeadline,
			})
		}
	}
}

func (s *Server) taskPayloadForPlayer(ctx context.Context, duel *domain.Duel, playerID uuid.UUID) (*TaskPayload, error) {
	if duel == nil {
		return nil, nil
	}

	var snapshot duelusecase.HintSnapshot
	var ok bool
	if s.hints != nil {
		snapshot, ok = s.hints.PlayerSnapshot(duel.ID, playerID)
	}

	task := snapshot.Task
	if s.duels != nil {
		persisted, err := s.duels.GetPlayerTask(ctx, duel.ID, playerID)
		if err != nil {
			return nil, fmt.Errorf("DuelRepo.GetPlayerTask: %w", err)
		}
		task = persisted
	}
	if task == nil {
		return nil, nil
	}

	task, err := s.prepareOutboundTask(ctx, task)
	if err != nil {
		return nil, err
	}
	if !ok || len(snapshot.Schedule) == 0 {
		snapshot.Schedule = domain.BuildHintSchedule(duel.StartedAt, task.TimeLimit)
	}
	snapshot.Task = task

	payload := taskPayload(task, snapshot)
	return &payload, nil
}

func (s *Server) prepareOutboundTask(ctx context.Context, task *domain.Task) (*domain.Task, error) {
	if task == nil || task.SourceFileURL == nil {
		return task, nil
	}
	if s.storage == nil {
		return nil, errors.New("source file storage is not configured")
	}
	url, err := s.storage.PresignedGetURL(
		ctx,
		domain.TaskSourceFileKeyFromURL(task.ID, *task.SourceFileURL),
		time.Duration(task.TimeLimit)*time.Second,
	)
	if err != nil {
		return nil, fmt.Errorf("SourceFileStorage.PresignedGetURL: %w", err)
	}
	clone := *task
	clone.SourceFileURL = &url
	return &clone, nil
}

func (s *Server) attachToDuelHub(ctx context.Context, c *client, duelID uuid.UUID) bool {
	c.setDuel(duelID)
	if _, ok := s.hubs.Get(duelID); !ok {
		//nolint:contextcheck // Duel hubs are bound to server lifecycle, not to a single request.
		s.hubs.Create(s.ctx, duelID)
	}
	if err := s.hubs.Register(ctx, duelID, c); err != nil {
		c.clearDuel()
		s.sendAppError(c, err)
		return false
	}
	return true
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (*domain.Player, bool) {
	token, ok := restmw.PlayerSessionTokenFromRequest(r)
	if !ok {
		s.logRequestSecurityEvent(r, "ws.auth", wsSecurityOutcomeFailure, wsAuthFailureFields(r))
		writeHandshakeProblem(w, r, http.StatusUnauthorized, "missing session token")
		return nil, false
	}
	player, err := s.players.GetBySessionToken(r.Context(), token)
	if err != nil || player == nil {
		s.logRequestSecurityEvent(r, "ws.auth", wsSecurityOutcomeFailure, logkit.Fields{
			"error_code": string(apperr.CodeInvalidSession),
			"reason":     "invalid_session",
		})
		writeHandshakeProblem(w, r, http.StatusUnauthorized, "invalid session token")
		return nil, false
	}
	return player, true
}

func (s *Server) acceptsOrigin(r *http.Request) bool {
	if s.acceptOptions != nil && s.acceptOptions.InsecureSkipVerify {
		return true
	}
	var patterns []string
	if s.acceptOptions != nil {
		patterns = s.acceptOptions.OriginPatterns
	}
	return websocketOriginAllowed(r, patterns, s.requireOrigin)
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
		matched, err := path.Match(strings.ToLower(pattern), strings.ToLower(target))
		if err == nil && matched {
			return true
		}
	}
	return false
}

func (s *Server) resolveClientIP(r *http.Request) string {
	if s.clientIP != nil {
		if ip := s.clientIP(r); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) cleanupClient(ctx context.Context, c *client) {
	if !s.clients.CompareAndDelete(c.player.ID, c) {
		c.Close()
		return
	}
	cleanupCtx := context.WithoutCancel(ctx)
	if c.isQueued() && s.matchmaking != nil {
		_ = s.matchmaking.LeaveQueue(cleanupCtx, c.player.ID)
	}
	if duelID, ok := c.currentDuel(); ok {
		s.hubs.Unregister(duelID, c)
		s.handleDisconnectAfterGrace(cleanupCtx, c, duelID)
	}
	c.Close()
}

func (s *Server) handleDisconnectAfterGrace(ctx context.Context, c *client, duelID uuid.UUID) {
	if s.reconnect == nil {
		return
	}
	if s.disconnectGrace <= 0 {
		s.reconnect.HandleDisconnect(ctx, duelID, c.player.ID)
		return
	}

	timer := time.NewTimer(s.disconnectGrace)
	defer timer.Stop()

	serverCtx := s.ctx
	if serverCtx == nil {
		serverCtx = context.Background()
	}
	select {
	case <-timer.C:
	case <-serverCtx.Done():
		return
	}

	if replacement, ok := s.clientByPlayer(c.player.ID); ok && replacement != c {
		if replacementDuelID, inDuel := replacement.currentDuel(); inDuel && replacementDuelID == duelID {
			return
		}
	}
	s.reconnect.HandleDisconnect(ctx, duelID, c.player.ID)
}

func (s *Server) clientByPlayer(playerID uuid.UUID) (*client, bool) {
	raw, ok := s.clients.Load(playerID)
	if !ok {
		return nil, false
	}
	return raw.(*client), true
}

func (s *Server) sendHintUnlocked(playerID uuid.UUID, event duelusecase.HintUnlocked) {
	if c, ok := s.clientByPlayer(playerID); ok {
		_ = c.sendEvent(EventHintUnlocked, hintUnlockedPayload(event))
	}
}

func (s *Server) sendAppError(c *client, err error) {
	var appErr *apperr.Error
	if errors.As(err, &appErr) {
		_ = c.sendError(string(appErr.Code), appErr.Message)
		return
	}
	_ = c.sendError(ErrorInternal, "internal error")
}
