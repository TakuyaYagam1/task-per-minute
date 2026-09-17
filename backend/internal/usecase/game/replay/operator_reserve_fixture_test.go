package replay_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/replay/mocks"
	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/replay"
)

type operatorReserveRepositoryState struct {
	mu        sync.Mutex
	authority replayusecase.OperatorReserveAuthority
	current   *replayusecase.OperatorReserve
	conflicts int
	writes    int
}

type operatorReserveRepositoryHarness struct {
	*gamemocks.MockOperatorReserveRepository

	state *operatorReserveRepositoryState
}

func newOperatorReserveRepositoryHarness(
	t *testing.T,
	authority replayusecase.OperatorReserveAuthority,
) *operatorReserveRepositoryHarness {
	t.Helper()
	state := &operatorReserveRepositoryState{authority: authority}
	repository := gamemocks.NewMockOperatorReserveRepository(t)
	repository.EXPECT().
		LoadOperatorReserveAuthority(mock.Anything, mock.Anything).
		RunAndReturn(state.loadAuthority).
		Maybe()
	repository.EXPECT().
		CommitOperatorReserve(mock.Anything, mock.Anything).
		RunAndReturn(state.commitReserve).
		Maybe()
	return &operatorReserveRepositoryHarness{
		MockOperatorReserveRepository: repository,
		state:                         state,
	}
}

func (s *operatorReserveRepositoryState) loadAuthority(
	_ context.Context,
	_ replayusecase.ReplayReplacementScope,
) (replayusecase.OperatorReserveAuthority, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	authority := s.authority
	if s.current != nil {
		current := *s.current
		authority.Current = &current
	}
	return authority, nil
}

func (s *operatorReserveRepositoryState) commitReserve(
	_ context.Context,
	record replayusecase.OperatorReserve,
) (*replayusecase.OperatorReserve, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conflicts > 0 {
		s.conflicts--
		return nil, false, domain.ErrConflict
	}
	if s.current != nil {
		return nil, false, domain.ErrConflict
	}
	committed := record
	s.current = &committed
	s.writes++
	return &committed, true, nil
}

func (h *operatorReserveRepositoryHarness) writeCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.writes
}

func operatorReserveFixture(
	t *testing.T,
) (replayusecase.OperatorReserveAuthority, replayusecase.OperatorReserveCommand) {
	t.Helper()

	exhaustionAuthority, exhaustionCommand := replayExhaustionFixture(t)
	exhaustionRepository := newReplayExhaustionRepositoryHarness(t, exhaustionAuthority)
	exhaustion, changed, err := replayusecase.NewReplayReserveExhaustionUseCase(exhaustionRepository).
		Pause(t.Context(), exhaustionCommand)
	require.NoError(t, err)
	require.True(t, changed)

	reserve, reserveCommand := operatorReserveAssignmentFixture(t)
	reserve.Scope = assignmentusecase.ReserveAssignmentScope{
		TournamentID: exhaustion.Scope.TournamentID,
		AssignmentID: exhaustion.Scope.AssignmentID,
		AttemptID:    exhaustion.AssignmentAttemptID,
		SlotID:       exhaustion.Scope.SlotID,
	}
	reserve.CurrentSnapshotID = exhaustion.ActiveSnapshotID
	reserve.RequiredCategory = exhaustion.Category
	reserve.ParticipantIDs = []uuid.UUID{
		exhaustion.Series.Series.FirstParticipantID,
		exhaustion.Series.Series.SecondParticipantID,
	}
	for index := range reserve.ParticipantReservations {
		reserve.ParticipantReservations[index].ParticipantID = reserve.ParticipantIDs[index]
		reserve.ParticipantReservations[index].Reservation.TournamentID = exhaustion.Scope.TournamentID
	}
	reserveCommand.Scope = reserve.Scope
	reserveCommand.ExpectedSnapshotID = reserve.CurrentSnapshotID
	reserveCommand.Mode = assignmentusecase.ReserveAssignmentModeOperator
	reserveCommand.OperatorID = operatorReserveID(20)
	reserveCommand.Reason = "approve one locked same-category replay reserve"

	authority := replayusecase.OperatorReserveAuthority{
		Scope: exhaustion.Scope, Revision: 15, Exhaustion: *exhaustion, Reserve: reserve,
	}
	command := replayusecase.OperatorReserveCommand{
		Scope: authority.Scope, CommandID: operatorReserveID(21),
		ExpectedExhaustionCommandID: exhaustion.CommandID,
		ExpectedRevisions:           reserve.Revisions,
		ProposedTaskID:              reserve.CandidateSnapshot.TaskID,
		ProposedVersion:             reserve.CandidateSnapshot.Version,
		ProposedSnapshotID:          reserve.CandidateSnapshot.SnapshotID,
		Reserve:                     reserveCommand,
	}
	return authority, command
}

