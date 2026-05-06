package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestTaskSourceFileKeyFromURLExtractsVersionedAndLegacyKeys(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	uploadID := uuid.New()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "versioned",
			raw:  "http://seaweed:8333/task-per-minute/" + domain.TaskSourceFileUploadKey(taskID, uploadID),
			want: domain.TaskSourceFileUploadKey(taskID, uploadID),
		},
		{
			name: "legacy",
			raw:  "http://seaweed:8333/task-per-minute/" + domain.TaskSourceFileKey(taskID),
			want: domain.TaskSourceFileKey(taskID),
		},
		{
			name: "malformed fallback",
			raw:  "://bad",
			want: domain.TaskSourceFileKey(taskID),
		},
		{
			name: "foreign task fallback",
			raw:  "http://seaweed:8333/task-per-minute/" + domain.TaskSourceFileUploadKey(uuid.New(), uploadID),
			want: domain.TaskSourceFileKey(taskID),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, domain.TaskSourceFileKeyFromURL(taskID, tt.raw))
		})
	}
}
