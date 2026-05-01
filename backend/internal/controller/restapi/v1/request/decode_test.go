package request_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/request"
)

type decodeDTO struct {
	Name string `json:"name"`
}

func TestDecodeJSON_Success(t *testing.T) {
	t.Parallel()

	var got decodeDTO
	req := newJSONRequest(`{"name":"alice"}`)

	require.NoError(t, request.DecodeJSON(req, &got))
	require.Equal(t, decodeDTO{Name: "alice"}, got)
}

func TestDecodeJSON_RejectsUnknownFields(t *testing.T) {
	t.Parallel()

	var got decodeDTO
	req := newJSONRequest(`{"name":"alice","extra":true}`)

	err := request.DecodeJSON(req, &got)

	var unknown *request.UnknownFieldsErr
	require.ErrorAs(t, err, &unknown)
	require.Equal(t, "extra", unknown.Field)
}

func TestDecodeJSON_RejectsBodyOverOneMB(t *testing.T) {
	t.Parallel()

	var got decodeDTO
	body := `{"name":"` + strings.Repeat("a", 1<<20) + `"}`
	req := newJSONRequest(body)

	require.ErrorIs(t, request.DecodeJSON(req, &got), request.ErrBodyTooLarge)
}

func TestDecodeJSON_RejectsEmptyBody(t *testing.T) {
	t.Parallel()

	var got decodeDTO
	req := newJSONRequest("   ")

	require.ErrorIs(t, request.DecodeJSON(req, &got), request.ErrEmptyBody)
}

func TestDecodeJSON_RejectsTrailingData(t *testing.T) {
	t.Parallel()

	var got decodeDTO
	req := newJSONRequest(`{"name":"alice"} {"name":"bob"}`)

	require.ErrorIs(t, request.DecodeJSON(req, &got), request.ErrTrailingData)
}

func TestDecodeJSON_ReturnsSyntaxError(t *testing.T) {
	t.Parallel()

	var got decodeDTO
	req := newJSONRequest(`{"name":`)

	err := request.DecodeJSON(req, &got)

	require.Error(t, err)
}

func TestDecodeJSON_NilBodyReturnsEmptyBody(t *testing.T) {
	t.Parallel()

	var got decodeDTO
	req := httptest.NewRequest(http.MethodPost, "/api/v1/test", nil)
	req.Body = nil

	require.ErrorIs(t, request.DecodeJSON(req, &got), request.ErrEmptyBody)
}

func newJSONRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}
