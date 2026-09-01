package arena_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestSemifinalAdvancement(t *testing.T) {
	t.Parallel()

	bracket := task052SemifinalBracket(t)
	matches := bracket.Semifinals()
	first := task052CompletedSemifinal(matches[0].Series, true, 1)
	second := task052CompletedSemifinal(matches[1].Series, false, 2)

	advanced, changed, err := arena.AdvanceSemifinalResults(
		arena.SemifinalAdvancement{},
		bracket,
		[]domain.ArenaSeries{second, first},
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, advanced.Complete())
	require.Equal(t, []uuid.UUID{*first.WinnerID, *second.WinnerID}, advanced.FinalParticipants())
	require.Equal(t, []uuid.UUID{first.SecondParticipantID, second.FirstParticipantID}, advanced.EliminatedParticipants())
	require.False(t, advanced.HasThirdPlace())
	require.NoError(t, advanced.Validate(bracket))

	repeated, changed, err := arena.AdvanceSemifinalResults(
		advanced,
		bracket,
		[]domain.ArenaSeries{first, second, first},
	)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, advanced.FinalParticipants(), repeated.FinalParticipants())
	require.Equal(t, advanced.EliminatedParticipants(), repeated.EliminatedParticipants())

	t.Run("rejects partial cancelled and unrelated Series", func(t *testing.T) {
		t.Parallel()

		partial := matches[0].Series
		partial.State = domain.ArenaSeriesStateActive
		cancelled := matches[0].Series
		cancelled.State = domain.ArenaSeriesStateCancelled
		scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task052ID(310))
		resultRevisionID := domain.ArenaOfficialResultRevisionID(task052ID(311))
		cancelled.CurrentScoreRevisionID = &scoreRevisionID
		cancelled.CurrentResultRevisionID = &resultRevisionID
		unrelated := task052CompletedSemifinal(matches[0].Series, true, 3)
		unrelated.ID = task052ID(312)

		for name, result := range map[string]domain.ArenaSeries{
			"partial": partial, "cancelled": cancelled, "unrelated": unrelated,
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, gotChanged, gotErr := arena.AdvanceSemifinalResults(
					arena.SemifinalAdvancement{}, bracket, []domain.ArenaSeries{result},
				)
				require.ErrorIs(t, gotErr, arena.ErrInvalidSemifinalAdvancement)
				require.False(t, gotChanged)
				require.Equal(t, arena.SemifinalAdvancement{}, got)
			})
		}
	})
}

func task052SemifinalBracket(t *testing.T) arena.SemifinalBracket {
	t.Helper()

	fixture := task051SwissFixture(t, false)
	finalSwiss, err := arena.PlanFinalSwissProjection(fixture.command)
	require.NoError(t, err)
	top4, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
		TournamentID:          fixture.command.TournamentID,
		RevisionID:            task051RevisionID(5201),
		RevisionNo:            1,
		Source:                finalSwiss,
		CurrentTerminalSeries: task051ResultHeads(fixture.command),
		CreatedAt:             fixture.command.CreatedAt.Add(time.Minute),
	})
	require.NoError(t, err)
	bracket, err := arena.PlanStrengthMatchedSemifinals(arena.SemifinalBracketCommand{
		TournamentID: fixture.command.TournamentID,
		RevisionID:   task051RevisionID(5202),
		RevisionNo:   1,
		Top4:         top4,
		SeriesIDs:    [2]uuid.UUID{task052ID(1), task052ID(2)},
		CreatedAt:    fixture.command.CreatedAt.Add(2 * time.Minute),
	})
	require.NoError(t, err)
	return bracket
}

func task052CompletedSemifinal(series domain.ArenaSeries, firstWins bool, suffix int) domain.ArenaSeries {
	series.State = domain.ArenaSeriesStateCompleted
	winnerID := series.SecondParticipantID
	series.Score.SecondParticipantWins = 1
	if firstWins {
		winnerID = series.FirstParticipantID
		series.Score = domain.ArenaSeriesScore{FirstParticipantWins: 1}
	}
	scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task052ID(100 + suffix))
	resultRevisionID := domain.ArenaOfficialResultRevisionID(task052ID(200 + suffix))
	series.WinnerID = &winnerID
	series.CurrentScoreRevisionID = &scoreRevisionID
	series.CurrentResultRevisionID = &resultRevisionID
	return series
}
