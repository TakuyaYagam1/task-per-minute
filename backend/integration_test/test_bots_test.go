//go:build integration && testtools

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/bootstrap"
	"github.com/TakuyaYagam1/task-per-minute/internal/testbots"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

// Only the human and organizer are driven by this test. All other player
// mutations originate from the runner through real HTTP and WebSocket clients.
func TestBotsTournamentLifecycle(t *testing.T) {
	for _, size := range []int{8, 16} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			truncateRoundProofTables(t.Context(), t)
			t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })
			catalog := prepareBotCatalog(t)
			seed := &restFixture{databaseFixture: newDatabaseFixture()}
			humanAccount := seed.joinPlayerViaUsecase(t, "tester")
			csrf, err := middleware.NewPlayerCSRFToken(*humanAccount.SessionToken)
			require.NoError(t, err)
			human := tournamentFlowPlayer{id: humanAccount.ID, session: *humanAccount.SessionToken, csrf: csrf}
			accounts := make([]testbots.Account, 0, 16)
			for i := 1; i <= 16; i++ {
				name := fmt.Sprintf("demo%02d", i)
				a := seed.joinPlayerViaUsecase(t, name)
				accounts = append(accounts, testbots.Account{Username: name, Password: restPlayerPassword, PlayerID: a.ID.String()})
			}
			directory := t.TempDir()
			writePrivate := func(name string, value any) string {
				data, err := json.Marshal(value)
				require.NoError(t, err)
				path := filepath.Join(directory, name)
				require.NoError(t, os.WriteFile(path, data, 0600))
				return path
			}
			accountsFile := writePrivate("accounts.json", map[string]any{"version": 1, "applied": true, "accounts": accounts})
			tasks := make([]testbots.Task, 0, len(catalog.flags))
			for id, answer := range catalog.flags {
				var category, kind string
				require.NoError(t, sharedPool.QueryRow(t.Context(), `SELECT category, kind FROM tasks WHERE id = $1`, id).Scan(&category, &kind))
				tasks = append(tasks, testbots.Task{TaskID: id.String(), Version: 1, Answer: answer, Category: category, Kind: kind})
			}
			catalogFile := writePrivate("tasks.json", map[string]any{"version": 1, "content_revision": catalog.revision, "tasks": tasks})
			keyFile := filepath.Join(directory, "control.key")
			require.NoError(t, os.WriteFile(keyFile, []byte(strings.Repeat("a", 64)), 0600))
			runnerServer := httptest.NewUnstartedServer(nil)
			t.Setenv("TEST_BOTS_URL", "http://"+runnerServer.Listener.Addr().String())
			t.Setenv("TEST_BOTS_CONTROLLER_PLAYER_ID", human.id.String())
			t.Setenv("TEST_BOTS_KEY_FILE", keyFile)
			fixture := newTournamentFlowRESTFixture(t)
			runtime := tournamentFlowRuntimeForFixture(t, fixture)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/participant/realtime") {
					runtime.webSocket.ServeHTTP(w, r)
					return
				}
				middleware.CSRFGuard()(fixture.handler).ServeHTTP(w, r)
			}))
			t.Cleanup(backend.Close)
			config := testbots.Config{BackendURL: backend.URL, Origin: backend.URL, ControllerID: human.id.String(), AccountsFile: accountsFile, CatalogFile: catalogFile, KeyFile: keyFile, StateDir: filepath.Join(directory, "state")}
			runner, err := testbots.New(config)
			require.NoError(t, err)
			var current atomic.Pointer[testbots.Engine]
			current.Store(runner)
			runnerServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { current.Load().ServeHTTP(w, r) })
			runnerServer.Start()
			t.Cleanup(runnerServer.Close)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func(engine *testbots.Engine, ctx context.Context, done chan struct{}) {
				defer close(done)
				engine.Start(ctx)
			}(runner, ctx, done)
			t.Cleanup(func() { cancel(); <-done })
			admin := fixture.adminAccessToken(t)
			created := createTournamentWithRosterSizeThroughREST(t, fixture, admin, catalog.revision, "bots", size)
			setBotTournamentConfiguration(t, fixture, admin, created.Id)
			snapshot := tournamentAdminSnapshotThroughREST(t, fixture, admin, created.Id)
			openRegistrationThroughREST(t, fixture, admin, created.Id, snapshot.NextCursor.ProjectionRevision)
			playerRoot := "/api/v1/tournaments/" + created.Id.String() + "/participant"
			botHumanPost(t, fixture, human, playerRoot+"/queue", nil)
			botHumanPost(t, fixture, human, playerRoot+"/queue/check-in", nil)
			botRoot := "/api/v1/players/test-bots/" + created.Id.String()
			botHumanPost(t, fixture, human, botRoot+"/actions", testbots.Action{Action: "start", Scenario: "golden"})
			waitBotCondition(t, fixture, human, botRoot, 45*time.Second, func() bool {
				snapshot = tournamentAdminSnapshotThroughREST(t, fixture, admin, created.Id)
				if len(snapshot.Roster.Participants) != size {
					return false
				}
				for _, p := range snapshot.Roster.Participants {
					if string(p.Attendance) != "checked_in" {
						return false
					}
				}
				return true
			})
			t.Log("all bots joined and checked in")
			botHumanPost(t, fixture, human, botRoot+"/actions", testbots.Action{Action: "pause"})
			cancel()
			<-done
			runner, err = testbots.New(config)
			require.NoError(t, err)
			current.Store(runner)
			ctx, cancel = context.WithCancel(t.Context())
			done = make(chan struct{})
			go func(engine *testbots.Engine, ctx context.Context, done chan struct{}) {
				defer close(done)
				engine.Start(ctx)
			}(runner, ctx, done)
			require.Equal(t, "paused", botView(t, fixture, human, botRoot).Status)
			botHumanPost(t, fixture, human, botRoot+"/actions", testbots.Action{Action: "resume"})
			preflight := runTournamentRosterPreflightThroughREST(t, fixture, admin, created.Id)
			lockTournamentRosterThroughREST(t, fixture, admin, created.Id, snapshot.Roster, preflight)
			startSwissThroughREST(t, fixture, admin, created.Id)
			connectTournamentParticipantsThroughProduction(t, fixture, created.Id, []tournamentFlowPlayer{human})
			rounds := 3
			if size == 16 {
				rounds = 4
			}
			for round := 1; round <= rounds; round++ {
				_, resp := doTournamentFlowJSON(t, fixture, "GET", fmt.Sprintf("/api/v1/admin/test-bots/%s/pairings?round=%d", created.Id, round), "", adminSession(admin), uuid.New(), "")
				require.Equal(t, 200, resp.Code, resp.Body.String())
				pairs := decodeJSON[struct {
					Pairs []api.ManualPairInput `json:"pairs"`
				}](t, resp)
				snapshot = tournamentAdminSnapshotThroughREST(t, fixture, admin, created.Id)
				body, err := json.Marshal(api.PairingConfigurationRequest{Categories: []api.Category{"crypto"}, CategoryMode: "admin", ExpectedProjectionRevision: snapshot.NextCursor.ProjectionRevision, PairingMode: "manual", ManualPairings: &pairs.Pairs, RoundNumber: int32(round)})
				require.NoError(t, err)
				_, resp = doTournamentFlowJSON(t, fixture, "POST", "/api/v1/admin/tournaments/"+created.Id.String()+"/pairings", string(body), adminSession(admin), uuid.New(), "")
				require.Equal(t, 200, resp.Code, resp.Body.String())
				swiss := decodeJSON[api.SwissRound](t, resp)
				snapshot = tournamentAdminSnapshotThroughREST(t, fixture, admin, created.Id)
				wave := findProductionSwissWave(t, snapshot, swiss)
				runBotWave(t, fixture, admin, created.Id, human, botRoot, wave.Id, round != 1)
				t.Logf("Swiss round %d complete", round)
			}
			snapshot = tournamentAdminSnapshotThroughREST(t, fixture, admin, created.Id)
			applyTournamentActionThroughREST(t, fixture, admin, created.Id, snapshot.NextCursor.ProjectionRevision, "start_golden", uuid.New())
			snapshot = tournamentAdminSnapshotThroughREST(t, fixture, admin, created.Id)
			body, _ := json.Marshal(api.GoldenOpenRequest{ExpectedProjectionRevision: snapshot.NextCursor.ProjectionRevision})
			_, resp := doTournamentFlowJSON(t, fixture, "POST", "/api/v1/admin/tournaments/"+created.Id.String()+"/golden/open", string(body), adminSession(admin), uuid.New(), "")
			require.Equal(t, 200, resp.Code, resp.Body.String())
			group := goldenParticipantThroughREST(t, fixture, created.Id, human)
			setGoldenParticipantReadyThroughREST(t, fixture, created.Id, human, group)
			waitBotCondition(t, fixture, human, botRoot, 30*time.Second, func() bool {
				for _, g := range goldenOperatorThroughREST(t, fixture, admin, created.Id).Groups {
					if string(g.State) != "ready" {
						return false
					}
				}
				return true
			})
			for _, g := range goldenOperatorThroughREST(t, fixture, admin, created.Id).Groups {
				startGoldenGroupWithCurrentRevision(t, fixture, admin, created.Id, g.GroupId)
			}
			group = goldenParticipantThroughREST(t, fixture, created.Id, human)
			answer := botHumanAnswer(t, fixture, human, botRoot)
			submitGoldenParticipantThroughREST(t, fixture, created.Id, human, group, answer)
			waitBotCondition(t, fixture, human, botRoot, 110*time.Second, func() bool {
				for _, g := range goldenOperatorThroughREST(t, fixture, admin, created.Id).Groups {
					if string(g.State) != "completed" {
						return false
					}
				}
				return true
			})
			t.Log("Golden complete")
			snapshot = tournamentAdminSnapshotThroughREST(t, fixture, admin, created.Id)
			applyTournamentActionThroughREST(t, fixture, admin, created.Id, snapshot.NextCursor.ProjectionRevision, "start_playoffs", uuid.New())
			waitBotCondition(t, fixture, human, botRoot, 180*time.Second, func() bool {
				snapshot = tournamentAdminSnapshotThroughREST(t, fixture, admin, created.Id)
				if string(snapshot.Tournament.State) == "completed" {
					return true
				}
				botHumanStep(t, fixture, created.Id, human, botRoot, true)
				for _, wave := range snapshot.Waves {
					if wave.State == api.WaveStatePlanned || wave.State == api.WaveStateReadyWindowOpen || wave.State == api.WaveStateReady || wave.State == api.WaveStateActive {
						advanceBotWave(t, fixture, admin, created.Id, wave)
					}
				}
				return false
			})
			assertCompletedTournamentThroughREST(t, fixture, admin, created.Id)
			waitBotCondition(t, fixture, human, botRoot, 5*time.Second, func() bool { return botView(t, fixture, human, botRoot).Status == "completed" })
			t.Log("champion recorded; runner stopped")
			var liveReservations int
			require.NoError(t, sharedPool.QueryRow(t.Context(), `SELECT count(*) FROM participant_reservations WHERE tournament_id = $1`, created.Id).Scan(&liveReservations))
			require.Zero(t, liveReservations, "completed tournament must release participation locks")
			// A second tournament can reuse the accounts without clearing results.
			repeat := createTournamentWithRosterSizeThroughREST(t, fixture, admin, catalog.revision, "repeat", size)
			setBotTournamentConfiguration(t, fixture, admin, repeat.Id)
			snapshot = tournamentAdminSnapshotThroughREST(t, fixture, admin, repeat.Id)
			openRegistrationThroughREST(t, fixture, admin, repeat.Id, snapshot.NextCursor.ProjectionRevision)
			root := "/api/v1/tournaments/" + repeat.Id.String() + "/participant"
			botHumanPost(t, fixture, human, root+"/queue", nil)
			botHumanPost(t, fixture, human, root+"/queue/check-in", nil)
			root = "/api/v1/players/test-bots/" + repeat.Id.String()
			botHumanPost(t, fixture, human, root+"/actions", testbots.Action{Action: "start", Scenario: "free"})
			waitBotCondition(t, fixture, human, root, 30*time.Second, func() bool {
				snapshot = tournamentAdminSnapshotThroughREST(t, fixture, admin, repeat.Id)
				if len(snapshot.Roster.Participants) != size {
					return false
				}
				for _, participant := range snapshot.Roster.Participants {
					if participant.Attendance != "checked_in" {
						return false
					}
				}
				return true
			})
			cancelBody := fmt.Sprintf(`{"expected_projection_revision":%d,"action":"cancel","confirmed":true,"reason":"test run complete"}`, snapshot.NextCursor.ProjectionRevision)
			_, cancelled := doTournamentFlowJSON(t, fixture, "POST", "/api/v1/admin/tournaments/"+repeat.Id.String()+"/actions", cancelBody, adminSession(admin), uuid.New(), "")
			require.Equal(t, http.StatusOK, cancelled.Code, cancelled.Body.String())
			waitBotCondition(t, fixture, human, root, 5*time.Second, func() bool { return botView(t, fixture, human, root).Status == "cancelled" })
			assertCompletedTournamentThroughREST(t, fixture, admin, created.Id)
			if size == 16 {
				// Candidate counts depend on which tasks these players have already
				// seen. Downgrade must preserve the evidence in either case.
				var minimum, before, after int
				require.NoError(t, sharedPool.QueryRow(t.Context(), `SELECT min(jsonb_array_length(candidates)), count(*) FROM exact_normal_assignment_sources`).Scan(&minimum, &before))
				database := stdlib.OpenDB(*sharedPool.Config().ConnConfig)
				defer database.Close()
				dir := bootstrap.ResolveMigrationsDir("db/migrations")
				if minimum < 3 {
					require.Error(t, goose.DownContext(t.Context(), database, dir))
				} else {
					require.NoError(t, goose.DownContext(t.Context(), database, dir))
					require.NoError(t, goose.UpToContext(t.Context(), database, dir, 41))
				}
				version, err := goose.GetDBVersionContext(t.Context(), database)
				require.NoError(t, err)
				require.EqualValues(t, 41, version)
				require.NoError(t, sharedPool.QueryRow(t.Context(), `SELECT count(*) FROM exact_normal_assignment_sources`).Scan(&after))
				require.Equal(t, before, after)
			}
		})
	}
}

