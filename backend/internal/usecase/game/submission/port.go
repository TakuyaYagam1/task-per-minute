package submission

import (
	"context"

	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

type SubmissionRepository interface {
	LoadSubmissionAuthority(
		ctx context.Context,
		scope gamedomain.SubmissionScope,
	) (SubmissionAuthority, error)
	CommitSubmission(
		ctx context.Context,
		commit SubmissionCommit,
	) (*gamedomain.Submission, bool, error)
}
