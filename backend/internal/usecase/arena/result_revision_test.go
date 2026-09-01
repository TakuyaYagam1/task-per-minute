package arena

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestOfficialResultRevision(t *testing.T) {
	t.Run("exposes a rehydratable head and complete row version CAS", func(t *testing.T) {
		fixture := task053GameResultFixture(t)
		fixture.authority.SeriesRevision = ArenaSeriesRowRevision(7)
		fixture.authority.AttemptRevision = ArenaAttemptRowRevision(11)

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
		require.Equal(t, ArenaSeriesRowRevision(7), condition.ExpectedSeriesRevision())
		require.Equal(t, ArenaAttemptRowRevision(11), condition.ExpectedAttemptRevision())
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
		fixture.authority.PersistedSeries = cloneArenaSeries(fixture.authority.ProjectedSeries)
		fixture.authority.CurrentHead = &current
		fixture.command.CommandID = task053PlannerID(21)
		fixture.command.RevisionID = domain.ArenaOfficialResultRevisionID(task053PlannerID(22))
		fixture.command.ExpectedCurrentRevisionID = officialResultRevisionIDPointer(first.Revision().ID())
		fixture.command.Outcome.GameReason = domain.ArenaGameResultReasonOperatorForfeit
		fixture.command.Outcome.WinnerID = task053UUIDPointer(fixture.secondParticipantID)
		fixture.authority.ProjectedSeries = cloneArenaSeries(fixture.authority.PersistedSeries)
		projectedGame := &fixture.authority.ProjectedSeries.Slots[0].Attempts[0]
		projectedGame.ResultReason = fixture.command.Outcome.GameReason
		projectedGame.WinnerID = task053UUIDPointer(fixture.secondParticipantID)
		projectedGame.ResultRevisionID = officialResultRevisionIDPointer(fixture.command.RevisionID)
		previousSourceID := current.SourceProjection.ID()
		fixture.authority.SourceProjection = task053Projection(t, task053PlannerID(25), fixture.command.Scope.TournamentID,
			domain.ArenaArtifactKindGameResult, fixture.command.Scope.GameID, 2, &previousSourceID, "game-correction")
		fixture.command.ExpectedSourceProjection = fixture.authority.SourceProjection
		operatorID := task053PlannerID(26)
		fixture.command.Actor = ArenaResultActor{Kind: ArenaResultActorOperator, PrincipalID: &operatorID}
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
			previous := domain.ArenaOfficialResultRevisionID(task053PlannerID(94))
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
			priorSourceID := domain.ArenaDerivedRevisionID(task053PlannerID(95))
			currentSource := task053Projection(t, task053PlannerID(96), fixture.command.Scope.TournamentID,
				domain.ArenaArtifactKindGameResult, fixture.command.Scope.GameID, 2, &priorSourceID, "cycle-current")
			currentSourceID := currentSource.ID()
			cycledSource := task053Projection(t, priorSourceID.UUID(), fixture.command.Scope.TournamentID,
				domain.ArenaArtifactKindGameResult, fixture.command.Scope.GameID, 3, &currentSourceID, "cycle-next")
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
			command.Actor = ArenaResultActor{Kind: ArenaResultActorServer}

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
				domain.ArenaOfficialResultRevisionID(task053PlannerID(89)),
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
		fixture.authority.PersistedSeries = cloneArenaSeries(fixture.authority.ProjectedSeries)
		fixture.authority.CurrentHead = &current
		fixture.command.CommandID = task053PlannerID(23)
		fixture.command.RevisionID = domain.ArenaOfficialResultRevisionID(task053PlannerID(24))
		fixture.command.ExpectedCurrentRevisionID = officialResultRevisionIDPointer(first.Revision().ID())
		fixture.authority.ProjectedSeries = cloneArenaSeries(fixture.authority.PersistedSeries)
		fixture.authority.ProjectedSeries.Slots[0].Attempts[0].ResultRevisionID =
			officialResultRevisionIDPointer(fixture.command.RevisionID)
		previousSourceID := current.SourceProjection.ID()
		fixture.authority.SourceProjection = task053Projection(t, task053PlannerID(27), fixture.command.Scope.TournamentID,
			domain.ArenaArtifactKindGameResult, fixture.command.Scope.GameID, 2, &previousSourceID, "game-correction-time")
		fixture.command.ExpectedSourceProjection = fixture.authority.SourceProjection
		operatorID := task053PlannerID(28)
		fixture.command.Actor = ArenaResultActor{Kind: ArenaResultActorOperator, PrincipalID: &operatorID}

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
		require.Equal(t, domain.ArenaSeriesStateCompleted, plan.Revision().Outcome().SeriesState)
		require.Equal(t, ArenaSeriesResultReasonScoreComplete, plan.Revision().Outcome().SeriesReason)
		require.Equal(t, fixture.authority.PersistedSeries.CurrentScoreRevisionID, plan.Revision().Outcome().ScoreRevisionID)
	})

	t.Run("plans a cancelled Series outcome with a cancellation reason and no winner", func(t *testing.T) {
		fixture := task053SeriesResultFixture(t)
		fixture.authority.ProjectedSeries.State = domain.ArenaSeriesStateCancelled
		fixture.authority.ProjectedSeries.WinnerID = nil
		fixture.authority.ProjectedSeriesReason = ArenaSeriesResultReasonSeriesCancelled
		fixture.command.Outcome.SeriesState = domain.ArenaSeriesStateCancelled
		fixture.command.Outcome.SeriesReason = ArenaSeriesResultReasonSeriesCancelled
		fixture.command.Outcome.WinnerID = nil

		plan, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.Equal(t, domain.ArenaSeriesStateCancelled, plan.Revision().Outcome().SeriesState)
		require.Nil(t, plan.Revision().Outcome().WinnerID)
	})

	t.Run("accepts trusted score and Series heads from the same composite projection", func(t *testing.T) {
		t.Run("Game result with score head", func(t *testing.T) {
			fixture := task053GameResultFixture(t)
			scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task053PlannerID(65))
			fixture.authority.ProjectedSeries.Score.FirstParticipantWins = 1
			fixture.authority.ProjectedSeries.CurrentScoreRevisionID = &scoreRevisionID

			_, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
			require.NoError(t, err)
		})

		t.Run("Series result with Game and score heads", func(t *testing.T) {
			base := task053GameResultFixture(t)
			oldScoreID := domain.ArenaSeriesScoreRevisionID(task053PlannerID(66))
			newScoreID := domain.ArenaSeriesScoreRevisionID(task053PlannerID(67))
			gameResultID := domain.ArenaOfficialResultRevisionID(task053PlannerID(68))
			seriesResultID := domain.ArenaOfficialResultRevisionID(task053PlannerID(69))
			persisted := cloneArenaSeries(base.authority.PersistedSeries)
			persisted.CurrentScoreRevisionID = &oldScoreID
			projected := cloneArenaSeries(base.authority.ProjectedSeries)
			projected.Slots[0].Attempts[0].ResultRevisionID = &gameResultID
			projected.Score.FirstParticipantWins = 1
			projected.CurrentScoreRevisionID = &newScoreID
			projected.State = domain.ArenaSeriesStateCompleted
			projected.WinnerID = task053UUIDPointer(base.firstParticipantID)
			projected.CurrentResultRevisionID = &seriesResultID
			scope := OfficialResultScope{
				TournamentID: persisted.TournamentID,
				SeriesID:     persisted.ID,
				Kind:         OfficialResultSubjectSeries,
			}
			source := task053Projection(t, task053PlannerID(70), persisted.TournamentID,
				domain.ArenaArtifactKindSeriesResult, persisted.ID, 1, nil, "series-bundle")
			command := OfficialResultRevisionCommand{
				Scope:                    scope,
				CommandID:                task053PlannerID(71),
				RevisionID:               seriesResultID,
				Actor:                    ArenaResultActor{Kind: ArenaResultActorServer},
				ExpectedSourceProjection: source,
				Outcome: OfficialResultOutcome{
					SeriesState:     domain.ArenaSeriesStateCompleted,
					SeriesReason:    ArenaSeriesResultReasonScoreComplete,
					WinnerID:        task053UUIDPointer(base.firstParticipantID),
					ScoreRevisionID: &newScoreID,
				},
			}
			authority := OfficialResultRevisionAuthority{
				Scope:                 scope,
				PersistedSeries:       persisted,
				ProjectedSeries:       projected,
				ProjectedSeriesReason: ArenaSeriesResultReasonScoreComplete,
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
			mutate func(*domain.ArenaSeries)
		}{
			{
				name: "format",
				mutate: func(series *domain.ArenaSeries) {
					series.Format = domain.ArenaSeriesFormatBO3
					series.Score.FirstParticipantWins = 2
				},
			},
			{
				name: "attempt identity",
				mutate: func(series *domain.ArenaSeries) {
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
		fixture.command.Outcome.SeriesReason = ArenaSeriesResultReasonOperatorCorrection

		_, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrOfficialResultRevisionConflict)
	})

	t.Run("rejects stale subject head and source expectations", func(t *testing.T) {
		fixture := task053GameResultFixture(t)
		staleHead := domain.ArenaOfficialResultRevisionID(task053PlannerID(30))
		fork := task053Projection(t, task053PlannerID(31), fixture.command.Scope.TournamentID,
			domain.ArenaArtifactKindGameResult, fixture.command.Scope.GameID, 1, nil, "fork")
		digestFork := task053Projection(t, fixture.authority.SourceProjection.ID().UUID(),
			fixture.command.Scope.TournamentID, domain.ArenaArtifactKindGameResult,
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
						cloneArenaGame(authority.ProjectedSeries.Slots[0].Attempts[0])
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
					game.State = domain.ArenaGameStateActive
					game.ResultReason = ""
					game.WinnerID = nil
					game.ResultRevisionID = nil
				},
			},
			{
				name: "wrong projected head",
				mutate: func(_ *OfficialResultRevisionCommand, authority *OfficialResultRevisionAuthority) {
					wrong := domain.ArenaOfficialResultRevisionID(task053PlannerID(32))
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
					command.Outcome.GameReason = domain.ArenaGameResultReasonSurrender
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
			actor      ArenaResultActor
			recordedAt time.Time
			wantError  bool
		}{
			{name: "server", actor: ArenaResultActor{Kind: ArenaResultActorServer}, recordedAt: fixture.recordedAt},
			{name: "operator", actor: ArenaResultActor{Kind: ArenaResultActorOperator, PrincipalID: &operatorID}, recordedAt: fixture.recordedAt},
			{name: "server principal", actor: ArenaResultActor{Kind: ArenaResultActorServer, PrincipalID: &operatorID}, recordedAt: fixture.recordedAt, wantError: true},
			{name: "operator without principal", actor: ArenaResultActor{Kind: ArenaResultActorOperator}, recordedAt: fixture.recordedAt, wantError: true},
			{name: "local time", actor: ArenaResultActor{Kind: ArenaResultActorServer}, recordedAt: fixture.recordedAt.In(time.FixedZone("offset", 3600)), wantError: true},
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
		maxAuthority.SeriesRevision = ArenaSeriesRowRevision(math.MaxInt64)
		maxAuthority.AttemptRevision = ArenaAttemptRowRevision(math.MaxInt64)
		_, err = PlanOfficialResultRevision(fixture.command, maxAuthority, fixture.recordedAt)
		require.NoError(t, err)

		negative := int64(-1)
		for _, mutate := range []func(*OfficialResultRevisionAuthority){
			func(authority *OfficialResultRevisionAuthority) {
				authority.SeriesRevision = ArenaSeriesRowRevision(negative)
			},
			func(authority *OfficialResultRevisionAuthority) {
				authority.AttemptRevision = ArenaAttemptRowRevision(negative)
			},
		} {
			authority := cloneOfficialPlannerAuthority(fixture.authority)
			mutate(&authority)
			_, err := PlanOfficialResultRevision(fixture.command, authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
		}
		series := task053SeriesResultFixture(t)
		series.authority.AttemptRevision = ArenaAttemptRowRevision(negative)
		_, err = PlanOfficialResultRevision(series.command, series.authority, series.recordedAt)
		require.ErrorIs(t, err, ErrInvalidOfficialResultRevision)
	})

	t.Run("rejects locally owned official result identity collisions", func(t *testing.T) {
		t.Run("proposed Game result is already owned by another Game", func(t *testing.T) {
			fixture := task053GameResultFixture(t)
			fixture.authority.PersistedSeries.Format = domain.ArenaSeriesFormatBO3
			fixture.authority.ProjectedSeries.Format = domain.ArenaSeriesFormatBO3
			slotID := task053PlannerID(97)
			game := domain.ArenaGame{
				ID: task053PlannerID(98), SlotID: slotID, AttemptNo: 1,
				State: domain.ArenaGameStateCompleted, ResultReason: domain.ArenaGameResultReasonSolved,
				WinnerID:         task053UUIDPointer(fixture.firstParticipantID),
				ResultRevisionID: officialResultRevisionIDPointer(fixture.command.RevisionID),
			}
			slot := domain.ArenaGameSlot{
				ID: slotID, SeriesID: fixture.command.Scope.SeriesID, Position: 2,
				Category: domain.CategoryCrypto, Attempts: []domain.ArenaGame{game},
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
			fixture.authority.PersistedSeries.Format = domain.ArenaSeriesFormatBO3
			fixture.authority.ProjectedSeries.Format = domain.ArenaSeriesFormatBO3
			shared := domain.ArenaOfficialResultRevisionID(task053PlannerID(99))
			for position := 2; position <= 3; position++ {
				slotID := task053PlannerID(98 + position)
				game := domain.ArenaGame{
					ID: task053PlannerID(101 + position), SlotID: slotID, AttemptNo: 1,
					State: domain.ArenaGameStateCompleted, ResultReason: domain.ArenaGameResultReasonSolved,
					WinnerID: task053UUIDPointer(fixture.firstParticipantID), ResultRevisionID: &shared,
				}
				slot := domain.ArenaGameSlot{
					ID: slotID, SeriesID: fixture.command.Scope.SeriesID, Position: position,
					Category: domain.CategoryCrypto, Attempts: []domain.ArenaGame{game},
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
		fixture.authority.PersistedSeries.Format = domain.ArenaSeriesFormatBO3
		fixture.authority.ProjectedSeries.Format = domain.ArenaSeriesFormatBO3
		duplicateSlotID := task053PlannerID(91)
		persistedDuplicate := cloneArenaGame(fixture.authority.PersistedSeries.Slots[0].Attempts[0])
		persistedDuplicate.SlotID = duplicateSlotID
		projectedDuplicate := cloneArenaGame(fixture.authority.ProjectedSeries.Slots[0].Attempts[0])
		projectedDuplicate.SlotID = duplicateSlotID
		fixture.authority.PersistedSeries.Slots = append(fixture.authority.PersistedSeries.Slots, domain.ArenaGameSlot{
			ID: duplicateSlotID, SeriesID: fixture.command.Scope.SeriesID, Position: 2,
			Category: domain.CategoryCrypto, Attempts: []domain.ArenaGame{persistedDuplicate},
		})
		fixture.authority.ProjectedSeries.Slots = append(fixture.authority.ProjectedSeries.Slots, domain.ArenaGameSlot{
			ID: duplicateSlotID, SeriesID: fixture.command.Scope.SeriesID, Position: 2,
			Category: domain.CategoryCrypto, Attempts: []domain.ArenaGame{projectedDuplicate},
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
			alias := domain.ArenaSeriesScoreRevisionID(
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
		secondFixture.command.RevisionID = domain.ArenaOfficialResultRevisionID(task053PlannerID(93))
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
		fixture.authority.PersistedSeries.Slots[0].Attempts[0].State = domain.ArenaGameStateReady
		fixture.authority.ProjectedSeries.Slots[0].Attempts[0].WinnerID = nil
		require.Equal(t, fixture.firstParticipantID, *plan.Revision().Outcome().WinnerID)
		require.Equal(t, domain.ArenaGameStateActive,
			plan.Condition().ExpectedSeries().Slots[0].Attempts[0].State)

		outcome := plan.Revision().Outcome()
		*outcome.WinnerID = task053PlannerID(51)
		actor := plan.Revision().Actor()
		operatorID := task053PlannerID(52)
		actor.PrincipalID = &operatorID
		require.Equal(t, operatorID, *actor.PrincipalID)
		series := plan.Condition().ExpectedSeries()
		series.Slots[0].Attempts[0].State = domain.ArenaGameStatePaused
		head := plan.Revision().Head()
		*head.Outcome.WinnerID = task053PlannerID(53)
		require.Equal(t, fixture.firstParticipantID, *plan.Revision().Outcome().WinnerID)
		require.Nil(t, plan.Revision().Actor().PrincipalID)
		require.Equal(t, domain.ArenaGameStateActive,
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

type task053OfficialFixture struct {
	command             OfficialResultRevisionCommand
	authority           OfficialResultRevisionAuthority
	recordedAt          time.Time
	firstParticipantID  uuid.UUID
	secondParticipantID uuid.UUID
}

func task053GameResultFixture(t *testing.T) task053OfficialFixture {
	t.Helper()
	tournamentID := task053PlannerID(1)
	seriesID := task053PlannerID(2)
	firstID := task053PlannerID(3)
	secondID := task053PlannerID(4)
	slotID := task053PlannerID(5)
	gameID := task053PlannerID(6)
	revisionID := domain.ArenaOfficialResultRevisionID(task053PlannerID(7))
	scope := OfficialResultScope{
		TournamentID: tournamentID,
		SeriesID:     seriesID,
		GameID:       gameID,
		Kind:         OfficialResultSubjectGame,
	}
	persisted := domain.ArenaSeries{
		ID:                  seriesID,
		TournamentID:        tournamentID,
		FirstParticipantID:  firstID,
		SecondParticipantID: secondID,
		Format:              domain.ArenaSeriesFormatBO1,
		State:               domain.ArenaSeriesStateActive,
		Score:               domain.ArenaSeriesScore{},
		Slots: []domain.ArenaGameSlot{{
			ID:          slotID,
			SeriesID:    seriesID,
			Position:    1,
			Category:    domain.CategoryWeb,
			ScoreBefore: domain.ArenaSeriesScore{},
			Attempts: []domain.ArenaGame{{
				ID:        gameID,
				SlotID:    slotID,
				AttemptNo: 1,
				State:     domain.ArenaGameStateActive,
			}},
		}},
	}
	projected := cloneArenaSeries(persisted)
	projectedGame := &projected.Slots[0].Attempts[0]
	projectedGame.State = domain.ArenaGameStateCompleted
	projectedGame.ResultReason = domain.ArenaGameResultReasonSolved
	projectedGame.WinnerID = task053UUIDPointer(firstID)
	projectedGame.ResultRevisionID = officialResultRevisionIDPointer(revisionID)
	source := task053Projection(t, task053PlannerID(8), tournamentID,
		domain.ArenaArtifactKindGameResult, gameID, 1, nil, "game-result")
	outcome := OfficialResultOutcome{
		GameState:  domain.ArenaGameStateCompleted,
		GameReason: domain.ArenaGameResultReasonSolved,
		WinnerID:   task053UUIDPointer(firstID),
	}
	return task053OfficialFixture{
		command: OfficialResultRevisionCommand{
			Scope:                    scope,
			CommandID:                task053PlannerID(9),
			RevisionID:               revisionID,
			Actor:                    ArenaResultActor{Kind: ArenaResultActorServer},
			ExpectedSourceProjection: source,
			Outcome:                  outcome,
		},
		authority: OfficialResultRevisionAuthority{
			Scope:            scope,
			PersistedSeries:  persisted,
			ProjectedSeries:  projected,
			SourceProjection: source,
			SeriesRevision:   1,
			AttemptRevision:  1,
		},
		recordedAt:          time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		firstParticipantID:  firstID,
		secondParticipantID: secondID,
	}
}

func task053SeriesResultFixture(t *testing.T) task053OfficialFixture {
	t.Helper()
	fixture := task053GameResultFixture(t)
	seriesID := fixture.authority.PersistedSeries.ID
	tournamentID := fixture.authority.PersistedSeries.TournamentID
	scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task053PlannerID(60))
	gameRevisionID := domain.ArenaOfficialResultRevisionID(task053PlannerID(61))
	resultRevisionID := domain.ArenaOfficialResultRevisionID(task053PlannerID(62))
	persisted := cloneArenaSeries(fixture.authority.ProjectedSeries)
	persisted.Score = domain.ArenaSeriesScore{FirstParticipantWins: 1}
	persisted.CurrentScoreRevisionID = &scoreRevisionID
	persisted.Slots[0].Attempts[0].ResultRevisionID = &gameRevisionID
	projected := cloneArenaSeries(persisted)
	projected.State = domain.ArenaSeriesStateCompleted
	projected.WinnerID = task053UUIDPointer(fixture.firstParticipantID)
	projected.CurrentResultRevisionID = &resultRevisionID
	scope := OfficialResultScope{
		TournamentID: tournamentID,
		SeriesID:     seriesID,
		Kind:         OfficialResultSubjectSeries,
	}
	source := task053Projection(t, task053PlannerID(63), tournamentID,
		domain.ArenaArtifactKindSeriesResult, seriesID, 1, nil, "series-result")
	outcome := OfficialResultOutcome{
		SeriesState:     domain.ArenaSeriesStateCompleted,
		SeriesReason:    ArenaSeriesResultReasonScoreComplete,
		WinnerID:        task053UUIDPointer(fixture.firstParticipantID),
		ScoreRevisionID: &scoreRevisionID,
	}
	return task053OfficialFixture{
		command: OfficialResultRevisionCommand{
			Scope:                    scope,
			CommandID:                task053PlannerID(64),
			RevisionID:               resultRevisionID,
			Actor:                    ArenaResultActor{Kind: ArenaResultActorServer},
			ExpectedSourceProjection: source,
			Outcome:                  outcome,
		},
		authority: OfficialResultRevisionAuthority{
			Scope:                 scope,
			PersistedSeries:       persisted,
			ProjectedSeries:       projected,
			ProjectedSeriesReason: ArenaSeriesResultReasonScoreComplete,
			SourceProjection:      source,
			SeriesRevision:        1,
		},
		recordedAt:          fixture.recordedAt,
		firstParticipantID:  fixture.firstParticipantID,
		secondParticipantID: fixture.secondParticipantID,
	}
}

func task053GameCorrectionFixture(t *testing.T) task053OfficialFixture {
	t.Helper()
	fixture := task053GameResultFixture(t)
	first, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
	require.NoError(t, err)
	current := first.Revision().Head()
	fixture.authority.PersistedSeries = cloneArenaSeries(fixture.authority.ProjectedSeries)
	fixture.authority.ProjectedSeries = cloneArenaSeries(fixture.authority.PersistedSeries)
	fixture.authority.CurrentHead = &current
	fixture.authority.SeriesRevision = 2
	fixture.authority.AttemptRevision = 2
	fixture.command.CommandID = task053PlannerID(81)
	fixture.command.RevisionID = domain.ArenaOfficialResultRevisionID(task053PlannerID(82))
	fixture.command.ExpectedCurrentRevisionID = officialResultRevisionIDPointer(current.ID)
	operatorID := task053PlannerID(83)
	fixture.command.Actor = ArenaResultActor{Kind: ArenaResultActorOperator, PrincipalID: &operatorID}
	fixture.command.Outcome.GameReason = domain.ArenaGameResultReasonOperatorForfeit
	fixture.command.Outcome.WinnerID = task053UUIDPointer(fixture.secondParticipantID)
	projectedGame := &fixture.authority.ProjectedSeries.Slots[0].Attempts[0]
	projectedGame.ResultReason = fixture.command.Outcome.GameReason
	projectedGame.WinnerID = task053UUIDPointer(fixture.secondParticipantID)
	projectedGame.ResultRevisionID = officialResultRevisionIDPointer(fixture.command.RevisionID)
	previousSourceID := current.SourceProjection.ID()
	fixture.authority.SourceProjection = task053Projection(t, task053PlannerID(84), fixture.command.Scope.TournamentID,
		domain.ArenaArtifactKindGameResult, fixture.command.Scope.GameID, 2, &previousSourceID, "game-correction")
	fixture.command.ExpectedSourceProjection = fixture.authority.SourceProjection
	fixture.recordedAt = fixture.recordedAt.Add(time.Minute)
	return fixture
}

func task053PlannerID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", number))
}

func task053Projection(
	t *testing.T,
	id uuid.UUID,
	tournamentID uuid.UUID,
	kind domain.ArenaArtifactKind,
	entityID uuid.UUID,
	revisionNo int,
	previous *domain.ArenaDerivedRevisionID,
	payload string,
) domain.ArenaDerivedRevision {
	t.Helper()
	projection, err := domain.NewArenaProjectionRevision(
		domain.ArenaDerivedRevisionID(id),
		tournamentID,
		domain.ArenaArtifactRef{Kind: kind, EntityID: entityID},
		revisionNo,
		previous,
		time.Date(2026, time.August, 31, 11, revisionNo, 0, 0, time.UTC),
		[]byte(payload),
	)
	require.NoError(t, err)
	return projection.Revision()
}

func task053UUIDPointer(value uuid.UUID) *uuid.UUID {
	clone := value
	return &clone
}

func officialResultRevisionIDPointer(
	value domain.ArenaOfficialResultRevisionID,
) *domain.ArenaOfficialResultRevisionID {
	clone := value
	return &clone
}

func cloneOfficialPlannerCommand(command OfficialResultRevisionCommand) OfficialResultRevisionCommand {
	clone := command
	clone.Actor = cloneArenaResultActor(command.Actor)
	clone.ExpectedCurrentRevisionID = cloneOfficialResultRevisionIDPointer(command.ExpectedCurrentRevisionID)
	clone.Outcome = cloneOfficialResultOutcome(command.Outcome)
	return clone
}

func cloneOfficialPlannerAuthority(
	authority OfficialResultRevisionAuthority,
) OfficialResultRevisionAuthority {
	clone := authority
	clone.PersistedSeries = cloneArenaSeries(authority.PersistedSeries)
	clone.ProjectedSeries = cloneArenaSeries(authority.ProjectedSeries)
	if authority.CurrentHead != nil {
		current := authority.CurrentHead.Clone()
		clone.CurrentHead = &current
	}
	return clone
}
