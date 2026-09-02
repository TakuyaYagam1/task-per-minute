//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/memory"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

func TestArenaSecurityBoundaries(t *testing.T) {
	t.Run("cross role and tournament isolation", func(t *testing.T) {
		fixture := newArenaSecurityFixture(t)

		participantOnOperator := fixture.request(
			t,
			http.MethodGet,
			"/api/v1/arena/operator/tournaments/"+fixture.tournamentID.String()+"/roster",
			"",
			"",
			&fixture.ownerSession,
			nil,
		)
		requireArenaProblem(t, participantOnOperator.response, http.StatusUnauthorized, "internal-store-detail")
		fixture.validateResponse(t, participantOnOperator.request, participantOnOperator.response)

		operatorOnParticipant := fixture.request(
			t,
			http.MethodGet,
			"/api/v1/arena/tournaments/"+fixture.tournamentID.String()+"/participant/lobby",
			"",
			bearer(fixture.operatorToken),
			nil,
			nil,
		)
		requireArenaProblem(t, operatorOnParticipant.response, http.StatusUnauthorized, "internal-store-detail")
		fixture.validateResponse(t, operatorOnParticipant.request, operatorOnParticipant.response)

		foreignOperator := fixture.request(
			t,
			http.MethodGet,
			"/api/v1/arena/operator/tournaments/"+fixture.foreignTournamentID.String()+"/roster",
			"",
			bearer(fixture.operatorToken),
			nil,
			nil,
		)
		requireArenaProblem(t, foreignOperator.response, http.StatusForbidden, "internal-store-detail")
		fixture.validateResponse(t, foreignOperator.request, foreignOperator.response)

		foreignParticipant := fixture.request(
			t,
			http.MethodGet,
			"/api/v1/arena/tournaments/"+fixture.foreignTournamentID.String()+"/participant/lobby",
			"",
			"",
			&fixture.ownerSession,
			nil,
		)
		requireArenaProblem(t, foreignParticipant.response, http.StatusForbidden, "internal-store-detail")
		fixture.validateResponse(t, foreignParticipant.request, foreignParticipant.response)

		rosterCalls, lobbyCalls := fixture.service.protectedReadCounts()
		require.Zero(t, rosterCalls)
		require.Zero(t, lobbyCalls)
	})

	t.Run("participant object scope and write redaction", func(t *testing.T) {
		fixture := newArenaSecurityFixture(t)
		assignmentPath := "/api/v1/arena/tournaments/" + fixture.tournamentID.String() +
			"/participant/assignments/" + fixture.assignmentID.String()

		foreignAssignment := fixture.request(
			t,
			http.MethodGet,
			assignmentPath,
			"",
			"",
			&fixture.outsiderSession,
			nil,
		)
		requireArenaProblem(t, foreignAssignment.response, http.StatusForbidden, "assignment-owner-detail")
		fixture.validateResponse(t, foreignAssignment.request, foreignAssignment.response)

		ownedAssignment := fixture.request(
			t,
			http.MethodGet,
			assignmentPath,
			"",
			"",
			&fixture.ownerSession,
			nil,
		)
		require.Equal(t, http.StatusOK, ownedAssignment.response.Code)
		fixture.validateResponse(t, ownedAssignment.request, ownedAssignment.response)
		assignment := decodeJSON[api.ArenaParticipantAssignmentResponse](t, ownedAssignment.response)
		require.Equal(t, fixture.assignmentID, assignment.Assignment.Id)
		require.Equal(t, fixture.ownerID, assignment.Assignment.Receipt.ParticipantId)
		require.NotContains(t, ownedAssignment.response.Body.String(), "submitted_flag")

		assignmentCommands := fixture.service.assignmentCommandsSnapshot()
		require.Len(t, assignmentCommands, 2)
		require.Equal(t, fixture.outsiderID, assignmentCommands[0].Actor.PlayerID)
		require.Equal(t, fixture.ownerID, assignmentCommands[1].Actor.PlayerID)

		answer := "test-answer"
		commandID := uuid.MustParse("f6d8de58-cc50-4aa4-a8f7-3fa0a230b767")
		submission := fixture.request(
			t,
			http.MethodPost,
			"/api/v1/arena/tournaments/"+fixture.tournamentID.String()+
				"/participant/series/"+fixture.seriesID.String()+
				"/games/"+fixture.gameID.String()+"/submissions",
			fmt.Sprintf(`{"expected_projection_revision":7,"submitted_flag":%q}`, answer),
			"",
			&fixture.ownerSession,
			&commandID,
		)
		require.Equal(t, http.StatusOK, submission.response.Code)
		fixture.validateResponse(t, submission.request, submission.response)
		require.NotContains(t, submission.response.Body.String(), answer)
		require.NotContains(t, submission.response.Body.String(), "submitted_flag")

		submissionCommand, ok := fixture.service.submissionCommandSnapshot()
		require.True(t, ok)
		require.Equal(t, fixture.ownerID, submissionCommand.Actor.PlayerID)
		require.Equal(t, fixture.tournamentID, submissionCommand.TournamentID)
		require.Equal(t, fixture.seriesID, submissionCommand.SeriesID)
		require.Equal(t, fixture.gameID, submissionCommand.GameID)
		require.Equal(t, answer, submissionCommand.SubmittedFlag)
	})

	t.Run("anonymous public view is allowlisted", func(t *testing.T) {
		fixture := newArenaSecurityFixture(t)

		publicView := fixture.request(
			t,
			http.MethodGet,
			"/api/v1/arena/public/tournaments/"+fixture.tournamentID.String(),
			"",
			"",
			nil,
			nil,
		)
		require.Equal(t, http.StatusOK, publicView.response.Code)
		fixture.validateResponse(t, publicView.request, publicView.response)

		var payload map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(publicView.response.Body.Bytes(), &payload))
		require.Len(t, payload, 7)
		for _, key := range []string{
			"tournament_id", "preset", "state", "roster_size", "started_at", "finished_at", "projection_revision",
		} {
			require.Contains(t, payload, key)
		}
		for _, privateField := range []string{
			"assignment", "participant_id", "submitted_flag", "winner_id", "result_reason", "audit_event_id",
		} {
			require.NotContains(t, publicView.response.Body.String(), privateField)
		}

		internalFailure := fixture.request(
			t,
			http.MethodGet,
			"/api/v1/arena/public/tournaments/"+fixture.errorTournamentID.String(),
			"",
			"",
			nil,
			nil,
		)
		requireArenaProblem(t, internalFailure.response, http.StatusInternalServerError, "internal-store-detail")

		requireOnlyArenaPublicRoutes(t)
	})

	t.Run("ingress body and pagination limits", func(t *testing.T) {
		fixture := newArenaSecurityFixture(t)
		validCursor := strings.Repeat("c", 1024)

		validPage := fixture.request(
			t,
			http.MethodGet,
			"/api/v1/arena/operator/tournaments?page_size=200&cursor="+validCursor,
			"",
			bearer(fixture.operatorToken),
			nil,
			nil,
		)
		require.Equal(t, http.StatusOK, validPage.response.Code)
		fixture.validateResponse(t, validPage.request, validPage.response)
		listCalls, listCommand := fixture.service.listSnapshot()
		require.Equal(t, 1, listCalls)
		require.Equal(t, int32(200), listCommand.PageSize)
		require.Equal(t, validCursor, listCommand.Cursor)

		oversizedPage := fixture.request(
			t,
			http.MethodGet,
			"/api/v1/arena/operator/tournaments?page_size=201",
			"",
			bearer(fixture.operatorToken),
			nil,
			nil,
		)
		requireArenaProblem(t, oversizedPage.response, http.StatusBadRequest, "201")

		oversizedCursor := fixture.request(
			t,
			http.MethodGet,
			"/api/v1/arena/operator/tournaments?cursor="+strings.Repeat("c", 1025),
			"",
			bearer(fixture.operatorToken),
			nil,
			nil,
		)
		requireArenaProblem(t, oversizedCursor.response, http.StatusBadRequest, strings.Repeat("c", 32))

		unknownFieldID := uuid.MustParse("3cf202a9-cf19-417a-9197-b3c0b8bd5c2b")
		unknownField := fixture.request(
			t,
			http.MethodPost,
			"/api/v1/arena/operator/tournaments/"+fixture.tournamentID.String()+"/actions",
			`{"expected_projection_revision":7,"action":"complete","confirmed":true,"operator_id":"spoofed-operator"}`,
			bearer(fixture.operatorToken),
			nil,
			&unknownFieldID,
		)
		requireArenaProblem(t, unknownField.response, http.StatusBadRequest, "spoofed-operator", "operator_id")
		fixture.validateResponse(t, unknownField.request, unknownField.response)

		oversizedBodyID := uuid.MustParse("29442c76-8dc9-4558-aa53-4b38cd32fd0b")
		oversizedBody := fixture.request(
			t,
			http.MethodPost,
			"/api/v1/arena/operator/tournaments/"+fixture.tournamentID.String()+"/actions",
			`{"expected_projection_revision":7,"action":"complete","confirmed":true,"reason":"`+
				strings.Repeat("x", (1<<20)+1)+`"}`,
			bearer(fixture.operatorToken),
			nil,
			&oversizedBodyID,
		)
		requireArenaProblem(t, oversizedBody.response, http.StatusRequestEntityTooLarge, strings.Repeat("x", 32))
		require.Less(t, oversizedBody.response.Body.Len(), 1024)

		listCalls, _ = fixture.service.listSnapshot()
		require.Equal(t, 1, listCalls)
		require.Zero(t, fixture.service.applyCallCount())
	})

	t.Run("idempotency and stale revisions", func(t *testing.T) {
		fixture := newArenaSecurityFixture(t)
		path := "/api/v1/arena/operator/tournaments/" + fixture.tournamentID.String() + "/actions"
		commandID := uuid.MustParse("56c4aa88-2704-4938-9bb6-4bb789f5c265")
		body := `{"expected_projection_revision":7,"action":"open_registration","confirmed":true}`

		accepted := fixture.request(t, http.MethodPost, path, body, bearer(fixture.operatorToken), nil, &commandID)
		require.Equal(t, http.StatusOK, accepted.response.Code)
		fixture.validateResponse(t, accepted.request, accepted.response)
		acceptedTournament := decodeJSON[api.ArenaTournament](t, accepted.response)
		require.NotNil(t, acceptedTournament.Revision)
		require.Equal(t, int64(8), *acceptedTournament.Revision)
		require.Equal(t, 1, fixture.service.applyCallCount())

		replayed := fixture.request(t, http.MethodPost, path, body, bearer(fixture.operatorToken), nil, &commandID)
		requireArenaProblem(t, replayed.response, http.StatusConflict, commandID.String())
		require.Equal(t, 1, fixture.service.applyCallCount())

		staleID := uuid.MustParse("b24b7f52-c7b1-41c5-bc2a-d3f116e7d1e2")
		stale := fixture.request(t, http.MethodPost, path, body, bearer(fixture.operatorToken), nil, &staleID)
		require.Equal(t, http.StatusConflict, stale.response.Code)
		fixture.validateResponse(t, stale.request, stale.response)
		conflict := decodeJSON[api.ArenaRevisionConflict](t, stale.response)
		require.Equal(t, int64(7), conflict.ExpectedRevision)
		require.Equal(t, int64(8), conflict.CurrentRevision)
		require.NotNil(t, conflict.CurrentState)
		require.Equal(t, api.ArenaTournamentStateRegistration, *conflict.CurrentState)
		require.NotContains(t, stale.response.Body.String(), "internal-store-detail")
		require.Equal(t, 2, fixture.service.applyCallCount())
	})
}

