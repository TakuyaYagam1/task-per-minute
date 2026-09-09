package participant

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

const (
	maxSubmittedFlagBytes   = 4096
	maxSurrenderReasonRunes = 512
)

func (a *ParticipantUseCase) SetReady(
	ctx context.Context,
	command usecase.ReadyCommand,
) (usecase.ReadinessEvent, error) {
	if ctx == nil {
		return usecase.ReadinessEvent{}, domain.ErrValidation
	}
	if a == nil || a.commands == nil {
		return usecase.ReadinessEvent{}, domain.ErrInternal
	}
	if !validReadyCommand(command) {
		return usecase.ReadinessEvent{}, domain.ErrValidation
	}
	event, err := a.commands.SetReady(ctx, command)
	if err != nil {
		return usecase.ReadinessEvent{}, err
	}
	if err := validateReadinessResult(command, event); err != nil {
		return usecase.ReadinessEvent{}, err
	}
	return readinessEventView(event), nil
}

func (a *ParticipantUseCase) SubmitDraftAction(
	ctx context.Context,
	command usecase.DraftActionCommand,
) (usecase.DraftExecutionView, error) {
	if ctx == nil {
		return usecase.DraftExecutionView{}, domain.ErrValidation
	}
	if a == nil || a.commands == nil {
		return usecase.DraftExecutionView{}, domain.ErrInternal
	}
	if !validDraftActionCommand(command) {
		return usecase.DraftExecutionView{}, domain.ErrValidation
	}
	execution, err := a.commands.SubmitDraftAction(ctx, command)
	if err != nil {
		return usecase.DraftExecutionView{}, err
	}
	if execution.Validate() != nil {
		return usecase.DraftExecutionView{}, domain.ErrInternal
	}
	draft, err := execution.DomainDraft()
	if err != nil || draft.SeriesID != command.SeriesID {
		return usecase.DraftExecutionView{}, domain.ErrInternal
	}
	return draftExecutionView(execution), nil
}

func (a *ParticipantUseCase) SubmitFlag(
	ctx context.Context,
	command usecase.SubmissionCommand,
) (usecase.SubmissionResult, error) {
	if ctx == nil {
		return usecase.SubmissionResult{}, domain.ErrValidation
	}
	if a == nil || a.commands == nil {
		return usecase.SubmissionResult{}, domain.ErrInternal
	}
	if !validSubmissionCommand(command) {
		return usecase.SubmissionResult{}, domain.ErrValidation
	}
	result, err := a.commands.SubmitFlag(ctx, command)
	if err != nil {
		return usecase.SubmissionResult{}, err
	}
	record := result.Submission
	if gamedomain.ValidateSubmission(record) != nil || record.CommandID != command.CommandID ||
		record.ParticipantID == uuid.Nil || record.Scope.Game.TournamentID != command.TournamentID ||
		record.Scope.Game.SeriesID != command.SeriesID || record.Scope.Game.GameID != command.GameID ||
		result.ProjectionRevision < command.ExpectedProjectionRevision {
		return usecase.SubmissionResult{}, domain.ErrInternal
	}
	return result, nil
}

func (a *ParticipantUseCase) Surrender(
	ctx context.Context,
	command usecase.SurrenderCommand,
) (usecase.OfficialResultView, error) {
	if ctx == nil {
		return usecase.OfficialResultView{}, domain.ErrValidation
	}
	if a == nil || a.commands == nil {
		return usecase.OfficialResultView{}, domain.ErrInternal
	}
	command.Reason = strings.TrimSpace(command.Reason)
	if !validSurrenderCommand(command) {
		return usecase.OfficialResultView{}, domain.ErrValidation
	}
	revision, err := a.commands.Surrender(ctx, command)
	if err != nil {
		return usecase.OfficialResultView{}, err
	}
	if validateOfficialResultView(revision) != nil || revision.CommandID != command.CommandID ||
		revision.TournamentID != command.TournamentID || revision.SeriesID != command.SeriesID {
		return usecase.OfficialResultView{}, domain.ErrInternal
	}
	return revision, nil
}

