package websocket

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	eventdelivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
	eventdeliverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery/mocks"
)

func TestParticipantLifecycleConnectsBeforeInitialSnapshotAndDisconnects(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(1201)
	playerID := tournamentSourceID(1202)
	token := tournamentSourceID(1203)
	subscriberID := tournamentSourceID(1204)
	instanceID := tournamentSourceID(1205)
	connectionGeneration := int64(7)
	player := participantSessionPlayer(playerID, token, time.Now().Add(time.Hour))
	players := middlewaremocks.NewMockPlayerSessionReader(t)
	players.EXPECT().GetBySessionToken(mock.Anything, token).Return(player, nil).Maybe()
	participantFrame, _, _ := tournamentWriteTestFrames(t, tournamentID, playerID)
	participantMessage, err := DecodeTournamentParticipantMessage(participantFrame)
	require.NoError(t, err)
	require.NotNil(t, participantMessage.Participant)
	before := *participantMessage.Participant
	after := before
	after.Envelope.ProjectionRevision++
	after.Envelope.Participant.Revision++
	connected := new(atomic.Bool)

	repository := participantConnectionRepository(t, tournamentID, playerID, subscriberID, connectionGeneration)
	repository.EXPECT().Cursor(mock.Anything, tournamentID).Return(int64(0), nil).Once()
	repository.EXPECT().ListAfter(mock.Anything, mock.MatchedBy(func(request eventdelivery.ReplayRequest) bool {
		return request.TournamentID == tournamentID && request.AfterSequence == 0 &&
			request.Audience == eventdelivery.AudienceParticipant && request.PrincipalID == playerID
	})).Return(nil, nil).Once()
	repository.EXPECT().SubscriberCursor(mock.Anything, mock.Anything).Return(int64(0), nil).Once()
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{InstanceID: instanceID, WorkerID: tournamentSourceID(1206)})
	require.NoError(t, err)
	lifecycle := newRecordingParticipantLifecycle()
	lifecycle.connectHook = func() { connected.Store(true) }
	server := tournamentWebSocketTestServerWithPlayerReader(
		t,
		players,
		tournamentID,
		playerID,
		WithRealtimeDelivery(delivery),
		WithTournamentParticipantFlow(statefulTournamentParticipantFlow{
			before: before, after: after, connected: connected,
		}),
		WithTournamentParticipantLifecycleFlow(lifecycle),
	)
	httpServer := httptestServer(t, server)
	connection := dialTournamentParticipant(t, httpServer.URL, tournamentID, token)
	t.Cleanup(func() { _ = connection.CloseNow() })

	connect := lifecycle.waitConnect(t)
	require.Equal(t, usecase.TournamentParticipantConnectionCommand{
		TournamentID:         tournamentID,
		PlayerID:             playerID,
		ConnectionID:         connect.ConnectionID,
		ConnectionGeneration: connectionGeneration,
	}, connect)

	_, data, err := connection.Read(t.Context())
	require.NoError(t, err)
	message, err := DecodeTournamentParticipantMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Participant)
	require.True(t, connected.Load())
	require.Equal(t, after.Envelope.ProjectionRevision, message.Participant.Envelope.ProjectionRevision)
	require.Equal(t, after.Envelope.Participant.Revision, message.Participant.Envelope.Participant.Revision)

	require.NoError(t, connection.CloseNow())
	disconnect := lifecycle.waitDisconnect(t)
	require.Equal(t, connect, disconnect)
}

func TestParticipantLifecycleDisconnectsWhenInitialSnapshotFailsAfterConnect(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(1211)
	playerID := tournamentSourceID(1212)
	token := tournamentSourceID(1213)
	subscriberID := tournamentSourceID(1214)
	instanceID := tournamentSourceID(1215)
	player := participantSessionPlayer(playerID, token, time.Now().Add(time.Hour))
	players := middlewaremocks.NewMockPlayerSessionReader(t)
	players.EXPECT().GetBySessionToken(mock.Anything, token).Return(player, nil).Maybe()
	repository := participantConnectionRepository(t, tournamentID, playerID, subscriberID, 3)
	repository.EXPECT().Cursor(mock.Anything, tournamentID).Return(int64(0), nil).Once()
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{InstanceID: instanceID, WorkerID: tournamentSourceID(1216)})
	require.NoError(t, err)
	lifecycle := newRecordingParticipantLifecycle()
	server := tournamentWebSocketTestServerWithPlayerReader(
		t,
		players,
		tournamentID,
		playerID,
		WithRealtimeDelivery(delivery),
		WithTournamentParticipantFlow(failingParticipantFlow{err: errors.New("snapshot unavailable")}),
		WithTournamentParticipantLifecycleFlow(lifecycle),
	)
	httpServer := httptestServer(t, server)
	connection := dialTournamentParticipant(t, httpServer.URL, tournamentID, token)
	t.Cleanup(func() { _ = connection.CloseNow() })

	_, data, err := connection.Read(t.Context())
	require.NoError(t, err)
	message, err := DecodeTournamentParticipantMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Rejected)
	connect := lifecycle.waitConnect(t)
	disconnect := lifecycle.waitDisconnect(t)
	require.Equal(t, connect, disconnect)
	require.Equal(t, 1, lifecycle.connectCount())
	require.Equal(t, 1, lifecycle.disconnectCount())
}

