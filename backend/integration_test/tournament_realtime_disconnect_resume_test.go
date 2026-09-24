//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	inboundws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	playerrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	realtimerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/realtime"
	participantrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/connection"
	tournamentsnapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/snapshot"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gamereconnect "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	connection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/connection"
)

func TestTournamentRealtimeDisconnectResume(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	started := participantReconnectStartFixture(ctx, t, 0)
	participantA := started.playerID
	participantB := started.fixture.participants[1]
	participantBAccountID := participantReconnectPlayerAccount(ctx, t, participantB)
	participantAToken := setRealtimePlayerSession(ctx, t, started.playerAccountID)
	participantBToken := setRealtimePlayerSession(ctx, t, participantBAccountID)

	deadline := started.started.GameClock.OriginalDeadline.UTC()
	initialAt := deadline.Add(-20 * time.Second)
	disconnectAt := deadline.Add(-10 * time.Second)
	reconnectAt := disconnectAt.Add(5 * time.Second)
	reconnectDeadline := disconnectAt.Add(30 * time.Second)
	clock := newParticipantConnectionClock(initialAt)

	connectionRepository := participantrepo.NewParticipantConnectionPostgres(
		started.fixture.tx,
		participantReconnectAuthorityProvider{identity: started.fixture.executionAuthority},
	)
	coordinator, err := connection.NewCoordinator(connection.Dependencies{
		Transactions: started.fixture.tx,
		Authority:    connectionRepository,
		Repository:   connectionRepository,
		Disconnect:   gamereconnect.NewDisconnectUseCase(started.fixture.adapter, clock),
		Reconnect:    gamereconnect.ReconnectNewUseCase(started.fixture.adapter, clock),
		Clock:        clock,
		Config:       connection.Config{ReconnectDuration: 30 * time.Second},
	})
	require.NoError(t, err)
	lifecycle := &participantConnectionRecordingLifecycle{
		delegate:    coordinator,
		disconnects: make(chan participantConnectionLifecycleResult, 8),
	}

	snapshotSource, err := inboundws.NewTournamentProductionSnapshotSource(
		tournamentsnapshotrepo.NewTournamentSnapshotPostgres(started.fixture.tx),
	)
	require.NoError(t, err)
	participantFlow, err := inboundws.NewTournamentParticipantFlow(snapshotSource)
	require.NoError(t, err)
	operatorFlow, err := inboundws.NewTournamentOperatorFlow(snapshotSource)
	require.NoError(t, err)

	restAuthFixture := newTournamentFlowRESTFixture(t)
	adminToken := restAuthFixture.adminAccessToken(t)
	realtime, err := inboundws.NewRealtimeDelivery(
		realtimerepo.NewRealtimeOutboxPostgres(started.fixture.tx),
		inboundws.RealtimeDeliveryConfig{
			InstanceID:   uuid.New(),
			WorkerID:     uuid.New(),
			PollInterval: 200 * time.Millisecond,
			RetryDelay:   20 * time.Millisecond,
		},
	)
	require.NoError(t, err)
	deliveryCtx, cancelDelivery := context.WithCancel(context.Background())
	deliveryErr := make(chan error, 1)
	go func() { deliveryErr <- realtime.Run(deliveryCtx) }()
	t.Cleanup(func() {
		cancelDelivery()
		select {
		case runErr := <-deliveryErr:
			require.NoError(t, runErr)
		case <-time.After(time.Second):
			t.Errorf("realtime delivery worker did not stop")
		}
	})
	require.Eventually(t, realtime.Ready, 3*time.Second, 10*time.Millisecond)

	server := inboundws.NewServer(
		playerrepo.NewPlayerPostgres(started.fixture.tx),
		inboundws.WithTournamentParticipantFlow(participantFlow),
		inboundws.WithTournamentParticipantLifecycleFlow(lifecycle),
		inboundws.WithTournamentOperatorFlow(operatorFlow),
		inboundws.WithTournamentOperatorSessionResolver(tournamentFlowOperatorSessionResolver(restAuthFixture.auth)),
		inboundws.WithRealtimeDelivery(realtime),
	)
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	participantPath := strings.ReplaceAll(
		inboundws.TournamentParticipantWebSocketPath,
		"{tournament_id}",
		started.fixture.tournamentID.String(),
	)
	participantEndpoint := "ws" + strings.TrimPrefix(httpServer.URL, "http") + participantPath
	participantAOptions := realtimePlayerWebSocketOptions(participantAToken)
	participantBOptions := realtimePlayerWebSocketOptions(participantBToken)
	participantAConnection := dialTournamentFlowWebSocket(t, participantEndpoint, participantAOptions)
	participantBConnection := dialTournamentFlowWebSocket(t, participantEndpoint, participantBOptions)

	operatorPath := strings.ReplaceAll(
		inboundws.TournamentOperatorWebSocketPath,
		"{tournament_id}",
		started.fixture.tournamentID.String(),
	)
	operatorEndpoint := "ws" + strings.TrimPrefix(httpServer.URL, "http") + operatorPath
	operatorConnection := dialTournamentFlowWebSocket(
		t,
		operatorEndpoint,
		tournamentFlowAdminWebSocketOptions(t, adminToken),
	)

	participantAInitialData := readTournamentFlowWebSocket(t, participantAConnection)
	participantAInitial, err := inboundws.DecodeTournamentParticipantMessage(participantAInitialData)
	require.NoError(t, err)
	require.NotNil(t, participantAInitial.Participant)
	assertParticipantRealtimeEnvelope(
		t,
		participantAInitialData,
		participantAInitial.Participant.Envelope,
		started.fixture.tournamentID,
		started.playerAccountID,
	)
	require.NotNil(t, participantAInitial.Participant.Envelope.ResumeID)
	resumeID := *participantAInitial.Participant.Envelope.ResumeID
	assertRealtimeParticipantGameActive(t, participantAInitial.Participant.Envelope.Participant, started.started.Game.ID)

	participantBInitialData := readTournamentFlowWebSocket(t, participantBConnection)
	participantBInitial, err := inboundws.DecodeTournamentParticipantMessage(participantBInitialData)
	require.NoError(t, err)
	require.NotNil(t, participantBInitial.Participant)
	assertParticipantRealtimeEnvelope(
		t,
		participantBInitialData,
		participantBInitial.Participant.Envelope,
		started.fixture.tournamentID,
		participantBAccountID,
	)
	require.Equal(
		t,
		participantAInitial.Participant.Envelope.Sequence,
		participantBInitial.Participant.Envelope.Sequence,
	)
	assertRealtimeParticipantGameActive(t, participantBInitial.Participant.Envelope.Participant, started.started.Game.ID)

	operatorInitialData := readTournamentFlowWebSocket(t, operatorConnection)
	operatorInitial, err := inboundws.DecodeTournamentOperatorMessage(operatorInitialData)
	require.NoError(t, err)
	require.NotNil(t, operatorInitial.Operator)
	assertOperatorRealtimeEnvelope(
		t,
		operatorInitialData,
		operatorInitial.Operator.Envelope,
		started.fixture.tournamentID,
	)
	baselineSequence := participantAInitial.Participant.Envelope.Sequence
	require.Equal(t, baselineSequence, operatorInitial.Operator.Envelope.Sequence)
	require.Nil(t, operatorInitial.Operator.Envelope.Operator.Pause)
	assertRealtimeOperatorPresence(
		t,
		operatorInitial.Operator.Envelope.Operator,
		participantA,
		participantB,
		string(pausedomain.PresenceStateConnected),
		string(pausedomain.PresenceStateConnected),
	)

	clock.FreezeAt(disconnectAt)
	require.NoError(t, participantAConnection.CloseNow())
	require.NoError(t, participantConnectionWaitForDisconnect(t, lifecycle))

	paused := readRealtimeDurableState(ctx, t, started, participantA, participantB)
	require.Equal(t, string(domain.GameStatePaused), paused.GameState)
	require.Equal(t, started.started.Game.ID, paused.GameID)
	require.Equal(t, started.started.GameRevision+1, paused.GameRevision)
	require.NotEqual(t, uuid.Nil, paused.PauseID)
	require.Equal(t, string(pausedomain.PresenceStateDisconnected), paused.Presence[participantA].State)
	require.Equal(t, string(pausedomain.PresenceStateConnected), paused.Presence[participantB].State)
	require.Equal(t, disconnectAt, paused.Presence[participantA].DisconnectedAt)
	require.Equal(t, disconnectAt, paused.PauseStartedAt)
	require.Equal(t, disconnectAt, paused.FrozenAt)
	require.Equal(t, deadline.Sub(disconnectAt).Milliseconds(), paused.FrozenRemainingMS)
	require.Equal(t, string(pausedomain.ReconnectStateOpen), paused.IntervalState)
	require.Equal(t, disconnectAt, paused.IntervalOpenedAt)
	require.Equal(t, reconnectDeadline, paused.IntervalDeadline)
	require.Equal(t, int64(1), paused.IntervalRevision)

	disconnectEvents := readRealtimeOutboxEvents(ctx, t, started, baselineSequence)
	require.Len(t, disconnectEvents, 1)
	disconnectEvent := disconnectEvents[0]
	require.Equal(t, baselineSequence+1, disconnectEvent.Sequence)
	require.Equal(t, started.started.Game.ID, disconnectEvent.GameAttemptID)
	require.Equal(t, started.started.Series.ID, disconnectEvent.SeriesID)
	require.Equal(t, started.fixture.waveID, disconnectEvent.WaveID)
	require.Equal(t, paused.GameRevision, disconnectEvent.GameRevision)
	require.Equal(t, started.fixture.projectionRevisionID, disconnectEvent.ProjectionRevisionID)
	require.Equal(t, started.started.CurrentProjectionRevision, disconnectEvent.ProjectionRevision)
	require.False(t, disconnectEvent.Terminal)
	require.Equal(t, "all", disconnectEvent.Audience)
	require.True(t, disconnectEvent.PrincipalIsNull)
	require.Equal(t, "game.reconnect.changed", disconnectEvent.Topic)
	require.Equal(t, disconnectAt, disconnectEvent.CreatedAt)
	require.NotEqual(t, uuid.Nil, disconnectEvent.IdempotencyKey)
	require.NotEqual(t, uuid.Nil, disconnectEvent.CommandID)
	assertReconnectOutboxPayload(t, disconnectEvent, "disconnect", started.started.Game.ID, paused.GameRevision)
	assertReconnectOutboxSource(
		t,
		disconnectEvent,
		started,
		gamereconnect.MutationDisconnect,
		1,
		2,
		paused.GameRevision,
	)

	participantBPausedData := readTournamentFlowWebSocket(t, participantBConnection)
	participantBPaused, err := inboundws.DecodeTournamentParticipantMessage(participantBPausedData)
	require.NoError(t, err)
	require.NotNil(t, participantBPaused.Participant)
	require.Equal(t, disconnectEvent.ID, participantBPaused.Participant.Envelope.EventID)
	require.Equal(t, disconnectEvent.Sequence, participantBPaused.Participant.Envelope.Sequence)
	assertParticipantRealtimeEnvelope(
		t,
		participantBPausedData,
		participantBPaused.Participant.Envelope,
		started.fixture.tournamentID,
		participantBAccountID,
	)
	assertRealtimeParticipantGamePaused(t, participantBPaused.Participant.Envelope.Participant, paused)

	operatorPausedData := readTournamentFlowWebSocket(t, operatorConnection)
	operatorPaused, err := inboundws.DecodeTournamentOperatorMessage(operatorPausedData)
	require.NoError(t, err)
	require.NotNil(t, operatorPaused.Operator)
	require.Equal(t, disconnectEvent.ID, operatorPaused.Operator.Envelope.EventID)
	require.Equal(t, disconnectEvent.Sequence, operatorPaused.Operator.Envelope.Sequence)
	assertOperatorRealtimeEnvelope(t, operatorPausedData, operatorPaused.Operator.Envelope, started.fixture.tournamentID)
	require.NotNil(t, operatorPaused.Operator.Envelope.Operator.Pause)
	operatorPause := operatorPaused.Operator.Envelope.Operator.Pause
	require.Equal(t, paused.PauseID, operatorPause.PauseID)
	require.Equal(t, "active", operatorPause.State)
	require.Equal(t, "disconnect", operatorPause.Reason)
	require.Equal(t, paused.GameID, *operatorPause.GameID)
	require.Equal(t, paused.FrozenRemainingMS, *operatorPause.FrozenRemainingMS)
	require.Equal(t, paused.IntervalDeadline, *operatorPause.ReconnectDeadline)
	require.Equal(t, paused.PauseRevision, operatorPause.GraphRevision)
	require.Equal(t, disconnectAt, operatorPause.PausedAt)
	assertRealtimeOperatorPresence(
		t,
		operatorPaused.Operator.Envelope.Operator,
		participantA,
		participantB,
		string(pausedomain.PresenceStateDisconnected),
		string(pausedomain.PresenceStateConnected),
	)
	assertRealtimeDeliveryReceipts(
		ctx,
		t,
		disconnectEvent.ID,
		[]realtimeExpectedReceipt{
			{role: "participant", principalID: participantBAccountID},
			{role: "operator"},
		},
	)

	clock.FreezeAt(reconnectAt)
	participantAReconnect := dialTournamentFlowWebSocket(
		t,
		participantEndpoint+"?resume_id="+resumeID.String(),
		participantAOptions,
	)
	participantAResumedInitialData := readTournamentFlowWebSocket(t, participantAReconnect)
	participantAResumedInitial, err := inboundws.DecodeTournamentParticipantMessage(participantAResumedInitialData)
	require.NoError(t, err)
	require.NotNil(t, participantAResumedInitial.Participant)
	assertParticipantRealtimeEnvelope(
		t,
		participantAResumedInitialData,
		participantAResumedInitial.Participant.Envelope,
		started.fixture.tournamentID,
		started.playerAccountID,
	)
	require.Equal(t, resumeID, *participantAResumedInitial.Participant.Envelope.ResumeID)
	require.Equal(t, disconnectEvent.Sequence, participantAResumedInitial.Participant.Envelope.Sequence)
	assertRealtimeParticipantGameResumed(t, participantAResumedInitial.Participant.Envelope.Participant, paused, reconnectAt)

	resumed := readRealtimeDurableState(ctx, t, started, participantA, participantB)
	require.Equal(t, string(domain.GameStateActive), resumed.GameState)
	require.Equal(t, started.started.Game.ID, resumed.GameID)
	require.Equal(t, paused.GameRevision+1, resumed.GameRevision)
	require.Equal(t, paused.PauseID, resumed.PauseID)
	require.Equal(t, "resumed", resumed.PauseState)
	require.Equal(t, reconnectAt, resumed.PauseResolvedAt)
	require.Equal(t, paused.FrozenRemainingMS, resumed.FrozenRemainingMS)
	require.Equal(t, reconnectAt, resumed.ResumedAt)
	require.Equal(t, reconnectAt.Add(time.Duration(resumed.FrozenRemainingMS)*time.Millisecond), resumed.ResumedDeadline)
	require.Equal(t, string(pausedomain.ReconnectStateReconnected), resumed.IntervalState)
	require.Equal(t, reconnectAt, resumed.IntervalClosedAt)
	require.Equal(t, int64(2), resumed.IntervalRevision)
	require.Equal(t, string(pausedomain.PresenceStateConnected), resumed.Presence[participantA].State)
	require.Equal(t, paused.Presence[participantA].PresenceEpoch+1, resumed.Presence[participantA].PresenceEpoch)
	require.Equal(t, reconnectAt, resumed.Presence[participantA].ConnectedAt)

	allEvents := readRealtimeOutboxEvents(ctx, t, started, baselineSequence)
	require.Len(t, allEvents, 2)
	var reconnectSourceCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*)
		FROM outbox_reconnect_sources
		WHERE tournament_id = $1 AND roster_id = $2`,
		started.fixture.tournamentID, started.fixture.rosterID,
	).Scan(&reconnectSourceCount))
	require.Equal(t, 2, reconnectSourceCount)
	require.Equal(t, disconnectEvent.ID, allEvents[0].ID)
	resumeEvent := allEvents[1]
	require.Greater(t, resumeEvent.Sequence, disconnectEvent.Sequence)
	require.Equal(t, disconnectEvent.Sequence+1, resumeEvent.Sequence)
	require.Equal(t, reconnectAt, resumeEvent.CreatedAt)
	require.Equal(t, started.started.Game.ID, resumeEvent.GameAttemptID)
	require.Equal(t, started.started.Series.ID, resumeEvent.SeriesID)
	require.Equal(t, started.fixture.waveID, resumeEvent.WaveID)
	require.Equal(t, resumed.GameRevision, resumeEvent.GameRevision)
	require.Equal(t, started.fixture.projectionRevisionID, resumeEvent.ProjectionRevisionID)
	require.Equal(t, started.started.CurrentProjectionRevision, resumeEvent.ProjectionRevision)
	require.False(t, resumeEvent.Terminal)
	require.Equal(t, "all", resumeEvent.Audience)
	require.True(t, resumeEvent.PrincipalIsNull)
	require.Equal(t, "game.reconnect.changed", resumeEvent.Topic)
	require.NotEqual(t, disconnectEvent.IdempotencyKey, resumeEvent.IdempotencyKey)
	require.NotEqual(t, disconnectEvent.CommandID, resumeEvent.CommandID)
	assertReconnectOutboxPayload(t, resumeEvent, "reconnect", started.started.Game.ID, resumed.GameRevision)
	assertReconnectOutboxSource(t, resumeEvent, started, gamereconnect.MutationReconnect, 2, 3, resumed.GameRevision)

	participantBResumedData := readTournamentFlowWebSocket(t, participantBConnection)
	participantBResumed, err := inboundws.DecodeTournamentParticipantMessage(participantBResumedData)
	require.NoError(t, err)
	require.NotNil(t, participantBResumed.Participant)
	require.Equal(t, resumeEvent.ID, participantBResumed.Participant.Envelope.EventID)
	require.Equal(t, resumeEvent.Sequence, participantBResumed.Participant.Envelope.Sequence)
	assertParticipantRealtimeEnvelope(
		t,
		participantBResumedData,
		participantBResumed.Participant.Envelope,
		started.fixture.tournamentID,
		participantBAccountID,
	)
	assertRealtimeParticipantGameResumed(t, participantBResumed.Participant.Envelope.Participant, resumed, reconnectAt)

	operatorResumedData := readTournamentFlowWebSocket(t, operatorConnection)
	operatorResumed, err := inboundws.DecodeTournamentOperatorMessage(operatorResumedData)
	require.NoError(t, err)
	require.NotNil(t, operatorResumed.Operator)
	require.Equal(t, resumeEvent.ID, operatorResumed.Operator.Envelope.EventID)
	require.Equal(t, resumeEvent.Sequence, operatorResumed.Operator.Envelope.Sequence)
	assertOperatorRealtimeEnvelope(t, operatorResumedData, operatorResumed.Operator.Envelope, started.fixture.tournamentID)
	require.Nil(t, operatorResumed.Operator.Envelope.Operator.Pause)
	assertRealtimeOperatorPresence(
		t,
		operatorResumed.Operator.Envelope.Operator,
		participantA,
		participantB,
		string(pausedomain.PresenceStateConnected),
		string(pausedomain.PresenceStateConnected),
	)

	participantAResumedData := readTournamentFlowWebSocket(t, participantAReconnect)
	participantAResumed, err := inboundws.DecodeTournamentParticipantMessage(participantAResumedData)
	require.NoError(t, err)
	require.NotNil(t, participantAResumed.Participant)
	require.Equal(t, resumeEvent.ID, participantAResumed.Participant.Envelope.EventID)
	require.Equal(t, resumeEvent.Sequence, participantAResumed.Participant.Envelope.Sequence)
	assertParticipantRealtimeEnvelope(
		t,
		participantAResumedData,
		participantAResumed.Participant.Envelope,
		started.fixture.tournamentID,
		started.playerAccountID,
	)
	require.Equal(t, resumeID, *participantAResumed.Participant.Envelope.ResumeID)
	assertRealtimeParticipantGameResumed(t, participantAResumed.Participant.Envelope.Participant, resumed, reconnectAt)

	assertRealtimeDeliveryReceipts(
		ctx,
		t,
		resumeEvent.ID,
		[]realtimeExpectedReceipt{
			{role: "participant", principalID: started.playerAccountID},
			{role: "participant", principalID: participantBAccountID},
			{role: "operator"},
		},
	)
}

func setRealtimePlayerSession(ctx context.Context, t *testing.T, playerID uuid.UUID) uuid.UUID {
	t.Helper()
	token := uuid.New()
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	_, err := sharedPool.Exec(ctx, `
		UPDATE players
		SET session_token = $2, session_expires_at = $3
		WHERE id = $1`, playerID, token, expiresAt)
	require.NoError(t, err)
	return token
}

func realtimePlayerWebSocketOptions(sessionToken uuid.UUID) *coderws.DialOptions {
	return &coderws.DialOptions{HTTPHeader: http.Header{
		"Cookie": {(&http.Cookie{
			Name:  middleware.PlayerSessionCookieName,
			Value: sessionToken.String(),
		}).String()},
	}}
}

type realtimeReconnectDurablePresence struct {
	State            string
	PresenceEpoch    int64
	Revision         int64
	ConnectedAt      time.Time
	DisconnectedAt   time.Time
	DisconnectedAtOK bool
}

type realtimeReconnectDurableState struct {
	GameID             uuid.UUID
	GameState          string
	GameRevision       int64
	PauseID            uuid.UUID
	PauseState         string
	PauseReason        string
	PauseRevision      int64
	PauseStartedAt     time.Time
	PauseResolvedAt    time.Time
	PauseResolvedAtOK  bool
	FrozenAt           time.Time
	FrozenRemainingMS  int64
	ResumedAt          time.Time
	ResumedAtOK        bool
	ResumedDeadline    time.Time
	ResumedDeadlineOK  bool
	IntervalID         uuid.UUID
	IntervalState      string
	IntervalOpenedAt   time.Time
	IntervalDeadline   time.Time
	IntervalClosedAt   time.Time
	IntervalClosedAtOK bool
	IntervalRevision   int64
	Presence           map[uuid.UUID]realtimeReconnectDurablePresence
}

func readRealtimeDurableState(
	ctx context.Context,
	t *testing.T,
	started participantReconnectStartedFixture,
	participantA, participantB uuid.UUID,
) realtimeReconnectDurableState {
	t.Helper()
	state := realtimeReconnectDurableState{
		GameID:   started.started.Game.ID,
		Presence: make(map[uuid.UUID]realtimeReconnectDurablePresence, 2),
	}
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, revision
		FROM game_attempts
		WHERE id = $1 AND series_id = $2 AND roster_id = $3`,
		started.started.Game.ID, started.started.Series.ID, started.fixture.rosterID,
	).Scan(&state.GameState, &state.GameRevision))

	var pauseResolvedAt pgtype.Timestamptz
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id, state, reason, revision, started_at, resolved_at
		FROM pauses
		WHERE tournament_id = $1 AND roster_id = $2 AND game_attempt_id = $3
		ORDER BY started_at DESC, id DESC
		LIMIT 1`,
		started.fixture.tournamentID, started.fixture.rosterID, started.started.Game.ID,
	).Scan(
		&state.PauseID,
		&state.PauseState,
		&state.PauseReason,
		&state.PauseRevision,
		&state.PauseStartedAt,
		&pauseResolvedAt,
	))
	if pauseResolvedAt.Valid {
		state.PauseResolvedAt = pauseResolvedAt.Time.UTC()
		state.PauseResolvedAtOK = true
	}

	var resumedAt, resumedDeadline pgtype.Timestamptz
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT frozen_at, frozen_remaining_ms, resumed_at, resumed_deadline
		FROM pause_clocks
		WHERE pause_id = $1 AND game_attempt_id = $2`,
		state.PauseID, started.started.Game.ID,
	).Scan(
		&state.FrozenAt,
		&state.FrozenRemainingMS,
		&resumedAt,
		&resumedDeadline,
	))
	if resumedAt.Valid {
		state.ResumedAt = resumedAt.Time.UTC()
		state.ResumedAtOK = true
	}
	if resumedDeadline.Valid {
		state.ResumedDeadline = resumedDeadline.Time.UTC()
		state.ResumedDeadlineOK = true
	}

	var intervalClosedAt pgtype.Timestamptz
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id, state, opened_at, deadline_at, closed_at, revision
		FROM reconnect_intervals
		WHERE pause_id = $1 AND game_attempt_id = $2 AND participant_id = $3
		ORDER BY opened_at DESC, id DESC
		LIMIT 1`,
		state.PauseID, started.started.Game.ID, participantA,
	).Scan(
		&state.IntervalID,
		&state.IntervalState,
		&state.IntervalOpenedAt,
		&state.IntervalDeadline,
		&intervalClosedAt,
		&state.IntervalRevision,
	))
	if intervalClosedAt.Valid {
		state.IntervalClosedAt = intervalClosedAt.Time.UTC()
		state.IntervalClosedAtOK = true
	}

	for _, participantID := range []uuid.UUID{participantA, participantB} {
		var disconnectedAt pgtype.Timestamptz
		var presence realtimeReconnectDurablePresence
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT state, presence_epoch, revision, connected_at, disconnected_at
			FROM presence_states
			WHERE tournament_id = $1 AND roster_id = $2 AND series_id = $3 AND participant_id = $4`,
			started.fixture.tournamentID, started.fixture.rosterID, started.started.Series.ID, participantID,
		).Scan(
			&presence.State,
			&presence.PresenceEpoch,
			&presence.Revision,
			&presence.ConnectedAt,
			&disconnectedAt,
		))
		if disconnectedAt.Valid {
			presence.DisconnectedAt = disconnectedAt.Time.UTC()
			presence.DisconnectedAtOK = true
		}
		presence.ConnectedAt = presence.ConnectedAt.UTC()
		state.Presence[participantID] = presence
	}
	state.PauseStartedAt = state.PauseStartedAt.UTC()
	state.FrozenAt = state.FrozenAt.UTC()
	state.IntervalOpenedAt = state.IntervalOpenedAt.UTC()
	state.IntervalDeadline = state.IntervalDeadline.UTC()
	return state
}

