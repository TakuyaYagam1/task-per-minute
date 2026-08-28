//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type arenaCorrectionRepositoryFixture struct {
	resultFixture arenaResultAuditMigrationFixture
	participants  []uuid.UUID
	result        *postgres.ArenaResultCommitRecord
	projection    *postgres.ArenaProjectionRecord
	waveID        uuid.UUID
	windowID      uuid.UUID
	waveRevision  int64
	deadline      time.Time
	nextTime      time.Time
}

func TestArenaCorrectionRepository(t *testing.T) {
	t.Run("rebuilds the complete DAG and rolls back late failures", func(t *testing.T) {
		ctx := context.Background()
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaCorrectionRepositoryFixture(t, ctx)
		repository := postgres.NewArenaCorrectionPostgres(postgres.NewTxManager(sharedPool))
		traversal, err := repository.Traverse(
			ctx,
			postgres.ArenaResultScope{
				TournamentID: fixture.resultFixture.draft.tournamentID,
				RosterID:     fixture.resultFixture.draft.rosterID,
				SeriesID:     fixture.resultFixture.draft.seriesID,
				AttemptID:    fixture.resultFixture.attemptID,
			},
			fixture.result.GameRevision.ID,
		)
		require.NoError(t, err)
		require.True(t, traversal.SourceIsCurrent)
		require.Empty(t, traversal.CutoffCode)
		require.Len(t, traversal.Descendants, 4)

		input := newArenaCorrectionInput(t, ctx, fixture, fixture.result, fixture.projection, 1, fixture.nextTime)
		record, err := repository.Rebuild(ctx, input)
		require.NoError(t, err)
		require.EqualValues(t, 2, record.ResultCommit.GameRevision.RevisionNumber)
		require.EqualValues(t, 3, record.ResultCommit.ScoreRevision.RevisionNumber)
		require.EqualValues(t, 2, record.ResultCommit.SeriesRevision.RevisionNumber)
		require.Equal(t, "arena.result.corrected", record.ResultCommit.Audit.Action)
		require.Equal(t, input.ProjectionIDs.RevisionID, record.Projection.Revision.ID)
		require.Len(t, record.Projection.Artifacts, 4)
		require.Equal(t, []uuid.UUID{fixture.windowID}, record.ClosedReadyWindows)

		var (
			oldProjectionState string
			oldArtifactCount   int
			windowState        string
			waveState          string
			readinessCount     int
			auditCount         int
		)
		err = sharedPool.QueryRow(ctx, `
			SELECT state
			FROM arena_projection_revisions
			WHERE id = $1`, fixture.projection.Revision.ID).Scan(&oldProjectionState)
		require.NoError(t, err)
		require.Equal(t, "superseded", oldProjectionState)
		err = sharedPool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM arena_projection_artifacts
			WHERE produced_by_revision_id = $1`, fixture.projection.Revision.ID).Scan(&oldArtifactCount)
		require.NoError(t, err)
		require.Equal(t, 4, oldArtifactCount)
		err = sharedPool.QueryRow(ctx, `
			SELECT ready_window.state, wave.state,
				(SELECT COUNT(*) FROM arena_wave_readiness WHERE ready_window_id = ready_window.id)
			FROM arena_ready_windows AS ready_window
			INNER JOIN arena_waves AS wave ON wave.id = ready_window.wave_id
			WHERE ready_window.id = $1`, fixture.windowID).Scan(&windowState, &waveState, &readinessCount)
		require.NoError(t, err)
		require.Equal(t, "superseded", windowState)
		require.Equal(t, "superseded", waveState)
		require.Zero(t, readinessCount)
		err = sharedPool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM arena_audit_events
			WHERE tournament_id = $1`, fixture.resultFixture.draft.tournamentID).Scan(&auditCount)
		require.NoError(t, err)
		require.Equal(t, 2, auditCount)

		failedInput := newArenaCorrectionInput(
			t, ctx, fixture, record.ResultCommit, record.Projection, 0, fixture.nextTime.Add(time.Second),
		)
		failedInput.ProjectionArtifacts[0].Dependencies = append(
			failedInput.ProjectionArtifacts[0].Dependencies,
			postgres.ArenaProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact",
				DependsOnArtifactID: &failedInput.ProjectionArtifacts[0].ID,
			},
		)
		_, err = repository.Rebuild(ctx, failedInput)
		require.Error(t, err)

		var (
			commitCount       int
			currentRevisionID uuid.UUID
			currentProjection uuid.UUID
		)
		err = sharedPool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM arena_result_commits
			WHERE attempt_id = $1`, fixture.resultFixture.attemptID).Scan(&commitCount)
		require.NoError(t, err)
		require.Equal(t, 2, commitCount)
		err = sharedPool.QueryRow(ctx, `
			SELECT current_revision_id
			FROM arena_official_result_heads
			WHERE entity_kind = 'game_attempt' AND entity_id = $1`, fixture.resultFixture.attemptID).
			Scan(&currentRevisionID)
		require.NoError(t, err)
		require.Equal(t, record.ResultCommit.GameRevision.ID, currentRevisionID)
		err = sharedPool.QueryRow(ctx, `
			SELECT id
			FROM arena_projection_revisions
			WHERE tournament_id = $1 AND state = 'published'`, fixture.resultFixture.draft.tournamentID).
			Scan(&currentProjection)
		require.NoError(t, err)
		require.Equal(t, record.Projection.Revision.ID, currentProjection)
	})

	t.Run("rejects a started Wave cutoff without mutations", func(t *testing.T) {
		ctx := context.Background()
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaCorrectionRepositoryFixture(t, ctx)
		waveRepository := postgres.NewArenaWavePostgres(postgres.NewTxManager(sharedPool))
		waveRevision := fixture.waveRevision
		for _, participantID := range fixture.participants[1:] {
			record, _, err := waveRepository.MarkReady(
				ctx,
				fixture.resultFixture.draft.tournamentID,
				fixture.waveID,
				fixture.windowID,
				participantID,
				waveRevision,
				fixture.nextTime.Add(-time.Second),
			)
			require.NoError(t, err)
			waveRevision = record.Revision
		}
		startedAt := fixture.nextTime.Add(-500 * time.Millisecond)
		_, changed, err := waveRepository.Start(
			ctx,
			fixture.resultFixture.draft.tournamentID,
			fixture.waveID,
			fixture.windowID,
			waveRevision,
			startedAt,
		)
		require.NoError(t, err)
		require.True(t, changed)

		repository := postgres.NewArenaCorrectionPostgres(postgres.NewTxManager(sharedPool))
		traversal, err := repository.Traverse(
			ctx,
			postgres.ArenaResultScope{
				TournamentID: fixture.resultFixture.draft.tournamentID,
				RosterID:     fixture.resultFixture.draft.rosterID,
				SeriesID:     fixture.resultFixture.draft.seriesID,
				AttemptID:    fixture.resultFixture.attemptID,
			},
			fixture.result.GameRevision.ID,
		)
		require.NoError(t, err)
		require.Equal(t, "wave_started", traversal.CutoffCode)
		require.Empty(t, traversal.Descendants)

		input := newArenaCorrectionInput(t, ctx, fixture, fixture.result, fixture.projection, 1, fixture.nextTime)
		_, err = repository.Rebuild(ctx, input)
		require.ErrorIs(t, err, postgres.ErrArenaCorrectionCutoff)
		var commitCount int
		err = sharedPool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM arena_result_commits
			WHERE attempt_id = $1`, fixture.resultFixture.attemptID).Scan(&commitCount)
		require.NoError(t, err)
		require.Equal(t, 1, commitCount)
	})
}