func TestParticipantLifecyclePropagatesDurableConnectionGeneration(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(1221)
	playerID := tournamentSourceID(1222)
	connectionID := tournamentSourceID(1223)
	server := NewServer(nil, WithTournamentParticipantLifecycleFlow(newRecordingParticipantLifecycle()))
	lifecycle := server.participantLifecycle.(*recordingParticipantLifecycle)
	session := &realtimeDeliverySession{subscriber: eventdelivery.Subscriber{
		ConnectionID:         connectionID,
		ConnectionGeneration: 19,
	}}

	command, active, err := server.connectParticipantConnection(
		context.Background(),
		tournamentConnectionScope{Role: TournamentRoleParticipant, TournamentID: tournamentID},
		tournamentConnectionPrincipal{Player: &domain.Player{ID: playerID}},
		session,
	)
	require.NoError(t, err)
	require.True(t, active)
	require.Equal(t, connectionID, command.ConnectionID)
	require.Equal(t, int64(19), command.ConnectionGeneration)
	require.Equal(t, command, lifecycle.waitConnect(t))
}

func TestParticipantLifecycleDisconnectUsesDetachedBoundedContext(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	cancel()
	lifecycle := newRecordingParticipantLifecycle()
	server := NewServer(nil, WithTournamentParticipantLifecycleFlow(lifecycle))
	command := usecase.TournamentParticipantConnectionCommand{
		TournamentID:         tournamentSourceID(1231),
		PlayerID:             tournamentSourceID(1232),
		ConnectionID:         tournamentSourceID(1233),
		ConnectionGeneration: 5,
	}

	server.closeParticipantConnection(parent, command)
	require.Equal(t, command, lifecycle.waitDisconnect(t))
	require.NoError(t, lifecycle.disconnectErr())
	require.True(t, lifecycle.disconnectHasDeadline())
	require.Greater(t, lifecycle.disconnectDeadlineRemaining(), time.Duration(0))
}

func TestParticipantLifecycleDoesNotUseGoldenConnectionFlow(t *testing.T) {
	t.Parallel()

	lifecycle := newRecordingParticipantLifecycle()
	golden := &recordingGoldenConnection{}
	server := NewServer(
		nil,
		WithTournamentParticipantLifecycleFlow(lifecycle),
		WithGoldenConnectionFlow(golden),
	)

	_, active, err := server.connectParticipantConnection(
		context.Background(),
		tournamentConnectionScope{Role: TournamentRoleParticipant, TournamentID: tournamentSourceID(1241)},
		tournamentConnectionPrincipal{Player: &domain.Player{ID: tournamentSourceID(1242)}},
		&realtimeDeliverySession{subscriber: eventdelivery.Subscriber{
			ConnectionID:         tournamentSourceID(1243),
			ConnectionGeneration: 1,
		}},
	)
	require.NoError(t, err)
	require.True(t, active)
	require.Zero(t, golden.calls)
	require.Len(t, lifecycle.connectCommands(), 1)
}

func participantConnectionRepository(
	t *testing.T,
	tournamentID uuid.UUID,
	playerID uuid.UUID,
	subscriberID uuid.UUID,
	connectionGeneration int64,
) *eventdeliverymocks.MockSubscriptionRepository {
	t.Helper()
	repository := eventdeliverymocks.NewMockSubscriptionRepository(t)
	repository.EXPECT().OpenSubscription(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, request eventdelivery.SubscriptionOpenRequest) (eventdelivery.Subscription, error) {
			subscription := eventdelivery.Subscription{
				Subscriber: eventdelivery.Subscriber{
					ID:                   subscriberID,
					InstanceID:           request.InstanceID,
					ConnectionID:         request.ConnectionID,
					ConnectionGeneration: connectionGeneration,
					TournamentID:         tournamentID,
					Audience:             request.Audience,
					PrincipalID:          playerID,
					AfterSequence:        request.AfterSequence,
					ConnectedAt:          request.OpenedAt,
				},
				TerminalState: eventdelivery.TerminalReceiptNone,
			}
			return subscription, nil
		}).Once()
	return repository
}

type failingParticipantFlow struct {
	err error
}

