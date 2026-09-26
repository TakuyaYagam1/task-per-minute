package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	inboundmocks "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound/mocks"
)

func TestSubmitParticipantDraftActionCurrentErrorMapping(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	seriesID := uuid.New()
	playerID := uuid.New()
	commandID := uuid.New()
	currentState := api.TournamentStateSwiss

	conflict := &inbound.RevisionConflictError{
		TournamentID:     tournamentID,
		ExpectedRevision: 6,
		CurrentRevision:  6,
		CurrentState:     domain.TournamentStateSwiss,
	}

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   *api.ProjectionRevisionProblem
	}{
		{
			name:       "expired draft action falls through to internal error",
			err:        domain.ErrDraftDeadline,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "stale draft turn falls through to internal error",
			err:        domain.ErrDraftStaleTurn,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "normalized invalid draft command is a bad request",
			err:        domain.ErrValidation,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "participant state conflict is recoverable with equal revisions",
			err:        conflict,
			wantStatus: http.StatusConflict,
			wantBody: &api.ProjectionRevisionProblem{
				ExpectedRevision: conflict.ExpectedRevision,
				CurrentRevision:  conflict.CurrentRevision,
				CurrentState:     &currentState,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			participant := inboundmocks.NewMockTournamentParticipantUseCase(t)
			participant.EXPECT().
				SubmitDraftAction(mock.Anything, mock.Anything).
				Return(inbound.DraftExecutionView{}, test.err).
				Once()
			server := New(Dependencies{TournamentParticipant: participant})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/tournaments/"+tournamentID.String()+"/participant/series/"+seriesID.String()+"/draft/actions",
				strings.NewReader(`{"expected_projection_revision":6,"expected_draft_revision":2,"expected_turn":2,"action":"ban","category":"pwn"}`),
			)
			request.Header.Set("Content-Type", "application/json")

			serveWithPlayer(t, playerID, recorder, request, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				server.SubmitParticipantDraftAction(
					w,
					r,
					tournamentID,
					seriesID,
					api.SubmitParticipantDraftActionParams{IdempotencyKey: commandID},
				)
			}))

			require.Equal(t, test.wantStatus, recorder.Code)
			if test.wantBody == nil {
				return
			}

			var body api.ProjectionRevisionProblem
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			require.Equal(t, test.wantBody.ExpectedRevision, body.ExpectedRevision)
			require.Equal(t, test.wantBody.CurrentRevision, body.CurrentRevision)
			require.NotNil(t, body.Detail)
			require.Equal(t, "participant state conflict", *body.Detail)
			require.NotNil(t, body.CurrentState)
			require.Equal(t, *test.wantBody.CurrentState, *body.CurrentState)
		})
	}
}
