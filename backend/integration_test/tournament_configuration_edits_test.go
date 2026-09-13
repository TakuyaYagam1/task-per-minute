//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
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
	authority, err := postgres.NewTournamentConfigurationPostgres(fixture.mgr).LoadConfiguration(
		ctx,
		tournamentadmin.ConfigurationLoadQuery{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
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
	execution := tournamentadmin.NewExecutionWorkflow(tournamentadmin.ExecutionWorkflowDependencies{
		Transactions: fixture.mgr, Repository: postgres.NewTournamentAdminExecutionPostgres(fixture.mgr),
	})
	_, err := execution.ConfigurePairings(ctx, tournamentadmin.PairingCommand{
		CommandScope:               tournamentadmin.CommandScope{Operator: tournamentadmin.OperatorIdentity{ActorID: uuid.New()}, TournamentID: created.Id, CommandID: uuid.New()},
		ExpectedProjectionRevision: snapshot.NextCursor.ProjectionRevision, RoundNumber: 1,
		PairingMode: tournamentadmin.PairingModeAutomatic, CategoryMode: domain.CategoryModeRandom,
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
	executionRepository := postgres.NewTournamentAdminExecutionPostgres(fixture.mgr)
	pairingAuthority, err := executionRepository.LockPairingAuthority(ctx, created.Id)
	require.NoError(t, err)
	require.Len(t, pairingAuthority.Participants, 4)
	pairs := []tournamentadmin.ParticipantPair{
		{FirstParticipantID: pairingAuthority.Participants[0].ID, SecondParticipantID: pairingAuthority.Participants[1].ID},
		{FirstParticipantID: pairingAuthority.Participants[2].ID, SecondParticipantID: pairingAuthority.Participants[3].ID},
	}
	execution := tournamentadmin.NewExecutionWorkflow(tournamentadmin.ExecutionWorkflowDependencies{Transactions: fixture.mgr, Repository: executionRepository})
	_, err = execution.ConfigurePairings(ctx, tournamentadmin.PairingCommand{
		CommandScope:               tournamentadmin.CommandScope{Operator: tournamentadmin.OperatorIdentity{ActorID: uuid.New()}, TournamentID: created.Id, CommandID: uuid.New()},
		ExpectedProjectionRevision: pairingAuthority.ProjectionRevision, RoundNumber: 1,
		PairingMode: tournamentadmin.PairingModeManual, CategoryMode: domain.CategoryModeRandom,
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
