package submission_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/submission"
)

func TestSubmissionValidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 20, 0, 0, 0, time.UTC)

	t.Run("records correct and invalid flags without retaining the secret", func(t *testing.T) {
		t.Parallel()

		authority, command := submissionFixture(t, now)
		harness := newSubmissionRepositoryHarness(t, authority, now)
		usecase := gameusecase.NewSubmissionUseCase(harness.repository)

		correct, changed, err := usecase.Submit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.True(t, correct.Correct)
		require.Equal(t, int64(1), correct.Sequence)
		require.Equal(t, now, correct.CommittedAt)
		intentDigest, digestErr := gameusecase.SubmissionIntentDigest(command.SubmittedFlag)
		require.NoError(t, digestErr)
		require.Equal(t, intentDigest, harness.lastCommit().IntentDigest)

		invalidCommand := command
		invalidCommand.CommandID = submissionFixtureID(31)
		invalidCommand.SubmittedFlag = "wrong-answer"
		invalid, changed, err := usecase.Submit(t.Context(), invalidCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, invalid.Correct)
		require.Equal(t, int64(2), invalid.Sequence)
		require.NotContains(t, fmt.Sprintf("%#v", harness.lastCommit()), command.SubmittedFlag)
		require.NotContains(t, fmt.Sprintf("%#v", harness.records()), command.SubmittedFlag)
	})

	t.Run("orders equal server timestamps by monotonic Game sequence", func(t *testing.T) {
		t.Parallel()

		authority, command := submissionFixture(t, now)
		harness := newSubmissionRepositoryHarness(t, authority, now)
		usecase := gameusecase.NewSubmissionUseCase(harness.repository)
		for index := 0; index < 3; index++ {
			command.CommandID = submissionFixtureID(40 + index)
			_, _, err := usecase.Submit(t.Context(), command)
			require.NoError(t, err)
		}
		records := harness.records()
		ordered, err := gamedomain.OrderSubmissions([]gamedomain.Submission{
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
			mutateAuth func(*gameusecase.SubmissionAuthority)
			mutateCmd  func(*gameusecase.SubmissionCommand)
			expected   error
		}{
			{
				name: "foreign actor",
				mutateCmd: func(command *gameusecase.SubmissionCommand) {
					command.ActorParticipantID = submissionFixtureID(90)
				},
				expected: domain.ErrAssignmentParticipant,
			},
			{
				name: "disconnected participant",
				mutateAuth: func(authority *gameusecase.SubmissionAuthority) {
					authority.ConnectedParticipantIDs = authority.ConnectedParticipantIDs[1:]
				},
				expected: gameusecase.ErrSubmissionDisconnected,
			},
			{
				name: "inactive Game",
				mutateAuth: func(authority *gameusecase.SubmissionAuthority) {
					authority.StartedGame.Series.Series.Slots[0].Attempts[0].State =
						domain.GameStateReady
				},
				expected: gameusecase.ErrSubmissionNotOpen,
			},
			{
				name: "paused execution",
				mutateAuth: func(authority *gameusecase.SubmissionAuthority) {
					authority.Paused = true
				},
				expected: gameusecase.ErrSubmissionNotOpen,
			},
			{
				name: "malformed flag",
				mutateCmd: func(command *gameusecase.SubmissionCommand) {
					command.SubmittedFlag = ""
				},
				expected: gamedomain.ErrInvalidSubmission,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				authority, command := submissionFixture(t, now)
				if test.mutateAuth != nil {
					test.mutateAuth(&authority)
				}
				if test.mutateCmd != nil {
					test.mutateCmd(&command)
				}
				harness := newSubmissionRepositoryHarness(t, authority, now)
				record, changed, err := gameusecase.NewSubmissionUseCase(harness.repository).
					Submit(t.Context(), command)
				require.Zero(t, record)
				require.False(t, changed)
				require.ErrorIs(t, err, test.expected)
				require.Equal(t, 0, harness.commitCount())
			})
		}
	})

	t.Run("returns sanitized repository errors", func(t *testing.T) {
		t.Parallel()

		authority, command := submissionFixture(t, now)
		harness := newSubmissionRepositoryHarness(t, authority, now)
		harness.setCommitError(errors.New("storage rejected"))
		_, _, err := gameusecase.NewSubmissionUseCase(harness.repository).Submit(t.Context(), command)
		require.Error(t, err)
		require.NotContains(t, err.Error(), command.SubmittedFlag)
	})
}

