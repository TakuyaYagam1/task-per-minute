package v1

import (
	"context"
	"encoding/hex"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type ArenaOperatorCommandKind string

const (
	ArenaOperatorCommandCorrection ArenaOperatorCommandKind = "correction"
	ArenaOperatorCommandNoShow     ArenaOperatorCommandKind = "no_show"
	ArenaOperatorCommandForfeit    ArenaOperatorCommandKind = "forfeit"
	ArenaOperatorCommandReplay     ArenaOperatorCommandKind = "replay"
)

type ArenaOperatorCorrectionCommand struct {
	Operator          ArenaOperatorIdentity
	Kind              ArenaOperatorCommandKind
	TournamentID      uuid.UUID
	SeriesID          uuid.UUID
	GameID            uuid.UUID
	CommandID         uuid.UUID
	ExpectedRevision  int64
	Confirmed         bool
	Reason            api.ArenaCorrectionReason
	Explanation       string
	Fields            []api.ArenaCorrectionField
	Patch             api.ArenaCorrectionPatch
	ProjectionIntents []api.ArenaCorrectionProjectionIntent
	UnlockIntents     []api.ArenaCorrectionUnlockIntent
}

type ArenaOperatorCorrectionService interface {
	CorrectGame(ctx context.Context, command ArenaOperatorCorrectionCommand) (api.ArenaCorrectionEvidence, error)
}

type ArenaNoShowCommand struct {
	Operator  ArenaOperatorIdentity
	CommandID uuid.UUID
	Request   api.ArenaOperatorNoShowRequest
}

type ArenaReserveAssignmentCommand struct {
	Operator  ArenaOperatorIdentity
	CommandID uuid.UUID
	Request   api.ArenaOperatorReserveRequest
}

type ArenaForfeitCommand struct {
	Operator  ArenaOperatorIdentity
	CommandID uuid.UUID
	Request   api.ArenaOperatorForfeitRequest
}

type ArenaReplayCommand struct {
	Operator  ArenaOperatorIdentity
	CommandID uuid.UUID
	Request   api.ArenaOperatorReplayRequest
}

type ArenaExplicitOperatorService interface {
	ResolveNoShow(ctx context.Context, command ArenaNoShowCommand) error
	AssignReserve(ctx context.Context, command ArenaReserveAssignmentCommand) error
	RecordForfeit(ctx context.Context, command ArenaForfeitCommand) error
	ReplayGame(ctx context.Context, command ArenaReplayCommand) error
}

func (c *arenaAdminController) ResolveArenaNoShow(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	waveID api.ArenaWaveId,
	params api.ResolveArenaNoShowParams,
) {
	operator, service, ok := c.explicitOperatorService(w, r)
	if !ok {
		return
	}
	var body api.ArenaOperatorNoShowRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if params.IdempotencyKey == uuid.Nil || !validArenaNoShowRequest(body, tournamentID, waveID) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	if err := service.ResolveNoShow(r.Context(), ArenaNoShowCommand{
		Operator: operator, CommandID: params.IdempotencyKey, Request: body,
	}); err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *arenaAdminController) AssignArenaOperatorReserve(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	seriesID api.ArenaSeriesId,
	assignmentID api.ArenaAssignmentId,
	params api.AssignArenaOperatorReserveParams,
) {
	operator, service, ok := c.explicitOperatorService(w, r)
	if !ok {
		return
	}
	var body api.ArenaOperatorReserveRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if params.IdempotencyKey == uuid.Nil || !validArenaReserveRequest(body, tournamentID, seriesID, assignmentID) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	if err := service.AssignReserve(r.Context(), ArenaReserveAssignmentCommand{
		Operator: operator, CommandID: params.IdempotencyKey, Request: body,
	}); err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *arenaAdminController) RecordArenaOperatorForfeit(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	seriesID api.ArenaSeriesId,
	params api.RecordArenaOperatorForfeitParams,
) {
	operator, service, ok := c.explicitOperatorService(w, r)
	if !ok {
		return
	}
	var body api.ArenaOperatorForfeitRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	body.RuleId = strings.TrimSpace(body.RuleId)
	if params.IdempotencyKey == uuid.Nil || !validArenaForfeitRequest(body, tournamentID, seriesID) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	if err := service.RecordForfeit(r.Context(), ArenaForfeitCommand{
		Operator: operator, CommandID: params.IdempotencyKey, Request: body,
	}); err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *arenaAdminController) ReplayArenaOperatorGame(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	seriesID api.ArenaSeriesId,
	gameID api.ArenaGameId,
	params api.ReplayArenaOperatorGameParams,
) {
	operator, service, ok := c.explicitOperatorService(w, r)
	if !ok {
		return
	}
	var body api.ArenaOperatorReplayRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if params.IdempotencyKey == uuid.Nil || !validArenaReplayRequest(body, tournamentID, seriesID, gameID) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	if err := service.ReplayGame(r.Context(), ArenaReplayCommand{
		Operator: operator, CommandID: params.IdempotencyKey, Request: body,
	}); err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *arenaAdminController) explicitOperatorService(
	w http.ResponseWriter,
	r *http.Request,
) (ArenaOperatorIdentity, ArenaExplicitOperatorService, bool) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return ArenaOperatorIdentity{}, nil, false
	}
	service, ok := c.service.(ArenaExplicitOperatorService)
	if !ok || service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return ArenaOperatorIdentity{}, nil, false
	}
	return operator, service, true
}

