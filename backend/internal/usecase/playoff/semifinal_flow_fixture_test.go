package playoff_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gamesettlement "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/settlement"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
	gamesubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/submission"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	readinessusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/seriesgraph"
)

type semifinalFlowFixture struct {
	Bracket   playoff.SemifinalBracket
	Authority playoff.SemifinalAdvancementAuthority
	Input     playoff.SemifinalFlowInput
	Matches   []playoff.SemifinalMatch
	RosterID  uuid.UUID
	CreatedAt time.Time
}

func newSemifinalFlowFixture(t *testing.T) semifinalFlowFixture {
	t.Helper()
	bracket := newSemifinalBracket(t)
	matches := bracket.Semifinals()
	projection := bracket.Projection().Revision()
	rosterID := semifinalFlowID(9000)
	createdAt := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	inputs := [2]seriesgraph.MaterializeInput{}
	for index, match := range matches {
		inputs[index] = semifinalFlowMaterializeInput(t, match, rosterID, createdAt, index)
	}
	authority := playoff.SemifinalAdvancementAuthority{
		TournamentID:      projection.TournamentID(),
		BracketRevisionID: projection.ID(),
		Semifinals:        matches,
	}
	return semifinalFlowFixture{
		Bracket: bracket, Authority: authority, Matches: matches, RosterID: rosterID, CreatedAt: createdAt,
		Input: playoff.SemifinalFlowInput{Authority: authority, RosterID: rosterID, Materialize: inputs},
	}
}

func semifinalFlowMaterializeInput(
	t *testing.T,
	match playoff.SemifinalMatch,
	rosterID uuid.UUID,
	createdAt time.Time,
	position int,
) seriesgraph.MaterializeInput {
	t.Helper()
	series := match.Series
	categoryRevisionID := semifinalFlowID(9100 + position)
	poolID := semifinalFlowID(9200)
	category := domain.CategoryWeb
	revision := draft.CategoryRevision{
		ID: categoryRevisionID, TournamentID: series.TournamentID, SeriesID: series.ID, RosterID: rosterID,
		Revision: 1, Stage: domain.TournamentStageSemifinal, Format: domain.SeriesFormatBO1,
		Mode: domain.CategoryModeRandom, SourceContentRevision: 1,
		CategoryPool: domain.CategoryPoolRevision{
			ID: poolID, Revision: 1, Format: domain.SeriesFormatBO1,
			Categories: []domain.Category{domain.CategoryCrypto, domain.CategoryReverse, domain.CategoryWeb},
		},
		CreatedAt: createdAt,
	}
	require.NoError(t, revision.Validate())
	plan := semifinalFlowExactPlan(t, createdAt, series, rosterID, categoryRevisionID, poolID, category, position)
	return seriesgraph.MaterializeInput{
		CommandID: semifinalFlowID(9300 + position), DeliveredAt: createdAt.Add(5 * time.Minute),
		Series: series, CategoryRevision: revision,
		SelectedCategories: []domain.Category{category}, AssignmentPlans: []assignment.ExactNormalAssignmentPlan{plan},
	}
}

