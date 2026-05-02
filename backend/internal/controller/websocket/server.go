package websocket

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

const defaultHubCloseDelay = 100 * time.Millisecond

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
	FinalizeOpponentForfeit(ctx context.Context, duelID, winnerID uuid.UUID)
	FinalizePlayerForfeit(ctx context.Context, duelID, loserID uuid.UUID) (*domain.Duel, error)
	CloseDuel(duelID uuid.UUID)
}

type Server struct {
	ctx           context.Context
	players       usecase.PlayerRepo
	matchmaking   Matchmaking
	flags         FlagSubmitter
	hubs          *HubRegistry
	broadcaster   *Broadcaster
	hints         *duelusecase.HintScheduler
	acceptOptions *coderws.AcceptOptions
	reconnect     ReconnectManager
	closeDelay    time.Duration

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

func WithHubCloseDelay(delay time.Duration) Option {
	return func(s *Server) {
		s.closeDelay = delay
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
		ctx:         context.Background(),
		players:     players,
		matchmaking: matchmaking,
		flags:       flags,
		hubs:        hubs,
		closeDelay:  defaultHubCloseDelay,
	}
	for _, opt := range options {
		opt(s)
	}
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
	s.hubs.CloseAll()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/ws" {
		http.NotFound(w, r)
		return
	}

	player, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	conn, err := coderws.Accept(w, r, s.acceptOptions)
	if err != nil {
		return
	}

	c := newClient(player, conn)
	var oldClient *client
	if old, loaded := s.clients.Swap(player.ID, c); loaded {
		oldClient = old.(*client)
	}

	connCtx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	//nolint:contextcheck // WebSocket connections also stop on server shutdown, not only request lifetime.
	stopOnServerShutdown := context.AfterFunc(s.ctx, cancel)
	defer cancel()
	defer stopOnServerShutdown()
	go c.writePump(connCtx)

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
			s.sendDuelResume(c, decision, false)
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
		s.sendDuelResume(c, decision, false)
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
		s.reconnect.FinalizeOpponentForfeit(ctx, decision.Duel.ID, c.player.ID)
		return true
	}

	if decision.Resume {
		s.sendDuelResume(c, decision, true)
		return true
	}

	if decision.OpponentDisconnected {
		s.sendDuelResume(c, decision, false)
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
	s.sendDuelResume(c, decision, false)
	return true
}

func (s *Server) sendDuelResume(c *client, decision *duelusecase.ReconnectDecision, notifyOpponent bool) {
	payload := DuelResumePayload{
		DuelID:   decision.Duel.ID,
		Deadline: decision.NewDeadline,
	}
	if decision.OpponentDisconnected {
		payload.OpponentDisconnected = true
		payload.OpponentReconnectDeadline = &decision.OpponentReconnectDeadline
	}
	if s.hints != nil {
		if snapshot, ok := s.hints.PlayerSnapshot(decision.Duel.ID, c.player.ID); ok && snapshot.Task != nil {
			task := taskPayload(snapshot.Task, snapshot)
			payload.Task = &task
		}
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
	raw := r.URL.Query().Get("token")
	if raw == "" {
		http.Error(w, "missing session token", http.StatusUnauthorized)
		return nil, false
	}
	token, err := uuid.Parse(raw)
	if err != nil {
		http.Error(w, "invalid session token", http.StatusUnauthorized)
		return nil, false
	}
	player, err := s.players.GetBySessionToken(r.Context(), token)
	if err != nil || player == nil {
		http.Error(w, "invalid session token", http.StatusUnauthorized)
		return nil, false
	}
	return player, true
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
		if s.reconnect != nil {
			s.reconnect.HandleDisconnect(cleanupCtx, duelID, c.player.ID)
		}
	}
	c.Close()
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
