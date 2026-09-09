package authority_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
	authoritymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority/mocks"
)

type mutableAuthorityClock struct {
	mu   sync.Mutex
	now  time.Time
	mock *authoritymocks.MockClock
}

type mutableAuthorityTimeSource struct {
	mu   sync.Mutex
	now  time.Time
	mock *authoritymocks.MockTimeSource
}

func newMutableAuthorityTimeSource(t *testing.T, now time.Time) *mutableAuthorityTimeSource {
	t.Helper()
	state := &mutableAuthorityTimeSource{now: now}
	source := authoritymocks.NewMockTimeSource(t)
	source.EXPECT().AuthorityTime(mock.Anything).RunAndReturn(state.current).Maybe()
	state.mock = source
	return state
}

func (c *mutableAuthorityTimeSource) current(context.Context) (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now, nil
}

func (c *mutableAuthorityTimeSource) set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func newMutableAuthorityClock(t *testing.T, now time.Time) *mutableAuthorityClock {
	t.Helper()
	state := &mutableAuthorityClock{now: now}
	clock := authoritymocks.NewMockClock(t)
	clock.EXPECT().Now().RunAndReturn(state.current).Maybe()
	state.mock = clock
	return state
}

func (c *mutableAuthorityClock) current() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableAuthorityClock) set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

type authorityClaimResult struct {
	lease   *authoritydomain.Lease
	changed bool
	err     error
}

type authorityRepositoryOptions struct {
	loadBarrier         *sync.WaitGroup
	transactionNow      time.Time
	advanceBeforeCommit time.Duration
}

type authorityRepositoryState struct {
	mu                  sync.Mutex
	current             *authoritydomain.Lease
	commands            map[task042AuthorityCommandKey]authoritydomain.Lease
	finds               int
	loads               int
	writes              int
	commits             int
	loadBarrier         *sync.WaitGroup
	barrierLoads        int
	transactionNow      time.Time
	advanceBeforeCommit time.Duration
}

type authorityRepositoryHarness struct {
	*authoritymocks.MockRepository

	state *authorityRepositoryState
}

type task042AuthorityCommandKey struct {
	tournamentID uuid.UUID
	commandID    uuid.UUID
}

func newAuthorityRepositoryHarness(
	t *testing.T,
	options authorityRepositoryOptions,
) *authorityRepositoryHarness {
	t.Helper()
	state := &authorityRepositoryState{
		loadBarrier:         options.loadBarrier,
		transactionNow:      options.transactionNow,
		advanceBeforeCommit: options.advanceBeforeCommit,
	}
	repository := authoritymocks.NewMockRepository(t)
	repository.EXPECT().
		FindAuthorityCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(state.findCommand).
		Maybe()
	repository.EXPECT().
		LoadAuthority(mock.Anything, mock.Anything).
		RunAndReturn(state.loadAuthority).
		Maybe()
	repository.EXPECT().
		CommitAuthority(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(state.commitAuthority).
		Maybe()
	return &authorityRepositoryHarness{
		MockRepository: repository,
		state:          state,
	}
}

func (s *authorityRepositoryState) findCommand(
	_ context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*authoritydomain.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finds++
	recorded, exists := s.commands[task042AuthorityCommandKey{
		tournamentID: tournamentID,
		commandID:    commandID,
	}]
	if !exists {
		return nil, nil
	}
	clone := task042CloneLease(recorded)
	return &clone, nil
}

func (s *authorityRepositoryState) loadAuthority(
	_ context.Context,
	_ uuid.UUID,
) (*authoritydomain.Lease, error) {
	s.mu.Lock()
	s.loads++
	wait := s.loadBarrier != nil && s.barrierLoads < 2
	if wait {
		s.barrierLoads++
	}
	var current *authoritydomain.Lease
	if s.current != nil {
		clone := task042CloneLease(*s.current)
		current = &clone
	}
	s.mu.Unlock()
	if wait {
		s.loadBarrier.Done()
		s.loadBarrier.Wait()
	}
	return current, nil
}

func (s *authorityRepositoryState) commitAuthority(
	_ context.Context,
	condition authorityusecase.CommitCondition,
	lease authoritydomain.Lease,
) (*authoritydomain.Lease, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits++
	transactionNow := s.transactionNow
	if transactionNow.IsZero() {
		transactionNow = lease.RenewedAt
	}
	if s.advanceBeforeCommit != 0 {
		transactionNow = transactionNow.Add(s.advanceBeforeCommit)
		s.transactionNow = transactionNow
		s.advanceBeforeCommit = 0
	}
	if !task042AuthorityConditionMatches(condition, s.current, transactionNow) ||
		!lease.Proves(lease.Identity(), transactionNow) {
		return nil, false, domain.ErrConflict
	}
	stored := task042CloneLease(lease)
	s.current = &stored
	if s.commands == nil {
		s.commands = make(map[task042AuthorityCommandKey]authoritydomain.Lease)
	}
	s.commands[task042AuthorityCommandKey{
		tournamentID: lease.TournamentID,
		commandID:    lease.CommandID,
	}] = task042CloneLease(lease)
	s.writes++
	result := task042CloneLease(stored)
	return &result, true, nil
}

func (h *authorityRepositoryHarness) loadCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.loads
}

