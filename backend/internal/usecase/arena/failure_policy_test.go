package arena_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestFailureClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		class  arena.NormalAttemptFailureClass
		reason domain.ArenaGameResultReason
	}{
		{arena.NormalAttemptFailureNoSolve, domain.ArenaGameResultReasonNoSolve},
		{arena.NormalAttemptFailureTask, domain.ArenaGameResultReasonTaskFailure},
		{arena.NormalAttemptFailureCommonPlatform, domain.ArenaGameResultReasonCommonPlatformFailure},
		{arena.NormalAttemptFailureDisconnect, domain.ArenaGameResultReasonDisconnect},
		{arena.NormalAttemptFailureExecutionEpochBreak, domain.ArenaGameResultReasonExecutionEpochBreak},
	}

	for _, test := range tests {
		t.Run(string(test.class), func(t *testing.T) {
			t.Parallel()

			decision, err := arena.ClassifyNormalAttemptFailure(test.class, domain.CategoryWeb)
			require.NoError(t, err)
			require.NoError(t, decision.Validate())
			require.Equal(t, test.reason, decision.Reason)
			require.Equal(t, domain.ArenaGameStateVoid, decision.GameState)
			require.Equal(t, domain.ArenaSeriesStateReplayRequired, decision.SeriesState)
			require.Equal(t, domain.CategoryWeb, decision.CategoryCutoff)
			require.True(t, decision.Reason.IsLegalFor(decision.GameState))
		})
	}

	_, err := arena.ClassifyNormalAttemptFailure("unknown", domain.CategoryWeb)
	require.ErrorIs(t, err, arena.ErrInvalidFailureClassification)
	_, err = arena.ClassifyNormalAttemptFailure(arena.NormalAttemptFailureNoSolve, "unknown")
	require.ErrorIs(t, err, arena.ErrInvalidFailureClassification)

	tampered, err := arena.ClassifyNormalAttemptFailure(
		arena.NormalAttemptFailureDisconnect,
		domain.CategoryWeb,
	)
	require.NoError(t, err)
	tampered.Reason = domain.ArenaGameResultReasonSolved
	require.True(t, errors.Is(tampered.Validate(), arena.ErrInvalidFailureClassification))
}

func task040ID(value int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("task-040-%d", value)))
}
