package correction_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestCorrectionStageRollback(t *testing.T) {
	t.Parallel()

	command, authority := task056CorrectionFixture(t)
	correction, err := correctionusecase.BuildPlan(command, authority)
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
		repository := newTask057CorrectionStageRepository(t, correctionusecase.StageSnapshot{
			TournamentID: command.TournamentID, TournamentState: authority.TournamentState,
			TournamentRevision: authority.TournamentRevision,
			Layout:             correctionusecase.StageLayout{Mode: correctionusecase.StageModePlayoff},
		})
		stageResult, err := newTask057CorrectionStageUseCase(t, repository, changedAt).Rollback(
			t.Context(), correctionusecase.StageCommand{
				Correction: correction,
				Corrected: correctionusecase.StageLayout{
					Mode: correctionusecase.StageModeGolden, GoldenGroups: []domain.GoldenGroupState{corrected},
				},
			},
		)
		require.NoError(t, err)
		require.Equal(t, correctionusecase.StagePlayoffToGolden, stageResult.Transition)
		require.True(t, stageResult.WithdrawPlayoff)
		require.False(t, stageResult.CreatePlayoff)
		require.Empty(t, stageResult.GroupSupersessions)
		require.Empty(t, stageResult.CancelledAttempts)
		require.Equal(t, 1, repository.writeCount())
		require.Equal(t, corrected.ID, repository.committed().Result.Corrected.GoldenGroups[0].ID)
	})

	t.Run("moves paused Golden execution to playoff atomically", func(t *testing.T) {
		t.Parallel()

		current, pause := task057PausedGoldenGroup(
			t, command.TournamentID, task057ID(30), task057RevisionID(31), previousSource,
			[]uuid.UUID{task057ID(40), task057ID(41)}, changedAt.Add(-time.Minute),
		)
		repository := newTask057CorrectionStageRepository(t, correctionusecase.StageSnapshot{
			TournamentID: command.TournamentID, TournamentState: authority.TournamentState,
			TournamentRevision: authority.TournamentRevision,
			Layout: correctionusecase.StageLayout{
				Mode:         correctionusecase.StageModeGolden,
				GoldenGroups: []domain.GoldenGroupState{current},
				Paused:       []correctionusecase.StagePauseExpectation{pause},
			},
		})
		nextRevisionID := task057RevisionID(32)
		stageResult, err := newTask057CorrectionStageUseCase(t, repository, changedAt).Rollback(
			t.Context(), correctionusecase.StageCommand{
				Correction: correction,
				Corrected:  correctionusecase.StageLayout{Mode: correctionusecase.StageModePlayoff},
				GroupSupersessions: []correctionusecase.StageGroupSupersessionIntent{{
					GroupID: current.ID, RevisionID: nextRevisionID,
				}},
			},
		)
		require.NoError(t, err)
		require.Equal(t, correctionusecase.StageGoldenToPlayoff, stageResult.Transition)
		require.True(t, stageResult.CreatePlayoff)
		require.Len(t, stageResult.GroupSupersessions, 1)
		require.Equal(t, current.RevisionID, stageResult.GroupSupersessions[0].PreviousRevisionID)
		require.Equal(t, nextRevisionID, stageResult.GroupSupersessions[0].RevisionID)
		require.Nil(t, stageResult.GroupSupersessions[0].ReplacementGroupID)
		require.Len(t, stageResult.CancelledAttempts, 1)
		require.Equal(t, domain.GoldenAttemptStateCancelled, stageResult.CancelledAttempts[0].State)
		require.Equal(t, changedAt, *stageResult.CancelledAttempts[0].FinishedAt)
		require.Equal(t, nextRevisionID, stageResult.CancelledAttempts[0].GroupRevisionID)
		require.Equal(t, 1, repository.writeCount())
		require.Len(t, repository.committed().Result.GroupSupersessions, 1)
		require.Len(t, repository.committed().Result.CancelledAttempts, 1)
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
		repository := newTask057CorrectionStageRepository(t, correctionusecase.StageSnapshot{
			TournamentID: command.TournamentID, TournamentState: authority.TournamentState,
			TournamentRevision: authority.TournamentRevision,
			Layout: correctionusecase.StageLayout{
				Mode:         correctionusecase.StageModeGolden,
				GoldenGroups: []domain.GoldenGroupState{current},
				Paused:       []correctionusecase.StagePauseExpectation{pause},
			},
		})
		nextRevisionID := task057RevisionID(52)
		stageResult, err := newTask057CorrectionStageUseCase(t, repository, changedAt).Rollback(
			t.Context(), correctionusecase.StageCommand{
				Correction: correction,
				Corrected: correctionusecase.StageLayout{
					Mode: correctionusecase.StageModeGolden, GoldenGroups: []domain.GoldenGroupState{replacement},
				},
				GroupSupersessions: []correctionusecase.StageGroupSupersessionIntent{{
					GroupID: current.ID, RevisionID: nextRevisionID,
				}},
			},
		)
		require.NoError(t, err)
		require.Equal(t, correctionusecase.StageGoldenGroupsChanged, stageResult.Transition)
		require.Len(t, stageResult.GroupSupersessions, 1)
		require.NotNil(t, stageResult.GroupSupersessions[0].ReplacementGroupID)
		require.Equal(t, replacement.ID, *stageResult.GroupSupersessions[0].ReplacementGroupID)
		require.NotEqual(t, current.ID, replacement.ID)
		require.NotEqual(t, current.RevisionID, replacement.RevisionID)
		require.Len(t, stageResult.CancelledAttempts, 1)
		require.Equal(t, domain.GoldenAttemptStateCancelled, stageResult.CancelledAttempts[0].State)
		require.Equal(t, 1, repository.writeCount())
	})

	t.Run("rejects a Golden Attempt that already started", func(t *testing.T) {
		t.Parallel()

		current, pause := task057PausedGoldenGroup(
			t, command.TournamentID, task057ID(75), task057RevisionID(76), previousSource,
			[]uuid.UUID{task057ID(77), task057ID(78)}, changedAt.Add(-time.Minute),
		)
		startedAt := changedAt.Add(-30 * time.Second)
		current.Attempts[0].State = domain.GoldenAttemptStateActive
		current.Attempts[0].StartedAt = &startedAt
		_, err := domain.NewGoldenGroup(current)
		require.NoError(t, err)
		repository := newTask057CorrectionStageRepository(t, correctionusecase.StageSnapshot{
			TournamentID: command.TournamentID, TournamentState: authority.TournamentState,
			TournamentRevision: authority.TournamentRevision,
			Layout: correctionusecase.StageLayout{
				Mode: correctionusecase.StageModeGolden, GoldenGroups: []domain.GoldenGroupState{current},
				Paused: []correctionusecase.StagePauseExpectation{pause},
			},
		})
		stageResult, err := newTask057CorrectionStageUseCase(t, repository, changedAt).Rollback(
			t.Context(), correctionusecase.StageCommand{
				Correction: correction, Corrected: correctionusecase.StageLayout{Mode: correctionusecase.StageModePlayoff},
				GroupSupersessions: []correctionusecase.StageGroupSupersessionIntent{{
					GroupID: current.ID, RevisionID: task057RevisionID(79),
				}},
			},
		)
		require.ErrorIs(t, err, correctionusecase.ErrInvalidStage)
		require.Equal(t, correctionusecase.StageResult{}, stageResult)
		require.Zero(t, repository.writeCount())
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
			state  domain.TournamentState
			events []correctionusecase.CutoffEvent
		}{
			{name: "wave started", state: authority.TournamentState, events: task057CutoffEvents(command.TournamentID, target, correctionusecase.CutoffWaveStarted, 920)},
			{name: "task delivered", state: authority.TournamentState, events: task057CutoffEvents(command.TournamentID, target, correctionusecase.CutoffTaskDelivered, 921)},
			{name: "no show recorded", state: authority.TournamentState, events: task057CutoffEvents(command.TournamentID, target, correctionusecase.CutoffNoShowRecorded, 922)},
			{name: "forfeit recorded", state: authority.TournamentState, events: task057CutoffEvents(command.TournamentID, target, correctionusecase.CutoffForfeitRecorded, 923)},
			{name: "Golden direct allocated", state: authority.TournamentState, events: task057CutoffEvents(command.TournamentID, target, correctionusecase.CutoffGoldenAllocated, 924)},
			{name: "Tournament terminal", state: domain.TournamentStateCompleted},
		}
		for _, cutoff := range cutoffs {
			t.Run(cutoff.name, func(t *testing.T) {
				t.Parallel()

				repository := newTask057CorrectionStageRepository(t, correctionusecase.StageSnapshot{
					TournamentID: command.TournamentID, TournamentState: cutoff.state,
					TournamentRevision: authority.TournamentRevision, CutoffEvents: cutoff.events,
					Layout: correctionusecase.StageLayout{
						Mode:         correctionusecase.StageModeGolden,
						GoldenGroups: []domain.GoldenGroupState{current},
						Paused:       []correctionusecase.StagePauseExpectation{pause},
					},
				})
				before := repository.loaded()
				stageResult, err := newTask057CorrectionStageUseCase(t, repository, changedAt).Rollback(
					t.Context(), correctionusecase.StageCommand{
						Correction: correction,
						Corrected:  correctionusecase.StageLayout{Mode: correctionusecase.StageModePlayoff},
						GroupSupersessions: []correctionusecase.StageGroupSupersessionIntent{{
							GroupID: current.ID, RevisionID: task057RevisionID(82),
						}},
					},
				)
				require.ErrorIs(t, err, correctionusecase.ErrCutoff)
				require.Equal(t, correctionusecase.StageResult{}, stageResult)
				require.Zero(t, repository.writeCount())
				require.True(t, reflect.DeepEqual(before, repository.loaded()))
			})
		}
	})
}