func (h *authorityRepositoryHarness) findCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.finds
}

func (h *authorityRepositoryHarness) writeCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.writes
}

func (h *authorityRepositoryHarness) commitCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.commits
}

func (h *authorityRepositoryHarness) replaceCurrent(
	lease authoritydomain.Lease,
) {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	stored := task042CloneLease(lease)
	h.state.current = &stored
}

func task042Lease(
	now time.Time,
	tournamentID uuid.UUID,
	epoch int64,
) authoritydomain.Lease {
	return authoritydomain.Lease{
		TournamentID: tournamentID, HolderID: task042ID(60), LeaseID: task042ID(61),
		Epoch: epoch, ProcessKind: authoritydomain.ProcessAuthority,
		Revision: 4, CommandID: task042ID(62),
		Previous:   &authoritydomain.Stamp{LeaseID: task042ID(63), Epoch: epoch - 1},
		AcquiredAt: now.Add(-time.Second), RenewedAt: now.Add(-time.Second),
		ExpiresAt: now.Add(time.Minute),
	}
}

func task042CloneLease(
	lease authoritydomain.Lease,
) authoritydomain.Lease {
	clone := lease
	if lease.Previous != nil {
		previous := *lease.Previous
		clone.Previous = &previous
	}
	return clone
}

func task042AuthorityConditionMatches(
	condition authorityusecase.CommitCondition,
	current *authoritydomain.Lease,
	transactionNow time.Time,
) bool {
	if condition.Validate() != nil {
		return false
	}
	if current == nil {
		return condition.ExpectedState == authorityusecase.ExpectedAbsent
	}
	if current.Validate() != nil || condition.ExpectedRevision != current.Revision ||
		condition.ExpectedStamp != *current.Stamp() {
		return false
	}
	switch condition.ExpectedState {
	case authorityusecase.ExpectedAbsent:
		return false
	case authorityusecase.ExpectedLive:
		return current.Proves(current.Identity(), transactionNow)
	case authorityusecase.ExpectedExpired:
		return !transactionNow.IsZero() && transactionNow.Location() == time.UTC &&
			!transactionNow.Before(current.ExpiresAt)
	default:
		return false
	}
}

func task042Barrier(size int) *sync.WaitGroup {
	barrier := &sync.WaitGroup{}
	barrier.Add(size)
	return barrier
}

func task042ID(value int) uuid.UUID {
	name := fmt.Sprintf("task-042-%s", time.Unix(int64(value), 0).UTC().Format(time.RFC3339))
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name))
}
