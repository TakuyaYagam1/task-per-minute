package playoff

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func validFinalInitialPlan(plan playoff.FinalInitialPlan) bool {
	series := plan.Execution.Series
	return plan.StageCommandID != uuid.Nil && plan.RosterID != uuid.Nil && plan.ExpectedSeriesRevision >= 1 &&
		!plan.InitialScoreRevisionID.IsZero() && plan.Execution.Validate() == nil &&
		series.Format == domain.SeriesFormatBO3 && series.State == domain.SeriesStateActive &&
		len(series.Slots) == 1 && plan.CurrentWave.Validate() == nil &&
		plan.CurrentWave.State == domain.WaveStatePlanned && validFinalGameBinding(plan.Binding) &&
		plan.Binding.GameID == series.Slots[0].Attempts[0].ID && domain.IsValidServerTime(plan.ActivatedAt)
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validFinalContinuationPlan(plan playoff.FinalContinuationPlan) bool {
	return plan.StageCommandID != uuid.Nil && plan.RosterID != uuid.Nil && plan.SeriesID != uuid.Nil &&
		!plan.SourceScoreRevision.IsZero() && !plan.SourceResultRevision.IsZero() &&
		plan.Slot.Validate() == nil && len(plan.Slot.Attempts) == 1 &&
		plan.Slot.ID == plan.Next.SlotID && plan.Slot.Attempts[0].ID == plan.Next.GameID &&
		plan.Slot.Attempts[0].State == domain.GameStatePlanned && plan.Wave.Validate() == nil &&
		plan.Wave.State == domain.WaveStatePlanned && plan.Wave.ID == plan.Next.WaveID &&
		plan.Wave.RevisionID == plan.Next.WaveRevisionID && validFinalGameBinding(plan.Binding) &&
		plan.Binding.GameID == plan.Next.GameID && domain.IsValidServerTime(plan.CreatedAt)
}

func validFinalGameBinding(binding playoff.FinalGameBinding) bool {
	return binding.GameID != uuid.Nil && binding.AssignmentID != uuid.Nil && binding.AssignmentRevision >= 1 &&
		binding.PlanID != uuid.Nil && binding.PlanRevisionID != uuid.Nil && binding.BranchID != uuid.Nil &&
		binding.ReservationID != uuid.Nil && binding.SnapshotID != uuid.Nil && binding.DeadlineSeconds > 0 &&
		binding.ContentDigest != ([sha256.Size]byte{})
}

func (repository *PlayoffTerminalPostgres) lockFinalInitialization(
	ctx context.Context,
	stage sqlc.LockPostseasonFinalStageRow,
) (sqlc.TournamentStagePlayoffFinalInitialization, bool, error) {
	row, err := repository.tx.Querier(ctx).LockPostseasonFinalInitialization(ctx,
		sqlc.LockPostseasonFinalInitializationParams{CommandID: stage.CommandID, TournamentID: stage.TournamentID})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.TournamentStagePlayoffFinalInitialization{}, false, nil
	}
	if err != nil {
		return sqlc.TournamentStagePlayoffFinalInitialization{}, false, err
	}
	return row, true, nil
}

func finalInitializationMatchesPlan(
	row sqlc.TournamentStagePlayoffFinalInitialization,
	plan playoff.FinalInitialPlan,
	stage sqlc.LockPostseasonFinalStageRow,
) bool {
	series := plan.Execution.Series
	return row.CommandID == stage.CommandID && row.TournamentID == stage.TournamentID &&
		row.RosterID == plan.RosterID && row.FinalSeriesID == series.ID && row.DraftID == stage.DraftID &&
		row.CompletedDraftRevisionID == stage.CurrentDraftRevisionID &&
		row.InitialScoreRevisionID == plan.InitialScoreRevisionID.UUID() && row.FirstSlotID == series.Slots[0].ID &&
		row.FirstGameID == plan.Binding.GameID && row.FirstWaveID == plan.CurrentWave.ID &&
		row.FirstWaveRevisionID == plan.CurrentWave.RevisionID.UUID() && row.FirstAssignmentID == plan.Binding.AssignmentID
}

