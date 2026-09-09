package postgres

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

const maxTournamentLifecycleReasonLength = 512

func tournamentAdminLifecycleAuthority(
	row sqlc.LockTournamentLifecycleAuthorityRow,
) (tournamentadmin.LifecycleAuthority, error) {
	view := usecase.TournamentView{
		ID: row.ID, RosterID: row.RosterID, Preset: domain.TournamentPreset(row.Preset),
		State: domain.TournamentState(row.State), Revision: row.TournamentRevision,
		RosterSize: int(row.RosterSize),
	}
	view.PausedFromState = lifecycleTournamentState(row.PausedFromState)
	var ok bool
	view.CreatedAt, ok = lifecycleRequiredTime(row.TournamentCreatedAt.Valid, row.TournamentCreatedAt.Time)
	if !ok {
		return tournamentadmin.LifecycleAuthority{}, domain.ErrInternal
	}
	view.UpdatedAt, ok = lifecycleRequiredTime(row.TournamentUpdatedAt.Valid, row.TournamentUpdatedAt.Time)
	if !ok {
		return tournamentadmin.LifecycleAuthority{}, domain.ErrInternal
	}
	view.StartedAt, ok = lifecycleOptionalTime(row.TournamentStartedAt.Valid, row.TournamentStartedAt.Time)
	if !ok {
		return tournamentadmin.LifecycleAuthority{}, domain.ErrInternal
	}
	view.FinishedAt, ok = lifecycleOptionalTime(row.TournamentFinishedAt.Valid, row.TournamentFinishedAt.Time)
	if !ok || !validLifecycleTournamentView(view, row.ID) || row.ProjectionRevisionID == uuid.Nil ||
		row.ProjectionRevision < 1 {
		return tournamentadmin.LifecycleAuthority{}, domain.ErrInternal
	}
	return tournamentadmin.LifecycleAuthority{
		Tournament: view, ProjectionRevisionID: row.ProjectionRevisionID,
		ProjectionRevision: row.ProjectionRevision,
	}, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func lifecycleRosterReadyForSwiss(
	roster sqlc.Roster,
	participants []sqlc.ListTournamentAdminRosterParticipantsRow,
	reservations []sqlc.ParticipantReservation,
) bool {
	if roster.ID == uuid.Nil || roster.TournamentID == uuid.Nil || !roster.LockedAt.Valid ||
		roster.ExecutionStartedAt.Valid || len(participants) < domain.TournamentMinParticipants ||
		len(participants) > domain.TournamentMaxParticipants || len(reservations) != len(participants) {
		return false
	}
	players := make(map[uuid.UUID]struct{}, len(participants))
	for _, participant := range participants {
		if participant.ID == uuid.Nil || participant.RosterID != roster.ID ||
			participant.TournamentID != roster.TournamentID || participant.PlayerID == uuid.Nil ||
			participant.Attendance != string(domain.AttendanceStateCheckedIn) {
			return false
		}
		if _, exists := players[participant.PlayerID]; exists {
			return false
		}
		players[participant.PlayerID] = struct{}{}
	}
	for _, reservation := range reservations {
		if reservation.PlayerID == uuid.Nil || reservation.ReservationID == uuid.Nil ||
			reservation.TournamentID != roster.TournamentID || reservation.Revision < 1 {
			return false
		}
		if _, exists := players[reservation.PlayerID]; !exists {
			return false
		}
		delete(players, reservation.PlayerID)
	}
	return len(players) == 0
}

func tournamentAdminLifecycleCommand(
	row sqlc.TournamentLifecycleCommand,
) (*tournamentadmin.LifecycleCommandRecord, error) {
	view := usecase.TournamentView{
		ID: row.TournamentID, RosterID: row.RosterID, Preset: domain.TournamentPreset(row.Preset),
		State: domain.TournamentState(row.ResultingTournamentState), Revision: row.ResultingTournamentRevision,
		RosterSize: int(row.RosterSize),
	}
	if tournamentadmin.TournamentAction(row.Action) == tournamentadmin.TournamentActionPause {
		state := domain.TournamentState(row.SourceTournamentState)
		view.PausedFromState = &state
	}
	var ok bool
	view.CreatedAt, ok = lifecycleRequiredTime(row.TournamentCreatedAt.Valid, row.TournamentCreatedAt.Time)
	if !ok {
		return nil, domain.ErrInternal
	}
	view.UpdatedAt, ok = lifecycleRequiredTime(row.TournamentUpdatedAt.Valid, row.TournamentUpdatedAt.Time)
	if !ok {
		return nil, domain.ErrInternal
	}
	view.StartedAt, ok = lifecycleOptionalTime(row.TournamentStartedAt.Valid, row.TournamentStartedAt.Time)
	if !ok {
		return nil, domain.ErrInternal
	}
	view.FinishedAt, ok = lifecycleOptionalTime(row.TournamentFinishedAt.Valid, row.TournamentFinishedAt.Time)
	if !ok {
		return nil, domain.ErrInternal
	}
	executedAt, ok := lifecycleRequiredTime(row.ExecutedAt.Valid, row.ExecutedAt.Time)
	if !ok || !row.CreatedAt.Valid {
		return nil, domain.ErrInternal
	}

	record := &tournamentadmin.LifecycleCommandRecord{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: row.ActorID},
			TournamentID: row.TournamentID, CommandID: row.CommandID,
		},
		Action:                     tournamentadmin.TournamentAction(row.Action),
		SourceProjectionRevisionID: row.SourceProjectionRevisionID,
		SourceProjectionRevision:   row.SourceProjectionRevision,
		SourceTournamentRevision:   row.SourceTournamentRevision,
		SourceTournamentState:      domain.TournamentState(row.SourceTournamentState),
		Result:                     view, Reason: stringValue(row.Reason), ExecutionSnapshot: bytes.Clone(row.ExecutionSnapshot),
		ExecutedAt: executedAt,
	}
	if row.PauseID.Valid {
		record.PauseID = row.PauseID.UUID
	}
	if row.SourcePauseCommandID.Valid {
		record.SourcePauseCommandID = row.SourcePauseCommandID.UUID
	}
	createdAt := row.CreatedAt.Time.UTC()
	if !domain.IsValidServerTime(createdAt) || createdAt.Before(executedAt) || !validLifecycleCommandRecord(*record) {
		return nil, domain.ErrInternal
	}
	return record, nil
}

