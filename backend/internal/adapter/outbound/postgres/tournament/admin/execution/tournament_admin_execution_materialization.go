package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/seriesgraph"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

// MaterializeSwissRandomBO1 exposes the core materializer to the root
// configuration facade without leaking root-package dependencies into this
// child package.
func (r *TournamentAdminExecutionPostgres) MaterializeSwissRandomBO1(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) error {
	return r.materializeSwissRandomBO1(ctx, plan)
}

// MaterializeSwissAdminBO1 exposes the core materializer to the root
// configuration facade without leaking root-package dependencies into this
// child package.
func (r *TournamentAdminExecutionPostgres) MaterializeSwissAdminBO1(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) error {
	return r.materializeSwissAdminBO1(ctx, plan)
}

// SwissCategoryRevisionParams is the narrow mapping bridge used by the
// unmoved playoff materializer.
func SwissCategoryRevisionParams(
	revision draftusecase.CategoryRevision,
	lock draftusecase.CategoryLock,
	normalPoolID uuid.UUID,
) (sqlc.CreateSwissCategoryRevisionParams, error) {
	return swissCategoryRevisionParams(revision, lock, normalPoolID)
}

// materializeSwissRandomBO1 persists the executable graph for every paired
// Series in one outer transaction. The workflow intentionally leaves admin
// and draft category modes to their dedicated materializers.
func (r *TournamentAdminExecutionPostgres) materializeSwissRandomBO1(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) error {
	if !validSwissRandomMaterializationPlan(plan) {
		return domain.ErrValidation
	}
	content, err := loadTournamentPreflightContent(ctx, r.tx.Querier(ctx), plan.Command.TournamentID)
	if err != nil {
		return fmt.Errorf("load Swiss materialization content: %w", err)
	}
	if content.configuration.Validate() != nil || content.configuration.NormalPool.ID == uuid.Nil {
		return domain.ErrInvalidContentConfiguration
	}
	for index, pair := range plan.Pairs {
		if err := r.materializeSwissRandomSeries(
			ctx, plan, pair, plan.SeriesIDs[index], content.configuration,
		); err != nil {
			return fmt.Errorf("materialize Swiss Series %d: %w", index+1, err)
		}
	}
	return nil
}

// materializeSwissAdminBO1 persists the executable graph after the operator's
// category selection has been locked with actor and reason evidence.
func (r *TournamentAdminExecutionPostgres) materializeSwissAdminBO1(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) error {
	if !validSwissAdminMaterializationPlan(plan) {
		return domain.ErrValidation
	}
	content, err := loadTournamentPreflightContent(ctx, r.tx.Querier(ctx), plan.Command.TournamentID)
	if err != nil {
		return fmt.Errorf("load Swiss admin materialization content: %w", err)
	}
	if content.configuration.Validate() != nil || content.configuration.NormalPool.ID == uuid.Nil {
		return domain.ErrInvalidContentConfiguration
	}
	for index, pair := range plan.Pairs {
		if err := r.materializeSwissAdminSeries(
			ctx, plan, pair, plan.SeriesIDs[index], content.configuration,
		); err != nil {
			return fmt.Errorf("materialize Swiss admin Series %d: %w", index+1, err)
		}
	}
	return nil
}

func validSwissRandomMaterializationPlan(plan tournamentadmin.PairingPlan) bool {
	if plan.Command.CategoryMode != domain.CategoryModeRandom || plan.Command.TournamentID == uuid.Nil ||
		plan.Authority.RosterID == uuid.Nil || !validServerTime(plan.DecidedAt) || len(plan.Pairs) == 0 ||
		len(plan.SeriesIDs) != len(plan.Pairs) {
		return false
	}
	return validSwissMaterializationPairs(plan)
}

func validSwissMaterializationPairs(plan tournamentadmin.PairingPlan) bool {
	seenSeries := make(map[uuid.UUID]struct{}, len(plan.SeriesIDs))
	for index, pair := range plan.Pairs {
		if pair.FirstParticipantID == uuid.Nil || pair.SecondParticipantID == uuid.Nil ||
			pair.FirstParticipantID == pair.SecondParticipantID || plan.SeriesIDs[index] == uuid.Nil {
			return false
		}
		if _, duplicate := seenSeries[plan.SeriesIDs[index]]; duplicate {
			return false
		}
		seenSeries[plan.SeriesIDs[index]] = struct{}{}
	}
	return true
}

