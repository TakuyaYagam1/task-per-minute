package v1

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

func TestDownloadTaskSourceRedirectsToPresignedURL(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	sourceURL := "https://files.example.com/task-per-minute/tasks/source.zip?X-Amz-Signature=test"
	auth := newAdminCookieAuthUsecase(t)
	pair, err := auth.Login(t.Context(), "admin-password")
	require.NoError(t, err)

	upload := &sourceDownloadUploadStub{sourceURL: sourceURL}
	server := New(Dependencies{AdminAuth: auth, Upload: upload})
	handler := NewHandler(server, HandlerOptions{AdminAuth: auth})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tasks/"+taskID.String()+"/source", nil)
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: pair.AccessToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusFound, rr.Code)
	require.Equal(t, sourceURL, rr.Header().Get("Location"))
	require.Equal(t, taskID, upload.taskID)
}

type sourceDownloadUploadStub struct {
	sourceURL string
	taskID    uuid.UUID
}

func (s *sourceDownloadUploadStub) UploadSourceFile(context.Context, uuid.UUID, io.Reader, int64, string) (string, error) {
	panic("unused")
}

func (s *sourceDownloadUploadStub) ClearSourceFile(context.Context, uuid.UUID, usecase.TaskInput) (*domain.Task, error) {
	panic("unused")
}

func (s *sourceDownloadUploadStub) PresignedSourceFileURL(_ context.Context, taskID uuid.UUID) (string, error) {
	s.taskID = taskID
	return s.sourceURL, nil
}

func (s *sourceDownloadUploadStub) DeleteSourceFile(context.Context, uuid.UUID, *string) error {
	panic("unused")
}
