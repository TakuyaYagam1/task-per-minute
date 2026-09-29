package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	playerNotificationsHeartbeat   = 25 * time.Second
	playerNotificationsMaxLifetime = 5 * time.Minute
	playerNotificationsWriteWindow = 50 * time.Second
)

func (s *Server) ListPlayerNotifications(w http.ResponseWriter, r *http.Request) {
	player, ok := middleware.GetPlayerFromCtx(r.Context())
	if !ok || player.SessionToken == nil {
		errmap.HandleError(w, r, domain.ErrInvalidSession)
		return
	}
	if s.playerNotifications == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	notifications, err := s.playerNotifications.List(r.Context(), player.ID)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	items := make([]api.PlayerNotification, 0, len(notifications))
	for _, item := range notifications {
		items = append(items, api.PlayerNotification{
			Id:             item.ID,
			Type:           api.PlayerNotificationType(item.Type),
			TournamentId:   item.TournamentID,
			TournamentName: item.TournamentName,
			CreatedAt:      item.CreatedAt,
			ExpiresAt:      item.ExpiresAt,
		})
	}
	response.WriteJSON(w, http.StatusOK, api.PlayerNotificationsResponse{Notifications: items})
}

func (s *Server) StreamPlayerNotifications(w http.ResponseWriter, r *http.Request) {
	player, ok := middleware.GetPlayerFromCtx(r.Context())
	if !ok || player.SessionToken == nil {
		errmap.HandleError(w, r, domain.ErrInvalidSession)
		return
	}
	if s.playerNotifications == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	events, unsubscribe, err := s.playerNotifications.Subscribe(r.Context(), player.ID)
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

	refreshPlayerNotificationWriteDeadline(w)
	if !writePlayerNotificationEvent(w, "ready") {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(playerNotificationsHeartbeat)
	defer heartbeat.Stop()
	lifetime := time.NewTimer(playerNotificationsMaxLifetime)
	defer lifetime.Stop()
	for {
		select {
		case _, open := <-events:
			if !open {
				return
			}
			refreshPlayerNotificationWriteDeadline(w)
			if !writePlayerNotificationEvent(w, "changed") {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			refreshPlayerNotificationWriteDeadline(w)
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-lifetime.C:
			return
		}
	}
}

func refreshPlayerNotificationWriteDeadline(w http.ResponseWriter) {
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(playerNotificationsWriteWindow))
}

func writePlayerNotificationEvent(w http.ResponseWriter, event string) bool {
	data, err := json.Marshal(struct{}{})
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err == nil
}
