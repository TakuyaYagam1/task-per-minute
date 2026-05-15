package duel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/ctxutil"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

const (
	DefaultReconnectWindow          = 2 * time.Minute
	DefaultReconnectDisconnectLimit = 2
	reconnectAsyncTimeout           = 10 * time.Second
)

type DuelTimer interface {
	Start(duelID uuid.UUID, deadline time.Time, onExpire func())
	Stop(duelID uuid.UUID) bool
	Freeze(duelID uuid.UUID, pausedAt time.Time) bool
	Resume(duelID uuid.UUID, resumedAt time.Time) (time.Time, bool)
}

type ReconnectManager struct {
	ctx         context.Context
	tx          usecase.TxManager
	duels       usecase.DuelRepo
	players     usecase.PlayerRepo
	timers      DuelTimer
	broadcaster usecase.DuelBroadcaster
	clock       clock.Clock
	board       usecase.LeaderboardBumper
	log         logkit.Logger

	window          time.Duration
	disconnectLimit int

	states sync.Map
}

type ReconnectOption func(*ReconnectManager)

func WithReconnectWindow(window time.Duration) ReconnectOption {
	return func(m *ReconnectManager) {
		if window > 0 {
			m.window = window
		}
	}
}

func WithReconnectDisconnectLimit(limit int) ReconnectOption {
	return func(m *ReconnectManager) {
		if limit >= 0 {
			m.disconnectLimit = limit
		}
	}
}

func WithLeaderboardStore(board usecase.LeaderboardBumper) ReconnectOption {
	return func(m *ReconnectManager) {
		m.board = board
	}
}

// WithReconnectContext binds the manager to the server lifecycle so that
// async finalize calls (triggered by reconnect-window expiry or duel-deadline
// expiry) carry a context detached from any request, but still observable for
// shutdown coordination.
func WithReconnectContext(ctx context.Context) ReconnectOption {
	return func(m *ReconnectManager) {
		if ctx != nil {
			m.ctx = ctx
		}
	}
}

// WithReconnectLogger attaches a structured logger so leaderboard-bump
// failures inside async finalize are surfaced rather than silently dropped.
func WithReconnectLogger(log logkit.Logger) ReconnectOption {
	return func(m *ReconnectManager) {
		m.log = log
	}
}

func NewReconnectManager(
	tx usecase.TxManager,
	duels usecase.DuelRepo,
	players usecase.PlayerRepo,
	timers DuelTimer,
	broadcaster usecase.DuelBroadcaster,
	clk clock.Clock,
	options ...ReconnectOption,
) *ReconnectManager {
	if clk == nil {
		clk = clock.Real{}
	}
	m := &ReconnectManager{
		ctx:             context.Background(),
		tx:              tx,
		duels:           duels,
		players:         players,
		timers:          timers,
		broadcaster:     broadcaster,
		clock:           clk,
		window:          DefaultReconnectWindow,
		disconnectLimit: DefaultReconnectDisconnectLimit,
	}
	for _, opt := range options {
		opt(m)
	}
	return m
}

// StopAll stops every reconnect-window timer across all tracked duels. Use
// during graceful shutdown or test cleanup to drain leaked time.AfterFunc
// goroutines that would otherwise outlive the request context.
func (m *ReconnectManager) StopAll() {
	if m == nil {
		return
	}
	m.states.Range(func(_, value any) bool {
		state := value.(*reconnectDuelState)
		state.mu.Lock()
		state.stopReconnectTimersLocked()
		state.closed = true
		state.mu.Unlock()
		return true
	})
}

func (m *ReconnectManager) StartDuelTimer(duel *domain.Duel) {
	if m == nil || m.timers == nil || duel == nil {
		return
	}
	m.timers.Start(duel.ID, duel.Deadline, func() {
		m.handleDuelTimerExpired(duel.ID)
	})
}

func (m *ReconnectManager) BeginDisconnect(ctx context.Context, duelID, playerID uuid.UUID) {
	m.handleDisconnect(ctx, duelID, playerID, false)
}

