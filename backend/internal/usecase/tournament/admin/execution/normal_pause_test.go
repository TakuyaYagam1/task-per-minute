package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func TestExecutionWorkflowNormalWavePauseResumeIsAtomicAndReplayable(t *testing.T) {
	t.Parallel()

	fixture := newExecutionNormalPauseFixture(t)
	transactions := &executionNormalPauseTransactions{}
	workflow := NewExecutionWorkflow(ExecutionWorkflowDependencies{
		Transactions: transactions, Repository: fixture, NormalPause: fixture,
		Authority: executionNormalPauseAuthority{identity: fixture.identity},
	})
	pause := fixture.command(WaveActionPause, executionTestID(702))
	paused, err := workflow.ControlWave(t.Context(), pause)
	if err != nil {
		t.Fatalf("ControlWave(pause) error = %v", err)
	}
	if paused.Wave.State != domain.WaveStatePaused || fixture.pauseWrites != 1 || transactions.physical != 1 {
		t.Fatalf("pause result = %+v, writes = %d, transactions = %d", paused, fixture.pauseWrites, transactions.physical)
	}

	replayed, err := workflow.ControlWave(t.Context(), pause)
	if err != nil || replayed.Wave.State != domain.WaveStatePaused || fixture.pauseWrites != 1 || transactions.physical != 1 {
		t.Fatalf("pause replay = %+v, error = %v, writes = %d, transactions = %d", replayed, err, fixture.pauseWrites, transactions.physical)
	}

	resume := fixture.command(WaveActionResume, executionTestID(703))
	resumed, err := workflow.ControlWave(t.Context(), resume)
	if err != nil {
		t.Fatalf("ControlWave(resume) error = %v", err)
	}
	if resumed.Wave.State != domain.WaveStateActive || fixture.resumeWrites != 1 || transactions.physical != 2 {
		t.Fatalf("resume result = %+v, writes = %d, transactions = %d", resumed, fixture.resumeWrites, transactions.physical)
	}
	duplicate, err := workflow.ControlWave(t.Context(), resume)
	if err != nil || duplicate.Wave.State != domain.WaveStateActive || fixture.resumeWrites != 1 || transactions.physical != 2 {
		t.Fatalf("resume replay = %+v, error = %v, writes = %d, transactions = %d", duplicate, err, fixture.resumeWrites, transactions.physical)
	}
}

func TestExecutionWorkflowNormalWavePauseResumeSupportsPlayoffs(t *testing.T) {
	t.Parallel()

	fixture := newExecutionNormalPauseFixture(t)
	fixture.current.TournamentState = domain.TournamentStatePlayoffs
	fixture.normalAuthority.Graph.Tournament.State = domain.TournamentStatePlayoffs
	fixture.normalAuthority.Revisions = gameusecase.PauseGraphRevisionsFrom(fixture.normalAuthority.Graph)
	workflow := NewExecutionWorkflow(ExecutionWorkflowDependencies{
		Transactions: &executionNormalPauseTransactions{}, Repository: fixture, NormalPause: fixture,
		Authority: executionNormalPauseAuthority{identity: fixture.identity},
	})
	if _, err := workflow.ControlWave(t.Context(), fixture.command(WaveActionPause, executionTestID(707))); err != nil {
		t.Fatalf("ControlWave(playoffs pause) error = %v", err)
	}
	if _, err := workflow.ControlWave(t.Context(), fixture.command(WaveActionResume, executionTestID(708))); err != nil {
		t.Fatalf("ControlWave(playoffs resume) error = %v", err)
	}
	if fixture.current.TournamentState != domain.TournamentStatePlayoffs || fixture.current.View.Wave.State != domain.WaveStateActive {
		t.Fatalf("playoffs resume state = %s/%s", fixture.current.TournamentState, fixture.current.View.Wave.State)
	}
}