func setBotTournamentConfiguration(t *testing.T, f *restFixture, admin string, id uuid.UUID) {
	t.Helper()
	path := "/api/v1/admin/tournaments/" + id.String() + "/configuration"
	_, resp := doTournamentFlowJSON(t, f, "GET", path, "", adminSession(admin), uuid.New(), "")
	require.Equal(t, 200, resp.Code)
	c := decodeJSON[api.TournamentConfiguration](t, resp)
	body, _ := json.Marshal(api.UpdateTournamentConfigurationRequest{ExpectedProjectionRevision: c.ProjectionRevision, ExpectedConfigurationRevision: c.ConfigurationRevision, ReserveCount: 0, Confirmed: true, Reason: "test bot scenario", SwissDefault: api.TournamentConfigurationStageDefaultInput{Mode: "admin", Categories: []api.Category{"crypto"}}, SemifinalDefault: api.TournamentConfigurationStageDefaultInput{Mode: "admin", Categories: []api.Category{"crypto"}}, UnlockIntents: []api.ConfigurationUnlockIntent{}})
	_, resp = doTournamentFlowJSON(t, f, "PATCH", path, string(body), adminSession(admin), uuid.New(), "")
	require.Equal(t, 200, resp.Code, resp.Body.String())
}

func prepareBotCatalog(t *testing.T) tournamentFlowCatalog {
	t.Helper()
	catalog := tournamentFlowCatalog{flags: map[uuid.UUID]string{}}
	for _, spec := range []struct {
		category, kind string
		count          int
	}{
		{"crypto", "normal", 35}, {"web", "normal", 4}, {"reverse", "normal", 3}, {"forensics", "normal", 1}, {"pwn", "normal", 1},
		{"crypto", "golden", 8}, {"web", "golden", 1}, {"reverse", "golden", 1},
	} {
		for range spec.count {
			id := uuid.New()
			answer := "fixture-" + uuid.NewString()
			_, err := sharedPool.Exec(t.Context(), `INSERT INTO tasks (id, title, description, category, difficulty, time_limit, flag, kind) VALUES ($1, $2, 'bot fixture', $3, 'easy', 180, $4, $5)`, id, "bot-"+id.String(), spec.category, answer, spec.kind)
			require.NoError(t, err)
			catalog.flags[id] = answer
		}
	}
	_, err := sharedPool.Exec(t.Context(), `INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source) SELECT task_id, version, 1, true, 'content_validation' FROM task_versions`)
	require.NoError(t, err)
	_, err = sharedPool.Exec(t.Context(), `SELECT publish_task_pool_heads()`)
	require.NoError(t, err)
	catalog.revision = currentTaskPoolPublicationRevision(t.Context(), t)
	return catalog
}

