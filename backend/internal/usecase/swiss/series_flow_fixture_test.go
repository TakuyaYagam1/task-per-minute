package swiss_test

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
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gamesettlement "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/settlement"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
	gamesubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/submission"
	readinessusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	seriesgraph "github.com/TakuyaYagam1/task-per-minute/internal/usecase/seriesgraph"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func seriesFlowInput(t *testing.T, mode domain.CategoryMode) (swissusecase.SeriesFlowInput, domain.Category) {
	t.Helper()

	now := seriesFlowTime()
	tournamentID := seriesFlowID(1)
	seriesID := seriesFlowID(2)
	firstParticipantID := seriesFlowID(3)
	secondParticipantID := seriesFlowID(4)
	rosterID := seriesFlowID(5)
	categoryRevisionID := seriesFlowID(6)
	poolID := seriesFlowID(7)

	revision := seriesFlowCategoryRevision(t, mode, tournamentID, seriesID, rosterID, categoryRevisionID, now)
	selected := seriesFlowCategory(t, revision, mode, firstParticipantID, secondParticipantID, now)
	plan := seriesFlowExactPlan(t, now, tournamentID, rosterID, seriesID, categoryRevisionID, poolID,
		firstParticipantID, secondParticipantID, selected)
	series := domain.Series{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: domain.SeriesFormatBO1, State: domain.SeriesStatePlanned,
	}
	require.NoError(t, series.Validate())

	return swissusecase.SeriesFlowInput{
		PairingID:           seriesFlowID(8),
		AssignmentRevision:  7,
		ReservationRevision: 11,
		MaterializeInput: seriesgraph.MaterializeInput{
			CommandID:          seriesFlowID(9),
			DeliveredAt:        now.Add(5 * time.Minute),
			Series:             series,
			CategoryRevision:   revision,
			SelectedCategories: []domain.Category{selected},
			AssignmentPlans:    []assignmentusecase.ExactNormalAssignmentPlan{plan},
		},
	}, selected
}

func seriesFlowCategoryRevision(
	t *testing.T,
	mode domain.CategoryMode,
	tournamentID, seriesID, rosterID, revisionID uuid.UUID,
	now time.Time,
) draftusecase.CategoryRevision {
	t.Helper()

	configuration := seriesFlowContentConfiguration(t, tournamentID)
	command := draftusecase.CategoryRevisionCommand{
		ID: revisionID, SeriesID: seriesID, RosterID: rosterID,
		SeriesState: domain.SeriesStatePlanned, Stage: domain.TournamentStageSwiss,
		Configuration: configuration, CreatedAt: now,
	}
	if mode != domain.CategoryModeRandom {
		command.ModeOverride = &mode
	}
	revision, changed, err := draftusecase.DeriveCategoryRevision(nil, command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.SeriesFormatBO1, revision.Format)
	require.Equal(t, mode, revision.Mode)
	require.NoError(t, revision.Validate())
	return revision
}

func seriesFlowCategory(
	t *testing.T,
	revision draftusecase.CategoryRevision,
	mode domain.CategoryMode,
	firstParticipantID, secondParticipantID uuid.UUID,
	now time.Time,
) domain.Category {
	t.Helper()

	switch mode {
	case domain.CategoryModeRandom:
		lock, changed, err := draftusecase.LockRandom(nil, revision, draftusecase.RandomSelectionCommand{
			LockID: revision.ID, EvidenceID: seriesFlowID(10), LockedAt: now.Add(time.Minute),
		})
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, lock.Validate(revision))
		require.Len(t, lock.SelectedCategories, 1)
		return lock.SelectedCategories[0]
	case domain.CategoryModeAdmin:
		selected := domain.CategoryWeb
		lock, changed, err := draftusecase.LockAdmin(nil, revision, draftusecase.AdminSelectionCommand{
			LockID: revision.ID, ActorID: seriesFlowID(11),
			ExpectedCategoryRevisionID: revision.ID, ExpectedCategoryRevision: revision.Revision,
			SelectedCategory: &selected, Reason: "Swiss operator choice", LockedAt: now.Add(time.Minute),
		})
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, lock.Validate(revision))
		require.Len(t, lock.SelectedCategories, 1)
		return lock.SelectedCategories[0]
	case domain.CategoryModeDraft:
		draft, err := draftusecase.NewBO1(revision, draftusecase.StartCommand{
			DraftID: seriesFlowID(12), FirstParticipantID: firstParticipantID,
			SecondParticipantID: secondParticipantID, FirstDeadline: now.Add(time.Minute),
		})
		require.NoError(t, err)
		draft, err = draftusecase.ApplyBO1Action(draft, draftusecase.ActionCommand{
			ExpectedTurn: 1, ActorID: firstParticipantID, Action: domain.DraftActionBan,
			Category: domain.CategoryCrypto, OccurredAt: now.Add(10 * time.Second),
			NextDeadline: now.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		draft, err = draftusecase.ApplyBO1Action(draft, draftusecase.ActionCommand{
			ExpectedTurn: 2, ActorID: secondParticipantID, Action: domain.DraftActionBan,
			Category: domain.CategoryReverse, OccurredAt: now.Add(70 * time.Second),
		})
		require.NoError(t, err)
		selected, err := draftusecase.BO1GameCategory(draft)
		require.NoError(t, err)
		return selected
	default:
		t.Fatalf("unsupported category mode %s", mode)
		return ""
	}
}

