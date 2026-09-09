package golden_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/mocks"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func task050PrestartFixture(
	t *testing.T,
	openedAt time.Time,
	base int,
) (goldenusecase.GoldenWaveExecution, goldenusecase.GoldenPrestartOperatorAuthorization, goldenusecase.RetainedGoldenPrestartPauseCommand) {
	t.Helper()
	state := prestartTask048GoldenState(t, openedAt)
	waveRepository := prestartNewGoldenWaveRepositoryHarness(t, state)
	execution := prestartTask048OpenGoldenExecution(t, waveRepository, state, openedAt, base)
	actorID := prestartTask049ID(base + 90)
	authorization, err := goldenusecase.NewGoldenPrestartOperatorAuthorization(
		execution.Scope.TournamentID, actorID, prestartTask049ID(base+91), 1,
	)
	require.NoError(t, err)
	command := goldenusecase.RetainedGoldenPrestartPauseCommand{
		Scope: execution.Scope, CommandID: prestartTask049ID(base + 92), SessionID: prestartTask049ID(base + 93),
		ActorID: actorID, Reason: goldenusecase.GoldenPrestartPauseOperatorManual,
		ExpectedExecution: execution.Expectation(), ExpectedAuthorization: authorization.Expectation(),
		NextSessionRevisionID: prestartTask049ID(base + 94),
	}
	return execution, authorization, command
}

func task050ResumeCommand(
	paused goldenusecase.RetainedGoldenPrestartRecord,
	authorization goldenusecase.GoldenPrestartOperatorAuthorization,
	actorID uuid.UUID,
	base int,
) goldenusecase.RetainedGoldenPrestartResumeCommand {
	return goldenusecase.RetainedGoldenPrestartResumeCommand{
		Scope: paused.Scope, CommandID: prestartTask049ID(base), SessionID: paused.SessionID,
		ActorID: actorID, ExpectedSession: paused.Expectation(), ExpectedAuthorization: authorization.Expectation(),
		NextSessionRevisionID: prestartTask049ID(base + 1), NextExecutionRevisionID: prestartTask049ID(base + 2),
		NextWaveRevisionID: domain.WaveRevisionID(prestartTask049ID(base + 3)), NextWindowID: prestartTask049ID(base + 4),
		NextWaveWindowRevisionID: domain.ReadyWindowRevisionID(prestartTask049ID(base + 5)),
		NextWindowRevisionID:     prestartTask049ID(base + 6), NextReadinessRevisionID: prestartTask049ID(base + 7),
		NextPresenceRevisionID: prestartTask049ID(base + 8),
	}
}

type task050RetainedPrestartRepositoryHarness struct {
	*goldenmocks.MockPrestartRepository

	mu            sync.Mutex
	execution     *goldenusecase.GoldenWaveExecution
	authorization goldenusecase.GoldenPrestartOperatorAuthorization
	archived      []goldenusecase.GoldenWaveExecution
	current       *goldenusecase.RetainedGoldenPrestartRecord
	replays       map[uuid.UUID]goldenusecase.RetainedGoldenPrestartRecord
	beforeCommit  func()
	returnRecord  *goldenusecase.RetainedGoldenPrestartRecord
	writes        int
}

func newTask050RetainedPrestartRepositoryHarness(
	t *testing.T,
	execution goldenusecase.GoldenWaveExecution,
	authorization goldenusecase.GoldenPrestartOperatorAuthorization,
) *task050RetainedPrestartRepositoryHarness {
	t.Helper()

	live := execution.Snapshot()
	harness := &task050RetainedPrestartRepositoryHarness{
		execution: &live, authorization: authorization,
		replays: make(map[uuid.UUID]goldenusecase.RetainedGoldenPrestartRecord),
	}
	repository := goldenmocks.NewMockPrestartRepository(t)
	repository.EXPECT().
		FindRetainedGoldenPrestartCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.findRetainedPrestartReplay).
		Maybe()
	repository.EXPECT().
		LoadRetainedGoldenPrestartAuthority(mock.Anything, mock.Anything).
		RunAndReturn(harness.loadRetainedPrestartSnapshot).
		Maybe()
	repository.EXPECT().
		CommitRetainedGoldenPrestart(mock.Anything, mock.Anything).
		RunAndReturn(harness.commitRetainedPrestartState).
		Maybe()
	harness.MockPrestartRepository = repository
	return harness
}

func (r *task050RetainedPrestartRepositoryHarness) findRetainedPrestartReplay(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*goldenusecase.RetainedGoldenPrestartRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, found := r.replays[commandID]
	if !found {
		return nil, nil
	}
	clone := record.Snapshot()
	return &clone, nil
}

