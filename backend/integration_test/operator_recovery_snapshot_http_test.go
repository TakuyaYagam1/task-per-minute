//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestOperatorRecoverySnapshotThroughProductionREST(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(context.Background(), t) })

	fixture, plan := createCommittedReplayAuthorityFixture(ctx, t)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT reservation.id, snapshot.id
		FROM task_version_reservations AS reservation
		JOIN assignment_plan_edges AS edge ON edge.id = reservation.edge_id
		JOIN task_snapshots AS snapshot ON snapshot.reservation_id = reservation.id
		WHERE reservation.plan_id = $1 AND reservation.branch_id = $2 AND edge.position = 3`,
		plan.id, plan.branchID).Scan(&plan.reservationID, &plan.snapshotID))
	assignmentID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	require.NoError(t, insertReplayAuthorityAssignment(ctx, tx, fixture, plan, assignmentID))
	require.NoError(t, insertReplayAuthorityHead(ctx, tx, assignmentID))
	require.NoError(t, insertReplayAuthorityPool(ctx, tx, assignmentID))
	require.NoError(t, tx.Commit(ctx))
	seedAdditionalRecoveryRosterParticipants(ctx, t, fixture.rosterID)

	seedOperatorReserveExhaustion(ctx, t, fixture, plan, assignmentID)

	rest := newTournamentFlowRESTFixture(t)
	adminToken := rest.adminAccessToken(t)
	snapshot := tournamentAdminSnapshotThroughREST(t, rest, adminToken, fixture.tournamentID)
	require.Len(t, snapshot.RecoveryControls, 1)
	control := snapshot.RecoveryControls[0]
	require.Equal(t, api.OperatorRecoveryControlKind("reserve_exhausted"), control.Kind)
	require.Equal(t, fixture.seriesID, control.SeriesId)
	require.NotEqual(t, uuid.Nil, control.AssignmentId)
	require.NotEqual(t, uuid.Nil, control.SlotId)
	require.Len(t, control.Attempts, 1)
	require.Equal(t, int32(1), control.Attempts[0].AttemptNo)
	require.Equal(t, api.GameState("void"), control.Attempts[0].State)
	require.Equal(t, api.GameResultReason("no_solve"), *control.Attempts[0].ResultReason)
	require.NotNil(t, control.ReserveExhausted)
	require.Nil(t, control.Replay)
	require.NotEmpty(t, control.ReserveExhausted.Candidates)
	for _, candidate := range control.ReserveExhausted.Candidates {
		var category string
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT category
			FROM task_versions
			WHERE task_id = $1 AND version = $2`, candidate.TaskId, candidate.Version).Scan(&category))
		require.Equal(t, string(control.Category), category)
	}

	details := control.ReserveExhausted
	candidate := details.Candidates[0]
	var beforeAttempts, beforeAssignments, beforeOperatorReserves int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM game_attempts WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&beforeAttempts))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM assignments WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&beforeAssignments))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM operator_replay_reserves WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&beforeOperatorReserves))
	var beforeAuthorityRevision, beforeSeriesRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision FROM replay_reserve_authorities WHERE assignment_id = $1`,
		control.AssignmentId).Scan(&beforeAuthorityRevision))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision FROM series WHERE id = $1`, control.SeriesId).Scan(&beforeSeriesRevision))

	proposedSnapshotID := uuid.New()
	reserveRequest := api.OperatorReserveRequest{
		AssignmentAttemptId:           control.Attempts[0].Id,
		AssignmentId:                  control.AssignmentId,
		Confirmed:                     true,
		EvidenceId:                    uuid.New(),
		ExpectedArtifactRevision:      details.ExpectedArtifactRevision,
		ExpectedArtifactRevisionId:    details.ExpectedArtifactRevisionId,
		ExpectedAssignmentRevision:    details.ExpectedAssignmentRevision,
		ExpectedAuthorityRevision:     control.ExpectedAuthorityRevision,
		ExpectedCategoryRevision:      details.ExpectedCategoryRevision,
		ExpectedCategoryRevisionId:    details.ExpectedCategoryRevisionId,
		ExpectedExhaustionCommandId:   details.ExpectedExhaustionCommandId,
		ExpectedHistoryRevision:       details.ExpectedHistoryRevision,
		ExpectedHistoryRevisionId:     details.ExpectedHistoryRevisionId,
		ExpectedPoolRevision:          details.ExpectedPoolRevision,
		ExpectedPoolRevisionId:        details.ExpectedPoolRevisionId,
		ExpectedReservationRevision:   details.ExpectedReservationRevision,
		ExpectedReservationRevisionId: details.ExpectedReservationRevisionId,
		ExpectedSnapshotId:            details.ExpectedSnapshotId,
		OldWaveId:                     control.OldWaveId,
		ProposedSnapshotId:            proposedSnapshotID,
		ProposedTaskId:                candidate.TaskId,
		ProposedVersion:               candidate.Version,
		Reason:                        "operator reserve recovery",
		SeriesId:                      control.SeriesId,
		SlotId:                        control.SlotId,
		TournamentId:                  fixture.tournamentID,
	}
	body, err := json.Marshal(reserveRequest)
	require.NoError(t, err)
	reserveCommandID := uuid.New()
	reservePath := "/api/v1/admin/tournaments/" + fixture.tournamentID.String() +
		"/series/" + control.SeriesId.String() + "/assignments/" + control.AssignmentId.String() + "/operator-reserves"
	request, response := doTournamentFlowJSON(
		t, rest, http.MethodPost, reservePath, string(body), adminSession(adminToken), reserveCommandID, "",
	)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	rest.validateResponse(t, request, response)

	var afterAttempts, afterAssignments, afterOperatorReserves int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM game_attempts WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&afterAttempts))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM assignments WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&afterAssignments))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM operator_replay_reserves WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&afterOperatorReserves))
	require.Equal(t, beforeAttempts, afterAttempts)
	require.Equal(t, beforeAssignments, afterAssignments)
	require.Equal(t, beforeOperatorReserves+1, afterOperatorReserves)

	var storedTaskID, storedSnapshotID uuid.UUID
	var storedVersion int32
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT proposed_task_id, proposed_version, proposed_snapshot_id
		FROM operator_replay_reserves WHERE command_id = $1`, reserveCommandID).Scan(
		&storedTaskID, &storedVersion, &storedSnapshotID))
	require.Equal(t, candidate.TaskId, storedTaskID)
	require.Equal(t, candidate.Version, storedVersion)
	require.Equal(t, proposedSnapshotID, storedSnapshotID)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id FROM task_snapshots
		WHERE id = $1 AND task_id = $2 AND task_version = $3 AND category = $4`,
		proposedSnapshotID, candidate.TaskId, candidate.Version, control.Category).Scan(&storedSnapshotID))
	require.Equal(t, proposedSnapshotID, storedSnapshotID)
	var authorityRevision int64
	var currentSnapshotID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision, active_snapshot_id FROM replay_reserve_authorities WHERE assignment_id = $1`,
		control.AssignmentId).Scan(&authorityRevision, &currentSnapshotID))
	require.Equal(t, beforeAuthorityRevision+1, authorityRevision)
	require.Equal(t, details.CurrentSnapshotId, currentSnapshotID)
	var seriesState string
	var seriesRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, revision FROM series WHERE id = $1`, control.SeriesId).Scan(&seriesState, &seriesRevision))
	require.Equal(t, string(domain.SeriesStateReplayRequired), seriesState)
	require.Equal(t, beforeSeriesRevision+1, seriesRevision)

	staleCommandID := uuid.New()
	staleRequest, staleResponse := doTournamentFlowJSON(
		t, rest, http.MethodPost, reservePath, string(body), adminSession(adminToken), staleCommandID, "",
	)
	require.Equal(t, http.StatusConflict, staleResponse.Code, staleResponse.Body.String())
	rest.validateResponse(t, staleRequest, staleResponse)
	var finalOperatorReserves, finalAttempts, finalAssignments int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM operator_replay_reserves WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&finalOperatorReserves))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM game_attempts WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&finalAttempts))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM assignments WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&finalAssignments))
	require.Equal(t, afterOperatorReserves, finalOperatorReserves)
	require.Equal(t, afterAttempts, finalAttempts)
	require.Equal(t, afterAssignments, finalAssignments)

	replaySnapshot := tournamentAdminSnapshotThroughREST(t, rest, adminToken, fixture.tournamentID)
	require.Len(t, replaySnapshot.RecoveryControls, 1)
	replayControl := replaySnapshot.RecoveryControls[0]
	require.Equal(t, api.OperatorRecoveryControlKind("replay"), replayControl.Kind)
	require.Equal(t, control.AssignmentId, replayControl.AssignmentId)
	require.Equal(t, control.SeriesId, replayControl.SeriesId)
	require.Equal(t, control.SlotId, replayControl.SlotId)
	require.Equal(t, control.Attempts[0].Id, replayControl.Attempts[0].Id)
	require.NotNil(t, replayControl.Replay)
	require.True(t, replayControl.Replay.Available)
	require.NotEqual(t, uuid.Nil, replayControl.Replay.ExpectedClosureRevisionId)

	var beforeReplayReplacements int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM replay_replacements WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&beforeReplayReplacements))

	replayCommandID := uuid.NewSHA1(reserveCommandID, []byte("operator-replay-command"))
	replacementAttemptID := uuid.NewSHA1(reserveCommandID, []byte("operator-replay-assignment-attempt"))
	replacementGameID := uuid.NewSHA1(reserveCommandID, []byte("operator-replay-game"))
	replacementWaveID := uuid.NewSHA1(reserveCommandID, []byte("operator-replay-wave"))
	replacementWaveRevisionID := uuid.NewSHA1(reserveCommandID, []byte("operator-replay-wave-revision"))
	readyWindowID := uuid.NewSHA1(reserveCommandID, []byte("operator-replay-ready-window"))
	readyWindowRevisionID := uuid.NewSHA1(reserveCommandID, []byte("operator-replay-ready-window-revision"))
	replayRequest := api.OperatorReplayRequest{
		AssignmentAttemptId:       replacementAttemptID,
		AssignmentId:              replayControl.AssignmentId,
		Confirmed:                 true,
		ExpectedAuthorityRevision: replayControl.ExpectedAuthorityRevision,
		ExpectedClosureRevisionId: replayControl.Replay.ExpectedClosureRevisionId,
		FailedGameId:              replayControl.Attempts[0].Id,
		OldWaveId:                 replayControl.OldWaveId,
		ReadyWindowId:             readyWindowID,
		ReadyWindowRevisionId:     readyWindowRevisionID,
		Reason:                    "operator replay recovery",
		ReplacementGameId:         replacementGameID,
		ReplacementWaveId:         replacementWaveID,
		ReplacementWaveRevisionId: replacementWaveRevisionID,
		SeriesId:                  replayControl.SeriesId,
		SlotId:                    replayControl.SlotId,
		TournamentId:              fixture.tournamentID,
	}
	replayBody, err := json.Marshal(replayRequest)
	require.NoError(t, err)
	replayPath := "/api/v1/admin/tournaments/" + fixture.tournamentID.String() +
		"/series/" + replayControl.SeriesId.String() + "/games/" + replayControl.Attempts[0].Id.String() + "/replays"
	replayHTTPRequest, replayHTTPResponse := doTournamentFlowJSON(
		t, rest, http.MethodPost, replayPath, string(replayBody), adminSession(adminToken), replayCommandID, "",
	)
	require.Equal(t, http.StatusNoContent, replayHTTPResponse.Code, replayHTTPResponse.Body.String())
	rest.validateResponse(t, replayHTTPRequest, replayHTTPResponse)

	var afterReplayAttempts, afterReplayAssignments, afterReplayReplacements int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM game_attempts WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&afterReplayAttempts))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM assignments WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&afterReplayAssignments))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM replay_replacements WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&afterReplayReplacements))
	require.Equal(t, finalAttempts+1, afterReplayAttempts)
	require.Equal(t, finalAssignments+1, afterReplayAssignments)
	require.Equal(t, beforeReplayReplacements+1, afterReplayReplacements)

	var replacementSlotID uuid.UUID
	var replacementAttemptNo int32
	var replacementState string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT slot_id, attempt_number, state
		FROM game_attempts
		WHERE id = $1 AND series_id = $2 AND roster_id = $3`,
		replacementGameID, fixture.seriesID, fixture.rosterID).Scan(
		&replacementSlotID, &replacementAttemptNo, &replacementState))
	require.Equal(t, replayControl.SlotId, replacementSlotID)
	require.Equal(t, int32(2), replacementAttemptNo)
	require.Equal(t, string(domain.GameStatePlanned), replacementState)

	var replacementSnapshotID, replacementTaskID uuid.UUID
	var replacementVersion int32
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT snapshot_id, task_id, task_version
		FROM assignments
		WHERE id = $1 AND attempt_id = $2 AND series_id = $3 AND roster_id = $4`,
		replacementAttemptID, replacementGameID, fixture.seriesID, fixture.rosterID).Scan(
		&replacementSnapshotID, &replacementTaskID, &replacementVersion))
	require.Equal(t, proposedSnapshotID, replacementSnapshotID)
	require.Equal(t, candidate.TaskId, replacementTaskID)
	require.Equal(t, candidate.Version, replacementVersion)
	var replacementCategory string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT category FROM task_snapshots WHERE id = $1 AND task_id = $2 AND task_version = $3`,
		replacementSnapshotID, replacementTaskID, replacementVersion).Scan(&replacementCategory))
	require.Equal(t, string(replayControl.Category), replacementCategory)

	var storedReplacementAttemptID, storedReplacementGameID, storedReplacementWaveID uuid.UUID
	var storedReplacementWaveRevisionID, storedReadyWindowID, storedReadyWindowRevisionID uuid.UUID
	var storedReplacementSnapshotID uuid.UUID
	var storedReservePosition int16
	var sourceSeriesRevision, resultingSeriesRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT replacement_assignment_attempt_id, replacement_game_id, replacement_wave_id,
			replacement_wave_revision_id, ready_window_id, ready_window_revision_id,
			snapshot_id, reserve_position, source_series_revision, resulting_series_revision
		FROM replay_replacements WHERE command_id = $1`, replayCommandID).Scan(
		&storedReplacementAttemptID, &storedReplacementGameID, &storedReplacementWaveID,
		&storedReplacementWaveRevisionID, &storedReadyWindowID, &storedReadyWindowRevisionID,
		&storedReplacementSnapshotID, &storedReservePosition, &sourceSeriesRevision, &resultingSeriesRevision))
	require.Equal(t, replacementAttemptID, storedReplacementAttemptID)
	require.Equal(t, replacementGameID, storedReplacementGameID)
	require.Equal(t, replacementWaveID, storedReplacementWaveID)
	require.Equal(t, replacementWaveRevisionID, storedReplacementWaveRevisionID)
	require.Equal(t, readyWindowID, storedReadyWindowID)
	require.Equal(t, readyWindowRevisionID, storedReadyWindowRevisionID)
	require.Equal(t, proposedSnapshotID, storedReplacementSnapshotID)
	require.Equal(t, int16(4), storedReservePosition)
	require.Equal(t, replayControl.ExpectedAuthorityRevision, sourceSeriesRevision)
	require.Equal(t, replayControl.ExpectedAuthorityRevision+1, resultingSeriesRevision)

	var finalSeriesState string
	var finalSeriesRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, revision FROM series WHERE id = $1`, replayControl.SeriesId).Scan(
		&finalSeriesState, &finalSeriesRevision))
	require.Equal(t, string(domain.SeriesStateReady), finalSeriesState)
	require.Equal(t, replayControl.ExpectedAuthorityRevision+1, finalSeriesRevision)
	var replacementReadiness, replacementReady, replacementReadyAt int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE ready), COUNT(*) FILTER (WHERE ready_at IS NOT NULL)
		FROM wave_readiness
		WHERE wave_id = $1`, replacementWaveID).Scan(
		&replacementReadiness, &replacementReady, &replacementReadyAt))
	require.Equal(t, 2, replacementReadiness)
	require.Zero(t, replacementReady, "replay must not inherit readiness from the failed attempt")
	require.Zero(t, replacementReadyAt, "replay must open a fresh readiness window")

	retryReplayRequest, retryReplayResponse := doTournamentFlowJSON(
		t, rest, http.MethodPost, replayPath, string(replayBody), adminSession(adminToken), replayCommandID, "",
	)
	require.Equal(t, http.StatusNoContent, retryReplayResponse.Code, retryReplayResponse.Body.String())
	rest.validateResponse(t, retryReplayRequest, retryReplayResponse)

	staleReplayCommandID := uuid.NewSHA1(reserveCommandID, []byte("operator-replay-stale-command"))
	staleReplayRequest, staleReplayResponse := doTournamentFlowJSON(
		t, rest, http.MethodPost, replayPath, string(replayBody), adminSession(adminToken), staleReplayCommandID, "",
	)
	require.Equal(t, http.StatusConflict, staleReplayResponse.Code, staleReplayResponse.Body.String())
	rest.validateResponse(t, staleReplayRequest, staleReplayResponse)
	var finalReplayAttempts, finalReplayAssignments, finalReplayReplacements int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM game_attempts WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&finalReplayAttempts))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM assignments WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&finalReplayAssignments))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM replay_replacements WHERE series_id = $1 AND roster_id = $2`,
		fixture.seriesID, fixture.rosterID).Scan(&finalReplayReplacements))
	require.Equal(t, afterReplayAttempts, finalReplayAttempts)
	require.Equal(t, afterReplayAssignments, finalReplayAssignments)
	require.Equal(t, afterReplayReplacements, finalReplayReplacements)
}
func seedAdditionalRecoveryRosterParticipants(
	ctx context.Context,
	t *testing.T,
	rosterID uuid.UUID,
) {
	t.Helper()
	players := createMigrationPlayers(ctx, t, 2)
	for index, playerID := range players {
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO participants (roster_id, player_id, seed, attendance)
			VALUES ($1, $2, $3, 'checked_in')`, rosterID, playerID, index+3)
		require.NoError(t, err)
	}
}

