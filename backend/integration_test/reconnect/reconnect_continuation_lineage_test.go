//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testReconnectContinuationDeadlineAndLineage(t *testing.T) {
	ctx := context.Background()

	for _, testCase := range []struct {
		name     string
		openedAt func(time.Time) time.Time
		deadline func(time.Time, time.Duration) time.Time
	}{
		{
			name:     "rejects shifted deadline one microsecond early",
			openedAt: func(suspendedAt time.Time) time.Time { return suspendedAt.Add(30 * time.Second) },
			deadline: func(openedAt time.Time, remaining time.Duration) time.Time {
				return openedAt.Add(remaining).Add(-time.Microsecond)
			},
		},
		{
			name:     "rejects shifted deadline one microsecond late",
			openedAt: func(suspendedAt time.Time) time.Time { return suspendedAt.Add(30 * time.Second) },
			deadline: func(openedAt time.Time, remaining time.Duration) time.Time {
				return openedAt.Add(remaining).Add(time.Microsecond)
			},
		},
		{
			name:     "rejects continuation opened at suspension boundary",
			openedAt: func(suspendedAt time.Time) time.Time { return suspendedAt },
			deadline: func(openedAt time.Time, remaining time.Duration) time.Time {
				return openedAt.Add(remaining)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetMigrationTables(ctx, t)
			t.Cleanup(func() { resetMigrationTables(ctx, t) })

			fixture, sourceID, sourceDeadline, suspendedAt := createCancelledReconnectRoot(ctx, t, 1)
			openedAt := testCase.openedAt(suspendedAt)
			err := insertReconnectContinuation(ctx, fixture, reconnectContinuationInput{
				id:                 uuid.New(),
				continuedFromID:    &sourceID,
				suspendedByPauseID: nil,
				participantID:      fixture.draft.participantIDs[0],
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         testCase.deadline(openedAt, sourceDeadline.Sub(suspendedAt)),
			})
			requireReconnectCheckViolation(
				t,
				err,
				"reconnect continuation does not match suspended predecessor",
			)
		})
	}

	t.Run("rejects missing predecessor", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture, _, sourceDeadline, suspendedAt := createCancelledReconnectRoot(ctx, t, 1)
		openedAt := suspendedAt.Add(30 * time.Second)
		missingID := uuid.New()
		err := insertReconnectContinuation(ctx, fixture, reconnectContinuationInput{
			id:                 uuid.New(),
			continuedFromID:    &missingID,
			suspendedByPauseID: nil,
			participantID:      fixture.draft.participantIDs[0],
			presenceEpoch:      2,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           openedAt,
			deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
		})
		requireReconnectPostgresError(
			t,
			err,
			"23503",
			"reconnect_intervals_continued_from_fk",
			"",
		)
	})

	for _, testCase := range []struct {
		name   string
		mutate func(*reconnectContinuationInput)
	}{
		{
			name: "rejects continuation number without predecessor",
			mutate: func(input *reconnectContinuationInput) {
				input.continuedFromID = nil
			},
		},
		{
			name: "rejects predecessor on a root segment",
			mutate: func(input *reconnectContinuationInput) {
				input.continuationNumber = 0
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetMigrationTables(ctx, t)
			t.Cleanup(func() { resetMigrationTables(ctx, t) })

			fixture, sourceID, sourceDeadline, suspendedAt := createCancelledReconnectRoot(ctx, t, 1)
			openedAt := suspendedAt.Add(30 * time.Second)
			input := reconnectContinuationInput{
				id:                 uuid.New(),
				continuedFromID:    &sourceID,
				suspendedByPauseID: nil,
				participantID:      fixture.draft.participantIDs[0],
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			}
			testCase.mutate(&input)

			err := insertReconnectContinuation(ctx, fixture, input)
			requireReconnectPostgresError(
				t,
				err,
				"23514",
				"reconnect_intervals_lineage_check",
				"",
			)
		})
	}
}
