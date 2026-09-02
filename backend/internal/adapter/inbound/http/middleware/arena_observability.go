package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	appobservability "github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

const (
	arenaHTTPEvent        = "arena.http"
	arenaHTTPUnscoped     = "unscoped"
	arenaHTTPEntityKind   = "http_request"
	arenaHTTPStatusPrefix = "status_"
)

// ArenaStructuredLogging emits one canonical event for each Arena HTTP request.
func ArenaStructuredLogging(observer appobservability.ArenaEventObserver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if observer == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			class, arena := arenaTransportClass(r)
			if !arena {
				next.ServeHTTP(w, r)
				return
			}

			startedAt := time.Now()
			recorder := newStatusRecorder(w)
			next.ServeHTTP(recorder, r)
			status := recorder.Status()
			correlationID := GetRequestIDFromCtx(r.Context())
			if correlationID == "" {
				correlationID = arenaHTTPUnscoped
			}
			_ = appobservability.EmitArenaEvent(r.Context(), observer, appobservability.ArenaEventInput{
				Event:         arenaHTTPEvent,
				Outcome:       arenaHTTPOutcome(status),
				CorrelationID: correlationID,
				TournamentID:  arenaTransportTournamentID(r),
				EntityKind:    arenaHTTPEntityKind,
				EntityID:      correlationID,
				Stage:         class,
				Transition:    strings.ToLower(r.Method),
				Duration:      time.Since(startedAt),
				ReasonCode:    arenaHTTPStatusPrefix + fmt.Sprint(status),
			})
		})
	}
}

func arenaTransportClass(r *http.Request) (string, bool) {
	if r == nil || r.URL == nil || !strings.HasPrefix(r.URL.Path, "/api/v1/arena/") {
		return "", false
	}
	if class, ok := classifyArenaEndpoint(r); ok {
		return string(class), true
	}
	return "unclassified", true
}

func arenaTransportTournamentID(r *http.Request) string {
	if r == nil || r.URL == nil {
		return arenaHTTPUnscoped
	}
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	for index, segment := range segments {
		if segment == "tournaments" && index+1 < len(segments) && segments[index+1] != "" {
			return segments[index+1]
		}
	}
	return arenaHTTPUnscoped
}

func arenaHTTPOutcome(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return appobservability.ArenaOutcomeRetry
	case status >= http.StatusInternalServerError:
		return appobservability.ArenaOutcomeFailure
	case status >= http.StatusBadRequest:
		return appobservability.ArenaOutcomeRejected
	default:
		return appobservability.ArenaOutcomeSuccess
	}
}
