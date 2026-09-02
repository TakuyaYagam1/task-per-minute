package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/requestmeta"
	arenaws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/arena"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

func TestArenaTransportComposition(t *testing.T) {
	tournamentID := uuid.MustParse("72000000-0000-4000-8000-000000000001")

	t.Run("anonymous public socket is read only and releases connection state", func(t *testing.T) {
		public := &arenaPublicFlowStub{
			payload:  ArenaPublicPayload{Envelopes: []ArenaPublicEnvelope{}},
			opened:   make(chan ArenaPublicConnectionRequest, 1),
			released: make(chan struct{}),
		}
		subscription := &arenaTerminalSubscriptionStub{
			deliveries: make(chan arenaws.CancellationDelivery),
			closed:     make(chan struct{}),
		}
		terminal := &arenaTerminalFlowStub{subscription: subscription}
		server := NewServer(
			nil,
			nil,
			nil,
			NewHubRegistry(),
			WithArenaPublicFlow(public),
			WithArenaTerminalFlow(terminal),
			WithHubCloseDelay(0),
		)
		httpServer := httptest.NewServer(server)
		defer httpServer.Close()

		conn, resp, err := coderws.Dial(t.Context(), wsTestEndpoint(httpServer.URL)+"?arena_role=public", nil)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			defer resp.Body.Close()
		}

		writeTestEvent(t, conn, EventArenaConnect, ArenaConnectPayload{
			Role: ArenaRolePublic, TournamentID: tournamentID,
		})
		message, err := DecodeArenaPublicMessage(readArenaTestMessage(t, conn))
		require.NoError(t, err)
		require.NotNil(t, message.Public)

		select {
		case request := <-public.opened:
			require.Equal(t, tournamentID, request.TournamentID)
			require.Nil(t, request.Cursor)
		case <-time.After(time.Second):
			t.Fatal("public flow was not opened")
		}
		terminalRequest := terminal.lastRequest()
		require.Equal(t, ArenaRolePublic, terminalRequest.Role)
		require.False(t, terminalRequest.Authenticated)

		writeTestEvent(t, conn, "arena_submit", map[string]any{"flag": "not-routed"})
		message, err = DecodeArenaPublicMessage(readArenaTestMessage(t, conn))
		require.NoError(t, err)
		require.Equal(t, ArenaRejectionForbidden, message.Rejected.Code)

		require.NoError(t, conn.Close(coderws.StatusNormalClosure, ""))
		requireSignal(t, public.released, "public connection context was not released")
		requireSignal(t, subscription.closed, "terminal subscription was not closed")
	})

	t.Run("public marker preserves origin and query credential checks", func(t *testing.T) {
		public := &arenaPublicFlowStub{payload: ArenaPublicPayload{Envelopes: []ArenaPublicEnvelope{}}}
		server := NewServer(nil, nil, nil, NewHubRegistry(), WithArenaPublicFlow(public))
		httpServer := httptest.NewServer(server)
		defer httpServer.Close()

		headers := http.Header{}
		headers.Set("Origin", "https://untrusted.example")
		conn, resp, err := coderws.Dial(t.Context(), wsTestEndpoint(httpServer.URL)+"?arena_role=public", &coderws.DialOptions{HTTPHeader: headers})
		if conn != nil {
			_ = conn.Close(coderws.StatusNormalClosure, "")
		}
		require.Error(t, err)
		require.NotNil(t, resp)
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		conn, resp, err = coderws.Dial(t.Context(), wsTestEndpoint(httpServer.URL)+"?arena_role=public&access_token=unsafe", nil)
		if conn != nil {
			_ = conn.Close(coderws.StatusNormalClosure, "")
		}
		require.Error(t, err)
		require.NotNil(t, resp)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
		require.Zero(t, public.calls())
	})

	t.Run("authenticated roles use trusted principals", func(t *testing.T) {
		token := uuid.New()
		player := &domain.Player{ID: uuid.New(), Username: "alice", SessionToken: &token, Status: domain.PlayerStatusIdle}
		participant := &arenaParticipantFlowStub{payload: ArenaParticipantPayload{Envelopes: []arenaws.ParticipantRealtimeEnvelope{}}}
		operator := &arenaOperatorFlowStub{payload: ArenaOperatorPayload{Envelopes: []ArenaOperatorEnvelope{}}}
		server := NewServer(
			&shutdownPlayerRepo{player: player},
			nil,
			nil,
			NewHubRegistry(),
			WithArenaParticipantFlow(participant),
			WithArenaOperatorFlow(operator),
			WithHubCloseDelay(0),
		)
		httpServer := httptest.NewServer(server)
		defer httpServer.Close()

		conn, resp, err := dialArenaTestWS(t, httpServer.URL, ArenaRoleParticipant, token)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			defer resp.Body.Close()
		}
		writeTestEvent(t, conn, EventArenaResume, ArenaResumePayload{
			Role: ArenaRoleParticipant,
			Cursor: arenaws.RealtimeCursor{
				SchemaVersion: arenaws.ArenaRealtimeSchemaVersion, TournamentID: tournamentID,
				LastSequence: 4, ProjectionRevision: 4,
			},
		})
		message, err := DecodeArenaParticipantMessage(readArenaTestMessage(t, conn))
		require.NoError(t, err)
		require.NotNil(t, message.Participant)
		require.Equal(t, player.ID, participant.request.Principal.PlayerID)
		require.True(t, participant.request.Principal.Authenticated)
		require.Equal(t, tournamentID, participant.request.Principal.TournamentID)
		require.NoError(t, conn.Close(coderws.StatusNormalClosure, ""))

		conn, resp, err = dialArenaTestWS(t, httpServer.URL, ArenaRoleOperator, token)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			defer resp.Body.Close()
		}
		writeTestEvent(t, conn, EventArenaConnect, ArenaConnectPayload{Role: ArenaRoleOperator, TournamentID: tournamentID})
		operatorMessage, err := DecodeArenaOperatorMessage(readArenaTestMessage(t, conn))
		require.NoError(t, err)
		require.Equal(t, ArenaRejectionRoleMismatch, operatorMessage.Rejected.Code)
		require.Zero(t, operator.calls)
		_ = conn.CloseNow()
	})

	t.Run("operator resolver supplies the only operator authority", func(t *testing.T) {
		token := uuid.New()
		player := &domain.Player{ID: uuid.New(), Username: "operator", SessionToken: &token, Status: domain.PlayerStatusIdle}
		operator := &arenaOperatorFlowStub{payload: ArenaOperatorPayload{Envelopes: []ArenaOperatorEnvelope{}}}
		server := NewServer(
			&shutdownPlayerRepo{player: player},
			nil,
			nil,
			NewHubRegistry(),
			WithArenaOperatorFlow(operator),
			WithArenaOperatorPrincipalResolver(func(_ *http.Request, authenticated *domain.Player, scopedTournamentID uuid.UUID) (arenaws.OperatorRealtimePrincipal, bool) {
				return arenaws.OperatorRealtimePrincipal{
					Authenticated: true,
					PrincipalID:   authenticated.ID,
					Role:          arenaws.OperatorRealtimeRole,
					TournamentID:  scopedTournamentID,
				}, true
			}),
			WithHubCloseDelay(0),
		)
		httpServer := httptest.NewServer(server)
		defer httpServer.Close()

		conn, resp, err := dialArenaTestWS(t, httpServer.URL, ArenaRoleOperator, token)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			defer resp.Body.Close()
		}
		writeTestEvent(t, conn, EventArenaConnect, ArenaConnectPayload{Role: ArenaRoleOperator, TournamentID: tournamentID})
		message, err := DecodeArenaOperatorMessage(readArenaTestMessage(t, conn))
		require.NoError(t, err)
		require.NotNil(t, message.Operator)
		require.Equal(t, player.ID, operator.request.Principal.PrincipalID)
		require.Equal(t, arenaws.OperatorRealtimeRole, operator.request.Principal.Role)
		require.NoError(t, conn.Close(coderws.StatusNormalClosure, ""))
	})

	t.Run("cancellation snapshot precedes terminal close", func(t *testing.T) {
		public := &arenaPublicFlowStub{
			payload:  ArenaPublicPayload{Envelopes: []ArenaPublicEnvelope{}},
			released: make(chan struct{}),
		}
		deliveries := make(chan arenaws.CancellationDelivery, 2)
		snapshot := arenaPublicCancellationSnapshot(t, tournamentID)
		deliveries <- arenaws.CancellationDelivery{Kind: arenaws.CancellationDeliverySnapshot, Snapshot: &snapshot}
		deliveries <- arenaws.CancellationDelivery{
			Kind: arenaws.CancellationDeliveryTerminal,
			Public: &arenaws.CancellationPublicTerminal{
				TournamentID: tournamentID, State: arenaws.CancellationTerminalState,
			},
		}
		close(deliveries)
		terminal := &arenaTerminalFlowStub{subscription: &arenaTerminalSubscriptionStub{
			deliveries: deliveries,
			closed:     make(chan struct{}),
		}}
		server := NewServer(
			nil,
			nil,
			nil,
			NewHubRegistry(),
			WithArenaPublicFlow(public),
			WithArenaTerminalFlow(terminal),
			WithHubCloseDelay(0),
		)
		httpServer := httptest.NewServer(server)
		defer httpServer.Close()

		conn, resp, err := coderws.Dial(t.Context(), wsTestEndpoint(httpServer.URL)+"?arena_role=public", nil)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			defer resp.Body.Close()
		}
		writeTestEvent(t, conn, EventArenaConnect, ArenaConnectPayload{Role: ArenaRolePublic, TournamentID: tournamentID})

		initial, err := DecodeArenaPublicMessage(readArenaTestMessage(t, conn))
		require.NoError(t, err)
		require.NotNil(t, initial.Public)
		finalSnapshot, err := DecodeArenaPublicMessage(readArenaTestMessage(t, conn))
		require.NoError(t, err)
		require.NotNil(t, finalSnapshot.Public)
		require.Len(t, finalSnapshot.Public.Envelopes, 1)
		require.Equal(t, int64(8), finalSnapshot.Public.Envelopes[0].Sequence)
		terminalMessage, err := DecodeArenaPublicMessage(readArenaTestMessage(t, conn))
		require.NoError(t, err)
		require.NotNil(t, terminalMessage.Terminal)

		readCtx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		_, _, err = conn.Read(readCtx)
		require.Error(t, err)
		requireSignal(t, public.released, "public connection context was not released after terminal close")
	})
}

