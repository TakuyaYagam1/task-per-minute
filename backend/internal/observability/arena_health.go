package observability

import (
	"context"
	"errors"
)

// ArenaHealthState is the current operating state of one Arena dependency.
type ArenaHealthState string

const (
	ArenaHealthStateHealthy  ArenaHealthState = "healthy"
	ArenaHealthStateDegraded ArenaHealthState = "degraded"
	ArenaHealthStateFailed   ArenaHealthState = "failed"
)

// ArenaReadinessState is the current traffic-readiness state of one Arena dependency.
type ArenaReadinessState string

const (
	ArenaReadinessStateReady    ArenaReadinessState = "ready"
	ArenaReadinessStateNotReady ArenaReadinessState = "not_ready"
	ArenaReadinessStateStale    ArenaReadinessState = "stale"
)

var ErrInvalidArenaHealth = errors.New("invalid Arena health snapshot")

// ArenaDependencyStatus reports health and readiness independently.
type ArenaDependencyStatus struct {
	Health    ArenaHealthState
	Readiness ArenaReadinessState
}

// ArenaHealthSnapshot names every Arena runtime dependency exposed by health.
type ArenaHealthSnapshot struct {
	Authority    ArenaDependencyStatus
	Submission   ArenaDependencyStatus
	TaskDelivery ArenaDependencyStatus
	Outbox       ArenaDependencyStatus
	Realtime     ArenaDependencyStatus
	Clock        ArenaDependencyStatus
	Recovery     ArenaDependencyStatus
}

// ArenaHealthSource supplies the latest authoritative dependency snapshot.
type ArenaHealthSource interface {
	ArenaHealth(ctx context.Context) ArenaHealthSnapshot
}

// ArenaHealthSourceFunc adapts a function to ArenaHealthSource.
type ArenaHealthSourceFunc func(ctx context.Context) ArenaHealthSnapshot

func (source ArenaHealthSourceFunc) ArenaHealth(ctx context.Context) ArenaHealthSnapshot {
	if source == nil {
		return FailedArenaHealthSnapshot()
	}
	return source(ctx)
}

// HealthyArenaHealthSnapshot returns a fully healthy and ready baseline.
func HealthyArenaHealthSnapshot() ArenaHealthSnapshot {
	healthy := ArenaDependencyStatus{
		Health:    ArenaHealthStateHealthy,
		Readiness: ArenaReadinessStateReady,
	}
	return ArenaHealthSnapshot{
		Authority:    healthy,
		Submission:   healthy,
		TaskDelivery: healthy,
		Outbox:       healthy,
		Realtime:     healthy,
		Clock:        healthy,
		Recovery:     healthy,
	}
}

// FailedArenaHealthSnapshot returns a fail-closed dependency baseline.
func FailedArenaHealthSnapshot() ArenaHealthSnapshot {
	failed := ArenaDependencyStatus{
		Health:    ArenaHealthStateFailed,
		Readiness: ArenaReadinessStateNotReady,
	}
	return ArenaHealthSnapshot{
		Authority:    failed,
		Submission:   failed,
		TaskDelivery: failed,
		Outbox:       failed,
		Realtime:     failed,
		Clock:        failed,
		Recovery:     failed,
	}
}

// Validate rejects unknown dependency states.
func (snapshot ArenaHealthSnapshot) Validate() error {
	for _, status := range snapshot.dependencies() {
		if !status.Health.valid() || !status.Readiness.valid() {
			return ErrInvalidArenaHealth
		}
	}
	return nil
}

// Healthy reports whether every named dependency is healthy.
func (snapshot ArenaHealthSnapshot) Healthy() bool {
	if snapshot.Validate() != nil {
		return false
	}
	for _, status := range snapshot.dependencies() {
		if status.Health != ArenaHealthStateHealthy {
			return false
		}
	}
	return true
}

// Ready reports whether every named dependency can serve authoritative traffic.
func (snapshot ArenaHealthSnapshot) Ready() bool {
	if snapshot.Validate() != nil {
		return false
	}
	for _, status := range snapshot.dependencies() {
		if status.Readiness != ArenaReadinessStateReady {
			return false
		}
	}
	return true
}

func (snapshot ArenaHealthSnapshot) dependencies() [7]ArenaDependencyStatus {
	return [7]ArenaDependencyStatus{
		snapshot.Authority,
		snapshot.Submission,
		snapshot.TaskDelivery,
		snapshot.Outbox,
		snapshot.Realtime,
		snapshot.Clock,
		snapshot.Recovery,
	}
}

func (state ArenaHealthState) valid() bool {
	switch state {
	case ArenaHealthStateHealthy, ArenaHealthStateDegraded, ArenaHealthStateFailed:
		return true
	default:
		return false
	}
}

func (state ArenaReadinessState) valid() bool {
	switch state {
	case ArenaReadinessStateReady, ArenaReadinessStateNotReady, ArenaReadinessStateStale:
		return true
	default:
		return false
	}
}
