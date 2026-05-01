package websocket_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	wscontroller "github.com/TakuyaYagam1/task-per-minute/internal/controller/websocket"
)

func TestHubRegistry_RegisterBroadcastClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registry := wscontroller.NewHubRegistry()
	duelID := uuid.New()
	registry.Create(ctx, duelID)

	sub := &recordingSubscriber{messages: make(chan []byte, 1)}
	require.NoError(t, registry.Register(t.Context(), duelID, sub))
	require.NoError(t, registry.BroadcastJSON(t.Context(), duelID, wscontroller.EventMatchFound, map[string]string{"ok": "yes"}))

	var event wscontroller.Event
	require.NoError(t, json.Unmarshal(receiveMessage(t, sub.messages), &event))
	require.Equal(t, wscontroller.EventMatchFound, event.Type)

	require.True(t, registry.Close(duelID))
	_, ok := registry.Get(duelID)
	require.False(t, ok)
	require.False(t, registry.Close(duelID))
}

type recordingSubscriber struct {
	messages chan []byte
	closed   bool
}

func (s *recordingSubscriber) Send(data []byte) bool {
	select {
	case s.messages <- data:
		return true
	default:
		return false
	}
}

func (s *recordingSubscriber) Close() {
	s.closed = true
}

func receiveMessage(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()

	select {
	case msg := <-ch:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for hub broadcast")
		return nil
	}
}
