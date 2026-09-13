//go:build integration

package integration_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestSwissNoRepeatThroughProductionHandlers(t *testing.T) {
	flow := newSwissCategoryFlow(t, "strict-no-repeat")
	orderedParticipants := swissNoRepeatOrderedParticipants(t, flow)

	initial := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	roundOne := configureProductionSwissPairingsThroughREST(
		t, flow.fixture, flow.adminToken, flow.tournamentID,
		initial.NextCursor.ProjectionRevision, 1,
	)
	require.Len(t, roundOne.Pairings, 2)

	roundOneWave := findProductionSwissWave(
		t,
		tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID),
		roundOne,
	)
	require.Equal(t, api.WaveStatePlanned, roundOneWave.State)
	assertSwissPrestartTaskSecrecy(t, flow, roundOneWave)
	roundOneWave = openAndStartSwissWaveThroughREST(t, flow, roundOneWave)
	settleProductionSwissWaveThroughREST(
		t, flow.fixture, flow.tournamentID, roundOneWave, flow.playersByParticipant, flow.catalog.flags,
		func(series api.Series) uuid.UUID {
			return productionSwissWinner(t, series, orderedParticipants, false)
		},
	)
	completeSwissWaveThroughREST(t, &flow, roundOneWave)
	assertExactlyOneSwissOfficialResult(t, flow, roundOne, roundOneWave)

	// A manual round-two rematch must fail before the round graph or receipt is written.
	beforeRejected := readSwissNoRepeatState(t, flow)
	manualPairings := swissNoRepeatManualPairings(roundOne)
	manualBody, err := json.Marshal(api.PairingConfigurationRequest{
		Categories:                 []api.Category{api.CategoryWeb, api.CategoryCrypto, api.CategoryForensics},
		CategoryMode:               api.CategoryModeRandom,
		ExpectedProjectionRevision: beforeRejected.projectionRevision,
		ManualPairings:             &manualPairings,
		PairingMode:                api.Manual,
		RoundNumber:                2,
	})
	require.NoError(t, err)
	manualRequest, manualResponse := doTournamentFlowJSON(
		t, flow.fixture, http.MethodPost,
		"/api/v1/admin/tournaments/"+flow.tournamentID.String()+"/pairings",
		string(manualBody), adminSession(flow.adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusBadRequest, manualResponse.Code, manualResponse.Body.String())
	flow.fixture.validateResponse(t, manualRequest, manualResponse)
	assertSwissNoRepeatStateUnchanged(t, flow, beforeRejected)

	// Legacy clients that still send repeat_override must be rejected by the
	// OpenAPI additionalProperties:false contract before handler dispatch.
	legacyBody, err := json.Marshal(map[string]any{
		"categories":                   []api.Category{api.CategoryWeb, api.CategoryCrypto, api.CategoryForensics},
		"category_mode":                api.CategoryModeRandom,
		"expected_projection_revision": beforeRejected.projectionRevision,
		"manual_pairings":              manualPairings,
		"pairing_mode":                 api.Manual,
		"round_number":                 2,
		"repeat_override": map[string]any{
			"confirmed": true,
			"reason":    "legacy repeat override",
		},
	})
	require.NoError(t, err)
	legacyRequest, legacyResponse := doTournamentFlowJSON(
		t, flow.fixture, http.MethodPost,
		"/api/v1/admin/tournaments/"+flow.tournamentID.String()+"/pairings",
		string(legacyBody), adminSession(flow.adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusBadRequest, legacyResponse.Code, legacyResponse.Body.String())
	flow.fixture.validateResponse(t, legacyRequest, legacyResponse)
	assertSwissNoRepeatStateUnchanged(t, flow, beforeRejected)

	roundTwo := configureProductionSwissPairingsThroughREST(
		t, flow.fixture, flow.adminToken, flow.tournamentID,
		beforeRejected.projectionRevision, 2,
	)
	require.Len(t, roundTwo.Pairings, 2)
	assertSwissNoRepeatRoundPairsDisjoint(t, roundOne, roundTwo)
	afterRoundTwo := readSwissNoRepeatState(t, flow)
	require.Equal(t, beforeRejected.commands+1, afterRoundTwo.commands)

	configuration := readSwissNoRepeatConfiguration(t, flow)
	roundTwoConfiguration := swissNoRepeatConfigurationRound(t, configuration, 2)
	require.False(t, roundTwoConfiguration.Started)

	// The round revision endpoint is still pre-start, but it cannot turn round
	// two into a round-one rematch.
	rematchPairs := swissNoRepeatConfigurationPairs(roundOne)
	rematchBody, err := json.Marshal(api.ReplaceTournamentSwissRoundConfigurationRequest{
		Categories:                 append([]api.Category(nil), roundTwoConfiguration.Categories...),
		Confirmed:                  api.ReplaceTournamentSwissRoundConfigurationRequestConfirmed(true),
		ExpectedProjectionRevision: configuration.ProjectionRevision,
		ExpectedRoundRevision:      roundTwoConfiguration.Revision,
		ManualPairings:             &rematchPairs,
		Mode:                       roundTwoConfiguration.Mode,
		Reason:                     "attempt to rematch round one before round two starts",
		UnlockIntents:              append([]api.ConfigurationUnlockIntent(nil), roundTwoConfiguration.UnlockIntents...),
	})
	require.NoError(t, err)
	rematchRequest, rematchResponse := doTournamentFlowJSON(
		t, flow.fixture, http.MethodPut,
		"/api/v1/admin/tournaments/"+flow.tournamentID.String()+"/swiss/rounds/2",
		string(rematchBody), adminSession(flow.adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusBadRequest, rematchResponse.Code, rematchResponse.Body.String())
	flow.fixture.validateResponse(t, rematchRequest, rematchResponse)
	assertSwissNoRepeatStateUnchanged(t, flow, afterRoundTwo)

	// The revision captured before round two is stale after the successful
	// round-two command and must not produce a third graph or receipt.
	staleBody, err := json.Marshal(api.PairingConfigurationRequest{
		Categories:                 []api.Category{api.CategoryWeb, api.CategoryCrypto, api.CategoryForensics},
		CategoryMode:               api.CategoryModeRandom,
		ExpectedProjectionRevision: beforeRejected.projectionRevision,
		PairingMode:                api.Automatic,
		RoundNumber:                2,
	})
	require.NoError(t, err)
	staleRequest, staleResponse := doTournamentFlowJSON(
		t, flow.fixture, http.MethodPost,
		"/api/v1/admin/tournaments/"+flow.tournamentID.String()+"/pairings",
		string(staleBody), adminSession(flow.adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusConflict, staleResponse.Code, staleResponse.Body.String())
	flow.fixture.validateResponse(t, staleRequest, staleResponse)
	assertSwissNoRepeatStateUnchanged(t, flow, afterRoundTwo)

	roundTwoWave := findProductionSwissWave(
		t,
		tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID),
		roundTwo,
	)
	require.Equal(t, api.WaveStatePlanned, roundTwoWave.State)
	assertSwissPrestartTaskSecrecy(t, flow, roundTwoWave)
	roundTwoWave = openAndStartSwissWaveThroughREST(t, flow, roundTwoWave)
	settleProductionSwissWaveThroughREST(
		t, flow.fixture, flow.tournamentID, roundTwoWave, flow.playersByParticipant, flow.catalog.flags,
		func(series api.Series) uuid.UUID {
			return productionSwissWinner(t, series, orderedParticipants, false)
		},
	)
	completeSwissWaveThroughREST(t, &flow, roundTwoWave)
	assertExactlyOneSwissOfficialResult(t, flow, roundTwo, roundTwoWave)
}

type swissNoRepeatState struct {
	commands           int
	projectionRevision int64
	rounds             []api.TournamentConfigurationRound
}

func readSwissNoRepeatState(t *testing.T, flow swissCategoryFlow) swissNoRepeatState {
	t.Helper()
	configuration := readSwissNoRepeatConfiguration(t, flow)
	return swissNoRepeatState{
		commands:           swissPairingCommandCount(t, flow.tournamentID),
		projectionRevision: configuration.ProjectionRevision,
		rounds:             append([]api.TournamentConfigurationRound(nil), configuration.Rounds...),
	}
}

func assertSwissNoRepeatStateUnchanged(t *testing.T, flow swissCategoryFlow, before swissNoRepeatState) {
	t.Helper()
	after := readSwissNoRepeatState(t, flow)
	require.Equal(t, before.commands, after.commands, "rejected request must not create a pairing command")
	require.Equal(t, before.projectionRevision, after.projectionRevision,
		"rejected request must not advance the projection revision")
	require.Equal(t, before.rounds, after.rounds,
		"rejected request must not change round pairings or revisions")
}

func readSwissNoRepeatConfiguration(t *testing.T, flow swissCategoryFlow) api.TournamentConfiguration {
	t.Helper()
	path := "/api/v1/admin/tournaments/" + flow.tournamentID.String() + "/configuration"
	request, response := doTournamentFlowJSON(
		t, flow.fixture, http.MethodGet, path, "", adminSession(flow.adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	flow.fixture.validateResponse(t, request, response)
	return decodeJSON[api.TournamentConfiguration](t, response)
}

func swissNoRepeatConfigurationRound(
	t *testing.T,
	configuration api.TournamentConfiguration,
	roundNumber int32,
) api.TournamentConfigurationRound {
	t.Helper()
	for _, round := range configuration.Rounds {
		if round.RoundNumber == roundNumber {
			return round
		}
	}
	require.FailNow(t, "Swiss configuration round is missing", "round: %d", roundNumber)
	return api.TournamentConfigurationRound{}
}

func swissNoRepeatManualPairings(round api.SwissRound) []api.ManualPairInput {
	pairs := make([]api.ManualPairInput, len(round.Pairings))
	for index, pairing := range round.Pairings {
		pairs[index] = api.ManualPairInput{
			FirstParticipantId:  pairing.FirstParticipantId,
			SecondParticipantId: pairing.SecondParticipantId,
		}
	}
	return pairs
}

func swissNoRepeatConfigurationPairs(round api.SwissRound) []api.ConfigurationParticipantPair {
	pairs := make([]api.ConfigurationParticipantPair, len(round.Pairings))
	for index, pairing := range round.Pairings {
		first, second := pairing.FirstParticipantId, pairing.SecondParticipantId
		if strings.Compare(first.String(), second.String()) > 0 {
			first, second = second, first
		}
		pairs[index] = api.ConfigurationParticipantPair{
			FirstParticipantId:  first,
			SecondParticipantId: second,
		}
	}
	return pairs
}

func assertSwissNoRepeatRoundPairsDisjoint(t *testing.T, first, second api.SwissRound) {
	t.Helper()
	seen := make(map[string]struct{}, len(first.Pairings))
	for _, pairing := range first.Pairings {
		seen[productionParticipantPairKey(pairing.FirstParticipantId, pairing.SecondParticipantId)] = struct{}{}
	}
	for _, pairing := range second.Pairings {
		_, repeated := seen[productionParticipantPairKey(pairing.FirstParticipantId, pairing.SecondParticipantId)]
		require.False(t, repeated, "round two repeated a round-one opponent pair")
	}
}

func swissNoRepeatOrderedParticipants(t *testing.T, flow swissCategoryFlow) []uuid.UUID {
	t.Helper()
	participants := make([]uuid.UUID, 0, len(flow.playersByParticipant))
	for participantID := range flow.playersByParticipant {
		participants = append(participants, participantID)
	}
	slices.SortFunc(participants, func(first, second uuid.UUID) int {
		return strings.Compare(first.String(), second.String())
	})
	return participants
}