func validSwissAdminMaterializationPlan(plan tournamentadmin.PairingPlan) bool {
	if plan.Command.CategoryMode != domain.CategoryModeAdmin || plan.Command.TournamentID == uuid.Nil ||
		plan.Command.Operator.ActorID == uuid.Nil || len(plan.Command.Categories) != 1 ||
		!plan.Command.Categories[0].IsValid() || plan.Authority.RosterID == uuid.Nil ||
		!validServerTime(plan.DecidedAt) || len(plan.Pairs) == 0 ||
		len(plan.SeriesIDs) != len(plan.Pairs) {
		return false
	}
	return validSwissMaterializationPairs(plan)
}

func (r *TournamentAdminExecutionPostgres) materializeSwissRandomSeries(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
	pair swissusecase.Pair,
	seriesID uuid.UUID,
	configuration domain.ContentConfiguration,
) error {
	return r.materializeSwissRandomSeriesConcrete(
		ctx, plan, pair.FirstParticipantID, pair.SecondParticipantID, seriesID, configuration,
	)
}

func (r *TournamentAdminExecutionPostgres) materializeSwissAdminSeries(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
	pair swissusecase.Pair,
	seriesID uuid.UUID,
	configuration domain.ContentConfiguration,
) error {
	createdAt := plan.DecidedAt.Round(0).UTC()
	mode := domain.CategoryModeAdmin
	revisionID := tournamentAdminExecutionID(seriesID, "swiss-category-revision")
	revision, changed, err := draftusecase.DeriveCategoryRevision(nil, draftusecase.CategoryRevisionCommand{
		ID: revisionID, SeriesID: seriesID, RosterID: plan.Authority.RosterID,
		SeriesState: domain.SeriesStatePlanned, Stage: domain.TournamentStageSwiss,
		Configuration: configuration, ModeOverride: &mode, CategoryLocked: false, CreatedAt: createdAt,
	})
	if err != nil {
		return fmt.Errorf("derive admin category revision: %w", err)
	}
	if !changed {
		return fmt.Errorf("derive Swiss admin category revision: %w", domain.ErrInternal)
	}
	selected := plan.Command.Categories[0]
	lock, changed, err := draftusecase.LockAdmin(nil, revision, draftusecase.AdminSelectionCommand{
		LockID: revision.ID, ActorID: plan.Command.Operator.ActorID,
		ExpectedCategoryRevisionID: revision.ID, ExpectedCategoryRevision: revision.Revision,
		SelectedCategory: &selected, Reason: "Swiss pairing category selection", LockedAt: createdAt,
	})
	if err != nil {
		if errors.Is(err, draftusecase.ErrInvalidAdminSelection) {
			return fmt.Errorf("lock admin category: %w", domain.ErrValidation)
		}
		return fmt.Errorf("lock admin category: %w", err)
	}
	if !changed || len(lock.SelectedCategories) != 1 || lock.SelectedCategories[0] != selected {
		return fmt.Errorf("validate Swiss admin category lock: %w", domain.ErrInternal)
	}
	return r.materializeSwissLockedSeriesConcrete(
		ctx, plan, pair.FirstParticipantID, pair.SecondParticipantID, seriesID, configuration, revision, lock,
	)
}

func swissCategoryRevisionParams(
	revision draftusecase.CategoryRevision,
	lock draftusecase.CategoryLock,
	normalPoolID uuid.UUID,
) (sqlc.CreateSwissCategoryRevisionParams, error) {
	if normalPoolID == uuid.Nil || revision.Validate() != nil || lock.Validate(revision) != nil ||
		lock.Mode != revision.Mode || (lock.Mode == domain.CategoryModeRandom && lock.DecisionEvidence == nil) ||
		(lock.Mode == domain.CategoryModeAdmin && (lock.SelectorActorID == nil || lock.SelectionReason == "")) {
		return sqlc.CreateSwissCategoryRevisionParams{}, domain.ErrValidation
	}
	categoryPool, err := categoryJSON(revision.CategoryPool.Categories)
	if err != nil {
		return sqlc.CreateSwissCategoryRevisionParams{}, err
	}
	selectedCategories, err := categoryJSON(lock.SelectedCategories)
	if err != nil {
		return sqlc.CreateSwissCategoryRevisionParams{}, err
	}
	params := sqlc.CreateSwissCategoryRevisionParams{
		ID: revision.ID, SeriesID: revision.SeriesID, RosterID: revision.RosterID,
		Revision: revision.Revision, SourcePoolRevisionID: normalPoolID,
		Mode: string(revision.Mode), CategoryPool: categoryPool, SelectedCategories: selectedCategories,
		CreatedAt: tstz(revision.CreatedAt),
	}
	if lock.SelectorActorID != nil {
		params.SelectorActorID = uuid.NullUUID{UUID: *lock.SelectorActorID, Valid: true}
	}
	if lock.SelectionReason != "" {
		reason := lock.SelectionReason
		params.SelectionReason = &reason
	}
	if lock.DecisionEvidence == nil {
		params.DecidedAt = tstz(lock.LockedAt)
		return params, nil
	}
	return swissRandomCategoryEvidenceParams(params, *lock.DecisionEvidence)
}

