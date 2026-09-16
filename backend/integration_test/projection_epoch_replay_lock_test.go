//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	authorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/authority"
	executionrecoveryrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/recovery"
	recoveryrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery"
	recoveryterminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func TestEpochReplayRetainsFenceAndUnlockedReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := prepareFinalSwissSettlement(ctx, t)
	repository, record, condition := prepareSwissEpochReplay(ctx, t, fixture)
	stale := condition
	stale.ExpectedLeaseRevision--
	_, changed, err := repository.CommitEpochReplay(ctx, stale, record)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, changed)
	committed, changed, err := repository.CommitEpochReplay(ctx, condition, record)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, record.Attempt.CommandID, committed.Attempt.CommandID)

	holder, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer holder.Rollback(context.Background())
	_, err = holder.Exec(ctx, "SELECT id FROM tournaments WHERE id = $1 FOR UPDATE", fixture.tournamentID)
	require.NoError(t, err)
	lookupCtx, stopLookup := context.WithTimeout(ctx, time.Second)
	defer stopLookup()
	replayed, changed, err := repository.CommitEpochReplay(lookupCtx, condition, record)
	require.NoError(t, err, "stored replay must not acquire the scope prefix")
	require.False(t, changed)
	require.Equal(t, committed.CommandDigest, replayed.CommandDigest)
	require.Equal(t, committed.Attempt.CommandID, replayed.Attempt.CommandID)
	require.NoError(t, holder.Rollback(ctx))
	var commits, replays int
	var state string
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT attempt.state,
		(SELECT count(*) FROM result_commits WHERE attempt_id = attempt.id),
		(SELECT count(*) FROM execution_epoch_replays WHERE game_attempt_id = attempt.id)
		FROM game_attempts AS attempt WHERE id = $1`, record.Attempt.Scope.GameID).Scan(&state, &commits, &replays))
	require.Equal(t, "void", state)
	require.Equal(t, 1, commits)
	require.Equal(t, 1, replays)
}

func TestEpochReplayAndParticipantSettlementUseSameLockOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := prepareFinalSwissSettlement(ctx, t)
	repository, record, condition := prepareSwissEpochReplay(ctx, t, fixture)
	participant, scope := submitSwissReceiptSeries(ctx, t, fixture, 1)

	headerHeld, resumeParticipant := make(chan error, 1), make(chan struct{})
	participantDone, replayDone := make(chan error, 1), make(chan error, 1)
	replayPID := make(chan int32, 1)
	go func() {
		participantDone <- fixture.tx.Do(ctx, func(txCtx context.Context) error {
			// Force the participant's existing scope prefix to precede the
			// competing epoch replay, then execute real settlement below.
			_, err := fixture.tx.Conn(txCtx).Exec(txCtx, "SELECT id FROM tournaments WHERE id = $1 FOR UPDATE", fixture.tournamentID)
			if err == nil {
				_, err = fixture.tx.Conn(txCtx).Exec(txCtx, "SELECT id FROM rosters WHERE id = $1 FOR UPDATE", fixture.rosterID)
			}
			headerHeld <- err
			if err != nil {
				return err
			}
			select {
			case <-resumeParticipant:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, _, err = gameusecase.SettlementNewUseCase(participant).Settle(txCtx, gameusecase.SettlementCommand{Scope: scope, CommandID: uuid.New()})
			return err
		})
	}()
	select {
	case err := <-headerHeld:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		replayDone <- fixture.tx.Do(ctx, func(txCtx context.Context) error {
			var pid int32
			if err := fixture.tx.Conn(txCtx).QueryRow(txCtx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				return err
			}
			replayPID <- pid
			_, _, err := repository.CommitEpochReplay(txCtx, condition, record)
			return err
		})
	}()
	var pid int32
	select {
	case pid = <-replayPID:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waiting := false
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		if err := sharedPool.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1)) > 0", pid).Scan(&waiting); err != nil || waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(resumeParticipant)
	var participantErr, replayErr error
	select {
	case participantErr = <-participantDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case replayErr = <-replayDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.True(t, waiting, "epoch replay must contend with participant authority")
	for _, outcome := range []error{participantErr, replayErr} {
		var detail *pgconn.PgError
		if errors.As(outcome, &detail) {
			require.NotEqual(t, "40P01", detail.Code, "epoch Game fence cannot precede Tournament")
		}
	}
	require.NoError(t, participantErr)
	require.ErrorIs(t, replayErr, domain.ErrConflict)
	var commits, replays int
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM result_commits WHERE attempt_id = $1),
		(SELECT count(*) FROM execution_epoch_replays WHERE game_attempt_id = $1)`, record.Attempt.Scope.GameID).Scan(&commits, &replays))
	require.Equal(t, 1, commits)
	require.Zero(t, replays)
	readFinalSwissReceipt(ctx, t, fixture)
}