type arenaSecurityFixture struct {
	*restFixture

	service             *arenaSecurityService
	operatorToken       string
	ownerID             uuid.UUID
	ownerSession        uuid.UUID
	outsiderID          uuid.UUID
	outsiderSession     uuid.UUID
	tournamentID        uuid.UUID
	foreignTournamentID uuid.UUID
	errorTournamentID   uuid.UUID
	assignmentID        uuid.UUID
	seriesID            uuid.UUID
	gameID              uuid.UUID
}

type arenaSecurityResponse struct {
	request  *http.Request
	response *httptest.ResponseRecorder
}

func newArenaSecurityFixture(t *testing.T) *arenaSecurityFixture {
	t.Helper()

	database := newDuelFixture()
	clock := realIntegrationClock()
	players := playerusecase.NewUseCase(database.mgr, database.players, database.duels, clock)
	owner, err := players.Join(context.Background(), uniq("arena_owner"))
	require.NoError(t, err)
	require.NotNil(t, owner.SessionToken)
	outsider, err := players.Join(context.Background(), uniq("arena_outsider"))
	require.NoError(t, err)
	require.NotNil(t, outsider.SessionToken)

	auth := adminusecase.NewAuthUseCase(adminusecase.AuthConfig{
		Secret:        []byte("01234567890123456789012345678901"),
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    24 * time.Hour,
		AdminPassword: []byte(restAdminPassword),
	}, clock, memory.NewRevocation(clock))
	tokenPair, err := auth.Login(context.Background(), restAdminPassword)
	require.NoError(t, err)

	tournamentID := uuid.MustParse("67442136-59df-4c37-a3f8-228b63badbc1")
	service := newArenaSecurityService(tournamentID, owner.ID)
	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)
	limits := middleware.NewArenaRequestLimits(middleware.ArenaLimitConfig{
		PublicRead:          middleware.ArenaEndpointLimit{Requests: 100, Window: time.Hour},
		OperatorRead:        middleware.ArenaEndpointLimit{Requests: 100, Window: time.Hour},
		OperatorMutation:    middleware.ArenaEndpointLimit{Requests: 100, Window: time.Hour},
		ParticipantRead:     middleware.ArenaEndpointLimit{Requests: 100, Window: time.Hour},
		ParticipantMutation: middleware.ArenaEndpointLimit{Requests: 100, Window: time.Hour},
	})
	authorizer := middleware.ArenaScopeAuthorizerFunc(func(_ context.Context, access middleware.ArenaAccess) bool {
		switch access.Role {
		case middleware.ArenaRoleOperator:
			return access.ActorID != "" && access.SessionID != "" &&
				(access.TournamentID == uuid.Nil || access.TournamentID == tournamentID)
		case middleware.ArenaRoleParticipant:
			return access.TournamentID == tournamentID &&
				(access.ActorID == owner.ID.String() || access.ActorID == outsider.ID.String())
		default:
			return false
		}
	})

	server := restv1.New(restv1.Dependencies{
		ArenaAdmin:              service,
		ArenaParticipantService: service,
	})
	handler := restv1.NewHandler(server, restv1.HandlerOptions{
		AdminAuth:        auth,
		PlayerRepo:       database.players,
		ArenaAuthorizer:  authorizer,
		ArenaLimits:      limits,
		RequestValidator: validator,
		Middlewares: []api.MiddlewareFunc{
			middleware.Build(logkit.Noop()),
		},
	})

	return &arenaSecurityFixture{
		restFixture: &restFixture{
			duelFixture: database,
			handler:     handler,
			auth:        auth,
			validator:   newOpenAPIResponseValidator(t),
		},
		service:             service,
		operatorToken:       tokenPair.AccessToken,
		ownerID:             owner.ID,
		ownerSession:        *owner.SessionToken,
		outsiderID:          outsider.ID,
		outsiderSession:     *outsider.SessionToken,
		tournamentID:        tournamentID,
		foreignTournamentID: uuid.MustParse("dd64d5b1-370f-4771-842d-21031b009ce8"),
		errorTournamentID:   uuid.MustParse("98f4eaee-603d-4705-b0f4-440c20844c31"),
		assignmentID:        service.assignmentID,
		seriesID:            service.seriesID,
		gameID:              service.gameID,
	}
}