func swissRandomCategoryEvidenceParams(params sqlc.CreateSwissCategoryRevisionParams, evidence domain.DecisionEvidence) (sqlc.CreateSwissCategoryRevisionParams, error) {
	decisionInputs, err := marshalJSON("Swiss category decision inputs", evidence.NormalizedInputs)
	if err != nil {
		return sqlc.CreateSwissCategoryRevisionParams{}, err
	}
	decisionResult, err := marshalJSON("Swiss category decision result", evidence.Result)
	if err != nil {
		return sqlc.CreateSwissCategoryRevisionParams{}, err
	}
	params.DecisionEvidenceID = uuid.NullUUID{UUID: evidence.ID, Valid: true}
	params.DecisionAlgorithmVersion = &evidence.AlgorithmVersion
	params.DecisionInputs = decisionInputs
	params.DecisionSeed = append([]byte(nil), evidence.Seed[:]...)
	params.DecisionResult = decisionResult
	params.DecisionReplayDigest = append([]byte(nil), evidence.ReplayDigest[:]...)
	params.DecisionOwnerID = uuid.NullUUID{UUID: evidence.OwnerID, Valid: true}
	params.DecidedAt = tstz(evidence.DecidedAt)
	return params, nil
}

func (r *TournamentAdminExecutionPostgres) persistSwissCategoryRevision(
	ctx context.Context,
	revision draftusecase.CategoryRevision,
	lock draftusecase.CategoryLock,
	normalPoolID uuid.UUID,
) error {
	params, err := swissCategoryRevisionParams(revision, lock, normalPoolID)
	if err != nil {
		return err
	}
	created, err := r.tx.Querier(ctx).CreateSwissCategoryRevision(ctx, params)
	if err != nil {
		return executionWriteError("create Swiss category revision", err)
	}
	if created.ID != revision.ID || created.SeriesID != revision.SeriesID ||
		created.RosterID != revision.RosterID || created.Revision != revision.Revision ||
		created.Mode != string(revision.Mode) {
		return fmt.Errorf("validate persisted Swiss category revision: %w", domain.ErrInternal)
	}
	return nil
}

func createMaterializedSeriesPresence(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID, rosterID, seriesID uuid.UUID,
	participantIDs [2]uuid.UUID,
	connectedAt time.Time,
) error {
	for index, participantID := range participantIDs {
		presenceID := tournamentAdminExecutionID(seriesID, fmt.Sprintf("participant-%d-presence", index+1))
		created, err := querier.CreateSeriesPresence(ctx, sqlc.CreateSeriesPresenceParams{
			ID: presenceID, TournamentID: tournamentID, RosterID: rosterID,
			SeriesID: seriesID, ParticipantID: participantID, ConnectedAt: tstz(connectedAt),
		})
		if err != nil {
			return executionWriteError("create Series participant presence", err)
		}
		if created.ID != presenceID || created.TournamentID != tournamentID || created.RosterID != rosterID ||
			created.SeriesID != seriesID || created.ParticipantID != participantID || created.State != "connected" ||
			created.PresenceEpoch != 1 || created.Revision != 1 || created.DisconnectedAt.Valid {
			return fmt.Errorf("validate Series participant presence: %w", domain.ErrInternal)
		}
	}
	return nil
}

