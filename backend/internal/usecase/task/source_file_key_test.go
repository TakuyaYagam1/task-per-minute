package task

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSourceFileKeyFromURLAcceptsOnlyVersionedTaskObject(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	uploadID := uuid.New()
	key := sourceFileUploadKey(taskID, uploadID)

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "canonical object URL",
			raw:  "http://seaweed:8333/task-per-minute/" + key,
			want: key,
		},
		{
			name: "https object URL",
			raw:  "https://files.example.test/bucket/" + key,
			want: key,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := sourceFileKeyFromURL(taskID, tt.raw)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSourceFileKeyFromURLRejectsLegacyAndForeignObjects(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	uploadID := uuid.New()
	foreignTaskID := uuid.New()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "legacy stable key", raw: "http://seaweed/bucket/tasks/" + taskID.String() + "/source.zip"},
		{name: "foreign task", raw: "http://seaweed/bucket/" + sourceFileUploadKey(foreignTaskID, uploadID)},
		{name: "malformed URL", raw: "://bad"},
		{name: "relative URL", raw: "/bucket/" + sourceFileUploadKey(taskID, uploadID)},
		{name: "wrong scheme", raw: "file://seaweed/bucket/" + sourceFileUploadKey(taskID, uploadID)},
		{name: "non canonical upload ID", raw: "http://seaweed/bucket/tasks/" + taskID.String() + "/sources/" + strings.ToUpper(uploadID.String()) + ".zip"},
		{name: "nested object", raw: "http://seaweed/bucket/" + sourceFileUploadKey(taskID, uploadID) + "/extra"},
		{name: "encoded separator", raw: "http://seaweed/bucket/tasks/" + taskID.String() + "/sources%2F" + uploadID.String() + ".zip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := sourceFileKeyFromURL(taskID, tt.raw)
			require.ErrorIs(t, err, errInvalidSourceFileURL)
			require.Empty(t, got)
		})
	}
}