func validArenaNoShowRequest(body api.ArenaOperatorNoShowRequest, tournamentID, waveID uuid.UUID) bool {
	return body.TournamentId == tournamentID && body.WaveId == waveID && tournamentID != uuid.Nil && waveID != uuid.Nil &&
		body.WindowId != uuid.Nil && body.SeriesId != uuid.Nil && body.Confirmed && validArenaOperatorReason(body.Reason, 512) &&
		body.ExpectedAuthorityRevision >= 1 && body.ExpectedWaveRevisionId != uuid.Nil &&
		body.ExpectedWindowRevisionId != uuid.Nil && validArenaSeriesState(body.ExpectedSeriesState) &&
		validUniqueArenaUUIDs(body.GameResultRevisionIds, 1, 3) && body.ScoreRevisionId != uuid.Nil &&
		body.SeriesResultRevisionId != uuid.Nil
}

//nolint:gocyclo // The command must bind every supplied source revision before facade dispatch.
func validArenaReserveRequest(
	body api.ArenaOperatorReserveRequest,
	tournamentID, seriesID, assignmentID uuid.UUID,
) bool {
	return body.TournamentId == tournamentID && body.SeriesId == seriesID && body.AssignmentId == assignmentID &&
		tournamentID != uuid.Nil && seriesID != uuid.Nil && assignmentID != uuid.Nil && body.OldWaveId != uuid.Nil &&
		body.SlotId != uuid.Nil && body.AssignmentAttemptId != uuid.Nil && body.Confirmed && validArenaOperatorReason(body.Reason, 512) &&
		body.ExpectedAuthorityRevision >= 1 && body.ExpectedExhaustionCommandId != uuid.Nil &&
		body.ExpectedAssignmentRevision >= 1 && body.ExpectedPoolRevisionId != uuid.Nil && body.ExpectedPoolRevision >= 1 &&
		body.ExpectedHistoryRevisionId != uuid.Nil && body.ExpectedHistoryRevision >= 1 &&
		body.ExpectedArtifactRevisionId != uuid.Nil && body.ExpectedArtifactRevision >= 1 &&
		body.ExpectedReservationRevisionId != uuid.Nil && body.ExpectedReservationRevision >= 1 &&
		body.ExpectedCategoryRevisionId != uuid.Nil && body.ExpectedCategoryRevision >= 1 &&
		body.ProposedTaskId != uuid.Nil && body.ProposedVersion >= 1 && body.ProposedSnapshotId != uuid.Nil &&
		body.ExpectedSnapshotId != uuid.Nil && body.EvidenceId != uuid.Nil
}

//nolint:gocyclo // Settlement identity, evidence, and optional Game expectations form one ingress boundary.
func validArenaForfeitRequest(body api.ArenaOperatorForfeitRequest, tournamentID, seriesID uuid.UUID) bool {
	if body.TournamentId != tournamentID || body.SeriesId != seriesID || tournamentID == uuid.Nil || seriesID == uuid.Nil ||
		body.ForfeitingParticipantId == uuid.Nil || !body.Confirmed || !validArenaOperatorReason(body.Reason, 256) ||
		body.ExpectedAuthorityRevision < 1 || body.Basis != api.RuleViolation || !validArenaRuleID(body.RuleId) ||
		!validUniqueArenaUUIDs(body.EvidenceIds, 1, 16) || body.ScoreRevisionId == uuid.Nil ||
		body.SeriesResultRevisionId == uuid.Nil || body.AuditEventId == uuid.Nil || body.OutboxEventId == uuid.Nil ||
		body.ProjectionRevisionId == uuid.Nil || (body.GameResultRevisionId != nil && *body.GameResultRevisionId == uuid.Nil) {
		return false
	}
	if body.ExpectedGame != nil {
		return body.ExpectedGame.SlotId != uuid.Nil && body.ExpectedGame.GameId != uuid.Nil &&
			body.ExpectedGame.AttemptNo >= 1 && validArenaGameState(body.ExpectedGame.State)
	}
	return true
}

