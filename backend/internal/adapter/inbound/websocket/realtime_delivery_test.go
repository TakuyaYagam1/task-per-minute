package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	eventdelivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
	eventdeliverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery/mocks"
)

func TestRealtimeDeliveryHealthTracksHeartbeatFailureAndStaleness(t *testing.T) {
	t.Parallel()

	at := tournamentSourceTime().Add(5 * time.Minute)
	delivery, err := NewRealtimeDelivery(
		newSubscriptionRepository(t, tournamentSourceID(940)),
		RealtimeDeliveryConfig{
			InstanceID: tournamentSourceID(941),
			WorkerID:   tournamentSourceID(942),
			StaleAfter: time.Second,
		},
	)
	require.NoError(t, err)

	delivery.markWorkerStarted(at)
	delivery.recordWorkerAttempt(at, nil)
	healthy := delivery.Health(at.Add(time.Second))
	require.True(t, healthy.Started)
	require.True(t, healthy.Running)
	require.False(t, healthy.Stale)
	require.Equal(t, at, *healthy.LastSuccessAt)
	require.Zero(t, healthy.ConsecutiveFailures)

	stale := delivery.Health(at.Add(time.Second + time.Nanosecond))
	require.True(t, stale.Stale)

	delivery.recordWorkerAttempt(at.Add(2*time.Second), errors.New("realtime repository unavailable"))
	failed := delivery.Health(at.Add(2 * time.Second))
	require.Equal(t, int64(1), failed.ConsecutiveFailures)
	require.Equal(t, at.Add(2*time.Second), *failed.LastFailureAt)

	delivery.recordWorkerAttempt(at.Add(3*time.Second), nil)
	recovered := delivery.Health(at.Add(3 * time.Second))
	require.Zero(t, recovered.ConsecutiveFailures)
	require.Equal(t, at.Add(3*time.Second), *recovered.LastSuccessAt)

	delivery.markWorkerStopped()
	stopped := delivery.Health(at.Add(2 * time.Second))
	require.False(t, stopped.Running)
	require.True(t, delivery.Health(time.Time{}).Stale)
}

func TestRealtimeDeliveryWritesAndAcknowledgesPublicSession(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(951)
	playerID := tournamentSourceID(952)
	subscriberID := tournamentSourceID(953)
	instanceID := tournamentSourceID(954)
	workerID := tournamentSourceID(955)
	claimToken := tournamentSourceID(956)
	at := tournamentSourceTime().Add(time.Minute)
	event := publicRealtimeEvent(tournamentID, at)
	subscriber := eventdelivery.Subscriber{
		ID: subscriberID, InstanceID: instanceID, TournamentID: tournamentID,
		Audience: eventdelivery.AudiencePublic, AfterSequence: 1, ConnectedAt: at,
	}
	repository := newSubscriptionRepository(t, subscriberID)
	sessionReady := make(chan struct{})
	repository.EXPECT().Cursor(mock.Anything, tournamentID).Return(int64(1), nil).Once()
	repository.EXPECT().ListAfter(mock.Anything, eventdelivery.ReplayRequest{
		TournamentID: tournamentID, AfterSequence: 1, Limit: 8,
		Audience: eventdelivery.AudiencePublic,
	}).Run(func(context.Context, eventdelivery.ReplayRequest) { close(sessionReady) }).
		Return([]eventdelivery.Event{}, nil).Once()
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: subscriber, Event: event, WorkerID: workerID, ClaimToken: claimToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).Return(true, nil).Once()
	repository.EXPECT().AcknowledgeDelivery(mock.Anything, realtimeDeliveryAcknowledgement(eventdelivery.DeliveryAcknowledgement{
		SubscriberID: subscriberID, EventID: event.ID, WorkerID: workerID,
		ClaimToken: claimToken, Sequence: event.Sequence, AcknowledgedAt: at,
	})).Return(true, nil).Once()
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: workerID, LeaseDuration: time.Second,
		RetryDelay: time.Millisecond, ReplayBatch: 8, Now: func() time.Time { return at },
		NewToken: func() uuid.UUID { return claimToken },
	})
	require.NoError(t, err)
	server := tournamentWebSocketTestServer(t, tournamentID, playerID, WithRealtimeDelivery(delivery))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	connection, response, err := coderws.Dial(
		t.Context(),
		tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID),
		nil,
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	_, initialData, err := connection.Read(t.Context())
	require.NoError(t, err)
	initial, err := DecodeTournamentPublicMessage(initialData)
	require.NoError(t, err)
	require.NotNil(t, initial.Public)
	require.Equal(t, int64(1), initial.Public.Envelope.Sequence)
	awaitRealtimeSignal(t, sessionReady)

	require.NoError(t, delivery.Deliver(t.Context(), event))
	_, data, err := connection.Read(t.Context())
	require.NoError(t, err)
	message, err := DecodeTournamentPublicMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Public)
	require.Equal(t, event.ID, message.Public.Envelope.EventID)
	require.Equal(t, event.Sequence, message.Public.Envelope.Sequence)
	require.Equal(t, event.Sequence, message.Public.Envelope.Public.LastSequence)
	require.Equal(t, event.OccurredAt, message.Public.Envelope.OccurredAt)

	require.NoError(t, connection.CloseNow())
}

