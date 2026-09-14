package connection

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

type lifecycleHarness struct {
	command    inbound.TournamentParticipantConnectionCommand
	resolved   ParticipantConnectionAuthority
	lease      DurableLease
	clock      *fixedClock
	provider   *recordingAuthority
	repo       *recordingRepository
	recovery   *recordingRecoveryRepository
	tx         *recordingTransactions
	readiness  *recordingReadiness
	paused     *recordingPausedPresence
	disconnect *recordingDisconnect
	reconnect  *recordingReconnect
	terminal   *recordingTerminalAdvancer
}

func newLifecycleHarness(t *testing.T) lifecycleHarness {
	t.Helper()
	tournamentID := uuid.New()
	playerID := uuid.New()
	rosterID := uuid.New()
	participantID := uuid.New()
	authority := authoritydomain.Identity{
		TournamentID: tournamentID,
		HolderID:     uuid.New(),
		LeaseID:      uuid.New(),
		Epoch:        1,
		ProcessKind:  authoritydomain.ProcessAuthority,
	}
	scope := pausedomain.GraphScope{
		TournamentID: tournamentID,
		RosterID:     rosterID,
		WaveID:       uuid.New(),
		Authority:    authority,
	}
	resolved := ParticipantConnectionAuthority{
		TournamentID:  tournamentID,
		RosterID:      rosterID,
		ParticipantID: participantID,
		PlayerID:      playerID,
		Scope:         scope,
		Authority:     authority,
	}
	command := inbound.TournamentParticipantConnectionCommand{
		TournamentID:         tournamentID,
		PlayerID:             playerID,
		ConnectionID:         uuid.New(),
		ConnectionGeneration: 1,
	}
	lease := DurableLease{
		ID:                   uuid.New(),
		TournamentID:         tournamentID,
		RosterID:             rosterID,
		ParticipantID:        participantID,
		PlayerID:             playerID,
		ConnectionID:         command.ConnectionID,
		ConnectionGeneration: command.ConnectionGeneration,
	}
	return lifecycleHarness{
		command: command, resolved: resolved, lease: lease,
		clock:    &fixedClock{value: time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)},
		provider: &recordingAuthority{value: resolved},
		repo:     &recordingRepository{}, tx: &recordingTransactions{},
		readiness: &recordingReadiness{}, paused: &recordingPausedPresence{},
		disconnect: &recordingDisconnect{}, reconnect: &recordingReconnect{},
		terminal: &recordingTerminalAdvancer{},
	}
}

func (h lifecycleHarness) coordinator(t *testing.T) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(Dependencies{
		Transactions: h.tx, Authority: h.provider, Repository: h.repo,
		Readiness: h.readiness, PausedPresence: h.paused,
		Disconnect: h.disconnect, Reconnect: h.reconnect,
		TerminalAdvancer: h.terminal,
		Clock:            h.clock, Config: Config{ReconnectDuration: 30 * time.Second},
	})
	require.NoError(t, err)
	return coordinator
}

func TestCoordinatorDisconnectsLastTabWithOperatorPresenceEvidenceOnly(t *testing.T) {
	h := newLifecycleHarness(t)
	h.repo.closeResult = CloseConnectionResult{
		Lease: h.lease, Closed: true, ActiveLeaseCount: 0,
		Action: ResolvedAction{
			Kind: ActionPausedPresence,
			PausedPresence: &game.PausedPresenceCommand{
				Scope: h.resolved.Scope, PauseID: uuid.New(), ParticipantID: h.resolved.ParticipantID,
				ExpectedGraphRevision: 2, ExpectedPauseRevision: 1,
				ExpectedPresenceEpoch: 3, ExpectedPresenceRevision: 3,
			},
		},
	}

	err := h.coordinator(t).Disconnect(context.Background(), h.command)
	require.NoError(t, err)
	require.Len(t, h.paused.commands(), 1)
	require.Equal(t, pausedomain.PresenceStateDisconnected, h.paused.commands()[0].NextState)
	require.NotEqual(t, uuid.Nil, h.paused.commands()[0].CommandID)
	require.Empty(t, h.readiness.commands())
	require.Empty(t, h.disconnect.commands())
	require.Empty(t, h.reconnect.commands())
	require.Equal(t, 1, h.tx.count())
	require.Equal(t, h.resolved.ParticipantID, h.repo.closeCommands()[0].ParticipantID)
}

