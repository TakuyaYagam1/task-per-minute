package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	httpkitmw "github.com/wahrwelt-kit/go-httpkit/httputil/middleware"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	inboundmocks "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound/mocks"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

func TestTournamentController_ListTournaments(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("37d75cce-8f91-4ff1-bf13-b40fb69a7331")
	rosterID := uuid.MustParse("0cfbad45-738c-490b-bd4a-0a8c74c5c8bf")
	createdAt := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

	t.Run("requires authenticated operator", func(t *testing.T) {
		service := inboundmocks.NewMockTournamentUseCase(t)
		controller := newTournamentController(service, nil)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tournaments", nil)

		controller.ListTournaments(recorder, request, api.ListTournamentsParams{})

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
		service.AssertNotCalled(t, "ListTournaments", mock.Anything, mock.Anything)
	})

	t.Run("dispatches one scoped query", func(t *testing.T) {
		state := api.TournamentStateSwiss
		cursorValue := inbound.TournamentCursor{CreatedAt: createdAt, TournamentID: tournamentID}
		cursor, err := encodeTournamentCursor(&cursorValue)
		require.NoError(t, err)
		pageSize := int32(10)
		service := inboundmocks.NewMockTournamentUseCase(t)
		var command inbound.TournamentListCommand
		service.EXPECT().ListTournaments(mock.Anything, mock.Anything).
			Run(func(_ context.Context, input inbound.TournamentListCommand) { command = input }).
			Return(inbound.TournamentPage{
				Items: []inbound.TournamentView{tournamentViewFixture(
					tournamentID, rosterID, domain.TournamentStateSwiss, 7, createdAt,
				)},
				Next: &cursorValue,
			}, nil).
			Once()
		controller := newTournamentController(service, nil)
		recorder := authenticatedOperatorRequest(
			t, http.MethodGet, "",
			func(w http.ResponseWriter, r *http.Request) {
				controller.ListTournaments(w, r, api.ListTournamentsParams{
					State: &state, Cursor: &cursor, PageSize: &pageSize,
				})
			},
		)

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, testOperatorIdentity(t), command.Operator)
		require.Equal(t, domain.TournamentState(state), command.State)
		require.Equal(t, cursorValue, *command.After)
		require.Equal(t, int(pageSize), command.PageSize)

		var payload api.TournamentListResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
		require.Len(t, payload.Items, 1)
		require.Equal(t, int64(7), payload.Items[0].Revision)
		require.Equal(t, "September Invitational", payload.Items[0].Name)
		require.Equal(t, "september-invitational", payload.Items[0].PublicId)
		require.Equal(t, int32(8), payload.Items[0].PlannedRosterSize)
		require.Equal(t, int64(3), payload.Items[0].ContentRevision)
		require.NotNil(t, payload.NextCursor)
		decoded, err := decodeTournamentCursor(*payload.NextCursor)
		require.NoError(t, err)
		require.Equal(t, cursorValue, *decoded)
	})

	t.Run("rejects malformed cursor before dispatch", func(t *testing.T) {
		service := inboundmocks.NewMockTournamentUseCase(t)
		controller := newTournamentController(service, nil)
		cursor := "not-a-cursor"
		recorder := authenticatedOperatorRequest(
			t, http.MethodGet, "",
			func(w http.ResponseWriter, r *http.Request) {
				controller.ListTournaments(w, r, api.ListTournamentsParams{Cursor: &cursor})
			},
		)

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		service.AssertNotCalled(t, "ListTournaments", mock.Anything, mock.Anything)
	})
}