func TestPlanServerOwnedStageRollbackCancelsPausedGoldenWithDeterministicIdentity(t *testing.T) {
	t.Parallel()

	command, authority := task056CorrectionFixture(t)
	correction, err := correctionusecase.BuildPlan(command, authority)
	require.NoError(t, err)
	previousSource := task057StageRevision(t, correction.CutoffCondition().AffectedRevisions())
	changedAt := command.RequestedAt.Add(time.Minute)
	current, pause := task057PausedGoldenGroup(
		t, command.TournamentID, task057ID(210), task057RevisionID(211), previousSource,
		[]uuid.UUID{task057ID(212), task057ID(213)}, changedAt.Add(-time.Minute),
	)
	snapshot := correctionusecase.StageSnapshot{
		TournamentID: command.TournamentID, TournamentState: authority.TournamentState,
		TournamentRevision: authority.TournamentRevision,
		Layout: correctionusecase.StageLayout{
			Mode: correctionusecase.StageModeGolden, GoldenGroups: []domain.GoldenGroupState{current},
			Paused: []correctionusecase.StagePauseExpectation{pause},
		},
		Swiss: task057SwissAuthority(t, correction, task057SwissNoImpactingTie),
	}

	result, err := correctionusecase.PlanServerOwnedStageRollback(correction, snapshot, changedAt)
	require.NoError(t, err)
	require.Equal(t, correctionusecase.StageGoldenToPlayoff, result.Transition)
	require.True(t, result.CreatePlayoff)
	require.Len(t, result.GroupSupersessions, 1)
	require.Equal(
		t,
		domain.DerivedRevisionID(uuid.NewSHA1(
			command.CommandID,
			[]byte("tournament-correction:golden-supersession:"+current.ID.String()),
		)),
		result.GroupSupersessions[0].RevisionID,
	)
	require.Len(t, result.CancelledAttempts, 1)
	require.Equal(t, domain.GoldenAttemptStateCancelled, result.CancelledAttempts[0].State)
	require.Equal(t, changedAt, *result.CancelledAttempts[0].FinishedAt)
	require.NoError(t, correctionusecase.ValidateServerOwnedStageRollback(correction, snapshot, changedAt, result))

	tampered := result
	tampered.CancelledAttempts = append([]domain.GoldenAttempt(nil), result.CancelledAttempts...)
	tampered.CancelledAttempts[0].ID = uuid.New()
	require.ErrorIs(
		t,
		correctionusecase.ValidateServerOwnedStageRollback(correction, snapshot, changedAt, tampered),
		correctionusecase.ErrInvalidStage,
	)
}

