package game

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidSubmission = errors.New("invalid game submission")

func ValidateSubmission(record Submission) error {
	if !record.Scope.IsValid() || record.CommandID == uuid.Nil || record.ParticipantID == uuid.Nil ||
		record.Sequence < 1 || !domain.IsValidServerTime(record.CommittedAt) || record.SnapshotID == uuid.Nil ||
		record.TaskID == uuid.Nil || record.ContentDigest == [sha256.Size]byte{} {
		return fmt.Errorf("%w: invalid record identity, timestamp, or content binding", ErrInvalidSubmission)
	}
	return nil
}

func OrderSubmissions(submissions []Submission) ([]Submission, error) {
	ordered := append([]Submission(nil), submissions...)
	seenSequences := make(map[int64]struct{}, len(ordered))
	seenCommands := make(map[uuid.UUID]struct{}, len(ordered))
	var scope SubmissionScope
	for index, submission := range ordered {
		if err := ValidateSubmission(submission); err != nil {
			return nil, err
		}
		if index == 0 {
			scope = submission.Scope
		} else if submission.Scope != scope {
			return nil, fmt.Errorf("%w: ordering crosses Game scope", ErrInvalidSubmission)
		}
		if _, duplicate := seenSequences[submission.Sequence]; duplicate {
			return nil, fmt.Errorf("%w: duplicate Game sequence", ErrInvalidSubmission)
		}
		if _, duplicate := seenCommands[submission.CommandID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate command identity", ErrInvalidSubmission)
		}
		seenSequences[submission.Sequence] = struct{}{}
		seenCommands[submission.CommandID] = struct{}{}
	}
	sort.Slice(ordered, func(first, second int) bool {
		if !ordered[first].CommittedAt.Equal(ordered[second].CommittedAt) {
			return ordered[first].CommittedAt.Before(ordered[second].CommittedAt)
		}
		return ordered[first].Sequence < ordered[second].Sequence
	})
	return ordered, nil
}
