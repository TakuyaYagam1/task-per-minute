package arena_test

import (
	"context"
	"crypto/sha256"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestCorrectionStageRollback(t *testing.T) {
	t.Parallel()

	command, authority := task056CorrectionFixture(t)
	correction, err := arena.PlanAtomicCorrection(command, authority)
	require.NoError(t, err)
	previousSource := task057StageRevision(t, correction.CutoffCondition().AffectedRevisions())
	correctedSource := task057StageProjection(t, correction.ProjectionRevisions())
	changedAt := command.RequestedAt.Add(time.Minute)

	t.Run("moves playoff execution to Golden", func(t *testing.T) {
		t.Parallel()

		corrected := task057GoldenGroup(
			t, command.TournamentID, task057ID(10), task057RevisionID(11), correctedSource,
			1, []uuid.UUID{task057ID(20), task057ID(21)}, nil,
		)
		repository := &task057CorrectionStageRepository{snapshot: arena.CorrectionStageSnapshot{
			TournamentID: command.TournamentID, TournamentState: authority.TournamentState,
			TournamentRevision: authority.TournamentRevision,
			Layout:             arena.CorrectionStageLayout{Mode: arena.CorrectionStagePlayoff},
		}}
		result, err := arena.NewCorrectionStageUseCase(
			directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt},
		).Rollback(t.Context(), arena.CorrectionStageCommand{
			Correction: correction,
			Corrected: arena.CorrectionStageLayout{
				Mode: arena.CorrectionStageGolden, GoldenGroups: []domain.ArenaGoldenGroupState{corrected},
			},
		})
		require.NoError(t, err)
		require.Equal(t, arena.CorrectionStagePlayoffToGolden, result.Transition)
		require.True(t, result.WithdrawPlayoff)
		require.False(t, result.CreatePlayoff)
		require.Empty(t, result.GroupSupersessions)
		require.Empty(t, result.CancelledAttempts)
		require.Equal(t, 1, repository.writes)
		require.Equal(t, corrected.ID, repository.commit.Result.Corrected.GoldenGroups[0].ID)
	})

	t.Run("moves paused Golden execution to playoff atomically", func(t *testing.T) {
		t.Parallel()

		current, pause := task057PausedGoldenGroup(
			t, command.TournamentID, task057ID(30), task057RevisionID(31), previousSource,
			[]uuid.UUID{task057ID(40), task057ID(41)}, changedAt.Add(-time.Minute),
		)
		repository := &task057CorrectionStageRepository{snapshot: arena.CorrectionStageSnapshot{
			TournamentID: command.TournamentID, TournamentState: authority.TournamentState,
			TournamentRevision: authority.TournamentRevision,
			Layout: arena.CorrectionStageLayout{
				Mode:         arena.CorrectionStageGolden,
				GoldenGroups: []domain.ArenaGoldenGroupState{current},
				Paused:       []arena.RetainedGoldenPrestartExpectation{pause},
			},
		}}
		nextRevisionID := task057RevisionID(32)
		result, err := arena.NewCorrectionStageUseCase(
			directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt},
		).Rollback(t.Context(), arena.CorrectionStageCommand{
			Correction: correction,
			Corrected:  arena.CorrectionStageLayout{Mode: arena.CorrectionStagePlayoff},
			GroupSupersessions: []arena.CorrectionStageGroupSupersessionIntent{{
				GroupID: current.ID, RevisionID: nextRevisionID,
			}},
		})
		require.NoError(t, err)
		require.Equal(t, arena.CorrectionStageGoldenToPlayoff, result.Transition)
		require.True(t, result.CreatePlayoff)
		require.Len(t, result.GroupSupersessions, 1)
		require.Equal(t, current.RevisionID, result.GroupSupersessions[0].PreviousRevisionID)
		require.Equal(t, nextRevisionID, result.GroupSupersessions[0].RevisionID)
		require.Nil(t, result.GroupSupersessions[0].ReplacementGroupID)
		require.Len(t, result.CancelledAttempts, 1)
		require.Equal(t, domain.ArenaGoldenAttemptStateCancelled, result.CancelledAttempts[0].State)
		require.Equal(t, changedAt, *result.CancelledAttempts[0].FinishedAt)
		require.Equal(t, nextRevisionID, result.CancelledAttempts[0].GroupRevisionID)
		require.Equal(t, 1, repository.writes)
		require.Len(t, repository.commit.Result.GroupSupersessions, 1)
		require.Len(t, repository.commit.Result.CancelledAttempts, 1)
	})

	t.Run("replaces changed Golden tie groups with fresh identities", func(t *testing.T) {
		t.Parallel()

		current, pause := task057PausedGoldenGroup(
			t, command.TournamentID, task057ID(50), task057RevisionID(51), previousSource,
			[]uuid.UUID{task057ID(60), task057ID(61), task057ID(62)}, changedAt.Add(-time.Minute),
		)
		replacement := task057GoldenGroup(
			t, command.TournamentID, task057ID(70), task057RevisionID(71), correctedSource,
			1, []uuid.UUID{task057ID(60), task057ID(61)}, nil,
		)
		repository := &task057CorrectionStageRepository{snapshot: arena.CorrectionStageSnapshot{
			TournamentID: command.TournamentID, TournamentState: authority.TournamentState,
			TournamentRevision: authority.TournamentRevision,
			Layout: arena.CorrectionStageLayout{
				Mode:         arena.CorrectionStageGolden,
				GoldenGroups: []domain.ArenaGoldenGroupState{current},
				Paused:       []arena.RetainedGoldenPrestartExpectation{pause},
			},
		}}
		nextRevisionID := task057RevisionID(52)
		result, err := arena.NewCorrectionStageUseCase(
			directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt},
		).Rollback(t.Context(), arena.CorrectionStageCommand{
			Correction: correction,
			Corrected: arena.CorrectionStageLayout{
				Mode: arena.CorrectionStageGolden, GoldenGroups: []domain.ArenaGoldenGroupState{replacement},
			},
			GroupSupersessions: []arena.CorrectionStageGroupSupersessionIntent{{
				GroupID: current.ID, RevisionID: nextRevisionID,
			}},
		})
		require.NoError(t, err)
		require.Equal(t, arena.CorrectionStageGoldenGroupsChanged, result.Transition)
		require.Len(t, result.GroupSupersessions, 1)
		require.NotNil(t, result.GroupSupersessions[0].ReplacementGroupID)
		require.Equal(t, replacement.ID, *result.GroupSupersessions[0].ReplacementGroupID)
		require.NotEqual(t, current.ID, replacement.ID)
		require.NotEqual(t, current.RevisionID, replacement.RevisionID)
		require.Len(t, result.CancelledAttempts, 1)
		require.Equal(t, domain.ArenaGoldenAttemptStateCancelled, result.CancelledAttempts[0].State)
		require.Equal(t, 1, repository.writes)
	})

	t.Run("rejects a Golden Attempt that already started", func(t *testing.T) {
		t.Parallel()

		current, pause := task057PausedGoldenGroup(
			t, command.TournamentID, task057ID(75), task057RevisionID(76), previousSource,
			[]uuid.UUID{task057ID(77), task057ID(78)}, changedAt.Add(-time.Minute),
		)
		startedAt := changedAt.Add(-30 * time.Second)
		current.Attempts[0].State = domain.ArenaGoldenAttemptStateActive
		current.Attempts[0].StartedAt = &startedAt
		_, err := domain.NewArenaGoldenGroup(current)
		require.NoError(t, err)
		repository := &task057CorrectionStageRepository{snapshot: arena.CorrectionStageSnapshot{
			TournamentID: command.TournamentID, TournamentState: authority.TournamentState,
			TournamentRevision: authority.TournamentRevision,
			Layout: arena.CorrectionStageLayout{
				Mode: arena.CorrectionStageGolden, GoldenGroups: []domain.ArenaGoldenGroupState{current},
				Paused: []arena.RetainedGoldenPrestartExpectation{pause},
			},
		}}
		result, err := arena.NewCorrectionStageUseCase(
			directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt},
		).Rollback(t.Context(), arena.CorrectionStageCommand{
			Correction: correction, Corrected: arena.CorrectionStageLayout{Mode: arena.CorrectionStagePlayoff},
			GroupSupersessions: []arena.CorrectionStageGroupSupersessionIntent{{
				GroupID: current.ID, RevisionID: task057RevisionID(79),
			}},
		})
		require.ErrorIs(t, err, arena.ErrInvalidCorrectionStage)
		require.Equal(t, arena.CorrectionStageResult{}, result)
		require.Zero(t, repository.writes)
	})

	t.Run("rejects a cutoff without committing state", func(t *testing.T) {
		t.Parallel()

		current, pause := task057PausedGoldenGroup(
			t, command.TournamentID, task057ID(80), task057RevisionID(81), previousSource,
			[]uuid.UUID{task057ID(90), task057ID(91)}, changedAt.Add(-time.Minute),
		)
		target := correction.CutoffCondition().TargetRevision()
		cutoffs := []struct {
			name   string
			state  domain.ArenaTournamentState
			events []arena.CorrectionCutoffEvent
		}{
			{name: "wave started", state: authority.TournamentState, events: task057CutoffEvents(command.TournamentID, target, arena.CorrectionCutoffWaveStarted, 920)},
			{name: "task delivered", state: authority.TournamentState, events: task057CutoffEvents(command.TournamentID, target, arena.CorrectionCutoffTaskDelivered, 921)},
			{name: "no show recorded", state: authority.TournamentState, events: task057CutoffEvents(command.TournamentID, target, arena.CorrectionCutoffNoShowRecorded, 922)},
			{name: "forfeit recorded", state: authority.TournamentState, events: task057CutoffEvents(command.TournamentID, target, arena.CorrectionCutoffForfeitRecorded, 923)},
			{name: "Golden direct allocated", state: authority.TournamentState, events: task057CutoffEvents(command.TournamentID, target, arena.CorrectionCutoffGoldenAllocated, 924)},
			{name: "Tournament terminal", state: domain.ArenaTournamentStateCompleted},
		}
		for _, cutoff := range cutoffs {
			t.Run(cutoff.name, func(t *testing.T) {
				repository := &task057CorrectionStageRepository{snapshot: arena.CorrectionStageSnapshot{
					TournamentID: command.TournamentID, TournamentState: cutoff.state,
					TournamentRevision: authority.TournamentRevision, CutoffEvents: cutoff.events,
					Layout: arena.CorrectionStageLayout{
						Mode:         arena.CorrectionStageGolden,
						GoldenGroups: []domain.ArenaGoldenGroupState{current},
						Paused:       []arena.RetainedGoldenPrestartExpectation{pause},
					},
				}}
				before := repository.snapshot
				result, err := arena.NewCorrectionStageUseCase(
					directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt},
				).Rollback(t.Context(), arena.CorrectionStageCommand{
					Correction: correction,
					Corrected:  arena.CorrectionStageLayout{Mode: arena.CorrectionStagePlayoff},
					GroupSupersessions: []arena.CorrectionStageGroupSupersessionIntent{{
						GroupID: current.ID, RevisionID: task057RevisionID(82),
					}},
				})
				require.ErrorIs(t, err, arena.ErrCorrectionCutoff)
				require.Equal(t, arena.CorrectionStageResult{}, result)
				require.Zero(t, repository.writes)
				require.True(t, reflect.DeepEqual(before, repository.snapshot))
			})
		}
	})
}

