package catalog

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
)

const (
	tournamentActiveConstraint   = "tournaments_single_active_idx"
	tournamentPublicIDConstraint = "tournaments_public_id_unique"
)

// TournamentCatalogPostgres owns the base tournament persistence boundary.
// The parent postgres package keeps a compatibility facade around this type so
// existing callers can continue to use TournamentPostgres.
type TournamentCatalogPostgres struct {
	tx *db.TxManager
}

type TournamentCreateInput struct {
	ID                uuid.UUID
	RosterID          uuid.UUID
	Name              string
	PublicID          string
	PlannedRosterSize int
	ContentRevision   int64
	CreatedAt         time.Time
}

type TournamentTransitionInput struct {
	ID               uuid.UUID
	ExpectedRevision int64
	ExpectedState    domain.TournamentState
	NextState        domain.TournamentState
	PausedFromState  *domain.TournamentState
	UpdatedAt        time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
}

func NewTournamentCatalogPostgres(tx *db.TxManager) *TournamentCatalogPostgres {
	return &TournamentCatalogPostgres{tx: tx}
}

func (r *TournamentCatalogPostgres) Create(
	ctx context.Context,
	in TournamentCreateInput,
) (sqlc.Tournament, sqlc.Roster, error) {
	metadata := domain.TournamentMetadata{
		Name:              in.Name,
		PublicID:          in.PublicID,
		PlannedRosterSize: in.PlannedRosterSize,
		ContentRevision:   in.ContentRevision,
	}
	if r == nil || r.tx == nil || in.ID == uuid.Nil || in.RosterID == uuid.Nil ||
		metadata.Validate(domain.TournamentPresetV1) != nil || !validServerTime(in.CreatedAt) {
		return sqlc.Tournament{}, sqlc.Roster{}, domain.ErrValidation
	}

	var tournament sqlc.Tournament
	var roster sqlc.Roster
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		binding, bindErr := LoadContentBinding(txCtx, querier, in.ID, in.RosterID, in.ContentRevision)
		if bindErr != nil {
			return bindErr
		}
		if bindErr = RevalidateContentPools(txCtx, querier, binding); bindErr != nil {
			return bindErr
		}

		var createErr error
		tournament, createErr = querier.CreateTournament(txCtx, sqlc.CreateTournamentParams{
			//nolint:gosec // Metadata validation bounds planned roster size to 4..16.
			ID: in.ID, Name: in.Name, PublicID: in.PublicID, PlannedRosterSize: int32(in.PlannedRosterSize),
			ContentRevision: in.ContentRevision, CreatedAt: tstz(in.CreatedAt),
		})
		if createErr != nil {
			return fmt.Errorf("TournamentPostgres - Create - Querier.CreateTournament: %w", createErr)
		}
		roster, createErr = querier.CreateTournamentRoster(txCtx, sqlc.CreateTournamentRosterParams{
			ID: in.RosterID, TournamentID: in.ID, CreatedAt: tstz(in.CreatedAt),
		})
		if createErr != nil {
			return fmt.Errorf("TournamentPostgres - Create - Querier.CreateTournamentRoster: %w", createErr)
		}
		if createErr = PersistContentBinding(txCtx, querier, binding, in.CreatedAt); createErr != nil {
			return createErr
		}
		return nil
	})
	if err != nil {
		if isUniqueViolation(err, tournamentPublicIDConstraint) {
			return sqlc.Tournament{}, sqlc.Roster{}, domain.WrapError(err, domain.ErrConflict)
		}
		return sqlc.Tournament{}, sqlc.Roster{}, err
	}
	return tournament, roster, nil
}

func (r *TournamentCatalogPostgres) Get(ctx context.Context, id uuid.UUID) (sqlc.Tournament, error) {
	row, err := r.tx.Querier(ctx).GetTournament(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.Tournament{}, ErrTournamentNotFound
		}
		return sqlc.Tournament{}, fmt.Errorf("TournamentPostgres - Get - Querier.GetTournament: %w", err)
	}
	return row, nil
}

func (r *TournamentCatalogPostgres) Active(ctx context.Context) (sqlc.Tournament, error) {
	row, err := r.tx.Querier(ctx).GetActiveTournament(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.Tournament{}, nil
		}
		return sqlc.Tournament{}, fmt.Errorf("TournamentPostgres - Active - Querier.GetActiveTournament: %w", err)
	}
	return row, nil
}

func (r *TournamentCatalogPostgres) List(ctx context.Context) ([]sqlc.Tournament, error) {
	rows, err := r.tx.Querier(ctx).ListTournaments(ctx)
	if err != nil {
		return nil, fmt.Errorf("TournamentPostgres - List - Querier.ListTournaments: %w", err)
	}
	return rows, nil
}

func (r *TournamentCatalogPostgres) Transition(
	ctx context.Context,
	in TournamentTransitionInput,
) (sqlc.Tournament, bool, error) {
	if err := validateTournamentTransitionInput(in); err != nil {
		return sqlc.Tournament{}, false, err
	}

	var updated sqlc.Tournament
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		updated, err = r.tx.Querier(txCtx).UpdateTournamentCAS(txCtx, sqlc.UpdateTournamentCASParams{
			NextState:        string(in.NextState),
			PausedFromState:  nullableState(in.PausedFromState),
			UpdatedAt:        tstz(in.UpdatedAt),
			StartedAt:        nullableTSTZ(in.StartedAt),
			FinishedAt:       nullableTSTZ(in.FinishedAt),
			ID:               in.ID,
			ExpectedRevision: in.ExpectedRevision,
			ExpectedState:    string(in.ExpectedState),
		})
		if err != nil {
			return err
		}
		if !in.NextState.IsTerminal() {
			return nil
		}
		if _, err = r.tx.Querier(txCtx).ReleaseTournamentReservations(txCtx, in.ID); err != nil {
			return fmt.Errorf("TournamentPostgres - Transition - Querier.ReleaseTournamentReservations: %w", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.Tournament{}, false, nil
		}
		if isUniqueViolation(err, tournamentActiveConstraint) {
			return sqlc.Tournament{}, false, domain.WrapError(err, domain.ErrConflict)
		}
		return sqlc.Tournament{}, false, fmt.Errorf("TournamentPostgres - Transition - Querier.UpdateTournamentCAS: %w", err)
	}
	return updated, true, nil
}

var ErrTournamentNotFound = errors.New("tournament repository: tournament not found")

func validServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
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

func nullableState(value *domain.TournamentState) *string {
	if value == nil {
		return nil
	}
	out := string(*value)
	return &out
}

func validateTournamentTransitionInput(in TournamentTransitionInput) error {
	if in.ID == uuid.Nil || in.ExpectedRevision < 1 || !in.ExpectedState.IsValid() || !in.NextState.IsValid() ||
		!validServerTime(in.UpdatedAt) {
		return domain.ErrValidation
	}
	state := domain.Tournament{State: in.NextState, PausedFromState: in.PausedFromState}
	if err := state.Validate(); err != nil {
		return domain.ErrValidation
	}
	if in.StartedAt != nil && !validServerTime(*in.StartedAt) {
		return domain.ErrValidation
	}
	if in.FinishedAt != nil && !validServerTime(*in.FinishedAt) {
		return domain.ErrValidation
	}
	return nil
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
