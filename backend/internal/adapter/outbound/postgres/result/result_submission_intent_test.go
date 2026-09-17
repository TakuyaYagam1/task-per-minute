package result

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestSubmissionMatchesInputRequiresExactIntentDigest(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	payloadDigest := sha256.Sum256([]byte("immutable-content"))
	intentDigest := sha256.Sum256([]byte("participant-submission-intent:v1\x00accepted-flag"))
	input := SubmissionInput{
		ID: uuid.New(),
		Scope: ResultScope{
			TournamentID: uuid.New(), RosterID: uuid.New(), SeriesID: uuid.New(), AttemptID: uuid.New(),
		},
		AssignmentID: uuid.New(), ParticipantID: uuid.New(),
		IdempotencyKey: uuid.New(), Status: submissionStatusAccepted,
		PayloadDigest: payloadDigest, IntentDigest: intentDigest,
		SubmittedAt: now, ReceivedAt: now, CreatedAt: now,
		ExpectedAttemptRevision: 1, ExpectedAttemptState: domain.GameStateActive,
	}
	row := sqlc.SubmissionEvent{
		ID:           input.ID,
		TournamentID: input.Scope.TournamentID, RosterID: input.Scope.RosterID,
		SeriesID: input.Scope.SeriesID, AttemptID: input.Scope.AttemptID,
		AssignmentID: input.AssignmentID, ParticipantID: input.ParticipantID,
		ServerSequence: 1, IdempotencyKey: input.IdempotencyKey,
		Status: input.Status, PayloadDigest: input.PayloadDigest[:], IntentDigest: input.IntentDigest[:],
		SubmittedAt: tstz(now), ReceivedAt: tstz(now), CreatedAt: tstz(now),
	}

	require.True(t, submissionMatchesInput(row, input))

	row.IntentDigest = append([]byte(nil), row.IntentDigest...)
	row.IntentDigest[0] ^= 0xff
	require.False(t, submissionMatchesInput(row, input))
}
