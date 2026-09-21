package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestTournamentIncidentResponseIncludesHMACAuthenticityEnvelope(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentAdminTestID(100)
	generatedAt := tournamentAdminTestTime()
	canonicalContent := []byte(`{"tournament_id":"10000000-0000-4000-8000-000000000100"}`)
	bundle := inbound.AdminIncidentBundle{
		TournamentID:       tournamentID,
		ProjectionRevision: 3,
		GeneratedAt:        generatedAt,
		CanonicalContent:   canonicalContent,
		SHA256:             sha256.Sum256(canonicalContent),
		Algorithm:          "hmac-sha256-v1",
		KeyID:              "incident-2026-09",
		MAC:                sha256.Sum256([]byte("signature")),
	}

	payload := tournamentIncidentResponse(bundle)
	require.Equal(t, api.HmacSha256V1, payload.Algorithm)
	require.Equal(t, "incident-2026-09", payload.KeyId)
	require.Equal(t, hex.EncodeToString(bundle.MAC[:]), payload.Mac)
	require.Equal(t, hex.EncodeToString(bundle.SHA256[:]), payload.Sha256)
}

func TestParseSHA256Hex(t *testing.T) {
	t.Parallel()

	valid := strings.Repeat("ab", 32)
	digest, err := parseSHA256Hex(valid)
	require.NoError(t, err)
	require.Equal(t, valid, hex.EncodeToString(digest[:]))

	for _, value := range []string{
		strings.ToUpper(valid),
		valid[:len(valid)-1],
		strings.Repeat("zz", 32),
	} {
		t.Run(value[:min(len(value), 12)], func(t *testing.T) {
			t.Parallel()
			_, parseErr := parseSHA256Hex(value)
			require.ErrorIs(t, parseErr, domain.ErrValidation)
		})
	}
}

func TestTournamentActionRejectsManualCompletion(t *testing.T) {
	t.Parallel()

	_, err := tournamentAction(api.TournamentActionRequestAction("complete"))
	require.ErrorIs(t, err, domain.ErrValidation)

	action, err := tournamentAction(api.TournamentActionRequestActionStartPlayoffs)
	require.NoError(t, err)
	require.Equal(t, inbound.AdminTournamentActionStartPlayoffs, action)
}

