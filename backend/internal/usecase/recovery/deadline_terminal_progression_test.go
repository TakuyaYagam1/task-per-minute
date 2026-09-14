package recovery_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	recoverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery/mocks"
)

type deadlineTransactionMarker struct{}

type recordingDeadlineTransactions struct {
	calls     int
	commits   int
	rollbacks int
}

func (transactions *recordingDeadlineTransactions) Do(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	transactions.calls++
	err := fn(context.WithValue(ctx, deadlineTransactionMarker{}, true))
	if err != nil {
		transactions.rollbacks++
		return err
	}
	transactions.commits++
	return nil
}

type recordingDeadlineAdvancer struct {
	commands []playoff.TerminalSeriesCommand
	err      error
}

func (advancer *recordingDeadlineAdvancer) AdvanceAfterSeriesSettlement(
	ctx context.Context,
	command playoff.TerminalSeriesCommand,
) (playoff.TerminalReceipt, error) {
	if _, ok := ctx.Value(deadlineTransactionMarker{}).(bool); !ok {
		return playoff.TerminalReceipt{}, fmt.Errorf("terminal advancement escaped deadline transaction")
	}
	advancer.commands = append(advancer.commands, command)
	return playoff.TerminalReceipt{}, advancer.err
}

func TestTerminalDeadlineHandlerAdvancesReconnectInsideOuterTransaction(t *testing.T) {
	now := fixedDeadlineNow()
	authority := reconnectDeadlineAuthority(now)
	transactions := &recordingDeadlineTransactions{}
	advancer := &recordingDeadlineAdvancer{}
	store := recoverymocks.NewMockDeadlineTerminalStore(t)
	store.EXPECT().LoadDeadlineAuthority(mock.Anything, authority.Deadline).
		Run(func(ctx context.Context, _ recovery.PendingDeadline) {
			requireDeadlineTransactionContext(ctx, t)
		}).Return(authority, true, nil).Once()
	store.EXPECT().CommitDeadlinePlan(mock.Anything, mock.Anything).
		Run(func(ctx context.Context, plan recovery.DeadlineTerminalPlan) {
			requireDeadlineTransactionContext(ctx, t)
			require.NotNil(t, plan.ReconnectTimeout)
			require.NotNil(t, plan.ReconnectTimeout.SeriesResultRevision)
			require.Nil(t, plan.ReconnectTimeout.ReplayRoute)
		}).Return(true, nil).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now).Once()
	handler := recovery.NewTerminalDeadlineHandlerWithDependencies(
		transactions, store, clock, advancer,
	)

	changed, err := handler.HandleDeadline(context.Background(), authority.Deadline)

	require.True(t, changed)
	require.NoError(t, err)
	require.Equal(t, 1, transactions.calls)
	require.Equal(t, 1, transactions.commits)
	require.Zero(t, transactions.rollbacks)
	require.Equal(t, []playoff.TerminalSeriesCommand{{
		TournamentID: authority.Deadline.TournamentID,
		SeriesID:     authority.Deadline.SeriesID,
	}}, advancer.commands)
}