func TestPlanServerOwnedStageRollbackDerivesEveryTopologyFromSwissLedger(t *testing.T) {
	t.Parallel()

	command, authority := task056CorrectionFixture(t)
	correction, err := correctionusecase.BuildPlan(command, authority)
	require.NoError(t, err)
	changedAt := command.RequestedAt.Add(time.Minute)
	source := task057StageProjection(t, correction.ProjectionRevisions())

	t.Run("playoff to Golden", func(t *testing.T) {
		t.Parallel()
		result, planErr := correctionusecase.PlanServerOwnedStageRollback(correction, correctionusecase.StageSnapshot{
			TournamentID: command.TournamentID, TournamentState: domain.TournamentStatePlayoffs,
			TournamentRevision: authority.TournamentRevision,
			Layout:             correctionusecase.StageLayout{Mode: correctionusecase.StageModePlayoff},
			Swiss:              task057SwissAuthority(t, correction, task057SwissImpactingTie),
		}, changedAt)
		require.NoError(t, planErr)
		require.Equal(t, correctionusecase.StagePlayoffToGolden, result.Transition)
		require.True(t, result.WithdrawPlayoff)
		require.NotEmpty(t, result.Corrected.GoldenGroups)
		require.Equal(t, source, result.Corrected.GoldenGroups[0].SourceProjectionRevisionID)
	})

	t.Run("Golden to playoffs", func(t *testing.T) {
		t.Parallel()
		current, pause := task057PausedGoldenGroup(
			t, command.TournamentID, task057ID(330), task057RevisionID(331), source,
			[]uuid.UUID{task057ID(332), task057ID(333)}, changedAt.Add(-time.Minute),
		)
		result, planErr := correctionusecase.PlanServerOwnedStageRollback(correction, correctionusecase.StageSnapshot{
			TournamentID: command.TournamentID, TournamentState: domain.TournamentStateGolden,
			TournamentRevision: authority.TournamentRevision,
			Layout: correctionusecase.StageLayout{Mode: correctionusecase.StageModeGolden,
				GoldenGroups: []domain.GoldenGroupState{current}, Paused: []correctionusecase.StagePauseExpectation{pause}},
			Swiss: task057SwissAuthority(t, correction, task057SwissNoImpactingTie),
		}, changedAt)
		require.NoError(t, planErr)
		require.Equal(t, correctionusecase.StageGoldenToPlayoff, result.Transition)
		require.True(t, result.CreatePlayoff)
		require.Len(t, result.GroupSupersessions, 1)
	})

	t.Run("Golden groups changed", func(t *testing.T) {
		t.Parallel()
		current, pause := task057PausedGoldenGroup(
			t, command.TournamentID, task057ID(340), task057RevisionID(341), source,
			[]uuid.UUID{task057ID(342), task057ID(343)}, changedAt.Add(-time.Minute),
		)
		result, planErr := correctionusecase.PlanServerOwnedStageRollback(correction, correctionusecase.StageSnapshot{
			TournamentID: command.TournamentID, TournamentState: domain.TournamentStateGolden,
			TournamentRevision: authority.TournamentRevision,
			Layout: correctionusecase.StageLayout{Mode: correctionusecase.StageModeGolden,
				GoldenGroups: []domain.GoldenGroupState{current}, Paused: []correctionusecase.StagePauseExpectation{pause}},
			Swiss: task057SwissAuthority(t, correction, task057SwissImpactingTie),
		}, changedAt)
		require.NoError(t, planErr)
		require.Equal(t, correctionusecase.StageGoldenGroupsChanged, result.Transition)
		require.NotEmpty(t, result.Corrected.GoldenGroups)
		require.Len(t, result.GroupSupersessions, 1)
	})

	t.Run("unchanged Golden topology", func(t *testing.T) {
		t.Parallel()
		swissAuthority := task057SwissAuthority(t, correction, task057SwissImpactingTie)
		seed, seedErr := correctionusecase.PlanServerOwnedStageRollback(correction, correctionusecase.StageSnapshot{
			TournamentID: command.TournamentID, TournamentState: domain.TournamentStatePlayoffs,
			TournamentRevision: authority.TournamentRevision,
			Layout:             correctionusecase.StageLayout{Mode: correctionusecase.StageModePlayoff},
			Swiss:              swissAuthority,
		}, changedAt)
		require.NoError(t, seedErr)
		current := seed.Corrected.GoldenGroups
		result, planErr := correctionusecase.PlanServerOwnedStageRollback(correction, correctionusecase.StageSnapshot{
			TournamentID: command.TournamentID, TournamentState: domain.TournamentStateGolden,
			TournamentRevision: authority.TournamentRevision,
			Layout:             correctionusecase.StageLayout{Mode: correctionusecase.StageModeGolden, GoldenGroups: current},
			Swiss:              swissAuthority,
		}, changedAt)
		require.NoError(t, planErr)
		require.Equal(t, correctionusecase.StageUnchanged, result.Transition)
		require.Empty(t, result.GroupSupersessions)
		require.Equal(t, current, result.Corrected.GoldenGroups)
	})
}

