package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
)

const tournamentActiveConstraint = "tournaments_single_active_idx"

type TournamentLifecyclePostgres struct {
	tx *db.TxManager
}

var _ lifecycleusecase.TournamentLifecycleRepository = (*TournamentLifecyclePostgres)(nil)

func NewTournamentLifecyclePostgres(tx *db.TxManager) *TournamentLifecyclePostgres {
	return &TournamentLifecyclePostgres{tx: tx}
}

func (r *TournamentLifecyclePostgres) GetTournament(
	ctx context.Context,
	id uuid.UUID,
) (*lifecycleusecase.LifecycleTournamentRecord, error) {
	if r == nil || r.tx == nil || id == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).GetTournamentSummary(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, lifecycleusecase.ErrTournamentNotFound
		}
		return nil, fmt.Errorf("TournamentLifecyclePostgres - GetTournament - Querier.GetTournamentSummary: %w", err)
	}
	return tournamentLifecycleSummaryRecord(row)
}

func (r *TournamentLifecyclePostgres) TransitionTournament(
	ctx context.Context,
	in lifecycleusecase.TournamentLifecycleTransitionInput,
) (*lifecycleusecase.LifecycleTournamentRecord, bool, error) {
	if r == nil || r.tx == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateTournamentTransitionInput(in); err != nil {
		return nil, false, err
	}
	current := domain.Tournament{State: in.ExpectedState}
	if !current.CanTransitionTo(in.NextState) {
		return nil, false, domain.ErrValidation
	}

	var record *lifecycleusecase.LifecycleTournamentRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		_, err := r.tx.Querier(txCtx).UpdateTournamentCAS(txCtx, sqlc.UpdateTournamentCASParams{
			NextState:        string(in.NextState),
			PausedFromState:  nullableState(in.PausedFromState),
			UpdatedAt:        tstz(in.TransitionedAt),
			StartedAt:        nullableTSTZ(in.StartedAt),
			FinishedAt:       nullableTSTZ(in.FinishedAt),
			ID:               in.TournamentID,
			ExpectedRevision: in.ExpectedRevision,
			ExpectedState:    string(in.ExpectedState),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if isUniqueViolation(err, tournamentActiveConstraint) {
				return activeTournamentConflict(err)
			}
			return fmt.Errorf("TournamentPostgres - Transition - Querier.UpdateTournamentCAS: %w", err)
		}
		if in.NextState.IsTerminal() {
			if _, err = r.tx.Querier(txCtx).ReleaseTournamentReservations(txCtx, in.TournamentID); err != nil {
				return fmt.Errorf("TournamentPostgres - Transition - Querier.ReleaseTournamentReservations: %w", err)
			}
		}
		changed = true
		row, err := r.tx.Querier(txCtx).GetTournamentSummary(txCtx, in.TournamentID)
		if err != nil {
			return fmt.Errorf("load transitioned tournament: %w", err)
		}
		record, err = tournamentLifecycleSummaryRecord(row)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return record, changed, nil
}

func activeTournamentConflict(err error) error {
	return errors.Join(lifecycleusecase.ErrActiveTournamentConflict, domain.WrapError(err, domain.ErrConflict))
}

func validateTournamentTransitionInput(in lifecycleusecase.TournamentLifecycleTransitionInput) error {
	if in.TournamentID == uuid.Nil || in.ExpectedRevision < 1 || !in.ExpectedState.IsValid() ||
		!in.NextState.IsValid() || !domain.IsValidServerTime(in.TransitionedAt) {
		return domain.ErrValidation
	}
	state := domain.Tournament{State: in.NextState, PausedFromState: in.PausedFromState}
	if err := state.Validate(); err != nil {
		return domain.ErrValidation
	}
	if in.StartedAt != nil && !domain.IsValidServerTime(*in.StartedAt) {
		return domain.ErrValidation
	}
	if in.FinishedAt != nil && !domain.IsValidServerTime(*in.FinishedAt) {
		return domain.ErrValidation
	}
	return nil
}

func tournamentLifecycleSummaryRecord(
	row sqlc.GetTournamentSummaryRow,
) (*lifecycleusecase.LifecycleTournamentRecord, error) {
	record := &lifecycleusecase.LifecycleTournamentRecord{
		ID: row.ID, State: domain.TournamentState(row.State), Revision: row.Revision,
		UpdatedAt: row.UpdatedAt.Time.UTC(),
		StartedAt: utcTimePointer(nullableTime(row.StartedAt)), FinishedAt: utcTimePointer(nullableTime(row.FinishedAt)),
	}
	if row.PausedFromState != nil {
		state := domain.TournamentState(*row.PausedFromState)
		record.PausedFromState = &state
	}
	return record, nil
}

func nullableState(value *domain.TournamentState) *string {
	if value == nil {
		return nil
	}
	out := string(*value)
	return &out
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func nullableTSTZ(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	out := value.Time
	return &out
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
