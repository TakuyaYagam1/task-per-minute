package arena

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestSeriesResultCascade(t *testing.T) {
	t.Run("binds one terminal Series result to the current terminal score", func(t *testing.T) {
		fixture := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)

		plan, err := PlanSeriesResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.NoError(t, plan.Validate())

		result := plan.SeriesResult().Revision()
		require.Equal(t, fixture.authority.CurrentScore.ID, *result.Outcome().ScoreRevisionID)
		require.Equal(t, domain.ArenaRevisionDependency{
			SourceRevisionID:  fixture.authority.CurrentScore.SourceProjection.ID(),
			DerivedRevisionID: result.SourceProjection().ID(),
		}, plan.Dependency())
		require.Equal(t, fixture.command.AuditEventID, plan.Audit().EventID)
		require.Equal(t, fixture.command.SeriesResult.CommandID, plan.Condition().CommandID())
		require.NotEqual(t, [32]byte{}, plan.Condition().IdempotencyKey())
	})

	t.Run("binds an unchanged cancellation score to the new score revision", func(t *testing.T) {
		fixture := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCancelled)

		plan, err := PlanSeriesResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.Equal(t, domain.ArenaSeriesScore{}, fixture.authority.CurrentScore.Score)
		require.Equal(t, fixture.authority.CurrentScore.ID,
			*plan.SeriesResult().Revision().Outcome().ScoreRevisionID)
		require.Len(t, plan.Dependencies(), 1)
	})

	t.Run("rejects a Series row revision that cannot be advanced", func(t *testing.T) {
		fixture := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)
		fixture.authority.SeriesResult.SeriesRevision = ArenaSeriesRowRevision(math.MaxInt64)

		_, err := PlanSeriesResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidSeriesResultCascade)
	})

	t.Run("rejects identities aliased with current score lineage", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*task054SeriesCascadeCase)
		}{
			{
				name: "audit aliases head command",
				mutate: func(fixture *task054SeriesCascadeCase) {
					fixture.command.AuditEventID = fixture.authority.CurrentScore.CommandID
				},
			},
			{
				name: "result aliases head predecessor",
				mutate: func(fixture *task054SeriesCascadeCase) {
					require.NotNil(t, fixture.authority.CurrentScore.PreviousRevisionID)
					resultID := domain.ArenaOfficialResultRevisionID(
						fixture.authority.CurrentScore.PreviousRevisionID.UUID(),
					)
					fixture.command.SeriesResult.RevisionID = resultID
					fixture.authority.SeriesResult.ProjectedSeries.CurrentResultRevisionID = &resultID
				},
			},
			{
				name: "audit aliases head actor",
				mutate: func(fixture *task054SeriesCascadeCase) {
					principalID := task054ID(52)
					fixture.authority.CurrentScore.Actor = ArenaResultActor{
						Kind: ArenaResultActorOperator, PrincipalID: &principalID,
					}
					fixture.command.AuditEventID = principalID
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				fixture := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)
				test.mutate(&fixture)
				_, err := PlanSeriesResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
				require.ErrorIs(t, err, ErrInvalidSeriesResultCascade)
			})
		}
	})

	t.Run("rejects non-terminal missing stale and ambiguous evidence", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*SeriesResultCascadeCommand, *SeriesResultCascadeAuthority)
		}{
			{
				name: "non-terminal Series",
				mutate: func(_ *SeriesResultCascadeCommand, authority *SeriesResultCascadeAuthority) {
					authority.SeriesResult.ProjectedSeries.State = domain.ArenaSeriesStateActive
					authority.SeriesResult.ProjectedSeries.WinnerID = nil
					authority.SeriesResult.ProjectedSeries.CurrentResultRevisionID = nil
				},
			},
			{
				name: "terminal Series without result head",
				mutate: func(_ *SeriesResultCascadeCommand, authority *SeriesResultCascadeAuthority) {
					authority.SeriesResult.ProjectedSeries.CurrentResultRevisionID = nil
				},
			},
			{
				name: "stale score ID",
				mutate: func(command *SeriesResultCascadeCommand, _ *SeriesResultCascadeAuthority) {
					command.SeriesResult.Outcome.ScoreRevisionID = seriesScoreRevisionIDPointer(
						domain.ArenaSeriesScoreRevisionID(task054ID(50)),
					)
				},
			},
			{
				name: "missing current score",
				mutate: func(_ *SeriesResultCascadeCommand, authority *SeriesResultCascadeAuthority) {
					authority.CurrentScore = SeriesScoreRevisionHead{}
				},
			},
			{
				name: "score provenance differs from Series",
				mutate: func(_ *SeriesResultCascadeCommand, authority *SeriesResultCascadeAuthority) {
					authority.CurrentScore.Attempts[0].Reason = domain.ArenaGameResultReasonNoShow
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				fixture := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)
				test.mutate(&fixture.command, &fixture.authority)
				_, err := PlanSeriesResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
				require.Error(t, err)
			})
		}
	})

	t.Run("rejects audit alias and backwards dependency chronology", func(t *testing.T) {
		t.Run("audit alias", func(t *testing.T) {
			fixture := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)
			fixture.command.SeriesResult.Actor = ArenaResultActor{
				Kind:        ArenaResultActorOperator,
				PrincipalID: task053UUIDPointer(fixture.command.AuditEventID),
			}
			_, err := PlanSeriesResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesResultCascade)
		})

		t.Run("backwards edge", func(t *testing.T) {
			fixture := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)
			source := fixture.command.SeriesResult.ExpectedSourceProjection
			projection, err := domain.NewArenaProjectionRevision(
				source.ID(), source.TournamentID(), source.Artifact(), source.RevisionNo(),
				source.PreviousRevisionID(),
				fixture.authority.CurrentScore.SourceProjection.CreatedAt().Add(-time.Second),
				[]byte("backwards Series result"),
			)
			require.NoError(t, err)
			fixture.command.SeriesResult.ExpectedSourceProjection = projection.Revision()
			fixture.authority.SeriesResult.SourceProjection = projection.Revision()
			_, err = PlanSeriesResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidSeriesResultCascade)
		})
	})

	t.Run("fingerprints payloads and isolates caller mutation", func(t *testing.T) {
		first := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)
		second := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)
		source := second.command.SeriesResult.ExpectedSourceProjection
		projection, err := domain.NewArenaProjectionRevision(
			source.ID(), source.TournamentID(), source.Artifact(), source.RevisionNo(),
			source.PreviousRevisionID(), source.CreatedAt(), []byte("different Series payload"),
		)
		require.NoError(t, err)
		second.command.SeriesResult.ExpectedSourceProjection = projection.Revision()
		second.authority.SeriesResult.SourceProjection = projection.Revision()
		firstPlan, err := PlanSeriesResultCascade(first.command, first.authority, first.recordedAt)
		require.NoError(t, err)
		secondPlan, err := PlanSeriesResultCascade(second.command, second.authority, second.recordedAt)
		require.NoError(t, err)
		require.NotEqual(t, firstPlan.Condition().IdempotencyKey(), secondPlan.Condition().IdempotencyKey())

		first.command.SeriesResult.Outcome.ScoreRevisionID = nil
		first.authority.CurrentScore.Attempts[0].State = domain.ArenaGameStateVoid
		dependencies := firstPlan.Dependencies()
		dependencies[0].SourceRevisionID = domain.ArenaDerivedRevisionID(task054ID(51))
		condition := firstPlan.Condition()
		condition.expectedSeries.Slots[0].Attempts[0].State = domain.ArenaGameStateVoid
		require.NoError(t, firstPlan.Validate())
	})

	t.Run("includes the complete expected Series in the idempotency key", func(t *testing.T) {
		first := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)
		second := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)
		second.authority.SeriesResult.PersistedSeries.Slots[0].Category = domain.CategoryCrypto
		second.authority.SeriesResult.ProjectedSeries.Slots[0].Category = domain.CategoryCrypto

		firstPlan, err := PlanSeriesResultCascade(first.command, first.authority, first.recordedAt)
		require.NoError(t, err)
		secondPlan, err := PlanSeriesResultCascade(second.command, second.authority, second.recordedAt)
		require.NoError(t, err)
		require.NotEqual(t, firstPlan.Condition().IdempotencyKey(), secondPlan.Condition().IdempotencyKey())
	})

	t.Run("rejects correction-style successor commands", func(t *testing.T) {
		fixture := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)
		current := domain.ArenaOfficialResultRevisionID(task054ID(53))
		fixture.command.SeriesResult.ExpectedCurrentRevisionID = &current

		_, err := PlanSeriesResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidSeriesResultCascade)
	})

	t.Run("supports concurrent deterministic replay", func(t *testing.T) {
		fixture := task054SeriesCascadeFixture(t, domain.ArenaSeriesStateCompleted)
		baseline, err := PlanSeriesResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		repeated, err := PlanSeriesResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.Equal(t, baseline, repeated)

		const callers = 24
		var group sync.WaitGroup
		plans := make(chan SeriesResultCascadePlan, callers)
		errs := make(chan error, callers)
		for range callers {
			group.Add(1)
			go func() {
				defer group.Done()
				plan, planErr := PlanSeriesResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
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

type task054SeriesCascadeCase struct {
	command    SeriesResultCascadeCommand
	authority  SeriesResultCascadeAuthority
	recordedAt time.Time
}

func task054SeriesCascadeFixture(
	t *testing.T,
	terminalState domain.ArenaSeriesState,
) task054SeriesCascadeCase {
	t.Helper()
	gameState := domain.ArenaGameStateCompleted
	if terminalState == domain.ArenaSeriesStateCancelled {
		gameState = domain.ArenaGameStateCancelled
	}
	gameFixture := task054GameCascadeFixture(t, gameState)
	gamePlan, err := PlanGameResultCascade(
		gameFixture.command,
		gameFixture.authority,
		gameFixture.recordedAt,
	)
	require.NoError(t, err)
	persisted := cloneArenaSeries(gameFixture.authority.GameResult.ProjectedSeries)
	currentScore := gamePlan.ScoreRevision().Revision().Head()
	resultID := domain.ArenaOfficialResultRevisionID(task054ID(30))
	projected := cloneArenaSeries(persisted)
	projected.State = terminalState
	projected.CurrentResultRevisionID = officialResultRevisionIDPointer(resultID)
	reason := ArenaSeriesResultReasonScoreComplete
	if terminalState == domain.ArenaSeriesStateCompleted {
		projected.WinnerID = task053UUIDPointer(projected.FirstParticipantID)
	} else {
		reason = ArenaSeriesResultReasonSeriesCancelled
		projected.WinnerID = nil
	}
	require.NoError(t, persisted.Validate())
	require.NoError(t, projected.Validate())
	projection, err := domain.NewArenaProjectionRevision(
		domain.ArenaDerivedRevisionID(task054ID(31)),
		persisted.TournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesResult, EntityID: persisted.ID},
		1,
		nil,
		gameFixture.recordedAt.Add(30*time.Second),
		[]byte("series result"),
	)
	require.NoError(t, err)
	source := projection.Revision()
	command := OfficialResultRevisionCommand{
		Scope: OfficialResultScope{
			TournamentID: persisted.TournamentID,
			SeriesID:     persisted.ID,
			Kind:         OfficialResultSubjectSeries,
		},
		CommandID:                task054ID(32),
		RevisionID:               resultID,
		Actor:                    ArenaResultActor{Kind: ArenaResultActorServer},
		ExpectedSourceProjection: source,
		Outcome: OfficialResultOutcome{
			SeriesState:     terminalState,
			SeriesReason:    reason,
			WinnerID:        task054UUIDPointerOrNil(projected.WinnerID),
			ScoreRevisionID: seriesScoreRevisionIDPointer(currentScore.ID),
		},
	}
	return task054SeriesCascadeCase{
		command: SeriesResultCascadeCommand{
			AuditEventID: task054ID(33),
			SeriesResult: command,
		},
		authority: SeriesResultCascadeAuthority{
			SeriesResult: OfficialResultRevisionAuthority{
				Scope:                 command.Scope,
				PersistedSeries:       persisted,
				ProjectedSeries:       projected,
				ProjectedSeriesReason: reason,
				SourceProjection:      source,
				SeriesRevision:        8,
			},
			CurrentScore: currentScore,
		},
		recordedAt: gameFixture.recordedAt.Add(time.Minute),
	}
}
