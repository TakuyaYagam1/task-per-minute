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
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
	snapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/snapshot"
	configurationrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/configuration"
	tournamentsnapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/snapshot"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
	configurationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/configuration"
	tournamentadminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	adminoperation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
	snapshotusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
)

func TestTournamentConfigurationDefaultsThroughProductionHTTPAndPostgres(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	prepareTournamentContentPublication(ctx, t, uniq("configuration_defaults"))
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	selection := getTournamentContentThroughREST(t, fixture, adminToken)
	tournament, _ := createTournamentWithContentThroughREST(
		t, fixture, adminToken, selection.ContentRevision, "configuration-defaults",
	)
	path := "/api/v1/admin/tournaments/" + tournament.Id.String() + "/configuration"
	authority, err := configurationrepo.NewProductionTournamentConfigurationPostgres(fixture.mgr).LoadConfiguration(
		ctx,
		configurationusecase.ConfigurationLoadQuery{
			Operator:     adminoperation.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: tournament.Id,
		},
	)
	require.NoError(t, err)
	require.NoError(t, authority.Configuration.Validate(), "loaded content configuration")
	require.True(t, authority.TournamentState.IsValid(), "loaded tournament state")
	require.NotEqual(t, uuid.Nil, authority.ProjectionRevisionID, "loaded projection identity")
	require.GreaterOrEqual(t, authority.ProjectionRevision, int64(1), "loaded projection revision")
	require.GreaterOrEqual(t, authority.TournamentRevision, int64(1), "loaded tournament revision")
	require.True(t, authority.SwissDefault.Mode.IsValid(), "loaded Swiss mode")
	require.NotEmpty(t, authority.SwissDefault.Categories, "loaded Swiss categories")
	require.True(t, authority.GoldenDefault.Mode.IsValid(), "loaded Golden mode")
	require.NotEmpty(t, authority.GoldenDefault.Categories, "loaded Golden categories")
	require.True(t, authority.SemifinalDefault.Mode.IsValid(), "loaded semifinal mode")
	require.NotEmpty(t, authority.SemifinalDefault.Categories, "loaded semifinal categories")
	require.Len(t, authority.FinalDefault.Categories, 5, "loaded final categories")
	require.Len(t, authority.Series, 0, "new tournament has no Series")
	require.Len(t, authority.Rounds, 0, "new tournament has no Swiss rounds")
	require.NoError(t, authority.Validate(tournament.Id))

	request, response := doTournamentFlowJSON(
		t, fixture, http.MethodGet, path, "", adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	initial := decodeJSON[api.TournamentConfiguration](t, response)
	require.Equal(t, tournament.Id, initial.TournamentId)
	require.Len(t, initial.CategoryPools, 2)
	require.Empty(t, initial.Series)

	var bo1 api.TournamentConfigurationCategoryPool
	for _, pool := range initial.CategoryPools {
		if pool.Format == api.Bo1 {
			bo1 = pool
		}
	}
	require.NotEqual(t, uuid.Nil, bo1.Id)
	require.NotEmpty(t, bo1.Categories)
	selectedCategory := bo1.Categories[0]
	body, err := json.Marshal(api.UpdateTournamentConfigurationRequest{
		ExpectedProjectionRevision:    initial.ProjectionRevision,
		ExpectedConfigurationRevision: initial.ConfigurationRevision,
		Confirmed:                     api.UpdateTournamentConfigurationRequestConfirmed(true),
		Reason:                        "set explicit operator defaults before execution",
		SwissDefault: api.TournamentConfigurationStageDefaultInput{
			Mode: api.CategoryModeAdmin, Categories: []api.Category{selectedCategory},
		},
		SemifinalDefault: api.TournamentConfigurationStageDefaultInput{
			Mode: api.CategoryModeRandom, Categories: []api.Category{selectedCategory},
		},
		UnlockIntents: []api.ConfigurationUnlockIntent{},
	})
	require.NoError(t, err)
	commandID := uuid.New()
	request, response = doTournamentFlowJSON(
		t, fixture, http.MethodPatch, path, string(body), adminSession(adminToken), commandID, "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	evidence := decodeJSON[api.TournamentConfigurationMutationEvidence](t, response)
	require.Equal(t, commandID, evidence.CommandId)
	require.Equal(t, initial.ConfigurationRevision, evidence.PreviousConfigurationRevision)
	require.Equal(t, initial.ConfigurationRevision+1, evidence.NextConfigurationRevision)

	request, response = doTournamentFlowJSON(
		t, fixture, http.MethodGet, path, "", adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	updated := decodeJSON[api.TournamentConfiguration](t, response)
	require.Equal(t, initial.ConfigurationRevision+1, updated.ConfigurationRevision)
	require.Equal(t, api.CategoryModeAdmin, updated.SwissDefault.Mode)
	require.Equal(t, []api.Category{selectedCategory}, updated.SwissDefault.Categories)
	require.Equal(t, api.CategoryModeRandom, updated.SemifinalDefault.Mode)
	require.Equal(t, []api.Category{selectedCategory}, updated.SemifinalDefault.Categories)

	request, response = doTournamentFlowJSON(
		t, fixture, http.MethodPatch, path, string(body), adminSession(adminToken), commandID, "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	require.Equal(t, evidence, decodeJSON[api.TournamentConfigurationMutationEvidence](t, response))
}

func TestTournamentSeriesConfigurationRebuildsUnstartedAssignmentThroughProductionHTTPAndPostgres(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	prepareCreateToChampionContent(ctx, t)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	content := getTournamentContentThroughREST(t, fixture, adminToken)
	players := joinTournamentFlowPlayers(t, fixture, 4)
	created := createTournamentThroughREST(t, fixture, adminToken, content.ContentRevision, "configuration-series")
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
	preflight := runTournamentRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
	lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
	startSwissThroughREST(t, fixture, adminToken, created.Id)
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	execution := tournamentadminexecution.NewExecutionWorkflow(tournamentadminexecution.ExecutionWorkflowDependencies{
		Transactions: fixture.mgr, Repository: executionrepo.NewRepository(fixture.mgr, resultauthority.FinalizeProjection),
	})
	_, err := execution.ConfigurePairings(ctx, pairingusecase.PairingCommand{
		CommandScope:               adminoperation.CommandScope{Operator: adminoperation.OperatorIdentity{ActorID: uuid.New()}, TournamentID: created.Id, CommandID: uuid.New()},
		ExpectedProjectionRevision: snapshot.NextCursor.ProjectionRevision, RoundNumber: 1,
		PairingMode: pairingusecase.PairingModeAutomatic, CategoryMode: domain.CategoryModeRandom,
		Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryForensics},
	})
	require.NoError(t, err)

	configurationPath := "/api/v1/admin/tournaments/" + created.Id.String() + "/configuration"
	request, response := doTournamentFlowJSON(t, fixture, http.MethodGet, configurationPath, "", adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	authority := decodeJSON[api.TournamentConfiguration](t, response)
	require.NotEmpty(t, authority.Series)
	series := authority.Series[0]
	require.NotEmpty(t, series.UnlockIntents)
	selected := api.CategoryCrypto
	if len(series.Categories) == 1 && series.Categories[0] == api.CategoryCrypto {
		selected = api.CategoryWeb
	}
	body, err := json.Marshal(api.UpdateTournamentSeriesConfigurationRequest{
		ExpectedProjectionRevision: authority.ProjectionRevision, ExpectedSeriesRevision: series.Revision,
		Confirmed: api.UpdateTournamentSeriesConfigurationRequestConfirmed(true), Reason: "replace one unstarted Series category",
		Mode: api.CategoryModeAdmin, Categories: []api.Category{selected}, UnlockIntents: series.UnlockIntents,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + created.Id.String() + "/series/" + series.Id.String() + "/configuration"
	request, response = doTournamentFlowJSON(t, fixture, http.MethodPatch, path, string(body), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	evidence := decodeJSON[api.TournamentConfigurationMutationEvidence](t, response)
	require.Contains(t, evidence.SupersededArtifactIds, series.Id)
	require.Len(t, evidence.RebuiltArtifactIds, 1)

	request, response = doTournamentFlowJSON(t, fixture, http.MethodGet, configurationPath, "", adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	updated := decodeJSON[api.TournamentConfiguration](t, response)
	found := false
	for _, candidate := range updated.Series {
		require.NotEqual(t, series.Id, candidate.Id)
		if candidate.Id == evidence.RebuiltArtifactIds[0] {
			found = true
			require.Equal(t, api.CategoryModeAdmin, candidate.Mode)
			require.Equal(t, []api.Category{selected}, candidate.Categories)
		}
	}
	require.True(t, found)
}

func TestTournamentSeriesConfigurationSuccessorStartsExistingWaveThroughProductionHTTPAndPostgres(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		timeLimit int
	}{
		{name: "catalog-60", timeLimit: 60},
		{name: "catalog-90", timeLimit: 90},
		{name: "catalog-180", timeLimit: 180},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testTournamentSeriesConfigurationSuccessorStartsExistingWave(t, testCase.timeLimit)
		})
	}
}

func testTournamentSeriesConfigurationSuccessorStartsExistingWave(t *testing.T, normalTaskTimeLimit int) {
	flow := newSwissCategoryFlowWithNormalTaskTimeLimit(t, "series-successor-wave-start", normalTaskTimeLimit)
	beforePairing := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	round := configureSwissCategoryPairingsThroughREST(
		t, flow.fixture, flow.adminToken, flow.tournamentID,
		beforePairing.NextCursor.ProjectionRevision, 1, api.CategoryModeRandom,
		[]api.Category{api.CategoryWeb}, uuid.New(),
	)
	paired := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	plannedWave := findProductionSwissWave(t, paired, round)

	configurationPath := "/api/v1/admin/tournaments/" + flow.tournamentID.String() + "/configuration"
	request, response := doTournamentFlowJSON(
		t, flow.fixture, http.MethodGet, configurationPath, "", adminSession(flow.adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	flow.fixture.validateResponse(t, request, response)
	configuration := decodeJSON[api.TournamentConfiguration](t, response)
	require.NotEmpty(t, configuration.Series)
	series := configuration.Series[0]
	require.NotEmpty(t, series.UnlockIntents)
	selected := api.CategoryCrypto
	if len(series.Categories) == 1 && series.Categories[0] == selected {
		selected = api.CategoryWeb
	}
	body, err := json.Marshal(api.UpdateTournamentSeriesConfigurationRequest{
		ExpectedProjectionRevision: configuration.ProjectionRevision,
		ExpectedSeriesRevision:     series.Revision,
		Confirmed:                  api.UpdateTournamentSeriesConfigurationRequestConfirmed(true),
		Reason:                     "replace one Series category before starting its existing Wave",
		Mode:                       api.CategoryModeAdmin,
		Categories:                 []api.Category{selected},
		UnlockIntents:              series.UnlockIntents,
	})
	require.NoError(t, err)
	seriesPath := "/api/v1/admin/tournaments/" + flow.tournamentID.String() +
		"/series/" + series.Id.String() + "/configuration"
	request, response = doTournamentFlowJSON(
		t, flow.fixture, http.MethodPatch, seriesPath, string(body), adminSession(flow.adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	flow.fixture.validateResponse(t, request, response)
	evidence := decodeJSON[api.TournamentConfigurationMutationEvidence](t, response)
	require.Contains(t, evidence.SupersededArtifactIds, series.Id)
	require.Len(t, evidence.RebuiltArtifactIds, 1)
	successorID := evidence.RebuiltArtifactIds[0]

	revised := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	revisedWave := findProductionWaveByID(t, revised, plannedWave.Id)
	var successorMemberCount, predecessorMemberCount int
	for _, member := range revisedWave.Members {
		if member.SeriesId == nil {
			continue
		}
		if *member.SeriesId == successorID {
			successorMemberCount++
		}
		if *member.SeriesId == series.Id {
			predecessorMemberCount++
		}
	}
	require.Equal(t, 2, successorMemberCount)
	require.Zero(t, predecessorMemberCount)

	opened := controlProductionWaveThroughREST(
		t, flow.fixture, flow.adminToken, flow.tournamentID, revisedWave.Id,
		revised.NextCursor.ProjectionRevision, api.WaveControlRequestActionOpenReadyWindow,
	)
	require.Equal(t, api.WaveStateReadyWindowOpen, opened.State)
	for _, member := range opened.Members {
		player, ok := flow.playersByParticipant[member.ParticipantId]
		require.True(t, ok, "missing player for participant %s", member.ParticipantId)
		participant := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, player)
		readyBody, marshalErr := json.Marshal(api.ParticipantReadyRequest{
			ExpectedProjectionRevision: participant.NextCursor.ProjectionRevision,
			Ready:                      true,
		})
		require.NoError(t, marshalErr)
		readyPath := "/api/v1/tournaments/" + flow.tournamentID.String() +
			"/participant/waves/" + opened.Id.String() + "/ready"
		readyRequest, readyResponse := doTournamentFlowJSON(
			t, flow.fixture, http.MethodPost, readyPath, string(readyBody),
			cookieSession(player.session.String()), uuid.New(), player.csrf,
		)
		require.Equal(t, http.StatusOK, readyResponse.Code, readyResponse.Body.String())
		flow.fixture.validateResponse(t, readyRequest, readyResponse)
	}
	readySnapshot := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	readyWave := findProductionWaveByID(t, readySnapshot, opened.Id)
	require.Equal(t, api.WaveStateReady, readyWave.State)

	clock := tournamentFlowRuntimeForFixture(t, flow.fixture).clock
	startNow := clock.Now().Add(123 * time.Nanosecond)
	require.NotZero(t, startNow.Nanosecond()%int(time.Microsecond))
	clock.FreezeAt(startNow)
	startPath := "/api/v1/admin/tournaments/" + flow.tournamentID.String() +
		"/waves/" + readyWave.Id.String() + "/actions"
	staleBody, err := json.Marshal(api.WaveControlRequest{
		ExpectedProjectionRevision: readySnapshot.NextCursor.ProjectionRevision + 1,
		Action:                     api.WaveControlRequestActionStart,
		Confirmed:                  true,
	})
	require.NoError(t, err)
	request, response = doTournamentFlowJSON(
		t, flow.fixture, http.MethodPost, startPath, string(staleBody),
		adminSession(flow.adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	flow.fixture.validateResponse(t, request, response)
	afterConflict := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	afterConflictWave := findProductionWaveByID(t, afterConflict, readyWave.Id)
	require.Equal(t, api.WaveStateReady, afterConflictWave.State)
	require.Equal(t, readySnapshot.NextCursor.ProjectionRevision, afterConflict.NextCursor.ProjectionRevision)

	started := controlProductionWaveThroughREST(
		t, flow.fixture, flow.adminToken, flow.tournamentID, readyWave.Id,
		afterConflict.NextCursor.ProjectionRevision, api.WaveControlRequestActionStart,
	)
	require.Equal(t, api.WaveStateActive, started.State)
	require.NotNil(t, started.StartedAt)
	for _, member := range started.Members {
		player, ok := flow.playersByParticipant[member.ParticipantId]
		require.True(t, ok, "missing player for participant %s", member.ParticipantId)
		participant := participantSnapshotThroughREST(t, flow.fixture, flow.tournamentID, player)
		require.NotNil(t, participant.Assignment, "participant %s has no active assignment", member.ParticipantId)
		require.Equal(t, int32(normalTaskTimeLimit), participant.Assignment.ActiveSnapshot.TimeLimit)
		require.NotNil(t, participant.Assignment.Context.StartedAt)
		require.NotNil(t, participant.Assignment.Context.EffectiveDeadline)
		require.Equal(t, *started.StartedAt, *participant.Assignment.Context.StartedAt)
		require.Equal(
			t,
			started.StartedAt.Add(domain.TournamentTaskDuration),
			*participant.Assignment.Context.EffectiveDeadline,
		)
	}
}

func TestTournamentManualRoundRevisionRebuildsPairingsThroughProductionHTTPAndPostgres(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })
	prepareCreateToChampionContent(ctx, t)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	content := getTournamentContentThroughREST(t, fixture, adminToken)
	players := joinTournamentFlowPlayers(t, fixture, 4)
	created := createTournamentThroughREST(t, fixture, adminToken, content.ContentRevision, "configuration-round")
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
	preflight := runTournamentRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
	lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
	startSwissThroughREST(t, fixture, adminToken, created.Id)
	executionRepository := executionrepo.NewRepository(fixture.mgr, resultauthority.FinalizeProjection)
	pairingAuthority, err := executionRepository.LockPairingAuthority(ctx, created.Id)
	require.NoError(t, err)
	require.Len(t, pairingAuthority.Participants, 4)
	pairs := []pairingusecase.ParticipantPair{
		{FirstParticipantID: pairingAuthority.Participants[0].ID, SecondParticipantID: pairingAuthority.Participants[1].ID},
		{FirstParticipantID: pairingAuthority.Participants[2].ID, SecondParticipantID: pairingAuthority.Participants[3].ID},
	}
	execution := tournamentadminexecution.NewExecutionWorkflow(tournamentadminexecution.ExecutionWorkflowDependencies{Transactions: fixture.mgr, Repository: executionRepository})
	_, err = execution.ConfigurePairings(ctx, pairingusecase.PairingCommand{
		CommandScope:               adminoperation.CommandScope{Operator: adminoperation.OperatorIdentity{ActorID: uuid.New()}, TournamentID: created.Id, CommandID: uuid.New()},
		ExpectedProjectionRevision: pairingAuthority.ProjectionRevision, RoundNumber: 1,
		PairingMode: pairingusecase.PairingModeManual, CategoryMode: domain.CategoryModeRandom,
		Categories:     []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryForensics},
		ManualPairings: pairs, ManualPairingsProvided: true,
	})
	require.NoError(t, err)
	configurationPath := "/api/v1/admin/tournaments/" + created.Id.String() + "/configuration"
	request, response := doTournamentFlowJSON(t, fixture, http.MethodGet, configurationPath, "", adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	authority := decodeJSON[api.TournamentConfiguration](t, response)
	require.Len(t, authority.Rounds, 1)
	round := authority.Rounds[0]
	require.Len(t, round.Pairings, 2)
	require.NotEmpty(t, round.UnlockIntents)
	revisedPairs := []api.ConfigurationParticipantPair{
		{FirstParticipantId: round.Pairings[0].FirstParticipantId, SecondParticipantId: round.Pairings[1].FirstParticipantId},
		{FirstParticipantId: round.Pairings[0].SecondParticipantId, SecondParticipantId: round.Pairings[1].SecondParticipantId},
	}
	for index := range revisedPairs {
		if revisedPairs[index].FirstParticipantId.String() > revisedPairs[index].SecondParticipantId.String() {
			revisedPairs[index].FirstParticipantId, revisedPairs[index].SecondParticipantId =
				revisedPairs[index].SecondParticipantId, revisedPairs[index].FirstParticipantId
		}
	}
	body, err := json.Marshal(api.ReplaceTournamentSwissRoundConfigurationRequest{
		ExpectedProjectionRevision: authority.ProjectionRevision, ExpectedRoundRevision: round.Revision,
		Confirmed: api.ReplaceTournamentSwissRoundConfigurationRequestConfirmed(true), Reason: "correct manual pairings before Wave start",
		Mode: api.CategoryModeAdmin, Categories: []api.Category{api.CategoryWeb}, ManualPairings: &revisedPairs,
		UnlockIntents: round.UnlockIntents,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + created.Id.String() + "/swiss/rounds/1"
	request, response = doTournamentFlowJSON(t, fixture, http.MethodPut, path, string(body), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	evidence := decodeJSON[api.TournamentConfigurationMutationEvidence](t, response)
	require.Len(t, evidence.RebuiltArtifactIds, 3)
	request, response = doTournamentFlowJSON(t, fixture, http.MethodGet, configurationPath, "", adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	updated := decodeJSON[api.TournamentConfiguration](t, response)
	require.Equal(t, round.Revision+1, updated.Rounds[0].Revision)
	require.ElementsMatch(t, revisedPairs, updated.Rounds[0].Pairings)
}

func TestTournamentOddSwissRoundRevisionRebuildsByeThroughProductionHTTPAndPostgres(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	catalog := prepareCreateToChampionContentForRosterSize(ctx, t, createToChampionNormalTaskCount, 5)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	players := joinTournamentFlowPlayers(t, fixture, 5)
	created := createTournamentWithRosterSizeThroughREST(t, fixture, adminToken, catalog.revision, "configuration-odd-round", 5)
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
	preflight := runTournamentRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
	lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
	startSwissThroughREST(t, fixture, adminToken, created.Id)
	connectTournamentParticipantsThroughProduction(t, fixture, created.Id, players)

	participants := make(map[int32]uuid.UUID, len(roster.Participants))
	for _, participant := range roster.Participants {
		participants[participant.Seed] = participant.Id
	}
	require.Len(t, participants, 5)
	for seed, participantID := range participants {
		require.NotEqual(t, uuid.Nil, participantID, "seed %d participant", seed)
	}
	a, b, c, d, e := participants[2], participants[3], participants[4], participants[5], participants[1]

	beforePairing := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	invalidByeBody, err := json.Marshal(api.PairingConfigurationRequest{
		Categories:                 []api.Category{api.CategoryWeb},
		CategoryMode:               api.CategoryModeAdmin,
		ExpectedProjectionRevision: beforePairing.NextCursor.ProjectionRevision,
		ManualByeParticipantId:     &a,
		ManualPairings: &[]api.ManualPairInput{
			{FirstParticipantId: a, SecondParticipantId: b},
			{FirstParticipantId: c, SecondParticipantId: d},
		},
		PairingMode: api.Manual, RoundNumber: 1,
	})
	require.NoError(t, err)
	invalidByeRequest, invalidByeResponse := doTournamentFlowJSON(
		t, fixture, http.MethodPost, "/api/v1/admin/tournaments/"+created.Id.String()+"/pairings",
		string(invalidByeBody), adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusBadRequest, invalidByeResponse.Code, invalidByeResponse.Body.String())
	fixture.validateResponse(t, invalidByeRequest, invalidByeResponse)
	invalidSnapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	require.Empty(t, invalidSnapshot.Series)
	require.Empty(t, invalidSnapshot.Waves)
	require.Zero(t, swissPairingCommandCount(t, created.Id))

	initialPairings := []api.ManualPairInput{
		{FirstParticipantId: a, SecondParticipantId: b},
		{FirstParticipantId: c, SecondParticipantId: d},
	}
	initialBody, err := json.Marshal(api.PairingConfigurationRequest{
		Categories:                 []api.Category{api.CategoryWeb},
		CategoryMode:               api.CategoryModeAdmin,
		ExpectedProjectionRevision: beforePairing.NextCursor.ProjectionRevision,
		ManualByeParticipantId:     &e,
		ManualPairings:             &initialPairings,
		PairingMode:                api.Manual,
		RoundNumber:                1,
	})
	require.NoError(t, err)
	initialCommandID := uuid.New()
	initialRequest, initialResponse := doTournamentFlowJSON(
		t, fixture, http.MethodPost, "/api/v1/admin/tournaments/"+created.Id.String()+"/pairings",
		string(initialBody), adminSession(adminToken), initialCommandID, "",
	)
	require.Equal(t, http.StatusOK, initialResponse.Code, initialResponse.Body.String())
	fixture.validateResponse(t, initialRequest, initialResponse)
	initialRound := decodeJSON[api.SwissRound](t, initialResponse)
	require.Len(t, initialRound.Pairings, 2)
	require.NotNil(t, initialRound.Bye)
	require.Equal(t, e, initialRound.Bye.ParticipantId)
	require.ElementsMatch(t, []uuid.UUID{a, b, c, d, e}, initialRound.RosterParticipantIds)
	require.Equal(t, 1, swissPairingCommandCount(t, created.Id))

	configurationPath := "/api/v1/admin/tournaments/" + created.Id.String() + "/configuration"
	readConfiguration := func() api.TournamentConfiguration {
		t.Helper()
		request, response := doTournamentFlowJSON(
			t, fixture, http.MethodGet, configurationPath, "", adminSession(adminToken), uuid.New(), "",
		)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		fixture.validateResponse(t, request, response)
		return decodeJSON[api.TournamentConfiguration](t, response)
	}
	configuration := readConfiguration()
	initialConfigurationRound := configurationRoundForNumber(t, configuration, 1)
	require.Equal(t, e, *initialConfigurationRound.ByeParticipantId)
	require.ElementsMatch(t, configurationPairKeys([]api.ConfigurationParticipantPair{
		{FirstParticipantId: a, SecondParticipantId: b},
		{FirstParticipantId: c, SecondParticipantId: d},
	}), configurationPairKeys(initialConfigurationRound.Pairings))
	initialSQL := readOddSwissRoundSQLState(t, created.Id, roster.Id, initialRound.Id)
	require.Equal(t, 1, initialSQL.byeCount)
	require.Equal(t, 1, initialSQL.waveLinkCount)

	revisedPairings := []api.ConfigurationParticipantPair{
		{FirstParticipantId: a, SecondParticipantId: e},
		{FirstParticipantId: b, SecondParticipantId: c},
	}
	revisedBody, err := json.Marshal(api.ReplaceTournamentSwissRoundConfigurationRequest{
		Categories:                 []api.Category{api.CategoryWeb},
		Confirmed:                  api.ReplaceTournamentSwissRoundConfigurationRequestConfirmed(true),
		ExpectedProjectionRevision: configuration.ProjectionRevision,
		ExpectedRoundRevision:      initialConfigurationRound.Revision,
		ManualByeParticipantId:     &d,
		ManualPairings:             &revisedPairings,
		Mode:                       api.CategoryModeAdmin,
		Reason:                     "correct odd-roster manual pairings and bye before Wave start",
		UnlockIntents:              initialConfigurationRound.UnlockIntents,
	})
	require.NoError(t, err)
	revisedCommandID := uuid.New()
	revisedRequest, revisedResponse := doTournamentFlowJSON(
		t, fixture, http.MethodPut, "/api/v1/admin/tournaments/"+created.Id.String()+"/swiss/rounds/1",
		string(revisedBody), adminSession(adminToken), revisedCommandID, "",
	)
	require.Equal(t, http.StatusOK, revisedResponse.Code, revisedResponse.Body.String())
	fixture.validateResponse(t, revisedRequest, revisedResponse)
	revisedEvidence := decodeJSON[api.TournamentConfigurationMutationEvidence](t, revisedResponse)
	require.Equal(t, revisedCommandID, revisedEvidence.CommandId)
	require.Contains(t, revisedEvidence.SupersededArtifactIds, initialConfigurationRound.Id)
	require.Len(t, revisedEvidence.RebuiltArtifactIds, 3)

	updatedConfiguration := readConfiguration()
	updatedRound := configurationRoundForNumber(t, updatedConfiguration, 1)
	require.Equal(t, initialConfigurationRound.Revision+1, updatedRound.Revision)
	require.Equal(t, d, *updatedRound.ByeParticipantId)
	require.ElementsMatch(t, configurationPairKeys(revisedPairings), configurationPairKeys(updatedRound.Pairings))

	// The same authenticated configuration command returns its immutable evidence.
	replayRequest, replayResponse := doTournamentFlowJSON(
		t, fixture, http.MethodPut, "/api/v1/admin/tournaments/"+created.Id.String()+"/swiss/rounds/1",
		string(revisedBody), adminSession(adminToken), revisedCommandID, "",
	)
	require.Equal(t, http.StatusOK, replayResponse.Code, replayResponse.Body.String())
	fixture.validateResponse(t, replayRequest, replayResponse)
	require.Equal(t, revisedEvidence, decodeJSON[api.TournamentConfigurationMutationEvidence](t, replayResponse))
	require.Equal(t, updatedConfiguration, readConfiguration())

	// A stale projection and round revision must fail before a second graph is written.
	staleRequest, staleResponse := doTournamentFlowJSON(
		t, fixture, http.MethodPut, "/api/v1/admin/tournaments/"+created.Id.String()+"/swiss/rounds/1",
		string(revisedBody), adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusConflict, staleResponse.Code, staleResponse.Body.String())
	fixture.validateResponse(t, staleRequest, staleResponse)
	require.Equal(t, updatedConfiguration, readConfiguration())
	require.Equal(t, initialSQL, readOddSwissRoundSQLState(t, created.Id, roster.Id, initialRound.Id))

	// A bye participant already covered by a pairing is an invalid/repeated bye.
	invalidRepeatedByeBody, err := json.Marshal(api.ReplaceTournamentSwissRoundConfigurationRequest{
		Categories:                 []api.Category{api.CategoryWeb},
		Confirmed:                  api.ReplaceTournamentSwissRoundConfigurationRequestConfirmed(true),
		ExpectedProjectionRevision: updatedConfiguration.ProjectionRevision,
		ExpectedRoundRevision:      updatedRound.Revision,
		ManualByeParticipantId:     &e,
		ManualPairings:             &revisedPairings,
		Mode:                       api.CategoryModeAdmin,
		Reason:                     "reject a bye already used by a pairing",
		UnlockIntents:              updatedRound.UnlockIntents,
	})
	require.NoError(t, err)
	invalidRepeatedByeRequest, invalidRepeatedByeResponse := doTournamentFlowJSON(
		t, fixture, http.MethodPut, "/api/v1/admin/tournaments/"+created.Id.String()+"/swiss/rounds/1",
		string(invalidRepeatedByeBody), adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusBadRequest, invalidRepeatedByeResponse.Code, invalidRepeatedByeResponse.Body.String())
	fixture.validateResponse(t, invalidRepeatedByeRequest, invalidRepeatedByeResponse)
	require.Equal(t, updatedConfiguration, readConfiguration())

	flow := swissCategoryFlow{
		fixture:      fixture,
		adminToken:   adminToken,
		tournamentID: created.Id,
		playersByParticipant: productionPlayersByParticipant(t, roster, func() map[uuid.UUID]tournamentFlowPlayer {
			playersByID := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
			for _, player := range players {
				playersByID[player.id] = player
			}
			return playersByID
		}()),
		catalog: catalog,
	}
	_, err = snapshotrepo.NewTournamentAdminSnapshotPostgres(fixture.mgr).GetOperatorSnapshot(ctx, snapshotusecase.SnapshotQuery{
		Operator: adminoperation.OperatorIdentity{ActorID: uuid.New()}, TournamentID: created.Id,
	})
	require.NoError(t, err, "revised odd Swiss graph must load through the production snapshot repository")
	participantSnapshots := tournamentsnapshotrepo.NewTournamentSnapshotPostgres(fixture.mgr)
	for participantID, player := range flow.playersByParticipant {
		_, snapshotErr := participantSnapshots.ParticipantSnapshot(ctx, inbound.ParticipantSnapshotQuery{
			TournamentID: created.Id, PlayerID: player.id,
		})
		require.NoError(t, snapshotErr, "participant %s must load the revised Swiss graph", participantID)
	}
	plannedWave := findOddSwissWave(
		t, tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id), updatedRound.Pairings, d,
	)
	startedWave := openAndStartRevisedOddSwissWaveThroughREST(t, flow, plannedWave)
	require.Equal(t, api.WaveStateActive, startedWave.State)
	startedConfiguration := readConfiguration()
	startedRound := configurationRoundForNumber(t, startedConfiguration, 1)
	require.True(t, startedRound.Started)
	require.Equal(t, d, *startedRound.ByeParticipantId)

	publicSnapshotRequest, publicSnapshotResponse := doTournamentFlowJSON(
		t, fixture, http.MethodGet, "/api/v1/tournaments/"+created.Id.String()+"/snapshot",
		"", "", uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, publicSnapshotResponse.Code, publicSnapshotResponse.Body.String())
	fixture.validateResponse(t, publicSnapshotRequest, publicSnapshotResponse)
	publicSnapshot := decodeJSON[api.PublicRecoverySnapshot](t, publicSnapshotResponse)
	require.Empty(t, publicSnapshot.Scoreboard.Entries, "in-progress Swiss points stay out of the published standings")
	expectedPublicSeries := make(map[uuid.UUID]struct{}, 2)
	for _, member := range startedWave.Members {
		if member.SeriesId != nil {
			expectedPublicSeries[*member.SeriesId] = struct{}{}
		}
	}
	require.Len(t, expectedPublicSeries, 2)
	require.Len(t, publicSnapshot.LiveSeries, len(expectedPublicSeries))
	for _, series := range publicSnapshot.LiveSeries {
		_, found := expectedPublicSeries[series.SeriesId]
		require.True(t, found, "public snapshot exposed a superseded Swiss Series %s", series.SeriesId)
		require.Equal(t, api.PublicLiveSeriesStageSwiss, series.Stage)
		require.NotNil(t, series.RoundNumber)
		require.Equal(t, int32(1), *series.RoundNumber)
		require.Nil(t, series.ScheduledAt, "no authoritative schedule must remain explicitly absent")
	}

	byeEvidence := readOddSwissByeEvidence(t, created.Id, roster.Id, initialRound.Id, startedWave.Id)
	require.Equal(t, d, byeEvidence.waveByeParticipant)
	require.True(t, byeEvidence.waveByeRevision.Valid)
	require.Equal(t, 1, byeEvidence.ledgerCount)
	require.Equal(t, 1, byeEvidence.ledgerPoints)
	require.Equal(t, d, byeEvidence.ledgerParticipant)
	require.Equal(t, byeEvidence.waveByeRevision, byeEvidence.ledgerByeRevision)
	require.Equal(t, int16(1), byeEvidence.ledgerPointsAwarded)
	require.Equal(t, int16(1), byeEvidence.roundNumber)

	// The active round's bye must be D and the pre-start E row must not survive as
	// a second active bye. The pairings and command graph remain one round.
	var activeByeParticipant uuid.UUID
	var activeByePoints int16
	err = sharedPool.QueryRow(ctx, `SELECT participant_id, points_awarded
		FROM swiss_byes WHERE round_id = $1 AND roster_id = $2`, initialRound.Id, roster.Id).Scan(&activeByeParticipant, &activeByePoints)
	require.NoError(t, err)
	require.Equal(t, d, activeByeParticipant)
	require.Equal(t, int16(1), activeByePoints)
	require.Equal(t, 1, swissPairingCommandCount(t, created.Id))

	// Once Wave start has crossed the cutoff, a valid-looking replacement is an
	// atomic no-op: neither configuration nor any SQL child graph may change.
	beforeCutoffConfiguration := readConfiguration()
	beforeCutoffSQL := readOddSwissRoundSQLState(t, created.Id, roster.Id, initialRound.Id)
	cutoffPairings := []api.ConfigurationParticipantPair{
		{FirstParticipantId: a, SecondParticipantId: d},
		{FirstParticipantId: b, SecondParticipantId: e},
	}
	cutoffBody, err := json.Marshal(api.ReplaceTournamentSwissRoundConfigurationRequest{
		Categories:                 []api.Category{api.CategoryWeb},
		Confirmed:                  api.ReplaceTournamentSwissRoundConfigurationRequestConfirmed(true),
		ExpectedProjectionRevision: beforeCutoffConfiguration.ProjectionRevision,
		ExpectedRoundRevision:      startedRound.Revision,
		ManualByeParticipantId:     &c,
		ManualPairings:             &cutoffPairings,
		Mode:                       api.CategoryModeAdmin,
		Reason:                     "reject Swiss round edit after Wave start",
		UnlockIntents:              startedRound.UnlockIntents,
	})
	require.NoError(t, err)
	cutoffRequest, cutoffResponse := doTournamentFlowJSON(
		t, fixture, http.MethodPut, "/api/v1/admin/tournaments/"+created.Id.String()+"/swiss/rounds/1",
		string(cutoffBody), adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusConflict, cutoffResponse.Code, cutoffResponse.Body.String())
	fixture.validateResponse(t, cutoffRequest, cutoffResponse)
	require.Equal(t, beforeCutoffConfiguration, readConfiguration())
	require.Equal(t, beforeCutoffSQL, readOddSwissRoundSQLState(t, created.Id, roster.Id, initialRound.Id))
}

type oddSwissRoundSQLState struct {
	commandCount    int
	pairingCount    int
	memberCount     int
	historyCount    int
	byeCount        int
	waveLinkCount   int
	byeLedgerCount  int
	byeLedgerPoints int
}

func readOddSwissRoundSQLState(t *testing.T, tournamentID, rosterID, roundID uuid.UUID) oddSwissRoundSQLState {
	t.Helper()
	ctx := context.Background()
	state := oddSwissRoundSQLState{}
	err := sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM swiss_pairing_commands WHERE tournament_id = $1 AND roster_id = $2),
			(SELECT count(*) FROM swiss_pairings WHERE round_id = $3 AND roster_id = $2),
			(SELECT count(*) FROM swiss_pairing_members WHERE round_id = $3 AND roster_id = $2),
			(SELECT count(*) FROM swiss_opponent_history WHERE round_id = $3 AND roster_id = $2),
			(SELECT count(*) FROM swiss_byes WHERE round_id = $3 AND roster_id = $2),
			(SELECT count(*) FROM swiss_wave_links WHERE round_id = $3 AND roster_id = $2),
			(SELECT count(*) FROM swiss_point_ledger_entries
				WHERE tournament_id = $1 AND roster_id = $2 AND round_id = $3 AND source_kind = 'bye'),
			(SELECT COALESCE(sum(points), 0) FROM swiss_point_ledger_entries
				WHERE tournament_id = $1 AND roster_id = $2 AND round_id = $3 AND source_kind = 'bye')`,
		tournamentID, rosterID, roundID,
	).Scan(
		&state.commandCount, &state.pairingCount, &state.memberCount, &state.historyCount,
		&state.byeCount, &state.waveLinkCount, &state.byeLedgerCount, &state.byeLedgerPoints,
	)
	require.NoError(t, err)
	return state
}

type oddSwissByeEvidence struct {
	waveByeParticipant  uuid.UUID
	waveByeRevision     uuid.NullUUID
	ledgerCount         int
	ledgerPoints        int
	ledgerParticipant   uuid.UUID
	ledgerByeRevision   uuid.NullUUID
	ledgerPointsAwarded int16
	roundNumber         int16
}

func readOddSwissByeEvidence(t *testing.T, tournamentID, rosterID, roundID, waveID uuid.UUID) oddSwissByeEvidence {
	t.Helper()
	ctx := context.Background()
	evidence := oddSwissByeEvidence{}
	err := sharedPool.QueryRow(ctx, `SELECT bye_participant_id, bye_revision_id
		FROM swiss_wave_links
		WHERE wave_id = $1 AND tournament_id = $2 AND roster_id = $3 AND round_id = $4`,
		waveID, tournamentID, rosterID, roundID,
	).Scan(&evidence.waveByeParticipant, &evidence.waveByeRevision)
	require.NoError(t, err)
	err = sharedPool.QueryRow(ctx, `SELECT count(*), COALESCE(sum(points), 0)
		FROM swiss_point_ledger_entries
		WHERE tournament_id = $1 AND roster_id = $2 AND round_id = $3 AND source_kind = 'bye'`,
		tournamentID, rosterID, roundID,
	).Scan(&evidence.ledgerCount, &evidence.ledgerPoints)
	require.NoError(t, err)
	require.Equal(t, 1, evidence.ledgerCount)
	err = sharedPool.QueryRow(ctx, `SELECT participant_id, bye_revision_id, points, round_number
		FROM swiss_point_ledger_entries
		WHERE tournament_id = $1 AND roster_id = $2 AND round_id = $3 AND source_kind = 'bye'`,
		tournamentID, rosterID, roundID,
	).Scan(&evidence.ledgerParticipant, &evidence.ledgerByeRevision, &evidence.ledgerPointsAwarded, &evidence.roundNumber)
	require.NoError(t, err)
	return evidence
}

func configurationRoundForNumber(t *testing.T, configuration api.TournamentConfiguration, roundNumber int32) api.TournamentConfigurationRound {
	t.Helper()
	for _, round := range configuration.Rounds {
		if round.RoundNumber == roundNumber {
			return round
		}
	}
	require.FailNow(t, "Swiss configuration round is missing", "round: %d", roundNumber)
	return api.TournamentConfigurationRound{}
}

func configurationPairKeys(pairings []api.ConfigurationParticipantPair) []string {
	keys := make([]string, len(pairings))
	for index, pairing := range pairings {
		keys[index] = productionParticipantPairKey(pairing.FirstParticipantId, pairing.SecondParticipantId)
	}
	return keys
}

func openAndStartRevisedOddSwissWaveThroughREST(t *testing.T, flow swissCategoryFlow, planned api.Wave) api.Wave {
	t.Helper()
	snapshot := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	wave := findProductionWaveByID(t, snapshot, planned.Id)
	wave = controlProductionWaveThroughREST(
		t, flow.fixture, flow.adminToken, flow.tournamentID, wave.Id,
		snapshot.NextCursor.ProjectionRevision, api.WaveControlRequestActionOpenReadyWindow,
	)
	require.Equal(t, api.WaveStateReadyWindowOpen, wave.State)

	repository := tournamentsnapshotrepo.NewTournamentSnapshotPostgres(flow.fixture.mgr)
	for _, member := range wave.Members {
		player, ok := flow.playersByParticipant[member.ParticipantId]
		require.True(t, ok, "missing player for participant %s", member.ParticipantId)
		participant, err := repository.ParticipantSnapshot(t.Context(), inbound.ParticipantSnapshotQuery{
			TournamentID: flow.tournamentID, PlayerID: player.id,
		})
		require.NoError(t, err, "participant %s snapshot after revised Wave ready-window open", member.ParticipantId)
		body, err := json.Marshal(api.ParticipantReadyRequest{
			ExpectedProjectionRevision: participant.Cursor.ProjectionRevision,
			Ready:                      true,
		})
		require.NoError(t, err)
		path := "/api/v1/tournaments/" + flow.tournamentID.String() + "/participant/waves/" + wave.Id.String() + "/ready"
		request, response := doTournamentFlowJSON(
			t, flow.fixture, http.MethodPost, path, string(body), cookieSession(player.session.String()), uuid.New(), player.csrf,
		)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		flow.fixture.validateResponse(t, request, response)
	}

	snapshot = tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	wave = findProductionWaveByID(t, snapshot, wave.Id)
	require.Equal(t, api.WaveStateReady, wave.State)
	executionRepository := executionrepo.NewRepository(flow.fixture.mgr, resultauthority.FinalizeProjection)
	authority, err := executionRepository.LockWaveAuthority(
		t.Context(), flow.tournamentID, wave.Id,
	)
	require.NoError(t, err, "revised odd Swiss Wave authority must load before start")
	require.NotNil(t, authority.View.Wave.ReadyWindow)
	startScope := gamestart.StartScope{
		TournamentID: flow.tournamentID,
		WaveID:       wave.Id,
		WindowID:     authority.View.Wave.ReadyWindow.ID,
	}
	_, err = executionRepository.LoadWaveStartAuthority(t.Context(), startScope)
	require.NoError(t, err, "revised odd Swiss Wave start authority must load before start")
	return controlProductionWaveThroughREST(
		t, flow.fixture, flow.adminToken, flow.tournamentID, wave.Id,
		snapshot.NextCursor.ProjectionRevision, api.WaveControlRequestActionStart,
	)
}

func findOddSwissWave(
	t *testing.T,
	snapshot api.OperatorRecoverySnapshot,
	pairings []api.ConfigurationParticipantPair,
	byeParticipantID uuid.UUID,
) api.Wave {
	t.Helper()
	seriesByPair := make(map[string]uuid.UUID, len(snapshot.Series))
	for _, series := range snapshot.Series {
		seriesByPair[productionParticipantPairKey(series.FirstParticipantId, series.SecondParticipantId)] = series.Id
	}
	targetSeriesIDs := make(map[uuid.UUID]struct{}, len(pairings))
	for _, pairing := range pairings {
		seriesID, ok := seriesByPair[productionParticipantPairKey(pairing.FirstParticipantId, pairing.SecondParticipantId)]
		require.True(t, ok, "pairing %s/%s has no materialized Series", pairing.FirstParticipantId, pairing.SecondParticipantId)
		targetSeriesIDs[seriesID] = struct{}{}
	}
	for _, wave := range snapshot.Waves {
		if len(wave.Members) != len(targetSeriesIDs)*2+1 {
			continue
		}
		seenSeries := make(map[uuid.UUID]struct{}, len(targetSeriesIDs))
		matchedBye := false
		for _, member := range wave.Members {
			if member.SeriesId == nil {
				matchedBye = member.ParticipantId == byeParticipantID
				continue
			}
			if _, ok := targetSeriesIDs[*member.SeriesId]; ok {
				seenSeries[*member.SeriesId] = struct{}{}
			}
		}
		if matchedBye && len(seenSeries) == len(targetSeriesIDs) {
			return wave
		}
	}
	require.FailNow(t, "odd Swiss round has no matching Wave", "bye: %s", byeParticipantID)
	return api.Wave{}
}
