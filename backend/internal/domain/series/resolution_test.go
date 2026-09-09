package series_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	game "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func TestSeriesNoDrawInvariant(t *testing.T) {
	t.Parallel()

	t.Run("BO1 and BO3 complete with exactly one score winner", func(t *testing.T) {
		t.Parallel()

		for _, format := range []domain.SeriesFormat{
			domain.SeriesFormatBO1,
			domain.SeriesFormatBO3,
		} {
			current := seriesExecutionFixture(format, domain.SeriesStateActive, domain.GameStateActive)
			winnerID := current.Series.FirstParticipantID
			current = completeCurrentGame(
				current,
				winnerID,
				domain.GameResultReasonSolved,
				domain.OfficialResultRevisionID(seriesFixtureID(20+format.WinsRequired())),
			)
			score := current.Series.Score
			score.FirstParticipantWins = format.WinsRequired()
			scoreRevisionID := domain.SeriesScoreRevisionID(seriesFixtureID(30 + format.WinsRequired()))
			resultRevisionID := domain.OfficialResultRevisionID(seriesFixtureID(40 + format.WinsRequired()))

			resolved, changed, err := game.ResolveCompetitive(current, game.ResolutionCommand{
				Route: game.CompetitiveSeriesRouteFairResult,
				Terminal: &game.TerminalEvidence{
					Score: score, WinnerID: &winnerID,
					ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &resultRevisionID,
				},
			})
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, domain.SeriesStateCompleted, resolved.Series.State)
			require.Equal(t, winnerID, *resolved.Series.WinnerID)
			require.NoError(t, game.ValidateResolution(
				game.CompetitiveSeriesRouteFairResult,
				resolved,
			))
		}
	})

	t.Run("completed draw and invented winner are rejected", func(t *testing.T) {
		t.Parallel()

		current := seriesExecutionFixture(
			domain.SeriesFormatBO3,
			domain.SeriesStateActive,
			domain.GameStateActive,
		)
		scoreRevisionID := domain.SeriesScoreRevisionID(seriesFixtureID(43))
		resultRevisionID := domain.OfficialResultRevisionID(seriesFixtureID(44))

		_, changed, err := game.ResolveCompetitive(current, game.ResolutionCommand{
			Route: game.CompetitiveSeriesRouteFairResult,
			Terminal: &game.TerminalEvidence{
				Score:           current.Series.Score,
				ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &resultRevisionID,
			},
		})
		require.False(t, changed)
		require.ErrorIs(t, err, game.ErrCompetitiveSeriesDraw)

		inventedWinner := current.Series.FirstParticipantID
		_, changed, err = game.ResolveCompetitive(current, game.ResolutionCommand{
			Route: game.CompetitiveSeriesRouteFairResult,
			Terminal: &game.TerminalEvidence{
				Score: current.Series.Score, WinnerID: &inventedWinner,
				ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &resultRevisionID,
			},
		})
		require.False(t, changed)
		require.ErrorIs(t, err, game.ErrCompetitiveSeriesDraw)

		validScore := current.Series.Score
		validScore.FirstParticipantWins = current.Series.Format.WinsRequired()
		_, changed, err = game.ResolveCompetitive(current, game.ResolutionCommand{
			Route: game.CompetitiveSeriesRouteFairResult,
			Terminal: &game.TerminalEvidence{
				Score: validScore, WinnerID: &inventedWinner,
				ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &resultRevisionID,
			},
		})
		require.False(t, changed)
		require.ErrorIs(t, err, game.ErrInvalidCompetitiveSeriesResolution)
	})

	t.Run("deadline and correction require replay", func(t *testing.T) {
		t.Parallel()

		for _, route := range []game.ResolutionRoute{
			game.CompetitiveSeriesRouteDeadline,
			game.CompetitiveSeriesRouteCorrection,
		} {
			current := seriesExecutionFixture(
				domain.SeriesFormatBO1,
				domain.SeriesStateActive,
				domain.GameStateActive,
			)
			resolved, changed, err := game.ResolveCompetitive(current, game.ResolutionCommand{
				Route: route,
			})
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, domain.SeriesStateReplayRequired, resolved.Series.State)
			require.Nil(t, resolved.Series.WinnerID)
			require.NoError(t, game.ValidateResolution(route, resolved))
		}
	})

	t.Run("replay exhaustion and recovery require technical pause", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name        string
			route       game.ResolutionRoute
			seriesState domain.SeriesState
			gameState   domain.GameState
			resumeState domain.SeriesState
		}{
			{
				name: "replay exhaustion", route: game.CompetitiveSeriesRouteReplayExhausted,
				seriesState: domain.SeriesStateReplayRequired,
				gameState:   domain.GameStateVoid,
				resumeState: domain.SeriesStateReplayRequired,
			},
			{
				name: "restart recovery", route: game.CompetitiveSeriesRouteRecovery,
				seriesState: domain.SeriesStateActive,
				gameState:   domain.GameStatePaused,
				resumeState: domain.SeriesStateActive,
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				current := seriesExecutionFixture(
					domain.SeriesFormatBO3,
					test.seriesState,
					test.gameState,
				)
				resolved, changed, err := game.ResolveCompetitive(
					current,
					game.ResolutionCommand{Route: test.route},
				)
				require.NoError(t, err)
				require.True(t, changed)
				require.Equal(t, domain.SeriesStateTechnicalPause, resolved.Series.State)
				require.Equal(t, test.resumeState, *resolved.ResumeState)
				require.Nil(t, resolved.Series.WinnerID)
				require.NoError(t, game.ValidateResolution(test.route, resolved))
			})
		}
	})

	t.Run("tournament cancellation is the only no-winner terminal route", func(t *testing.T) {
		t.Parallel()

		current := seriesExecutionFixture(
			domain.SeriesFormatBO3,
			domain.SeriesStateActive,
			domain.GameStateActive,
		)
		scoreRevisionID := domain.SeriesScoreRevisionID(seriesFixtureID(45))
		resultRevisionID := domain.OfficialResultRevisionID(seriesFixtureID(46))
		terminal := &game.TerminalEvidence{
			Score:           current.Series.Score,
			ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &resultRevisionID,
		}

		cancelled, changed, err := game.ResolveCompetitive(current, game.ResolutionCommand{
			Route:    game.CompetitiveSeriesRouteTournamentCancellation,
			Terminal: terminal,
		})
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.SeriesStateCancelled, cancelled.Series.State)
		require.Nil(t, cancelled.Series.WinnerID)
		require.NoError(t, game.ValidateResolution(
			game.CompetitiveSeriesRouteTournamentCancellation,
			cancelled,
		))

		_, changed, err = game.ResolveCompetitive(current, game.ResolutionCommand{
			Route:    game.CompetitiveSeriesRouteDeadline,
			Terminal: terminal,
		})
		require.False(t, changed)
		require.ErrorIs(t, err, game.ErrInvalidCompetitiveSeriesResolution)
	})
}