func semifinalFlowExactPlan(
	t *testing.T,
	createdAt time.Time,
	series domain.Series,
	rosterID, categoryRevisionID, poolID uuid.UUID,
	category domain.Category,
	position int,
) assignment.ExactNormalAssignmentPlan {
	t.Helper()
	scope := assignment.ExactNormalAssignmentScope{
		TournamentID: series.TournamentID, RosterID: rosterID, SeriesID: series.ID,
		SlotID: semifinalFlowID(9400 + position), CategoryLockID: categoryRevisionID,
	}
	participants := []uuid.UUID{series.FirstParticipantID, series.SecondParticipantID}
	versions := make([]domain.TaskVersionRef, 3)
	candidates := make([]assignment.ExactNormalTaskVersion, 3)
	for index := range candidates {
		taskID := semifinalFlowID(9500 + index)
		task := domain.Task{
			ID: taskID, Title: fmt.Sprintf("semifinal task %d", index), Description: "Solve this task.",
			Category: category, Difficulty: domain.DifficultyHard, TimeLimit: 90,
			Flag: fmt.Sprintf("FLAG{%s}", taskID), Hints: []string{"hint"},
		}
		versions[index] = domain.TaskVersionRef{TaskID: taskID, Version: 1}
		candidates[index] = assignment.ExactNormalTaskVersion{
			PoolRevisionID: poolID, Version: 1, Task: task,
		}
	}
	authority := assignment.ExactNormalAssignmentAuthority{
		Scope: scope, Category: category,
		Revisions: assignment.ExactNormalAssignmentSourceRevisions{
			SeriesRevision: 1, PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: semifinalFlowID(9600 + position), HistoryRevision: 1, RosterRevision: 1,
			ArtifactRevisionID: semifinalFlowID(9700 + position), ArtifactRevision: 1,
			CategoryRevisionID: categoryRevisionID, CategoryRevision: 1,
		},
		Pool: domain.TaskPoolRevision{
			ID: poolID, Revision: 1, Kind: domain.AssignmentTaskKindNormal, Versions: versions,
		},
		ParticipantIDs: participants,
		ParticipantReservations: []assignment.ExactNormalParticipantReservation{
			{ParticipantID: participants[0], PlayerID: semifinalFlowID(9800), Reservation: semifinalFlowReservation(semifinalFlowID(9900), semifinalFlowID(9800), series.TournamentID, createdAt)},
			{ParticipantID: participants[1], PlayerID: semifinalFlowID(9801), Reservation: semifinalFlowReservation(semifinalFlowID(9901), semifinalFlowID(9801), series.TournamentID, createdAt)},
		},
		Candidates:     candidates,
		GraphDigest:    sha256.Sum256([]byte(fmt.Sprintf("semifinal-graph-%d", position))),
		ArtifactDigest: sha256.Sum256([]byte(fmt.Sprintf("semifinal-artifact-%d", position))),
	}
	command := assignment.ExactNormalAssignmentCommand{
		Scope: scope, PlanID: semifinalFlowID(10000 + position), PlanRevisionID: semifinalFlowID(10100 + position),
		BranchID: semifinalFlowID(10200 + position), DecisionEvidenceID: semifinalFlowID(10300 + position), CreatedAt: createdAt,
	}
	for index := range command.EdgeIDs {
		command.EdgeIDs[index] = semifinalFlowID(10400 + position*10 + index)
		command.ReservationIDs[index] = semifinalFlowID(10500 + position*10 + index)
		command.SnapshotIDs[index] = semifinalFlowID(10600 + position*10 + index)
	}
	plan, err := assignment.BuildExactNormalAssignment(command, authority)
	require.NoError(t, err)
	return plan
}

func semifinalFlowReservation(
	reservationID, playerID, tournamentID uuid.UUID,
	at time.Time,
) domain.ParticipantReservation {
	return domain.ParticipantReservation{
		PlayerID: playerID, ReservationID: reservationID, TournamentID: tournamentID,
		Revision: 1, AcquiredAt: at, UpdatedAt: at,
	}
}

func semifinalFlowID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("30000000-0000-0000-0000-%012d", number))
}