//nolint:gocyclo // Replay scope and every fresh replacement identity are validated together.
func validArenaReplayRequest(body api.ArenaOperatorReplayRequest, tournamentID, seriesID, gameID uuid.UUID) bool {
	return body.TournamentId == tournamentID && body.SeriesId == seriesID && body.FailedGameId == gameID &&
		tournamentID != uuid.Nil && seriesID != uuid.Nil && gameID != uuid.Nil && body.OldWaveId != uuid.Nil &&
		body.SlotId != uuid.Nil && body.AssignmentId != uuid.Nil && body.Confirmed && validArenaOperatorReason(body.Reason, 512) &&
		body.ExpectedAuthorityRevision >= 1 && body.ExpectedClosureRevisionId != uuid.Nil &&
		body.AssignmentAttemptId != uuid.Nil && body.ReplacementGameId != uuid.Nil &&
		body.ReplacementWaveId != uuid.Nil && body.ReplacementWaveRevisionId != uuid.Nil &&
		body.ReadyWindowId != uuid.Nil && body.ReadyWindowRevisionId != uuid.Nil
}

func validArenaOperatorReason(reason string, maximum int) bool {
	return reason != "" && utf8.RuneCountInString(reason) <= maximum
}

func validUniqueArenaUUIDs(values []uuid.UUID, minimum, maximum int) bool {
	if len(values) < minimum || len(values) > maximum {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validArenaRuleID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("._:-", char) {
			continue
		}
		return false
	}
	return true
}

func validArenaSeriesState(state api.ArenaSeriesState) bool {
	switch state {
	case api.ArenaSeriesStatePlanned, api.ArenaSeriesStateLocked, api.ArenaSeriesStateDraft,
		api.ArenaSeriesStateReady, api.ArenaSeriesStateActive, api.ArenaSeriesStateReplayRequired,
		api.ArenaSeriesStateTechnicalPause, api.ArenaSeriesStateCompleted, api.ArenaSeriesStateCancelled:
		return true
	default:
		return false
	}
}

