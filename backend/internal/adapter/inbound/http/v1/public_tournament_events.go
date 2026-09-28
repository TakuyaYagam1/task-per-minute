package v1

import (
	"net/http"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const publicTournamentEventsHeartbeat = 25 * time.Second

func (s *Server) StreamPublicTournamentEvents(w http.ResponseWriter, r *http.Request) {
	if s.adminEvents == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	events, unsubscribe, err := s.adminEvents.SubscribeAdminChanges(r.Context())
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	defer unsubscribe()

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")

	if !writeAdminEvent(w, "ready", nil) {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(publicTournamentEventsHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case topic, ok := <-events:
			if !ok {
				return
			}
			if topic != AdminEventTopicTournaments {
				continue
			}
			if !writeAdminEvent(w, "changed", adminChangedEventPayload{Topic: AdminEventTopicTournaments}) {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
