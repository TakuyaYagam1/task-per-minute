package response

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestTaskIncludesServerOwnedKindAndEnabledState(t *testing.T) {
	t.Parallel()

	got := Task(&domain.Task{
		ID: uuid.New(), Title: "task", Description: "description", Category: domain.CategoryWeb,
		Difficulty: domain.DifficultyEasy, TimeLimit: 60, Flag: "FLAG{task}",
		Kind: domain.TaskKindGolden, Enabled: false,
		CreatedAt: time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC),
	})

	require.Equal(t, api.TaskKindGolden, got.Kind)
	require.False(t, got.Enabled)
}