func tournamentAdminLifecycleCommandParams(
	record tournamentadmin.LifecycleCommandRecord,
) sqlc.CreateTournamentLifecycleCommandParams {
	// The command record validator bounds roster size by TournamentMaxParticipants.
	//nolint:gosec // int -> int32 is safe after that invariant check.
	rosterSize := int32(record.Result.RosterSize)
	return sqlc.CreateTournamentLifecycleCommandParams{
		CommandID: record.CommandID, TournamentID: record.TournamentID, RosterID: record.Result.RosterID,
		ActorID: record.Operator.ActorID, Action: string(record.Action),
		SourceProjectionRevisionID:  record.SourceProjectionRevisionID,
		SourceProjectionRevision:    record.SourceProjectionRevision,
		SourceTournamentRevision:    record.SourceTournamentRevision,
		SourceTournamentState:       string(record.SourceTournamentState),
		ResultingTournamentRevision: record.Result.Revision,
		ResultingTournamentState:    string(record.Result.State), Reason: lifecycleReasonPointer(record.Reason),
		PauseID: nullableUUIDValue(record.PauseID), SourcePauseCommandID: nullableUUIDValue(record.SourcePauseCommandID),
		ExecutionSnapshot: bytes.Clone(record.ExecutionSnapshot), Preset: string(record.Result.Preset),
		RosterSize: rosterSize, TournamentCreatedAt: tstz(record.Result.CreatedAt),
		TournamentUpdatedAt: tstz(record.Result.UpdatedAt), TournamentStartedAt: nullableTSTZ(record.Result.StartedAt),
		TournamentFinishedAt: nullableTSTZ(record.Result.FinishedAt), ExecutedAt: tstz(record.ExecutedAt),
	}
}