func seriesFlowContentConfiguration(t *testing.T, tournamentID uuid.UUID) domain.ContentConfiguration {
	t.Helper()

	bo1PoolID := seriesFlowID(20)
	bo3PoolID := seriesFlowID(21)
	configuration, err := domain.CreateContentConfiguration(domain.ContentConfigurationInput{
		TournamentID: tournamentID,
		CategoryPools: []domain.CategoryPoolRevision{
			{ID: bo1PoolID, Revision: 1, Format: domain.SeriesFormatBO1,
				Categories: []domain.Category{domain.CategoryCrypto, domain.CategoryReverse, domain.CategoryWeb}},
			{ID: bo3PoolID, Revision: 1, Format: domain.SeriesFormatBO3,
				Categories: []domain.Category{domain.CategoryCrypto, domain.CategoryForensics, domain.CategoryPwn, domain.CategoryReverse, domain.CategoryWeb}},
		},
		NormalPool: domain.TaskPoolRevision{
			ID: seriesFlowID(22), Revision: 1, Kind: domain.AssignmentTaskKindNormal,
			Versions: []domain.TaskVersionRef{{TaskID: seriesFlowID(23), Version: 1}},
		},
		GoldenPool: domain.TaskPoolRevision{
			ID: seriesFlowID(24), Revision: 1, Kind: domain.AssignmentTaskKindGolden,
			Versions: []domain.TaskVersionRef{{TaskID: seriesFlowID(25), Version: 1}},
		},
		StageDefaults: []domain.StageContentDefault{
			{Stage: domain.TournamentStageSwiss, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeRandom, CategoryPoolRevisionID: bo1PoolID, TaskPoolKind: domain.AssignmentTaskKindNormal},
			{Stage: domain.TournamentStageGolden, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeRandom, CategoryPoolRevisionID: bo1PoolID, TaskPoolKind: domain.AssignmentTaskKindGolden},
			{Stage: domain.TournamentStageSemifinal, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeAdmin, CategoryPoolRevisionID: bo1PoolID, TaskPoolKind: domain.AssignmentTaskKindNormal},
			{Stage: domain.TournamentStageFinal, Format: domain.SeriesFormatBO3, CategoryMode: domain.CategoryModeDraft, CategoryPoolRevisionID: bo3PoolID, TaskPoolKind: domain.AssignmentTaskKindNormal},
		},
	})
	require.NoError(t, err)
	return configuration
}

