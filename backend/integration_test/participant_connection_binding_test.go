//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	inboundws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	playerrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	realtimerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/realtime"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	participantrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	eventdelivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	connection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/connection"
)

func TestParticipantConnectionBindingFollowsCurrentSubscriberFence(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	fixture := createParticipantReconnectSwissProofFixture(ctx, t)
	playerID := fixture.participants[0]
	playerAccountID := participantReconnectPlayerAccount(ctx, t, playerID)
	sessionToken := uuid.New()
	sessionExpiresAt := time.Now().UTC().Add(time.Hour)
	_, err := sharedPool.Exec(ctx, `
		UPDATE players
		SET session_token = $2, session_expires_at = $3
		WHERE id = $1`, playerAccountID, sessionToken, sessionExpiresAt)
	require.NoError(t, err)

	clock := newParticipantConnectionClock(time.Now().UTC())
	connectionRepository := participantrepo.NewParticipantConnectionPostgres(
		fixture.tx,
		participantReconnectAuthorityProvider{identity: fixture.executionAuthority},
	)
	coordinator, err := connection.NewCoordinator(connection.Dependencies{
		Transactions: fixture.tx,
		Authority:    connectionRepository,
		Repository:   connectionRepository,
		Disconnect:   gameusecase.NewDisconnectUseCase(fixture.adapter, clock),
		Reconnect:    gameusecase.ReconnectNewUseCase(fixture.adapter, clock),
		Clock:        clock,
		Config:       connection.Config{ReconnectDuration: 30 * time.Second},
	})
	require.NoError(t, err)
	lifecycle := &participantConnectionRecordingLifecycle{
		delegate:    coordinator,
		disconnects: make(chan error, 4),
	}

	realtime, err := inboundws.NewRealtimeDelivery(
		participantConnectionRealtimeRepository{
			SubscriptionRepository: realtimerepo.NewRealtimeOutboxPostgres(fixture.tx),
		},
		inboundws.RealtimeDeliveryConfig{InstanceID: uuid.New(), WorkerID: uuid.New()},
	)
	require.NoError(t, err)
	players := playerrepo.NewPlayerPostgres(fixture.tx)
	server := inboundws.NewServer(
		players,
		inboundws.WithTournamentParticipantFlow(participantConnectionBindingFlow{}),
		inboundws.WithTournamentParticipantLifecycleFlow(lifecycle),
		inboundws.WithRealtimeDelivery(realtime),
	)
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	endpoint := httpServer.URL + "/api/v1/tournaments/" + fixture.tournamentID.String() + "/participant/realtime"
	options := &coderws.DialOptions{HTTPHeader: http.Header{
		"Cookie": {(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: sessionToken.String()}).String()},
	}}
	first := dialTournamentFlowWebSocket(t, endpoint, options)
	firstData := readTournamentFlowWebSocket(t, first)
	firstMessage, err := inboundws.DecodeTournamentParticipantMessage(firstData)
	require.NoError(t, err)
	require.NotNil(t, firstMessage.Participant)
	require.NotNil(t, firstMessage.Participant.Envelope.ResumeID)
	resumeID := *firstMessage.Participant.Envelope.ResumeID
	require.Eventually(t, func() bool {
		return participantReconnectLeaseCount(ctx, fixture, playerID, "active") == 1 &&
			participantReconnectLeaseBindingIsEmpty(ctx, fixture, playerID)
	}, 3*time.Second, 20*time.Millisecond)

	// The first socket was opened before the wave started.  Starting the wave
	// changes the graph binding while that exact lease remains open.
	startRecord := participantConnectionBindingStartWave(ctx, t, fixture)
	require.NotNil(t, startRecord)
	activeBeforeTakeover, err := fixture.adapter.LoadAuthority(ctx, pausedomain.GraphScope{
		TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		WaveID: fixture.waveID, Authority: fixture.executionAuthority,
	}, playerID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStateActive, activeBeforeTakeover.Game.State)
	clock.FreezeAt(activeBeforeTakeover.GameClock.OriginalDeadline.Add(-10 * time.Second))

	second := dialTournamentFlowWebSocket(t, endpoint+"?resume_id="+resumeID.String(), options)
	secondData := readTournamentFlowWebSocket(t, second)
	secondMessage, err := inboundws.DecodeTournamentParticipantMessage(secondData)
	require.NoError(t, err)
	require.NotNil(t, secondMessage.Participant)
	require.Equal(t, resumeID, *secondMessage.Participant.Envelope.ResumeID)
	require.Eventually(t, func() bool {
		return participantReconnectLeaseCount(ctx, fixture, playerID, "active") == 1 &&
			participantReconnectGameState(ctx, fixture, activeBeforeTakeover.Game.ID) == string(domain.GameStateActive)
	}, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, participantConnectionWaitForDisconnect(t, lifecycle))

	// The replaced generation closes its own durable lease, but its stale fence
	// cannot pause the current game while generation 2 remains connected.

	// The last current fence resolves the current graph binding and freezes the
	// game even though the lease originally opened before the wave started.
	require.NoError(t, second.CloseNow())
	require.NoError(t, participantConnectionWaitForDisconnect(t, lifecycle))
	require.Eventually(t, func() bool {
		return participantReconnectLeaseCount(ctx, fixture, playerID, "active") == 0 &&
			participantReconnectGameState(ctx, fixture, activeBeforeTakeover.Game.ID) == string(domain.GameStatePaused)
	}, 3*time.Second, 20*time.Millisecond)

	clock.FreezeAt(activeBeforeTakeover.GameClock.OriginalDeadline.Add(-5 * time.Second))
	third := dialTournamentFlowWebSocket(t, endpoint, options)
	_ = readTournamentFlowWebSocket(t, third)
	require.Eventually(t, func() bool {
		return participantReconnectGameState(ctx, fixture, activeBeforeTakeover.Game.ID) == string(domain.GameStateActive) &&
			participantReconnectLeaseCount(ctx, fixture, playerID, "active") == 1
	}, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, third.CloseNow())
}