func TestRealtimeDeliveryInitialWatermarkZeroReplaysFirstEvent(t *testing.T) {
	tournamentID := tournamentSourceID(964)
	playerID := tournamentSourceID(965)
	subscriberID := tournamentSourceID(966)
	instanceID := tournamentSourceID(967)
	workerID := tournamentSourceID(968)
	claimToken := tournamentSourceID(969)
	at := tournamentSourceTime().Add(90 * time.Second)
	event := publicRealtimeEvent(tournamentID, at)
	event.ID = tournamentSourceID(970)
	event.Sequence = 1
	subscriber := eventdelivery.Subscriber{
		ID: subscriberID, InstanceID: instanceID, TournamentID: tournamentID,
		Audience: eventdelivery.AudiencePublic, AfterSequence: 0, ConnectedAt: at,
	}
	repository := newSubscriptionRepository(t, subscriberID)
	repository.EXPECT().Cursor(mock.Anything, tournamentID).Return(int64(0), nil).Once()
	repository.EXPECT().ListAfter(mock.Anything, eventdelivery.ReplayRequest{
		TournamentID: tournamentID, AfterSequence: 0, Limit: 8, Audience: eventdelivery.AudiencePublic,
	}).Return([]eventdelivery.Event{event}, nil).Once()
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: subscriber, Event: event, WorkerID: workerID, ClaimToken: claimToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).Return(true, nil).Once()
	acknowledged := make(chan struct{})
	repository.EXPECT().AcknowledgeDelivery(mock.Anything, realtimeDeliveryAcknowledgement(eventdelivery.DeliveryAcknowledgement{
		SubscriberID: subscriberID, EventID: event.ID, WorkerID: workerID, ClaimToken: claimToken,
		Sequence: event.Sequence, AcknowledgedAt: at,
	})).Run(func(context.Context, eventdelivery.DeliveryAcknowledgement) {
		close(acknowledged)
	}).Return(true, nil).Once()
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: workerID, LeaseDuration: time.Second, RetryDelay: time.Millisecond,
		ReplayBatch: 8, Now: func() time.Time { return at },
		NewToken: func() uuid.UUID { return claimToken },
	})
	require.NoError(t, err)
	server := tournamentWebSocketTestServer(t, tournamentID, playerID, WithRealtimeDelivery(delivery))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	connection, response, err := coderws.Dial(
		t.Context(), tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID), nil,
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	_, initialData, err := connection.Read(t.Context())
	require.NoError(t, err)
	initial, err := DecodeTournamentPublicMessage(initialData)
	require.NoError(t, err)
	require.NotNil(t, initial.Public)
	require.Equal(t, int64(0), initial.Public.Envelope.Sequence)
	require.Equal(t, int64(0), initial.Public.Envelope.Public.LastSequence)
	_, eventData, err := connection.Read(t.Context())
	require.NoError(t, err)
	replayed, err := DecodeTournamentPublicMessage(eventData)
	require.NoError(t, err)
	require.NotNil(t, replayed.Public)
	require.Equal(t, event.ID, replayed.Public.Envelope.EventID)
	require.Equal(t, int64(1), replayed.Public.Envelope.Sequence)
	awaitRealtimeSignal(t, acknowledged)
	require.NoError(t, connection.CloseNow())
}

func TestRealtimeDeliveryWritesPendingTerminalAfterInitialSnapshot(t *testing.T) {
	tournamentID := tournamentSourceID(1004)
	playerID := tournamentSourceID(1005)
	subscriberID := tournamentSourceID(1006)
	instanceID := tournamentSourceID(1007)
	connectionID := tournamentSourceID(1008)
	workerID := tournamentSourceID(1009)
	claimToken := tournamentSourceID(1010)
	at := tournamentSourceTime().Add(9 * time.Minute)
	terminal := publicRealtimeEvent(tournamentID, at)
	terminal.ID = subscriberID
	terminal.Sequence = 3
	terminal.Terminal = true
	var opened eventdelivery.SubscriptionOpenRequest
	repository := eventdeliverymocks.NewMockSubscriptionRepository(t)
	repository.EXPECT().OpenSubscription(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, request eventdelivery.SubscriptionOpenRequest) (eventdelivery.Subscription, error) {
			opened = request
			subscriber := eventdelivery.Subscriber{
				ID: subscriberID, InstanceID: request.InstanceID, ConnectionID: request.ConnectionID,
				ConnectionGeneration: 1,
				TournamentID:         request.TournamentID, Audience: request.Audience, PrincipalID: request.PrincipalID,
				AfterSequence: request.AfterSequence, ConnectedAt: request.OpenedAt,
			}
			return eventdelivery.Subscription{
				Subscriber: subscriber, TerminalState: eventdelivery.TerminalReceiptPending, PendingTerminal: &terminal,
			}, nil
		}).Once()
	repository.EXPECT().Cursor(mock.Anything, tournamentID).Return(int64(3), nil).Once()
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: eventdelivery.Subscriber{
			ID: subscriberID, InstanceID: instanceID, TournamentID: tournamentID,
			Audience: eventdelivery.AudiencePublic, AfterSequence: 3, ConnectedAt: at,
		}, Event: terminal, WorkerID: workerID, ClaimToken: claimToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).Return(true, nil).Once()
	repository.EXPECT().AcknowledgeDelivery(mock.Anything, realtimeDeliveryAcknowledgement(eventdelivery.DeliveryAcknowledgement{
		SubscriberID: subscriberID, EventID: terminal.ID, WorkerID: workerID, ClaimToken: claimToken,
		Sequence: terminal.Sequence, AcknowledgedAt: at,
	})).Return(true, nil).Once()
	repository.EXPECT().CloseSubscriber(mock.Anything, realtimeSubscriberClose(eventdelivery.SubscriberClose{
		SubscriberID: subscriberID, InstanceID: instanceID, ClosedAt: at, Reason: "tournament_terminal",
	})).Return(nil).Once()
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: workerID, LeaseDuration: time.Second, RetryDelay: time.Millisecond,
		Now: func() time.Time { return at }, NewConnectionID: func() uuid.UUID { return connectionID },
		NewToken: func() uuid.UUID { return claimToken },
	})
	require.NoError(t, err)
	server := tournamentWebSocketTestServer(t, tournamentID, playerID, WithRealtimeDelivery(delivery))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	connection, response, err := coderws.Dial(
		t.Context(), tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID), nil,
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	_, initialData, err := connection.Read(t.Context())
	require.NoError(t, err)
	initial, err := DecodeTournamentPublicMessage(initialData)
	require.NoError(t, err)
	require.NotNil(t, initial.Public)
	require.Equal(t, int64(3), initial.Public.Envelope.Sequence)
	require.NotNil(t, initial.Public.Envelope.ResumeID)
	require.Equal(t, subscriberID, *initial.Public.Envelope.ResumeID)
	require.Equal(t, uuid.Nil, opened.ResumeID)
	_, terminalData, err := connection.Read(t.Context())
	require.NoError(t, err)
	message, err := DecodeTournamentPublicMessage(terminalData)
	require.NoError(t, err)
	require.NotNil(t, message.Terminal)
	require.Equal(t, terminal.ID, message.Terminal.EventID)
	_, _, err = connection.Read(t.Context())
	require.Error(t, err)
}

