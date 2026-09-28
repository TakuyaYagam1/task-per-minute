package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
)

func (r *TournamentAdminLifecyclePostgres) LockTournamentDeletionScope(
	ctx context.Context,
	tournamentID uuid.UUID,
) (tournamentadmin.TournamentDeletionScope, error) {
	if !validTournamentAdminLifecycleRepository(ctx, r) || tournamentID == uuid.Nil {
		return tournamentadmin.TournamentDeletionScope{}, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).LockTournamentDeletionScope(ctx, tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tournamentadmin.TournamentDeletionScope{}, domain.ErrTournamentNotFound
	}
	if err != nil {
		return tournamentadmin.TournamentDeletionScope{}, fmt.Errorf(
			"TournamentAdminLifecyclePostgres - lock deletion scope: %w", err,
		)
	}
	state := domain.TournamentState(row.State)
	if row.TournamentID != tournamentID || row.RosterID == uuid.Nil || !state.IsValid() ||
		row.Revision < 1 || !row.UpdatedAt.Valid {
		return tournamentadmin.TournamentDeletionScope{}, domain.ErrInternal
	}
	return tournamentadmin.TournamentDeletionScope{
		TournamentID: row.TournamentID,
		RosterID:     row.RosterID,
		State:        state,
		Revision:     row.Revision,
		UpdatedAt:    row.UpdatedAt.Time.UTC(),
		FinishedAt:   lifecycleUTCNullableTime(row.FinishedAt),
		DeletedAt:    lifecycleUTCNullableTime(row.DeletedAt),
	}, nil
}

func (r *TournamentAdminLifecyclePostgres) FindTournamentDeletion(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*tournamentadmin.TournamentDeletionRecord, error) {
	if !validTournamentAdminLifecycleRepository(ctx, r) || tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).FindTournamentDeletion(ctx, sqlc.FindTournamentDeletionParams{
		TournamentID: tournamentID,
		CommandID:    commandID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminLifecyclePostgres - find deletion: %w", err)
	}
	record := tournamentDeletionRecord(row)
	if record.TournamentID != tournamentID || record.CommandID != commandID {
		return nil, domain.ErrInternal
	}
	return &record, nil
}

func (r *TournamentAdminLifecyclePostgres) MarkTournamentDeleted(
	ctx context.Context,
	input tournamentadmin.TournamentDeletionInput,
) (*tournamentadmin.TournamentDeletionRecord, error) {
	if !validTournamentAdminLifecycleRepository(ctx, r) || !validTournamentDeletionInput(input) {
		return nil, domain.ErrValidation
	}
	updated, err := r.tx.Querier(ctx).MarkTournamentDeleted(ctx, sqlc.MarkTournamentDeletedParams{
		DeletedAt:        tstz(input.DeletedAt),
		TournamentID:     input.TournamentID,
		ExpectedRevision: input.ExpectedRevision,
		Cancelled:        input.Cancelled,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrConflict
	}
	if err != nil {
		return nil, lifecycleWriteError("mark tournament deleted", err)
	}
	if !deletedTournamentRowValid(updated, input) {
		return nil, domain.ErrInternal
	}
	created, err := r.tx.Querier(ctx).CreateTournamentDeletion(ctx, sqlc.CreateTournamentDeletionParams{
		CommandID:         input.CommandID,
		TournamentID:      input.TournamentID,
		ActorID:           input.ActorID,
		SourceRevision:    input.SourceRevision,
		ResultingRevision: updated.Revision,
		SourceState:       string(input.SourceState),
		Cancelled:         input.Cancelled,
		Reason:            input.Reason,
		DeletedAt:         tstz(input.DeletedAt),
		CreatedAt:         tstz(input.CreatedAt),
	})
	if err != nil {
		return nil, lifecycleWriteError("create tournament deletion", err)
	}
	if !created.DeletedAt.Valid || !created.CreatedAt.Valid {
		return nil, domain.ErrInternal
	}
	record := tournamentDeletionRecord(created)
	if !tournamentDeletionRecordValid(record, input, updated.Revision) {
		return nil, domain.ErrInternal
	}
	return &record, nil
}

func deletedTournamentRowValid(
	row sqlc.MarkTournamentDeletedRow,
	input tournamentadmin.TournamentDeletionInput,
) bool {
	expectedRevision := input.ExpectedRevision + 1
	if input.Cancelled {
		expectedRevision = input.ExpectedRevision
	}
	return row.TournamentID == input.TournamentID && row.Revision == expectedRevision &&
		row.UpdatedAt.Valid && row.DeletedAt.Valid &&
		(!input.Cancelled || domain.TournamentState(row.State) == domain.TournamentStateCancelled)
}

func tournamentDeletionRecord(row sqlc.TournamentDeletion) tournamentadmin.TournamentDeletionRecord {
	return tournamentadmin.TournamentDeletionRecord{
		CommandID:         row.CommandID,
		TournamentID:      row.TournamentID,
		ActorID:           row.ActorID,
		SourceRevision:    row.SourceRevision,
		ResultingRevision: row.ResultingRevision,
		SourceState:       domain.TournamentState(row.SourceState),
		Cancelled:         row.Cancelled,
		Reason:            row.Reason,
		DeletedAt:         row.DeletedAt.Time.UTC(),
		CreatedAt:         row.CreatedAt.Time.UTC(),
	}
}

func validTournamentDeletionInput(input tournamentadmin.TournamentDeletionInput) bool {
	if input.CommandID == uuid.Nil || input.TournamentID == uuid.Nil || input.ActorID == uuid.Nil ||
		input.SourceRevision < 1 || input.ExpectedRevision < 1 || !input.SourceState.IsValid() ||
		input.Reason != tournamentadmin.TournamentDeletionReason || input.DeletedAt.IsZero() ||
		input.CreatedAt.IsZero() || input.DeletedAt.After(input.CreatedAt) {
		return false
	}
	requiresCancellation := input.SourceState != domain.TournamentStateDraft && !input.SourceState.IsTerminal()
	if input.Cancelled != requiresCancellation {
		return false
	}
	if input.Cancelled {
		return input.ExpectedRevision == input.SourceRevision+1
	}
	return input.ExpectedRevision == input.SourceRevision
}

func tournamentDeletionRecordValid(
	record tournamentadmin.TournamentDeletionRecord,
	input tournamentadmin.TournamentDeletionInput,
	resultingRevision int64,
) bool {
	recordedDeletedAt := record.DeletedAt.UTC().Truncate(time.Microsecond)
	recordedCreatedAt := record.CreatedAt.UTC().Truncate(time.Microsecond)
	inputDeletedAt := input.DeletedAt.UTC().Truncate(time.Microsecond)
	inputCreatedAt := input.CreatedAt.UTC().Truncate(time.Microsecond)
	return record.CommandID == input.CommandID && record.TournamentID == input.TournamentID &&
		record.ActorID == input.ActorID && record.SourceRevision == input.SourceRevision &&
		record.ResultingRevision == resultingRevision && record.SourceState == input.SourceState &&
		record.Cancelled == input.Cancelled && record.Reason == input.Reason &&
		recordedDeletedAt.Equal(inputDeletedAt) && recordedCreatedAt.Equal(inputCreatedAt)
}

var _ tournamentadmin.DeletionRepository = (*TournamentAdminLifecyclePostgres)(nil)