func seriesFlowExactPlan(
	t *testing.T,
	now time.Time,
	tournamentID, rosterID, seriesID, categoryRevisionID, poolID uuid.UUID,
	firstParticipantID, secondParticipantID uuid.UUID,
	category domain.Category,
) assignmentusecase.ExactNormalAssignmentPlan {
	t.Helper()

	scope := assignmentusecase.ExactNormalAssignmentScope{
		TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID,
		SlotID: seriesFlowID(30), CategoryLockID: categoryRevisionID,
	}
	participants := []uuid.UUID{firstParticipantID, secondParticipantID}
	tasks := make([]domain.Task, 3)
	versions := make([]domain.TaskVersionRef, len(tasks))
	candidates := make([]assignmentusecase.ExactNormalTaskVersion, len(tasks))
	for index := range tasks {
		taskID := seriesFlowID(40 + index)
		tasks[index] = domain.Task{
			ID: taskID, Title: fmt.Sprintf("%s task %d", category, index),
			Description: "Solve this Swiss task.", Category: category,
			Difficulty: domain.DifficultyHard, TimeLimit: 90,
			Flag: fmt.Sprintf("FLAG{%s}", taskID), Hints: []string{"hint one", "hint two"},
		}
		versions[index] = domain.TaskVersionRef{TaskID: taskID, Version: 1}
		candidates[index] = assignmentusecase.ExactNormalTaskVersion{PoolRevisionID: poolID, Version: 1, Task: tasks[index]}
	}
	authority := assignmentusecase.ExactNormalAssignmentAuthority{
		Scope: scope, Category: category,
		Revisions: assignmentusecase.ExactNormalAssignmentSourceRevisions{
			SeriesRevision: 1, PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: seriesFlowID(50), HistoryRevision: 1, RosterRevision: 1,
			ArtifactRevisionID: seriesFlowID(51), ArtifactRevision: 1,
			CategoryRevisionID: categoryRevisionID, CategoryRevision: 1,
		},
		Pool:           domain.TaskPoolRevision{ID: poolID, Revision: 1, Kind: domain.AssignmentTaskKindNormal, Versions: versions},
		ParticipantIDs: participants,
		ParticipantReservations: []assignmentusecase.ExactNormalParticipantReservation{
			{ParticipantID: firstParticipantID, PlayerID: seriesFlowID(60), Reservation: seriesFlowReservation(seriesFlowID(61), seriesFlowID(60), tournamentID, now)},
			{ParticipantID: secondParticipantID, PlayerID: seriesFlowID(62), Reservation: seriesFlowReservation(seriesFlowID(63), seriesFlowID(62), tournamentID, now)},
		},
		Candidates: candidates, GraphDigest: sha256.Sum256([]byte("series-flow-graph")), ArtifactDigest: sha256.Sum256([]byte("series-flow-artifact")),
	}
	command := assignmentusecase.ExactNormalAssignmentCommand{
		Scope: scope, PlanID: seriesFlowID(70), PlanRevisionID: seriesFlowID(71),
		BranchID: seriesFlowID(72), DecisionEvidenceID: seriesFlowID(73), CreatedAt: now,
	}
	for index := range command.EdgeIDs {
		command.EdgeIDs[index] = seriesFlowID(80 + index)
		command.ReservationIDs[index] = seriesFlowID(90 + index)
		command.SnapshotIDs[index] = seriesFlowID(100 + index)
	}
	plan, err := assignmentusecase.BuildExactNormalAssignment(command, authority)
	require.NoError(t, err)
	require.NoError(t, plan.Validate())
	return plan
}

func seriesFlowReservation(id, playerID, tournamentID uuid.UUID, at time.Time) domain.ParticipantReservation {
	return domain.ParticipantReservation{
		PlayerID: playerID, ReservationID: id, TournamentID: tournamentID,
		Revision: 1, AcquiredAt: at, UpdatedAt: at,
	}
}

func roundLockProofInputFromSeriesFlow(flow swissusecase.SeriesFlow) swissusecase.RoundLockProofInput {
	participants := []uuid.UUID{flow.Graph.Series.FirstParticipantID, flow.Graph.Series.SecondParticipantID,
		seriesFlowID(110), seriesFlowID(111)}
	return swissusecase.RoundLockProofInput{
		TournamentID: flow.Graph.Series.TournamentID, RosterID: flow.Graph.CategoryRevision.RosterID,
		RoundID: seriesFlowID(112), Preset: domain.TournamentPresetV1, RoundNumber: 1,
		SourceProjectionRevisionID: seriesFlowID(113), PreflightRevisionID: seriesFlowID(114),
		NormalPoolRevisionID: seriesFlowID(115), WaveID: seriesFlowID(116),
		WaveRevisionID:       domain.WaveRevisionID(seriesFlowID(117)),
		Revisions:            swissusecase.RoundLockRevisions{SourceProjection: 1, Roster: 1, Round: 1, History: 0, NormalPool: 1, Wave: 1},
		RosterParticipantIDs: participants,
		Series: []swissusecase.LockedSeries{
			flow.LockedSeries,
			{SeriesID: seriesFlowID(118), PairingID: seriesFlowID(119), FirstParticipantID: participants[2], SecondParticipantID: participants[3], CategoryRevisionID: seriesFlowID(120), CategoryRevision: 1, AssignmentID: seriesFlowID(121), AssignmentRevision: 1, AssignmentPlanID: seriesFlowID(122), AssignmentPlanRevisionID: seriesFlowID(123), ReservationID: seriesFlowID(124), ReservationRevision: 1},
		},
	}
}

