package arena_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestArenaSubmissionValidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 20, 0, 0, 0, time.UTC)

	t.Run("records correct and invalid flags without retaining the secret", func(t *testing.T) {
		t.Parallel()

		authority, command := task037SubmissionFixture(t, now)
		repository := &arenaSubmissionRepositoryFake{authority: authority, committedAt: now}
		usecase := arena.NewArenaSubmissionUseCase(repository)

		correct, changed, err := usecase.Submit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.True(t, correct.Correct)
		require.Equal(t, int64(1), correct.Sequence)
		require.Equal(t, now, correct.CommittedAt)

		invalidCommand := command
		invalidCommand.CommandID = task037ID(31)
		invalidCommand.SubmittedFlag = "wrong-answer"
		invalid, changed, err := usecase.Submit(t.Context(), invalidCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, invalid.Correct)
		require.Equal(t, int64(2), invalid.Sequence)
		require.NotContains(t, fmt.Sprintf("%#v", repository.lastCommit()), command.SubmittedFlag)
		require.NotContains(t, fmt.Sprintf("%#v", repository.records()), command.SubmittedFlag)
	})

	t.Run("orders equal server timestamps by monotonic Game sequence", func(t *testing.T) {
		t.Parallel()

		authority, command := task037SubmissionFixture(t, now)
		repository := &arenaSubmissionRepositoryFake{authority: authority, committedAt: now}
		usecase := arena.NewArenaSubmissionUseCase(repository)
		for index := 0; index < 3; index++ {
			command.CommandID = task037ID(40 + index)
			_, _, err := usecase.Submit(t.Context(), command)
			require.NoError(t, err)
		}
		records := repository.records()
		ordered, err := arena.OrderArenaSubmissions([]arena.ArenaSubmissionRecord{
			records[2], records[0], records[1],
		})
		require.NoError(t, err)
		require.Equal(t, []int64{1, 2, 3}, []int64{
			ordered[0].Sequence, ordered[1].Sequence, ordered[2].Sequence,
		})
	})

	t.Run("rejects foreign disconnected inactive paused and malformed submissions", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			mutateAuth func(*arena.ArenaSubmissionAuthority)
			mutateCmd  func(*arena.ArenaSubmissionCommand)
			expected   error
		}{
			{
				name: "foreign actor",
				mutateCmd: func(command *arena.ArenaSubmissionCommand) {
					command.ActorParticipantID = task037ID(90)
				},
				expected: domain.ErrArenaAssignmentParticipant,
			},
			{
				name: "disconnected participant",
				mutateAuth: func(authority *arena.ArenaSubmissionAuthority) {
					authority.ConnectedParticipantIDs = authority.ConnectedParticipantIDs[1:]
				},
				expected: arena.ErrArenaSubmissionDisconnected,
			},
			{
				name: "inactive Game",
				mutateAuth: func(authority *arena.ArenaSubmissionAuthority) {
					authority.StartedGame.Series.Series.Slots[0].Attempts[0].State =
						domain.ArenaGameStateReady
				},
				expected: arena.ErrArenaSubmissionNotOpen,
			},
			{
				name: "paused execution",
				mutateAuth: func(authority *arena.ArenaSubmissionAuthority) {
					authority.Paused = true
				},
				expected: arena.ErrArenaSubmissionNotOpen,
			},
			{
				name: "malformed flag",
				mutateCmd: func(command *arena.ArenaSubmissionCommand) {
					command.SubmittedFlag = ""
				},
				expected: arena.ErrInvalidArenaSubmission,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				authority, command := task037SubmissionFixture(t, now)
				if test.mutateAuth != nil {
					test.mutateAuth(&authority)
				}
				if test.mutateCmd != nil {
					test.mutateCmd(&command)
				}
				repository := &arenaSubmissionRepositoryFake{
					authority: authority, committedAt: now,
				}
				record, changed, err := arena.NewArenaSubmissionUseCase(repository).
					Submit(t.Context(), command)
				require.Zero(t, record)
				require.False(t, changed)
				require.ErrorIs(t, err, test.expected)
				require.Equal(t, 0, repository.commitCount())
			})
		}
	})

	t.Run("returns sanitized repository errors", func(t *testing.T) {
		t.Parallel()

		authority, command := task037SubmissionFixture(t, now)
		repository := &arenaSubmissionRepositoryFake{
			authority: authority, committedAt: now, commitErr: errors.New("storage rejected"),
		}
		_, _, err := arena.NewArenaSubmissionUseCase(repository).Submit(t.Context(), command)
		require.Error(t, err)
		require.NotContains(t, err.Error(), command.SubmittedFlag)
	})
}

type arenaSubmissionRepositoryFake struct {
	authority   arena.ArenaSubmissionAuthority
	committedAt time.Time
	commitErr   error
	commits     int
	commit      arena.ArenaSubmissionCommit
}