func (f *arenaSecurityFixture) request(
	t *testing.T,
	method string,
	path string,
	body string,
	authorization string,
	playerSession *uuid.UUID,
	commandID *uuid.UUID,
) arenaSecurityResponse {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if commandID != nil {
		request.Header.Set("Idempotency-Key", commandID.String())
	}
	if playerSession != nil {
		request.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: playerSession.String()})
		if method != http.MethodGet && method != http.MethodHead {
			csrfToken, err := middleware.NewPlayerCSRFToken(*playerSession)
			require.NoError(t, err)
			request.AddCookie(&http.Cookie{Name: middleware.PlayerCSRFCookieName, Value: csrfToken})
			request.Header.Set(middleware.CSRFHeaderName, csrfToken)
		}
	}

	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return arenaSecurityResponse{request: request, response: response}
}

func requireArenaProblem(t *testing.T, response *httptest.ResponseRecorder, status int, forbidden ...string) {
	t.Helper()
	require.Equal(t, status, response.Code)
	require.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
	problem := decodeJSON[api.ProblemDetails](t, response)
	require.Equal(t, int32(status), problem.Status)
	require.NotEmpty(t, problem.Title)
	require.NotNil(t, problem.Detail)
	for _, value := range forbidden {
		require.NotContains(t, response.Body.String(), value)
	}
}

