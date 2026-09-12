//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

type swissCategoryModeCase struct {
	name       string
	mode       api.CategoryMode
	categories []api.Category
}

type swissCategoryFlow struct {
	fixture              *restFixture
	adminToken           string
	tournamentID         uuid.UUID
	playersByParticipant map[uuid.UUID]tournamentFlowPlayer
	catalog              tournamentFlowCatalog
}

func TestSwissCategoryModesThroughProductionHandlers(t *testing.T) {
	cases := []swissCategoryModeCase{
		{
			name:       "random",
			mode:       api.CategoryModeRandom,
			categories: []api.Category{api.CategoryCrypto, api.CategoryReverse, api.CategoryWeb},
		},
		{
			name:       "admin",
			mode:       api.CategoryModeAdmin,
			categories: []api.Category{api.CategoryWeb},
		},
		{
			name:       "draft",
			mode:       api.CategoryModeDraft,
			categories: []api.Category{api.CategoryCrypto, api.CategoryReverse, api.CategoryWeb},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flow := newSwissCategoryFlow(t, tc.name)
			before := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
			commandsBefore := swissPairingCommandCount(t, flow.tournamentID)

			commandID := uuid.New()
			round := configureSwissCategoryPairingsThroughREST(
				t, flow.fixture, flow.adminToken, flow.tournamentID,
				before.NextCursor.ProjectionRevision, 1, tc.mode, tc.categories, commandID,
			)
			require.Equal(t, commandsBefore+1, swissPairingCommandCount(t, flow.tournamentID))
			// The same authenticated command must return the stored immutable result.
			replayed := configureSwissCategoryPairingsThroughREST(
				t, flow.fixture, flow.adminToken, flow.tournamentID,
				before.NextCursor.ProjectionRevision, 1, tc.mode, tc.categories, commandID,
			)
			require.Equal(t, round, replayed)
			require.Equal(t, commandsBefore+1, swissPairingCommandCount(t, flow.tournamentID))

			snapshot := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
			wave := findProductionSwissWave(t, snapshot, round)
			require.Equal(t, api.WaveStatePlanned, wave.State)
			assertSwissPrestartTaskSecrecy(t, flow, wave)
			if tc.mode == api.CategoryModeDraft {
				assertSwissDraftStartedWithoutTask(t, flow, wave)
				assertSwissDraftReservationsBeforeActivation(t, flow)
				assertSwissDraftBlocksReadyWindow(t, flow, wave)
				completeSwissDraftsThroughREST(t, flow, wave)
				assertSwissPrestartTaskSecrecy(t, flow, wave)
			}

			startedWave := openAndStartSwissWaveThroughREST(t, flow, wave)
			if tc.mode != api.CategoryModeDraft {
				assertSwissAssignmentsCategory(t, flow, startedWave, tc.mode, tc.categories)
			}

			snapshot = tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
			assertSwissMaterializedCategory(t, flow, snapshot, startedWave, tc.mode, tc.categories)
			assertSwissAssignmentReservationLifecycle(t, flow, startedWave, tc.mode)

			orderedParticipants := make([]uuid.UUID, 0, len(flow.playersByParticipant))
			for participantID := range flow.playersByParticipant {
				orderedParticipants = append(orderedParticipants, participantID)
			}
			slices.SortFunc(orderedParticipants, func(first, second uuid.UUID) int {
				return strings.Compare(first.String(), second.String())
			})
			settleProductionSwissWaveThroughREST(
				t, flow.fixture, flow.tournamentID, startedWave, flow.playersByParticipant, flow.catalog.flags,
				func(series api.Series) uuid.UUID {
					return productionSwissWinner(t, series, orderedParticipants, false)
				},
			)
			completeSwissWaveThroughREST(t, &flow, startedWave)
			assertExactlyOneSwissOfficialResult(t, flow, round, startedWave)
		})
	}
}