func (r *arenaSubmissionRepositoryFake) LoadArenaSubmissionAuthority(
	_ context.Context,
	_ arena.ArenaSubmissionScope,
) (arena.ArenaSubmissionAuthority, error) {
	return cloneArenaSubmissionAuthority(r.authority), nil
}

func (r *arenaSubmissionRepositoryFake) CommitArenaSubmission(
	_ context.Context,
	commit arena.ArenaSubmissionCommit,
) (*arena.ArenaSubmissionRecord, bool, error) {
	r.commits++
	r.commit = commit
	if r.commitErr != nil {
		return nil, false, r.commitErr
	}
	if commit.ExpectedAuthorityRevision != r.authority.Revision || r.authority.Paused ||
		commit.ExpectedGameState != domain.ArenaGameStateActive ||
		!commit.ExpectedDeadline.Equal(r.authority.StartedGame.Deadline) ||
		!r.committedAt.Before(commit.ExpectedDeadline) {
		return nil, false, domain.ErrConflict
	}
	record := arena.ArenaSubmissionRecord{
		Scope: commit.Scope, CommandID: commit.CommandID,
		ParticipantID: commit.ParticipantID, Sequence: int64(len(r.authority.Submissions) + 1),
		CommittedAt: r.committedAt, Correct: commit.Correct,
		SnapshotID: commit.SnapshotID, TaskID: commit.TaskID, ContentDigest: commit.ContentDigest,
	}
	r.authority.Revision++
	r.authority.Submissions = append(r.authority.Submissions, record)
	return &record, true, nil
}

func (r *arenaSubmissionRepositoryFake) lastCommit() arena.ArenaSubmissionCommit {
	return r.commit
}

func (r *arenaSubmissionRepositoryFake) records() []arena.ArenaSubmissionRecord {
	return append([]arena.ArenaSubmissionRecord(nil), r.authority.Submissions...)
}

func (r *arenaSubmissionRepositoryFake) commitCount() int {
	return r.commits
}

func task037SubmissionFixture(
	t *testing.T,
	now time.Time,
) (arena.ArenaSubmissionAuthority, arena.ArenaSubmissionCommand) {
	t.Helper()

	snapshot, err := arena.BuildImmutableTaskSnapshot(immutableTaskSnapshotFixture())
	require.NoError(t, err)
	instances := snapshot.Instances()
	tournamentID := task037ID(1)
	seriesID := task037ID(2)
	slotID := task037ID(3)
	gameID := task037ID(4)
	gameScope := arena.GameScope{
		TournamentID: tournamentID, SeriesID: seriesID, SlotID: slotID, GameID: gameID,
	}
	series := arena.SeriesExecution{Series: domain.ArenaSeries{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID:  instances[0].ParticipantID,
		SecondParticipantID: instances[1].ParticipantID,
		Format:              domain.ArenaSeriesFormatBO3, State: domain.ArenaSeriesStateActive,
		Slots: []domain.ArenaGameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.ArenaSeriesScore{},
			Attempts: []domain.ArenaGame{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.ArenaGameStateActive,
			}},
		}},
	}}
	scope := arena.ArenaSubmissionScope{
		WaveID: task037ID(5), Game: gameScope, AssignmentID: task037ID(6),
	}
	authority := arena.ArenaSubmissionAuthority{
		Scope: scope, Revision: 1,
		StartedGame: arena.StartedWaveGame{
			Scope: gameScope, ParticipantIDs: [2]uuid.UUID{
				instances[0].ParticipantID, instances[1].ParticipantID,
			},
			Series: series, AssignmentID: scope.AssignmentID, AssignmentRevision: 1,
			PlanRevisionID: task037ID(7), SnapshotID: snapshot.Snapshot().SnapshotID,
			ContentDigest: snapshot.ContentDigest(), DeadlineSeconds: 90,
			StartedAt: now.Add(-time.Second), Deadline: now.Add(89 * time.Second), DeliveryEnabled: true,
		},
		Snapshot: snapshot,
		ConnectedParticipantIDs: []uuid.UUID{
			instances[0].ParticipantID, instances[1].ParticipantID,
		},
	}
	command := arena.ArenaSubmissionCommand{
		Scope: scope, CommandID: task037ID(30),
		ActorParticipantID: instances[0].ParticipantID,
		ParticipantID:      instances[0].ParticipantID,
		SubmittedFlag:      snapshot.Snapshot().Flag,
	}
	return authority, command
}

func cloneArenaSubmissionAuthority(
	authority arena.ArenaSubmissionAuthority,
) arena.ArenaSubmissionAuthority {
	cloned := authority
	cloned.ConnectedParticipantIDs = append([]uuid.UUID(nil), authority.ConnectedParticipantIDs...)
	cloned.Submissions = append([]arena.ArenaSubmissionRecord(nil), authority.Submissions...)
	return cloned
}

func task037ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0037-%012d", value))
}

var _ arena.ArenaSubmissionRepository = (*arenaSubmissionRepositoryFake)(nil)