func (c *arenaAdminController) CorrectArenaGameResult(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	seriesID api.ArenaSeriesId,
	gameID api.ArenaGameId,
	params api.CorrectArenaGameResultParams,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	service, ok := c.service.(ArenaOperatorCorrectionService)
	if !ok || service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaOperatorCorrectionRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	command, ok := newArenaOperatorCorrectionCommand(
		operator,
		tournamentID,
		seriesID,
		gameID,
		params.IdempotencyKey,
		body,
	)
	if !ok {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := service.CorrectGame(r.Context(), command)
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func newArenaOperatorCorrectionCommand(
	operator ArenaOperatorIdentity,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
	gameID uuid.UUID,
	commandID uuid.UUID,
	body api.ArenaOperatorCorrectionRequest,
) (ArenaOperatorCorrectionCommand, bool) {
	explanation := strings.TrimSpace(body.Explanation)
	projectionIntents := cloneArenaProjectionIntents(body.ProjectionIntents)
	unlockIntents := cloneArenaUnlockIntents(body.UnlockIntents)
	kind := arenaOperatorCorrectionKind(body.Patch)

	command := ArenaOperatorCorrectionCommand{
		Operator: operator, Kind: kind, TournamentID: tournamentID, SeriesID: seriesID,
		GameID: gameID, CommandID: commandID, ExpectedRevision: body.ExpectedProjectionRevision,
		Confirmed: body.Confirmed, Reason: body.Reason, Explanation: explanation,
		Fields: append([]api.ArenaCorrectionField(nil), body.Fields...), Patch: body.Patch,
		ProjectionIntents: projectionIntents, UnlockIntents: unlockIntents,
	}
	if !validArenaOperatorCorrectionCommand(command) {
		return ArenaOperatorCorrectionCommand{}, false
	}
	return command, true
}

//nolint:gocyclo // Correction validation deliberately keeps the complete patch and intent evidence fail-closed.
func validArenaOperatorCorrectionCommand(command ArenaOperatorCorrectionCommand) bool {
	if command.Operator.Subject == "" || command.Operator.SessionID == "" ||
		command.TournamentID == uuid.Nil || command.SeriesID == uuid.Nil || command.GameID == uuid.Nil ||
		command.CommandID == uuid.Nil || command.ExpectedRevision < 1 || !command.Confirmed ||
		command.Explanation == "" || utf8.RuneCountInString(command.Explanation) > 512 ||
		!validArenaCorrectionReason(command.Reason) || !validArenaCorrectionFields(command.Fields) ||
		!validSHA256Hex(command.Patch.SolveMetadata.EvidenceDigest) {
		return false
	}

	for _, intent := range command.ProjectionIntents {
		if intent.DecisionId == uuid.Nil || intent.NextRevisionId == uuid.Nil || !validSHA256Hex(intent.PayloadDigest) {
			return false
		}
	}
	for _, intent := range command.UnlockIntents {
		if intent.TournamentId != command.TournamentID || intent.ReservationId == uuid.Nil ||
			intent.OwnerId == uuid.Nil || intent.ExpectedRevision < 1 {
			return false
		}
	}

	switch command.Kind {
	case ArenaOperatorCommandNoShow, ArenaOperatorCommandForfeit:
		return command.Patch.State == api.ArenaGameStateCompleted && command.Patch.WinnerId != nil &&
			*command.Patch.WinnerId != uuid.Nil && hasArenaCorrectionField(command.Fields, api.ResultReason) &&
			hasArenaCorrectionField(command.Fields, api.Winner)
	case ArenaOperatorCommandReplay:
		return command.Patch.State == api.ArenaGameStateSuperseded && command.Patch.WinnerId == nil &&
			len(command.ProjectionIntents) > 0 && hasArenaCorrectionField(command.Fields, api.ResultReason)
	case ArenaOperatorCommandCorrection:
		return validArenaGameState(command.Patch.State) && validArenaGameResultReason(command.Patch.Reason)
	default:
		return false
	}
}

func arenaOperatorCorrectionKind(patch api.ArenaCorrectionPatch) ArenaOperatorCommandKind {
	switch {
	case patch.Reason == api.ArenaGameResultReasonNoShow:
		return ArenaOperatorCommandNoShow
	case patch.Reason == api.ArenaGameResultReasonOperatorForfeit:
		return ArenaOperatorCommandForfeit
	case patch.State == api.ArenaGameStateSuperseded && patch.Reason == api.ArenaGameResultReasonDerivedRevisionSuperseded:
		return ArenaOperatorCommandReplay
	default:
		return ArenaOperatorCommandCorrection
	}
}

func validArenaCorrectionReason(reason api.ArenaCorrectionReason) bool {
	switch reason {
	case api.OperatorRuling, api.ScorekeepingError, api.VerifiedSubmission:
		return true
	default:
		return false
	}
}

func validArenaCorrectionFields(fields []api.ArenaCorrectionField) bool {
	if len(fields) == 0 || len(fields) > 3 {
		return false
	}
	seen := make(map[api.ArenaCorrectionField]struct{}, len(fields))
	for _, field := range fields {
		switch field {
		case api.ResultReason, api.SolveMetadata, api.Winner:
		default:
			return false
		}
		if _, duplicate := seen[field]; duplicate {
			return false
		}
		seen[field] = struct{}{}
	}
	return true
}

func hasArenaCorrectionField(fields []api.ArenaCorrectionField, target api.ArenaCorrectionField) bool {
	for _, field := range fields {
		if field == target {
			return true
		}
	}
	return false
}

func validArenaGameState(state api.ArenaGameState) bool {
	switch state {
	case api.ArenaGameStatePlanned, api.ArenaGameStateReady, api.ArenaGameStateActive,
		api.ArenaGameStatePaused, api.ArenaGameStateCompleted, api.ArenaGameStateVoid,
		api.ArenaGameStateCancelled, api.ArenaGameStateSuperseded:
		return true
	default:
		return false
	}
}

func validArenaGameResultReason(reason api.ArenaGameResultReason) bool {
	switch reason {
	case api.ArenaGameResultReasonSolved, api.ArenaGameResultReasonSurrender,
		api.ArenaGameResultReasonOperatorForfeit, api.ArenaGameResultReasonNoSolve,
		api.ArenaGameResultReasonTaskFailure, api.ArenaGameResultReasonCommonPlatformFailure,
		api.ArenaGameResultReasonDisconnect, api.ArenaGameResultReasonExecutionEpochBreak,
		api.ArenaGameResultReasonNoShow, api.ArenaGameResultReasonSeriesCancelled,
		api.ArenaGameResultReasonTournamentCancelled, api.ArenaGameResultReasonDerivedRevisionSuperseded:
		return true
	default:
		return false
	}
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func cloneArenaProjectionIntents(value *[]api.ArenaCorrectionProjectionIntent) []api.ArenaCorrectionProjectionIntent {
	if value == nil {
		return nil
	}
	return append([]api.ArenaCorrectionProjectionIntent(nil), (*value)...)
}

func cloneArenaUnlockIntents(value *[]api.ArenaCorrectionUnlockIntent) []api.ArenaCorrectionUnlockIntent {
	if value == nil {
		return nil
	}
	return append([]api.ArenaCorrectionUnlockIntent(nil), (*value)...)
}
