package arena

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestSeriesScoreRevision(t *testing.T) {
	t.Run("exposes a rehydratable head and complete row version CAS", func(t *testing.T) {
		fixture := task053InitialScoreFixture(t)
		fixture.authority.SeriesRevision = ArenaSeriesRowRevision(5)
		fixture.authority.AttemptRevision = 0

		plan, err := PlanInitialSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.NoError(t, plan.Validate())

		revision := plan.Revision()
		head := revision.Head()
		require.NoError(t, head.Validate())
		require.Equal(t, fixture.command.Scope, head.Scope)
		require.Equal(t, fixture.command.RevisionID, head.ID)
		require.Equal(t, domain.ArenaSeriesScore{}, head.Score)
		require.Empty(t, head.Attempts)

		condition := plan.Condition()
		require.Equal(t, ArenaSeriesRowRevision(5), condition.ExpectedSeriesRevision())
		require.Equal(t, ArenaAttemptRowRevision(0), condition.ExpectedAttemptRevision())
		require.Nil(t, condition.ExpectedCurrentRevisionID())
		require.Equal(t, 0, condition.ExpectedCurrentOrdinal())
	})

	t.Run("plans the initial empty score for planned to locked projection", func(t *testing.T) {
		fixture := task053InitialScoreFixture(t)

		plan, err := PlanInitialSeriesScoreRevision(
			fixture.command,
			fixture.authority,
			fixture.recordedAt,
		)
		require.NoError(t, err)
		require.NoError(t, plan.Condition().Validate())
		require.NoError(t, plan.Revision().Validate())

		require.Equal(t, fixture.command.Scope, plan.Condition().Scope())
		require.Equal(t, fixture.authority.PersistedSeries, plan.Condition().ExpectedSeries())
		require.Nil(t, plan.Condition().ExpectedCurrentHead())
		require.Equal(t, fixture.authority.SourceProjection, plan.Condition().ExpectedSourceProjection())
		require.Equal(t, fixture.command.RevisionID, plan.Revision().ID())
		require.Nil(t, plan.Revision().PreviousRevisionID())
		require.Equal(t, 1, plan.Revision().Ordinal())
		require.Equal(t, SeriesScoreRevisionOperationInitialize, plan.Revision().Operation())
		require.Equal(t, domain.ArenaSeriesScore{}, plan.Revision().Score())
		require.Empty(t, plan.Revision().Attempts())
		require.Nil(t, plan.Revision().CommandAttempt())
	})

	t.Run("rejects anything except one initial plan for planned to locked projection", func(t *testing.T) {
		fixture := task053InitialScoreFixture(t)
		stale := domain.ArenaSeriesScoreRevisionID(task053PlannerID(110))

		tests := []struct {
			name   string
			mutate func(*InitialSeriesScoreRevisionCommand, *SeriesScoreRevisionAuthority)
		}{
			{
				name: "persisted already locked",
				mutate: func(_ *InitialSeriesScoreRevisionCommand, authority *SeriesScoreRevisionAuthority) {
					authority.PersistedSeries.State = domain.ArenaSeriesStateLocked
				},
			},
			{
				name: "projection not locked",
				mutate: func(_ *InitialSeriesScoreRevisionCommand, authority *SeriesScoreRevisionAuthority) {
					authority.ProjectedSeries.State = domain.ArenaSeriesStateDraft
				},
			},
			{
				name: "existing current revision",
				mutate: func(_ *InitialSeriesScoreRevisionCommand, authority *SeriesScoreRevisionAuthority) {
					authority.PersistedSeries.CurrentScoreRevisionID = &stale
				},
			},
			{
				name: "nonzero projected score",
				mutate: func(_ *InitialSeriesScoreRevisionCommand, authority *SeriesScoreRevisionAuthority) {
					authority.ProjectedSeries.Score.FirstParticipantWins = 1
				},
			},
			{
				name: "wrong projected head",
				mutate: func(_ *InitialSeriesScoreRevisionCommand, authority *SeriesScoreRevisionAuthority) {
					authority.ProjectedSeries.CurrentScoreRevisionID = &stale
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := cloneInitialScorePlannerCommand(fixture.command)
				authority := cloneScorePlannerAuthority(fixture.authority)
				test.mutate(&command, &authority)
				_, err := PlanInitialSeriesScoreRevision(command, authority, fixture.recordedAt)
				require.Error(t, err)
			})
		}
	})

	t.Run("appends exactly one completed terminal attempt and increments its winner", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)

		plan, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.NoError(t, plan.Revision().Validate())
		require.Equal(t, 2, plan.Revision().Ordinal())
		require.Equal(t, fixture.current.ID, *plan.Revision().PreviousRevisionID())
		require.Equal(t, SeriesScoreRevisionOperationAppendAttempt, plan.Revision().Operation())
		require.Equal(t, domain.ArenaSeriesScore{FirstParticipantWins: 1}, plan.Revision().Score())
		require.Equal(t, []SeriesScoreAttemptReference{*fixture.command.Attempt}, plan.Revision().Attempts())
		require.Equal(t, fixture.command.Attempt, plan.Revision().CommandAttempt())
		require.Equal(t, fixture.current, *plan.Condition().ExpectedCurrentHead())
		require.Equal(t, seriesScoreRevisionIDPointer(fixture.current.ID), plan.Condition().ExpectedCurrentRevisionID())
		require.Equal(t, fixture.current.Ordinal, plan.Condition().ExpectedCurrentOrdinal())
	})

	t.Run("requires a direct successor source and operator replacement", func(t *testing.T) {
		t.Run("reused command ID", func(t *testing.T) {
			fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
			fixture.command.CommandID = fixture.current.CommandID

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
		})

		t.Run("revision two cycle", func(t *testing.T) {
			fixture := task053ReplaceScoreFixture(t)
			previous := *fixture.current.PreviousRevisionID
			fixture.command.RevisionID = previous
			fixture.authority.ProjectedSeries.CurrentScoreRevisionID = &previous

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
		})

		t.Run("source two cycle", func(t *testing.T) {
			fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
			priorSourceID := domain.ArenaDerivedRevisionID(task053PlannerID(181))
			currentSource := task053Projection(t, task053PlannerID(182), fixture.command.Scope.TournamentID,
				domain.ArenaArtifactKindSeriesScore, fixture.command.Scope.SeriesID, 2, &priorSourceID, "score-cycle-current")
			currentSourceID := currentSource.ID()
			cycledSource := task053Projection(t, priorSourceID.UUID(), fixture.command.Scope.TournamentID,
				domain.ArenaArtifactKindSeriesScore, fixture.command.Scope.SeriesID, 3, &currentSourceID, "score-cycle-next")
			fixture.authority.CurrentHead.SourceProjection = currentSource
			fixture.authority.SourceProjection = cycledSource
			fixture.command.ExpectedSourceProjection = cycledSource

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrSeriesScoreRevisionConflict)
		})

		t.Run("repeated append source", func(t *testing.T) {
			fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
			fixture.authority.SourceProjection = fixture.current.SourceProjection
			fixture.command.ExpectedSourceProjection = fixture.current.SourceProjection

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrSeriesScoreRevisionConflict)
		})

		t.Run("server replacement", func(t *testing.T) {
			fixture := task053ReplaceScoreFixture(t)
			fixture.command.Actor = ArenaResultActor{Kind: ArenaResultActorServer}

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
		})

		t.Run("equal timestamp", func(t *testing.T) {
			fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
			_, err := PlanSeriesScoreRevision(
				fixture.command,
				fixture.authority,
				fixture.current.RecordedAt,
			)
			require.NoError(t, err)
		})
	})

	t.Run("keeps non scoring terminal attempts at zero", func(t *testing.T) {
		tests := []struct {
			name   string
			state  domain.ArenaGameState
			reason domain.ArenaGameResultReason
		}{
			{name: "void", state: domain.ArenaGameStateVoid, reason: domain.ArenaGameResultReasonTaskFailure},
			{name: "cancelled", state: domain.ArenaGameStateCancelled, reason: domain.ArenaGameResultReasonSeriesCancelled},
			{name: "superseded", state: domain.ArenaGameStateSuperseded, reason: domain.ArenaGameResultReasonDerivedRevisionSuperseded},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				fixture := task053AppendScoreFixture(t, test.state)
				fixture.command.Attempt.Reason = test.reason
				game := &fixture.authority.ProjectedSeries.Slots[0].Attempts[0]
				game.ResultReason = test.reason

				plan, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
				require.NoError(t, err)
				require.Equal(t, domain.ArenaSeriesScore{}, plan.Revision().Score())
			})
		}
	})

	t.Run("keeps canonical slot and attempt order across appended terminal refs", func(t *testing.T) {
		firstAppend := task053AppendScoreFixture(t, domain.ArenaGameStateVoid)
		secondGameID := task053PlannerID(170)
		thirdGameID := task053PlannerID(175)
		secondSlotID := task053PlannerID(178)
		voidPlan, err := PlanSeriesScoreRevision(
			firstAppend.command,
			firstAppend.authority,
			firstAppend.recordedAt,
		)
		require.NoError(t, err)

		persisted := cloneArenaSeries(firstAppend.authority.ProjectedSeries)
		persisted.CurrentScoreRevisionID = seriesScoreRevisionIDPointer(voidPlan.Revision().ID())
		persisted.Slots[0].Attempts = append(persisted.Slots[0].Attempts, domain.ArenaGame{
			ID:        secondGameID,
			SlotID:    persisted.Slots[0].ID,
			AttemptNo: 2,
			State:     domain.ArenaGameStateActive,
		})
		projected := cloneArenaSeries(persisted)
		secondResultID := domain.ArenaOfficialResultRevisionID(task053PlannerID(171))
		secondScoreID := domain.ArenaSeriesScoreRevisionID(task053PlannerID(172))
		projected.CurrentScoreRevisionID = &secondScoreID
		projected.Score.FirstParticipantWins = 1
		secondGame := &projected.Slots[0].Attempts[1]
		secondGame.State = domain.ArenaGameStateCompleted
		secondGame.ResultReason = domain.ArenaGameResultReasonSolved
		secondGame.WinnerID = task053UUIDPointer(projected.FirstParticipantID)
		secondGame.ResultRevisionID = officialResultRevisionIDPointer(secondResultID)
		secondAttempt := SeriesScoreAttemptReference{
			SlotID:                      projected.Slots[0].ID,
			SlotPosition:                1,
			GameID:                      secondGameID,
			AttemptNo:                   2,
			State:                       domain.ArenaGameStateCompleted,
			WinnerID:                    task053UUIDPointer(projected.FirstParticipantID),
			Reason:                      domain.ArenaGameResultReasonSolved,
			CurrentGameResultRevisionID: secondResultID,
		}
		previousSourceID := voidPlan.Revision().SourceProjection().ID()
		secondSource := task053Projection(t, task053PlannerID(173), projected.TournamentID,
			domain.ArenaArtifactKindSeriesScore, projected.ID, 3, &previousSourceID, "score-second")
		secondCommand := SeriesScoreRevisionCommand{
			Scope:                     firstAppend.command.Scope,
			Operation:                 SeriesScoreRevisionOperationAppendAttempt,
			CommandID:                 task053PlannerID(174),
			RevisionID:                secondScoreID,
			Actor:                     ArenaResultActor{Kind: ArenaResultActorServer},
			ExpectedCurrentRevisionID: seriesScoreRevisionIDPointer(voidPlan.Revision().ID()),
			ExpectedSourceProjection:  secondSource,
			Attempt:                   &secondAttempt,
		}
		secondPlan, err := PlanSeriesScoreRevision(
			secondCommand,
			SeriesScoreRevisionAuthority{
				Scope:            secondCommand.Scope,
				PersistedSeries:  persisted,
				ProjectedSeries:  projected,
				SourceProjection: secondSource,
				CurrentHead:      task053ScoreHeadPointer(voidPlan.Revision().Head()),
				SeriesRevision:   3,
				AttemptRevision:  2,
			},
			firstAppend.recordedAt.Add(time.Minute),
		)
		require.NoError(t, err)

		persisted = cloneArenaSeries(projected)
		persisted.Slots = append(persisted.Slots, domain.ArenaGameSlot{
			ID:          secondSlotID,
			SeriesID:    persisted.ID,
			Position:    2,
			Category:    domain.CategoryCrypto,
			ScoreBefore: domain.ArenaSeriesScore{FirstParticipantWins: 1},
			Attempts: []domain.ArenaGame{{
				ID:        thirdGameID,
				SlotID:    secondSlotID,
				AttemptNo: 1,
				State:     domain.ArenaGameStateActive,
			}},
		})
		thirdProjected := cloneArenaSeries(persisted)
		thirdResultID := domain.ArenaOfficialResultRevisionID(task053PlannerID(176))
		thirdScoreID := domain.ArenaSeriesScoreRevisionID(task053PlannerID(177))
		thirdProjected.CurrentScoreRevisionID = &thirdScoreID
		thirdProjected.Score.SecondParticipantWins = 1
		thirdGame := &thirdProjected.Slots[1].Attempts[0]
		thirdGame.State = domain.ArenaGameStateCompleted
		thirdGame.ResultReason = domain.ArenaGameResultReasonSolved
		thirdGame.WinnerID = task053UUIDPointer(thirdProjected.SecondParticipantID)
		thirdGame.ResultRevisionID = officialResultRevisionIDPointer(thirdResultID)
		thirdAttempt := SeriesScoreAttemptReference{
			SlotID:                      secondSlotID,
			SlotPosition:                2,
			GameID:                      thirdGameID,
			AttemptNo:                   1,
			State:                       domain.ArenaGameStateCompleted,
			WinnerID:                    task053UUIDPointer(thirdProjected.SecondParticipantID),
			Reason:                      domain.ArenaGameResultReasonSolved,
			CurrentGameResultRevisionID: thirdResultID,
		}
		previousSourceID = secondPlan.Revision().SourceProjection().ID()
		thirdSource := task053Projection(t, task053PlannerID(179), thirdProjected.TournamentID,
			domain.ArenaArtifactKindSeriesScore, thirdProjected.ID, 4, &previousSourceID, "score-third")
		thirdCommand := SeriesScoreRevisionCommand{
			Scope:                     firstAppend.command.Scope,
			Operation:                 SeriesScoreRevisionOperationAppendAttempt,
			CommandID:                 task053PlannerID(180),
			RevisionID:                thirdScoreID,
			Actor:                     ArenaResultActor{Kind: ArenaResultActorServer},
			ExpectedCurrentRevisionID: seriesScoreRevisionIDPointer(secondPlan.Revision().ID()),
			ExpectedSourceProjection:  thirdSource,
			Attempt:                   &thirdAttempt,
		}
		thirdPlan, err := PlanSeriesScoreRevision(
			thirdCommand,
			SeriesScoreRevisionAuthority{
				Scope:            thirdCommand.Scope,
				PersistedSeries:  persisted,
				ProjectedSeries:  thirdProjected,
				SourceProjection: thirdSource,
				CurrentHead:      task053ScoreHeadPointer(secondPlan.Revision().Head()),
				SeriesRevision:   4,
				AttemptRevision:  3,
			},
			firstAppend.recordedAt.Add(2*time.Minute),
		)
		require.NoError(t, err)
		require.Equal(t, []int{1, 1, 2}, []int{
			thirdPlan.Revision().Attempts()[0].SlotPosition,
			thirdPlan.Revision().Attempts()[1].SlotPosition,
			thirdPlan.Revision().Attempts()[2].SlotPosition,
		})
		require.Equal(t, []int{1, 2, 1}, []int{
			thirdPlan.Revision().Attempts()[0].AttemptNo,
			thirdPlan.Revision().Attempts()[1].AttemptNo,
			thirdPlan.Revision().Attempts()[2].AttemptNo,
		})
		require.Equal(t, domain.ArenaSeriesScore{
			FirstParticipantWins:  1,
			SecondParticipantWins: 1,
		}, thirdPlan.Revision().Score())
	})

	t.Run("replaces one result at the same stable attempt position", func(t *testing.T) {
		fixture := task053ReplaceScoreFixture(t)

		plan, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.Equal(t, 3, plan.Revision().Ordinal())
		require.Equal(t, SeriesScoreRevisionOperationReplaceResult, plan.Revision().Operation())
		require.Len(t, plan.Revision().Attempts(), 1)
		require.Equal(t, *fixture.command.Attempt, plan.Revision().Attempts()[0])
		require.Equal(t, domain.ArenaSeriesScore{}, plan.Revision().Score())
	})

	t.Run("rejects append and replacement shape drift", func(t *testing.T) {
		appendFixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
		replaceFixture := task053ReplaceScoreFixture(t)

		t.Run("append with two new attempts", func(t *testing.T) {
			command := cloneScorePlannerCommand(appendFixture.command)
			authority := cloneScorePlannerAuthority(appendFixture.authority)
			second := authority.ProjectedSeries.Slots[0].Attempts[0]
			second.ID = task053PlannerID(120)
			second.AttemptNo = 2
			second.State = domain.ArenaGameStateCompleted
			second.ResultReason = domain.ArenaGameResultReasonSolved
			second.ResultRevisionID = officialResultRevisionIDPointer(
				domain.ArenaOfficialResultRevisionID(task053PlannerID(121)),
			)
			authority.ProjectedSeries.Slots[0].Attempts[0].State = domain.ArenaGameStateVoid
			authority.ProjectedSeries.Slots[0].Attempts[0].ResultReason = domain.ArenaGameResultReasonTaskFailure
			authority.ProjectedSeries.Slots[0].Attempts[0].WinnerID = nil
			authority.ProjectedSeries.Slots[0].Attempts = append(
				authority.ProjectedSeries.Slots[0].Attempts,
				second,
			)

			_, err := PlanSeriesScoreRevision(command, authority, appendFixture.recordedAt)
			require.Error(t, err)
		})

		t.Run("replacement at another position", func(t *testing.T) {
			command := cloneScorePlannerCommand(replaceFixture.command)
			command.Attempt.AttemptNo = 2

			_, err := PlanSeriesScoreRevision(command, replaceFixture.authority, replaceFixture.recordedAt)
			require.Error(t, err)
		})

		t.Run("replacement with unchanged result revision", func(t *testing.T) {
			command := cloneScorePlannerCommand(replaceFixture.command)
			command.Attempt.CurrentGameResultRevisionID = replaceFixture.current.Attempts[0].CurrentGameResultRevisionID

			_, err := PlanSeriesScoreRevision(command, replaceFixture.authority, replaceFixture.recordedAt)
			require.Error(t, err)
		})

		t.Run("new target Game", func(t *testing.T) {
			command := cloneScorePlannerCommand(appendFixture.command)
			authority := cloneScorePlannerAuthority(appendFixture.authority)
			newGame := cloneArenaGame(authority.ProjectedSeries.Slots[0].Attempts[0])
			newGame.ID = task053PlannerID(122)
			newGame.AttemptNo = 2
			newGame.State = domain.ArenaGameStateVoid
			newGame.ResultReason = domain.ArenaGameResultReasonTaskFailure
			newGame.WinnerID = nil
			newResultID := domain.ArenaOfficialResultRevisionID(task053PlannerID(123))
			newGame.ResultRevisionID = &newResultID
			authority.ProjectedSeries.Slots[0].Attempts[0].State = domain.ArenaGameStateVoid
			authority.ProjectedSeries.Slots[0].Attempts[0].ResultReason = domain.ArenaGameResultReasonTaskFailure
			authority.ProjectedSeries.Slots[0].Attempts[0].WinnerID = nil
			authority.ProjectedSeries.Slots[0].Attempts = append(
				authority.ProjectedSeries.Slots[0].Attempts,
				newGame,
			)
			command.Attempt.GameID = newGame.ID
			command.Attempt.AttemptNo = 2
			command.Attempt.State = newGame.State
			command.Attempt.WinnerID = nil
			command.Attempt.Reason = newGame.ResultReason
			command.Attempt.CurrentGameResultRevisionID = newResultID

			_, err := PlanSeriesScoreRevision(command, authority, appendFixture.recordedAt)
			require.ErrorIs(t, err, ErrSeriesScoreRevisionConflict)
		})

		t.Run("slot score before mutation", func(t *testing.T) {
			authority := cloneScorePlannerAuthority(appendFixture.authority)
			authority.ProjectedSeries.Slots[0].ScoreBefore.FirstParticipantWins = 1

			_, err := PlanSeriesScoreRevision(appendFixture.command, authority, appendFixture.recordedAt)
			require.ErrorIs(t, err, ErrSeriesScoreRevisionConflict)
		})

		t.Run("unrelated Game evidence", func(t *testing.T) {
			authority := cloneScorePlannerAuthority(appendFixture.authority)
			for _, series := range []*domain.ArenaSeries{&authority.PersistedSeries, &authority.ProjectedSeries} {
				series.Slots = append(series.Slots, domain.ArenaGameSlot{
					ID: task053PlannerID(124), SeriesID: series.ID, Position: 2,
					Category: domain.CategoryCrypto, Attempts: []domain.ArenaGame{{
						ID: task053PlannerID(125), SlotID: task053PlannerID(124), AttemptNo: 1,
						State: domain.ArenaGameStateActive,
					}},
				})
			}
			authority.ProjectedSeries.Slots[1].Attempts[0].State = domain.ArenaGameStateReady

			_, err := PlanSeriesScoreRevision(appendFixture.command, authority, appendFixture.recordedAt)
			require.ErrorIs(t, err, ErrSeriesScoreRevisionConflict)
		})
	})

	t.Run("rejects stale head source and settled current payload", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
		stale := domain.ArenaSeriesScoreRevisionID(task053PlannerID(130))
		fork := task053Projection(t, task053PlannerID(131), fixture.command.Scope.TournamentID,
			domain.ArenaArtifactKindSeriesScore, fixture.command.Scope.SeriesID, 2,
			fixture.authority.SourceProjection.PreviousRevisionID(), "score-fork")
		currentSourceID := fixture.current.SourceProjection.ID()
		jump := task053Projection(t, task053PlannerID(132), fixture.command.Scope.TournamentID,
			domain.ArenaArtifactKindSeriesScore, fixture.command.Scope.SeriesID, 3,
			&currentSourceID, "score-jump")

		tests := []struct {
			name   string
			mutate func(*SeriesScoreRevisionCommand, *SeriesScoreRevisionAuthority)
		}{
			{
				name: "command head",
				mutate: func(command *SeriesScoreRevisionCommand, _ *SeriesScoreRevisionAuthority) {
					command.ExpectedCurrentRevisionID = &stale
				},
			},
			{
				name: "persisted head",
				mutate: func(_ *SeriesScoreRevisionCommand, authority *SeriesScoreRevisionAuthority) {
					authority.PersistedSeries.CurrentScoreRevisionID = &stale
				},
			},
			{
				name: "source fork",
				mutate: func(command *SeriesScoreRevisionCommand, _ *SeriesScoreRevisionAuthority) {
					command.ExpectedSourceProjection = fork
				},
			},
			{
				name: "source jump",
				mutate: func(command *SeriesScoreRevisionCommand, authority *SeriesScoreRevisionAuthority) {
					command.ExpectedSourceProjection = jump
					authority.SourceProjection = jump
				},
			},
			{
				name: "settled score drift",
				mutate: func(_ *SeriesScoreRevisionCommand, authority *SeriesScoreRevisionAuthority) {
					authority.PersistedSeries.Score.FirstParticipantWins = 1
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := cloneScorePlannerCommand(fixture.command)
				authority := cloneScorePlannerAuthority(fixture.authority)
				test.mutate(&command, &authority)
				_, err := PlanSeriesScoreRevision(command, authority, fixture.recordedAt)
				require.ErrorIs(t, err, ErrSeriesScoreRevisionConflict)
			})
		}
	})

	t.Run("preserves row revisions that snapshots cannot reveal", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
		first, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)

		advanced := cloneScorePlannerAuthority(fixture.authority)
		advanced.SeriesRevision++
		advanced.AttemptRevision++
		second, err := PlanSeriesScoreRevision(fixture.command, advanced, fixture.recordedAt)
		require.NoError(t, err)
		require.NotEqual(t, first.Condition().ExpectedSeriesRevision(), second.Condition().ExpectedSeriesRevision())
		require.NotEqual(t, first.Condition().ExpectedAttemptRevision(), second.Condition().ExpectedAttemptRevision())

		for _, mutate := range []func(*SeriesScoreRevisionAuthority){
			func(authority *SeriesScoreRevisionAuthority) { authority.SeriesRevision = 0 },
			func(authority *SeriesScoreRevisionAuthority) { authority.AttemptRevision = 0 },
		} {
			authority := cloneScorePlannerAuthority(fixture.authority)
			mutate(&authority)
			_, err := PlanSeriesScoreRevision(fixture.command, authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
		}

		maxAuthority := cloneScorePlannerAuthority(fixture.authority)
		maxAuthority.SeriesRevision = ArenaSeriesRowRevision(math.MaxInt64)
		maxAuthority.AttemptRevision = ArenaAttemptRowRevision(math.MaxInt64)
		_, err = PlanSeriesScoreRevision(fixture.command, maxAuthority, fixture.recordedAt)
		require.NoError(t, err)

		negative := int64(-1)
		for _, mutate := range []func(*SeriesScoreRevisionAuthority){
			func(authority *SeriesScoreRevisionAuthority) {
				authority.SeriesRevision = ArenaSeriesRowRevision(negative)
			},
			func(authority *SeriesScoreRevisionAuthority) {
				authority.AttemptRevision = ArenaAttemptRowRevision(negative)
			},
		} {
			authority := cloneScorePlannerAuthority(fixture.authority)
			mutate(&authority)
			_, err := PlanSeriesScoreRevision(fixture.command, authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
		}
		initial := task053InitialScoreFixture(t)
		initial.authority.AttemptRevision = ArenaAttemptRowRevision(negative)
		_, err = PlanInitialSeriesScoreRevision(initial.command, initial.authority, initial.recordedAt)
		require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
	})

	t.Run("rejects terminal and live duplicate Game identities", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
		duplicateSlotID := task053PlannerID(133)
		for _, series := range []*domain.ArenaSeries{&fixture.authority.PersistedSeries, &fixture.authority.ProjectedSeries} {
			duplicate := domain.ArenaGame{
				ID: fixture.command.Attempt.GameID, SlotID: duplicateSlotID, AttemptNo: 1,
				State: domain.ArenaGameStateActive,
			}
			series.Slots = append(series.Slots, domain.ArenaGameSlot{
				ID: duplicateSlotID, SeriesID: series.ID, Position: 2,
				Category: domain.CategoryCrypto, Attempts: []domain.ArenaGame{duplicate},
			})
		}

		_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
	})

	t.Run("rejects local UUID cross-role aliases", func(t *testing.T) {
		t.Run("command equals revision", func(t *testing.T) {
			fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
			fixture.command.CommandID = fixture.command.RevisionID.UUID()

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
		})

		t.Run("source equals result reference", func(t *testing.T) {
			fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
			fixture.command.Attempt.CurrentGameResultRevisionID = domain.ArenaOfficialResultRevisionID(
				fixture.command.ExpectedSourceProjection.ID().UUID(),
			)

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
		})

		t.Run("actor equals source", func(t *testing.T) {
			fixture := task053ReplaceScoreFixture(t)
			principal := fixture.command.ExpectedSourceProjection.ID().UUID()
			fixture.command.Actor.PrincipalID = &principal

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
		})
	})

	t.Run("rejects a spliced plan", func(t *testing.T) {
		firstFixture := task053InitialScoreFixture(t)
		first, err := PlanInitialSeriesScoreRevision(
			firstFixture.command,
			firstFixture.authority,
			firstFixture.recordedAt,
		)
		require.NoError(t, err)
		secondFixture := task053InitialScoreFixture(t)
		secondFixture.command.CommandID = task053PlannerID(134)
		secondFixture.command.RevisionID = domain.ArenaSeriesScoreRevisionID(task053PlannerID(135))
		secondFixture.authority.ProjectedSeries.CurrentScoreRevisionID = &secondFixture.command.RevisionID
		second, err := PlanInitialSeriesScoreRevision(
			secondFixture.command,
			secondFixture.authority,
			secondFixture.recordedAt,
		)
		require.NoError(t, err)

		spliced := SeriesScoreRevisionPlan{condition: first.condition, revision: second.revision}
		require.ErrorIs(t, spliced.Validate(), ErrInvalidSeriesScoreRevision)
	})

	t.Run("fails closed on successor ordinal overflow", func(t *testing.T) {
		fixture := task053ReplaceScoreFixture(t)
		fixture.authority.CurrentHead.Ordinal = int(^uint(0) >> 1)

		_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
	})

	t.Run("returns detached immutable values", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
		plan, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)

		*fixture.command.Attempt.WinnerID = task053PlannerID(140)
		fixture.authority.PersistedSeries.State = domain.ArenaSeriesStateDraft
		fixture.authority.ProjectedSeries.Slots[0].Attempts[0].WinnerID = nil
		require.Equal(t, fixture.firstParticipantID, *plan.Revision().Attempts()[0].WinnerID)
		require.Equal(t, domain.ArenaSeriesStateLocked, plan.Condition().ExpectedSeries().State)

		attempts := plan.Revision().Attempts()
		*attempts[0].WinnerID = task053PlannerID(141)
		commandAttempt := plan.Revision().CommandAttempt()
		*commandAttempt.WinnerID = task053PlannerID(142)
		conditionSeries := plan.Condition().ExpectedSeries()
		conditionSeries.State = domain.ArenaSeriesStateActive
		require.Equal(t, domain.ArenaSeriesStateActive, conditionSeries.State)
		head := plan.Revision().Head()
		*head.Attempts[0].WinnerID = task053PlannerID(143)
		require.Equal(t, fixture.firstParticipantID, *plan.Revision().Attempts()[0].WinnerID)
		require.Equal(t, fixture.firstParticipantID, *plan.Revision().CommandAttempt().WinnerID)
		require.Equal(t, domain.ArenaSeriesStateLocked, plan.Condition().ExpectedSeries().State)
	})

	t.Run("is deterministic under concurrent pure calls", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
		baseline, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)

		const callers = 32
		plans := make(chan SeriesScoreRevisionPlan, callers)
		errs := make(chan error, callers)
		var group sync.WaitGroup
		for range callers {
			group.Add(1)
			go func() {
				defer group.Done()
				plan, planErr := PlanSeriesScoreRevision(
					fixture.command,
					fixture.authority,
					fixture.recordedAt,
				)
				plans <- plan
				errs <- planErr
			}()
		}
		group.Wait()
		close(plans)
		close(errs)

		for planErr := range errs {
			require.NoError(t, planErr)
		}
		for plan := range plans {
			require.Equal(t, baseline, plan)
		}
	})
}

