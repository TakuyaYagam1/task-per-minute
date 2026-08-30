package arena_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestPrivateTaskDeliveryDeniesPreStartAndOpponentAccess(t *testing.T) {
	t.Parallel()

	authority, command := privateTaskDeliveryFixture(t)
	repository := &privateTaskDeliveryRepositoryFake{authority: authority}
	usecase := arena.NewPrivateTaskDeliveryUseCase(repository)

	_, _, err := usecase.Deliver(t.Context(), command)
	require.ErrorIs(t, err, arena.ErrPrivateTaskNotStarted)

	startedAt := command.RequestedAt.Add(-time.Second)
	repository.setStartedAt(startedAt)
	command.ActorParticipantID = authority.Snapshot.Instances()[1].ParticipantID
	_, _, err = usecase.Deliver(t.Context(), command)
	require.ErrorIs(t, err, domain.ErrArenaAssignmentParticipant)
}

func TestPrivateTaskDeliveryIsIdempotentAndReturnsOnlyOwnerSnapshot(t *testing.T) {
	t.Parallel()

	authority, command := privateTaskDeliveryFixture(t)
	startedAt := command.RequestedAt.Add(-time.Second)
	authority.StartedAt = &startedAt
	repository := &privateTaskDeliveryRepositoryFake{authority: authority}
	usecase := arena.NewPrivateTaskDeliveryUseCase(repository)

	view, changed, err := usecase.Deliver(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, command.ParticipantID, view.ParticipantID)
	require.Equal(t, command.ReceiptID, view.ReceiptID)
	require.Equal(t, authority.Snapshot.Snapshot().SnapshotID, view.SnapshotID)

	opponent, ok := authority.Snapshot.InstanceFor(authority.Snapshot.Instances()[1].ParticipantID)
	require.True(t, ok)
	serialized := fmt.Sprintf("%+v", view)
	require.NotContains(t, serialized, authority.Snapshot.Snapshot().Flag)
	require.NotContains(t, serialized, opponent.InstanceID.String())

	refresh := command
	refresh.ReceiptID = task034ID(55)
	refreshed, changed, err := usecase.Deliver(t.Context(), refresh)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, view, refreshed)
	require.Equal(t, 1, repository.commitCount())
}

func TestPrivateTaskDeliveryReconcilesConcurrentRefresh(t *testing.T) {
	t.Parallel()

	authority, command := privateTaskDeliveryFixture(t)
	startedAt := command.RequestedAt.Add(-time.Second)
	authority.StartedAt = &startedAt
	repository := &privateTaskDeliveryRepositoryFake{authority: authority, conflicts: 1}

	view, changed, err := arena.NewPrivateTaskDeliveryUseCase(repository).Deliver(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, command.ReceiptID, view.ReceiptID)
	require.Equal(t, 2, repository.loadCount())
	require.Equal(t, 2, repository.commitCount())
}

type privateTaskDeliveryRepositoryFake struct {
	mu        sync.Mutex
	authority arena.PrivateTaskDeliveryAuthority
	conflicts int
	loads     int
	commits   int
}

func (r *privateTaskDeliveryRepositoryFake) LoadPrivateTaskDeliveryAuthority(
	_ context.Context,
	scope arena.PrivateTaskDeliveryScope,
) (arena.PrivateTaskDeliveryAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loads++
	if r.authority.Scope != scope {
		return arena.PrivateTaskDeliveryAuthority{}, errors.New("unknown assignment")
	}
	return clonePrivateTaskDeliveryAuthority(r.authority), nil
}

func (r *privateTaskDeliveryRepositoryFake) CommitPrivateTaskDelivery(
	_ context.Context,
	commit arena.PrivateTaskDeliveryCommit,
) (*domain.ArenaDeliveryReceipt, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits++
	if r.conflicts > 0 {
		r.conflicts--
		return nil, false, domain.ErrConflict
	}
	if r.authority.Revision != commit.ExpectedRevision || r.authority.StartedAt == nil ||
		!r.authority.StartedAt.Equal(commit.ExpectedStartedAt) ||
		r.authority.Snapshot.ContentDigest() != commit.ContentDigest {
		return nil, false, domain.ErrConflict
	}
	for index := range r.authority.Receipts {
		receipt := &r.authority.Receipts[index]
		if receipt.ParticipantID == commit.Receipt.ParticipantID {
			cloned := *receipt
			return &cloned, false, nil
		}
	}
	receipt := commit.Receipt
	r.authority.Receipts = append(r.authority.Receipts, receipt)
	r.authority.Revision++
	return &receipt, true, nil
}

func (r *privateTaskDeliveryRepositoryFake) setStartedAt(startedAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authority.StartedAt = &startedAt
}

func (r *privateTaskDeliveryRepositoryFake) loadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loads
}

func (r *privateTaskDeliveryRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func privateTaskDeliveryFixture(
	t *testing.T,
) (arena.PrivateTaskDeliveryAuthority, arena.PrivateTaskDeliveryCommand) {
	t.Helper()

	snapshot, err := arena.BuildImmutableTaskSnapshot(immutableTaskSnapshotFixture())
	require.NoError(t, err)
	instances := snapshot.Instances()
	scope := arena.PrivateTaskDeliveryScope{
		AssignmentID: task034ID(40),
		AttemptID:    task034ID(41),
	}
	now := time.Date(2026, 8, 30, 18, 0, 0, 0, time.UTC)
	authority := arena.PrivateTaskDeliveryAuthority{
		Scope: scope, Revision: 1, Snapshot: snapshot,
	}
	command := arena.PrivateTaskDeliveryCommand{
		Scope: scope, ActorParticipantID: instances[0].ParticipantID,
		ParticipantID: instances[0].ParticipantID, ReceiptID: task034ID(42),
		RequestedAt: now,
	}
	return authority, command
}

func clonePrivateTaskDeliveryAuthority(
	authority arena.PrivateTaskDeliveryAuthority,
) arena.PrivateTaskDeliveryAuthority {
	cloned := authority
	cloned.Receipts = append([]domain.ArenaDeliveryReceipt(nil), authority.Receipts...)
	if authority.StartedAt != nil {
		startedAt := *authority.StartedAt
		cloned.StartedAt = &startedAt
	}
	return cloned
}

var _ arena.PrivateTaskDeliveryRepository = (*privateTaskDeliveryRepositoryFake)(nil)
