package correction_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	correctionmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction/stage/mocks"
)

func task057CutoffEvents(
	tournamentID uuid.UUID,
	target domain.DerivedRevision,
	kind correctionusecase.CutoffKind,
	id int,
) []correctionusecase.CutoffEvent {
	return []correctionusecase.CutoffEvent{{
		ID: task057ID(id), Kind: kind, TournamentID: tournamentID,
		SourceRevisionID: target.ID(), OccurredAt: target.CreatedAt().Add(time.Second),
	}}
}

func task057StageRevision(
	t *testing.T,
	revisions []domain.DerivedRevision,
) domain.DerivedRevisionID {
	t.Helper()
	for _, revision := range revisions {
		if revision.Artifact().Kind == domain.ArtifactKindStandings {
			return revision.ID()
		}
	}
	t.Fatal("correction cutoff has no standings revision")
	return domain.DerivedRevisionID{}
}

func task057StageProjection(
	t *testing.T,
	projections []domain.ProjectionRevision,
) domain.DerivedRevisionID {
	t.Helper()
	revisions := make([]domain.DerivedRevision, len(projections))
	for index, projection := range projections {
		revisions[index] = projection.Revision()
	}
	return task057StageRevision(t, revisions)
}

func task057PausedGoldenGroup(
	t *testing.T,
	tournamentID, groupID uuid.UUID,
	revisionID, sourceRevisionID domain.DerivedRevisionID,
	participants []uuid.UUID,
	pausedAt time.Time,
) (domain.GoldenGroupState, correctionusecase.StagePauseExpectation) {
	t.Helper()
	attemptID := task057ID(int(groupID[15]) + 1000)
	retainedAt := pausedAt
	attempt := domain.GoldenAttempt{
		ID: attemptID, GroupID: groupID, GroupRevisionID: revisionID, AttemptNo: 1,
		State: domain.GoldenAttemptStatePlanned, ParticipantIDs: append([]uuid.UUID(nil), participants...),
		RetainedAt: &retainedAt,
	}
	group := task057GoldenGroup(
		t, tournamentID, groupID, revisionID, sourceRevisionID, 1, participants, &attempt,
	)
	return group, correctionusecase.StagePauseExpectation{
		TournamentID: tournamentID, GroupID: groupID, GroupRevisionID: revisionID,
		SessionID: task057ID(int(groupID[15]) + 1100), RevisionID: task057ID(int(groupID[15]) + 1200),
		Revision: 1, State: correctionusecase.StagePauseStatePaused,
		PayloadDigest: sha256.Sum256([]byte("retained Golden technical pause")),
	}
}

func task057GoldenGroup(
	t *testing.T,
	tournamentID, groupID uuid.UUID,
	revisionID, sourceRevisionID domain.DerivedRevisionID,
	positionFrom int,
	participants []uuid.UUID,
	attempt *domain.GoldenAttempt,
) domain.GoldenGroupState {
	t.Helper()
	members := make([]domain.GoldenMember, len(participants))
	for index, participantID := range participants {
		members[index] = domain.GoldenMember{ParticipantID: participantID}
	}
	state := domain.GoldenGroupState{
		ID: groupID, TournamentID: tournamentID, RevisionID: revisionID,
		SourceProjectionRevisionID: sourceRevisionID,
		PositionFrom:               positionFrom, PositionTo: positionFrom + len(participants) - 1,
		ParticipationEstablished: attempt != nil, Members: members,
	}
	if attempt != nil {
		state.Attempts = []domain.GoldenAttempt{*attempt}
	}
	_, err := domain.NewGoldenGroup(state)
	require.NoError(t, err)
	return state
}

func task057ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("57000000-0000-0000-0000-%012x", value))
}

func task057RevisionID(value int) domain.DerivedRevisionID {
	return domain.DerivedRevisionID(task057ID(value))
}

func newTask057CorrectionStageUseCase(
	t *testing.T,
	repository *task057CorrectionStageRepository,
	now time.Time,
) *correctionusecase.StageUseCase {
	t.Helper()
	transactions := correctionmocks.NewMockTransactionManager(t)
	transactions.EXPECT().
		Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		}).
		Once()
	clock := correctionmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return correctionusecase.NewStageUseCase(transactions, repository.mock, clock)
}

type task057CorrectionStageRepository struct {
	mu         sync.Mutex
	mock       *correctionmocks.MockStageRepository
	snapshot   correctionusecase.StageSnapshot
	lastCommit correctionusecase.StageCommit
	writes     int
}

