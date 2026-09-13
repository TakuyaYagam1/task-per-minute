package admin

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestBuildPairingPlanRejectsNonDeterministicManualBye(t *testing.T) {
	t.Parallel()

	authority := executionPairingAuthority(t, 5)
	requested := authority.Participants[0].ID
	command := PairingCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: executionTestID(80)},
			TournamentID: authority.TournamentID, CommandID: executionTestID(81),
		},
		ExpectedProjectionRevision: authority.ProjectionRevision,
		RoundNumber:                1, PairingMode: PairingModeManual,
		CategoryMode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryWeb},
		ManualPairingsProvided: true, ManualByeParticipantID: &requested,
	}

	_, err := buildPairingPlan(command, authority, executionTestTime())
	if !errors.Is(err, ErrManualByeMismatch) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("buildPairingPlan() error = %v, want deterministic bye conflict", err)
	}
	var mismatch *ManualByeMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("buildPairingPlan() error type = %T, want *ManualByeMismatchError", err)
	}
	selected := authority.Participants[len(authority.Participants)-1].ID
	if mismatch.RequestedParticipantID != requested || mismatch.SelectedParticipantID != selected {
		t.Fatalf("manual bye mismatch = (%s, %s), want (%s, %s)",
			mismatch.RequestedParticipantID, mismatch.SelectedParticipantID, requested, selected)
	}
}

func TestBuildAutomaticPairingPlanPreservesByeEvidence(t *testing.T) {
	t.Parallel()

	authority := executionPairingAuthority(t, 5)
	command := PairingCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: executionTestID(90)},
			TournamentID: authority.TournamentID, CommandID: executionTestID(91),
		},
		ExpectedProjectionRevision: authority.ProjectionRevision,
		RoundNumber:                1, PairingMode: PairingModeAutomatic,
		CategoryMode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryWeb},
	}

	plan, err := buildPairingPlan(command, authority, executionTestTime())
	if err != nil {
		t.Fatalf("buildPairingPlan() error = %v", err)
	}
	if plan.Automatic == nil || plan.Bye == nil || len(plan.Pairs) != 2 || len(plan.SeriesIDs) != 2 {
		t.Fatalf("buildPairingPlan() produced incomplete plan: %+v", plan)
	}
	if plan.Bye.ParticipantID != authority.Participants[4].ID {
		t.Fatalf("bye participant = %s, want %s", plan.Bye.ParticipantID, authority.Participants[4].ID)
	}
	for _, pair := range plan.Pairs {
		if pair.FirstParticipantID == plan.Bye.ParticipantID || pair.SecondParticipantID == plan.Bye.ParticipantID {
			t.Fatalf("bye participant %s appears in pair %+v", plan.Bye.ParticipantID, pair)
		}
	}
	if _, err := plan.Bye.Evidence.Replay(); err != nil {
		t.Fatalf("bye evidence replay error = %v", err)
	}
	if _, err := plan.Automatic.Evidence.Replay(); err != nil {
		t.Fatalf("pairing evidence replay error = %v", err)
	}
}

func TestBuildManualPairingPlanRejectsOpponentRepeat(t *testing.T) {
	t.Parallel()

	authority := executionPairingAuthority(t, 4)
	authority.PreviousMeetings = []swissusecase.Pair{{
		FirstParticipantID:  authority.Participants[0].ID,
		SecondParticipantID: authority.Participants[1].ID,
	}}
	command := PairingCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: executionTestID(95)},
			TournamentID: authority.TournamentID, CommandID: executionTestID(96),
		},
		ExpectedProjectionRevision: authority.ProjectionRevision,
		RoundNumber:                1, PairingMode: PairingModeManual,
		CategoryMode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryWeb},
		ManualPairingsProvided: true,
		ManualPairings: []ParticipantPair{
			{FirstParticipantID: authority.Participants[0].ID, SecondParticipantID: authority.Participants[1].ID},
			{FirstParticipantID: authority.Participants[2].ID, SecondParticipantID: authority.Participants[3].ID},
		},
	}

	plan, err := buildPairingPlan(command, authority, executionTestTime())
	if !errors.Is(err, swissusecase.ErrManualPairingRepeat) {
		t.Fatalf("buildPairingPlan() error = %v, want ErrManualPairingRepeat", err)
	}
	if len(plan.Pairs) != 0 {
		t.Fatalf("buildPairingPlan() returned a plan on repeat: %#v", plan)
	}
}