func (m *ReconnectManager) HandleDisconnect(ctx context.Context, duelID, playerID uuid.UUID) {
	m.handleDisconnect(ctx, duelID, playerID, true)
}

func (m *ReconnectManager) handleDisconnect(ctx context.Context, duelID, playerID uuid.UUID, notify bool) {
	duel, ok := m.activeDuelForDisconnect(ctx, duelID, playerID)
	if !ok {
		return
	}

	outcome := m.recordDisconnect(duel, playerID, m.clock.Now(), notify) //nolint:contextcheck // Disconnect timers intentionally outlive request cleanup.
	if outcome.finalizeDraw {
		m.finalize(ctx, duel.ID, nil, "disconnect_limit_exceeded")
		return
	}
	if outcome.broadcast && m.broadcaster != nil {
		m.broadcaster.BroadcastOpponentDisconnected(ctx, duel.ID, playerID, outcome.deadline)
	}
}

func (m *ReconnectManager) activeDuelForDisconnect(
	ctx context.Context,
	duelID uuid.UUID,
	playerID uuid.UUID,
) (*domain.Duel, bool) {
	if m == nil || m.duels == nil {
		return nil, false
	}
	duel, err := m.duels.GetByID(ctx, duelID)
	if err != nil || duel == nil || duel.Status != domain.DuelStatusActive {
		return nil, false
	}
	_, ok := opponentID(duel, playerID)
	if !ok {
		return nil, false
	}
	return duel, true
}

type disconnectOutcome struct {
	deadline     time.Time
	broadcast    bool
	finalizeDraw bool
}

func (m *ReconnectManager) recordDisconnect(
	duel *domain.Duel,
	playerID uuid.UUID,
	now time.Time,
	notify bool,
) disconnectOutcome {
	state := m.stateFor(duel)
	var outcome disconnectOutcome

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.closed {
		return outcome
	}
	if entry, exists := state.disconnected[playerID]; exists {
		return updateExistingDisconnect(entry, notify)
	}

	state.counts[playerID]++
	if state.counts[playerID] > m.disconnectLimit {
		state.closed = true
		state.stopReconnectTimersLocked()
		outcome.finalizeDraw = true
		return outcome
	}

	if len(state.disconnected) == 0 && m.timers != nil {
		m.timers.Freeze(duel.ID, now)
	}
	outcome.deadline = now.Add(m.window)
	outcome.broadcast = notify

	state.disconnected[playerID] = m.newReconnectEntry(duel.ID, playerID, now, outcome.deadline, notify)
	return outcome
}

func updateExistingDisconnect(entry *reconnectPlayerState, notify bool) disconnectOutcome {
	var outcome disconnectOutcome
	if notify && !entry.notified && !entry.expired {
		entry.notified = true
		outcome.deadline = entry.deadline
		outcome.broadcast = true
	}
	return outcome
}

func (m *ReconnectManager) newReconnectEntry(
	duelID uuid.UUID,
	playerID uuid.UUID,
	now time.Time,
	deadline time.Time,
	notify bool,
) *reconnectPlayerState {
	entry := &reconnectPlayerState{
		deadline:       deadline,
		disconnectedAt: now,
		notified:       notify,
	}

	entry.timer = time.AfterFunc(m.window, func() {
		m.expireReconnect(duelID, playerID)
	})
	return entry
}

type ReconnectDecision struct {
	Duel                      *domain.Duel
	OpponentID                uuid.UUID
	NewDeadline               time.Time
	Resume                    bool
	OpponentExpired           bool
	WindowExpired             bool
	OpponentDisconnected      bool
	OpponentReconnectDeadline time.Time
}

