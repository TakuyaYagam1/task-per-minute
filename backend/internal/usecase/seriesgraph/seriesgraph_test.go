package seriesgraph_test

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	gamestate "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesstate "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	seriesgraph "github.com/TakuyaYagam1/task-per-minute/internal/usecase/seriesgraph"
)

func TestMaterializeSeriesGraphBO1(t *testing.T) {
	input := seriesGraphFixture(t, domain.SeriesFormatBO1)
	graph, changed, err := seriesgraph.Materialize(nil, input)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, graph.Validate())
	require.Len(t, graph.Series.Slots, 1)
	require.Len(t, graph.Assignments, 1)
	require.Len(t, graph.TaskReservations, 3)
	require.Len(t, graph.Snapshots, 3)
	require.Len(t, graph.ParticipantReservations, 2)
	require.Len(t, graph.DeliveryReceipts, 2)
	require.NotEqual(t, [sha256.Size]byte{}, graph.Proof.GraphDigest)
	require.NotEmpty(t, graph.Proof.ProofHash)
	require.Equal(t, input.DeliveredAt, graph.DeliveryReceipts[0].DeliveredAt)
}

func TestMaterializeSeriesGraphBO3(t *testing.T) {
	input := seriesGraphFixture(t, domain.SeriesFormatBO3)
	graph, changed, err := seriesgraph.Materialize(nil, input)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, graph.Validate())
	require.Len(t, graph.Series.Slots, 1)
	require.Len(t, graph.Assignments, 3)
	require.Len(t, graph.TaskReservations, 9)
	require.Len(t, graph.Snapshots, 9)
	require.Len(t, graph.ParticipantReservations, 2)
	require.Len(t, graph.DeliveryReceipts, 2)
	for index, slot := range graph.Series.Slots {
		require.Equal(t, index+1, slot.Position)
		require.Len(t, slot.Attempts, 1)
		require.Equal(t, domain.GameStatePlanned, slot.Attempts[0].State)
	}
	for index := 1; index < len(graph.Assignments); index++ {
		require.Equal(t, index+1, graph.Assignments[index].Position)
		require.NotEqual(t, graph.Assignments[index-1].SlotID, graph.Assignments[index].SlotID)
		require.NotEqual(t, graph.Assignments[index-1].AttemptID, graph.Assignments[index].AttemptID)
		require.Empty(t, graph.Assignments[index].DeliveryReceipts)
		require.Empty(t, graph.Assignments[index].Assignment.Receipts())
	}
}