func TestPairingRequestDigestRetainsLegacyNullOverride(t *testing.T) {
	t.Parallel()

	command := PairingCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: executionTestID(97)},
			TournamentID: executionTestID(98), CommandID: executionTestID(99),
		},
		ExpectedProjectionRevision: 4, RoundNumber: 2,
		PairingMode: PairingModeManual, CategoryMode: domain.CategoryModeAdmin,
		Categories: []domain.Category{domain.CategoryWeb},
		ManualPairings: []ParticipantPair{{
			FirstParticipantID: executionTestID(100), SecondParticipantID: executionTestID(101),
		}},
		ManualPairingsProvided: true,
	}

	got, err := executionRequestDigest(command)
	if err != nil {
		t.Fatalf("executionRequestDigest() error = %v", err)
	}
	legacy := struct {
		CommandScope

		ExpectedProjectionRevision int64
		RoundNumber                int
		PairingMode                PairingMode
		CategoryMode               domain.CategoryMode
		Categories                 []domain.Category
		ManualPairings             []ParticipantPair
		ManualPairingsProvided     bool
		ManualByeParticipantID     *uuid.UUID
		RepeatOverride             *struct{}
	}{
		CommandScope:               command.CommandScope,
		ExpectedProjectionRevision: command.ExpectedProjectionRevision,
		RoundNumber:                command.RoundNumber,
		PairingMode:                command.PairingMode,
		CategoryMode:               command.CategoryMode,
		Categories:                 command.Categories,
		ManualPairings:             command.ManualPairings,
		ManualPairingsProvided:     command.ManualPairingsProvided,
		ManualByeParticipantID:     command.ManualByeParticipantID,
	}
	//nolint:musttag // This fixture reproduces the exact pre-removal default JSON field names used by the command digest.
	document, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("json.Marshal(legacy pairing command) error = %v", err)
	}
	want := sha256.Sum256(document)
	if got != want {
		t.Fatalf("pairing request digest = %x, want legacy digest %x", got, want)
	}
}

func TestPlanWaveStartAcceptsCompleteOddRosterGraph(t *testing.T) {
	t.Parallel()

	openedAt := executionTestTime()
	authority := executionWaveAuthority(t, openedAt)
	command := WaveCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: executionTestID(100)},
			TournamentID: authority.View.Wave.TournamentID, CommandID: executionTestID(101),
		},
		WaveID: authority.View.Wave.ID, ExpectedProjectionRevision: authority.ProjectionRevision,
		Action: WaveActionStart, Confirmed: true,
	}

	next, err := planWaveMutation(command, authority, openedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("planWaveMutation() error = %v", err)
	}
	if next.State != domain.WaveStateActive || next.StartedAt == nil || next.ReadyWindow == nil ||
		next.ReadyWindow.State != domain.ReadyWindowStateConsumed {
		t.Fatalf("planWaveMutation() next = %+v, want active consumed Wave", next)
	}
}

func TestPlanWaveStartFailsClosedOnIncompleteDeliveryGraph(t *testing.T) {
	t.Parallel()

	openedAt := executionTestTime()
	authority := executionWaveAuthority(t, openedAt)
	authority.Graph.DeliveryMemberCount--
	command := WaveCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: executionTestID(110)},
			TournamentID: authority.View.Wave.TournamentID, CommandID: executionTestID(111),
		},
		WaveID: authority.View.Wave.ID, ExpectedProjectionRevision: authority.ProjectionRevision,
		Action: WaveActionStart, Confirmed: true,
	}

	_, err := planWaveMutation(command, authority, openedAt.Add(time.Second))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("planWaveMutation() error = %v, want conflict", err)
	}
}