func TestSwissManualAdminCategoryThroughProductionHandlers(t *testing.T) {
	flow := newSwissCategoryFlow(t, "manual-admin")
	before := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	participants := make([]uuid.UUID, 0, len(flow.playersByParticipant))
	for participantID := range flow.playersByParticipant {
		participants = append(participants, participantID)
	}
	slices.SortFunc(participants, func(first, second uuid.UUID) int {
		return strings.Compare(first.String(), second.String())
	})
	require.Len(t, participants, 4)
	manualPairings := []api.ManualPairInput{
		{FirstParticipantId: participants[0], SecondParticipantId: participants[1]},
		{FirstParticipantId: participants[2], SecondParticipantId: participants[3]},
	}
	body, err := json.Marshal(api.PairingConfigurationRequest{
		Categories:                 []api.Category{api.CategoryWeb},
		CategoryMode:               api.CategoryModeAdmin,
		ExpectedProjectionRevision: before.NextCursor.ProjectionRevision,
		ManualPairings:             &manualPairings,
		PairingMode:                api.Manual,
		RoundNumber:                1,
	})
	require.NoError(t, err)
	commandID := uuid.New()
	path := "/api/v1/admin/tournaments/" + flow.tournamentID.String() + "/pairings"
	req, resp := doTournamentFlowJSON(
		t, flow.fixture, http.MethodPost, path, string(body), adminSession(flow.adminToken), commandID, "",
	)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	flow.fixture.validateResponse(t, req, resp)
	round := decodeJSON[api.SwissRound](t, resp)
	require.Len(t, round.Pairings, len(manualPairings))
	expectedPairs := make(map[string]struct{}, len(manualPairings))
	for _, pairing := range manualPairings {
		expectedPairs[productionParticipantPairKey(pairing.FirstParticipantId, pairing.SecondParticipantId)] = struct{}{}
	}
	for _, pairing := range round.Pairings {
		_, ok := expectedPairs[productionParticipantPairKey(pairing.FirstParticipantId, pairing.SecondParticipantId)]
		require.True(t, ok, "manual pairing was not materialized: %s/%s", pairing.FirstParticipantId, pairing.SecondParticipantId)
	}

	snapshot := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	wave := findProductionSwissWave(t, snapshot, round)
	assertSwissPrestartTaskSecrecy(t, flow, wave)
	started := openAndStartSwissWaveThroughREST(t, flow, wave)
	assertSwissAssignmentsCategory(t, flow, started, api.CategoryModeAdmin, []api.Category{api.CategoryWeb})
	snapshot = tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	assertSwissMaterializedCategory(t, flow, snapshot, started, api.CategoryModeAdmin, []api.Category{api.CategoryWeb})
	assertSwissAssignmentReservationLifecycle(t, flow, started, api.CategoryModeAdmin)

	orderedParticipants := append([]uuid.UUID(nil), participants...)
	settleProductionSwissWaveThroughREST(
		t, flow.fixture, flow.tournamentID, started, flow.playersByParticipant, flow.catalog.flags,
		func(series api.Series) uuid.UUID {
			return productionSwissWinner(t, series, orderedParticipants, false)
		},
	)
	completeSwissWaveThroughREST(t, &flow, started)
	assertExactlyOneSwissOfficialResult(t, flow, round, started)
}

func TestSwissDraftTimeoutWorkerCompletesProductionFlow(t *testing.T) {
	flow := newSwissCategoryFlow(t, "draft-timeout-worker")
	before := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	round := configureSwissCategoryPairingsThroughREST(
		t, flow.fixture, flow.adminToken, flow.tournamentID,
		before.NextCursor.ProjectionRevision, 1, api.CategoryModeDraft,
		[]api.Category{api.CategoryCrypto, api.CategoryReverse, api.CategoryWeb}, uuid.New(),
	)
	wave := findProductionSwissWave(t, tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID), round)
	require.Equal(t, api.WaveStatePlanned, wave.State)
	assertSwissDraftStartedWithoutTask(t, flow, wave)
	assertSwissDraftReservationsBeforeActivation(t, flow)
	assertSwissDraftBlocksReadyWindow(t, flow, wave)

	runtime := tournamentFlowRuntimeForFixture(t, flow.fixture)
	for step := 0; step < 4; step++ {
		active := swissActiveDrafts(t, flow)
		if len(active) == 0 {
			break
		}
		deadline := swissEarliestDraftDeadline(t, active)
		require.Eventually(t, func() bool {
			return !runtime.clock.Now().Before(deadline)
		}, 20*time.Second, 10*time.Millisecond, "Swiss draft deadline must become due")

		beforeActions := swissDraftActionCounts(t, flow.tournamentID)
		worker := newSwissDraftDeadlineWorker(t, flow)
		processed, err := worker.Process(context.Background())
		require.NoError(t, err)
		require.GreaterOrEqual(t, processed.Scanned, 1)
		require.GreaterOrEqual(t, processed.Changed, 1)
		afterActions := swissDraftActionCounts(t, flow.tournamentID)
		require.Equal(t, beforeActions.total+processed.Changed, afterActions.total)
		require.Equal(t, beforeActions.automatic+processed.Changed, afterActions.automatic)

		// A newly constructed worker represents a restart between polls. The
		// immutable timeout command and current revision must make this a no-op.
		restarted := newSwissDraftDeadlineWorker(t, flow)
		replayed, err := restarted.Process(context.Background())
		require.NoError(t, err)
		require.Zero(t, replayed.Changed)
		require.Equal(t, afterActions, swissDraftActionCounts(t, flow.tournamentID))
	}

	active := swissActiveDrafts(t, flow)
	require.Empty(t, active, "all Swiss BO1 drafts must complete through deadline processing")
	actions := swissDraftActionCounts(t, flow.tournamentID)
	require.Equal(t, 4, actions.total, "two BO1 drafts require two bans each")
	require.Equal(t, actions.total, actions.automatic)

	// Draft completion must not disclose a task before the shared Wave starts.
	planned := findProductionSwissWave(t, tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID), round)
	assertSwissPrestartTaskSecrecy(t, flow, planned)
	started := openAndStartSwissWaveThroughREST(t, flow, planned)
	assertSwissAssignmentsCategory(t, flow, started, api.CategoryModeDraft,
		[]api.Category{api.CategoryCrypto, api.CategoryReverse, api.CategoryWeb})
	snapshot := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	assertSwissMaterializedCategory(t, flow, snapshot, started, api.CategoryModeDraft,
		[]api.Category{api.CategoryCrypto, api.CategoryReverse, api.CategoryWeb})
	assertSwissAssignmentReservationLifecycle(t, flow, started, api.CategoryModeDraft)

	orderedParticipants := make([]uuid.UUID, 0, len(flow.playersByParticipant))
	for participantID := range flow.playersByParticipant {
		orderedParticipants = append(orderedParticipants, participantID)
	}
	slices.SortFunc(orderedParticipants, func(first, second uuid.UUID) int {
		return strings.Compare(first.String(), second.String())
	})
	settleProductionSwissWaveThroughREST(
		t, flow.fixture, flow.tournamentID, started, flow.playersByParticipant, flow.catalog.flags,
		func(series api.Series) uuid.UUID {
			return productionSwissWinner(t, series, orderedParticipants, false)
		},
	)
	completeSwissWaveThroughREST(t, &flow, started)
	assertExactlyOneSwissOfficialResult(t, flow, round, started)
}

