package arena

import (
	"context"
	"errors"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

const (
	arenaLifecycleEvent = "arena.lifecycle.transition"
	arenaWaveStartEvent = "arena.command.wave_start"

	arenaLifecycleStage = "tournament_lifecycle"
	arenaWaveStartStage = "wave_start"

	arenaEventPhaseValidation          = "validation"
	arenaEventPhaseRepositoryLookup    = "repository_lookup"
	arenaEventPhaseRepositoryMutation  = "repository_mutation"
	arenaEventPhaseAuthorityLookup     = "authority_lookup"
	arenaEventPhaseAuthorityValidation = "authority_validation"
	arenaEventPhaseBuild               = "build"
	arenaEventPhaseClock               = "clock"
	arenaEventPhaseCommit              = "commit"
	arenaEventPhaseResultValidation    = "result_validation"
)

type arenaEventMeasurement struct {
	observer  observability.ArenaEventObserver
	clock     Clock
	startedAt time.Time
}

type arenaCoreEventInput struct {
	event         string
	outcome       string
	correlationID string
	commandID     string
	tournamentID  string
	entityKind    string
	entityID      string
	stage         string
	transition    string
	duration      time.Duration
	reasonCode    string
	revision      int64
}

func newArenaEventMeasurement(
	clock Clock,
	observer observability.ArenaEventObserver,
) arenaEventMeasurement {
	measurement := arenaEventMeasurement{clock: clock, observer: observer}
	if clock != nil && observer != nil {
		measurement.startedAt = clock.Now()
	}
	return measurement
}

func (m arenaEventMeasurement) duration() time.Duration {
	if m.observer == nil || m.clock == nil || m.startedAt.IsZero() {
		return 0
	}
	duration := m.clock.Now().Sub(m.startedAt)
	if duration < 0 {
		return 0
	}
	return duration
}

func emitTournamentLifecycleEvent(
	ctx context.Context,
	measurement arenaEventMeasurement,
	command TournamentLifecycleCommand,
	currentState domain.ArenaTournamentState,
	record *TournamentRecord,
	changed bool,
	err error,
	phase string,
) {
	if measurement.observer == nil {
		return
	}
	outcome, reasonCode := tournamentLifecycleEventResult(changed, err, phase)
	revision := nonnegativeArenaRevision(command.ExpectedRevision)
	if record != nil {
		revision = nonnegativeArenaRevision(record.Revision)
	}
	tournamentID := command.TournamentID.String()
	_ = observability.EmitArenaEvent(ctx, measurement.observer, observability.ArenaEventInput{
		Event:         arenaLifecycleEvent,
		Outcome:       outcome,
		CorrelationID: tournamentID,
		TournamentID:  tournamentID,
		EntityKind:    "tournament",
		EntityID:      tournamentID,
		Stage:         arenaLifecycleStage,
		Transition:    tournamentLifecycleEventTransition(currentState, command.NextState),
		Duration:      measurement.duration(),
		ReasonCode:    reasonCode,
		Revision:      revision,
	})
}

func tournamentLifecycleEventResult(changed bool, err error, phase string) (string, string) {
	if err == nil {
		if changed {
			return observability.ArenaOutcomeSuccess, "transitioned"
		}
		return observability.ArenaOutcomeSuccess, "idempotent_replay"
	}
	switch {
	case errors.Is(err, domain.ErrValidation):
		if phase == arenaEventPhaseValidation {
			return observability.ArenaOutcomeRejected, "invalid_command"
		}
		return observability.ArenaOutcomeFailure, "invalid_clock"
	case errors.Is(err, ErrTournamentNotFound):
		return observability.ArenaOutcomeRejected, "tournament_not_found"
	case errors.Is(err, domain.ErrConflict):
		return observability.ArenaOutcomeRejected, "conflict"
	case errors.Is(err, ErrTournamentGuardedTransition):
		return observability.ArenaOutcomeRejected, "guarded_transition"
	case errors.Is(err, domain.ErrArenaTournamentTransition):
		return observability.ArenaOutcomeRejected, "transition_not_allowed"
	case errors.Is(err, domain.ErrInvalidArenaTournamentState):
		return observability.ArenaOutcomeRejected, "invalid_state"
	case errors.Is(err, domain.ErrInternal):
		return observability.ArenaOutcomeFailure, "invalid_result"
	}
	switch phase {
	case arenaEventPhaseRepositoryLookup:
		return observability.ArenaOutcomeFailure, "repository_lookup_failed"
	case arenaEventPhaseRepositoryMutation:
		return observability.ArenaOutcomeFailure, "repository_mutation_failed"
	default:
		return observability.ArenaOutcomeFailure, "operation_failed"
	}
}

func tournamentLifecycleEventTransition(
	current domain.ArenaTournamentState,
	next domain.ArenaTournamentState,
) string {
	if !next.IsValid() {
		return "invalid"
	}
	if !current.IsValid() {
		return "unknown_to_" + next.String()
	}
	return current.String() + "_to_" + next.String()
}

func emitWaveStartEvent(
	ctx context.Context,
	measurement arenaEventMeasurement,
	command StartWaveCommand,
	record *WaveStartRecord,
	changed bool,
	retried bool,
	err error,
	phase string,
	revision int64,
) {
	if measurement.observer == nil {
		return
	}
	outcome, reasonCode := waveStartEventResult(changed, retried, err, phase)
	if record != nil {
		revision = record.ExpectedAuthorityRevision
	}
	commandID := command.CommandID.String()
	tournamentID := command.Scope.TournamentID.String()
	_ = observability.EmitArenaEvent(ctx, measurement.observer, observability.ArenaEventInput{
		Event:         arenaWaveStartEvent,
		Outcome:       outcome,
		CorrelationID: commandID,
		CommandID:     commandID,
		TournamentID:  tournamentID,
		EntityKind:    "wave",
		EntityID:      command.Scope.WaveID.String(),
		Stage:         arenaWaveStartStage,
		Transition:    "ready_to_active",
		Duration:      measurement.duration(),
		ReasonCode:    reasonCode,
		Revision:      nonnegativeArenaRevision(revision),
	})
}

func waveStartEventResult(changed bool, retried bool, err error, phase string) (string, string) {
	if err == nil {
		return successfulWaveStartEventResult(changed, retried)
	}
	switch {
	case errors.Is(err, domain.ErrValidation):
		if phase == arenaEventPhaseClock {
			return observability.ArenaOutcomeFailure, "invalid_clock"
		}
		return observability.ArenaOutcomeRejected, "invalid_command"
	case errors.Is(err, ErrWaveStartAuthorityConflict):
		return observability.ArenaOutcomeRejected, "authority_conflict"
	case errors.Is(err, ErrWaveStartConflict), errors.Is(err, domain.ErrConflict):
		return observability.ArenaOutcomeRejected, "conflict"
	case errors.Is(err, domain.ErrInternal):
		return observability.ArenaOutcomeFailure, "invalid_result"
	case errors.Is(err, ErrInvalidWaveStart):
		switch phase {
		case arenaEventPhaseValidation:
			return observability.ArenaOutcomeRejected, "invalid_command"
		case arenaEventPhaseAuthorityValidation:
			return observability.ArenaOutcomeFailure, "invalid_authority"
		case arenaEventPhaseBuild:
			return observability.ArenaOutcomeFailure, "start_build_failed"
		default:
			return observability.ArenaOutcomeFailure, "operation_failed"
		}
	}
	switch phase {
	case arenaEventPhaseAuthorityLookup:
		return observability.ArenaOutcomeFailure, "authority_lookup_failed"
	case arenaEventPhaseCommit:
		return observability.ArenaOutcomeFailure, "repository_commit_failed"
	case arenaEventPhaseBuild:
		return observability.ArenaOutcomeFailure, "start_build_failed"
	default:
		return observability.ArenaOutcomeFailure, "operation_failed"
	}
}

func successfulWaveStartEventResult(changed bool, retried bool) (string, string) {
	if retried {
		return observability.ArenaOutcomeRetry, "conflict_retried"
	}
	if changed {
		return observability.ArenaOutcomeSuccess, "started"
	}
	return observability.ArenaOutcomeSuccess, "idempotent_replay"
}

func nonnegativeArenaRevision(revision int64) int64 {
	if revision < 0 {
		return 0
	}
	return revision
}

func emitArenaCoreEvent(
	ctx context.Context,
	observer observability.ArenaEventObserver,
	input arenaCoreEventInput,
) {
	if observer == nil {
		return
	}
	if input.duration < 0 {
		input.duration = 0
	}
	_ = observability.EmitArenaEvent(ctx, observer, observability.ArenaEventInput{
		Event:         input.event,
		Outcome:       input.outcome,
		CorrelationID: input.correlationID,
		CommandID:     input.commandID,
		TournamentID:  input.tournamentID,
		EntityKind:    input.entityKind,
		EntityID:      input.entityID,
		Stage:         input.stage,
		Transition:    input.transition,
		Duration:      input.duration,
		ReasonCode:    input.reasonCode,
		Revision:      nonnegativeArenaRevision(input.revision),
	})
}

func arenaCoreEventResult(changed bool, err error, conflicts ...error) (string, string) {
	if err == nil {
		if changed {
			return observability.ArenaOutcomeSuccess, "committed"
		}
		return observability.ArenaOutcomeSuccess, "idempotent_replay"
	}
	if errors.Is(err, domain.ErrValidation) {
		return observability.ArenaOutcomeRejected, "invalid_command"
	}
	for _, conflict := range conflicts {
		if errors.Is(err, conflict) {
			return observability.ArenaOutcomeFailure, "conflict_exhausted"
		}
	}
	if errors.Is(err, domain.ErrConflict) {
		return observability.ArenaOutcomeFailure, "conflict_exhausted"
	}
	return observability.ArenaOutcomeFailure, "operation_failed"
}

func firstArenaEventObserver(
	observers ...observability.ArenaEventObserver,
) observability.ArenaEventObserver {
	return observability.FirstArenaEventObserver(observers...)
}
