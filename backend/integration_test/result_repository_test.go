//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestResultAuditMigrationFixtureUsesWaveGenesisScoreNode(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	var (
		currentScoreRevisionID uuid.UUID
		projectionNodeID       uuid.UUID
		authorityKind          string
		waveID                 uuid.UUID
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT series.current_score_revision_id,
			projection_node.id,
			authority.source_kind,
			wave_series.wave_id
		FROM series
		INNER JOIN result_projection_nodes AS projection_node
			ON projection_node.id = series.current_score_revision_id
			AND projection_node.entity_id = series.id
			AND projection_node.artifact_kind = 'series_score'
			AND projection_node.revision_number = 1
		INNER JOIN result_projection_node_authorities AS authority
			ON authority.id = projection_node.authority_id
		INNER JOIN wave_series
			ON wave_series.series_id = series.id
			AND wave_series.tournament_id = series.tournament_id
			AND wave_series.roster_id = series.roster_id
		WHERE series.id = $1`, fixture.draft.seriesID).
		Scan(&currentScoreRevisionID, &projectionNodeID, &authorityKind, &waveID)
	require.NoError(t, err)
	require.Equal(t, currentScoreRevisionID, projectionNodeID)
	require.Equal(t, "wave_initialization", authorityKind)
	require.NotEqual(t, uuid.Nil, waveID)
}

func TestConcurrentResultSettlement(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	repository := resultauthority.NewResultPostgres(postgres.NewTxManager(sharedPool))
	_, sourceProjectionRevision := currentPublishedProjection(
		ctx,
		t,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
	)
	winnerID := fixture.draft.participantIDs[0]
	submittedAt := fixture.lockedAt.Add(time.Second)
	payloadDigest := [32]byte{}
	copy(payloadDigest[:], bytes.Repeat([]byte{31}, 32))
	intentDigest := [32]byte{}
	copy(intentDigest[:], bytes.Repeat([]byte{30}, 32))
	submissionInput := resultrepo.SubmissionInput{
		ID: uuid.New(),
		Scope: resultrepo.ResultScope{
			TournamentID: fixture.draft.tournamentID, RosterID: fixture.draft.rosterID,
			SeriesID: fixture.draft.seriesID, AttemptID: fixture.attemptID,
		},
		AssignmentID: fixture.assignmentID, ParticipantID: winnerID,
		IdempotencyKey: uuid.New(), Status: "accepted", PayloadDigest: payloadDigest, IntentDigest: intentDigest,
		SubmittedAt: submittedAt, ReceivedAt: submittedAt, CreatedAt: submittedAt,
		ExpectedAttemptRevision: 1, ExpectedAttemptState: domain.GameStateActive,
	}
	submission, changed, err := repository.RecordSubmission(ctx, submissionInput)
	require.NoError(t, err)
	require.True(t, changed)
	require.Positive(t, submission.ServerSequence)
	require.Equal(t, intentDigest, submission.IntentDigest)

	replayedSubmission, replayed, err := repository.RecordSubmission(ctx, submissionInput)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, submission, replayedSubmission)

	conflictingIntent := submissionInput
	conflictingIntent.IntentDigest[0] ^= 0xff
	_, changed, err = repository.RecordSubmission(ctx, conflictingIntent)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, changed)

	var persistedIntent []byte
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT intent_digest
		FROM submission_events
		WHERE id = $1`, submission.ID).Scan(&persistedIntent))
	require.Equal(t, intentDigest[:], persistedIntent)

	var seriesRevision int64
	err = sharedPool.QueryRow(ctx, `SELECT revision FROM series WHERE id = $1`, fixture.draft.seriesID).
		Scan(&seriesRevision)
	require.NoError(t, err)

	type settleResult struct {
		record  *resultrepo.ResultCommitRecord
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
			record, won, settleErr := repository.Settle(ctx, resultrepo.ResultSettlementInput{
				IDs: resultrepo.ResultSettlementIDs{
					CommitID: uuid.New(), ResultEventID: uuid.New(), ResultEventIdempotencyKey: uuid.New(),
					GameResultRevisionID: uuid.New(), SeriesScoreRevisionID: uuid.New(),
					SeriesResultRevisionID: uuid.New(), AuditEventID: uuid.New(), OutboxEventID: uuid.New(),
					OutboxIdempotencyKey: uuid.New(), ProjectionEvidenceID: uuid.New(), CommitIdempotencyKey: uuid.New(),
				},
				Scope: resultrepo.ResultScope{
					TournamentID: fixture.draft.tournamentID, RosterID: fixture.draft.rosterID,
					SeriesID: fixture.draft.seriesID, AttemptID: fixture.attemptID,
				},
				SubmissionEventID: submission.ID,
				GameState:         domain.GameStateCompleted, GameReason: domain.GameResultReasonSolved,
				GameWinnerID: &winnerID, Score: domain.SeriesScore{FirstParticipantWins: 1},
				NextSeriesState: domain.SeriesStateCompleted, SeriesResultReason: "score_complete",
				SeriesWinnerID: &winnerID, ActorKind: "server",
				ProjectionArtifactKinds: []domain.ArtifactKind{
					domain.ArtifactKindStandings, domain.ArtifactKindBracket,
				},
				ProjectionPayloadDigest: digest, SettledAt: settledAt,
				ExpectedAttemptRevision: 1, ExpectedAttemptState: domain.GameStateActive,
				ExpectedSeriesRevision: seriesRevision, ExpectedSeriesState: domain.SeriesStateLocked,
			})
			results <- settleResult{record: record, changed: won, err: settleErr}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	changedCount := 0
	commitIDs := make(map[uuid.UUID]struct{})
	resultSequences := make(map[int64]struct{})
	for result := range results {
		require.NoError(t, result.err)
		require.NotNil(t, result.record)
		require.Positive(t, result.record.Event.ServerSequence)
		if result.changed {
			changedCount++
		}
		commitIDs[result.record.Commit.ID] = struct{}{}
		resultSequences[result.record.Event.ServerSequence] = struct{}{}
	}
	require.Equal(t, 1, changedCount)
	require.Len(t, commitIDs, 1)
	require.Len(t, resultSequences, 1)

	scope := resultrepo.ResultScope{
		TournamentID: fixture.draft.tournamentID, RosterID: fixture.draft.rosterID,
		SeriesID: fixture.draft.seriesID, AttemptID: fixture.attemptID,
	}
	current, err := repository.Current(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, "completed", current.Event.ResultState)
	require.Equal(t, "completed", current.SeriesRevision.ResultState)
	require.EqualValues(t, 2, current.ScoreRevision.RevisionNumber)
	require.Equal(t, "tournament.result.committed", current.Outbox.Topic)

	var (
		ledgerScoreRevisionID uuid.UUID
		ledgerSeriesID        uuid.UUID
		ledgerAttemptID       uuid.UUID
		ledgerGameRevisionID  uuid.UUID
		ledgerResultEventID   uuid.UUID
		ledgerState           string
		ledgerReason          string
		adjudicationCount     int
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT score_revision_id, series_id, game_attempt_id,
			game_result_revision_id, result_event_id, result_state, result_reason
		FROM series_score_revision_attempts
		WHERE score_revision_id = $1`, current.ScoreRevision.ID,
	).Scan(
		&ledgerScoreRevisionID,
		&ledgerSeriesID,
		&ledgerAttemptID,
		&ledgerGameRevisionID,
		&ledgerResultEventID,
		&ledgerState,
		&ledgerReason,
	))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM series_score_revision_adjudications
		WHERE score_revision_id = $1`, current.ScoreRevision.ID,
	).Scan(&adjudicationCount))
	require.Equal(t, current.ScoreRevision.ID, ledgerScoreRevisionID)
	require.Equal(t, fixture.draft.seriesID, ledgerSeriesID)
	require.Equal(t, fixture.attemptID, ledgerAttemptID)
	require.Equal(t, current.GameRevision.ID, ledgerGameRevisionID)
	require.Equal(t, current.Event.ID, ledgerResultEventID)
	require.Equal(t, string(domain.GameStateCompleted), ledgerState)
	require.Equal(t, string(domain.GameResultReasonSolved), ledgerReason)
	require.Zero(t, adjudicationCount)

	history, err := repository.History(ctx, scope)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Contains(t, resultSequences, current.Event.ServerSequence)
	require.Equal(t, current.Event.ServerSequence, history[0].ServerSequence)
	require.Positive(t, history[0].ServerSequence)
	require.EqualValues(t, 2, history[0].ScoreRevisionNumber)

	var submissionSequence, resultSequence int64
	err = sharedPool.QueryRow(ctx, `
		SELECT submission_event_sequence, result_event_sequence
		FROM game_attempts
		WHERE id = $1`, fixture.attemptID,
	).Scan(&submissionSequence, &resultSequence)
	require.NoError(t, err)
	require.Equal(t, submission.ServerSequence, submissionSequence)
	require.Equal(t, current.Event.ServerSequence, resultSequence)

	var evidenceCounts [4]int
	err = sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM result_commits WHERE attempt_id = $1),
			(SELECT COUNT(*) FROM audit_events WHERE result_event_id = $2),
			(SELECT COUNT(*)
			 FROM outbox_events AS event
			 INNER JOIN outbox_result_sources AS source ON source.outbox_event_id = event.id
			 WHERE source.result_event_id = $2),
			(SELECT COUNT(*) FROM result_projection_evidence WHERE result_event_id = $2)`,
		fixture.attemptID,
		current.Event.ID,
	).Scan(&evidenceCounts[0], &evidenceCounts[1], &evidenceCounts[2], &evidenceCounts[3])
	require.NoError(t, err)
	require.Equal(t, [4]int{1, 1, 1, 1}, evidenceCounts)

	var (
		persistedOutboxID      uuid.UUID
		persistedResultEventID uuid.UUID
		persistedSeriesID      uuid.UUID
		persistedEvidenceID    uuid.UUID
		persistedIdempotency   uuid.UUID
		persistedSequence      int64
		persistedProjectionID  uuid.UUID
		persistedProjectionRev int64
		persistedOrdinal       int16
		sourceTargetID         uuid.UUID
		sourceTargetRev        int64
		sourceTargetOrdinal    int16
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT event.id,
			source.result_event_id, source.series_id, source.projection_evidence_id,
			event.idempotency_key, event.sequence,
			event.projection_revision_id, projection.revision_number, event.projection_ordinal,
			source.projection_revision_id, source.projection_revision, source.projection_ordinal
		FROM outbox_events AS event
		INNER JOIN outbox_result_sources AS source ON source.outbox_event_id = event.id
		INNER JOIN projection_revisions AS projection ON projection.id = event.projection_revision_id
		WHERE event.id = $1`, current.Outbox.ID,
	).Scan(
		&persistedOutboxID,
		&persistedResultEventID,
		&persistedSeriesID,
		&persistedEvidenceID,
		&persistedIdempotency,
		&persistedSequence,
		&persistedProjectionID,
		&persistedProjectionRev,
		&persistedOrdinal,
		&sourceTargetID,
		&sourceTargetRev,
		&sourceTargetOrdinal,
	))
	require.Equal(t, current.Outbox.ID, persistedOutboxID)
	require.Equal(t, current.Event.ID, persistedResultEventID)
	require.Equal(t, fixture.draft.seriesID, persistedSeriesID)
	require.Equal(t, current.ProjectionEvidence.ID, persistedEvidenceID)
	require.Equal(t, current.Outbox.IdempotencyKey, persistedIdempotency)
	require.Equal(t, current.Outbox.Sequence, persistedSequence)
	require.Equal(t, current.ProjectionEvidence.ID, persistedProjectionID)
	require.Equal(t, current.Outbox.ProjectionRevisionID, persistedProjectionID)
	require.EqualValues(t, sourceProjectionRevision+1, persistedProjectionRev)
	require.Positive(t, persistedOrdinal)
	require.Equal(t, persistedProjectionID, sourceTargetID)
	require.Equal(t, persistedProjectionRev, sourceTargetRev)
	require.Equal(t, persistedOrdinal, sourceTargetOrdinal)

	assertResultOutboxPayloadRetryConflict(ctx, t, current)

	publishedProjectionID, publishedProjectionRev := currentPublishedProjection(
		ctx,
		t,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
	)
	require.Equal(t, persistedProjectionID, publishedProjectionID)
	require.Equal(t, persistedProjectionRev, publishedProjectionRev)
}