func botHumanPost(t *testing.T, f *restFixture, human tournamentFlowPlayer, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	var body string
	if value != nil {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		body = string(raw)
	}
	_, resp := doTournamentFlowJSON(t, f, "POST", path, body, cookieSession(human.session.String()), uuid.New(), human.csrf)
	if resp.Code == 409 && strings.Contains(path, "/participant/") && !strings.HasSuffix(path, "/queue") && !strings.HasSuffix(path, "/check-in") {
		// Concurrent bot commands can advance the shared projection after this
		// human snapshot. The next driver iteration rereads it, like the UI.
		return resp
	}
	require.Contains(t, []int{200, 201, 202, 204}, resp.Code, "player endpoint %s returned %d", path, resp.Code)
	return resp
}

func botView(t *testing.T, f *restFixture, human tournamentFlowPlayer, root string) testbots.View {
	t.Helper()
	_, resp := doTournamentFlowJSON(t, f, "GET", root+"/state", "", cookieSession(human.session.String()), uuid.New(), "")
	require.Equal(t, 200, resp.Code)
	return decodeJSON[testbots.View](t, resp)
}

func waitBotCondition(t *testing.T, f *restFixture, human tournamentFlowPlayer, root string, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		view := botView(t, f, human, root)
		require.NotEqual(t, "paused", view.Status, view.Message)
		for _, bot := range view.Bots {
			require.NotEqual(t, "error", bot.Status, "%s: %s", bot.Username, bot.Message)
		}
		time.Sleep(500 * time.Millisecond)
	}
	view := botView(t, f, human, root)
	t.Fatalf("timed out waiting for tournament: %s; bots: %+v", view.Message, view.Bots)
}