type submissionRepositoryState struct {
	mu              sync.Mutex
	authority       gameusecase.SubmissionAuthority
	committedAt     time.Time
	commitErr       error
	commitConflicts int
	commits         int
	commit          gameusecase.SubmissionCommit
}

type submissionRepositoryHarness struct {
	repository *gamemocks.MockSubmissionRepository
	state      *submissionRepositoryState
}

func newSubmissionRepositoryHarness(
	t *testing.T,
	authority gameusecase.SubmissionAuthority,
	committedAt time.Time,
) *submissionRepositoryHarness {
	t.Helper()

	state := &submissionRepositoryState{
		authority:   cloneSubmissionAuthority(authority),
		committedAt: committedAt,
	}
	repository := gamemocks.NewMockSubmissionRepository(t)
	repository.EXPECT().LoadSubmissionAuthority(mock.Anything, mock.Anything).
		RunAndReturn(func(
			context.Context,
			gamedomain.SubmissionScope,
		) (gameusecase.SubmissionAuthority, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			return cloneSubmissionAuthority(state.authority), nil
		}).Maybe()
	repository.EXPECT().CommitSubmission(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			commit gameusecase.SubmissionCommit,
		) (*gamedomain.Submission, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.commits++
			state.commit = commit
			if state.commitConflicts > 0 {
				state.commitConflicts--
				return nil, false, domain.ErrConflict
			}
			if state.commitErr != nil {
				return nil, false, state.commitErr
			}
			if commit.ExpectedAuthorityRevision != state.authority.Revision ||
				state.authority.Paused ||
				commit.ExpectedGameState != domain.GameStateActive ||
				!commit.ExpectedDeadline.Equal(state.authority.StartedGame.Deadline) ||
				!state.committedAt.Before(commit.ExpectedDeadline) {
				return nil, false, domain.ErrConflict
			}
			record := gamedomain.Submission{
				Scope: commit.Scope, CommandID: commit.CommandID,
				ParticipantID: commit.ParticipantID,
				Sequence:      int64(len(state.authority.Submissions) + 1),
				CommittedAt:   state.committedAt, Correct: commit.Correct,
				SnapshotID: commit.SnapshotID, TaskID: commit.TaskID,
				ContentDigest: commit.ContentDigest,
			}
			state.authority.Revision++
			state.authority.Submissions = append(state.authority.Submissions, record)
			return &record, true, nil
		}).Maybe()
	return &submissionRepositoryHarness{repository: repository, state: state}
}

func (h *submissionRepositoryHarness) lastCommit() gameusecase.SubmissionCommit {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.commit
}

func (h *submissionRepositoryHarness) records() []gamedomain.Submission {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return append([]gamedomain.Submission(nil), h.state.authority.Submissions...)
}

func (h *submissionRepositoryHarness) commitCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.commits
}

func (h *submissionRepositoryHarness) setCommitError(err error) {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	h.state.commitErr = err
}

