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
	playerrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	realtimerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/realtime"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	participantrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/connection"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	eventdelivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
	gamereconnect "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
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
		Disconnect:   gamereconnect.NewDisconnectUseCase(fixture.adapter, clock),
		Reconnect:    gamereconnect.ReconnectNewUseCase(fixture.adapter, clock),
		Clock:        clock,
		Config:       connection.Config{ReconnectDuration: 30 * time.Second},
	})
	require.NoError(t, err)
	lifecycle := &participantConnectionRecordingLifecycle{
		delegate:    coordinator,
		disconnects: make(chan participantConnectionLifecycleResult, 4),
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
		Disconnect:   gamereconnect.NewDisconnectUseCase(fixture.adapter, clock),
		Reconnect:    gamereconnect.ReconnectNewUseCase(fixture.adapter, clock),
		Clock:        clock,
		Config:       connection.Config{ReconnectDuration: 30 * time.Second},
	})
	require.NoError(t, err)
	lifecycle := &participantConnectionRecordingLifecycle{
		delegate:           coordinator,
		connects:           make(chan participantConnectionLifecycleResult, 8),
		disconnectAttempts: make(chan inbound.TournamentParticipantConnectionCommand, 8),
		disconnects:        make(chan participantConnectionLifecycleResult, 8),
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
	firstConnect := participantConnectionWaitForConnectEvent(t, lifecycle)
	require.NoError(t, firstConnect.err)
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
	participantConnectionWaitForDisconnectAttemptMatching(t, lifecycle, firstConnect.command)
	require.NoError(t, participantConnectionWaitForDisconnectMatching(t, lifecycle, firstConnect.command))
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
	replacementDisconnectRelease := make(chan struct{})
	releaseReplacementDisconnect := sync.OnceFunc(func() { close(replacementDisconnectRelease) })
	defer releaseReplacementDisconnect()
	lifecycle.gateDisconnect(replacementDisconnectRelease)
	third := dialTournamentFlowWebSocket(t, endpoint, options)
	thirdData := readTournamentFlowWebSocket(t, third)
	thirdConnect := participantConnectionWaitForConnectEvent(t, lifecycle)
	require.NoError(t, thirdConnect.err)
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

	// A replacement connection can race the stale generation's close. Start the
	// clock after every durable reconnect timestamp, then release the stale
	// disconnect only after the durable subscriber takeover has reached the
	// lifecycle boundary.
	presenceUpdatedAt := participantConnectionPresenceUpdatedAt(ctx, t, fixture, ids.FinalSeriesID, participantID)
	reconnectUpdatedAt := participantConnectionReconnectUpdatedAt(ctx, t, ids.SecondGameID, participantID)
	replacementTimestamp := participantConnectionAuthorityTimestamp(resumed)
	if presenceUpdatedAt.After(replacementTimestamp) {
		replacementTimestamp = presenceUpdatedAt
	}
	if reconnectUpdatedAt.After(replacementTimestamp) {
		replacementTimestamp = reconnectUpdatedAt
	}
	require.NotNil(t, resumed.GameClock.ResumedDeadline)
	replacementAt := resumed.GameClock.ResumedDeadline.Add(-3 * time.Second)
	require.True(t, replacementAt.After(replacementTimestamp),
		"replacement connect must be after persisted reconnect state")
	require.True(t, replacementAt.Before(*resumed.GameClock.ResumedDeadline),
		"replacement close must remain inside the resumed game clock")
	clock.FreezeAt(replacementAt)
	fourth := dialTournamentFlowWebSocket(t, endpoint+"?resume_id="+thirdMessage.Participant.Envelope.ResumeID.String(), options)
	fourthConnect := participantConnectionWaitForConnectEvent(t, lifecycle)
	require.NoError(t, fourthConnect.err)
	_ = readTournamentFlowWebSocket(t, fourth)
	participantConnectionWaitForDisconnectAttemptMatching(t, lifecycle, thirdConnect.command)
	clock.FreezeAt(resumed.GameClock.ResumedDeadline.Add(-time.Second))
	releaseReplacementDisconnect()
	require.NoError(t, participantConnectionWaitForDisconnectMatching(t, lifecycle, thirdConnect.command))
	require.Equal(t, 1, participantReconnectLeaseCount(ctx, fixture, participantID, "active"))
	require.Equal(t, string(domain.GameStateActive), participantReconnectGameState(ctx, fixture, ids.SecondGameID))
	require.Equal(t, 1, participantReconnectGamePauseCount(ctx, t, fixture, ids.SecondGameID))
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

func participantConnectionPresenceUpdatedAt(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	seriesID, participantID uuid.UUID,
) time.Time {
	t.Helper()
	var updatedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT updated_at
		FROM presence_states
		WHERE tournament_id = $1 AND roster_id = $2 AND series_id = $3 AND participant_id = $4`,
		fixture.tournamentID, fixture.rosterID, seriesID, participantID).Scan(&updatedAt))
	return updatedAt.UTC().Truncate(time.Microsecond)
}

func participantConnectionReconnectUpdatedAt(
	ctx context.Context,
	t *testing.T,
	gameID, participantID uuid.UUID,
) time.Time {
	t.Helper()
	var updatedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COALESCE(MAX(updated_at), TIMESTAMPTZ 'epoch')
		FROM reconnect_intervals
		WHERE game_attempt_id = $1 AND participant_id = $2`, gameID, participantID).Scan(&updatedAt))
	return updatedAt.UTC().Truncate(time.Microsecond)
}

