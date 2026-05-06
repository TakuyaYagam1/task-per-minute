package openapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

func TestUpdateTaskRequestMarshalOmitsAbsentNullableFields(t *testing.T) {
	title := "updated title"

	body, err := json.Marshal(openapi.UpdateTaskRequest{Title: &title})

	require.NoError(t, err)
	require.JSONEq(t, `{"title":"updated title"}`, string(body))
	require.NotContains(t, string(body), "source_file_url")
	require.NotContains(t, string(body), "task_url")
}

func TestUpdateTaskRequestMarshalExplicitNullableFields(t *testing.T) {
	taskURL := "https://task.example"

	body, err := json.Marshal(openapi.UpdateTaskRequest{
		TaskUrl:       openapi.NewNullableString(taskURL),
		SourceFileUrl: openapi.NullString(),
	})

	require.NoError(t, err)
	require.JSONEq(t, `{"task_url":"https://task.example","source_file_url":null}`, string(body))
}

func TestUpdateTaskRequestUnmarshalNullableFields(t *testing.T) {
	var body openapi.UpdateTaskRequest

	require.NoError(t, json.Unmarshal([]byte(`{"task_url":null,"source_file_url":"https://files.example/source.zip"}`), &body))

	taskURL, taskURLSet := body.TaskUrl.Value()
	require.True(t, taskURLSet)
	require.Nil(t, taskURL)

	sourceFileURL, sourceFileURLSet := body.SourceFileUrl.Value()
	require.True(t, sourceFileURLSet)
	require.NotNil(t, sourceFileURL)
	require.Equal(t, "https://files.example/source.zip", *sourceFileURL)
}
