//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

func TestAdminUploadUsecase_UploadSourceFile_HappyPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newDuelFixture()
	st := newSeaweedStorage(t)
	uc := admin.NewUploadUsecase(f.tasks, st)
	task := f.makeForensicsTask(t, uniq("forensics"), 90)
	payload := []byte{'P', 'K', 0x03, 0x04, 'z', 'i', 'p'}

	presignedURL, err := uc.UploadSourceFile(
		ctx, task.ID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)
	require.NoError(t, err)
	require.Contains(t, presignedURL, "X-Amz-Signature")
	require.Contains(t, presignedURL, "tasks/"+task.ID.String()+"/sources/")

	got, err := f.tasks.GetByID(ctx, task.ID)
	require.NoError(t, err)
	require.NotNil(t, got.SourceFileURL)
	require.Contains(t, *got.SourceFileURL, "tasks/"+task.ID.String()+"/sources/")
	require.NotContains(t, *got.SourceFileURL, "X-Amz-Signature")

	resp := httpGetWithTimeout(t, presignedURL)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, payload, body)
}