func TestExecutionWorkflowNormalWavePauseCanonicalizesSubMillisecondDeadline(t *testing.T) {
	t.Parallel()

	fixture := newExecutionNormalPauseFixture(t)
	deadline := fixture.now.Add(time.Minute + 750*time.Microsecond)
	fixture.normalAuthority.Graph.Games[0].Deadline = &deadline
	fixture.normalAuthority.Revisions = gameusecase.PauseGraphRevisionsFrom(fixture.normalAuthority.Graph)
	workflow := NewExecutionWorkflow(ExecutionWorkflowDependencies{
		Transactions: &executionNormalPauseTransactions{}, Repository: fixture, NormalPause: fixture,
		Authority: executionNormalPauseAuthority{identity: fixture.identity},
	})
	if _, err := workflow.ControlWave(t.Context(), fixture.command(WaveActionPause, executionTestID(709))); err != nil {
		t.Fatalf("ControlWave(sub-millisecond pause) error = %v", err)
	}
	frozen := fixture.resumeAuthority.Pause.Graph.FrozenDeadlines[0]
	if frozen.Remaining != time.Minute+time.Millisecond || !frozen.OriginalDeadline.Equal(fixture.now.Add(time.Minute+time.Millisecond)) {
		t.Fatalf("canonical frozen deadline = %+v", frozen)
	}
	if _, err := workflow.ControlWave(t.Context(), fixture.command(WaveActionResume, executionTestID(710))); err != nil {
		t.Fatalf("ControlWave(sub-millisecond resume) error = %v", err)
	}
}

func TestExecutionWorkflowNormalWavePauseResumeActiveDraft(t *testing.T) {
	t.Parallel()

	fixture := newExecutionNormalPauseFixture(t)
	series := &fixture.normalAuthority.Graph.Series[0]
	series.Execution.Series.State = domain.SeriesStateDraft
	series.CurrentGameID = nil
	fixture.normalAuthority.Graph.Games = nil
	fixture.normalAuthority.Graph.Draft = executionNormalPauseDraft(t, fixture, series.Execution.Series)
	series.Execution.Series.FirstParticipantID = fixture.normalAuthority.Graph.Draft.FirstParticipantID
	series.Execution.Series.SecondParticipantID = fixture.normalAuthority.Graph.Draft.SecondParticipantID
	fixture.normalAuthority.Revisions = gameusecase.PauseGraphRevisionsFrom(fixture.normalAuthority.Graph)
	fixture.current.Graph.CurrentGameCount, fixture.current.Graph.ActiveGameCount = 0, 0
	workflow := NewExecutionWorkflow(ExecutionWorkflowDependencies{
		Transactions: &executionNormalPauseTransactions{}, Repository: fixture, NormalPause: fixture,
		Authority: executionNormalPauseAuthority{identity: fixture.identity},
	})
	if _, err := workflow.ControlWave(t.Context(), fixture.command(WaveActionPause, executionTestID(730))); err != nil {
		t.Fatalf("ControlWave(Draft pause) error = %v", err)
	}
	paused := fixture.resumeAuthority.Pause.Graph.Draft
	if paused == nil || paused.State != draftusecase.ExecutionStatePaused || paused.AbsoluteDeadline != nil ||
		len(fixture.resumeAuthority.FrozenDeadlines) != 1 || fixture.resumeAuthority.FrozenDeadlines[0].Kind != gameusecase.PauseDeadlineDraft {
		t.Fatalf("paused Draft = %+v, frozen = %+v", paused, fixture.resumeAuthority.FrozenDeadlines)
	}
	fixture.now = fixture.now.Add(time.Second)
	if _, err := workflow.ControlWave(t.Context(), fixture.command(WaveActionResume, executionTestID(731))); err != nil {
		t.Fatalf("ControlWave(Draft resume) error = %v", err)
	}
}

