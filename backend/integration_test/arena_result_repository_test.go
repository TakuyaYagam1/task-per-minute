//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestArenaConcurrentSettlement(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaResultAuditMigrationFixture(t, ctx)
	repository := postgres.NewArenaResultPostgres(postgres.NewTxManager(sharedPool))
	winnerID := fixture.draft.participantIDs[0]
	submittedAt := fixture.lockedAt.Add(time.Second)
	payloadDigest := [32]byte{}
	copy(payloadDigest[:], bytes.Repeat([]byte{31}, 32))
	submission, changed, err := repository.RecordSubmission(ctx, postgres.ArenaSubmissionInput{
		ID: uuid.New(),
		Scope: postgres.ArenaResultScope{
			TournamentID: fixture.draft.tournamentID, RosterID: fixture.draft.rosterID,
			SeriesID: fixture.draft.seriesID, AttemptID: fixture.attemptID,
		},
		AssignmentID: fixture.assignmentID, ParticipantID: winnerID, ServerSequence: 1,
		IdempotencyKey: uuid.New(), Status: "accepted", PayloadDigest: payloadDigest,
		SubmittedAt: submittedAt, ReceivedAt: submittedAt, CreatedAt: submittedAt,
		ExpectedAttemptRevision: 1, ExpectedAttemptState: domain.ArenaGameStateActive,
	})
	require.NoError(t, err)
	require.True(t, changed)

	var seriesRevision int64
	err = sharedPool.QueryRow(ctx, `SELECT revision FROM arena_series WHERE id = $1`, fixture.draft.seriesID).
		Scan(&seriesRevision)
	require.NoError(t, err)

	type settleResult struct {
		record  *postgres.ArenaResultCommitRecord
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan settleResult, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			settledAt := fixture.lockedAt.Add(2 * time.Second)
			digest := [32]byte{}
			copy(digest[:], bytes.Repeat([]byte{32}, 32))
			record, won, settleErr := repository.Settle(ctx, postgres.ArenaResultSettlementInput{
				IDs: postgres.ArenaResultSettlementIDs{
					CommitID: uuid.New(), ResultEventID: uuid.New(), ResultEventIdempotencyKey: uuid.New(),
					GameResultRevisionID: uuid.New(), SeriesScoreRevisionID: uuid.New(),
					SeriesResultRevisionID: uuid.New(), AuditEventID: uuid.New(), OutboxEventID: uuid.New(),
					OutboxIdempotencyKey: uuid.New(), ProjectionEvidenceID: uuid.New(), CommitIdempotencyKey: uuid.New(),
				},
				Scope: postgres.ArenaResultScope{
					TournamentID: fixture.draft.tournamentID, RosterID: fixture.draft.rosterID,
					SeriesID: fixture.draft.seriesID, AttemptID: fixture.attemptID,
				},
				SubmissionEventID: submission.ID, ResultServerSequence: 1,
				GameState: domain.ArenaGameStateCompleted, GameReason: domain.ArenaGameResultReasonSolved,
				GameWinnerID: &winnerID, Score: domain.ArenaSeriesScore{FirstParticipantWins: 1},
				NextSeriesState: domain.ArenaSeriesStateCompleted, SeriesResultReason: "score_complete",
				SeriesWinnerID: &winnerID, ActorKind: "server",
				ProjectionArtifactKinds: []domain.ArenaArtifactKind{
					domain.ArenaArtifactKindStandings, domain.ArenaArtifactKindBracket,
				},
				ProjectionPayloadDigest: digest, SettledAt: settledAt,
				ExpectedAttemptRevision: 1, ExpectedAttemptState: domain.ArenaGameStateActive,
				ExpectedSeriesRevision: seriesRevision, ExpectedSeriesState: domain.ArenaSeriesStateLocked,
			})
			results <- settleResult{record: record, changed: won, err: settleErr}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	changedCount := 0
	commitIDs := make(map[uuid.UUID]struct{})
	for result := range results {
		require.NoError(t, result.err)
		require.NotNil(t, result.record)
		if result.changed {
			changedCount++
		}
		commitIDs[result.record.Commit.ID] = struct{}{}
	}
	require.Equal(t, 1, changedCount)
	require.Len(t, commitIDs, 1)

	scope := postgres.ArenaResultScope{
		TournamentID: fixture.draft.tournamentID, RosterID: fixture.draft.rosterID,
		SeriesID: fixture.draft.seriesID, AttemptID: fixture.attemptID,
	}
	current, err := repository.Current(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, "completed", current.Event.ResultState)
	require.Equal(t, "completed", current.SeriesRevision.ResultState)
	require.EqualValues(t, 2, current.ScoreRevision.RevisionNumber)
	require.Equal(t, "arena.result.committed", current.Outbox.Topic)

	history, err := repository.History(ctx, scope)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.EqualValues(t, 2, history[0].ScoreRevisionNumber)

	var evidenceCounts [4]int
	err = sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM arena_result_commits WHERE attempt_id = $1),
			(SELECT COUNT(*) FROM arena_audit_events WHERE result_event_id = $2),
			(SELECT COUNT(*) FROM arena_outbox_events WHERE result_event_id = $2),
			(SELECT COUNT(*) FROM arena_result_projection_evidence WHERE result_event_id = $2)`,
		fixture.attemptID,
		current.Event.ID,
	).Scan(&evidenceCounts[0], &evidenceCounts[1], &evidenceCounts[2], &evidenceCounts[3])
	require.NoError(t, err)
	require.Equal(t, [4]int{1, 1, 1, 1}, evidenceCounts)

	var casualRows int
	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM duels
		WHERE winner_id IN (
			SELECT player_id FROM arena_participants WHERE roster_id = $1
		)`, fixture.draft.rosterID).Scan(&casualRows)
	require.NoError(t, err)
	require.Zero(t, casualRows)
}