func seedOperatorReserveExhaustion(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
	plan replayAuthorityPlanFixture,
	assignmentID uuid.UUID,
) {
	t.Helper()
	var slotID, snapshotID uuid.UUID
	var category string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.slot_id, assignment.snapshot_id, slot.category
		FROM assignments AS assignment
		JOIN game_attempts AS attempt ON attempt.id = assignment.attempt_id
		JOIN game_slots AS slot ON slot.id = attempt.slot_id
		WHERE assignment.id = $1`, assignmentID).Scan(&slotID, &snapshotID, &category))

	participants := make([]uuid.UUID, 2)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT first_participant_id, second_participant_id
		FROM series
		WHERE id = $1 AND roster_id = $2`, fixture.seriesID, fixture.rosterID).Scan(
		&participants[0], &participants[1],
	))
	var createdAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx,
		`SELECT GREATEST(tournament.created_at, roster.created_at, series.created_at) + INTERVAL '1 microsecond'
		 FROM tournaments AS tournament
		 JOIN rosters AS roster ON roster.tournament_id = tournament.id
		 JOIN series ON series.roster_id = roster.id AND series.tournament_id = tournament.id
		 WHERE tournament.id = $1 AND roster.id = $2 AND series.id = $3`,
		fixture.tournamentID, fixture.rosterID, fixture.seriesID).Scan(&createdAt))
	createdAt = createdAt.Round(0).UTC()
	startedAt := createdAt.Add(time.Microsecond)
	failedAt := startedAt.Add(time.Microsecond)
	closedAt := failedAt.Add(time.Microsecond)
	oldWaveID := uuid.New()
	closureRevisionID := uuid.New()
	readyWindowID := uuid.New()
	readyWindowRevisionID := uuid.New()
	routeID := uuid.New()
	resultEventID := uuid.New()
	resultRevisionID := uuid.New()
	scoreRevisionID := uuid.New()
	auditEventID := uuid.New()
	projectionEvidenceID := uuid.New()
	outboxEventID := uuid.New()
	resultCommandID := uuid.New()
	outboxIdempotencyKey := uuid.New()
	commitIdempotencyKey := uuid.New()
	exhaustionID := uuid.New()
	var previousProjectionID, previousCutoffID uuid.UUID
	var previousProjectionRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id, revision_number, cutoff_id
		FROM projection_revisions
		WHERE tournament_id = $1 AND roster_id = $2
		ORDER BY revision_number DESC
		LIMIT 1`, fixture.tournamentID, fixture.rosterID).Scan(
		&previousProjectionID, &previousProjectionRevision, &previousCutoffID))
	projectionID := uuid.New()
	projectionRevision := previousProjectionRevision + 1
	cutoffID := uuid.New()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO projection_cutoffs (
			id, tournament_id, roster_id, sequence_number, previous_cutoff_id,
			source_kind, reason, cutoff_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 'operator_rebuild', 'operator recovery snapshot', $6, $6)`,
		cutoffID, fixture.tournamentID, fixture.rosterID, projectionRevision,
		previousCutoffID, startedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO projection_revisions (
			id, tournament_id, roster_id, revision_number, previous_revision_id,
			cutoff_id, state, published_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'published', $7, $7)`,
		projectionID, fixture.tournamentID, fixture.rosterID, projectionRevision,
		previousProjectionID, cutoffID, startedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE tournaments
		SET state = 'swiss', revision = revision + 1, started_at = $2, updated_at = $2
		WHERE id = $1`, fixture.tournamentID, startedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE rosters
		SET locked_at = $2, execution_started_at = $2, revision = revision + 1, updated_at = $2
		WHERE id = $1`, fixture.rosterID, startedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE game_attempts
		SET result_event_sequence = result_event_sequence + 1
		WHERE id = $1`, plan.attemptID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO result_events (
			id, tournament_id, roster_id, series_id, attempt_id,
			server_sequence, idempotency_key, result_state, result_reason,
			occurred_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 1, $6, 'void', 'no_solve', $7, $7)`,
		resultEventID, fixture.tournamentID, fixture.rosterID, fixture.seriesID,
		plan.attemptID, uuid.New(), failedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO official_result_revisions (
			id, tournament_id, roster_id, entity_kind, entity_id, series_id,
			game_attempt_id, result_event_id, revision_number, command_id,
			actor_kind, source_projection_revision_id, source_projection_revision,
			result_state, result_reason, created_at
		)
		VALUES ($1, $2, $3, 'game_attempt', $4, $5, $4, $6, 1, $7,
			'server', $8, $9, 'void', 'no_solve', $10)`,
		resultRevisionID, fixture.tournamentID, fixture.rosterID, plan.attemptID,
		fixture.seriesID, resultEventID, resultCommandID, projectionID, projectionRevision, failedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO series_score_revisions (
			id, tournament_id, roster_id, series_id, result_event_id,
			previous_revision_id, revision_number, operation, command_id,
			actor_kind, command_attempt_id, source_projection_revision_id,
			source_projection_revision, first_participant_wins, second_participant_wins,
			created_at
		)
		SELECT $1, $2, $3, $4, $5, heads.current_revision_id, 2, 'append_attempt',
			$6, 'server', $7, $8, $9, series.first_participant_wins,
			series.second_participant_wins, $10
		FROM series_score_heads AS heads
		JOIN series ON series.id = heads.series_id
		WHERE heads.series_id = $4 AND heads.roster_id = $3`,
		scoreRevisionID, fixture.tournamentID, fixture.rosterID, fixture.seriesID,
		resultEventID, uuid.New(), plan.attemptID, projectionID, projectionRevision, failedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO series_score_revision_attempts (
			score_revision_id, tournament_id, roster_id, series_id, position,
			slot_id, slot_position, game_attempt_id, attempt_number,
			game_result_revision_id, result_event_id, result_state, result_reason,
			occurred_at, created_at
		)
		VALUES ($1, $2, $3, $4, 1, $5, 1, $6, 1, $7, $8,
			'void', 'no_solve', $9, $9)`,
		scoreRevisionID, fixture.tournamentID, fixture.rosterID, fixture.seriesID,
		slotID, plan.attemptID, resultRevisionID, resultEventID, failedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO result_projection_evidence (
			id, tournament_id, roster_id, series_id, result_event_id,
			artifact_kinds, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, '["game_result", "series_score"]'::JSONB,
			$6, $7)`, projectionEvidenceID, fixture.tournamentID, fixture.rosterID,
		fixture.seriesID, resultEventID, bytes.Repeat([]byte{2}, 32), failedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_events (
			id, tournament_id, roster_id, series_id, result_event_id,
			actor_kind, action, payload, occurred_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 'server', 'tournament.result.committed',
			'{"reason":"no_solve"}'::JSONB, $6, $6)`, auditEventID, fixture.tournamentID,
		fixture.rosterID, fixture.seriesID, resultEventID, failedAt)
	require.NoError(t, err)
	_, err = sqlc.New(tx).CreateResultOutboxEvent(ctx, sqlc.CreateResultOutboxEventParams{
		ID: outboxEventID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ProjectionRevisionID: projectionID, ProjectionRevision: projectionRevision,
		SeriesID: fixture.seriesID, ResultEventID: resultEventID,
		ProjectionEvidenceID: projectionEvidenceID, IdempotencyKey: outboxIdempotencyKey,
		Topic: "tournament.result.committed", Payload: []byte(`{"event":"result.committed"}`),
		CreatedAt: pgtype.Timestamptz{Time: failedAt, Valid: true},
	})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO result_commits (
			tournament_id, roster_id, series_id, attempt_id, result_event_id,
			game_result_revision_id, series_score_revision_id, audit_event_id,
			outbox_event_id, projection_evidence_id, idempotency_key, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		fixture.tournamentID, fixture.rosterID, fixture.seriesID, plan.attemptID,
		resultEventID, resultRevisionID, scoreRevisionID, auditEventID, outboxEventID,
		projectionEvidenceID, commitIdempotencyKey, failedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO official_result_heads (
			entity_kind, entity_id, series_id, roster_id, game_attempt_id,
			current_revision_id, updated_at
		)
		VALUES ('game_attempt', $1, $2, $3, $1, $4, $5)`,
		plan.attemptID, fixture.seriesID, fixture.rosterID, resultRevisionID, failedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE series_score_heads
		SET current_revision_id = $2, revision = revision + 1, updated_at = $3
		WHERE series_id = $1 AND roster_id = $4`, fixture.seriesID, scoreRevisionID, failedAt, fixture.rosterID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE game_attempts
		SET state = 'void', result_reason = 'no_solve', result_revision_id = $2,
			revision = revision + 1, updated_at = $3, finished_at = $3
		WHERE id = $1`, plan.attemptID, resultRevisionID, failedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO waves (
			id, tournament_id, roster_id, revision_id, revision, state,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, 1, 'planned', $5, $5)`,
		oldWaveID, fixture.tournamentID, fixture.rosterID, closureRevisionID, createdAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO ready_windows (
			id, wave_id, roster_id, revision_id, state, opened_at, deadline, created_at
		)
		VALUES ($1, $2, $3, $4, 'open', $5, $6, $5)`,
		readyWindowID, oldWaveID, fixture.rosterID, readyWindowRevisionID, startedAt, startedAt.Add(time.Minute))
	require.NoError(t, err)
	for _, participantID := range participants {
		_, err = tx.Exec(ctx, `
			INSERT INTO wave_members (wave_id, roster_id, participant_id, created_at)
			VALUES ($1, $2, $3, $4)`, oldWaveID, fixture.rosterID, participantID, createdAt)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO wave_readiness (wave_id, roster_id, participant_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $4)`, oldWaveID, fixture.rosterID, participantID, createdAt)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO wave_series (wave_id, tournament_id, roster_id, series_id, created_at)
		VALUES ($1, $2, $3, $4, $5)`, oldWaveID, fixture.tournamentID, fixture.rosterID, fixture.seriesID, createdAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE wave_readiness
		SET ready_window_id = $2, ready = true, ready_at = $3,
			revision = revision + 1, updated_at = $3
		WHERE wave_id = $1`, oldWaveID, readyWindowID, startedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE ready_windows
		SET state = 'consumed', consumed_at = $2
		WHERE id = $1`, readyWindowID, startedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE waves
		SET state = 'completed', revision = 2, started_at = $2,
			closed_at = $3, updated_at = $3
		WHERE id = $1`, oldWaveID, startedAt, closedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO wave_member_routes (
			id, tournament_id, roster_id, wave_id, series_id, slot_id,
			game_attempt_id, category, routed_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		routeID, fixture.tournamentID, fixture.rosterID, oldWaveID, fixture.seriesID,
		slotID, plan.attemptID, category, failedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE series
		SET state = 'replay_required', current_score_revision_id = $2, updated_at = $3
		WHERE id = $1`, fixture.seriesID, scoreRevisionID, failedAt)
	require.NoError(t, err)
	document, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"payload": map[string]any{
			"command_id": exhaustionID,
			"scope": map[string]any{
				"tournament_id": fixture.tournamentID,
				"old_wave_id":   oldWaveID,
				"series_id":     fixture.seriesID,
				"slot_id":       slotID,
				"assignment_id": assignmentID,
			},
			"expected_authority_revision":       1,
			"failed_attempt_command_id":         resultCommandID,
			"failed_attempt_authority_revision": 1,
			"failure_class":                     "no_solve",
			"game_result_ordinal":               1,
			"score_ordinal":                     2,
			"route_id":                          routeID,
			"audit_event_id":                    auditEventID,
			"outbox_event_id":                   outboxEventID,
			"projection_revision_id":            projectionID,
			"source_projection_revision":        projectionRevision,
			"closure_command_id":                commitIdempotencyKey,
			"closure_authority_revision":        1,
			"previous_closure_revision_id":      uuid.New(),
			"assignment_attempt_id":             plan.attemptID,
			"failed_game_id":                    plan.attemptID,
			"closure_revision_id":               closureRevisionID,
			"active_snapshot_id":                snapshotID,
			"reserve_position":                  domain.AssignmentReserveCount + 1,
			"category":                          category,
			"source_series_revision":            1,
			"resulting_series_revision":         2,
			"paused_at":                         closedAt,
		},
	})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO replay_reserve_exhaustions (
			command_id, tournament_id, roster_id, old_wave_id, series_id, slot_id,
			assignment_id, assignment_attempt_id, failed_game_id, closure_revision_id,
			active_snapshot_id, from_snapshot_id, reserve_position, category,
			source_series_revision, resulting_series_revision, request_digest,
			authority_document, record_document, paused_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $9, $10, $10, 3, $11,
			1, 2, $12, $13::JSONB, $13::JSONB, $14, $14)`,
		exhaustionID, fixture.tournamentID, fixture.rosterID, oldWaveID, fixture.seriesID,
		slotID, assignmentID, plan.attemptID, closureRevisionID, snapshotID, category,
		bytes.Repeat([]byte{1}, 32), document, closedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE series
		SET state = 'technical_pause', revision = 2, updated_at = $2
		WHERE id = $1`, fixture.seriesID, closedAt)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}