func (m *ReconnectManager) ConsumeReconnect(ctx context.Context, playerID uuid.UUID) (*ReconnectDecision, error) {
	if m == nil || m.duels == nil {
		return nil, nil
	}
	duel, err := m.duels.GetActiveByPlayerID(ctx, playerID)
	if err != nil || duel == nil {
		return nil, err
	}
	state, ok := m.loadState(duel.ID)
	if !ok {
		return nil, nil
	}
	otherID, ok := opponentID(duel, playerID)
	if !ok {
		return nil, nil
	}

	consume, ok := state.consumeReconnect(playerID)
	if !ok {
		return nil, nil
	}
	decision := &ReconnectDecision{
		Duel:                      duel,
		OpponentID:                otherID,
		NewDeadline:               duel.Deadline,
		Resume:                    consume.resume,
		OpponentExpired:           consume.expiredOpponent,
		WindowExpired:             consume.windowExpired,
		OpponentDisconnected:      consume.opponentDisconnected,
		OpponentReconnectDeadline: consume.opponentDeadline,
	}
	if !consume.resume {
		return decision, nil
	}

	now := m.clock.Now()
	newDeadline := duel.Deadline
	timerResumed := false
	if m.timers != nil {
		if resumed, ok := m.timers.Resume(duel.ID, now); ok {
			newDeadline = resumed
			timerResumed = true
		}
	}
	updated, err := m.duels.UpdateDeadline(ctx, duel.ID, newDeadline)
	if err != nil {
		if timerResumed && m.timers != nil {
			m.timers.Freeze(duel.ID, now)
		}
		//nolint:contextcheck // Reconnect rollback restarts a window timer that must outlive this request.
		m.rollbackPendingReconnect(state, duel.ID, playerID, now)
		return nil, fmt.Errorf("update reconnect deadline: %w", err)
	}
	if updated != nil {
		newDeadline = updated.Deadline
	}
	state.commitReconnect(playerID)
	decision.NewDeadline = newDeadline
	return decision, nil
}

func (m *ReconnectManager) rollbackPendingReconnect(
	state *reconnectDuelState,
	duelID uuid.UUID,
	playerID uuid.UUID,
	now time.Time,
) {
	if m == nil || state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return
	}
	entry, ok := state.disconnected[playerID]
	if !ok || !entry.reconnecting || entry.expired {
		return
	}
	entry.reconnecting = false
	delay := entry.deadline.Sub(now)
	if delay < 0 {
		delay = 0
	}
	entry.timer = time.AfterFunc(delay, func() {
		m.expireReconnect(duelID, playerID)
	})
}

func (m *ReconnectManager) ActiveDuel(ctx context.Context, playerID uuid.UUID) (*ReconnectDecision, error) {
	if m == nil || m.duels == nil {
		return nil, nil
	}
	duel, err := m.duels.GetActiveByPlayerID(ctx, playerID)
	if err != nil || duel == nil {
		return nil, err
	}
	otherID, ok := opponentID(duel, playerID)
	if !ok {
		return nil, nil
	}
	decision := &ReconnectDecision{
		Duel:        duel,
		OpponentID:  otherID,
		NewDeadline: duel.Deadline,
		Resume:      true,
	}
	if state, ok := m.loadState(duel.ID); ok {
		decision.OpponentDisconnected, decision.OpponentReconnectDeadline = state.disconnectedDeadline(otherID)
	}
	return decision, nil
}

func (m *ReconnectManager) DuelPaused(duelID uuid.UUID) bool {
	if m == nil {
		return false
	}
	state, ok := m.loadState(duelID)
	if !ok {
		return false
	}
	return state.paused()
}

func (m *ReconnectManager) FinalizeDraw(ctx context.Context, duelID uuid.UUID) (*domain.Duel, error) {
	return m.finalizeAndBroadcast(ctx, duelID, nil)
}

func (m *ReconnectManager) FinalizePlayerForfeit(
	ctx context.Context,
	duelID uuid.UUID,
	loserID uuid.UUID,
) (*domain.Duel, error) {
	if m == nil || m.duels == nil {
		return nil, nil
	}
	finalizeCtx, finalizeCancel := boundedDetached(ctx)
	defer finalizeCancel()
	duel, err := m.duels.GetByID(finalizeCtx, duelID)
	if err != nil {
		return nil, err
	}
	if duel == nil || duel.Status != domain.DuelStatusActive {
		return nil, nil
	}
	winnerID, ok := opponentID(duel, loserID)
	if !ok {
		return nil, apperr.ErrNotDuelParticipant
	}
	winner := winnerID
	return m.finalizeAndBroadcast(finalizeCtx, duelID, &winner)
}