func createArenaCorrectionRepositoryFixture(
	t testing.TB,
	ctx context.Context,
) arenaCorrectionRepositoryFixture {
	t.Helper()

	resultFixture := createArenaResultAuditMigrationFixture(t, ctx)
	participants := append([]uuid.UUID(nil), resultFixture.draft.participantIDs...)
	playerIDs := createArenaMigrationPlayers(t, ctx, 2)
	for index, playerID := range playerIDs {
		var participantID uuid.UUID
		err := sharedPool.QueryRow(ctx, `
			INSERT INTO arena_participants (roster_id, player_id, seed, attendance)
			VALUES ($1, $2, $3, 'checked_in')
			RETURNING id`, resultFixture.draft.rosterID, playerID, index+3).Scan(&participantID)
		require.NoError(t, err)
		participants = append(participants, participantID)
	}

	resultRepository := postgres.NewArenaResultPostgres(postgres.NewTxManager(sharedPool))
	winnerID := participants[0]
	createdAt := resultFixture.lockedAt.Add(time.Second)
	digest := sha256.Sum256([]byte("accepted correction fixture submission"))
	submission, changed, err := resultRepository.RecordSubmission(ctx, postgres.ArenaSubmissionInput{
		ID: uuid.New(),
		Scope: postgres.ArenaResultScope{
			TournamentID: resultFixture.draft.tournamentID, RosterID: resultFixture.draft.rosterID,
			SeriesID: resultFixture.draft.seriesID, AttemptID: resultFixture.attemptID,
		},
		AssignmentID: resultFixture.assignmentID, ParticipantID: winnerID, ServerSequence: 1,
		IdempotencyKey: uuid.New(), Status: "accepted", PayloadDigest: digest,
		SubmittedAt: createdAt, ReceivedAt: createdAt, CreatedAt: createdAt,
		ExpectedAttemptRevision: 1, ExpectedAttemptState: domain.ArenaGameStateActive,
	})
	require.NoError(t, err)
	require.True(t, changed)

	var seriesRevision int64
	err = sharedPool.QueryRow(ctx, `SELECT revision FROM arena_series WHERE id = $1`, resultFixture.draft.seriesID).
		Scan(&seriesRevision)
	require.NoError(t, err)
	settledAt := createdAt.Add(time.Second)
	projectionDigest := sha256.Sum256([]byte("initial correction fixture projection"))
	result, changed, err := resultRepository.Settle(ctx, postgres.ArenaResultSettlementInput{
		IDs: postgres.ArenaResultSettlementIDs{
			CommitID: uuid.New(), ResultEventID: uuid.New(), ResultEventIdempotencyKey: uuid.New(),
			GameResultRevisionID: uuid.New(), SeriesScoreRevisionID: uuid.New(),
			SeriesResultRevisionID: uuid.New(), AuditEventID: uuid.New(), OutboxEventID: uuid.New(),
			OutboxIdempotencyKey: uuid.New(), ProjectionEvidenceID: uuid.New(), CommitIdempotencyKey: uuid.New(),
		},
		Scope: postgres.ArenaResultScope{
			TournamentID: resultFixture.draft.tournamentID, RosterID: resultFixture.draft.rosterID,
			SeriesID: resultFixture.draft.seriesID, AttemptID: resultFixture.attemptID,
		},
		SubmissionEventID: submission.ID, ResultServerSequence: 1,
		GameState: domain.ArenaGameStateCompleted, GameReason: domain.ArenaGameResultReasonSolved,
		GameWinnerID: &winnerID, Score: domain.ArenaSeriesScore{FirstParticipantWins: 1},
		NextSeriesState: domain.ArenaSeriesStateCompleted, SeriesResultReason: "score_complete",
		SeriesWinnerID: &winnerID, ActorKind: "server",
		ProjectionArtifactKinds: []domain.ArenaArtifactKind{
			domain.ArenaArtifactKindStandings, domain.ArenaArtifactKindBracket,
			domain.ArenaArtifactKindTopFour, domain.ArenaArtifactKindChampion,
		},
		ProjectionPayloadDigest: projectionDigest, SettledAt: settledAt,
		ExpectedAttemptRevision: 1, ExpectedAttemptState: domain.ArenaGameStateActive,
		ExpectedSeriesRevision: seriesRevision, ExpectedSeriesState: domain.ArenaSeriesStateLocked,
	})
	require.NoError(t, err)
	require.True(t, changed)

	projectionRepository := postgres.NewArenaProjectionPostgres(postgres.NewTxManager(sharedPool))
	projectionAt := settledAt.Add(time.Second)
	projection, err := projectionRepository.Publish(ctx, postgres.ArenaProjectionPublishInput{
		IDs: postgres.ArenaProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: postgres.ArenaProjectionScope{
			TournamentID: resultFixture.draft.tournamentID, RosterID: resultFixture.draft.rosterID,
		},
		Source: postgres.ArenaProjectionSource{
			Kind: "official_result", OfficialResultRevisionID: &result.GameRevision.ID,
			Reason: "initial result projection",
		},
		Artifacts: arenaCorrectionProjectionArtifacts(
			participants, result.GameRevision.ID, resultFixture.draft.seriesID, "initial",
		),
		SupersessionReason: "replace initial projection", CutoffAt: projectionAt,
		CreatedAt: projectionAt, PublishedAt: projectionAt,
	})
	require.NoError(t, err)

	waveRepository := postgres.NewArenaWavePostgres(postgres.NewTxManager(sharedPool))
	waveID := uuid.New()
	waveAt := projectionAt.Add(time.Second)
	_, err = waveRepository.Create(ctx, postgres.ArenaWaveCreateInput{
		ID: waveID, TournamentID: resultFixture.draft.tournamentID, RosterID: resultFixture.draft.rosterID,
		RevisionID: domain.ArenaWaveRevisionID(uuid.New()), ParticipantIDs: participants, CreatedAt: waveAt,
	})
	require.NoError(t, err)
	windowID := uuid.New()
	openedAt := waveAt.Add(time.Second)
	deadline := openedAt.Add(30 * time.Second)
	wave, changed, err := waveRepository.OpenReadyWindow(
		ctx,
		resultFixture.draft.tournamentID,
		waveID,
		1,
		postgres.ArenaReadyWindowInput{
			ID: windowID, RevisionID: domain.ArenaReadyWindowRevisionID(uuid.New()),
			OpenedAt: openedAt, Deadline: deadline,
		},
	)
	require.NoError(t, err)
	require.True(t, changed)
	wave, changed, err = waveRepository.MarkReady(
		ctx,
		resultFixture.draft.tournamentID,
		waveID,
		windowID,
		participants[0],
		wave.Revision,
		openedAt.Add(time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)

	return arenaCorrectionRepositoryFixture{
		resultFixture: resultFixture, participants: participants, result: result, projection: projection,
		waveID: waveID, windowID: windowID, waveRevision: wave.Revision,
		deadline: deadline, nextTime: openedAt.Add(2 * time.Second),
	}
}

func newArenaCorrectionInput(
	t testing.TB,
	ctx context.Context,
	fixture arenaCorrectionRepositoryFixture,
	currentResult *postgres.ArenaResultCommitRecord,
	currentProjection *postgres.ArenaProjectionRecord,
	winnerIndex int,
	correctedAt time.Time,
) postgres.ArenaCorrectionInput {
	t.Helper()

	var (
		attemptRevision int64
		attemptState    string
		seriesRevision  int64
		seriesState     string
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT revision, state
		FROM arena_game_attempts
		WHERE id = $1`, fixture.resultFixture.attemptID).Scan(&attemptRevision, &attemptState)
	require.NoError(t, err)
	err = sharedPool.QueryRow(ctx, `
		SELECT revision, state
		FROM arena_series
		WHERE id = $1`, fixture.resultFixture.draft.seriesID).Scan(&seriesRevision, &seriesState)
	require.NoError(t, err)

	ids := postgres.ArenaResultSettlementIDs{
		CommitID: uuid.New(), ResultEventID: uuid.New(), ResultEventIdempotencyKey: uuid.New(),
		GameResultRevisionID: uuid.New(), SeriesScoreRevisionID: uuid.New(),
		SeriesResultRevisionID: uuid.New(), AuditEventID: uuid.New(), OutboxEventID: uuid.New(),
		OutboxIdempotencyKey: uuid.New(), ProjectionEvidenceID: uuid.New(), CommitIdempotencyKey: uuid.New(),
	}
	winnerID := fixture.participants[winnerIndex]
	score := domain.ArenaSeriesScore{}
	if winnerIndex == 0 {
		score.FirstParticipantWins = 1
	} else {
		score.SecondParticipantWins = 1
	}
	reason := fmt.Sprintf("operator correction revision %d", currentResult.GameRevision.RevisionNumber+1)
	digest := sha256.Sum256([]byte(reason))
	seriesResultRevisionID := currentResult.SeriesRevision.ID
	return postgres.ArenaCorrectionInput{
		IDs: ids, ProjectionIDs: postgres.ArenaProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: postgres.ArenaResultScope{
			TournamentID: fixture.resultFixture.draft.tournamentID, RosterID: fixture.resultFixture.draft.rosterID,
			SeriesID: fixture.resultFixture.draft.seriesID, AttemptID: fixture.resultFixture.attemptID,
		},
		SourceRevisionID:        currentResult.GameRevision.ID,
		ExpectedAttemptRevision: attemptRevision, ExpectedAttemptState: domain.ArenaGameState(attemptState),
		ExpectedScoreRevisionID: currentResult.ScoreRevision.ID,
		ExpectedScoreRevision:   currentResult.ScoreRevision.RevisionNumber,
		ExpectedSeriesRevision:  seriesRevision, ExpectedSeriesState: domain.ArenaSeriesState(seriesState),
		ExpectedSeriesResultRevisionID: &seriesResultRevisionID,
		ExpectedProjectionRevisionID:   currentProjection.Revision.ID,
		ResultServerSequence:           currentResult.Event.ServerSequence + 1,
		GameState:                      domain.ArenaGameStateCompleted, GameReason: domain.ArenaGameResultReasonSurrender,
		GameWinnerID: &winnerID, Score: score, NextSeriesState: domain.ArenaSeriesStateCompleted,
		SeriesResultReason: "operator_correction", SeriesWinnerID: &winnerID,
		OperatorID: uuid.New(), Reason: reason,
		ProjectionArtifacts: arenaCorrectionProjectionArtifacts(
			fixture.participants, ids.GameResultRevisionID, fixture.resultFixture.draft.seriesID,
			"correction-"+ids.GameResultRevisionID.String(),
		),
		ProjectionPayloadDigest: digest, CorrectedAt: correctedAt,
	}
}

func arenaCorrectionProjectionArtifacts(
	participantIDs []uuid.UUID,
	sourceRevisionID uuid.UUID,
	seriesID uuid.UUID,
	version string,
) []postgres.ArenaProjectionArtifactInput {
	standingsID := uuid.New()
	bracketID := uuid.New()
	topFourID := uuid.New()
	championID := uuid.New()
	standingsMembers := make([]postgres.ArenaProjectionMemberInput, 0, len(participantIDs))
	positionMembers := make([]postgres.ArenaProjectionMemberInput, 0, len(participantIDs))
	for index, participantID := range participantIDs {
		score := int64((len(participantIDs) - index) * 1000)
		position := int32(index + 1)
		standingsMembers = append(standingsMembers, postgres.ArenaProjectionMemberInput{
			ParticipantID: participantID, Position: position, ScoreMilli: &score,
		})
		positionMembers = append(positionMembers, postgres.ArenaProjectionMemberInput{
			ParticipantID: participantID, Position: position,
		})
	}
	return []postgres.ArenaProjectionArtifactInput{
		arenaCorrectionProjectionArtifact(
			standingsID,
			domain.ArenaArtifactKindStandings,
			version,
			json.RawMessage(fmt.Sprintf(`{"entries":[{"version":%q}]}`, version)),
			standingsMembers,
			postgres.ArenaProjectionDependencyInput{
				ID: uuid.New(), Kind: "official_result", OfficialResultRevisionID: &sourceRevisionID,
				OfficialResultSeriesID: &seriesID,
			},
		),
		arenaCorrectionProjectionArtifact(
			bracketID,
			domain.ArenaArtifactKindBracket,
			version,
			json.RawMessage(fmt.Sprintf(`{"rounds":[{"version":%q}]}`, version)),
			positionMembers,
			postgres.ArenaProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &standingsID,
			},
		),
		arenaCorrectionProjectionArtifact(
			topFourID,
			domain.ArenaArtifactKindTopFour,
			version,
			json.RawMessage(fmt.Sprintf(
				`{"participants":[%q,%q,%q,%q]}`,
				participantIDs[0], participantIDs[1], participantIDs[2], participantIDs[3],
			)),
			positionMembers,
			postgres.ArenaProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &bracketID,
			},
		),
		arenaCorrectionProjectionArtifact(
			championID,
			domain.ArenaArtifactKindChampion,
			version,
			json.RawMessage(fmt.Sprintf(`{"participant_id":%q}`, participantIDs[0])),
			positionMembers[:1],
			postgres.ArenaProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &topFourID,
			},
		),
	}
}

func arenaCorrectionProjectionArtifact(
	id uuid.UUID,
	kind domain.ArenaArtifactKind,
	version string,
	payload json.RawMessage,
	members []postgres.ArenaProjectionMemberInput,
	dependency postgres.ArenaProjectionDependencyInput,
) postgres.ArenaProjectionArtifactInput {
	digest := sha256.Sum256(payload)
	return postgres.ArenaProjectionArtifactInput{
		ID: id, Kind: kind, Key: string(kind) + "-" + version, Payload: payload,
		PayloadDigest: digest, Members: members,
		Dependencies: []postgres.ArenaProjectionDependencyInput{dependency},
	}
}
