package observability

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const runtimeWorkerHeartbeatNameMaxBytes = 64

// RuntimeWorkerHeartbeat is a shared, payload-free liveness signal for one
// local runtime worker. Instance identity is used only by the shared store to
// aggregate replicas and is never emitted as telemetry.
type RuntimeWorkerHeartbeat struct {
	InstanceID uuid.UUID
	Worker     string
	ObservedAt time.Time
}

func (heartbeat RuntimeWorkerHeartbeat) Validate() error {
	if heartbeat.InstanceID == uuid.Nil || !validRuntimeWorkerHeartbeatName(heartbeat.Worker) ||
		heartbeat.ObservedAt.IsZero() || heartbeat.ObservedAt.Location() != time.UTC {
		return fmt.Errorf("invalid runtime worker heartbeat")
	}
	return nil
}

// RuntimeWorkerHeartbeatStatus is a sanitized aggregate. It intentionally
// excludes individual instance identities and timestamps.
type RuntimeWorkerHeartbeatStatus struct {
	Worker         string
	FreshInstances int
}

func (status RuntimeWorkerHeartbeatStatus) Healthy() bool {
	return validRuntimeWorkerHeartbeatName(status.Worker) && status.FreshInstances > 0
}

func (status RuntimeWorkerHeartbeatStatus) Revision() string {
	if !validRuntimeWorkerHeartbeatName(status.Worker) || status.FreshInstances < 0 {
		return ""
	}
	return fmt.Sprintf("%s:replicas:%d", status.Worker, status.FreshInstances)
}

// RuntimeWorkerHeartbeatReporter records one local worker liveness signal in
// the shared aggregation store.
type RuntimeWorkerHeartbeatReporter interface {
	ReportRuntimeWorkerHeartbeat(ctx context.Context, heartbeat RuntimeWorkerHeartbeat) error
}

// RuntimeWorkerHeartbeatReader returns the number of replicas that reported
// success after freshAfter for one bounded worker name.
type RuntimeWorkerHeartbeatReader interface {
	RuntimeWorkerHeartbeatStatus(
		ctx context.Context,
		worker string,
		freshAfter time.Time,
	) (RuntimeWorkerHeartbeatStatus, error)
}

func validRuntimeWorkerHeartbeatName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > runtimeWorkerHeartbeatNameMaxBytes || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}
