package participant

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (a *ParticipantUseCase) GetLobby(ctx context.Context, query usecase.LobbyQuery) (usecase.LobbyView, error) {
	_, state, err := a.readState(ctx, query.Actor, query.TournamentID)
	if err != nil {
		return usecase.LobbyView{}, err
	}
	return cloneLobbyView(state.Lobby), nil
}

func (a *ParticipantUseCase) GetAssignment(
	ctx context.Context,
	query usecase.AssignmentQuery,
) (usecase.AssignmentResult, error) {
	if query.AssignmentID == uuid.Nil {
		return usecase.AssignmentResult{}, domain.ErrValidation
	}
	authority, state, err := a.readState(ctx, query.Actor, query.TournamentID)
	if err != nil {
		return usecase.AssignmentResult{}, err
	}
	if err := authorizeAssignment(authority, query.AssignmentID); err != nil {
		return usecase.AssignmentResult{}, err
	}
	if state.Assignment == nil || state.Assignment.AssignmentID != query.AssignmentID {
		return usecase.AssignmentResult{}, domain.ErrInternal
	}
	return usecase.AssignmentResult{
		TournamentID:       state.TournamentID,
		ParticipantID:      state.ParticipantID,
		ProjectionRevision: state.ProjectionRevision,
		Assignment:         cloneParticipantAssignment(*state.Assignment),
	}, nil
}

func (a *ParticipantUseCase) GetSnapshot(
	ctx context.Context,
	query usecase.SnapshotQuery,
) (usecase.RecoveryView, error) {
	if !validRecoveryCursor(query.Cursor) {
		return usecase.RecoveryView{}, domain.ErrValidation
	}
	_, state, err := a.readState(ctx, query.Actor, query.TournamentID)
	if err != nil {
		return usecase.RecoveryView{}, err
	}
	if query.Cursor != nil && query.Cursor.ProjectionRevision > state.ProjectionRevision {
		return usecase.RecoveryView{}, domain.ErrValidation
	}
	return cloneRecoveryView(state), nil
}

func (a *ParticipantUseCase) readState(
	ctx context.Context,
	actor usecase.Identity,
	tournamentID uuid.UUID,
) (usecase.ParticipantSnapshotView, usecase.RecoveryView, error) {
	if a == nil || a.snapshots == nil || a.states == nil {
		return usecase.ParticipantSnapshotView{}, usecase.RecoveryView{}, domain.ErrInternal
	}
	authority, err := a.loadAuthority(ctx, actor, tournamentID)
	if err != nil {
		return usecase.ParticipantSnapshotView{}, usecase.RecoveryView{}, err
	}
	state, err := a.states.ReadParticipantState(ctx, StateQuery{
		TournamentID:               tournamentID,
		PlayerID:                   actor.PlayerID,
		ExpectedProjectionRevision: authority.Cursor.ProjectionRevision,
	})
	if err != nil {
		return usecase.ParticipantSnapshotView{}, usecase.RecoveryView{}, err
	}
	if err := validateRecoveryView(authority, state); err != nil {
		return usecase.ParticipantSnapshotView{}, usecase.RecoveryView{}, err
	}
	return authority, cloneRecoveryView(state), nil
}

func (a *ParticipantUseCase) loadAuthority(
	ctx context.Context,
	actor usecase.Identity,
	tournamentID uuid.UUID,
) (usecase.ParticipantSnapshotView, error) {
	if a == nil || a.snapshots == nil {
		return usecase.ParticipantSnapshotView{}, domain.ErrInternal
	}
	if ctx == nil || actor.PlayerID == uuid.Nil || tournamentID == uuid.Nil {
		return usecase.ParticipantSnapshotView{}, domain.ErrValidation
	}
	view, err := a.snapshots.ParticipantSnapshot(ctx, usecase.ParticipantSnapshotQuery{
		TournamentID: tournamentID,
		PlayerID:     actor.PlayerID,
	})
	if err != nil {
		return usecase.ParticipantSnapshotView{}, err
	}
	if err := validateAuthorityView(view, actor, tournamentID); err != nil {
		return usecase.ParticipantSnapshotView{}, err
	}
	return view, nil
}

