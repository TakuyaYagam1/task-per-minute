package draft

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	exactdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
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

type SeriesPresenceWriter func(context.Context, *sqlc.Queries, uuid.UUID, uuid.UUID, uuid.UUID, [2]uuid.UUID, time.Time) error

// MaterializeSwissDraftBO1 persists the deterministic BO1 Swiss draft graph.
//
//nolint:gocyclo // Materializes one deterministic draft graph and validates each persisted invariant inline.
func MaterializeSwissDraftBO1(
	ctx context.Context,
	tx *db.TxManager,
	plan pairingusecase.PairingPlan,
	presence SeriesPresenceWriter,
) error {
	if tx == nil || presence == nil || plan.Command.CategoryMode != domain.CategoryModeDraft || len(plan.Pairs) == 0 || len(plan.SeriesIDs) != len(plan.Pairs) {
		return domain.ErrValidation
	}
	content, err := rosterrepo.LoadPreflightContent(ctx, tx.Querier(ctx), plan.Command.TournamentID)
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
			Configuration: content.Configuration, ModeOverride: &mode, CreatedAt: plan.DecidedAt,
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
		drafts := draft.NewDraftPostgres(tx)
		if _, err := drafts.Create(ctx, draft.DraftCreateInput{
			ID: execution.ID, SeriesID: execution.SeriesID, RosterID: plan.Authority.RosterID,
			CategoryRevisionID: category.ID, CategoryRevision: category.Revision,
			SourcePoolRevision: content.Configuration.NormalPool.ID,
			FirstParticipantID: execution.FirstParticipantID, SecondParticipantID: execution.SecondParticipantID,
			Format: execution.Format, Pool: execution.Pool, InitialRevisionID: execution.RevisionID,
			CommandID: execution.CommandID, ServiceEpoch: execution.ServiceEpoch,
			AbsoluteDeadline: *execution.AbsoluteDeadline, DecisionEvidence: execution.FirstActorDecision,
			CreatedAt: plan.DecidedAt,
		}); err != nil {
			return fmt.Errorf("create Swiss draft: %w", err)
		}
		repository := exactdraftrepo.NewExactDraftBranchPlanPostgres(tx, drafts)
		authority, err := repository.LoadExactDraftBranchPlanAuthority(ctx, execution.ID)
		if err != nil {
			return err
		}
		command := swissDraftAssignmentCommand(ids, execution, authority)
		if _, _, err := assignmentusecase.NewExactDraftBranchPlanUseCase(repository).PlanAndCommit(ctx, command); err != nil {
			return fmt.Errorf("reserve Swiss draft branches: %w", err)
		}
		if err := presence(ctx, tx.Querier(ctx), plan.Command.TournamentID,
			plan.Authority.RosterID, execution.SeriesID,
			[2]uuid.UUID{execution.FirstParticipantID, execution.SecondParticipantID}, plan.DecidedAt); err != nil {
			return err
		}
	}
	return nil
}

func tournamentAdminExecutionID(namespace uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(role))
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
