package v1

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestUpdateTaskInputPreservesSourceFileWithoutClearCommand(t *testing.T) {
	t.Parallel()

	sourceFileURL := "https://files.example/source.zip"
	existing := taskWithSourceFile(sourceFileURL)

	input := updateTaskInput(existing, api.UpdateTaskRequest{})

	require.Equal(t, &sourceFileURL, input.SourceFileURL)
}

func TestUpdateTaskInputIgnoresFalseClearCommand(t *testing.T) {
	t.Parallel()

	sourceFileURL := "https://files.example/source.zip"
	clearSourceFile := false
	existing := taskWithSourceFile(sourceFileURL)

	input := updateTaskInput(existing, api.UpdateTaskRequest{ClearSourceFile: &clearSourceFile})

	require.Equal(t, &sourceFileURL, input.SourceFileURL)
}

func TestUpdateTaskInputClearsSourceFileOnExplicitCommand(t *testing.T) {
	t.Parallel()

	sourceFileURL := "https://files.example/source.zip"
	clearSourceFile := true
	existing := taskWithSourceFile(sourceFileURL)

	input := updateTaskInput(existing, api.UpdateTaskRequest{ClearSourceFile: &clearSourceFile})

	require.Nil(t, input.SourceFileURL)
}

func TestCreateTaskInputKeepsOmittedAuthorityFieldsUnset(t *testing.T) {
	t.Parallel()

	input := createTaskInput(api.CreateTaskRequest{})

	require.Empty(t, input.Kind)
	require.Nil(t, input.Enabled)
}

func TestCreateTaskInputPreservesExplicitKindAndEnabled(t *testing.T) {
	t.Parallel()

	kind := api.TaskKindGolden
	enabled := false
	input := createTaskInput(api.CreateTaskRequest{Kind: &kind, Enabled: &enabled})

	require.Equal(t, domain.TaskKindGolden, input.Kind)
	require.NotNil(t, input.Enabled)
	require.False(t, *input.Enabled)
}

func TestUpdateTaskInputPreservesOrChangesAuthorityFields(t *testing.T) {
	t.Parallel()

	existing := &domain.Task{Kind: domain.TaskKindGolden, Enabled: true}
	preserved := updateTaskInput(existing, api.UpdateTaskRequest{})
	require.Equal(t, domain.TaskKindGolden, preserved.Kind)
	require.True(t, preserved.Enabled)

	kind := api.TaskKindNormal
	enabled := false
	updated := updateTaskInput(existing, api.UpdateTaskRequest{Kind: &kind, Enabled: &enabled})
	require.Equal(t, domain.TaskKindNormal, updated.Kind)
	require.False(t, updated.Enabled)
}

func taskWithSourceFile(sourceFileURL string) *domain.Task {
	return &domain.Task{SourceFileURL: &sourceFileURL}
}
