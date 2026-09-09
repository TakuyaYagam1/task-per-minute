package playoff_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func newTop4GoldenAllocationState(
	t *testing.T,
	finalSwiss playoff.FinalSwissProjection,
	participate bool,
) goldenusecase.GoldenState {
	t.Helper()

	groups := finalSwiss.GoldenGroups()
	require.Len(t, groups, 1)
	group := groups[0]
	members := group.Revision.Members()
	active := make([]uuid.UUID, len(members))
	groupMembers := make([]domain.GoldenMember, len(members))
	for index, member := range members {
		active[index] = member.ParticipantID
		groupMembers[index] = domain.GoldenMember{ParticipantID: member.ParticipantID}
	}
	poolID := playoffID(7800)
	versions := make([]domain.TaskVersionRef, domain.AssignmentReserveCount+1)
	candidates := make([]goldenusecase.TaskVersion, len(versions))
	for index := range versions {
		task := newPlayoffGoldenTask(8000 + index)
		versions[index] = domain.TaskVersionRef{TaskID: task.ID, Version: 2}
		candidates[index] = goldenusecase.TaskVersion{
			PoolRevisionID: poolID, Version: 2, Task: task,
			Health: domain.TaskVersionHealth{
				TaskID: task.ID, Version: 2, PoolRevisionID: poolID,
				PoolKind: domain.AssignmentTaskKindGolden, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
			ArtifactDigest: goldenusecase.TaskArtifactDigest(task, 2),
		}
	}
	reservedAt := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)
	reservations := make([]goldenusecase.ParticipantReservation, len(active))
	for index, participantID := range active {
		playerID := playoffID(7820 + index)
		reservations[index] = goldenusecase.ParticipantReservation{
			ParticipantID: participantID, PlayerID: playerID,
			Reservation: domain.ParticipantReservation{
				PlayerID: playerID, ReservationID: playoffID(7840 + index),
				TournamentID: group.State.TournamentID, Revision: 1,
				AcquiredAt: reservedAt, UpdatedAt: reservedAt,
			},
		}
	}
	authority, err := goldenusecase.BuildAuthority(goldenusecase.Authority{
		Scope: goldenusecase.Scope{
			TournamentID: group.State.TournamentID, PlanSetID: playoffID(7860),
		},
		Revisions: goldenusecase.Revisions{
			SourceProjectionRevisionID: finalSwiss.GoldenSource().RevisionID,
			GroupSetRevisionID:         playoffID(7861), GroupSetRevision: 1,
			PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: playoffID(7862), HistoryRevision: 1,
			TaskHealthRevisionID: playoffID(7863), TaskHealthRevision: 1,
			ArtifactRevisionID: playoffID(7864), ArtifactRevision: 1,
			ReservationRevisionID: playoffID(7865), ReservationRevision: 1,
			MembershipRevisionID: playoffID(7866), MembershipRevision: 1,
		},
		Source: finalSwiss.GoldenSource(),
		Groups: []goldenusecase.GroupAuthority{{
			Revision: group.Revision, ActiveParticipantIDs: active,
		}},
		Pool: domain.TaskPoolRevision{
			ID: poolID, Revision: 1, Kind: domain.AssignmentTaskKindGolden, Versions: versions,
		},
		Candidates: candidates, ParticipantReservations: reservations,
	})
	require.NoError(t, err)
	groupCommand := goldenusecase.GroupCommand{
		GroupID: group.State.ID, GroupRevisionID: group.State.RevisionID,
	}
	for index := range groupCommand.EdgeIDs {
		groupCommand.EdgeIDs[index] = playoffID(7880 + index*3)
		groupCommand.ReservationIDs[index] = playoffID(7881 + index*3)
		groupCommand.SnapshotIDs[index] = playoffID(7882 + index*3)
	}
	plan, err := goldenusecase.BuildExactPlan(goldenusecase.Command{
		Scope: authority.Scope, PlanID: playoffID(7900), PlanRevisionID: playoffID(7901),
		Expected: authority.Expectation(), GroupCommands: []goldenusecase.GroupCommand{groupCommand},
		CreatedAt: reservedAt.Add(time.Hour),
	}, authority)
	require.NoError(t, err)
	attemptID := playoffID(7910)
	openedAt := reservedAt.Add(time.Hour + time.Second)
	state, err := goldenusecase.BuildGoldenState(goldenusecase.GoldenState{
		Scope: goldenusecase.GoldenStateScope{
			TournamentID: group.State.TournamentID, GroupID: group.State.ID,
			GroupRevisionID: group.State.RevisionID,
		},
		Topology: group.Revision, ExactPlan: plan,
		Group: domain.GoldenGroupState{
			ID: group.State.ID, TournamentID: group.State.TournamentID,
			RevisionID:                 group.State.RevisionID,
			SourceProjectionRevisionID: group.State.SourceProjectionRevisionID,
			PositionFrom:               group.State.PositionFrom, PositionTo: group.State.PositionTo,
			Members: groupMembers,
			Attempts: []domain.GoldenAttempt{{
				ID: attemptID, GroupID: group.State.ID, GroupRevisionID: group.State.RevisionID,
				AttemptNo: 1, State: domain.GoldenAttemptStateWaitingReady,
				ParticipantIDs: active,
			}},
		},
		Membership: goldenusecase.GoldenMembershipRevision{RevisionID: playoffID(7911), Revision: 1},
		RevisionID: playoffID(7912), Revision: 1,
		Windows: []goldenusecase.GoldenReadyWindow{{
			ID: playoffID(7913), RevisionID: playoffID(7914), Revision: 1,
			AttemptID: attemptID, AttemptNo: 1, OpenedAt: openedAt,
			Deadline: openedAt.Add(30 * time.Second), State: goldenusecase.GoldenReadyWindowOpen,
			ReadinessRevisionID: playoffID(7915), ReadinessRevision: 1,
			PresenceRevisionID: playoffID(7916), PresenceRevision: 1,
			PresentParticipantIDs: active,
		}},
	})
	require.NoError(t, err)
	repository := newTop4GoldenStateRepository(t, state)
	ready := []uuid.UUID(nil)
	if participate {
		ready = []uuid.UUID{active[0]}
	}
	resolved := resolveTop4GoldenReadyWindow(t, repository, state, ready, openedAt, 7920)
	allocated, changed, err := goldenusecase.NewGoldenFallbackUseCase(
		repository, newTop4GoldenClock(t, resolved.NoShows[len(resolved.NoShows)-1].ResolvedAt),
	).Allocate(t.Context(), goldenusecase.GoldenFallbackCommand{
		Scope: resolved.Scope, CommandID: playoffID(7950), AllocationID: playoffID(7951),
		ExpectedState: resolved.Expectation(), NextStateRevisionID: playoffID(7952),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, allocated.Validate())
	return *allocated
}

func newTop4GoldenStateRepository(
	t *testing.T,
	initial goldenusecase.GoldenState,
) *goldenmocks.MockStateRepository {
	t.Helper()

	current := initial.Snapshot()
	repository := goldenmocks.NewMockStateRepository(t)
	repository.EXPECT().LoadGoldenState(mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, goldenusecase.GoldenStateScope) (goldenusecase.GoldenState, error) {
			return current.Snapshot(), nil
		},
	).Maybe()
	repository.EXPECT().CommitGoldenState(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, commit goldenusecase.GoldenStateCommit) (*goldenusecase.GoldenState, bool, error) {
			if !commit.Expected.Equal(current.Expectation()) {
				return nil, false, domain.ErrConflict
			}
			current = commit.Next.Snapshot()
			result := current.Snapshot()
			return &result, true, nil
		},
	).Maybe()
	return repository
}