func botHumanAnswer(t *testing.T, f *restFixture, human tournamentFlowPlayer, root string) string {
	t.Helper()
	resp := botHumanPost(t, f, human, root+"/answer", nil)
	return decodeJSON[struct {
		Answer string `json:"answer"`
	}](t, resp).Answer
}

func botHumanStep(t *testing.T, f *restFixture, id uuid.UUID, human tournamentFlowPlayer, botRoot string, win bool) {
	t.Helper()
	s := participantSnapshotThroughREST(t, f, id, human)
	root := "/api/v1/tournaments/" + id.String() + "/participant"
	switch string(s.Lobby.RequiredAction) {
	case "ready":
		if s.Wave != nil {
			botHumanPost(t, f, human, root+"/waves/"+s.Wave.Id.String()+"/ready", api.ParticipantReadyRequest{ExpectedProjectionRevision: s.NextCursor.ProjectionRevision, Ready: true})
		}
	case "draft":
		if d := s.Draft; d != nil && d.CurrentActorId != nil && *d.CurrentActorId == s.Lobby.ParticipantId && d.CurrentAction != nil && len(d.LegalCategories) > 0 {
			botHumanPost(t, f, human, root+"/series/"+d.SeriesId.String()+"/draft/actions", api.ParticipantDraftActionRequest{ExpectedProjectionRevision: s.NextCursor.ProjectionRevision, ExpectedDraftRevision: d.Revision, ExpectedTurn: d.Turn, Action: *d.CurrentAction, Category: d.LegalCategories[0]})
		}
	case "review_result":
		if s.Series != nil {
			botHumanPost(t, f, human, root+"/series/"+s.Series.Id.String()+"/post-series", api.ParticipantPostSeriesRequest{ExpectedProjectionRevision: s.NextCursor.ProjectionRevision, Action: "acknowledge_result"})
		}
	case "play":
		if win && s.Assignment != nil && s.Assignment.Context.GameState == "active" {
			answer := botHumanAnswer(t, f, human, botRoot)
			botHumanPost(t, f, human, root+"/series/"+s.Assignment.Context.SeriesId.String()+"/games/"+s.Assignment.Context.GameId.String()+"/submissions", api.ParticipantSubmissionRequest{ExpectedProjectionRevision: s.NextCursor.ProjectionRevision, SubmittedFlag: &answer})
		}
	}
}

