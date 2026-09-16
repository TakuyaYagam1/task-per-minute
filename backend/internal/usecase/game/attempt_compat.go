package game

import (
	"time"

	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
)

var (
	ErrInvalidFailedAttempt      = attemptusecase.ErrInvalidFailedAttempt
	ErrFailedAttemptUnavailable  = attemptusecase.ErrFailedAttemptUnavailable
	ErrFailedAttemptConflict     = attemptusecase.ErrFailedAttemptConflict
	ErrFailedAttemptCommandReuse = attemptusecase.ErrFailedAttemptCommandReuse
)

func AttemptNewUseCase(repository AttemptRepository, clock AttemptClock) *AttemptUseCase {
	return attemptusecase.AttemptNewUseCase(repository, clock)
}

// BuildRecord builds the terminal record used by execution replay.
func BuildRecord(
	command AttemptCommand,
	authority AttemptAuthority,
	terminalizedAt time.Time,
) (AttemptRecord, error) {
	return attemptusecase.BuildRecord(command, authority, terminalizedAt)
}

// Reconcile compares a replay command with a retained terminal record.
func Reconcile(
	record AttemptRecord,
	command AttemptCommand,
) (*AttemptRecord, error) {
	return attemptusecase.Reconcile(record, command)
}

// RecordsEqual reports whether two retained terminal records match.
func RecordsEqual(first, second AttemptRecord) bool {
	return attemptusecase.RecordsEqual(first, second)
}

// CloneRecord clones a terminal record at the execution boundary.
func CloneRecord(record AttemptRecord) AttemptRecord {
	return attemptusecase.CloneRecord(record)
}

// ValidateCommand validates a command before execution replay.
func ValidateCommand(command AttemptCommand) error {
	return attemptusecase.ValidateCommand(command)
}

// ValidateAuthority validates the authority loaded for execution replay.
func ValidateAuthority(authority AttemptAuthority) error {
	return attemptusecase.ValidateAuthority(authority)
}