func validLifecycleResumeInput(input tournamentadmin.LifecycleResumeInput) bool {
	reason := strings.TrimSpace(input.Reason)
	return input.TournamentID != uuid.Nil && input.RosterID != uuid.Nil &&
		input.ExpectedTournamentRevision >= 1 && input.ExpectedProjectionRevision >= 1 &&
		input.PauseRevisionID != uuid.Nil && reason != "" && reason == input.Reason &&
		len(reason) <= maxTournamentLifecycleReasonLength && validLifecycleJSONObject(input.ExecutionSnapshot) &&
		domain.IsValidServerTime(input.ResumedAt)
}

func validLifecyclePauseCancellationInput(input tournamentadmin.LifecyclePauseCancellationInput) bool {
	reason := strings.TrimSpace(input.Reason)
	return input.TournamentID != uuid.Nil && input.RosterID != uuid.Nil && input.ResultingTournamentRevision >= 2 &&
		input.PauseRevisionID != uuid.Nil && reason != "" && reason == input.Reason &&
		len(reason) <= maxTournamentLifecycleReasonLength && domain.IsValidServerTime(input.CancelledAt)
}

func validLifecycleCommandRecord(record tournamentadmin.LifecycleCommandRecord) bool {
	return validLifecycleCommandHeader(record) && validLifecycleCommandEvidence(record) &&
		validLifecycleCommandTransition(record)
}

func validLifecycleCommandHeader(record tournamentadmin.LifecycleCommandRecord) bool {
	if record.TournamentID == uuid.Nil || record.CommandID == uuid.Nil || record.Operator.ActorID == uuid.Nil ||
		!validLifecycleAction(record.Action) || record.SourceProjectionRevisionID == uuid.Nil ||
		record.SourceProjectionRevision < 1 || record.SourceTournamentRevision < 1 ||
		!record.SourceTournamentState.IsValid() || record.Result.ID != record.TournamentID ||
		record.Result.Revision != record.SourceTournamentRevision+1 ||
		!record.ExecutedAt.Equal(record.Result.UpdatedAt) || !validLifecycleTournamentView(record.Result, record.TournamentID) {
		return false
	}
	reason := strings.TrimSpace(record.Reason)
	if reason != record.Reason || len(reason) > maxTournamentLifecycleReasonLength {
		return false
	}
	return true
}

func validLifecycleCommandEvidence(record tournamentadmin.LifecycleCommandRecord) bool {
	reason := record.Reason
	switch record.Action {
	case tournamentadmin.TournamentActionPause:
		return reason != "" && record.PauseID != uuid.Nil && record.SourcePauseCommandID == uuid.Nil &&
			validLifecycleJSONObject(record.ExecutionSnapshot)
	case tournamentadmin.TournamentActionResume:
		return reason != "" && record.PauseID != uuid.Nil && record.SourcePauseCommandID != uuid.Nil &&
			validLifecycleJSONObject(record.ExecutionSnapshot)
	case tournamentadmin.TournamentActionCancel:
		return reason != "" && noLifecyclePauseEvidence(record)
	case tournamentadmin.TournamentActionOpenRegistration, tournamentadmin.TournamentActionStartSwiss,
		tournamentadmin.TournamentActionStartGolden, tournamentadmin.TournamentActionStartPlayoffs,
		tournamentadmin.TournamentActionComplete:
		return noLifecyclePauseEvidence(record)
	default:
		return false
	}
}

func validLifecycleCommandTransition(record tournamentadmin.LifecycleCommandRecord) bool {
	switch record.Action {
	case tournamentadmin.TournamentActionPause:
		return record.Result.State == domain.TournamentStateTechnicalPause && record.Result.PausedFromState != nil &&
			*record.Result.PausedFromState == record.SourceTournamentState
	case tournamentadmin.TournamentActionResume:
		return record.SourceTournamentState == domain.TournamentStateTechnicalPause &&
			record.Result.State != domain.TournamentStateTechnicalPause && record.Result.PausedFromState == nil
	case tournamentadmin.TournamentActionCancel:
		return record.Result.State == domain.TournamentStateCancelled && record.Result.PausedFromState == nil
	case tournamentadmin.TournamentActionOpenRegistration, tournamentadmin.TournamentActionStartSwiss,
		tournamentadmin.TournamentActionStartGolden, tournamentadmin.TournamentActionStartPlayoffs,
		tournamentadmin.TournamentActionComplete:
		next, ok := lifecycleActionTarget(record.Action)
		return ok && record.Result.State == next && record.Result.PausedFromState == nil
	default:
		return false
	}
}