func runBotWave(t *testing.T, f *restFixture, admin string, id uuid.UUID, human tournamentFlowPlayer, root string, waveID uuid.UUID, win bool) {
	t.Helper()
	waitBotCondition(t, f, human, root, 80*time.Second, func() bool {
		botHumanStep(t, f, id, human, root, win)
		s := tournamentAdminSnapshotThroughREST(t, f, admin, id)
		wave := findProductionWaveByID(t, s, waveID)
		if wave.State == api.WaveStateCompleted {
			return true
		}
		advanceBotWave(t, f, admin, id, wave)
		return false
	})
}

func advanceBotWave(t *testing.T, f *restFixture, admin string, id uuid.UUID, wave api.Wave) {
	t.Helper()
	s := tournamentAdminSnapshotThroughREST(t, f, admin, id)
	if s.Tournament.State == "completed" || s.Tournament.State == "cancelled" {
		return
	}
	wave = findProductionWaveByID(t, s, wave.Id)
	switch wave.State {
	case api.WaveStatePlanned:
		controlProductionWaveThroughREST(t, f, admin, id, wave.Id, s.NextCursor.ProjectionRevision, "open_ready_window")
	case api.WaveStateReadyWindowOpen, api.WaveStateReady:
		for _, m := range wave.Members {
			if !m.Ready {
				return
			}
		}
		controlProductionWaveThroughREST(t, f, admin, id, wave.Id, s.NextCursor.ProjectionRevision, "start")
	case api.WaveStateActive:
		for _, m := range wave.Members {
			if m.SeriesId != nil {
				series := productionSeriesByID(t, s, *m.SeriesId)
				settled := false
				for _, slot := range series.Slots {
					for _, game := range slot.Attempts {
						if game.State == "active" || game.State == "technical_pause" {
							return
						}
						settled = settled || game.State == "completed"
					}
				}
				if !settled {
					return
				}
			}
		}
		controlProductionWaveThroughREST(t, f, admin, id, wave.Id, s.NextCursor.ProjectionRevision, "complete")
	}
}
