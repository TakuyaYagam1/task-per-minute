package progression

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/seriesgraph"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

// materializePlayoffSemifinals completes the two locked bracket Series in the
// same transaction as their projection publication. Stage evidence is bound
// before exact-normal planning because that planner intentionally locks the
// published bracket as its source of truth.
//
//nolint:gocyclo // Both semifinal graphs must be derived and persisted against one locked playoff publication.
func (r *TournamentProgressionPostgres) materializePlayoffSemifinals(
	ctx context.Context,
	plan tournamentprogression.Plan,
	publication tournamentprogression.PlayoffPublication,
	now time.Time,
) error {
	if r == nil || r.tx == nil || ctx == nil || plan.Bracket == nil || plan.Top4 == nil ||
		!plan.PublicationIDs.Valid() || !domain.IsValidServerTime(now) {
		return domain.ErrValidation
	}
	matches := plan.Bracket.Semifinals()
	if len(matches) != 2 || publication.SemifinalSeriesIDs != [2]uuid.UUID{matches[0].Series.ID, matches[1].Series.ID} {
		return domain.ErrConflict
	}
	command := plan.Record.Command
	querier := r.tx.Querier(ctx)
	if err := ensurePlayoffSemifinalStageEvidence(ctx, querier, plan, publication, now); err != nil {
		return err
	}

	content, err := loadTournamentPreflightContent(ctx, querier, command.TournamentID)
	if err != nil {
		return fmt.Errorf("load playoff semifinal content: %w", err)
	}
	if content.configuration.Validate() != nil || content.configuration.NormalPool.ID == uuid.Nil {
		return domain.ErrInvalidContentConfiguration
	}

	var inputs [2]seriesgraph.MaterializeInput
	for index, match := range matches {
		input, err := r.preparePlayoffSemifinalMaterialization(
			ctx, plan, content.configuration, match, index, now,
		)
		if err != nil {
			return fmt.Errorf("prepare playoff semifinal %d: %w", index+1, err)
		}
		inputs[index] = input
	}
	authority := playoff.SemifinalAdvancementAuthority{
		TournamentID:      command.TournamentID,
		BracketRevisionID: plan.Bracket.Projection().Revision().ID(),
		Semifinals:        matches,
	}
	flow, changed, err := playoff.MaterializeSemifinals(nil, playoff.SemifinalFlowInput{
		Authority:   authority,
		RosterID:    command.RosterID,
		Materialize: inputs,
	})
	if err != nil {
		return fmt.Errorf("materialize playoff semifinal graphs: %w", err)
	}
	if !changed || flow.Validate() != nil {
		return domain.ErrInternal
	}
	for index := range flow.Graphs {
		if err := r.persistPlayoffSemifinalGraph(ctx, querier, plan, flow.Graphs[index], matches[index], index, now); err != nil {
			return fmt.Errorf("persist playoff semifinal %d graph: %w", index+1, err)
		}
	}
	return nil
}

func ensurePlayoffSemifinalStageEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	plan tournamentprogression.Plan,
	publication tournamentprogression.PlayoffPublication,
	now time.Time,
) error {
	command, ids := plan.Record.Command, plan.PublicationIDs
	id, err := querier.CreateTournamentStagePlayoffEvidence(ctx, sqlc.CreateTournamentStagePlayoffEvidenceParams{
		CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: command.RosterID,
		SourceProjectionRevisionID: plan.Record.Source.RevisionID, SourceProjectionRevision: plan.Record.Source.Revision,
		PublishedProjectionRevisionID: publication.PublishedProjectionID, PublishedProjectionRevision: publication.PublishedRevision,
		Top4ArtifactID: publication.Top4ArtifactID, BracketArtifactID: publication.BracketArtifactID,
		Top4NodeID: ids.Top4NodeID, BracketNodeID: ids.BracketNodeID,
		FirstSemifinalSeriesID: publication.SemifinalSeriesIDs[0], SecondSemifinalSeriesID: publication.SemifinalSeriesIDs[1],
		ProofDigest: plan.Record.ProofDigest[:], CreatedAt: tstz(now),
	})
	if err := progressionWrittenID("create playoff evidence", command.CommandID, id, err); err != nil {
		return err
	}
	for _, match := range plan.Bracket.Semifinals() {
		position, err := progressionInt16(match.Position)
		if err != nil {
			return err
		}
		id, writeErr := querier.CreateTournamentStagePlayoffSemifinal(ctx, sqlc.CreateTournamentStagePlayoffSemifinalParams{
			CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: command.RosterID,
			BracketArtifactID: publication.BracketArtifactID, Position: position, SeriesID: match.Series.ID, CreatedAt: tstz(now),
		})
		if err := progressionWrittenID("bind semifinal", match.Series.ID, id, writeErr); err != nil {
			return err
		}
	}
	return nil
}