func TestCoordinatorRecoversExpiredLeaseWithLastLeaseAction(t *testing.T) {
	h := newLifecycleHarness(t)
	h.recovery = &recordingRecoveryRepository{
		result: CloseConnectionResult{
			Lease:            h.lease,
			Closed:           true,
			ActiveLeaseCount: 0,
			Action: ResolvedAction{
				Kind: ActionPausedPresence,
				PausedPresence: &game.PausedPresenceCommand{
					Scope: h.resolved.Scope, PauseID: uuid.New(), ParticipantID: h.resolved.ParticipantID,
					ExpectedGraphRevision: 2, ExpectedPauseRevision: 1,
					ExpectedPresenceEpoch: 3, ExpectedPresenceRevision: 3,
				},
			},
		},
	}
	coordinator, err := NewCoordinator(Dependencies{
		Transactions: h.tx, Authority: h.provider, Repository: h.repo, Recovery: h.recovery,
		Readiness: h.readiness, PausedPresence: h.paused,
		Disconnect: h.disconnect, Reconnect: h.reconnect,
		TerminalAdvancer: h.terminal, Clock: h.clock,
		Config: Config{ReconnectDuration: 30 * time.Second},
	})
	require.NoError(t, err)

	candidate := OrphanedConnectionLease{Lease: h.lease, Revision: 1, Authority: h.resolved.Authority}
	require.NoError(t, coordinator.recoverOrphanedConnection(context.Background(), candidate))
	require.Len(t, h.recovery.candidates, 1)
	require.Len(t, h.paused.commands(), 1)
	require.Equal(t, pausedomain.PresenceStateDisconnected, h.paused.commands()[0].NextState)
	require.Equal(t, 1, h.tx.count())
}

func TestCoordinatorRejectsOwnerlessRecoveryCandidate(t *testing.T) {
	h := newLifecycleHarness(t)
	h.recovery = &recordingRecoveryRepository{}
	coordinator, err := NewCoordinator(Dependencies{
		Transactions: h.tx, Authority: h.provider, Repository: h.repo, Recovery: h.recovery,
		Clock: h.clock, Config: Config{ReconnectDuration: time.Second},
	})
	require.NoError(t, err)

	err = coordinator.recoverOrphanedConnection(context.Background(), OrphanedConnectionLease{
		Lease: h.lease, Revision: 1,
	})
	require.ErrorIs(t, err, ErrInvalidLease)
	require.Zero(t, h.tx.count())
}

func TestCoordinatorRecoveryRaceAppliesLastLeaseActionOnce(t *testing.T) {
	h := newLifecycleHarness(t)
	h.recovery = &recordingRecoveryRepository{}
	remaining := 1
	h.recovery.closeFn = func() CloseConnectionResult {
		h.recovery.mu.Lock()
		defer h.recovery.mu.Unlock()
		if remaining == 0 {
			return CloseConnectionResult{Closed: false, ActiveLeaseCount: 0, Action: ResolvedAction{Kind: ActionNone}}
		}
		remaining--
		return CloseConnectionResult{
			Lease: h.lease, Closed: true, ActiveLeaseCount: 0,
			Action: ResolvedAction{Kind: ActionPausedPresence, PausedPresence: &game.PausedPresenceCommand{
				Scope: h.resolved.Scope, PauseID: uuid.New(), ParticipantID: h.resolved.ParticipantID,
				ExpectedGraphRevision: 2, ExpectedPauseRevision: 1,
				ExpectedPresenceEpoch: 3, ExpectedPresenceRevision: 3,
			}},
		}
	}
	coordinator, err := NewCoordinator(Dependencies{
		Transactions: h.tx, Authority: h.provider, Repository: h.repo, Recovery: h.recovery,
		PausedPresence: h.paused, Clock: h.clock,
		Config: Config{ReconnectDuration: time.Second},
	})
	require.NoError(t, err)
	candidate := OrphanedConnectionLease{Lease: h.lease, Revision: 1, Authority: h.resolved.Authority}

	const callers = 32
	var wait sync.WaitGroup
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			_ = coordinator.recoverOrphanedConnection(context.Background(), candidate)
		}()
	}
	wait.Wait()
	require.Len(t, h.paused.commands(), 1)
}

