package presence

import (
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

// ValidatePausedPresenceCommand validates the identity and expected revisions
// carried by one paused-presence change command.
func ValidatePausedPresenceCommand(command PausedPresenceCommand) error {
	if !model.ValidPauseGraphScope(command.Scope) || command.PauseID == uuid.Nil || command.CommandID == uuid.Nil ||
		command.ParticipantID == uuid.Nil || command.ExpectedGraphRevision < 1 || command.ExpectedPauseRevision < 1 ||
		command.ExpectedPresenceEpoch < 1 || command.ExpectedPresenceRevision < 1 ||
		(command.NextState != pausedomain.PresenceStateConnected && command.NextState != pausedomain.PresenceStateDisconnected) {
		return pausedPresenceError("invalid command identity, state, or expectation")
	}
	return nil
}

// ValidatePausedPresenceAuthority validates the immutable pause evidence and
// the selected live Presence row before a change is applied.
func ValidatePausedPresenceAuthority(authority PausedPresenceAuthority) error {
	if model.ValidateNormalPauseRecord(authority.Pause) != nil || authority.Pause.State != model.PauseStateActive ||
		!model.AllowsNormalPause(authority.Pause.Reason) || authority.TerminalActionRevision < 0 ||
		model.ValidatePausePresence(authority.Presence) != nil {
		return ErrPausedPresenceSuppression
	}
	if err := validatePausedPresenceImmutable(authority); err != nil {
		return err
	}
	snapshot, found := pausePresenceSnapshot(authority.Pause.Graph.Presence, authority.Presence.ParticipantID)
	if !found || !PausePresenceTracksSnapshot(snapshot, authority.Presence, authority.Pause.PausedAt) {
		return ErrPausedPresenceConflict
	}
	return nil
}

// PausePresenceTracksSnapshot verifies the selected live row against the row
// captured in the active pause graph.
func PausePresenceTracksSnapshot(snapshot, current pausedomain.PausePresence, pausedAt time.Time) bool {
	if !SamePausePresenceIdentity(snapshot, current) || current.PresenceEpoch < snapshot.PresenceEpoch ||
		current.Revision < snapshot.Revision {
		return false
	}
	epochDelta := current.PresenceEpoch - snapshot.PresenceEpoch
	revisionDelta := current.Revision - snapshot.Revision
	if epochDelta != revisionDelta {
		return false
	}
	if epochDelta == 0 {
		return reflect.DeepEqual(snapshot, current)
	}
	return !current.UpdatedAt.Before(pausedAt)
}

func validatePausedPresenceImmutable(authority PausedPresenceAuthority) error {
	if !reflect.DeepEqual(authority.Reconnect, authority.Pause.Graph.Reconnect) ||
		!reflect.DeepEqual(authority.Counters, authority.Pause.Graph.Counters) ||
		!reflect.DeepEqual(authority.FrozenDeadlines, authority.Pause.Graph.FrozenDeadlines) ||
		authority.TerminalActionRevision != authority.Pause.Graph.TerminalActionRevision {
		return pausedPresenceError("mutable pause evidence")
	}
	for _, interval := range authority.Reconnect {
		if err := model.ValidatePauseReconnect(interval); err != nil {
			return pausedPresenceError("Reconnect evidence: %v", err)
		}
	}
	for _, counter := range authority.Counters {
		if !model.ValidPauseReconnectCounter(counter) {
			return pausedPresenceError("invalid reconnect counter")
		}
	}
	for _, frozen := range authority.FrozenDeadlines {
		if err := model.ValidateFrozenDeadline(frozen, true); err != nil {
			return pausedPresenceError("frozen deadline: %v", err)
		}
	}
	return nil
}

// MatchPausedPresenceCommand verifies that a command addresses the locked
// pause and the expected selected Presence row.
func MatchPausedPresenceCommand(authority PausedPresenceAuthority, command PausedPresenceCommand) error {
	pause := authority.Pause
	if pause.Scope != command.Scope || pause.PauseID != command.PauseID || pause.Graph.Revision != command.ExpectedGraphRevision ||
		pause.Revision != command.ExpectedPauseRevision || pause.Scope.Authority != command.Scope.Authority ||
		authority.Presence.ParticipantID != command.ParticipantID || authority.Presence.PresenceEpoch != command.ExpectedPresenceEpoch ||
		authority.Presence.Revision != command.ExpectedPresenceRevision {
		return ErrPausedPresenceConflict
	}
	found := false
	for _, snapshot := range pause.Graph.Presence {
		if snapshot.ParticipantID == command.ParticipantID {
			found = SamePausePresenceIdentity(snapshot, authority.Presence)
			break
		}
	}
	if !found {
		return ErrPausedPresenceConflict
	}
	if authority.Presence.State == command.NextState {
		return ErrPausedPresenceState
	}
	return nil
}

// BuildPausedPresenceRecord applies one state transition to the selected
// Presence row while retaining every other pause evidence collection.
func BuildPausedPresenceRecord(authority PausedPresenceAuthority, command PausedPresenceCommand, changedAt time.Time) (PausedPresenceRecord, error) {
	if changedAt.Before(authority.Pause.PausedAt) || changedAt.Before(authority.Presence.UpdatedAt) {
		return PausedPresenceRecord{}, pausedPresenceError("change time precedes durable evidence")
	}
	if authority.Presence.PresenceEpoch == math.MaxInt64 || authority.Presence.Revision == math.MaxInt64 {
		return PausedPresenceRecord{}, ErrPausedPresenceOverflow
	}
	next := ClonePausedPresenceAuthority(authority)
	next.Presence.PresenceEpoch++
	next.Presence.Revision++
	next.Presence.State = command.NextState
	next.Presence.UpdatedAt = changedAt
	switch command.NextState {
	case pausedomain.PresenceStateConnected:
		next.Presence.ConnectedAt = changedAt
		next.Presence.DisconnectedAt = nil
	case pausedomain.PresenceStateDisconnected:
		next.Presence.DisconnectedAt = model.CloneTimePointer(&changedAt)
	default:
		return PausedPresenceRecord{}, pausedPresenceError("unknown next state")
	}
	if err := model.ValidatePausePresence(next.Presence); err != nil {
		return PausedPresenceRecord{}, err
	}
	if !PausedPresenceImmutableEqual(authority, next) {
		return PausedPresenceRecord{}, domain.ErrInternal
	}
	record := PausedPresenceRecord{Command: command, Authority: next, ChangedAt: changedAt}
	if err := ValidatePausedPresenceRecord(record); err != nil {
		return PausedPresenceRecord{}, err
	}
	return ClonePausedPresenceRecord(record), nil
}

// ValidatePausedPresenceRecord validates a committed idempotent result.
func ValidatePausedPresenceRecord(record PausedPresenceRecord) error {
	if ValidatePausedPresenceCommand(record.Command) != nil || !model.PauseValidServerTime(record.ChangedAt) ||
		ValidatePausedPresenceAuthority(record.Authority) != nil || record.Authority.Presence.State != record.Command.NextState ||
		record.Authority.Pause.Scope != record.Command.Scope || record.Authority.Pause.PauseID != record.Command.PauseID ||
		record.Authority.Pause.Graph.Revision != record.Command.ExpectedGraphRevision || record.Authority.Pause.Revision != record.Command.ExpectedPauseRevision ||
		record.Authority.Presence.ParticipantID != record.Command.ParticipantID ||
		record.Authority.Presence.PresenceEpoch != record.Command.ExpectedPresenceEpoch+1 ||
		record.Authority.Presence.Revision != record.Command.ExpectedPresenceRevision+1 ||
		!record.Authority.Presence.UpdatedAt.Equal(record.ChangedAt) ||
		!pausedPresenceEventTimeMatches(record.Authority.Presence, record.Command.NextState, record.ChangedAt) {
		return pausedPresenceError("invalid committed record")
	}
	return nil
}

func pausedPresenceEventTimeMatches(presence pausedomain.PausePresence, state pausedomain.PresenceState, changedAt time.Time) bool {
	switch state {
	case pausedomain.PresenceStateConnected:
		return presence.DisconnectedAt == nil && presence.ConnectedAt.Equal(changedAt)
	case pausedomain.PresenceStateDisconnected:
		return presence.DisconnectedAt != nil && presence.DisconnectedAt.Equal(changedAt)
	default:
		return false
	}
}

func pausePresenceSnapshot(values []pausedomain.PausePresence, participantID uuid.UUID) (pausedomain.PausePresence, bool) {
	for _, value := range values {
		if value.ParticipantID == participantID {
			return value, true
		}
	}
	return pausedomain.PausePresence{}, false
}

// SamePausePresenceIdentity compares the durable identity fields of Presence
// rows while ignoring mutable state and revision evidence.
func SamePausePresenceIdentity(first, second pausedomain.PausePresence) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID && first.RosterID == second.RosterID &&
		first.SeriesID == second.SeriesID && first.ParticipantID == second.ParticipantID
}

