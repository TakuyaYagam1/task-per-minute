package task_test

import (
	"testing"

	taskmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task/mocks"
)

func newTaskRepositoryMock(t *testing.T) *taskmocks.MockRepository {
	t.Helper()
	return taskmocks.NewMockRepository(t)
}
