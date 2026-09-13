package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/participantarchive"
)

type ParticipantArchivePostgres struct {
	tx *TxManager
}

func NewParticipantArchivePostgres(tx *TxManager) *ParticipantArchivePostgres {
	return &ParticipantArchivePostgres{tx: tx}
}

func (r *ParticipantArchivePostgres) FindSource(
	ctx context.Context,
	query inbound.ParticipantArchiveQuery,
) (participantarchive.Source, error) {
	if ctx == nil || r == nil || r.tx == nil || query.Actor.PlayerID == uuid.Nil ||
		query.TournamentID == uuid.Nil || query.AssignmentID == uuid.Nil {
		return participantarchive.Source{}, domain.ErrValidation
	}

	row, err := r.tx.Querier(ctx).GetParticipantArchiveSource(ctx, sqlc.GetParticipantArchiveSourceParams{
		TournamentID: query.TournamentID,
		PlayerID:     query.Actor.PlayerID,
		AssignmentID: query.AssignmentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return participantarchive.Source{}, domain.ErrAssignmentParticipant
	}
	if err != nil {
		return participantarchive.Source{}, fmt.Errorf("ParticipantArchivePostgres - FindSource: %w", err)
	}
	if row.TaskID == uuid.Nil {
		return participantarchive.Source{}, domain.ErrInternal
	}
	return participantarchive.Source{TaskID: row.TaskID, CanonicalURL: row.SourceFileUrl}, nil
}

var _ participantarchive.Repository = (*ParticipantArchivePostgres)(nil)