func validLifecycleTournamentView(view usecase.TournamentView, tournamentID uuid.UUID) bool {
	if view.ID != tournamentID || view.RosterID == uuid.Nil || !view.Preset.IsValid() ||
		view.Revision < 1 || view.RosterSize < 0 || view.RosterSize > domain.TournamentMaxParticipants ||
		!domain.IsValidServerTime(view.CreatedAt) || !domain.IsValidServerTime(view.UpdatedAt) ||
		view.UpdatedAt.Before(view.CreatedAt) ||
		(domain.Tournament{State: view.State, PausedFromState: view.PausedFromState}).Validate() != nil {
		return false
	}
	return validLifecycleEventTime(view.StartedAt, view.CreatedAt, view.UpdatedAt) &&
		validLifecycleEventTime(view.FinishedAt, view.CreatedAt, view.UpdatedAt) &&
		(view.StartedAt == nil || view.FinishedAt == nil || !view.FinishedAt.Before(*view.StartedAt))
}

func validLifecycleEventTime(value *time.Time, createdAt time.Time, updatedAt time.Time) bool {
	return value == nil || domain.IsValidServerTime(*value) &&
		!value.Before(createdAt) && !value.After(updatedAt)
}

func lifecycleRequiredTime(valid bool, value time.Time) (time.Time, bool) {
	if !valid {
		return time.Time{}, false
	}
	result := value.UTC()
	return result, domain.IsValidServerTime(result)
}

func lifecycleOptionalTime(valid bool, value time.Time) (*time.Time, bool) {
	if !valid {
		return nil, true
	}
	result := value.UTC()
	if !domain.IsValidServerTime(result) {
		return nil, false
	}
	return &result, true
}

func lifecycleTournamentState(value *string) *domain.TournamentState {
	if value == nil {
		return nil
	}
	state := domain.TournamentState(*value)
	return &state
}

func lifecycleReasonPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func noLifecyclePauseEvidence(record tournamentadmin.LifecycleCommandRecord) bool {
	return record.PauseID == uuid.Nil && record.SourcePauseCommandID == uuid.Nil && len(record.ExecutionSnapshot) == 0
}

func validLifecycleJSONObject(value json.RawMessage) bool {
	if !json.Valid(value) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil && len(object) > 0
}

func validLifecycleAction(action tournamentadmin.TournamentAction) bool {
	switch action {
	case tournamentadmin.TournamentActionOpenRegistration, tournamentadmin.TournamentActionStartSwiss,
		tournamentadmin.TournamentActionStartGolden, tournamentadmin.TournamentActionStartPlayoffs,
		tournamentadmin.TournamentActionPause, tournamentadmin.TournamentActionResume,
		tournamentadmin.TournamentActionComplete, tournamentadmin.TournamentActionCancel:
		return true
	default:
		return false
	}
}

func lifecycleActionTarget(action tournamentadmin.TournamentAction) (domain.TournamentState, bool) {
	switch action {
	case tournamentadmin.TournamentActionOpenRegistration:
		return domain.TournamentStateRegistration, true
	case tournamentadmin.TournamentActionStartSwiss:
		return domain.TournamentStateSwiss, true
	case tournamentadmin.TournamentActionStartGolden:
		return domain.TournamentStateGolden, true
	case tournamentadmin.TournamentActionStartPlayoffs:
		return domain.TournamentStatePlayoffs, true
	case tournamentadmin.TournamentActionComplete:
		return domain.TournamentStateCompleted, true
	case tournamentadmin.TournamentActionPause, tournamentadmin.TournamentActionResume,
		tournamentadmin.TournamentActionCancel:
		return "", false
	default:
		return "", false
	}
}