func validateAuthorityView(
	view usecase.ParticipantSnapshotView,
	actor usecase.Identity,
	tournamentID uuid.UUID,
) error {
	if view.TournamentID != tournamentID || view.PlayerID != actor.PlayerID ||
		view.ParticipantID == uuid.Nil ||
		view.Cursor.ProjectionRevision < 1 || view.Cursor.EventSequence < 0 ||
		!domain.IsValidServerTime(view.Cursor.ObservedAt) {
		return domain.ErrInternal
	}
	if view.Assignment != nil {
		assignment := view.Assignment
		if assignment.AssignmentID == uuid.Nil || assignment.AttemptID == uuid.Nil ||
			assignment.SeriesID == uuid.Nil || assignment.GameID == uuid.Nil || assignment.WaveID == uuid.Nil ||
			assignment.Task.SnapshotID == uuid.Nil || assignment.Task.TaskID == uuid.Nil ||
			strings.TrimSpace(assignment.Task.Title) == "" ||
			!domain.Category(assignment.Task.Category).IsValid() ||
			!domain.Difficulty(assignment.Task.Difficulty).IsValid() ||
			!domain.IsValidTaskTimeLimit(assignment.Task.TimeLimitSeconds) {
			return domain.ErrInternal
		}
	}
	if view.Opponent != nil {
		opponent := view.Opponent
		if opponent.PlayerID == uuid.Nil || opponent.PlayerID == actor.PlayerID ||
			opponent.SeriesID == uuid.Nil || strings.TrimSpace(opponent.DisplayName) == "" ||
			!domain.SeriesState(opponent.SeriesState).IsValid() || opponent.Score < 0 {
			return domain.ErrInternal
		}
	}
	return nil
}

func validateRecoveryView(
	authority usecase.ParticipantSnapshotView,
	view usecase.RecoveryView,
) error {
	if view.TournamentID != authority.TournamentID || view.ParticipantID != authority.ParticipantID ||
		view.ProjectionRevision != authority.Cursor.ProjectionRevision ||
		view.ParticipantViewRevision < 1 || view.EventSequence < 0 ||
		!domain.IsValidServerTime(view.ObservedAt) {
		return domain.ErrInternal
	}
	if err := validateLobbyView(view.Lobby, view); err != nil {
		return err
	}
	if err := validateAssignmentAlignment(authority, view.Assignment); err != nil {
		return err
	}
	if err := validateDraftView(authority.ParticipantID, view.Draft); err != nil {
		return err
	}
	if err := validateSeriesView(authority, view.Series); err != nil {
		return err
	}
	return validateWaveView(authority, view.Wave)
}

func validateLobbyView(lobby usecase.LobbyView, recovery usecase.RecoveryView) error {
	if lobby.TournamentID != recovery.TournamentID || lobby.ParticipantID != recovery.ParticipantID ||
		lobby.ProjectionRevision != recovery.ProjectionRevision || !lobby.State.IsValid() {
		return domain.ErrInternal
	}
	for _, item := range lobby.Series {
		if item.SeriesID == uuid.Nil || item.WaveID == uuid.Nil ||
			item.ParticipantID != recovery.ParticipantID || item.OpponentID == uuid.Nil ||
			item.OpponentID == recovery.ParticipantID || strings.TrimSpace(item.OpponentDisplayName) == "" ||
			!item.Format.IsValid() || !item.State.IsValid() {
			return domain.ErrInternal
		}
	}
	return nil
}

