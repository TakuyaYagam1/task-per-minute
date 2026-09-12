package postgres

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

// Swiss drafts retain the existing exact-draft reservation identity scheme.
func swissDraftIdentity(seriesID uuid.UUID) (playoff.FinalStageIDs, error) {
	ids, err := playoff.FinalStageIdentity(seriesID)
	if err != nil {
		return playoff.FinalStageIDs{}, err
	}
	ids.FinalSeriesID = seriesID
	ids.CategoryRevisionID = tournamentAdminExecutionID(seriesID, "swiss-category-revision")
	ids.DraftID = tournamentAdminExecutionID(seriesID, "swiss-draft")
	ids.FirstSlotID = tournamentAdminExecutionID(seriesID, "swiss-game-slot-1")
	if !ids.Valid() {
		return playoff.FinalStageIDs{}, domain.ErrValidation
	}
	return ids, nil
}

func exactDraftIdentity(commandID, seriesID uuid.UUID, format domain.SeriesFormat) (playoff.FinalStageIDs, error) {
	if format == domain.SeriesFormatBO1 {
		if commandID != seriesID {
			return playoff.FinalStageIDs{}, domain.ErrConflict
		}
		return swissDraftIdentity(seriesID)
	}
	if format != domain.SeriesFormatBO3 {
		return playoff.FinalStageIDs{}, domain.ErrConflict
	}
	return playoff.FinalStageIdentity(commandID)
}

func exactDraftCategoryCount(format domain.SeriesFormat) int {
	switch format {
	case domain.SeriesFormatBO1:
		return 1
	case domain.SeriesFormatBO3:
		return 3
	default:
		return 0
	}
}

func (r *TournamentAdminExecutionPostgres) materializeSwissDraftBO1(ctx context.Context, plan tournamentadmin.PairingPlan) error {
	if plan.Command.CategoryMode != domain.CategoryModeDraft || len(plan.Pairs) == 0 || len(plan.SeriesIDs) != len(plan.Pairs) {
		return domain.ErrValidation
	}
	content, err := loadTournamentPreflightContent(ctx, r.tx.Querier(ctx), plan.Command.TournamentID)
	if err != nil {
		return err
	}
	for index, pair := range plan.Pairs {
		ids, identityErr := swissDraftIdentity(plan.SeriesIDs[index])
		if identityErr != nil {
			return identityErr
		}
		mode := domain.CategoryModeDraft
		category, _, categoryErr := draftusecase.DeriveCategoryRevision(nil, draftusecase.CategoryRevisionCommand{
			ID: ids.CategoryRevisionID, SeriesID: ids.FinalSeriesID, RosterID: plan.Authority.RosterID,
			SeriesState: domain.SeriesStatePlanned, Stage: domain.TournamentStageSwiss,
			Configuration: content.configuration, ModeOverride: &mode, CreatedAt: plan.DecidedAt,
		})
		if categoryErr != nil {
			return categoryErr
		}
		if !swissDraftCategoriesMatch(plan.Command.Categories, category.CategoryPool.Categories) {
			return domain.ErrValidation
		}
		execution, startErr := draftusecase.StartExecution(draftusecase.ExecutionStartCommand{
			CategoryRevision: category, DraftID: ids.DraftID, InitialRevisionID: ids.DraftInitialRevisionID,
			DecisionEvidenceID: ids.DraftOrderDecisionID,
			ParticipantIDs:     [2]uuid.UUID{pair.FirstParticipantID, pair.SecondParticipantID},
			ServiceEpoch:       ids.DraftServiceEpochID, CommandID: plan.Command.CommandID, StartedAt: plan.DecidedAt,
		})
		if startErr != nil {
			return startErr
		}
		drafts := NewDraftPostgres(r.tx)
		if _, err := drafts.Create(ctx, DraftCreateInput{
			ID: execution.ID, SeriesID: execution.SeriesID, RosterID: plan.Authority.RosterID,
			CategoryRevisionID: category.ID, CategoryRevision: category.Revision,
			SourcePoolRevision: content.configuration.NormalPool.ID,
			FirstParticipantID: execution.FirstParticipantID, SecondParticipantID: execution.SecondParticipantID,
			Format: execution.Format, Pool: execution.Pool, InitialRevisionID: execution.RevisionID,
			CommandID: execution.CommandID, ServiceEpoch: execution.ServiceEpoch,
			AbsoluteDeadline: *execution.AbsoluteDeadline, DecisionEvidence: execution.FirstActorDecision,
			CreatedAt: plan.DecidedAt,
		}); err != nil {
			return fmt.Errorf("create Swiss draft: %w", err)
		}
		repository := NewExactDraftBranchPlanPostgres(r.tx, drafts)
		authority, err := repository.LoadExactDraftBranchPlanAuthority(ctx, execution.ID)
		if err != nil {
			return err
		}
		command := swissDraftAssignmentCommand(ids, execution, authority)
		if _, _, err := assignmentusecase.NewExactDraftBranchPlanUseCase(repository).PlanAndCommit(ctx, command); err != nil {
			return fmt.Errorf("reserve Swiss draft branches: %w", err)
		}
		if err := createMaterializedSeriesPresence(ctx, r.tx.Querier(ctx), plan.Command.TournamentID,
			plan.Authority.RosterID, execution.SeriesID,
			[2]uuid.UUID{execution.FirstParticipantID, execution.SecondParticipantID}, plan.DecidedAt); err != nil {
			return err
		}
	}
	return nil
}

