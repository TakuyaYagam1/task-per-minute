package participant

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

type CommandCoordinatorDependencies struct {
	Transactions ParticipantTransactionManager
	Authority    CommandAuthority
	Readiness    ReadinessWorkflow
	Draft        DraftActionWorkflow
	Submission   SubmissionWorkflow
	Settlement   SettlementWorkflow
	Surrender    SurrenderWorkflow
	PostSeries   PostSeriesWorkflow
	Postseason   ParticipantPostseasonWorkflow
}

// CommandCoordinator is the single use case entrypoint for participant
// mutations. Every path enters a write transaction before resolving actor and
// aggregate authority.
type CommandCoordinator struct {
	transactions ParticipantTransactionManager
	authority    CommandAuthority
	readiness    ReadinessWorkflow
	draft        DraftActionWorkflow
	submission   SubmissionWorkflow
	settlement   SettlementWorkflow
	surrender    SurrenderWorkflow
	postSeries   PostSeriesWorkflow
	postseason   ParticipantPostseasonWorkflow
}

func NewCommandCoordinator(deps CommandCoordinatorDependencies) *CommandCoordinator {
	return &CommandCoordinator{
		transactions: deps.Transactions,
		authority:    deps.Authority,
		readiness:    deps.Readiness,
		draft:        deps.Draft,
		submission:   deps.Submission,
		settlement:   deps.Settlement,
		surrender:    deps.Surrender,
		postSeries:   deps.PostSeries,
		postseason:   deps.Postseason,
	}
}

func (c *CommandCoordinator) SetReady(
	ctx context.Context,
	command usecase.ReadyCommand,
) (readiness.ReadinessEvent, error) {
	if ctx == nil || !validReadyCommand(command) {
		return readiness.ReadinessEvent{}, domain.ErrValidation
	}
	if c == nil || c.transactions == nil || c.authority == nil || c.readiness == nil {
		return readiness.ReadinessEvent{}, domain.ErrInternal
	}

	var event readiness.ReadinessEvent
	err := c.transactions.Do(ctx, func(txCtx context.Context) error {
		resolved, err := c.authority.ResolveReady(txCtx, command)
		if err != nil {
			return err
		}
		if err := validateResolvedReady(command, resolved); err != nil {
			return err
		}
		record, _, err := c.readiness.SetParticipantReady(txCtx, resolved.Command)
		if err != nil {
			return err
		}
		resolvedEvent, found := readinessCommandEvent(record, command.CommandID)
		if !found {
			return domain.ErrInternal
		}
		event = resolvedEvent
		return nil
	})
	if err != nil {
		return readiness.ReadinessEvent{}, err
	}
	return event, nil
}

