package roster

import (
	"time"

	"github.com/google/uuid"
)

// The exported validation and record helpers are the narrow compatibility
// surface used by the aggregate admin facade while callers migrate to this
// capability package.
func ValidRosterQuery(query RosterQuery) bool {
	return validRosterQuery(query)
}

func ValidReplaceRosterCommand(command ReplaceRosterCommand) bool {
	return validReplaceRosterCommand(command)
}

func ValidPreflightCommand(command PreflightCommand) bool {
	return validPreflightCommand(command)
}

func ValidLockRosterCommand(command LockRosterCommand) bool {
	return validLockRosterCommand(command)
}

func ValidUnlockRosterCommand(command UnlockRosterCommand) bool {
	return validUnlockRosterCommand(command)
}

func ValidRosterView(view RosterView, tournamentID uuid.UUID) bool {
	return validRosterView(view, tournamentID)
}

func RosterRequestDigest(action RosterOperationAction, command any) ([32]byte, error) {
	return rosterRequestDigest(action, command)
}

func CloneRosterView(view RosterView) RosterView {
	return cloneRosterView(view)
}

func ValidRosterOperationRecord(record RosterOperationRecord) bool {
	return validRosterOperationRecord(record)
}

func NewRosterOperationRecord(
	scope CommandScope,
	action RosterOperationAction,
	authority RosterAuthority,
	resultingRosterRevision int64,
	digest [32]byte,
	preflightRevisionID uuid.UUID,
	checkedInPlayerIDs []uuid.UUID,
	result any,
	executedAt time.Time,
) (RosterOperationRecord, error) {
	return newRosterOperationRecord(
		scope, action, authority, resultingRosterRevision, digest,
		rosterOperationEvidence{
			preflightRevisionID: preflightRevisionID,
			checkedInPlayerIDs:  checkedInPlayerIDs,
		}, result, executedAt,
	)
}
