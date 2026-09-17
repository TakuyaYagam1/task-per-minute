package draft

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	exactdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/seriesgraph"
)

// completeSwissDraft runs within the transaction that appends the last action.
// A failure rolls back that action as well as every branch and graph mutation.
func (r *ParticipantDraftRepository) completeSwissDraft(ctx context.Context, execution draftusecase.Execution) error {
	if execution.Format != domain.SeriesFormatBO1 || execution.State != draftusecase.ExecutionStateCompleted {
		return nil
	}
	q := r.tx.Querier(ctx)
	scope, err := q.LockSwissDraftCompletion(ctx, execution.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if scope.SeriesState != string(domain.SeriesStatePlanned) || scope.SeriesID != execution.SeriesID {
		return domain.ErrConflict
	}
	if execution.Validate() != nil || len(execution.Actions) == 0 {
		return domain.ErrInternal
	}
	// The deadline worker uses the server clock that accepted the final action.
	// Keep graph timestamps at least as late as that persisted action when the
	// database clock trails the application clock.
	lastActionAt := execution.Actions[len(execution.Actions)-1].OccurredAt
	if scope.CompletedAt.Time.Before(lastActionAt) {
		scope.CompletedAt = tstz(lastActionAt)
	}
	assignment, err := r.activateSwissDraftBranch(ctx, scope, execution)
	if err != nil {
		return err
	}
	category, err := r.swissDraftCompletionCategory(ctx, scope, execution)
	if err != nil {
		return err
	}
	graph, _, err := seriesgraph.Materialize(nil, seriesgraph.MaterializeInput{
		CommandID: execution.CommandID, DeliveredAt: scope.CompletedAt.Time.UTC(),
		Series: domain.Series{
			ID: scope.SeriesID, TournamentID: scope.TournamentID, FirstParticipantID: scope.FirstParticipantID,
			SecondParticipantID: scope.SecondParticipantID, Format: domain.SeriesFormatBO1, State: domain.SeriesStatePlanned,
		},
		CategoryRevision: category, SelectedCategories: execution.SelectedCategories,
		AssignmentPlans: []assignmentusecase.ExactNormalAssignmentPlan{assignment},
	})
	if err != nil {
		return err
	}
	return r.persistSwissDraftGraph(ctx, scope, graph)
}

func (r *ParticipantDraftRepository) activateSwissDraftBranch(ctx context.Context, scope sqlc.LockSwissDraftCompletionRow, execution draftusecase.Execution) (assignmentusecase.ExactNormalAssignmentPlan, error) {
	if r == nil || r.exactDrafts == nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, domain.ErrInternal
	}
	ids, err := swissDraftIdentity(scope.SeriesID)
	if err != nil || ids.DraftID != execution.ID || ids.CategoryRevisionID != scope.CategoryRevisionID {
		return assignmentusecase.ExactNormalAssignmentPlan{}, domain.ErrConflict
	}
	repository := exactdraftrepo.NewExactDraftBranchPlanPostgres(r.tx, r.exactDrafts)
	plan, _, err := assignmentusecase.NewExactDraftBranchPlanUseCase(repository).ActivateCompletedBranch(ctx,
		assignmentusecase.ExactDraftBranchActivationCommand{
			PlanID: ids.DraftAssignmentPlanID, DraftID: execution.ID,
			ExpectedPlanRevisionID:  ids.DraftAssignmentRevisionID,
			ExpectedDraftRevisionID: execution.RevisionID, ExpectedDraftRevision: execution.Revision,
			CommandID: execution.CommandID, CommittedAt: scope.CompletedAt.Time.UTC(),
			ReleaseReason: "Swiss draft completed",
		})
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, fmt.Errorf("activate Swiss draft branch: %w", err)
	}
	if plan == nil || plan.Validate() != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, domain.ErrInternal
	}
	active, found := exactdraftrepo.ActiveExactDraftBranch(*plan)
	if !found || len(active.Assignments) != 1 || len(execution.SelectedCategories) != 1 {
		return assignmentusecase.ExactNormalAssignmentPlan{}, domain.ErrConflict
	}
	return active.Assignments[0].Plan, nil
}