func TestSwissCategoryValidationRollsBackWithoutMaterialization(t *testing.T) {
	flow := newSwissCategoryFlow(t, "invalid-category")
	before := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	commandsBefore := swissPairingCommandCount(t, flow.tournamentID)

	body, err := json.Marshal(api.PairingConfigurationRequest{
		Categories:                 []api.Category{api.CategoryForensics},
		CategoryMode:               api.CategoryModeAdmin,
		ExpectedProjectionRevision: before.NextCursor.ProjectionRevision,
		PairingMode:                api.Automatic,
		RoundNumber:                1,
	})
	require.NoError(t, err)
	req, resp := doTournamentFlowJSON(
		t, flow.fixture, http.MethodPost,
		"/api/v1/admin/tournaments/"+flow.tournamentID.String()+"/pairings", string(body),
		adminSession(flow.adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
	flow.fixture.validateResponse(t, req, resp)

	after := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	require.Equal(t, before.NextCursor.ProjectionRevision, after.NextCursor.ProjectionRevision)
	require.Equal(t, before.NextCursor.AuditSequence, after.NextCursor.AuditSequence)
	require.Empty(t, after.Series)
	require.Empty(t, after.Waves)
	require.Equal(t, commandsBefore, swissPairingCommandCount(t, flow.tournamentID))
}

func TestSwissCategoryInsufficientCandidatesRollsBackPairing(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	catalog := prepareSwissCategoryShortageContent(ctx, t)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	players := joinTournamentFlowPlayers(t, fixture, 4)
	created := createTournamentThroughREST(t, fixture, adminToken, catalog.revision, "insufficient-candidates")
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
	preflight := runTournamentRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
	lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
	startSwissThroughREST(t, fixture, adminToken, created.Id)
	connectTournamentParticipantsThroughProduction(t, fixture, created.Id, players)

	playersByID := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
	for _, player := range players {
		playersByID[player.id] = player
	}
	flow := swissCategoryFlow{
		fixture:              fixture,
		adminToken:           adminToken,
		tournamentID:         created.Id,
		playersByParticipant: productionPlayersByParticipant(t, roster, playersByID),
		catalog:              catalog,
	}

	for roundNumber := 1; roundNumber <= 2; roundNumber++ {
		snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
		round := configureSwissCategoryPairingsThroughREST(
			t, fixture, adminToken, created.Id, snapshot.NextCursor.ProjectionRevision,
			int32(roundNumber), api.CategoryModeAdmin, []api.Category{api.CategoryWeb}, uuid.New(),
		)
		wave := findProductionSwissWave(t, tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id), round)
		started := openAndStartSwissWaveThroughREST(t, flow, wave)
		assertSwissAssignmentsCategory(t, flow, started, api.CategoryModeAdmin, []api.Category{api.CategoryWeb})
		orderedParticipants := make([]uuid.UUID, 0, len(flow.playersByParticipant))
		for participantID := range flow.playersByParticipant {
			orderedParticipants = append(orderedParticipants, participantID)
		}
		slices.SortFunc(orderedParticipants, func(first, second uuid.UUID) int {
			return strings.Compare(first.String(), second.String())
		})
		settleProductionSwissWaveThroughREST(
			t, fixture, created.Id, started, flow.playersByParticipant, catalog.flags,
			func(series api.Series) uuid.UUID {
				return productionSwissWinner(t, series, orderedParticipants, false)
			},
		)
		completeSwissWaveThroughREST(t, &flow, started)
	}

	for _, tc := range []struct {
		name       string
		mode       api.CategoryMode
		categories []api.Category
	}{
		{
			name:       "admin-web",
			mode:       api.CategoryModeAdmin,
			categories: []api.Category{api.CategoryWeb},
		},
		{
			name:       "draft-full-pool",
			mode:       api.CategoryModeDraft,
			categories: []api.Category{api.CategoryCrypto, api.CategoryReverse, api.CategoryWeb},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertSwissInsufficientPairingRollback(t, flow, 3, tc.mode, tc.categories)
		})
	}
}