func semifinalFlowContentConfiguration(t *testing.T, tournamentID uuid.UUID) domain.ContentConfiguration {
	t.Helper()
	bo1CategoryPoolID := semifinalFlowID(12500)
	bo3CategoryPoolID := semifinalFlowID(12501)
	configuration, err := domain.CreateContentConfiguration(domain.ContentConfigurationInput{
		TournamentID: tournamentID,
		CategoryPools: []domain.CategoryPoolRevision{
			{ID: bo1CategoryPoolID, Revision: 1, Format: domain.SeriesFormatBO1,
				Categories: []domain.Category{domain.CategoryCrypto, domain.CategoryReverse, domain.CategoryWeb}},
			{ID: bo3CategoryPoolID, Revision: 1, Format: domain.SeriesFormatBO3,
				Categories: []domain.Category{domain.CategoryCrypto, domain.CategoryForensics, domain.CategoryPwn, domain.CategoryReverse, domain.CategoryWeb}},
		},
		NormalPool: domain.TaskPoolRevision{
			ID: semifinalFlowID(12502), Revision: 1, Kind: domain.AssignmentTaskKindNormal,
			Versions: []domain.TaskVersionRef{{TaskID: semifinalFlowID(12503), Version: 1}},
		},
		GoldenPool: domain.TaskPoolRevision{
			ID: semifinalFlowID(12504), Revision: 1, Kind: domain.AssignmentTaskKindGolden,
			Versions: []domain.TaskVersionRef{{TaskID: semifinalFlowID(12505), Version: 1}},
		},
		StageDefaults: []domain.StageContentDefault{
			{Stage: domain.TournamentStageSwiss, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeRandom, CategoryPoolRevisionID: bo1CategoryPoolID, TaskPoolKind: domain.AssignmentTaskKindNormal},
			{Stage: domain.TournamentStageGolden, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeRandom, CategoryPoolRevisionID: bo1CategoryPoolID, TaskPoolKind: domain.AssignmentTaskKindGolden},
			{Stage: domain.TournamentStageSemifinal, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeAdmin, CategoryPoolRevisionID: bo1CategoryPoolID, TaskPoolKind: domain.AssignmentTaskKindNormal},
			{Stage: domain.TournamentStageFinal, Format: domain.SeriesFormatBO3, CategoryMode: domain.CategoryModeDraft, CategoryPoolRevisionID: bo3CategoryPoolID, TaskPoolKind: domain.AssignmentTaskKindNormal},
		},
	})
	require.NoError(t, err)
	require.NoError(t, configuration.Validate())
	return configuration
}

type semifinalTerminalRepository struct {
	stage *playoff.SemifinalStageAuthority
	plan  *playoff.FinalDraftPlan
}

func (r *semifinalTerminalRepository) LoadSemifinalStage(
	_ context.Context, _ playoff.TerminalSeriesCommand,
) (*playoff.SemifinalStageAuthority, error) {
	if r.stage == nil {
		return nil, nil
	}
	value := *r.stage
	value.Series = append([]domain.Series(nil), r.stage.Series...)
	return &value, nil
}

func (r *semifinalTerminalRepository) PersistFinalDraft(
	_ context.Context, plan playoff.FinalDraftPlan,
) (bool, error) {
	value := plan
	value.Advancement = append([]playoff.SemifinalAdvancementResult(nil), plan.Advancement...)
	r.plan = &value
	return true, nil
}

func (r *semifinalTerminalRepository) LoadFinalDraft(
	_ context.Context, _ playoff.TerminalDraftCommand,
) (*playoff.FinalDraftAuthority, error) {
	return nil, nil
}

func (r *semifinalTerminalRepository) PersistFinalInitial(
	_ context.Context, _ playoff.FinalInitialPlan,
) (bool, error) {
	return false, nil
}

func (r *semifinalTerminalRepository) LoadFinalSettlement(
	_ context.Context, _ playoff.TerminalSeriesCommand,
) (*playoff.FinalSettlementAuthority, error) {
	return nil, nil
}

func (r *semifinalTerminalRepository) PersistFinalContinuation(
	_ context.Context, _ playoff.FinalContinuationPlan,
) (bool, error) {
	return false, nil
}

type semifinalDraftPlanner struct {
	plan *playoff.FinalDraftPlan
}

func (p *semifinalDraftPlanner) PlanFinalDraft(
	_ context.Context, plan playoff.FinalDraftPlan,
) (bool, error) {
	value := plan
	value.Advancement = append([]playoff.SemifinalAdvancementResult(nil), plan.Advancement...)
	p.plan = &value
	return true, nil
}

func (p *semifinalDraftPlanner) ActivateFinalDraft(
	_ context.Context, _ playoff.FinalDraftAuthority, _ playoff.TerminalDraftCommand,
) ([]playoff.FinalGameBinding, bool, error) {
	return nil, false, nil
}

