package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"slices"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func validRosterAuthority(authority RosterAuthority, tournamentID uuid.UUID) bool {
	return authority.ProjectionRevisionID != uuid.Nil && authority.ProjectionRevision >= 1 &&
		authority.TournamentPreset.IsValid() && authority.TournamentState.IsValid() &&
		authority.TournamentRevision >= 1 &&
		validRosterView(authority.Roster, tournamentID)
}

func validRosterActionAuthority(action RosterOperationAction, authority RosterAuthority) bool {
	switch action {
	case RosterOperationReplace:
		return (authority.TournamentState == domain.TournamentStateDraft ||
			authority.TournamentState == domain.TournamentStateRegistration) &&
			!authority.Roster.Locked && !authority.Roster.ExecutionStarted
	case RosterOperationPreflight, RosterOperationLock:
		return authority.TournamentState == domain.TournamentStateRegistration &&
			!authority.Roster.Locked && !authority.Roster.ExecutionStarted
	case RosterOperationUnlock:
		return authority.TournamentState == domain.TournamentStateRosterLocked &&
			authority.Roster.Locked && !authority.Roster.ExecutionStarted
	default:
		return false
	}
}

func validRosterMutationResult(result RosterView, authority RosterAuthority) bool {
	return validRosterView(result, authority.Roster.TournamentID) && result.ID == authority.Roster.ID &&
		result.Revision == authority.Roster.Revision+1
}

func validPreflightInputAuthority(input tournamentpreflight.ReportInput, authority RosterAuthority) bool {
	return input.RosterRevision == authority.Roster.Revision && input.PairingRevision >= 1 &&
		input.Structural.TournamentID == authority.Roster.TournamentID &&
		input.Runtime.TournamentID == authority.Roster.TournamentID &&
		input.Structural.Preset == input.Runtime.Preset &&
		input.Structural.ExpectedRosterSize == len(input.Structural.Participants) &&
		input.Runtime.RosterSize == len(input.Structural.Participants)
}

func preflightCheckedInPlayerIDs(input tournamentpreflight.ReportInput) []uuid.UUID {
	playerIDs := make([]uuid.UUID, 0, len(input.Structural.Participants))
	for _, participant := range input.Structural.Participants {
		if participant.Attendance == domain.AttendanceStateCheckedIn {
			playerIDs = append(playerIDs, participant.PlayerID)
		}
	}
	return canonicalRosterIDs(playerIDs)
}

func preflightAuthorizesLock(
	record *RosterOperationRecord,
	command LockRosterCommand,
	authority RosterAuthority,
) bool {
	if !preflightOperationMatchesLock(record, command, authority) {
		return false
	}
	var report tournamentpreflight.ReportRevision
	//nolint:musttag // ResultDocument is a versioned usecase-owned evidence snapshot validated below.
	return json.Unmarshal(record.ResultDocument, &report) == nil && report.Validate() == nil && report.Passed() &&
		report.ID == record.CommandID && report.TournamentID == command.TournamentID
}

func preflightOperationMatchesLock(
	record *RosterOperationRecord,
	command LockRosterCommand,
	authority RosterAuthority,
) bool {
	return record != nil && validRosterOperationRecord(*record) && record.Action == RosterOperationPreflight &&
		record.TournamentID == command.TournamentID && record.CommandID == command.PreflightRevisionID &&
		record.RosterID == authority.Roster.ID &&
		record.SourceProjectionRevisionID == authority.ProjectionRevisionID &&
		record.SourceProjectionRevision == authority.ProjectionRevision &&
		record.SourceTournamentRevision == authority.TournamentRevision &&
		record.SourceTournamentState == authority.TournamentState &&
		record.SourceRosterRevision == authority.Roster.Revision &&
		slices.Equal(record.CheckedInPlayerIDs, canonicalRosterIDs(command.CheckedInPlayerIDs))
}

func rosterRequestDigest(action RosterOperationAction, command any) ([32]byte, error) {
	payload, err := json.Marshal(struct {
		Action  RosterOperationAction `json:"action"`
		Command any                   `json:"command"`
	}{Action: action, Command: canonicalRosterCommand(command)})
	if err != nil {
		return [32]byte{}, domain.ErrInternal
	}
	return sha256.Sum256(payload), nil
}

func canonicalRosterCommand(command any) any {
	switch value := command.(type) {
	case ReplaceRosterCommand:
		value.Participants = append([]RosterParticipantInput(nil), value.Participants...)
		slices.SortFunc(value.Participants, func(first, second RosterParticipantInput) int {
			if first.Seed != second.Seed {
				return first.Seed - second.Seed
			}
			return bytes.Compare(first.PlayerID[:], second.PlayerID[:])
		})
		return value
	case LockRosterCommand:
		value.CheckedInPlayerIDs = canonicalRosterIDs(value.CheckedInPlayerIDs)
		return value
	default:
		return command
	}
}

func canonicalRosterIDs(values []uuid.UUID) []uuid.UUID {
	canonical := append([]uuid.UUID(nil), values...)
	slices.SortFunc(canonical, func(first, second uuid.UUID) int { return bytes.Compare(first[:], second[:]) })
	return canonical
}

