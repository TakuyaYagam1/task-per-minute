package websocket

import (
	"encoding/json"
	"net/http"

	restmw "github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

const wsProblemContentType = "application/problem+json"

func writeHandshakeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	instance := ""
	if r != nil && r.URL != nil {
		instance = r.URL.Path
	}
	problem := openapi.ProblemDetails{
		Type:     "about:blank",
		Title:    http.StatusText(status),
		Status:   problemStatus(status),
		Detail:   &detail,
		Instance: &instance,
	}
	if r != nil {
		if requestID := restmw.GetRequestIDFromCtx(r.Context()); requestID != "" {
			problem.RequestId = &requestID
		}
	}

	w.Header().Set("Content-Type", wsProblemContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem)
}

func problemStatus(status int) int32 {
	if status < http.StatusBadRequest || status > http.StatusNetworkAuthenticationRequired {
		return http.StatusInternalServerError
	}
	return int32(status)
}