func TestTournamentController_CreateTournament(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("37d75cce-8f91-4ff1-bf13-b40fb69a7331")
	rosterID := uuid.MustParse("0cfbad45-738c-490b-bd4a-0a8c74c5c8bf")
	commandID := uuid.MustParse("7524f043-40d5-40a5-a549-344c4640401f")
	createdAt := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

	t.Run("dispatches operator and idempotency scope", func(t *testing.T) {
		service := inboundmocks.NewMockTournamentUseCase(t)
		var command inbound.TournamentCreateCommand
		service.EXPECT().CreateTournament(mock.Anything, mock.Anything).
			Run(func(_ context.Context, input inbound.TournamentCreateCommand) { command = input }).
			Return(inbound.TournamentResult{Tournament: tournamentViewFixture(
				tournamentID, rosterID, domain.TournamentStateDraft, 1, createdAt,
			), Changed: true}, nil).
			Once()
		controller := newTournamentController(service, nil)
		recorder := authenticatedOperatorRequest(
			t, http.MethodPost,
			`{"expected_revision":0,"preset":"tournament_v1","name":"September Invitational","public_id":"september-invitational","planned_roster_size":8,"content_revision":3}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.CreateTournament(w, r, api.CreateTournamentParams{IdempotencyKey: commandID})
			},
		)

		require.Equal(t, http.StatusCreated, recorder.Code)
		require.Equal(t, commandID, command.IdempotencyKey)
		require.Zero(t, command.ExpectedRevision)
		require.Equal(t, domain.TournamentPresetV1, command.Preset)
		require.Equal(t, "September Invitational", command.Name)
		require.Equal(t, "september-invitational", command.PublicID)
		require.Equal(t, 8, command.PlannedRosterSize)
		require.Equal(t, int64(3), command.ContentRevision)
		require.Equal(t, testOperatorIdentity(t), command.Operator)
		var payload api.Tournament
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
		require.Equal(t, command.Name, payload.Name)
		require.Equal(t, command.PublicID, payload.PublicId)
		require.Equal(t, int32(command.PlannedRosterSize), payload.PlannedRosterSize)
		require.Equal(t, command.ContentRevision, payload.ContentRevision)
	})

	t.Run("maps current revision on conflict", func(t *testing.T) {
		service := inboundmocks.NewMockTournamentUseCase(t)
		service.EXPECT().CreateTournament(mock.Anything, mock.Anything).
			Return(inbound.TournamentResult{}, &inbound.TournamentRevisionConflictError{
				AggregateID:      tournamentID,
				ExpectedRevision: 0,
				CurrentRevision:  2,
				CurrentState:     domain.TournamentStateDraft,
			}).Once()
		controller := newTournamentController(service, nil)
		recorder := authenticatedOperatorRequest(
			t, http.MethodPost,
			`{"expected_revision":0,"preset":"tournament_v1","name":"September Invitational","public_id":"september-invitational","planned_roster_size":8,"content_revision":3}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.CreateTournament(w, r, api.CreateTournamentParams{IdempotencyKey: commandID})
			},
		)

		require.Equal(t, http.StatusConflict, recorder.Code)
		var payload api.TournamentRevisionProblem
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
		require.Equal(t, "application/problem+json", recorder.Header().Get("Content-Type"))
		require.Equal(t, "about:blank", payload.Type)
		require.Equal(t, http.StatusText(http.StatusConflict), payload.Title)
		require.Equal(t, int32(http.StatusConflict), payload.Status)
		require.NotNil(t, payload.Detail)
		require.Equal(t, "tournament revision conflict", *payload.Detail)
		require.NotNil(t, payload.Instance)
		require.Equal(t, "/api/v1/admin/tournaments", *payload.Instance)
		require.NotNil(t, payload.RequestId)
		require.Equal(t, "tournament-request-id", *payload.RequestId)
		require.Zero(t, payload.ExpectedRevision)
		require.Equal(t, int64(2), payload.CurrentRevision)
		require.NotNil(t, payload.CurrentState)
		require.Equal(t, api.TournamentStateDraft, *payload.CurrentState)
	})
}