type realtimeReconnectOutboxEvent struct {
	ID                        uuid.UUID
	IdempotencyKey            uuid.UUID
	Sequence                  int64
	ProjectionRevisionID      uuid.UUID
	ProjectionRevision        int64
	ProjectionOrdinal         int16
	Terminal                  bool
	Audience                  string
	PrincipalIsNull           bool
	Topic                     string
	Payload                   []byte
	CreatedAt                 time.Time
	GameAttemptID             uuid.UUID
	SeriesID                  uuid.UUID
	WaveID                    uuid.UUID
	GameRevision              int64
	CommandID                 uuid.UUID
	MutationKind              string
	ExpectedAuthorityRevision int64
	ResultAuthorityRevision   int64
}

func readRealtimeOutboxEvents(
	ctx context.Context,
	t *testing.T,
	started participantReconnectStartedFixture,
	afterSequence int64,
) []realtimeReconnectOutboxEvent {
	t.Helper()
	rows, err := sharedPool.Query(ctx, `
		SELECT event.id, event.idempotency_key, event.sequence,
			event.projection_revision_id, event.projection_revision,
			event.projection_ordinal, event.terminal, event.audience,
			event.principal_id IS NULL, event.topic, event.payload, event.created_at,
			source.game_attempt_id, source.series_id, source.wave_id, source.game_revision,
			source.command_id, source.mutation_kind,
			source.expected_authority_revision, source.result_authority_revision
		FROM outbox_events AS event
		INNER JOIN outbox_reconnect_sources AS source ON source.outbox_event_id = event.id
		WHERE event.tournament_id = $1 AND event.roster_id = $2 AND event.sequence > $3
		ORDER BY event.sequence`,
		started.fixture.tournamentID, started.fixture.rosterID, afterSequence,
	)
	require.NoError(t, err)
	defer rows.Close()

	events := make([]realtimeReconnectOutboxEvent, 0, 2)
	for rows.Next() {
		var event realtimeReconnectOutboxEvent
		require.NoError(t, rows.Scan(
			&event.ID,
			&event.IdempotencyKey,
			&event.Sequence,
			&event.ProjectionRevisionID,
			&event.ProjectionRevision,
			&event.ProjectionOrdinal,
			&event.Terminal,
			&event.Audience,
			&event.PrincipalIsNull,
			&event.Topic,
			&event.Payload,
			&event.CreatedAt,
			&event.GameAttemptID,
			&event.SeriesID,
			&event.WaveID,
			&event.GameRevision,
			&event.CommandID,
			&event.MutationKind,
			&event.ExpectedAuthorityRevision,
			&event.ResultAuthorityRevision,
		))
		event.CreatedAt = event.CreatedAt.UTC()
		events = append(events, event)
	}
	require.NoError(t, rows.Err())
	return events
}