func TestRealtimeDeliveryWrittenTerminalClosesAfterInitialSnapshot(t *testing.T) {
	tournamentID := tournamentSourceID(1011)
	playerID := tournamentSourceID(1012)
	subscriberID := tournamentSourceID(1013)
	instanceID := tournamentSourceID(1014)
	connectionID := tournamentSourceID(1015)
	at := tournamentSourceTime().Add(10 * time.Minute)
	repository := eventdeliverymocks.NewMockSubscriptionRepository(t)
	repository.EXPECT().OpenSubscription(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, request eventdelivery.SubscriptionOpenRequest) (eventdelivery.Subscription, error) {
			return eventdelivery.Subscription{
				Subscriber: eventdelivery.Subscriber{
					ID: subscriberID, InstanceID: request.InstanceID, ConnectionID: request.ConnectionID,
					ConnectionGeneration: 1,
					TournamentID:         request.TournamentID, Audience: request.Audience, PrincipalID: request.PrincipalID,
					AfterSequence: request.AfterSequence, ConnectedAt: request.OpenedAt,
				},
				TerminalState: eventdelivery.TerminalReceiptWritten,
			}, nil
		}).Once()
	repository.EXPECT().Cursor(mock.Anything, tournamentID).Return(int64(3), nil).Once()
	repository.EXPECT().CloseSubscriber(mock.Anything, realtimeSubscriberClose(eventdelivery.SubscriberClose{
		SubscriberID: subscriberID, InstanceID: instanceID, ClosedAt: at, Reason: "terminal_already_written",
	})).Return(nil).Once()
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: tournamentSourceID(1016), Now: func() time.Time { return at },
		NewConnectionID: func() uuid.UUID { return connectionID },
	})
	require.NoError(t, err)
	server := tournamentWebSocketTestServer(t, tournamentID, playerID, WithRealtimeDelivery(delivery))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	connection, response, err := coderws.Dial(
		t.Context(), tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID), nil,
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	_, initialData, err := connection.Read(t.Context())
	require.NoError(t, err)
	initial, err := DecodeTournamentPublicMessage(initialData)
	require.NoError(t, err)
	require.NotNil(t, initial.Public)
	require.Equal(t, int64(3), initial.Public.Envelope.Sequence)
	require.NotNil(t, initial.Public.Envelope.ResumeID)
	require.Equal(t, subscriberID, *initial.Public.Envelope.ResumeID)
	_, _, err = connection.Read(t.Context())
	require.Error(t, err)
}

func TestRealtimeDeliveryWorkerCatchesUpSessionOnEveryReplica(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(981)
	playerID := tournamentSourceID(982)
	subscriberID := tournamentSourceID(983)
	instanceID := tournamentSourceID(984)
	workerID := tournamentSourceID(985)
	claimToken := tournamentSourceID(986)
	at := tournamentSourceTime().Add(3 * time.Minute)
	event := publicRealtimeEvent(tournamentID, at)
	event.ID = tournamentSourceID(987)
	subscriber := eventdelivery.Subscriber{
		ID: subscriberID, InstanceID: instanceID, TournamentID: tournamentID,
		Audience: eventdelivery.AudiencePublic, AfterSequence: 1, ConnectedAt: at,
	}
	repository := newSubscriptionRepository(t, subscriberID)
	initialCatchUp := make(chan struct{})
	dependencyFailed := make(chan struct{})
	allowRecovery := make(chan struct{})
	repository.EXPECT().Cursor(mock.Anything, tournamentID).Return(int64(1), nil).Once()
	repository.EXPECT().ListAfter(mock.Anything, eventdelivery.ReplayRequest{
		TournamentID: tournamentID, AfterSequence: 1, Limit: 8,
		Audience: eventdelivery.AudiencePublic,
	}).Run(func(context.Context, eventdelivery.ReplayRequest) { close(initialCatchUp) }).
		Return([]eventdelivery.Event{}, nil).Once()
	repository.EXPECT().ListAfter(mock.Anything, eventdelivery.ReplayRequest{
		TournamentID: tournamentID, AfterSequence: 1, Limit: 8,
		Audience: eventdelivery.AudiencePublic,
	}).Run(func(context.Context, eventdelivery.ReplayRequest) { close(dependencyFailed) }).
		Return(nil, errors.New("temporary projection dependency failure")).Once()
	repository.EXPECT().ListAfter(mock.Anything, eventdelivery.ReplayRequest{
		TournamentID: tournamentID, AfterSequence: 1, Limit: 8,
		Audience: eventdelivery.AudiencePublic,
	}).Run(func(context.Context, eventdelivery.ReplayRequest) { <-allowRecovery }).
		Return([]eventdelivery.Event{event}, nil).Once()
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: subscriber, Event: event, WorkerID: workerID, ClaimToken: claimToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).Return(true, nil).Once()
	repository.EXPECT().AcknowledgeDelivery(mock.Anything, realtimeDeliveryAcknowledgement(eventdelivery.DeliveryAcknowledgement{
		SubscriberID: subscriberID, EventID: event.ID, WorkerID: workerID,
		ClaimToken: claimToken, Sequence: event.Sequence, AcknowledgedAt: at,
	})).Return(true, nil).Once()
	repository.EXPECT().ListAfter(mock.Anything, eventdelivery.ReplayRequest{
		TournamentID: tournamentID, AfterSequence: event.Sequence, Limit: 8,
		Audience: eventdelivery.AudiencePublic,
	}).Return([]eventdelivery.Event{}, nil).Maybe()

	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: workerID, LeaseDuration: time.Second,
		RetryDelay: time.Millisecond, PollInterval: time.Millisecond, ReplayBatch: 8,
		Now:      func() time.Time { return at },
		NewToken: func() uuid.UUID { return claimToken },
	})
	require.NoError(t, err)
	server := tournamentWebSocketTestServer(t, tournamentID, playerID, WithRealtimeDelivery(delivery))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	connection, response, err := coderws.Dial(
		t.Context(),
		tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID),
		nil,
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	_, _, err = connection.Read(t.Context())
	require.NoError(t, err)
	awaitRealtimeSignal(t, initialCatchUp)

	workerCtx, cancelWorker := context.WithCancel(t.Context())
	workerDone := make(chan error, 1)
	go func() { workerDone <- delivery.Run(workerCtx) }()
	awaitRealtimeSignal(t, dependencyFailed)
	require.Eventually(t, func() bool {
		health := delivery.Health(at)
		return health.Running && health.ConsecutiveFailures == 1 && health.LastFailureAt != nil
	}, time.Second, time.Millisecond)
	close(allowRecovery)
	require.Eventually(t, delivery.Ready, time.Second, time.Millisecond)
	require.Zero(t, delivery.Health(at).ConsecutiveFailures)

	_, data, err := connection.Read(t.Context())
	require.NoError(t, err)
	message, err := DecodeTournamentPublicMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Public)
	require.Equal(t, event.ID, message.Public.Envelope.EventID)
	require.Equal(t, event.Sequence, message.Public.Envelope.Sequence)

	cancelWorker()
	require.NoError(t, <-workerDone)
	require.False(t, delivery.Ready())
	require.NoError(t, connection.CloseNow())
}

