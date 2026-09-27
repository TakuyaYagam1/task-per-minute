//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/draftseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/pauseseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
	snapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/snapshot"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestTournamentPublicLiveProjectionThroughHTTPAndRealtime(t *testing.T) {
	t.Run("active game uses authoritative runtime deadline and redacts presence", func(t *testing.T) {
		ctx := context.Background()
		TruncateTables(t, sharedPool)
		t.Cleanup(func() { TruncateTables(t, sharedPool) })

		draft := createDraftMigrationFixture(ctx, t)
		fixture := createResultAuditMigrationFixtureFromDraft(ctx, t, draft)
		body, read := readPublicProjection(ctx, t, fixture.draft.tournamentID)
		game := publicGameForSeries(t, read.Snapshot, fixture.draft.seriesID)
		require.Equal(t, string(domain.GameStateActive), game.State)
		require.Equal(t, 1, game.Position)
		require.Equal(t, "unknown", game.FirstConnectionStatus)
		require.Equal(t, "unknown", game.SecondConnectionStatus)
		require.NotNil(t, game.StartedAt)
		require.NotNil(t, game.EffectiveDeadline)

		var startedAt time.Time
		var timeLimit int
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT attempt.started_at, snapshot.time_limit
			FROM game_attempts AS attempt
			JOIN assignments AS assignment ON assignment.attempt_id = attempt.id AND assignment.state = 'active'
			JOIN task_snapshots AS snapshot ON snapshot.id = assignment.snapshot_id
			WHERE attempt.id = $1`, fixture.attemptID).Scan(&startedAt, &timeLimit))
		require.NotEqual(t, 180, timeLimit)
		require.Equal(t, startedAt.UTC(), game.StartedAt.UTC())
		require.Equal(t, startedAt.Add(domain.TournamentTaskDuration).UTC(), game.EffectiveDeadline.UTC())
		assertPublicRedaction(t, body)
	})

	t.Run("paused game removes deadline while retaining public state", func(t *testing.T) {
		ctx := context.Background()
		TruncateTables(t, sharedPool)
		t.Cleanup(func() { TruncateTables(t, sharedPool) })

		draft := createDraftMigrationFixture(ctx, t)
		fixture := createResultAuditMigrationFixtureFromDraft(ctx, t, draft)
		pausedAt := fixture.lockedAt.Add(time.Second)
		attemptID := fixture.attemptID
		for _, participantID := range fixture.draft.participantIDs {
			_, err := sharedPool.Exec(ctx, `
				INSERT INTO presence_states (
					id, tournament_id, roster_id, series_id, participant_id,
					state, presence_epoch, revision, connected_at, updated_at
				)
				VALUES ($1, $2, $3, $4, $5, 'connected', 1, 1, $6, $6)`,
				uuid.New(), fixture.draft.tournamentID, fixture.draft.rosterID,
				fixture.draft.seriesID, participantID, fixture.lockedAt,
			)
			require.NoError(t, err)
		}
		_, err := pauseseed.CreatePause(ctx, sharedPool, pauseseed.Input{
			TournamentID:    fixture.draft.tournamentID,
			RosterID:        fixture.draft.rosterID,
			SeriesID:        fixture.draft.seriesID,
			ParticipantIDs:  fixture.draft.participantIDs,
			ScopeKind:       "game_attempt",
			ScopeID:         attemptID,
			GameAttemptID:   &attemptID,
			Reason:          "operator",
			PausedFromState: "active",
			PausedAt:        pausedAt,
			SlotLimit:       2,
		})
		require.NoError(t, err)

		body, read := readPublicProjection(ctx, t, fixture.draft.tournamentID)
		game := publicGameForSeries(t, read.Snapshot, fixture.draft.seriesID)
		require.Equal(t, string(domain.GameStatePaused), game.State)
		require.NotNil(t, game.StartedAt)
		require.Nil(t, game.EffectiveDeadline)
		require.Equal(t, "connected", game.FirstConnectionStatus)
		require.Equal(t, "connected", game.SecondConnectionStatus)
		assertPublicRedaction(t, body)
	})

	t.Run("completed game exposes official result without execution evidence", func(t *testing.T) {
		ctx := context.Background()
		TruncateTables(t, sharedPool)
		t.Cleanup(func() { TruncateTables(t, sharedPool) })

		draft := createDraftMigrationFixture(ctx, t)
		fixture := createResultAuditMigrationFixtureFromDraft(ctx, t, draft)
		submissionID, _ := createAcceptedSubmission(ctx, t, fixture)
		commit := createAtomicResultCommit(ctx, t, fixture, submissionID)
		var startedAt, receivedAt time.Time
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT attempt.started_at, submission.received_at
			FROM game_attempts AS attempt
			JOIN submission_events AS submission
				ON submission.attempt_id = attempt.id
			WHERE attempt.id = $1 AND submission.id = $2`, fixture.attemptID, submissionID).Scan(&startedAt, &receivedAt))

		body, read := readPublicProjection(ctx, t, fixture.draft.tournamentID)
		game := publicGameForSeries(t, read.Snapshot, fixture.draft.seriesID)
		require.Equal(t, string(domain.GameStateCompleted), game.State)
		require.Nil(t, game.EffectiveDeadline)
		require.NotNil(t, game.FinishedAt)
		require.NotNil(t, game.ResultReason)
		require.Equal(t, string(domain.GameResultReasonSolved), *game.ResultReason)
		require.NotNil(t, game.SolveTimeMS)
		require.NotNil(t, game.FinishedAt)
		require.NotEqual(t, receivedAt.UTC(), game.FinishedAt.UTC())
		require.Equal(t, receivedAt.UTC().Sub(startedAt.UTC()).Milliseconds(), *game.SolveTimeMS)
		require.NotNil(t, game.WinnerDisplayName)
		require.NotEmpty(t, read.Snapshot.OfficialResults)
		official := read.Snapshot.OfficialResults[0]
		require.Equal(t, fixture.draft.seriesID, official.SeriesID)
		require.Equal(t, commit.seriesResultRevisionID, official.RevisionID)
		require.Equal(t, "completed", official.State)
		require.NotEmpty(t, official.WinnerDisplayName)
		assertPublicRedaction(t, body)
	})

	t.Run("accepted submission at game start preserves true zero solve time", func(t *testing.T) {
		ctx := context.Background()
		TruncateTables(t, sharedPool)
		t.Cleanup(func() { TruncateTables(t, sharedPool) })

		fixture := createResultAuditMigrationFixture(ctx, t)
		var startedAt time.Time
		require.NoError(t, sharedPool.QueryRow(ctx, `SELECT started_at FROM game_attempts WHERE id = $1`, fixture.attemptID).Scan(&startedAt))
		submissionID := insertPublicProjectionSubmission(ctx, t, fixture, startedAt)
		createAtomicResultCommit(ctx, t, fixture, submissionID)

		_, read := readPublicProjection(ctx, t, fixture.draft.tournamentID)
		game := publicGameForSeries(t, read.Snapshot, fixture.draft.seriesID)
		require.NotNil(t, game.SolveTimeMS)
		require.Equal(t, int64(0), *game.SolveTimeMS)
	})

	t.Run("corrected official head without accepted submission does not reuse solved head timing", func(t *testing.T) {
		ctx := context.Background()
		TruncateTables(t, sharedPool)
		t.Cleanup(func() { TruncateTables(t, sharedPool) })

		fixture := createCorrectionRepositoryFixture(ctx, t)
		corrections := correctionrepo.NewCorrectionPostgres(postgres.NewTxManager(sharedPool))
		input := newCorrectionInput(
			ctx, t,
			fixture,
			fixture.result,
			fixture.projection,
			1,
			fixture.nextTime,
		)
		corrected, err := corrections.Rebuild(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, corrected)
		require.Equal(t, string(domain.GameResultReasonSurrender), corrected.ResultCommit.GameRevision.ResultReason)
		reader := postgres.NewTxManager(sharedPool)
		var (
			found                bool
			currentGameFinished  bool
			currentGameReason    string
			solveSubmissionFound bool
		)
		err = reader.ReadSnapshot(ctx, func(readCtx context.Context) error {
			rows, queryErr := reader.Querier(readCtx).ListPublicTournamentReadSeries(
				readCtx, fixture.resultFixture.draft.tournamentID,
			)
			if queryErr != nil {
				return queryErr
			}
			for _, row := range rows {
				if row.SeriesID != fixture.resultFixture.draft.seriesID {
					continue
				}
				found = true
				currentGameFinished = row.CurrentGameFinishedAt.Valid
				currentGameReason = row.CurrentGameResultReason
				solveSubmissionFound = row.CurrentGameSolveSubmissionReceivedAt.Valid
				break
			}
			return nil
		})
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, currentGameFinished)
		require.Equal(t, string(domain.GameResultReasonSurrender), currentGameReason)
		require.False(t, solveSubmissionFound)
	})

	t.Run("BO1 draft transitions from active to completed with automatic action", func(t *testing.T) {
		ctx := context.Background()
		TruncateTables(t, sharedPool)
		t.Cleanup(func() { TruncateTables(t, sharedPool) })

		fixture := createDraftMigrationFixture(ctx, t)
		body, read := readPublicProjection(ctx, t, fixture.tournamentID)
		assertActiveDraft(t, read.Snapshot, "bo1", fixture.seriesID, 3, 0)
		assertPublicRedaction(t, body)

		completePublicDraft(ctx, t, fixture, "bo1")
		body, read = readPublicProjection(ctx, t, fixture.tournamentID)
		draft := requirePublicDraft(t, read.Snapshot)
		require.Equal(t, "bo1", draft.Format)
		require.Equal(t, "completed", draft.State)
		require.Nil(t, draft.CurrentTurn)
		require.Nil(t, draft.CurrentAction)
		require.Nil(t, draft.CurrentActorDisplayName)
		require.Nil(t, draft.TurnDeadline)
		require.Len(t, draft.SelectedCategories, 1)
		require.Len(t, draft.Actions, 2)
		require.True(t, draft.Actions[1].Automatic)
		assertPublicRedaction(t, body)
	})

	t.Run("BO3 draft exposes four turns and completes with three categories", func(t *testing.T) {
		ctx := context.Background()
		TruncateTables(t, sharedPool)
		t.Cleanup(func() { TruncateTables(t, sharedPool) })

		base := createDraftMigrationFixture(ctx, t)
		createdAt := base.createdAt.Add(time.Minute)
		seriesID := createMigrationSeries(ctx, t, base.tournamentID, base.rosterID, base.participantIDs, "bo3")
		seed, err := draftseed.CreateDraft(ctx, sharedPool, draftseed.DraftInput{
			SeriesID:             seriesID,
			RosterID:             base.rosterID,
			SourcePoolRevisionID: base.normalPoolRevisionID,
			FirstParticipantID:   base.participantIDs[0],
			SecondParticipantID:  base.participantIDs[1],
			Format:               domain.SeriesFormatBO3,
			CategoryPool: []domain.Category{
				domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryForensics,
				domain.CategoryReverse, domain.CategoryPwn,
			},
			AbsoluteDeadline: createdAt.Add(15 * time.Second),
			CreatedAt:        createdAt,
		})
		require.NoError(t, err)
		fixture := draftMigrationFixture{
			tournamentID:        base.tournamentID,
			rosterID:            base.rosterID,
			seriesID:            seriesID,
			draftID:             seed.DraftID,
			initialRevisionID:   seed.InitialRevisionID,
			participantIDs:      base.participantIDs,
			initialServiceEpoch: seed.InitialServiceEpoch,
			createdAt:           createdAt,
		}

		body, read := readPublicProjection(ctx, t, fixture.tournamentID)
		assertActiveDraft(t, read.Snapshot, "bo3", fixture.seriesID, 5, 0)
		assertPublicRedaction(t, body)

		completePublicDraft(ctx, t, fixture, "bo3")
		body, read = readPublicProjection(ctx, t, fixture.tournamentID)
		draft := requirePublicDraft(t, read.Snapshot)
		require.Equal(t, "bo3", draft.Format)
		require.Equal(t, "completed", draft.State)
		require.Len(t, draft.SelectedCategories, 3)
		require.Len(t, draft.Actions, 4)
		require.True(t, draft.Actions[3].Automatic)
		assertPublicRedaction(t, body)
	})
}