func assertReconnectOutboxPayload(
	t *testing.T,
	event realtimeReconnectOutboxEvent,
	mutationKind string,
	gameID uuid.UUID,
	gameRevision int64,
) {
	t.Helper()
	var payload struct {
		Schema       string    `json:"schema"`
		GameID       uuid.UUID `json:"game_id"`
		GameRevision int64     `json:"game_revision"`
		MutationKind string    `json:"mutation_kind"`
	}
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	require.Equal(t, "game-reconnect-changed-v1", payload.Schema)
	require.Equal(t, gameID, payload.GameID)
	require.Equal(t, gameRevision, payload.GameRevision)
	require.Equal(t, mutationKind, payload.MutationKind)
}

func assertReconnectOutboxSource(
	t *testing.T,
	event realtimeReconnectOutboxEvent,
	started participantReconnectStartedFixture,
	mutationKind gamereconnect.MutationKind,
	expectedRevision, resultRevision, expectedGameRevision int64,
) {
	t.Helper()
	require.Equal(t, string(mutationKind), event.MutationKind)
	require.Equal(t, expectedRevision, event.ExpectedAuthorityRevision)
	require.Equal(t, resultRevision, event.ResultAuthorityRevision)
	require.Equal(t, started.started.Game.ID, event.GameAttemptID)
	require.Equal(t, started.started.Series.ID, event.SeriesID)
	require.Equal(t, started.fixture.waveID, event.WaveID)
	require.Equal(t, expectedGameRevision, event.GameRevision)
	require.NotEqual(t, uuid.Nil, event.CommandID)
}

