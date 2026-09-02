package bootstrap

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
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	rootws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestProvideArenaCoreComposesSupportedUseCases(t *testing.T) {
	t.Parallel()

	var repository arenaTournamentRepositoryStub
	core := provideArenaCore(
		&repository,
		&repository,
		&repository,
		&repository,
		fixedArenaClock{},
		provideArenaObservability(logkit.Noop()),
	)

	require.NotNil(t, core)
	require.NotNil(t, core.tournaments)
	require.NotNil(t, core.attendance)
	require.NotNil(t, core.rosters)
	require.NotNil(t, core.lifecycle)
}

func TestProvideArenaOperatorPrincipalResolverUsesVerifiedAdminSubject(t *testing.T) {
	t.Parallel()

	auth := adminusecase.NewAuthUseCase(adminusecase.AuthConfig{
		Secret:        []byte("arena-operator-test-secret"),
		AccessTTL:     time.Hour,
		RefreshTTL:    24 * time.Hour,
		AdminPassword: []byte("operator-password"),
	}, fixedArenaClock{}, arenaRevocationStoreStub{})
	pair, err := auth.Login(t.Context(), "operator-password")
	require.NoError(t, err)

	request := httptest.NewRequest(http.MethodGet, "/ws?arena_role=operator", nil)
	request.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	playerID := uuid.New()
	tournamentID := uuid.New()
	resolver := provideArenaOperatorPrincipalResolver(auth)

	principal, ok := resolver(request, &domain.Player{ID: playerID}, tournamentID)
	require.True(t, ok)
	require.True(t, principal.Authenticated)
	require.Equal(t, "operator", principal.Role)
	require.Equal(t, tournamentID, principal.TournamentID)
	require.NotEqual(t, uuid.Nil, principal.PrincipalID)
	require.NotEqual(t, playerID, principal.PrincipalID)

	repeated, ok := resolver(request, &domain.Player{ID: uuid.New()}, tournamentID)
	require.True(t, ok)
	require.Equal(t, principal.PrincipalID, repeated.PrincipalID)
}

func TestProvideArenaOperatorPrincipalResolverRejectsUnverifiedRequests(t *testing.T) {
	t.Parallel()

	auth := adminusecase.NewAuthUseCase(adminusecase.AuthConfig{
		Secret:        []byte("arena-operator-test-secret"),
		AccessTTL:     time.Hour,
		RefreshTTL:    24 * time.Hour,
		AdminPassword: []byte("operator-password"),
	}, fixedArenaClock{}, arenaRevocationStoreStub{})
	resolver := provideArenaOperatorPrincipalResolver(auth)

	tests := []struct {
		name    string
		request *http.Request
	}{
		{name: "missing token", request: httptest.NewRequest(http.MethodGet, "/ws?arena_role=operator", nil)},
		{name: "invalid token", request: requestWithBearerToken("not-a-token")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			principal, ok := resolver(tt.request, &domain.Player{ID: uuid.New()}, uuid.New())

			require.False(t, ok)
			require.False(t, principal.Authenticated)
			require.Equal(t, uuid.Nil, principal.PrincipalID)
		})
	}
}

func TestProvideRawWebSocketServerAddsArenaFlows(t *testing.T) {
	t.Parallel()

	raw := provideRawWebSocketServerWithArena(
		t.Context(),
		&config.Config{},
		logkit.Noop(),
		nil,
		nil,
		nil,
		rootws.NewHubRegistry(),
		nil,
		nil,
		nil,
		nil,
		nil,
		arenaWebSocketOptions{public: arenaPublicFlowStub{}},
		provideArenaObservability(logkit.Noop()),
	)
	server := httptest.NewServer(raw.Server)
	t.Cleanup(server.Close)

	dialContext, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?arena_role=public"
	conn, handshakeResponse, err := coderws.Dial(dialContext, url, &coderws.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{server.URL}},
	})
	require.NoError(t, err)
	if handshakeResponse != nil && handshakeResponse.Body != nil {
		defer handshakeResponse.Body.Close()
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	command, err := json.Marshal(map[string]any{
		"type": rootws.EventArenaConnect,
		"payload": map[string]any{
			"role":          rootws.ArenaRolePublic,
			"tournament_id": uuid.New(),
		},
	})
	require.NoError(t, err)
	require.NoError(t, conn.Write(dialContext, coderws.MessageText, command))

	messageType, response, err := conn.Read(dialContext)
	require.NoError(t, err)
	require.Equal(t, coderws.MessageText, messageType)
	var rejection struct {
		Type string `json:"type"`
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(response, &rejection))
	require.Equal(t, rootws.EventArenaRejected, rejection.Type)
	require.Equal(t, string(rootws.ArenaRejectionRoleMismatch), rejection.Code)
}

