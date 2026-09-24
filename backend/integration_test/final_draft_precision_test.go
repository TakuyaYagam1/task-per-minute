//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	participantdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func TestFinalDraftActionAtTurnTwoClockPrecision(t *testing.T) {
	cases := []struct {
		name             string
		actionTimeOffset time.Duration
		wantStatus       int
	}{
		{
			name:             "nanosecond_before_deadline",
			actionTimeOffset: -time.Second + 123*time.Nanosecond,
			wantStatus:       http.StatusOK,
		},
		{
			name:             "microsecond_before_deadline",
			actionTimeOffset: -time.Second,
			wantStatus:       http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFinalDraftPrecisionScenario(t, tc.actionTimeOffset, tc.wantStatus, false)
		})
	}
}

func TestFinalDraftActionAfterAutomaticFirstBan(t *testing.T) {
	runFinalDraftPrecisionScenario(t, -6*time.Second, http.StatusOK, true)
}

type twoGroupGoldenFixture struct {
	rest         *restFixture
	adminToken   string
	tournamentID uuid.UUID
	catalog      tournamentFlowCatalog
	players      map[uuid.UUID]tournamentFlowPlayer
	ready        api.GoldenOperatorResponse
}

func prepareTwoGroupGoldenThroughREST(t *testing.T) twoGroupGoldenFixture {
	t.Helper()
	ctx := t.Context()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })
	const rosterSize = 4
	catalog := prepareCreateToChampionContent(ctx, t)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	content := getTournamentContentThroughREST(t, fixture, adminToken)
	players := joinTournamentFlowPlayers(t, fixture, rosterSize)
	created := createTournamentWithRosterSizeThroughREST(
		t, fixture, adminToken, content.ContentRevision, "golden-two-groups", rosterSize,
	)
	setTournamentReserveCountThroughREST(t, fixture, adminToken, created.Id, domain.AssignmentReserveCount)
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
	preflight := runFinalDraftRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
	lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
	startSwissThroughREST(t, fixture, adminToken, created.Id)
	connectTournamentParticipantsThroughProduction(t, fixture, created.Id, players)

	// Prefer the participant with fewer wins, as the native Golden fixture
	// does. Four players complete the three real Swiss rounds with two wins
	// each for two players and one win each for the other two: two tie groups.
	wins := make(map[uuid.UUID]int)
	chooseWinner := func(series api.Series) uuid.UUID {
		winner := series.FirstParticipantId
		if wins[series.SecondParticipantId] < wins[winner] {
			winner = series.SecondParticipantId
		}
		wins[winner]++
		return winner
	}
	stage := "Swiss setup for two Golden groups"
	runFinalDraftSwissThroughREST(t, fixture, adminToken, created.Id, players, catalog.flags, 3, 180, &stage, chooseWinner)
	winCounts := make([]int, 0, len(roster.Participants))
	for _, participant := range roster.Participants {
		winCounts = append(winCounts, wins[participant.Id])
	}
	sort.Ints(winCounts)
	require.Equal(t, []int{1, 1, 2, 2}, winCounts)
	projectionRevision := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id).
		NextCursor.ProjectionRevision
	applyTournamentActionThroughREST(t, fixture, adminToken, created.Id, projectionRevision, "start_golden", uuid.New())
	projectionRevision = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id).
		NextCursor.ProjectionRevision
	openBody, err := json.Marshal(api.GoldenOpenRequest{ExpectedProjectionRevision: projectionRevision})
	require.NoError(t, err)
	root := "/api/v1/admin/tournaments/" + created.Id.String() + "/golden"
	openReq, openResp := doTournamentFlowJSON(t, fixture, http.MethodPost, root+"/open", string(openBody),
		adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, openResp.Code)
	fixture.validateResponse(t, openReq, openResp)
	opened := decodeJSON[api.GoldenOperatorResponse](t, openResp)
	require.Len(t, opened.Groups, 2)
	playersByID := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
	for _, player := range players {
		playersByID[player.id] = player
	}
	playersByParticipant := productionPlayersByParticipant(t, roster, playersByID)
	for _, group := range opened.Groups {
		for _, member := range group.Members {
			player := playersByParticipant[member.ParticipantId]
			participant := goldenParticipantThroughREST(t, fixture, created.Id, player)
			ready := setGoldenParticipantReadyThroughREST(t, fixture, created.Id, player, participant)
			require.True(t, ready.Ready)
			require.Nil(t, ready.Task)
		}
	}
	cached := goldenOperatorThroughREST(t, fixture, adminToken, created.Id)
	require.Len(t, cached.Groups, 2)
	for _, group := range cached.Groups {
		require.Equal(t, api.GoldenRuntimeState("ready"), group.State)
		require.Equal(t, cached.Groups[0].RuntimeRevision, group.RuntimeRevision)
	}
	return twoGroupGoldenFixture{
		rest: fixture, adminToken: adminToken, tournamentID: created.Id,
		catalog: catalog, players: playersByParticipant, ready: cached,
	}
}