// ReconcilePausedPresence validates and clones a previously committed result.
func ReconcilePausedPresence(record PausedPresenceRecord, command PausedPresenceCommand) (*PausedPresenceRecord, error) {
	if ValidatePausedPresenceRecord(record) != nil || record.Command != command {
		return nil, ErrPausedPresenceCommandReuse
	}
	clone := ClonePausedPresenceRecord(record)
	return &clone, nil
}

// PausedPresenceExpectationFrom builds the CAS expectation for a selected
// Presence row.
func PausedPresenceExpectationFrom(authority PausedPresenceAuthority, command PausedPresenceCommand) PausedPresenceExpectation {
	presence := authority.Presence
	return PausedPresenceExpectation{
		PauseID: command.PauseID, GraphRevision: command.ExpectedGraphRevision,
		PauseRevision: command.ExpectedPauseRevision, Authority: command.Scope.Authority,
		Presence: PausePresenceRevision{
			ID: presence.ID, TournamentID: presence.TournamentID, RosterID: presence.RosterID,
			SeriesID: presence.SeriesID, ParticipantID: presence.ParticipantID,
			PresenceEpoch: command.ExpectedPresenceEpoch, Revision: command.ExpectedPresenceRevision,
		},
	}
}

// PausedPresenceImmutableEqual verifies that a state transition changed no
// pause evidence beyond the selected Presence row.
func PausedPresenceImmutableEqual(first, second PausedPresenceAuthority) bool {
	return reflect.DeepEqual(first.Pause, second.Pause) && reflect.DeepEqual(first.Reconnect, second.Reconnect) &&
		reflect.DeepEqual(first.Counters, second.Counters) && reflect.DeepEqual(first.FrozenDeadlines, second.FrozenDeadlines) &&
		first.TerminalActionRevision == second.TerminalActionRevision
}