type semifinalExecutionFixture struct {
	readyWindow *readinessusecase.ReadyWindowUseCase
	readiness   *readinessusecase.ReadinessUseCase
	start       *gamestart.StartUseCase
	submission  *gamesubmission.SubmissionUseCase
	settlement  *gamesettlement.SettlementUseCase

	readyWindowRepo *semifinalReadyWindowRepository
	readinessRepo   *semifinalReadinessRepository
	startRepo       *semifinalStartRepository
	submissionRepo  *semifinalSubmissionRepository
	settlementRepo  *semifinalSettlementRepository

	openCommand       readinessusecase.OpenReadyWindowCommand
	readyCommands     [2]readinessusecase.ReadyCommand
	startCommand      gamestart.StartCommand
	submissionCommand gamesubmission.SubmissionCommand
	settlementCommand gamesettlement.SettlementCommand
}

func newSemifinalExecutionFixture(
	t *testing.T,
	graphIndex int,
	flow playoff.SemifinalFlow,
) *semifinalExecutionFixture {
	t.Helper()
	require.NoError(t, flow.Validate())
	graph := flow.Graphs[graphIndex]
	now := flow.Graphs[graphIndex].CategoryRevision.CreatedAt.Add(10 * time.Minute)
	tournamentID := graph.Series.TournamentID
	firstParticipantID := graph.Series.FirstParticipantID
	secondParticipantID := graph.Series.SecondParticipantID
	waveID := semifinalFlowID(11000 + graphIndex*100)
	waveRevisionID := domain.WaveRevisionID(semifinalFlowID(11100 + graphIndex*100))
	windowID := semifinalFlowID(11200 + graphIndex*100)
	windowRevisionID := domain.ReadyWindowRevisionID(semifinalFlowID(11300 + graphIndex*100))
	revisions := domain.ReadyWindowSourceRevisions{
		WaveRevisionID: waveRevisionID, WaveRevision: 1,
		ProjectionRevisionID: semifinalFlowID(11400 + graphIndex*100), ProjectionRevision: 1,
		ArtifactRevisionID: semifinalFlowID(11500 + graphIndex*100), ArtifactRevision: 1,
	}
	plannedWave := domain.Wave{
		ID: waveID, TournamentID: tournamentID, RevisionID: waveRevisionID,
		State:   domain.WaveStatePlanned,
		Members: []domain.WaveMember{{ParticipantID: firstParticipantID}, {ParticipantID: secondParticipantID}},
	}
	windowScope := readinessusecase.ReadyWindowScope{TournamentID: tournamentID, WaveID: waveID}
	openCommand := readinessusecase.OpenReadyWindowCommand{
		Scope: windowScope, CommandID: semifinalFlowID(11600 + graphIndex*100), WindowID: windowID,
		WindowRevisionID: windowRevisionID, ExpectedRevisions: revisions,
	}
	readyWindowRepo := &semifinalReadyWindowRepository{authority: readinessusecase.ReadyWindowAuthority{
		Scope: windowScope, Revision: 1, Revisions: revisions, Wave: plannedWave,
	}}
	readinessScope := readinessusecase.ReadinessScope{WaveID: waveID, WindowID: windowID}
	readinessRepo := &semifinalReadinessRepository{authority: readinessusecase.ReadinessAuthority{
		Scope: readinessScope, Revision: 1, Wave: plannedWave,
	}}
	readyCommands := [2]readinessusecase.ReadyCommand{
		{
			Scope: readinessScope, CommandID: semifinalFlowID(11700 + graphIndex*100),
			ActorParticipantID: firstParticipantID, ParticipantID: firstParticipantID,
			ExpectedWaveRevisionID: waveRevisionID, ExpectedWindowRevisionID: windowRevisionID,
		},
		{
			Scope: readinessScope, CommandID: semifinalFlowID(11800 + graphIndex*100),
			ActorParticipantID: secondParticipantID, ParticipantID: secondParticipantID,
			ExpectedWaveRevisionID: waveRevisionID, ExpectedWindowRevisionID: windowRevisionID,
		},
	}
	readySeries, transitionChanged, err := seriesdomain.Transition(
		seriesdomain.Execution{Series: graph.Series},
		seriesdomain.TransitionCommand{NextState: domain.SeriesStateLocked},
	)
	require.NoError(t, err)
	require.True(t, transitionChanged)
	readySeries, transitionChanged, err = seriesdomain.Transition(
		readySeries,
		seriesdomain.TransitionCommand{NextState: domain.SeriesStateReady},
	)
	require.NoError(t, err)
	require.True(t, transitionChanged)
	slot := readySeries.Series.Slots[0]
	attempt := slot.Attempts[len(slot.Attempts)-1]
	aggregate := graph.Assignments[0]
	primary := aggregate.Plan.SelectedEdges[0]
	gameScope := gamedomain.Scope{
		TournamentID: tournamentID, SeriesID: readySeries.Series.ID, SlotID: slot.ID, GameID: attempt.ID,
	}
	startScope := gamestart.StartScope{TournamentID: tournamentID, WaveID: waveID, WindowID: windowID}
	startGame := gamestart.GameAuthority{
		Scope: gameScope, ParticipantIDs: [2]uuid.UUID{firstParticipantID, secondParticipantID},
		Series: readySeries, AssignmentID: aggregate.ID, AssignmentRevision: 1,
		PlanRevisionID: aggregate.Plan.PlanRevisionID, SnapshotID: primary.Snapshot.SnapshotID,
		ContentDigest: primary.ContentDigest, DeadlineSeconds: 180,
	}
	startAuthority := gamestart.StartAuthority{
		Scope: startScope, WaveRevision: 1, Revisions: revisions,
		ReadinessRevisions: map[uuid.UUID]int64{firstParticipantID: 1, secondParticipantID: 1},
		Wave:               plannedWave, Games: []gamestart.GameAuthority{startGame},
	}
	startRepo := &semifinalStartRepository{authority: startAuthority, serverTime: now}
	startCommand := gamestart.StartCommand{
		Scope: startScope, CommandID: semifinalFlowID(11900 + graphIndex*100), ActorID: semifinalFlowID(12000 + graphIndex*100),
		ExecutionAuthority: authoritydomain.Identity{
			TournamentID: tournamentID, HolderID: semifinalFlowID(12100 + graphIndex*100),
			LeaseID: semifinalFlowID(12200 + graphIndex*100), Epoch: 1, ProcessKind: authoritydomain.ProcessAuthority,
		},
		ExpectedProjectionRevision: revisions.ProjectionRevision, ExpectedRevisions: revisions,
		RequestDigest: sha256.Sum256([]byte(fmt.Sprintf("semifinal-start-%d", graphIndex))),
	}
	snapshot := &semifinalSnapshot{
		value: primary.Snapshot, digest: primary.ContentDigest,
		participants: map[uuid.UUID]struct{}{firstParticipantID: {}, secondParticipantID: {}},
	}
	submissionScope := gamedomain.SubmissionScope{WaveID: waveID, Game: gameScope, AssignmentID: aggregate.ID}
	submissionRepo := &semifinalSubmissionRepository{authority: gamesubmission.SubmissionAuthority{
		Scope: submissionScope, Revision: 1, Snapshot: snapshot,
		ConnectedParticipantIDs: []uuid.UUID{firstParticipantID, secondParticipantID},
	}}
	submissionCommand := gamesubmission.SubmissionCommand{
		Scope: submissionScope, CommandID: semifinalFlowID(12300 + graphIndex*100),
		ActorParticipantID: firstParticipantID, ParticipantID: firstParticipantID,
		SubmittedFlag: primary.Snapshot.Flag,
	}
	settlementRepo := &semifinalSettlementRepository{authority: gamesettlement.SettlementAuthority{
		Scope: submissionScope, Revision: 1, CurrentScoreOrdinal: 0, CurrentProjectionRevision: 1,
	}}
	settlementCommand := gamesettlement.SettlementCommand{Scope: submissionScope, CommandID: semifinalFlowID(12400 + graphIndex*100)}
	clock := semifinalFlowClock{now: now}
	return &semifinalExecutionFixture{
		readyWindow:     readinessusecase.NewReadyWindowUseCase(readyWindowRepo, clock),
		readiness:       readinessusecase.NewReadinessUseCase(readinessRepo, clock),
		start:           gamestart.NewStartUseCase(startRepo, clock),
		submission:      gamesubmission.NewSubmissionUseCase(submissionRepo),
		settlement:      gamesettlement.SettlementNewUseCase(settlementRepo),
		readyWindowRepo: readyWindowRepo, readinessRepo: readinessRepo, startRepo: startRepo,
		submissionRepo: submissionRepo, settlementRepo: settlementRepo,
		openCommand: openCommand, readyCommands: readyCommands, startCommand: startCommand,
		submissionCommand: submissionCommand, settlementCommand: settlementCommand,
	}
}

