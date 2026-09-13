package game_test

import (
	"context"
	"sync"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

type task045RepositoryHarness struct {
	*gamemocks.MockReconnectRepository

	mu              sync.Mutex
	authority       gameusecase.ReconnectAuthority
	records         map[uuid.UUID]*gameusecase.ReconnectRecord
	writes          int
	commitCalls     int
	conflicts       int
	loads           int
	barrier         *task045Barrier
	alwaysConflict  bool
	mutateCommitted func(*gameusecase.ReconnectRecord)
	returned        *gameusecase.ReconnectRecord
	shareOwned      bool
}

func newTask045RepositoryHarness(
	t *testing.T,
	authority gameusecase.ReconnectAuthority,
) *task045RepositoryHarness {
	t.Helper()
	harness := &task045RepositoryHarness{
		authority: cloneTask045Authority(authority),
		records:   make(map[uuid.UUID]*gameusecase.ReconnectRecord),
	}
	repository := gamemocks.NewMockReconnectRepository(t)
	repository.EXPECT().
		FindCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.findReconnectCommand).
		Maybe()
	repository.EXPECT().
		LoadAuthority(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.loadReconnectAuthority).
		Maybe()
	repository.EXPECT().
		CommitMutation(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.commitReconnectMutation).
		Maybe()
	harness.MockReconnectRepository = repository
	return harness
}

func (f *task045RepositoryHarness) findReconnectCommand(_ context.Context, tournamentID, commandID uuid.UUID) (*gameusecase.ReconnectRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.records[commandID]
	if !ok || record.ReconnectAuthority.Scope.TournamentID != tournamentID {
		return nil, nil
	}
	if f.shareOwned {
		return record, nil
	}
	clone := cloneTask045Record(*record)
	return &clone, nil
}

func (f *task045RepositoryHarness) loadReconnectAuthority(ctx context.Context, _ pause.GraphScope, _ uuid.UUID) (gameusecase.ReconnectAuthority, error) {
	f.mu.Lock()
	f.loads++
	load := f.loads
	barrier := f.barrier
	clone := cloneTask045Authority(f.authority)
	f.mu.Unlock()
	if barrier != nil && load <= barrier.parties {
		if err := barrier.wait(ctx); err != nil {
			return gameusecase.ReconnectAuthority{}, err
		}
	}
	return clone, nil
}

func (f *task045RepositoryHarness) commitReconnectMutation(_ context.Context, expectedRevision int64, record gameusecase.ReconnectRecord) (*gameusecase.ReconnectRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	if f.alwaysConflict {
		f.conflicts++
		return nil, false, domain.ErrConflict
	}
	if f.authority.Revision != expectedRevision {
		f.conflicts++
		return nil, false, domain.ErrConflict
	}
	if record.ExpectedAuthorityRevision != expectedRevision || record.ReconnectAuthority.Revision != expectedRevision+1 {
		return nil, false, domain.ErrValidation
	}
	commandID := task045RecordCommandID(record)
	if commandID == uuid.Nil {
		return nil, false, domain.ErrValidation
	}
	stored := cloneTask045Record(record)
	f.authority = cloneTask045Authority(record.ReconnectAuthority)
	f.records[commandID] = &stored
	f.writes++
	if f.shareOwned {
		f.returned = f.records[commandID]
		return f.returned, true, nil
	}
	clone := cloneTask045Record(stored)
	if f.mutateCommitted != nil {
		f.mutateCommitted(&clone)
	}
	f.returned = &clone
	return f.returned, true, nil
}

func (f *task045RepositoryHarness) snapshot() gameusecase.ReconnectAuthority {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneTask045Authority(f.authority)
}

func (f *task045RepositoryHarness) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func (f *task045RepositoryHarness) conflictCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conflicts
}

func (f *task045RepositoryHarness) commitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commitCalls
}

func (f *task045RepositoryHarness) mutateReturned() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.returned != nil && len(f.returned.ReconnectAuthority.Presence) > 0 {
		f.returned.ReconnectAuthority.Presence[0].State = pause.PresenceStateDisconnected
		if f.returned.ScoreRevision != nil && len(f.returned.ScoreRevision.GameResultRevisionIDs) > 0 {
			f.returned.ScoreRevision.GameResultRevisionIDs[0] = domain.OfficialResultRevisionID(task045ID(998))
		}
	}
}

func (f *task045RepositoryHarness) corruptReceipt(commandID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := cloneTask045Record(*f.records[commandID])
	record.ReconnectAuthority.Game.State = domain.GameStateVoid
	f.records[commandID] = &record
}

func (f *task045RepositoryHarness) mutateReceipt(commandID uuid.UUID, mutate func(*gameusecase.ReconnectRecord)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := cloneTask045Record(*f.records[commandID])
	mutate(&record)
	f.records[commandID] = &record
}

type task045Barrier struct {
	parties int
	arrived chan struct{}
	release chan struct{}
}

func newTask045Barrier() *task045Barrier {
	const parties = 2
	return &task045Barrier{parties: parties, arrived: make(chan struct{}, parties), release: make(chan struct{})}
}

func (b *task045Barrier) wait(ctx context.Context) error {
	select {
	case b.arrived <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *task045Barrier) await(ctx context.Context) error {
	for range b.parties {
		select {
		case <-b.arrived:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	close(b.release)
	return nil
}
