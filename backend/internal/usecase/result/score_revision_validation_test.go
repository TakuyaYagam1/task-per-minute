package result

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestSeriesScoreRevisionValidation(t *testing.T) {
	t.Run("accepts pre-start operator forfeit only with immutable adjudication evidence", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
		base, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)

		head := base.Revision().Head()
		previousID := head.ID
		head.ID = domain.SeriesScoreRevisionID(task053PlannerID(190))
		head.PreviousRevisionID = &previousID
		head.Ordinal++
		head.Operation = SeriesScoreRevisionOperationPreStartForfeit
		head.CommandID = task053PlannerID(191)
		head.Actor = domain.ResultActor{
			Kind:        domain.ResultActorOperator,
			PrincipalID: task053UUIDPointer(task053PlannerID(192)),
		}
		head.CommandAttempt = nil
		head.Attempts = nil
		head.Score = domain.SeriesScore{FirstParticipantWins: head.Format.WinsRequired()}
		head.TerminalEvidence = &SeriesScoreTerminalEvidence{
			Source:                  SeriesScoreTerminalSourcePreStartForfeit,
			CommitID:                task053PlannerID(193),
			ForfeitingParticipantID: head.SecondParticipantID,
			WinnerID:                head.FirstParticipantID,
			AnchorAttemptID:         task053PlannerID(194),
		}
		head.RecordedAt = head.RecordedAt.Add(time.Minute)

		require.NoError(t, head.Validate())
	})

	t.Run("rejects append and replacement shape drift", func(t *testing.T) {
		appendFixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
		replaceFixture := task053ReplaceScoreFixture(t)

		t.Run("append with two new attempts", func(t *testing.T) {
			command := cloneScorePlannerCommand(appendFixture.command)
			authority := cloneScorePlannerAuthority(appendFixture.authority)
			second := authority.ProjectedSeries.Slots[0].Attempts[0]
			second.ID = task053PlannerID(120)
			second.AttemptNo = 2
			second.State = domain.GameStateCompleted
			second.ResultReason = domain.GameResultReasonSolved
			second.ResultRevisionID = officialResultRevisionIDPointer(
				domain.OfficialResultRevisionID(task053PlannerID(121)),
			)
			authority.ProjectedSeries.Slots[0].Attempts[0].State = domain.GameStateVoid
			authority.ProjectedSeries.Slots[0].Attempts[0].ResultReason = domain.GameResultReasonTaskFailure
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
			newGame := cloneGame(authority.ProjectedSeries.Slots[0].Attempts[0])
			newGame.ID = task053PlannerID(122)
			newGame.AttemptNo = 2
			newGame.State = domain.GameStateVoid
			newGame.ResultReason = domain.GameResultReasonTaskFailure
			newGame.WinnerID = nil
			newResultID := domain.OfficialResultRevisionID(task053PlannerID(123))
			newGame.ResultRevisionID = &newResultID
			authority.ProjectedSeries.Slots[0].Attempts[0].State = domain.GameStateVoid
			authority.ProjectedSeries.Slots[0].Attempts[0].ResultReason = domain.GameResultReasonTaskFailure
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
			for _, series := range []*domain.Series{&authority.PersistedSeries, &authority.ProjectedSeries} {
				series.Slots = append(series.Slots, domain.GameSlot{
					ID: task053PlannerID(124), SeriesID: series.ID, Position: 2,
					Category: domain.CategoryCrypto, Attempts: []domain.Game{{
						ID: task053PlannerID(125), SlotID: task053PlannerID(124), AttemptNo: 1,
						State: domain.GameStateActive,
					}},
				})
			}
			authority.ProjectedSeries.Slots[1].Attempts[0].State = domain.GameStateReady

			_, err := PlanSeriesScoreRevision(appendFixture.command, authority, appendFixture.recordedAt)
			require.ErrorIs(t, err, ErrSeriesScoreRevisionConflict)
		})
	})

	t.Run("rejects stale head source and settled current payload", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
		stale := domain.SeriesScoreRevisionID(task053PlannerID(130))
		fork := task053Projection(t, task053PlannerID(131), fixture.command.Scope.TournamentID,
			domain.ArtifactKindSeriesScore, fixture.command.Scope.SeriesID, 2,
			fixture.authority.SourceProjection.PreviousRevisionID(), "score-fork")
		currentSourceID := fixture.current.SourceProjection.ID()
		jump := task053Projection(t, task053PlannerID(132), fixture.command.Scope.TournamentID,
			domain.ArtifactKindSeriesScore, fixture.command.Scope.SeriesID, 3,
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
		fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
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
		maxAuthority.SeriesRevision = SeriesRowRevision(math.MaxInt64)
		maxAuthority.AttemptRevision = AttemptRowRevision(math.MaxInt64)
		_, err = PlanSeriesScoreRevision(fixture.command, maxAuthority, fixture.recordedAt)
		require.NoError(t, err)

		negative := int64(-1)
		for _, mutate := range []func(*SeriesScoreRevisionAuthority){
			func(authority *SeriesScoreRevisionAuthority) {
				authority.SeriesRevision = SeriesRowRevision(negative)
			},
			func(authority *SeriesScoreRevisionAuthority) {
				authority.AttemptRevision = AttemptRowRevision(negative)
			},
		} {
			authority := cloneScorePlannerAuthority(fixture.authority)
			mutate(&authority)
			_, err := PlanSeriesScoreRevision(fixture.command, authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
		}
	})

	t.Run("rejects terminal and live duplicate Game identities", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
		duplicateSlotID := task053PlannerID(133)
		for _, series := range []*domain.Series{&fixture.authority.PersistedSeries, &fixture.authority.ProjectedSeries} {
			duplicate := domain.Game{
				ID: fixture.command.Attempt.GameID, SlotID: duplicateSlotID, AttemptNo: 1,
				State: domain.GameStateActive,
			}
			series.Slots = append(series.Slots, domain.GameSlot{
				ID: duplicateSlotID, SeriesID: series.ID, Position: 2,
				Category: domain.CategoryCrypto, Attempts: []domain.Game{duplicate},
			})
		}

		_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
	})

	t.Run("rejects local UUID cross-role aliases", func(t *testing.T) {
		t.Run("command equals revision", func(t *testing.T) {
			fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
			fixture.command.CommandID = fixture.command.RevisionID.UUID()

			_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
		})

		t.Run("source equals result reference", func(t *testing.T) {
			fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
			fixture.command.Attempt.CurrentGameResultRevisionID = domain.OfficialResultRevisionID(
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

	t.Run("fails closed on successor ordinal overflow", func(t *testing.T) {
		fixture := task053ReplaceScoreFixture(t)
		fixture.authority.CurrentHead.Ordinal = int(^uint(0) >> 1)

		_, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidSeriesScoreRevision)
	})

	t.Run("returns detached immutable values", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
		plan, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)

		*fixture.command.Attempt.WinnerID = task053PlannerID(140)
		fixture.authority.PersistedSeries.State = domain.SeriesStateDraft
		fixture.authority.ProjectedSeries.Slots[0].Attempts[0].WinnerID = nil
		require.Equal(t, fixture.firstParticipantID, *plan.Revision().Attempts()[0].WinnerID)
		require.Equal(t, domain.SeriesStateLocked, plan.Condition().ExpectedSeries().State)

		attempts := plan.Revision().Attempts()
		*attempts[0].WinnerID = task053PlannerID(141)
		commandAttempt := plan.Revision().CommandAttempt()
		*commandAttempt.WinnerID = task053PlannerID(142)
		conditionSeries := plan.Condition().ExpectedSeries()
		conditionSeries.State = domain.SeriesStateActive
		require.Equal(t, domain.SeriesStateActive, conditionSeries.State)
		head := plan.Revision().Head()
		*head.Attempts[0].WinnerID = task053PlannerID(143)
		require.Equal(t, fixture.firstParticipantID, *plan.Revision().Attempts()[0].WinnerID)
		require.Equal(t, fixture.firstParticipantID, *plan.Revision().CommandAttempt().WinnerID)
		require.Equal(t, domain.SeriesStateLocked, plan.Condition().ExpectedSeries().State)
	})

	t.Run("is deterministic under concurrent pure calls", func(t *testing.T) {
		fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
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
