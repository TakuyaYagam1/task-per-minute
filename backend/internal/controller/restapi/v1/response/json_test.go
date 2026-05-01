package response_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/response"
)

func TestWriteJSON_WritesStatusHeaderAndBody(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()

	response.WriteJSON(rr, http.StatusCreated, map[string]string{"id": "task-1"})

	require.Equal(t, http.StatusCreated, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	require.JSONEq(t, `{"id":"task-1"}`, rr.Body.String())
}

func TestWriteJSON_NoContentSkipsBody(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()

	response.WriteJSON(rr, http.StatusNoContent, map[string]string{"ignored": "true"})

	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	require.Empty(t, rr.Body.String())
}

func TestWriteProblem_WritesProblemContentType(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()

	response.WriteProblem(rr, http.StatusBadRequest, map[string]string{"title": "Bad Request"})

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
	require.JSONEq(t, `{"title":"Bad Request"}`, rr.Body.String())
}