func TestTournamentController_GetTournamentContent(t *testing.T) {
	t.Parallel()

	publicationID := uuid.MustParse("61e9d7d4-7a86-4b5e-9d7f-0a0a2bf95a01")
	normalPoolRevisionID := uuid.MustParse("61e9d7d4-7a86-4b5e-9d7f-0a0a2bf95a02")
	goldenPoolRevisionID := uuid.MustParse("61e9d7d4-7a86-4b5e-9d7f-0a0a2bf95a03")
	publishedAt := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	contentView := inbound.TournamentContentView{
		ContentRevision:      7,
		PublicationID:        publicationID,
		PublishedAt:          publishedAt,
		NormalPoolRevisionID: normalPoolRevisionID,
		GoldenPoolRevisionID: goldenPoolRevisionID,
	}

	t.Run("requires an authenticated operator", func(t *testing.T) {
		service := &tournamentContentUseCaseStub{}
		controller := newTournamentController(service, nil)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tournament-content", nil)

		controller.GetTournamentContent(recorder, request)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
		require.Zero(t, service.contentCalls)
	})

	t.Run("returns only the published selection metadata", func(t *testing.T) {
		service := &tournamentContentUseCaseStub{
			content: func(_ context.Context, operator inbound.OperatorIdentity) (inbound.TournamentContentView, error) {
				require.Equal(t, testOperatorIdentity(t), operator)
				return contentView, nil
			},
		}
		controller := newTournamentController(service, nil)
		recorder := authenticatedOperatorRequest(
			t, http.MethodGet, "",
			func(w http.ResponseWriter, r *http.Request) { controller.GetTournamentContent(w, r) },
		)

		require.Equal(t, http.StatusOK, recorder.Code)
		var payload map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
		require.Len(t, payload, 5)
		for _, field := range []string{
			"content_revision", "publication_id", "published_at", "normal_pool_revision_id", "golden_pool_revision_id",
		} {
			require.Contains(t, payload, field)
		}
		var responseBody api.TournamentContentSelection
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &responseBody))
		require.Equal(t, contentView.ContentRevision, responseBody.ContentRevision)
		require.Equal(t, contentView.PublicationID, responseBody.PublicationId)
		require.Equal(t, contentView.PublishedAt, responseBody.PublishedAt)
		require.Equal(t, contentView.NormalPoolRevisionID, responseBody.NormalPoolRevisionId)
		require.Equal(t, contentView.GoldenPoolRevisionID, responseBody.GoldenPoolRevisionId)
	})

	t.Run("maps an unusable selection to RFC7807 422", func(t *testing.T) {
		service := &tournamentContentUseCaseStub{
			content: func(context.Context, inbound.OperatorIdentity) (inbound.TournamentContentView, error) {
				return inbound.TournamentContentView{}, domain.ErrInvalidContentConfiguration
			},
		}
		controller := newTournamentController(service, nil)
		recorder := authenticatedOperatorRequest(
			t, http.MethodGet, "",
			func(w http.ResponseWriter, r *http.Request) { controller.GetTournamentContent(w, r) },
		)

		require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
		require.Equal(t, "application/problem+json", recorder.Header().Get("Content-Type"))
		var problem api.ProblemDetails
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &problem))
		require.NotNil(t, problem.Detail)
		require.Equal(t, domain.ErrInvalidContentConfiguration.Error(), *problem.Detail)
	})

	t.Run("rejects an invalid view from the usecase", func(t *testing.T) {
		service := &tournamentContentUseCaseStub{
			content: func(context.Context, inbound.OperatorIdentity) (inbound.TournamentContentView, error) {
				invalid := contentView
				invalid.ContentRevision = 0
				return invalid, nil
			},
		}
		controller := newTournamentController(service, nil)
		recorder := authenticatedOperatorRequest(
			t, http.MethodGet, "",
			func(w http.ResponseWriter, r *http.Request) { controller.GetTournamentContent(w, r) },
		)

		require.Equal(t, http.StatusInternalServerError, recorder.Code)
	})

	t.Run("rejects an expired admin access session", func(t *testing.T) {
		service := &tournamentContentUseCaseStub{content: func(context.Context, inbound.OperatorIdentity) (inbound.TournamentContentView, error) {
			return contentView, nil
		}}
		controller := newTournamentController(service, nil)
		verifier := middlewaremocks.NewMockAdminAccessVerifier(t)
		verifier.EXPECT().VerifyAccess(mock.Anything, "expired-access").Return(nil, domain.ErrTokenExpired).Once()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tournament-content", nil)
		request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: "expired-access"})

		middleware.AdminSession(verifier)(http.HandlerFunc(controller.GetTournamentContent)).ServeHTTP(recorder, request)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
		require.Zero(t, service.contentCalls)
	})

	t.Run("does not accept a player session as admin authentication", func(t *testing.T) {
		service := &tournamentContentUseCaseStub{content: func(context.Context, inbound.OperatorIdentity) (inbound.TournamentContentView, error) {
			return contentView, nil
		}}
		controller := newTournamentController(service, nil)
		verifier := middlewaremocks.NewMockAdminAccessVerifier(t)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tournament-content", nil)
		request.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: uuid.NewString()})

		middleware.AdminSession(verifier)(http.HandlerFunc(controller.GetTournamentContent)).ServeHTTP(recorder, request)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
		require.Zero(t, service.contentCalls)
	})
}