func participantConnectionAuthorityTimestamp(authority gamereconnect.ReconnectAuthority) time.Time {
	latest := time.Time{}
	consider := func(candidate time.Time) {
		if candidate.After(latest) {
			latest = candidate
		}
	}
	for _, presence := range authority.Presence {
		consider(presence.ConnectedAt)
		consider(presence.UpdatedAt)
		if presence.DisconnectedAt != nil {
			consider(*presence.DisconnectedAt)
		}
	}
	for _, interval := range authority.Reconnect {
		consider(interval.OpenedAt)
		consider(interval.UpdatedAt)
		if interval.ClosedAt != nil {
			consider(*interval.ClosedAt)
		}
	}
	return latest.UTC().Truncate(time.Microsecond)
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
	delegate           inbound.TournamentParticipantConnectionUseCase
	connects           chan participantConnectionLifecycleResult
	disconnectAttempts chan inbound.TournamentParticipantConnectionCommand
	disconnects        chan participantConnectionLifecycleResult
	mu                 sync.Mutex
	disconnectGate     *participantConnectionDisconnectGate
	pendingAttempts    []inbound.TournamentParticipantConnectionCommand
	pendingDisconnects []participantConnectionLifecycleResult
}

type participantConnectionLifecycleResult struct {
	command inbound.TournamentParticipantConnectionCommand
	err     error
}

type participantConnectionDisconnectGate struct {
	release <-chan struct{}
}

func (lifecycle *participantConnectionRecordingLifecycle) gateDisconnect(release <-chan struct{}) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.disconnectGate = &participantConnectionDisconnectGate{release: release}
}

func (lifecycle *participantConnectionRecordingLifecycle) Connect(
	ctx context.Context,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	err := lifecycle.delegate.Connect(ctx, command)
	if lifecycle.connects != nil {
		lifecycle.connects <- participantConnectionLifecycleResult{command: command, err: err}
	}
	return err
}

func (lifecycle *participantConnectionRecordingLifecycle) Disconnect(
	ctx context.Context,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	if lifecycle.disconnectAttempts != nil {
		lifecycle.disconnectAttempts <- command
	}
	lifecycle.mu.Lock()
	gate := lifecycle.disconnectGate
	lifecycle.mu.Unlock()
	var err error
	if gate != nil {
		select {
		case <-gate.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err == nil {
		err = lifecycle.delegate.Disconnect(ctx, command)
	}
	if gate != nil && err == nil {
		lifecycle.mu.Lock()
		if lifecycle.disconnectGate == gate {
			lifecycle.disconnectGate = nil
		}
		lifecycle.mu.Unlock()
	}
	lifecycle.disconnects <- participantConnectionLifecycleResult{command: command, err: err}
	return err
}

func participantConnectionWaitForDisconnectAttemptMatching(
	t *testing.T,
	lifecycle *participantConnectionRecordingLifecycle,
	expected inbound.TournamentParticipantConnectionCommand,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		if lifecycle.takePendingDisconnectAttempt(expected) {
			return true
		}
		select {
		case candidate := <-lifecycle.disconnectAttempts:
			if !participantConnectionCommandsMatch(candidate, expected) {
				lifecycle.mu.Lock()
				lifecycle.pendingAttempts = append(lifecycle.pendingAttempts, candidate)
				lifecycle.mu.Unlock()
				return false
			}
			return true
		default:
			return false
		}
	}, 3*time.Second, 20*time.Millisecond)
}