func TestProvideRESTServerWithClockUsesSharedClock(t *testing.T) {
	t.Parallel()

	clock := fixedArenaClock{}
	auth := adminusecase.NewAuthUseCase(adminusecase.AuthConfig{
		Secret:        []byte("arena-rest-clock-test-secret"),
		AccessTTL:     time.Hour,
		RefreshTTL:    24 * time.Hour,
		AdminPassword: []byte("operator-password"),
	}, clock, arenaRevocationStoreStub{})
	server := provideRESTServerWithClock(
		nil,
		auth,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		restv1.HealthChecks{},
		clockFunc(clock.Now),
		nil,
		adminRefreshRateLimiter{},
		nil,
		leaderboardRateLimiter{},
		logkit.Noop(),
		provideArenaObservability(logkit.Noop()),
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/login",
		strings.NewReader(`{"password":"operator-password"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.AdminLogin(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		ExpiresIn int32 `json:"expires_in"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, int32(time.Hour/time.Second), response.ExpiresIn)
}

type arenaPublicFlowStub struct{}

func (arenaPublicFlowStub) OpenArenaPublic(
	context.Context,
	rootws.ArenaPublicConnectionRequest,
) (rootws.ArenaPublicPayload, error) {
	return rootws.ArenaPublicPayload{}, rootws.ErrArenaRoleMismatch
}

func requestWithBearerToken(token string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/ws?arena_role=operator", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

type fixedArenaClock struct{}

func (fixedArenaClock) Now() time.Time {
	return time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
}

type arenaRevocationStoreStub struct{}

func (arenaRevocationStoreStub) Revoke(context.Context, string, time.Time) error {
	return nil
}

func (arenaRevocationStoreStub) IsRevoked(context.Context, string) (bool, error) {
	return false, nil
}

type arenaTournamentRepositoryStub struct{}

func (arenaTournamentRepositoryStub) CreateTournamentDraft(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	time.Time,
) (*arena.TournamentRecord, *arena.RosterRecord, error) {
	return nil, nil, arena.ErrTournamentNotFound
}

func (arenaTournamentRepositoryStub) GetTournament(context.Context, uuid.UUID) (*arena.TournamentRecord, error) {
	return nil, arena.ErrTournamentNotFound
}

func (arenaTournamentRepositoryStub) ListTournaments(context.Context) ([]arena.TournamentRecord, error) {
	return nil, nil
}

func (arenaTournamentRepositoryStub) InviteParticipant(
	context.Context,
	arena.ParticipantInput,
) (*arena.ParticipantRecord, bool, error) {
	return nil, false, nil
}

func (arenaTournamentRepositoryStub) ChangeAttendance(
	context.Context,
	uuid.UUID,
	domain.ArenaAttendanceState,
	domain.ArenaAttendanceState,
	time.Time,
) (*arena.ParticipantRecord, bool, error) {
	return nil, false, nil
}

func (arenaTournamentRepositoryStub) ReplaceWithdrawnParticipant(
	context.Context,
	arena.ParticipantReplacementInput,
) (*arena.ParticipantRecord, bool, error) {
	return nil, false, nil
}

func (arenaTournamentRepositoryStub) ListRosterParticipants(
	context.Context,
	uuid.UUID,
) ([]arena.ParticipantRecord, error) {
	return nil, nil
}

func (arenaTournamentRepositoryStub) GetRosterSnapshot(context.Context, uuid.UUID) (*arena.RosterRecord, error) {
	return nil, arena.ErrRosterNotFound
}

func (arenaTournamentRepositoryStub) LockRosterAndReserveExpected(
	context.Context,
	uuid.UUID,
	int64,
	[]uuid.UUID,
	time.Time,
) (*arena.RosterRecord, bool, error) {
	return nil, false, nil
}

func (arenaTournamentRepositoryStub) UnlockRosterAndReleaseExpected(
	context.Context,
	uuid.UUID,
	int64,
	time.Time,
) (*arena.RosterRecord, bool, error) {
	return nil, false, nil
}

func (arenaTournamentRepositoryStub) TransitionTournament(
	context.Context,
	arena.TournamentLifecycleTransitionInput,
) (*arena.TournamentRecord, bool, error) {
	return nil, false, nil
}