type task053InitialScoreCase struct {
	command             InitialSeriesScoreRevisionCommand
	authority           SeriesScoreRevisionAuthority
	recordedAt          time.Time
	firstParticipantID  uuid.UUID
	secondParticipantID uuid.UUID
}

type task053ScoreFixture struct {
	command            SeriesScoreRevisionCommand
	authority          SeriesScoreRevisionAuthority
	current            SeriesScoreRevisionHead
	recordedAt         time.Time
	firstParticipantID uuid.UUID
}

func task053InitialScoreFixture(t *testing.T) task053InitialScoreCase {
	t.Helper()
	tournamentID := task053PlannerID(101)
	seriesID := task053PlannerID(102)
	firstID := task053PlannerID(103)
	secondID := task053PlannerID(104)
	revisionID := domain.ArenaSeriesScoreRevisionID(task053PlannerID(105))
	scope := SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID}
	persisted := domain.ArenaSeries{
		ID:                  seriesID,
		TournamentID:        tournamentID,
		FirstParticipantID:  firstID,
		SecondParticipantID: secondID,
		Format:              domain.ArenaSeriesFormatBO3,
		State:               domain.ArenaSeriesStatePlanned,
		Score:               domain.ArenaSeriesScore{},
		Slots: []domain.ArenaGameSlot{{
			ID:          task053PlannerID(150),
			SeriesID:    seriesID,
			Position:    1,
			Category:    domain.CategoryWeb,
			ScoreBefore: domain.ArenaSeriesScore{},
			Attempts: []domain.ArenaGame{{
				ID:        task053PlannerID(151),
				SlotID:    task053PlannerID(150),
				AttemptNo: 1,
				State:     domain.ArenaGameStatePlanned,
			}},
		}},
	}
	projected := cloneArenaSeries(persisted)
	projected.State = domain.ArenaSeriesStateLocked
	projected.CurrentScoreRevisionID = seriesScoreRevisionIDPointer(revisionID)
	source := task053Projection(t, task053PlannerID(106), tournamentID,
		domain.ArenaArtifactKindSeriesScore, seriesID, 1, nil, "initial-score")
	return task053InitialScoreCase{
		command: InitialSeriesScoreRevisionCommand{
			Scope:                    scope,
			CommandID:                task053PlannerID(107),
			RevisionID:               revisionID,
			Actor:                    ArenaResultActor{Kind: ArenaResultActorServer},
			ExpectedSourceProjection: source,
		},
		authority: SeriesScoreRevisionAuthority{
			Scope:            scope,
			PersistedSeries:  persisted,
			ProjectedSeries:  projected,
			SourceProjection: source,
			SeriesRevision:   1,
		},
		recordedAt:          time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		firstParticipantID:  firstID,
		secondParticipantID: secondID,
	}
}