func (repository *PlayoffTerminalPostgres) validateFinalInitialDraft(
	ctx context.Context,
	stage sqlc.LockPostseasonFinalStageRow,
	plan playoff.FinalInitialPlan,
) error {
	draft, err := repository.finalDraftExecution(ctx, stage)
	if err != nil || draft.State != draftusecase.ExecutionStateCompleted ||
		stage.CurrentDraftRevisionID != draft.RevisionID || stage.CurrentDraftRevision < 1 ||
		plan.Execution.Series.ID != stage.FinalSeriesID || plan.InitialScoreRevisionID.IsZero() {
		if err != nil {
			return err
		}
		return domain.ErrConflict
	}
	return nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) createFinalGameGraph(
	ctx context.Context,
	querier *sqlc.Queries,
	stage sqlc.LockPostseasonFinalStageRow,
	series domain.Series,
	slot domain.GameSlot,
	wave domain.Wave,
	binding playoff.FinalGameBinding,
	createdAt time.Time,
	genesis bool,
) error {
	if slot.Validate() != nil || len(slot.Attempts) != 1 || wave.Validate() != nil ||
		wave.State != domain.WaveStatePlanned || !validFinalGameBinding(binding) ||
		series.ID != stage.FinalSeriesID || series.TournamentID != stage.TournamentID ||
		slot.SeriesID != series.ID || binding.GameID != slot.Attempts[0].ID ||
		wave.TournamentID != series.TournamentID || !finalWaveMatchesSeries(wave, series) ||
		!domain.IsValidServerTime(createdAt) {
		return fmt.Errorf("final game graph binding: %w", domain.ErrConflict)
	}
	if _, err := querier.CreateGameSlot(ctx, sqlc.CreateGameSlotParams{
		ID:       slot.ID,
		SeriesID: series.ID,
		RosterID: stage.RosterID,
		//nolint:gosec // Domain validation bounds this value before the storage conversion.
		SlotNumber: int16(slot.Position),
		Category:   string(slot.Category),
		//nolint:gosec // Domain validation bounds this value before the storage conversion.
		FirstParticipantWinsBefore: int16(slot.ScoreBefore.FirstParticipantWins),
		//nolint:gosec // Domain validation bounds this value before the storage conversion.
		SecondParticipantWinsBefore: int16(slot.ScoreBefore.SecondParticipantWins),
		CreatedAt:                   tstz(createdAt),
	}); err != nil {
		return err
	}
	if _, err := querier.CreateGameAttempt(ctx, sqlc.CreateGameAttemptParams{
		ID:            binding.GameID,
		SlotID:        slot.ID,
		SeriesID:      series.ID,
		RosterID:      stage.RosterID,
		AttemptNumber: 1,
		State:         string(domain.GameStatePlanned),
		CreatedAt:     tstz(createdAt),
	}); err != nil {
		return err
	}
	if err := querier.CreatePostseasonPlannedWave(ctx, sqlc.CreatePostseasonPlannedWaveParams{
		ID: wave.ID, TournamentID: stage.TournamentID, RosterID: stage.RosterID,
		RevisionID: wave.RevisionID.UUID(), CreatedAt: tstz(createdAt),
	}); err != nil {
		return err
	}
	for _, participantID := range []uuid.UUID{series.FirstParticipantID, series.SecondParticipantID} {
		if err := querier.CreateWaveMember(ctx, sqlc.CreateWaveMemberParams{
			WaveID: wave.ID, RosterID: stage.RosterID, ParticipantID: participantID, CreatedAt: tstz(createdAt),
		}); err != nil {
			return err
		}
		if _, err := querier.CreateWaveReadinessHead(ctx, sqlc.CreateWaveReadinessHeadParams{
			WaveID: wave.ID, RosterID: stage.RosterID, ParticipantID: participantID, CreatedAt: tstz(createdAt),
		}); err != nil {
			return err
		}
	}
	if err := querier.CreateWaveSeries(ctx, sqlc.CreateWaveSeriesParams{
		WaveID: wave.ID, TournamentID: stage.TournamentID, RosterID: stage.RosterID,
		SeriesID: series.ID, CreatedAt: tstz(createdAt),
	}); err != nil {
		return err
	}
	if genesis {
		if err := createMaterializedSeriesPresence(
			ctx, querier, stage.TournamentID, stage.RosterID, series.ID,
			[2]uuid.UUID{series.FirstParticipantID, series.SecondParticipantID}, createdAt,
		); err != nil {
			return err
		}
		if err := createWaveGenesisProjectionNode(ctx, querier, WaveCreateInput{
			ID: wave.ID, TournamentID: stage.TournamentID, RosterID: stage.RosterID,
			RevisionID: wave.RevisionID, CommandID: stage.CommandID,
			SourceProjectionRevisionID: stage.PublishedProjectionRevisionID,
			SourceProjectionRevision:   stage.PublishedProjectionRevision, CreatedAt: createdAt,
		}, WaveSeriesInput{
			ID: series.ID, FirstParticipantID: series.FirstParticipantID, SecondParticipantID: series.SecondParticipantID,
			Format: series.Format, InitialScoreRevisionID: *series.CurrentScoreRevisionID,
		}); err != nil {
			return err
		}
	}
	if err := repository.createAssignmentTx(ctx, AssignmentCreateInput{
		ID: binding.AssignmentID, AttemptID: binding.GameID, SeriesID: series.ID, RosterID: stage.RosterID,
		PlanID: binding.PlanID, BranchID: binding.BranchID, ReservationID: binding.ReservationID,
		SnapshotID: binding.SnapshotID, CreatedAt: createdAt,
	}); err != nil {
		return fmt.Errorf("final game assignment: %w", err)
	}
	return nil
}