func TestExecutionWorkflowNormalWavePauseResumeReadyWindow(t *testing.T) {
	t.Parallel()

	fixture := newExecutionNormalPauseFixture(t)
	wave := &fixture.normalAuthority.Graph.Wave.Wave
	wave.State = domain.WaveStateReadyWindowOpen
	wave.StartedAt = nil
	wave.PausedAt = nil
	wave.ReadyWindow.State = domain.ReadyWindowStateOpen
	wave.ReadyWindow.ConsumedAt = nil
	wave.ReadyWindow.Deadline = fixture.now.Add(45 * time.Second)
	for index := range wave.Members {
		wave.Members[index].Ready = false
	}
	series := &fixture.normalAuthority.Graph.Series[0]
	series.Execution.Series.State = domain.SeriesStateReady
	series.CurrentGameID = nil
	fixture.normalAuthority.Graph.Games = nil
	fixture.normalAuthority.Revisions = gameusecase.PauseGraphRevisionsFrom(fixture.normalAuthority.Graph)
	fixture.current.View.Wave = *wave
	fixture.current.Graph.CurrentGameCount, fixture.current.Graph.ActiveGameCount = 0, 0
	workflow := NewExecutionWorkflow(ExecutionWorkflowDependencies{
		Transactions: &executionNormalPauseTransactions{}, Repository: fixture, NormalPause: fixture,
		Authority: executionNormalPauseAuthority{identity: fixture.identity},
	})
	if _, err := workflow.ControlWave(t.Context(), fixture.command(WaveActionPause, executionTestID(732))); err != nil {
		t.Fatalf("ControlWave(ready-window pause) error = %v", err)
	}
	if frozen := fixture.resumeAuthority.FrozenDeadlines; len(frozen) != 1 || frozen[0].Kind != gameusecase.PauseDeadlineReadyWindow || frozen[0].Remaining != 45*time.Second {
		t.Fatalf("ready-window frozen deadline = %+v", frozen)
	}
	fixture.now = fixture.now.Add(5 * time.Second)
	resumed, err := workflow.ControlWave(t.Context(), fixture.command(WaveActionResume, executionTestID(733)))
	if err != nil {
		t.Fatalf("ControlWave(ready-window resume) error = %v", err)
	}
	if resumed.Wave.State != domain.WaveStateReadyWindowOpen || resumed.Wave.ReadyWindow == nil ||
		!resumed.Wave.ReadyWindow.Deadline.Equal(fixture.now.Add(45*time.Second)) {
		t.Fatalf("resumed ready window = %+v", resumed.Wave)
	}
}

func TestExecutionWorkflowNormalWavePauseRejectsStaleAndResumesAroundDisconnectedGame(t *testing.T) {
	t.Parallel()

	fixture := newExecutionNormalPauseFixture(t)
	transactions := &executionNormalPauseTransactions{}
	workflow := NewExecutionWorkflow(ExecutionWorkflowDependencies{
		Transactions: transactions, Repository: fixture, NormalPause: fixture,
		Authority: executionNormalPauseAuthority{identity: fixture.identity},
	})
	stale := fixture.command(WaveActionPause, executionTestID(704))
	stale.ExpectedProjectionRevision--
	if _, err := workflow.ControlWave(t.Context(), stale); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale pause error = %v, want conflict", err)
	}
	if fixture.pauseWrites != 0 {
		t.Fatalf("stale pause writes = %d, want 0", fixture.pauseWrites)
	}

	pause := fixture.command(WaveActionPause, executionTestID(705))
	if _, err := workflow.ControlWave(t.Context(), pause); err != nil {
		t.Fatalf("pause setup error = %v", err)
	}
	disconnectedAt := fixture.now
	fixture.resumeAuthority.Presence[0].State = pausedomain.PresenceStateDisconnected
	fixture.resumeAuthority.Presence[0].DisconnectedAt = &disconnectedAt
	fixture.resumeAuthority.Presence[0].UpdatedAt = disconnectedAt
	fixture.resumeAuthority.Presence[0].PresenceEpoch++
	fixture.resumeAuthority.Presence[0].Revision++
	fixture.now = fixture.now.Add(time.Second)
	resume := fixture.command(WaveActionResume, executionTestID(706))
	resumed, err := workflow.ControlWave(t.Context(), resume)
	if err != nil {
		t.Fatalf("disconnected resume error = %v", err)
	}
	if fixture.resumeWrites != 1 || resumed.Wave.State != domain.WaveStateActive ||
		fixture.current.Graph.PausedGameCount != 1 || fixture.current.Graph.ActiveGameCount != 0 {
		t.Fatalf("disconnected resume writes = %d, Wave = %s, graph = %+v", fixture.resumeWrites, resumed.Wave.State, fixture.current.Graph)
	}
}

