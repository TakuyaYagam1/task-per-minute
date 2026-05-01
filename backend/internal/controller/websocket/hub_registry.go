package websocket

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	wskit "github.com/wahrwelt-kit/go-wskit"
)

var ErrHubNotFound = errors.New("websocket: hub not found")

type HubRegistry struct {
	hubs            sync.Map // map[uuid.UUID]*hubEntry
	options         []wskit.HubOption
	registerTimeout time.Duration
}

type hubEntry struct {
	hub    *wskit.Hub
	cancel context.CancelFunc

	mu   sync.Mutex
	acks map[wskit.Subscriber]chan struct{}
}

func NewHubRegistry(options ...wskit.HubOption) *HubRegistry {
	return &HubRegistry{
		options:         options,
		registerTimeout: time.Second,
	}
}

func (r *HubRegistry) Create(ctx context.Context, duelID uuid.UUID) *wskit.Hub {
	entry := &hubEntry{acks: make(map[wskit.Subscriber]chan struct{})}
	hubCtx, cancel := context.WithCancel(ctx)
	entry.cancel = cancel
	opts := append([]wskit.HubOption{}, r.options...)
	opts = append(opts, wskit.WithOnConnect(entry.confirmConnect))
	entry.hub = wskit.NewHub(opts...)

	actual, loaded := r.hubs.LoadOrStore(duelID, entry)
	if loaded {
		cancel()
		return actual.(*hubEntry).hub
	}

	go entry.hub.Run(hubCtx)
	return entry.hub
}

func (r *HubRegistry) Get(duelID uuid.UUID) (*wskit.Hub, bool) {
	raw, ok := r.hubs.Load(duelID)
	if !ok {
		return nil, false
	}
	return raw.(*hubEntry).hub, true
}

func (r *HubRegistry) Register(ctx context.Context, duelID uuid.UUID, sub wskit.Subscriber) error {
	raw, ok := r.hubs.Load(duelID)
	if !ok {
		return ErrHubNotFound
	}

	entry := raw.(*hubEntry)
	ack := entry.prepareAck(sub)
	entry.hub.Register(sub)

	waitCtx := ctx
	cancel := func() {}
	if _, ok := waitCtx.Deadline(); !ok && r.registerTimeout > 0 {
		waitCtx, cancel = context.WithTimeout(waitCtx, r.registerTimeout)
	}
	defer cancel()

	select {
	case <-ack:
		return nil
	case <-waitCtx.Done():
		entry.dropAck(sub)
		return waitCtx.Err()
	}
}

func (r *HubRegistry) Unregister(duelID uuid.UUID, sub wskit.Subscriber) {
	raw, ok := r.hubs.Load(duelID)
	if !ok {
		return
	}
	raw.(*hubEntry).hub.Unregister(sub)
}

func (r *HubRegistry) BroadcastJSON(_ context.Context, duelID uuid.UUID, typ string, payload any) error {
	raw, ok := r.hubs.Load(duelID)
	if !ok {
		return ErrHubNotFound
	}
	data, err := marshalEvent(typ, payload)
	if err != nil {
		return err
	}
	raw.(*hubEntry).hub.Broadcast(data)
	return nil
}

func (r *HubRegistry) Close(duelID uuid.UUID) bool {
	raw, ok := r.hubs.LoadAndDelete(duelID)
	if !ok {
		return false
	}
	raw.(*hubEntry).cancel()
	return true
}

func (r *HubRegistry) CloseAll() {
	if r == nil {
		return
	}
	r.hubs.Range(func(key, raw any) bool {
		r.hubs.Delete(key)
		raw.(*hubEntry).cancel()
		return true
	})
}

func (e *hubEntry) prepareAck(sub wskit.Subscriber) <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	ack := make(chan struct{})
	e.acks[sub] = ack
	return ack
}

func (e *hubEntry) confirmConnect(sub wskit.Subscriber) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ack, ok := e.acks[sub]
	if !ok {
		return
	}
	close(ack)
	delete(e.acks, sub)
}

func (e *hubEntry) dropAck(sub wskit.Subscriber) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.acks, sub)
}