type semifinalFlowClock struct{ now time.Time }

func (c semifinalFlowClock) Now() time.Time { return c.now }

type semifinalReadyWindowRepository struct {
	mu        sync.Mutex
	authority readinessusecase.ReadyWindowAuthority
}

func (r *semifinalReadyWindowRepository) LoadReadyWindowAuthority(
	_ context.Context, _ readinessusecase.ReadyWindowScope,
) (readinessusecase.ReadyWindowAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSemifinalReadyWindowAuthority(r.authority), nil
}

func (r *semifinalReadyWindowRepository) CommitReadyWindow(
	_ context.Context, record readinessusecase.ReadyWindowRecord,
) (*readinessusecase.ReadyWindowRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.authority.Current != nil || record.ExpectedAuthorityRevision != r.authority.Revision {
		return nil, false, domain.ErrConflict
	}
	stored := cloneSemifinalReadyWindowRecord(record)
	r.authority.Revision++
	r.authority.Wave = cloneSemifinalWave(record.Wave)
	r.authority.Current = &stored
	return &stored, true, nil
}

type semifinalReadinessRepository struct {
	mu        sync.Mutex
	authority readinessusecase.ReadinessAuthority
}

func (r *semifinalReadinessRepository) LoadReadinessAuthority(
	_ context.Context, _ readinessusecase.ReadinessScope,
) (readinessusecase.ReadinessAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSemifinalReadinessAuthority(r.authority), nil
}