func TestExecutionWorkflowNormalWaveResumeContinuesSuspendedReconnect(t *testing.T) {
	t.Parallel()

	fixture := newExecutionNormalPauseFixture(t)
	participant := &fixture.normalAuthority.Graph.Presence[0]
	disconnectedAt := fixture.now.Add(-40 * time.Second)
	participant.State = pausedomain.PresenceStateDisconnected
	participant.DisconnectedAt = &disconnectedAt
	participant.UpdatedAt = disconnectedAt
	earlierPauseID := executionTestID(741)
	game := fixture.normalAuthority.Graph.Games[0]
	sourceID := executionTestID(742)
	fixture.normalAuthority.Graph.Counters = []pausedomain.PauseReconnectCounter{{
		PauseID: earlierPauseID, RosterID: fixture.normalAuthority.Scope.RosterID,
		ParticipantID: participant.ParticipantID, Limit: domain.ReconnectCycleLimit, Used: 1, Revision: 1,
	}}
	fixture.normalAuthority.Graph.Reconnect = []pausedomain.PauseReconnectInterval{{
		ID: sourceID, PauseID: earlierPauseID, RosterID: fixture.normalAuthority.Scope.RosterID,
		SeriesID: participant.SeriesID, GameID: game.Game.ID, ParticipantID: participant.ParticipantID,
		PresenceEpoch: participant.PresenceEpoch, Number: 1, State: pausedomain.ReconnectStateOpen,
		OpenedAt: fixture.now.Add(-30 * time.Second), Deadline: fixture.now.Add(30 * time.Second),
		Revision: 1, UpdatedAt: fixture.now.Add(-30 * time.Second),
	}}
	fixture.normalAuthority.Revisions = gameusecase.PauseGraphRevisionsFrom(fixture.normalAuthority.Graph)
	workflow := NewExecutionWorkflow(ExecutionWorkflowDependencies{
		Transactions: &executionNormalPauseTransactions{}, Repository: fixture, NormalPause: fixture,
		Authority: executionNormalPauseAuthority{identity: fixture.identity},
	})
	if _, err := workflow.ControlWave(t.Context(), fixture.command(WaveActionPause, executionTestID(743))); err != nil {
		t.Fatalf("ControlWave(pause) error = %v", err)
	}
	fixture.now = fixture.now.Add(5 * time.Second)
	resumed, err := workflow.ControlWave(t.Context(), fixture.command(WaveActionResume, executionTestID(744)))
	if err != nil {
		t.Fatalf("ControlWave(resume continuation) error = %v, writes = %d, record = %+v", err, fixture.resumeWrites, fixture.presenceRecord)
	}
	record := fixture.presenceRecord
	if record == nil || record.First.Disposition != gameusecase.PauseResumeParticipantContinuation ||
		record.First.SourceInterval == nil || record.First.SourceInterval.ID != sourceID ||
		record.First.CurrentInterval == nil || !record.First.CurrentInterval.Deadline.Equal(fixture.now.Add(30*time.Second)) ||
		record.First.Counter.Used != 1 || record.First.Counter.Revision != 1 {
		t.Fatalf("continuation record = %+v", record)
	}
	if resumed.Wave.State != domain.WaveStateActive || fixture.current.TournamentState != domain.TournamentStateSwiss ||
		fixture.current.Graph.PausedGameCount != 1 || fixture.current.Graph.PausedSeriesCount != 1 {
		t.Fatalf("partial resume = wave %s, tournament %s, graph %+v", resumed.Wave.State, fixture.current.TournamentState, fixture.current.Graph)
	}
}

type executionNormalPauseTxKey struct{}

type executionNormalPauseTransactions struct{ physical int }

func (transactions *executionNormalPauseTransactions) Do(ctx context.Context, run func(context.Context) error) error {
	if ctx.Value(executionNormalPauseTxKey{}) != nil {
		return run(ctx)
	}
	transactions.physical++
	return run(context.WithValue(ctx, executionNormalPauseTxKey{}, true))
}

type executionNormalPauseAuthority struct{ identity authoritydomain.Identity }

func (authority executionNormalPauseAuthority) AuthorityFor(context.Context, uuid.UUID) (authoritydomain.Identity, error) {
	return authority.identity, nil
}

type executionNormalPauseFixture struct {
	now             time.Time
	identity        authoritydomain.Identity
	current         WaveAuthority
	normalAuthority gameusecase.NormalPauseAuthority
	resumeAuthority gameusecase.PauseResumeAuthority
	receipts        map[uuid.UUID]WaveCommandRecord
	pauseWrites     int
	resumeWrites    int
	presenceRecord  *gameusecase.PauseResumePresenceRecord
}