type realtimeExpectedReceipt struct {
	role        string
	principalID uuid.UUID
}

func assertRealtimeDeliveryReceipts(
	ctx context.Context,
	t *testing.T,
	eventID uuid.UUID,
	expected []realtimeExpectedReceipt,
) {
	t.Helper()
	var rows []struct {
		role        string
		principalID uuid.NullUUID
		outcome     string
		attempts    int32
	}
	require.Eventually(t, func() bool {
		queryRows, err := sharedPool.Query(ctx, `
			SELECT role, principal_id, outcome, attempt_count
			FROM realtime_delivery_receipts
			WHERE event_id = $1
			ORDER BY role, principal_id NULLS FIRST`, eventID)
		if err != nil {
			return false
		}
		defer queryRows.Close()
		rows = rows[:0]
		for queryRows.Next() {
			var row struct {
				role        string
				principalID uuid.NullUUID
				outcome     string
				attempts    int32
			}
			if queryRows.Scan(&row.role, &row.principalID, &row.outcome, &row.attempts) != nil {
				return false
			}
			rows = append(rows, row)
		}
		if queryRows.Err() != nil || len(rows) != len(expected) {
			return false
		}
		for _, row := range rows {
			if row.outcome != "written" || row.attempts != 1 || !row.principalID.Valid {
				return false
			}
		}
		for _, want := range expected {
			found := false
			for _, row := range rows {
				if row.role != want.role {
					continue
				}
				if want.principalID != uuid.Nil && (!row.principalID.Valid || row.principalID.UUID != want.principalID) {
					continue
				}
				found = true
				break
			}
			if !found {
				return false
			}
		}
		return true
	}, 3*time.Second, 20*time.Millisecond)
	require.Len(t, rows, len(expected))
}

