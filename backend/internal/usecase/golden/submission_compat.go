package golden

import (
	"time"

	"github.com/google/uuid"

	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"
)

type GoldenSubmissionScope = goldensubmission.GoldenSubmissionScope
type GoldenSubmissionLedgerExpectation = goldensubmission.GoldenSubmissionLedgerExpectation
type GoldenSubmissionDisposition = goldensubmission.GoldenSubmissionDisposition
type GoldenSubmissionVerification = goldensubmission.GoldenSubmissionVerification
type GoldenSubmissionCommand = goldensubmission.GoldenSubmissionCommand
type GoldenSubmissionRecord = goldensubmission.GoldenSubmissionRecord
type GoldenSubmissionReceipt = goldensubmission.GoldenSubmissionReceipt
type GoldenSubmissionLedger = goldensubmission.GoldenSubmissionLedger
type GoldenSubmissionAuthority = goldensubmission.GoldenSubmissionAuthority
type GoldenSubmissionCommit = goldensubmission.GoldenSubmissionCommit
type GoldenSubmissionUseCase = goldensubmission.GoldenSubmissionUseCase
type SubmissionRepository = goldensubmission.SubmissionRepository

var (
	ErrInvalidGoldenSubmission           = goldensubmission.ErrInvalidGoldenSubmission
	ErrGoldenSubmissionAuthorityConflict = goldensubmission.ErrGoldenSubmissionAuthorityConflict
	ErrGoldenSubmissionConflict          = goldensubmission.ErrGoldenSubmissionConflict
	ErrGoldenSubmissionCommandReuse      = goldensubmission.ErrGoldenSubmissionCommandReuse
	ErrGoldenSubmissionIncorrect         = goldensubmission.ErrGoldenSubmissionIncorrect
	ErrGoldenSubmissionClosed            = goldensubmission.ErrGoldenSubmissionClosed
	ErrGoldenSubmissionDuplicateLimit    = goldensubmission.ErrGoldenSubmissionDuplicateLimit
)

const (
	GoldenSubmissionAccepted  = goldensubmission.GoldenSubmissionAccepted
	GoldenSubmissionDuplicate = goldensubmission.GoldenSubmissionDuplicate
)

func NewGoldenSubmissionLedger(scope GoldenSubmissionScope, revisionID uuid.UUID) (GoldenSubmissionLedger, error) {
	return goldensubmission.NewGoldenSubmissionLedger(scope, revisionID)
}

func NewGoldenSubmissionUseCase(repository SubmissionRepository) *GoldenSubmissionUseCase {
	return goldensubmission.NewGoldenSubmissionUseCase(repository)
}

func ApplyGoldenSubmissionCommit(
	commit GoldenSubmissionCommit,
	live GoldenSubmissionAuthority,
	submissionID uint64,
	committedAt time.Time,
) (GoldenSubmissionLedger, error) {
	return goldensubmission.ApplyGoldenSubmissionCommit(commit, live, submissionID, committedAt)
}

func ReserveLedgerIdentities(reserved map[uuid.UUID]struct{}, ledger GoldenSubmissionLedger) {
	goldensubmission.ReserveLedgerIdentities(reserved, ledger)
}

func ScopeMatchesExecution(scope GoldenSubmissionScope, execution GoldenWaveExecution) bool {
	return goldensubmission.ScopeMatchesExecution(scope, execution)
}

func ValidRecord(submission GoldenSubmissionRecord, scope GoldenSubmissionScope, expectedID uint64) bool {
	return goldensubmission.ValidRecord(submission, scope, expectedID)
}