func newExecutionNormalPauseFixture(t *testing.T) *executionNormalPauseFixture {
	t.Helper()
	now := executionTestTime()
	tournamentID, rosterID, waveID := executionTestID(710), executionTestID(711), executionTestID(712)
	firstID, secondID := executionTestID(713), executionTestID(714)
	seriesID, slotID, gameID := executionTestID(715), executionTestID(716), executionTestID(717)
	startedAt := now.Add(-time.Minute)
	windowOpenedAt := startedAt.Add(-time.Minute)
	windowConsumedAt := startedAt
	deadline := now.Add(time.Minute)
	wave := domain.Wave{
		ID: waveID, TournamentID: tournamentID, RevisionID: domain.WaveRevisionID(executionTestID(718)),
		State: domain.WaveStateActive, Members: []domain.WaveMember{{ParticipantID: firstID, Ready: true}, {ParticipantID: secondID, Ready: true}},
		ReadyWindow: &domain.ReadyWindow{ID: executionTestID(719), WaveID: waveID,
			RevisionID: domain.ReadyWindowRevisionID(executionTestID(720)), State: domain.ReadyWindowStateConsumed,
			OpenedAt: windowOpenedAt, Deadline: startedAt, ConsumedAt: &windowConsumedAt},
		StartedAt: &startedAt,
	}
	game := domain.Game{ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStateActive}
	series := domain.Series{ID: seriesID, TournamentID: tournamentID, FirstParticipantID: firstID,
		SecondParticipantID: secondID, Format: domain.SeriesFormatBO1, State: domain.SeriesStateActive,
		Slots: []domain.GameSlot{{ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb, Attempts: []domain.Game{game}}}}
	identity := authoritydomain.Identity{TournamentID: tournamentID, HolderID: executionTestID(721), LeaseID: executionTestID(722), Epoch: 2, ProcessKind: authoritydomain.ProcessAuthority}
	scope := pausedomain.GraphScope{TournamentID: tournamentID, RosterID: rosterID, WaveID: waveID, Authority: identity}
	graph := gameusecase.PauseGraph{Scope: scope, Revision: 1,
		Tournament: gameusecase.TournamentRecord{ID: tournamentID, RosterID: rosterID, Preset: domain.TournamentPresetV1,
			State: domain.TournamentStateSwiss, Revision: 4, RosterSize: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: startedAt, StartedAt: &startedAt},
		Wave:   gameusecase.PauseWave{Wave: wave, Revision: 7},
		Series: []gameusecase.PauseSeries{{Execution: seriesdomain.Execution{Series: series}, Revision: 5, CurrentGameID: &gameID}},
		Games:  []gameusecase.PauseGame{{SeriesID: seriesID, Game: game, Revision: 6, Deadline: &deadline}},
		Presence: []pausedomain.PausePresence{
			{ID: executionTestID(723), TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: firstID, State: pausedomain.PresenceStateConnected, PresenceEpoch: 1, Revision: 1, ConnectedAt: startedAt, UpdatedAt: startedAt},
			{ID: executionTestID(724), TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: secondID, State: pausedomain.PresenceStateConnected, PresenceEpoch: 1, Revision: 1, ConnectedAt: startedAt, UpdatedAt: startedAt},
		},
	}
	revisions := gameusecase.PauseGraphRevisionsFrom(graph)
	view := WaveView{Wave: wave, Revision: 7, ReadinessRevisions: map[uuid.UUID]int64{firstID: 1, secondID: 1}, SeriesIDs: map[uuid.UUID]uuid.UUID{firstID: seriesID, secondID: seriesID}}
	current := WaveAuthority{TournamentState: domain.TournamentStateSwiss, TournamentRevision: 4,
		RosterID: rosterID, RosterRevision: 2, ProjectionRevisionID: executionTestID(725), ProjectionRevision: 8,
		SourceRevisions: domain.ReadyWindowSourceRevisions{WaveRevisionID: wave.RevisionID, WaveRevision: 7, ProjectionRevisionID: executionTestID(725), ProjectionRevision: 8},
		View:            view, Graph: WaveGraph{SeriesCount: 1, PlayableMemberCount: 2, CurrentGameCount: 1, ActiveSeriesCount: 1, ActiveGameCount: 1}}
	return &executionNormalPauseFixture{now: now, identity: identity, current: current,
		normalAuthority: gameusecase.NormalPauseAuthority{Scope: scope, Revisions: revisions, Graph: graph, Complete: true},
		receipts:        make(map[uuid.UUID]WaveCommandRecord)}
}

