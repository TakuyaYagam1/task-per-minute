package duel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

const (
	DefaultReconnectWindow          = 2 * time.Minute
	DefaultReconnectDisconnectLimit = 2
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

func (m *ReconnectManager) HandleDisconnect(ctx context.Context, duelID, playerID uuid.UUID) {
	if m == nil || m.duels == nil {
		return
	}

	duel, err := m.duels.GetByID(ctx, duelID)
	if err != nil || duel == nil || duel.Status != domain.DuelStatusActive {
		return
	}
	opponentID, ok := opponentID(duel, playerID)
	if !ok {
		return
	}

	now := m.clock.Now()
	state := m.stateFor(duel)
	var reconnectDeadline time.Time
	shouldBroadcastDisconnect := false
	var immediateWinner *uuid.UUID

	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return
	}
	if _, exists := state.disconnected[playerID]; exists {
		state.mu.Unlock()
		return
	}

	state.counts[playerID]++
	if state.counts[playerID] > m.disconnectLimit {
		winner := opponentID
		immediateWinner = &winner
		state.closed = true
		state.stopReconnectTimersLocked()
	} else {
		if len(state.disconnected) == 0 && m.timers != nil {
			m.timers.Freeze(duelID, now)
		}
		reconnectDeadline = now.Add(m.window)
		entry := &reconnectPlayerState{
			deadline:       reconnectDeadline,
			disconnectedAt: now,
		}
		//nolint:contextcheck // Reconnect windows must keep running after the disconnect cleanup returns.
		entry.timer = time.AfterFunc(m.window, func() {
			m.expireReconnect(duelID, playerID)
		})
		state.disconnected[playerID] = entry
		shouldBroadcastDisconnect = true
	}
	state.mu.Unlock()

	if immediateWinner != nil {
		m.finalize(ctx, duelID, immediateWinner)
		return
	}
	if shouldBroadcastDisconnect && m.broadcaster != nil {
		m.broadcaster.BroadcastOpponentDisconnected(ctx, duelID, playerID, reconnectDeadline)
	}
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

func (m *ReconnectManager) FinalizeOpponentForfeit(ctx context.Context, duelID, winnerID uuid.UUID) {
	winner := winnerID
	m.finalize(ctx, duelID, &winner)
}

func (m *ReconnectManager) FinalizePlayerForfeit(
	ctx context.Context,
	duelID uuid.UUID,
	loserID uuid.UUID,
) (*domain.Duel, error) {
	if m == nil || m.duels == nil {
		return nil, nil
	}
	duel, err := m.duels.GetByID(ctx, duelID)
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
	return m.finalizeAndBroadcast(ctx, duelID, &winner)
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

	var winnerID *uuid.UUID
	draw := false

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
	if opponentDisconnected && opponentEntry.expired {
		draw = true
	} else {
		winner := other
		winnerID = &winner
	}
	state.closed = true
	state.stopReconnectTimersLocked()
	state.mu.Unlock()

	finalizeCtx := m.detachedCtx()
	if draw {
		m.finalize(finalizeCtx, duelID, nil)
		return
	}
	m.finalize(finalizeCtx, duelID, winnerID)
}

// detachedCtx returns a context derived from the server lifecycle but
// stripped of cancellation, so async finalize work that started before
// shutdown can complete (or hit a DB timeout) without being aborted
// mid-transaction by ctx.Done().
func (m *ReconnectManager) detachedCtx() context.Context {
	if m == nil || m.ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(m.ctx)
}

func (m *ReconnectManager) finalize(ctx context.Context, duelID uuid.UUID, winnerID *uuid.UUID) {
	_, _ = m.finalizeAndBroadcast(ctx, duelID, winnerID)
}

func (m *ReconnectManager) finalizeAndBroadcast(
	ctx context.Context,
	duelID uuid.UUID,
	winnerID *uuid.UUID,
) (*domain.Duel, error) {
	if m == nil || m.tx == nil || m.duels == nil || m.players == nil {
		return nil, nil
	}

	finished, err := finalizeDuel(ctx, m.tx, m.duels, m.players, m.clock.Now(), duelID, winnerID, m.board, m.log)
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
	ctx := m.detachedCtx()
	duel, err := m.duels.GetByID(ctx, duelID)
	if err != nil || duel == nil {
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