func TestRealtimeDeliveryWritesTerminalEventAndClosesSession(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(991)
	playerID := tournamentSourceID(992)
	subscriberID := tournamentSourceID(993)
	instanceID := tournamentSourceID(994)
	workerID := tournamentSourceID(995)
	claimToken := tournamentSourceID(996)
	at := tournamentSourceTime().Add(4 * time.Minute)
	event := terminalRealtimeEvent(tournamentID, tournamentSourceID(997), 2, at)
	subscriber := eventdelivery.Subscriber{
		ID: subscriberID, InstanceID: instanceID, TournamentID: tournamentID,
		Audience: eventdelivery.AudiencePublic, AfterSequence: 1, ConnectedAt: at,
	}
	repository := newSubscriptionRepository(t, subscriberID)
	sessionReady := make(chan struct{})
	sessionClosed := make(chan struct{})
	repository.EXPECT().Cursor(mock.Anything, tournamentID).Return(int64(1), nil).Once()
	repository.EXPECT().ListAfter(mock.Anything, eventdelivery.ReplayRequest{
		TournamentID: tournamentID, AfterSequence: 1, Limit: 8,
		Audience: eventdelivery.AudiencePublic,
	}).Run(func(context.Context, eventdelivery.ReplayRequest) { close(sessionReady) }).
		Return([]eventdelivery.Event{}, nil).Once()
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: subscriber, Event: event, WorkerID: workerID, ClaimToken: claimToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).Return(true, nil).Once()
	repository.EXPECT().AcknowledgeDelivery(mock.Anything, realtimeDeliveryAcknowledgement(eventdelivery.DeliveryAcknowledgement{
		SubscriberID: subscriberID, EventID: event.ID, WorkerID: workerID,
		ClaimToken: claimToken, Sequence: event.Sequence, AcknowledgedAt: at,
	})).Return(true, nil).Once()
	repository.EXPECT().CloseSubscriber(mock.Anything, realtimeSubscriberClose(eventdelivery.SubscriberClose{
		SubscriberID: subscriberID, InstanceID: instanceID, ClosedAt: at,
		Reason: "tournament_terminal",
	})).Run(func(context.Context, eventdelivery.SubscriberClose) { close(sessionClosed) }).Return(nil).Once()

	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: workerID, LeaseDuration: time.Second,
		RetryDelay: time.Millisecond, ReplayBatch: 8, Now: func() time.Time { return at },
		NewToken: func() uuid.UUID { return claimToken },
	})
	require.NoError(t, err)
	server := tournamentWebSocketTestServer(t, tournamentID, playerID, WithRealtimeDelivery(delivery))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	connection, response, err := coderws.Dial(
		t.Context(),
		tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID),
		nil,
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	_, _, err = connection.Read(t.Context())
	require.NoError(t, err)
	awaitRealtimeSignal(t, sessionReady)

	require.NoError(t, delivery.Deliver(t.Context(), event))
	_, snapshotData, err := connection.Read(t.Context())
	require.NoError(t, err)
	snapshot, err := DecodeTournamentPublicMessage(snapshotData)
	require.NoError(t, err)
	require.NotNil(t, snapshot.Public)
	require.Equal(t, event.ID, snapshot.Public.Envelope.EventID)
	require.Equal(t, event.Sequence, snapshot.Public.Envelope.Sequence)

	_, terminalData, err := connection.Read(t.Context())
	require.NoError(t, err)
	terminal, err := DecodeTournamentPublicMessage(terminalData)
	require.NoError(t, err)
	require.NotNil(t, terminal.Terminal)
	require.Equal(t, domain.TournamentStateCancelled, terminal.Terminal.State)
	require.Equal(t, event.ID, terminal.Terminal.EventID)
	awaitRealtimeSignal(t, sessionClosed)
	_, _, err = connection.Read(t.Context())
	require.Error(t, err)
}

func TestRealtimeDeliveryWritesFinalSnapshotThenTerminalEventAndClosesSession(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(931)
	playerID := tournamentSourceID(932)
	subscriberID := tournamentSourceID(933)
	instanceID := tournamentSourceID(934)
	workerID := tournamentSourceID(935)
	claimToken := tournamentSourceID(936)
	at := tournamentSourceTime().Add(5 * time.Minute)
	event := publicRealtimeEvent(tournamentID, at)
	event.ID = tournamentSourceID(937)
	event.Terminal = true
	subscriber := eventdelivery.Subscriber{
		ID: subscriberID, InstanceID: instanceID, TournamentID: tournamentID,
		Audience: eventdelivery.AudiencePublic, AfterSequence: 1, ConnectedAt: at,
	}
	repository := newSubscriptionRepository(t, subscriberID)
	sessionReady := make(chan struct{})
	sessionClosed := make(chan struct{})
	repository.EXPECT().Cursor(mock.Anything, tournamentID).Return(int64(1), nil).Once()
	repository.EXPECT().ListAfter(mock.Anything, eventdelivery.ReplayRequest{
		TournamentID: tournamentID, AfterSequence: 1, Limit: 8,
		Audience: eventdelivery.AudiencePublic,
	}).Run(func(context.Context, eventdelivery.ReplayRequest) { close(sessionReady) }).
		Return([]eventdelivery.Event{}, nil).Once()
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: subscriber, Event: event, WorkerID: workerID, ClaimToken: claimToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).Return(true, nil).Once()
	repository.EXPECT().AcknowledgeDelivery(mock.Anything, realtimeDeliveryAcknowledgement(eventdelivery.DeliveryAcknowledgement{
		SubscriberID: subscriberID, EventID: event.ID, WorkerID: workerID,
		ClaimToken: claimToken, Sequence: event.Sequence, AcknowledgedAt: at,
	})).Return(true, nil).Once()
	repository.EXPECT().CloseSubscriber(mock.Anything, realtimeSubscriberClose(eventdelivery.SubscriberClose{
		SubscriberID: subscriberID, InstanceID: instanceID, ClosedAt: at,
		Reason: "tournament_terminal",
	})).Run(func(context.Context, eventdelivery.SubscriberClose) { close(sessionClosed) }).Return(nil).Once()

	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: workerID, LeaseDuration: time.Second,
		RetryDelay: time.Millisecond, ReplayBatch: 8, Now: func() time.Time { return at },
		NewToken: func() uuid.UUID { return claimToken },
	})
	require.NoError(t, err)
	server := tournamentWebSocketTestServer(t, tournamentID, playerID, WithRealtimeDelivery(delivery))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	connection, response, err := coderws.Dial(
		t.Context(),
		tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID),
		nil,
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	_, _, err = connection.Read(t.Context())
	require.NoError(t, err)
	awaitRealtimeSignal(t, sessionReady)

	require.NoError(t, delivery.Deliver(t.Context(), event))
	_, snapshotData, err := connection.Read(t.Context())
	require.NoError(t, err)
	snapshot, err := DecodeTournamentPublicMessage(snapshotData)
	require.NoError(t, err)
	require.NotNil(t, snapshot.Public)
	require.Equal(t, event.ID, snapshot.Public.Envelope.EventID)
	require.Equal(t, event.Sequence, snapshot.Public.Envelope.Sequence)

	_, terminalData, err := connection.Read(t.Context())
	require.NoError(t, err)
	terminal, err := DecodeTournamentPublicMessage(terminalData)
	require.NoError(t, err)
	require.NotNil(t, terminal.Terminal)
	require.Equal(t, domain.TournamentStateCompleted, terminal.Terminal.State)
	require.Equal(t, event.ID, terminal.Terminal.EventID)
	require.Equal(t, event.Sequence, terminal.Terminal.Sequence)
	awaitRealtimeSignal(t, sessionClosed)
	_, _, err = connection.Read(t.Context())
	require.Error(t, err)
}