func TestTournamentCorrectionCommandPreservesCASAndEvidence(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentAdminTestID(1)
	seriesID := tournamentAdminTestID(2)
	gameID := tournamentAdminTestID(3)
	commandID := tournamentAdminTestID(4)
	operatorID := tournamentAdminTestID(5)
	winnerID := tournamentAdminTestID(6)
	submissionID := tournamentAdminTestID(7)
	solvedAt := tournamentAdminTestTime()
	previousRevisionID := tournamentAdminTestID(8)
	digest := strings.Repeat("1a", 32)
	bindingDigest := strings.Repeat("2b", 32)
	projectionIntents := []api.CorrectionProjectionIntent{{
		ExpectedRevision: api.CorrectionProjectionRevisionExpectation{
			Id: tournamentAdminTestID(10), TournamentId: tournamentID,
			ArtifactKind: api.ArtifactKindGameResult, ArtifactId: gameID, RevisionNo: 3,
			PreviousRevisionId: &previousRevisionID, PayloadDigest: digest, CreatedAt: tournamentAdminTestTime(),
		},
		NextRevisionId: tournamentAdminTestID(11), DecisionId: tournamentAdminTestID(12),
		PayloadDigest: digest,
	}}
	unlockIntents := []api.CorrectionUnlockIntent{{
		ReservationId: tournamentAdminTestID(20), TournamentId: tournamentID,
		OwnerId: tournamentAdminTestID(21), SourceRevisionId: tournamentAdminTestID(22),
		ExpectedRevision: 4, ExpectedUsed: false, ExpectedDisclosed: false,
		EvidenceDigest: digest, BindingDigest: bindingDigest,
	}}
	body := api.OperatorCorrectionRequest{
		SourceResultRevision: tournamentAdminTestID(9), ExpectedProjectionRevision: 9, Confirmed: true, Reason: api.OperatorRuling,
		Explanation: "verified operator correction", Fields: []api.CorrectionField{api.Winner, api.SolveMetadata},
		Patch: api.CorrectionPatch{
			State: api.GameStateCompleted, Reason: api.GameResultReasonSolved, WinnerId: &winnerID,
			SolveMetadata: api.CorrectionSolveMetadata{
				SolvedAt: &solvedAt, SubmissionId: &submissionID, EvidenceDigest: digest,
			},
		},
		ProjectionIntents: projectionIntents,
		UnlockIntents:     unlockIntents,
	}

	command, err := tournamentCorrectionCommand(
		inbound.AdminOperatorIdentity{ActorID: operatorID},
		tournamentID,
		seriesID,
		gameID,
		commandID,
		body,
	)
	require.NoError(t, err)
	require.Equal(t, operatorID, command.Operator.ActorID)
	require.Equal(t, tournamentID, command.TournamentID)
	require.Equal(t, seriesID, command.SeriesID)
	require.Equal(t, gameID, command.GameID)
	require.Equal(t, tournamentAdminTestID(9), command.SourceResultRevision)
	require.Equal(t, commandID, command.CommandID)
	require.Equal(t, int64(9), command.ExpectedProjectionRevision)
	require.Equal(t, []string{"winner", "solve_metadata"}, command.Fields)
	require.Equal(t, domain.GameStateCompleted, command.Patch.State)
	require.Equal(t, domain.GameResultReasonSolved, command.Patch.Reason)
	require.Equal(t, winnerID, *command.Patch.WinnerID)
	require.Equal(t, submissionID, *command.Patch.SubmissionID)
	require.Equal(t, solvedAt, *command.Patch.SolvedAt)
	require.Len(t, command.ProjectionIntents, 1)
	require.Equal(t, tournamentID, command.ProjectionIntents[0].ExpectedRevision.TournamentID)
	require.Equal(t, previousRevisionID, *command.ProjectionIntents[0].ExpectedRevision.PreviousRevisionID)
	require.Len(t, command.UnlockIntents, 1)
	require.Equal(t, int64(4), command.UnlockIntents[0].ExpectedRevision)
	require.False(t, command.UnlockIntents[0].ExpectedUsed)
	require.False(t, command.UnlockIntents[0].ExpectedDisclosed)

	projectionIntents[0].ExpectedRevision.TournamentId = tournamentAdminTestID(999)
	unlockIntents[0].ExpectedRevision = 99
	require.Equal(t, tournamentID, command.ProjectionIntents[0].ExpectedRevision.TournamentID)
	require.Equal(t, int64(4), command.UnlockIntents[0].ExpectedRevision)
}

