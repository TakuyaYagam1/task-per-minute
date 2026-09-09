package game_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

func TestFailureClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		class  game.FailureClass
		reason domain.GameResultReason
	}{
		{game.FailureNoSolve, domain.GameResultReasonNoSolve},
		{game.FailureTask, domain.GameResultReasonTaskFailure},
		{game.FailureCommonPlatform, domain.GameResultReasonCommonPlatformFailure},
		{game.FailureDisconnect, domain.GameResultReasonDisconnect},
		{game.FailureExecutionEpochBreak, domain.GameResultReasonExecutionEpochBreak},
	}

	for _, test := range tests {
		t.Run(string(test.class), func(t *testing.T) {
			t.Parallel()

			decision, err := game.ClassifyFailure(test.class, domain.CategoryWeb)
			require.NoError(t, err)
			require.NoError(t, decision.Validate())
			require.Equal(t, test.reason, decision.Reason)
			require.Equal(t, domain.GameStateVoid, decision.GameState)
			require.Equal(t, domain.SeriesStateReplayRequired, decision.SeriesState)
			require.Equal(t, domain.CategoryWeb, decision.CategoryCutoff)
			require.True(t, decision.Reason.IsLegalFor(decision.GameState))
		})
	}

	_, err := game.ClassifyFailure("unknown", domain.CategoryWeb)
	require.ErrorIs(t, err, game.ErrInvalidFailureClassification)
	_, err = game.ClassifyFailure(game.FailureNoSolve, "unknown")
	require.ErrorIs(t, err, game.ErrInvalidFailureClassification)

	tampered, err := game.ClassifyFailure(
		game.FailureDisconnect,
		domain.CategoryWeb,
	)
	require.NoError(t, err)
	tampered.Reason = domain.GameResultReasonSolved
	require.ErrorIs(t, tampered.Validate(), game.ErrInvalidFailureClassification)
}
