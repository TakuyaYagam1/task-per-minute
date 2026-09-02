package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestArenaOperatorHandlers(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("ee3b576c-1e16-40cf-b6c9-94734dbcbf2d")
	seriesID := uuid.MustParse("ce0cc9f3-c573-4904-af93-4f38a22f93cd")
	gameID := uuid.MustParse("6efc183c-3504-43ea-b202-8dfe2c157381")
	commandID := uuid.MustParse("83130187-ae82-45e1-ad8d-a7b00e087560")
	winnerID := uuid.MustParse("d6f5b116-c0bb-4cf0-9736-02565473d6c1")
	requestedAt := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	digest := "e22e00c0fc743f0d45eface4a1f9ffdc48592c5c3790d828ca2ce4b22447e158"

	t.Run("dispatches a confirmed no-show correction with scoped evidence", func(t *testing.T) {
		fields := []api.ArenaCorrectionField{api.ResultReason, api.Winner}
		service := &arenaOperatorServiceStub{
			arenaAdminServiceStub: &arenaAdminServiceStub{},
			correctionResult: api.ArenaCorrectionEvidence{
				CommandId: &commandID, TournamentId: &tournamentID, SeriesId: &seriesID, GameId: &gameID,
				OperatorId: &commandID, Reason: api.OperatorRuling, Fields: &fields,
				RequestedAt: &requestedAt, ValidationDigest: &digest,
			},
		}
		controller := newArenaAdminController(service)
		body := `{"expected_projection_revision":17,"confirmed":true,"reason":"operator_ruling","explanation":"participant missed the ready deadline","fields":["result_reason","winner"],"patch":{"state":"completed","reason":"no_show","winner_id":"` + winnerID.String() + `","solve_metadata":{"solved_at":null,"submission_id":null,"evidence_digest":"` + digest + `"}}}`

		recorder := authenticatedArenaRequest(t, http.MethodPost, "/corrections", body, func(w http.ResponseWriter, r *http.Request) {
			controller.CorrectArenaGameResult(
				w, r, tournamentID, seriesID, gameID,
				api.CorrectArenaGameResultParams{IdempotencyKey: commandID},
			)
		})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.correctionCalls)
		require.Equal(t, ArenaOperatorCommandNoShow, service.correctionCommand.Kind)
		require.Equal(t, tournamentID, service.correctionCommand.TournamentID)
		require.Equal(t, seriesID, service.correctionCommand.SeriesID)
		require.Equal(t, gameID, service.correctionCommand.GameID)
		require.Equal(t, commandID, service.correctionCommand.CommandID)
		require.Equal(t, int64(17), service.correctionCommand.ExpectedRevision)
		require.Equal(t, "admin", service.correctionCommand.Operator.Subject)
		require.Equal(t, "participant missed the ready deadline", service.correctionCommand.Explanation)

		var response api.ArenaCorrectionEvidence
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, commandID, *response.CommandId)
		require.Equal(t, digest, *response.ValidationDigest)
	})

	t.Run("classifies forfeit and replay corrections", func(t *testing.T) {
		tests := []struct {
			name       string
			state      string
			result     string
			winner     string
			projection string
			want       ArenaOperatorCommandKind
		}{
			{name: "forfeit", state: "completed", result: "operator_forfeit", winner: `"` + winnerID.String() + `"`, want: ArenaOperatorCommandForfeit},
			{name: "replay", state: "superseded", result: "derived_revision_superseded", winner: "null", projection: `,"projection_intents":[{"expected_revision":{"id":"0e29853d-97bb-4643-89f9-759a6bb7fe25","tournament_id":"` + tournamentID.String() + `","artifact_kind":"game_result","artifact_id":"` + gameID.String() + `","revision_no":17,"previous_revision_id":null,"payload_digest":"` + digest + `","created_at":"2026-09-02T12:00:00Z"},"next_revision_id":"22388e80-fc25-4cc6-8c01-0a44e18e47e6","decision_id":"5a26281e-0f98-4aeb-b55a-325f17f42865","payload_digest":"` + digest + `"}]`, want: ArenaOperatorCommandReplay},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				service := &arenaOperatorServiceStub{arenaAdminServiceStub: &arenaAdminServiceStub{}}
				controller := newArenaAdminController(service)
				body := `{"expected_projection_revision":17,"confirmed":true,"reason":"operator_ruling","explanation":"verified operator decision","fields":["result_reason","winner"],"patch":{"state":"` + test.state + `","reason":"` + test.result + `","winner_id":` + test.winner + `,"solve_metadata":{"solved_at":null,"submission_id":null,"evidence_digest":"` + digest + `"}}` + test.projection + `}`

				recorder := authenticatedArenaRequest(t, http.MethodPost, "/corrections", body, func(w http.ResponseWriter, r *http.Request) {
					controller.CorrectArenaGameResult(w, r, tournamentID, seriesID, gameID, api.CorrectArenaGameResultParams{IdempotencyKey: commandID})
				})

				require.Equal(t, http.StatusOK, recorder.Code)
				require.Equal(t, test.want, service.correctionCommand.Kind)
			})
		}
	})

	t.Run("rejects unconfirmed or incomplete correction evidence", func(t *testing.T) {
		service := &arenaOperatorServiceStub{arenaAdminServiceStub: &arenaAdminServiceStub{}}
		controller := newArenaAdminController(service)
		body := `{"expected_projection_revision":17,"confirmed":false,"reason":"operator_ruling","explanation":"","fields":["result_reason"],"patch":{"state":"completed","reason":"no_show","winner_id":null,"solve_metadata":{"solved_at":null,"submission_id":null,"evidence_digest":"short"}}}`

		recorder := authenticatedArenaRequest(t, http.MethodPost, "/corrections", body, func(w http.ResponseWriter, r *http.Request) {
			controller.CorrectArenaGameResult(w, r, tournamentID, seriesID, gameID, api.CorrectArenaGameResultParams{IdempotencyKey: commandID})
		})

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Zero(t, service.correctionCalls)
	})

	t.Run("returns a stable sanitized correction conflict", func(t *testing.T) {
		service := &arenaOperatorServiceStub{
			arenaAdminServiceStub: &arenaAdminServiceStub{},
			correctionErr: &ArenaRevisionConflictError{
				ExpectedRevision: 17,
				CurrentRevision:  19,
				CurrentState:     "technical_pause",
			},
		}
		controller := newArenaAdminController(service)
		body := `{"expected_projection_revision":17,"confirmed":true,"reason":"operator_ruling","explanation":"verified operator decision","fields":["result_reason","winner"],"patch":{"state":"completed","reason":"operator_forfeit","winner_id":"` + winnerID.String() + `","solve_metadata":{"solved_at":null,"submission_id":null,"evidence_digest":"` + digest + `"}}}`

		recorder := authenticatedArenaRequest(t, http.MethodPost, "/corrections", body, func(w http.ResponseWriter, r *http.Request) {
			controller.CorrectArenaGameResult(w, r, tournamentID, seriesID, gameID, api.CorrectArenaGameResultParams{IdempotencyKey: commandID})
		})

		require.Equal(t, http.StatusConflict, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "database")
		var conflict api.ArenaRevisionConflict
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &conflict))
		require.Equal(t, int64(17), conflict.ExpectedRevision)
		require.Equal(t, int64(19), conflict.CurrentRevision)
	})

	t.Run("dispatches explicit operator commands with authenticated authority", func(t *testing.T) {
		waveID := uuid.MustParse("f11541d2-f5b0-45b4-a687-a02cc8da716b")
		assignmentID := uuid.MustParse("0ce03d57-5c69-40e8-a7a3-7ac4dc066f47")
		evidenceID := uuid.MustParse("61cc9814-d6a5-4c69-8dc3-59e84df54cfc")
		service := &arenaOperatorServiceStub{arenaAdminServiceStub: &arenaAdminServiceStub{}}
		controller := newArenaAdminController(service)
		marshal := func(t *testing.T, value any) string {
			t.Helper()
			body, err := json.Marshal(value)
			require.NoError(t, err)
			return string(body)
		}

		noShow := api.ArenaOperatorNoShowRequest{
			TournamentId: tournamentID, WaveId: waveID, WindowId: evidenceID, SeriesId: seriesID,
			Confirmed: true, Reason: " missed ready deadline ", ExpectedAuthorityRevision: 8,
			ExpectedWaveRevisionId: evidenceID, ExpectedWindowRevisionId: commandID,
			ExpectedSeriesState: api.ArenaSeriesStateActive, GameResultRevisionIds: []uuid.UUID{gameID},
			ScoreRevisionId: evidenceID, SeriesResultRevisionId: commandID,
		}
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/no-shows", marshal(t, noShow), func(w http.ResponseWriter, r *http.Request) {
			controller.ResolveArenaNoShow(w, r, tournamentID, waveID, api.ResolveArenaNoShowParams{IdempotencyKey: commandID})
		})
		require.Equal(t, http.StatusNoContent, recorder.Code)
		require.Equal(t, "missed ready deadline", service.noShowCommand.Request.Reason)
		require.Equal(t, "admin", service.noShowCommand.Operator.Subject)
		require.Equal(t, commandID, service.noShowCommand.CommandID)

		reserve := api.ArenaOperatorReserveRequest{
			TournamentId: tournamentID, OldWaveId: waveID, SeriesId: seriesID, SlotId: evidenceID,
			AssignmentId: assignmentID, AssignmentAttemptId: commandID, Confirmed: true, Reason: " reserve after exhaustion ",
			ExpectedAuthorityRevision: 9, ExpectedExhaustionCommandId: evidenceID,
			ExpectedAssignmentRevision: 2, ExpectedPoolRevisionId: evidenceID, ExpectedPoolRevision: 2,
			ExpectedHistoryRevisionId: commandID, ExpectedHistoryRevision: 2,
			ExpectedArtifactRevisionId: evidenceID, ExpectedArtifactRevision: 2,
			ExpectedReservationRevisionId: commandID, ExpectedReservationRevision: 2,
			ExpectedCategoryRevisionId: evidenceID, ExpectedCategoryRevision: 2,
			ProposedTaskId: evidenceID, ProposedVersion: 3, ProposedSnapshotId: commandID,
			ExpectedSnapshotId: evidenceID, EvidenceId: commandID,
		}
		recorder = authenticatedArenaRequest(t, http.MethodPost, "/operator-reserves", marshal(t, reserve), func(w http.ResponseWriter, r *http.Request) {
			controller.AssignArenaOperatorReserve(w, r, tournamentID, seriesID, assignmentID, api.AssignArenaOperatorReserveParams{IdempotencyKey: commandID})
		})
		require.Equal(t, http.StatusNoContent, recorder.Code)
		require.Equal(t, assignmentID, service.reserveCommand.Request.AssignmentId)
		require.Equal(t, "reserve after exhaustion", service.reserveCommand.Request.Reason)

		forfeit := api.ArenaOperatorForfeitRequest{
			TournamentId: tournamentID, SeriesId: seriesID, ForfeitingParticipantId: evidenceID,
			Confirmed: true, Reason: " rule violation ", ExpectedAuthorityRevision: 10,
			Basis: api.RuleViolation, RuleId: "arena.rule.1", EvidenceIds: []uuid.UUID{evidenceID},
			ScoreRevisionId: evidenceID, SeriesResultRevisionId: commandID, AuditEventId: evidenceID,
			OutboxEventId: commandID, ProjectionRevisionId: evidenceID,
		}
		recorder = authenticatedArenaRequest(t, http.MethodPost, "/operator-forfeits", marshal(t, forfeit), func(w http.ResponseWriter, r *http.Request) {
			controller.RecordArenaOperatorForfeit(w, r, tournamentID, seriesID, api.RecordArenaOperatorForfeitParams{IdempotencyKey: commandID})
		})
		require.Equal(t, http.StatusNoContent, recorder.Code)
		require.Equal(t, seriesID, service.forfeitCommand.Request.SeriesId)
		require.Equal(t, "rule violation", service.forfeitCommand.Request.Reason)

		replay := api.ArenaOperatorReplayRequest{
			TournamentId: tournamentID, OldWaveId: waveID, SeriesId: seriesID, SlotId: evidenceID,
			AssignmentId: assignmentID, FailedGameId: gameID, Confirmed: true, Reason: " task failure ",
			ExpectedAuthorityRevision: 11, ExpectedClosureRevisionId: evidenceID,
			AssignmentAttemptId: commandID, ReplacementGameId: evidenceID, ReplacementWaveId: commandID,
			ReplacementWaveRevisionId: evidenceID, ReadyWindowId: commandID, ReadyWindowRevisionId: evidenceID,
		}
		recorder = authenticatedArenaRequest(t, http.MethodPost, "/replays", marshal(t, replay), func(w http.ResponseWriter, r *http.Request) {
			controller.ReplayArenaOperatorGame(w, r, tournamentID, seriesID, gameID, api.ReplayArenaOperatorGameParams{IdempotencyKey: commandID})
		})
		require.Equal(t, http.StatusNoContent, recorder.Code)
		require.Equal(t, gameID, service.replayCommand.Request.FailedGameId)
		require.Equal(t, "task failure", service.replayCommand.Request.Reason)

		require.Equal(t, 1, service.noShowCalls)
		require.Equal(t, 1, service.reserveCalls)
		require.Equal(t, 1, service.forfeitCalls)
		require.Equal(t, 1, service.replayCalls)
	})

	t.Run("rejects explicit operator commands with mismatched scope or missing confirmation", func(t *testing.T) {
		waveID := uuid.MustParse("f11541d2-f5b0-45b4-a687-a02cc8da716b")
		service := &arenaOperatorServiceStub{arenaAdminServiceStub: &arenaAdminServiceStub{}}
		controller := newArenaAdminController(service)
		body, err := json.Marshal(api.ArenaOperatorNoShowRequest{
			TournamentId: uuid.MustParse("32fc5246-7b0a-448a-aaef-ef57e6bef860"), WaveId: waveID,
			WindowId: commandID, SeriesId: seriesID, Confirmed: false, Reason: " ",
			ExpectedAuthorityRevision: 8, ExpectedWaveRevisionId: commandID,
			ExpectedWindowRevisionId: commandID, ExpectedSeriesState: api.ArenaSeriesStateActive,
			GameResultRevisionIds: []uuid.UUID{gameID}, ScoreRevisionId: commandID, SeriesResultRevisionId: commandID,
		})
		require.NoError(t, err)

		recorder := authenticatedArenaRequest(t, http.MethodPost, "/no-shows", string(body), func(w http.ResponseWriter, r *http.Request) {
			controller.ResolveArenaNoShow(w, r, tournamentID, waveID, api.ResolveArenaNoShowParams{IdempotencyKey: commandID})
		})

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Zero(t, service.noShowCalls)
	})
}

