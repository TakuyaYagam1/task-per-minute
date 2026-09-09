package game

import "time"

// BuildFailedAttemptRecord builds the terminal record used by execution replay.
func BuildRecord(
	command AttemptCommand,
	authority AttemptAuthority,
	terminalizedAt time.Time,
) (AttemptRecord, error) {
	return buildFailedAttemptRecord(command, authority, terminalizedAt)
}

// ReconcileFailedAttempt compares a replay command with a retained terminal record.
func Reconcile(
	record AttemptRecord,
	command AttemptCommand,
) (*AttemptRecord, error) {
	return reconcileFailedAttempt(record, command)
}

// FailedAttemptRecordsEqual reports whether two retained terminal records match.
func RecordsEqual(first, second AttemptRecord) bool {
	return failedAttemptRecordsEqual(first, second)
}

// CloneFailedAttemptRecord clones a terminal record at the execution boundary.
func CloneRecord(record AttemptRecord) AttemptRecord {
	return cloneFailedAttemptRecord(record)
}