func requireOnlyArenaPublicRoutes(t *testing.T) {
	t.Helper()
	spec, err := api.GetSwagger()
	require.NoError(t, err)
	const publicPrefix = "/api/v1/arena/public/"
	remaining := map[string]struct{}{
		"/api/v1/arena/public/tournaments/{tournament_id}":            {},
		"/api/v1/arena/public/tournaments/{tournament_id}/scoreboard": {},
		"/api/v1/arena/public/tournaments/{tournament_id}/bracket":    {},
		"/api/v1/arena/public/tournaments/{tournament_id}/live-draft": {},
		"/api/v1/arena/public/tournaments/{tournament_id}/snapshot":   {},
	}
	for path, item := range spec.Paths.Map() {
		isPublicPath := strings.HasPrefix(path, publicPrefix)
		if isPublicPath {
			require.Contains(t, remaining, path, "unexpected public Arena route")
			delete(remaining, path)
		}
		for _, operation := range item.Operations() {
			scope := operation.Extensions["x-arena-auth-scope"]
			if isPublicPath {
				require.Equal(t, "public", scope, "public Arena route must declare public scope")
			} else {
				require.NotEqual(t, "public", scope, "public Arena scope must remain on the allowlisted paths")
			}
		}
	}
	require.Empty(t, remaining, "published Arena public routes are incomplete")
}