func (r *ParticipantDraftRepository) swissDraftCompletionCategory(ctx context.Context, scope sqlc.LockSwissDraftCompletionRow, execution draftusecase.Execution) (draftusecase.CategoryRevision, error) {
	if r.contentLoader == nil {
		return draftusecase.CategoryRevision{}, domain.ErrInternal
	}
	content, err := r.contentLoader(ctx, r.tx.Querier(ctx), scope.TournamentID)
	if err != nil {
		return draftusecase.CategoryRevision{}, err
	}
	mode := domain.CategoryModeDraft
	category, _, err := draftusecase.DeriveCategoryRevision(nil, draftusecase.CategoryRevisionCommand{
		ID: scope.CategoryRevisionID, SeriesID: scope.SeriesID, RosterID: scope.RosterID,
		SeriesState: domain.SeriesStatePlanned, Stage: domain.TournamentStageSwiss,
		Configuration: content, ModeOverride: &mode, CreatedAt: scope.CategoryCreatedAt.Time.UTC(),
	})
	if err != nil {
		return draftusecase.CategoryRevision{}, err
	}
	if category.Revision != scope.CategoryRevision || !slices.Equal(category.CategoryPool.Categories, execution.Pool) ||
		content.NormalPool.ID != scope.SourcePoolRevisionID {
		return draftusecase.CategoryRevision{}, domain.ErrConflict
	}
	return category, nil
}

func (r *ParticipantDraftRepository) persistSwissDraftGraph(ctx context.Context, scope sqlc.LockSwissDraftCompletionRow, graph seriesgraph.SeriesGraph) error {
	if len(graph.Series.Slots) != 1 || len(graph.Series.Slots[0].Attempts) != 1 || len(graph.Assignments) != 1 {
		return domain.ErrInternal
	}
	q := r.tx.Querier(ctx)
	now := scope.CompletedAt.Time.UTC()
	if _, err := q.LockSwissSeriesForMaterialization(ctx, sqlc.LockSwissSeriesForMaterializationParams{
		SeriesID: scope.SeriesID, TournamentID: scope.TournamentID, RosterID: scope.RosterID, UpdatedAt: tstz(now),
	}); err != nil {
		return err
	}
	slot := graph.Series.Slots[0]
	if _, err := q.CreateGameSlot(ctx, sqlc.CreateGameSlotParams{
		ID: slot.ID, SeriesID: scope.SeriesID, RosterID: scope.RosterID,
		SlotNumber: 1, Category: string(slot.Category), CreatedAt: tstz(now),
	}); err != nil {
		return err
	}
	attempt := slot.Attempts[0]
	if _, err := q.CreateGameAttempt(ctx, sqlc.CreateGameAttemptParams{
		ID: attempt.ID, SlotID: slot.ID, SeriesID: scope.SeriesID, RosterID: scope.RosterID,
		AttemptNumber: 1, State: string(domain.GameStatePlanned), CreatedAt: tstz(now),
	}); err != nil {
		return err
	}
	aggregate := graph.Assignments[0]
	primary := aggregate.Plan.SelectedEdges[0]
	if err := assignmentrepo.NewAssignmentPostgres(r.tx).CreateAssignmentTx(ctx, assignmentrepo.AssignmentCreateInput{
		ID: aggregate.ID, AttemptID: attempt.ID, SeriesID: scope.SeriesID, RosterID: scope.RosterID,
		PlanID: aggregate.Plan.PlanID, BranchID: aggregate.Plan.BranchID,
		ReservationID: primary.ReservationID, SnapshotID: primary.Snapshot.SnapshotID, CreatedAt: now,
	}); err != nil {
		return err
	}
	readyAt := now.Add(time.Microsecond)
	if _, err := q.ReadySwissGameForMaterialization(ctx, sqlc.ReadySwissGameForMaterializationParams{
		GameID: attempt.ID, SlotID: slot.ID, SeriesID: scope.SeriesID, RosterID: scope.RosterID, UpdatedAt: tstz(readyAt),
	}); err != nil {
		return err
	}
	_, err := q.ReadySwissSeriesForMaterialization(ctx, sqlc.ReadySwissSeriesForMaterializationParams{
		SeriesID: scope.SeriesID, TournamentID: scope.TournamentID, RosterID: scope.RosterID, UpdatedAt: tstz(readyAt),
	})
	return err
}