func (m *ReconnectManager) CloseDuel(duelID uuid.UUID) {
	if m == nil {
		return
	}
	if m.timers != nil {
		m.timers.Stop(duelID)
	}
	raw, ok := m.states.LoadAndDelete(duelID)
	if !ok {
		return
	}
	state := raw.(*reconnectDuelState)
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closed = true
	state.stopReconnectTimersLocked()
}

func (m *ReconnectManager) stateFor(duel *domain.Duel) *reconnectDuelState {
	state := &reconnectDuelState{
		duelID:       duel.ID,
		player1ID:    duel.Player1ID,
		player2ID:    duel.Player2ID,
		disconnected: make(map[uuid.UUID]*reconnectPlayerState),
		counts:       make(map[uuid.UUID]int),
	}
	raw, _ := m.states.LoadOrStore(duel.ID, state)
	return raw.(*reconnectDuelState)
}

func (m *ReconnectManager) loadState(duelID uuid.UUID) (*reconnectDuelState, bool) {
	raw, ok := m.states.Load(duelID)
	if !ok {
		return nil, false
	}
	return raw.(*reconnectDuelState), true
}

func (m *ReconnectManager) expireReconnect(duelID, playerID uuid.UUID) {
	state, ok := m.loadState(duelID)
	if !ok {
		return
	}

	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return
	}
	entry, ok := state.disconnected[playerID]
	if !ok {
		state.mu.Unlock()
		return
	}
	if entry.reconnecting {
		state.mu.Unlock()
		return
	}
	entry.expired = true

	other := state.opponent(playerID)
	opponentEntry, opponentDisconnected := state.disconnected[other]
	if opponentDisconnected && !opponentEntry.expired {
		state.mu.Unlock()
		return
	}
	state.closed = true
	state.stopReconnectTimersLocked()
	state.mu.Unlock()

	finalizeCtx, finalizeCancel := m.detachedCtx()
	defer finalizeCancel()
	m.finalize(finalizeCtx, duelID, nil, "reconnect_window_expired")
}

// detachedCtx returns a context derived from the server lifecycle but
// stripped of cancellation, so async finalize work that started before
// shutdown can complete (or hit a DB timeout) without being aborted
// mid-transaction by ctx.Done().
func (m *ReconnectManager) detachedCtx() (context.Context, context.CancelFunc) {
	if m == nil || m.ctx == nil {
		return context.WithTimeout(context.Background(), reconnectAsyncTimeout)
	}
	return ctxutil.DetachedWithTimeout(m.ctx, reconnectAsyncTimeout)
}

func boundedDetached(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), reconnectAsyncTimeout)
	}
	return ctxutil.DetachedWithTimeout(ctx, reconnectAsyncTimeout)
}

func (m *ReconnectManager) finalize(ctx context.Context, duelID uuid.UUID, winnerID *uuid.UUID, reason string) {
	if _, err := m.finalizeAndBroadcast(ctx, duelID, winnerID); err != nil && m.log != nil {
		fields := logkit.Fields{
			"duel_id": duelID.String(),
			"error":   err.Error(),
		}
		if reason != "" {
			fields["reason"] = reason
		}
		if winnerID != nil {
			fields["winner_id"] = winnerID.String()
		}
		m.log.Error("duel async finalize failed", fields)
	}
}

func (m *ReconnectManager) finalizeAndBroadcast(
	ctx context.Context,
	duelID uuid.UUID,
	winnerID *uuid.UUID,
) (*domain.Duel, error) {
	if m == nil || m.tx == nil || m.duels == nil || m.players == nil {
		return nil, nil
	}

	finished, err := finalizeDuel(ctx, m.tx, m.duels, m.players, m.clock.Now(), duelID, winnerID, nil, m.log)
	if err != nil || finished == nil {
		return finished, err
	}

	m.CloseDuel(duelID)
	if m.broadcaster != nil {
		m.broadcaster.BroadcastDuelFinished(ctx, finished)
	}
	return finished, nil
}

