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
	recoveryterminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

type readinessRecoveryCase struct {
	name                  string
	readyParticipantCount int
	sameSeries            bool
	expectedSeriesStates  []string
	expectReadyWave       bool
}

type readinessRecoveryClock struct{ now time.Time }

func (clock readinessRecoveryClock) Now() time.Time { return clock.now }

func TestReadyWindowRecoveryScenarios(t *testing.T) {
	cases := []readinessRecoveryCase{
		{
			name:                  "none_ready",
			readyParticipantCount: 0,
			expectedSeriesStates:  []string{"cancelled", "cancelled"},
		},
		{
			name:                  "one_ready",
			readyParticipantCount: 1,
			expectedSeriesStates:  []string{"completed", "cancelled"},
		},
		{
			name:                  "two_ready_same_series",
			readyParticipantCount: 2,
			sameSeries:            true,
			expectedSeriesStates:  []string{"ready", "cancelled"},
		},
		{
			name:                  "two_ready_one_per_series",
			readyParticipantCount: 2,
			expectedSeriesStates:  []string{"completed", "completed"},
		},
		{
			name:                  "all_ready",
			readyParticipantCount: 4,
			expectReadyWave:       true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flow := newSwissCategoryFlow(t, "readiness-recovery-"+tc.name)
			before := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
			round := configureSwissCategoryPairingsThroughREST(
				t, flow.fixture, flow.adminToken, flow.tournamentID,
				before.NextCursor.ProjectionRevision, 1, api.CategoryModeRandom,
				[]api.Category{api.CategoryCrypto, api.CategoryReverse, api.CategoryWeb}, uuid.New(),
			)
			snapshot := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
			wave := findProductionSwissWave(t, snapshot, round)
			require.Len(t, snapshot.Series, 2)
			seriesIDs := make([]uuid.UUID, 0, len(snapshot.Series))
			for _, series := range snapshot.Series {
				seriesIDs = append(seriesIDs, series.Id)
			}

			publicBefore := readinessPublicSnapshotThroughREST(t, flow)
			require.Len(t, publicBefore.LiveSeries, 2)
			wave = controlProductionWaveThroughREST(
				t, flow.fixture, flow.adminToken, flow.tournamentID, wave.Id,
				snapshot.NextCursor.ProjectionRevision, api.WaveControlRequestActionOpenReadyWindow,
			)
			require.Equal(t, api.WaveStateReadyWindowOpen, wave.State)
			publicAfterOpen := readinessPublicSnapshotThroughREST(t, flow)
			require.Len(t, publicAfterOpen.LiveSeries, 2)

			readyIDs := readinessScenarioParticipants(t, wave, tc.readyParticipantCount, tc.sameSeries)
			for _, participantID := range readyIDs {
				setProductionParticipantReady(t, flow, wave, participantID, http.StatusOK)
				publicAfterReady := readinessPublicSnapshotThroughREST(t, flow)
				require.Len(t, publicAfterReady.LiveSeries, 2)
			}

			snapshot = tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
			wave = findProductionWaveByID(t, snapshot, wave.Id)
			if tc.expectReadyWave {
				require.Equal(t, api.WaveStateReady, wave.State)
				require.NotNil(t, wave.ReadyWindow)
				require.Equal(t, api.ReadyWindowStateOpen, wave.ReadyWindow.State)
				require.Len(t, readinessPublicSnapshotThroughREST(t, flow).LiveSeries, 2)
				return
			}

			pending := readyWindowPendingThroughDB(t, flow.tournamentID, snapshot.Roster.Id, wave.Id)
			clock := readinessRecoveryClock{now: pending.DueAt.Add(time.Second + 789*time.Nanosecond)}
			store := recoveryterminalrepo.NewRecoveryTerminalPostgresWithDependencies(
				flow.fixture.mgr, nil, clock,
				wavestartrepo.EnsurePreStartSwissRoundProofForCommand, resultauthority.FinalizeProjection,
			)
			handler := recovery.NewTerminalDeadlineHandlerWithDependencies(
				flow.fixture.mgr, store, clock, newTournamentFlowTerminalCoordinator(flow.fixture.mgr),
			)
			changed, err := handler.HandleDeadline(context.Background(), pending)
			require.NoError(t, err)
			require.True(t, changed)

			var windowState, waveState string
			require.NoError(t, sharedPool.QueryRow(context.Background(), `
				SELECT ready_window.state, wave.state
				FROM ready_windows AS ready_window
				JOIN waves AS wave ON wave.id = ready_window.wave_id
				WHERE ready_window.id = $1`, pending.ID).Scan(&windowState, &waveState))
			require.Equal(t, "expired", windowState)
			require.Equal(t, "ready_window_expired", waveState)

			states := seriesStatesThroughDB(t, seriesIDs)
			require.ElementsMatch(t, tc.expectedSeriesStates, states)

			lateParticipantID := firstUnreadyParticipant(t, wave, readyIDs)
			setProductionParticipantReady(t, flow, wave, lateParticipantID, http.StatusConflict)
		})
	}
}