func TestCoordinatorDoesNotPauseWhenAnotherTabRemains(t *testing.T) {
	h := newLifecycleHarness(t)
	h.repo.closeResult = CloseConnectionResult{
		Lease: h.lease, Closed: true, ActiveLeaseCount: 1,
		Action: ResolvedAction{Kind: ActionPausedPresence, PausedPresence: &game.PausedPresenceCommand{
			Scope: h.resolved.Scope, PauseID: uuid.New(), ParticipantID: h.resolved.ParticipantID,
			ExpectedGraphRevision: 2, ExpectedPauseRevision: 1,
			ExpectedPresenceEpoch: 3, ExpectedPresenceRevision: 3,
		}},
	}

	require.NoError(t, h.coordinator(t).Disconnect(context.Background(), h.command))
	require.Empty(t, h.paused.commands())
}

func TestCoordinatorIgnoresDuplicateOrStaleCloseWithoutReset(t *testing.T) {
	h := newLifecycleHarness(t)
	h.repo.closeResult = CloseConnectionResult{Closed: false, ActiveLeaseCount: 1, Action: ResolvedAction{Kind: ActionNone}}

	require.NoError(t, h.coordinator(t).Disconnect(context.Background(), h.command))
	stale := h.command
	stale.ConnectionGeneration = 9
	require.NoError(t, h.coordinator(t).Disconnect(context.Background(), stale))

	commands := h.repo.closeCommands()
	require.Len(t, commands, 2)
	require.Equal(t, int64(1), commands[0].ConnectionGeneration)
	require.Equal(t, int64(9), commands[1].ConnectionGeneration)
	require.Empty(t, h.paused.commands())
	require.Empty(t, h.readiness.commands())
	require.Empty(t, h.disconnect.commands())
}

func TestCoordinatorReconnectsBeforeResolvedDeadlineWithDeterministicIDs(t *testing.T) {
	h := newLifecycleHarness(t)
	intervalID := uuid.New()
	h.repo.openResult = OpenConnectionResult{
		Lease: h.lease, Opened: true, ActiveLeaseCount: 1,
		Action: ResolvedAction{
			Kind:     ActionGameReconnect,
			Deadline: h.clock.value.Add(time.Minute),
			Reconnect: &game.ReconnectCommand{
				Scope: h.resolved.Scope, ParticipantID: h.resolved.ParticipantID, IntervalID: intervalID,
			},
		},
	}

	require.NoError(t, h.coordinator(t).Connect(context.Background(), h.command))
	require.Len(t, h.reconnect.commands(), 1)
	got := h.reconnect.commands()[0]
	require.Equal(t, intervalID, got.IntervalID)
	require.NotEqual(t, uuid.Nil, got.CommandID)
	require.NotEqual(t, uuid.Nil, got.Settlement.GameResultRevisionID.UUID())
	require.NotEqual(t, uuid.Nil, got.Settlement.ScoreRevisionID.UUID())
	require.NotEqual(t, uuid.Nil, got.Settlement.SeriesResultRevisionID.UUID())
	require.NotEqual(t, got.Settlement.AuditEventID, got.Settlement.OutboxEventID)

	h.clock.value = h.clock.value.Add(2 * time.Minute)
	h.repo.openResult = OpenConnectionResult{
		Lease: h.lease, Opened: true, ActiveLeaseCount: 1,
		Action: ResolvedAction{
			Kind: ActionGameReconnect, Deadline: h.clock.value.Add(-time.Second),
			Reconnect: &game.ReconnectCommand{
				Scope: h.resolved.Scope, ParticipantID: h.resolved.ParticipantID, IntervalID: intervalID,
			},
		},
	}
	err := h.coordinator(t).Connect(context.Background(), h.command)
	require.ErrorIs(t, err, game.ErrDeadline)
	require.Len(t, h.reconnect.commands(), 1)
}

func TestCoordinatorAdvancesCompletedReconnectInOuterTransaction(t *testing.T) {
	h := newLifecycleHarness(t)
	seriesID := uuid.New()
	h.reconnect.record = terminalReconnectRecord(h.resolved.Scope, seriesID, domain.SeriesStateCompleted)
	intervalID := uuid.New()
	h.repo.openResult = OpenConnectionResult{
		Lease: h.lease, Opened: true, ActiveLeaseCount: 1,
		Action: ResolvedAction{
			Kind:     ActionGameReconnect,
			Deadline: h.clock.value.Add(time.Minute),
			Reconnect: &game.ReconnectCommand{
				Scope: h.resolved.Scope, ParticipantID: h.resolved.ParticipantID, IntervalID: intervalID,
			},
		},
	}

	require.NoError(t, h.coordinator(t).Connect(context.Background(), h.command))
	require.Equal(t, []playoff.TerminalSeriesCommand{{TournamentID: h.resolved.TournamentID, SeriesID: seriesID}}, h.terminal.commands())
	require.Equal(t, 1, h.tx.count())
}

