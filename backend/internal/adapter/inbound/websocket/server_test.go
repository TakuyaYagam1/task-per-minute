package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/requestmeta"
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentsnapshot "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentsnapshotmocks "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound/mocks"
)

func TestServerRoleAuthenticationBoundaries(t *testing.T) {
	t.Parallel()
	tournamentID := tournamentSourceID(201)
	playerID := tournamentSourceID(202)
	operatorID := tournamentSourceID(203)
	server := tournamentWebSocketTestServer(t, tournamentID, playerID,
		WithTournamentOperatorSessionResolver(func(r *http.Request, scopedID uuid.UUID) (TournamentOperatorSession, bool) {
			cookie, err := r.Cookie("tpm_admin_access")
			if err != nil || cookie.Value != "admin-access" || scopedID != tournamentID {
				return TournamentOperatorSession{}, false
			}
			return TournamentOperatorSession{
				Principal: tournamentws.OperatorRealtimePrincipal{
					Authenticated: true,
					PrincipalID:   operatorID,
					Role:          tournamentws.OperatorRealtimeRole,
					TournamentID:  scopedID,
				},
				ExpiresAt: time.Now().Add(time.Hour),
				Validate:  func(context.Context) bool { return true },
			}, true
		}),
	)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	t.Run("public requires no session", func(t *testing.T) {
		connection, response, err := coderws.Dial(context.Background(), tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID), nil)
		require.NoError(t, err)
		if response != nil && response.Body != nil {
			require.NoError(t, response.Body.Close())
		}
		defer func() { _ = connection.CloseNow() }()
		_, data, err := connection.Read(context.Background())
		require.NoError(t, err)
		message, err := DecodeTournamentPublicMessage(data)
		require.NoError(t, err)
		require.NotNil(t, message.Public)
	})

	t.Run("participant requires player session", func(t *testing.T) {
		connection, response, err := coderws.Dial(context.Background(), tournamentWebSocketURL(httpServer.URL, TournamentRoleParticipant, tournamentID), nil)
		require.Error(t, err)
		require.Nil(t, connection)
		require.NotNil(t, response)
		require.Equal(t, http.StatusUnauthorized, response.StatusCode)
		require.NoError(t, response.Body.Close())
	})

	t.Run("operator uses admin principal without player session", func(t *testing.T) {
		connection, response, err := coderws.Dial(context.Background(), tournamentWebSocketURL(httpServer.URL, TournamentRoleOperator, tournamentID), &coderws.DialOptions{
			HTTPHeader: http.Header{
				"Cookie": {(&http.Cookie{Name: "tpm_admin_access", Value: "admin-access"}).String()},
				"Origin": {httpServer.URL},
			},
		})
		require.NoError(t, err)
		if response != nil && response.Body != nil {
			require.NoError(t, response.Body.Close())
		}
		defer func() { _ = connection.CloseNow() }()
		_, data, err := connection.Read(context.Background())
		require.NoError(t, err)
		message, err := DecodeTournamentOperatorMessage(data)
		require.NoError(t, err)
		require.NotNil(t, message.Operator)
		require.Equal(t, operatorID, message.Operator.Envelope.Operator.Waves[0].Members[0].ParticipantID)
	})
}

func TestServerShutdownClosesTournamentConnections(t *testing.T) {
	t.Parallel()
	tournamentID := tournamentSourceID(211)
	server := tournamentWebSocketTestServer(t, tournamentID, tournamentSourceID(212))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	connection, response, err := coderws.Dial(context.Background(), tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID), nil)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	defer func() { _ = connection.CloseNow() }()
	_, _, err = connection.Read(context.Background())
	require.NoError(t, err)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server.Shutdown(shutdownCtx)
	require.NoError(t, shutdownCtx.Err())

	readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	_, _, err = connection.Read(readCtx)
	require.Error(t, err)

	_, response, err = coderws.Dial(context.Background(), tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID), nil)
	require.Error(t, err)
	require.NotNil(t, response)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestServerRejectsCredentialQueryParameters(t *testing.T) {
	t.Parallel()
	tournamentID := tournamentSourceID(221)
	server := tournamentWebSocketTestServer(t, tournamentID, tournamentSourceID(222))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	url := tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID) + "?access_token=private"
	connection, response, err := coderws.Dial(context.Background(), url, nil)
	require.Error(t, err)
	require.Nil(t, connection)
	require.NotNil(t, response)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestTournamentResumeQueryIsBoundedAndNonCredential(t *testing.T) {
	t.Parallel()

	resumeID := tournamentSourceID(229)
	tests := []struct {
		name string
		url  string
		want uuid.UUID
		bad  bool
	}{
		{name: "absent", url: "https://example.test/api/v1/tournaments/id/realtime"},
		{name: "valid", url: "https://example.test/api/v1/tournaments/id/realtime?resume_id=" + resumeID.String(), want: resumeID},
		{name: "duplicate", url: "https://example.test/api/v1/tournaments/id/realtime?resume_id=" + resumeID.String() + "&resume_id=" + resumeID.String(), bad: true},
		{name: "malformed", url: "https://example.test/api/v1/tournaments/id/realtime?resume_id=not-an-id", bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, tt.url, nil)
			resume, err := tournamentResumeFromRequest(request)
			if tt.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, resume.ID)
		})
	}
}

