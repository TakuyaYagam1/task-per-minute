package arena_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestSeriesNoDrawInvariant(t *testing.T) {
	t.Parallel()

	t.Run("BO1 and BO3 complete with exactly one score winner", func(t *testing.T) {
		t.Parallel()

		for _, format := range []domain.ArenaSeriesFormat{
			domain.ArenaSeriesFormatBO1,
			domain.ArenaSeriesFormatBO3,
		} {
			current := task039SeriesExecution(format, domain.ArenaSeriesStateActive, domain.ArenaGameStateActive)
			winnerID := current.Series.FirstParticipantID
			current = task039CompleteCurrentGame(
				current,
				winnerID,
				domain.ArenaGameResultReasonSolved,
				domain.ArenaOfficialResultRevisionID(task039ID(20+format.WinsRequired())),
			)
			score := current.Series.Score
			score.FirstParticipantWins = format.WinsRequired()
			scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task039ID(30 + format.WinsRequired()))
			resultRevisionID := domain.ArenaOfficialResultRevisionID(task039ID(40 + format.WinsRequired()))

			resolved, changed, err := arena.ResolveCompetitiveSeries(current, arena.CompetitiveSeriesResolutionCommand{
				Route: arena.CompetitiveSeriesRouteFairResult,
				Terminal: &arena.SeriesTerminalEvidence{
					Score: score, WinnerID: &winnerID,
					ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &resultRevisionID,
				},
			})
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, domain.ArenaSeriesStateCompleted, resolved.Series.State)
			require.Equal(t, winnerID, *resolved.Series.WinnerID)
			require.NoError(t, arena.ValidateCompetitiveSeriesResolution(
				arena.CompetitiveSeriesRouteFairResult,
				resolved,
			))
		}
	})

	t.Run("completed draw and invented winner are rejected", func(t *testing.T) {
		t.Parallel()

		current := task039SeriesExecution(
			domain.ArenaSeriesFormatBO3,
			domain.ArenaSeriesStateActive,
			domain.ArenaGameStateActive,
		)
		scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task039ID(43))
		resultRevisionID := domain.ArenaOfficialResultRevisionID(task039ID(44))

		_, changed, err := arena.ResolveCompetitiveSeries(current, arena.CompetitiveSeriesResolutionCommand{
			Route: arena.CompetitiveSeriesRouteFairResult,
			Terminal: &arena.SeriesTerminalEvidence{
				Score:           current.Series.Score,
				ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &resultRevisionID,
			},
		})
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrCompetitiveSeriesDraw)

		inventedWinner := current.Series.FirstParticipantID
		_, changed, err = arena.ResolveCompetitiveSeries(current, arena.CompetitiveSeriesResolutionCommand{
			Route: arena.CompetitiveSeriesRouteFairResult,
			Terminal: &arena.SeriesTerminalEvidence{
				Score: current.Series.Score, WinnerID: &inventedWinner,
				ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &resultRevisionID,
			},
		})
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrCompetitiveSeriesDraw)

		validScore := current.Series.Score
		validScore.FirstParticipantWins = current.Series.Format.WinsRequired()
		_, changed, err = arena.ResolveCompetitiveSeries(current, arena.CompetitiveSeriesResolutionCommand{
			Route: arena.CompetitiveSeriesRouteFairResult,
			Terminal: &arena.SeriesTerminalEvidence{
				Score: validScore, WinnerID: &inventedWinner,
				ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &resultRevisionID,
			},
		})
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrInvalidCompetitiveSeriesResolution)
	})

	t.Run("deadline and correction require replay", func(t *testing.T) {
		t.Parallel()

		for _, route := range []arena.CompetitiveSeriesRoute{
			arena.CompetitiveSeriesRouteDeadline,
			arena.CompetitiveSeriesRouteCorrection,
		} {
			current := task039SeriesExecution(
				domain.ArenaSeriesFormatBO1,
				domain.ArenaSeriesStateActive,
				domain.ArenaGameStateActive,
			)
			resolved, changed, err := arena.ResolveCompetitiveSeries(current, arena.CompetitiveSeriesResolutionCommand{
				Route: route,
			})
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, domain.ArenaSeriesStateReplayRequired, resolved.Series.State)
			require.Nil(t, resolved.Series.WinnerID)
			require.NoError(t, arena.ValidateCompetitiveSeriesResolution(route, resolved))
		}
	})

	t.Run("replay exhaustion and recovery require technical pause", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name        string
			route       arena.CompetitiveSeriesRoute
			seriesState domain.ArenaSeriesState
			gameState   domain.ArenaGameState
			resumeState domain.ArenaSeriesState
		}{
			{
				name: "replay exhaustion", route: arena.CompetitiveSeriesRouteReplayExhausted,
				seriesState: domain.ArenaSeriesStateReplayRequired,
				gameState:   domain.ArenaGameStateVoid,
				resumeState: domain.ArenaSeriesStateReplayRequired,
			},
			{
				name: "restart recovery", route: arena.CompetitiveSeriesRouteRecovery,
				seriesState: domain.ArenaSeriesStateActive,
				gameState:   domain.ArenaGameStatePaused,
				resumeState: domain.ArenaSeriesStateActive,
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				current := task039SeriesExecution(
					domain.ArenaSeriesFormatBO3,
					test.seriesState,
					test.gameState,
				)
				resolved, changed, err := arena.ResolveCompetitiveSeries(
					current,
					arena.CompetitiveSeriesResolutionCommand{Route: test.route},
				)
				require.NoError(t, err)
				require.True(t, changed)
				require.Equal(t, domain.ArenaSeriesStateTechnicalPause, resolved.Series.State)
				require.Equal(t, test.resumeState, *resolved.ResumeState)
				require.Nil(t, resolved.Series.WinnerID)
				require.NoError(t, arena.ValidateCompetitiveSeriesResolution(test.route, resolved))
			})
		}
	})

	t.Run("tournament cancellation is the only no-winner terminal route", func(t *testing.T) {
		t.Parallel()

		current := task039SeriesExecution(
			domain.ArenaSeriesFormatBO3,
			domain.ArenaSeriesStateActive,
			domain.ArenaGameStateActive,
		)
		scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task039ID(45))
		resultRevisionID := domain.ArenaOfficialResultRevisionID(task039ID(46))
		terminal := &arena.SeriesTerminalEvidence{
			Score:           current.Series.Score,
			ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &resultRevisionID,
		}

		cancelled, changed, err := arena.ResolveCompetitiveSeries(current, arena.CompetitiveSeriesResolutionCommand{
			Route:    arena.CompetitiveSeriesRouteTournamentCancellation,
			Terminal: terminal,
		})
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.ArenaSeriesStateCancelled, cancelled.Series.State)
		require.Nil(t, cancelled.Series.WinnerID)
		require.NoError(t, arena.ValidateCompetitiveSeriesResolution(
			arena.CompetitiveSeriesRouteTournamentCancellation,
			cancelled,
		))

		_, changed, err = arena.ResolveCompetitiveSeries(current, arena.CompetitiveSeriesResolutionCommand{
			Route:    arena.CompetitiveSeriesRouteDeadline,
			Terminal: terminal,
		})
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrInvalidCompetitiveSeriesResolution)
	})
}