func TestTournamentCorrectionCommandRejectsMalformedDigest(t *testing.T) {
	t.Parallel()

	body := api.OperatorCorrectionRequest{
		Patch: api.CorrectionPatch{
			SolveMetadata: api.CorrectionSolveMetadata{EvidenceDigest: strings.Repeat("AB", 32)},
		},
	}
	_, err := tournamentCorrectionCommand(
		inbound.AdminOperatorIdentity{ActorID: tournamentAdminTestID(1)},
		tournamentAdminTestID(2), tournamentAdminTestID(3), tournamentAdminTestID(4),
		tournamentAdminTestID(5), body,
	)
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestTournamentSwissMappingDoesNotDropInvalidOptionalRecords(t *testing.T) {
	t.Parallel()

	_, err := tournamentSwissByeResponse(&inbound.AdminSwissByeView{PointsAwarded: int(^uint32(0))})
	require.ErrorIs(t, err, domain.ErrInternal)

	_, err = tournamentSwissPairingEvidenceResponse(&inbound.AdminSwissPairingEvidenceView{
		Purpose: "unknown", AlgorithmVersion: domain.DecisionAlgorithmV1,
	})
	require.ErrorIs(t, err, domain.ErrInternal)

	_, err = tournamentSwissPairingEvidenceResponse(&inbound.AdminSwissPairingEvidenceView{
		Purpose: string(domain.DecisionPurposePairing), AlgorithmVersion: "unknown",
	})
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestTournamentRecoveryControlsResponsePreservesEmptyAndLegalReplay(t *testing.T) {
	t.Parallel()

	empty, err := tournamentRecoveryControlsResponse(nil)
	require.NoError(t, err)
	require.NotNil(t, empty)
	require.Empty(t, empty)

	assignmentID, seriesID, slotID := uuid.New(), uuid.New(), uuid.New()
	oldWaveID, closureID, attemptID, resultRevisionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	controls, err := tournamentRecoveryControlsResponse([]inbound.AdminRecoveryControl{{
		AssignmentID: assignmentID, SeriesID: seriesID, SlotID: slotID, OldWaveID: oldWaveID,
		Attempts: []domain.Game{{
			ID: attemptID, SlotID: slotID, AttemptNo: 1, State: domain.GameStateVoid,
			ResultReason: domain.GameResultReasonNoSolve,
			ResultRevisionID: func() *domain.OfficialResultRevisionID {
				value := domain.OfficialResultRevisionID(resultRevisionID)
				return &value
			}(),
		}},
		Category: domain.CategoryWeb, ExpectedAuthorityRevision: 7,
		Kind: inbound.AdminRecoveryControlReplay, Reason: "no solve",
		Replay: &inbound.AdminRecoveryReplayDetails{Available: true, ExpectedClosureRevisionID: closureID},
	}})
	require.NoError(t, err)
	require.Len(t, controls, 1)
	require.Equal(t, api.Replay, controls[0].Kind)
	require.Equal(t, closureID, controls[0].Replay.ExpectedClosureRevisionId)
	require.Len(t, controls[0].Attempts, 1)
	require.Equal(t, api.GameResultReasonNoSolve, *controls[0].Attempts[0].ResultReason)
}

func TestWriteTournamentAdminErrorMapsValidatedRevisionConflict(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tournaments/example/actions", nil)
	writeTournamentAdminError(recorder, request, &inbound.AdminRevisionConflictError{
		ExpectedRevision: 4, CurrentRevision: 5, CurrentState: domain.TournamentStateSwiss,
	})

	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Equal(t, "application/problem+json", recorder.Header().Get("Content-Type"))
	var payload api.ProjectionRevisionProblem
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "about:blank", payload.Type)
	require.Equal(t, http.StatusText(http.StatusConflict), payload.Title)
	require.Equal(t, int32(http.StatusConflict), payload.Status)
	require.Equal(t, int64(4), payload.ExpectedRevision)
	require.Equal(t, int64(5), payload.CurrentRevision)
	require.NotNil(t, payload.CurrentState)
	require.Equal(t, api.TournamentStateSwiss, *payload.CurrentState)
	require.NotNil(t, payload.Detail)
	require.Equal(t, "projection revision conflict", *payload.Detail)
	require.NotNil(t, payload.Instance)
	require.Equal(t, "/api/v1/tournaments/example/actions", *payload.Instance)
}

func TestWriteTournamentAdminErrorRejectsMalformedRevisionConflict(t *testing.T) {
	t.Parallel()

	tests := []inbound.AdminRevisionConflictError{
		{ExpectedRevision: 0, CurrentRevision: 5},
		{ExpectedRevision: 4, CurrentRevision: 0},
		{ExpectedRevision: 4, CurrentRevision: 5, CurrentState: domain.TournamentState("unknown")},
	}
	for _, conflict := range tests {
		t.Run(conflict.Error(), func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tournaments/example/actions", nil)
			writeTournamentAdminError(recorder, request, &conflict)
			require.Equal(t, http.StatusInternalServerError, recorder.Code)
		})
	}
}

func TestWriteCorrectionAdminErrorMapsStableConflictCode(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tournaments/example/corrections", nil)
	writeCorrectionAdminError(recorder, request, &inbound.AdminCorrectionConflictError{
		ExpectedRevision: 4, CurrentRevision: 5, CurrentState: domain.TournamentStateSwiss,
		Code: "incomplete_unlock",
	})

	require.Equal(t, http.StatusConflict, recorder.Code)
	var payload api.CorrectionConflictProblem
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, api.IncompleteUnlock, payload.Code)
	require.Equal(t, int64(4), payload.ExpectedRevision)
	require.Equal(t, int64(5), payload.CurrentRevision)
	require.NotNil(t, payload.CurrentState)
	require.Equal(t, api.TournamentStateSwiss, *payload.CurrentState)
}

func TestWriteCorrectionAdminErrorRejectsUnknownConflictCode(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tournaments/example/corrections", nil)
	writeCorrectionAdminError(recorder, request, &inbound.AdminCorrectionConflictError{
		ExpectedRevision: 4, CurrentRevision: 5, Code: "unknown",
	})

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
}

func tournamentAdminTestID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("10000000-0000-4000-8000-%012d", value))
}

func tournamentAdminTestTime() time.Time {
	return time.Date(2026, time.September, 6, 13, 0, 0, 0, time.UTC)
}
