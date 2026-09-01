package arena_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestSupersededUnstartedExecution(t *testing.T) {
	t.Parallel()

	recordedAt := time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)

	t.Run("supersedes only the unnecessary unstarted Game", func(t *testing.T) {
		t.Parallel()

		series := task057ExecutionSeries(t, 100, domain.ArenaSeriesFormatBO3, 3)
		task057CompleteFirstTwoGames(t, &series, 180)
		unneeded := series.Slots[2].Attempts[0]
		intent := task057GameTerminalIntent(t, series, unneeded, 200, recordedAt)
		plan, err := arena.PlanExecutionSupersession(arena.ExecutionSupersessionCommand{
			CommandID: task057ID(210), Action: arena.ExecutionSupersedeGames,
			Series: series, SeriesRevision: 4, GameIntents: []arena.ExecutionGameTerminalIntent{intent},
			RecordedAt: recordedAt,
		})
		require.NoError(t, err)
		require.NoError(t, plan.Validate())
		require.Equal(t, series, plan.OriginalSeries())
		updated := plan.Series()
		require.Equal(t, domain.ArenaSeriesStateCompleted, updated.State)
		for index, slot := range updated.Slots {
			game := slot.Attempts[0]
			if index != 2 {
				require.Equal(t, series.Slots[index].Attempts[0], game)
				continue
			}
			require.Equal(t, domain.ArenaGameStateSuperseded, game.State)
			require.Equal(t, domain.ArenaGameResultReasonDerivedRevisionSuperseded, game.ResultReason)
			require.Equal(t, intent.ResultRevisionID, *game.ResultRevisionID)
		}
		require.Len(t, plan.GameResultRevisions(), 1)
		require.Equal(t, 1, plan.GameResultRevisions()[0].Revision().Ordinal())
		require.Equal(
			t, domain.ArenaGameResultReasonDerivedRevisionSuperseded,
			plan.GameResultRevisions()[0].Revision().Outcome().GameReason,
		)
		require.Nil(t, plan.SeriesResultRevision())
	})

	t.Run("cancels a withdrawn unstarted Series and creates a fresh replacement", func(t *testing.T) {
		t.Parallel()

		series := task057ExecutionSeries(t, 300, domain.ArenaSeriesFormatBO1, 1)
		game := series.Slots[0].Attempts[0]
		gameIntent := task057GameTerminalIntent(t, series, game, 400, recordedAt)
		seriesSource := task057ResultSource(
			t, series.TournamentID, domain.ArenaArtifactKindSeriesResult,
			series.ID, task057RevisionID(410), recordedAt.Add(-time.Second),
		)
		replacement := task057ExecutionSeries(t, 500, domain.ArenaSeriesFormatBO1, 1)
		replacement.TournamentID = series.TournamentID
		require.NoError(t, replacement.Validate())
		seriesResultID := domain.ArenaOfficialResultRevisionID(task057ID(411))
		scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task057ID(412))
		plan, err := arena.PlanExecutionSupersession(arena.ExecutionSupersessionCommand{
			CommandID: task057ID(413), Action: arena.ExecutionCancelSeries,
			Series: series, SeriesRevision: 7, GameIntents: []arena.ExecutionGameTerminalIntent{gameIntent},
			SeriesIntent: &arena.ExecutionSeriesTerminalIntent{
				ResultRevisionID: seriesResultID, ScoreRevisionID: scoreRevisionID,
				SourceProjection: seriesSource, SeriesRevision: 8,
			},
			Replacement: &replacement, RecordedAt: recordedAt,
		})
		require.NoError(t, err)
		require.NoError(t, plan.Validate())
		require.Equal(t, series, plan.OriginalSeries())
		cancelled := plan.Series()
		require.Equal(t, domain.ArenaSeriesStateCancelled, cancelled.State)
		require.Equal(t, scoreRevisionID, *cancelled.CurrentScoreRevisionID)
		require.Equal(t, seriesResultID, *cancelled.CurrentResultRevisionID)
		cancelledGame := cancelled.Slots[0].Attempts[0]
		require.Equal(t, domain.ArenaGameStateCancelled, cancelledGame.State)
		require.Equal(t, domain.ArenaGameResultReasonSeriesCancelled, cancelledGame.ResultReason)
		require.Len(t, plan.GameResultRevisions(), 1)
		require.Equal(t, 1, plan.GameResultRevisions()[0].Revision().Ordinal())
		seriesRevision := plan.SeriesResultRevision()
		require.NotNil(t, seriesRevision)
		require.Equal(t, 1, seriesRevision.Revision().Ordinal())
		require.Equal(t, arena.ArenaSeriesResultReasonSeriesCancelled, seriesRevision.Revision().Outcome().SeriesReason)
		plannedReplacement := plan.Replacement()
		require.NotNil(t, plannedReplacement)
		require.Equal(t, replacement, *plannedReplacement)
		require.NotEqual(t, series.ID, plannedReplacement.ID)
		require.NotEqual(t, series.Slots[0].ID, plannedReplacement.Slots[0].ID)
		require.NotEqual(t, game.ID, plannedReplacement.Slots[0].Attempts[0].ID)
	})

	t.Run("rejects started Games for both terminal actions", func(t *testing.T) {
		t.Parallel()

		for _, action := range []arena.ExecutionSupersessionAction{
			arena.ExecutionSupersedeGames,
			arena.ExecutionCancelSeries,
		} {
			t.Run(string(action), func(t *testing.T) {
				t.Parallel()
				series := task057ExecutionSeries(t, 600, domain.ArenaSeriesFormatBO1, 1)
				series.Slots[0].Attempts[0].State = domain.ArenaGameStateActive
				intent := task057GameTerminalIntent(t, series, series.Slots[0].Attempts[0], 700, recordedAt)
				command := arena.ExecutionSupersessionCommand{
					CommandID: task057ID(710), Action: action, Series: series, SeriesRevision: 2,
					GameIntents: []arena.ExecutionGameTerminalIntent{intent}, RecordedAt: recordedAt,
				}
				if action == arena.ExecutionCancelSeries {
					source := task057ResultSource(
						t, series.TournamentID, domain.ArenaArtifactKindSeriesResult,
						series.ID, task057RevisionID(711), recordedAt.Add(-time.Second),
					)
					command.SeriesIntent = &arena.ExecutionSeriesTerminalIntent{
						ResultRevisionID: domain.ArenaOfficialResultRevisionID(task057ID(712)),
						ScoreRevisionID:  domain.ArenaSeriesScoreRevisionID(task057ID(713)),
						SourceProjection: source, SeriesRevision: 3,
					}
				}
				plan, err := arena.PlanExecutionSupersession(command)
				require.ErrorIs(t, err, arena.ErrInvalidExecutionSupersession)
				require.Equal(t, arena.ExecutionSupersessionPlan{}, plan)
			})
		}
	})
}

