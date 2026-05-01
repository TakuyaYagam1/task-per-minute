package v1

import (
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	instance := r.URL.Path
	requestID := middleware.GetRequestIDFromCtx(r.Context())
	response.WriteProblem(w, status, openapi.ProblemDetails{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    problemStatus(status),
		Detail:    &detail,
		Instance:  &instance,
		RequestId: &requestID,
	})
}

func problemStatus(status int) int32 {
	if status < http.StatusBadRequest || status > http.StatusNetworkAuthenticationRequired {
		return http.StatusInternalServerError
	}
	return int32(status)
}