func (m *ReconnectManager) handleDuelTimerExpired(duelID uuid.UUID) {
	if m == nil || m.duels == nil {
		return
	}
	ctx, cancel := m.detachedCtx()
	defer cancel()
	duel, err := m.duels.GetByID(ctx, duelID)
	if err != nil {
		if m.log != nil {
			m.log.Error("duel timer load failed", logkit.Fields{
				"duel_id": duelID.String(),
				"error":   err.Error(),
			})
		}
		return
	}
	if duel == nil {
		return
	}
	m.CloseDuel(duelID)
	if m.broadcaster != nil {
		m.broadcaster.BroadcastDuelExpired(ctx, duelID)
		m.broadcaster.BroadcastDuelFinished(ctx, duel)
	}
}

type reconnectDuelState struct {
	mu           sync.Mutex
	duelID       uuid.UUID
	player1ID    uuid.UUID
	player2ID    uuid.UUID
	disconnected map[uuid.UUID]*reconnectPlayerState
	counts       map[uuid.UUID]int
	closed       bool
}

func (s *reconnectDuelState) opponent(playerID uuid.UUID) uuid.UUID {
	if playerID == s.player1ID {
		return s.player2ID
	}
	return s.player1ID
}

func (s *reconnectDuelState) stopReconnectTimersLocked() {
	for _, entry := range s.disconnected {
		if entry.timer != nil {
			entry.timer.Stop()
		}
	}
}

type reconnectConsumeResult struct {
	resume               bool
	expiredOpponent      bool
	windowExpired        bool
	opponentDisconnected bool
	opponentDeadline     time.Time
}

func (s *reconnectDuelState) consumeReconnect(playerID uuid.UUID) (reconnectConsumeResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result reconnectConsumeResult
	if s.closed {
		return result, false
	}
	entry, ok := s.disconnected[playerID]
	if !ok {
		return result, false
	}
	if entry.expired {
		result.windowExpired = true
		return result, true
	}
	if entry.reconnecting {
		return result, false
	}
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
	entry.reconnecting = true
	for disconnectedPlayerID, disconnected := range s.disconnected {
		if disconnectedPlayerID == playerID {
			continue
		}
		if disconnected.expired {
			result.expiredOpponent = true
			result.opponentDisconnected = false
			result.opponentDeadline = time.Time{}
			break
		}
		result.opponentDisconnected = true
		result.opponentDeadline = disconnected.deadline
		break
	}
	switch {
	case result.expiredOpponent:
		s.closed = true
		s.stopReconnectTimersLocked()
	case result.opponentDisconnected:
		delete(s.disconnected, playerID)
		entry.reconnecting = false
	default:
		result.resume = true
	}
	return result, true
}

func (s *reconnectDuelState) commitReconnect(playerID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.disconnected[playerID]
	if !ok || !entry.reconnecting {
		return
	}
	delete(s.disconnected, playerID)
}

func (s *reconnectDuelState) disconnectedDeadline(playerID uuid.UUID) (bool, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, time.Time{}
	}
	entry, ok := s.disconnected[playerID]
	if !ok || entry.expired {
		return false, time.Time{}
	}
	return true, entry.deadline
}

func (s *reconnectDuelState) paused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	for _, entry := range s.disconnected {
		if !entry.expired {
			return true
		}
	}
	return false
}

type reconnectPlayerState struct {
	timer          *time.Timer
	deadline       time.Time
	disconnectedAt time.Time
	expired        bool
	reconnecting   bool
	notified       bool
}

func opponentID(duel *domain.Duel, playerID uuid.UUID) (uuid.UUID, bool) {
	switch playerID {
	case duel.Player1ID:
		return duel.Player2ID, true
	case duel.Player2ID:
		return duel.Player1ID, true
	default:
		return uuid.Nil, false
	}
}