func swissDraftCategoriesMatch(requested, pool []domain.Category) bool {
	if len(requested) != len(pool) {
		return false
	}
	for _, selected := range requested {
		if !slices.Contains(pool, selected) {
			return false
		}
	}
	return true
}

func swissDraftAssignmentCommand(ids playoff.FinalStageIDs, execution draftusecase.Execution, authority assignmentusecase.ExactDraftBranchPlanAuthority) assignmentusecase.ExactDraftBranchPlanCommand {
	command := assignmentusecase.ExactDraftBranchPlanCommand{
		PlanID: ids.DraftAssignmentPlanID, PlanRevisionID: ids.DraftAssignmentRevisionID,
		DraftID: execution.ID, ExpectedDraftRevisionID: execution.RevisionID,
		ExpectedDraftRevision: execution.Revision, CreatedAt: execution.FirstActorDecision.DecidedAt,
		Branches: make([]assignmentusecase.ExactDraftBranchCommand, len(authority.Branches)),
	}
	for index, source := range authority.Branches {
		branch := assignmentusecase.ExactDraftBranchCommand{
			BranchID: ids.DraftAssignmentBranchID(source.Key), Key: source.Key,
			Assignments: make([]assignmentusecase.ExactNormalAssignmentCommand, len(source.Assignments)),
		}
		for position, exact := range source.Assignments {
			child := ids.DraftAssignmentChildBranchID(source.Key, position+1)
			branch.ChildBranchIDs[position] = child
			item := assignmentusecase.ExactNormalAssignmentCommand{
				Scope: exact.Scope, PlanID: command.PlanID, PlanRevisionID: command.PlanRevisionID,
				BranchID: child, DecisionEvidenceID: ids.DraftAssignmentDecisionID(source.Key, position+1), CreatedAt: command.CreatedAt,
			}
			for reserve := range item.EdgeIDs {
				item.EdgeIDs[reserve] = ids.DraftAssignmentEdgeID(source.Key, position+1, reserve+1)
				item.ReservationIDs[reserve] = ids.DraftAssignmentReservationID(source.Key, position+1, reserve+1)
				item.SnapshotIDs[reserve] = ids.DraftAssignmentSnapshotID(source.Key, position+1, reserve+1)
			}
			branch.Assignments[position] = item
		}
		command.Branches[index] = branch
	}
	return command
}