func (flow failingParticipantFlow) OpenTournamentParticipant(context.Context, TournamentParticipantConnectionRequest) (TournamentParticipantPayload, error) {
	return TournamentParticipantPayload{}, flow.err
}

type statefulTournamentParticipantFlow struct {
	before    TournamentParticipantPayload
	after     TournamentParticipantPayload
	connected *atomic.Bool
}

func (flow statefulTournamentParticipantFlow) OpenTournamentParticipant(context.Context, TournamentParticipantConnectionRequest) (TournamentParticipantPayload, error) {
	if flow.connected != nil && flow.connected.Load() {
		return flow.after, nil
	}
	return flow.before, nil
}

type recordingParticipantLifecycle struct {
	mu                  sync.Mutex
	connects            []usecase.TournamentParticipantConnectionCommand
	disconnects         []usecase.TournamentParticipantConnectionCommand
	connectSignal       chan struct{}
	disconnectSignal    chan struct{}
	connectHook         func()
	connectOnce         sync.Once
	disconnectOnce      sync.Once
	disconnectErrValue  error
	disconnectDeadline  bool
	disconnectRemaining time.Duration
}

func newRecordingParticipantLifecycle() *recordingParticipantLifecycle {
	return &recordingParticipantLifecycle{
		connectSignal:    make(chan struct{}),
		disconnectSignal: make(chan struct{}),
	}
}

func (flow *recordingParticipantLifecycle) Connect(_ context.Context, command usecase.TournamentParticipantConnectionCommand) error {
	flow.mu.Lock()
	flow.connects = append(flow.connects, command)
	flow.mu.Unlock()
	if flow.connectHook != nil {
		flow.connectHook()
	}
	flow.connectOnce.Do(func() { close(flow.connectSignal) })
	return nil
}

func (flow *recordingParticipantLifecycle) Disconnect(ctx context.Context, command usecase.TournamentParticipantConnectionCommand) error {
	flow.mu.Lock()
	flow.disconnects = append(flow.disconnects, command)
	flow.disconnectErrValue = ctx.Err()
	_, flow.disconnectDeadline = ctx.Deadline()
	if flow.disconnectDeadline {
		deadline, _ := ctx.Deadline()
		flow.disconnectRemaining = time.Until(deadline)
	}
	flow.mu.Unlock()
	flow.disconnectOnce.Do(func() { close(flow.disconnectSignal) })
	return nil
}

func (flow *recordingParticipantLifecycle) waitConnect(t *testing.T) usecase.TournamentParticipantConnectionCommand {
	t.Helper()
	select {
	case <-flow.connectSignal:
	case <-time.After(time.Second):
		t.Fatal("participant lifecycle connect was not called")
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	return flow.connects[0]
}

func (flow *recordingParticipantLifecycle) waitDisconnect(t *testing.T) usecase.TournamentParticipantConnectionCommand {
	t.Helper()
	select {
	case <-flow.disconnectSignal:
	case <-time.After(time.Second):
		t.Fatal("participant lifecycle disconnect was not called")
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	return flow.disconnects[0]
}

func (flow *recordingParticipantLifecycle) connectCount() int {
	flow.mu.Lock()
	defer flow.mu.Unlock()
	return len(flow.connects)
}

func (flow *recordingParticipantLifecycle) disconnectCount() int {
	flow.mu.Lock()
	defer flow.mu.Unlock()
	return len(flow.disconnects)
}

func (flow *recordingParticipantLifecycle) connectCommands() []usecase.TournamentParticipantConnectionCommand {
	flow.mu.Lock()
	defer flow.mu.Unlock()
	return append([]usecase.TournamentParticipantConnectionCommand(nil), flow.connects...)
}

func (flow *recordingParticipantLifecycle) disconnectErr() error {
	flow.mu.Lock()
	defer flow.mu.Unlock()
	return flow.disconnectErrValue
}

func (flow *recordingParticipantLifecycle) disconnectHasDeadline() bool {
	flow.mu.Lock()
	defer flow.mu.Unlock()
	return flow.disconnectDeadline
}

func (flow *recordingParticipantLifecycle) disconnectDeadlineRemaining() time.Duration {
	flow.mu.Lock()
	defer flow.mu.Unlock()
	return flow.disconnectRemaining
}

type recordingGoldenConnection struct {
	calls int
}

func (flow *recordingGoldenConnection) SetConnected(context.Context, usecase.GoldenConnectionCommand) error {
	flow.calls++
	return nil
}

func httptestServer(t *testing.T, server *Server) *httptest.Server {
	t.Helper()
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return httpServer
}

var _ TournamentParticipantLifecycleFlow = (*recordingParticipantLifecycle)(nil)
var _ GoldenConnectionFlow = (*recordingGoldenConnection)(nil)