func TestMaterializeSeriesGraphBO3ProgressUsesReservedFutureBinding(t *testing.T) {
	input := seriesGraphFixture(t, domain.SeriesFormatBO3)
	graph, changed, err := seriesgraph.Materialize(nil, input)
	require.NoError(t, err)
	require.True(t, changed)

	current := seriesstate.Execution{Series: graph.Series}
	for _, nextState := range []domain.SeriesState{
		domain.SeriesStateLocked,
		domain.SeriesStateDraft,
		domain.SeriesStateReady,
		domain.SeriesStateActive,
	} {
		transitioned, transitionChanged, transitionErr := seriesstate.Transition(current, seriesstate.TransitionCommand{
			NextState: nextState,
		})
		require.NoError(t, transitionErr)
		require.True(t, transitionChanged)
		current = transitioned
	}

	planned := current.Series.Slots[0].Attempts[0]
	readySlot, slotChanged, err := gamestate.TransitionSlotAttempt(current.Series.Slots[0], gamestate.TransitionCommand{
		GameID: planned.ID, ExpectedAttemptNo: planned.AttemptNo,
		ExpectedState: domain.GameStatePlanned, NextState: domain.GameStateReady,
	})
	require.NoError(t, err)
	require.True(t, slotChanged)
	activeSlot, slotChanged, err := gamestate.TransitionSlotAttempt(readySlot, gamestate.TransitionCommand{
		GameID: planned.ID, ExpectedAttemptNo: planned.AttemptNo,
		ExpectedState: domain.GameStateReady, NextState: domain.GameStateActive,
	})
	require.NoError(t, err)
	require.True(t, slotChanged)
	current.Series.Slots[0] = activeSlot

	completed := current.Series.Slots[0].Attempts[0]
	winnerID := current.Series.FirstParticipantID
	resultRevisionID := domain.OfficialResultRevisionID(testID(90_001))
	completed.State = domain.GameStateCompleted
	completed.ResultReason = domain.GameResultReasonSolved
	completed.WinnerID = &winnerID
	completed.ResultRevisionID = &resultRevisionID

	second := graph.Assignments[1]
	next := seriesstate.NextGameWave{
		WaveID:         testID(90_002),
		WaveRevisionID: domain.WaveRevisionID(testID(90_003)),
		SlotID:         second.SlotID,
		GameID:         second.AttemptID,
		Category:       second.Plan.Category,
	}
	progression, changed, err := seriesstate.ProgressScore(current, seriesstate.ScoreProgressionCommand{
		Game: completed,
		ScoreRevision: seriesstate.ScoreRevision{
			ID:                    domain.SeriesScoreRevisionID(testID(90_004)),
			SeriesID:              current.Series.ID,
			FirstParticipantID:    current.Series.FirstParticipantID,
			SecondParticipantID:   current.Series.SecondParticipantID,
			Ordinal:               1,
			Format:                current.Series.Format,
			ScoreBefore:           current.Series.Score,
			ScoreAfter:            domain.SeriesScore{FirstParticipantWins: 1},
			GameResultRevisionIDs: []domain.OfficialResultRevisionID{resultRevisionID},
			RecordedAt:            input.DeliveredAt.Add(time.Minute),
		},
		Next: &next,
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.SeriesStateActive, progression.Series.Series.State)
	require.Equal(t, domain.SeriesScore{FirstParticipantWins: 1}, progression.Series.Series.Score)
	require.Len(t, progression.Series.Series.Slots, 2)
	require.NotNil(t, progression.NextWave)
	require.Equal(t, next.WaveID, progression.NextWave.ID)

	added := progression.Series.Series.Slots[1]
	require.Equal(t, second.SlotID, added.ID)
	require.Equal(t, second.Plan.Category, added.Category)
	require.Equal(t, domain.SeriesScore{FirstParticipantWins: 1}, added.ScoreBefore)
	require.Len(t, added.Attempts, 1)
	require.Equal(t, second.AttemptID, added.Attempts[0].ID)
	require.Equal(t, domain.GameStatePlanned, added.Attempts[0].State)

	third := graph.Assignments[2]
	require.NotEqual(t, added.ID, third.SlotID)
	require.NotEqual(t, added.Attempts[0].ID, third.AttemptID)
	require.Equal(t, 3, third.Position)
	require.Empty(t, third.DeliveryReceipts)
	require.Empty(t, third.Assignment.Receipts())
	require.Len(t, graph.Series.Slots, 1)
	require.NoError(t, graph.Validate())
	require.NoError(t, progression.Validate())
}

func TestMaterializeSeriesGraphRejectsDeliveryBeforeSourceCreation(t *testing.T) {
	base := seriesGraphFixture(t, domain.SeriesFormatBO3)
	tests := map[string]func(*seriesgraph.MaterializeInput){
		"category authority": func(input *seriesgraph.MaterializeInput) {
			input.DeliveredAt = input.CategoryRevision.CreatedAt.Add(-time.Second)
		},
		"assignment plan": func(input *seriesgraph.MaterializeInput) {
			input.CategoryRevision.CreatedAt = input.AssignmentPlans[0].CreatedAt.Add(-time.Minute)
			input.DeliveredAt = input.AssignmentPlans[1].CreatedAt.Add(-time.Second)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := cloneInput(base)
			mutate(&input)
			_, changed, err := seriesgraph.Materialize(nil, input)
			require.False(t, changed)
			require.ErrorIs(t, err, seriesgraph.ErrInvalidSeriesGraph)
		})
	}
}

