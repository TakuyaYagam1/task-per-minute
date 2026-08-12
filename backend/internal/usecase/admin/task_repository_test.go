package admin_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

type taskRepositoryMock struct {
	mock.Mock
}

func newTaskRepositoryMock(t *testing.T) *taskRepositoryMock {
	t.Helper()
	repository := &taskRepositoryMock{}
	repository.Test(t)
	t.Cleanup(func() {
		repository.AssertExpectations(t)
	})
	return repository
}

func (m *taskRepositoryMock) Create(ctx context.Context, in admin.TaskInput) (*domain.Task, error) {
	result := m.Called(ctx, in)
	if run, ok := result.Get(0).(func(context.Context, admin.TaskInput) (*domain.Task, error)); ok {
		return run(ctx, in)
	}
	var task *domain.Task
	if result.Get(0) != nil {
		task = result.Get(0).(*domain.Task)
	}
	return task, result.Error(1)
}

func (m *taskRepositoryMock) GetByID(ctx context.Context, id uuid.UUID) (*domain.Task, error) {
	result := m.Called(ctx, id)
	if run, ok := result.Get(0).(func(context.Context, uuid.UUID) (*domain.Task, error)); ok {
		return run(ctx, id)
	}
	var task *domain.Task
	if result.Get(0) != nil {
		task = result.Get(0).(*domain.Task)
	}
	return task, result.Error(1)
}

func (m *taskRepositoryMock) List(ctx context.Context) ([]*domain.Task, error) {
	result := m.Called(ctx)
	if run, ok := result.Get(0).(func(context.Context) ([]*domain.Task, error)); ok {
		return run(ctx)
	}
	var tasks []*domain.Task
	if result.Get(0) != nil {
		tasks = result.Get(0).([]*domain.Task)
	}
	return tasks, result.Error(1)
}

func (m *taskRepositoryMock) Update(
	ctx context.Context,
	id uuid.UUID,
	in admin.TaskInput,
) (*domain.Task, error) {
	result := m.Called(ctx, id, in)
	if run, ok := result.Get(0).(func(context.Context, uuid.UUID, admin.TaskInput) (*domain.Task, error)); ok {
		return run(ctx, id, in)
	}
	var task *domain.Task
	if result.Get(0) != nil {
		task = result.Get(0).(*domain.Task)
	}
	return task, result.Error(1)
}

func (m *taskRepositoryMock) Delete(ctx context.Context, id uuid.UUID) error {
	return m.Called(ctx, id).Error(0)
}

func (m *taskRepositoryMock) IsUsedInActiveDuel(ctx context.Context, id uuid.UUID) (bool, error) {
	result := m.Called(ctx, id)
	return result.Bool(0), result.Error(1)
}

var (
	_ admin.TaskRepository       = (*taskRepositoryMock)(nil)
	_ admin.UploadTaskRepository = (*taskRepositoryMock)(nil)
)
