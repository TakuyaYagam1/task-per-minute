package result

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestOfficialResultRevision(t *testing.T) {
	t.Run("exposes a rehydratable head and complete row version CAS", func(t *testing.T) {
		fixture := task053GameResultFixture(t)
		fixture.authority.SeriesRevision = SeriesRowRevision(7)
		fixture.authority.AttemptRevision = AttemptRowRevision(11)

		plan, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.NoError(t, plan.Validate())

		revision := plan.Revision()
		head := revision.Head()
		require.NoError(t, head.Validate())
		require.Equal(t, fixture.command.Scope, head.Scope)
		require.Equal(t, fixture.command.RevisionID, head.ID)
		require.Equal(t, fixture.command.Outcome, head.Outcome)
		require.Equal(t, fixture.command.ExpectedSourceProjection, head.SourceProjection)

		condition := plan.Condition()
		require.Equal(t, SeriesRowRevision(7), condition.ExpectedSeriesRevision())
		require.Equal(t, AttemptRowRevision(11), condition.ExpectedAttemptRevision())
		require.Nil(t, condition.ExpectedCurrentRevisionID())
		require.Equal(t, 0, condition.ExpectedCurrentOrdinal())
	})

	t.Run("plans a terminal Game result from the persisted subject", func(t *testing.T) {
		fixture := task053GameResultFixture(t)

		plan, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.NoError(t, plan.Condition().Validate())
		require.NoError(t, plan.Revision().Validate())

		require.Equal(t, fixture.command.Scope, plan.Condition().Scope())
		require.Equal(t, fixture.authority.PersistedSeries, plan.Condition().ExpectedSeries())
		require.Nil(t, plan.Condition().ExpectedCurrentHead())
		require.Equal(t, fixture.authority.SourceProjection, plan.Condition().ExpectedSourceProjection())

		require.Equal(t, fixture.command.RevisionID, plan.Revision().ID())
		require.Equal(t, 1, plan.Revision().Ordinal())
		require.Nil(t, plan.Revision().PreviousRevisionID())
		require.Equal(t, fixture.command.CommandID, plan.Revision().CommandID())
		require.Equal(t, fixture.command.Actor, plan.Revision().Actor())
		require.Equal(t, fixture.command.Outcome, plan.Revision().Outcome())
		require.Equal(t, fixture.command.ExpectedSourceProjection, plan.Revision().SourceProjection())
		require.Equal(t, fixture.recordedAt, plan.Revision().RecordedAt())
	})

	t.Run("plans a corrected Game result from the exact current head", func(t *testing.T) {
		fixture := task053GameResultFixture(t)
		first, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)

		current := first.Revision().Head()
		fixture.authority.PersistedSeries = cloneSeries(fixture.authority.ProjectedSeries)
		fixture.authority.CurrentHead = &current
		fixture.command.CommandID = task053PlannerID(21)
		fixture.command.RevisionID = domain.OfficialResultRevisionID(task053PlannerID(22))
		fixture.command.ExpectedCurrentRevisionID = officialResultRevisionIDPointer(first.Revision().ID())
		fixture.command.Outcome.GameReason = domain.GameResultReasonOperatorForfeit
		fixture.command.Outcome.WinnerID = task053UUIDPointer(fixture.secondParticipantID)
		fixture.authority.ProjectedSeries = cloneSeries(fixture.authority.PersistedSeries)
		projectedGame := &fixture.authority.ProjectedSeries.Slots[0].Attempts[0]
		projectedGame.ResultReason = fixture.command.Outcome.GameReason
		projectedGame.WinnerID = task053UUIDPointer(fixture.secondParticipantID)
		projectedGame.ResultRevisionID = officialResultRevisionIDPointer(fixture.command.RevisionID)
		previousSourceID := current.SourceProjection.ID()
		fixture.authority.SourceProjection = task053Projection(t, task053PlannerID(25), fixture.command.Scope.TournamentID,
			domain.ArtifactKindGameResult, fixture.command.Scope.GameID, 2, &previousSourceID, "game-correction")
		fixture.command.ExpectedSourceProjection = fixture.authority.SourceProjection
		operatorID := task053PlannerID(26)
		fixture.command.Actor = domain.ResultActor{Kind: domain.ResultActorOperator, PrincipalID: &operatorID}
		fixture.recordedAt = fixture.recordedAt.Add(time.Minute)

		plan, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.Equal(t, 2, plan.Revision().Ordinal())
		require.Equal(t, first.Revision().ID(), *plan.Revision().PreviousRevisionID())
		require.Equal(t, current, *plan.Condition().ExpectedCurrentHead())
		require.Equal(t, officialResultRevisionIDPointer(current.ID), plan.Condition().ExpectedCurrentRevisionID())
		require.Equal(t, current.Ordinal, plan.Condition().ExpectedCurrentOrdinal())
	})

	t.Run("requires a direct successor source and operator for a correction", func(t *testing.T) {
		fixture := task053GameCorrectionFixture(t)

		t.Run("reused command ID", func(t *testing.T) {
			command := cloneOfficialPlannerCommand(fixture.command)
			command.CommandID = fixture.authority.CurrentHead.CommandID

			_, err := PlanOfficialResultRevision(command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		})

		t.Run("revision two cycle", func(t *testing.T) {
			authority := cloneOfficialPlannerAuthority(fixture.authority)
			previous := domain.OfficialResultRevisionID(task053PlannerID(94))
			authority.CurrentHead.Ordinal = 2
			authority.CurrentHead.PreviousRevisionID = &previous
			command := cloneOfficialPlannerCommand(fixture.command)
			command.RevisionID = previous
			authority.ProjectedSeries.Slots[0].Attempts[0].ResultRevisionID = &previous

			_, err := PlanOfficialResultRevision(command, authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		})

		t.Run("source two cycle", func(t *testing.T) {
			authority := cloneOfficialPlannerAuthority(fixture.authority)
			priorSourceID := domain.DerivedRevisionID(task053PlannerID(95))
			currentSource := task053Projection(t, task053PlannerID(96), fixture.command.Scope.TournamentID,
				domain.ArtifactKindGameResult, fixture.command.Scope.GameID, 2, &priorSourceID, "cycle-current")
			currentSourceID := currentSource.ID()
			cycledSource := task053Projection(t, priorSourceID.UUID(), fixture.command.Scope.TournamentID,
				domain.ArtifactKindGameResult, fixture.command.Scope.GameID, 3, &currentSourceID, "cycle-next")
			authority.CurrentHead.SourceProjection = currentSource
			authority.SourceProjection = cycledSource
			command := cloneOfficialPlannerCommand(fixture.command)
			command.ExpectedSourceProjection = cycledSource

			_, err := PlanOfficialResultRevision(command, authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrOfficialResultRevisionConflict)
		})

		t.Run("repeated source", func(t *testing.T) {
			authority := cloneOfficialPlannerAuthority(fixture.authority)
			authority.SourceProjection = authority.CurrentHead.SourceProjection
			command := cloneOfficialPlannerCommand(fixture.command)
			command.ExpectedSourceProjection = authority.SourceProjection

			_, err := PlanOfficialResultRevision(command, authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrOfficialResultRevisionConflict)
		})

		t.Run("server actor", func(t *testing.T) {
			command := cloneOfficialPlannerCommand(fixture.command)
			command.Actor = domain.ResultActor{Kind: domain.ResultActorServer}

			_, err := PlanOfficialResultRevision(command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		})

		t.Run("equal timestamp", func(t *testing.T) {
			_, err := PlanOfficialResultRevision(
				fixture.command,
				fixture.authority,
				fixture.authority.CurrentHead.RecordedAt,
			)
			require.NoError(t, err)
		})

		t.Run("ordinal overflow", func(t *testing.T) {
			authority := cloneOfficialPlannerAuthority(fixture.authority)
			authority.CurrentHead.Ordinal = int(^uint(0) >> 1)
			authority.CurrentHead.PreviousRevisionID = officialResultRevisionIDPointer(
				domain.OfficialResultRevisionID(task053PlannerID(89)),
			)

			_, err := PlanOfficialResultRevision(fixture.command, authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		})
	})

	t.Run("rejects a correction timestamp before the current head", func(t *testing.T) {
		fixture := task053GameResultFixture(t)
		first, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)

		current := first.Revision().Head()
		fixture.authority.PersistedSeries = cloneSeries(fixture.authority.ProjectedSeries)
		fixture.authority.CurrentHead = &current
		fixture.command.CommandID = task053PlannerID(23)
		fixture.command.RevisionID = domain.OfficialResultRevisionID(task053PlannerID(24))
		fixture.command.ExpectedCurrentRevisionID = officialResultRevisionIDPointer(first.Revision().ID())
		fixture.authority.ProjectedSeries = cloneSeries(fixture.authority.PersistedSeries)
		fixture.authority.ProjectedSeries.Slots[0].Attempts[0].ResultRevisionID =
			officialResultRevisionIDPointer(fixture.command.RevisionID)
		previousSourceID := current.SourceProjection.ID()
		fixture.authority.SourceProjection = task053Projection(t, task053PlannerID(27), fixture.command.Scope.TournamentID,
			domain.ArtifactKindGameResult, fixture.command.Scope.GameID, 2, &previousSourceID, "game-correction-time")
		fixture.command.ExpectedSourceProjection = fixture.authority.SourceProjection
		operatorID := task053PlannerID(28)
		fixture.command.Actor = domain.ResultActor{Kind: domain.ResultActorOperator, PrincipalID: &operatorID}

		_, err = PlanOfficialResultRevision(
			fixture.command,
			fixture.authority,
			first.Revision().RecordedAt().Add(-time.Minute),
		)
		require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
	})

	t.Run("binds a terminal Series outcome to its trusted score head and reason", func(t *testing.T) {
		fixture := task053SeriesResultFixture(t)

		plan, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.NoError(t, plan.Revision().Validate())
		require.Equal(t, domain.SeriesStateCompleted, plan.Revision().Outcome().SeriesState)
		require.Equal(t, domain.SeriesResultReasonScoreComplete, plan.Revision().Outcome().SeriesReason)
		require.Equal(t, fixture.authority.PersistedSeries.CurrentScoreRevisionID, plan.Revision().Outcome().ScoreRevisionID)
	})

	t.Run("plans a cancelled Series outcome with a cancellation reason and no winner", func(t *testing.T) {
		fixture := task053SeriesResultFixture(t)
		fixture.authority.ProjectedSeries.State = domain.SeriesStateCancelled
		fixture.authority.ProjectedSeries.WinnerID = nil
		fixture.authority.ProjectedSeriesReason = domain.SeriesResultReasonSeriesCancelled
		fixture.command.Outcome.SeriesState = domain.SeriesStateCancelled
		fixture.command.Outcome.SeriesReason = domain.SeriesResultReasonSeriesCancelled
		fixture.command.Outcome.WinnerID = nil

		plan, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.Equal(t, domain.SeriesStateCancelled, plan.Revision().Outcome().SeriesState)
		require.Nil(t, plan.Revision().Outcome().WinnerID)
	})

	t.Run("accepts trusted score and Series heads from the same composite projection", func(t *testing.T) {
		t.Run("Game result with score head", func(t *testing.T) {
			fixture := task053GameResultFixture(t)
			scoreRevisionID := domain.SeriesScoreRevisionID(task053PlannerID(65))
			fixture.authority.ProjectedSeries.Score.FirstParticipantWins = 1
			fixture.authority.ProjectedSeries.CurrentScoreRevisionID = &scoreRevisionID

			_, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.NoError(t, err)
		})

		t.Run("Series result with Game and score heads", func(t *testing.T) {
			base := task053GameResultFixture(t)
			oldScoreID := domain.SeriesScoreRevisionID(task053PlannerID(66))
			newScoreID := domain.SeriesScoreRevisionID(task053PlannerID(67))
			gameResultID := domain.OfficialResultRevisionID(task053PlannerID(68))
			seriesResultID := domain.OfficialResultRevisionID(task053PlannerID(69))
			persisted := cloneSeries(base.authority.PersistedSeries)
			persisted.CurrentScoreRevisionID = &oldScoreID
			projected := cloneSeries(base.authority.ProjectedSeries)
			projected.Slots[0].Attempts[0].ResultRevisionID = &gameResultID
			projected.Score.FirstParticipantWins = 1
			projected.CurrentScoreRevisionID = &newScoreID
			projected.State = domain.SeriesStateCompleted
			projected.WinnerID = task053UUIDPointer(base.firstParticipantID)
			projected.CurrentResultRevisionID = &seriesResultID
			scope := OfficialResultScope{
				TournamentID: persisted.TournamentID,
				SeriesID:     persisted.ID,
				Kind:         OfficialResultSubjectSeries,
			}
			source := task053Projection(t, task053PlannerID(70), persisted.TournamentID,
				domain.ArtifactKindSeriesResult, persisted.ID, 1, nil, "series-bundle")
			command := OfficialResultRevisionCommand{
				Scope:                    scope,
				CommandID:                task053PlannerID(71),
				RevisionID:               seriesResultID,
				Actor:                    domain.ResultActor{Kind: domain.ResultActorServer},
				ExpectedSourceProjection: source,
				Outcome: OfficialResultOutcome{
					SeriesState:     domain.SeriesStateCompleted,
					SeriesReason:    domain.SeriesResultReasonScoreComplete,
					WinnerID:        task053UUIDPointer(base.firstParticipantID),
					ScoreRevisionID: &newScoreID,
				},
			}
			authority := OfficialResultRevisionAuthority{
				Scope:                 scope,
				PersistedSeries:       persisted,
				ProjectedSeries:       projected,
				ProjectedSeriesReason: domain.SeriesResultReasonScoreComplete,
				SourceProjection:      source,
				SeriesRevision:        1,
			}

			_, err := PlanOfficialResultRevision(command, authority, base.recordedAt)
			require.NoError(t, err)
		})
	})

	t.Run("rejects structural mutation in a composite Series projection", func(t *testing.T) {
		fixture := task053SeriesResultFixture(t)
		tests := []struct {
			name   string
			mutate func(*domain.Series)
		}{
			{
				name: "format",
				mutate: func(series *domain.Series) {
					series.Format = domain.SeriesFormatBO3
					series.Score.FirstParticipantWins = 2
				},
			},
			{
				name: "attempt identity",
				mutate: func(series *domain.Series) {
					series.Slots[0].Attempts[0].ID = task053PlannerID(72)
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				authority := cloneOfficialPlannerAuthority(fixture.authority)
				test.mutate(&authority.ProjectedSeries)
				_, err := PlanOfficialResultRevision(fixture.command, authority, fixture.recordedAt)
				require.Error(t, err)
			})
		}
	})

	t.Run("rejects a legal but untrusted Series reason", func(t *testing.T) {
		fixture := task053SeriesResultFixture(t)
		fixture.command.Outcome.SeriesReason = domain.SeriesResultReasonOperatorCorrection

		_, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrOfficialResultRevisionConflict)
	})

	t.Run("rejects stale subject head and source expectations", func(t *testing.T) {
		fixture := task053GameResultFixture(t)
		staleHead := domain.OfficialResultRevisionID(task053PlannerID(30))
		fork := task053Projection(t, task053PlannerID(31), fixture.command.Scope.TournamentID,
			domain.ArtifactKindGameResult, fixture.command.Scope.GameID, 1, nil, "fork")
		digestFork := task053Projection(t, fixture.authority.SourceProjection.ID().UUID(),
			fixture.command.Scope.TournamentID, domain.ArtifactKindGameResult,
			fixture.command.Scope.GameID, 1, nil, "different-payload")

		tests := []struct {
			name   string
			mutate func(*OfficialResultRevisionCommand, *OfficialResultRevisionAuthority)
		}{
			{
				name: "command head",
				mutate: func(command *OfficialResultRevisionCommand, _ *OfficialResultRevisionAuthority) {
					command.ExpectedCurrentRevisionID = &staleHead
				},
			},
			{
				name: "persisted head",
				mutate: func(_ *OfficialResultRevisionCommand, authority *OfficialResultRevisionAuthority) {
					authority.PersistedSeries.Slots[0].Attempts[0] =
						cloneGame(authority.ProjectedSeries.Slots[0].Attempts[0])
					authority.PersistedSeries.Slots[0].Attempts[0].ResultRevisionID = &staleHead
				},
			},
			{
				name: "source fork",
				mutate: func(command *OfficialResultRevisionCommand, _ *OfficialResultRevisionAuthority) {
					command.ExpectedSourceProjection = fork
				},
			},
			{
				name: "source digest fork",
				mutate: func(command *OfficialResultRevisionCommand, _ *OfficialResultRevisionAuthority) {
					command.ExpectedSourceProjection = digestFork
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := cloneOfficialPlannerCommand(fixture.command)
				authority := cloneOfficialPlannerAuthority(fixture.authority)
				test.mutate(&command, &authority)

				_, err := PlanOfficialResultRevision(command, authority, fixture.recordedAt)
				require.ErrorIs(t, err, ErrOfficialResultRevisionConflict)
			})
		}
	})
}