func task057CutoffEvents(
	tournamentID uuid.UUID,
	target domain.ArenaDerivedRevision,
	kind arena.CorrectionCutoffKind,
	id int,
) []arena.CorrectionCutoffEvent {
	return []arena.CorrectionCutoffEvent{{
		ID: task057ID(id), Kind: kind, TournamentID: tournamentID,
		SourceRevisionID: target.ID(), OccurredAt: target.CreatedAt().Add(time.Second),
	}}
}

func task057StageRevision(
	t *testing.T,
	revisions []domain.ArenaDerivedRevision,
) domain.ArenaDerivedRevisionID {
	t.Helper()
	for _, revision := range revisions {
		if revision.Artifact().Kind == domain.ArenaArtifactKindStandings {
			return revision.ID()
		}
	}
	t.Fatal("correction cutoff has no standings revision")
	return domain.ArenaDerivedRevisionID{}
}

func task057StageProjection(
	t *testing.T,
	projections []domain.ArenaProjectionRevision,
) domain.ArenaDerivedRevisionID {
	t.Helper()
	revisions := make([]domain.ArenaDerivedRevision, len(projections))
	for index, projection := range projections {
		revisions[index] = projection.Revision()
	}
	return task057StageRevision(t, revisions)
}

func task057PausedGoldenGroup(
	t *testing.T,
	tournamentID, groupID uuid.UUID,
	revisionID, sourceRevisionID domain.ArenaDerivedRevisionID,
	participants []uuid.UUID,
	pausedAt time.Time,
) (domain.ArenaGoldenGroupState, arena.RetainedGoldenPrestartExpectation) {
	t.Helper()
	attemptID := task057ID(int(groupID[15]) + 1000)
	retainedAt := pausedAt
	attempt := domain.ArenaGoldenAttempt{
		ID: attemptID, GroupID: groupID, GroupRevisionID: revisionID, AttemptNo: 1,
		State: domain.ArenaGoldenAttemptStatePlanned, ParticipantIDs: append([]uuid.UUID(nil), participants...),
		RetainedAt: &retainedAt,
	}
	group := task057GoldenGroup(
		t, tournamentID, groupID, revisionID, sourceRevisionID, 1, participants, &attempt,
	)
	return group, arena.RetainedGoldenPrestartExpectation{
		Scope: arena.GoldenStateScope{
			TournamentID: tournamentID, GroupID: groupID, GroupRevisionID: revisionID,
		},
		SessionID: task057ID(int(groupID[15]) + 1100), RevisionID: task057ID(int(groupID[15]) + 1200),
		Revision: 1, State: arena.RetainedGoldenPrestartPaused,
		PayloadDigest: sha256.Sum256([]byte("retained Golden technical pause")),
	}
}

