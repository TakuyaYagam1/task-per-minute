package connection

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

type lifecycleHarness struct {
	command    inbound.TournamentParticipantConnectionCommand
	resolved   ParticipantConnectionAuthority
	lease      DurableLease
	clock      *fixedClock
	provider   *recordingAuthority
	repo       *recordingRepository
	tx         *recordingTransactions
	readiness  *recordingReadiness
	paused     *recordingPausedPresence
	disconnect *recordingDisconnect
	reconnect  *recordingReconnect
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
	}
}

func (h lifecycleHarness) coordinator(t *testing.T) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(Dependencies{
		Transactions: h.tx, Authority: h.provider, Repository: h.repo,
		Readiness: h.readiness, PausedPresence: h.paused,
		Disconnect: h.disconnect, Reconnect: h.reconnect,
		Clock: h.clock, Config: Config{ReconnectDuration: 30 * time.Second},
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
	mu    sync.Mutex
	value ParticipantConnectionAuthority
	err   error
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
	mu     sync.Mutex
	values []game.DisconnectCommand
	err    error
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
	workflow.mu.Unlock()
	return nil, true, err
}

func (workflow *recordingDisconnect) commands() []game.DisconnectCommand {
	workflow.mu.Lock()
	defer workflow.mu.Unlock()
	return append([]game.DisconnectCommand(nil), workflow.values...)
}

type recordingReconnect struct {
	mu     sync.Mutex
	values []game.ReconnectCommand
	err    error
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
	workflow.mu.Unlock()
	return nil, true, err
}

func (workflow *recordingReconnect) commands() []game.ReconnectCommand {
	workflow.mu.Lock()
	defer workflow.mu.Unlock()
	return append([]game.ReconnectCommand(nil), workflow.values...)
}