func readinessScenarioParticipants(
	t *testing.T,
	wave api.Wave,
	readyCount int,
	sameSeries bool,
) []uuid.UUID {
	t.Helper()
	require.GreaterOrEqual(t, readyCount, 0)
	require.LessOrEqual(t, readyCount, len(wave.Members))
	if readyCount == 0 {
		return nil
	}
	if readyCount == len(wave.Members) {
		participants := make([]uuid.UUID, 0, len(wave.Members))
		for _, member := range wave.Members {
			participants = append(participants, member.ParticipantId)
		}
		return participants
	}

	seriesMembers := make(map[uuid.UUID][]uuid.UUID)
	seriesOrder := make([]uuid.UUID, 0, 2)
	for _, member := range wave.Members {
		require.NotNil(t, member.SeriesId)
		seriesID := *member.SeriesId
		if _, seen := seriesMembers[seriesID]; !seen {
			seriesOrder = append(seriesOrder, seriesID)
		}
		seriesMembers[seriesID] = append(seriesMembers[seriesID], member.ParticipantId)
	}
	require.Len(t, seriesOrder, 2)
	for _, seriesID := range seriesOrder {
		require.Len(t, seriesMembers[seriesID], 2)
	}

	switch {
	case readyCount == 1:
		return []uuid.UUID{seriesMembers[seriesOrder[0]][0]}
	case readyCount == 2 && sameSeries:
		return append([]uuid.UUID(nil), seriesMembers[seriesOrder[0]]...)
	case readyCount == 2:
		return []uuid.UUID{seriesMembers[seriesOrder[0]][0], seriesMembers[seriesOrder[1]][0]}
	default:
		require.FailNow(t, "unsupported readiness scenario")
		return nil
	}
}

func setProductionParticipantReady(
	t *testing.T,
	flow swissCategoryFlow,
	wave api.Wave,
	participantID uuid.UUID,
	expectedStatus int,
) {
	t.Helper()
	player, ok := flow.playersByParticipant[participantID]
	require.True(t, ok, "missing player for participant %s", participantID)
	participant := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, player)
	body, err := json.Marshal(api.ParticipantReadyRequest{
		ExpectedProjectionRevision: participant.NextCursor.ProjectionRevision,
		Ready:                      true,
	})
	require.NoError(t, err)
	path := "/api/v1/tournaments/" + flow.tournamentID.String() +
		"/participant/waves/" + wave.Id.String() + "/ready"
	req, resp := doTournamentFlowJSON(
		t, flow.fixture, http.MethodPost, path, string(body),
		cookieSession(player.session.String()), uuid.New(), player.csrf,
	)
	require.Equal(t, expectedStatus, resp.Code, resp.Body.String())
	if expectedStatus != http.StatusOK {
		return
	}
	flow.fixture.validateResponse(t, req, resp)
	event := decodeJSON[api.ReadinessEvent](t, resp)
	require.Equal(t, wave.Id, event.WaveId)
	require.Equal(t, participantID, event.ParticipantId)
}

func firstUnreadyParticipant(t *testing.T, wave api.Wave, readyIDs []uuid.UUID) uuid.UUID {
	t.Helper()
	ready := make(map[uuid.UUID]struct{}, len(readyIDs))
	for _, participantID := range readyIDs {
		ready[participantID] = struct{}{}
	}
	for _, member := range wave.Members {
		if _, ok := ready[member.ParticipantId]; !ok {
			return member.ParticipantId
		}
	}
	require.FailNow(t, "readiness scenario has no participant left for late command")
	return uuid.Nil
}

func readyWindowPendingThroughDB(
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	waveID uuid.UUID,
) recovery.PendingDeadline {
	t.Helper()
	var pending recovery.PendingDeadline
	var deadline time.Time
	require.NoError(t, sharedPool.QueryRow(context.Background(), `
		SELECT ready_window.id, ready_window.revision_id, ready_window.deadline, wave.revision
		FROM ready_windows AS ready_window
		JOIN waves AS wave ON wave.id = ready_window.wave_id
		WHERE ready_window.wave_id = $1`, waveID).Scan(
		&pending.ID, &pending.ReadyWindowRevisionID, &deadline, &pending.ExpectedRevision,
	))
	pending.Kind = recovery.DeadlineKindReadyWindow
	pending.TournamentID = tournamentID
	pending.RosterID = rosterID
	pending.WaveID = waveID
	pending.DueAt = deadline.UTC()
	return pending
}

func seriesStatesThroughDB(t *testing.T, seriesIDs []uuid.UUID) []string {
	t.Helper()
	rows, err := sharedPool.Query(
		context.Background(),
		"SELECT state FROM series WHERE id = ANY($1::uuid[]) ORDER BY id",
		seriesIDs,
	)
	require.NoError(t, err)
	defer rows.Close()
	states := make([]string, 0, len(seriesIDs))
	for rows.Next() {
		var state string
		require.NoError(t, rows.Scan(&state))
		states = append(states, state)
	}
	require.NoError(t, rows.Err())
	return states
}

func readinessPublicSnapshotThroughREST(t *testing.T, flow swissCategoryFlow) api.PublicRecoverySnapshot {
	t.Helper()
	path := "/api/v1/tournaments/" + flow.tournamentID.String() + "/snapshot"
	req, resp := flow.fixture.doJSON(t, http.MethodGet, path, "", "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	flow.fixture.validateResponse(t, req, resp)
	return decodeJSON[api.PublicRecoverySnapshot](t, resp)
}
