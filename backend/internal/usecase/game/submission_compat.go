package game

import (
	"crypto/sha256"

	gamesubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/submission"
)

var (
	ErrSubmissionNotOpen      = gamesubmission.ErrSubmissionNotOpen
	ErrSubmissionDisconnected = gamesubmission.ErrSubmissionDisconnected
	ErrSubmissionConflict     = gamesubmission.ErrSubmissionConflict
	ErrSubmissionCommandReuse = gamesubmission.ErrSubmissionCommandReuse
)

type SubmissionAuthority = gamesubmission.SubmissionAuthority
type SubmissionSnapshot = gamesubmission.SubmissionSnapshot
type SubmissionCommand = gamesubmission.SubmissionCommand
type SubmissionCommit = gamesubmission.SubmissionCommit
type SubmissionRepository = gamesubmission.SubmissionRepository
type SubmissionUseCase = gamesubmission.SubmissionUseCase

func NewSubmissionUseCase(repository SubmissionRepository) *SubmissionUseCase {
	return gamesubmission.NewSubmissionUseCase(repository)
}

func SubmissionIntentDigest(submittedFlag string) ([sha256.Size]byte, error) {
	return gamesubmission.SubmissionIntentDigest(submittedFlag)
}
