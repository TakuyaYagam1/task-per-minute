package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	telemetryadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/telemetry"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	observabilitymocks "github.com/TakuyaYagam1/task-per-minute/internal/observability/mocks"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentadminmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/mocks"
	tournamentadminobserved "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/observed"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func TestRosterPreflightIngressCommandIDCorrelatesTerminalTelemetry(t *testing.T) {
	t.Parallel()

	const accessToken = "preflight-correlation-token"
	tournamentID := uuid.MustParse("20000000-0000-4000-8000-000000000001")
	commandID := uuid.MustParse("20000000-0000-4000-8000-000000000002")
	next := tournamentadminmocks.NewMockAdminService(t)
	next.EXPECT().RunPreflight(mock.Anything, mock.MatchedBy(func(command tournamentadmin.PreflightCommand) bool {
		return command.TournamentID == tournamentID && command.CommandID == commandID &&
			command.ExpectedProjectionRevision == 7
	})).Return(tournamentpreflight.ReportRevision{}, domain.ErrConflict).Once()

	events := observabilitymocks.NewMockTournamentEventObserver(t)
	events.EXPECT().ObserveTournamentEvent(mock.Anything, mock.MatchedBy(func(event observability.TournamentEvent) bool {
		return event.Event == "tournament.admin.command" &&
			event.Outcome == observability.TournamentOutcomeRejected &&
			event.CorrelationID == commandID.String() && event.CommandID == commandID.String() &&
			event.TournamentID == tournamentID.String() && event.EntityID == tournamentID.String() &&
			event.EntityKind == "tournament" && event.Stage == "maintenance" &&
			event.Transition == string(tournamentadmin.OperationPreflightRun) &&
			event.ReasonCode == "stale_revision" && event.Revision == 7
	})).Once()
	events.EXPECT().ObserveTournamentEvent(mock.Anything, mock.MatchedBy(func(event observability.TournamentEvent) bool {
		return event.Event == "tournament.http" &&
			event.Outcome == observability.TournamentOutcomeRejected &&
			event.CorrelationID == commandID.String() && event.CommandID == "" &&
			event.TournamentID == tournamentID.String() && event.EntityID == commandID.String() &&
			event.EntityKind == "http_request" && event.Stage == "operator_mutation" &&
			event.Transition == "post" && event.ReasonCode == "status_409"
	})).Once()
	admin := tournamentadminobserved.NewObservedService(next, nil, telemetryadapter.NewTournamentAdminObserver(events))

	verifier := middlewaremocks.NewMockAdminAccessVerifier(t)
	verifier.EXPECT().VerifyAccess(mock.Anything, accessToken).Return(&authusecase.Claims{
		JTI: "admin-access-session", Subject: "admin", Kind: authusecase.TokenKindAccess,
	}, nil).Once()
	handler := middleware.Build(logkit.Noop(), middleware.WithTournamentEventObserver(events))(NewHandler(New(Dependencies{
		TournamentAdmin:                   tournamentadmin.NewInboundAdapter(admin),
		OperatorTournamentMutationLimiter: newAllowingRateLimiter(t),
	}), HandlerOptions{AdminAuth: verifier}))
	csrfToken, err := middleware.NewAdminCSRFToken(middleware.AdminAccessCSRFCookieName, accessToken)
	require.NoError(t, err)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/tournaments/"+tournamentID.String()+"/roster/preflight",
		strings.NewReader(`{"expected_projection_revision":7}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", commandID.String())
	request.Header.Set(middleware.CSRFHeaderName, csrfToken)
	request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: accessToken})
	request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCSRFCookieName, Value: csrfToken})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code)
}