func validateAssignmentAlignment(
	authority usecase.ParticipantSnapshotView,
	assignment *usecase.TournamentParticipantAssignmentView,
) error {
	if (authority.Assignment == nil) != (assignment == nil) {
		return domain.ErrInternal
	}
	if assignment == nil {
		return nil
	}
	if err := validateParticipantAssignment(*assignment); err != nil {
		return err
	}
	base := authority.Assignment
	if assignment.ParticipantID != authority.ParticipantID || assignment.AssignmentID != base.AssignmentID ||
		assignment.AttemptID != base.AttemptID || assignment.SeriesID != base.SeriesID ||
		assignment.GameID != base.GameID || assignment.WaveID != base.WaveID ||
		assignment.ActiveSnapshot.SnapshotID != base.Task.SnapshotID ||
		assignment.ActiveSnapshot.TaskID != base.Task.TaskID {
		return domain.ErrInternal
	}
	return nil
}

func validateParticipantAssignment(assignment usecase.TournamentParticipantAssignmentView) error {
	if assignment.AssignmentID == uuid.Nil || assignment.AttemptID == uuid.Nil ||
		assignment.ParticipantID == uuid.Nil || assignment.SeriesID == uuid.Nil ||
		assignment.GameID == uuid.Nil || assignment.WaveID == uuid.Nil ||
		assignment.UndisclosedReserveCount < 0 || validateTaskSnapshot(assignment.ActiveSnapshot) != nil ||
		assignment.Receipt.Validate() != nil {
		return domain.ErrInternal
	}
	receipt := assignment.Receipt
	if receipt.AssignmentID != assignment.AssignmentID || receipt.AttemptID != assignment.AttemptID ||
		receipt.ParticipantID != assignment.ParticipantID ||
		receipt.SnapshotID != assignment.ActiveSnapshot.SnapshotID ||
		receipt.TaskID != assignment.ActiveSnapshot.TaskID {
		return domain.ErrInternal
	}
	return nil
}

func validateTaskSnapshot(snapshot usecase.TaskSnapshotView) error {
	if snapshot.SnapshotID == uuid.Nil || snapshot.TaskID == uuid.Nil || snapshot.Version < 1 ||
		!snapshot.Kind.IsValid() || !domain.IsValidTaskTitle(snapshot.Title) ||
		!domain.IsValidTaskDescription(snapshot.Description) || !snapshot.Category.IsValid() ||
		!snapshot.Difficulty.IsValid() || !domain.IsValidTaskTimeLimit(snapshot.TimeLimit) ||
		!domain.IsValidTaskURLShape(snapshot.Category, snapshot.TaskURL, snapshot.SourceFileURL) {
		return domain.ErrInternal
	}
	return nil
}

func validateDraftView(participantID uuid.UUID, view *usecase.DraftView) error {
	if view == nil {
		return nil
	}
	execution := view.Execution
	if execution.ID == uuid.Nil || execution.SeriesID == uuid.Nil || execution.Revision < 1 ||
		execution.State == "" || (execution.FirstParticipantID != participantID && execution.SecondParticipantID != participantID) {
		return domain.ErrInternal
	}
	return nil
}

func validateSeriesView(
	authority usecase.ParticipantSnapshotView,
	series *domain.Series,
) error {
	if series == nil {
		return nil
	}
	if series.Validate() != nil || series.TournamentID != authority.TournamentID ||
		(series.FirstParticipantID != authority.ParticipantID && series.SecondParticipantID != authority.ParticipantID) {
		return domain.ErrInternal
	}
	return nil
}

func validateWaveView(
	authority usecase.ParticipantSnapshotView,
	view *usecase.WaveView,
) error {
	if view == nil {
		return nil
	}
	if view.Revision < 1 || view.Wave.Validate() != nil || view.Wave.TournamentID != authority.TournamentID {
		return domain.ErrInternal
	}
	found := false
	for _, member := range view.Wave.Members {
		if view.ReadinessRevisions[member.ParticipantID] < 1 || view.SeriesIDs[member.ParticipantID] == uuid.Nil {
			return domain.ErrInternal
		}
		found = found || member.ParticipantID == authority.ParticipantID
	}
	if !found {
		return domain.ErrInternal
	}
	return nil
}

func authorizeAssignment(view usecase.ParticipantSnapshotView, assignmentID uuid.UUID) error {
	if view.Assignment == nil || view.Assignment.AssignmentID != assignmentID {
		return domain.ErrAssignmentParticipant
	}
	return nil
}

