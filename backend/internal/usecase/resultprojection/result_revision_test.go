package resultprojection_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func TestOfficialResultRevisionProjection(t *testing.T) {
	t.Parallel()

	tournamentID := task055ID(1)
	seriesID := task055ID(2)
	gameID := task055ID(3)
	firstID := task055ID(4)
	secondID := task055ID(5)
	baseTime := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)

	t.Run("derives exact Game status and nullability", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			state      domain.GameState
			reason     domain.GameResultReason
			winnerID   *uuid.UUID
			wantStatus projection.OfficialResultStatus
		}{
			{
				name: "solved", state: domain.GameStateCompleted,
				reason: domain.GameResultReasonSolved, winnerID: task055UUIDPointer(firstID),
				wantStatus: projection.OfficialResultStatusSolved,
			},
			{
				name: "surrender", state: domain.GameStateCompleted,
				reason: domain.GameResultReasonSurrender, winnerID: task055UUIDPointer(firstID),
				wantStatus: projection.OfficialResultStatusCompleted,
			},
			{
				name: "operator forfeit", state: domain.GameStateCompleted,
				reason: domain.GameResultReasonOperatorForfeit, winnerID: task055UUIDPointer(firstID),
				wantStatus: projection.OfficialResultStatusCompleted,
			},
			{
				name: "void", state: domain.GameStateVoid,
				reason:     domain.GameResultReasonTaskFailure,
				wantStatus: projection.OfficialResultStatusVoid,
			},
			{
				name: "cancelled", state: domain.GameStateCancelled,
				reason:     domain.GameResultReasonSeriesCancelled,
				wantStatus: projection.OfficialResultStatusCancelled,
			},
			{
				name: "superseded", state: domain.GameStateSuperseded,
				reason:     domain.GameResultReasonDerivedRevisionSuperseded,
				wantStatus: projection.OfficialResultStatusSuperseded,
			},
		}
		for index, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				recordedAt := baseTime.Add(time.Duration(index+2) * time.Minute)
				head := task055GameResultHead(
					t, tournamentID, seriesID, gameID, firstID, test.state, test.reason,
					test.winnerID, recordedAt, 20+index*10,
				)

				plan, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
					TerminalSource: projection.TerminalResultSourcePlayed,
					Result:         head,
					ResultProjection: task055ExactProjection(
						t, head.SourceProjection, fmt.Sprintf("game-%d", 20+index*10),
					),
				})
				require.NoError(t, err)
				require.NoError(t, plan.Validate())

				public := plan.Public()
				require.Equal(t, resultusecase.OfficialResultSubjectGame, public.Subject)
				require.Equal(t, test.wantStatus, public.Status)
				require.Equal(t, tournamentID, public.TournamentID)
				require.Equal(t, seriesID, public.SeriesID)
				require.NotNil(t, public.GameID)
				require.Equal(t, gameID, *public.GameID)
				require.Equal(t, test.winnerID, public.WinnerID)
				require.Nil(t, public.Score)
				require.Equal(t, recordedAt, public.ResolvedAt)

				operator := plan.Operator()
				require.Equal(t, public, operator.Public)
				require.NotNil(t, operator.GameState)
				require.NotNil(t, operator.GameReason)
				require.Equal(t, test.state, *operator.GameState)
				require.Equal(t, test.reason, *operator.GameReason)
				require.Nil(t, operator.SeriesState)
				require.Nil(t, operator.SeriesReason)
				require.Nil(t, operator.ScoreRevisionID)
				require.Equal(t, head.ID, operator.ResultRevisionID)
				require.Equal(t, head.SourceProjection.ID(), operator.SourceProjectionRevisionID)
			})
		}
	})

	t.Run("derives exact BO1 BO3 and no Game Series fields", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			format     domain.SeriesFormat
			score      domain.SeriesScore
			state      domain.SeriesState
			reason     domain.SeriesResultReason
			winnerID   *uuid.UUID
			wantStatus projection.OfficialResultStatus
			attempts   int
		}{
			{
				name: "BO1", format: domain.SeriesFormatBO1,
				score: domain.SeriesScore{FirstParticipantWins: 1},
				state: domain.SeriesStateCompleted, reason: domain.SeriesResultReasonScoreComplete,
				winnerID: task055UUIDPointer(firstID), wantStatus: projection.OfficialResultStatusCompleted,
				attempts: 1,
			},
			{
				name: "BO3", format: domain.SeriesFormatBO3,
				score: domain.SeriesScore{SecondParticipantWins: 2},
				state: domain.SeriesStateCompleted, reason: domain.SeriesResultReasonScoreComplete,
				winnerID: task055UUIDPointer(secondID), wantStatus: projection.OfficialResultStatusCompleted,
				attempts: 2,
			},
			{
				name: "cancelled without Game", format: domain.SeriesFormatBO3,
				score: domain.SeriesScore{}, state: domain.SeriesStateCancelled,
				reason:     domain.SeriesResultReasonTournamentCancelled,
				wantStatus: projection.OfficialResultStatusVoid,
			},
		}
		for index, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				recordedAt := baseTime.Add(time.Duration(index+20) * time.Minute)
				score := task055ScoreHead(
					t, tournamentID, seriesID, firstID, secondID, test.format,
					test.score, test.attempts, recordedAt.Add(-time.Second), 100+index*20,
				)
				resultHead := task055SeriesResultHead(
					t, tournamentID, seriesID, score.ID, test.state, test.reason,
					test.winnerID, recordedAt, 110+index*20,
				)

				plan, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
					TerminalSource:   projection.TerminalResultSourcePlayed,
					Result:           resultHead,
					ResultProjection: task055ExactProjection(t, resultHead.SourceProjection, fmt.Sprintf("series-%d", 110+index*20)),
					Score:            task055ScoreHeadPointer(score),
					ScoreProjection:  task055ProjectionPointer(task055ExactProjection(t, score.SourceProjection, fmt.Sprintf("score-%d", 100+index*20))),
				})
				require.NoError(t, err)
				require.NoError(t, plan.Validate())

				public := plan.Public()
				require.Equal(t, resultusecase.OfficialResultSubjectSeries, public.Subject)
				require.Equal(t, test.wantStatus, public.Status)
				require.Nil(t, public.GameID)
				require.NotNil(t, public.Score)
				require.Equal(t, test.score, *public.Score)
				require.Equal(t, test.winnerID, public.WinnerID)
				require.Equal(t, recordedAt, public.ResolvedAt)

				operator := plan.Operator()
				require.Nil(t, operator.GameState)
				require.Nil(t, operator.GameReason)
				require.NotNil(t, operator.SeriesState)
				require.NotNil(t, operator.SeriesReason)
				require.Equal(t, test.state, *operator.SeriesState)
				require.Equal(t, test.reason, *operator.SeriesReason)
				require.NotNil(t, operator.ScoreRevisionID)
				require.Equal(t, score.ID, *operator.ScoreRevisionID)
			})
		}
	})

	t.Run("rejects a spliced Series score and returns defensive copies", func(t *testing.T) {
		t.Parallel()

		score := task055ScoreHead(
			t, tournamentID, seriesID, firstID, secondID, domain.SeriesFormatBO1,
			domain.SeriesScore{FirstParticipantWins: 1}, 1, baseTime.Add(time.Minute), 300,
		)
		resultHead := task055SeriesResultHead(
			t, tournamentID, seriesID, score.ID, domain.SeriesStateCompleted,
			domain.SeriesResultReasonScoreComplete, task055UUIDPointer(firstID),
			baseTime.Add(2*time.Minute), 310,
		)
		wrong := score.Clone()
		wrong.ID = domain.SeriesScoreRevisionID(task055ID(399))
		_, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
			TerminalSource:   projection.TerminalResultSourcePlayed,
			Result:           resultHead,
			ResultProjection: task055ExactProjection(t, resultHead.SourceProjection, "series-310"),
			Score:            task055ScoreHeadPointer(wrong),
			ScoreProjection:  task055ProjectionPointer(task055ExactProjection(t, score.SourceProjection, "score-300")),
		})
		require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)

		plan, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
			TerminalSource:   projection.TerminalResultSourcePlayed,
			Result:           resultHead,
			ResultProjection: task055ExactProjection(t, resultHead.SourceProjection, "series-310"),
			Score:            task055ScoreHeadPointer(score),
			ScoreProjection:  task055ProjectionPointer(task055ExactProjection(t, score.SourceProjection, "score-300")),
		})
		require.NoError(t, err)
		public := plan.Public()
		*public.WinnerID = secondID
		public.Score.FirstParticipantWins = 0
		operator := plan.Operator()
		*operator.Public.WinnerID = secondID
		require.Equal(t, firstID, *plan.Public().WinnerID)
		require.Equal(t, 1, plan.Public().Score.FirstParticipantWins)
		require.Equal(t, firstID, *plan.Operator().Public.WinnerID)
	})
}
