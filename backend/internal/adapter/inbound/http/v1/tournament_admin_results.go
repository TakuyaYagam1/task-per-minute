package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (c *tournamentController) ResolveTournamentNoShow(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	waveID api.WaveId,
	params api.ResolveTournamentNoShowParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.OperatorNoShowRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	if body.TournamentId != tournamentID || body.WaveId != waveID {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	err := service.ResolveNoShow(r.Context(), inbound.AdminNoShowCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		},
		WaveID: body.WaveId, WindowID: body.WindowId, SeriesID: body.SeriesId,
		Confirmed: body.Confirmed, Reason: body.Reason,
		ExpectedAuthorityRevision: body.ExpectedAuthorityRevision,
		ExpectedWaveRevisionID:    body.ExpectedWaveRevisionId,
		ExpectedWindowRevisionID:  body.ExpectedWindowRevisionId,
		ExpectedSeriesState:       domain.SeriesState(body.ExpectedSeriesState),
		GameResultRevisionIDs:     append([]uuid.UUID(nil), body.GameResultRevisionIds...),
		ScoreRevisionID:           body.ScoreRevisionId, SeriesResultRevisionID: body.SeriesResultRevisionId,
	})
	writeTournamentNoContent(w, r, err)
}

func (c *tournamentController) AssignOperatorReserve(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	seriesID api.SeriesId,
	assignmentID api.AssignmentId,
	params api.AssignOperatorReserveParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.OperatorReserveRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	if body.TournamentId != tournamentID || body.SeriesId != seriesID || body.AssignmentId != assignmentID {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	err := service.AssignReserve(r.Context(), inbound.AdminReserveCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		},
		OldWaveID: body.OldWaveId, SeriesID: seriesID, SlotID: body.SlotId,
		AssignmentID: assignmentID, AssignmentAttemptID: body.AssignmentAttemptId,
		Confirmed: body.Confirmed, Reason: body.Reason,
		ExpectedAuthorityRevision:     body.ExpectedAuthorityRevision,
		ExpectedExhaustionCommandID:   body.ExpectedExhaustionCommandId,
		ExpectedAssignmentRevision:    body.ExpectedAssignmentRevision,
		ExpectedPoolRevisionID:        body.ExpectedPoolRevisionId,
		ExpectedPoolRevision:          body.ExpectedPoolRevision,
		ExpectedHistoryRevisionID:     body.ExpectedHistoryRevisionId,
		ExpectedHistoryRevision:       body.ExpectedHistoryRevision,
		ExpectedArtifactRevisionID:    body.ExpectedArtifactRevisionId,
		ExpectedArtifactRevision:      body.ExpectedArtifactRevision,
		ExpectedReservationRevisionID: body.ExpectedReservationRevisionId,
		ExpectedReservationRevision:   body.ExpectedReservationRevision,
		ExpectedCategoryRevisionID:    body.ExpectedCategoryRevisionId,
		ExpectedCategoryRevision:      body.ExpectedCategoryRevision,
		ProposedTaskID:                body.ProposedTaskId, ProposedVersion: int(body.ProposedVersion),
		ProposedSnapshotID: body.ProposedSnapshotId, ExpectedSnapshotID: body.ExpectedSnapshotId,
		EvidenceID: body.EvidenceId,
	})
	writeTournamentNoContent(w, r, err)
}

func (c *tournamentController) RecordTournamentForfeit(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	seriesID api.SeriesId,
	params api.RecordTournamentForfeitParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.OperatorForfeitRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	if body.TournamentId != tournamentID || body.SeriesId != seriesID {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	var expectedGame *inbound.AdminGameExpectation
	if body.ExpectedGame != nil {
		expectedGame = &inbound.AdminGameExpectation{
			SlotID: body.ExpectedGame.SlotId, GameID: body.ExpectedGame.GameId,
			AttemptNo: int(body.ExpectedGame.AttemptNo), State: domain.GameState(body.ExpectedGame.State),
		}
	}
	err := service.RecordForfeit(r.Context(), inbound.AdminForfeitCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		},
		SeriesID: seriesID, ForfeitingParticipantID: body.ForfeitingParticipantId,
		Confirmed: body.Confirmed, Reason: body.Reason,
		ExpectedAuthorityRevision: body.ExpectedAuthorityRevision,
		ExpectedGame:              expectedGame, Basis: string(body.Basis), RuleID: body.RuleId,
		EvidenceIDs:          append([]uuid.UUID(nil), body.EvidenceIds...),
		GameResultRevisionID: cloneUUIDPointer(body.GameResultRevisionId),
		ScoreRevisionID:      body.ScoreRevisionId, SeriesResultRevisionID: body.SeriesResultRevisionId,
		AuditEventID: body.AuditEventId, OutboxEventID: body.OutboxEventId,
		ProjectionRevisionID: body.ProjectionRevisionId,
	})
	writeTournamentNoContent(w, r, err)
}

func (c *tournamentController) ReplayTournamentGame(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	seriesID api.SeriesId,
	gameID api.GameId,
	params api.ReplayTournamentGameParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.OperatorReplayRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	if body.TournamentId != tournamentID || body.SeriesId != seriesID || body.FailedGameId != gameID {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	err := service.ReplayGame(r.Context(), inbound.AdminReplayCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		},
		OldWaveID: body.OldWaveId, SeriesID: seriesID, SlotID: body.SlotId,
		AssignmentID: body.AssignmentId, FailedGameID: gameID,
		Confirmed: body.Confirmed, Reason: body.Reason,
		ExpectedAuthorityRevision: body.ExpectedAuthorityRevision,
		ExpectedClosureRevisionID: body.ExpectedClosureRevisionId,
		AssignmentAttemptID:       body.AssignmentAttemptId,
		ReplacementGameID:         body.ReplacementGameId, ReplacementWaveID: body.ReplacementWaveId,
		ReplacementWaveRevisionID: body.ReplacementWaveRevisionId,
		ReadyWindowID:             body.ReadyWindowId, ReadyWindowRevisionID: body.ReadyWindowRevisionId,
	})
	writeTournamentNoContent(w, r, err)
}