func task053AppendScoreFixture(t *testing.T, state domain.ArenaGameState) task053ScoreFixture {
	t.Helper()
	initial := task053InitialScoreFixture(t)
	firstPlan, err := PlanInitialSeriesScoreRevision(initial.command, initial.authority, initial.recordedAt)
	require.NoError(t, err)
	current := firstPlan.Revision().Head()
	persisted := cloneArenaSeries(initial.authority.ProjectedSeries)
	projected := cloneArenaSeries(persisted)
	slotID := projected.Slots[0].ID
	gameID := projected.Slots[0].Attempts[0].ID
	gameResultID := domain.ArenaOfficialResultRevisionID(task053PlannerID(152))
	projected.State = domain.ArenaSeriesStateActive
	projected.CurrentScoreRevisionID = seriesScoreRevisionIDPointer(
		domain.ArenaSeriesScoreRevisionID(task053PlannerID(153)),
	)
	game := &projected.Slots[0].Attempts[0]
	game.State = state
	game.ResultRevisionID = officialResultRevisionIDPointer(gameResultID)
	attempt := SeriesScoreAttemptReference{
		SlotID:                      slotID,
		SlotPosition:                1,
		GameID:                      gameID,
		AttemptNo:                   1,
		State:                       state,
		CurrentGameResultRevisionID: gameResultID,
	}
	if state == domain.ArenaGameStateCompleted {
		game.ResultReason = domain.ArenaGameResultReasonSolved
		game.WinnerID = task053UUIDPointer(initial.authority.PersistedSeries.FirstParticipantID)
		attempt.Reason = domain.ArenaGameResultReasonSolved
		attempt.WinnerID = task053UUIDPointer(initial.authority.PersistedSeries.FirstParticipantID)
		projected.Score.FirstParticipantWins = 1
	} else {
		game.ResultReason = domain.ArenaGameResultReasonTaskFailure
		attempt.Reason = domain.ArenaGameResultReasonTaskFailure
	}
	previousSourceID := current.SourceProjection.ID()
	source := task053Projection(t, task053PlannerID(154), projected.TournamentID,
		domain.ArenaArtifactKindSeriesScore, projected.ID, 2, &previousSourceID, "score-append")
	command := SeriesScoreRevisionCommand{
		Scope:                     initial.command.Scope,
		Operation:                 SeriesScoreRevisionOperationAppendAttempt,
		CommandID:                 task053PlannerID(155),
		RevisionID:                *projected.CurrentScoreRevisionID,
		Actor:                     ArenaResultActor{Kind: ArenaResultActorServer},
		ExpectedCurrentRevisionID: seriesScoreRevisionIDPointer(current.ID),
		ExpectedSourceProjection:  source,
		Attempt:                   &attempt,
	}
	return task053ScoreFixture{
		command: command,
		authority: SeriesScoreRevisionAuthority{
			Scope:            command.Scope,
			PersistedSeries:  persisted,
			ProjectedSeries:  projected,
			SourceProjection: source,
			CurrentHead:      &current,
			SeriesRevision:   2,
			AttemptRevision:  1,
		},
		current:            current,
		recordedAt:         initial.recordedAt.Add(time.Minute),
		firstParticipantID: initial.firstParticipantID,
	}
}

