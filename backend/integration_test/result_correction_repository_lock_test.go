//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestResultCorrectionRepositoryRechecksCurrentRevisionUnderLock(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createCorrectionRepositoryFixture(ctx, t)
	repository := correctionrepo.NewCorrectionPostgres(postgres.NewTxManager(sharedPool))
	firstInput := newCorrectionInput(
		ctx, t, fixture, fixture.result, fixture.projection, 1, fixture.nextTime,
	)
	secondInput := newCorrectionInput(
		ctx, t, fixture, fixture.result, fixture.projection, 0, fixture.nextTime,
	)
	for range 2 {
		traversal, err := repository.Traverse(ctx, firstInput.Scope, firstInput.SourceRevisionID)
		require.NoError(t, err)
		require.True(t, traversal.SourceIsCurrent)
		require.Empty(t, traversal.CutoffCode)
	}

	start := make(chan struct{})
	outcomes := make(chan error, 2)
	for _, input := range []correctionrepo.CorrectionInput{firstInput, secondInput} {
		go func(in correctionrepo.CorrectionInput) {
			<-start
			_, err := repository.Rebuild(ctx, in)
			outcomes <- err
		}(input)
	}
	close(start)

	errs := []error{<-outcomes, <-outcomes}
	succeeded := 0
	conflicted := 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, domain.ErrConflict):
			conflicted++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, conflicted)

	var commitCount int
	err := sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM result_commits
		WHERE attempt_id = $1`, fixture.resultFixture.attemptID).Scan(&commitCount)
	require.NoError(t, err)
	require.Equal(t, 2, commitCount)
}