func (r *semifinalReadinessRepository) CommitReadiness(
	_ context.Context, commit readinessusecase.ReadinessCommit,
) (*readinessusecase.ReadinessRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if commit.ExpectedRevision != r.authority.Revision {
		return nil, false, domain.ErrConflict
	}
	r.authority.Revision++
	r.authority.Wave = cloneSemifinalWave(commit.Wave)
	r.authority.Events = append(r.authority.Events, commit.Event)
	record := readinessusecase.ReadinessRecord{
		Scope: r.authority.Scope, Revision: r.authority.Revision,
		Wave: cloneSemifinalWave(r.authority.Wave), Events: append([]readinessusecase.ReadinessEvent(nil), r.authority.Events...),
	}
	return &record, true, nil
}

func (r *semifinalReadinessRepository) setWave(wave domain.Wave) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authority.Wave = cloneSemifinalWave(wave)
}

type semifinalStartRepository struct {
	mu         sync.Mutex
	authority  gamestart.StartAuthority
	serverTime time.Time
}

func (r *semifinalStartRepository) LoadWaveStartAuthority(
	_ context.Context, _ gamestart.StartScope,
) (gamestart.StartAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSemifinalStartAuthority(r.authority), nil
}

func (r *semifinalStartRepository) ReadWaveStartTime(_ context.Context) (time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.serverTime, nil
}

func (r *semifinalStartRepository) CommitWaveStart(
	_ context.Context, record gamestart.StartRecord,
) (*gamestart.StartRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.authority.Current != nil || record.ExpectedWaveRevision != r.authority.WaveRevision {
		return nil, false, domain.ErrConflict
	}
	stored := cloneSemifinalStartRecord(record)
	r.authority.WaveRevision++
	r.authority.Wave = cloneSemifinalWave(record.Wave)
	r.authority.Current = &stored
	return &stored, true, nil
}

func (r *semifinalStartRepository) setWave(wave domain.Wave) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authority.Wave = cloneSemifinalWave(wave)
}

type semifinalSnapshot struct {
	value        domain.AssignmentTaskSnapshot
	digest       [sha256.Size]byte
	participants map[uuid.UUID]struct{}
}

func (s *semifinalSnapshot) Snapshot() domain.AssignmentTaskSnapshot {
	value := s.value
	value.Hints = append([]string(nil), s.value.Hints...)
	if s.value.TaskURL != nil {
		url := *s.value.TaskURL
		value.TaskURL = &url
	}
	if s.value.SourceFileURL != nil {
		url := *s.value.SourceFileURL
		value.SourceFileURL = &url
	}
	return value
}