func TestTerminalDeadlineHandlerAdvancesActiveReconnectInsideOuterTransaction(t *testing.T) {
	now := fixedDeadlineNow()
	authority := reconnectDeadlineAuthority(now)
	authority.ReconnectTimeout.Series.Format = domain.SeriesFormatBO3
	transactions := &recordingDeadlineTransactions{}
	advancer := &recordingDeadlineAdvancer{}
	store := recoverymocks.NewMockDeadlineTerminalStore(t)
	store.EXPECT().LoadDeadlineAuthority(mock.Anything, authority.Deadline).
		Return(authority, true, nil).Once()
	store.EXPECT().CommitDeadlinePlan(mock.Anything, mock.Anything).
		Run(func(ctx context.Context, plan recovery.DeadlineTerminalPlan) {
			requireDeadlineTransactionContext(ctx, t)
			require.NotNil(t, plan.ReconnectTimeout)
			require.NotNil(t, plan.ReconnectTimeout.GameResultRevision)
			require.NotNil(t, plan.ReconnectTimeout.ScoreRevision)
			require.NotNil(t, plan.ReconnectTimeout.Evidence)
			require.Nil(t, plan.ReconnectTimeout.SeriesResultRevision)
			require.Nil(t, plan.ReconnectTimeout.VoidGameResultRevision)
			require.Nil(t, plan.ReconnectTimeout.ReplayRoute)
			require.Equal(t, domain.SeriesStateActive, plan.ReconnectTimeout.ReconnectAuthority.Series.State)
		}).Return(true, nil).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now).Once()
	handler := recovery.NewTerminalDeadlineHandlerWithDependencies(
		transactions, store, clock, advancer,
	)

	changed, err := handler.HandleDeadline(context.Background(), authority.Deadline)

	require.True(t, changed)
	require.NoError(t, err)
	require.Equal(t, 1, transactions.calls)
	require.Equal(t, 1, transactions.commits)
	require.Equal(t, []playoff.TerminalSeriesCommand{{
		TournamentID: authority.Deadline.TournamentID,
		SeriesID:     authority.Deadline.SeriesID,
	}}, advancer.commands)
}

func TestTerminalDeadlineHandlerRollsBackWhenReconnectAdvancementFails(t *testing.T) {
	now := fixedDeadlineNow()
	authority := reconnectDeadlineAuthority(now)
	transactions := &recordingDeadlineTransactions{}
	advancementErr := errors.New("playoff advancement failed")
	advancer := &recordingDeadlineAdvancer{err: advancementErr}
	store := recoverymocks.NewMockDeadlineTerminalStore(t)
	store.EXPECT().LoadDeadlineAuthority(mock.Anything, authority.Deadline).Return(authority, true, nil).Once()
	store.EXPECT().CommitDeadlinePlan(mock.Anything, mock.Anything).Return(true, nil).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now).Once()
	handler := recovery.NewTerminalDeadlineHandlerWithDependencies(
		transactions, store, clock, advancer,
	)

	changed, err := handler.HandleDeadline(context.Background(), authority.Deadline)

	require.False(t, changed)
	require.ErrorIs(t, err, advancementErr)
	require.Equal(t, 1, transactions.calls)
	require.Zero(t, transactions.commits)
	require.Equal(t, 1, transactions.rollbacks)
	require.Len(t, advancer.commands, 1)
}

func TestTerminalDeadlineHandlerSkipsAdvancementWhenCommitIsUnchanged(t *testing.T) {
	now := fixedDeadlineNow()
	authority := reconnectDeadlineAuthority(now)
	transactions := &recordingDeadlineTransactions{}
	advancer := &recordingDeadlineAdvancer{}
	store := recoverymocks.NewMockDeadlineTerminalStore(t)
	store.EXPECT().LoadDeadlineAuthority(mock.Anything, authority.Deadline).Return(authority, true, nil).Once()
	store.EXPECT().CommitDeadlinePlan(mock.Anything, mock.Anything).Return(false, nil).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now).Once()
	handler := recovery.NewTerminalDeadlineHandlerWithDependencies(
		transactions, store, clock, advancer,
	)

	changed, err := handler.HandleDeadline(context.Background(), authority.Deadline)

	require.False(t, changed)
	require.NoError(t, err)
	require.Empty(t, advancer.commands)
	require.Equal(t, 1, transactions.commits)
	require.Zero(t, transactions.rollbacks)
}

func fixedDeadlineNow() time.Time {
	return time.Date(2026, time.September, 7, 16, 0, 0, 0, time.UTC)
}

func requireDeadlineTransactionContext(ctx context.Context, t *testing.T) {
	t.Helper()
	if _, ok := ctx.Value(deadlineTransactionMarker{}).(bool); !ok {
		t.Fatal("expected deadline transaction context")
	}
}