func newTask057CorrectionStageRepository(
	t *testing.T,
	snapshot correctionusecase.StageSnapshot,
) *task057CorrectionStageRepository {
	t.Helper()
	harness := &task057CorrectionStageRepository{snapshot: cloneTask057CorrectionStageSnapshot(snapshot)}
	harness.mock = correctionmocks.NewMockStageRepository(t)
	harness.mock.EXPECT().
		LoadCorrectionStage(mock.Anything, snapshot.TournamentID).
		RunAndReturn(func(context.Context, uuid.UUID) (correctionusecase.StageSnapshot, error) {
			return harness.loaded(), nil
		}).
		Once()
	harness.mock.EXPECT().
		CommitCorrectionStage(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, commit correctionusecase.StageCommit) (bool, error) {
			return harness.commit(commit)
		}).
		Maybe()
	return harness
}

func (h *task057CorrectionStageRepository) commit(commit correctionusecase.StageCommit) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastCommit = cloneTask057CorrectionStageCommit(commit)
	h.snapshot.Layout = cloneTask057CorrectionStageLayout(commit.Result.Corrected)
	h.writes++
	return true, nil
}

func (h *task057CorrectionStageRepository) loaded() correctionusecase.StageSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return cloneTask057CorrectionStageSnapshot(h.snapshot)
}

func (h *task057CorrectionStageRepository) committed() correctionusecase.StageCommit {
	h.mu.Lock()
	defer h.mu.Unlock()
	return cloneTask057CorrectionStageCommit(h.lastCommit)
}

func (h *task057CorrectionStageRepository) writeCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.writes
}

func cloneTask057CorrectionStageSnapshot(
	snapshot correctionusecase.StageSnapshot,
) correctionusecase.StageSnapshot {
	clone := snapshot
	clone.CutoffEvents = append([]correctionusecase.CutoffEvent(nil), snapshot.CutoffEvents...)
	clone.Layout = cloneTask057CorrectionStageLayout(snapshot.Layout)
	return clone
}

func cloneTask057CorrectionStageCommit(commit correctionusecase.StageCommit) correctionusecase.StageCommit {
	clone := commit
	clone.Correction = append([]byte(nil), commit.Correction...)
	clone.Result = cloneTask057CorrectionStageResult(commit.Result)
	return clone
}

func cloneTask057CorrectionStageResult(stageResult correctionusecase.StageResult) correctionusecase.StageResult {
	clone := stageResult
	clone.Corrected = cloneTask057CorrectionStageLayout(stageResult.Corrected)
	clone.GroupSupersessions = make([]correctionusecase.StageGroupSupersession, len(stageResult.GroupSupersessions))
	for index, supersession := range stageResult.GroupSupersessions {
		clone.GroupSupersessions[index] = supersession
		clone.GroupSupersessions[index].ReplacementGroupID = task057UUIDPointer(supersession.ReplacementGroupID)
		clone.GroupSupersessions[index].Previous = cloneTask057GoldenGroup(supersession.Previous)
	}
	clone.CancelledAttempts = make([]domain.GoldenAttempt, len(stageResult.CancelledAttempts))
	for index, attempt := range stageResult.CancelledAttempts {
		clone.CancelledAttempts[index] = cloneTask057GoldenAttempt(attempt)
	}
	return clone
}

func cloneTask057CorrectionStageLayout(layout correctionusecase.StageLayout) correctionusecase.StageLayout {
	clone := layout
	clone.GoldenGroups = make([]domain.GoldenGroupState, len(layout.GoldenGroups))
	for index, group := range layout.GoldenGroups {
		clone.GoldenGroups[index] = cloneTask057GoldenGroup(group)
	}
	clone.Paused = append([]correctionusecase.StagePauseExpectation(nil), layout.Paused...)
	return clone
}

func cloneTask057GoldenGroup(group domain.GoldenGroupState) domain.GoldenGroupState {
	clone := group
	clone.Members = append([]domain.GoldenMember(nil), group.Members...)
	clone.Attempts = make([]domain.GoldenAttempt, len(group.Attempts))
	for index, attempt := range group.Attempts {
		clone.Attempts[index] = cloneTask057GoldenAttempt(attempt)
	}
	return clone
}

func cloneTask057GoldenAttempt(attempt domain.GoldenAttempt) domain.GoldenAttempt {
	clone := attempt
	clone.PreviousAttemptID = task057UUIDPointer(attempt.PreviousAttemptID)
	clone.ParticipantIDs = append([]uuid.UUID(nil), attempt.ParticipantIDs...)
	clone.RetainedAt = task057TimePointer(attempt.RetainedAt)
	clone.StartedAt = task057TimePointer(attempt.StartedAt)
	clone.FinishedAt = task057TimePointer(attempt.FinishedAt)
	return clone
}

func task057UUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func task057TimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