func (s *semifinalSnapshot) ContentDigest() [sha256.Size]byte { return s.digest }

func (s *semifinalSnapshot) HasParticipant(participantID uuid.UUID) bool {
	_, found := s.participants[participantID]
	return found
}

type semifinalSubmissionRepository struct {
	mu         sync.Mutex
	authority  gamesubmission.SubmissionAuthority
	commitTime time.Time
}

func (r *semifinalSubmissionRepository) LoadSubmissionAuthority(
	_ context.Context, _ gamedomain.SubmissionScope,
) (gamesubmission.SubmissionAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSemifinalSubmissionAuthority(r.authority), nil
}

func (r *semifinalSubmissionRepository) CommitSubmission(
	_ context.Context, commit gamesubmission.SubmissionCommit,
) (*gamedomain.Submission, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if commit.ExpectedAuthorityRevision != r.authority.Revision {
		return nil, false, domain.ErrConflict
	}
	stored := gamedomain.Submission{
		Scope: commit.Scope, CommandID: commit.CommandID, ParticipantID: commit.ParticipantID,
		Sequence: int64(len(r.authority.Submissions) + 1), CommittedAt: r.commitTime,
		Correct: commit.Correct, SnapshotID: commit.SnapshotID, TaskID: commit.TaskID,
		ContentDigest: commit.ContentDigest,
	}
	r.authority.Revision++
	r.authority.Submissions = append(r.authority.Submissions, stored)
	return &stored, true, nil
}

func (r *semifinalSubmissionRepository) setStartedGame(started gamedomain.Started, commitTime time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authority.StartedGame = cloneSemifinalStartedGame(started)
	r.commitTime = commitTime
}

type semifinalSettlementRepository struct {
	mu        sync.Mutex
	authority gamesettlement.SettlementAuthority
}

func (r *semifinalSettlementRepository) LoadConcurrentWinnerAuthority(
	_ context.Context, _ gamedomain.SubmissionScope,
) (gamesettlement.SettlementAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSemifinalSettlementAuthority(r.authority), nil
}

func (r *semifinalSettlementRepository) CommitConcurrentWinnerSettlement(
	_ context.Context, settlement gamesettlement.SettlementRecord,
) (*gamesettlement.SettlementRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if settlement.ExpectedAuthorityRevision != r.authority.Revision || r.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := cloneSemifinalSettlementRecord(settlement)
	r.authority.Revision++
	r.authority.Current = &stored
	return &stored, true, nil
}

func (r *semifinalSettlementRepository) setAuthority(
	started gamedomain.Started,
	submissions []gamedomain.Submission,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authority.StartedGame = cloneSemifinalStartedGame(started)
	r.authority.Submissions = append([]gamedomain.Submission(nil), submissions...)
}

func cloneSemifinalWave(value domain.Wave) domain.Wave {
	clone := value
	clone.Members = append([]domain.WaveMember(nil), value.Members...)
	if value.ReadyWindow != nil {
		window := *value.ReadyWindow
		window.ConsumedAt = cloneSemifinalTimePointer(value.ReadyWindow.ConsumedAt)
		clone.ReadyWindow = &window
	}
	clone.StartedAt = cloneSemifinalTimePointer(value.StartedAt)
	clone.PausedAt = cloneSemifinalTimePointer(value.PausedAt)
	return clone
}

func cloneSemifinalTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSemifinalReadyWindowAuthority(value readinessusecase.ReadyWindowAuthority) readinessusecase.ReadyWindowAuthority {
	clone := value
	clone.Wave = cloneSemifinalWave(value.Wave)
	if value.Current != nil {
		current := cloneSemifinalReadyWindowRecord(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneSemifinalReadyWindowRecord(value readinessusecase.ReadyWindowRecord) readinessusecase.ReadyWindowRecord {
	clone := value
	clone.Wave = cloneSemifinalWave(value.Wave)
	return clone
}

func cloneSemifinalReadinessAuthority(value readinessusecase.ReadinessAuthority) readinessusecase.ReadinessAuthority {
	clone := value
	clone.Wave = cloneSemifinalWave(value.Wave)
	clone.Events = append([]readinessusecase.ReadinessEvent(nil), value.Events...)
	return clone
}

func cloneSemifinalStartAuthority(value gamestart.StartAuthority) gamestart.StartAuthority {
	clone := value
	clone.Wave = cloneSemifinalWave(value.Wave)
	clone.ReadinessRevisions = cloneSemifinalReadinessRevisions(value.ReadinessRevisions)
	clone.Games = make([]gamestart.GameAuthority, len(value.Games))
	for index, game := range value.Games {
		clone.Games[index] = game
		clone.Games[index].Series = seriesdomain.CloneExecution(game.Series)
	}
	if value.Current != nil {
		current := cloneSemifinalStartRecord(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneSemifinalStartRecord(value gamestart.StartRecord) gamestart.StartRecord {
	clone := value
	clone.Wave = cloneSemifinalWave(value.Wave)
	clone.ReadinessRevisions = cloneSemifinalReadinessRevisions(value.ReadinessRevisions)
	clone.Games = make([]gamedomain.Started, len(value.Games))
	for index, game := range value.Games {
		clone.Games[index] = game
		clone.Games[index].Series = seriesdomain.CloneExecution(game.Series)
	}
	return clone
}

func cloneSemifinalReadinessRevisions(value map[uuid.UUID]int64) map[uuid.UUID]int64 {
	if value == nil {
		return nil
	}
	clone := make(map[uuid.UUID]int64, len(value))
	for participantID, revision := range value {
		clone[participantID] = revision
	}
	return clone
}

func cloneSemifinalStartedGame(value gamedomain.Started) gamedomain.Started {
	clone := value
	clone.Series = seriesdomain.CloneExecution(value.Series)
	return clone
}

func cloneSemifinalSubmissionAuthority(value gamesubmission.SubmissionAuthority) gamesubmission.SubmissionAuthority {
	clone := value
	clone.StartedGame = cloneSemifinalStartedGame(value.StartedGame)
	clone.ConnectedParticipantIDs = append([]uuid.UUID(nil), value.ConnectedParticipantIDs...)
	clone.Submissions = append([]gamedomain.Submission(nil), value.Submissions...)
	return clone
}

func cloneSemifinalSettlementAuthority(value gamesettlement.SettlementAuthority) gamesettlement.SettlementAuthority {
	clone := value
	clone.StartedGame = cloneSemifinalStartedGame(value.StartedGame)
	clone.Submissions = append([]gamedomain.Submission(nil), value.Submissions...)
	clone.CurrentGameResultRevisionIDs = append([]domain.OfficialResultRevisionID(nil), value.CurrentGameResultRevisionIDs...)
	if value.Current != nil {
		current := cloneSemifinalSettlementRecord(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneSemifinalSettlementRecord(value gamesettlement.SettlementRecord) gamesettlement.SettlementRecord {
	clone := value
	clone.Game = cloneSemifinalGame(value.Game)
	clone.SettlementGameResultRevision.PreviousRevisionID = cloneSemifinalResultRevisionPointer(value.SettlementGameResultRevision.PreviousRevisionID)
	clone.ScoreRevision.PreviousRevisionID = cloneSemifinalScoreRevisionPointer(value.ScoreRevision.PreviousRevisionID)
	clone.ScoreRevision.GameResultRevisionIDs = append([]domain.OfficialResultRevisionID(nil), value.ScoreRevision.GameResultRevisionIDs...)
	clone.Series = seriesdomain.CloneExecution(seriesdomain.Execution{Series: value.Series}).Series
	return clone
}

func cloneSemifinalGame(value domain.Game) domain.Game {
	clone := value
	if value.WinnerID != nil {
		winnerID := *value.WinnerID
		clone.WinnerID = &winnerID
	}
	if value.ResultRevisionID != nil {
		resultID := *value.ResultRevisionID
		clone.ResultRevisionID = &resultID
	}
	return clone
}

func cloneSemifinalResultRevisionPointer(value *domain.OfficialResultRevisionID) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSemifinalScoreRevisionPointer(value *domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