type arenaPublicFlowStub struct {
	mu          sync.Mutex
	payload     ArenaPublicPayload
	opened      chan ArenaPublicConnectionRequest
	released    chan struct{}
	releaseOnce sync.Once
	openCalls   int
}

func (s *arenaPublicFlowStub) OpenArenaPublic(ctx context.Context, request ArenaPublicConnectionRequest) (ArenaPublicPayload, error) {
	s.mu.Lock()
	s.openCalls++
	s.mu.Unlock()
	if s.opened != nil {
		s.opened <- request
	}
	if s.released != nil {
		go func() {
			<-ctx.Done()
			s.releaseOnce.Do(func() { close(s.released) })
		}()
	}
	return s.payload, nil
}

func (s *arenaPublicFlowStub) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openCalls
}

type arenaParticipantFlowStub struct {
	payload ArenaParticipantPayload
	request ArenaParticipantConnectionRequest
	calls   int
}

func (s *arenaParticipantFlowStub) OpenArenaParticipant(_ context.Context, request ArenaParticipantConnectionRequest) (ArenaParticipantPayload, error) {
	s.calls++
	s.request = request
	return s.payload, nil
}

type arenaOperatorFlowStub struct {
	payload ArenaOperatorPayload
	request ArenaOperatorConnectionRequest
	calls   int
}

