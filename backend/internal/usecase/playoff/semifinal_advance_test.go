package playoff_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func TestSemifinalAdvancement(t *testing.T) {
	t.Parallel()

	bracket := newSemifinalBracket(t)
	matches := bracket.Semifinals()
	first := completedSemifinal(matches[0].Series, true, 1)
	second := completedSemifinal(matches[1].Series, false, 2)
	advanced, changed, err := playoff.AdvanceSemifinalResults(
		playoff.SemifinalAdvancement{}, bracket, []domain.Series{second, first},
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, advanced.Complete())
	require.Equal(t, []uuid.UUID{*first.WinnerID, *second.WinnerID}, advanced.FinalParticipants())
	require.Equal(t, []uuid.UUID{first.SecondParticipantID, second.FirstParticipantID}, advanced.EliminatedParticipants())
	require.False(t, advanced.HasThirdPlace())
	require.NoError(t, advanced.Validate(bracket))

	repeated, changed, err := playoff.AdvanceSemifinalResults(
		advanced, bracket, []domain.Series{first, second, first},
	)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, advanced.FinalParticipants(), repeated.FinalParticipants())
	require.Equal(t, advanced.EliminatedParticipants(), repeated.EliminatedParticipants())

	t.Run("rejects partial cancelled and unrelated Series", func(t *testing.T) {
		t.Parallel()

		partial := matches[0].Series
		partial.State = domain.SeriesStateActive
		cancelled := matches[0].Series
		cancelled.State = domain.SeriesStateCancelled
		scoreRevisionID := domain.SeriesScoreRevisionID(semifinalID(310))
		resultRevisionID := domain.OfficialResultRevisionID(semifinalID(311))
		cancelled.CurrentScoreRevisionID = &scoreRevisionID
		cancelled.CurrentResultRevisionID = &resultRevisionID
		unrelated := completedSemifinal(matches[0].Series, true, 3)
		unrelated.ID = semifinalID(312)

		for name, result := range map[string]domain.Series{
			"partial": partial, "cancelled": cancelled, "unrelated": unrelated,
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, gotChanged, gotErr := playoff.AdvanceSemifinalResults(
					playoff.SemifinalAdvancement{}, bracket, []domain.Series{result},
				)
				require.ErrorIs(t, gotErr, playoff.ErrInvalidSemifinalAdvancement)
				require.False(t, gotChanged)
				require.Equal(t, playoff.SemifinalAdvancement{}, got)
			})
		}
	})
}