type arenaSecurityService struct {
	mu sync.Mutex

	tournamentID uuid.UUID
	ownerID      uuid.UUID
	assignmentID uuid.UUID
	seriesID     uuid.UUID
	gameID       uuid.UUID
	waveID       uuid.UUID
	slotID       uuid.UUID
	taskID       uuid.UUID
	snapshotID   uuid.UUID
	attemptID    uuid.UUID
	receiptID    uuid.UUID
	now          time.Time
	revision     int64
	state        api.ArenaTournamentState

	listCalls          int
	lastListCommand    restv1.ArenaListTournamentsCommand
	applyCalls         int
	rosterCalls        int
	lobbyCalls         int
	assignmentCommands []restv1.ArenaParticipantAssignmentCommand
	lastSubmission     restv1.ArenaParticipantSubmissionCommand
	hasSubmission      bool
}

func newArenaSecurityService(tournamentID, ownerID uuid.UUID) *arenaSecurityService {
	return &arenaSecurityService{
		tournamentID: tournamentID,
		ownerID:      ownerID,
		assignmentID: uuid.MustParse("10041de7-23f2-4ded-8df5-79d7bcb4d729"),
		seriesID:     uuid.MustParse("c9c3f93f-8f57-486c-8b50-7bf2571560e0"),
		gameID:       uuid.MustParse("af889412-e380-41a5-835c-9f73d49ce65b"),
		waveID:       uuid.MustParse("f5656f43-45a7-46a6-9638-0803b7a6865a"),
		slotID:       uuid.MustParse("cbd61083-0017-4f61-b2cd-1b1f8649bdb8"),
		taskID:       uuid.MustParse("4fcf1a94-2374-4039-b8cc-5a54689061f8"),
		snapshotID:   uuid.MustParse("36f40567-a20a-4e64-b62c-a6a454a38161"),
		attemptID:    uuid.MustParse("e68c283a-e055-4207-b98a-e3ce5700ad88"),
		receiptID:    uuid.MustParse("4de40923-b3cc-40d3-8334-92057f32c6ac"),
		now:          time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
		revision:     7,
		state:        api.ArenaTournamentStateDraft,
	}
}