func executionNormalPauseDraft(
	t *testing.T,
	fixture *executionNormalPauseFixture,
	series domain.Series,
) *draftusecase.Execution {
	t.Helper()
	revision := draftusecase.CategoryRevision{
		ID: executionTestID(734), TournamentID: fixture.current.View.Wave.TournamentID,
		SeriesID: series.ID, RosterID: fixture.current.RosterID, Revision: 1,
		Stage: domain.TournamentStageSwiss, Format: series.Format, Mode: domain.CategoryModeDraft,
		SourceContentRevision: 1,
		CategoryPool: domain.CategoryPoolRevision{
			ID: executionTestID(735), Revision: 1, Format: series.Format,
			Categories: []domain.Category{domain.CategoryCrypto, domain.CategoryReverse, domain.CategoryWeb},
		},
		CreatedAt: fixture.now.Add(-time.Minute),
	}
	draft, err := draftusecase.StartExecution(draftusecase.ExecutionStartCommand{
		CategoryRevision: revision, DraftID: executionTestID(736), InitialRevisionID: executionTestID(737),
		DecisionEvidenceID: executionTestID(738),
		ParticipantIDs:     [2]uuid.UUID{series.FirstParticipantID, series.SecondParticipantID},
		ServiceEpoch:       executionTestID(739), CommandID: executionTestID(740), StartedAt: fixture.now.Add(-time.Second),
	})
	if err != nil {
		t.Fatalf("StartExecution() error = %v", err)
	}
	return &draft
}

func (fixture *executionNormalPauseFixture) command(action WaveAction, commandID uuid.UUID) WaveCommand {
	return WaveCommand{CommandScope: CommandScope{Operator: OperatorIdentity{ActorID: executionTestID(726)}, TournamentID: fixture.current.View.Wave.TournamentID, CommandID: commandID},
		WaveID: fixture.current.View.Wave.ID, ExpectedProjectionRevision: fixture.current.ProjectionRevision, Action: action, Confirmed: true}
}