func TestTournamentContentRouteUsesAdminSessionAndNoStoreHeaders(t *testing.T) {
	t.Parallel()

	service := &tournamentContentUseCaseStub{
		content: func(context.Context, inbound.OperatorIdentity) (inbound.TournamentContentView, error) {
			return inbound.TournamentContentView{
				ContentRevision:      7,
				PublicationID:        uuid.MustParse("61e9d7d4-7a86-4b5e-9d7f-0a0a2bf95a01"),
				PublishedAt:          time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
				NormalPoolRevisionID: uuid.MustParse("61e9d7d4-7a86-4b5e-9d7f-0a0a2bf95a02"),
				GoldenPoolRevisionID: uuid.MustParse("61e9d7d4-7a86-4b5e-9d7f-0a0a2bf95a03"),
			}, nil
		},
	}
	verifier := middlewaremocks.NewMockAdminAccessVerifier(t)
	verifier.EXPECT().VerifyAccess(mock.Anything, "admin-access").Return(&authusecase.Claims{
		Subject: "admin", JTI: "admin-session", Kind: authusecase.TokenKindAccess,
	}, nil).Once()
	server := New(Dependencies{
		Tournaments:                   service,
		OperatorTournamentReadLimiter: newAllowingRateLimiter(t),
	})
	handler := middleware.NoStoreSensitiveResponses()(NewHandler(server, HandlerOptions{AdminAuth: verifier}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tournament-content", nil)
	request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: "admin-access"})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "no-cache", recorder.Header().Get("Pragma"))
	require.Equal(t, 1, service.contentCalls)
}

type tournamentContentUseCaseStub struct {
	content      func(context.Context, inbound.OperatorIdentity) (inbound.TournamentContentView, error)
	contentCalls int
}

func (s *tournamentContentUseCaseStub) ListTournaments(context.Context, inbound.TournamentListCommand) (inbound.TournamentPage, error) {
	return inbound.TournamentPage{}, domain.ErrInternal
}

func (s *tournamentContentUseCaseStub) CreateTournament(context.Context, inbound.TournamentCreateCommand) (inbound.TournamentResult, error) {
	return inbound.TournamentResult{}, domain.ErrInternal
}

func (s *tournamentContentUseCaseStub) GetTournamentContent(
	ctx context.Context,
	operator inbound.OperatorIdentity,
) (inbound.TournamentContentView, error) {
	s.contentCalls++
	if s.content == nil {
		return inbound.TournamentContentView{}, domain.ErrInternal
	}
	return s.content(ctx, operator)
}

func tournamentViewFixture(
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	state domain.TournamentState,
	revision int64,
	createdAt time.Time,
) inbound.TournamentView {
	return inbound.TournamentView{
		ID: tournamentID, RosterID: rosterID, Preset: domain.TournamentPresetV1,
		Name: "September Invitational", PublicID: "september-invitational",
		PlannedRosterSize: 8, ContentRevision: 3,
		State: state, Revision: revision, RosterSize: 8,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func testOperatorIdentity(t *testing.T) inbound.OperatorIdentity {
	t.Helper()
	actorID, err := inbound.OperatorActorID("admin")
	require.NoError(t, err)
	return inbound.OperatorIdentity{ActorID: actorID}
}

func authenticatedOperatorRequest(
	t *testing.T,
	method string,
	body string,
	handler http.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	const accessToken = "access-token"
	request := httptest.NewRequest(method, "/api/v1/admin/tournaments", strings.NewReader(body))
	request.Header.Set("X-Request-ID", "tournament-request-id")
	request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: accessToken})
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		csrfToken, err := middleware.NewAdminCSRFToken(middleware.AdminAccessCSRFCookieName, accessToken)
		require.NoError(t, err)
		request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCSRFCookieName, Value: csrfToken})
		request.Header.Set(middleware.CSRFHeaderName, csrfToken)
		request.Header.Set("Origin", "http://example.com")
	}
	verifier := middlewaremocks.NewMockAdminAccessVerifier(t)
	verifier.EXPECT().VerifyAccess(mock.Anything, accessToken).Return(&authusecase.Claims{
		JTI: "access-session", Subject: "admin", Kind: authusecase.TokenKindAccess,
		IssuedAt:  time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC),
	}, nil).Once()
	httpkitmw.RequestID()(
		middleware.OriginGuard(nil)(
			middleware.CSRFGuard()(
				middleware.AdminSession(verifier)(handler),
			),
		),
	).ServeHTTP(recorder, request)
	return recorder
}
