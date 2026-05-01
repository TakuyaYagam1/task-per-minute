package duel

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

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
	tx          usecase.TxManager
	duels       usecase.DuelRepo
	players     usecase.PlayerRepo
	timers      DuelTimer
	broadcaster usecase.DuelBroadcaster
	clock       clock.Clock

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
	Duel            *domain.Duel
	OpponentID      uuid.UUID
	NewDeadline     time.Time
	Resume          bool
	OpponentExpired bool
	WindowExpired   bool
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

	resume, expiredOpponent, windowExpired, ok := state.consumeReconnect(playerID)
	if !ok {
		return nil, nil
	}
	decision := &ReconnectDecision{
		Duel:            duel,
		OpponentID:      otherID,
		Resume:          resume,
		OpponentExpired: expiredOpponent,
		WindowExpired:   windowExpired,
	}
	if !resume {
		return decision, nil
	}

	now := m.clock.Now()
	newDeadline := duel.Deadline
	if m.timers != nil {
		if resumed, ok := m.timers.Resume(duel.ID, now); ok {
			newDeadline = resumed
		}
	}
	updated, err := m.duels.UpdateDeadline(ctx, duel.ID, newDeadline)
	if err == nil && updated != nil {
		newDeadline = updated.Deadline
	}
	decision.NewDeadline = newDeadline
	return decision, nil
}

func (m *ReconnectManager) FinalizeOpponentForfeit(ctx context.Context, duelID, winnerID uuid.UUID) {
	winner := winnerID
	m.finalize(ctx, duelID, &winner)
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

	if draw {
		m.finalize(context.Background(), duelID, nil)
		return
	}
	m.finalize(context.Background(), duelID, winnerID)
}

func (m *ReconnectManager) finalize(ctx context.Context, duelID uuid.UUID, winnerID *uuid.UUID) {
	if m == nil || m.tx == nil || m.duels == nil || m.players == nil {
		return
	}

	finished, err := finalizeDuel(ctx, m.tx, m.duels, m.players, m.clock.Now(), duelID, winnerID)
	if err != nil || finished == nil {
		return
	}

	m.CloseDuel(duelID)
	if m.broadcaster != nil {
		m.broadcaster.BroadcastDuelFinished(ctx, finished)
	}
}

func (m *ReconnectManager) handleDuelTimerExpired(duelID uuid.UUID) {
	if m == nil || m.duels == nil {
		return
	}
	ctx := context.Background()
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

func (s *reconnectDuelState) consumeReconnect(playerID uuid.UUID) (resume, expiredOpponent, windowExpired, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, false, false, false
	}
	entry, ok := s.disconnected[playerID]
	if !ok {
		return false, false, false, false
	}
	if entry.expired {
		return false, false, true, true
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	delete(s.disconnected, playerID)
	for otherPlayerID, disconnected := range s.disconnected {
		if otherPlayerID != playerID && disconnected.expired {
			expiredOpponent = true
		}
	}
	if expiredOpponent {
		s.closed = true
		s.stopReconnectTimersLocked()
	} else {
		resume = len(s.disconnected) == 0
	}
	return resume, expiredOpponent, false, true
}

type reconnectPlayerState struct {
	timer          *time.Timer
	deadline       time.Time
	disconnectedAt time.Time
	expired        bool
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
