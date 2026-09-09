package result

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestOfficialResultRevisionValidation(t *testing.T) {
	t.Run("rejects invalid projected result transitions", func(t *testing.T) {
		fixture := task053GameResultFixture(t)

		tests := []struct {
			name   string
			mutate func(*OfficialResultRevisionCommand, *OfficialResultRevisionAuthority)
		}{
			{
				name: "non terminal projection",
				mutate: func(_ *OfficialResultRevisionCommand, authority *OfficialResultRevisionAuthority) {
					game := &authority.ProjectedSeries.Slots[0].Attempts[0]
					game.State = domain.GameStateActive
					game.ResultReason = ""
					game.WinnerID = nil
					game.ResultRevisionID = nil
				},
			},
			{
				name: "wrong projected head",
				mutate: func(_ *OfficialResultRevisionCommand, authority *OfficialResultRevisionAuthority) {
					wrong := domain.OfficialResultRevisionID(task053PlannerID(32))
					authority.ProjectedSeries.Slots[0].Attempts[0].ResultRevisionID = &wrong
				},
			},
			{
				name: "unrelated participant mutation",
				mutate: func(_ *OfficialResultRevisionCommand, authority *OfficialResultRevisionAuthority) {
					authority.ProjectedSeries.SecondParticipantID = task053PlannerID(33)
				},
			},
			{
				name: "command outcome drift",
				mutate: func(command *OfficialResultRevisionCommand, _ *OfficialResultRevisionAuthority) {
					command.Outcome.GameReason = domain.GameResultReasonSurrender
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := cloneOfficialPlannerCommand(fixture.command)
				authority := cloneOfficialPlannerAuthority(fixture.authority)
				test.mutate(&command, &authority)

				_, err := PlanOfficialResultRevision(command, authority, fixture.recordedAt)
				require.Error(t, err)
			})
		}
	})

	t.Run("validates typed actor provenance and recorded time", func(t *testing.T) {
		fixture := task053GameResultFixture(t)
		operatorID := task053PlannerID(40)

		tests := []struct {
			name       string
			actor      domain.ResultActor
			recordedAt time.Time
			wantError  bool
		}{
			{name: "server", actor: domain.ResultActor{Kind: domain.ResultActorServer}, recordedAt: fixture.recordedAt},
			{name: "operator", actor: domain.ResultActor{Kind: domain.ResultActorOperator, PrincipalID: &operatorID}, recordedAt: fixture.recordedAt},
			{name: "server principal", actor: domain.ResultActor{Kind: domain.ResultActorServer, PrincipalID: &operatorID}, recordedAt: fixture.recordedAt, wantError: true},
			{name: "operator without principal", actor: domain.ResultActor{Kind: domain.ResultActorOperator}, recordedAt: fixture.recordedAt, wantError: true},
			{name: "local time", actor: domain.ResultActor{Kind: domain.ResultActorServer}, recordedAt: fixture.recordedAt.In(time.FixedZone("offset", 3600)), wantError: true},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := cloneOfficialPlannerCommand(fixture.command)
				command.Actor = test.actor
				_, err := PlanOfficialResultRevision(command, fixture.authority, test.recordedAt)
				if test.wantError {
					require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
					return
				}
				require.NoError(t, err)
			})
		}
	})

	t.Run("preserves row revisions that snapshots cannot reveal", func(t *testing.T) {
		fixture := task053GameResultFixture(t)
		first, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)

		advanced := cloneOfficialPlannerAuthority(fixture.authority)
		advanced.SeriesRevision++
		advanced.AttemptRevision++
		second, err := PlanOfficialResultRevision(fixture.command, advanced, fixture.recordedAt)
		require.NoError(t, err)
		require.NotEqual(t, first.Condition().ExpectedSeriesRevision(), second.Condition().ExpectedSeriesRevision())
		require.NotEqual(t, first.Condition().ExpectedAttemptRevision(), second.Condition().ExpectedAttemptRevision())

		for _, mutate := range []func(*OfficialResultRevisionAuthority){
			func(authority *OfficialResultRevisionAuthority) { authority.SeriesRevision = 0 },
			func(authority *OfficialResultRevisionAuthority) { authority.AttemptRevision = 0 },
		} {
			authority := cloneOfficialPlannerAuthority(fixture.authority)
			mutate(&authority)
			_, err := PlanOfficialResultRevision(fixture.command, authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		}

		maxAuthority := cloneOfficialPlannerAuthority(fixture.authority)
		maxAuthority.SeriesRevision = SeriesRowRevision(math.MaxInt64)
		maxAuthority.AttemptRevision = AttemptRowRevision(math.MaxInt64)
		_, err = PlanOfficialResultRevision(fixture.command, maxAuthority, fixture.recordedAt)
		require.NoError(t, err)

		negative := int64(-1)
		for _, mutate := range []func(*OfficialResultRevisionAuthority){
			func(authority *OfficialResultRevisionAuthority) {
				authority.SeriesRevision = SeriesRowRevision(negative)
			},
			func(authority *OfficialResultRevisionAuthority) {
				authority.AttemptRevision = AttemptRowRevision(negative)
			},
		} {
			authority := cloneOfficialPlannerAuthority(fixture.authority)
			mutate(&authority)
			_, err := PlanOfficialResultRevision(fixture.command, authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		}
		series := task053SeriesResultFixture(t)
		series.authority.AttemptRevision = AttemptRowRevision(negative)
		_, err = PlanOfficialResultRevision(series.command, series.authority, series.recordedAt)
		require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
	})

	t.Run("rejects locally owned official result identity collisions", func(t *testing.T) {
		t.Run("proposed Game result is already owned by another Game", func(t *testing.T) {
			fixture := task053GameResultFixture(t)
			fixture.authority.PersistedSeries.Format = domain.SeriesFormatBO3
			fixture.authority.ProjectedSeries.Format = domain.SeriesFormatBO3
			slotID := task053PlannerID(97)
			game := domain.Game{
				ID: task053PlannerID(98), SlotID: slotID, AttemptNo: 1,
				State: domain.GameStateCompleted, ResultReason: domain.GameResultReasonSolved,
				WinnerID:         task053UUIDPointer(fixture.firstParticipantID),
				ResultRevisionID: officialResultRevisionIDPointer(fixture.command.RevisionID),
			}
			slot := domain.GameSlot{
				ID: slotID, SeriesID: fixture.command.Scope.SeriesID, Position: 2,
				Category: domain.CategoryCrypto, Attempts: []domain.Game{game},
			}
			fixture.authority.PersistedSeries.Slots = append(fixture.authority.PersistedSeries.Slots, slot)
			fixture.authority.ProjectedSeries.Slots = append(fixture.authority.ProjectedSeries.Slots, slot)

			_, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		})

		t.Run("proposed Series result is already owned by a Game", func(t *testing.T) {
			fixture := task053SeriesResultFixture(t)
			fixture.authority.PersistedSeries.Slots[0].Attempts[0].ResultRevisionID =
				officialResultRevisionIDPointer(fixture.command.RevisionID)
			fixture.authority.ProjectedSeries.Slots[0].Attempts[0].ResultRevisionID =
				officialResultRevisionIDPointer(fixture.command.RevisionID)

			_, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		})

		t.Run("two non-target Games share a result", func(t *testing.T) {
			fixture := task053GameResultFixture(t)
			fixture.authority.PersistedSeries.Format = domain.SeriesFormatBO3
			fixture.authority.ProjectedSeries.Format = domain.SeriesFormatBO3
			shared := domain.OfficialResultRevisionID(task053PlannerID(99))
			for position := 2; position <= 3; position++ {
				slotID := task053PlannerID(98 + position)
				game := domain.Game{
					ID: task053PlannerID(101 + position), SlotID: slotID, AttemptNo: 1,
					State: domain.GameStateCompleted, ResultReason: domain.GameResultReasonSolved,
					WinnerID: task053UUIDPointer(fixture.firstParticipantID), ResultRevisionID: &shared,
				}
				slot := domain.GameSlot{
					ID: slotID, SeriesID: fixture.command.Scope.SeriesID, Position: position,
					Category: domain.CategoryCrypto, Attempts: []domain.Game{game},
				}
				fixture.authority.PersistedSeries.Slots = append(fixture.authority.PersistedSeries.Slots, slot)
				fixture.authority.ProjectedSeries.Slots = append(fixture.authority.ProjectedSeries.Slots, slot)
			}

			_, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		})
	})

	t.Run("rejects ambiguous Game identities before subject lookup", func(t *testing.T) {
		fixture := task053GameResultFixture(t)
		fixture.authority.PersistedSeries.Format = domain.SeriesFormatBO3
		fixture.authority.ProjectedSeries.Format = domain.SeriesFormatBO3
		duplicateSlotID := task053PlannerID(91)
		persistedDuplicate := cloneGame(fixture.authority.PersistedSeries.Slots[0].Attempts[0])
		persistedDuplicate.SlotID = duplicateSlotID
		projectedDuplicate := cloneGame(fixture.authority.ProjectedSeries.Slots[0].Attempts[0])
		projectedDuplicate.SlotID = duplicateSlotID
		fixture.authority.PersistedSeries.Slots = append(fixture.authority.PersistedSeries.Slots, domain.GameSlot{
			ID: duplicateSlotID, SeriesID: fixture.command.Scope.SeriesID, Position: 2,
			Category: domain.CategoryCrypto, Attempts: []domain.Game{persistedDuplicate},
		})
		fixture.authority.ProjectedSeries.Slots = append(fixture.authority.ProjectedSeries.Slots, domain.GameSlot{
			ID: duplicateSlotID, SeriesID: fixture.command.Scope.SeriesID, Position: 2,
			Category: domain.CategoryCrypto, Attempts: []domain.Game{projectedDuplicate},
		})

		_, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
	})

	t.Run("rejects local UUID cross-role aliases", func(t *testing.T) {
		t.Run("command equals revision", func(t *testing.T) {
			fixture := task053GameResultFixture(t)
			fixture.command.CommandID = fixture.command.RevisionID.UUID()

			_, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		})

		t.Run("source equals score revision", func(t *testing.T) {
			fixture := task053SeriesResultFixture(t)
			alias := domain.SeriesScoreRevisionID(
				fixture.command.ExpectedSourceProjection.ID().UUID(),
			)
			fixture.command.Outcome.ScoreRevisionID = &alias

			_, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		})

		t.Run("actor equals source", func(t *testing.T) {
			fixture := task053GameCorrectionFixture(t)
			principal := fixture.command.ExpectedSourceProjection.ID().UUID()
			fixture.command.Actor.PrincipalID = &principal

			_, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		})
	})

	t.Run("rejects a spliced plan", func(t *testing.T) {
		firstFixture := task053GameResultFixture(t)
		first, err := PlanOfficialResultRevision(firstFixture.command, firstFixture.authority, firstFixture.recordedAt)
		require.NoError(t, err)
		secondFixture := task053GameResultFixture(t)
		secondFixture.command.CommandID = task053PlannerID(92)
		secondFixture.command.RevisionID = domain.OfficialResultRevisionID(task053PlannerID(93))
		secondFixture.authority.ProjectedSeries.Slots[0].Attempts[0].ResultRevisionID =
			officialResultRevisionIDPointer(secondFixture.command.RevisionID)
		second, err := PlanOfficialResultRevision(secondFixture.command, secondFixture.authority, secondFixture.recordedAt)
		require.NoError(t, err)

		spliced := OfficialResultRevisionPlan{condition: first.condition, revision: second.revision}
		require.ErrorIs(t, spliced.Validate(), ErrInvalidOfficialResultRevision)
	})

	t.Run("returns detached immutable values", func(t *testing.T) {
		fixture := task053GameResultFixture(t)
		plan, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)

		*fixture.command.Outcome.WinnerID = task053PlannerID(50)
		fixture.authority.PersistedSeries.Slots[0].Attempts[0].State = domain.GameStateReady
		fixture.authority.ProjectedSeries.Slots[0].Attempts[0].WinnerID = nil
		require.Equal(t, fixture.firstParticipantID, *plan.Revision().Outcome().WinnerID)
		require.Equal(t, domain.GameStateActive,
			plan.Condition().ExpectedSeries().Slots[0].Attempts[0].State)

		outcome := plan.Revision().Outcome()
		*outcome.WinnerID = task053PlannerID(51)
		actor := plan.Revision().Actor()
		operatorID := task053PlannerID(52)
		actor.PrincipalID = &operatorID
		require.Equal(t, operatorID, *actor.PrincipalID)
		series := plan.Condition().ExpectedSeries()
		series.Slots[0].Attempts[0].State = domain.GameStatePaused
		head := plan.Revision().Head()
		*head.Outcome.WinnerID = task053PlannerID(53)
		require.Equal(t, fixture.firstParticipantID, *plan.Revision().Outcome().WinnerID)
		require.Nil(t, plan.Revision().Actor().PrincipalID)
		require.Equal(t, domain.GameStateActive,
			plan.Condition().ExpectedSeries().Slots[0].Attempts[0].State)
	})

	t.Run("is deterministic under concurrent pure calls", func(t *testing.T) {
		fixture := task053GameResultFixture(t)
		baseline, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)

		const callers = 32
		plans := make(chan OfficialResultRevisionPlan, callers)
		errs := make(chan error, callers)
		var group sync.WaitGroup
		for range callers {
			group.Add(1)
			go func() {
				defer group.Done()
				plan, planErr := PlanOfficialResultRevision(
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