func validRecoveryCursor(cursor *usecase.RecoveryCursor) bool {
	return cursor == nil || (cursor.ProjectionRevision >= 1 &&
		cursor.ParticipantViewRevision >= 1 && cursor.EventSequence >= 0)
}

func cloneRecoveryView(view usecase.RecoveryView) usecase.RecoveryView {
	cloned := view
	cloned.Lobby = cloneLobbyView(view.Lobby)
	if view.Assignment != nil {
		assignment := cloneParticipantAssignment(*view.Assignment)
		cloned.Assignment = &assignment
	}
	if view.Draft != nil {
		draft := *view.Draft
		draft.Execution = cloneDraftExecutionView(view.Draft.Execution)
		cloned.Draft = &draft
	}
	if view.Series != nil {
		series := participantCloneSeries(*view.Series)
		cloned.Series = &series
	}
	if view.Wave != nil {
		wave := cloneWaveView(*view.Wave)
		cloned.Wave = &wave
	}
	return cloned
}

func cloneLobbyView(view usecase.LobbyView) usecase.LobbyView {
	cloned := view
	cloned.Series = append([]usecase.LobbySeriesView(nil), view.Series...)
	return cloned
}

func cloneParticipantAssignment(
	view usecase.TournamentParticipantAssignmentView,
) usecase.TournamentParticipantAssignmentView {
	cloned := view
	cloned.ActiveSnapshot.Hints = append([]string(nil), view.ActiveSnapshot.Hints...)
	cloned.ActiveSnapshot.TaskURL = cloneString(view.ActiveSnapshot.TaskURL)
	cloned.ActiveSnapshot.SourceFileURL = cloneString(view.ActiveSnapshot.SourceFileURL)
	return cloned
}

func participantCloneSeries(value domain.Series) domain.Series {
	cloned := value
	cloned.WinnerID = cloneUUID(value.WinnerID)
	if value.CurrentScoreRevisionID != nil {
		revision := *value.CurrentScoreRevisionID
		cloned.CurrentScoreRevisionID = &revision
	}
	if value.CurrentResultRevisionID != nil {
		revision := *value.CurrentResultRevisionID
		cloned.CurrentResultRevisionID = &revision
	}
	cloned.Slots = append([]domain.GameSlot(nil), value.Slots...)
	for index := range cloned.Slots {
		cloned.Slots[index].Attempts = append([]domain.Game(nil), value.Slots[index].Attempts...)
		for attemptIndex := range cloned.Slots[index].Attempts {
			attempt := &cloned.Slots[index].Attempts[attemptIndex]
			attempt.WinnerID = cloneUUID(attempt.WinnerID)
			if attempt.ResultRevisionID != nil {
				revision := *attempt.ResultRevisionID
				attempt.ResultRevisionID = &revision
			}
		}
	}
	return cloned
}

func cloneWaveView(view usecase.WaveView) usecase.WaveView {
	cloned := view
	cloned.Wave.Members = append([]domain.WaveMember(nil), view.Wave.Members...)
	if view.Wave.ReadyWindow != nil {
		window := *view.Wave.ReadyWindow
		window.ConsumedAt = participantCloneTime(view.Wave.ReadyWindow.ConsumedAt)
		cloned.Wave.ReadyWindow = &window
	}
	cloned.Wave.StartedAt = participantCloneTime(view.Wave.StartedAt)
	cloned.Wave.PausedAt = participantCloneTime(view.Wave.PausedAt)
	cloned.ReadinessRevisions = make(map[uuid.UUID]int64, len(view.ReadinessRevisions))
	for key, value := range view.ReadinessRevisions {
		cloned.ReadinessRevisions[key] = value
	}
	cloned.SeriesIDs = make(map[uuid.UUID]uuid.UUID, len(view.SeriesIDs))
	for key, value := range view.SeriesIDs {
		cloned.SeriesIDs[key] = value
	}
	return cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func participantCloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