func assertSwissInsufficientPairingRollback(
	t *testing.T,
	flow swissCategoryFlow,
	roundNumber int32,
	mode api.CategoryMode,
	categories []api.Category,
) {
	t.Helper()
	before := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	commandsBefore := swissPairingCommandCount(t, flow.tournamentID)
	branchesBefore := swissAssignmentBranchStates(t, flow.tournamentID)
	ledgerBefore := swissReservationLedgerForTournament(t, flow.tournamentID)
	scopeBefore := readSwissMaterializationCounts(t, flow.tournamentID, before.Roster.Id)

	body, err := json.Marshal(api.PairingConfigurationRequest{
		Categories:                 append([]api.Category(nil), categories...),
		CategoryMode:               mode,
		ExpectedProjectionRevision: before.NextCursor.ProjectionRevision,
		PairingMode:                api.Automatic,
		RoundNumber:                roundNumber,
	})
	require.NoError(t, err)
	req, resp := doTournamentFlowJSON(
		t, flow.fixture, http.MethodPost,
		"/api/v1/admin/tournaments/"+flow.tournamentID.String()+"/pairings", string(body),
		adminSession(flow.adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusConflict, resp.Code, resp.Body.String())
	flow.fixture.validateResponse(t, req, resp)

	after := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	require.Equal(t, before.NextCursor, after.NextCursor)
	require.Equal(t, before.Series, after.Series)
	require.Equal(t, before.Waves, after.Waves)
	require.Equal(t, commandsBefore, swissPairingCommandCount(t, flow.tournamentID))
	require.Equal(t, branchesBefore, swissAssignmentBranchStates(t, flow.tournamentID))
	require.Equal(t, ledgerBefore, swissReservationLedgerForTournament(t, flow.tournamentID))
	require.Equal(t, scopeBefore, readSwissMaterializationCounts(t, flow.tournamentID, after.Roster.Id))
}

func newSwissCategoryFlow(t *testing.T, name string) swissCategoryFlow {
	t.Helper()
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	catalog := prepareCreateToChampionContent(ctx, t)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	players := joinTournamentFlowPlayers(t, fixture, 4)
	created := createTournamentThroughREST(t, fixture, adminToken, catalog.revision, "swiss-category-"+name)
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
	preflight := runTournamentRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
	lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
	startSwissThroughREST(t, fixture, adminToken, created.Id)
	connectTournamentParticipantsThroughProduction(t, fixture, created.Id, players)

	playersByID := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
	for _, player := range players {
		playersByID[player.id] = player
	}
	return swissCategoryFlow{
		fixture:              fixture,
		adminToken:           adminToken,
		tournamentID:         created.Id,
		playersByParticipant: productionPlayersByParticipant(t, roster, playersByID),
		catalog:              catalog,
	}
}

func configureSwissCategoryPairingsThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	projectionRevision int64,
	roundNumber int32,
	mode api.CategoryMode,
	categories []api.Category,
	commandID uuid.UUID,
) api.SwissRound {
	t.Helper()
	body, err := json.Marshal(api.PairingConfigurationRequest{
		Categories:                 append([]api.Category(nil), categories...),
		CategoryMode:               mode,
		ExpectedProjectionRevision: projectionRevision,
		PairingMode:                api.Automatic,
		RoundNumber:                roundNumber,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/pairings"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPost, path, string(body), adminSession(adminToken), commandID, "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	round := decodeJSON[api.SwissRound](t, resp)
	require.Equal(t, roundNumber, round.RoundNumber)
	require.NotEmpty(t, round.Pairings)
	return round
}

func openAndStartSwissWaveThroughREST(t *testing.T, flow swissCategoryFlow, planned api.Wave) api.Wave {
	t.Helper()
	snapshot := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	wave := findProductionWaveByID(t, snapshot, planned.Id)
	wave = controlProductionWaveThroughREST(
		t, flow.fixture, flow.adminToken, flow.tournamentID, wave.Id,
		snapshot.NextCursor.ProjectionRevision, api.WaveControlRequestActionOpenReadyWindow,
	)
	require.Equal(t, api.WaveStateReadyWindowOpen, wave.State)

	for _, member := range wave.Members {
		player, ok := flow.playersByParticipant[member.ParticipantId]
		require.True(t, ok, "missing player for participant %s", member.ParticipantId)
		participant := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, player)
		body, err := json.Marshal(api.ParticipantReadyRequest{
			ExpectedProjectionRevision: participant.NextCursor.ProjectionRevision,
			Ready:                      true,
		})
		require.NoError(t, err)
		path := "/api/v1/tournaments/" + flow.tournamentID.String() +
			"/participant/waves/" + wave.Id.String() + "/ready"
		req, resp := doTournamentFlowJSON(
			t, flow.fixture, http.MethodPost, path, string(body), cookieSession(player.session.String()), uuid.New(), player.csrf,
		)
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		flow.fixture.validateResponse(t, req, resp)
		ready := decodeJSON[api.ReadinessEvent](t, resp)
		require.Equal(t, wave.Id, ready.WaveId)
	}

	snapshot = tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	wave = findProductionWaveByID(t, snapshot, wave.Id)
	require.Equal(t, api.WaveStateReady, wave.State)
	return controlProductionWaveThroughREST(
		t, flow.fixture, flow.adminToken, flow.tournamentID, wave.Id,
		snapshot.NextCursor.ProjectionRevision, api.WaveControlRequestActionStart,
	)
}

func assertSwissPrestartTaskSecrecy(t *testing.T, flow swissCategoryFlow, wave api.Wave) {
	t.Helper()
	for _, member := range wave.Members {
		player, ok := flow.playersByParticipant[member.ParticipantId]
		require.True(t, ok)
		participant := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, player)
		require.Nil(t, participant.Assignment, "task assignment must stay hidden before Wave start")
	}
	path := "/api/v1/tournaments/" + flow.tournamentID.String() + "/snapshot"
	req, resp := flow.fixture.doJSON(t, http.MethodGet, path, "", "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	flow.fixture.validateResponse(t, req, resp)
	require.NotContains(t, resp.Body.String(), `"task_id"`)
	require.NotContains(t, resp.Body.String(), `"flag"`)
}

func assertSwissDraftStartedWithoutTask(t *testing.T, flow swissCategoryFlow, wave api.Wave) {
	t.Helper()
	for _, member := range wave.Members {
		player, ok := flow.playersByParticipant[member.ParticipantId]
		require.True(t, ok)
		participant := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, player)
		require.Nil(t, participant.Assignment, "draft must not disclose a task before selection")
		require.NotNil(t, participant.Draft)
		require.Equal(t, api.DraftStateActive, participant.Draft.State)
		require.NotNil(t, member.SeriesId)
		require.Equal(t, *member.SeriesId, participant.Draft.SeriesId)
	}
}