func rosterOperationMatches(
	record RosterOperationRecord,
	scope CommandScope,
	action RosterOperationAction,
	expectedProjectionRevision int64,
	digest [32]byte,
) bool {
	return validRosterOperationRecord(record) && record.CommandScope == scope && record.Action == action &&
		record.SourceProjectionRevision == expectedProjectionRevision && record.RequestDigest == digest
}

func validRosterOperationRecord(record RosterOperationRecord) bool {
	if !validRosterOperationHeader(record) {
		return false
	}
	if !validCanonicalRosterIDs(record.CheckedInPlayerIDs) {
		return false
	}
	switch record.Action {
	case RosterOperationPreflight:
		return record.ResultingRosterRevision == record.SourceRosterRevision &&
			record.ResultingTournamentRevision == record.SourceTournamentRevision &&
			record.ResultingTournamentState == record.SourceTournamentState &&
			record.PreflightRevisionID == uuid.Nil
	case RosterOperationReplace:
		return record.ResultingRosterRevision == record.SourceRosterRevision+1 &&
			record.ResultingTournamentRevision == record.SourceTournamentRevision &&
			record.ResultingTournamentState == record.SourceTournamentState &&
			record.PreflightRevisionID == uuid.Nil && len(record.CheckedInPlayerIDs) == 0
	case RosterOperationLock, RosterOperationUnlock:
		return validRosterTransitionOperation(record)
	}
	return false
}

func validCanonicalRosterIDs(values []uuid.UUID) bool {
	if len(values) > domain.TournamentMaxParticipants {
		return false
	}
	for index, value := range values {
		if value == uuid.Nil || index > 0 && bytes.Compare(values[index-1][:], value[:]) >= 0 {
			return false
		}
	}
	return true
}

func validRosterOperationHeader(record RosterOperationRecord) bool {
	return validCommandScope(record.CommandScope) && record.Action.valid() && record.RosterID != uuid.Nil &&
		record.SourceProjectionRevisionID != uuid.Nil && record.SourceProjectionRevision >= 1 &&
		record.SourceTournamentRevision >= 1 && record.SourceTournamentState.IsValid() &&
		record.ResultingTournamentRevision >= record.SourceTournamentRevision &&
		record.ResultingTournamentState.IsValid() && record.SourceRosterRevision >= 1 &&
		record.ResultingRosterRevision >= record.SourceRosterRevision && record.RequestDigest != ([32]byte{}) &&
		domain.IsValidServerTime(record.ExecutedAt) && validJSONObject(record.ResultDocument)
}

func validRosterTransitionOperation(record RosterOperationRecord) bool {
	if record.ResultingRosterRevision != record.SourceRosterRevision+1 ||
		record.ResultingTournamentRevision != record.SourceTournamentRevision+1 ||
		record.ResultingTournamentState != rosterResultingTournamentState(record.Action, RosterAuthority{
			TournamentState: record.SourceTournamentState,
		}) {
		return false
	}
	if record.Action == RosterOperationLock {
		return record.PreflightRevisionID != uuid.Nil &&
			len(record.CheckedInPlayerIDs) >= domain.TournamentMinParticipants
	}
	return record.PreflightRevisionID == uuid.Nil && len(record.CheckedInPlayerIDs) == 0
}

func rosterResultingTournamentRevision(action RosterOperationAction, authority RosterAuthority) int64 {
	if action == RosterOperationLock || action == RosterOperationUnlock {
		return authority.TournamentRevision + 1
	}
	return authority.TournamentRevision
}

func rosterResultingTournamentState(action RosterOperationAction, authority RosterAuthority) domain.TournamentState {
	switch action {
	case RosterOperationLock:
		return domain.TournamentStateRosterLocked
	case RosterOperationUnlock:
		return domain.TournamentStateRegistration
	case RosterOperationReplace, RosterOperationPreflight:
		return authority.TournamentState
	}
	return ""
}

func (a RosterOperationAction) valid() bool {
	switch a {
	case RosterOperationReplace, RosterOperationPreflight, RosterOperationLock, RosterOperationUnlock:
		return true
	}
	return false
}

func cloneRosterView(view RosterView) RosterView {
	cloned := view
	cloned.Participants = append([]RosterParticipantView(nil), view.Participants...)
	cloned.LockedAt = cloneTimeValue(view.LockedAt)
	cloned.ExecutionStartedAt = cloneTimeValue(view.ExecutionStartedAt)
	return cloned
}

func clonePreflightReport(report tournamentpreflight.ReportRevision) tournamentpreflight.ReportRevision {
	cloned := report
	cloned.NormalizedInputs = append([]string(nil), report.NormalizedInputs...)
	cloned.Revisions = append([]tournamentpreflight.SourceRevision(nil), report.Revisions...)
	cloned.Checks = append([]tournamentpreflight.Check(nil), report.Checks...)
	for index := range cloned.Checks {
		cloned.Checks[index].Evidence = append([]string(nil), report.Checks[index].Evidence...)
	}
	return cloned
}