func TestServerRejectsLegacyQueryScope(t *testing.T) {
	t.Parallel()
	tournamentID := tournamentSourceID(231)
	server := tournamentWebSocketTestServer(t, tournamentID, tournamentSourceID(232))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws?tournament_role=public&tournament_id=" + tournamentID.String()
	connection, response, err := coderws.Dial(context.Background(), url, nil)
	require.Error(t, err)
	require.Nil(t, connection)
	require.NotNil(t, response)
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestServerClosesAfterMalformedFrameBudget(t *testing.T) {
	tournamentID := tournamentSourceID(234)
	server := tournamentWebSocketTestServer(t, tournamentID, tournamentSourceID(235))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	dialCtx, dialCancel := context.WithTimeout(t.Context(), time.Second)
	connection, response, err := coderws.Dial(
		dialCtx, tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID), nil,
	)
	dialCancel()
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.CloseNow() })
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	initialCtx, initialCancel := context.WithTimeout(t.Context(), time.Second)
	_, _, err = connection.Read(initialCtx)
	initialCancel()
	require.NoError(t, err)
	for range maximumMalformedFrames {
		writeCtx, writeCancel := context.WithTimeout(t.Context(), time.Second)
		writeErr := connection.Write(writeCtx, coderws.MessageText, []byte(`{`))
		writeCancel()
		require.NoError(t, writeErr)
	}
	rejectionCtx, rejectionCancel := context.WithTimeout(t.Context(), time.Second)
	_, data, err := connection.Read(rejectionCtx)
	rejectionCancel()
	require.NoError(t, err)
	message, err := DecodeTournamentPublicMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Rejected)
	require.Equal(t, TournamentRejectionInvalidFrame, message.Rejected.Code)
	closeCtx, closeCancel := context.WithTimeout(t.Context(), time.Second)
	_, _, err = connection.Read(closeCtx)
	closeCancel()
	require.Error(t, err)
}

func TestServerClosesAfterInboundCommandRateBudget(t *testing.T) {
	tournamentID := tournamentSourceID(236)
	server := tournamentWebSocketTestServer(t, tournamentID, tournamentSourceID(237))
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	connection, response, err := coderws.Dial(
		t.Context(), tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID), nil,
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	_, _, err = connection.Read(t.Context())
	require.NoError(t, err)
	for range maximumInboundCommands + 1 {
		require.NoError(t, connection.Write(t.Context(), coderws.MessageText, []byte(`{}`)))
	}
	_, data, err := connection.Read(t.Context())
	require.NoError(t, err)
	message, err := DecodeTournamentPublicMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Rejected)
	require.Equal(t, TournamentRejectionRateLimited, message.Rejected.Code)
	_, _, err = connection.Read(t.Context())
	require.Error(t, err)
}

func TestServerClosesOperatorConnectionWhenSessionBecomesInvalid(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(241)
	operatorID := tournamentSourceID(242)
	var valid atomic.Bool
	valid.Store(true)
	server := tournamentWebSocketTestServer(
		t,
		tournamentID,
		tournamentSourceID(243),
		WithSessionMonitor(10*time.Millisecond, 100*time.Millisecond),
		WithTournamentOperatorSessionResolver(operatorSessionResolver(
			tournamentID,
			operatorID,
			time.Now().Add(time.Hour),
			func(context.Context) bool { return valid.Load() },
		)),
	)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	connection := dialTournamentOperator(t, httpServer.URL, tournamentID)
	defer func() { _ = connection.CloseNow() }()
	_, _, err := connection.Read(t.Context())
	require.NoError(t, err)

	valid.Store(false)
	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, data, err := connection.Read(readCtx)
	require.NoError(t, err)
	message, err := DecodeTournamentOperatorMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Rejected)
	require.Equal(t, TournamentRejectionUnauthenticated, message.Rejected.Code)
}