func readPublicProjection(ctx context.Context, t *testing.T, tournamentID uuid.UUID) ([]byte, tournamentws.PublicRealtimeReadResult) {
	t.Helper()
	reader := snapshotrepo.NewTournamentSnapshotPostgres(postgres.NewTxManager(sharedPool))
	server := v1.New(v1.Dependencies{
		TournamentSnapshots:         reader,
		PublicTournamentReadLimiter: tournamentFlowLimiter{},
	})
	handler := v1.NewHandler(server, v1.HandlerOptions{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tournaments/"+tournamentID.String()+"/snapshot", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	source, err := websocket.NewTournamentProductionSnapshotSource(reader)
	require.NoError(t, err)
	read, err := source.PublicRealtimeRead(ctx, tournamentID)
	require.NoError(t, err)
	require.NoError(t, read.Snapshot.Validate())
	assertPublicProjectionParity(t, recorder.Body.Bytes(), read.Snapshot)
	return recorder.Body.Bytes(), read
}

func assertPublicProjectionParity(t *testing.T, body []byte, snapshot tournamentws.PublicSnapshot) {
	t.Helper()
	var actual map[string]any
	require.NoError(t, json.Unmarshal(body, &actual))

	expectedBody, err := json.Marshal(map[string]any{
		"live_draft":       snapshot.Draft,
		"live_series":      snapshot.LiveSeries,
		"official_results": snapshot.OfficialResults,
	})
	require.NoError(t, err)
	var expected map[string]any
	require.NoError(t, json.Unmarshal(expectedBody, &expected))
	if draft, ok := actual["live_draft"].(map[string]any); ok {
		require.Equal(t, snapshot.Tournament.TournamentID.String(), draft["tournament_id"])
		require.Equal(t, float64(snapshot.Revision), draft["projection_revision"])
	}
	stripPublicTransportMetadata(actual)
	stripPublicTransportMetadata(expected)

	for _, field := range []string{"live_draft", "live_series", "official_results"} {
		require.Equal(t, expected[field], actual[field], "REST and realtime %s differ", field)
	}
}

func stripPublicTransportMetadata(value any) {
	switch typed := value.(type) {
	case map[string]any:
		delete(typed, "tournament_id")
		delete(typed, "projection_revision")
		for _, nested := range typed {
			stripPublicTransportMetadata(nested)
		}
	case []any:
		for _, nested := range typed {
			stripPublicTransportMetadata(nested)
		}
	}
}

func publicGameForSeries(t *testing.T, snapshot tournamentws.PublicSnapshot, seriesID uuid.UUID) tournamentws.PublicCurrentGame {
	t.Helper()
	for _, series := range snapshot.LiveSeries {
		if series.SeriesID == seriesID {
			require.NotNil(t, series.CurrentGame)
			return *series.CurrentGame
		}
	}
	t.Fatalf("public series %s not found", seriesID)
	return tournamentws.PublicCurrentGame{}
}

func insertPublicProjectionSubmission(
	ctx context.Context,
	t *testing.T,
	fixture resultAuditMigrationFixture,
	receivedAt time.Time,
) uuid.UUID {
	t.Helper()
	submissionID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		UPDATE game_attempts
		SET submission_event_sequence = submission_event_sequence + 1
		WHERE id = $1`, fixture.attemptID)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO submission_events (
			id, tournament_id, roster_id, series_id, attempt_id, assignment_id,
			participant_id, server_sequence, idempotency_key, status,
			payload_digest, intent_digest, submitted_at, received_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, 'accepted', $9, $10, $11, $11, $11)`,
		submissionID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		fixture.assignmentID,
		fixture.draft.participantIDs[0],
		uuid.New(),
		bytes.Repeat([]byte{20}, 32),
		bytes.Repeat([]byte{21}, 32),
		receivedAt,
	)
	require.NoError(t, err)
	return submissionID
}

func requirePublicDraft(t *testing.T, snapshot tournamentws.PublicSnapshot) tournamentws.PublicDraft {
	t.Helper()
	require.NotNil(t, snapshot.Draft)
	return *snapshot.Draft
}

func assertActiveDraft(t *testing.T, snapshot tournamentws.PublicSnapshot, format string, seriesID uuid.UUID, poolSize, actionCount int) {
	t.Helper()
	draft := requirePublicDraft(t, snapshot)
	require.Equal(t, seriesID, draft.SeriesID)
	require.Equal(t, format, draft.Format)
	require.Equal(t, "active", draft.State)
	require.Len(t, draft.Pool, poolSize)
	require.Len(t, draft.SelectedCategories, 0)
	require.Len(t, draft.Actions, actionCount)
	require.NotNil(t, draft.CurrentTurn)
	require.Equal(t, actionCount+1, *draft.CurrentTurn)
	require.NotNil(t, draft.CurrentAction)
	require.NotNil(t, draft.CurrentActorDisplayName)
	require.NotNil(t, draft.TurnDeadline)
}

func completePublicDraft(ctx context.Context, t *testing.T, fixture draftMigrationFixture, format string) {
	t.Helper()
	turns := 2
	selected := `["pwn"]`
	categories := []string{"web", "crypto"}
	if format == "bo3" {
		turns = 4
		selected = `["web","crypto","forensics"]`
		categories = []string{"web", "crypto", "forensics", "reverse"}
	}

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, "SET CONSTRAINTS ALL DEFERRED")
	require.NoError(t, err)
	previousRevisionID := fixture.initialRevisionID
	for turn := 1; turn <= turns; turn++ {
		revisionID := uuid.New()
		commandID := uuid.New()
		at := fixture.createdAt.Add(time.Duration(turn) * time.Second)
		revisionTurn := turn + 1
		state := "active"
		currentActor := fixture.participantIDs[turn%2]
		currentAction := "ban"
		turnDeadline := at.Add(15 * time.Second)
		if revisionTurn > 2 {
			currentAction = "pick"
		}
		var selectedCategories any = "[]"
		var decisionEvidenceID any
		var decisionPurpose any
		var decisionAlgorithm any
		var decisionInputs any
		var decisionSeed any
		var decisionResult any
		var decisionDigest any
		var decisionOwner any
		var decidedAt any
		if turn == turns {
			state = "completed"
			revisionTurn = turn
			currentActor = uuid.Nil
			currentAction = ""
			turnDeadline = time.Time{}
			selectedCategories = selected
			decisionEvidenceID = uuid.New()
			decisionPurpose = "category"
			decisionAlgorithm = "hmac-sha256-order-v1"
			decisionInputs = `["crypto","pwn"]`
			decisionSeed = bytes.Repeat([]byte{3}, 32)
			decisionResult = `["crypto","pwn"]`
			decisionDigest = bytes.Repeat([]byte{4}, 32)
			decisionOwner = fixture.draftID
			decidedAt = at.Add(-time.Second)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO draft_revisions (
				id, draft_id, series_id, roster_id, revision,
				previous_revision_id, command_id, service_epoch,
				state, turn_number, current_actor_id, current_action,
				absolute_deadline, selected_categories,
				decision_evidence_id, decision_purpose, decision_algorithm_version,
				decision_inputs, decision_seed, decision_result, decision_replay_digest,
				decision_owner_id, decided_at, created_at
			)
			VALUES (
				$1, $2, $3, $4, $5,
				$6, $7, $8,
				$9, $10, NULLIF($11, '00000000-0000-0000-0000-000000000000'::uuid), NULLIF($12, ''),
				NULLIF($13, '0001-01-01 00:00:00+00'::timestamptz), $14::jsonb,
				$15, $16, $17, $18::jsonb, $19, $20::jsonb, $21, $22, $23, $24
			)`,
			revisionID, fixture.draftID, fixture.seriesID, fixture.rosterID, turn+1,
			previousRevisionID, commandID, fixture.initialServiceEpoch,
			state, revisionTurn, currentActor, currentAction,
			turnDeadline, selectedCategories,
			decisionEvidenceID, decisionPurpose, decisionAlgorithm,
			decisionInputs, decisionSeed, decisionResult, decisionDigest,
			decisionOwner, decidedAt, at)
		require.NoError(t, err)

		action := "ban"
		if turn > 2 {
			action = "pick"
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO draft_actions (
				draft_id, result_revision_id, command_id, turn_number,
				actor_id, action, category, scheduled_deadline, occurred_at,
				automatic, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $9)`,
			fixture.draftID, revisionID, commandID, turn,
			fixture.participantIDs[(turn-1)%2], action, categories[turn-1], at.Add(time.Second), at, turn == turns)
		require.NoError(t, err)
		previousRevisionID = revisionID
	}
	require.NoError(t, tx.Commit(ctx))
}

func assertPublicRedaction(t *testing.T, body []byte) {
	t.Helper()
	var decoded any
	require.NoError(t, json.Unmarshal(body, &decoded))
	lower := strings.ToLower(string(body))
	for _, key := range []string{
		"task_id", "assignment_id", "snapshot_id", "attempt_id", "game_id",
		"hints", "flag", "task_url", "source_file_url", "seed", "evidence",
		"service_epoch", "presence_epoch", "execution_epoch", "actor_id", "command_id",
		"decision_evidence", "decision_inputs", "decision_result", "decision_seed",
	} {
		require.NotContains(t, lower, `"`+key+`"`)
	}
}