func TestParticipantConnectionBindingSurvivesFinalGameAdvance(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	fixture, stageCommand := prepareTechnicalPlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, stageCommand)
	require.NoError(t, err)
	firstSemifinal := semifinalSettlementInput(ctx, t, fixture, stageCommand.CommandID, 1)
	_, changed, err := resultauthority.NewResultPostgres(fixture.tx).Settle(ctx, firstSemifinal)
	require.NoError(t, err)
	require.True(t, changed)
	_, secondWaveID, secondSemifinalParticipants := technicalPlayoffSeries(ctx, t, fixture, stageCommand.CommandID, 2)
	technicalStartWave(ctx, t, fixture, secondWaveID, secondSemifinalParticipants)
	pending := technicalDisconnectPending(ctx, t, fixture, secondWaveID, secondSemifinalParticipants[0])
	changed, err = technicalDeadlineHandler(fixture, pending).HandleDeadline(ctx, pending)
	require.NoError(t, err)
	require.True(t, changed)
	ids, err := playoff.FinalStageIdentity(stageCommand.CommandID)
	require.NoError(t, err)
	activateTechnicalFinal(ctx, t, fixture, ids)
	_, finalParticipants := technicalFinalParticipants(ctx, t, fixture, ids.FinalSeriesID)
	participantID := finalParticipants[0]
	participantConnectionEnsurePresence(ctx, t, fixture, ids.FinalSeriesID, finalParticipants)
	playerAccountID := participantReconnectPlayerAccount(ctx, t, participantID)
	sessionToken := uuid.New()
	sessionExpiresAt := time.Now().UTC().Add(time.Hour)
	_, err = sharedPool.Exec(ctx, `
		UPDATE players
		SET session_token = $2, session_expires_at = $3
		WHERE id = $1`, playerAccountID, sessionToken, sessionExpiresAt)
	require.NoError(t, err)

	clock := newParticipantConnectionClock(time.Now().UTC())
	connectionRepository := participantrepo.NewParticipantConnectionPostgres(
		fixture.tx,
		participantReconnectAuthorityProvider{identity: fixture.executionAuthority},
	)
	coordinator, err := connection.NewCoordinator(connection.Dependencies{
		Transactions: fixture.tx,
		Authority:    connectionRepository,
		Repository:   connectionRepository,
		Disconnect:   gameusecase.NewDisconnectUseCase(fixture.adapter, clock),
		Reconnect:    gameusecase.ReconnectNewUseCase(fixture.adapter, clock),
		Clock:        clock,
		Config:       connection.Config{ReconnectDuration: 30 * time.Second},
	})
	require.NoError(t, err)
	lifecycle := &participantConnectionRecordingLifecycle{
		delegate:    coordinator,
		disconnects: make(chan error, 8),
	}

	realtime, err := inboundws.NewRealtimeDelivery(
		participantConnectionRealtimeRepository{
			SubscriptionRepository: realtimerepo.NewRealtimeOutboxPostgres(fixture.tx),
		},
		inboundws.RealtimeDeliveryConfig{InstanceID: uuid.New(), WorkerID: uuid.New()},
	)
	require.NoError(t, err)
	server := inboundws.NewServer(
		playerrepo.NewPlayerPostgres(fixture.tx),
		inboundws.WithTournamentParticipantFlow(participantConnectionBindingFlow{}),
		inboundws.WithTournamentParticipantLifecycleFlow(lifecycle),
		inboundws.WithRealtimeDelivery(realtime),
	)
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	endpoint := httpServer.URL + "/api/v1/tournaments/" + fixture.tournamentID.String() + "/participant/realtime"
	options := &coderws.DialOptions{HTTPHeader: http.Header{
		"Cookie": {(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: sessionToken.String()}).String()},
	}}
	// Move Game 1 to active before opening the socket.  The socket must keep
	// its lease binding while the terminal coordinator creates Game 2.
	firstStart := technicalStartWave(ctx, t, fixture, ids.FirstWaveID, finalParticipants)
	require.Len(t, firstStart.Games, 1)
	first := dialTournamentFlowWebSocket(t, endpoint, options)
	firstData := readTournamentFlowWebSocket(t, first)
	firstMessage, err := inboundws.DecodeTournamentParticipantMessage(firstData)
	require.NoError(t, err)
	require.NotNil(t, firstMessage.Participant)
	require.NotNil(t, firstMessage.Participant.Envelope.ResumeID)
	require.Eventually(t, func() bool {
		return participantReconnectLeaseCount(ctx, fixture, participantID, "active") == 1 &&
			participantReconnectGameState(ctx, fixture, ids.FirstGameID) == string(domain.GameStateActive)
	}, 3*time.Second, 20*time.Millisecond)

	firstGamePending := technicalDisconnectPending(ctx, t, fixture, ids.FirstWaveID, participantID)
	changed, err = technicalDeadlineHandler(fixture, firstGamePending).HandleDeadline(ctx, firstGamePending)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, string(domain.GameStateCompleted), participantReconnectGameState(ctx, fixture, ids.FirstGameID))

	technicalReconnectPresence(ctx, t, fixture, ids.FinalSeriesID, participantID, firstGamePending.DueAt)
	secondStart := technicalStartWave(ctx, t, fixture, ids.SecondWaveID, finalParticipants)
	require.Len(t, secondStart.Games, 1)
	secondScope := pausedomain.GraphScope{
		TournamentID: fixture.tournamentID,
		RosterID:     fixture.rosterID,
		WaveID:       ids.SecondWaveID,
		Authority:    fixture.executionAuthority,
	}
	secondActive, err := fixture.adapter.LoadAuthority(ctx, secondScope, participantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStateActive, secondActive.Game.State)
	disconnectAt := secondActive.GameClock.OriginalDeadline.Add(-10 * time.Second)
	clock.FreezeAt(disconnectAt)

	// The unchanged current fence opened against Game 1.  Its close-time
	// resolution must pause only the current Game 2, never the completed Game 1.
	require.NoError(t, first.CloseNow())
	require.NoError(t, participantConnectionWaitForDisconnect(t, lifecycle))
	require.Eventually(t, func() bool {
		return participantReconnectLeaseCount(ctx, fixture, participantID, "active") == 0 &&
			participantReconnectGameState(ctx, fixture, ids.FirstGameID) == string(domain.GameStateCompleted) &&
			participantReconnectGameState(ctx, fixture, ids.SecondGameID) == string(domain.GameStatePaused)
	}, 3*time.Second, 20*time.Millisecond)

	paused, err := fixture.adapter.LoadAuthority(ctx, secondScope, participantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStatePaused, paused.Game.State)
	require.Len(t, paused.Reconnect, 1)
	require.Equal(t, disconnectAt, paused.Reconnect[0].OpenedAt)
	require.Equal(t, disconnectAt.Add(30*time.Second), paused.Reconnect[0].Deadline)

	// Reconnect five seconds into the interval and verify the exact frozen
	// remainder is resumed.  Then replace that current fence and prove its
	// stale close cannot mutate the active Game 2.
	reconnectAt := disconnectAt.Add(5 * time.Second)
	clock.FreezeAt(reconnectAt)
	third := dialTournamentFlowWebSocket(t, endpoint, options)
	thirdData := readTournamentFlowWebSocket(t, third)
	thirdMessage, err := inboundws.DecodeTournamentParticipantMessage(thirdData)
	require.NoError(t, err)
	require.NotNil(t, thirdMessage.Participant)
	require.NotNil(t, thirdMessage.Participant.Envelope.ResumeID)
	require.Eventually(t, func() bool {
		return participantReconnectLeaseCount(ctx, fixture, participantID, "active") == 1 &&
			participantReconnectGameState(ctx, fixture, ids.SecondGameID) == string(domain.GameStateActive)
	}, 3*time.Second, 20*time.Millisecond)
	resumed, err := fixture.adapter.LoadAuthority(ctx, secondScope, participantID)
	require.NoError(t, err)
	require.Equal(t, paused.GameClock.Remaining, resumed.GameClock.Remaining)
	require.NotNil(t, resumed.GameClock.ResumedAt)
	require.Equal(t, reconnectAt, *resumed.GameClock.ResumedAt)
	require.Equal(t, reconnectAt.Add(paused.GameClock.Remaining), *resumed.GameClock.ResumedDeadline)

	fourth := dialTournamentFlowWebSocket(t, endpoint+"?resume_id="+thirdMessage.Participant.Envelope.ResumeID.String(), options)
	_ = readTournamentFlowWebSocket(t, fourth)
	require.NoError(t, participantConnectionWaitForDisconnect(t, lifecycle))
	require.Eventually(t, func() bool {
		return participantReconnectLeaseCount(ctx, fixture, participantID, "active") == 1 &&
			participantReconnectGameState(ctx, fixture, ids.SecondGameID) == string(domain.GameStateActive) &&
			participantReconnectGamePauseCount(ctx, t, fixture, ids.SecondGameID) == 1
	}, 3*time.Second, 20*time.Millisecond)
}