type task057SwissShape int

const (
	task057SwissImpactingTie task057SwissShape = iota + 1
	task057SwissNoImpactingTie
)

func task057SwissAuthority(
	t *testing.T,
	correction correctionusecase.Plan,
	shape task057SwissShape,
) correctionusecase.StageSwissAuthority {
	t.Helper()
	series := correction.Series()
	participants := []uuid.UUID{series.FirstParticipantID, series.SecondParticipantID, task057ID(510), task057ID(511)}
	ledger := make([]resultprojection.CanonicalSwissPointLedgerEntry, 0, 12)
	appendResult := func(round int, seriesID, first, second, winner uuid.UUID) {
		resultID := task057ID(600 + round*10 + len(ledger))
		firstPoints, secondPoints := 0, 0
		if winner == first {
			firstPoints = swissusecase.SeriesWinPoints
		} else {
			secondPoints = swissusecase.SeriesWinPoints
		}
		ledger = append(ledger,
			resultprojection.CanonicalSwissPointLedgerEntry{RoundID: task057ID(700 + round), RoundRevisionID: task057ID(710 + round), RoundNumber: round,
				SourceKind: swissusecase.PointSourceSeries, SourceSeriesID: seriesID, SeriesResultRevisionID: resultID,
				ResultLabel: swissusecase.SeriesResultPlayed, ParticipantID: first, OpponentID: &second, Points: firstPoints, StableSeed: 1 + indexUUID(participants, first)},
			resultprojection.CanonicalSwissPointLedgerEntry{RoundID: task057ID(700 + round), RoundRevisionID: task057ID(710 + round), RoundNumber: round,
				SourceKind: swissusecase.PointSourceSeries, SourceSeriesID: seriesID, SeriesResultRevisionID: resultID,
				ResultLabel: swissusecase.SeriesResultPlayed, ParticipantID: second, OpponentID: &first, Points: secondPoints, StableSeed: 1 + indexUUID(participants, second)},
		)
	}
	first, second, third, fourth := participants[0], participants[1], participants[2], participants[3]
	// The target Series starts with the opposite winner. The correction plan
	// changes it to first, so the authority cannot accidentally reuse a
	// client-supplied final layout.
	appendResult(1, series.ID, first, second, second)
	appendResult(1, task057ID(720), third, fourth, third)
	switch shape {
	case task057SwissImpactingTie:
		appendResult(2, task057ID(721), first, third, first)
		appendResult(2, task057ID(722), second, fourth, second)
		appendResult(3, task057ID(723), fourth, first, fourth)
		appendResult(3, task057ID(724), second, third, second)
	case task057SwissNoImpactingTie:
		appendResult(2, task057ID(725), first, third, first)
		appendResult(2, task057ID(726), second, fourth, second)
		appendResult(3, task057ID(727), first, fourth, first)
		appendResult(3, task057ID(728), second, third, second)
	default:
		t.Fatalf("unknown Swiss shape %d", shape)
	}
	canonical := make([]resultprojection.CanonicalSwissParticipant, len(participants))
	for index, participantID := range participants {
		canonical[index] = resultprojection.CanonicalSwissParticipant{ID: participantID, StableSeed: index + 1}
	}
	return correctionusecase.StageSwissAuthority{Participants: canonical, Ledger: ledger, Complete: true}
}

func indexUUID(values []uuid.UUID, target uuid.UUID) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}
