package middleware

import (
	"encoding/json"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

const problemContentType = "application/problem+json"

func writeUnauthorized(w http.ResponseWriter, r *http.Request, detail string) {
	writeProblem(w, r, http.StatusUnauthorized, "Unauthorized", detail)
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	writeProblemWithCode(w, r, status, title, detail, "")
}

func writeProblemWithCode(w http.ResponseWriter, r *http.Request, status int, title, detail, code string) {
	instance := r.URL.Path
	problem := api.ProblemDetails{
		Type:     "about:blank",
		Title:    title,
		Status:   problemStatus(status),
		Detail:   &detail,
		Instance: &instance,
	}
	if code != "" {
		problem.Code = &code
	}
	if requestID := GetRequestIDFromCtx(r.Context()); requestID != "" {
		problem.RequestId = &requestID
	}

	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(problem); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func problemStatus(status int) int32 {
	if status < http.StatusBadRequest || status > http.StatusNetworkAuthenticationRequired {
		return http.StatusInternalServerError
	}
	return int32(status)
}