func assertRealtimeParticipantGameActive(t *testing.T, snapshot tournamentws.ParticipantSnapshot, gameID uuid.UUID) {
	t.Helper()
	require.NotNil(t, snapshot.Game)
	require.Equal(t, gameID, snapshot.Game.GameID)
	require.Equal(t, string(domain.GameStateActive), snapshot.Game.State)
	require.NotNil(t, snapshot.Assignment)
	require.Equal(t, gameID, snapshot.Assignment.GameID)
	require.Nil(t, snapshot.Game.Pause)
}

func assertRealtimeParticipantGamePaused(
	t *testing.T,
	snapshot tournamentws.ParticipantSnapshot,
	paused realtimeReconnectDurableState,
) {
	t.Helper()
	require.NotNil(t, snapshot.Game)
	require.Equal(t, paused.GameID, snapshot.Game.GameID)
	require.Equal(t, string(domain.GameStatePaused), snapshot.Game.State)
	require.Equal(t, paused.PauseID, snapshot.Game.Pause.PauseID)
	require.Equal(t, paused.PauseState, snapshot.Game.Pause.State)
	require.Equal(t, paused.FrozenAt, snapshot.Game.Pause.FrozenAt.UTC())
	require.Equal(t, paused.FrozenRemainingMS, snapshot.Game.Pause.FrozenRemainingMS)
	require.Nil(t, snapshot.Game.Pause.ResumedAt)
	require.Nil(t, snapshot.Game.Pause.ResumedDeadline)
	require.NotNil(t, snapshot.Game.Pause.ReconnectDeadline)
	require.Equal(t, paused.IntervalDeadline, snapshot.Game.Pause.ReconnectDeadline.UTC())
}

