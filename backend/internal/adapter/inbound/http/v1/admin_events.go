package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const adminEventsHeartbeat = 25 * time.Second

type adminChangedEventPayload struct {
	Topic string `json:"topic"`
}

func (s *Server) StreamAdminEvents(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
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

	heartbeat := time.NewTicker(adminEventsHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case topic, ok := <-events:
			if !ok {
				return
			}
			if !isAdminEventTopic(topic) {
				continue
			}
			if !writeAdminEvent(w, "changed", adminChangedEventPayload{Topic: topic}) {
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

func isAdminEventTopic(topic string) bool {
	switch topic {
	case AdminEventTopicPlayers, AdminEventTopicTasks, AdminEventTopicTournaments:
		return true
	default:
		return false
	}
}

func writeAdminEvent(w http.ResponseWriter, event string, payload any) bool {
	data := []byte("{}")
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		data = encoded
	}
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err == nil
}