func (fixture *executionNormalPauseFixture) LockWaveAuthority(context.Context, uuid.UUID, uuid.UUID) (WaveAuthority, error) {
	return fixture.current, nil
}
func (fixture *executionNormalPauseFixture) FindWaveCommand(_ context.Context, _ uuid.UUID, id uuid.UUID) (*WaveCommandRecord, error) {
	record, ok := fixture.receipts[id]
	if !ok {
		return nil, nil
	}
	return &record, nil
}
func (fixture *executionNormalPauseFixture) ReadExecutionTime(context.Context) (time.Time, error) {
	return fixture.now, nil
}
func (fixture *executionNormalPauseFixture) SaveWaveCommand(_ context.Context, record WaveCommandRecord) error {
	fixture.receipts[record.CommandID] = record
	return nil
}
func (*executionNormalPauseFixture) LockPairingAuthority(context.Context, uuid.UUID) (PairingAuthority, error) {
	return PairingAuthority{}, domain.ErrInternal
}
func (*executionNormalPauseFixture) FindPairingCommand(context.Context, uuid.UUID, uuid.UUID) (*PairingCommandRecord, error) {
	return nil, domain.ErrInternal
}
func (*executionNormalPauseFixture) CommitPairing(context.Context, PairingPlan) (SwissRoundView, error) {
	return SwissRoundView{}, domain.ErrInternal
}
func (*executionNormalPauseFixture) SavePairingCommand(context.Context, PairingCommandRecord) error {
	return domain.ErrInternal
}
func (*executionNormalPauseFixture) CommitWave(context.Context, WaveMutation) (WaveView, error) {
	return WaveView{}, domain.ErrInternal
}
func (*executionNormalPauseFixture) FindNormalPauseCommand(context.Context, uuid.UUID, uuid.UUID) (*gameusecase.NormalPauseRecord, error) {
	return nil, nil
}
func (fixture *executionNormalPauseFixture) LoadNormalPauseAuthority(context.Context, pausedomain.GraphScope) (gameusecase.NormalPauseAuthority, error) {
	return fixture.normalAuthority, nil
}
func (fixture *executionNormalPauseFixture) CommitNormalPause(_ context.Context, _ gameusecase.PauseGraphRevisions, record gameusecase.NormalPauseRecord) (*gameusecase.NormalPauseRecord, bool, error) {
	fixture.pauseWrites++
	frozenDeadlines := append([]gameusecase.PauseFrozenDeadline(nil), record.Graph.FrozenDeadlines...)
	for index := range frozenDeadlines {
		frozenDeadlines[index].Remaining = time.Duration(frozenDeadlines[index].Remaining.Milliseconds()) * time.Millisecond
	}
	fixture.resumeAuthority = gameusecase.PauseResumeAuthority{
		Pause:           record,
		Presence:        append([]pausedomain.PausePresence(nil), record.Graph.Presence...),
		Reconnect:       append([]pausedomain.PauseReconnectInterval(nil), record.Graph.Reconnect...),
		Counters:        append([]pausedomain.PauseReconnectCounter(nil), record.Graph.Counters...),
		FrozenDeadlines: frozenDeadlines,
	}
	fixture.current.TournamentState = domain.TournamentStateTechnicalPause
	fixture.current.TournamentRevision++
	fixture.current.View.Wave = record.Graph.Wave.Wave
	fixture.current.View.Revision = record.Graph.Wave.Revision
	fixture.current.SourceRevisions.WaveRevision = fixture.current.View.Revision
	fixture.current.Graph.ActiveSeriesCount, fixture.current.Graph.ActiveGameCount = 0, 0
	fixture.current.Graph.PausedSeriesCount, fixture.current.Graph.PausedGameCount = 1, 1
	return &record, true, nil
}
func (*executionNormalPauseFixture) FindPauseResumeCommand(context.Context, uuid.UUID, uuid.UUID) (*gameusecase.PauseResumeRecord, error) {
	return nil, nil
}
func (*executionNormalPauseFixture) FindPauseResumePresenceCommand(context.Context, uuid.UUID, uuid.UUID) (*gameusecase.PauseResumePresenceRecord, error) {
	return nil, nil
}
func (fixture *executionNormalPauseFixture) LoadPauseResumeAuthority(context.Context, pausedomain.GraphScope, uuid.UUID) (gameusecase.PauseResumeAuthority, error) {
	return fixture.resumeAuthority, nil
}
func (fixture *executionNormalPauseFixture) CommitPauseResume(_ context.Context, _ gameusecase.PauseResumeExpectation, record gameusecase.PauseResumeRecord) (*gameusecase.PauseResumeRecord, bool, error) {
	fixture.resumeWrites++
	fixture.current.TournamentState = record.Graph.Tournament.State
	fixture.current.TournamentRevision = record.Graph.Tournament.Revision
	fixture.current.View.Wave = record.Graph.Wave.Wave
	fixture.current.View.Revision = record.Graph.Wave.Revision
	fixture.current.SourceRevisions.WaveRevision = fixture.current.View.Revision
	fixture.current.Graph.PausedSeriesCount, fixture.current.Graph.PausedGameCount = 0, 0
	fixture.current.Graph.ActiveSeriesCount, fixture.current.Graph.ActiveGameCount = 1, 1
	return &record, true, nil
}
func (fixture *executionNormalPauseFixture) LoadPauseResumePresenceAuthority(
	_ context.Context,
	_ pausedomain.GraphScope,
	_ uuid.UUID,
	seriesPauseID uuid.UUID,
	gamePauseID uuid.UUID,
) (gameusecase.PauseResumePresenceAuthority, error) {
	resume := executionNormalPausePresenceProjection(fixture.resumeAuthority, gamePauseID)
	series := resume.Pause.Graph.Series[0]
	game := resume.Pause.Graph.Games[0]
	clock := pausedomain.PauseResumeGameClock{
		PauseID: gamePauseID, GameID: game.Game.ID,
		OriginalDeadline: resume.FrozenDeadlines[0].OriginalDeadline,
		FrozenAt:         resume.FrozenDeadlines[0].FrozenAt,
		Remaining:        resume.FrozenDeadlines[0].Remaining, Revision: 1,
	}
	startedAt := resume.Pause.PausedAt.Add(-time.Nanosecond)
	return gameusecase.PauseResumePresenceAuthority{
		Resume: resume,
		SeriesDecision: gameusecase.PauseResumeDecisionAuthority{
			PauseID: seriesPauseID, ScopeKind: gameusecase.PauseResumeDecisionScopeSeries,
			CurrentRevisionID: executionID(seriesPauseID, "revision"), State: gameusecase.PauseStateActive,
			Revision: 1, SeriesID: series.Execution.Series.ID, StartedAt: startedAt,
		},
		GameDecision: gameusecase.PauseResumeDecisionAuthority{
			PauseID: gamePauseID, ScopeKind: gameusecase.PauseResumeDecisionScopeGameAttempt,
			CurrentRevisionID: executionID(gamePauseID, "revision"), State: gameusecase.PauseStateActive,
			Revision: 1, SeriesID: series.Execution.Series.ID, GameID: game.Game.ID,
			ParentPauseID: &seriesPauseID, Depth: 1, StartedAt: startedAt, GameClock: &clock,
		},
	}, nil
}

