//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type participantHintTimingContent struct {
	flag      string
	sourceURL string
	hints     [domain.TaskHintCount]string
}

func TestParticipantHintTimingThroughProductionHandlers(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	content := prepareParticipantHintTimingContent(ctx, t)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	selection := getTournamentContentThroughREST(t, fixture, adminToken)
	players := joinTournamentFlowPlayers(t, fixture, 4)
	created := createTournamentWithRosterSizeThroughREST(
		t, fixture, adminToken, selection.ContentRevision, "participant_hint_timing", 4,
	)
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
	preflight := runTournamentRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
	lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
	startSwissThroughREST(t, fixture, adminToken, created.Id)

	round := configureProductionSwissPairingsThroughREST(
		t, fixture, adminToken, created.Id,
		tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id).NextCursor.ProjectionRevision,
		1,
	)
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	wave := findProductionSwissWave(t, snapshot, round)
	wave = controlProductionWaveThroughREST(
		t, fixture, adminToken, created.Id, wave.Id,
		snapshot.NextCursor.ProjectionRevision, api.WaveControlRequestActionOpenReadyWindow,
	)
	require.Equal(t, api.WaveStateReadyWindowOpen, wave.State)

	playersByParticipant := productionPlayersByParticipant(t, roster, mapTournamentFlowPlayers(players))
	for _, member := range wave.Members {
		player, ok := playersByParticipant[member.ParticipantId]
		require.True(t, ok, "missing player for participant %s", member.ParticipantId)
		participant := participantSnapshotThroughREST(t, fixture, created.Id, player)
		body, err := json.Marshal(api.ParticipantReadyRequest{
			ExpectedProjectionRevision: participant.NextCursor.ProjectionRevision,
			Ready:                      true,
		})
		require.NoError(t, err)
		path := "/api/v1/tournaments/" + created.Id.String() +
			"/participant/waves/" + wave.Id.String() + "/ready"
		commandID := uuid.New()
		req, resp := doTournamentFlowJSON(
			t, fixture, http.MethodPost, path, string(body),
			cookieSession(player.session.String()), commandID, player.csrf,
		)
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		fixture.validateResponse(t, req, resp)
		ready := decodeJSON[api.ReadinessEvent](t, resp)
		require.Equal(t, wave.Id, ready.WaveId)

		var occurredAt, createdAt time.Time
		err = sharedPool.QueryRow(ctx, `
			SELECT occurred_at, created_at
			FROM readiness_events
			WHERE command_id = $1`, commandID).Scan(&occurredAt, &createdAt)
		require.NoError(t, err)
		require.False(t, occurredAt.After(createdAt),
			"readiness event occurred_at must not be after created_at")
		require.True(t, occurredAt.Equal(createdAt),
			"readiness event timestamps must use the same authoritative instant")
	}

	snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	wave = findProductionWaveByID(t, snapshot, wave.Id)
	wave = controlProductionWaveThroughREST(
		t, fixture, adminToken, created.Id, wave.Id,
		snapshot.NextCursor.ProjectionRevision, api.WaveControlRequestActionStart,
	)
	require.Equal(t, api.WaveStateActive, wave.State)
	require.NotNil(t, wave.StartedAt)

	selectedParticipantID := wave.Members[0].ParticipantId
	selectedPlayer, ok := playersByParticipant[selectedParticipantID]
	require.True(t, ok, "missing selected player for participant %s", selectedParticipantID)
	getSnapshot := func() (api.ParticipantRecoverySnapshot, string) {
		t.Helper()
		path := "/api/v1/tournaments/" + created.Id.String() + "/participant/snapshot"
		req, resp := doTournamentFlowJSON(
			t, fixture, http.MethodGet, path, "",
			cookieSession(selectedPlayer.session.String()), uuid.Nil, "",
		)
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		fixture.validateResponse(t, req, resp)
		return decodeJSON[api.ParticipantRecoverySnapshot](t, resp), resp.Body.String()
	}
	getAssignment := func(assignmentID uuid.UUID) (api.ParticipantAssignmentResponse, string) {
		t.Helper()
		path := "/api/v1/tournaments/" + created.Id.String() +
			"/participant/assignments/" + assignmentID.String()
		req, resp := doTournamentFlowJSON(
			t, fixture, http.MethodGet, path, "",
			cookieSession(selectedPlayer.session.String()), uuid.Nil, "",
		)
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		fixture.validateResponse(t, req, resp)
		return decodeJSON[api.ParticipantAssignmentResponse](t, resp), resp.Body.String()
	}
	assertParticipantHintRedaction := func(serialized string, hints ...string) {
		t.Helper()
		for _, marker := range append([]string{content.flag, content.sourceURL}, hints...) {
			require.NotContains(t, serialized, marker)
		}
	}

	participant, serialized := getSnapshot()
	require.NotNil(t, participant.Assignment)
	require.NotNil(t, participant.Wave)
	require.Equal(t, api.WaveStateActive, participant.Wave.State)
	require.Equal(t, selectedParticipantID, participant.Assignment.Receipt.ParticipantId)
	require.Empty(t, participant.Assignment.ActiveSnapshot.Hints)
	assertParticipantHintRedaction(serialized, content.hints[1], content.hints[2])

	var assignmentState string
	err := sharedPool.QueryRow(ctx, `SELECT state FROM assignments WHERE id = $1`, participant.Assignment.Id).
		Scan(&assignmentState)
	require.NoError(t, err)
	require.Equal(t, "active", assignmentState)

	assignmentID := participant.Assignment.Id
	assignment, serialized := getAssignment(assignmentID)
	require.Equal(t, assignmentID, assignment.Assignment.Id)
	require.Equal(t, selectedParticipantID, assignment.Assignment.Receipt.ParticipantId)
	require.Empty(t, assignment.Assignment.ActiveSnapshot.Hints)
	assertParticipantHintRedaction(serialized, content.hints[1], content.hints[2])

	firstBoundary := wave.StartedAt.Add(45 * time.Second)
	require.Eventually(t, func() bool {
		var databaseNow time.Time
		err := sharedPool.QueryRow(context.Background(), `SELECT clock_timestamp()`).Scan(&databaseNow)
		if err != nil {
			t.Logf("authoritative time query failed: %v", err)
			return false
		}
		if databaseNow.Before(firstBoundary) {
			return false
		}

		participant, participantSerialized := getSnapshot()
		assignment, assignmentSerialized := getAssignment(assignmentID)
		require.NotNil(t, participant.Assignment)
		require.Equal(t, assignmentID, participant.Assignment.Id)
		require.Equal(t, assignmentID, assignment.Assignment.Id)
		assertParticipantHintRedaction(participantSerialized, content.hints[1], content.hints[2])
		assertParticipantHintRedaction(assignmentSerialized, content.hints[1], content.hints[2])
		return len(participant.Assignment.ActiveSnapshot.Hints) == 1 &&
			participant.Assignment.ActiveSnapshot.Hints[0] == content.hints[0] &&
			len(assignment.Assignment.ActiveSnapshot.Hints) == 1 &&
			assignment.Assignment.ActiveSnapshot.Hints[0] == content.hints[0]
	}, 55*time.Second, 10*time.Millisecond)
}