func TestPlanWavePlayoffSingleSeriesLifecycle(t *testing.T) {
	t.Parallel()

	openedAt := executionTestTime()
	authority := executionPlayoffWaveAuthority(t, openedAt)
	command := WaveCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: executionTestID(230)},
			TournamentID: authority.View.Wave.TournamentID, CommandID: executionTestID(231),
		},
		WaveID: authority.View.Wave.ID, ExpectedProjectionRevision: authority.ProjectionRevision,
		Action: WaveActionOpenReadyWindow, Confirmed: true,
	}

	opened, err := planWaveMutation(command, authority, openedAt)
	if err != nil {
		t.Fatalf("open ready window error = %v", err)
	}
	if opened.State != domain.WaveStateReadyWindowOpen || opened.ReadyWindow == nil {
		t.Fatalf("opened Wave = %+v, want open ready window", opened)
	}

	for index := range opened.Members {
		opened.Members[index].Ready = true
	}
	opened.State = domain.WaveStateReady
	authority.View.Wave = opened
	authority.Graph = WaveGraph{
		SeriesCount: 1, PlayableMemberCount: 2, CurrentGameCount: 1,
		ReadySeriesCount: 1, ReadyGameCount: 1, AssignmentCount: 1, DeliveryMemberCount: 2,
	}
	command.Action = WaveActionStart
	started, err := planWaveMutation(command, authority, openedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("start playoff wave error = %v", err)
	}
	if started.State != domain.WaveStateActive || started.ReadyWindow == nil ||
		started.ReadyWindow.State != domain.ReadyWindowStateConsumed {
		t.Fatalf("started Wave = %+v, want active consumed Wave", started)
	}

	authority.View.Wave = started
	authority.Graph = WaveGraph{
		SeriesCount: 1, CurrentGameCount: 1, TerminalSeriesCount: 1, TerminalGameCount: 1,
	}
	command.Action = WaveActionComplete
	completed, err := planWaveMutation(command, authority, openedAt.Add(2*time.Second))
	if err != nil {
		t.Fatalf("complete playoff wave error = %v", err)
	}
	if completed.State != domain.WaveStateCompleted || completed.RevisionID == started.RevisionID {
		t.Fatalf("completed Wave = %+v, want completed fresh revision", completed)
	}
}

func TestWaveGraphRequiresAllowedTournamentStateAndShape(t *testing.T) {
	t.Parallel()

	graphFor := func(seriesCount int) WaveGraph {
		return WaveGraph{
			SeriesCount: seriesCount, PlayableMemberCount: seriesCount * 2,
			CurrentGameCount: seriesCount, ReadySeriesCount: seriesCount, ReadyGameCount: seriesCount,
			ActiveSeriesCount: seriesCount, ActiveGameCount: seriesCount,
			PausedSeriesCount: seriesCount, PausedGameCount: seriesCount,
			TerminalSeriesCount: seriesCount, TerminalGameCount: seriesCount,
			AssignmentCount: seriesCount, DeliveryMemberCount: seriesCount * 2,
		}
	}
	tests := []struct {
		name         string
		state        domain.TournamentState
		seriesCount  int
		wantAccepted bool
	}{
		{name: "swiss multi series", state: domain.TournamentStateSwiss, seriesCount: 2, wantAccepted: true},
		{name: "playoffs single series", state: domain.TournamentStatePlayoffs, seriesCount: 1, wantAccepted: true},
		{name: "swiss single series", state: domain.TournamentStateSwiss, seriesCount: 1, wantAccepted: true},
		{name: "golden rejected", state: domain.TournamentStateGolden, seriesCount: 1},
		{name: "completed rejected", state: domain.TournamentStateCompleted, seriesCount: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			authority := WaveAuthority{TournamentState: tt.state, Graph: graphFor(tt.seriesCount)}
			checks := []struct {
				name string
				got  bool
			}{
				{name: "planned", got: waveGraphPlanned(authority)},
				{name: "startable", got: waveGraphStartable(authority)},
				{name: "active", got: waveGraphActive(authority)},
				{name: "paused", got: waveGraphPaused(authority)},
				{name: "terminal", got: waveGraphTerminal(authority)},
			}
			for _, check := range checks {
				if check.got != tt.wantAccepted {
					t.Errorf("%s graph accepted = %t, want %t", check.name, check.got, tt.wantAccepted)
				}
			}
		})
	}
}