func resolveTop4GoldenReadyWindow(
	t *testing.T,
	repository *goldenmocks.MockStateRepository,
	state goldenusecase.GoldenState,
	readyIDs []uuid.UUID,
	openedAt time.Time,
	base int,
) goldenusecase.GoldenState {
	t.Helper()

	current := state
	for index, participantID := range readyIDs {
		window := current.Windows[0]
		ready, changed, err := goldenusecase.NewGoldenParticipationUseCase(
			repository, newTop4GoldenClock(t, openedAt.Add(time.Duration(index+1)*time.Second)),
		).AcceptReady(t.Context(), goldenusecase.GoldenReadyCommand{
			Scope: current.Scope, CommandID: playoffID(base + index*10),
			ActorParticipantID: participantID, ParticipantID: participantID,
			AttemptID: window.AttemptID, WindowID: window.ID,
			ExpectedState: current.Expectation(), ExpectedWindow: window.Expectation(),
			NextStateRevisionID:     playoffID(base + index*10 + 1),
			NextWindowRevisionID:    playoffID(base + index*10 + 2),
			NextReadinessRevisionID: playoffID(base + index*10 + 3),
		})
		require.NoError(t, err)
		require.True(t, changed)
		current = *ready
	}
	window := current.Windows[0]
	resolved, changed, err := goldenusecase.NewGoldenNoShowUseCase(
		repository, newTop4GoldenClock(t, window.Deadline.Add(time.Nanosecond)),
	).Resolve(t.Context(), goldenusecase.GoldenNoShowCommand{
		Scope: current.Scope, CommandID: playoffID(base + len(readyIDs)*10),
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: current.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID:      playoffID(base + len(readyIDs)*10 + 1),
		NextWindowRevisionID:     playoffID(base + len(readyIDs)*10 + 2),
		NextMembershipRevisionID: playoffID(base + len(readyIDs)*10 + 3),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, resolved.NoShows[0].ResolvedAt.After(openedAt))
	return *resolved
}

func newTop4GoldenClock(t *testing.T, now time.Time) *goldenmocks.MockStateClock {
	t.Helper()
	clock := goldenmocks.NewMockStateClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func newPlayoffGoldenTask(number int) domain.Task {
	taskURL := "https://tasks.example.test/golden"
	return domain.Task{
		ID: playoffID(number), Title: "Golden challenge",
		Description: "Solve the Golden challenge.", Category: domain.CategoryWeb,
		Difficulty: domain.DifficultyMedium, TimeLimit: 300, Flag: "FLAG{golden}",
		Hints: []string{"Inspect the request."}, TaskURL: &taskURL,
	}
}