func TestRealtimeDeliveryStopsSessionWhenLostClaimHasStaleFence(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(1032)
	subscriberID := tournamentSourceID(1033)
	instanceID := tournamentSourceID(1034)
	connectionID := tournamentSourceID(1035)
	workerID := tournamentSourceID(1036)
	claimToken := tournamentSourceID(1037)
	at := tournamentSourceTime().Add(13 * time.Minute)
	event := publicRealtimeEvent(tournamentID, at)
	subscriber := eventdelivery.Subscriber{
		ID:                   subscriberID,
		InstanceID:           instanceID,
		ConnectionID:         connectionID,
		ConnectionGeneration: 1,
		TournamentID:         tournamentID,
		Audience:             eventdelivery.AudiencePublic,
		AfterSequence:        event.Sequence - 1,
		ConnectedAt:          at,
	}
	repository := eventdeliverymocks.NewMockSubscriptionRepository(t)
	repository.EXPECT().SubscriberCursor(mock.Anything, subscriber).
		Return(int64(0), eventdelivery.ErrSubscriberScope).Once()
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: subscriber, Event: event, WorkerID: workerID, ClaimToken: claimToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).Return(false, nil).Once()
	connection := &recordingRealtimeSocket{}
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: workerID, LeaseDuration: time.Second,
		Now: func() time.Time { return at }, NewToken: func() uuid.UUID { return claimToken },
	})
	require.NoError(t, err)
	session := &realtimeDeliverySession{
		subscriber: subscriber, connection: connection,
		scope:  tournamentWriteScope{Role: TournamentRolePublic, TournamentID: tournamentID},
		render: func(context.Context, eventdelivery.Event) ([]byte, error) { return nil, nil },
	}
	session.lastSequence.Store(subscriber.AfterSequence)
	delivery.sessions[subscriber.ID] = session

	require.NoError(t, delivery.deliverSession(t.Context(), session, event))
	require.False(t, delivery.sessionActive(session))
	require.Equal(t, 1, connection.closeCount)
}

func TestRealtimeDeliveryCatchUpClosesStaleFenceOnEmptyReplay(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(1038)
	subscriberID := tournamentSourceID(1039)
	instanceID := tournamentSourceID(1040)
	connectionID := tournamentSourceID(1041)
	at := tournamentSourceTime().Add(14 * time.Minute)
	subscriber := eventdelivery.Subscriber{
		ID:                   subscriberID,
		InstanceID:           instanceID,
		ConnectionID:         connectionID,
		ConnectionGeneration: 1,
		TournamentID:         tournamentID,
		Audience:             eventdelivery.AudiencePublic,
		AfterSequence:        0,
		ConnectedAt:          at,
	}
	repository := eventdeliverymocks.NewMockSubscriptionRepository(t)
	repository.EXPECT().ListAfter(mock.Anything, mock.Anything).Return([]eventdelivery.Event(nil), nil).Once()
	repository.EXPECT().SubscriberCursor(mock.Anything, subscriber).
		Return(int64(0), eventdelivery.ErrSubscriberScope).Once()
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: tournamentSourceID(1042), Now: func() time.Time { return at },
	})
	require.NoError(t, err)
	connection := &recordingRealtimeSocket{}
	session := &realtimeDeliverySession{
		subscriber: subscriber, connection: connection,
		scope:  tournamentWriteScope{Role: TournamentRolePublic, TournamentID: tournamentID},
		render: func(context.Context, eventdelivery.Event) ([]byte, error) { return nil, nil },
	}
	session.lastSequence.Store(subscriber.AfterSequence)
	delivery.sessions[subscriber.ID] = session

	require.NoError(t, delivery.CatchUp(t.Context(), session))
	require.False(t, delivery.sessionActive(session))
	require.Equal(t, 1, connection.closeCount)
}

func TestRealtimeDeliveryFrameRejectsProjectionRegression(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(961)
	event := publicRealtimeEvent(tournamentID, tournamentSourceTime().Add(time.Minute))
	event.ProjectionRevision++
	_, publicData, _ := tournamentWriteTestFrames(t, tournamentID, tournamentSourceID(962))
	message, err := DecodeTournamentPublicMessage(publicData)
	require.NoError(t, err)
	require.NotNil(t, message.Public)
	payload := *message.Public
	applyPublicDeliveryMetadata(&payload, event.Sequence, &event, uuid.Nil)
	data, err := MarshalTournamentPublic(payload)
	require.NoError(t, err)

	require.ErrorIs(t, validateRealtimeDeliveryFrame(
		tournamentWriteScope{Role: TournamentRolePublic, TournamentID: tournamentID},
		data,
		event,
	), ErrTournamentWriteScope)
}