func TestCoordinatorAdvancesActiveReconnectInOuterTransaction(t *testing.T) {
	h := newLifecycleHarness(t)
	seriesID := uuid.New()
	h.reconnect.record = terminalReconnectRecord(h.resolved.Scope, seriesID, domain.SeriesStateActive)
	h.repo.openResult = OpenConnectionResult{
		Lease: h.lease, Opened: true, ActiveLeaseCount: 1,
		Action: ResolvedAction{Kind: ActionGameReconnect, Deadline: h.clock.value.Add(time.Minute), Reconnect: &game.ReconnectCommand{
			Scope: h.resolved.Scope, ParticipantID: h.resolved.ParticipantID, IntervalID: uuid.New(),
		}},
	}

	require.NoError(t, h.coordinator(t).Connect(context.Background(), h.command))
	require.Equal(t, []playoff.TerminalSeriesCommand{{
		TournamentID: h.resolved.TournamentID,
		SeriesID:     seriesID,
	}}, h.terminal.commands())
}

func TestCoordinatorAdvancesCompletedDisconnectInOuterTransaction(t *testing.T) {
	h := newLifecycleHarness(t)
	seriesID := uuid.New()
	h.disconnect.record = terminalReconnectRecord(h.resolved.Scope, seriesID, domain.SeriesStateCompleted)
	h.repo.closeResult = CloseConnectionResult{
		Lease: h.lease, Closed: true, ActiveLeaseCount: 0,
		Action: ResolvedAction{
			Kind: ActionGameDisconnect,
			Disconnect: &game.DisconnectCommand{
				Scope: h.resolved.Scope, ParticipantID: h.resolved.ParticipantID,
			},
		},
	}

	require.NoError(t, h.coordinator(t).Disconnect(context.Background(), h.command))
	require.Equal(t, []playoff.TerminalSeriesCommand{{
		TournamentID: h.resolved.TournamentID,
		SeriesID:     seriesID,
	}}, h.terminal.commands())
	require.Equal(t, 1, h.tx.count())
}

func TestCoordinatorDoesNotAdvanceReplayOrUnchangedReconnect(t *testing.T) {
	tests := []struct {
		name        string
		changed     *bool
		route       bool
		void        bool
		noResult    bool
		seriesState domain.SeriesState
	}{
		{name: "replay required", route: true, seriesState: domain.SeriesStateReplayRequired},
		{name: "void result", void: true},
		{name: "cancelled Series", seriesState: domain.SeriesStateCancelled},
		{name: "no result", noResult: true},
		{name: "unchanged", changed: boolPointer(false)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newLifecycleHarness(t)
			seriesID := uuid.New()
			seriesState := test.seriesState
			if seriesState == "" {
				seriesState = domain.SeriesStateCompleted
			}
			route := (*game.WaveMemberRoute)(nil)
			if test.route {
				route = &game.WaveMemberRoute{ID: uuid.New()}
			}
			h.reconnect.record = terminalReconnectRecord(h.resolved.Scope, seriesID, seriesState)
			h.reconnect.record.ReplayRoute = route
			if test.void {
				h.reconnect.record.VoidGameResultRevision = &game.AttemptGameResultRevision{ID: domain.OfficialResultRevisionID(uuid.New())}
			}
			if test.noResult {
				h.reconnect.record.GameResultRevision = nil
			}
			h.reconnect.changed = test.changed
			h.repo.openResult = OpenConnectionResult{
				Lease: h.lease, Opened: true, ActiveLeaseCount: 1,
				Action: ResolvedAction{Kind: ActionGameReconnect, Deadline: h.clock.value.Add(time.Minute), Reconnect: &game.ReconnectCommand{
					Scope: h.resolved.Scope, ParticipantID: h.resolved.ParticipantID, IntervalID: uuid.New(),
				}},
			}

			require.NoError(t, h.coordinator(t).Connect(context.Background(), h.command))
			require.Empty(t, h.terminal.commands())
		})
	}
}

