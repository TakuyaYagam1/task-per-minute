package v1

import (
	"fmt"
	"net/http"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/errmap"
)

const adminPlayerEventsHeartbeat = 25 * time.Second

func (s *Server) StreamAdminPlayerEvents(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if s.adminPlayerEvents == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	events, unsubscribe, err := s.adminPlayerEvents.SubscribeAdminPlayerChanges(r.Context())
	if err != nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}
	defer unsubscribe()

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")

	if !writeAdminPlayerEvent(w, "ready") {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(adminPlayerEventsHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case _, ok := <-events:
			if !ok || !writeAdminPlayerEvent(w, "players_changed") {
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

func writeAdminPlayerEvent(w http.ResponseWriter, event string) bool {
	_, err := fmt.Fprintf(w, "event: %s\ndata: {}\n\n", event)
	return err == nil
}