func participantConnectionEnsurePresence(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	seriesID uuid.UUID,
	participantIDs []uuid.UUID,
) {
	t.Helper()
	for _, participantID := range participantIDs {
		var exists bool
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM presence_states
				WHERE tournament_id = $1 AND roster_id = $2 AND series_id = $3 AND participant_id = $4
			)`, fixture.tournamentID, fixture.rosterID, seriesID, participantID).Scan(&exists))
		if !exists {
			participantReconnectCreatePresence(ctx, t, fixture.tournamentID, fixture.rosterID, seriesID,
				[]uuid.UUID{participantID}, time.Now().UTC().Truncate(time.Microsecond))
		}
	}
}

type participantConnectionClock struct {
	mu sync.RWMutex
	at time.Time
}

func newParticipantConnectionClock(at time.Time) *participantConnectionClock {
	return &participantConnectionClock{at: at.UTC().Truncate(time.Microsecond)}
}

func (clock *participantConnectionClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.at
}

func (clock *participantConnectionClock) FreezeAt(at time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.at = at.UTC().Truncate(time.Microsecond)
}

type participantConnectionRealtimeRepository struct {
	eventdelivery.SubscriptionRepository
}

func (repository participantConnectionRealtimeRepository) OpenSubscription(
	ctx context.Context,
	request eventdelivery.SubscriptionOpenRequest,
) (eventdelivery.Subscription, error) {
	subscription, err := repository.SubscriptionRepository.OpenSubscription(ctx, request)
	if err != nil {
		return eventdelivery.Subscription{}, err
	}
	// The technical fixture has a prior semifinal terminal event.  Keep the
	// durable subscriber and socket fence, but leave that unrelated event out
	// of this connection's initial delivery so the test can hold the socket
	// across the final series transition.
	subscription.TerminalState = eventdelivery.TerminalReceiptNone
	subscription.PendingTerminal = nil
	return subscription, nil
}

type participantConnectionBindingFlow struct{}

type participantConnectionRecordingLifecycle struct {
	delegate    inbound.TournamentParticipantConnectionUseCase
	disconnects chan error
}

func (lifecycle *participantConnectionRecordingLifecycle) Connect(
	ctx context.Context,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	return lifecycle.delegate.Connect(ctx, command)
}

func (lifecycle *participantConnectionRecordingLifecycle) Disconnect(
	ctx context.Context,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	err := lifecycle.delegate.Disconnect(ctx, command)
	lifecycle.disconnects <- err
	return err
}

func participantConnectionWaitForDisconnect(t *testing.T, lifecycle *participantConnectionRecordingLifecycle) error {
	t.Helper()
	var result error
	require.Eventually(t, func() bool {
		select {
		case result = <-lifecycle.disconnects:
			return true
		default:
			return false
		}
	}, 3*time.Second, 20*time.Millisecond)
	return result
}

func (participantConnectionBindingFlow) OpenTournamentParticipant(
	_ context.Context,
	request inboundws.TournamentParticipantConnectionRequest,
) (inboundws.TournamentParticipantPayload, error) {
	snapshot, err := tournamentws.NewParticipantSnapshot(
		tournamentws.ParticipantSnapshotScope{TournamentID: request.TournamentID, PlayerID: request.Principal.PlayerID},
		tournamentws.ParticipantSnapshotInput{
			TournamentID: request.TournamentID, PlayerID: request.Principal.PlayerID,
			Revision: 1, LastSequence: 0,
		},
	)
	if err != nil {
		return inboundws.TournamentParticipantPayload{}, err
	}
	metadata := tournamentws.RealtimeEnvelopeMetadata{
		SchemaVersion: tournamentws.TournamentRealtimeSchemaVersion,
		TournamentID:  request.TournamentID, Sequence: 0, EventID: uuid.New(),
		OccurredAt: time.Now().UTC().Truncate(time.Microsecond), ProjectionRevision: 1,
	}
	envelope, err := tournamentws.NewRealtimeEnvelope(metadata, snapshot)
	if err != nil || envelope.Participant == nil {
		return inboundws.TournamentParticipantPayload{}, err
	}
	return inboundws.NewTournamentParticipantPayload(tournamentws.ParticipantRealtimeEnvelope{
		SchemaVersion: envelope.SchemaVersion, TournamentID: envelope.TournamentID,
		Sequence: envelope.Sequence, EventID: envelope.EventID, OccurredAt: envelope.OccurredAt,
		ProjectionRevision: envelope.ProjectionRevision, Participant: *envelope.Participant,
	})
}

func participantConnectionBindingStartWave(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
) *gameusecase.StartRecord {
	t.Helper()
	command := fixture.startCommand(ctx, t)
	var record *gameusecase.StartRecord
	var changed bool
	err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var startErr error
		record, changed, startErr = fixture.start.Start(txCtx, command)
		return startErr
	})
	require.NoError(t, err)
	require.True(t, changed)
	return record
}

func participantReconnectPlayerAccount(ctx context.Context, t *testing.T, participantID uuid.UUID) uuid.UUID {
	t.Helper()
	var playerID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT player_id FROM participants WHERE id = $1`, participantID).Scan(&playerID))
	return playerID
}