func TestServerClosesOperatorConnectionAtSessionExpiry(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(251)
	server := tournamentWebSocketTestServer(
		t,
		tournamentID,
		tournamentSourceID(252),
		WithSessionMonitor(time.Hour, 100*time.Millisecond),
		WithTournamentOperatorSessionResolver(operatorSessionResolver(
			tournamentID,
			tournamentSourceID(253),
			time.Now().Add(250*time.Millisecond),
			func(context.Context) bool { return true },
		)),
	)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	connection := dialTournamentOperator(t, httpServer.URL, tournamentID)
	defer func() { _ = connection.CloseNow() }()
	_, _, err := connection.Read(t.Context())
	require.NoError(t, err)

	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, data, err := connection.Read(readCtx)
	require.NoError(t, err)
	message, err := DecodeTournamentOperatorMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Rejected)
	require.Equal(t, TournamentRejectionUnauthenticated, message.Rejected.Code)
}

func TestServerKeepsParticipantConnectionAfterSuccessfulSessionCheck(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(254)
	playerID := tournamentSourceID(255)
	token := tournamentSourceID(256)
	expiresAt := time.Now().Add(time.Hour)
	player := participantSessionPlayer(playerID, token, expiresAt)
	var reads atomic.Int32
	players := middlewaremocks.NewMockPlayerSessionReader(t)
	players.EXPECT().GetBySessionToken(mock.Anything, token).RunAndReturn(func(context.Context, uuid.UUID) (*domain.Player, error) {
		reads.Add(1)
		return player, nil
	}).Maybe()
	server := tournamentWebSocketTestServerWithPlayerReader(
		t,
		players,
		tournamentID,
		playerID,
		WithSessionMonitor(10*time.Millisecond, 100*time.Millisecond),
	)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	connection := dialTournamentParticipant(t, httpServer.URL, tournamentID, token)
	defer func() { _ = connection.CloseNow() }()
	_, data, err := connection.Read(t.Context())
	require.NoError(t, err)
	message, err := DecodeTournamentParticipantMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Participant)
	require.Eventually(t, func() bool { return reads.Load() >= 2 }, time.Second, 10*time.Millisecond)
}

func TestServerClosesParticipantConnectionWhenSessionTokenBecomesStale(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(257)
	playerID := tournamentSourceID(258)
	token := tournamentSourceID(259)
	rotatedToken := tournamentSourceID(260)
	expiresAt := time.Now().Add(time.Hour)
	var reads atomic.Int32
	players := middlewaremocks.NewMockPlayerSessionReader(t)
	players.EXPECT().GetBySessionToken(mock.Anything, token).RunAndReturn(func(context.Context, uuid.UUID) (*domain.Player, error) {
		if reads.Add(1) == 1 {
			return participantSessionPlayer(playerID, token, expiresAt), nil
		}
		return participantSessionPlayer(playerID, rotatedToken, expiresAt), nil
	}).Maybe()
	server := tournamentWebSocketTestServerWithPlayerReader(
		t,
		players,
		tournamentID,
		playerID,
		WithSessionMonitor(10*time.Millisecond, 100*time.Millisecond),
	)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	connection := dialTournamentParticipant(t, httpServer.URL, tournamentID, token)
	defer func() { _ = connection.CloseNow() }()
	_, _, err := connection.Read(t.Context())
	require.NoError(t, err)

	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, data, err := connection.Read(readCtx)
	require.NoError(t, err)
	message, err := DecodeTournamentParticipantMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Rejected)
	require.Equal(t, TournamentRejectionUnauthenticated, message.Rejected.Code)
}

func TestServerClosesParticipantConnectionAtSessionExpiry(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(264)
	playerID := tournamentSourceID(265)
	token := tournamentSourceID(266)
	expiresAt := time.Now().Add(250 * time.Millisecond)
	player := participantSessionPlayer(playerID, token, expiresAt)
	players := middlewaremocks.NewMockPlayerSessionReader(t)
	players.EXPECT().GetBySessionToken(mock.Anything, token).Return(player, nil).Maybe()
	server := tournamentWebSocketTestServerWithPlayerReader(
		t,
		players,
		tournamentID,
		playerID,
		WithSessionMonitor(time.Hour, 100*time.Millisecond),
	)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	connection := dialTournamentParticipant(t, httpServer.URL, tournamentID, token)
	defer func() { _ = connection.CloseNow() }()
	_, _, err := connection.Read(t.Context())
	require.NoError(t, err)

	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, data, err := connection.Read(readCtx)
	require.NoError(t, err)
	message, err := DecodeTournamentParticipantMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Rejected)
	require.Equal(t, TournamentRejectionUnauthenticated, message.Rejected.Code)
}