func operatorReserveAssignmentFixture(
	t *testing.T,
) (assignmentusecase.ReserveAssignmentAuthority, assignmentusecase.ReserveAssignmentCommand) {
	t.Helper()

	eligibility := operatorReserveAssignmentEligibility()
	snapshot, err := taskexec.BuildSnapshot(taskexec.SnapshotInput{
		SnapshotID: operatorReserveAssignmentID(60),
		Version:    eligibility.Candidate.Version,
		Kind:       eligibility.Pool.Kind,
		Task:       eligibility.Candidate.Task,
	})
	require.NoError(t, err)
	digest, err := taskexec.SnapshotDigest(snapshot)
	require.NoError(t, err)
	now := time.Date(2026, 8, 30, 19, 0, 0, 0, time.UTC)
	scope := assignmentusecase.ReserveAssignmentScope{
		TournamentID: operatorReserveAssignmentID(61),
		AssignmentID: operatorReserveAssignmentID(62),
		AttemptID:    operatorReserveAssignmentID(63),
		SlotID:       operatorReserveAssignmentID(64),
	}
	reservations := []assignmentusecase.ExactNormalParticipantReservation{
		{
			ParticipantID: eligibility.ParticipantIDs[0],
			PlayerID:      operatorReserveAssignmentID(65),
			Reservation: operatorReserveAssignmentReservation(
				3401,
				operatorReserveAssignmentID(65),
				scope.TournamentID,
				now,
			),
		},
		{
			ParticipantID: eligibility.ParticipantIDs[1],
			PlayerID:      operatorReserveAssignmentID(66),
			Reservation: operatorReserveAssignmentReservation(
				3402,
				operatorReserveAssignmentID(66),
				scope.TournamentID,
				now,
			),
		},
	}
	authority := assignmentusecase.ReserveAssignmentAuthority{
		Scope: scope,
		Revisions: assignmentusecase.ReserveAssignmentSourceRevisions{
			AssignmentRevision:    4,
			PoolRevisionID:        eligibility.Pool.ID,
			PoolRevision:          eligibility.Pool.Revision,
			HistoryRevisionID:     operatorReserveAssignmentID(67),
			HistoryRevision:       5,
			ArtifactRevisionID:    operatorReserveAssignmentID(68),
			ArtifactRevision:      6,
			ReservationRevisionID: operatorReserveAssignmentID(69),
			ReservationRevision:   7,
			CategoryRevisionID:    operatorReserveAssignmentID(71),
			CategoryRevision:      8,
		},
		CurrentSnapshotID:       operatorReserveAssignmentID(59),
		RequiredCategory:        eligibility.Category,
		ParticipantIDs:          eligibility.ParticipantIDs,
		ParticipantReservations: reservations,
		Pool:                    eligibility.Pool,
		CandidateHealth:         eligibility.Candidate.Health,
		CandidateSnapshot:       snapshot,
		CandidateContentDigest:  digest,
	}
	command := assignmentusecase.ReserveAssignmentCommand{
		Scope:              scope,
		ExpectedSnapshotID: authority.CurrentSnapshotID,
		EvidenceID:         operatorReserveAssignmentID(72),
		Mode:               assignmentusecase.ReserveAssignmentModeAutomatic,
		PromotedAt:         now,
	}
	return authority, command
}

func setOperatorReserveCandidateCategory(
	t *testing.T,
	authority *assignmentusecase.ReserveAssignmentAuthority,
	category domain.Category,
) {
	t.Helper()
	task := operatorReserveAssignmentTask(4, category)
	snapshot, err := taskexec.BuildSnapshot(taskexec.SnapshotInput{
		SnapshotID: authority.CandidateSnapshot.SnapshotID,
		Version:    authority.CandidateSnapshot.Version,
		Kind:       authority.CandidateSnapshot.Kind,
		Task:       task,
	})
	require.NoError(t, err)
	digest, err := taskexec.SnapshotDigest(snapshot)
	require.NoError(t, err)
	authority.CandidateSnapshot = snapshot
	authority.CandidateContentDigest = digest
}

func operatorReserveAssignmentEligibility() assignmentusecase.TaskEligibilityInput {
	poolID := operatorReserveAssignmentID(1)
	participantIDs := []uuid.UUID{operatorReserveAssignmentID(2), operatorReserveAssignmentID(3)}
	task := operatorReserveAssignmentTask(4, domain.CategoryWeb)
	return assignmentusecase.TaskEligibilityInput{
		Pool: domain.TaskPoolRevision{
			ID: poolID, Revision: 7, Kind: domain.AssignmentTaskKindNormal,
			Versions: []domain.TaskVersionRef{{TaskID: task.ID, Version: 3}},
		},
		Category:       domain.CategoryWeb,
		ParticipantIDs: participantIDs,
		Candidate: assignmentusecase.TaskEligibilityCandidate{
			Version: 3,
			Task:    task,
			Health: domain.TaskVersionHealth{
				TaskID: task.ID, Version: 3, PoolRevisionID: poolID,
				PoolKind: domain.AssignmentTaskKindNormal, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
		},
	}
}

func operatorReserveAssignmentTask(id int, category domain.Category) domain.Task {
	return domain.Task{
		ID: operatorReserveAssignmentID(id), Title: fmt.Sprintf("Task %d", id),
		Description: "Solve the isolated challenge.", Category: category,
		Difficulty: domain.DifficultyHard, TimeLimit: 90,
		Flag: fmt.Sprintf("FLAG{%d}", id), Hints: []string{"first hint", "second hint"},
	}
}

func operatorReserveAssignmentReservation(
	id int,
	playerID uuid.UUID,
	tournamentID uuid.UUID,
	at time.Time,
) domain.ParticipantReservation {
	return domain.ParticipantReservation{
		PlayerID: playerID,
		ReservationID: uuid.MustParse(
			fmt.Sprintf("00000000-0000-0000-0000-%012d", id),
		),
		TournamentID: tournamentID,
		Revision:     1,
		AcquiredAt:   at,
		UpdatedAt:    at,
	}
}

func operatorReserveID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("41000000-0000-0000-0000-%012d", number))
}

func operatorReserveAssignmentID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0034-%012d", value))
}