func (lifecycle *participantConnectionRecordingLifecycle) takePendingDisconnectAttempt(
	expected inbound.TournamentParticipantConnectionCommand,
) bool {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	for index, candidate := range lifecycle.pendingAttempts {
		if participantConnectionCommandsMatch(candidate, expected) {
			lifecycle.pendingAttempts = append(
				lifecycle.pendingAttempts[:index],
				lifecycle.pendingAttempts[index+1:]...,
			)
			return true
		}
	}
	return false
}

func participantConnectionWaitForDisconnect(t *testing.T, lifecycle *participantConnectionRecordingLifecycle) error {
	return participantConnectionWaitForDisconnectResult(t, lifecycle, nil).err
}

func participantConnectionWaitForDisconnectMatching(
	t *testing.T,
	lifecycle *participantConnectionRecordingLifecycle,
	expected inbound.TournamentParticipantConnectionCommand,
) error {
	return participantConnectionWaitForDisconnectResult(t, lifecycle, &expected).err
}

func participantConnectionWaitForDisconnectResult(
	t *testing.T,
	lifecycle *participantConnectionRecordingLifecycle,
	expected *inbound.TournamentParticipantConnectionCommand,
) participantConnectionLifecycleResult {
	t.Helper()
	var result participantConnectionLifecycleResult
	require.Eventually(t, func() bool {
		if pending, ok := lifecycle.takePendingDisconnect(expected); ok {
			result = pending
			return true
		}
		select {
		case candidate := <-lifecycle.disconnects:
			if expected != nil && !participantConnectionCommandsMatch(candidate.command, *expected) {
				lifecycle.mu.Lock()
				lifecycle.pendingDisconnects = append(lifecycle.pendingDisconnects, candidate)
				lifecycle.mu.Unlock()
				return false
			}
			result = candidate
			return true
		default:
			return false
		}
	}, 3*time.Second, 20*time.Millisecond)
	return result
}

func participantConnectionWaitForConnect(t *testing.T, lifecycle *participantConnectionRecordingLifecycle) error {
	return participantConnectionWaitForConnectEvent(t, lifecycle).err
}

func participantConnectionWaitForConnectEvent(
	t *testing.T,
	lifecycle *participantConnectionRecordingLifecycle,
) participantConnectionLifecycleResult {
	t.Helper()
	var result participantConnectionLifecycleResult
	require.Eventually(t, func() bool {
		select {
		case result = <-lifecycle.connects:
			return true
		default:
			return false
		}
	}, 3*time.Second, 20*time.Millisecond)
	return result
}

func (lifecycle *participantConnectionRecordingLifecycle) takePendingDisconnect(
	expected *inbound.TournamentParticipantConnectionCommand,
) (participantConnectionLifecycleResult, bool) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	for index, candidate := range lifecycle.pendingDisconnects {
		if expected == nil || participantConnectionCommandsMatch(candidate.command, *expected) {
			lifecycle.pendingDisconnects = append(
				lifecycle.pendingDisconnects[:index],
				lifecycle.pendingDisconnects[index+1:]...,
			)
			return candidate, true
		}
	}
	return participantConnectionLifecycleResult{}, false
}

func participantConnectionCommandsMatch(
	left inbound.TournamentParticipantConnectionCommand,
	right inbound.TournamentParticipantConnectionCommand,
) bool {
	return left.ConnectionID == right.ConnectionID &&
		left.ConnectionGeneration == right.ConnectionGeneration
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
) *gamestart.StartRecord {
	t.Helper()
	command := fixture.startCommand(ctx, t)
	var record *gamestart.StartRecord
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