func TestCoordinatorRollsBackWhenTerminalAdvancementFails(t *testing.T) {
	h := newLifecycleHarness(t)
	seriesID := uuid.New()
	h.reconnect.record = terminalReconnectRecord(h.resolved.Scope, seriesID, domain.SeriesStateCompleted)
	h.terminal.err = errors.New("terminal advancement failed")
	h.repo.openResult = OpenConnectionResult{
		Lease: h.lease, Opened: true, ActiveLeaseCount: 1,
		Action: ResolvedAction{Kind: ActionGameReconnect, Deadline: h.clock.value.Add(time.Minute), Reconnect: &game.ReconnectCommand{
			Scope: h.resolved.Scope, ParticipantID: h.resolved.ParticipantID, IntervalID: uuid.New(),
		}},
	}

	err := h.coordinator(t).Connect(context.Background(), h.command)
	require.ErrorIs(t, err, h.terminal.err)
	require.Len(t, h.terminal.commands(), 1)
	require.Equal(t, 1, h.tx.count())
}

func TestCoordinatorClearsReadinessOnLastPreStartDisconnect(t *testing.T) {
	h := newLifecycleHarness(t)
	h.repo.closeResult = CloseConnectionResult{
		Lease: h.lease, Closed: true, ActiveLeaseCount: 0,
		Action: ResolvedAction{
			Kind: ActionClearReadiness,
			Readiness: &readiness.DisconnectReadinessCommand{
				Scope:                    readiness.ReadinessScope{WaveID: h.resolved.Scope.WaveID, WindowID: uuid.New()},
				ParticipantID:            h.resolved.ParticipantID,
				ExpectedWaveRevisionID:   domain.WaveRevisionID(uuid.New()),
				ExpectedWindowRevisionID: domain.ReadyWindowRevisionID(uuid.New()),
			},
		},
	}

	require.NoError(t, h.coordinator(t).Disconnect(context.Background(), h.command))
	require.Len(t, h.readiness.commands(), 1)
	require.Equal(t, h.resolved.ParticipantID, h.readiness.commands()[0].ParticipantID)
	require.NotEqual(t, uuid.Nil, h.readiness.commands()[0].CommandID)
	require.Empty(t, h.paused.commands())
}

func TestCoordinatorRejectsWrongResolvedBindingBeforeLeaseMutation(t *testing.T) {
	h := newLifecycleHarness(t)
	h.provider.value.RosterID = uuid.New()

	err := h.coordinator(t).Connect(context.Background(), h.command)
	require.ErrorIs(t, err, ErrInvalidAuthority)
	require.Empty(t, h.repo.openCommands())
	require.Empty(t, h.repo.closeCommands())
}

func TestCoordinatorRejectsInvalidTransportCommandBeforeTransaction(t *testing.T) {
	h := newLifecycleHarness(t)
	invalid := h.command
	invalid.ConnectionGeneration = 0

	err := h.coordinator(t).Connect(context.Background(), invalid)
	require.ErrorIs(t, err, inbound.ErrInvalidTournamentParticipantConnectionCommand)
	require.Equal(t, 0, h.tx.count())
	require.Empty(t, h.repo.openCommands())
}

func TestCoordinatorGoldenAndNoCurrentActionRemainNoOp(t *testing.T) {
	h := newLifecycleHarness(t)
	h.repo.openResult = OpenConnectionResult{Lease: h.lease, Opened: true, ActiveLeaseCount: 1, Action: ResolvedAction{Kind: ActionNone}}

	require.NoError(t, h.coordinator(t).Connect(context.Background(), h.command))
	require.Empty(t, h.paused.commands())
	require.Empty(t, h.reconnect.commands())
	require.Empty(t, h.disconnect.commands())
	require.Empty(t, h.readiness.commands())
}

