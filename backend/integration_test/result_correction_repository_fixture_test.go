//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func createCorrectionRepositoryFixture(
	ctx context.Context, tb testing.TB,
) correctionRepositoryFixture {
	tb.Helper()

	resultFixture := createResultAuditMigrationFixture(ctx, tb)
	participants := append([]uuid.UUID(nil), resultFixture.draft.participantIDs...)
	playerIDs := createMigrationPlayers(ctx, tb, 2)
	for _, playerID := range playerIDs {
		var participantID uuid.UUID
		err := sharedPool.QueryRow(ctx, `
			INSERT INTO participants (roster_id, player_id, seed, attendance)
			VALUES (
				$1,
				$2,
				(SELECT COALESCE(MAX(seed), 0) + 1 FROM participants WHERE roster_id = $1),
				'checked_in'
			)
			RETURNING id`, resultFixture.draft.rosterID, playerID).Scan(&participantID)
		require.NoError(tb, err)
		participants = append(participants, participantID)
	}

	resultRepository := resultauthority.NewResultPostgres(postgres.NewTxManager(sharedPool))
	winnerID := participants[0]
	createdAt := resultFixture.lockedAt.Add(time.Second)
	digest := sha256.Sum256([]byte("accepted correction fixture submission"))
	submission, changed, err := resultRepository.RecordSubmission(ctx, resultrepo.SubmissionInput{
		ID: uuid.New(),
		Scope: resultrepo.ResultScope{
			TournamentID: resultFixture.draft.tournamentID, RosterID: resultFixture.draft.rosterID,
			SeriesID: resultFixture.draft.seriesID, AttemptID: resultFixture.attemptID,
		},
		AssignmentID: resultFixture.assignmentID, ParticipantID: winnerID,
		IdempotencyKey: uuid.New(), Status: "accepted", PayloadDigest: digest,
		IntentDigest: digest,
		SubmittedAt:  createdAt, ReceivedAt: createdAt, CreatedAt: createdAt,
		ExpectedAttemptRevision: 1, ExpectedAttemptState: domain.GameStateActive,
	})
	require.NoError(tb, err)
	require.True(tb, changed)

	var seriesRevision int64
	err = sharedPool.QueryRow(ctx, `SELECT revision FROM series WHERE id = $1`, resultFixture.draft.seriesID).
		Scan(&seriesRevision)
	require.NoError(tb, err)
	settledAt := createdAt.Add(time.Second)
	projectionDigest := sha256.Sum256([]byte("initial correction fixture projection"))
	result, changed, err := resultRepository.Settle(ctx, resultrepo.ResultSettlementInput{
		IDs: resultrepo.ResultSettlementIDs{
			CommitID: uuid.New(), ResultEventID: uuid.New(), ResultEventIdempotencyKey: uuid.New(),
			GameResultRevisionID: uuid.New(), SeriesScoreRevisionID: uuid.New(),
			SeriesResultRevisionID: uuid.New(), AuditEventID: uuid.New(), OutboxEventID: uuid.New(),
			OutboxIdempotencyKey: uuid.New(), ProjectionEvidenceID: uuid.New(), CommitIdempotencyKey: uuid.New(),
		},
		Scope: resultrepo.ResultScope{
			TournamentID: resultFixture.draft.tournamentID, RosterID: resultFixture.draft.rosterID,
			SeriesID: resultFixture.draft.seriesID, AttemptID: resultFixture.attemptID,
		},
		SubmissionEventID: submission.ID,
		GameState:         domain.GameStateCompleted, GameReason: domain.GameResultReasonSolved,
		GameWinnerID: &winnerID, Score: domain.SeriesScore{FirstParticipantWins: 1},
		NextSeriesState: domain.SeriesStateCompleted, SeriesResultReason: "score_complete",
		SeriesWinnerID: &winnerID, ActorKind: "server",
		ProjectionArtifactKinds: []domain.ArtifactKind{
			domain.ArtifactKindStandings, domain.ArtifactKindBracket,
			domain.ArtifactKindTopFour,
		},
		ProjectionPayloadDigest: projectionDigest, SettledAt: settledAt,
		ExpectedAttemptRevision: 1, ExpectedAttemptState: domain.GameStateActive,
		ExpectedSeriesRevision: seriesRevision, ExpectedSeriesState: domain.SeriesStateLocked,
	})
	require.NoError(tb, err)
	require.True(tb, changed)

	projectionRepository := projectionrepo.NewProjectionPostgres(postgres.NewTxManager(sharedPool))
	projectionAt := settledAt.Add(time.Second)
	projection, err := projectionRepository.Publish(ctx, projectionrepo.ProjectionPublishInput{
		IDs: projectionrepo.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: projectionrepo.ProjectionScope{
			TournamentID: resultFixture.draft.tournamentID, RosterID: resultFixture.draft.rosterID,
		},
		Source: projectionrepo.ProjectionSource{
			Kind: "official_result", OfficialResultRevisionID: &result.GameRevision.ID,
			Reason: "initial result projection",
		},
		Artifacts: correctionProjectionArtifacts(
			participants, result.GameRevision.ID, resultFixture.draft.seriesID, "initial",
		),
		SupersessionReason: "replace initial projection", CutoffAt: projectionAt,
		CreatedAt: projectionAt, PublishedAt: projectionAt,
	})
	require.NoError(tb, err)

	waveRepository := postgres.NewWavePostgres(postgres.NewTxManager(sharedPool))
	waveID := uuid.New()
	waveAt := projectionAt.Add(time.Second)
	_, err = waveRepository.Create(ctx, postgres.WaveCreateInput{
		ID: waveID, TournamentID: resultFixture.draft.tournamentID, RosterID: resultFixture.draft.rosterID,
		RevisionID: domain.WaveRevisionID(uuid.New()), ParticipantIDs: participants,
		CommandID: uuid.New(), SourceProjectionRevisionID: projection.Revision.ID,
		SourceProjectionRevision: projection.Revision.RevisionNumber,
		Series: []postgres.WaveSeriesInput{
			{
				ID: uuid.New(), FirstParticipantID: participants[0], SecondParticipantID: participants[1],
				Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()),
			},
			{
				ID: uuid.New(), FirstParticipantID: participants[2], SecondParticipantID: participants[3],
				Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()),
			},
		},
		CreatedAt: waveAt,
	})
	require.NoError(tb, err)
	windowID := uuid.New()
	openedAt := waveAt.Add(time.Second)
	deadline := openedAt.Add(30 * time.Second)
	wave, changed, err := waveRepository.OpenReadyWindow(
		ctx,
		resultFixture.draft.tournamentID,
		waveID,
		1,
		postgres.ReadyWindowInput{
			ID: windowID, RevisionID: domain.ReadyWindowRevisionID(uuid.New()),
			OpenedAt: openedAt, Deadline: deadline,
		},
	)
	require.NoError(tb, err)
	require.True(tb, changed)
	wave, changed, err = waveRepository.MarkReady(
		ctx,
		resultFixture.draft.tournamentID,
		waveID,
		windowID,
		participants[0],
		wave.Revision,
		wave.ReadinessRevisions[participants[0]],
		openedAt.Add(time.Second),
	)
	require.NoError(tb, err)
	require.True(tb, changed)

	return correctionRepositoryFixture{
		resultFixture: resultFixture, participants: participants, result: result, projection: projection,
		waveID: waveID, windowID: windowID, waveRevision: wave.Revision,
		readinessRevisions: wave.ReadinessRevisions,
		deadline:           deadline, nextTime: openedAt.Add(2 * time.Second),
	}
}
