package tasktelemetry

import (
	"bytes"
	"errors"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"
	"testing"
)

func TestSourceFileCleanupLoggerWritesStructuredFailure(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	log, err := logkit.New(
		logkit.WithLevel(logkit.DebugLevel),
		logkit.WithSyncWriter(&output),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Close()) })

	taskID := uuid.New()
	NewSourceFileCleanupLogger(log).ObserveSourceFileCleanup(t.Context(), taskusecase.SourceFileCleanupFailure{
		Operation: "upload_replace_old",
		TaskID:    taskID,
		ObjectKey: "tasks/source.zip",
		Err:       errors.New("storage unavailable"),
	})

	require.Contains(t, output.String(), "source file cleanup failed")
	require.Contains(t, output.String(), "upload_replace_old")
	require.Contains(t, output.String(), taskID.String())
	require.Contains(t, output.String(), "tasks/source.zip")
	require.Contains(t, output.String(), "storage unavailable")
}

func TestSourceFileCleanupLoggerRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	require.Nil(t, NewSourceFileCleanupLogger(nil))
	var logger *SourceFileCleanupLogger
	require.NotPanics(t, func() {
		logger.ObserveSourceFileCleanup(t.Context(), taskusecase.SourceFileCleanupFailure{})
	})
}