func TestWaveGraphTerminalAcceptsContinuingBO3SeriesOnly(t *testing.T) {
	t.Parallel()

	authority := WaveAuthority{
		TournamentState: domain.TournamentStatePlayoffs,
		Graph: WaveGraph{
			SeriesCount: 1, ContinuingSeriesCount: 1,
			CurrentGameCount: 1, TerminalGameCount: 1,
		},
	}
	if !waveGraphTerminal(authority) {
		t.Fatal("terminal Game in a continuing BO3 Series must close its Wave")
	}
	authority.Graph.ContinuingSeriesCount = 0
	if waveGraphTerminal(authority) {
		t.Fatal("active Series without BO3 continuation evidence must not close its Wave")
	}
}

func TestWaveGraphStartableAcceptsContinuingBO3SeriesOnly(t *testing.T) {
	t.Parallel()

	authority := WaveAuthority{
		TournamentState: domain.TournamentStatePlayoffs,
		Graph: WaveGraph{
			SeriesCount: 1, PlayableMemberCount: 2, ContinuingSeriesCount: 1,
			CurrentGameCount: 1, ReadyGameCount: 1, AssignmentCount: 1, DeliveryMemberCount: 2,
		},
	}
	if !waveGraphStartable(authority) {
		t.Fatal("ready Game in a continuing BO3 Series must start its Wave")
	}
	authority.Graph.ContinuingSeriesCount = 0
	if waveGraphStartable(authority) {
		t.Fatal("non-ready Series without BO3 continuation evidence must not start its Wave")
	}
}

func TestPlanWaveCompleteCreatesDeterministicClosureRevision(t *testing.T) {
	t.Parallel()

	mutatedAt := executionTestTime().Add(time.Minute)
	authority := executionWaveAuthority(t, executionTestTime())
	authority.View.Wave.State = domain.WaveStateActive
	authority.View.Wave.StartedAt = testTimePointer(executionTestTime())
	authority.View.Wave.ReadyWindow.State = domain.ReadyWindowStateConsumed
	authority.View.Wave.ReadyWindow.ConsumedAt = testTimePointer(executionTestTime())
	authority.Graph = WaveGraph{
		SeriesCount: 2, PlayableMemberCount: 4, TerminalSeriesCount: 2,
		CurrentGameCount: 2, TerminalGameCount: 2,
	}
	command := WaveCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: executionTestID(140)},
			TournamentID: authority.View.Wave.TournamentID, CommandID: executionTestID(141),
		},
		WaveID: authority.View.Wave.ID, ExpectedProjectionRevision: authority.ProjectionRevision,
		Action: WaveActionComplete, Confirmed: true,
	}

	first, err := planWaveMutation(command, authority, mutatedAt)
	if err != nil {
		t.Fatalf("planWaveMutation() error = %v", err)
	}
	second, err := planWaveMutation(command, authority, mutatedAt)
	if err != nil {
		t.Fatalf("planWaveMutation() replay error = %v", err)
	}
	wantRevisionID := domain.WaveRevisionID(executionID(command.CommandID, "wave-closure-revision"))
	if first.State != domain.WaveStateCompleted || first.RevisionID != wantRevisionID ||
		second.RevisionID != wantRevisionID || first.RevisionID == authority.View.Wave.RevisionID {
		t.Fatalf("closure revisions = (%s, %s), want stable fresh %s",
			first.RevisionID.UUID(), second.RevisionID.UUID(), wantRevisionID.UUID())
	}
}