func TestMaterializeSeriesGraphExactReplayReturnsDetachedStoredGraph(t *testing.T) {
	input := seriesGraphFixture(t, domain.SeriesFormatBO3)
	first, changed, err := seriesgraph.Materialize(nil, input)
	require.NoError(t, err)
	require.True(t, changed)

	replayed, changed, err := seriesgraph.Materialize(&first, input)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, first, replayed)

	replayed.SelectedCategories[0] = domain.CategoryMisc
	replayed.Snapshots[0].Snapshot.Hints[0] = "changed"
	replayed.Series.Slots[0].Attempts[0].State = domain.GameStateActive
	require.Equal(t, domain.CategoryMisc, replayed.SelectedCategories[0])
	require.NotEqual(t, replayed.SelectedCategories, first.SelectedCategories)
	require.NoError(t, first.Validate())
}

func TestMaterializeSeriesGraphCommandReuseAndConflict(t *testing.T) {
	input := seriesGraphFixture(t, domain.SeriesFormatBO1)
	first, _, err := seriesgraph.Materialize(nil, input)
	require.NoError(t, err)

	altered := input
	altered.CategoryRevision.SourceContentRevision++
	_, changed, err := seriesgraph.Materialize(&first, altered)
	require.False(t, changed)
	require.ErrorIs(t, err, seriesgraph.ErrSeriesGraphCommandReuse)

	differentCommand := input
	differentCommand.CommandID = testID(90_000)
	_, changed, err = seriesgraph.Materialize(&first, differentCommand)
	require.False(t, changed)
	require.ErrorIs(t, err, seriesgraph.ErrSeriesGraphConflict)
}

func TestMaterializeSeriesGraphRejectsForeignAndDuplicateInputs(t *testing.T) {
	base := seriesGraphFixture(t, domain.SeriesFormatBO3)
	tests := map[string]func(*seriesgraph.MaterializeInput){
		"foreign category authority": func(input *seriesgraph.MaterializeInput) {
			input.CategoryRevision.TournamentID = testID(80_001)
		},
		"foreign scope": func(input *seriesgraph.MaterializeInput) {
			input.AssignmentPlans[0].Scope.TournamentID = testID(80_002)
		},
		"foreign participant": func(input *seriesgraph.MaterializeInput) {
			input.AssignmentPlans[0].ParticipantIDs[0] = testID(80_003)
		},
		"inconsistent participant reservations": func(input *seriesgraph.MaterializeInput) {
			input.AssignmentPlans[1].ParticipantReservations[0].Reservation.Revision++
		},
		"duplicate selected category": func(input *seriesgraph.MaterializeInput) {
			input.SelectedCategories[1] = input.SelectedCategories[0]
		},
		"duplicate plan identity": func(input *seriesgraph.MaterializeInput) {
			input.AssignmentPlans[1].PlanID = input.AssignmentPlans[0].PlanID
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := cloneInput(base)
			mutate(&input)
			_, changed, err := seriesgraph.Materialize(nil, input)
			require.False(t, changed)
			require.ErrorIs(t, err, seriesgraph.ErrInvalidSeriesGraph)
		})
	}
}

func TestMaterializeSeriesGraphRejectsInvalidProofAndDeliveryHistory(t *testing.T) {
	base := seriesGraphFixture(t, domain.SeriesFormatBO1)
	tests := map[string]func(*seriesgraph.MaterializeInput){
		"invalid proof": func(input *seriesgraph.MaterializeInput) {
			input.AssignmentPlans[0].ProofHash = "not-a-sha256"
		},
		"history conflict": func(input *seriesgraph.MaterializeInput) {
			input.AssignmentPlans[0].History = []capacity.TaskUse{{ParticipantID: input.Series.FirstParticipantID, TaskID: input.AssignmentPlans[0].SelectedEdges[0].Snapshot.TaskID}}
		},
		"delivery identity conflict": func(input *seriesgraph.MaterializeInput) {
			input.AssignmentPlans[0].SelectedEdges[0].Snapshot.SnapshotID = testID(81_001)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := cloneInput(base)
			mutate(&input)
			_, changed, err := seriesgraph.Materialize(nil, input)
			require.False(t, changed)
			require.ErrorIs(t, err, seriesgraph.ErrInvalidSeriesGraph)
		})
	}
}

