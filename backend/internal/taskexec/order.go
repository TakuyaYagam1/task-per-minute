package taskexec

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type Submission struct {
	ID            uuid.UUID
	ParticipantID uuid.UUID
	ReceivedAt    time.Time
}

func OrderSubmissions(submissions []Submission) ([]Submission, error) {
	ordered := append([]Submission(nil), submissions...)
	seen := make(map[uuid.UUID]struct{}, len(ordered))
	for _, submission := range ordered {
		if submission.ID == uuid.Nil || submission.ParticipantID == uuid.Nil ||
			submission.ReceivedAt.IsZero() || submission.ReceivedAt.Location() != time.UTC {
			return nil, fmt.Errorf("%w: invalid submission ordering input", domain.ErrValidation)
		}
		if _, duplicate := seen[submission.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate submission identity", domain.ErrValidation)
		}
		seen[submission.ID] = struct{}{}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if !ordered[i].ReceivedAt.Equal(ordered[j].ReceivedAt) {
			return ordered[i].ReceivedAt.Before(ordered[j].ReceivedAt)
		}
		if comparison := bytes.Compare(ordered[i].ID[:], ordered[j].ID[:]); comparison != 0 {
			return comparison < 0
		}
		return bytes.Compare(ordered[i].ParticipantID[:], ordered[j].ParticipantID[:]) < 0
	})
	return ordered, nil
}

func PrecedesDeadline(receivedAt, deadline time.Time) bool {
	return receivedAt.Before(deadline)
}
