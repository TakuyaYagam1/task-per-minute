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

func TestGameResultCascade(t *testing.T) {
	t.Run("plans one atomic Game result and score successor", func(t *testing.T) {
		fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)

		plan, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.NoError(t, plan.Validate())

		game := plan.GameResult().Revision()
		score := plan.ScoreRevision().Revision()
		require.Equal(t, fixture.command.GameResult.RevisionID, game.ID())
		require.Equal(t, fixture.command.ScoreRevision.RevisionID, score.ID())
		require.Equal(t, domain.ArenaSeriesScore{FirstParticipantWins: 1}, score.Score())
		require.Equal(t, []SeriesScoreAttemptReference{*fixture.command.ScoreRevision.Attempt}, score.Attempts())
		require.Equal(t, domain.ArenaRevisionDependency{
			SourceRevisionID:  game.SourceProjection().ID(),
			DerivedRevisionID: score.SourceProjection().ID(),
		}, plan.Dependency())
		require.Equal(t, fixture.command.AuditEventID, plan.Audit().EventID)
		require.Equal(t, fixture.command.GameResult.CommandID, plan.Condition().CommandID())
		require.NotEqual(t, [32]byte{}, plan.Condition().IdempotencyKey())
	})

	t.Run("rejects a score projection that predates the Game result projection", func(t *testing.T) {
		fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		source := fixture.command.GameResult.ExpectedSourceProjection
		scoreSource := fixture.command.ScoreRevision.ExpectedSourceProjection
		projection, err := domain.NewArenaProjectionRevision(
			source.ID(), source.TournamentID(), source.Artifact(), source.RevisionNo(),
			source.PreviousRevisionID(), scoreSource.CreatedAt().Add(time.Second),
			[]byte("late Game result projection"),
		)
		require.NoError(t, err)
		fixture.command.GameResult.ExpectedSourceProjection = projection.Revision()
		fixture.authority.GameResult.SourceProjection = projection.Revision()

		_, err = PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidGameResultCascade)
	})

	t.Run("rejects row revisions that cannot be advanced", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*GameResultCascadeAuthority)
		}{
			{
				name: "Series revision overflow",
				mutate: func(authority *GameResultCascadeAuthority) {
					authority.GameResult.SeriesRevision = ArenaSeriesRowRevision(math.MaxInt64)
					authority.ScoreRevision.SeriesRevision = ArenaSeriesRowRevision(math.MaxInt64)
				},
			},
			{
				name: "attempt revision overflow",
				mutate: func(authority *GameResultCascadeAuthority) {
					authority.GameResult.AttemptRevision = ArenaAttemptRowRevision(math.MaxInt64)
					authority.ScoreRevision.AttemptRevision = ArenaAttemptRowRevision(math.MaxInt64)
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
				test.mutate(&fixture.authority)
				_, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
				require.ErrorIs(t, err, ErrInvalidGameResultCascade)
			})
		}
	})

	t.Run("rejects an audit identity aliased with an operator", func(t *testing.T) {
		fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		fixture.command.GameResult.Actor = ArenaResultActor{
			Kind: ArenaResultActorOperator, PrincipalID: task053UUIDPointer(fixture.command.AuditEventID),
		}
		fixture.command.ScoreRevision.Actor = fixture.command.GameResult.Actor

		_, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidGameResultCascade)
	})

	t.Run("rejects identities aliased with current score lineage", func(t *testing.T) {
		fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		fixture.command.AuditEventID = fixture.authority.ScoreRevision.CurrentHead.CommandID

		_, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidGameResultCascade)
	})

	t.Run("rejects source UUID collisions with current score lineage", func(t *testing.T) {
		t.Run("current source", func(t *testing.T) {
			fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
			currentSource := fixture.authority.ScoreRevision.CurrentHead.SourceProjection
			gameSource := task054Projection(
				t,
				currentSource.ID().UUID(),
				currentSource.TournamentID(),
				domain.ArenaArtifactKindGameResult,
				fixture.command.GameResult.Scope.GameID,
				1,
				nil,
				"colliding Game source",
			)
			fixture.command.GameResult.ExpectedSourceProjection = gameSource
			fixture.authority.GameResult.SourceProjection = gameSource

			_, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidGameResultCascade)
		})

		t.Run("predecessor source", func(t *testing.T) {
			fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
			tournamentID := fixture.command.GameResult.Scope.TournamentID
			seriesID := fixture.command.GameResult.Scope.SeriesID
			gameID := fixture.command.GameResult.Scope.GameID
			predecessorID := domain.ArenaDerivedRevisionID(task054ID(54))
			headSource := task054Projection(
				t, task054ID(55), tournamentID, domain.ArenaArtifactKindSeriesScore,
				seriesID, 2, &predecessorID, "current score head source",
			)
			nextHeadSourceID := headSource.ID()
			nextScoreSource := task054Projection(
				t, task054ID(13), tournamentID, domain.ArenaArtifactKindSeriesScore,
				seriesID, 3, &nextHeadSourceID, "next score source",
			)
			gameSource := task054Projection(
				t, predecessorID.UUID(), tournamentID, domain.ArenaArtifactKindGameResult,
				gameID, 1, nil, "predecessor-colliding Game source",
			)
			fixture.authority.ScoreRevision.CurrentHead.SourceProjection = headSource
			fixture.command.ScoreRevision.ExpectedSourceProjection = nextScoreSource
			fixture.authority.ScoreRevision.SourceProjection = nextScoreSource
			fixture.command.GameResult.ExpectedSourceProjection = gameSource
			fixture.authority.GameResult.SourceProjection = gameSource

			_, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
			require.ErrorIs(t, err, ErrInvalidGameResultCascade)
		})
	})

	t.Run("rejects a sibling source lineage cycle", func(t *testing.T) {
		fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		scoreSource := fixture.command.ScoreRevision.ExpectedSourceProjection
		predecessorID := scoreSource.ID()
		projection, err := domain.NewArenaProjectionRevision(
			fixture.command.GameResult.ExpectedSourceProjection.ID(),
			fixture.command.GameResult.Scope.TournamentID,
			domain.ArenaArtifactRef{
				Kind:     domain.ArenaArtifactKindGameResult,
				EntityID: fixture.command.GameResult.Scope.GameID,
			},
			2,
			&predecessorID,
			scoreSource.CreatedAt(),
			[]byte("cyclic Game source"),
		)
		require.NoError(t, err)
		fixture.command.GameResult.ExpectedSourceProjection = projection.Revision()
		fixture.authority.GameResult.SourceProjection = projection.Revision()

		_, err = PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidGameResultCascade)
	})

	t.Run("rejects a rekeyed plan audit aliased with current score lineage", func(t *testing.T) {
		fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		plan, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)

		plan.audit.EventID = plan.condition.expectedScoreHead.CommandID
		plan.condition.auditEventID = plan.audit.EventID
		plan.condition.idempotencyKey = gameResultCascadeIdempotencyKey(
			plan.audit,
			plan.gameResult.Revision(),
			plan.scoreRevision.Revision(),
			plan.condition.expectedSeries,
			plan.condition.expectedScoreHead,
			plan.condition.expectedSeriesRevision,
			plan.condition.expectedAttemptRevision,
		)

		require.ErrorIs(t, plan.Validate(), ErrInvalidGameResultCascade)
	})

	t.Run("includes projection payload identity in the idempotency key", func(t *testing.T) {
		first := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		second := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		source := second.command.ScoreRevision.ExpectedSourceProjection
		projection, err := domain.NewArenaProjectionRevision(
			source.ID(),
			source.TournamentID(),
			source.Artifact(),
			source.RevisionNo(),
			source.PreviousRevisionID(),
			source.CreatedAt(),
			[]byte("different score payload"),
		)
		require.NoError(t, err)
		second.command.ScoreRevision.ExpectedSourceProjection = projection.Revision()
		second.authority.ScoreRevision.SourceProjection = projection.Revision()

		firstPlan, err := PlanGameResultCascade(first.command, first.authority, first.recordedAt)
		require.NoError(t, err)
		secondPlan, err := PlanGameResultCascade(second.command, second.authority, second.recordedAt)
		require.NoError(t, err)
		require.NotEqual(t, firstPlan.Condition().IdempotencyKey(), secondPlan.Condition().IdempotencyKey())
	})

	t.Run("includes the complete current score head in the idempotency key", func(t *testing.T) {
		first := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		second := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		second.authority.ScoreRevision.CurrentHead.CommandID = task054ID(43)

		firstPlan, err := PlanGameResultCascade(first.command, first.authority, first.recordedAt)
		require.NoError(t, err)
		secondPlan, err := PlanGameResultCascade(second.command, second.authority, second.recordedAt)
		require.NoError(t, err)
		require.NotEqual(t, firstPlan.Condition().IdempotencyKey(), secondPlan.Condition().IdempotencyKey())

		firstPlan.condition.expectedScoreHead.CommandID = task054ID(44)
		require.ErrorIs(t, firstPlan.Validate(), ErrInvalidGameResultCascade)
	})

	t.Run("preserves a fresh score revision when the numeric score is unchanged", func(t *testing.T) {
		fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCancelled)

		plan, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.Equal(t, domain.ArenaSeriesScore{}, plan.ScoreRevision().Revision().Score())
		require.NotEqual(
			t,
			fixture.authority.ScoreRevision.CurrentHead.ID,
			plan.ScoreRevision().Revision().ID(),
		)
		require.Equal(t, fixture.command.GameResult.RevisionID,
			plan.ScoreRevision().Revision().Attempts()[0].CurrentGameResultRevisionID)
	})

	t.Run("rejects spliced child snapshots rows and causal fields", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*GameResultCascadeCommand, *GameResultCascadeAuthority)
		}{
			{
				name: "different child command",
				mutate: func(command *GameResultCascadeCommand, _ *GameResultCascadeAuthority) {
					command.ScoreRevision.CommandID = task054ID(40)
				},
			},
			{
				name: "different persisted snapshot",
				mutate: func(_ *GameResultCascadeCommand, authority *GameResultCascadeAuthority) {
					authority.ScoreRevision.PersistedSeries.State = domain.ArenaSeriesStateReady
				},
			},
			{
				name: "different row revision",
				mutate: func(_ *GameResultCascadeCommand, authority *GameResultCascadeAuthority) {
					authority.ScoreRevision.SeriesRevision++
				},
			},
			{
				name: "wrong operation",
				mutate: func(command *GameResultCascadeCommand, _ *GameResultCascadeAuthority) {
					command.ScoreRevision.Operation = SeriesScoreRevisionOperationReplaceResult
				},
			},
			{
				name: "wrong result reference",
				mutate: func(command *GameResultCascadeCommand, _ *GameResultCascadeAuthority) {
					command.ScoreRevision.Attempt.CurrentGameResultRevisionID =
						domain.ArenaOfficialResultRevisionID(task054ID(41))
				},
			},
			{
				name: "early Series result",
				mutate: func(_ *GameResultCascadeCommand, authority *GameResultCascadeAuthority) {
					resultID := domain.ArenaOfficialResultRevisionID(task054ID(42))
					authority.GameResult.ProjectedSeries.CurrentResultRevisionID = &resultID
					authority.ScoreRevision.ProjectedSeries.CurrentResultRevisionID = &resultID
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
				test.mutate(&fixture.command, &fixture.authority)
				_, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
				require.Error(t, err)
			})
		}
	})

	t.Run("rejects correction-style successor commands", func(t *testing.T) {
		fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		current := domain.ArenaOfficialResultRevisionID(task054ID(45))
		fixture.command.GameResult.ExpectedCurrentRevisionID = &current

		_, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.ErrorIs(t, err, ErrInvalidGameResultCascade)
	})

	t.Run("is deterministic isolated from mutation and safe for concurrent reads", func(t *testing.T) {
		fixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		replayFixture := task054GameCascadeFixture(t, domain.ArenaGameStateCompleted)
		baseline, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		second, err := PlanGameResultCascade(fixture.command, fixture.authority, fixture.recordedAt)
		require.NoError(t, err)
		require.Equal(t, baseline, second)

		fixture.command.ScoreRevision.Attempt.State = domain.ArenaGameStateVoid
		fixture.authority.GameResult.ProjectedSeries.Slots[0].Attempts[0].State = domain.ArenaGameStateVoid
		condition := baseline.Condition()
		condition.expectedSeries.Slots[0].Attempts[0].State = domain.ArenaGameStateVoid
		score := baseline.ScoreRevision().Revision().Attempts()
		score[0].State = domain.ArenaGameStateVoid
		require.NoError(t, baseline.Validate())
		require.Equal(t, domain.ArenaGameStateCompleted,
			baseline.ScoreRevision().Revision().Attempts()[0].State)

		const callers = 24
		var group sync.WaitGroup
		plans := make(chan GameResultCascadePlan, callers)
		errs := make(chan error, callers)
		for range callers {
			group.Add(1)
			go func() {
				defer group.Done()
				plan, planErr := PlanGameResultCascade(
					replayFixture.command,
					replayFixture.authority,
					replayFixture.recordedAt,
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

type task054GameCascadeCase struct {
	command    GameResultCascadeCommand
	authority  GameResultCascadeAuthority
	recordedAt time.Time
}

func task054GameCascadeFixture(
	t *testing.T,
	terminalState domain.ArenaGameState,
) task054GameCascadeCase {
	t.Helper()
	tournamentID := task054ID(1)
	seriesID := task054ID(2)
	firstID := task054ID(3)
	secondID := task054ID(4)
	slotID := task054ID(5)
	gameID := task054ID(6)
	initialScoreID := domain.ArenaSeriesScoreRevisionID(task054ID(7))
	gameResultID := domain.ArenaOfficialResultRevisionID(task054ID(8))
	nextScoreID := domain.ArenaSeriesScoreRevisionID(task054ID(9))
	initialScoreSource := task054Projection(
		t, task054ID(10), tournamentID, domain.ArenaArtifactKindSeriesScore, seriesID, 1, nil, "initial score",
	)
	currentScore := SeriesScoreRevisionHead{
		Scope:               SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID},
		ID:                  initialScoreID,
		Ordinal:             1,
		Operation:           SeriesScoreRevisionOperationInitialize,
		CommandID:           task054ID(11),
		Actor:               ArenaResultActor{Kind: ArenaResultActorServer},
		FirstParticipantID:  firstID,
		SecondParticipantID: secondID,
		Format:              domain.ArenaSeriesFormatBO1,
		SourceProjection:    initialScoreSource,
		RecordedAt:          time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC),
	}
	require.NoError(t, currentScore.Validate())

	persisted := domain.ArenaSeries{
		ID:                     seriesID,
		TournamentID:           tournamentID,
		FirstParticipantID:     firstID,
		SecondParticipantID:    secondID,
		Format:                 domain.ArenaSeriesFormatBO1,
		State:                  domain.ArenaSeriesStateActive,
		CurrentScoreRevisionID: seriesScoreRevisionIDPointer(initialScoreID),
		Slots: []domain.ArenaGameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			Attempts: []domain.ArenaGame{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.ArenaGameStatePlanned,
			}},
		}},
	}
	projected := cloneArenaSeries(persisted)
	projected.CurrentScoreRevisionID = seriesScoreRevisionIDPointer(nextScoreID)
	game := &projected.Slots[0].Attempts[0]
	game.State = terminalState
	game.ResultRevisionID = officialResultRevisionIDPointer(gameResultID)
	if terminalState == domain.ArenaGameStateCompleted {
		game.ResultReason = domain.ArenaGameResultReasonSolved
		game.WinnerID = task053UUIDPointer(firstID)
		projected.Score.FirstParticipantWins = 1
	} else {
		game.ResultReason = domain.ArenaGameResultReasonNoShow
	}
	require.NoError(t, persisted.Validate())
	require.NoError(t, projected.Validate())

	gameSource := task054Projection(
		t, task054ID(12), tournamentID, domain.ArenaArtifactKindGameResult, gameID, 1, nil, "game result",
	)
	previousScoreSourceID := initialScoreSource.ID()
	scoreSource := task054Projection(
		t, task054ID(13), tournamentID, domain.ArenaArtifactKindSeriesScore, seriesID, 2,
		&previousScoreSourceID, "score successor",
	)
	commandID := task054ID(14)
	actor := ArenaResultActor{Kind: ArenaResultActorServer}
	attempt := SeriesScoreAttemptReference{
		SlotID:                      slotID,
		SlotPosition:                1,
		GameID:                      gameID,
		AttemptNo:                   1,
		State:                       terminalState,
		Reason:                      game.ResultReason,
		WinnerID:                    task054UUIDPointerOrNil(game.WinnerID),
		CurrentGameResultRevisionID: gameResultID,
	}
	gameCommand := OfficialResultRevisionCommand{
		Scope: OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID, GameID: gameID, Kind: OfficialResultSubjectGame,
		},
		CommandID: commandID, RevisionID: gameResultID, Actor: actor,
		ExpectedSourceProjection: gameSource,
		Outcome: OfficialResultOutcome{
			GameState: terminalState, GameReason: game.ResultReason,
			WinnerID: task054UUIDPointerOrNil(game.WinnerID),
		},
	}
	scoreCommand := SeriesScoreRevisionCommand{
		Scope:     SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID},
		Operation: SeriesScoreRevisionOperationAppendAttempt,
		CommandID: commandID, RevisionID: nextScoreID, Actor: actor,
		ExpectedCurrentRevisionID: seriesScoreRevisionIDPointer(initialScoreID),
		ExpectedSourceProjection:  scoreSource,
		Attempt:                   &attempt,
	}
	return task054GameCascadeCase{
		command: GameResultCascadeCommand{
			AuditEventID: task054ID(15), GameResult: gameCommand, ScoreRevision: scoreCommand,
		},
		authority: GameResultCascadeAuthority{
			GameResult: OfficialResultRevisionAuthority{
				Scope: gameCommand.Scope, PersistedSeries: persisted, ProjectedSeries: projected,
				SourceProjection: gameSource, SeriesRevision: 7, AttemptRevision: 3,
			},
			ScoreRevision: SeriesScoreRevisionAuthority{
				Scope: scoreCommand.Scope, PersistedSeries: persisted, ProjectedSeries: projected,
				SourceProjection: scoreSource, CurrentHead: &currentScore,
				SeriesRevision: 7, AttemptRevision: 3,
			},
		},
		recordedAt: time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC),
	}
}

func task054UUIDPointerOrNil(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	return task053UUIDPointer(*value)
}

func task054ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("10000000-0000-4000-8000-%012d", number))
}

func task054Projection(
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
		time.Date(2026, time.September, 1, 8, revisionNo, 0, 0, time.UTC),
		[]byte(payload),
	)
	require.NoError(t, err)
	return projection.Revision()
}