func submissionFixture(
	t *testing.T,
	now time.Time,
) (gameusecase.SubmissionAuthority, gameusecase.SubmissionCommand) {
	t.Helper()

	assignmentID := submissionFixtureID(6)
	participantIDs := [2]uuid.UUID{submissionFixtureID(20), submissionFixtureID(21)}
	task := domain.Task{
		ID: submissionFixtureID(22), Title: "Task 4",
		Description: "Solve the isolated challenge.", Category: domain.CategoryWeb,
		Difficulty: domain.DifficultyHard, TimeLimit: 90,
		Flag: "FLAG{4}", Hints: []string{"first hint", "second hint"},
	}
	taskSnapshot, err := taskexec.BuildSnapshot(taskexec.SnapshotInput{
		SnapshotID: submissionFixtureID(10), Version: 3,
		Kind: domain.AssignmentTaskKindNormal, Task: task,
	})
	require.NoError(t, err)
	digest, err := taskexec.SnapshotDigest(taskSnapshot)
	require.NoError(t, err)
	snapshot := submissionSnapshotFixture{
		snapshot: taskSnapshot, digest: digest, participantIDs: participantIDs,
	}
	tournamentID := submissionFixtureID(1)
	seriesID := submissionFixtureID(2)
	slotID := submissionFixtureID(3)
	gameID := submissionFixtureID(4)
	gameScope := gamedomain.Scope{
		TournamentID: tournamentID, SeriesID: seriesID, SlotID: slotID, GameID: gameID,
	}
	series := seriesdomain.Execution{Series: domain.Series{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID:  participantIDs[0],
		SecondParticipantID: participantIDs[1],
		Format:              domain.SeriesFormatBO3, State: domain.SeriesStateActive,
		Slots: []domain.GameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStateActive,
			}},
		}},
	}}
	scope := gamedomain.SubmissionScope{
		WaveID: submissionFixtureID(5), Game: gameScope, AssignmentID: assignmentID,
	}
	authority := gameusecase.SubmissionAuthority{
		Scope: scope, Revision: 1,
		StartedGame: gamedomain.Started{
			Scope: gameScope, ParticipantIDs: [2]uuid.UUID{
				participantIDs[0], participantIDs[1],
			},
			Series: series, AssignmentID: scope.AssignmentID, AssignmentRevision: 1,
			PlanRevisionID: submissionFixtureID(7), SnapshotID: snapshot.Snapshot().SnapshotID,
			ContentDigest: snapshot.ContentDigest(), DeadlineSeconds: 90,
			StartedAt: now.Add(-time.Second), Deadline: now.Add(89 * time.Second), DeliveryEnabled: true,
		},
		Snapshot: &snapshot,
		ConnectedParticipantIDs: []uuid.UUID{
			participantIDs[0], participantIDs[1],
		},
	}
	command := gameusecase.SubmissionCommand{
		Scope: scope, CommandID: submissionFixtureID(30),
		ActorParticipantID: participantIDs[0],
		ParticipantID:      participantIDs[0],
		SubmittedFlag:      snapshot.Snapshot().Flag,
	}
	return authority, command
}

type submissionSnapshotFixture struct {
	snapshot       domain.AssignmentTaskSnapshot
	digest         [sha256.Size]byte
	participantIDs [2]uuid.UUID
}

func (f *submissionSnapshotFixture) Snapshot() domain.AssignmentTaskSnapshot {
	clone := f.snapshot
	clone.Hints = append([]string(nil), f.snapshot.Hints...)
	return clone
}

func (f *submissionSnapshotFixture) ContentDigest() [sha256.Size]byte {
	return f.digest
}

func (f *submissionSnapshotFixture) HasParticipant(participantID uuid.UUID) bool {
	return participantID == f.participantIDs[0] || participantID == f.participantIDs[1]
}

func cloneSubmissionAuthority(
	authority gameusecase.SubmissionAuthority,
) gameusecase.SubmissionAuthority {
	cloned := authority
	cloned.ConnectedParticipantIDs = append([]uuid.UUID(nil), authority.ConnectedParticipantIDs...)
	cloned.Submissions = append([]gamedomain.Submission(nil), authority.Submissions...)
	return cloned
}

func submissionFixtureID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0037-%012d", value))
}