func TestStartWaveReplayReturnsRecordedResultAfterLaterWaveTransition(t *testing.T) {
	t.Parallel()

	record := executionWaveStartRecord(t)
	repository := gamemocks.NewMockStartRepository(t)
	repository.EXPECT().LoadWaveStartAuthority(mock.Anything, mock.Anything).
		Return(gameusecase.StartAuthority{
			Scope: record.Scope, WaveRevision: record.ExpectedWaveRevision + 2,
			Revisions: record.Revisions, Current: &record,
		}, nil).Once()
	usecase := gameusecase.NewStartUseCase(repository, executionWorkflowClock{at: record.StartedAt})
	workflow := &ExecutionWorkflow{waveStart: usecase}
	later := record.Wave
	later.State = domain.WaveStateCompleted
	authority := WaveAuthority{
		TournamentState: domain.TournamentStateSwiss,
		SourceRevisions: record.Revisions,
		View:            WaveView{Wave: later, Revision: record.ExpectedWaveRevision + 2},
	}
	command := WaveCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: record.ActorID},
			TournamentID: record.Scope.TournamentID, CommandID: record.CommandID,
		},
		WaveID: record.Scope.WaveID, ExpectedProjectionRevision: record.ExpectedProjectionRevision,
		Action: WaveActionStart, Confirmed: true,
	}

	newAuthority := record.ExecutionAuthority
	newAuthority.HolderID = executionTestID(220)
	newAuthority.LeaseID = executionTestID(221)
	newAuthority.Epoch++
	view, err := workflow.startWaveLocked(t.Context(), command, record.RequestDigest, authority, newAuthority)
	if err != nil {
		t.Fatalf("startWaveLocked() error = %v", err)
	}
	if view.Wave.State != domain.WaveStateActive || view.Revision != record.ExpectedWaveRevision+1 ||
		view.Wave.StartedAt == nil || !view.Wave.StartedAt.Equal(record.StartedAt) {
		t.Fatalf("replayed view = %+v, want retained active start", view)
	}
}

func TestStartWaveLockedRequiresStartDependency(t *testing.T) {
	t.Parallel()

	record := executionWaveStartRecord(t)
	workflow := &ExecutionWorkflow{}
	command := WaveCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: record.ActorID},
			TournamentID: record.Scope.TournamentID, CommandID: record.CommandID,
		},
		WaveID: record.Scope.WaveID, ExpectedProjectionRevision: record.ExpectedProjectionRevision,
		Action: WaveActionStart, Confirmed: true,
	}
	_, err := workflow.startWaveLocked(
		t.Context(), command, record.RequestDigest, WaveAuthority{View: WaveView{Wave: record.Wave}},
		record.ExecutionAuthority,
	)
	if !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("startWaveLocked() error = %v, want internal dependency error", err)
	}
}

type executionWorkflowClock struct {
	at time.Time
}

func (clock executionWorkflowClock) Now() time.Time {
	return clock.at
}