func TestMaterializeSeriesGraphRejectsDetachedGraphMutation(t *testing.T) {
	input := seriesGraphFixture(t, domain.SeriesFormatBO1)
	graph, _, err := seriesgraph.Materialize(nil, input)
	require.NoError(t, err)
	graph.TaskReservations[0].ContentDigest = sha256.Sum256([]byte("tampered"))
	require.ErrorIs(t, graph.Validate(), seriesgraph.ErrInvalidSeriesGraph)
}

func seriesGraphFixture(t *testing.T, format domain.SeriesFormat) seriesgraph.MaterializeInput {
	t.Helper()
	now := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	tournamentID := testID(1)
	seriesID := testID(2)
	firstParticipantID := testID(3)
	secondParticipantID := testID(4)
	categoryRevisionID := testID(5)
	rosterID := testID(6)
	poolID := testID(7)

	var categories []domain.Category
	var selected []domain.Category
	switch format {
	case domain.SeriesFormatBO1:
		categories = []domain.Category{domain.CategoryCrypto, domain.CategoryReverse, domain.CategoryWeb}
		selected = []domain.Category{domain.CategoryWeb}
	case domain.SeriesFormatBO3:
		categories = []domain.Category{domain.CategoryCrypto, domain.CategoryForensics, domain.CategoryPwn, domain.CategoryReverse, domain.CategoryWeb}
		selected = []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryReverse}
	default:
		t.Fatalf("unsupported fixture format %s", format)
	}
	revision := draftusecase.CategoryRevision{
		ID: categoryRevisionID, TournamentID: tournamentID, SeriesID: seriesID, RosterID: rosterID,
		Revision: 1, Stage: stageForFormat(format), Format: format, Mode: domain.CategoryModeRandom,
		SourceContentRevision: 1,
		CategoryPool:          domain.CategoryPoolRevision{ID: testID(8), Revision: 1, Format: format, Categories: categories},
		CreatedAt:             now,
	}
	require.NoError(t, revision.Validate())
	series := domain.Series{
		ID: seriesID, TournamentID: tournamentID, FirstParticipantID: firstParticipantID,
		SecondParticipantID: secondParticipantID, Format: format, State: domain.SeriesStatePlanned,
	}
	require.NoError(t, series.Validate())

	plans := make([]assignmentusecase.ExactNormalAssignmentPlan, len(selected))
	for index, category := range selected {
		plans[index] = exactPlanFixture(t, now, tournamentID, rosterID, seriesID, categoryRevisionID,
			poolID, firstParticipantID, secondParticipantID, category, index)
	}
	return seriesgraph.MaterializeInput{
		CommandID: testID(9), DeliveredAt: now.Add(5 * time.Minute), Series: series, CategoryRevision: revision,
		SelectedCategories: selected, AssignmentPlans: plans,
	}
}