func TestCoordinatorCloseRaceAppliesLastTabActionOnce(t *testing.T) {
	h := newLifecycleHarness(t)
	var remaining int
	h.repo.closeFn = func() CloseConnectionResult {
		h.repo.mu.Lock()
		defer h.repo.mu.Unlock()
		if remaining > 0 {
			remaining--
			return CloseConnectionResult{
				Lease: h.lease, Closed: true, ActiveLeaseCount: 0,
				Action: ResolvedAction{Kind: ActionPausedPresence, PausedPresence: &game.PausedPresenceCommand{
					Scope: h.resolved.Scope, PauseID: uuid.New(), ParticipantID: h.resolved.ParticipantID,
					ExpectedGraphRevision: 2, ExpectedPauseRevision: 1,
					ExpectedPresenceEpoch: 3, ExpectedPresenceRevision: 3,
				}},
			}
		}
		return CloseConnectionResult{Closed: false, ActiveLeaseCount: 0}
	}
	remaining = 1

	const callers = 32
	coordinator := h.coordinator(t)
	var wait sync.WaitGroup
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			_ = coordinator.Disconnect(context.Background(), h.command)
		}()
	}
	wait.Wait()
	require.Len(t, h.paused.commands(), 1)
}

func TestNewCoordinatorRequiresExplicitPositiveReconnectDuration(t *testing.T) {
	h := newLifecycleHarness(t)
	_, err := NewCoordinator(Dependencies{
		Transactions: h.tx, Authority: h.provider, Repository: h.repo, Clock: h.clock,
	})
	require.ErrorIs(t, err, ErrInvalidConfiguration)
}

type fixedClock struct {
	mu    sync.RWMutex
	value time.Time
}

func (clock *fixedClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.value
}

type contextMarker struct{}

type recordingTransactions struct {
	mu    sync.Mutex
	calls int
}

func (transactions *recordingTransactions) Do(ctx context.Context, fn func(context.Context) error) error {
	transactions.mu.Lock()
	transactions.calls++
	transactions.mu.Unlock()
	return fn(context.WithValue(ctx, contextMarker{}, true))
}

func (transactions *recordingTransactions) count() int {
	transactions.mu.Lock()
	defer transactions.mu.Unlock()
	return transactions.calls
}

type recordingAuthority struct {
	mu            sync.Mutex
	value         ParticipantConnectionAuthority
	err           error
	recoveryCalls []uuid.UUID
}

func (authority *recordingAuthority) RecoveryAuthorityFor(
	_ context.Context,
	tournamentID uuid.UUID,
) (authoritydomain.Identity, bool, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.recoveryCalls = append(authority.recoveryCalls, tournamentID)
	if errors.Is(authority.err, authorityusecase.ErrNotOwner) {
		return authoritydomain.Identity{}, false, nil
	}
	if authority.err != nil {
		return authoritydomain.Identity{}, false, authority.err
	}
	return authority.value.Authority, true, nil
}

func (authority *recordingAuthority) ResolveParticipantConnection(
	ctx context.Context, _ uuid.UUID, _ uuid.UUID,
) (ParticipantConnectionAuthority, error) {
	if _, ok := ctx.Value(contextMarker{}).(bool); !ok {
		return ParticipantConnectionAuthority{}, fmt.Errorf("authority did not receive transaction context")
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.value, authority.err
}

type recordingRepository struct {
	mu          sync.Mutex
	openResult  OpenConnectionResult
	closeResult CloseConnectionResult
	openFn      func() OpenConnectionResult
	closeFn     func() CloseConnectionResult
	openCalls   []OpenConnectionCommand
	closeCalls  []CloseConnectionCommand
}

type recordingRecoveryRepository struct {
	mu          sync.Mutex
	tournaments []uuid.UUID
	candidates  []OrphanedConnectionLease
	result      CloseConnectionResult
	closeFn     func() CloseConnectionResult
}

func (repository *recordingRecoveryRepository) ListParticipantConnectionLeaseTournaments(
	context.Context,
) ([]uuid.UUID, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]uuid.UUID(nil), repository.tournaments...), nil
}

func (repository *recordingRecoveryRepository) ListParticipantConnectionRecoveryCandidates(
	context.Context,
	int32,
) ([]OrphanedConnectionLease, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]OrphanedConnectionLease(nil), repository.candidates...), nil
}

func (repository *recordingRecoveryRepository) CloseOrphanedConnection(
	ctx context.Context,
	_ ParticipantConnectionAuthority,
	candidate OrphanedConnectionLease,
) (CloseConnectionResult, error) {
	if _, ok := ctx.Value(contextMarker{}).(bool); !ok {
		return CloseConnectionResult{}, fmt.Errorf("recovery repository did not receive transaction context")
	}
	repository.mu.Lock()
	repository.candidates = append(repository.candidates, candidate)
	result := repository.result
	closeFn := repository.closeFn
	repository.mu.Unlock()
	if closeFn != nil {
		result = closeFn()
	}
	return result, nil
}