func prepareSwissEpochReplay(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture) (
	*executionrecoveryrepo.ExecutionRecoveryPostgres, gameusecase.EpochReplayRecord, gameusecase.EpochReplayCommitCondition,
) {
	t.Helper()
	authority := authorityrepo.NewExecutionAuthorityPostgres(fixture.tx)
	old := fixture.executionAuthority
	stamp := old.Stamp()
	renewed, changed, err := authorityusecase.NewWithTimeSource(authority, authority, 100*time.Millisecond).Claim(ctx, authorityusecase.ClaimCommand{
		TournamentID: old.TournamentID, HolderID: old.HolderID, LeaseID: old.LeaseID,
		CommandID: uuid.New(), ProcessKind: authoritydomain.ProcessAuthority, Expected: &stamp,
	})
	require.NoError(t, err)
	require.True(t, changed)
	if delay := time.Until(renewed.ExpiresAt); delay > 0 {
		time.Sleep(delay + time.Millisecond)
	}
	current, changed, err := authorityusecase.NewWithTimeSource(authority, authority, time.Minute).Claim(ctx, authorityusecase.ClaimCommand{
		TournamentID: old.TournamentID, HolderID: uuid.New(), LeaseID: uuid.New(),
		CommandID: uuid.New(), ProcessKind: authoritydomain.ProcessAuthority, Expected: renewed.Stamp(),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, old.Epoch+1, current.Epoch)
	deadlines := recoveryrepo.NewRecoveryPostgres(fixture.tx, nil)
	terminal := recoveryterminalrepo.NewRecoveryTerminalPostgresWithDependencies(
		fixture.tx, authority, playoffPublicationClock{},
		wavestartrepo.EnsurePreStartSwissRoundProofForCommand, resultauthority.FinalizeProjection,
	)
	repository := executionrecoveryrepo.NewExecutionRecoveryPostgresWithDependencies(
		fixture.tx, deadlines, terminal, resultauthority.FinalizeProjection,
	)
	candidates, err := repository.ListActiveGames(ctx, fixture.tournamentID, current.Identity())
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, fixture.binding[1].SeriesID, candidates[0].Scope.SeriesID)
	require.NotNil(t, candidates[0].EpochReplay)
	plan := &epochReplayPlanRepository{ExecutionRecoveryPostgres: repository}
	record, changed, err := gameusecase.NewEpochReplayUseCase(plan, authority).Replay(ctx, *candidates[0].EpochReplay)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.NoError(t, plan.condition.Validate())
	return repository, *record, plan.condition
}

// Capture the production planner's exact commit input before introducing the
// contention barrier. Every authority read and the eventual write use PostgreSQL.
type epochReplayPlanRepository struct {
	*executionrecoveryrepo.ExecutionRecoveryPostgres
	condition gameusecase.EpochReplayCommitCondition
}

func (r *epochReplayPlanRepository) CommitEpochReplay(_ context.Context, condition gameusecase.EpochReplayCommitCondition, record gameusecase.EpochReplayRecord) (*gameusecase.EpochReplayRecord, bool, error) {
	r.condition = condition
	return &record, true, nil
}