func TestRealtimeDeliverySerializesCatchUpBeforeLiveEvent(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(971)
	playerID := tournamentSourceID(972)
	subscriberID := tournamentSourceID(973)
	instanceID := tournamentSourceID(974)
	workerID := tournamentSourceID(975)
	firstToken := tournamentSourceID(976)
	secondToken := tournamentSourceID(977)
	at := tournamentSourceTime().Add(2 * time.Minute)
	first := publicRealtimeEvent(tournamentID, at)
	first.ID = tournamentSourceID(978)
	second := first.Clone()
	second.ID = tournamentSourceID(979)
	second.Sequence = first.Sequence + 1
	second.ProjectionOrdinal = first.ProjectionOrdinal + 1
	second.OccurredAt = first.OccurredAt.Add(time.Second)
	subscriber := eventdelivery.Subscriber{
		ID: subscriberID, InstanceID: instanceID, TournamentID: tournamentID,
		Audience: eventdelivery.AudiencePublic, AfterSequence: 1, ConnectedAt: at,
	}
	repository := newSubscriptionRepository(t, subscriberID)
	firstClaimed := make(chan struct{})
	releaseFirst := make(chan struct{})
	repository.EXPECT().Cursor(mock.Anything, tournamentID).Return(int64(1), nil).Once()
	repository.EXPECT().ListAfter(mock.Anything, eventdelivery.ReplayRequest{
		TournamentID: tournamentID, AfterSequence: 1, Limit: 8,
		Audience: eventdelivery.AudiencePublic,
	}).Return([]eventdelivery.Event{first}, nil).Once()
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: subscriber, Event: first, WorkerID: workerID, ClaimToken: firstToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).Run(func(context.Context, eventdelivery.DeliveryClaim) {
		close(firstClaimed)
		<-releaseFirst
	}).Return(true, nil).Once()
	firstAcknowledged := repository.EXPECT().AcknowledgeDelivery(
		mock.Anything,
		realtimeDeliveryAcknowledgement(eventdelivery.DeliveryAcknowledgement{
			SubscriberID: subscriberID, EventID: first.ID, WorkerID: workerID,
			ClaimToken: firstToken, Sequence: first.Sequence, AcknowledgedAt: at,
		}),
	).Return(true, nil).Once()
	secondSubscriber := subscriber
	secondSubscriber.AfterSequence = first.Sequence
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: secondSubscriber, Event: second, WorkerID: workerID, ClaimToken: secondToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).NotBefore(firstAcknowledged).Return(true, nil).Once()
	repository.EXPECT().AcknowledgeDelivery(mock.Anything, realtimeDeliveryAcknowledgement(eventdelivery.DeliveryAcknowledgement{
		SubscriberID: subscriberID, EventID: second.ID, WorkerID: workerID,
		ClaimToken: secondToken, Sequence: second.Sequence, AcknowledgedAt: at,
	})).Return(true, nil).Once()
	var tokenIndex atomic.Int32
	tokens := [...]uuid.UUID{firstToken, secondToken}
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: workerID, LeaseDuration: time.Second,
		RetryDelay: time.Millisecond, ReplayBatch: 8, Now: func() time.Time { return at },
		NewToken: func() uuid.UUID {
			return tokens[tokenIndex.Add(1)-1]
		},
	})
	require.NoError(t, err)
	server := tournamentWebSocketTestServer(t, tournamentID, playerID, WithRealtimeDelivery(delivery))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	connection, response, err := coderws.Dial(
		t.Context(),
		tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID),
		nil,
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	_, _, err = connection.Read(t.Context())
	require.NoError(t, err)
	awaitRealtimeSignal(t, firstClaimed)
	liveDone := make(chan error, 1)
	go func() { liveDone <- delivery.Deliver(t.Context(), second) }()
	close(releaseFirst)

	_, firstData, err := connection.Read(t.Context())
	require.NoError(t, err)
	firstMessage, err := DecodeTournamentPublicMessage(firstData)
	require.NoError(t, err)
	require.NotNil(t, firstMessage.Public)
	require.Equal(t, first.Sequence, firstMessage.Public.Envelope.Sequence)
	_, secondData, err := connection.Read(t.Context())
	require.NoError(t, err)
	secondMessage, err := DecodeTournamentPublicMessage(secondData)
	require.NoError(t, err)
	require.NotNil(t, secondMessage.Public)
	require.Equal(t, second.Sequence, secondMessage.Public.Envelope.Sequence)
	require.NoError(t, <-liveDone)
	require.NoError(t, connection.CloseNow())
}

func TestRealtimeDeliveryKeepsOpaqueSubscriberAcrossRestart(t *testing.T) {
	tournamentID := tournamentSourceID(991)
	stableSubscriberID := tournamentSourceID(992)
	firstConnectionID := tournamentSourceID(993)
	secondConnectionID := tournamentSourceID(994)
	at := tournamentSourceTime().Add(7 * time.Minute)
	requests := make([]eventdelivery.SubscriptionOpenRequest, 0, 2)
	repository := eventdeliverymocks.NewMockSubscriptionRepository(t)
	repository.EXPECT().OpenSubscription(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, request eventdelivery.SubscriptionOpenRequest) (eventdelivery.Subscription, error) {
			requests = append(requests, request)
			if len(requests) == 2 && request.ResumeID != stableSubscriberID {
				return eventdelivery.Subscription{}, eventdelivery.ErrSubscriberScope
			}
			subscriber := eventdelivery.Subscriber{
				ID:                   stableSubscriberID,
				InstanceID:           request.InstanceID,
				ConnectionID:         request.ConnectionID,
				ConnectionGeneration: 1,
				TournamentID:         request.TournamentID,
				Audience:             request.Audience,
				PrincipalID:          request.PrincipalID,
				AfterSequence:        request.AfterSequence,
				ConnectedAt:          request.OpenedAt,
			}
			return eventdelivery.Subscription{
				Subscriber: subscriber, TerminalState: eventdelivery.TerminalReceiptNone,
			}, nil
		}).Twice()
	newDelivery := func(instanceID, connectionID uuid.UUID) *RealtimeDelivery {
		delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
			InstanceID: instanceID, WorkerID: tournamentSourceID(995), Now: func() time.Time { return at },
			NewConnectionID: func() uuid.UUID { return connectionID },
		})
		require.NoError(t, err)
		return delivery
	}
	scope := tournamentWriteScope{Role: TournamentRolePublic, TournamentID: tournamentID}
	render := func(context.Context, eventdelivery.Event) ([]byte, error) { return nil, nil }

	first, err := newDelivery(tournamentSourceID(996), firstConnectionID).openSession(
		t.Context(), tournamentID, eventdelivery.AudiencePublic, uuid.Nil, 0, uuid.Nil,
		&recordingRealtimeSocket{}, scope, render,
	)
	require.NoError(t, err)
	require.Equal(t, stableSubscriberID, first.resumeID())

	second, err := newDelivery(tournamentSourceID(997), secondConnectionID).openSession(
		t.Context(), tournamentID, eventdelivery.AudiencePublic, uuid.Nil, 0, stableSubscriberID,
		&recordingRealtimeSocket{}, scope, render,
	)
	require.NoError(t, err)
	require.Equal(t, stableSubscriberID, second.resumeID())
	require.Equal(t, first.subscriber.ID, second.subscriber.ID)
	require.Len(t, requests, 2)
	require.Equal(t, uuid.Nil, requests[0].ResumeID)
	require.Equal(t, stableSubscriberID, requests[1].ResumeID)
	require.NotEqual(t, requests[0].ConnectionID, requests[1].ConnectionID)
}