func exactPlanFixture(
	t *testing.T,
	now time.Time,
	tournamentID, rosterID, seriesID, categoryRevisionID, poolID uuid.UUID,
	firstParticipantID, secondParticipantID uuid.UUID,
	category domain.Category,
	position int,
) assignmentusecase.ExactNormalAssignmentPlan {
	t.Helper()
	scope := assignmentusecase.ExactNormalAssignmentScope{
		TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID,
		SlotID: testID(100 + position), CategoryLockID: categoryRevisionID,
	}
	participants := []uuid.UUID{firstParticipantID, secondParticipantID}
	tasks := make([]domain.Task, 3)
	versions := make([]domain.TaskVersionRef, len(tasks))
	candidates := make([]assignmentusecase.ExactNormalTaskVersion, len(tasks))
	for index := range tasks {
		taskID := testID(1_000 + position*10 + index)
		tasks[index] = domain.Task{
			ID: taskID, Title: fmt.Sprintf("%s task %d", category, index), Description: "Solve this task.",
			Category: category, Difficulty: domain.DifficultyHard, TimeLimit: 90,
			Flag: fmt.Sprintf("FLAG{%d}", taskID), Hints: []string{"first", "second"},
		}
		versions[index] = domain.TaskVersionRef{TaskID: taskID, Version: 1}
		candidates[index] = assignmentusecase.ExactNormalTaskVersion{PoolRevisionID: poolID, Version: 1, Task: tasks[index]}
	}
	createdAt := now
	authority := assignmentusecase.ExactNormalAssignmentAuthority{
		Scope: scope, Category: category,
		Revisions: assignmentusecase.ExactNormalAssignmentSourceRevisions{
			SeriesRevision: 1, PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: testID(2_000 + position), HistoryRevision: 1, RosterRevision: 1,
			ArtifactRevisionID: testID(3_000 + position), ArtifactRevision: 1,
			CategoryRevisionID: categoryRevisionID, CategoryRevision: 1,
		},
		Pool:           domain.TaskPoolRevision{ID: poolID, Revision: 1, Kind: domain.AssignmentTaskKindNormal, Versions: versions},
		ParticipantIDs: participants,
		ParticipantReservations: []assignmentusecase.ExactNormalParticipantReservation{
			{ParticipantID: firstParticipantID, PlayerID: testID(4_000), Reservation: participantReservation(testID(5_000), testID(4_000), tournamentID, createdAt)},
			{ParticipantID: secondParticipantID, PlayerID: testID(4_001), Reservation: participantReservation(testID(5_001), testID(4_001), tournamentID, createdAt)},
		},
		Candidates: candidates, GraphDigest: sha256.Sum256([]byte(fmt.Sprintf("graph-%d", position))),
		ArtifactDigest: sha256.Sum256([]byte(fmt.Sprintf("artifact-%d", position))),
	}
	command := assignmentusecase.ExactNormalAssignmentCommand{
		Scope: scope, PlanID: testID(6_000 + position), PlanRevisionID: testID(7_000 + position),
		BranchID: testID(8_000 + position), DecisionEvidenceID: testID(9_000 + position), CreatedAt: createdAt,
	}
	for index := range command.EdgeIDs {
		command.EdgeIDs[index] = testID(10_000 + position*10 + index)
		command.ReservationIDs[index] = testID(11_000 + position*10 + index)
		command.SnapshotIDs[index] = testID(12_000 + position*10 + index)
	}
	plan, err := assignmentusecase.BuildExactNormalAssignment(command, authority)
	require.NoError(t, err)
	return plan
}

func participantReservation(id, playerID, tournamentID uuid.UUID, at time.Time) domain.ParticipantReservation {
	return domain.ParticipantReservation{PlayerID: playerID, ReservationID: id, TournamentID: tournamentID, Revision: 1, AcquiredAt: at, UpdatedAt: at}
}

func stageForFormat(format domain.SeriesFormat) domain.TournamentStage {
	if format == domain.SeriesFormatBO3 {
		return domain.TournamentStageFinal
	}
	return domain.TournamentStageSwiss
}

func testID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("10000000-0000-0000-0000-%012d", value))
}

func cloneInput(input seriesgraph.MaterializeInput) seriesgraph.MaterializeInput {
	cloned := input
	cloned.Series = input.Series
	cloned.Series.Slots = append([]domain.GameSlot(nil), input.Series.Slots...)
	cloned.CategoryRevision = input.CategoryRevision
	cloned.CategoryRevision.CategoryPool.Categories = append([]domain.Category(nil), input.CategoryRevision.CategoryPool.Categories...)
	cloned.SelectedCategories = append([]domain.Category(nil), input.SelectedCategories...)
	cloned.AssignmentPlans = make([]assignmentusecase.ExactNormalAssignmentPlan, len(input.AssignmentPlans))
	for index, plan := range input.AssignmentPlans {
		cloned.AssignmentPlans[index] = assignmentusecase.CloneExactNormalAssignmentPlan(plan)
	}
	return cloned
}