func TestGoldenStartRefreshesGroupRevision(t *testing.T) {
	setup := prepareTwoGroupGoldenThroughREST(t)
	fixture, adminToken, tournamentID := setup.rest, setup.adminToken, setup.tournamentID
	cached := setup.ready
	root := "/api/v1/admin/tournaments/" + tournamentID.String() + "/golden"
	first := startGoldenGroupWithCurrentRevision(t, fixture, adminToken, tournamentID, cached.Groups[0].GroupId)
	firstActive := goldenOperatorGroupByID(t, first, cached.Groups[0].GroupId)
	require.Equal(t, api.GoldenRuntimeState("active"), firstActive.State)
	require.Equal(t, cached.Groups[0].RuntimeRevision+1, firstActive.RuntimeRevision)

	// A different command cannot reuse the other group's pre-start revision.
	stale := cached.Groups[1]
	staleBody, err := json.Marshal(api.GoldenStartRequest{
		ExpectedRuntimeRevision: stale.RuntimeRevision, ReadyWindowId: stale.ReadyWindowId,
	})
	require.NoError(t, err)
	staleReq, staleResp := doTournamentFlowJSON(t, fixture, http.MethodPost,
		root+"/attempts/"+stale.AttemptId.String()+"/start", string(staleBody), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusConflict, staleResp.Code)
	fixture.validateResponse(t, staleReq, staleResp)
	unchanged := goldenOperatorGroupByID(t,
		goldenOperatorThroughREST(t, fixture, adminToken, tournamentID), stale.GroupId)
	require.Equal(t, api.GoldenRuntimeState("ready"), unchanged.State)
	require.Equal(t, firstActive.RuntimeRevision, unchanged.RuntimeRevision)
	t.Logf("golden-start-checkpoint ordinal=2 groups=2 cached_revision=%d current_revision=%d http_status=%d",
		stale.RuntimeRevision, unchanged.RuntimeRevision, staleResp.Code)

	// Exercise the same refresh helper used by both recovery-fixture loops.
	second := startGoldenGroupWithCurrentRevision(t, fixture, adminToken, tournamentID, stale.GroupId)
	secondActive := goldenOperatorGroupByID(t, second, stale.GroupId)
	require.Equal(t, api.GoldenRuntimeState("active"), secondActive.State)
	require.Equal(t, stale.AttemptId, secondActive.AttemptId)
	require.Equal(t, unchanged.RuntimeRevision+1, secondActive.RuntimeRevision)
	t.Logf("golden-start-checkpoint ordinal=2 groups=2 current_revision=%d resulting_revision=%d http_status=200",
		unchanged.RuntimeRevision, secondActive.RuntimeRevision)
}