func (r *task050RetainedPrestartRepositoryHarness) loadRetainedPrestartSnapshot(
	_ context.Context,
	_ goldenusecase.GoldenStateScope,
) (goldenusecase.RetainedGoldenPrestartAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	authority := goldenusecase.RetainedGoldenPrestartAuthority{Authorization: r.authorization}
	if r.execution != nil {
		execution := r.execution.Snapshot()
		authority.Execution = &execution
	}
	if len(r.archived) > 0 {
		execution := r.archived[len(r.archived)-1].Snapshot()
		authority.ArchivedExecution = &execution
	}
	if r.current != nil {
		current := r.current.Snapshot()
		authority.Current = &current
	}
	return authority, nil
}

func (r *task050RetainedPrestartRepositoryHarness) commitRetainedPrestartState(
	_ context.Context,
	commit goldenusecase.RetainedGoldenPrestartCommit,
) (*goldenusecase.RetainedGoldenPrestartRecord, bool, error) {
	if r.beforeCommit != nil {
		r.beforeCommit()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.authorization.Expectation().Equal(commit.ExpectedAuthorization) {
		return nil, false, domain.ErrConflict
	}
	if commit.ExpectedExecution == nil {
		if r.execution != nil {
			return nil, false, domain.ErrConflict
		}
	} else if r.execution == nil || !r.execution.Expectation().Equal(*commit.ExpectedExecution) {
		return nil, false, domain.ErrConflict
	}
	if commit.ExpectedSession == nil {
		if r.current != nil {
			return nil, false, domain.ErrConflict
		}
	} else if r.current == nil || !r.current.Expectation().Equal(*commit.ExpectedSession) {
		return nil, false, domain.ErrConflict
	}
	if commit.ExpectedArchivedExecution != nil {
		if len(r.archived) == 0 ||
			!r.archived[len(r.archived)-1].Expectation().Equal(*commit.ExpectedArchivedExecution) {
			return nil, false, domain.ErrConflict
		}
	} else if commit.PublishedExecution != nil {
		return nil, false, domain.ErrConflict
	}
	if _, duplicate := r.replays[commit.Record.CommandID]; duplicate {
		return nil, false, errors.New("duplicate retained prestart command")
	}
	if r.returnRecord != nil {
		clone := r.returnRecord.Snapshot()
		return &clone, false, nil
	}
	record := commit.Record.Snapshot()
	switch {
	case commit.ArchivedExecution != nil:
		if r.execution == nil || !r.execution.Expectation().Equal(commit.ArchivedExecution.Expectation()) ||
			commit.PublishedExecution != nil {
			return nil, false, errors.New("invalid retained pause transition")
		}
		r.archived = append(r.archived, commit.ArchivedExecution.Snapshot())
		r.execution = nil
	case commit.PublishedExecution != nil:
		if r.execution != nil || commit.PublishedExecution.Validate() != nil {
			return nil, false, errors.New("invalid retained resume transition")
		}
		live := commit.PublishedExecution.Snapshot()
		r.execution = &live
	default:
		return nil, false, errors.New("retained transition has no execution effect")
	}
	r.writes++
	r.current = &record
	r.replays[record.CommandID] = record.Snapshot()
	clone := record.Snapshot()
	return &clone, true, nil
}

func (r *task050RetainedPrestartRepositoryHarness) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func (r *task050RetainedPrestartRepositoryHarness) liveSnapshot() *goldenusecase.GoldenWaveExecution {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.execution == nil {
		return nil
	}
	clone := r.execution.Snapshot()
	return &clone
}

func (r *task050RetainedPrestartRepositoryHarness) archivedSnapshot() goldenusecase.GoldenWaveExecution {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.archived[len(r.archived)-1].Snapshot()
}

func (r *task050RetainedPrestartRepositoryHarness) currentSnapshot() goldenusecase.RetainedGoldenPrestartRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Snapshot()
}

func prestartNewGoldenClock(t *testing.T, now time.Time) *goldenmocks.MockPrestartClock {
	t.Helper()
	clock := goldenmocks.NewMockPrestartClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func prestartTask049Encode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

func prestartTask049TwoPartyBarrier(t *testing.T) func() {
	t.Helper()
	var mu sync.Mutex
	arrived := 0
	release := make(chan struct{})
	timedOut := make(chan struct{})
	timer := time.AfterFunc(2*time.Second, func() { close(timedOut) })
	return func() {
		mu.Lock()
		if arrived >= 2 {
			mu.Unlock()
			return
		}
		arrived++
		if arrived == 2 {
			timer.Stop()
			close(release)
		}
		mu.Unlock()
		select {
		case <-release:
		case <-timedOut:
			t.Errorf("two-party repository barrier timed out")
		}
	}
}