func executionNormalPausePresenceProjection(
	resume gameusecase.PauseResumeAuthority,
	gamePauseID uuid.UUID,
) gameusecase.PauseResumeAuthority {
	suspended := make(map[uuid.UUID]struct{}, len(resume.Pause.SuspendedReconnect))
	for _, value := range resume.Pause.SuspendedReconnect {
		suspended[value.ID] = struct{}{}
	}
	sourcePauseByParticipant := make(map[uuid.UUID]uuid.UUID, len(suspended))
	for _, value := range resume.Pause.Graph.Reconnect {
		if _, retained := suspended[value.ID]; retained {
			sourcePauseByParticipant[value.ParticipantID] = value.PauseID
		}
	}
	filterCounters := func(values []pausedomain.PauseReconnectCounter) []pausedomain.PauseReconnectCounter {
		result := make([]pausedomain.PauseReconnectCounter, 0, len(values))
		for _, value := range values {
			expectedPauseID := gamePauseID
			if sourcePauseID, ok := sourcePauseByParticipant[value.ParticipantID]; ok {
				expectedPauseID = sourcePauseID
			}
			if value.PauseID != expectedPauseID {
				continue
			}
			value.PauseID = gamePauseID
			result = append(result, value)
		}
		return result
	}
	filterReconnect := func(values []pausedomain.PauseReconnectInterval) []pausedomain.PauseReconnectInterval {
		result := make([]pausedomain.PauseReconnectInterval, 0, len(values))
		for _, value := range values {
			_, retained := suspended[value.ID]
			if value.PauseID != gamePauseID && !retained {
				continue
			}
			value.PauseID = gamePauseID
			result = append(result, value)
		}
		return result
	}
	resume.Counters = filterCounters(resume.Counters)
	resume.Reconnect = filterReconnect(resume.Reconnect)
	resume.Pause.Graph.Counters = filterCounters(resume.Pause.Graph.Counters)
	resume.Pause.Graph.Reconnect = filterReconnect(resume.Pause.Graph.Reconnect)
	resume.Pause.Expected.Counters = nil
	for _, value := range resume.Pause.Graph.Counters {
		resume.Pause.Expected.Counters = append(resume.Pause.Expected.Counters, gameusecase.PauseReconnectCounterRevision{
			PauseID: value.PauseID, RosterID: value.RosterID, ParticipantID: value.ParticipantID, Revision: value.Revision,
		})
	}
	resume.Pause.Expected.Reconnect = nil
	for _, value := range resume.Pause.Graph.Reconnect {
		revision := value.Revision
		if value.SuspendedByPauseID != nil {
			revision--
		}
		resume.Pause.Expected.Reconnect = append(resume.Pause.Expected.Reconnect, gameusecase.PauseChildRevision{ID: value.ID, Revision: revision})
	}
	return resume
}

func (fixture *executionNormalPauseFixture) CommitPauseResumePresence(
	_ context.Context,
	_ gameusecase.PauseResumePresenceExpectation,
	record gameusecase.PauseResumePresenceRecord,
) (*gameusecase.PauseResumePresenceRecord, bool, error) {
	fixture.resumeWrites++
	clone := record
	fixture.presenceRecord = &clone
	fixture.current.TournamentState = record.Command.Resume.Expected.TournamentState
	fixture.current.TournamentRevision++
	fixture.current.View.Wave.State = domain.WaveStateActive
	fixture.current.View.Wave.PausedAt = nil
	fixture.current.View.Revision++
	fixture.current.SourceRevisions.WaveRevision = fixture.current.View.Revision
	fixture.current.Graph.PausedSeriesCount, fixture.current.Graph.PausedGameCount = 1, 1
	fixture.current.Graph.ActiveSeriesCount, fixture.current.Graph.ActiveGameCount = 0, 0
	return &record, true, nil
}
func (fixture *executionNormalPauseFixture) ActiveNormalPauseID(context.Context, pausedomain.GraphScope) (uuid.UUID, error) {
	return fixture.resumeAuthority.Pause.PauseID, nil
}