func (s *arenaOperatorFlowStub) OpenArenaOperator(_ context.Context, request ArenaOperatorConnectionRequest) (ArenaOperatorPayload, error) {
	s.calls++
	s.request = request
	return s.payload, nil
}

type arenaTerminalFlowStub struct {
	mu           sync.Mutex
	request      ArenaTerminalSubscriptionRequest
	subscription ArenaTerminalSubscription
}

func (s *arenaTerminalFlowStub) SubscribeArenaTerminal(_ context.Context, request ArenaTerminalSubscriptionRequest) (ArenaTerminalSubscription, error) {
	s.mu.Lock()
	s.request = request
	s.mu.Unlock()
	return s.subscription, nil
}

func (s *arenaTerminalFlowStub) lastRequest() ArenaTerminalSubscriptionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.request
}

type arenaTerminalSubscriptionStub struct {
	deliveries <-chan arenaws.CancellationDelivery
	closed     chan struct{}
	closeOnce  sync.Once
}

func (s *arenaTerminalSubscriptionStub) Deliveries() <-chan arenaws.CancellationDelivery {
	return s.deliveries
}

func (s *arenaTerminalSubscriptionStub) Close() {
	s.closeOnce.Do(func() {
		if s.closed != nil {
			close(s.closed)
		}
	})
}

func dialArenaTestWS(t *testing.T, baseURL string, role ArenaRole, token uuid.UUID) (*coderws.Conn, *http.Response, error) {
	t.Helper()
	headers := http.Header{}
	headers.Add("Cookie", (&http.Cookie{Name: requestmeta.PlayerSessionCookieName, Value: token.String()}).String())
	return coderws.Dial(t.Context(), wsTestEndpoint(baseURL)+"?arena_role="+string(role), &coderws.DialOptions{HTTPHeader: headers})
}

func readArenaTestMessage(t *testing.T, conn *coderws.Conn) []byte {
	t.Helper()
	readCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	messageType, data, err := conn.Read(readCtx)
	require.NoError(t, err)
	require.Equal(t, coderws.MessageText, messageType)
	return data
}

func requireSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func arenaPublicCancellationSnapshot(t *testing.T, tournamentID uuid.UUID) arenaws.RealtimeEnvelope {
	t.Helper()
	snapshot, err := arenaws.NewPublicSnapshot(tournamentID, arenaws.PublicSnapshotInput{
		Revision:     8,
		LastSequence: 8,
		Tournament: arenaws.PublicTournamentInput{
			TournamentID: tournamentID,
			Preset:       "swiss",
			State:        arenaws.CancellationTerminalState,
		},
		Scoreboard:      []arenaws.PublicScoreboardEntryInput{},
		Bracket:         []arenaws.PublicBracketMatchInput{},
		LiveSeries:      []arenaws.PublicSeriesInput{},
		OfficialResults: []arenaws.PublicOfficialResultInput{},
	})
	require.NoError(t, err)
	envelope, err := arenaws.NewRealtimeEnvelope(arenaws.RealtimeEnvelopeMetadata{
		SchemaVersion:      arenaws.ArenaRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           8,
		EventID:            uuid.MustParse("72000000-0000-4000-8000-000000000008"),
		OccurredAt:         time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC),
		ProjectionRevision: 8,
	}, snapshot)
	require.NoError(t, err)
	return envelope
}

func TestServerShutdownLeavesQueuedClient(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	matchmaking := &shutdownMatchmaking{left: make(chan uuid.UUID, 1)}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		matchmaking,
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := dialTestWS(t, httpServer.URL, token)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	writeTestEvent(t, conn, EventJoinQueue, nil)
	require.Equal(t, EventQueueJoined, readTestEvent(t, conn).Type)

	server.Shutdown(context.Background())

	select {
	case playerID := <-matchmaking.left:
		require.Equal(t, player.ID, playerID)
	case <-time.After(time.Second):
		t.Fatal("queued client was not removed from matchmaking during shutdown")
	}
}

func TestServerSendDuelResumeIncludesOpponent(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	opponentID := uuid.New()
	duelID := uuid.New()
	deadline := time.Now().Add(time.Minute).UTC()
	c := &client{
		player: &domain.Player{
			ID: playerID,
		},
		send: make(chan []byte, 1),
		done: make(chan struct{}),
	}
	server := &Server{
		players: &shutdownPlayerRepo{
			player: &domain.Player{ID: opponentID, Username: "bob"},
		},
	}

	require.NoError(t, server.sendDuelResume(context.Background(), c, &duelusecase.ReconnectDecision{
		Duel: &domain.Duel{
			ID:        duelID,
			Player1ID: playerID,
			Player2ID: opponentID,
			Status:    domain.DuelStatusActive,
			Deadline:  deadline,
		},
		OpponentID:  opponentID,
		NewDeadline: deadline,
	}, false))

	select {
	case data := <-c.send:
		var got struct {
			Type    string            `json:"type"`
			Payload DuelResumePayload `json:"payload"`
		}
		require.NoError(t, json.Unmarshal(data, &got))
		require.Equal(t, EventDuelResume, got.Type)
		require.Equal(t, duelID, got.Payload.DuelID)
		require.Equal(t, opponentID, got.Payload.OpponentID)
		require.Equal(t, "bob", got.Payload.OpponentUsername)
		require.Equal(t, deadline, got.Payload.Deadline)
	case <-time.After(time.Second):
		t.Fatal("duel_resume event was not sent")
	}
}