func participantReconnectLeaseCount(ctx context.Context, fixture tournamentAdminSwissProofFixture, participantID uuid.UUID, state string) int {
	var count int
	if err := sharedPool.QueryRow(ctx, `
		SELECT count(*)
		FROM participant_connection_leases
		WHERE tournament_id = $1 AND roster_id = $2 AND participant_id = $3 AND state = $4`,
		fixture.tournamentID, fixture.rosterID, participantID, state).Scan(&count); err != nil {
		return -1
	}
	return count
}

func participantReconnectLeaseBindingIsEmpty(ctx context.Context, fixture tournamentAdminSwissProofFixture, participantID uuid.UUID) bool {
	var assignmentID, seriesID, attemptID *uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		SELECT assignment_id, series_id, game_attempt_id
		FROM participant_connection_leases
		WHERE tournament_id = $1 AND roster_id = $2 AND participant_id = $3 AND state = 'active'`,
		fixture.tournamentID, fixture.rosterID, participantID).Scan(&assignmentID, &seriesID, &attemptID)
	return err == nil && assignmentID == nil && seriesID == nil && attemptID == nil
}

func participantReconnectGameState(ctx context.Context, fixture tournamentAdminSwissProofFixture, gameID uuid.UUID) string {
	var state string
	if err := sharedPool.QueryRow(ctx, `
		SELECT state FROM game_attempts
		WHERE id = $1 AND roster_id = $2`, gameID, fixture.rosterID).Scan(&state); err != nil {
		return ""
	}
	return state
}