func task053ReplaceScoreFixture(t *testing.T) task053ScoreFixture {
	t.Helper()
	appendFixture := task053AppendScoreFixture(t, domain.ArenaGameStateCompleted)
	secondPlan, err := PlanSeriesScoreRevision(
		appendFixture.command,
		appendFixture.authority,
		appendFixture.recordedAt,
	)
	require.NoError(t, err)
	current := secondPlan.Revision().Head()
	persisted := cloneArenaSeries(appendFixture.authority.ProjectedSeries)
	projected := cloneArenaSeries(persisted)
	revisionID := domain.ArenaSeriesScoreRevisionID(task053PlannerID(160))
	gameResultID := domain.ArenaOfficialResultRevisionID(task053PlannerID(161))
	projected.CurrentScoreRevisionID = &revisionID
	projected.Score = domain.ArenaSeriesScore{}
	game := &projected.Slots[0].Attempts[0]
	game.State = domain.ArenaGameStateVoid
	game.ResultReason = domain.ArenaGameResultReasonTaskFailure
	game.WinnerID = nil
	game.ResultRevisionID = officialResultRevisionIDPointer(gameResultID)
	attempt := SeriesScoreAttemptReference{
		SlotID:                      game.SlotID,
		SlotPosition:                1,
		GameID:                      game.ID,
		AttemptNo:                   game.AttemptNo,
		State:                       game.State,
		Reason:                      game.ResultReason,
		CurrentGameResultRevisionID: gameResultID,
	}
	previousSourceID := current.SourceProjection.ID()
	source := task053Projection(t, task053PlannerID(162), projected.TournamentID,
		domain.ArenaArtifactKindSeriesScore, projected.ID, 3, &previousSourceID, "score-replace")
	command := SeriesScoreRevisionCommand{
		Scope:                     appendFixture.command.Scope,
		Operation:                 SeriesScoreRevisionOperationReplaceResult,
		CommandID:                 task053PlannerID(163),
		RevisionID:                revisionID,
		Actor:                     ArenaResultActor{Kind: ArenaResultActorOperator, PrincipalID: task053UUIDPointer(task053PlannerID(164))},
		ExpectedCurrentRevisionID: seriesScoreRevisionIDPointer(current.ID),
		ExpectedSourceProjection:  source,
		Attempt:                   &attempt,
	}
	return task053ScoreFixture{
		command: command,
		authority: SeriesScoreRevisionAuthority{
			Scope:            command.Scope,
			PersistedSeries:  persisted,
			ProjectedSeries:  projected,
			SourceProjection: source,
			CurrentHead:      &current,
			SeriesRevision:   3,
			AttemptRevision:  2,
		},
		current:            current,
		recordedAt:         appendFixture.recordedAt.Add(time.Minute),
		firstParticipantID: appendFixture.firstParticipantID,
	}
}