func finalWaveMatchesSeries(wave domain.Wave, series domain.Series) bool {
	if len(wave.Members) != 2 {
		return false
	}
	participants := map[uuid.UUID]bool{
		series.FirstParticipantID:  false,
		series.SecondParticipantID: false,
	}
	for _, member := range wave.Members {
		if member.Ready {
			return false
		}
		seen, exists := participants[member.ParticipantID]
		if !exists || seen {
			return false
		}
		participants[member.ParticipantID] = true
	}
	return participants[series.FirstParticipantID] && participants[series.SecondParticipantID]
}

func finalProgressionMatchesPlan(
	row sqlc.TournamentStagePlayoffFinalProgression,
	plan playoff.FinalContinuationPlan,
	stage sqlc.LockPostseasonFinalStageRow,
) bool {
	return row.CommandID == stage.CommandID && row.TournamentID == stage.TournamentID &&
		row.RosterID == plan.RosterID && row.FinalSeriesID == plan.SeriesID &&
		row.SourceScoreRevisionID == plan.SourceScoreRevision.UUID() &&
		row.SourceGameResultRevisionID == plan.SourceResultRevision.UUID() &&
		//nolint:gosec // Domain validation bounds this value before the storage conversion.
		row.NextPosition == int16(plan.Slot.Position) && row.NextSlotID == plan.Next.SlotID &&
		row.NextGameID == plan.Next.GameID && row.NextWaveID == plan.Next.WaveID &&
		row.NextWaveRevisionID == plan.Next.WaveRevisionID.UUID() && row.NextAssignmentID == plan.Binding.AssignmentID
}

func (repository *PlayoffTerminalPostgres) validateContinuationSource(
	ctx context.Context,
	stage sqlc.LockPostseasonFinalStageRow,
	plan playoff.FinalContinuationPlan,
) error {
	rows, err := repository.tx.Querier(ctx).LockPostseasonFinalScoreHistory(ctx,
		sqlc.LockPostseasonFinalScoreHistoryParams{SeriesID: stage.FinalSeriesID, RosterID: stage.RosterID})
	if err != nil || len(rows) < 2 {
		if err != nil {
			return err
		}
		return fmt.Errorf("final continuation score history: %w", domain.ErrConflict)
	}
	current := rows[len(rows)-1]
	if current.ID != plan.SourceScoreRevision.UUID() || !current.ResultEventID.Valid ||
		current.FirstParticipantWins >= 2 || current.SecondParticipantWins >= 2 {
		return fmt.Errorf("final continuation current score: %w", domain.ErrConflict)
	}
	attempts, err := repository.tx.Querier(ctx).LockSeriesScoreRevisionAttempts(ctx,
		sqlc.LockSeriesScoreRevisionAttemptsParams{
			ScoreRevisionID: current.ID, TournamentID: stage.TournamentID,
			RosterID: stage.RosterID, SeriesID: stage.FinalSeriesID,
		})
	if err != nil || len(attempts) == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("final continuation score attempts: %w", domain.ErrConflict)
	}
	last := attempts[len(attempts)-1]
	if last.GameResultRevisionID != plan.SourceResultRevision.UUID() || last.GameAttemptID == plan.Next.GameID ||
		last.ResultState != string(domain.GameStateCompleted) {
		return fmt.Errorf("final continuation result binding: %w", domain.ErrConflict)
	}
	return nil
}