func task057GoldenGroup(
	t *testing.T,
	tournamentID, groupID uuid.UUID,
	revisionID, sourceRevisionID domain.ArenaDerivedRevisionID,
	positionFrom int,
	participants []uuid.UUID,
	attempt *domain.ArenaGoldenAttempt,
) domain.ArenaGoldenGroupState {
	t.Helper()
	members := make([]domain.ArenaGoldenMember, len(participants))
	for index, participantID := range participants {
		members[index] = domain.ArenaGoldenMember{ParticipantID: participantID}
	}
	state := domain.ArenaGoldenGroupState{
		ID: groupID, TournamentID: tournamentID, RevisionID: revisionID,
		SourceProjectionRevisionID: sourceRevisionID,
		PositionFrom:               positionFrom, PositionTo: positionFrom + len(participants) - 1,
		ParticipationEstablished: attempt != nil, Members: members,
	}
	if attempt != nil {
		state.Attempts = []domain.ArenaGoldenAttempt{*attempt}
	}
	_, err := domain.NewArenaGoldenGroup(state)
	require.NoError(t, err)
	return state
}

type task057CorrectionStageRepository struct {
	snapshot arena.CorrectionStageSnapshot
	commit   arena.CorrectionStageCommit
	writes   int
}

func (r *task057CorrectionStageRepository) LoadCorrectionStage(
	_ context.Context,
	_ uuid.UUID,
) (arena.CorrectionStageSnapshot, error) {
	return r.snapshot, nil
}

func (r *task057CorrectionStageRepository) CommitCorrectionStage(
	_ context.Context,
	commit arena.CorrectionStageCommit,
) (bool, error) {
	r.commit = commit
	r.snapshot.Layout = commit.Result.Corrected
	r.writes++
	return true, nil
}

var _ arena.CorrectionStageRepository = (*task057CorrectionStageRepository)(nil)
