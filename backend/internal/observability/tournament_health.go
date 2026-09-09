package observability

import (
	"context"
	"errors"
)

// TournamentHealthState is the current operating state of one tournament dependency.
type TournamentHealthState string

const (
	TournamentHealthStateHealthy  TournamentHealthState = "healthy"
	TournamentHealthStateDegraded TournamentHealthState = "degraded"
	TournamentHealthStateFailed   TournamentHealthState = "failed"
)

// TournamentReadinessState is the current traffic-readiness state of one tournament dependency.
type TournamentReadinessState string

const (
	TournamentReadinessStateReady    TournamentReadinessState = "ready"
	TournamentReadinessStateNotReady TournamentReadinessState = "not_ready"
	TournamentReadinessStateStale    TournamentReadinessState = "stale"
)

var ErrInvalidTournamentHealth = errors.New("invalid tournament health snapshot")

// TournamentDependencyStatus reports health and readiness independently.
type TournamentDependencyStatus struct {
	Health    TournamentHealthState
	Readiness TournamentReadinessState
}

// TournamentHealthSnapshot names every tournament runtime dependency exposed by health.
type TournamentHealthSnapshot struct {
	Authority    TournamentDependencyStatus
	Submission   TournamentDependencyStatus
	TaskDelivery TournamentDependencyStatus
	Outbox       TournamentDependencyStatus
	Realtime     TournamentDependencyStatus
	Projection   TournamentDependencyStatus
	Clock        TournamentDependencyStatus
	Recovery     TournamentDependencyStatus
}

// TournamentHealthSource supplies the latest authoritative dependency snapshot.
type TournamentHealthSource interface {
	TournamentHealth(ctx context.Context) TournamentHealthSnapshot
}

// TournamentHealthSourceFunc adapts a function to TournamentHealthSource.
type TournamentHealthSourceFunc func(ctx context.Context) TournamentHealthSnapshot

func (source TournamentHealthSourceFunc) TournamentHealth(ctx context.Context) TournamentHealthSnapshot {
	if source == nil {
		return FailedTournamentHealthSnapshot()
	}
	return source(ctx)
}

// HealthyTournamentHealthSnapshot returns a fully healthy and ready baseline.
func HealthyTournamentHealthSnapshot() TournamentHealthSnapshot {
	healthy := TournamentDependencyStatus{
		Health:    TournamentHealthStateHealthy,
		Readiness: TournamentReadinessStateReady,
	}
	return TournamentHealthSnapshot{
		Authority:    healthy,
		Submission:   healthy,
		TaskDelivery: healthy,
		Outbox:       healthy,
		Realtime:     healthy,
		Projection:   healthy,
		Clock:        healthy,
		Recovery:     healthy,
	}
}

// FailedTournamentHealthSnapshot returns a fail-closed dependency baseline.
func FailedTournamentHealthSnapshot() TournamentHealthSnapshot {
	failed := TournamentDependencyStatus{
		Health:    TournamentHealthStateFailed,
		Readiness: TournamentReadinessStateNotReady,
	}
	return TournamentHealthSnapshot{
		Authority:    failed,
		Submission:   failed,
		TaskDelivery: failed,
		Outbox:       failed,
		Realtime:     failed,
		Projection:   failed,
		Clock:        failed,
		Recovery:     failed,
	}
}

// Validate rejects unknown dependency states.
func (snapshot TournamentHealthSnapshot) Validate() error {
	for _, status := range snapshot.dependencies() {
		if !status.Health.valid() || !status.Readiness.valid() {
			return ErrInvalidTournamentHealth
		}
	}
	return nil
}

// Healthy reports whether every named dependency is healthy.
func (snapshot TournamentHealthSnapshot) Healthy() bool {
	if snapshot.Validate() != nil {
		return false
	}
	for _, status := range snapshot.dependencies() {
		if status.Health != TournamentHealthStateHealthy {
			return false
		}
	}
	return true
}

// Ready reports whether every named dependency can serve authoritative traffic.
func (snapshot TournamentHealthSnapshot) Ready() bool {
	if snapshot.Validate() != nil {
		return false
	}
	for _, status := range snapshot.dependencies() {
		if status.Readiness != TournamentReadinessStateReady {
			return false
		}
	}
	return true
}

func (snapshot TournamentHealthSnapshot) dependencies() [8]TournamentDependencyStatus {
	return [8]TournamentDependencyStatus{
		snapshot.Authority,
		snapshot.Submission,
		snapshot.TaskDelivery,
		snapshot.Outbox,
		snapshot.Realtime,
		snapshot.Projection,
		snapshot.Clock,
		snapshot.Recovery,
	}
}

func (state TournamentHealthState) valid() bool {
	switch state {
	case TournamentHealthStateHealthy, TournamentHealthStateDegraded, TournamentHealthStateFailed:
		return true
	default:
		return false
	}
}

func (state TournamentReadinessState) valid() bool {
	switch state {
	case TournamentReadinessStateReady, TournamentReadinessStateNotReady, TournamentReadinessStateStale:
		return true
	default:
		return false
	}
}