func seriesScoreRevisionIDPointer(
	value domain.ArenaSeriesScoreRevisionID,
) *domain.ArenaSeriesScoreRevisionID {
	clone := value
	return &clone
}

func task053ScoreHeadPointer(value SeriesScoreRevisionHead) *SeriesScoreRevisionHead {
	clone := value.Clone()
	return &clone
}

func cloneInitialScorePlannerCommand(
	command InitialSeriesScoreRevisionCommand,
) InitialSeriesScoreRevisionCommand {
	clone := command
	clone.Actor = cloneArenaResultActor(command.Actor)
	return clone
}

func cloneScorePlannerCommand(command SeriesScoreRevisionCommand) SeriesScoreRevisionCommand {
	clone := command
	clone.Actor = cloneArenaResultActor(command.Actor)
	clone.ExpectedCurrentRevisionID = cloneSeriesScoreRevisionIDPointer(command.ExpectedCurrentRevisionID)
	clone.Attempt = cloneSeriesScoreAttemptReferencePointer(command.Attempt)
	return clone
}

func cloneScorePlannerAuthority(
	authority SeriesScoreRevisionAuthority,
) SeriesScoreRevisionAuthority {
	clone := authority
	clone.PersistedSeries = cloneArenaSeries(authority.PersistedSeries)
	clone.ProjectedSeries = cloneArenaSeries(authority.ProjectedSeries)
	if authority.CurrentHead != nil {
		current := authority.CurrentHead.Clone()
		clone.CurrentHead = &current
	}
	return clone
}