func TestRealtimeDeliveryRetriesAfterPartialTerminalWriteWithoutAcknowledgement(t *testing.T) {
	tournamentID := tournamentSourceID(998)
	subscriberID := tournamentSourceID(999)
	instanceID := tournamentSourceID(1000)
	workerID := tournamentSourceID(1001)
	claimToken := tournamentSourceID(1002)
	at := tournamentSourceTime().Add(8 * time.Minute)
	event := publicRealtimeEvent(tournamentID, at)
	event.Terminal = true
	subscriber := eventdelivery.Subscriber{
		ID: subscriberID, InstanceID: instanceID, ConnectionID: tournamentSourceID(1029), ConnectionGeneration: 1,
		TournamentID: tournamentID,
		Audience:     eventdelivery.AudiencePublic, AfterSequence: 1, ConnectedAt: at,
	}
	repository := newSubscriptionRepository(t, subscriberID)
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: subscriber, Event: event, WorkerID: workerID, ClaimToken: claimToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).Return(true, nil).Once()
	repository.EXPECT().RetryDelivery(mock.Anything, realtimeDeliveryRetry(eventdelivery.DeliveryRetry{
		SubscriberID: subscriberID, EventID: event.ID, WorkerID: workerID, ClaimToken: claimToken,
		AvailableAt: at.Add(time.Millisecond), Reason: "write_failed",
	})).Return(true, nil).Once()
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: workerID, LeaseDuration: time.Second, RetryDelay: time.Millisecond,
		Now: func() time.Time { return at }, NewToken: func() uuid.UUID { return claimToken },
	})
	require.NoError(t, err)
	_, snapshotFrame, _ := tournamentWriteTestFrames(t, tournamentID, tournamentSourceID(1003))
	message, err := DecodeTournamentPublicMessage(snapshotFrame)
	require.NoError(t, err)
	require.NotNil(t, message.Public)
	payload := *message.Public
	applyPublicDeliveryMetadata(&payload, event.Sequence, &event, uuid.Nil)
	frame, err := MarshalTournamentPublic(payload)
	require.NoError(t, err)
	connection := &recordingRealtimeSocket{failAtWrite: 2}
	session := &realtimeDeliverySession{
		subscriber: subscriber, connection: connection,
		scope:  tournamentWriteScope{Role: TournamentRolePublic, TournamentID: tournamentID},
		render: func(context.Context, eventdelivery.Event) ([]byte, error) { return frame, nil },
	}
	session.lastSequence.Store(subscriber.AfterSequence)
	delivery.sessions[subscriber.ID] = session

	err = delivery.deliverSession(t.Context(), session, event)
	require.ErrorIs(t, err, ErrRealtimeDeliveryWrite)
	require.Len(t, connection.frames, 1)
	require.Equal(t, 2, connection.writeCount)
}

func TestRealtimeDeliveryDoesNotCloseTerminalBeforeAcknowledgement(t *testing.T) {
	tournamentID := tournamentSourceID(1017)
	subscriberID := tournamentSourceID(1018)
	instanceID := tournamentSourceID(1019)
	workerID := tournamentSourceID(1020)
	claimToken := tournamentSourceID(1021)
	at := tournamentSourceTime().Add(11 * time.Minute)
	event := terminalRealtimeEvent(tournamentID, tournamentSourceID(1022), 2, at)
	subscriber := eventdelivery.Subscriber{
		ID: subscriberID, InstanceID: instanceID, ConnectionID: tournamentSourceID(1030), ConnectionGeneration: 1,
		TournamentID: tournamentID,
		Audience:     eventdelivery.AudiencePublic, AfterSequence: 1, ConnectedAt: at,
	}
	repository := newSubscriptionRepository(t, subscriberID)
	connection := &recordingRealtimeSocket{}
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: subscriber, Event: event, WorkerID: workerID, ClaimToken: claimToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).Return(true, nil).Once()
	repository.EXPECT().AcknowledgeDelivery(mock.Anything, mock.Anything).
		Run(func(context.Context, eventdelivery.DeliveryAcknowledgement) {
			require.Len(t, connection.frames, 2)
		}).Return(false, errors.New("receipt unavailable")).Once()
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: workerID, LeaseDuration: time.Second,
		Now: func() time.Time { return at }, NewToken: func() uuid.UUID { return claimToken },
	})
	require.NoError(t, err)
	frame := terminalProjectionFrame(t, tournamentID, event)
	session := &realtimeDeliverySession{
		subscriber: subscriber, connection: connection,
		scope:  tournamentWriteScope{Role: TournamentRolePublic, TournamentID: tournamentID},
		render: func(context.Context, eventdelivery.Event) ([]byte, error) { return frame, nil },
	}
	session.lastSequence.Store(subscriber.AfterSequence)
	delivery.sessions[subscriber.ID] = session

	err = delivery.deliverSession(t.Context(), session, event)
	require.ErrorIs(t, err, ErrRealtimeDeliveryRepository)
	require.Len(t, connection.frames, 2)
	require.Equal(t, 0, connection.closeCount)
	require.True(t, delivery.sessionActive(session))
}

func TestRealtimeDeliveryClosesSocketWhenTerminalCloseFails(t *testing.T) {
	tournamentID := tournamentSourceID(1023)
	subscriberID := tournamentSourceID(1024)
	instanceID := tournamentSourceID(1025)
	workerID := tournamentSourceID(1026)
	claimToken := tournamentSourceID(1027)
	at := tournamentSourceTime().Add(12 * time.Minute)
	event := terminalRealtimeEvent(tournamentID, tournamentSourceID(1028), 2, at)
	subscriber := eventdelivery.Subscriber{
		ID: subscriberID, InstanceID: instanceID, ConnectionID: tournamentSourceID(1031), ConnectionGeneration: 1,
		TournamentID: tournamentID,
		Audience:     eventdelivery.AudiencePublic, AfterSequence: 1, ConnectedAt: at,
	}
	repository := newSubscriptionRepository(t, subscriberID)
	connection := &recordingRealtimeSocket{}
	repository.EXPECT().ClaimDelivery(mock.Anything, realtimeDeliveryClaim(eventdelivery.DeliveryClaim{
		Subscriber: subscriber, Event: event, WorkerID: workerID, ClaimToken: claimToken,
		ClaimedAt: at, LeaseEnds: at.Add(time.Second),
	})).Return(true, nil).Once()
	repository.EXPECT().AcknowledgeDelivery(mock.Anything, mock.Anything).
		Run(func(context.Context, eventdelivery.DeliveryAcknowledgement) {
			require.Len(t, connection.frames, 2)
		}).Return(true, nil).Once()
	repository.EXPECT().CloseSubscriber(mock.Anything, realtimeSubscriberClose(eventdelivery.SubscriberClose{
		SubscriberID: subscriberID, InstanceID: instanceID, ClosedAt: at, Reason: "tournament_terminal",
	})).Return(errors.New("close receipt unavailable")).Once()
	delivery, err := NewRealtimeDelivery(repository, RealtimeDeliveryConfig{
		InstanceID: instanceID, WorkerID: workerID, LeaseDuration: time.Second,
		Now: func() time.Time { return at }, NewToken: func() uuid.UUID { return claimToken },
	})
	require.NoError(t, err)
	frame := terminalProjectionFrame(t, tournamentID, event)
	session := &realtimeDeliverySession{
		subscriber: subscriber, connection: connection,
		scope:  tournamentWriteScope{Role: TournamentRolePublic, TournamentID: tournamentID},
		render: func(context.Context, eventdelivery.Event) ([]byte, error) { return frame, nil },
	}
	session.lastSequence.Store(subscriber.AfterSequence)
	delivery.sessions[subscriber.ID] = session

	err = delivery.deliverSession(t.Context(), session, event)
	require.ErrorIs(t, err, ErrRealtimeDeliveryRepository)
	require.Len(t, connection.frames, 2)
	require.Equal(t, 1, connection.closeCount)
	require.False(t, delivery.sessionActive(session))
}

