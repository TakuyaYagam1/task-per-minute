package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
)

type ParticipantPostSeriesRepository struct {
	tx *TxManager
}

func NewParticipantPostSeriesRepository(tx *TxManager) *ParticipantPostSeriesRepository {
	return &ParticipantPostSeriesRepository{tx: tx}
}

func (r *ParticipantPostSeriesRepository) FindPostSeriesCommand(
	ctx context.Context,
	commandID uuid.UUID,
) (*tournamentparticipant.PostSeriesRecord, error) {
	if ctx == nil || r == nil || r.tx == nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).FindParticipantPostSeriesAction(ctx, commandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ParticipantPostSeriesRepository - find command: %w", err)
	}
	record := participantPostSeriesRecord(row)
	return &record, nil
}

func (r *ParticipantPostSeriesRepository) CommitPostSeries(
	ctx context.Context,
	commit tournamentparticipant.PostSeriesCommit,
) (*tournamentparticipant.PostSeriesRecord, bool, error) {
	if ctx == nil || r == nil || r.tx == nil || !validParticipantPostSeriesCommit(commit) {
		return nil, false, domain.ErrValidation
	}

	var result *tournamentparticipant.PostSeriesRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		existing, err := r.FindPostSeriesCommand(txCtx, commit.Record.CommandID)
		if err != nil {
			return err
		}
		if existing != nil {
			if *existing != commit.Record {
				return tournamentparticipant.ErrPostSeriesCommandReuse
			}
			result = existing
			return nil
		}

		row, err := r.tx.Querier(txCtx).CreateParticipantPostSeriesAction(
			txCtx,
			sqlc.CreateParticipantPostSeriesActionParams{
				CommandID:                     commit.Record.CommandID,
				TournamentID:                  commit.Record.TournamentID,
				RosterID:                      commit.Record.RosterID,
				SeriesID:                      commit.Record.SeriesID,
				ParticipantID:                 commit.Record.ParticipantID,
				CurrentResultRevisionID:       commit.Record.CurrentResultRevisionID.UUID(),
				SourceProjectionRevisionID:    commit.Record.SourceProjectionRevisionID,
				SourceProjectionRevision:      commit.Record.SourceProjectionRevision,
				ResultingProjectionRevisionID: commit.Record.ResultingProjectionRevisionID,
				ResultingProjectionRevision:   commit.Record.ResultingProjectionRevision,
				Action:                        string(commit.Record.Action),
				OccurredAt:                    tstz(commit.Record.OccurredAt),
				ExpectedSeriesState:           string(commit.ExpectedSeriesState),
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrConflict
		}
		if err != nil {
			return mapRepositoryWriteError("ParticipantPostSeriesRepository - commit", err)
		}
		mapped := participantPostSeriesRecord(row)
		if mapped != commit.Record {
			return domain.ErrInternal
		}
		result = &mapped
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if result == nil {
		return nil, false, domain.ErrInternal
	}
	return result, changed, nil
}

func validParticipantPostSeriesCommit(commit tournamentparticipant.PostSeriesCommit) bool {
	record := commit.Record
	return record.CommandID != uuid.Nil && record.TournamentID != uuid.Nil && record.RosterID != uuid.Nil &&
		record.SeriesID != uuid.Nil && record.ParticipantID != uuid.Nil &&
		!record.CurrentResultRevisionID.IsZero() && record.SourceProjectionRevisionID != uuid.Nil &&
		record.SourceProjectionRevision >= 1 && record.ResultingProjectionRevisionID != uuid.Nil &&
		record.ResultingProjectionRevision >= record.SourceProjectionRevision && record.Action.IsValid() &&
		domain.IsValidServerTime(record.OccurredAt) && commit.ExpectedSeriesState.IsTerminal() &&
		commit.ExpectedResultRevisionID == record.CurrentResultRevisionID &&
		commit.ExpectedProjectionRevision == record.SourceProjectionRevision
}

func participantPostSeriesRecord(row sqlc.ParticipantPostSeriesAction) tournamentparticipant.PostSeriesRecord {
	return tournamentparticipant.PostSeriesRecord{
		CommandID:                     row.CommandID,
		TournamentID:                  row.TournamentID,
		RosterID:                      row.RosterID,
		SeriesID:                      row.SeriesID,
		ParticipantID:                 row.ParticipantID,
		CurrentResultRevisionID:       domain.OfficialResultRevisionID(row.CurrentResultRevisionID),
		SourceProjectionRevisionID:    row.SourceProjectionRevisionID,
		SourceProjectionRevision:      row.SourceProjectionRevision,
		ResultingProjectionRevisionID: row.ResultingProjectionRevisionID,
		ResultingProjectionRevision:   row.ResultingProjectionRevision,
		Action:                        usecase.PostSeriesAction(row.Action),
		OccurredAt:                    row.OccurredAt.Time.UTC(),
	}
}

var _ tournamentparticipant.PostSeriesRepository = (*ParticipantPostSeriesRepository)(nil)
