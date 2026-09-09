package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
)

func TestDownloadTaskSourceRedirectsToPresignedURL(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	sourceURL := "https://files.example.com/task-per-minute/tasks/source.zip?X-Amz-Signature=test"
	verifier := newAdminAccessVerifier(t)

	upload := NewMockUploadService(t)
	upload.EXPECT().PresignedSourceFileURL(mock.Anything, taskID).Return(sourceURL, nil)
	server := New(Dependencies{Upload: upload})
	handler := NewHandler(server, HandlerOptions{AdminAuth: verifier})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tasks/"+taskID.String()+"/source", nil)
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: adminAccessTestToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusFound, rr.Code)
	require.Equal(t, sourceURL, rr.Header().Get("Location"))
}