func task057CompleteFirstTwoGames(t *testing.T, series *domain.ArenaSeries, base int) {
	t.Helper()
	winner := series.FirstParticipantID
	scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task057ID(base))
	resultRevisionID := domain.ArenaOfficialResultRevisionID(task057ID(base + 1))
	series.State = domain.ArenaSeriesStateCompleted
	series.Score = domain.ArenaSeriesScore{FirstParticipantWins: 2}
	series.WinnerID = &winner
	series.CurrentScoreRevisionID = &scoreRevisionID
	series.CurrentResultRevisionID = &resultRevisionID
	for index := 0; index < 2; index++ {
		gameResultRevisionID := domain.ArenaOfficialResultRevisionID(task057ID(base + 2 + index))
		game := &series.Slots[index].Attempts[0]
		game.State = domain.ArenaGameStateCompleted
		game.ResultReason = domain.ArenaGameResultReasonSolved
		game.WinnerID = &winner
		game.ResultRevisionID = &gameResultRevisionID
		series.Slots[index].ScoreBefore = domain.ArenaSeriesScore{FirstParticipantWins: index}
	}
	series.Slots[2].ScoreBefore = domain.ArenaSeriesScore{FirstParticipantWins: 2}
	require.NoError(t, series.Validate())
}

func task057ExecutionSeries(
	t *testing.T,
	base int,
	format domain.ArenaSeriesFormat,
	slots int,
) domain.ArenaSeries {
	t.Helper()
	seriesID := task057ID(base)
	series := domain.ArenaSeries{
		ID: seriesID, TournamentID: task057ID(base + 1),
		FirstParticipantID: task057ID(base + 2), SecondParticipantID: task057ID(base + 3),
		Format: format, State: domain.ArenaSeriesStatePlanned,
	}
	for index := range slots {
		slotID := task057ID(base + 10 + index)
		series.Slots = append(series.Slots, domain.ArenaGameSlot{
			ID: slotID, SeriesID: seriesID, Position: index + 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.ArenaSeriesScore{},
			Attempts: []domain.ArenaGame{{
				ID: task057ID(base + 20 + index), SlotID: slotID, AttemptNo: 1,
				State: domain.ArenaGameStatePlanned,
			}},
		})
	}
	require.NoError(t, series.Validate())
	return series
}

func task057GameTerminalIntent(
	t *testing.T,
	series domain.ArenaSeries,
	game domain.ArenaGame,
	base int,
	recordedAt time.Time,
) arena.ExecutionGameTerminalIntent {
	t.Helper()
	return arena.ExecutionGameTerminalIntent{
		GameID:           game.ID,
		ResultRevisionID: domain.ArenaOfficialResultRevisionID(task057ID(base)),
		SourceProjection: task057ResultSource(
			t, series.TournamentID, domain.ArenaArtifactKindGameResult,
			game.ID, task057RevisionID(base+1), recordedAt.Add(-time.Second),
		),
		AttemptRevision: 3,
	}
}

func task057ResultSource(
	t *testing.T,
	tournamentID uuid.UUID,
	kind domain.ArenaArtifactKind,
	entityID uuid.UUID,
	revisionID domain.ArenaDerivedRevisionID,
	createdAt time.Time,
) domain.ArenaDerivedRevision {
	t.Helper()
	projection, err := domain.NewArenaProjectionRevision(
		revisionID, tournamentID, domain.ArenaArtifactRef{Kind: kind, EntityID: entityID},
		1, nil, createdAt, []byte("execution correction source"),
	)
	require.NoError(t, err)
	return projection.Revision()
}

func task057RevisionID(value int) domain.ArenaDerivedRevisionID {
	return domain.ArenaDerivedRevisionID(task057ID(value))
}