func (c *tournamentController) CorrectTournamentGameResult(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	seriesID api.SeriesId,
	gameID api.GameId,
	params api.CorrectTournamentGameResultParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.OperatorCorrectionRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	command, err := tournamentCorrectionCommand(operator, tournamentID, seriesID, gameID, params.IdempotencyKey, body)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	evidence, err := service.CorrectGameResult(r.Context(), command)
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentCorrectionResponse(evidence)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func tournamentCorrectionCommand(
	operator inbound.AdminOperatorIdentity,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
	gameID uuid.UUID,
	commandID uuid.UUID,
	body api.OperatorCorrectionRequest,
) (inbound.AdminCorrectionCommand, error) {
	digest, err := parseSHA256Hex(body.Patch.SolveMetadata.EvidenceDigest)
	if err != nil {
		return inbound.AdminCorrectionCommand{}, domain.ErrValidation
	}
	command := inbound.AdminCorrectionCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: commandID,
		},
		SeriesID: seriesID, GameID: gameID,
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		Confirmed:                  body.Confirmed, Reason: string(body.Reason), Explanation: body.Explanation,
		Fields: make([]string, len(body.Fields)),
		Patch: inbound.AdminCorrectionPatch{
			State: domain.GameState(body.Patch.State), Reason: domain.GameResultReason(body.Patch.Reason),
			WinnerID: cloneUUIDPointer(body.Patch.WinnerId), SolvedAt: cloneTimePointer(body.Patch.SolveMetadata.SolvedAt),
			SubmissionID: cloneUUIDPointer(body.Patch.SolveMetadata.SubmissionId), EvidenceDigest: digest,
		},
	}
	for index, field := range body.Fields {
		command.Fields[index] = string(field)
	}
	if body.ProjectionIntents != nil {
		command.ProjectionIntents = make([]inbound.AdminCorrectionProjectionIntent, len(*body.ProjectionIntents))
		for index, intent := range *body.ProjectionIntents {
			mapped, mapErr := tournamentCorrectionProjectionIntent(intent)
			if mapErr != nil {
				return inbound.AdminCorrectionCommand{}, mapErr
			}
			command.ProjectionIntents[index] = mapped
		}
	}
	if body.UnlockIntents != nil {
		command.UnlockIntents = make([]inbound.AdminCorrectionUnlockIntent, len(*body.UnlockIntents))
		for index, intent := range *body.UnlockIntents {
			mapped, mapErr := tournamentCorrectionUnlockIntent(intent)
			if mapErr != nil {
				return inbound.AdminCorrectionCommand{}, mapErr
			}
			command.UnlockIntents[index] = mapped
		}
	}
	return command, nil
}

func tournamentCorrectionProjectionIntent(
	value api.CorrectionProjectionIntent,
) (inbound.AdminCorrectionProjectionIntent, error) {
	expectedDigest, err := parseSHA256Hex(value.ExpectedRevision.PayloadDigest)
	if err != nil {
		return inbound.AdminCorrectionProjectionIntent{}, domain.ErrValidation
	}
	payloadDigest, err := parseSHA256Hex(value.PayloadDigest)
	if err != nil {
		return inbound.AdminCorrectionProjectionIntent{}, domain.ErrValidation
	}
	return inbound.AdminCorrectionProjectionIntent{
		ExpectedRevision: inbound.AdminProjectionRevisionExpectation{
			ID: value.ExpectedRevision.Id, TournamentID: value.ExpectedRevision.TournamentId,
			ArtifactKind: string(value.ExpectedRevision.ArtifactKind), ArtifactID: value.ExpectedRevision.ArtifactId,
			RevisionNo:         int(value.ExpectedRevision.RevisionNo),
			PreviousRevisionID: cloneUUIDPointer(value.ExpectedRevision.PreviousRevisionId),
			PayloadDigest:      expectedDigest, CreatedAt: value.ExpectedRevision.CreatedAt,
		},
		NextRevisionID: value.NextRevisionId, DecisionID: value.DecisionId, PayloadDigest: payloadDigest,
	}, nil
}

func tournamentCorrectionUnlockIntent(
	value api.CorrectionUnlockIntent,
) (inbound.AdminCorrectionUnlockIntent, error) {
	evidenceDigest, err := parseSHA256Hex(value.EvidenceDigest)
	if err != nil {
		return inbound.AdminCorrectionUnlockIntent{}, domain.ErrValidation
	}
	bindingDigest, err := parseSHA256Hex(value.BindingDigest)
	if err != nil {
		return inbound.AdminCorrectionUnlockIntent{}, domain.ErrValidation
	}
	return inbound.AdminCorrectionUnlockIntent{
		ReservationID: value.ReservationId, TournamentID: value.TournamentId, OwnerID: value.OwnerId,
		SourceRevisionID: value.SourceRevisionId, ExpectedRevision: value.ExpectedRevision,
		ExpectedUsed: value.ExpectedUsed, ExpectedDisclosed: value.ExpectedDisclosed,
		EvidenceDigest: evidenceDigest, BindingDigest: bindingDigest,
	}, nil
}

func parseSHA256Hex(value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return digest, domain.ErrValidation
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return digest, domain.ErrValidation
	}
	copy(digest[:], decoded)
	return digest, nil
}

func writeTournamentNoContent(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