func assertSwissDraftBlocksReadyWindow(t *testing.T, flow swissCategoryFlow, wave api.Wave) {
	t.Helper()
	before := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	current := findProductionWaveByID(t, before, wave.Id)
	require.Equal(t, api.WaveStatePlanned, current.State)
	require.Nil(t, current.ReadyWindow)
	body, err := json.Marshal(api.WaveControlRequest{
		Action:                     api.WaveControlRequestActionOpenReadyWindow,
		Confirmed:                  true,
		ExpectedProjectionRevision: before.NextCursor.ProjectionRevision,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + flow.tournamentID.String() + "/waves/" + wave.Id.String() + "/actions"
	req, resp := doTournamentFlowJSON(
		t, flow.fixture, http.MethodPost, path, string(body), adminSession(flow.adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusConflict, resp.Code, resp.Body.String())
	flow.fixture.validateResponse(t, req, resp)
	after := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	require.Equal(t, before.NextCursor.ProjectionRevision, after.NextCursor.ProjectionRevision)
	require.Equal(t, before.NextCursor.AuditSequence, after.NextCursor.AuditSequence)
	current = findProductionWaveByID(t, after, wave.Id)
	require.Equal(t, api.WaveStatePlanned, current.State)
	require.Nil(t, current.ReadyWindow, "rejected ready-window command must not persist a window")
}

func assertSwissAssignmentsCategory(
	t *testing.T,
	flow swissCategoryFlow,
	wave api.Wave,
	mode api.CategoryMode,
	categories []api.Category,
) {
	t.Helper()
	for _, member := range wave.Members {
		player, ok := flow.playersByParticipant[member.ParticipantId]
		require.True(t, ok)
		participant := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, player)
		require.NotNil(t, participant.Assignment)
		category := participant.Assignment.ActiveSnapshot.Category
		if mode == api.CategoryModeAdmin {
			require.Equal(t, api.CategoryWeb, category)
		} else {
			require.Contains(t, categories, category)
		}
	}
}

func completeSwissDraftsThroughREST(t *testing.T, flow swissCategoryFlow, wave api.Wave) {
	t.Helper()
	for actions := 0; actions < 4; actions++ {
		found := false
		for _, player := range flow.playersByParticipant {
			participant := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, player)
			if participant.Draft == nil || participant.Draft.State != api.DraftStateActive {
				continue
			}
			current := *participant.Draft
			actorID := current.FirstParticipantId
			if current.Turn%2 == 0 {
				actorID = current.SecondParticipantId
			}
			actor, ok := flow.playersByParticipant[actorID]
			require.True(t, ok)
			actorSnapshot := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, actor)
			require.NotNil(t, actorSnapshot.Draft)
			current = *actorSnapshot.Draft
			category := productionNextDraftCategory(t, current)
			action := api.Ban
			if current.Turn > 2 {
				action = api.Pick
			}
			body, err := json.Marshal(api.ParticipantDraftActionRequest{
				ExpectedProjectionRevision: actorSnapshot.NextCursor.ProjectionRevision,
				ExpectedDraftRevision:      current.Revision,
				ExpectedTurn:               current.Turn,
				Action:                     action,
				Category:                   category,
			})
			require.NoError(t, err)
			path := "/api/v1/tournaments/" + flow.tournamentID.String() + "/participant/series/" +
				current.SeriesId.String() + "/draft/actions"
			req, resp := doTournamentFlowJSON(
				t, flow.fixture, http.MethodPost, path, string(body), cookieSession(actor.session.String()), uuid.New(), actor.csrf,
			)
			require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
			flow.fixture.validateResponse(t, req, resp)
			found = true
			break
		}
		if !found {
			return
		}
	}
	require.Empty(t, swissActiveDrafts(t, flow), "Swiss BO1 drafts must complete after four bans")
	actions := swissDraftActionCounts(t, flow.tournamentID)
	require.Equal(t, 4, actions.total)
	require.Zero(t, actions.automatic)
}

func newSwissDraftDeadlineWorker(t *testing.T, flow swissCategoryFlow) *draftusecase.DeadlineWorker {
	t.Helper()
	runtime := tournamentFlowRuntimeForFixture(t, flow.fixture)
	drafts := postgres.NewDraftPostgres(flow.fixture.databaseFixture.mgr)
	participantDrafts := postgres.NewParticipantDraftRepository(flow.fixture.databaseFixture.mgr, drafts)
	repository := postgres.NewSwissDraftDeadlinePostgres(flow.fixture.databaseFixture.mgr, participantDrafts)
	worker, err := draftusecase.NewDeadlineWorker(
		repository,
		runtime.clock,
		draftusecase.DeadlineWorkerConfig{
			BatchSize:    16,
			PollInterval: time.Second,
			ScanTimeout:  time.Second,
			StaleAfter:   time.Minute,
		},
	)
	require.NoError(t, err)
	return worker
}