func (s *arenaSecurityService) ListTournaments(
	_ context.Context,
	command restv1.ArenaListTournamentsCommand,
) (api.ArenaOperatorTournamentList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	s.lastListCommand = command
	return api.ArenaOperatorTournamentList{
		Items:      []api.ArenaTournament{s.tournamentLocked()},
		NextCursor: nil,
	}, nil
}

func (s *arenaSecurityService) CreateTournament(
	context.Context,
	restv1.ArenaCreateTournamentCommand,
) (api.ArenaTournament, error) {
	return api.ArenaTournament{}, domain.ErrInternal
}

func (s *arenaSecurityService) ApplyTournamentAction(
	_ context.Context,
	command restv1.ArenaTournamentActionCommand,
) (api.ArenaTournament, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyCalls++
	if command.TournamentID != s.tournamentID || command.ExpectedRevision != s.revision {
		return api.ArenaTournament{}, &restv1.ArenaRevisionConflictError{
			ExpectedRevision: command.ExpectedRevision,
			CurrentRevision:  s.revision,
			CurrentState:     string(s.state),
		}
	}
	if command.Action == restv1.ArenaTournamentActionOpenRegistration {
		s.state = api.ArenaTournamentStateRegistration
	}
	s.revision++
	return s.tournamentLocked(), nil
}

func (s *arenaSecurityService) GetRoster(
	context.Context,
	restv1.ArenaGetRosterCommand,
) (api.ArenaRoster, error) {
	s.mu.Lock()
	s.rosterCalls++
	s.mu.Unlock()
	return api.ArenaRoster{}, domain.ErrInternal
}

func (s *arenaSecurityService) ReplaceRoster(
	context.Context,
	restv1.ArenaReplaceRosterCommand,
) (api.ArenaRoster, error) {
	return api.ArenaRoster{}, domain.ErrInternal
}

func (s *arenaSecurityService) RunRosterPreflight(
	context.Context,
	restv1.ArenaRosterPreflightCommand,
) (api.ArenaPreflightReport, error) {
	return api.ArenaPreflightReport{}, domain.ErrInternal
}

func (s *arenaSecurityService) LockRoster(
	context.Context,
	restv1.ArenaLockRosterCommand,
) (api.ArenaRoster, error) {
	return api.ArenaRoster{}, domain.ErrInternal
}

func (s *arenaSecurityService) UnlockRoster(
	context.Context,
	restv1.ArenaUnlockRosterCommand,
) (api.ArenaRoster, error) {
	return api.ArenaRoster{}, domain.ErrInternal
}

func (s *arenaSecurityService) ConfigurePairings(
	context.Context,
	restv1.ArenaConfigurePairingsCommand,
) (api.ArenaSwissRound, error) {
	return api.ArenaSwissRound{}, domain.ErrInternal
}

func (s *arenaSecurityService) ControlWave(
	context.Context,
	restv1.ArenaWaveControlCommand,
) (api.ArenaWave, error) {
	return api.ArenaWave{}, domain.ErrInternal
}

func (s *arenaSecurityService) GetStandings(
	context.Context,
	restv1.ArenaTournamentReadCommand,
) (api.ArenaPublicScoreboardResponse, error) {
	return api.ArenaPublicScoreboardResponse{}, domain.ErrInternal
}

func (s *arenaSecurityService) GetBracket(
	context.Context,
	restv1.ArenaTournamentReadCommand,
) (api.ArenaPublicBracketResponse, error) {
	return api.ArenaPublicBracketResponse{}, domain.ErrInternal
}