func (r *TournamentAdminExecutionPostgres) materializeSwissRandomSeriesConcrete(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
	seriesID uuid.UUID,
	configuration domain.ContentConfiguration,
) error {
	createdAt := plan.DecidedAt.Round(0).UTC()
	mode := domain.CategoryModeRandom
	revisionID := tournamentAdminExecutionID(seriesID, "swiss-category-revision")
	revision, changed, err := draftusecase.DeriveCategoryRevision(nil, draftusecase.CategoryRevisionCommand{
		ID: revisionID, SeriesID: seriesID, RosterID: plan.Authority.RosterID,
		SeriesState: domain.SeriesStatePlanned, Stage: domain.TournamentStageSwiss,
		Configuration: configuration, ModeOverride: &mode, CategoryLocked: false, CreatedAt: createdAt,
	})
	if err != nil {
		return fmt.Errorf("derive category revision: %w", err)
	}
	if !changed {
		return fmt.Errorf("derive Swiss category revision: %w", domain.ErrInternal)
	}
	decisionID := tournamentAdminExecutionID(revision.ID, "swiss-category-decision")
	lock, changed, err := draftusecase.LockRandom(nil, revision, draftusecase.RandomSelectionCommand{
		LockID: revision.ID, EvidenceID: decisionID, LockedAt: createdAt,
	})
	if err != nil {
		return fmt.Errorf("lock random category: %w", err)
	}
	if !changed || len(lock.SelectedCategories) != 1 {
		return fmt.Errorf("validate Swiss category lock: %w", domain.ErrInternal)
	}
	return r.materializeSwissLockedSeriesConcrete(
		ctx, plan, firstParticipantID, secondParticipantID, seriesID, configuration, revision, lock,
	)
}