func assertRealtimeParticipantGameResumed(
	t *testing.T,
	snapshot tournamentws.ParticipantSnapshot,
	durable realtimeReconnectDurableState,
	at time.Time,
) {
	t.Helper()
	require.NotNil(t, snapshot.Game)
	require.Equal(t, durable.GameID, snapshot.Game.GameID)
	require.Equal(t, string(domain.GameStateActive), snapshot.Game.State)
	require.NotNil(t, snapshot.Game.Pause)
	require.Equal(t, durable.PauseID, snapshot.Game.Pause.PauseID)
	require.Equal(t, "resumed", snapshot.Game.Pause.State)
	require.Equal(t, durable.FrozenRemainingMS, snapshot.Game.Pause.FrozenRemainingMS)
	require.NotNil(t, snapshot.Game.Pause.ResumedAt)
	require.Equal(t, at, snapshot.Game.Pause.ResumedAt.UTC())
	require.NotNil(t, snapshot.Game.Pause.ResumedDeadline)
	require.Equal(
		t,
		at.Add(time.Duration(durable.FrozenRemainingMS)*time.Millisecond),
		snapshot.Game.Pause.ResumedDeadline.UTC(),
	)
	require.Nil(t, snapshot.Game.Pause.ReconnectDeadline)
}

func assertRealtimeOperatorPresence(
	t *testing.T,
	snapshot tournamentws.OperatorSnapshot,
	participantA, participantB uuid.UUID,
	stateA, stateB string,
) {
	t.Helper()
	var foundA, foundB bool
	for _, presence := range snapshot.Presence {
		switch presence.ParticipantID {
		case participantA:
			require.Equal(t, stateA, presence.State)
			foundA = true
		case participantB:
			require.Equal(t, stateB, presence.State)
			foundB = true
		}
	}
	require.True(t, foundA)
	require.True(t, foundB)
}