// PausedPresenceRecordsEqual compares committed records including their full
// authority snapshot.
func PausedPresenceRecordsEqual(first, second PausedPresenceRecord) bool {
	return first.Command == second.Command && first.ChangedAt.Equal(second.ChangedAt) && reflect.DeepEqual(first.Authority, second.Authority)
}

// ClonePausedPresenceAuthority deep-copies all mutable descendants.
func ClonePausedPresenceAuthority(value PausedPresenceAuthority) PausedPresenceAuthority {
	clone := value
	clone.Pause = model.CloneNormalPauseRecord(value.Pause)
	clone.Presence = value.Presence
	clone.Presence.DisconnectedAt = model.CloneTimePointer(value.Presence.DisconnectedAt)
	clone.Reconnect = append([]pausedomain.PauseReconnectInterval(nil), value.Reconnect...)
	for index := range clone.Reconnect {
		clone.Reconnect[index].ClosedAt = model.CloneTimePointer(value.Reconnect[index].ClosedAt)
	}
	clone.Counters = append([]pausedomain.PauseReconnectCounter(nil), value.Counters...)
	clone.FrozenDeadlines = model.ClonePauseFrozenDeadlineSlice(value.FrozenDeadlines)
	return clone
}

// ClonePausedPresenceRecord deep-copies a committed record.
func ClonePausedPresenceRecord(value PausedPresenceRecord) PausedPresenceRecord {
	clone := value
	clone.Authority = ClonePausedPresenceAuthority(value.Authority)
	return clone
}

func pausedPresenceError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPausedPresence, fmt.Sprintf(format, arguments...))
}
