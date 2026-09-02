package arena_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestArenaSubmissionAndRecoveryLogging(t *testing.T) {
	now := time.Date(2026, 9, 2, 13, 0, 0, 0, time.UTC)

	t.Run("emits accepted and idempotent submission events", func(t *testing.T) {
		authority, command := task037SubmissionFixture(t, now)
		repository := &arenaSubmissionRepositoryFake{authority: authority, committedAt: now}
		capture := &submissionEventCapture{}
		usecase := arena.NewArenaSubmissionUseCase(repository, capture)

		accepted, changed, err := usecase.Submit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.True(t, accepted.Correct)

		retained, changed, err := usecase.Submit(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, accepted, retained)

		require.Len(t, capture.events, 2)
		require.Equal(t, observability.ArenaOutcomeSuccess, capture.events[0].Outcome)
		require.Equal(t, "submission_accepted", capture.events[0].Transition)
		require.Equal(t, "committed", capture.events[0].ReasonCode)
		require.Equal(t, int64(1), capture.events[0].Revision)
		require.Equal(t, observability.ArenaOutcomeSuccess, capture.events[1].Outcome)
		require.Equal(t, "submission_idempotent", capture.events[1].Transition)
		require.Equal(t, "already_committed", capture.events[1].ReasonCode)
		require.Equal(t, "00000000-0000-0000-0037-000000000030", capture.events[1].CorrelationID)
		require.Equal(t, capture.events[1].CorrelationID, capture.events[1].CommandID)
		require.Equal(t, "game", capture.events[1].EntityKind)
		require.Equal(t, "submission", capture.events[1].Stage)
		requireSubmissionEventsSanitized(t, capture.events, authority, command, "")
	})

	t.Run("emits a bounded rejection reason", func(t *testing.T) {
		authority, command := task037SubmissionFixture(t, now)
		authority.ConnectedParticipantIDs = authority.ConnectedParticipantIDs[1:]
		capture := &submissionEventCapture{}

		record, changed, err := arena.NewArenaSubmissionUseCase(
			&arenaSubmissionRepositoryFake{authority: authority, committedAt: now},
			capture,
		).Submit(t.Context(), command)

		require.Zero(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrArenaSubmissionDisconnected)
		require.Len(t, capture.events, 1)
		require.Equal(t, observability.ArenaOutcomeRejected, capture.events[0].Outcome)
		require.Equal(t, "submission_rejected", capture.events[0].Transition)
		require.Equal(t, "participant_disconnected", capture.events[0].ReasonCode)
		requireSubmissionEventsSanitized(t, capture.events, authority, command, "")
	})

	t.Run("emits a conflict retry before acceptance", func(t *testing.T) {
		authority, command := task037SubmissionFixture(t, now)
		repository := &submissionConflictOnceRepository{delegate: &arenaSubmissionRepositoryFake{
			authority: authority, committedAt: now,
		}}
		capture := &submissionEventCapture{}

		record, changed, err := arena.NewArenaSubmissionUseCase(repository, capture).
			Submit(t.Context(), command)

		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, int64(1), record.Sequence)
		require.Len(t, capture.events, 2)
		require.Equal(t, observability.ArenaOutcomeRetry, capture.events[0].Outcome)
		require.Equal(t, "submission_conflict_retry", capture.events[0].Transition)
		require.Equal(t, "commit_conflict", capture.events[0].ReasonCode)
		require.Equal(t, observability.ArenaOutcomeSuccess, capture.events[1].Outcome)
		requireSubmissionEventsSanitized(t, capture.events, authority, command, "")
	})

	t.Run("reports conflict exhaustion after the real retry", func(t *testing.T) {
		authority, command := task037SubmissionFixture(t, now)
		capture := &submissionEventCapture{}

		record, changed, err := arena.NewArenaSubmissionUseCase(
			&arenaSubmissionRepositoryFake{
				authority: authority, committedAt: now, commitErr: domain.ErrConflict,
			},
			capture,
		).Submit(t.Context(), command)

		require.Zero(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrArenaSubmissionConflict)
		require.Len(t, capture.events, 2)
		require.Equal(t, observability.ArenaOutcomeRetry, capture.events[0].Outcome)
		require.Equal(t, observability.ArenaOutcomeFailure, capture.events[1].Outcome)
		require.Equal(t, "conflict_exhausted", capture.events[1].ReasonCode)
		requireSubmissionEventsSanitized(t, capture.events, authority, command, "")
	})

	t.Run("emits a sanitized failure without changing the returned error", func(t *testing.T) {
		authority, command := task037SubmissionFixture(t, now)
		storageErr := errors.New("storage credential sentinel")
		capture := &submissionEventCapture{}

		record, changed, err := arena.NewArenaSubmissionUseCase(
			&arenaSubmissionRepositoryFake{
				authority: authority, committedAt: now, commitErr: storageErr,
			},
			capture,
		).Submit(t.Context(), command)

		require.Zero(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, storageErr)
		require.Len(t, capture.events, 1)
		require.Equal(t, observability.ArenaOutcomeFailure, capture.events[0].Outcome)
		require.Equal(t, "submission_failed", capture.events[0].Transition)
		require.Equal(t, "commit_failed", capture.events[0].ReasonCode)
		requireSubmissionEventsSanitized(t, capture.events, authority, command, storageErr.Error())
	})

	t.Run("keeps the existing constructor form", func(t *testing.T) {
		authority, command := task037SubmissionFixture(t, now)

		record, changed, err := arena.NewArenaSubmissionUseCase(
			&arenaSubmissionRepositoryFake{authority: authority, committedAt: now},
		).Submit(t.Context(), command)

		require.NoError(t, err)
		require.True(t, changed)
		require.True(t, record.Correct)
	})
}

type submissionEventCapture struct {
	events []observability.ArenaEvent
}

func (c *submissionEventCapture) ObserveArenaEvent(_ context.Context, event observability.ArenaEvent) {
	c.events = append(c.events, event)
}

type submissionConflictOnceRepository struct {
	delegate   *arenaSubmissionRepositoryFake
	conflicted bool
}

func (r *submissionConflictOnceRepository) LoadArenaSubmissionAuthority(
	ctx context.Context,
	scope arena.ArenaSubmissionScope,
) (arena.ArenaSubmissionAuthority, error) {
	return r.delegate.LoadArenaSubmissionAuthority(ctx, scope)
}

func (r *submissionConflictOnceRepository) CommitArenaSubmission(
	ctx context.Context,
	commit arena.ArenaSubmissionCommit,
) (*arena.ArenaSubmissionRecord, bool, error) {
	if !r.conflicted {
		r.conflicted = true
		return nil, false, domain.ErrConflict
	}
	return r.delegate.CommitArenaSubmission(ctx, commit)
}

func requireSubmissionEventsSanitized(
	t *testing.T,
	events []observability.ArenaEvent,
	authority arena.ArenaSubmissionAuthority,
	command arena.ArenaSubmissionCommand,
	rawError string,
) {
	t.Helper()

	rendered := fmt.Sprintf("%#v", events)
	snapshot := authority.Snapshot.Snapshot()
	require.NotContains(t, rendered, command.SubmittedFlag)
	require.NotContains(t, rendered, snapshot.SnapshotID.String())
	require.NotContains(t, rendered, snapshot.TaskID.String())
	if rawError != "" {
		require.NotContains(t, rendered, rawError)
	}
}

var _ observability.ArenaEventObserver = (*submissionEventCapture)(nil)
var _ arena.ArenaSubmissionRepository = (*submissionConflictOnceRepository)(nil)
