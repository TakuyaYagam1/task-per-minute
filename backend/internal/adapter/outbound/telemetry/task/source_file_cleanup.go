package tasktelemetry

import (
	"context"

	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	logkit "github.com/wahrwelt-kit/go-logkit"
)

type SourceFileCleanupLogger struct {
	log logkit.Logger
}

var _ taskusecase.SourceFileCleanupObserver = (*SourceFileCleanupLogger)(nil)

func NewSourceFileCleanupLogger(log logkit.Logger) *SourceFileCleanupLogger {
	if log == nil {
		return nil
	}
	return &SourceFileCleanupLogger{log: log}
}

func (logger *SourceFileCleanupLogger) ObserveSourceFileCleanup(
	_ context.Context,
	failure taskusecase.SourceFileCleanupFailure,
) {
	if logger == nil || logger.log == nil || failure.Err == nil {
		return
	}
	fields := logkit.Fields{
		"operation": failure.Operation,
		"task_id":   failure.TaskID.String(),
		"error":     failure.Err.Error(),
	}
	if failure.ObjectKey != "" {
		fields["key"] = failure.ObjectKey
	}
	logger.log.Error("source file cleanup failed", fields)
}
