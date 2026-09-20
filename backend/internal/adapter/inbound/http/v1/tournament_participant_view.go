package v1

import (
	"encoding/hex"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

//nolint:gocyclo // One response boundary validates every participant-safe field before serialization.
func participantLobbyResponse(
	view usecase.LobbyView,
) (api.ParticipantLobbyResponse, error) {
	state := api.TournamentState(view.State)
	attendance := api.AttendanceState(view.Attendance)
	status := api.ParticipantLobbyStatus(view.Status)
	requiredAction := api.ParticipantLobbyRequiredAction(view.RequiredAction)
	if view.TournamentID == uuid.Nil || view.ParticipantID == uuid.Nil || view.ProjectionRevision < 1 ||
		!state.Valid() || !attendance.Valid() || !status.Valid() || !requiredAction.Valid() ||
		view.SwissPoints < 0 || view.SwissPoints > math.MaxInt32 {
		return api.ParticipantLobbyResponse{}, domain.ErrInternal
	}
	var currentSwissRound *int32
	if view.CurrentSwissRound != nil {
		if *view.CurrentSwissRound < 1 || *view.CurrentSwissRound > 4 {
			return api.ParticipantLobbyResponse{}, domain.ErrInternal
		}
		value := int32(*view.CurrentSwissRound)
		currentSwissRound = &value
	}
	series := make([]api.ParticipantLobbySeries, len(view.Series))
	for index, item := range view.Series {
		format := api.SeriesFormat(item.Format)
		seriesState := api.SeriesState(item.State)
		if item.SeriesID == uuid.Nil || item.WaveID == uuid.Nil || !format.Valid() || !seriesState.Valid() {
			return api.ParticipantLobbyResponse{}, domain.ErrInternal
		}
		series[index] = api.ParticipantLobbySeries{
			SeriesId:            item.SeriesID,
			WaveId:              item.WaveID,
			OpponentDisplayName: item.OpponentDisplayName,
			Format:              format,
			State:               seriesState,
		}
	}
	return api.ParticipantLobbyResponse{
		ParticipantId:      view.ParticipantID,
		Attendance:         attendance,
		CurrentSwissRound:  currentSwissRound,
		SwissPoints:        int32(view.SwissPoints),
		Status:             status,
		RequiredAction:     requiredAction,
		TournamentId:       view.TournamentID,
		ProjectionRevision: view.ProjectionRevision,
		State:              state,
		RosterLocked:       view.RosterLocked,
		Series:             series,
	}, nil
}

func participantAssignmentResponse(
	result usecase.AssignmentResult,
) (api.ParticipantAssignmentResponse, error) {
	assignment, err := participantAssignment(result.Assignment)
	if err != nil {
		return api.ParticipantAssignmentResponse{}, err
	}
	if result.TournamentID == uuid.Nil || result.ProjectionRevision < 1 {
		return api.ParticipantAssignmentResponse{}, domain.ErrInternal
	}
	return api.ParticipantAssignmentResponse{
		TournamentId:       result.TournamentID,
		ProjectionRevision: result.ProjectionRevision,
		Assignment:         assignment,
	}, nil
}

func participantAssignment(
	view usecase.TournamentParticipantAssignmentView,
) (api.ParticipantAssignment, error) {
	task, err := participantTaskSnapshot(view.ActiveSnapshot)
	if err != nil {
		return api.ParticipantAssignment{}, err
	}
	context, err := participantAssignmentContext(view.Context)
	if err != nil {
		return api.ParticipantAssignment{}, err
	}
	if view.AssignmentID == uuid.Nil || view.AttemptID == uuid.Nil ||
		view.UndisclosedReserveCount < 0 || view.UndisclosedReserveCount > math.MaxInt32 ||
		context.WaveId != view.WaveID || context.SeriesId != view.SeriesID || context.GameId != view.GameID {
		return api.ParticipantAssignment{}, domain.ErrInternal
	}
	receipt := view.Receipt
	return api.ParticipantAssignment{
		Id:                      view.AssignmentID,
		AttemptId:               view.AttemptID,
		ActiveSnapshot:          task,
		Context:                 context,
		UndisclosedReserveCount: int32(view.UndisclosedReserveCount),
		Receipt: api.DeliveryReceipt{
			Id:            receipt.ID,
			AssignmentId:  receipt.AssignmentID,
			AttemptId:     receipt.AttemptID,
			ParticipantId: receipt.ParticipantID,
			SnapshotId:    receipt.SnapshotID,
			TaskId:        receipt.TaskID,
			DeliveredAt:   receipt.DeliveredAt,
		},
	}, nil
}

func participantAssignmentContext(
	view usecase.ParticipantAssignmentContextView,
) (api.ParticipantAssignmentContext, error) {
	if !validParticipantAssignmentContextView(view) {
		return api.ParticipantAssignmentContext{}, domain.ErrInternal
	}
	stage := api.ParticipantAssignmentContextStage(view.Stage)
	gameState := api.GameState(view.GameState)
	var swissRound *int32
	if view.SwissRound != nil {
		// SwissRound is bounded to 1..4 by validParticipantAssignmentContextView.
		value := int32(*view.SwissRound) //nolint:gosec // bounded above
		swissRound = &value
	}
	return api.ParticipantAssignmentContext{
		WaveId:            view.WaveID,
		SeriesId:          view.SeriesID,
		SlotId:            view.SlotID,
		GameId:            view.GameID,
		Stage:             stage,
		SwissRound:        swissRound,
		GameNumber:        int32(view.GameNumber), //nolint:gosec // bounded above
		SeriesScore:       participantSeriesScore(view.SeriesScore),
		GameState:         gameState,
		StartedAt:         cloneTimePointer(view.StartedAt),
		EffectiveDeadline: cloneTimePointer(view.EffectiveDeadline),
	}, nil
}

func validParticipantAssignmentContextView(view usecase.ParticipantAssignmentContextView) bool {
	return validParticipantAssignmentContextViewIdentity(view) &&
		validParticipantAssignmentContextViewRound(view) &&
		validParticipantAssignmentContextViewTimes(view)
}

func validParticipantAssignmentContextViewIdentity(view usecase.ParticipantAssignmentContextView) bool {
	return view.WaveID != uuid.Nil && view.SeriesID != uuid.Nil && view.SlotID != uuid.Nil && view.GameID != uuid.Nil &&
		view.Stage != domain.TournamentStageGolden && view.Stage.IsValid() &&
		view.GameNumber >= 1 && view.GameNumber <= 3 && view.GameState.IsValid() &&
		view.SeriesScore.FirstParticipantWins >= 0 && view.SeriesScore.FirstParticipantWins <= 2 &&
		view.SeriesScore.SecondParticipantWins >= 0 && view.SeriesScore.SecondParticipantWins <= 2 &&
		(view.SeriesScore.FirstParticipantWins != 2 || view.SeriesScore.SecondParticipantWins != 2)
}

func validParticipantAssignmentContextViewRound(view usecase.ParticipantAssignmentContextView) bool {
	if view.Stage == domain.TournamentStageSwiss {
		return view.SwissRound != nil && *view.SwissRound >= 1 && *view.SwissRound <= 4
	}
	return view.SwissRound == nil &&
		(view.Stage == domain.TournamentStageSemifinal || view.Stage == domain.TournamentStageFinal)
}

func validParticipantAssignmentContextViewTimes(view usecase.ParticipantAssignmentContextView) bool {
	if (view.StartedAt != nil && !domain.IsValidServerTime(*view.StartedAt)) ||
		(view.EffectiveDeadline != nil && !domain.IsValidServerTime(*view.EffectiveDeadline)) {
		return false
	}
	if view.GameState == domain.GameStatePlanned || view.GameState == domain.GameStateReady {
		return view.StartedAt == nil && view.EffectiveDeadline == nil
	}
	if view.GameState == domain.GameStatePaused {
		return view.StartedAt != nil && view.EffectiveDeadline == nil
	}
	if view.GameState == domain.GameStateActive {
		return view.StartedAt != nil && view.EffectiveDeadline != nil
	}
	return view.EffectiveDeadline == nil
}

func participantTaskSnapshot(
	view usecase.TaskSnapshotView,
) (api.ParticipantTaskSnapshot, error) {
	category := api.Category(view.Category)
	difficulty := api.Difficulty(view.Difficulty)
	kind := api.TaskKind(view.Kind)
	if view.SnapshotID == uuid.Nil || view.TaskID == uuid.Nil ||
		view.Version < 1 || view.Version > math.MaxInt32 ||
		view.TimeLimit < 1 || view.TimeLimit > math.MaxInt32 ||
		!category.Valid() || !difficulty.Valid() || !kind.Valid() {
		return api.ParticipantTaskSnapshot{}, domain.ErrInternal
	}
	return api.ParticipantTaskSnapshot{
		SnapshotId:          view.SnapshotID,
		TaskId:              view.TaskID,
		Version:             int32(view.Version),
		Kind:                kind,
		Title:               view.Title,
		Description:         view.Description,
		Category:            category,
		Difficulty:          difficulty,
		TimeLimit:           int32(view.TimeLimit),
		Hints:               append([]string{}, view.Hints...),
		TaskUrl:             participantStringPointer(view.TaskURL),
		SourceFileAvailable: view.SourceFileURL != nil,
	}, nil
}

func participantRecoveryResponse(
	view usecase.RecoveryView,
) (api.ParticipantRecoverySnapshot, error) {
	lobby, err := participantLobbyResponse(view.Lobby)
	if err != nil {
		return api.ParticipantRecoverySnapshot{}, err
	}
	result := api.ParticipantRecoverySnapshot{
		TournamentId:       view.TournamentID,
		ProjectionRevision: view.ProjectionRevision,
		Lobby:              lobby,
		NextCursor: api.ParticipantRecoveryCursor{
			ProjectionRevision:      view.ProjectionRevision,
			ParticipantViewRevision: view.ParticipantViewRevision,
			EventSequence:           view.EventSequence,
		},
	}
	if view.Assignment != nil {
		assignment, mapErr := participantAssignment(*view.Assignment)
		if mapErr != nil {
			return api.ParticipantRecoverySnapshot{}, mapErr
		}
		result.Assignment = &assignment
	}
	if view.Draft != nil {
		draft, mapErr := participantDraftResponse(view.Draft.Execution)
		if mapErr != nil {
			return api.ParticipantRecoverySnapshot{}, mapErr
		}
		result.Draft = &draft
	}
	if view.Series != nil {
		series, mapErr := participantSeriesResponse(*view.Series)
		if mapErr != nil {
			return api.ParticipantRecoverySnapshot{}, mapErr
		}
		result.Series = &series
	}
	if view.Wave != nil {
		wave, mapErr := participantWaveResponse(*view.Wave)
		if mapErr != nil {
			return api.ParticipantRecoverySnapshot{}, mapErr
		}
		result.Wave = &wave
	}
	return result, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func participantDraftResponse(execution usecase.DraftExecutionView) (api.Draft, error) {
	if execution.ID == uuid.Nil || execution.SeriesID == uuid.Nil || execution.Revision < 1 ||
		execution.FirstParticipantID == uuid.Nil || execution.SecondParticipantID == uuid.Nil ||
		execution.FirstParticipantID == execution.SecondParticipantID || len(execution.Pool) < 3 || len(execution.Pool) > 5 {
		return api.Draft{}, domain.ErrInternal
	}
	format := api.SeriesFormat(execution.Format)
	state := api.DraftState(execution.State)
	if !format.Valid() || !state.Valid() || execution.Turn < 1 || execution.Turn > math.MaxInt32 {
		return api.Draft{}, domain.ErrInternal
	}
	pool, err := participantCategories(execution.Pool)
	if err != nil {
		return api.Draft{}, err
	}
	selected, err := participantCategories(execution.SelectedCategories)
	if err != nil {
		return api.Draft{}, err
	}
	if !uniqueParticipantCategories(pool) || len(selected) > 3 || !uniqueParticipantCategories(selected) || len(execution.Actions) > 4 {
		return api.Draft{}, domain.ErrInternal
	}
	legalCategories, err := participantCategories(execution.LegalCategories)
	if err != nil {
		return api.Draft{}, err
	}
	if len(legalCategories) > len(pool) || !uniqueParticipantCategories(legalCategories) ||
		!participantCategoriesSubset(legalCategories, pool) {
		return api.Draft{}, domain.ErrInternal
	}

	var currentActorID *uuid.UUID
	var currentAction *api.DraftActionType
	var turnDeadline *time.Time
	switch state {
	case api.DraftStateActive:
		if execution.CurrentActorID == nil || *execution.CurrentActorID == uuid.Nil ||
			(*execution.CurrentActorID != execution.FirstParticipantID && *execution.CurrentActorID != execution.SecondParticipantID) ||
			execution.CurrentAction == nil || !api.DraftActionType(*execution.CurrentAction).Valid() ||
			execution.TurnDeadline.IsZero() || !domain.IsValidServerTime(execution.TurnDeadline) || len(legalCategories) == 0 {
			return api.Draft{}, domain.ErrInternal
		}
		currentActorID = participantUUIDPointer(execution.CurrentActorID)
		currentActionValue := api.DraftActionType(*execution.CurrentAction)
		currentAction = &currentActionValue
		deadline := execution.TurnDeadline
		turnDeadline = &deadline
	case api.DraftStatePaused, api.DraftStateRecoveryRequired:
		// Persisted turn evidence stays internal while the server has made the
		// draft non-actionable. Participants receive an explicit state and no
		// mutation authority until a fresh active projection arrives.
		legalCategories = []api.Category{}
	case api.DraftStateCompleted, api.DraftStateSuperseded:
		if execution.CurrentActorID != nil || execution.CurrentAction != nil || !execution.TurnDeadline.IsZero() || len(legalCategories) != 0 {
			return api.Draft{}, domain.ErrInternal
		}
		legalCategories = []api.Category{}
	}
	actions := make([]api.DraftAction, len(execution.Actions))
	for index, item := range execution.Actions {
		action := api.DraftActionType(item.Action)
		category := api.Category(item.Category)
		if item.Turn < 1 || item.Turn > math.MaxInt32 || item.ActorID == uuid.Nil || !action.Valid() || !category.Valid() ||
			!domain.IsValidServerTime(item.OccurredAt) || !domain.IsValidServerTime(item.ScheduledDeadline) {
			return api.Draft{}, domain.ErrInternal
		}
		decisionEvidence, evidenceErr := participantDraftDecisionEvidence(item.DecisionEvidence)
		if evidenceErr != nil {
			return api.Draft{}, evidenceErr
		}
		if item.Automatic != (decisionEvidence != nil) {
			return api.Draft{}, domain.ErrInternal
		}
		if item.Automatic && (item.DecisionEvidence.Purpose != domain.DecisionPurposeCategory ||
			item.DecisionEvidence.OwnerID != execution.ID ||
			!item.DecisionEvidence.DecidedAt.Equal(item.ScheduledDeadline) ||
			len(item.DecisionEvidence.Result) == 0 || domain.Category(item.DecisionEvidence.Result[0]) != item.Category) {
			return api.Draft{}, domain.ErrInternal
		}
		actions[index] = api.DraftAction{
			Turn:             int32(item.Turn),
			ActorId:          item.ActorID,
			Action:           action,
			Category:         category,
			OccurredAt:       item.OccurredAt,
			TurnDeadline:     item.ScheduledDeadline,
			Automatic:        item.Automatic,
			DecisionEvidence: decisionEvidence,
		}
	}
	return api.Draft{
		Id:                  execution.ID,
		SeriesId:            execution.SeriesID,
		Format:              format,
		FirstParticipantId:  execution.FirstParticipantID,
		SecondParticipantId: execution.SecondParticipantID,
		Pool:                pool,
		State:               state,
		Turn:                int32(execution.Turn),
		CurrentActorId:      currentActorID,
		CurrentAction:       currentAction,
		TurnDeadline:        turnDeadline,
		LegalCategories:     legalCategoriesForResponse(state, legalCategories),
		Actions:             actions,
		SelectedCategories:  selected,
		Revision:            execution.Revision,
	}, nil
}

func participantDraftDecisionEvidence(value *domain.DecisionEvidence) (*api.DraftDecisionEvidence, error) {
	if value == nil {
		return nil, nil
	}
	if value.Validate() != nil {
		return nil, domain.ErrInternal
	}
	purpose := api.DraftDecisionEvidencePurpose(value.Purpose)
	algorithm := api.DraftDecisionEvidenceAlgorithmVersion(value.AlgorithmVersion)
	if !purpose.Valid() || !algorithm.Valid() || !domain.IsValidServerTime(value.DecidedAt) {
		return nil, domain.ErrInternal
	}
	return &api.DraftDecisionEvidence{
		Id:               value.ID,
		Purpose:          purpose,
		AlgorithmVersion: algorithm,
		NormalizedInputs: append([]string(nil), value.NormalizedInputs...),
		Result:           append([]string(nil), value.Result...),
		ReplayDigest:     hex.EncodeToString(value.ReplayDigest[:]),
		OwnerId:          value.OwnerID,
		DecidedAt:        value.DecidedAt,
	}, nil
}

func legalCategoriesForResponse(state api.DraftState, values []api.Category) []api.Category {
	if state != api.DraftStateActive {
		return []api.Category{}
	}
	return values
}

func uniqueParticipantCategories(values []api.Category) bool {
	seen := make(map[api.Category]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func participantCategoriesSubset(values, pool []api.Category) bool {
	allowed := make(map[api.Category]struct{}, len(pool))
	for _, value := range pool {
		allowed[value] = struct{}{}
	}
	for _, value := range values {
		if _, exists := allowed[value]; !exists {
			return false
		}
	}
	return true
}

func participantSeriesResponse(series domain.Series) (api.Series, error) {
	if series.Validate() != nil {
		return api.Series{}, domain.ErrInternal
	}
	format := api.SeriesFormat(series.Format)
	state := api.SeriesState(series.State)
	if !format.Valid() || !state.Valid() ||
		series.Score.FirstParticipantWins > math.MaxInt32 ||
		series.Score.SecondParticipantWins > math.MaxInt32 {
		return api.Series{}, domain.ErrInternal
	}
	slots := make([]api.GameSlot, len(series.Slots))
	for index, item := range series.Slots {
		slot, err := participantGameSlotResponse(item)
		if err != nil {
			return api.Series{}, err
		}
		slots[index] = slot
	}
	return api.Series{
		Id:                      series.ID,
		TournamentId:            series.TournamentID,
		FirstParticipantId:      series.FirstParticipantID,
		SecondParticipantId:     series.SecondParticipantID,
		Format:                  format,
		State:                   state,
		Score:                   participantSeriesScore(series.Score),
		WinnerId:                participantUUIDPointer(series.WinnerID),
		Slots:                   slots,
		CurrentScoreRevisionId:  seriesScoreRevisionID(series.CurrentScoreRevisionID),
		CurrentResultRevisionId: officialResultRevisionID(series.CurrentResultRevisionID),
	}, nil
}

func participantGameSlotResponse(slot domain.GameSlot) (api.GameSlot, error) {
	if slot.Validate() != nil || slot.Position < 1 || slot.Position > math.MaxInt32 {
		return api.GameSlot{}, domain.ErrInternal
	}
	category := api.Category(slot.Category)
	if !category.Valid() {
		return api.GameSlot{}, domain.ErrInternal
	}
	attempts := make([]api.Game, len(slot.Attempts))
	for index, item := range slot.Attempts {
		attempt, err := participantGameResponse(item)
		if err != nil {
			return api.GameSlot{}, err
		}
		attempts[index] = attempt
	}
	return api.GameSlot{
		Id:          slot.ID,
		SeriesId:    slot.SeriesID,
		Position:    int32(slot.Position),
		Category:    category,
		ScoreBefore: participantSeriesScore(slot.ScoreBefore),
		Attempts:    attempts,
	}, nil
}

func participantGameResponse(game domain.Game) (api.Game, error) {
	if game.Validate() != nil || game.AttemptNo < 1 || game.AttemptNo > math.MaxInt32 {
		return api.Game{}, domain.ErrInternal
	}
	state := api.GameState(game.State)
	if !state.Valid() {
		return api.Game{}, domain.ErrInternal
	}
	var reason *api.GameResultReason
	if game.ResultReason != "" {
		value := api.GameResultReason(game.ResultReason)
		if !value.Valid() {
			return api.Game{}, domain.ErrInternal
		}
		reason = &value
	}
	return api.Game{
		Id:               game.ID,
		SlotId:           game.SlotID,
		AttemptNo:        int32(game.AttemptNo),
		State:            state,
		ResultReason:     reason,
		WinnerId:         participantUUIDPointer(game.WinnerID),
		ResultRevisionId: officialResultRevisionID(game.ResultRevisionID),
	}, nil
}

func participantWaveResponse(view usecase.WaveView) (api.Wave, error) {
	wave := view.Wave
	if wave.Validate() != nil || view.Revision < 1 {
		return api.Wave{}, domain.ErrInternal
	}
	state := api.WaveState(wave.State)
	if !state.Valid() {
		return api.Wave{}, domain.ErrInternal
	}
	members := make([]api.WaveMember, len(wave.Members))
	for index, item := range wave.Members {
		revision := view.ReadinessRevisions[item.ParticipantID]
		seriesID, paired := view.SeriesIDs[item.ParticipantID]
		isBye := view.ByeParticipantID != nil && *view.ByeParticipantID == item.ParticipantID
		if revision < 1 || (isBye && paired) || (!isBye && (!paired || seriesID == uuid.Nil)) {
			return api.Wave{}, domain.ErrInternal
		}
		if isBye {
			members[index] = api.WaveMember{
				ParticipantId:     item.ParticipantID,
				ReadinessRevision: revision,
				Ready:             item.Ready,
			}
			continue
		}
		members[index] = api.WaveMember{
			ParticipantId:     item.ParticipantID,
			SeriesId:          &seriesID,
			Ready:             item.Ready,
			ReadinessRevision: revision,
		}
	}
	var readyWindow *api.ReadyWindow
	if wave.ReadyWindow != nil {
		windowState := api.ReadyWindowState(wave.ReadyWindow.State)
		if !windowState.Valid() {
			return api.Wave{}, domain.ErrInternal
		}
		readyWindow = &api.ReadyWindow{
			Id:         wave.ReadyWindow.ID,
			WaveId:     wave.ReadyWindow.WaveID,
			RevisionId: wave.ReadyWindow.RevisionID.UUID(),
			State:      windowState,
			OpenedAt:   wave.ReadyWindow.OpenedAt,
			Deadline:   wave.ReadyWindow.Deadline,
			ConsumedAt: cloneTimePointer(wave.ReadyWindow.ConsumedAt),
		}
	}
	return api.Wave{
		Id:           wave.ID,
		TournamentId: wave.TournamentID,
		RevisionId:   wave.RevisionID.UUID(),
		Revision:     view.Revision,
		State:        state,
		Members:      members,
		ReadyWindow:  readyWindow,
		StartedAt:    cloneTimePointer(wave.StartedAt),
		PausedAt:     cloneTimePointer(wave.PausedAt),
	}, nil
}

func participantCategories(values []domain.Category) ([]api.Category, error) {
	result := make([]api.Category, len(values))
	for index, value := range values {
		result[index] = api.Category(value)
		if !result[index].Valid() {
			return nil, domain.ErrInternal
		}
	}
	return result, nil
}

func participantSeriesScore(score domain.SeriesScore) api.SeriesScore {
	return api.SeriesScore{
		//nolint:gosec // Domain validation bounds this value before the storage conversion.
		FirstParticipantWins: int32(score.FirstParticipantWins),
		//nolint:gosec // Domain validation bounds this value before the storage conversion.
		SecondParticipantWins: int32(score.SecondParticipantWins),
	}
}

func seriesScoreRevisionID(value *domain.SeriesScoreRevisionID) *uuid.UUID {
	if value == nil {
		return nil
	}
	result := value.UUID()
	return &result
}

func officialResultRevisionID(value *domain.OfficialResultRevisionID) *uuid.UUID {
	if value == nil {
		return nil
	}
	result := value.UUID()
	return &result
}

func participantDigest(value [32]byte) string {
	return hex.EncodeToString(value[:])
}

func participantStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func participantUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