func TestSubprotocolBearerAuthRejected(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := coderws.Dial(t.Context(), "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws", &coderws.DialOptions{
		Subprotocols: []string{"tpm.bearer." + token.String()},
	})
	if conn != nil {
		_ = conn.Close(coderws.StatusNormalClosure, "")
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, wsProblemContentType, resp.Header.Get("Content-Type"))
	if resp.Body != nil {
		require.NoError(t, resp.Body.Close())
	}
}

func TestQueryTokenAuthRejected(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
		WithLogger(newWebSocketTestLogger(t, &logs)),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := coderws.Dial(t.Context(), wsTestEndpoint(httpServer.URL)+"?token="+token.String(), nil)
	if conn != nil {
		_ = conn.Close(coderws.StatusNormalClosure, "")
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 401, resp.StatusCode)
	require.Equal(t, wsProblemContentType, resp.Header.Get("Content-Type"))
	if resp.Body != nil {
		require.NoError(t, resp.Body.Close())
	}

	rawLogs := logs.String()
	require.NotContains(t, rawLogs, token.String())
	entry := requireWebSocketSecurityLogEntry(t, rawLogs, "ws.auth")
	require.Equal(t, wsSecurityOutcomeFailure, entry["outcome"])
	require.Equal(t, string(domain.ErrorCodeInvalidSession), entry["error_code"])
	require.Equal(t, "query_token_rejected", entry["reason"])
}