func (repository *recordingRepository) OpenConnection(
	ctx context.Context, command OpenConnectionCommand,
) (OpenConnectionResult, error) {
	if _, ok := ctx.Value(contextMarker{}).(bool); !ok {
		return OpenConnectionResult{}, fmt.Errorf("repository did not receive transaction context")
	}
	repository.mu.Lock()
	repository.openCalls = append(repository.openCalls, command)
	result := repository.openResult
	fn := repository.openFn
	repository.mu.Unlock()
	if fn != nil {
		result = fn()
	}
	return result, nil
}

func (repository *recordingRepository) CloseConnection(
	ctx context.Context, command CloseConnectionCommand,
) (CloseConnectionResult, error) {
	if _, ok := ctx.Value(contextMarker{}).(bool); !ok {
		return CloseConnectionResult{}, fmt.Errorf("repository did not receive transaction context")
	}
	repository.mu.Lock()
	repository.closeCalls = append(repository.closeCalls, command)
	result := repository.closeResult
	fn := repository.closeFn
	repository.mu.Unlock()
	if fn != nil {
		result = fn()
	}
	return result, nil
}

func (repository *recordingRepository) openCommands() []OpenConnectionCommand {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]OpenConnectionCommand(nil), repository.openCalls...)
}

func (repository *recordingRepository) closeCommands() []CloseConnectionCommand {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]CloseConnectionCommand(nil), repository.closeCalls...)
}

type recordingReadiness struct {
	mu     sync.Mutex
	values []readiness.DisconnectReadinessCommand
	err    error
}

func (workflow *recordingReadiness) ClearOnDisconnect(
	ctx context.Context, command readiness.DisconnectReadinessCommand,
) (*readiness.ReadinessRecord, bool, error) {
	if _, ok := ctx.Value(contextMarker{}).(bool); !ok {
		return nil, false, fmt.Errorf("readiness did not receive transaction context")
	}
	workflow.mu.Lock()
	workflow.values = append(workflow.values, command)
	err := workflow.err
	workflow.mu.Unlock()
	return nil, true, err
}

func (workflow *recordingReadiness) commands() []readiness.DisconnectReadinessCommand {
	workflow.mu.Lock()
	defer workflow.mu.Unlock()
	return append([]readiness.DisconnectReadinessCommand(nil), workflow.values...)
}

type recordingPausedPresence struct {
	mu     sync.Mutex
	values []game.PausedPresenceCommand
	err    error
}

func (workflow *recordingPausedPresence) Change(
	ctx context.Context, command game.PausedPresenceCommand,
) (*game.PausedPresenceRecord, bool, error) {
	if _, ok := ctx.Value(contextMarker{}).(bool); !ok {
		return nil, false, fmt.Errorf("paused presence did not receive transaction context")
	}
	workflow.mu.Lock()
	workflow.values = append(workflow.values, command)
	err := workflow.err
	workflow.mu.Unlock()
	return nil, true, err
}

func (workflow *recordingPausedPresence) commands() []game.PausedPresenceCommand {
	workflow.mu.Lock()
	defer workflow.mu.Unlock()
	return append([]game.PausedPresenceCommand(nil), workflow.values...)
}

type recordingDisconnect struct {
	mu      sync.Mutex
	values  []game.DisconnectCommand
	err     error
	record  *game.ReconnectRecord
	changed *bool
}

func (workflow *recordingDisconnect) Disconnect(
	ctx context.Context, command game.DisconnectCommand,
) (*game.ReconnectRecord, bool, error) {
	if _, ok := ctx.Value(contextMarker{}).(bool); !ok {
		return nil, false, fmt.Errorf("disconnect did not receive transaction context")
	}
	workflow.mu.Lock()
	workflow.values = append(workflow.values, command)
	err := workflow.err
	record := workflow.record
	changed := true
	if workflow.changed != nil {
		changed = *workflow.changed
	}
	workflow.mu.Unlock()
	return record, changed, err
}

func (workflow *recordingDisconnect) commands() []game.DisconnectCommand {
	workflow.mu.Lock()
	defer workflow.mu.Unlock()
	return append([]game.DisconnectCommand(nil), workflow.values...)
}

type recordingReconnect struct {
	mu      sync.Mutex
	values  []game.ReconnectCommand
	err     error
	record  *game.ReconnectRecord
	changed *bool
}