func prepareParticipantHintTimingContent(ctx context.Context, t testing.TB) participantHintTimingContent {
	t.Helper()
	content := participantHintTimingContent{
		flag:      "participant-hint-timing-flag",
		sourceURL: "https://internal.example.test/participant-hint-timing-source.zip",
		hints: [domain.TaskHintCount]string{
			"participant-hint-timing-first",
			"participant-hint-timing-second",
			"participant-hint-timing-third",
		},
	}
	chainSize := domain.AssignmentReserveCount + 1
	for _, category := range []struct {
		name  string
		count int
	}{
		{name: "web", count: 27},
		{name: "crypto", count: 27},
		{name: "forensics", count: chainSize},
		{name: "reverse", count: 27},
		{name: "pwn", count: chainSize},
	} {
		for range category.count {
			title := "participant_hint_timing_" + category.name + "_" + uuid.NewString()[:8]
			_, err := sharedPool.Exec(ctx, `
				INSERT INTO tasks (
					title, description, category, difficulty, time_limit, flag,
					hint_1, hint_2, hint_3, source_file_url, kind
				)
			VALUES ($1, 'participant hint timing fixture', $2, 'easy', 180, $3,
					$4, $5, $6, $7, 'normal')`,
				title, category.name, content.flag,
				content.hints[0], content.hints[1], content.hints[2], content.sourceURL,
			)
			require.NoError(t, err)
		}
	}
	for range domain.TournamentMinParticipants / 2 * chainSize {
		title := "participant_hint_timing_golden_" + uuid.NewString()[:8]
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO tasks (
				title, description, category, difficulty, time_limit, flag,
				hint_1, hint_2, hint_3, source_file_url, kind
			)
			VALUES ($1, 'participant hint timing Golden fixture', 'web', 'easy', 180, $2,
				$3, $4, $5, $6, 'golden')`,
			title, content.flag, content.hints[0], content.hints[1], content.hints[2], content.sourceURL,
		)
		require.NoError(t, err)
	}

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT version.task_id, version.version, 1, true, 'content_validation'
		FROM task_versions AS version
		WHERE NOT EXISTS (
			SELECT 1
			FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = version.task_id
				AND attestation.task_version = version.version
				AND attestation.revision = 1
		)`)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `SELECT publish_task_pool_heads()`)
	require.NoError(t, err)
	return content
}

func mapTournamentFlowPlayers(players []tournamentFlowPlayer) map[uuid.UUID]tournamentFlowPlayer {
	result := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
	for _, player := range players {
		result[player.id] = player
	}
	return result
}