func cloneSeriesFlowInput(input swissusecase.SeriesFlowInput) swissusecase.SeriesFlowInput {
	clone := input
	clone.MaterializeInput = cloneSeriesGraphInput(input.MaterializeInput)
	return clone
}

func cloneSeriesGraphInput(input seriesgraph.MaterializeInput) seriesgraph.MaterializeInput {
	clone := input
	clone.SelectedCategories = append([]domain.Category(nil), input.SelectedCategories...)
	clone.AssignmentPlans = make([]assignmentusecase.ExactNormalAssignmentPlan, len(input.AssignmentPlans))
	for index, plan := range input.AssignmentPlans {
		clone.AssignmentPlans[index] = assignmentusecase.CloneExactNormalAssignmentPlan(plan)
	}
	clone.Series.Slots = append([]domain.GameSlot(nil), input.Series.Slots...)
	clone.CategoryRevision.CategoryPool.Categories = append([]domain.Category(nil), input.CategoryRevision.CategoryPool.Categories...)
	return clone
}

func seriesFlowTime() time.Time {
	return time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
}

func seriesFlowID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("71000000-0000-0000-0000-%012d", number))
}

type seriesFlowExecutionFixture struct {
	readyWindow *readinessusecase.ReadyWindowUseCase
	readiness   *readinessusecase.ReadinessUseCase
	start       *gamestart.StartUseCase
	submission  *gamesubmission.SubmissionUseCase
	settlement  *gamesettlement.SettlementUseCase

	readyWindowRepo *seriesFlowReadyWindowRepository
	readinessRepo   *seriesFlowReadinessRepository
	startRepo       *seriesFlowStartRepository
	submissionRepo  *seriesFlowSubmissionRepository
	settlementRepo  *seriesFlowSettlementRepository

	openCommand       readinessusecase.OpenReadyWindowCommand
	readyCommands     [2]readinessusecase.ReadyCommand
	startCommand      gamestart.StartCommand
	submissionCommand gamesubmission.SubmissionCommand
	settlementCommand gamesettlement.SettlementCommand
}