func runFinalDraftPrecisionScenario(t *testing.T, actionTimeOffset time.Duration, wantStatus int, automaticFirst bool) {
	t.Helper()
	ctx := context.Background()
	const rosterSize = 16
	const normalTaskTimeLimit = 180
	stage := "construct 16-player content"
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("final-draft-checkpoint stage=%s", stage)
		}
	})

	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	swissRounds, err := domain.TournamentPresetV1.SwissRounds(rosterSize)
	require.NoError(t, err)
	// Shared BO1+BO3 categories need one chain per Swiss pairing, both
	// semifinals, and the final BO3 draft path.
	normalTaskCount := (rosterSize/2*swissRounds + 3) * (domain.AssignmentReserveCount + 1)
	catalog := prepareCreateToChampionContentForRosterSize(ctx, t, normalTaskCount, rosterSize)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	content := getTournamentContentThroughREST(t, fixture, adminToken)
	players := joinTournamentFlowPlayers(t, fixture, rosterSize)
	stage = "create and preflight 16-player roster"
	created := createTournamentWithRosterSizeThroughREST(
		t, fixture, adminToken, content.ContentRevision, "final-draft-turn-two", rosterSize,
	)
	setTournamentReserveCountThroughREST(t, fixture, adminToken, created.Id, domain.AssignmentReserveCount)
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
	preflight := runFinalDraftRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
	stage = "lock roster"
	lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
	stage = "start Swiss"
	startSwissThroughREST(t, fixture, adminToken, created.Id)
	stage = "connect participants"
	connectTournamentParticipantsThroughProduction(t, fixture, created.Id, players)
	stage = "play Swiss rounds"
	runFinalDraftSwissThroughREST(
		t, fixture, adminToken, created.Id, players, catalog.flags, swissRounds, normalTaskTimeLimit, &stage, nil,
	)
	stage = "Swiss complete; run Golden"

	projectionRevision := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id).
		NextCursor.ProjectionRevision
	stage = "open Golden"
	applyTournamentActionThroughREST(
		t, fixture, adminToken, created.Id, projectionRevision, "start_golden", uuid.New(),
	)
	projectionRevision = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id).
		NextCursor.ProjectionRevision
	stage = "run Golden recovery fixture"
	runGoldenThroughREST(t, fixture, adminToken, created.Id, projectionRevision, roster, players, catalog)
	stage = "start playoffs"
	stage = "Golden complete; run semifinals"

	projectionRevision = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id).
		NextCursor.ProjectionRevision
	playoffView := applyTournamentActionThroughREST(
		t, fixture, adminToken, created.Id, projectionRevision, "start_playoffs", uuid.New(),
	)
	require.Equal(t, api.TournamentState(domain.TournamentStatePlayoffs), playoffView.State)

	playersByID := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
	for _, player := range players {
		playersByID[player.id] = player
	}
	playersByParticipant := productionPlayersByParticipant(t, roster, playersByID)
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	stage = "execute semifinal series"
	for _, semifinal := range productionPlayoffSemifinals(t, snapshot) {
		runProductionPlayoffSeriesThroughREST(
			t, fixture, adminToken, created.Id, semifinal.Id,
			playersByParticipant, catalog.flags, normalTaskTimeLimit,
		)
	}
	stage = "semifinals complete; inspect final BO3 draft"

	draft, ok, observation := observeFinalDraftThroughREST(t, fixture, created.Id, playersByParticipant)
	if !ok {
		t.Fatalf("production final draft is not active after semifinals: %s", observation)
	}
	stage = "final BO3 draft " + observation
	require.NotNil(t, draft.TurnDeadline)

	runtime := tournamentFlowRuntimeForFixture(t, fixture)
	expectedSecondDraftRevision := draft.Revision
	switch draft.Turn {
	case 1:
		if automaticFirst {
			stage = "resolve first final draft timeout"
			resolveFinalDraftFirstTimeout(t, fixture, runtime, draft)
			expectedSecondDraftRevision = draft.Revision + 1
			break
		}
		runtime.clock.FreezeAt(draft.TurnDeadline.Add(-time.Second))
		firstActorID := draft.FirstParticipantId
		firstPlayer, found := playersByParticipant[firstActorID]
		require.True(t, found, "missing participant for first final draft turn")
		firstSnapshot := participantSnapshotThroughREST(t, fixture, created.Id, firstPlayer)
		require.NotNil(t, firstSnapshot.Draft)
		firstDraft := *firstSnapshot.Draft
		firstCategory := firstFinalBanCategory(t, firstDraft)
		firstCommandID := uuid.New()
		firstBody, err := json.Marshal(api.ParticipantDraftActionRequest{
			ExpectedProjectionRevision: firstSnapshot.NextCursor.ProjectionRevision,
			ExpectedDraftRevision:      firstDraft.Revision,
			ExpectedTurn:               firstDraft.Turn,
			Action:                     api.Ban,
			Category:                   firstCategory,
		})
		require.NoError(t, err)
		firstPath := "/api/v1/tournaments/" + created.Id.String() + "/participant/series/" +
			firstDraft.SeriesId.String() + "/draft/actions"
		firstReq, firstResp := doTournamentFlowJSON(
			t, fixture, http.MethodPost, firstPath, string(firstBody),
			cookieSession(firstPlayer.session.String()), firstCommandID, firstPlayer.csrf,
		)
		require.Equal(t, http.StatusOK, firstResp.Code, "first final draft action did not advance to turn two")
		fixture.validateResponse(t, firstReq, firstResp)
		stage = fmt.Sprintf("first final draft action returned HTTP %d", firstResp.Code)
		expectedSecondDraftRevision = firstDraft.Revision + 1
	case 2:
		require.False(t, automaticFirst, "automatic-first scenario must observe the original first-turn deadline")
	default:
		t.Fatalf("final BO3 draft did not start at turn one or target turn two: %s", observation)
	}

	secondActorID := draft.SecondParticipantId
	secondPlayer, found := playersByParticipant[secondActorID]
	require.True(t, found, "missing participant for second final draft turn")
	secondSnapshot := participantSnapshotThroughREST(t, fixture, created.Id, secondPlayer)
	require.NotNil(t, secondSnapshot.Draft)
	secondDraft := *secondSnapshot.Draft
	stage = fmt.Sprintf(
		"after first action final BO3 state=%s turn=%v draft_revision=%v projection_revision=%d",
		secondDraft.State, secondDraft.Turn, secondDraft.Revision,
		secondSnapshot.NextCursor.ProjectionRevision,
	)
	require.Equal(t, api.DraftStateActive, secondDraft.State)
	require.EqualValues(t, 2, secondDraft.Turn)
	require.Equal(t, expectedSecondDraftRevision, secondDraft.Revision)
	require.NotNil(t, secondDraft.TurnDeadline)
	if automaticFirst {
		require.Len(t, secondDraft.Actions, 1)
		require.True(t, secondDraft.Actions[0].Automatic)
	}
	stage = fmt.Sprintf(
		"final BO3 turn=%d state=%s draft_revision=%d projection_revision=%d",
		secondDraft.Turn, secondDraft.State, secondDraft.Revision,
		secondSnapshot.NextCursor.ProjectionRevision,
	)

	category := api.CategoryPwn
	if !finalDraftCategoryAvailable(secondDraft, category) {
		category = productionNextDraftCategory(t, secondDraft)
	}
	actionTime := secondDraft.TurnDeadline.Add(actionTimeOffset)
	freezeTournamentFlowClockForSingleInstant(runtime.clock, actionTime)
	commandID := uuid.New()
	body, err := json.Marshal(api.ParticipantDraftActionRequest{
		ExpectedProjectionRevision: secondSnapshot.NextCursor.ProjectionRevision,
		ExpectedDraftRevision:      secondDraft.Revision,
		ExpectedTurn:               secondDraft.Turn,
		Action:                     api.Ban,
		Category:                   category,
	})
	require.NoError(t, err)
	path := "/api/v1/tournaments/" + created.Id.String() + "/participant/series/" +
		secondDraft.SeriesId.String() + "/draft/actions"
	req, resp := doTournamentFlowJSON(
		t, fixture, http.MethodPost, path, string(body),
		cookieSession(secondPlayer.session.String()), commandID, secondPlayer.csrf,
	)

	if resp.Code != wantStatus {
		cause := probeFinalDraftActionError(
			t, fixture, runtime, secondDraft, secondSnapshot.NextCursor.ProjectionRevision,
			category, actionTime, commandID,
		)
		t.Errorf(
			"turn-two final draft action returned HTTP %d, want %d: stage=participant draft action; state=%s turn=%d action=ban category=%s draft_revision=%d projection_revision=%d deadline=%s attempted_at=%s probe=%s",
			resp.Code, wantStatus, secondDraft.State, secondDraft.Turn, category, secondDraft.Revision,
			secondSnapshot.NextCursor.ProjectionRevision,
			secondDraft.TurnDeadline.UTC().Format(time.RFC3339Nano), actionTime.UTC().Format(time.RFC3339Nano), cause,
		)
		return
	}
	fixture.validateResponse(t, req, resp)
	stage = fmt.Sprintf("final BO3 turn=%d returned HTTP %d", secondDraft.Turn, resp.Code)
	t.Logf("final-draft-result state=%s turn=%d draft_revision=%d projection_revision=%d action=ban category=%s automatic_first=%t http_status=%d deadline=%s attempted_at=%s",
		secondDraft.State, secondDraft.Turn, secondDraft.Revision, secondSnapshot.NextCursor.ProjectionRevision,
		category, automaticFirst, resp.Code, secondDraft.TurnDeadline.UTC().Format(time.RFC3339Nano),
		actionTime.UTC().Format(time.RFC3339Nano))
}