func (workflow *recordingReconnect) Reconnect(
	ctx context.Context, command game.ReconnectCommand,
) (*game.ReconnectRecord, bool, error) {
	if _, ok := ctx.Value(contextMarker{}).(bool); !ok {
		return nil, false, fmt.Errorf("reconnect did not receive transaction context")
	}
	workflow.mu.Lock()
	workflow.values = append(workflow.values, command)
	err := workflow.err
	record := workflow.record
	changed := true
	if workflow.changed != nil {
		changed = *workflow.changed
	}
	workflow.mu.Unlock()
	return record, changed, err
}

func (workflow *recordingReconnect) commands() []game.ReconnectCommand {
	workflow.mu.Lock()
	defer workflow.mu.Unlock()
	return append([]game.ReconnectCommand(nil), workflow.values...)
}

type recordingTerminalAdvancer struct {
	mu     sync.Mutex
	values []playoff.TerminalSeriesCommand
	err    error
}

func (advancer *recordingTerminalAdvancer) AdvanceAfterSeriesSettlement(
	ctx context.Context,
	command playoff.TerminalSeriesCommand,
) (playoff.TerminalReceipt, error) {
	if _, ok := ctx.Value(contextMarker{}).(bool); !ok {
		return playoff.TerminalReceipt{}, fmt.Errorf("terminal advancement did not receive transaction context")
	}
	advancer.mu.Lock()
	advancer.values = append(advancer.values, command)
	err := advancer.err
	advancer.mu.Unlock()
	return playoff.TerminalReceipt{}, err
}

func (advancer *recordingTerminalAdvancer) commands() []playoff.TerminalSeriesCommand {
	advancer.mu.Lock()
	defer advancer.mu.Unlock()
	return append([]playoff.TerminalSeriesCommand(nil), advancer.values...)
}

func boolPointer(value bool) *bool {
	return &value
}

func terminalReconnectRecord(
	scope pausedomain.GraphScope,
	seriesID uuid.UUID,
	seriesState domain.SeriesState,
) *game.ReconnectRecord {
	gameID := uuid.New()
	winnerID := uuid.New()
	gameResultID := domain.OfficialResultRevisionID(uuid.New())
	scoreRevisionID := domain.SeriesScoreRevisionID(uuid.New())
	firstParticipantID := winnerID
	secondParticipantID := uuid.New()
	score := domain.SeriesScore{FirstParticipantWins: 1}
	seriesResultID := domain.OfficialResultRevisionID(uuid.New())
	if seriesState == domain.SeriesStateCompleted {
		score.FirstParticipantWins = 2
	}
	series := domain.Series{
		ID: seriesID, TournamentID: scope.TournamentID,
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: domain.SeriesFormatBO3, State: seriesState, Score: score,
		CurrentScoreRevisionID: &scoreRevisionID,
	}
	result := (*game.SeriesRevision)(nil)
	if seriesState == domain.SeriesStateCompleted {
		series.WinnerID = &winnerID
		series.CurrentResultRevisionID = &seriesResultID
		result = &game.SeriesRevision{ID: seriesResultID, SeriesID: seriesID, State: seriesState, WinnerID: &winnerID, ScoreRevisionID: scoreRevisionID}
	}
	return &game.ReconnectRecord{
		ReconnectAuthority: game.ReconnectAuthority{
			Scope:  scope,
			Game:   domain.Game{ID: gameID, State: domain.GameStateCompleted, WinnerID: &winnerID, ResultRevisionID: &gameResultID},
			Series: series,
		},
		GameResultRevision: &game.GameRevision{ID: gameResultID, GameID: gameID, WinnerID: winnerID, Reason: domain.GameResultReasonOperatorForfeit},
		ScoreRevision: &seriesdomain.ScoreRevision{
			ID: scoreRevisionID, SeriesID: seriesID,
			FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
			Format: domain.SeriesFormatBO3, ScoreAfter: score,
			GameResultRevisionIDs: []domain.OfficialResultRevisionID{gameResultID},
		},
		SeriesResultRevision: result,
		Evidence: &seriesdomain.SettlementEvidence{
			AuditEventID: uuid.New(), OutboxEventID: uuid.New(), ProjectionRevisionID: uuid.New(),
			SourceProjectionRevision: 1, ProjectionRevision: 2,
		},
	}
}
