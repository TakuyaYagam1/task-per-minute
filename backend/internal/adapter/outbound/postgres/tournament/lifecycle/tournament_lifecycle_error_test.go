package lifecycle

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
)

func TestActiveTournamentConflictMapsExactConstraint(t *testing.T) {
	t.Parallel()

	cause := &pgconn.PgError{Code: "23505", ConstraintName: tournamentActiveConstraint}
	err := activeTournamentConflict(cause)

	require.ErrorIs(t, err, lifecycleusecase.ErrActiveTournamentConflict)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.NotErrorIs(t, err, lifecycleusecase.ErrTournamentNotFound)
}

func TestIsUniqueViolationRequiresActiveTournamentConstraint(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err       error
		wantMatch bool
	}{
		"active tournament constraint": {
			err:       &pgconn.PgError{Code: "23505", ConstraintName: tournamentActiveConstraint},
			wantMatch: true,
		},
		"other unique constraint": {
			err:       &pgconn.PgError{Code: "23505", ConstraintName: "other_unique_constraint"},
			wantMatch: false,
		},
		"other postgres error": {
			err:       &pgconn.PgError{Code: "23514", ConstraintName: tournamentActiveConstraint},
			wantMatch: false,
		},
		"wrapped active tournament constraint": {
			err:       fmt.Errorf("wrapped constraint error: %w", &pgconn.PgError{Code: "23505", ConstraintName: tournamentActiveConstraint}),
			wantMatch: true,
		},
		"generic error": {
			err:       errors.New("generic error"),
			wantMatch: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.wantMatch, isUniqueViolation(test.err, tournamentActiveConstraint))
		})
	}
}