//nolint:gocyclo // Swiss Series materialization persists the executable graph atomically with its exact assignment.
func (r *TournamentAdminExecutionPostgres) materializeSwissLockedSeriesConcrete(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
	seriesID uuid.UUID,
	configuration domain.ContentConfiguration,
	revision draftusecase.CategoryRevision,
	lock draftusecase.CategoryLock,
) error {
	createdAt := plan.DecidedAt.Round(0).UTC()
	readyAt := createdAt.Add(time.Microsecond)
	if err := r.persistSwissCategoryRevision(ctx, revision, lock, configuration.NormalPool.ID); err != nil {
		return err
	}
	locked, err := r.tx.Querier(ctx).LockSwissSeriesForMaterialization(ctx, sqlc.LockSwissSeriesForMaterializationParams{
		SeriesID: seriesID, TournamentID: plan.Command.TournamentID, RosterID: plan.Authority.RosterID,
		UpdatedAt: tstz(createdAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return executionWriteError("lock Swiss Series", err)
	}
	if locked.ID != seriesID || locked.State != string(domain.SeriesStateLocked) {
		return fmt.Errorf("validate locked Swiss Series: %w", domain.ErrInternal)
	}

	slotID := tournamentAdminExecutionID(seriesID, "swiss-game-slot-1")
	category := lock.SelectedCategories[0]
	if _, err := r.tx.Querier(ctx).CreateGameSlot(ctx, sqlc.CreateGameSlotParams{
		ID: slotID, SeriesID: seriesID, RosterID: plan.Authority.RosterID,
		SlotNumber: 1, Category: string(category), CreatedAt: tstz(createdAt),
	}); err != nil {
		return executionWriteError("create Swiss Game slot", err)
	}

	planID := tournamentAdminExecutionID(seriesID, "swiss-exact-normal-plan")
	var edgeIDs [domain.AssignmentReserveCount + 1]uuid.UUID
	var reservationIDs [domain.AssignmentReserveCount + 1]uuid.UUID
	var snapshotIDs [domain.AssignmentReserveCount + 1]uuid.UUID
	for index := range edgeIDs {
		edgeIDs[index] = tournamentAdminExecutionID(planID, fmt.Sprintf("swiss-exact-normal-edge-%d", index+1))
		reservationIDs[index] = tournamentAdminExecutionID(planID, fmt.Sprintf("swiss-exact-normal-reservation-%d", index+1))
		snapshotIDs[index] = tournamentAdminExecutionID(planID, fmt.Sprintf("swiss-exact-normal-snapshot-%d", index+1))
	}
	exactCommand := assignmentusecase.ExactNormalAssignmentCommand{
		Scope: assignmentusecase.ExactNormalAssignmentScope{
			TournamentID: plan.Command.TournamentID, RosterID: plan.Authority.RosterID,
			SeriesID: seriesID, SlotID: slotID, CategoryLockID: revision.ID,
		},
		PlanID: planID, PlanRevisionID: tournamentAdminExecutionID(planID, "swiss-exact-normal-plan-revision"),
		BranchID:           tournamentAdminExecutionID(planID, "swiss-exact-normal-branch"),
		DecisionEvidenceID: tournamentAdminExecutionID(planID, "swiss-exact-normal-decision"),
		EdgeIDs:            edgeIDs, ReservationIDs: reservationIDs, SnapshotIDs: snapshotIDs, CreatedAt: createdAt,
	}
	exactRepository := assignmentrepo.NewExactNormalAssignmentPostgres(r.tx)
	exactUseCase := assignmentusecase.NewExactNormalAssignmentUseCase(exactRepository)
	exactPlan, _, err := exactUseCase.PlanAndCommit(ctx, exactCommand)
	if err != nil {
		return fmt.Errorf("commit exact normal assignment: %w", err)
	}
	if exactPlan == nil {
		return fmt.Errorf("validate exact normal plan: %w", domain.ErrInternal)
	}

	logicalSeries := domain.Series{
		ID: seriesID, TournamentID: plan.Command.TournamentID,
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: domain.SeriesFormatBO1, State: domain.SeriesStatePlanned,
	}
	graph, changed, err := seriesgraph.Materialize(nil, seriesgraph.MaterializeInput{
		CommandID: plan.Command.CommandID, DeliveredAt: createdAt, Series: logicalSeries,
		CategoryRevision: revision, SelectedCategories: append([]domain.Category(nil), lock.SelectedCategories...),
		AssignmentPlans: []assignmentusecase.ExactNormalAssignmentPlan{*exactPlan},
	})
	if err != nil {
		return fmt.Errorf("materialize Swiss Series graph: %w", err)
	}
	if !changed || len(graph.Series.Slots) != 1 || len(graph.Assignments) != 1 {
		return fmt.Errorf("validate Swiss Series graph: %w", domain.ErrInternal)
	}
	slot := graph.Series.Slots[0]
	if len(slot.Attempts) != 1 {
		return fmt.Errorf("validate Swiss Game attempt: %w", domain.ErrInternal)
	}
	attempt := slot.Attempts[0]
	if _, err := r.tx.Querier(ctx).CreateGameAttempt(ctx, sqlc.CreateGameAttemptParams{
		ID: attempt.ID, SlotID: slot.ID, SeriesID: seriesID, RosterID: plan.Authority.RosterID,
		AttemptNumber: 1, State: string(domain.GameStatePlanned), CreatedAt: tstz(createdAt),
	}); err != nil {
		return executionWriteError("create Swiss Game attempt", err)
	}
	aggregate := graph.Assignments[0]
	primary := aggregate.Plan.SelectedEdges[0]
	assignments := assignmentrepo.NewAssignmentPostgres(r.tx)
	if err := assignments.CreateAssignmentTx(ctx, assignmentrepo.AssignmentCreateInput{
		ID: aggregate.ID, AttemptID: aggregate.AttemptID, SeriesID: seriesID,
		RosterID: plan.Authority.RosterID, PlanID: aggregate.Plan.PlanID, BranchID: aggregate.Plan.BranchID,
		ReservationID: primary.ReservationID, SnapshotID: primary.Snapshot.SnapshotID, CreatedAt: createdAt,
	}); err != nil {
		return fmt.Errorf("create Swiss assignment: %w", err)
	}
	if err := createMaterializedSeriesPresence(
		ctx, r.tx.Querier(ctx), plan.Command.TournamentID, plan.Authority.RosterID, seriesID,
		[2]uuid.UUID{firstParticipantID, secondParticipantID}, createdAt,
	); err != nil {
		return err
	}
	readyGame, err := r.tx.Querier(ctx).ReadySwissGameForMaterialization(ctx, sqlc.ReadySwissGameForMaterializationParams{
		GameID: attempt.ID, SlotID: slot.ID, SeriesID: seriesID,
		RosterID: plan.Authority.RosterID, UpdatedAt: tstz(readyAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return executionWriteError("ready Swiss Game", err)
	}
	if readyGame.ID != attempt.ID || readyGame.State != string(domain.GameStateReady) {
		return fmt.Errorf("validate ready Swiss Game: %w", domain.ErrInternal)
	}
	readySeries, err := r.tx.Querier(ctx).ReadySwissSeriesForMaterialization(ctx, sqlc.ReadySwissSeriesForMaterializationParams{
		SeriesID: seriesID, TournamentID: plan.Command.TournamentID, RosterID: plan.Authority.RosterID,
		UpdatedAt: tstz(readyAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return executionWriteError("ready Swiss Series", err)
	}
	if readySeries.ID != seriesID || readySeries.State != string(domain.SeriesStateReady) {
		return fmt.Errorf("validate ready Swiss Series: %w", domain.ErrInternal)
	}
	return nil
}