func TestServerRejectsConnectionsAtGlobalCapacity(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(261)
	server := tournamentWebSocketTestServer(
		t,
		tournamentID,
		tournamentSourceID(262),
		WithConnectionLimits(1, 1),
	)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	first, response, err := coderws.Dial(
		context.Background(),
		tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID),
		nil,
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	defer func() { _ = first.CloseNow() }()
	_, _, err = first.Read(t.Context())
	require.NoError(t, err)

	second, response, err := coderws.Dial(
		context.Background(),
		tournamentWebSocketURL(httpServer.URL, TournamentRolePublic, tournamentID),
		nil,
	)
	require.Error(t, err)
	require.Nil(t, second)
	require.NotNil(t, response)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestServerRejectsConnectionsAtPrincipalCapacity(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(271)
	server := tournamentWebSocketTestServer(
		t,
		tournamentID,
		tournamentSourceID(272),
		WithConnectionLimits(8, 1),
		WithTournamentOperatorSessionResolver(operatorSessionResolver(
			tournamentID,
			tournamentSourceID(273),
			time.Now().Add(time.Hour),
			func(context.Context) bool { return true },
		)),
	)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	first := dialTournamentOperator(t, httpServer.URL, tournamentID)
	defer func() { _ = first.CloseNow() }()
	_, _, err := first.Read(t.Context())
	require.NoError(t, err)

	second, response, err := coderws.Dial(
		context.Background(),
		tournamentWebSocketURL(httpServer.URL, TournamentRoleOperator, tournamentID),
		&coderws.DialOptions{HTTPHeader: http.Header{
			"Cookie": {(&http.Cookie{Name: "tpm_admin_access", Value: "admin-access"}).String()},
			"Origin": {httpServer.URL},
		}},
	)
	require.Error(t, err)
	require.Nil(t, second)
	require.NotNil(t, response)
	require.Equal(t, http.StatusTooManyRequests, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestConnectionReservationReleaseIsIdempotent(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, WithConnectionLimits(1, 1))
	scope := tournamentConnectionScope{Role: TournamentRolePublic, TournamentID: uuid.New()}
	release, limit := server.reserveConnection(scope, tournamentConnectionPrincipal{})
	require.Equal(t, connectionAvailable, limit)
	_, limit = server.reserveConnection(scope, tournamentConnectionPrincipal{})
	require.Equal(t, connectionGlobalLimit, limit)

	release()
	release()
	nextRelease, limit := server.reserveConnection(scope, tournamentConnectionPrincipal{})
	require.Equal(t, connectionAvailable, limit)
	nextRelease()
}

func operatorSessionResolver(
	tournamentID uuid.UUID,
	operatorID uuid.UUID,
	expiresAt time.Time,
	validate func(context.Context) bool,
) TournamentOperatorSessionResolver {
	return func(r *http.Request, scopedID uuid.UUID) (TournamentOperatorSession, bool) {
		cookie, err := r.Cookie("tpm_admin_access")
		if err != nil || cookie.Value != "admin-access" || scopedID != tournamentID {
			return TournamentOperatorSession{}, false
		}
		return TournamentOperatorSession{
			Principal: tournamentws.OperatorRealtimePrincipal{
				Authenticated: true,
				PrincipalID:   operatorID,
				Role:          tournamentws.OperatorRealtimeRole,
				TournamentID:  scopedID,
			},
			ExpiresAt: expiresAt,
			Validate:  validate,
		}, true
	}
}

func dialTournamentOperator(t *testing.T, baseURL string, tournamentID uuid.UUID) *coderws.Conn {
	t.Helper()
	connection, response, err := coderws.Dial(
		context.Background(),
		tournamentWebSocketURL(baseURL, TournamentRoleOperator, tournamentID),
		&coderws.DialOptions{HTTPHeader: http.Header{
			"Cookie": {(&http.Cookie{Name: "tpm_admin_access", Value: "admin-access"}).String()},
			"Origin": {baseURL},
		}},
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	return connection
}

func dialTournamentParticipant(t *testing.T, baseURL string, tournamentID, token uuid.UUID) *coderws.Conn {
	t.Helper()
	connection, response, err := coderws.Dial(
		context.Background(),
		tournamentWebSocketURL(baseURL, TournamentRoleParticipant, tournamentID),
		&coderws.DialOptions{HTTPHeader: http.Header{
			"Cookie": {(&http.Cookie{Name: requestmeta.PlayerSessionCookieName, Value: token.String()}).String()},
		}},
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	return connection
}

func participantSessionPlayer(playerID, token uuid.UUID, expiresAt time.Time) *domain.Player {
	return &domain.Player{
		ID:               playerID,
		Username:         "participant",
		SessionToken:     &token,
		SessionExpiresAt: &expiresAt,
		CreatedAt:        expiresAt.Add(-time.Hour),
	}
}

func tournamentWebSocketTestServer(
	t *testing.T,
	tournamentID uuid.UUID,
	playerID uuid.UUID,
	extra ...Option,
) *Server {
	t.Helper()
	return tournamentWebSocketTestServerWithPlayerReader(t, nil, tournamentID, playerID, extra...)
}

func tournamentWebSocketTestServerWithPlayerReader(
	t *testing.T,
	players PlayerSessionReader,
	tournamentID uuid.UUID,
	playerID uuid.UUID,
	extra ...Option,
) *Server {
	t.Helper()
	reader := tournamentServerSnapshotReader(t, tournamentID, playerID)
	source, err := NewTournamentProductionSnapshotSource(reader)
	require.NoError(t, err)
	participant, err := NewTournamentParticipantFlow(source)
	require.NoError(t, err)
	public, err := NewTournamentPublicFlow(source, &tournamentws.PublicRealtimeConfig{MaxConnections: 8})
	require.NoError(t, err)
	operator, err := NewTournamentOperatorFlow(source)
	require.NoError(t, err)
	options := make([]Option, 0, 3+len(extra))
	options = append(options,
		WithTournamentParticipantFlow(participant),
		WithTournamentPublicFlow(public),
		WithTournamentOperatorFlow(operator),
	)
	options = append(options, extra...)
	return NewServer(players, options...)
}

func tournamentServerSnapshotReader(
	t *testing.T,
	tournamentID uuid.UUID,
	playerID uuid.UUID,
) *tournamentsnapshotmocks.MockTournamentSnapshotUseCase {
	t.Helper()
	operator := tournamentSourceOperatorView(tournamentID)
	operator.Waves[0].Members[0].ParticipantID = tournamentSourceID(203)
	reader := tournamentsnapshotmocks.NewMockTournamentSnapshotUseCase(t)
	reader.EXPECT().ParticipantSnapshot(mock.Anything, mock.MatchedBy(func(query tournamentsnapshot.ParticipantSnapshotQuery) bool {
		return query.TournamentID == tournamentID && query.PlayerID == playerID
	})).Return(tournamentsnapshot.ParticipantSnapshotView{
		TournamentID: tournamentID,
		PlayerID:     playerID,
		Cursor:       tournamentSourceCursor(),
	}, nil).Maybe()
	reader.EXPECT().PublicSnapshot(
		mock.Anything,
		tournamentsnapshot.PublicSnapshotQuery{TournamentID: tournamentID},
	).Return(tournamentSourcePublicView(tournamentID), nil).Maybe()
	reader.EXPECT().OperatorSnapshot(mock.Anything, mock.MatchedBy(func(query tournamentsnapshot.OperatorSnapshotQuery) bool {
		return query.TournamentID == tournamentID && query.OperatorID != uuid.Nil
	})).Return(operator, nil).Maybe()
	return reader
}

func tournamentWebSocketURL(baseURL string, role TournamentRole, tournamentID uuid.UUID) string {
	var endpoint string
	switch role {
	case TournamentRolePublic:
		endpoint = TournamentPublicWebSocketPath
	case TournamentRoleParticipant:
		endpoint = TournamentParticipantWebSocketPath
	case TournamentRoleOperator:
		endpoint = TournamentOperatorWebSocketPath
	default:
		endpoint = "/unsupported"
	}
	endpoint = strings.ReplaceAll(endpoint, "{tournament_id}", tournamentID.String())
	return "ws" + strings.TrimPrefix(baseURL, "http") + endpoint
}