func (a *ParticipantUseCase) ApplyPostSeriesAction(
	ctx context.Context,
	command usecase.PostSeriesCommand,
) (usecase.PostSeriesResult, error) {
	if ctx == nil {
		return usecase.PostSeriesResult{}, domain.ErrValidation
	}
	if a == nil || a.commands == nil {
		return usecase.PostSeriesResult{}, domain.ErrInternal
	}
	if !validPostSeriesCommand(command) {
		return usecase.PostSeriesResult{}, domain.ErrValidation
	}
	result, err := a.commands.ApplyPostSeriesAction(ctx, command)
	if err != nil {
		return usecase.PostSeriesResult{}, err
	}
	if result.TournamentID != command.TournamentID || result.ParticipantID == uuid.Nil ||
		result.SeriesID != command.SeriesID || result.ProjectionRevision < command.ExpectedProjectionRevision ||
		result.AcceptedAction != command.Action || !result.AcceptedAction.IsValid() {
		return usecase.PostSeriesResult{}, domain.ErrInternal
	}
	return result, nil
}

func validReadyCommand(command usecase.ReadyCommand) bool {
	return validCommandIdentity(command.Actor, command.TournamentID, command.CommandID) &&
		command.WaveID != uuid.Nil && command.ExpectedProjectionRevision >= 1
}

func validDraftActionCommand(command usecase.DraftActionCommand) bool {
	return validCommandIdentity(command.Actor, command.TournamentID, command.CommandID) &&
		command.SeriesID != uuid.Nil && command.ExpectedProjectionRevision >= 1 &&
		command.ExpectedDraftRevision >= 1 && command.ExpectedTurn >= 1 &&
		command.Action.IsValid() && command.Category.IsValid()
}

func validSubmissionCommand(command usecase.SubmissionCommand) bool {
	return validCommandIdentity(command.Actor, command.TournamentID, command.CommandID) &&
		command.SeriesID != uuid.Nil && command.GameID != uuid.Nil &&
		command.ExpectedProjectionRevision >= 1 && utf8.ValidString(command.SubmittedFlag) &&
		len(command.SubmittedFlag) >= 1 && len(command.SubmittedFlag) <= maxSubmittedFlagBytes
}

func validSurrenderCommand(command usecase.SurrenderCommand) bool {
	return validCommandIdentity(command.Actor, command.TournamentID, command.CommandID) &&
		command.SeriesID != uuid.Nil && command.ExpectedProjectionRevision >= 1 && command.Confirmed &&
		utf8.ValidString(command.Reason) && utf8.RuneCountInString(command.Reason) <= maxSurrenderReasonRunes
}

func validPostSeriesCommand(command usecase.PostSeriesCommand) bool {
	return validCommandIdentity(command.Actor, command.TournamentID, command.CommandID) &&
		command.SeriesID != uuid.Nil && command.ExpectedProjectionRevision >= 1 && command.Action.IsValid()
}

func validCommandIdentity(actor usecase.Identity, tournamentID, commandID uuid.UUID) bool {
	return actor.PlayerID != uuid.Nil && tournamentID != uuid.Nil && commandID != uuid.Nil
}

func validateReadinessResult(command usecase.ReadyCommand, event readiness.ReadinessEvent) error {
	wantType := readiness.ReadinessEventCleared
	if command.Ready {
		wantType = readiness.ReadinessEventReady
	}
	if event.CommandID != command.CommandID || event.ParticipantID == uuid.Nil ||
		event.Scope.WaveID != command.WaveID || event.Scope.WindowID == uuid.Nil ||
		event.Type != wantType || !domain.IsValidServerTime(event.OccurredAt) {
		return domain.ErrInternal
	}
	return nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validateOfficialResultView(view usecase.OfficialResultView) error {
	if view.ID.IsZero() || view.Ordinal < 1 || view.CommandID == uuid.Nil ||
		view.TournamentID == uuid.Nil || view.SeriesID == uuid.Nil ||
		view.Actor.Validate() != nil || view.WinnerID == nil || *view.WinnerID == uuid.Nil ||
		view.ScoreRevisionID.IsZero() || view.SourceProjectionRevisionID == uuid.Nil ||
		view.SeriesState != domain.SeriesStateCompleted ||
		!view.SeriesReason.IsLegalFor(view.SeriesState) || !domain.IsValidServerTime(view.RecordedAt) {
		return domain.ErrInternal
	}
	if (view.Ordinal == 1) != (view.PreviousRevisionID == nil) {
		return domain.ErrInternal
	}
	if view.PreviousRevisionID != nil && (view.PreviousRevisionID.IsZero() || *view.PreviousRevisionID == view.ID) {
		return domain.ErrInternal
	}
	return nil
}