func newSeriesFlowExecutionFixture(t *testing.T, flow swissusecase.SeriesFlow) *seriesFlowExecutionFixture {
	t.Helper()
	require.NoError(t, flow.Validate())

	now := seriesFlowTime().Add(10 * time.Minute)
	tournamentID := flow.Graph.Series.TournamentID
	firstParticipantID := flow.Graph.Series.FirstParticipantID
	secondParticipantID := flow.Graph.Series.SecondParticipantID
	waveID := seriesFlowID(130)
	waveRevisionID := domain.WaveRevisionID(seriesFlowID(131))
	windowID := seriesFlowID(132)
	windowRevisionID := domain.ReadyWindowRevisionID(seriesFlowID(133))
	revisions := domain.ReadyWindowSourceRevisions{
		WaveRevisionID: waveRevisionID, WaveRevision: 1,
		ProjectionRevisionID: seriesFlowID(134), ProjectionRevision: 1,
		ArtifactRevisionID: seriesFlowID(135), ArtifactRevision: 1,
	}
	plannedWave := domain.Wave{
		ID: waveID, TournamentID: tournamentID, RevisionID: waveRevisionID,
		State:   domain.WaveStatePlanned,
		Members: []domain.WaveMember{{ParticipantID: firstParticipantID}, {ParticipantID: secondParticipantID}},
	}
	scope := readinessusecase.ReadyWindowScope{TournamentID: tournamentID, WaveID: waveID}
	openCommand := readinessusecase.OpenReadyWindowCommand{
		Scope: scope, CommandID: seriesFlowID(136), WindowID: windowID,
		WindowRevisionID: windowRevisionID, ExpectedRevisions: revisions,
	}
	readyWindowRepo := &seriesFlowReadyWindowRepository{authority: readinessusecase.ReadyWindowAuthority{
		Scope: scope, Revision: 1, Revisions: revisions, Wave: plannedWave,
	}}
	readinessScope := readinessusecase.ReadinessScope{WaveID: waveID, WindowID: windowID}
	readinessRepo := &seriesFlowReadinessRepository{authority: readinessusecase.ReadinessAuthority{
		Scope: readinessScope, Revision: 1, Wave: plannedWave,
	}}
	readyCommands := [2]readinessusecase.ReadyCommand{
		{
			Scope: readinessScope, CommandID: seriesFlowID(137),
			ActorParticipantID: firstParticipantID, ParticipantID: firstParticipantID,
			ExpectedWaveRevisionID: waveRevisionID, ExpectedWindowRevisionID: windowRevisionID,
		},
		{
			Scope: readinessScope, CommandID: seriesFlowID(138),
			ActorParticipantID: secondParticipantID, ParticipantID: secondParticipantID,
			ExpectedWaveRevisionID: waveRevisionID, ExpectedWindowRevisionID: windowRevisionID,
		},
	}

	readySeries, transitionChanged, err := seriesdomain.Transition(
		seriesdomain.Execution{Series: flow.Graph.Series},
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
	aggregate := flow.Graph.Assignments[0]
	primary := aggregate.Plan.SelectedEdges[0]
	gameScope := gamedomain.Scope{
		TournamentID: tournamentID, SeriesID: readySeries.Series.ID,
		SlotID: slot.ID, GameID: attempt.ID,
	}
	startScope := gamestart.StartScope{TournamentID: tournamentID, WaveID: waveID, WindowID: windowID}
	startGame := gamestart.GameAuthority{
		Scope:          gameScope,
		ParticipantIDs: [2]uuid.UUID{firstParticipantID, secondParticipantID},
		Series:         readySeries, AssignmentID: aggregate.ID,
		AssignmentRevision: flow.LockedSeries.AssignmentRevision,
		PlanRevisionID:     aggregate.Plan.PlanRevisionID,
		SnapshotID:         primary.Snapshot.SnapshotID, ContentDigest: primary.ContentDigest,
		DeadlineSeconds: 180,
	}
	startAuthority := gamestart.StartAuthority{
		Scope: startScope, WaveRevision: 1, Revisions: revisions,
		ReadinessRevisions: map[uuid.UUID]int64{firstParticipantID: 1, secondParticipantID: 1},
		Wave:               plannedWave, Games: []gamestart.GameAuthority{startGame},
	}
	startRepo := &seriesFlowStartRepository{authority: startAuthority, serverTime: now}
	startCommand := gamestart.StartCommand{
		Scope: startScope, CommandID: seriesFlowID(139), ActorID: seriesFlowID(140),
		ExecutionAuthority: authoritydomain.Identity{
			TournamentID: tournamentID, HolderID: seriesFlowID(141), LeaseID: seriesFlowID(142),
			Epoch: 1, ProcessKind: authoritydomain.ProcessAuthority,
		},
		ExpectedProjectionRevision: revisions.ProjectionRevision,
		ExpectedRevisions:          revisions, RequestDigest: sha256.Sum256([]byte("series-flow-start")),
	}

	snapshot := &seriesFlowSnapshot{
		value: primary.Snapshot, digest: primary.ContentDigest,
		participants: map[uuid.UUID]struct{}{firstParticipantID: {}, secondParticipantID: {}},
	}
	submissionScope := gamedomain.SubmissionScope{WaveID: waveID, Game: gameScope, AssignmentID: aggregate.ID}
	submissionRepo := &seriesFlowSubmissionRepository{
		authority: gamesubmission.SubmissionAuthority{
			Scope: submissionScope, Revision: 1, Snapshot: snapshot,
			ConnectedParticipantIDs: []uuid.UUID{firstParticipantID, secondParticipantID},
		},
	}
	submissionCommand := gamesubmission.SubmissionCommand{
		Scope: submissionScope, CommandID: seriesFlowID(143),
		ActorParticipantID: firstParticipantID, ParticipantID: firstParticipantID,
		SubmittedFlag: primary.Snapshot.Flag,
	}
	settlementRepo := &seriesFlowSettlementRepository{authority: gamesettlement.SettlementAuthority{
		Scope: submissionScope, Revision: 1, CurrentScoreOrdinal: 0,
		CurrentProjectionRevision: 1,
	}}
	settlementCommand := gamesettlement.SettlementCommand{Scope: submissionScope, CommandID: submissionCommand.CommandID}
	clock := seriesFlowClock{now: now}
	return &seriesFlowExecutionFixture{
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

type seriesFlowClock struct {
	now time.Time
}

func (c seriesFlowClock) Now() time.Time {
	return c.now
}

type seriesFlowReadyWindowRepository struct {
	mu        sync.Mutex
	authority readinessusecase.ReadyWindowAuthority
}

func (r *seriesFlowReadyWindowRepository) LoadReadyWindowAuthority(
	_ context.Context,
	_ readinessusecase.ReadyWindowScope,
) (readinessusecase.ReadyWindowAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSeriesFlowReadyWindowAuthority(r.authority), nil
}

func (r *seriesFlowReadyWindowRepository) CommitReadyWindow(
	_ context.Context,
	record readinessusecase.ReadyWindowRecord,
) (*readinessusecase.ReadyWindowRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.authority.Current != nil || record.ExpectedAuthorityRevision != r.authority.Revision {
		return nil, false, domain.ErrConflict
	}
	stored := cloneSeriesFlowReadyWindowRecord(record)
	r.authority.Revision++
	r.authority.Wave = cloneSeriesFlowWave(record.Wave)
	r.authority.Current = &stored
	return &stored, true, nil
}

type seriesFlowReadinessRepository struct {
	mu        sync.Mutex
	authority readinessusecase.ReadinessAuthority
}

func (r *seriesFlowReadinessRepository) LoadReadinessAuthority(
	_ context.Context,
	_ readinessusecase.ReadinessScope,
) (readinessusecase.ReadinessAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSeriesFlowReadinessAuthority(r.authority), nil
}

func (r *seriesFlowReadinessRepository) CommitReadiness(
	_ context.Context,
	commit readinessusecase.ReadinessCommit,
) (*readinessusecase.ReadinessRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if commit.ExpectedRevision != r.authority.Revision {
		return nil, false, domain.ErrConflict
	}
	r.authority.Revision++
	r.authority.Wave = cloneSeriesFlowWave(commit.Wave)
	r.authority.Events = append(r.authority.Events, commit.Event)
	record := readinessusecase.ReadinessRecord{
		Scope: r.authority.Scope, Revision: r.authority.Revision,
		Wave: cloneSeriesFlowWave(r.authority.Wave), Events: append([]readinessusecase.ReadinessEvent(nil), r.authority.Events...),
	}
	return &record, true, nil
}

func (r *seriesFlowReadinessRepository) setWave(wave domain.Wave) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authority.Wave = cloneSeriesFlowWave(wave)
}

type seriesFlowStartRepository struct {
	mu         sync.Mutex
	authority  gamestart.StartAuthority
	serverTime time.Time
}

func (r *seriesFlowStartRepository) LoadWaveStartAuthority(
	_ context.Context,
	_ gamestart.StartScope,
) (gamestart.StartAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSeriesFlowStartAuthority(r.authority), nil
}

func (r *seriesFlowStartRepository) ReadWaveStartTime(_ context.Context) (time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.serverTime, nil
}

func (r *seriesFlowStartRepository) CommitWaveStart(
	_ context.Context,
	record gamestart.StartRecord,
) (*gamestart.StartRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.authority.Current != nil || record.ExpectedWaveRevision != r.authority.WaveRevision {
		return nil, false, domain.ErrConflict
	}
	stored := cloneSeriesFlowStartRecord(record)
	r.authority.WaveRevision++
	r.authority.Wave = cloneSeriesFlowWave(record.Wave)
	r.authority.Current = &stored
	return &stored, true, nil
}

func (r *seriesFlowStartRepository) setWave(wave domain.Wave) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authority.Wave = cloneSeriesFlowWave(wave)
}

type seriesFlowSnapshot struct {
	value        domain.AssignmentTaskSnapshot
	digest       [sha256.Size]byte
	participants map[uuid.UUID]struct{}
}

func (s *seriesFlowSnapshot) Snapshot() domain.AssignmentTaskSnapshot {
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

func (s *seriesFlowSnapshot) ContentDigest() [sha256.Size]byte {
	return s.digest
}

func (s *seriesFlowSnapshot) HasParticipant(participantID uuid.UUID) bool {
	_, found := s.participants[participantID]
	return found
}

type seriesFlowSubmissionRepository struct {
	mu         sync.Mutex
	authority  gamesubmission.SubmissionAuthority
	commitTime time.Time
}

func (r *seriesFlowSubmissionRepository) LoadSubmissionAuthority(
	_ context.Context,
	_ gamedomain.SubmissionScope,
) (gamesubmission.SubmissionAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSeriesFlowSubmissionAuthority(r.authority), nil
}

func (r *seriesFlowSubmissionRepository) CommitSubmission(
	_ context.Context,
	commit gamesubmission.SubmissionCommit,
) (*gamedomain.Submission, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if commit.ExpectedAuthorityRevision != r.authority.Revision {
		return nil, false, domain.ErrConflict
	}
	stored := gamedomain.Submission{
		Scope: commit.Scope, CommandID: commit.CommandID,
		ParticipantID: commit.ParticipantID, Sequence: int64(len(r.authority.Submissions) + 1),
		CommittedAt: r.commitTime, Correct: commit.Correct,
		SnapshotID: commit.SnapshotID, TaskID: commit.TaskID, ContentDigest: commit.ContentDigest,
	}
	r.authority.Revision++
	r.authority.Submissions = append(r.authority.Submissions, stored)
	return &stored, true, nil
}

func (r *seriesFlowSubmissionRepository) setStartedGame(started gamedomain.Started, commitTime time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authority.StartedGame = cloneSeriesFlowStartedGame(started)
	r.commitTime = commitTime
}

type seriesFlowSettlementRepository struct {
	mu        sync.Mutex
	authority gamesettlement.SettlementAuthority
}

func (r *seriesFlowSettlementRepository) LoadConcurrentWinnerAuthority(
	_ context.Context,
	_ gamedomain.SubmissionScope,
) (gamesettlement.SettlementAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSeriesFlowSettlementAuthority(r.authority), nil
}

func (r *seriesFlowSettlementRepository) CommitConcurrentWinnerSettlement(
	_ context.Context,
	settlement gamesettlement.SettlementRecord,
) (*gamesettlement.SettlementRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if settlement.ExpectedAuthorityRevision != r.authority.Revision || r.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := cloneSeriesFlowSettlementRecord(settlement)
	r.authority.Revision++
	r.authority.Current = &stored
	return &stored, true, nil
}

func (r *seriesFlowSettlementRepository) setAuthority(
	started gamedomain.Started,
	submissions []gamedomain.Submission,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authority.StartedGame = cloneSeriesFlowStartedGame(started)
	r.authority.Submissions = append([]gamedomain.Submission(nil), submissions...)
}

func cloneSeriesFlowWave(value domain.Wave) domain.Wave {
	clone := value
	clone.Members = append([]domain.WaveMember(nil), value.Members...)
	if value.ReadyWindow != nil {
		window := *value.ReadyWindow
		window.ConsumedAt = cloneSeriesFlowTimePointer(value.ReadyWindow.ConsumedAt)
		clone.ReadyWindow = &window
	}
	clone.StartedAt = cloneSeriesFlowTimePointer(value.StartedAt)
	clone.PausedAt = cloneSeriesFlowTimePointer(value.PausedAt)
	return clone
}

func cloneSeriesFlowTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSeriesFlowReadyWindowAuthority(value readinessusecase.ReadyWindowAuthority) readinessusecase.ReadyWindowAuthority {
	clone := value
	clone.Wave = cloneSeriesFlowWave(value.Wave)
	if value.Current != nil {
		current := cloneSeriesFlowReadyWindowRecord(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneSeriesFlowReadyWindowRecord(value readinessusecase.ReadyWindowRecord) readinessusecase.ReadyWindowRecord {
	clone := value
	clone.Wave = cloneSeriesFlowWave(value.Wave)
	return clone
}

func cloneSeriesFlowReadinessAuthority(value readinessusecase.ReadinessAuthority) readinessusecase.ReadinessAuthority {
	clone := value
	clone.Wave = cloneSeriesFlowWave(value.Wave)
	clone.Events = append([]readinessusecase.ReadinessEvent(nil), value.Events...)
	return clone
}

func cloneSeriesFlowStartAuthority(value gamestart.StartAuthority) gamestart.StartAuthority {
	clone := value
	clone.Wave = cloneSeriesFlowWave(value.Wave)
	clone.ReadinessRevisions = cloneSeriesFlowReadinessRevisions(value.ReadinessRevisions)
	clone.Games = make([]gamestart.GameAuthority, len(value.Games))
	for index, game := range value.Games {
		clone.Games[index] = game
		clone.Games[index].Series = seriesdomain.CloneExecution(game.Series)
	}
	if value.Current != nil {
		current := cloneSeriesFlowStartRecord(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneSeriesFlowStartRecord(value gamestart.StartRecord) gamestart.StartRecord {
	clone := value
	clone.Wave = cloneSeriesFlowWave(value.Wave)
	clone.ReadinessRevisions = cloneSeriesFlowReadinessRevisions(value.ReadinessRevisions)
	clone.Games = make([]gamedomain.Started, len(value.Games))
	for index, game := range value.Games {
		clone.Games[index] = game
		clone.Games[index].Series = seriesdomain.CloneExecution(game.Series)
	}
	return clone
}

func cloneSeriesFlowReadinessRevisions(value map[uuid.UUID]int64) map[uuid.UUID]int64 {
	if value == nil {
		return nil
	}
	clone := make(map[uuid.UUID]int64, len(value))
	for participantID, revision := range value {
		clone[participantID] = revision
	}
	return clone
}

func cloneSeriesFlowStartedGame(value gamedomain.Started) gamedomain.Started {
	clone := value
	clone.ParticipantIDs = value.ParticipantIDs
	clone.Series = seriesdomain.CloneExecution(value.Series)
	return clone
}

func cloneSeriesFlowSubmissionAuthority(value gamesubmission.SubmissionAuthority) gamesubmission.SubmissionAuthority {
	clone := value
	clone.StartedGame = cloneSeriesFlowStartedGame(value.StartedGame)
	clone.ConnectedParticipantIDs = append([]uuid.UUID(nil), value.ConnectedParticipantIDs...)
	clone.Submissions = append([]gamedomain.Submission(nil), value.Submissions...)
	return clone
}

func cloneSeriesFlowSettlementAuthority(value gamesettlement.SettlementAuthority) gamesettlement.SettlementAuthority {
	clone := value
	clone.StartedGame = cloneSeriesFlowStartedGame(value.StartedGame)
	clone.Submissions = append([]gamedomain.Submission(nil), value.Submissions...)
	clone.CurrentGameResultRevisionIDs = append([]domain.OfficialResultRevisionID(nil), value.CurrentGameResultRevisionIDs...)
	if value.Current != nil {
		current := cloneSeriesFlowSettlementRecord(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneSeriesFlowSettlementRecord(value gamesettlement.SettlementRecord) gamesettlement.SettlementRecord {
	clone := value
	clone.Game = cloneSeriesFlowGame(value.Game)
	clone.SettlementGameResultRevision.PreviousRevisionID = cloneSeriesFlowResultRevisionPointer(value.SettlementGameResultRevision.PreviousRevisionID)
	clone.ScoreRevision.PreviousRevisionID = cloneSeriesFlowScoreRevisionPointer(value.ScoreRevision.PreviousRevisionID)
	clone.ScoreRevision.GameResultRevisionIDs = append([]domain.OfficialResultRevisionID(nil), value.ScoreRevision.GameResultRevisionIDs...)
	clone.Series = seriesdomain.CloneExecution(seriesdomain.Execution{Series: value.Series}).Series
	return clone
}

func cloneSeriesFlowGame(value domain.Game) domain.Game {
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

func cloneSeriesFlowResultRevisionPointer(value *domain.OfficialResultRevisionID) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSeriesFlowScoreRevisionPointer(value *domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