func (s *arenaSecurityService) GetPublicTournament(
	_ context.Context,
	command restv1.ArenaTournamentReadCommand,
) (api.ArenaPublicTournamentResponse, error) {
	if command.TournamentID == uuid.MustParse("98f4eaee-603d-4705-b0f4-440c20844c31") {
		return api.ArenaPublicTournamentResponse{}, fmt.Errorf("internal-store-detail: %w", domain.ErrInternal)
	}
	return api.ArenaPublicTournamentResponse{
		TournamentId:       s.tournamentID,
		Preset:             api.ArenaV1,
		State:              api.ArenaTournamentStateDraft,
		RosterSize:         16,
		StartedAt:          nil,
		FinishedAt:         nil,
		ProjectionRevision: 7,
	}, nil
}

func (s *arenaSecurityService) GetLiveDraft(
	context.Context,
	restv1.ArenaTournamentReadCommand,
) (api.ArenaPublicLiveDraftResponse, error) {
	return api.ArenaPublicLiveDraftResponse{}, domain.ErrInternal
}

func (s *arenaSecurityService) GetPublicSnapshot(
	context.Context,
	api.ArenaTournamentId,
	*api.ArenaPublicCursor,
) (api.ArenaPublicRecoverySnapshot, error) {
	return api.ArenaPublicRecoverySnapshot{}, domain.ErrInternal
}

func (s *arenaSecurityService) GetLobby(
	_ context.Context,
	command restv1.ArenaParticipantLobbyCommand,
) (api.ArenaParticipantLobbyResponse, error) {
	s.mu.Lock()
	s.lobbyCalls++
	s.mu.Unlock()
	return api.ArenaParticipantLobbyResponse{
		TournamentId:       command.TournamentID,
		State:              api.ArenaTournamentStateDraft,
		ProjectionRevision: 7,
		RosterLocked:       true,
		Series:             []api.ArenaParticipantLobbySeries{},
	}, nil
}

func (s *arenaSecurityService) GetAssignment(
	_ context.Context,
	command restv1.ArenaParticipantAssignmentCommand,
) (api.ArenaParticipantAssignmentResponse, error) {
	s.mu.Lock()
	s.assignmentCommands = append(s.assignmentCommands, command)
	s.mu.Unlock()
	if command.Actor.PlayerID != s.ownerID || command.TournamentID != s.tournamentID ||
		command.AssignmentID != s.assignmentID {
		return api.ArenaParticipantAssignmentResponse{}, fmt.Errorf(
			"assignment-owner-detail: %w",
			domain.ErrNotDuelParticipant,
		)
	}
	return s.assignmentResponse(), nil
}

func (s *arenaSecurityService) SetReady(
	context.Context,
	restv1.ArenaParticipantReadyCommand,
) (api.ArenaReadinessEvent, error) {
	return api.ArenaReadinessEvent{}, domain.ErrInternal
}

func (s *arenaSecurityService) SubmitDraftAction(
	context.Context,
	restv1.ArenaParticipantDraftActionCommand,
) (api.ArenaDraft, error) {
	return api.ArenaDraft{}, domain.ErrInternal
}

func (s *arenaSecurityService) SubmitFlag(
	_ context.Context,
	command restv1.ArenaParticipantSubmissionCommand,
) (api.ArenaParticipantSubmissionResponse, error) {
	if command.Actor.PlayerID != s.ownerID || command.TournamentID != s.tournamentID ||
		command.SeriesID != s.seriesID || command.GameID != s.gameID {
		return api.ArenaParticipantSubmissionResponse{}, domain.ErrNotDuelParticipant
	}
	s.mu.Lock()
	s.lastSubmission = command
	s.hasSubmission = true
	s.mu.Unlock()

	digest := strings.Repeat("a", 64)
	correct := false
	committedAt := s.now
	return api.ArenaParticipantSubmissionResponse{
		ProjectionRevision: s.revision,
		Submission: api.ArenaSubmissionRecord{
			Scope: api.ArenaSubmissionScope{
				WaveId: s.waveID, TournamentId: s.tournamentID, SeriesId: s.seriesID,
				SlotId: s.slotID, GameId: s.gameID, AssignmentId: s.assignmentID,
			},
			CommandId:     command.CommandID,
			ParticipantId: s.ownerID,
			Sequence:      1,
			CommittedAt:   &committedAt,
			Correct:       &correct,
			SnapshotId:    s.snapshotID,
			TaskId:        s.taskID,
			ContentDigest: &digest,
		},
	}, nil
}

