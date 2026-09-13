package task_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	taskmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task/mocks"
)

func TestSourceFilesPresignCanonicalSourceFileURLUsesImmutableKey(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	uploadID := uuid.New()
	key := fmt.Sprintf("tasks/%s/sources/%s.zip", taskID, uploadID)
	ttl := 5 * time.Minute
	storage := taskmocks.NewMockSourceFileStorage(t)
	storage.On("PresignedGetURL", mock.Anything, key, ttl).Return(func(_ context.Context, gotKey string, gotTTL time.Duration) (string, error) {
		require.Equal(t, key, gotKey)
		require.Equal(t, ttl, gotTTL)
		return "https://files.example.test/archive.zip?signature=fresh", nil
	}).Once()

	got, err := taskusecase.NewSourceFiles(nil, storage, nil).PresignCanonicalSourceFileURL(
		t.Context(), taskID, "http://seaweed:8333/task-per-minute/"+key, ttl,
	)
	require.NoError(t, err)
	require.Contains(t, got, "files.example.test")
}

func TestSourceFilesPresignCanonicalSourceFileURLRejectsForeignTask(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	foreignKey := fmt.Sprintf("tasks/%s/sources/%s.zip", uuid.New(), uuid.New())
	got, err := taskusecase.NewSourceFiles(nil, taskmocks.NewMockSourceFileStorage(t), nil).
		PresignCanonicalSourceFileURL(t.Context(), taskID, "http://seaweed:8333/task-per-minute/"+foreignKey, time.Minute)
	require.Error(t, err)
	require.Empty(t, got)
}