func assertResultOutboxPayloadRetryConflict(
	ctx context.Context,
	t *testing.T,
	current *resultrepo.ResultCommitRecord,
) {
	t.Helper()

	var (
		sequenceBefore int64
		eventCount     int
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT next_sequence
		FROM tournament_outbox_cursors
		WHERE tournament_id = $1`, current.Outbox.TournamentID,
	).Scan(&sequenceBefore))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM outbox_events
		WHERE tournament_id = $1`, current.Outbox.TournamentID,
	).Scan(&eventCount))

	_, err := sqlc.New(sharedPool).CreateResultOutboxEvent(ctx, sqlc.CreateResultOutboxEventParams{
		ID:                   uuid.New(),
		TournamentID:         current.Outbox.TournamentID,
		RosterID:             current.Outbox.RosterID,
		ProjectionRevisionID: current.Outbox.ProjectionRevisionID,
		ProjectionRevision:   current.Outbox.ProjectionRevision,
		Topic:                current.Outbox.Topic,
		Payload:              []byte(`{"result_event_id":"changed"}`),
		IdempotencyKey:       current.Outbox.IdempotencyKey,
		CreatedAt:            current.Outbox.CreatedAt,
		SeriesID:             current.ProjectionEvidence.SeriesID,
		ResultEventID:        current.Event.ID,
		ProjectionEvidenceID: current.ProjectionEvidence.ID,
	})
	require.ErrorIs(t, err, pgx.ErrNoRows)

	var (
		sequenceAfter   int64
		eventCountAfter int
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT next_sequence
		FROM tournament_outbox_cursors
		WHERE tournament_id = $1`, current.Outbox.TournamentID,
	).Scan(&sequenceAfter))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM outbox_events
		WHERE tournament_id = $1`, current.Outbox.TournamentID,
	).Scan(&eventCountAfter))
	require.Equal(t, sequenceBefore, sequenceAfter)
	require.Equal(t, eventCount, eventCountAfter)
}

func currentPublishedProjection(
	ctx context.Context,
	t testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
) (uuid.UUID, int64) {
	t.Helper()

	var (
		projectionID uuid.UUID
		revision     int64
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT id, revision_number
		FROM projection_revisions
		WHERE tournament_id = $1
			AND roster_id = $2
			AND state = 'published'
		ORDER BY revision_number DESC, id DESC
		LIMIT 1`, tournamentID, rosterID,
	).Scan(&projectionID, &revision)
	require.NoError(t, err)
	return projectionID, revision
}