func swissActiveDrafts(t *testing.T, flow swissCategoryFlow) map[uuid.UUID]api.Draft {
	t.Helper()
	active := make(map[uuid.UUID]api.Draft)
	for _, player := range flow.playersByParticipant {
		participant := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, player)
		if participant.Draft == nil || participant.Draft.State != api.DraftStateActive {
			continue
		}
		active[participant.Draft.Id] = *participant.Draft
	}
	return active
}

func swissEarliestDraftDeadline(t *testing.T, drafts map[uuid.UUID]api.Draft) time.Time {
	t.Helper()
	var earliest time.Time
	for _, draft := range drafts {
		require.NotNil(t, draft.TurnDeadline)
		if earliest.IsZero() || draft.TurnDeadline.Before(earliest) {
			earliest = *draft.TurnDeadline
		}
	}
	require.False(t, earliest.IsZero())
	return earliest
}

type swissDraftActionTotals struct {
	total     int
	automatic int
}

func swissDraftActionCounts(t *testing.T, tournamentID uuid.UUID) swissDraftActionTotals {
	t.Helper()
	var totals swissDraftActionTotals
	err := sharedPool.QueryRow(context.Background(), `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE action.automatic)
		FROM draft_actions AS action
		JOIN drafts AS draft ON draft.id = action.draft_id
		JOIN series ON series.id = draft.series_id AND series.roster_id = draft.roster_id
		WHERE series.tournament_id = $1`, tournamentID).Scan(&totals.total, &totals.automatic)
	require.NoError(t, err)
	return totals
}

type swissReservationLedger struct {
	plans                int
	total                int
	reserved             int
	committed            int
	released             int
	superseded           int
	disclosed            int
	undisclosed          int
	committedUndisclosed int
	releasedUndisclosed  int
}

func swissReservationLedgerForTournament(t *testing.T, tournamentID uuid.UUID) swissReservationLedger {
	t.Helper()
	var ledger swissReservationLedger
	err := sharedPool.QueryRow(context.Background(), `
		SELECT COUNT(DISTINCT plan.id),
			COUNT(reservation.id),
			COUNT(*) FILTER (WHERE reservation.state = 'reserved'),
			COUNT(*) FILTER (WHERE reservation.state = 'committed'),
			COUNT(*) FILTER (WHERE reservation.state = 'released'),
			COUNT(*) FILTER (WHERE reservation.state = 'superseded'),
			COUNT(*) FILTER (WHERE reservation.disclosed_at IS NOT NULL),
			COUNT(*) FILTER (WHERE reservation.disclosed_at IS NULL),
			COUNT(*) FILTER (WHERE reservation.state = 'committed' AND reservation.disclosed_at IS NULL),
			COUNT(*) FILTER (WHERE reservation.state = 'released' AND reservation.disclosed_at IS NULL)
		FROM assignment_plans AS plan
		LEFT JOIN task_version_reservations AS reservation ON reservation.plan_id = plan.id
		WHERE plan.tournament_id = $1`, tournamentID).Scan(
		&ledger.plans, &ledger.total, &ledger.reserved, &ledger.committed,
		&ledger.released, &ledger.superseded, &ledger.disclosed, &ledger.undisclosed,
		&ledger.committedUndisclosed, &ledger.releasedUndisclosed,
	)
	require.NoError(t, err)
	return ledger
}

type swissCategoryMaterializationScopeCounts struct {
	drafts           int
	draftRevisions   int
	exactDraftGroups int
	reservations     int
	plans            int
	categories       int
	gameSlots        int
	gameAttempts     int
	assignments      int
}

func readSwissMaterializationCounts(
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
) swissCategoryMaterializationScopeCounts {
	t.Helper()
	var counts swissCategoryMaterializationScopeCounts
	err := sharedPool.QueryRow(context.Background(), `
		SELECT
			(SELECT COUNT(*)
			 FROM drafts AS draft
			 JOIN series ON series.id = draft.series_id
				 AND series.roster_id = draft.roster_id
			WHERE draft.roster_id = $2 AND series.tournament_id = $1),
			(SELECT COUNT(*)
			 FROM draft_revisions AS revision
			 JOIN drafts AS draft ON draft.id = revision.draft_id
				 AND draft.roster_id = revision.roster_id
			 JOIN series ON series.id = draft.series_id
				 AND series.roster_id = draft.roster_id
			WHERE revision.roster_id = $2 AND series.tournament_id = $1),
			(SELECT COUNT(*)
			 FROM exact_draft_assignment_branches AS draft_group
			 JOIN assignment_plans AS plan ON plan.id = draft_group.plan_id
			WHERE plan.tournament_id = $1 AND plan.roster_id = $2),
			(SELECT COUNT(*)
			 FROM task_version_reservations AS reservation
			 JOIN assignment_plans AS plan ON plan.id = reservation.plan_id
			WHERE plan.tournament_id = $1 AND plan.roster_id = $2),
			(SELECT COUNT(*)
			 FROM assignment_plans AS plan
			WHERE plan.tournament_id = $1 AND plan.roster_id = $2),
			(SELECT COUNT(*)
			 FROM category_revisions AS category
			 JOIN series ON series.id = category.series_id
				 AND series.roster_id = category.roster_id
			WHERE category.roster_id = $2 AND series.tournament_id = $1),
			(SELECT COUNT(*)
			 FROM game_slots AS slot
			 JOIN series ON series.id = slot.series_id
				 AND series.roster_id = slot.roster_id
			WHERE slot.roster_id = $2 AND series.tournament_id = $1),
			(SELECT COUNT(*)
			 FROM game_attempts AS attempt
			 JOIN series ON series.id = attempt.series_id
				 AND series.roster_id = attempt.roster_id
			WHERE attempt.roster_id = $2 AND series.tournament_id = $1),
			(SELECT COUNT(*)
			 FROM assignments AS assignment
			 JOIN series ON series.id = assignment.series_id
				 AND series.roster_id = assignment.roster_id
			WHERE assignment.roster_id = $2 AND series.tournament_id = $1)`, tournamentID, rosterID).Scan(
		&counts.drafts, &counts.draftRevisions, &counts.exactDraftGroups,
		&counts.reservations, &counts.plans, &counts.categories,
		&counts.gameSlots, &counts.gameAttempts, &counts.assignments,
	)
	require.NoError(t, err)
	return counts
}

