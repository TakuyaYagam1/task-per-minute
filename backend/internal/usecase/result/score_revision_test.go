package result

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestSeriesScoreRevision(t *testing.T) {
	t.Run("appends exactly one completed terminal attempt and increments its winner", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)

		plan, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.NoError(t, plan.Revision().Validate())
		require.Equal(t, 2, plan.Revision().Ordinal())
		require.Equal(t, fixture.current.ID, *plan.Revision().PreviousRevisionID())
		require.Equal(t, SeriesScoreRevisionOperationAppendAttempt, plan.Revision().Operation())
		require.Equal(t, domain.SeriesScore{FirstParticipantWins: 1}, plan.Revision().Score())
		require.Equal(t, []SeriesScoreAttemptReference{*fixture.command.Attempt}, plan.Revision().Attempts())
		require.Equal(t, fixture.command.Attempt, plan.Revision().CommandAttempt())
		require.Equal(t, fixture.current, *plan.Condition().ExpectedCurrentHead())
		require.Equal(t, seriesScoreRevisionIDPointer(fixture.current.ID), plan.Condition().ExpectedCurrentRevisionID())
		require.Equal(t, fixture.current.Ordinal, plan.Condition().ExpectedCurrentOrdinal())
	})

	t.Run("requires a direct successor source and operator replacement", func(t *testing.T) {
		t.Run("reused command ID", func(t *testing.T) {
			fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
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
			fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
			priorSourceID := domain.DerivedRevisionID(task053PlannerID(181))
			currentSource := task053Projection(t, task053PlannerID(182), fixture.command.Scope.TournamentID,
				domain.ArtifactKindSeriesScore, fixture.command.Scope.SeriesID, 2, &priorSourceID, "score-cycle-current")
			currentSourceID := currentSource.ID()
			cycledSource := task053Projection(t, priorSourceID.UUID(), fixture.command.Scope.TournamentID,
				domain.ArtifactKindSeriesScore, fixture.command.Scope.SeriesID, 3, &currentSourceID, "score-cycle-next")
			fixture.authority.CurrentHead.SourceProjection = currentSource
			fixture.authority.SourceProjection = cycledSource
			fixture.command.ExpectedSourceProjection = cycledSource

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrSeriesScoreRevisionConflict)
		})

		t.Run("repeated append source", func(t *testing.T) {
			fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
			fixture.authority.SourceProjection = fixture.current.SourceProjection
			fixture.command.ExpectedSourceProjection = fixture.current.SourceProjection

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrSeriesScoreRevisionConflict)
		})

		t.Run("server replacement", func(t *testing.T) {
			fixture := task053ReplaceScoreFixture(t)
			fixture.command.Actor = domain.ResultActor{Kind: domain.ResultActorServer}

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
		})

		t.Run("equal timestamp", func(t *testing.T) {
			fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
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
			state  domain.GameState
			reason domain.GameResultReason
		}{
			{name: "void", state: domain.GameStateVoid, reason: domain.GameResultReasonTaskFailure},
			{name: "cancelled", state: domain.GameStateCancelled, reason: domain.GameResultReasonSeriesCancelled},
			{name: "superseded", state: domain.GameStateSuperseded, reason: domain.GameResultReasonDerivedRevisionSuperseded},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				fixture := task053AppendScoreFixture(t, test.state)
				fixture.command.Attempt.Reason = test.reason
				game := &fixture.authority.ProjectedSeries.Slots[0].Attempts[0]
				game.ResultReason = test.reason

				plan, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
				require.NoError(t, err)
				require.Equal(t, domain.SeriesScore{}, plan.Revision().Score())
			})
		}
	})

	t.Run("keeps canonical slot and attempt order across appended terminal refs", func(t *testing.T) {
		firstAppend := task053AppendScoreFixture(t, domain.GameStateVoid)
		secondGameID := task053PlannerID(170)
		thirdGameID := task053PlannerID(175)
		secondSlotID := task053PlannerID(178)
		voidPlan, err := PlanSeriesScoreRevision(
			firstAppend.command,
			firstAppend.authority,
			firstAppend.recordedAt,
		)
		require.NoError(t, err)

		persisted := cloneSeries(firstAppend.authority.ProjectedSeries)
		persisted.CurrentScoreRevisionID = seriesScoreRevisionIDPointer(voidPlan.Revision().ID())
		persisted.Slots[0].Attempts = append(persisted.Slots[0].Attempts, domain.Game{
			ID:        secondGameID,
			SlotID:    persisted.Slots[0].ID,
			AttemptNo: 2,
			State:     domain.GameStateActive,
		})
		projected := cloneSeries(persisted)
		secondResultID := domain.OfficialResultRevisionID(task053PlannerID(171))
		secondScoreID := domain.SeriesScoreRevisionID(task053PlannerID(172))
		projected.CurrentScoreRevisionID = &secondScoreID
		projected.Score.FirstParticipantWins = 1
		secondGame := &projected.Slots[0].Attempts[1]
		secondGame.State = domain.GameStateCompleted
		secondGame.ResultReason = domain.GameResultReasonSolved
		secondGame.WinnerID = task053UUIDPointer(projected.FirstParticipantID)
		secondGame.ResultRevisionID = officialResultRevisionIDPointer(secondResultID)
		secondAttempt := SeriesScoreAttemptReference{
			SlotID:                      projected.Slots[0].ID,
			SlotPosition:                1,
			GameID:                      secondGameID,
			AttemptNo:                   2,
			State:                       domain.GameStateCompleted,
			WinnerID:                    task053UUIDPointer(projected.FirstParticipantID),
			Reason:                      domain.GameResultReasonSolved,
			CurrentGameResultRevisionID: secondResultID,
		}
		previousSourceID := voidPlan.Revision().SourceProjection().ID()
		secondSource := task053Projection(t, task053PlannerID(173), projected.TournamentID,
			domain.ArtifactKindSeriesScore, projected.ID, 3, &previousSourceID, "score-second")
		secondCommand := SeriesScoreRevisionCommand{
			Scope:                     firstAppend.command.Scope,
			Operation:                 SeriesScoreRevisionOperationAppendAttempt,
			CommandID:                 task053PlannerID(174),
			RevisionID:                secondScoreID,
			Actor:                     domain.ResultActor{Kind: domain.ResultActorServer},
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

		persisted = cloneSeries(projected)
		persisted.Slots = append(persisted.Slots, domain.GameSlot{
			ID:          secondSlotID,
			SeriesID:    persisted.ID,
			Position:    2,
			Category:    domain.CategoryCrypto,
			ScoreBefore: domain.SeriesScore{FirstParticipantWins: 1},
			Attempts: []domain.Game{{
				ID:        thirdGameID,
				SlotID:    secondSlotID,
				AttemptNo: 1,
				State:     domain.GameStateActive,
			}},
		})
		thirdProjected := cloneSeries(persisted)
		thirdResultID := domain.OfficialResultRevisionID(task053PlannerID(176))
		thirdScoreID := domain.SeriesScoreRevisionID(task053PlannerID(177))
		thirdProjected.CurrentScoreRevisionID = &thirdScoreID
		thirdProjected.Score.SecondParticipantWins = 1
		thirdGame := &thirdProjected.Slots[1].Attempts[0]
		thirdGame.State = domain.GameStateCompleted
		thirdGame.ResultReason = domain.GameResultReasonSolved
		thirdGame.WinnerID = task053UUIDPointer(thirdProjected.SecondParticipantID)
		thirdGame.ResultRevisionID = officialResultRevisionIDPointer(thirdResultID)
		thirdAttempt := SeriesScoreAttemptReference{
			SlotID:                      secondSlotID,
			SlotPosition:                2,
			GameID:                      thirdGameID,
			AttemptNo:                   1,
			State:                       domain.GameStateCompleted,
			WinnerID:                    task053UUIDPointer(thirdProjected.SecondParticipantID),
			Reason:                      domain.GameResultReasonSolved,
			CurrentGameResultRevisionID: thirdResultID,
		}
		previousSourceID = secondPlan.Revision().SourceProjection().ID()
		thirdSource := task053Projection(t, task053PlannerID(179), thirdProjected.TournamentID,
			domain.ArtifactKindSeriesScore, thirdProjected.ID, 4, &previousSourceID, "score-third")
		thirdCommand := SeriesScoreRevisionCommand{
			Scope:                     firstAppend.command.Scope,
			Operation:                 SeriesScoreRevisionOperationAppendAttempt,
			CommandID:                 task053PlannerID(180),
			RevisionID:                thirdScoreID,
			Actor:                     domain.ResultActor{Kind: domain.ResultActorServer},
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
		require.Equal(t, domain.SeriesScore{
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
		require.Equal(t, domain.SeriesScore{}, plan.Revision().Score())
	})
}
