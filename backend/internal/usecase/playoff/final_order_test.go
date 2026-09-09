package playoff_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func TestFinalRetainsSeriesOrientationWithReversedDraftOrder(t *testing.T) {
	t.Parallel()
	bracket := newSemifinalBracket(t)
	matches := bracket.Semifinals()
	advanced, _, err := playoff.AdvanceSemifinalResults(playoff.SemifinalAdvancement{}, bracket,
		[]domain.Series{completedSemifinal(matches[0].Series, true, 1), completedSemifinal(matches[1].Series, false, 2)})
	require.NoError(t, err)
	participants := advanced.FinalParticipants()
	for _, outsider := range []bool{false, true} {
		first, second := participants[1], participants[0]
		if outsider {
			second = uuid.New()
		}
		seriesID := uuid.New()
		now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
		category := finalCategoryRevision(t, seriesID, bracket.Projection().Revision().TournamentID(), now)
		value, err := draft.NewBO3Final(category, draft.StartCommand{DraftID: uuid.New(), FirstParticipantID: first, SecondParticipantID: second, FirstDeadline: now.Add(time.Minute)})
		require.NoError(t, err)
		for index, action := range []draft.ActionCommand{
			{ActorID: first, Action: domain.DraftActionBan, Category: domain.CategoryCrypto},
			{ActorID: second, Action: domain.DraftActionBan, Category: domain.CategoryForensics},
			{ActorID: first, Action: domain.DraftActionPick, Category: domain.CategoryPwn},
			{ActorID: second, Action: domain.DraftActionPick, Category: domain.CategoryReverse},
		} {
			action.ExpectedTurn = index + 1
			action.OccurredAt = now.Add(time.Duration(index+1) * time.Second)
			if index < 3 {
				action.NextDeadline = now.Add(time.Minute)
			}
			value, err = draft.ApplyBO3FinalAction(value, action)
			require.NoError(t, err)
		}
		final, err := playoff.NewFinal(playoff.FinalCommand{SeriesID: seriesID, Advancement: advanced, Draft: value,
			InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()), FirstSlotID: uuid.New(), FirstGameID: uuid.New()})
		if outsider {
			require.ErrorIs(t, err, playoff.ErrInvalidFinal)
			continue
		}
		require.NoError(t, err)
		require.NoError(t, final.Validate())
		require.Equal(t, participants[0], final.Execution().Series.FirstParticipantID)
		require.Equal(t, participants[1], final.Execution().Series.SecondParticipantID)
	}
}
