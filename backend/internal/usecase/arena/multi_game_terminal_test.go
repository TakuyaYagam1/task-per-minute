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

func TestCanonicalMultiGameTerminalization(t *testing.T) {
	t.Run("orders all Game envelopes before the terminal Series evidence", func(t *testing.T) {
		fixture := task054MultiTerminalFixture(t)

		plan, err := PlanCanonicalMultiGameTerminalization(
			fixture.command,
			fixture.authority,
			fixture.recordedAt,
		)
		require.NoError(t, err)
		require.NoError(t, plan.Validate())

		games := plan.GameCascades()
		require.Len(t, games, 3)
		for index, game := range games {
			attempt := game.ScoreRevision().Revision().CommandAttempt()
			require.Equal(t, index+1, attempt.SlotPosition)
			require.Equal(t, domain.ArenaSeriesScore{}, game.ScoreRevision().Revision().Score())
			require.Len(t, game.ScoreRevision().Revision().Attempts(), index+1)
		}
		series := plan.SeriesResult()
		lastScore := games[len(games)-1].ScoreRevision().Revision()
		require.Equal(t, lastScore.ID(), *series.SeriesResult().Revision().Outcome().ScoreRevisionID)
		require.Equal(t, lastScore.SourceProjection().ID(), series.Dependency().SourceRevisionID)

		envelopes := plan.Envelopes()
		require.Len(t, envelopes, 4)
		for index := range games {
			require.Equal(t, CanonicalTerminalizationEnvelopeGame, envelopes[index].Kind())
		}
		require.Equal(t, CanonicalTerminalizationEnvelopeSeries, envelopes[len(envelopes)-1].Kind())
		require.Equal(t, fixture.command.CommandID, plan.Condition().CommandID())
		require.NotEqual(t, [32]byte{}, plan.Condition().IdempotencyKey())
	})

	t.Run("rejects terminalization that omits an unstarted Game", func(t *testing.T) {
		fixture := task054MultiTerminalFixture(t)
		var omitted GameResultCascadeAuthority
		commands := make([]GameResultCascadeCommand, 0, 2)
		for _, command := range fixture.command.Games {
			if command.ScoreRevision.Attempt.SlotPosition != 3 {
				commands = append(commands, command)
			}
		}
		authorities := make([]GameResultCascadeAuthority, 0, 2)
		for _, authority := range fixture.authority.Games {
			game, found := findArenaSeriesGame(
				authority.GameResult.PersistedSeries,
				authority.GameResult.Scope.GameID,
			)
			require.True(t, found)
			if game.SlotID == authority.GameResult.PersistedSeries.Slots[2].ID {
				omitted = authority
				continue
			}
			authorities = append(authorities, authority)
		}
		require.NotNil(t, omitted.ScoreRevision.CurrentHead)
		fixture.command.Games = commands
		fixture.authority.Games = authorities
		fixture.authority.SeriesResult.CurrentScore = omitted.ScoreRevision.CurrentHead.Clone()
		fixture.authority.SeriesResult.SeriesResult.PersistedSeries =
			cloneArenaSeries(omitted.GameResult.PersistedSeries)
		projected := cloneArenaSeries(omitted.GameResult.PersistedSeries)
		projected.State = domain.ArenaSeriesStateCancelled
		projected.CurrentResultRevisionID = officialResultRevisionIDPointer(
			fixture.command.SeriesResult.SeriesResult.RevisionID,
		)
		fixture.authority.SeriesResult.SeriesResult.ProjectedSeries = projected
		fixture.command.SeriesResult.SeriesResult.Outcome.ScoreRevisionID = seriesScoreRevisionIDPointer(
			omitted.ScoreRevision.CurrentHead.ID,
		)

		_, err := PlanCanonicalMultiGameTerminalization(
			fixture.command,
			fixture.authority,
			fixture.recordedAt,
		)
		require.ErrorIs(t, err, ErrInvalidCanonicalMultiGameTerminalization)
	})

	t.Run("rejects mixed actors and backwards dependency edges", func(t *testing.T) {
		t.Run("mixed actors", func(t *testing.T) {
			fixture := task054MultiTerminalFixture(t)
			fixture.command.SeriesResult.SeriesResult.Actor = ArenaResultActor{
				Kind:        ArenaResultActorOperator,
				PrincipalID: task053UUIDPointer(task054ID(310)),
			}
			_, err := PlanCanonicalMultiGameTerminalization(
				fixture.command, fixture.authority, fixture.recordedAt,
			)
			require.ErrorIs(t, err, ErrInvalidCanonicalMultiGameTerminalization)
		})

		t.Run("backwards Game to score edge", func(t *testing.T) {
			fixture := task054MultiTerminalFixture(t)
			command := &fixture.command.Games[1]
			var authority *GameResultCascadeAuthority
			for index := range fixture.authority.Games {
				if fixture.authority.Games[index].GameResult.Scope.GameID == command.GameResult.Scope.GameID {
					authority = &fixture.authority.Games[index]
					break
				}
			}
			require.NotNil(t, authority)
			scoreSource := command.ScoreRevision.ExpectedSourceProjection
			gameSource := command.GameResult.ExpectedSourceProjection
			projection, err := domain.NewArenaProjectionRevision(
				gameSource.ID(), gameSource.TournamentID(), gameSource.Artifact(),
				gameSource.RevisionNo(), gameSource.PreviousRevisionID(),
				scoreSource.CreatedAt().Add(time.Second), []byte("late Game result"),
			)
			require.NoError(t, err)
			command.GameResult.ExpectedSourceProjection = projection.Revision()
			authority.GameResult.SourceProjection = projection.Revision()

			_, err = PlanCanonicalMultiGameTerminalization(
				fixture.command, fixture.authority, fixture.recordedAt,
			)
			require.ErrorIs(t, err, ErrInvalidCanonicalMultiGameTerminalization)
		})
	})

	t.Run("rejects Game source UUID collisions with score lineage", func(t *testing.T) {
		tests := []struct {
			name     string
			position int
			sourceID func(SeriesScoreRevisionHead) domain.ArenaDerivedRevisionID
		}{
			{
				name: "current source", position: 1,
				sourceID: func(head SeriesScoreRevisionHead) domain.ArenaDerivedRevisionID {
					return head.SourceProjection.ID()
				},
			},
			{
				name: "predecessor source", position: 2,
				sourceID: func(head SeriesScoreRevisionHead) domain.ArenaDerivedRevisionID {
					require.NotNil(t, head.SourceProjection.PreviousRevisionID())
					return *head.SourceProjection.PreviousRevisionID()
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				fixture := task054MultiTerminalFixture(t)
				var command *GameResultCascadeCommand
				for index := range fixture.command.Games {
					if fixture.command.Games[index].ScoreRevision.Attempt.SlotPosition == test.position {
						command = &fixture.command.Games[index]
						break
					}
				}
				require.NotNil(t, command)
				var authority *GameResultCascadeAuthority
				for index := range fixture.authority.Games {
					if fixture.authority.Games[index].GameResult.Scope.GameID == command.GameResult.Scope.GameID {
						authority = &fixture.authority.Games[index]
						break
					}
				}
				require.NotNil(t, authority)
				sourceID := test.sourceID(*authority.ScoreRevision.CurrentHead)
				gameSource := task054Projection(
					t,
					sourceID.UUID(),
					command.GameResult.Scope.TournamentID,
					domain.ArenaArtifactKindGameResult,
					command.GameResult.Scope.GameID,
					1,
					nil,
					"colliding multi Game source",
				)
				command.GameResult.ExpectedSourceProjection = gameSource
				authority.GameResult.SourceProjection = gameSource

				_, err := PlanCanonicalMultiGameTerminalization(
					fixture.command, fixture.authority, fixture.recordedAt,
				)
				require.ErrorIs(t, err, ErrInvalidCanonicalMultiGameTerminalization)
			})
		}
	})

	t.Run("rejects replay of the initial score-head command", func(t *testing.T) {
		fixture := task054MultiTerminalFixture(t)
		var initialCommandID uuid.UUID
		for _, authority := range fixture.authority.Games {
			attempt, found := findArenaSeriesGame(
				authority.GameResult.PersistedSeries,
				authority.GameResult.Scope.GameID,
			)
			require.True(t, found)
			if attempt.SlotID == authority.GameResult.PersistedSeries.Slots[0].ID {
				initialCommandID = authority.ScoreRevision.CurrentHead.CommandID
				break
			}
		}
		require.NotEqual(t, uuid.Nil, initialCommandID)
		for index := range fixture.command.Games {
			command := &fixture.command.Games[index]
			if command.ScoreRevision.Attempt.SlotPosition == 2 {
				command.GameResult.CommandID = initialCommandID
				command.ScoreRevision.CommandID = initialCommandID
			}
		}
		for index := range fixture.authority.Games {
			authority := &fixture.authority.Games[index]
			game, found := findArenaSeriesGame(
				authority.GameResult.PersistedSeries,
				authority.GameResult.Scope.GameID,
			)
			require.True(t, found)
			if game.SlotID == authority.GameResult.PersistedSeries.Slots[2].ID {
				authority.ScoreRevision.CurrentHead.CommandID = initialCommandID
			}
		}

		_, err := PlanCanonicalMultiGameTerminalization(
			fixture.command, fixture.authority, fixture.recordedAt,
		)
		require.ErrorIs(t, err, ErrInvalidCanonicalMultiGameTerminalization)
	})

	t.Run("rejects N-3 score revision ABA", func(t *testing.T) {
		fixture := task054MultiTerminalFixture(t)
		var initialScoreID domain.ArenaSeriesScoreRevisionID
		for _, authority := range fixture.authority.Games {
			game, found := findArenaSeriesGame(
				authority.GameResult.PersistedSeries,
				authority.GameResult.Scope.GameID,
			)
			require.True(t, found)
			if game.SlotID == authority.GameResult.PersistedSeries.Slots[0].ID {
				initialScoreID = authority.ScoreRevision.CurrentHead.ID
				break
			}
		}
		require.False(t, initialScoreID.IsZero())
		for index := range fixture.command.Games {
			command := &fixture.command.Games[index]
			if command.ScoreRevision.Attempt.SlotPosition == 3 {
				command.ScoreRevision.RevisionID = initialScoreID
			}
		}
		for index := range fixture.authority.Games {
			authority := &fixture.authority.Games[index]
			game, found := findArenaSeriesGame(
				authority.GameResult.PersistedSeries,
				authority.GameResult.Scope.GameID,
			)
			require.True(t, found)
			if game.SlotID == authority.GameResult.PersistedSeries.Slots[2].ID {
				authority.GameResult.ProjectedSeries.CurrentScoreRevisionID =
					seriesScoreRevisionIDPointer(initialScoreID)
				authority.ScoreRevision.ProjectedSeries.CurrentScoreRevisionID =
					seriesScoreRevisionIDPointer(initialScoreID)
			}
		}
		fixture.command.SeriesResult.SeriesResult.Outcome.ScoreRevisionID =
			seriesScoreRevisionIDPointer(initialScoreID)
		fixture.authority.SeriesResult.CurrentScore.ID = initialScoreID
		fixture.authority.SeriesResult.SeriesResult.PersistedSeries.CurrentScoreRevisionID =
			seriesScoreRevisionIDPointer(initialScoreID)
		fixture.authority.SeriesResult.SeriesResult.ProjectedSeries.CurrentScoreRevisionID =
			seriesScoreRevisionIDPointer(initialScoreID)

		_, err := PlanCanonicalMultiGameTerminalization(
			fixture.command, fixture.authority, fixture.recordedAt,
		)
		require.ErrorIs(t, err, ErrInvalidCanonicalMultiGameTerminalization)
	})

	t.Run("rejects a predecessor source aliased with a later audit", func(t *testing.T) {
		fixture := task054MultiTerminalFixture(t)
		var initialSourceID uuid.UUID
		for _, authority := range fixture.authority.Games {
			attempt, found := findArenaSeriesGame(
				authority.GameResult.PersistedSeries,
				authority.GameResult.Scope.GameID,
			)
			require.True(t, found)
			if attempt.SlotID == authority.GameResult.PersistedSeries.Slots[0].ID {
				initialSourceID = authority.ScoreRevision.CurrentHead.SourceProjection.ID().UUID()
				break
			}
		}
		require.NotEqual(t, uuid.Nil, initialSourceID)
		for index := range fixture.command.Games {
			if fixture.command.Games[index].ScoreRevision.Attempt.SlotPosition == 3 {
				fixture.command.Games[index].AuditEventID = initialSourceID
				break
			}
		}

		_, err := PlanCanonicalMultiGameTerminalization(
			fixture.command, fixture.authority, fixture.recordedAt,
		)
		require.ErrorIs(t, err, ErrInvalidCanonicalMultiGameTerminalization)
	})

	t.Run("rejects aggregate row revisions that cannot be advanced", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*CanonicalMultiGameTerminalizationPlan)
		}{
			{
				name: "Series revision overflow",
				mutate: func(plan *CanonicalMultiGameTerminalizationPlan) {
					plan.condition.expectedSeriesRevision = ArenaSeriesRowRevision(math.MaxInt64)
				},
			},
			{
				name: "attempt revision overflow",
				mutate: func(plan *CanonicalMultiGameTerminalizationPlan) {
					plan.condition.expectedGameAttempts[0].ExpectedAttemptRevision =
						ArenaAttemptRowRevision(math.MaxInt64)
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				fixture := task054MultiTerminalFixture(t)
				plan, err := PlanCanonicalMultiGameTerminalization(
					fixture.command, fixture.authority, fixture.recordedAt,
				)
				require.NoError(t, err)
				test.mutate(&plan)
				require.ErrorIs(t, plan.Validate(), ErrInvalidCanonicalMultiGameTerminalization)
			})
		}
	})

	t.Run("rejects a final Series snapshot spliced from another score", func(t *testing.T) {
		fixture := task054MultiTerminalFixture(t)
		plan, err := PlanCanonicalMultiGameTerminalization(
			fixture.command, fixture.authority, fixture.recordedAt,
		)
		require.NoError(t, err)
		plan.finalSeries.Score.FirstParticipantWins = 1

		require.ErrorIs(t, plan.Validate(), ErrInvalidCanonicalMultiGameTerminalization)
	})

	t.Run("rejects a rekeyed terminal Series timestamp", func(t *testing.T) {
		fixture := task054MultiTerminalFixture(t)
		plan, err := PlanCanonicalMultiGameTerminalization(
			fixture.command, fixture.authority, fixture.recordedAt,
		)
		require.NoError(t, err)

		movedAt := fixture.recordedAt.Add(time.Minute)
		plan.seriesResult.result.revision.recordedAt = movedAt
		plan.seriesResult.audit.RecordedAt = movedAt
		plan.seriesResult.condition.idempotencyKey = seriesResultCascadeIdempotencyKey(
			plan.seriesResult.audit,
			plan.seriesResult.condition.expectedScoreHead,
			plan.seriesResult.result.revision,
			plan.seriesResult.condition.expectedSeries,
			plan.seriesResult.condition.expectedSeriesRevision,
		)
		plan.condition.idempotencyKey = canonicalMultiGameIdempotencyKey(
			plan.condition.commandID,
			plan.condition.recordedAt,
			plan.games,
			plan.seriesResult,
		)

		require.ErrorIs(t, plan.Validate(), ErrInvalidCanonicalMultiGameTerminalization)
	})

	t.Run("canonicalizes equivalent slot permutations", func(t *testing.T) {
		fixture := task054MultiTerminalFixture(t)
		baseline, err := PlanCanonicalMultiGameTerminalization(
			fixture.command, fixture.authority, fixture.recordedAt,
		)
		require.NoError(t, err)

		permuted := task054MultiTerminalFixture(t)
		for index := range permuted.authority.Games {
			task054ReverseSeriesSlots(&permuted.authority.Games[index].GameResult.PersistedSeries)
			task054ReverseSeriesSlots(&permuted.authority.Games[index].GameResult.ProjectedSeries)
			task054ReverseSeriesSlots(&permuted.authority.Games[index].ScoreRevision.PersistedSeries)
			task054ReverseSeriesSlots(&permuted.authority.Games[index].ScoreRevision.ProjectedSeries)
		}
		task054ReverseSeriesSlots(&permuted.authority.SeriesResult.SeriesResult.PersistedSeries)
		task054ReverseSeriesSlots(&permuted.authority.SeriesResult.SeriesResult.ProjectedSeries)
		permutedPlan, err := PlanCanonicalMultiGameTerminalization(
			permuted.command, permuted.authority, permuted.recordedAt,
		)
		require.NoError(t, err)
		require.Equal(t, baseline, permutedPlan)
	})

	t.Run("rejects a live Game outside the unstarted target set", func(t *testing.T) {
		fixture := task054MultiTerminalFixture(t)
		var liveCommand GameResultCascadeCommand
		commands := make([]GameResultCascadeCommand, 0, 2)
		for _, command := range fixture.command.Games {
			if command.ScoreRevision.Attempt.SlotPosition == 3 {
				liveCommand = command
				continue
			}
			commands = append(commands, command)
		}
		var omitted GameResultCascadeAuthority
		authorities := make([]GameResultCascadeAuthority, 0, 2)
		for _, authority := range fixture.authority.Games {
			if authority.GameResult.Scope.GameID == liveCommand.GameResult.Scope.GameID {
				omitted = authority
				continue
			}
			task054SetGameActive(
				&authority.GameResult.PersistedSeries,
				liveCommand.GameResult.Scope.GameID,
			)
			task054SetGameActive(
				&authority.GameResult.ProjectedSeries,
				liveCommand.GameResult.Scope.GameID,
			)
			task054SetGameActive(
				&authority.ScoreRevision.PersistedSeries,
				liveCommand.GameResult.Scope.GameID,
			)
			task054SetGameActive(
				&authority.ScoreRevision.ProjectedSeries,
				liveCommand.GameResult.Scope.GameID,
			)
			authorities = append(authorities, authority)
		}
		require.NotNil(t, omitted.ScoreRevision.CurrentHead)
		fixture.command.Games = commands
		fixture.authority.Games = authorities
		fixture.authority.SeriesResult.CurrentScore = omitted.ScoreRevision.CurrentHead.Clone()
		persisted := cloneArenaSeries(omitted.GameResult.PersistedSeries)
		task054SetGameActive(&persisted, liveCommand.GameResult.Scope.GameID)
		fixture.authority.SeriesResult.SeriesResult.PersistedSeries = persisted
		projected := cloneArenaSeries(persisted)
		projected.State = domain.ArenaSeriesStateCancelled
		projected.CurrentResultRevisionID = officialResultRevisionIDPointer(
			fixture.command.SeriesResult.SeriesResult.RevisionID,
		)
		fixture.authority.SeriesResult.SeriesResult.ProjectedSeries = projected
		fixture.command.SeriesResult.SeriesResult.Outcome.ScoreRevisionID = seriesScoreRevisionIDPointer(
			omitted.ScoreRevision.CurrentHead.ID,
		)

		_, err := PlanCanonicalMultiGameTerminalization(
			fixture.command, fixture.authority, fixture.recordedAt,
		)
		require.ErrorIs(t, err, ErrInvalidCanonicalMultiGameTerminalization)
	})

	t.Run("is permutation invariant isolated and safe for concurrent replay", func(t *testing.T) {
		fixture := task054MultiTerminalFixture(t)
		baseline, err := PlanCanonicalMultiGameTerminalization(
			fixture.command, fixture.authority, fixture.recordedAt,
		)
		require.NoError(t, err)
		permutedCommand := cloneCanonicalMultiGameCommand(fixture.command)
		permutedAuthority := cloneCanonicalMultiGameAuthority(fixture.authority)
		permutedCommand.Games[0], permutedCommand.Games[2] =
			permutedCommand.Games[2], permutedCommand.Games[0]
		permutedAuthority.Games[0], permutedAuthority.Games[1] =
			permutedAuthority.Games[1], permutedAuthority.Games[0]
		permuted, err := PlanCanonicalMultiGameTerminalization(
			permutedCommand, permutedAuthority, fixture.recordedAt,
		)
		require.NoError(t, err)
		require.Equal(t, baseline, permuted)

		fixture.command.Games[0].ScoreRevision.Attempt.State = domain.ArenaGameStateVoid
		fixture.authority.Games[0].GameResult.PersistedSeries.Slots[0].Attempts[0].State =
			domain.ArenaGameStateReady
		games := baseline.GameCascades()
		games[0].condition.expectedSeries.Slots[0].Attempts[0].State = domain.ArenaGameStateReady
		finalSeries := baseline.FinalSeries()
		finalSeries.Score.FirstParticipantWins = 1
		dependencies := baseline.Dependencies()
		dependencies[0].DerivedRevisionID = domain.ArenaDerivedRevisionID(task054ID(311))
		require.NoError(t, baseline.Validate())

		const callers = 24
		var group sync.WaitGroup
		plans := make(chan CanonicalMultiGameTerminalizationPlan, callers)
		errs := make(chan error, callers)
		for range callers {
			group.Add(1)
			go func() {
				defer group.Done()
				plan, planErr := PlanCanonicalMultiGameTerminalization(
					permutedCommand, permutedAuthority, fixture.recordedAt,
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

type task054MultiTerminalCase struct {
	command    CanonicalMultiGameTerminalizationCommand
	authority  CanonicalMultiGameTerminalizationAuthority
	recordedAt time.Time
}

func task054MultiTerminalFixture(t *testing.T) task054MultiTerminalCase {
	t.Helper()
	tournamentID := task054ID(201)
	seriesID := task054ID(202)
	firstID := task054ID(203)
	secondID := task054ID(204)
	initialScoreID := domain.ArenaSeriesScoreRevisionID(task054ID(205))
	initialScoreSource := task054Projection(
		t,
		task054ID(206),
		tournamentID,
		domain.ArenaArtifactKindSeriesScore,
		seriesID,
		1,
		nil,
		"multi initial score",
	)
	currentScore := SeriesScoreRevisionHead{
		Scope:               SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID},
		ID:                  initialScoreID,
		Ordinal:             1,
		Operation:           SeriesScoreRevisionOperationInitialize,
		CommandID:           task054ID(207),
		Actor:               ArenaResultActor{Kind: ArenaResultActorServer},
		FirstParticipantID:  firstID,
		SecondParticipantID: secondID,
		Format:              domain.ArenaSeriesFormatBO3,
		SourceProjection:    initialScoreSource,
		RecordedAt:          time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC),
	}
	require.NoError(t, currentScore.Validate())
	series := domain.ArenaSeries{
		ID:                     seriesID,
		TournamentID:           tournamentID,
		FirstParticipantID:     firstID,
		SecondParticipantID:    secondID,
		Format:                 domain.ArenaSeriesFormatBO3,
		State:                  domain.ArenaSeriesStateReady,
		CurrentScoreRevisionID: seriesScoreRevisionIDPointer(initialScoreID),
	}
	for position := 1; position <= 3; position++ {
		slotID := task054ID(210 + position)
		series.Slots = append(series.Slots, domain.ArenaGameSlot{
			ID: slotID, SeriesID: seriesID, Position: position, Category: domain.CategoryWeb,
			Attempts: []domain.ArenaGame{{
				ID: task054ID(220 + position), SlotID: slotID, AttemptNo: 1,
				State: domain.ArenaGameStatePlanned,
			}},
		})
	}
	require.NoError(t, series.Validate())

	commands := make([]GameResultCascadeCommand, 0, 3)
	authorities := make([]GameResultCascadeAuthority, 0, 3)
	currentSeries := cloneArenaSeries(series)
	previousScoreSource := initialScoreSource
	baseTime := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)
	for position := 1; position <= 3; position++ {
		persisted := cloneArenaSeries(currentSeries)
		projected := cloneArenaSeries(currentSeries)
		game := &projected.Slots[position-1].Attempts[0]
		resultID := domain.ArenaOfficialResultRevisionID(task054ID(230 + position))
		scoreID := domain.ArenaSeriesScoreRevisionID(task054ID(240 + position))
		game.State = domain.ArenaGameStateCancelled
		game.ResultReason = domain.ArenaGameResultReasonSeriesCancelled
		game.ResultRevisionID = officialResultRevisionIDPointer(resultID)
		projected.CurrentScoreRevisionID = seriesScoreRevisionIDPointer(scoreID)
		gameSource := task054Projection(
			t,
			task054ID(250+position),
			tournamentID,
			domain.ArenaArtifactKindGameResult,
			game.ID,
			1,
			nil,
			"multi Game result",
		)
		previousScoreSourceID := previousScoreSource.ID()
		scoreSource := task054Projection(
			t,
			task054ID(260+position),
			tournamentID,
			domain.ArenaArtifactKindSeriesScore,
			seriesID,
			position+1,
			&previousScoreSourceID,
			"multi score",
		)
		commandID := task054ID(270 + position)
		actor := ArenaResultActor{Kind: ArenaResultActorServer}
		attempt := SeriesScoreAttemptReference{
			SlotID: game.SlotID, SlotPosition: position, GameID: game.ID, AttemptNo: 1,
			State: domain.ArenaGameStateCancelled, Reason: domain.ArenaGameResultReasonSeriesCancelled,
			CurrentGameResultRevisionID: resultID,
		}
		gameCommand := OfficialResultRevisionCommand{
			Scope: OfficialResultScope{
				TournamentID: tournamentID, SeriesID: seriesID, GameID: game.ID,
				Kind: OfficialResultSubjectGame,
			},
			CommandID: commandID, RevisionID: resultID, Actor: actor,
			ExpectedSourceProjection: gameSource,
			Outcome: OfficialResultOutcome{
				GameState:  domain.ArenaGameStateCancelled,
				GameReason: domain.ArenaGameResultReasonSeriesCancelled,
			},
		}
		scoreCommand := SeriesScoreRevisionCommand{
			Scope:     SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID},
			Operation: SeriesScoreRevisionOperationAppendAttempt,
			CommandID: commandID, RevisionID: scoreID, Actor: actor,
			ExpectedCurrentRevisionID: seriesScoreRevisionIDPointer(currentScore.ID),
			ExpectedSourceProjection:  scoreSource,
			Attempt:                   &attempt,
		}
		command := GameResultCascadeCommand{
			AuditEventID:  task054ID(280 + position),
			GameResult:    gameCommand,
			ScoreRevision: scoreCommand,
		}
		authority := GameResultCascadeAuthority{
			GameResult: OfficialResultRevisionAuthority{
				Scope: gameCommand.Scope, PersistedSeries: persisted, ProjectedSeries: projected,
				SourceProjection: gameSource, SeriesRevision: 20, AttemptRevision: ArenaAttemptRowRevision(100 + position),
			},
			ScoreRevision: SeriesScoreRevisionAuthority{
				Scope: scoreCommand.Scope, PersistedSeries: persisted, ProjectedSeries: projected,
				SourceProjection: scoreSource, CurrentHead: task053ScoreHeadPointer(currentScore),
				SeriesRevision: 20, AttemptRevision: ArenaAttemptRowRevision(100 + position),
			},
		}
		step, err := PlanGameResultCascade(command, authority, baseTime.Add(5*time.Minute))
		require.NoError(t, err)
		commands = append(commands, command)
		authorities = append(authorities, authority)
		currentSeries = projected
		currentScore = step.ScoreRevision().Revision().Head()
		previousScoreSource = scoreSource
	}

	seriesResultID := domain.ArenaOfficialResultRevisionID(task054ID(291))
	finalSeries := cloneArenaSeries(currentSeries)
	finalSeries.State = domain.ArenaSeriesStateCancelled
	finalSeries.CurrentResultRevisionID = officialResultRevisionIDPointer(seriesResultID)
	seriesProjection, err := domain.NewArenaProjectionRevision(
		domain.ArenaDerivedRevisionID(task054ID(292)),
		tournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesResult, EntityID: seriesID},
		1,
		nil,
		baseTime.Add(4*time.Minute),
		[]byte("multi Series result"),
	)
	require.NoError(t, err)
	seriesSource := seriesProjection.Revision()
	seriesCommand := OfficialResultRevisionCommand{
		Scope: OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID, Kind: OfficialResultSubjectSeries,
		},
		CommandID: task054ID(293), RevisionID: seriesResultID,
		Actor:                    ArenaResultActor{Kind: ArenaResultActorServer},
		ExpectedSourceProjection: seriesSource,
		Outcome: OfficialResultOutcome{
			SeriesState:     domain.ArenaSeriesStateCancelled,
			SeriesReason:    ArenaSeriesResultReasonSeriesCancelled,
			ScoreRevisionID: seriesScoreRevisionIDPointer(currentScore.ID),
		},
	}
	tailCommand := SeriesResultCascadeCommand{
		AuditEventID: task054ID(294),
		SeriesResult: seriesCommand,
	}
	tailAuthority := SeriesResultCascadeAuthority{
		SeriesResult: OfficialResultRevisionAuthority{
			Scope: seriesCommand.Scope, PersistedSeries: currentSeries, ProjectedSeries: finalSeries,
			ProjectedSeriesReason: ArenaSeriesResultReasonSeriesCancelled,
			SourceProjection:      seriesSource,
			SeriesRevision:        20,
		},
		CurrentScore: currentScore,
	}
	commands = []GameResultCascadeCommand{commands[2], commands[0], commands[1]}
	authorities = []GameResultCascadeAuthority{authorities[1], authorities[2], authorities[0]}
	return task054MultiTerminalCase{
		command: CanonicalMultiGameTerminalizationCommand{
			CommandID: task054ID(295), Games: commands, SeriesResult: tailCommand,
		},
		authority: CanonicalMultiGameTerminalizationAuthority{
			Games: authorities, SeriesResult: tailAuthority,
		},
		recordedAt: baseTime.Add(5 * time.Minute),
	}
}

func task054ReverseSeriesSlots(series *domain.ArenaSeries) {
	for left, right := 0, len(series.Slots)-1; left < right; left, right = left+1, right-1 {
		series.Slots[left], series.Slots[right] = series.Slots[right], series.Slots[left]
	}
}

func task054SetGameActive(
	series *domain.ArenaSeries,
	gameID uuid.UUID,
) {
	game, found := findArenaSeriesGamePointer(series, gameID)
	if !found {
		return
	}
	game.State = domain.ArenaGameStateActive
	game.ResultReason = ""
	game.WinnerID = nil
	game.ResultRevisionID = nil
}
