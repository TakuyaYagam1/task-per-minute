package websocket

import (
	"encoding/json"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/requestmeta"
)

const wsProblemContentType = "application/problem+json"

type handshakeProblem struct {
	Detail    *string `json:"detail,omitempty"`
	Instance  *string `json:"instance,omitempty"`
	RequestID *string `json:"request_id,omitempty"`
	Status    int32   `json:"status"`
	Title     string  `json:"title"`
	Type      string  `json:"type"`
}

func writeHandshakeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	instance := ""
	if r != nil && r.URL != nil {
		instance = r.URL.Path
	}
	problem := handshakeProblem{
		Type:     "about:blank",
		Title:    http.StatusText(status),
		Status:   problemStatus(status),
		Detail:   &detail,
		Instance: &instance,
	}
	if r != nil {
		if requestID := requestmeta.RequestIDFromContext(r.Context()); requestID != "" {
			problem.RequestID = &requestID
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