func TestEmptyQueryTokenRejectedEvenWithCookie(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	players := &countingSessionPlayerRepo{}
	server := NewServer(
		players,
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	headers := http.Header{}
	headers.Add("Cookie", (&http.Cookie{Name: requestmeta.PlayerSessionCookieName, Value: token.String()}).String())
	conn, resp, err := coderws.Dial(t.Context(), wsTestEndpoint(httpServer.URL)+"?token=", &coderws.DialOptions{
		HTTPHeader: headers,
	})
	if conn != nil {
		_ = conn.Close(coderws.StatusNormalClosure, "")
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	if resp.Body != nil {
		require.NoError(t, resp.Body.Close())
	}
	require.Zero(t, players.sessionLookups)
}

func TestCookieAuth(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
		WithLogger(newWebSocketTestLogger(t, &logs)),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	headers := http.Header{}
	headers.Add("Cookie", (&http.Cookie{Name: requestmeta.PlayerSessionCookieName, Value: token.String()}).String())
	conn, resp, err := coderws.Dial(t.Context(), wsTestEndpoint(httpServer.URL), &coderws.DialOptions{
		HTTPHeader: headers,
	})
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	writeTestEvent(t, conn, EventPing, nil)
	require.Equal(t, EventPong, readTestEvent(t, conn).Type)

	rawLogs := logs.String()
	require.NotContains(t, rawLogs, token.String())
	entry := requireWebSocketSecurityLogEntry(t, rawLogs, "ws.auth")
	require.Equal(t, wsSecurityOutcomeSuccess, entry["outcome"])
	require.Equal(t, player.ID.String(), entry["player_id"])
}

func TestExpiredCookieAuthRejected(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	expiresAt := time.Now().Add(-time.Minute).UTC()
	player := &domain.Player{
		ID:               uuid.New(),
		Username:         "alice",
		SessionToken:     &token,
		SessionExpiresAt: &expiresAt,
		Status:           domain.PlayerStatusIdle,
	}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := dialTestWS(t, httpServer.URL, token)
	if conn != nil {
		_ = conn.Close(coderws.StatusNormalClosure, "")
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, wsProblemContentType, resp.Header.Get("Content-Type"))
	if resp.Body != nil {
		require.NoError(t, resp.Body.Close())
	}
}

func TestCrossOriginCookieAuthRejectedBeforeSessionLookup(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	token := uuid.New()
	players := &countingSessionPlayerRepo{}
	server := NewServer(
		players,
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
		WithLogger(newWebSocketTestLogger(t, &logs)),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	headers := http.Header{}
	headers.Set("Origin", "https://evil.example.com")
	headers.Add("Cookie", (&http.Cookie{Name: requestmeta.PlayerSessionCookieName, Value: token.String()}).String())
	conn, resp, err := coderws.Dial(t.Context(), wsTestEndpoint(httpServer.URL), &coderws.DialOptions{
		HTTPHeader: headers,
	})
	if conn != nil {
		_ = conn.Close(coderws.StatusNormalClosure, "")
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Equal(t, wsProblemContentType, resp.Header.Get("Content-Type"))
	if resp.Body != nil {
		require.NoError(t, resp.Body.Close())
	}
	require.Zero(t, players.sessionLookups)

	entry := requireWebSocketSecurityLogEntry(t, logs.String(), "ws.handshake")
	require.Equal(t, wsSecurityOutcomeFailure, entry["outcome"])
	require.Equal(t, "origin_not_allowed", entry["error_code"])
	require.Equal(t, "origin_not_allowed", entry["reason"])
}

func TestAllowedCrossOriginCookieAuth(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
		WithAcceptOptions(&coderws.AcceptOptions{OriginPatterns: []string{"https://app.example.com"}}),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	headers := http.Header{}
	headers.Set("Origin", "https://app.example.com")
	headers.Add("Cookie", (&http.Cookie{Name: requestmeta.PlayerSessionCookieName, Value: token.String()}).String())
	conn, resp, err := coderws.Dial(t.Context(), wsTestEndpoint(httpServer.URL), &coderws.DialOptions{
		HTTPHeader: headers,
	})
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	writeTestEvent(t, conn, EventPing, nil)
	require.Equal(t, EventPong, readTestEvent(t, conn).Type)
}

func TestRequireOriginRejectsMissingOriginBeforeSessionLookup(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	players := &countingSessionPlayerRepo{}
	server := NewServer(
		players,
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
		WithRequireOrigin(true),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	headers := http.Header{}
	headers.Add("Cookie", (&http.Cookie{Name: requestmeta.PlayerSessionCookieName, Value: token.String()}).String())
	conn, resp, err := coderws.Dial(t.Context(), wsTestEndpoint(httpServer.URL), &coderws.DialOptions{
		HTTPHeader: headers,
	})
	if conn != nil {
		_ = conn.Close(coderws.StatusNormalClosure, "")
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	if resp.Body != nil {
		require.NoError(t, resp.Body.Close())
	}
	require.Zero(t, players.sessionLookups)
}

func TestStaleSessionSecurityLogRedactsTokens(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	oldToken := uuid.New()
	newToken := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &oldToken,
		Status:       domain.PlayerStatusIdle,
	}
	players := newRotatingSessionPlayerRepo(player)
	server := NewServer(
		players,
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
		WithLogger(newWebSocketTestLogger(t, &logs)),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := dialTestWS(t, httpServer.URL, oldToken)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	players.setSessionToken(newToken)
	writeTestEvent(t, conn, EventPing, nil)

	event := readTestEvent(t, conn)
	require.Equal(t, EventError, event.Type)
	require.Equal(t, string(domain.ErrorCodeInvalidSession), event.Code)

	rawLogs := logs.String()
	require.NotContains(t, rawLogs, oldToken.String())
	require.NotContains(t, rawLogs, newToken.String())
	entry := requireWebSocketSecurityLogEntry(t, rawLogs, "ws.session")
	require.Equal(t, wsSecurityOutcomeFailure, entry["outcome"])
	require.Equal(t, string(domain.ErrorCodeInvalidSession), entry["error_code"])
	require.Equal(t, "stale_session", entry["reason"])
	require.Equal(t, player.ID.String(), entry["player_id"])
}

func TestActiveWebSocketExpiredSessionClosesWithoutClientMessage(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	players := newRotatingSessionPlayerRepo(player)
	server := NewServer(
		players,
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
		WithSessionMonitor(10*time.Millisecond, 20*time.Millisecond),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := dialTestWS(t, httpServer.URL, token)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	players.expireSession()
	event := readTestEvent(t, conn)
	require.Equal(t, EventError, event.Type)
	require.Equal(t, string(domain.ErrorCodeInvalidSession), event.Code)
}

func TestActiveWebSocketRotatedSessionClosesWithoutClientMessage(t *testing.T) {
	t.Parallel()

	oldToken := uuid.New()
	newToken := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &oldToken,
		Status:       domain.PlayerStatusIdle,
	}
	players := newRotatingSessionPlayerRepo(player)
	server := NewServer(
		players,
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
		WithSessionMonitor(10*time.Millisecond, 20*time.Millisecond),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := dialTestWS(t, httpServer.URL, oldToken)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	players.setSessionToken(newToken)
	event := readTestEvent(t, conn)
	require.Equal(t, EventError, event.Type)
	require.Equal(t, string(domain.ErrorCodeInvalidSession), event.Code)
}

func TestDuelResumeSendFailureClosesReconnectSocket(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	opponentID := uuid.New()
	duel := &domain.Duel{
		ID:        uuid.New(),
		Player1ID: playerID,
		Player2ID: opponentID,
		Status:    domain.DuelStatusActive,
		StartedAt: time.Now().UTC(),
		Deadline:  time.Now().Add(time.Minute).UTC(),
	}
	reconnect := &restoreReconnectManager{
		decision: &duelusecase.ReconnectDecision{
			Duel:        duel,
			OpponentID:  opponentID,
			NewDeadline: duel.Deadline,
		},
	}
	server := NewServer(
		&publishMatchPlayerRepo{players: map[uuid.UUID]*domain.Player{
			playerID:   {ID: playerID, Username: "alice"},
			opponentID: {ID: opponentID, Username: "bob"},
		}},
		nil,
		nil,
		NewHubRegistry(),
		WithReconnectManager(reconnect),
		WithHubCloseDelay(0),
	)
	c := &client{
		player: &domain.Player{ID: playerID, Username: "alice"},
		send:   make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	c.send <- []byte(`{"type":"already_full"}`)

	require.True(t, server.handleActiveDuelRestore(context.Background(), c))
	require.True(t, c.closed.Load())
}

func TestHandshakeRateLimitSecurityLogUsesResolvedClientIP(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	server := NewServer(
		nil,
		nil,
		nil,
		NewHubRegistry(),
		WithHandshakeRateLimiter(rejectingHandshakeLimiter{}),
		WithClientIPResolver(func(*http.Request) string { return "203.0.113.42" }),
		WithLogger(newWebSocketTestLogger(t, &logs)),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := coderws.Dial(t.Context(), wsTestEndpoint(httpServer.URL), nil)
	if conn != nil {
		_ = conn.Close(coderws.StatusNormalClosure, "")
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Equal(t, wsProblemContentType, resp.Header.Get("Content-Type"))
	if resp.Body != nil {
		require.NoError(t, resp.Body.Close())
	}

	entry := requireWebSocketSecurityLogEntry(t, logs.String(), "ws.handshake")
	require.Equal(t, wsSecurityOutcomeRateLimited, entry["outcome"])
	require.Equal(t, "rate_limited", entry["error_code"])
	require.Equal(t, "handshake_rate_limit", entry["reason"])
	require.Equal(t, "203.0.113.42", entry["client_ip"])
}

func TestServerShutdownStopsBackgroundTimers(t *testing.T) {
	t.Parallel()

	reconnect := &noopReconnectManager{}
	timers := &recordingTimerStopper{}
	server := NewServer(
		nil,
		nil,
		nil,
		NewHubRegistry(),
		WithReconnectManager(reconnect),
		WithTimerStopper(timers),
		WithHubCloseDelay(0),
	)

	server.Shutdown(context.Background())

	require.Equal(t, 1, reconnect.stopAllCalls)
	require.Equal(t, 1, timers.stopAllCalls)
}

func dialTestWS(t *testing.T, baseURL string, token uuid.UUID) (*coderws.Conn, *http.Response, error) {
	t.Helper()
	headers := http.Header{}
	headers.Add("Cookie", (&http.Cookie{Name: requestmeta.PlayerSessionCookieName, Value: token.String()}).String())
	return coderws.Dial(t.Context(), wsTestEndpoint(baseURL), &coderws.DialOptions{
		HTTPHeader: headers,
	})
}

func wsTestEndpoint(baseURL string) string {
	return "ws" + strings.TrimPrefix(baseURL, "http") + "/ws"
}

func TestSubprotocolWithoutCookieRejected(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	_, resp, err := coderws.Dial(t.Context(), "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws", &coderws.DialOptions{
		Subprotocols: []string{"tpm.bearer.not-a-uuid"},
	})
	require.Error(t, err)
	if resp != nil {
		require.Equal(t, 401, resp.StatusCode)
		require.Equal(t, wsProblemContentType, resp.Header.Get("Content-Type"))
		if resp.Body != nil {
			resp.Body.Close()
		}
	}
}

func TestHandshakeMethodErrorUsesProblemJSON(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, nil, nil, NewHubRegistry())
	req := httptest.NewRequest(http.MethodPost, "/ws", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Equal(t, http.MethodGet, rr.Header().Get("Allow"))
	require.Equal(t, wsProblemContentType, rr.Header().Get("Content-Type"))
}

// TestServerFlagSubmitRequiresConnectionDuel verifies the C2 fix: a client
// cannot drive flag_submit at an arbitrary duel via the wire payload. A
// connection with no bound duel must reject before the usecase layer.
func TestServerFlagSubmitRequiresConnectionDuel(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	flags := &countingFlagSubmitter{}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		flags,
		NewHubRegistry(),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := dialTestWS(t, httpServer.URL, token)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	foreignDuelID := uuid.New()
	writeTestEvent(t, conn, EventFlagSubmit, map[string]any{
		"duel_id": foreignDuelID,
		"flag":    "flag{maliciously-guessed}",
	})

	event := readTestEvent(t, conn)
	require.Equal(t, EventError, event.Type)
	require.Equal(t, ErrorInvalidPayload, event.Code)
	require.Zero(t, flags.submitCalls, "usecase must NOT receive a submission for a duel the client merely named")
}

// TestServerSurrenderRequiresConnectionDuel is the surrender-side mirror of
// the C2 hardening test above. A connection with no bound duel must not be
// able to surrender a foreign duel via the wire payload.
func TestServerSurrenderRequiresConnectionDuel(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "bob",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	reconnect := &noopReconnectManager{}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithReconnectManager(reconnect),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := dialTestWS(t, httpServer.URL, token)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	foreignDuelID := uuid.New()
	writeTestEvent(t, conn, EventSurrender, map[string]any{
		"duel_id": foreignDuelID,
	})

	event := readTestEvent(t, conn)
	require.Equal(t, EventError, event.Type)
	require.Equal(t, ErrorInvalidPayload, event.Code)
	require.Zero(t, reconnect.forfeitCalls, "FinalizePlayerForfeit must NOT be invoked for a duel the client merely named")
}

type countingFlagSubmitter struct {
	submitCalls int
}

func (f *countingFlagSubmitter) SubmitFlag(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string) (duelusecase.Result, error) {
	f.submitCalls++
	return duelusecase.Result{}, nil
}

// noopReconnectManager is a benign stub for tests that exercise ServeHTTP
// without needing real reconnect semantics: every probe returns "no decision"
// so the handshake bypasses the reconnect/restore branches and lands in
// readPump, while we still get to assert that mutating calls (forfeit) were
// or were not invoked.
type noopReconnectManager struct {
	forfeitCalls int
	stopAllCalls int
}

var _ ReconnectManager = (*noopReconnectManager)(nil)

func (m *noopReconnectManager) StartDuelTimer(*domain.Duel) {}

func (m *noopReconnectManager) BeginDisconnect(context.Context, uuid.UUID, uuid.UUID) {}

func (m *noopReconnectManager) HandleDisconnect(context.Context, uuid.UUID, uuid.UUID) {}

func (m *noopReconnectManager) ConsumeReconnect(
	context.Context,
	uuid.UUID,
) (*duelusecase.ReconnectDecision, error) {
	return nil, nil
}

func (m *noopReconnectManager) ActiveDuel(
	context.Context,
	uuid.UUID,
) (*duelusecase.ReconnectDecision, error) {
	return nil, nil
}

func (m *noopReconnectManager) DuelPaused(uuid.UUID) bool { return false }

func (m *noopReconnectManager) FinalizeDraw(context.Context, uuid.UUID) (*domain.Duel, error) {
	return nil, nil
}

func (m *noopReconnectManager) FinalizePlayerForfeit(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*domain.Duel, error) {
	m.forfeitCalls++
	return nil, nil
}

func (m *noopReconnectManager) CloseDuel(uuid.UUID) {}

func (m *noopReconnectManager) StopAll() {
	m.stopAllCalls++
}

type recordingTimerStopper struct {
	stopAllCalls int
}

func (s *recordingTimerStopper) StopAll() {
	s.stopAllCalls++
}

type rejectingHandshakeLimiter struct{}

func (rejectingHandshakeLimiter) Allow(string) bool { return false }

func (rejectingHandshakeLimiter) RetryAfter() string { return "1" }

type restoreReconnectManager struct {
	decision *duelusecase.ReconnectDecision
}

var _ ReconnectManager = (*restoreReconnectManager)(nil)

func (m *restoreReconnectManager) StartDuelTimer(*domain.Duel) {}

func (m *restoreReconnectManager) BeginDisconnect(context.Context, uuid.UUID, uuid.UUID) {}

func (m *restoreReconnectManager) HandleDisconnect(context.Context, uuid.UUID, uuid.UUID) {}

func (m *restoreReconnectManager) ConsumeReconnect(
	context.Context,
	uuid.UUID,
) (*duelusecase.ReconnectDecision, error) {
	return nil, nil
}

func (m *restoreReconnectManager) ActiveDuel(
	context.Context,
	uuid.UUID,
) (*duelusecase.ReconnectDecision, error) {
	return m.decision, nil
}

func (m *restoreReconnectManager) DuelPaused(uuid.UUID) bool { return false }

func (m *restoreReconnectManager) FinalizeDraw(context.Context, uuid.UUID) (*domain.Duel, error) {
	return nil, nil
}

func (m *restoreReconnectManager) FinalizePlayerForfeit(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*domain.Duel, error) {
	return nil, nil
}

func (m *restoreReconnectManager) CloseDuel(uuid.UUID) {}

func (m *restoreReconnectManager) StopAll() {}

func TestServerCleanupClientSkipsDisconnectForFastReplacement(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	duelID := uuid.New()
	reconnect := &recordingReconnectManager{}
	server := NewServer(
		nil,
		nil,
		nil,
		NewHubRegistry(),
		WithReconnectManager(reconnect),
		WithDisconnectGrace(20*time.Millisecond),
	)
	oldClient := newCleanupTestClient(playerID, duelID)
	replacement := newCleanupTestClient(playerID, duelID)
	server.clients.Store(playerID, oldClient)

	done := make(chan struct{})
	go func() {
		server.cleanupClient(context.Background(), oldClient)
		close(done)
	}()

	time.Sleep(5 * time.Millisecond)
	server.clients.Store(playerID, replacement)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not finish after disconnect grace")
	}
	require.Equal(t, []recordedDisconnect{{duelID: duelID, playerID: playerID}}, reconnect.beginDisconnects)
	require.Empty(t, reconnect.disconnects)
	require.True(t, oldClient.closed.Load())

	current, ok := server.clientByPlayer(playerID)
	require.True(t, ok)
	require.Same(t, replacement, current)
}

func TestServerCleanupClientDisconnectsAfterGraceWithoutReplacement(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	duelID := uuid.New()
	reconnect := &recordingReconnectManager{}
	server := NewServer(
		nil,
		nil,
		nil,
		NewHubRegistry(),
		WithReconnectManager(reconnect),
		WithDisconnectGrace(10*time.Millisecond),
	)
	c := newCleanupTestClient(playerID, duelID)
	server.clients.Store(playerID, c)

	server.cleanupClient(context.Background(), c)

	require.Equal(t, []recordedDisconnect{{duelID: duelID, playerID: playerID}}, reconnect.beginDisconnects)
	require.Equal(t, []recordedDisconnect{{duelID: duelID, playerID: playerID}}, reconnect.disconnects)
	require.True(t, c.closed.Load())
	_, ok := server.clientByPlayer(playerID)
	require.False(t, ok)
}

func newCleanupTestClient(playerID, duelID uuid.UUID) *client {
	c := &client{
		player: &domain.Player{ID: playerID},
		send:   make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	c.setDuel(duelID)
	return c
}

type rotatingSessionPlayerRepo struct {
	mu     sync.RWMutex
	player domain.Player
}

func newRotatingSessionPlayerRepo(player *domain.Player) *rotatingSessionPlayerRepo {
	repo := &rotatingSessionPlayerRepo{}
	if player != nil {
		repo.player = *player
	}
	return repo
}

func (r *rotatingSessionPlayerRepo) setSessionToken(token uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.player.SessionToken = &token
}

func (r *rotatingSessionPlayerRepo) expireSession() {
	r.mu.Lock()
	defer r.mu.Unlock()
	expiresAt := time.Unix(0, 0).UTC()
	r.player.SessionExpiresAt = &expiresAt
}

func (r *rotatingSessionPlayerRepo) Create(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *rotatingSessionPlayerRepo) JoinByUsername(context.Context, string, uuid.UUID, time.Time) (*domain.Player, error) {
	panic("unused")
}

func (r *rotatingSessionPlayerRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Player, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.player.ID != id {
		return nil, nil
	}
	out := r.player
	return validSessionTestPlayer(&out), nil
}

func (r *rotatingSessionPlayerRepo) GetByUsername(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *rotatingSessionPlayerRepo) GetBySessionToken(_ context.Context, token uuid.UUID) (*domain.Player, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.player.SessionToken == nil || *r.player.SessionToken != token {
		return nil, nil
	}
	out := r.player
	snapshot := validSessionTestPlayer(&out)
	if testSessionExpired(snapshot) {
		return nil, nil
	}
	return snapshot, nil
}

func (r *rotatingSessionPlayerRepo) UpdateSessionToken(context.Context, uuid.UUID, *uuid.UUID, *time.Time) (*domain.Player, error) {
	panic("unused")
}

func (r *rotatingSessionPlayerRepo) UpdateStatus(context.Context, uuid.UUID, domain.PlayerStatus) (*domain.Player, error) {
	panic("unused")
}

func (r *rotatingSessionPlayerRepo) UpdateStatusIfCurrent(
	context.Context,
	uuid.UUID,
	domain.PlayerStatus,
	domain.PlayerStatus,
) (*domain.Player, bool, error) {
	panic("unused")
}

type shutdownPlayerRepo struct {
	player *domain.Player
}

func (r *shutdownPlayerRepo) Create(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *shutdownPlayerRepo) JoinByUsername(context.Context, string, uuid.UUID, time.Time) (*domain.Player, error) {
	panic("unused")
}

func (r *shutdownPlayerRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Player, error) {
	if r.player == nil || r.player.ID != id {
		return nil, nil
	}
	out := *r.player
	return validSessionTestPlayer(&out), nil
}

func (r *shutdownPlayerRepo) GetByUsername(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *shutdownPlayerRepo) GetBySessionToken(_ context.Context, token uuid.UUID) (*domain.Player, error) {
	if r.player == nil || r.player.SessionToken == nil || *r.player.SessionToken != token {
		return nil, nil
	}
	out := *r.player
	snapshot := validSessionTestPlayer(&out)
	if testSessionExpired(snapshot) {
		return nil, nil
	}
	return snapshot, nil
}

func (r *shutdownPlayerRepo) UpdateSessionToken(context.Context, uuid.UUID, *uuid.UUID, *time.Time) (*domain.Player, error) {
	panic("unused")
}

func (r *shutdownPlayerRepo) UpdateStatus(context.Context, uuid.UUID, domain.PlayerStatus) (*domain.Player, error) {
	panic("unused")
}

func (r *shutdownPlayerRepo) UpdateStatusIfCurrent(
	context.Context,
	uuid.UUID,
	domain.PlayerStatus,
	domain.PlayerStatus,
) (*domain.Player, bool, error) {
	panic("unused")
}

type countingSessionPlayerRepo struct {
	sessionLookups int
}

func (r *countingSessionPlayerRepo) Create(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *countingSessionPlayerRepo) JoinByUsername(context.Context, string, uuid.UUID, time.Time) (*domain.Player, error) {
	panic("unused")
}

func (r *countingSessionPlayerRepo) GetByID(context.Context, uuid.UUID) (*domain.Player, error) {
	panic("unused")
}

func (r *countingSessionPlayerRepo) GetByUsername(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *countingSessionPlayerRepo) GetBySessionToken(context.Context, uuid.UUID) (*domain.Player, error) {
	r.sessionLookups++
	return nil, nil
}

func (r *countingSessionPlayerRepo) UpdateSessionToken(context.Context, uuid.UUID, *uuid.UUID, *time.Time) (*domain.Player, error) {
	panic("unused")
}

func validSessionTestPlayer(player *domain.Player) *domain.Player {
	if player == nil || player.SessionToken == nil || player.SessionExpiresAt != nil {
		return player
	}
	expiresAt := time.Now().Add(time.Hour).UTC()
	out := *player
	out.SessionExpiresAt = &expiresAt
	return &out
}

func testSessionExpired(player *domain.Player) bool {
	return player == nil || player.SessionExpiresAt == nil || !player.SessionExpiresAt.After(time.Now().UTC())
}

func (r *countingSessionPlayerRepo) UpdateStatus(context.Context, uuid.UUID, domain.PlayerStatus) (*domain.Player, error) {
	panic("unused")
}

func (r *countingSessionPlayerRepo) UpdateStatusIfCurrent(
	context.Context,
	uuid.UUID,
	domain.PlayerStatus,
	domain.PlayerStatus,
) (*domain.Player, bool, error) {
	panic("unused")
}

type shutdownMatchmaking struct {
	left chan uuid.UUID
}

func (m *shutdownMatchmaking) JoinQueue(context.Context, uuid.UUID) (*duelusecase.MatchResult, error) {
	return nil, nil
}

func (m *shutdownMatchmaking) LeaveQueue(_ context.Context, playerID uuid.UUID) error {
	m.left <- playerID
	return nil
}

func newWebSocketTestLogger(t *testing.T, logs *bytes.Buffer) logkit.Logger {
	t.Helper()

	log, err := logkit.New(
		logkit.WithLevel(logkit.DebugLevel),
		logkit.WithSyncWriter(logs),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, log.Close())
	})
	return log
}

func requireWebSocketSecurityLogEntry(t *testing.T, raw, event string) map[string]any {
	t.Helper()

	raw = strings.TrimSpace(raw)
	require.NotEmpty(t, raw)
	for _, line := range strings.Split(raw, "\n") {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["message"] == "security event" && entry["event"] == event {
			return entry
		}
	}
	t.Fatalf("security event %q not found in logs: %s", event, raw)
	return nil
}

func writeTestEvent(t *testing.T, conn *coderws.Conn, typ string, payload any) {
	t.Helper()

	data, err := json.Marshal(Event{Type: typ, Payload: payload})
	require.NoError(t, err)
	writeCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, conn.Write(writeCtx, coderws.MessageText, data))
}

func readTestEvent(t *testing.T, conn *coderws.Conn) Event {
	t.Helper()

	readCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	msgType, data, err := conn.Read(readCtx)
	require.NoError(t, err)
	require.Equal(t, coderws.MessageText, msgType)

	var event Event
	require.NoError(t, json.Unmarshal(data, &event))
	return event
}
