package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestNormalNoShowScopeValidation(t *testing.T) {
	t.Parallel()

	scope := domain.NormalNoShowScope{
		TournamentID: uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		WaveID:       uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		WindowID:     uuid.MustParse("33333333-3333-4333-8333-333333333333"),
		SeriesID:     uuid.MustParse("44444444-4444-4444-8444-444444444444"),
	}
	require.True(t, scope.IsValid())

	scope.WindowID = uuid.Nil
	require.False(t, scope.IsValid())
}