func assertSwissDraftReservationsBeforeActivation(t *testing.T, flow swissCategoryFlow) {
	t.Helper()
	ledger := swissReservationLedgerForTournament(t, flow.tournamentID)
	// Two BO1 Series each reserve six reachable paths, one three-task branch
	// per path. All alternatives remain undisclosed until the draft completes.
	require.Equal(t, 2, ledger.plans)
	require.Equal(t, 36, ledger.total)
	require.Equal(t, ledger.total, ledger.reserved)
	require.Zero(t, ledger.committed)
	require.Zero(t, ledger.released)
	require.Zero(t, ledger.superseded)
	require.Zero(t, ledger.disclosed)
	require.Equal(t, ledger.total, ledger.undisclosed)
	branches := swissAssignmentBranchStates(t, flow.tournamentID)
	require.Equal(t, 12, branches.reserved)
	require.Zero(t, branches.active)
	require.Zero(t, branches.released)
	require.Zero(t, branches.superseded)

	var games, receipts int
	err := sharedPool.QueryRow(context.Background(), `
		SELECT
			(SELECT COUNT(*) FROM game_slots AS slot
			 JOIN series ON series.id = slot.series_id AND series.roster_id = slot.roster_id
			 WHERE series.tournament_id = $1),
			(SELECT COUNT(*) FROM task_delivery_receipts AS receipt
			 JOIN rosters ON rosters.id = receipt.roster_id
			 WHERE rosters.tournament_id = $1)`, flow.tournamentID).Scan(&games, &receipts)
	require.NoError(t, err)
	require.Zero(t, games, "draft pairing must not create a Game before category selection")
	require.Zero(t, receipts, "draft pairing must not create a delivery receipt before Wave activation")
}

func assertSwissAssignmentReservationLifecycle(
	t *testing.T,
	flow swissCategoryFlow,
	wave api.Wave,
	mode api.CategoryMode,
) {
	t.Helper()
	ledger := swissReservationLedgerForTournament(t, flow.tournamentID)
	seriesIDs := make(map[uuid.UUID]struct{})
	for _, member := range wave.Members {
		if member.SeriesId != nil {
			seriesIDs[*member.SeriesId] = struct{}{}
		}
	}
	require.NotEmpty(t, seriesIDs)
	require.Equal(t, len(seriesIDs), ledger.committed/(domain.AssignmentReserveCount+1))
	require.Zero(t, ledger.reserved)
	require.Zero(t, ledger.superseded)
	branches := swissAssignmentBranchStates(t, flow.tournamentID)
	require.Zero(t, branches.reserved)
	require.Equal(t, len(seriesIDs), branches.active)
	require.Equal(t, branches.active+branches.released, branches.total)
	require.Equal(t, ledger.committed, ledger.disclosed+ledger.committedUndisclosed)
	require.Equal(t, ledger.committed, 3*len(seriesIDs))
	require.Equal(t, ledger.disclosed, len(seriesIDs), "one primary reservation per Series is disclosed")
	require.Equal(t, ledger.committedUndisclosed, 2*len(seriesIDs), "two reserves per active branch remain undisclosed")
	require.Equal(t, ledger.releasedUndisclosed, ledger.released)
	if mode == api.CategoryModeDraft {
		require.Greater(t, ledger.released, 0, "completed draft must release every losing branch")
	} else {
		require.Zero(t, ledger.released)
	}

	var activeAssignments, receipts int
	err := sharedPool.QueryRow(context.Background(), `
		SELECT
			COUNT(DISTINCT assignment.id), COUNT(receipt.id)
		FROM assignments AS assignment
		JOIN assignment_plans AS plan ON plan.id = assignment.plan_id
		LEFT JOIN task_delivery_receipts AS receipt ON receipt.assignment_id = assignment.id
		WHERE plan.tournament_id = $1 AND assignment.state = 'active'`, flow.tournamentID).
		Scan(&activeAssignments, &receipts)
	require.NoError(t, err)
	require.Equal(t, len(seriesIDs), activeAssignments)
	require.Equal(t, 2*len(seriesIDs), receipts, "each active assignment has one receipt for each participant")
	for _, player := range flow.playersByParticipant {
		participant := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, player)
		require.NotNil(t, participant.Assignment)
		require.Equal(t, int32(domain.AssignmentReserveCount), participant.Assignment.UndisclosedReserveCount)
	}
}

type swissAssignmentBranchStateCounts struct {
	total      int
	reserved   int
	active     int
	released   int
	superseded int
}

