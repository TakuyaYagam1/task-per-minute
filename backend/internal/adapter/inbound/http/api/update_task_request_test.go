package api_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestUpdateTaskRequestMarshalOmitsAbsentNullableFields(t *testing.T) {
	title := "updated title"

	body, err := json.Marshal(api.UpdateTaskRequest{Title: &title})

	require.NoError(t, err)
	require.JSONEq(t, `{"title":"updated title"}`, string(body))
	require.NotContains(t, string(body), "clear_source_file")
	require.NotContains(t, string(body), "task_url")
}

func TestUpdateTaskRequestMarshalExplicitFields(t *testing.T) {
	taskURL := "https://task.example"
	clearSourceFile := true

	body, err := json.Marshal(api.UpdateTaskRequest{
		TaskUrl:         api.NewNullableString(taskURL),
		ClearSourceFile: &clearSourceFile,
	})

	require.NoError(t, err)
	require.JSONEq(t, `{"task_url":"https://task.example","clear_source_file":true}`, string(body))
}

func TestUpdateTaskRequestUnmarshalNullableFields(t *testing.T) {
	var body api.UpdateTaskRequest

	require.NoError(t, json.Unmarshal([]byte(`{"task_url":null,"clear_source_file":true}`), &body))

	taskURL, taskURLSet := body.TaskUrl.Value()
	require.True(t, taskURLSet)
	require.Nil(t, taskURL)

	require.NotNil(t, body.ClearSourceFile)
	require.True(t, *body.ClearSourceFile)
}