//nolint:gocyclo // Preparation validates and binds the complete category, slot, and exact-assignment authority.
func (r *TournamentProgressionPostgres) preparePlayoffSemifinalMaterialization(
	ctx context.Context,
	plan tournamentprogression.Plan,
	configuration domain.ContentConfiguration,
	match playoff.SemifinalMatch,
	index int,
	createdAt time.Time,
) (seriesgraph.MaterializeInput, error) {
	command := plan.Record.Command
	querier := r.tx.Querier(ctx)
	stored, err := querier.LockPlayoffSemifinalSeriesForMaterialization(ctx, sqlc.LockPlayoffSemifinalSeriesForMaterializationParams{
		SeriesID: match.Series.ID, TournamentID: command.TournamentID, RosterID: command.RosterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return seriesgraph.MaterializeInput{}, domain.ErrConflict
	}
	if err != nil {
		return seriesgraph.MaterializeInput{}, err
	}
	if stored.ID != match.Series.ID || stored.TournamentID != command.TournamentID || stored.RosterID != command.RosterID ||
		stored.FirstParticipantID != match.Series.FirstParticipantID || stored.SecondParticipantID != match.Series.SecondParticipantID ||
		stored.Format != string(domain.SeriesFormatBO1) || stored.State != string(domain.SeriesStateLocked) ||
		!stored.CurrentScoreRevisionID.Valid || stored.CurrentResultRevisionID.Valid || stored.StartedAt.Valid || stored.FinishedAt.Valid {
		return seriesgraph.MaterializeInput{}, domain.ErrConflict
	}

	categoryMode := domain.CategoryModeRandom
	revision, changed, err := draftusecase.DeriveCategoryRevision(nil, draftusecase.CategoryRevisionCommand{
		ID:       tournamentAdminExecutionID(command.CommandID, fmt.Sprintf("playoff-semifinal-%d-category-revision", index+1)),
		SeriesID: match.Series.ID, RosterID: command.RosterID, SeriesState: domain.SeriesStatePlanned,
		Stage: domain.TournamentStageSemifinal, Configuration: configuration, ModeOverride: &categoryMode,
		CategoryLocked: false, CreatedAt: createdAt,
	})
	if err != nil {
		return seriesgraph.MaterializeInput{}, fmt.Errorf("derive category revision: %w", err)
	}
	if !changed {
		return seriesgraph.MaterializeInput{}, domain.ErrInternal
	}
	lock, changed, err := draftusecase.LockRandom(nil, revision, draftusecase.RandomSelectionCommand{
		LockID: revision.ID, EvidenceID: tournamentAdminExecutionID(revision.ID, "playoff-semifinal-category-decision"), LockedAt: createdAt,
	})
	if err != nil {
		return seriesgraph.MaterializeInput{}, fmt.Errorf("lock random category: %w", err)
	}
	if !changed || len(lock.SelectedCategories) != 1 {
		return seriesgraph.MaterializeInput{}, domain.ErrInternal
	}
	params, err := playoffSemifinalCategoryRevisionParams(revision, lock, configuration.NormalPool.ID)
	if err != nil {
		return seriesgraph.MaterializeInput{}, err
	}
	created, err := querier.CreatePlayoffSemifinalCategoryRevision(ctx, params)
	if err != nil {
		return seriesgraph.MaterializeInput{}, executionWriteError("create playoff semifinal category revision", err)
	}
	if created.ID != revision.ID || created.SeriesID != revision.SeriesID || created.RosterID != revision.RosterID ||
		created.Revision != revision.Revision || created.Mode != string(revision.Mode) {
		return seriesgraph.MaterializeInput{}, domain.ErrInternal
	}

	slotID := tournamentAdminExecutionID(match.Series.ID, fmt.Sprintf("playoff-semifinal-%d-game-slot-1", index+1))
	if _, err := querier.CreateGameSlot(ctx, sqlc.CreateGameSlotParams{
		ID: slotID, SeriesID: match.Series.ID, RosterID: command.RosterID, SlotNumber: 1,
		Category: string(lock.SelectedCategories[0]), CreatedAt: tstz(createdAt),
	}); err != nil {
		return seriesgraph.MaterializeInput{}, executionWriteError("create playoff semifinal game slot", err)
	}

	planID := tournamentAdminExecutionID(match.Series.ID, fmt.Sprintf("playoff-semifinal-%d-exact-normal-plan", index+1))
	exactCommand := playoffSemifinalExactNormalCommand(command, match, revision.ID, slotID, planID, createdAt, index)
	exactRepository := NewExactNormalAssignmentPostgres(r.tx)
	exactUseCase := assignmentusecase.NewExactNormalAssignmentUseCase(exactRepository)
	exactPlan, _, err := exactUseCase.PlanAndCommit(ctx, exactCommand)
	if err != nil {
		return seriesgraph.MaterializeInput{}, fmt.Errorf("commit exact normal assignment: %w", err)
	}
	if exactPlan == nil {
		return seriesgraph.MaterializeInput{}, domain.ErrInternal
	}
	return seriesgraph.MaterializeInput{
		CommandID:   tournamentAdminExecutionID(command.CommandID, fmt.Sprintf("playoff-semifinal-%d-graph", index+1)),
		DeliveredAt: createdAt, Series: match.Series, CategoryRevision: revision,
		SelectedCategories: append([]domain.Category(nil), lock.SelectedCategories...),
		AssignmentPlans:    []assignmentusecase.ExactNormalAssignmentPlan{*exactPlan},
	}, nil
}

func playoffSemifinalCategoryRevisionParams(
	revision draftusecase.CategoryRevision,
	lock draftusecase.CategoryLock,
	normalPoolID uuid.UUID,
) (sqlc.CreatePlayoffSemifinalCategoryRevisionParams, error) {
	swissParams, err := swissCategoryRevisionParams(revision, lock, normalPoolID)
	if err != nil {
		return sqlc.CreatePlayoffSemifinalCategoryRevisionParams{}, err
	}
	return sqlc.CreatePlayoffSemifinalCategoryRevisionParams{
		ID: revision.ID, SeriesID: revision.SeriesID, RosterID: revision.RosterID,
		Revision: revision.Revision, SourcePoolRevisionID: swissParams.SourcePoolRevisionID,
		Mode: swissParams.Mode, CategoryPool: swissParams.CategoryPool, SelectedCategories: swissParams.SelectedCategories,
		SelectorActorID: swissParams.SelectorActorID, SelectionReason: swissParams.SelectionReason,
		DecisionEvidenceID: swissParams.DecisionEvidenceID, DecisionAlgorithmVersion: swissParams.DecisionAlgorithmVersion,
		DecisionInputs: swissParams.DecisionInputs, DecisionSeed: swissParams.DecisionSeed,
		DecisionResult: swissParams.DecisionResult, DecisionReplayDigest: swissParams.DecisionReplayDigest,
		DecisionOwnerID: swissParams.DecisionOwnerID, DecidedAt: swissParams.DecidedAt, CreatedAt: swissParams.CreatedAt,
	}, nil
}

func playoffSemifinalExactNormalCommand(
	command tournamentprogression.Command,
	match playoff.SemifinalMatch,
	categoryRevisionID, slotID, planID uuid.UUID,
	createdAt time.Time,
	index int,
) assignmentusecase.ExactNormalAssignmentCommand {
	var edgeIDs [domain.AssignmentReserveCount + 1]uuid.UUID
	var reservationIDs [domain.AssignmentReserveCount + 1]uuid.UUID
	var snapshotIDs [domain.AssignmentReserveCount + 1]uuid.UUID
	for position := range edgeIDs {
		edgeIDs[position] = tournamentAdminExecutionID(planID, fmt.Sprintf("playoff-semifinal-%d-exact-normal-edge-%d", index+1, position+1))
		reservationIDs[position] = tournamentAdminExecutionID(planID, fmt.Sprintf("playoff-semifinal-%d-exact-normal-reservation-%d", index+1, position+1))
		snapshotIDs[position] = tournamentAdminExecutionID(planID, fmt.Sprintf("playoff-semifinal-%d-exact-normal-snapshot-%d", index+1, position+1))
	}
	return assignmentusecase.ExactNormalAssignmentCommand{
		Scope: assignmentusecase.ExactNormalAssignmentScope{
			TournamentID: command.TournamentID, RosterID: command.RosterID, SeriesID: match.Series.ID,
			SlotID: slotID, CategoryLockID: categoryRevisionID,
		},
		PlanID: planID, PlanRevisionID: tournamentAdminExecutionID(planID, fmt.Sprintf("playoff-semifinal-%d-exact-normal-plan-revision", index+1)),
		BranchID:           tournamentAdminExecutionID(planID, fmt.Sprintf("playoff-semifinal-%d-exact-normal-branch", index+1)),
		DecisionEvidenceID: tournamentAdminExecutionID(planID, fmt.Sprintf("playoff-semifinal-%d-exact-normal-decision", index+1)),
		EdgeIDs:            edgeIDs, ReservationIDs: reservationIDs, SnapshotIDs: snapshotIDs, CreatedAt: createdAt,
	}
}

//nolint:gocyclo // The executable semifinal graph is one atomic relational write boundary.
func (r *TournamentProgressionPostgres) persistPlayoffSemifinalGraph(
	ctx context.Context,
	querier *sqlc.Queries,
	plan tournamentprogression.Plan,
	graph seriesgraph.SeriesGraph,
	match playoff.SemifinalMatch,
	index int,
	createdAt time.Time,
) error {
	if err := graph.Validate(); err != nil || len(graph.Series.Slots) != 1 || len(graph.Assignments) != 1 ||
		graph.Series.ID != match.Series.ID || graph.Series.Format != domain.SeriesFormatBO1 {
		return domain.ErrConflict
	}
	if len(graph.SelectedCategories) != 1 {
		return domain.ErrConflict
	}
	slot := graph.Series.Slots[0]
	if len(slot.Attempts) != 1 || slot.SeriesID != match.Series.ID || slot.Category != graph.SelectedCategories[0] {
		return domain.ErrConflict
	}
	attempt := slot.Attempts[0]
	if attempt.State != domain.GameStatePlanned || attempt.SlotID != slot.ID {
		return domain.ErrConflict
	}
	readyAt, err := playoffSemifinalReadyAt(createdAt)
	if err != nil {
		return err
	}
	command := plan.Record.Command
	waveID := tournamentAdminExecutionID(command.CommandID, fmt.Sprintf("playoff-semifinal-%d-wave", index+1))
	waveRevisionID := tournamentAdminExecutionID(command.CommandID, fmt.Sprintf("playoff-semifinal-%d-wave-revision", index+1))
	if _, err := querier.CreateGameAttempt(ctx, sqlc.CreateGameAttemptParams{
		ID: attempt.ID, SlotID: slot.ID, SeriesID: match.Series.ID, RosterID: command.RosterID,
		AttemptNumber: 1, State: string(domain.GameStatePlanned), CreatedAt: tstz(createdAt),
	}); err != nil {
		return executionWriteError("create playoff semifinal game attempt", err)
	}
	if err := querier.CreatePlayoffSemifinalWave(ctx, sqlc.CreatePlayoffSemifinalWaveParams{
		ID: waveID, TournamentID: command.TournamentID, RosterID: command.RosterID,
		RevisionID: waveRevisionID, CreatedAt: tstz(createdAt),
	}); err != nil {
		return executionWriteError("create playoff semifinal wave", err)
	}
	for _, participantID := range []uuid.UUID{match.Series.FirstParticipantID, match.Series.SecondParticipantID} {
		if err := querier.CreateWaveMember(ctx, sqlc.CreateWaveMemberParams{
			WaveID: waveID, RosterID: command.RosterID, ParticipantID: participantID, CreatedAt: tstz(createdAt),
		}); err != nil {
			return executionWriteError("create playoff semifinal wave member", err)
		}
		if _, err := querier.CreateWaveReadinessHead(ctx, sqlc.CreateWaveReadinessHeadParams{
			WaveID: waveID, RosterID: command.RosterID, ParticipantID: participantID, CreatedAt: tstz(createdAt),
		}); err != nil {
			return executionWriteError("create playoff semifinal readiness head", err)
		}
	}
	if err := querier.CreateWaveSeries(ctx, sqlc.CreateWaveSeriesParams{
		WaveID: waveID, TournamentID: command.TournamentID, RosterID: command.RosterID,
		SeriesID: match.Series.ID, CreatedAt: tstz(createdAt),
	}); err != nil {
		return executionWriteError("link playoff semifinal wave", err)
	}
	if len(graph.Assignments[0].Plan.SelectedEdges) != domain.AssignmentReserveCount+1 {
		return domain.ErrConflict
	}
	aggregate := graph.Assignments[0]
	primary := aggregate.Plan.SelectedEdges[0]
	assignments := NewAssignmentPostgres(r.tx)
	if err := assignments.CreateAssignmentTx(ctx, AssignmentCreateInput{
		ID: aggregate.ID, AttemptID: aggregate.AttemptID, SeriesID: match.Series.ID, RosterID: command.RosterID,
		PlanID: aggregate.Plan.PlanID, BranchID: aggregate.Plan.BranchID,
		ReservationID: primary.ReservationID, SnapshotID: primary.Snapshot.SnapshotID, CreatedAt: createdAt,
	}); err != nil {
		return fmt.Errorf("create playoff semifinal assignment: %w", err)
	}
	if err := createMaterializedSeriesPresence(
		ctx, querier, command.TournamentID, command.RosterID, match.Series.ID,
		[2]uuid.UUID{match.Series.FirstParticipantID, match.Series.SecondParticipantID}, createdAt,
	); err != nil {
		return err
	}
	readyGame, err := querier.ReadyPlayoffSemifinalGameForMaterialization(ctx, sqlc.ReadyPlayoffSemifinalGameForMaterializationParams{
		GameID: attempt.ID, SlotID: slot.ID, SeriesID: match.Series.ID, RosterID: command.RosterID, UpdatedAt: tstz(readyAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return executionWriteError("ready playoff semifinal game", err)
	}
	if readyGame.ID != attempt.ID || readyGame.State != string(domain.GameStateReady) {
		return domain.ErrInternal
	}
	readySeries, err := querier.ReadyPlayoffSemifinalSeriesForMaterialization(ctx, sqlc.ReadyPlayoffSemifinalSeriesForMaterializationParams{
		SeriesID: match.Series.ID, TournamentID: command.TournamentID, RosterID: command.RosterID, UpdatedAt: tstz(readyAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return executionWriteError("ready playoff semifinal series", err)
	}
	if readySeries.ID != match.Series.ID || readySeries.State != string(domain.SeriesStateReady) {
		return domain.ErrInternal
	}
	return nil
}

// playoffSemifinalReadyAt is one PostgreSQL timestamp tick after the
// materialization timestamp. PostgreSQL timestamptz stores microseconds, so
// the increment must be at least one microsecond for the revision guards on
// game_attempts and series to observe a strictly later update.
func playoffSemifinalReadyAt(createdAt time.Time) (time.Time, error) {
	if !domain.IsValidServerTime(createdAt) {
		return time.Time{}, domain.ErrValidation
	}
	readyAt := createdAt.Truncate(time.Microsecond).Add(time.Microsecond)
	if !domain.IsValidServerTime(readyAt) || !readyAt.After(createdAt.Truncate(time.Microsecond)) {
		return time.Time{}, domain.ErrValidation
	}
	return readyAt, nil
}