func (s *arenaSecurityService) Surrender(
	context.Context,
	restv1.ArenaParticipantSurrenderCommand,
) (api.ArenaOfficialResultRevision, error) {
	return api.ArenaOfficialResultRevision{}, domain.ErrInternal
}

func (s *arenaSecurityService) ApplyPostSeriesAction(
	context.Context,
	restv1.ArenaParticipantPostSeriesCommand,
) (api.ArenaParticipantPostSeriesResponse, error) {
	return api.ArenaParticipantPostSeriesResponse{}, domain.ErrInternal
}

func (s *arenaSecurityService) GetSnapshot(
	context.Context,
	restv1.ArenaParticipantSnapshotCommand,
) (api.ArenaParticipantRecoverySnapshot, error) {
	return api.ArenaParticipantRecoverySnapshot{}, domain.ErrInternal
}

func (s *arenaSecurityService) tournamentLocked() api.ArenaTournament {
	revision := s.revision
	createdAt := s.now
	updatedAt := s.now
	return api.ArenaTournament{
		Id:              s.tournamentID,
		RosterId:        uuid.MustParse("cfca5e7f-a7e6-4327-9240-c002b6192a8e"),
		Preset:          api.ArenaV1,
		State:           s.state,
		Revision:        &revision,
		RosterSize:      16,
		PausedFromState: nil,
		CreatedAt:       &createdAt,
		UpdatedAt:       &updatedAt,
		StartedAt:       nil,
		FinishedAt:      nil,
	}
}

func (s *arenaSecurityService) assignmentResponse() api.ArenaParticipantAssignmentResponse {
	deliveredAt := s.now
	return api.ArenaParticipantAssignmentResponse{
		TournamentId:       s.tournamentID,
		ProjectionRevision: s.revision,
		Assignment: api.ArenaParticipantAssignment{
			Id:        s.assignmentID,
			AttemptId: s.attemptID,
			ActiveSnapshot: api.ArenaTaskSnapshot{
				SnapshotId:    s.snapshotID,
				TaskId:        s.taskID,
				Version:       1,
				Kind:          api.ArenaTaskKindNormal,
				Title:         "Participant task",
				Description:   "Visible only after assignment ownership is checked",
				Category:      api.ArenaCategoryWeb,
				Difficulty:    api.ArenaDifficultyEasy,
				TimeLimit:     90,
				Hints:         []string{"Participant hint"},
				TaskUrl:       nil,
				SourceFileUrl: nil,
			},
			UndisclosedReserveCount: 2,
			Receipt: api.ArenaDeliveryReceipt{
				Id:            s.receiptID,
				AssignmentId:  s.assignmentID,
				AttemptId:     s.attemptID,
				ParticipantId: s.ownerID,
				SnapshotId:    s.snapshotID,
				TaskId:        s.taskID,
				DeliveredAt:   &deliveredAt,
			},
		},
	}
}

func (s *arenaSecurityService) protectedReadCounts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rosterCalls, s.lobbyCalls
}

func (s *arenaSecurityService) listSnapshot() (int, restv1.ArenaListTournamentsCommand) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCalls, s.lastListCommand
}

func (s *arenaSecurityService) applyCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyCalls
}

func (s *arenaSecurityService) assignmentCommandsSnapshot() []restv1.ArenaParticipantAssignmentCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]restv1.ArenaParticipantAssignmentCommand(nil), s.assignmentCommands...)
}

func (s *arenaSecurityService) submissionCommandSnapshot() (restv1.ArenaParticipantSubmissionCommand, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSubmission, s.hasSubmission
}