func executionWaveStartRecord(t *testing.T) gameusecase.StartRecord {
	t.Helper()
	startedAt := executionTestTime()
	tournamentID := executionTestID(201)
	waveID := executionTestID(202)
	windowID := executionTestID(203)
	firstParticipantID := executionTestID(204)
	secondParticipantID := executionTestID(205)
	seriesID := executionTestID(206)
	slotID := executionTestID(207)
	gameID := executionTestID(208)
	waveRevisionID := domain.WaveRevisionID(executionTestID(209))
	wave := domain.Wave{
		ID: waveID, TournamentID: tournamentID, RevisionID: waveRevisionID,
		State: domain.WaveStateActive,
		Members: []domain.WaveMember{
			{ParticipantID: firstParticipantID, Ready: true},
			{ParticipantID: secondParticipantID, Ready: true},
		},
		ReadyWindow: &domain.ReadyWindow{
			ID: windowID, WaveID: waveID, RevisionID: domain.ReadyWindowRevisionID(executionTestID(210)),
			State: domain.ReadyWindowStateConsumed, OpenedAt: startedAt.Add(-10 * time.Second),
			Deadline: startedAt.Add(20 * time.Second), ConsumedAt: testTimePointer(startedAt),
		},
		StartedAt: testTimePointer(startedAt),
	}
	series := seriesdomain.Execution{Series: domain.Series{
		ID: seriesID, TournamentID: tournamentID, FirstParticipantID: firstParticipantID,
		SecondParticipantID: secondParticipantID, Format: domain.SeriesFormatBO1, State: domain.SeriesStateActive,
		Slots: []domain.GameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			Attempts: []domain.Game{{ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStateActive}},
		}},
	}}
	if err := wave.Validate(); err != nil {
		t.Fatalf("wave.Validate() error = %v", err)
	}
	if err := series.Validate(); err != nil {
		t.Fatalf("series.Validate() error = %v", err)
	}
	revisions := domain.ReadyWindowSourceRevisions{
		WaveRevisionID: waveRevisionID, WaveRevision: 7,
		ProjectionRevisionID: executionTestID(212), ProjectionRevision: 8,
		ArtifactRevisionID: executionTestID(213), ArtifactRevision: 8,
	}
	record := gameusecase.StartRecord{
		Scope:     gameusecase.StartScope{TournamentID: tournamentID, WaveID: waveID, WindowID: windowID},
		CommandID: executionTestID(214), ActorID: executionTestID(215),
		ExecutionAuthority: authoritydomain.Identity{
			TournamentID: tournamentID, HolderID: executionTestID(218), LeaseID: executionTestID(219),
			Epoch: 1, ProcessKind: authoritydomain.ProcessAuthority,
		},
		ExpectedWaveRevision: 7, ExpectedProjectionRevision: 8, Revisions: revisions,
		ReadinessRevisions: map[uuid.UUID]int64{firstParticipantID: 1, secondParticipantID: 1},
		RequestDigest:      [32]byte{1}, Wave: wave, StartedAt: startedAt,
		Games: []gamedomain.Started{{
			Scope:          gamedomain.Scope{TournamentID: tournamentID, SeriesID: seriesID, SlotID: slotID, GameID: gameID},
			ParticipantIDs: [2]uuid.UUID{firstParticipantID, secondParticipantID}, Series: series,
			AssignmentID: executionTestID(216), AssignmentRevision: 1, PlanRevisionID: executionTestID(211),
			SnapshotID: executionTestID(217), ContentDigest: [32]byte{2}, DeadlineSeconds: 180,
			StartedAt: startedAt, Deadline: startedAt.Add(180 * time.Second), DeliveryEnabled: true,
		}},
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("record.Validate() error = %v", err)
	}
	return record
}

func executionPairingAuthority(t *testing.T, size int) PairingAuthority {
	t.Helper()
	participants := make([]PairingParticipant, size)
	standings := make([]SwissStandingView, size)
	received := make(map[uuid.UUID]bool, size)
	for index := range size {
		participantID := executionTestID(index + 1)
		participants[index] = PairingParticipant{ID: participantID, StableSeed: index + 1}
		standings[index] = SwissStandingView{
			ParticipantID: participantID, Position: index + 1, Points: size - index,
			PointsLabel: "provisional", Buchholz: size - index,
			BuchholzStatus: "provisional", EffectiveTimeMS: int64(index), StableSeed: index + 1,
		}
		received[participantID] = false
	}
	return PairingAuthority{
		TournamentID: executionTestID(60), TournamentState: domain.TournamentStateSwiss,
		TournamentRevision: 3, RosterID: executionTestID(61), RosterRevision: 2,
		RosterLockedAt:       executionTestTime().Add(-time.Hour),
		ProjectionRevisionID: executionTestID(62), ProjectionRevision: 4,
		Participants: participants, Standings: standings,
		PriorMeetingCounts: make(map[swissusecase.PairKey]int),
		ReceivedBye:        received,
	}
}

