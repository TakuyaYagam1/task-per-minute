package v1

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestArenaSubmissionHandlers(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("2ef40d04-d2f4-4402-96ed-75511df8c0e3")
	seriesID := uuid.MustParse("1a1168f0-0a33-4640-a409-5d52db373ce3")
	gameID := uuid.MustParse("e1d91e4f-b035-44c6-b218-1440d156ab73")
	commandID := uuid.MustParse("389a1f31-dc7a-4cf8-88e7-95223e881109")
	playerID := uuid.MustParse("32b102d1-ddf8-481e-a73a-769769409cb0")
	flag := "TPM{transport-secret}"

	t.Run("submits the flag with the authenticated player and idempotency key", func(t *testing.T) {
		correct := true
		service := &arenaParticipantServiceStub{submissionResult: api.ArenaParticipantSubmissionResponse{
			ProjectionRevision: 42,
			Submission: api.ArenaSubmissionRecord{
				CommandId: commandID, ParticipantId: uuid.New(), Sequence: 3,
				SnapshotId: uuid.New(), TaskId: uuid.New(), Correct: &correct,
				Scope: api.ArenaSubmissionScope{
					TournamentId: tournamentID, SeriesId: seriesID, GameId: gameID,
					WaveId: uuid.New(), AssignmentId: uuid.New(), SlotId: uuid.New(),
				},
			},
		}}
		limiter := &arenaSubmissionLimiterStub{allow: true, retryAfter: "60"}
		controller := newArenaParticipantController(service, limiter)
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/submissions",
			`{"expected_projection_revision":41,"submitted_flag":"`+flag+`"}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.SubmitArenaParticipantFlag(
					w, r, tournamentID, seriesID, gameID,
					api.SubmitArenaParticipantFlagParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, playerID, service.submissionCommand.Actor.PlayerID)
		require.Equal(t, commandID, service.submissionCommand.CommandID)
		require.Equal(t, int64(41), service.submissionCommand.ExpectedProjectionRevision)
		require.Equal(t, flag, service.submissionCommand.SubmittedFlag)
		require.NotContains(t, recorder.Body.String(), flag)
		require.Equal(t, []string{"flag:" + playerID.String()}, limiter.keys)
	})

	t.Run("rejects malformed and oversized flags before dispatch", func(t *testing.T) {
		tests := []struct {
			name string
			body string
		}{
			{name: "missing", body: `{"expected_projection_revision":41}`},
			{name: "empty", body: `{"expected_projection_revision":41,"submitted_flag":""}`},
			{name: "oversized", body: `{"expected_projection_revision":41,"submitted_flag":"` + strings.Repeat("x", 4097) + `"}`},
			{name: "actor injection", body: `{"expected_projection_revision":41,"submitted_flag":"x","actor_id":"` + uuid.NewString() + `"}`},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				service := &arenaParticipantServiceStub{}
				controller := newArenaParticipantController(service, &arenaSubmissionLimiterStub{allow: true})
				recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/submissions", test.body,
					func(w http.ResponseWriter, r *http.Request) {
						controller.SubmitArenaParticipantFlag(
							w, r, tournamentID, seriesID, gameID,
							api.SubmitArenaParticipantFlagParams{IdempotencyKey: commandID},
						)
					})

				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Zero(t, service.submissionCalls)
			})
		}
	})

	t.Run("rate limits before the submission port", func(t *testing.T) {
		service := &arenaParticipantServiceStub{}
		controller := newArenaParticipantController(service, &arenaSubmissionLimiterStub{retryAfter: "90"})
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/submissions",
			`{"expected_projection_revision":41,"submitted_flag":"`+flag+`"}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.SubmitArenaParticipantFlag(
					w, r, tournamentID, seriesID, gameID,
					api.SubmitArenaParticipantFlagParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusTooManyRequests, recorder.Code)
		require.Equal(t, "90", recorder.Header().Get("Retry-After"))
		require.Zero(t, service.submissionCalls)
		require.NotContains(t, recorder.Body.String(), flag)
	})

	t.Run("sanitizes submission service errors", func(t *testing.T) {
		service := &arenaParticipantServiceStub{submissionErr: errors.New("validator stack contains " + flag)}
		controller := newArenaParticipantController(service, &arenaSubmissionLimiterStub{allow: true})
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/submissions",
			`{"expected_projection_revision":41,"submitted_flag":"`+flag+`"}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.SubmitArenaParticipantFlag(
					w, r, tournamentID, seriesID, gameID,
					api.SubmitArenaParticipantFlagParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusInternalServerError, recorder.Code)
		require.NotContains(t, recorder.Body.String(), flag)
		require.NotContains(t, recorder.Body.String(), "validator stack")
	})

	t.Run("surrenders with authenticated ownership and explicit confirmation", func(t *testing.T) {
		resultID := uuid.New()
		service := &arenaParticipantServiceStub{surrenderResult: api.ArenaOfficialResultRevision{
			Id: &resultID, TournamentId: tournamentID, SeriesId: seriesID,
			SubjectKind: api.ArenaOfficialResultSubjectKindSeries, ActorKind: api.Server,
		}}
		controller := newArenaParticipantController(service, &arenaSubmissionLimiterStub{allow: true})
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/surrender",
			`{"expected_projection_revision":42,"confirmed":true,"reason":"cannot continue"}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.SurrenderArenaParticipantSeries(
					w, r, tournamentID, seriesID,
					api.SurrenderArenaParticipantSeriesParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, playerID, service.surrenderCommand.Actor.PlayerID)
		require.Equal(t, commandID, service.surrenderCommand.CommandID)
		require.Equal(t, int64(42), service.surrenderCommand.ExpectedProjectionRevision)
		require.True(t, service.surrenderCommand.Confirmed)
		require.Equal(t, "cannot continue", service.surrenderCommand.Reason)
	})

	t.Run("rejects invalid surrender input before dispatch", func(t *testing.T) {
		tests := []struct {
			name string
			body string
		}{
			{name: "stale revision", body: `{"expected_projection_revision":0,"confirmed":true}`},
			{name: "not confirmed", body: `{"expected_projection_revision":42,"confirmed":false}`},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				service := &arenaParticipantServiceStub{}
				controller := newArenaParticipantController(service, &arenaSubmissionLimiterStub{allow: true})
				recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/surrender", test.body,
					func(w http.ResponseWriter, r *http.Request) {
						controller.SurrenderArenaParticipantSeries(
							w, r, tournamentID, seriesID,
							api.SurrenderArenaParticipantSeriesParams{IdempotencyKey: commandID},
						)
					})

				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Zero(t, service.surrenderCalls)
			})
		}
	})

	t.Run("wires the optional submission limiter into the participant controller", func(t *testing.T) {
		service := &arenaParticipantServiceStub{}
		limiter := &arenaSubmissionLimiterStub{retryAfter: "45"}
		server := New(Dependencies{
			ArenaParticipantService: service,
			ArenaSubmissionLimiter:  limiter,
		})
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/submissions",
			`{"expected_projection_revision":41,"submitted_flag":"`+flag+`"}`,
			func(w http.ResponseWriter, r *http.Request) {
				server.SubmitArenaParticipantFlag(
					w, r, tournamentID, seriesID, gameID,
					api.SubmitArenaParticipantFlagParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusTooManyRequests, recorder.Code)
		require.Equal(t, "45", recorder.Header().Get("Retry-After"))
		require.Zero(t, service.submissionCalls)
	})
}

type arenaSubmissionLimiterStub struct {
	allow      bool
	retryAfter string
	keys       []string
}

func (s *arenaSubmissionLimiterStub) Allow(key string) bool {
	s.keys = append(s.keys, key)
	return s.allow
}

func (s *arenaSubmissionLimiterStub) RetryAfter() string { return s.retryAfter }