func publicRealtimeEvent(tournamentID uuid.UUID, occurredAt time.Time) eventdelivery.Event {
	return eventdelivery.Event{
		ID: tournamentSourceID(957), CorrelationID: tournamentSourceID(960), TournamentID: tournamentID,
		ProjectionRevisionID: tournamentSourceID(958), Sequence: 2,
		ProjectionRevision: 7, ProjectionOrdinal: 1, Audience: eventdelivery.AudiencePublic,
		Topic:      "result.settled",
		Payload:    json.RawMessage(`{"result_event_id":"94000000-0000-4000-8000-000000000959"}`),
		OccurredAt: occurredAt, AttemptCount: 1,
	}
}

func terminalRealtimeEvent(tournamentID, eventID uuid.UUID, sequence int64, occurredAt time.Time) eventdelivery.Event {
	return eventdelivery.Event{
		ID: eventID, CorrelationID: tournamentSourceID(1033), TournamentID: tournamentID, ProjectionRevisionID: tournamentSourceID(1029), Sequence: sequence,
		ProjectionRevision: 7, ProjectionOrdinal: 1, Terminal: true, Audience: eventdelivery.AudienceAll, Topic: "tournament.cancelled",
		Payload: json.RawMessage(`{"state":"cancelled"}`), OccurredAt: occurredAt, AttemptCount: 1,
	}
}

func terminalProjectionFrame(t *testing.T, tournamentID uuid.UUID, event eventdelivery.Event) []byte {
	t.Helper()
	_, snapshotFrame, _ := tournamentWriteTestFrames(t, tournamentID, tournamentSourceID(1032))
	message, err := DecodeTournamentPublicMessage(snapshotFrame)
	require.NoError(t, err)
	require.NotNil(t, message.Public)
	payload := *message.Public
	applyPublicDeliveryMetadata(&payload, event.Sequence, &event, uuid.Nil)
	frame, err := MarshalTournamentPublic(payload)
	require.NoError(t, err)
	return frame
}

func awaitRealtimeSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for realtime delivery lifecycle")
	}
}

func realtimeDeliveryClaim(expected eventdelivery.DeliveryClaim) interface{} {
	return mock.MatchedBy(func(actual eventdelivery.DeliveryClaim) bool {
		if actual.Subscriber.ConnectionID == uuid.Nil || actual.Subscriber.ConnectionGeneration < 1 {
			return false
		}
		expected.Subscriber.ConnectionID = actual.Subscriber.ConnectionID
		expected.Subscriber.ConnectionGeneration = actual.Subscriber.ConnectionGeneration
		return reflect.DeepEqual(expected, actual)
	})
}

func realtimeDeliveryAcknowledgement(expected eventdelivery.DeliveryAcknowledgement) interface{} {
	return mock.MatchedBy(func(actual eventdelivery.DeliveryAcknowledgement) bool {
		if actual.InstanceID == uuid.Nil || actual.ConnectionID == uuid.Nil || actual.ConnectionGeneration < 1 {
			return false
		}
		expected.InstanceID = actual.InstanceID
		expected.ConnectionID = actual.ConnectionID
		expected.ConnectionGeneration = actual.ConnectionGeneration
		return expected == actual
	})
}

func realtimeSubscriberClose(expected eventdelivery.SubscriberClose) interface{} {
	return mock.MatchedBy(func(actual eventdelivery.SubscriberClose) bool {
		if actual.ConnectionID == uuid.Nil || actual.ConnectionGeneration < 1 {
			return false
		}
		expected.ConnectionID = actual.ConnectionID
		expected.ConnectionGeneration = actual.ConnectionGeneration
		return expected == actual
	})
}

func realtimeDeliveryRetry(expected eventdelivery.DeliveryRetry) interface{} {
	return mock.MatchedBy(func(actual eventdelivery.DeliveryRetry) bool {
		if actual.InstanceID == uuid.Nil || actual.ConnectionID == uuid.Nil || actual.ConnectionGeneration < 1 {
			return false
		}
		expected.InstanceID = actual.InstanceID
		expected.ConnectionID = actual.ConnectionID
		expected.ConnectionGeneration = actual.ConnectionGeneration
		return expected == actual
	})
}

func newSubscriptionRepository(t *testing.T, subscriberID uuid.UUID) *eventdeliverymocks.MockSubscriptionRepository {
	t.Helper()
	repository := eventdeliverymocks.NewMockSubscriptionRepository(t)
	repository.EXPECT().OpenSubscription(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, request eventdelivery.SubscriptionOpenRequest) (eventdelivery.Subscription, error) {
			return eventdelivery.Subscription{
				Subscriber: eventdelivery.Subscriber{
					ID:                   subscriberID,
					InstanceID:           request.InstanceID,
					ConnectionID:         request.ConnectionID,
					ConnectionGeneration: 1,
					TournamentID:         request.TournamentID,
					Audience:             request.Audience,
					PrincipalID:          request.PrincipalID,
					AfterSequence:        request.AfterSequence,
					ConnectedAt:          request.OpenedAt,
				},
				TerminalState: eventdelivery.TerminalReceiptNone,
			}, nil
		}).Maybe()
	repository.EXPECT().SubscriberCursor(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, subscriber eventdelivery.Subscriber) (int64, error) {
			return subscriber.AfterSequence, nil
		}).Maybe()
	return repository
}

type recordingRealtimeSocket struct {
	mu          sync.Mutex
	frames      [][]byte
	writeCount  int
	failAtWrite int
	closeCount  int
}

func (connection *recordingRealtimeSocket) Write(_ context.Context, _ coderws.MessageType, data []byte) error {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	connection.writeCount++
	if connection.failAtWrite == connection.writeCount {
		return errors.New("partial websocket write")
	}
	connection.frames = append(connection.frames, append([]byte(nil), data...))
	return nil
}

func (connection *recordingRealtimeSocket) Ping(context.Context) error {
	return nil
}

func (connection *recordingRealtimeSocket) CloseNow() error {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	connection.closeCount++
	return nil
}
