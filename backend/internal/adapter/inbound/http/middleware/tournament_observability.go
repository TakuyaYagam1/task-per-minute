package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	appobservability "github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

const (
	tournamentHTTPEvent        = "tournament.http"
	tournamentHTTPUnscoped     = "unscoped"
	tournamentHTTPEntityKind   = "http_request"
	tournamentHTTPStatusPrefix = "status_"
)

// TournamentStructuredLogging emits one canonical event for each tournament HTTP request.
func TournamentStructuredLogging(observer appobservability.TournamentEventObserver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if observer == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			class, tournament := tournamentTransportClass(r)
			if !tournament {
				next.ServeHTTP(w, r)
				return
			}

			startedAt := time.Now()
			recorder := newStatusRecorder(w)
			next.ServeHTTP(recorder, r)
			status := recorder.Status()
			correlationID := tournamentTransportCorrelationID(r, class)
			if correlationID == "" {
				correlationID = tournamentHTTPUnscoped
			}
			_ = appobservability.EmitTournamentEvent(r.Context(), observer, appobservability.TournamentEventInput{
				Event:         tournamentHTTPEvent,
				Outcome:       tournamentHTTPOutcome(status),
				CorrelationID: correlationID,
				TournamentID:  tournamentTransportTournamentID(r),
				EntityKind:    tournamentHTTPEntityKind,
				EntityID:      correlationID,
				Stage:         class,
				Transition:    strings.ToLower(r.Method),
				Duration:      time.Since(startedAt),
				ReasonCode:    tournamentHTTPStatusPrefix + fmt.Sprint(status),
			})
		})
	}
}

func tournamentTransportCorrelationID(r *http.Request, class string) string {
	if r == nil {
		return ""
	}
	fallback := GetRequestIDFromCtx(r.Context())
	if class != "operator_mutation" && class != "participant_mutation" {
		return fallback
	}

	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || len(values[0]) != len(uuid.Nil.String()) {
		return fallback
	}
	commandID, err := uuid.Parse(values[0])
	if err != nil || commandID == uuid.Nil {
		return fallback
	}
	return commandID.String()
}

func tournamentTransportClass(r *http.Request) (string, bool) {
	if r == nil || r.URL == nil {
		return "", false
	}
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case r.URL.Path == "/api/v1/admin/tournaments" || strings.HasPrefix(r.URL.Path, "/api/v1/admin/tournaments/"):
		if read {
			return "operator_read", true
		}
		return "operator_mutation", true
	case strings.HasPrefix(r.URL.Path, "/api/v1/tournaments/") && strings.Contains(r.URL.Path, "/participant/"):
		if read {
			return "participant_read", true
		}
		return "participant_mutation", true
	case strings.HasPrefix(r.URL.Path, "/api/v1/tournaments/"):
		if read {
			return "public_read", true
		}
		return "unclassified", true
	default:
		return "", false
	}
}

func tournamentTransportTournamentID(r *http.Request) string {
	if r == nil || r.URL == nil {
		return tournamentHTTPUnscoped
	}
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	for index, segment := range segments {
		if segment == "tournaments" && index+1 < len(segments) && segments[index+1] != "" {
			return segments[index+1]
		}
	}
	return tournamentHTTPUnscoped
}

func tournamentHTTPOutcome(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return appobservability.TournamentOutcomeRetry
	case status >= http.StatusInternalServerError:
		return appobservability.TournamentOutcomeFailure
	case status >= http.StatusBadRequest:
		return appobservability.TournamentOutcomeRejected
	default:
		return appobservability.TournamentOutcomeSuccess
	}
}