func (c *CommandCoordinator) SubmitDraftAction(
	ctx context.Context,
	command usecase.DraftActionCommand,
) (draftusecase.Execution, error) {
	if ctx == nil || !validDraftActionCommand(command) {
		return draftusecase.Execution{}, domain.ErrValidation
	}
	if c == nil || c.transactions == nil || c.authority == nil || c.draft == nil || c.postseason == nil {
		return draftusecase.Execution{}, domain.ErrInternal
	}

	var execution draftusecase.Execution
	err := c.transactions.Do(ctx, func(txCtx context.Context) error {
		resolved, err := c.authority.ResolveDraftAction(txCtx, command)
		if err != nil {
			return err
		}
		if err := validateResolvedDraftAction(command, resolved); err != nil {
			return err
		}
		result, err := c.draft.Apply(txCtx, resolved.Command)
		if err != nil {
			return err
		}
		if result.Draft.Validate() != nil {
			return domain.ErrInternal
		}
		execution = draftusecase.CloneExecution(result.Draft)
		if execution.State == draftusecase.ExecutionStateCompleted {
			if _, err := c.postseason.ActivateFinalAfterDraft(txCtx, playoff.TerminalDraftCommand{
				TournamentID: command.TournamentID,
				SeriesID:     resolved.SeriesID,
				DraftID:      execution.ID,
				CommandID:    command.CommandID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return draftusecase.Execution{}, err
	}
	return execution, nil
}

func (c *CommandCoordinator) SubmitFlag(
	ctx context.Context,
	command usecase.SubmissionCommand,
) (usecase.SubmissionResult, error) {
	if ctx == nil || !validSubmissionCommand(command) {
		return usecase.SubmissionResult{}, domain.ErrValidation
	}
	if c == nil || c.transactions == nil || c.authority == nil || c.submission == nil ||
		c.settlement == nil || c.postseason == nil {
		return usecase.SubmissionResult{}, domain.ErrInternal
	}

	var result usecase.SubmissionResult
	err := c.transactions.Do(ctx, func(txCtx context.Context) error {
		resolved, err := c.authority.ResolveSubmission(txCtx, command)
		if err != nil {
			return err
		}
		if err := validateResolvedSubmission(command, resolved); err != nil {
			return err
		}
		if resolved.Replay != nil {
			result = *resolved.Replay
			return nil
		}
		record, _, err := c.submission.Submit(txCtx, resolved.Command)
		if err != nil {
			return err
		}
		if gamedomain.ValidateSubmission(record) != nil {
			return domain.ErrInternal
		}
		projectionRevision := resolved.Authority.ProjectionRevision
		if record.Correct {
			settled, _, settleErr := c.settlement.Settle(txCtx, gameusecase.SettlementCommand{
				Scope:     record.Scope,
				CommandID: record.CommandID,
			})
			if settleErr != nil {
				return settleErr
			}
			if err := validateSubmissionSettlement(record, settled); err != nil {
				return err
			}
			projectionRevision = settled.Evidence.ProjectionRevision
			receipt, postErr := c.postseason.AdvanceAfterSeriesSettlement(txCtx, playoff.TerminalSeriesCommand{
				TournamentID: command.TournamentID, SeriesID: command.SeriesID,
			})
			if postErr != nil {
				return postErr
			}
			if receipt.ProjectionRevision > 0 {
				projectionRevision = receipt.ProjectionRevision
			}
		}
		result = usecase.SubmissionResult{
			ProjectionRevision: projectionRevision,
			Submission:         record,
		}
		return nil
	})
	if err != nil {
		return usecase.SubmissionResult{}, err
	}
	return result, nil
}

func validateSubmissionSettlement(
	submission gamedomain.Submission,
	settlement *gameusecase.SettlementRecord,
) error {
	if !submission.Correct || settlement == nil || settlement.Validate() != nil ||
		settlement.Scope != submission.Scope || !settlement.WinningSubmission.Correct ||
		settlement.Evidence.ProjectionRevision < 1 {
		return domain.ErrInternal
	}
	return nil
}

func (c *CommandCoordinator) Surrender(
	ctx context.Context,
	command usecase.SurrenderCommand,
) (usecase.OfficialResultView, error) {
	if ctx == nil || !validSurrenderCommand(command) {
		return usecase.OfficialResultView{}, domain.ErrValidation
	}
	if c == nil || c.transactions == nil || c.authority == nil || c.surrender == nil || c.postseason == nil {
		return usecase.OfficialResultView{}, domain.ErrInternal
	}

	var result usecase.OfficialResultView
	err := c.transactions.Do(ctx, func(txCtx context.Context) error {
		resolved, err := c.authority.ResolveSurrender(txCtx, command)
		if err != nil {
			return err
		}
		if err := validateResolvedSurrender(command, resolved); err != nil {
			return err
		}
		committed, _, err := c.surrender.Surrender(txCtx, resolved)
		if err != nil {
			return err
		}
		if validateOfficialResultView(committed) != nil {
			return domain.ErrInternal
		}
		if committed.SeriesState.IsTerminal() {
			if _, err := c.postseason.AdvanceAfterSeriesSettlement(txCtx, playoff.TerminalSeriesCommand{
				TournamentID: committed.TournamentID, SeriesID: committed.SeriesID,
			}); err != nil {
				return err
			}
		}
		result = committed
		return nil
	})
	if err != nil {
		return usecase.OfficialResultView{}, err
	}
	return result, nil
}

func (c *CommandCoordinator) ApplyPostSeriesAction(
	ctx context.Context,
	command usecase.PostSeriesCommand,
) (usecase.PostSeriesResult, error) {
	if ctx == nil || !validPostSeriesCommand(command) {
		return usecase.PostSeriesResult{}, domain.ErrValidation
	}
	if c == nil || c.transactions == nil || c.authority == nil || c.postSeries == nil {
		return usecase.PostSeriesResult{}, domain.ErrInternal
	}

	var result usecase.PostSeriesResult
	err := c.transactions.Do(ctx, func(txCtx context.Context) error {
		resolved, err := c.authority.ResolvePostSeries(txCtx, command)
		if err != nil {
			return err
		}
		if err := validateResolvedPostSeries(command, resolved); err != nil {
			return err
		}
		committed, _, err := c.postSeries.ApplyPostSeries(txCtx, resolved)
		if err != nil {
			return err
		}
		result = committed
		return nil
	})
	if err != nil {
		return usecase.PostSeriesResult{}, err
	}
	return result, nil
}

func validateResolvedAuthority(
	authority ParticipantCommandAuthority,
	actor usecase.Identity,
	tournamentID uuid.UUID,
	expectedRevision int64,
) error {
	if authority.TournamentID != tournamentID || authority.PlayerID != actor.PlayerID ||
		authority.RosterID == uuid.Nil || authority.ParticipantID == uuid.Nil ||
		!authority.TournamentState.IsValid() || authority.ProjectionRevisionID == uuid.Nil ||
		authority.ProjectionRevision != expectedRevision {
		return domain.ErrInternal
	}
	if authority.TournamentState == domain.TournamentStateCancelled {
		return domain.ErrConflict
	}
	return nil
}

func validateActiveAuthority(
	authority ParticipantCommandAuthority,
	actor usecase.Identity,
	tournamentID uuid.UUID,
	expectedRevision int64,
) error {
	if err := validateResolvedAuthority(authority, actor, tournamentID, expectedRevision); err != nil {
		return err
	}
	if authority.TournamentState.IsTerminal() {
		return domain.ErrConflict
	}
	return nil
}

func validateResolvedReady(command usecase.ReadyCommand, resolved ResolvedReadyCommand) error {
	leaf := resolved.Command
	if err := validateActiveAuthority(
		resolved.Authority,
		command.Actor,
		command.TournamentID,
		command.ExpectedProjectionRevision,
	); err != nil {
		return err
	}
	if leaf.Scope.WaveID != command.WaveID || leaf.Scope.WindowID == uuid.Nil ||
		leaf.CommandID != command.CommandID || leaf.ActorParticipantID != resolved.Authority.ParticipantID ||
		leaf.ParticipantID != resolved.Authority.ParticipantID || leaf.Ready != command.Ready ||
		leaf.ExpectedWaveRevisionID.IsZero() || leaf.ExpectedWindowRevisionID.IsZero() {
		return domain.ErrInternal
	}
	return nil
}

func validateResolvedDraftAction(command usecase.DraftActionCommand, resolved ResolvedDraftAction) error {
	leaf := resolved.Command
	if err := validateActiveAuthority(
		resolved.Authority,
		command.Actor,
		command.TournamentID,
		command.ExpectedProjectionRevision,
	); err != nil {
		return err
	}
	if resolved.SeriesID != command.SeriesID || leaf.DraftID == uuid.Nil ||
		leaf.ExpectedRevisionID == uuid.Nil || leaf.ExpectedRevision != command.ExpectedDraftRevision ||
		leaf.ExpectedServiceEpoch == uuid.Nil || leaf.ExpectedTurn != command.ExpectedTurn ||
		leaf.CommandID != command.CommandID || leaf.ResultRevisionID == uuid.Nil || leaf.ActionID == uuid.Nil ||
		leaf.ActorID != resolved.Authority.ParticipantID || leaf.Action != command.Action ||
		leaf.Category != command.Category {
		return domain.ErrInternal
	}
	return nil
}

func validateResolvedSubmission(command usecase.SubmissionCommand, resolved ResolvedSubmission) error {
	leaf := resolved.Command
	if resolved.Replay != nil {
		return validateResolvedSubmissionReplay(command, resolved)
	}
	if err := validateActiveAuthority(
		resolved.Authority,
		command.Actor,
		command.TournamentID,
		command.ExpectedProjectionRevision,
	); err != nil {
		return err
	}
	if leaf.Scope.Game.TournamentID != command.TournamentID ||
		leaf.Scope.Game.SeriesID != command.SeriesID || leaf.Scope.Game.GameID != command.GameID ||
		leaf.Scope.Game.SlotID == uuid.Nil || leaf.Scope.WaveID == uuid.Nil ||
		leaf.Scope.AssignmentID == uuid.Nil || leaf.CommandID != command.CommandID ||
		leaf.ActorParticipantID != resolved.Authority.ParticipantID ||
		leaf.ParticipantID != resolved.Authority.ParticipantID || leaf.SubmittedFlag != command.SubmittedFlag {
		return domain.ErrInternal
	}
	return nil
}

func validateResolvedSubmissionReplay(
	command usecase.SubmissionCommand,
	resolved ResolvedSubmission,
) error {
	authority := resolved.Authority
	replay := resolved.Replay
	if replay == nil || authority.TournamentID != command.TournamentID ||
		authority.PlayerID != command.Actor.PlayerID || authority.RosterID == uuid.Nil ||
		authority.ParticipantID == uuid.Nil || !authority.TournamentState.IsValid() ||
		authority.ProjectionRevisionID == uuid.Nil || authority.ProjectionRevision < 1 {
		return domain.ErrInternal
	}
	if gamedomain.ValidateSubmission(replay.Submission) != nil ||
		replay.Submission.CommandID != command.CommandID ||
		replay.Submission.ParticipantID != authority.ParticipantID ||
		replay.Submission.Scope.Game.TournamentID != command.TournamentID ||
		replay.Submission.Scope.Game.SeriesID != command.SeriesID ||
		replay.Submission.Scope.Game.GameID != command.GameID ||
		replay.Submission.Scope.Game.SlotID == uuid.Nil || replay.Submission.Scope.WaveID == uuid.Nil ||
		replay.Submission.Scope.AssignmentID == uuid.Nil || replay.ProjectionRevision < 1 {
		return domain.ErrInternal
	}
	return nil
}

func validateResolvedSurrender(command usecase.SurrenderCommand, resolved ResolvedSurrender) error {
	leaf := resolved.Command
	if err := validateActiveAuthority(
		resolved.Authority,
		command.Actor,
		command.TournamentID,
		command.ExpectedProjectionRevision,
	); err != nil {
		return err
	}
	if leaf.Scope.TournamentID != command.TournamentID || leaf.Scope.SeriesID != command.SeriesID ||
		leaf.CommandID != command.CommandID || leaf.ActorParticipantID != resolved.Authority.ParticipantID ||
		leaf.ForfeitingParticipantID != resolved.Authority.ParticipantID ||
		resolved.Reason != command.Reason || !leaf.ExpectedGame.IsValid() {
		return domain.ErrInternal
	}
	return nil
}

func validateResolvedPostSeries(command usecase.PostSeriesCommand, resolved ResolvedPostSeries) error {
	if err := validateResolvedAuthority(
		resolved.Authority,
		command.Actor,
		command.TournamentID,
		command.ExpectedProjectionRevision,
	); err != nil {
		return err
	}
	if resolved.SeriesID != command.SeriesID || !resolved.SeriesState.IsTerminal() ||
		resolved.CurrentResultRevisionID.IsZero() || resolved.CommandID != command.CommandID ||
		resolved.Action != command.Action {
		return domain.ErrInternal
	}
	return nil
}

func readinessCommandEvent(
	record *readiness.ReadinessRecord,
	commandID uuid.UUID,
) (readiness.ReadinessEvent, bool) {
	if record == nil {
		return readiness.ReadinessEvent{}, false
	}
	for index := len(record.Events) - 1; index >= 0; index-- {
		if record.Events[index].CommandID == commandID {
			return record.Events[index], true
		}
	}
	return readiness.ReadinessEvent{}, false
}

var _ CommandExecutor = (*CommandCoordinator)(nil)