func swissAssignmentBranchStates(t *testing.T, tournamentID uuid.UUID) swissAssignmentBranchStateCounts {
	t.Helper()
	var states swissAssignmentBranchStateCounts
	err := sharedPool.QueryRow(context.Background(), `
		SELECT COUNT(branch.id),
			COUNT(*) FILTER (WHERE branch.state = 'reserved'),
			COUNT(*) FILTER (WHERE branch.state = 'active'),
			COUNT(*) FILTER (WHERE branch.state = 'released'),
			COUNT(*) FILTER (WHERE branch.state = 'superseded')
		FROM assignment_branches AS branch
		JOIN assignment_plans AS plan ON plan.id = branch.plan_id
		WHERE plan.tournament_id = $1`, tournamentID).Scan(
		&states.total, &states.reserved, &states.active, &states.released, &states.superseded,
	)
	require.NoError(t, err)
	return states
}

func assertSwissMaterializedCategory(
	t *testing.T,
	flow swissCategoryFlow,
	snapshot api.OperatorRecoverySnapshot,
	wave api.Wave,
	mode api.CategoryMode,
	categories []api.Category,
) {
	t.Helper()
	seriesByID := make(map[uuid.UUID]api.Series, len(snapshot.Series))
	seriesIDs := make(map[uuid.UUID]struct{})
	for _, member := range wave.Members {
		if member.SeriesId != nil {
			seriesIDs[*member.SeriesId] = struct{}{}
		}
	}
	for _, series := range snapshot.Series {
		if _, ok := seriesIDs[series.Id]; ok {
			seriesByID[series.Id] = series
		}
	}
	require.Len(t, seriesByID, len(seriesIDs))
	for seriesID, series := range seriesByID {
		require.Len(t, series.Slots, 1, "Swiss BO1 series %s must expose one selected slot", seriesID)
		category := series.Slots[0].Category
		if mode == api.CategoryModeAdmin {
			require.Equal(t, api.CategoryWeb, category)
		} else {
			require.Contains(t, categories, category)
		}
	}
	if mode != api.CategoryModeDraft {
		return
	}
	for participantID, player := range flow.playersByParticipant {
		participant := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, player)
		require.NotNil(t, participant.Draft)
		require.NotNil(t, participant.Series)
		require.Equal(t, api.DraftStateCompleted, participant.Draft.State)
		require.Len(t, participant.Draft.SelectedCategories, 1)
		require.Equal(t, participant.Draft.SelectedCategories[0], seriesByID[participant.Series.Id].Slots[0].Category,
			"completed draft for participant %s must own the materialized slot", participantID)
	}
}

func completeSwissWaveThroughREST(t *testing.T, flow *swissCategoryFlow, wave api.Wave) {
	t.Helper()
	snapshot := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	current := findProductionWaveByID(t, snapshot, wave.Id)
	if current.State == api.WaveStateCompleted {
		return
	}
	complete := controlProductionWaveThroughREST(
		t, flow.fixture, flow.adminToken, flow.tournamentID, wave.Id,
		snapshot.NextCursor.ProjectionRevision, api.WaveControlRequestActionComplete,
	)
	require.Equal(t, api.WaveStateCompleted, complete.State)
}

func assertExactlyOneSwissOfficialResult(t *testing.T, flow swissCategoryFlow, round api.SwissRound, wave api.Wave) {
	t.Helper()
	seriesIDs := make(map[uuid.UUID]struct{})
	for _, member := range wave.Members {
		if member.SeriesId != nil {
			seriesIDs[*member.SeriesId] = struct{}{}
		}
	}
	path := "/api/v1/tournaments/" + flow.tournamentID.String() + "/snapshot"
	req, resp := flow.fixture.doJSON(t, http.MethodGet, path, "", "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	flow.fixture.validateResponse(t, req, resp)
	public := decodeJSON[api.PublicRecoverySnapshot](t, resp)
	counts := make(map[uuid.UUID]int, len(seriesIDs))
	for _, result := range public.OfficialResults {
		if _, ok := seriesIDs[result.SeriesId]; !ok {
			continue
		}
		require.Equal(t, api.SeriesStateCompleted, result.State)
		require.NotNil(t, result.WinnerDisplayName)
		counts[result.SeriesId]++
	}
	require.Len(t, counts, len(round.Pairings))
	for seriesID := range seriesIDs {
		require.Equal(t, 1, counts[seriesID], "series %s must have exactly one official result", seriesID)
	}
}

func prepareSwissCategoryShortageContent(ctx context.Context, t *testing.T) tournamentFlowCatalog {
	t.Helper()
	catalog := prepareCreateToChampionContent(ctx, t)
	_, err := sharedPool.Exec(ctx, `
		WITH retired AS (
			SELECT id
			FROM tasks
			WHERE kind = 'normal' AND category = 'web' AND deleted_at IS NULL
			ORDER BY id
			OFFSET 15
		)
		UPDATE tasks
		SET enabled = false, deleted_at = clock_timestamp()
		WHERE id IN (SELECT id FROM retired)`)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `SELECT publish_task_pool_heads()`)
	require.NoError(t, err)
	catalog.revision = currentTaskPoolPublicationRevision(ctx, t)
	return catalog
}

func swissPairingCommandCount(t *testing.T, tournamentID uuid.UUID) int {
	t.Helper()
	var count int
	err := sharedPool.QueryRow(
		context.Background(),
		`SELECT COUNT(*) FROM swiss_pairing_commands WHERE tournament_id = $1`,
		tournamentID,
	).Scan(&count)
	require.NoError(t, err)
	return count
}