type arenaOperatorServiceStub struct {
	*arenaAdminServiceStub

	correctionCalls   int
	correctionCommand ArenaOperatorCorrectionCommand
	correctionResult  api.ArenaCorrectionEvidence
	correctionErr     error

	noShowCalls   int
	noShowCommand ArenaNoShowCommand
	noShowErr     error

	reserveCalls   int
	reserveCommand ArenaReserveAssignmentCommand
	reserveErr     error

	forfeitCalls   int
	forfeitCommand ArenaForfeitCommand
	forfeitErr     error

	replayCalls   int
	replayCommand ArenaReplayCommand
	replayErr     error
}

func (s *arenaOperatorServiceStub) CorrectGame(
	_ context.Context,
	command ArenaOperatorCorrectionCommand,
) (api.ArenaCorrectionEvidence, error) {
	s.correctionCalls++
	s.correctionCommand = command
	return s.correctionResult, s.correctionErr
}

func (s *arenaOperatorServiceStub) ResolveNoShow(_ context.Context, command ArenaNoShowCommand) error {
	s.noShowCalls++
	s.noShowCommand = command
	return s.noShowErr
}

func (s *arenaOperatorServiceStub) AssignReserve(_ context.Context, command ArenaReserveAssignmentCommand) error {
	s.reserveCalls++
	s.reserveCommand = command
	return s.reserveErr
}

func (s *arenaOperatorServiceStub) RecordForfeit(_ context.Context, command ArenaForfeitCommand) error {
	s.forfeitCalls++
	s.forfeitCommand = command
	return s.forfeitErr
}

func (s *arenaOperatorServiceStub) ReplayGame(_ context.Context, command ArenaReplayCommand) error {
	s.replayCalls++
	s.replayCommand = command
	return s.replayErr
}