func executionWaveAuthority(t *testing.T, openedAt time.Time) WaveAuthority {
	t.Helper()
	tournamentID := executionTestID(120)
	waveID := executionTestID(121)
	revisionID := domain.WaveRevisionID(executionTestID(122))
	windowID := executionTestID(123)
	windowRevisionID := domain.ReadyWindowRevisionID(executionTestID(124))
	participants := []uuid.UUID{
		executionTestID(125), executionTestID(126), executionTestID(127),
		executionTestID(128), executionTestID(129),
	}
	members := make([]domain.WaveMember, len(participants))
	readiness := make(map[uuid.UUID]int64, len(participants))
	seriesIDs := make(map[uuid.UUID]uuid.UUID, len(participants)-1)
	for index, participantID := range participants {
		members[index] = domain.WaveMember{ParticipantID: participantID, Ready: true}
		readiness[participantID] = 3
		if index < 2 {
			seriesIDs[participantID] = executionTestID(130)
		} else if index < 4 {
			seriesIDs[participantID] = executionTestID(131)
		}
	}
	bye := participants[4]
	view := WaveView{
		Wave: domain.Wave{
			ID: waveID, TournamentID: tournamentID, RevisionID: revisionID,
			State: domain.WaveStateReady, Members: members,
			ReadyWindow: &domain.ReadyWindow{
				ID: windowID, WaveID: waveID, RevisionID: windowRevisionID,
				State: domain.ReadyWindowStateOpen, OpenedAt: openedAt,
				Deadline: openedAt.Add(domain.ReadyWindowDuration),
			},
		},
		Revision: 7, ReadinessRevisions: readiness, SeriesIDs: seriesIDs, ByeParticipantID: &bye,
	}
	return WaveAuthority{
		TournamentState: domain.TournamentStateSwiss, TournamentRevision: 4,
		RosterID: executionTestID(132), RosterRevision: 2,
		ProjectionRevisionID: executionTestID(133), ProjectionRevision: 8,
		SourceRevisions: domain.ReadyWindowSourceRevisions{
			WaveRevisionID: revisionID, WaveRevision: view.Revision,
			ProjectionRevisionID: executionTestID(133), ProjectionRevision: 8,
			ArtifactRevisionID: executionTestID(135), ArtifactRevision: 8,
		},
		View: view,
		Graph: WaveGraph{
			SeriesCount: 2, PlayableMemberCount: 4, CurrentGameCount: 2,
			ReadySeriesCount: 2, ReadyGameCount: 2, AssignmentCount: 2, DeliveryMemberCount: 4,
		},
	}
}

func executionPlayoffWaveAuthority(t *testing.T, _ time.Time) WaveAuthority {
	t.Helper()
	tournamentID := executionTestID(240)
	waveID := executionTestID(241)
	revisionID := domain.WaveRevisionID(executionTestID(242))
	firstParticipantID := executionTestID(243)
	secondParticipantID := executionTestID(244)
	seriesID := executionTestID(245)
	return WaveAuthority{
		TournamentState: domain.TournamentStatePlayoffs, TournamentRevision: 4,
		RosterID: executionTestID(247), RosterRevision: 2,
		ProjectionRevisionID: executionTestID(248), ProjectionRevision: 8,
		SourceRevisions: domain.ReadyWindowSourceRevisions{
			WaveRevisionID: revisionID, WaveRevision: 7,
			ProjectionRevisionID: executionTestID(248), ProjectionRevision: 8,
			ArtifactRevisionID: executionTestID(249), ArtifactRevision: 8,
		},
		View: WaveView{
			Wave: domain.Wave{
				ID: waveID, TournamentID: tournamentID, RevisionID: revisionID,
				State: domain.WaveStatePlanned,
				Members: []domain.WaveMember{
					{ParticipantID: firstParticipantID}, {ParticipantID: secondParticipantID},
				},
			},
			Revision:           7,
			ReadinessRevisions: map[uuid.UUID]int64{firstParticipantID: 3, secondParticipantID: 3},
			SeriesIDs: map[uuid.UUID]uuid.UUID{
				firstParticipantID: seriesID, secondParticipantID: seriesID,
			},
		},
		Graph: WaveGraph{SeriesCount: 1, PlayableMemberCount: 2},
	}
}

func executionTestID(value int) uuid.UUID {
	return uuid.MustParse("00000000-0000-4000-8000-" + leftPadExecutionID(value))
}

func leftPadExecutionID(value int) string {
	const digits = "000000000000"
	encoded := []byte(digits)
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded)
}

func executionTestTime() time.Time {
	return time.Date(2026, time.September, 6, 10, 0, 0, 0, time.UTC)
}

func testTimePointer(value time.Time) *time.Time {
	return &value
}