func resolveFinalDraftFirstTimeout(t *testing.T, fixture *restFixture, runtime tournamentFlowRuntime, draft api.Draft) {
	t.Helper()
	repository := participantdraftrepo.NewParticipantDraftRepositoryWithDependencies(
		fixture.mgr, draftrepo.NewDraftPostgres(fixture.mgr), loadTournamentFlowParticipantDraftContent,
	)
	current, err := repository.LoadDraft(t.Context(), draft.Id)
	if err != nil {
		t.Fatalf("final-draft-timeout stage=load_before_timeout cause=%s", finalDraftErrorClass(err))
	}
	require.NotNil(t, current)
	require.NotNil(t, current.AbsoluteDeadline)
	require.Equal(t, 1, current.Turn)
	deadline := *current.AbsoluteDeadline
	freezeTournamentFlowClockForSingleInstant(runtime.clock, deadline)
	result, err := draftusecase.NewTimeoutUseCase(repository, runtime.clock).Resolve(t.Context(), draftusecase.TimeoutCommand{
		DraftID: current.ID, ExpectedRevisionID: current.RevisionID, ExpectedRevision: current.Revision,
		ExpectedServiceEpoch: current.ServiceEpoch, CurrentServiceEpoch: current.ServiceEpoch,
		ExpectedTurn: current.Turn, ExpectedDeadline: deadline,
		CommandID: uuid.New(), ResultRevisionID: uuid.New(), ActionID: uuid.New(), DecisionEvidenceID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("final-draft-timeout stage=TimeoutUseCase.Resolve turn=%d draft_revision=%d deadline=%s cause=%s",
			current.Turn, current.Revision, deadline.UTC().Format(time.RFC3339Nano), finalDraftErrorClass(err))
	}
	require.True(t, result.Changed)
	require.Equal(t, 2, result.Draft.Turn)
	require.Len(t, result.Draft.Actions, 1)
	action := result.Draft.Actions[0]
	require.True(t, action.Automatic)
	require.True(t, action.OccurredAt.Equal(deadline))
	require.True(t, action.ScheduledDeadline.Equal(deadline))
	t.Logf("final-draft-timeout stage=committed state=%s turn=%d draft_revision=%d first_action=ban first_category=%s automatic=true occurred_at=%s",
		result.Draft.State, result.Draft.Turn, result.Draft.Revision, action.Category, action.OccurredAt.UTC().Format(time.RFC3339Nano))
}

func observeFinalDraftThroughREST(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	playersByParticipant map[uuid.UUID]tournamentFlowPlayer,
) (api.Draft, bool, string) {
	t.Helper()
	counts := make(map[string]int)
	for _, player := range playersByParticipant {
		path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/snapshot"
		req, resp := doTournamentFlowJSON(
			t, fixture, http.MethodGet, path, "", cookieSession(player.session.String()), uuid.Nil, "",
		)
		if resp.Code != http.StatusOK {
			return api.Draft{}, false, fmt.Sprintf("participant_snapshot_http=%d", resp.Code)
		}
		fixture.validateResponse(t, req, resp)
		participant := decodeJSON[api.ParticipantRecoverySnapshot](t, resp)
		if participant.Draft == nil {
			continue
		}
		draft := participant.Draft
		key := fmt.Sprintf("state=%s turn=%d draft_revision=%d", draft.State, draft.Turn, draft.Revision)
		counts[key]++
		if draft.State == api.DraftStateActive {
			return *draft, true, key
		}
	}
	if len(counts) == 0 {
		return api.Draft{}, false, "participant_draft_state=absent"
	}
	states := make([]string, 0, len(counts))
	for state := range counts {
		states = append(states, state)
	}
	sort.Strings(states)
	for index, state := range states {
		states[index] = fmt.Sprintf("%s participants=%d", state, counts[state])
	}
	return api.Draft{}, false, strings.Join(states, "; ")
}

func runFinalDraftSwissThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	players []tournamentFlowPlayer,
	flags map[uuid.UUID]string,
	rounds int,
	normalTaskTimeLimit int,
	stage *string,
	chooseWinner func(api.Series) uuid.UUID,
) {
	t.Helper()
	if chooseWinner == nil {
		chooseWinner = func(series api.Series) uuid.UUID { return series.FirstParticipantId }
	}
	playersByID := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
	for _, player := range players {
		playersByID[player.id] = player
	}

	for roundNumber := 1; roundNumber <= rounds; roundNumber++ {
		*stage = fmt.Sprintf("Swiss round %d pairings", roundNumber)
		snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
		round := configureProductionSwissPairingsThroughREST(
			t, fixture, adminToken, tournamentID, snapshot.NextCursor.ProjectionRevision, roundNumber,
		)
		snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
		wave := findProductionSwissWave(t, snapshot, round)
		wave = controlProductionWaveThroughREST(
			t, fixture, adminToken, tournamentID, wave.Id, snapshot.NextCursor.ProjectionRevision,
			api.WaveControlRequestActionOpenReadyWindow,
		)
		require.Equal(t, api.WaveStateReadyWindowOpen, wave.State)
		*stage = fmt.Sprintf("Swiss round %d participant readiness", roundNumber)

		playersByParticipant := productionPlayersByParticipant(t, snapshot.Roster, playersByID)
		for _, member := range wave.Members {
			player, ok := playersByParticipant[member.ParticipantId]
			require.True(t, ok, "missing player for participant %s", member.ParticipantId)
			participant := participantSnapshotThroughREST(t, fixture, tournamentID, player)
			body, err := json.Marshal(api.ParticipantReadyRequest{
				ExpectedProjectionRevision: participant.NextCursor.ProjectionRevision,
				Ready:                      true,
			})
			require.NoError(t, err)
			path := "/api/v1/tournaments/" + tournamentID.String() +
				"/participant/waves/" + wave.Id.String() + "/ready"
			req, resp := doTournamentFlowJSON(
				t, fixture, http.MethodPost, path, string(body), cookieSession(player.session.String()), uuid.New(), player.csrf,
			)
			require.Equal(t, http.StatusOK, resp.Code)
			fixture.validateResponse(t, req, resp)
		}

		snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
		*stage = fmt.Sprintf("Swiss round %d start and assignment validation", roundNumber)
		wave = findProductionWaveByID(t, snapshot, wave.Id)
		wave = controlProductionWaveThroughREST(
			t, fixture, adminToken, tournamentID, wave.Id, snapshot.NextCursor.ProjectionRevision,
			api.WaveControlRequestActionStart,
		)
		require.Equal(t, api.WaveStateActive, wave.State)
		assertRuntimeDeadlineForWaveAssignments(t, fixture, tournamentID, wave, playersByParticipant, normalTaskTimeLimit)
		*stage = fmt.Sprintf("Swiss round %d settlement", roundNumber)
		settleProductionSwissWaveThroughREST(
			t, fixture, tournamentID, wave, playersByParticipant, flags,
			chooseWinner,
		)

		snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
		*stage = fmt.Sprintf("Swiss round %d completion", roundNumber)
		wave = findProductionWaveByID(t, snapshot, wave.Id)
		if wave.State != api.WaveStateCompleted {
			wave = controlProductionWaveThroughREST(
				t, fixture, adminToken, tournamentID, wave.Id, snapshot.NextCursor.ProjectionRevision,
				api.WaveControlRequestActionComplete,
			)
		}
		require.Equal(t, api.WaveStateCompleted, wave.State)
	}
}

func runFinalDraftRosterPreflightThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
) api.PreflightReport {
	t.Helper()
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
	body, err := json.Marshal(api.PreflightRequest{
		ExpectedProjectionRevision: snapshot.NextCursor.ProjectionRevision,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/roster/preflight"
	req, resp := doTournamentFlowJSON(
		t, fixture, http.MethodPost, path, string(body), adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, resp.Code, "roster preflight request failed")
	fixture.validateResponse(t, req, resp)
	report := decodeJSON[api.PreflightReport](t, resp)
	if !report.Passed {
		failedChecks := make([]string, 0)
		for _, check := range report.Checks {
			if !check.Passed {
				failedChecks = append(failedChecks, string(check.Code))
			}
		}
		t.Fatalf("roster preflight failed checks=%v", failedChecks)
	}
	return report
}

func firstFinalBanCategory(t *testing.T, draft api.Draft) api.Category {
	t.Helper()
	for _, category := range []api.Category{api.CategoryForensics, api.CategoryWeb, api.CategoryCrypto, api.CategoryReverse} {
		if category != api.CategoryPwn && finalDraftCategoryAvailable(draft, category) {
			return category
		}
	}
	require.FailNow(t, "no legal first-turn ban leaves pwn available for turn two")
	return api.CategoryForensics
}

func finalDraftCategoryAvailable(draft api.Draft, category api.Category) bool {
	available := false
	for _, candidate := range draft.Pool {
		if candidate == category {
			available = true
			break
		}
	}
	if !available {
		return false
	}
	for _, action := range draft.Actions {
		if action.Category == category {
			return false
		}
	}
	return true
}

func freezeTournamentFlowClockForSingleInstant(clock *tournamentFlowClock, instant time.Time) {
	clock.mu.Lock()
	clock.step = 0
	clock.mu.Unlock()
	clock.FreezeAt(instant)
}

func probeFinalDraftActionError(
	t *testing.T,
	fixture *restFixture,
	runtime tournamentFlowRuntime,
	draft api.Draft,
	projectionRevision int64,
	category api.Category,
	attemptedAt time.Time,
	commandID uuid.UUID,
) string {
	t.Helper()
	repository := participantdraftrepo.NewParticipantDraftRepositoryWithDependencies(
		fixture.mgr, draftrepo.NewDraftPostgres(fixture.mgr), loadTournamentFlowParticipantDraftContent,
	)
	current, err := repository.LoadDraft(context.Background(), draft.Id)
	if err != nil {
		return "stage=ParticipantDraftRepository.LoadDraft cause=" + finalDraftErrorClass(err)
	}
	if current.State != draftusecase.ExecutionStateActive || current.Turn != int(draft.Turn) ||
		current.Revision != draft.Revision || current.CurrentActorID == nil {
		return fmt.Sprintf(
			"stage=probe_state state=%s turn=%d draft_revision=%d projection_revision=%d",
			current.State, current.Turn, current.Revision, projectionRevision,
		)
	}

	freezeTournamentFlowClockForSingleInstant(runtime.clock, attemptedAt)
	_, err = draftusecase.NewActionUseCase(repository, runtime.clock).Apply(
		context.Background(), draftusecase.PlayerActionCommand{
			DraftID:              current.ID,
			ExpectedRevisionID:   current.RevisionID,
			ExpectedRevision:     current.Revision,
			ExpectedServiceEpoch: current.ServiceEpoch,
			ExpectedTurn:         current.Turn,
			CommandID:            uuid.New(),
			ResultRevisionID:     uuid.New(),
			ActionID:             uuid.New(),
			ActorID:              *current.CurrentActorID,
			Action:               domain.DraftActionBan,
			Category:             domain.Category(category),
		},
	)
	if err == nil {
		return "stage=ActionUseCase.Apply cause=none"
	}
	return "stage=ActionUseCase.Apply cause=" + finalDraftErrorClass(err)
}

func finalDraftErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		if postgresError.ConstraintName != "" {
			return fmt.Sprintf("postgres_sqlstate=%s constraint=%s", postgresError.Code, postgresError.ConstraintName)
		}
		return "postgres_sqlstate=" + postgresError.Code
	}
	// Only known invariant labels are emitted; never include arbitrary DB details
	// or serialized decision evidence in the diagnostic log.
	for _, invariant := range []string{
		"automatic action decision evidence is invalid",
		"automatic action decision evidence is missing",
		"first actor evidence does not match participants",
	} {
		if strings.Contains(err.Error(), invariant) {
			return "draft.ErrInvalidExecution invariant=" + invariant
		}
	}
	for _, candidate := range []struct {
		err   error
		label string
	}{
		{domain.ErrDraftDeadline, "domain.ErrDraftDeadline"},
		{domain.ErrDraftStaleTurn, "domain.ErrDraftStaleTurn"},
		{domain.ErrDraftIllegalAction, "domain.ErrDraftIllegalAction"},
		{domain.ErrDraftCategoryUsed, "domain.ErrDraftCategoryUsed"},
		{domain.ErrConflict, "domain.ErrConflict"},
		{domain.ErrValidation, "domain.ErrValidation"},
		{domain.ErrInternal, "domain.ErrInternal"},
		{domain.ErrDecisionReplayMismatch, "domain.ErrDecisionReplayMismatch"},
		{domain.ErrInvalidDecisionEvidence, "domain.ErrInvalidDecisionEvidence"},
		{draftusecase.ErrActionConflict, "draft.ErrActionConflict"},
		{draftusecase.ErrInvalidExecution, "draft.ErrInvalidExecution"},
	} {
		if errors.Is(err, candidate.err) {
			return candidate.label
		}
	}
	return fmt.Sprintf("unclassified_type=%T", err)
}
