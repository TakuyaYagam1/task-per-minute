package testbots

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/google/uuid"
)

func TestGoldenSchedule(t *testing.T) {
	for _, size := range []int{8, 16} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			r := &Run{Size: size, Scenario: "golden", HumanParticipantID: uuid.NewString()}
			slots := map[string]int{r.HumanParticipantID: 1}
			for slot := 0; slot < size; slot++ {
				if slot == 1 {
					continue
				}
				id := uuid.NewString()
				r.Bots = append(r.Bots, Bot{Slot: slot, ParticipantID: id})
				slots[id] = slot
			}
			wins := make([]int, size)
			seen := map[string]bool{}
			for round := 1; round <= rounds(size); round++ {
				pairs, err := Pairings(r, round)
				if err != nil {
					t.Fatal(err)
				}
				if len(pairs) != size/2 {
					t.Fatal("incomplete round")
				}
				players := map[string]bool{}
				for _, p := range pairs {
					key := p.First + ":" + p.Second
					if seen[key] || players[p.First] || players[p.Second] {
						t.Fatal("duplicate pairing")
					}
					seen[key] = true
					players[p.First] = true
					players[p.Second] = true
					wins[min(slots[p.First], slots[p.Second])]++
				}
			}
			higher, equal := 0, 0
			for _, w := range wins {
				if w > wins[1] {
					higher++
				}
				if w == wins[1] {
					equal++
				}
			}
			if wins[1] != rounds(size)-1 || higher != 1 || equal != rounds(size) {
				t.Fatalf("wrong Golden group: wins=%v", wins)
			}
		})
	}
}
func testEngine(t *testing.T) (*Engine, Config) {
	t.Helper()
	root := t.TempDir()
	cfg := Config{ControllerID: uuid.NewString(), BackendURL: "http://127.0.0.1:1", Origin: "http://localhost:3000", AccountsFile: filepath.Join(root, "accounts.json"), CatalogFile: filepath.Join(root, "tasks.json"), KeyFile: filepath.Join(root, "control.key"), StateDir: filepath.Join(root, "state")}
	accounts := []Account{}
	for i := 1; i <= 16; i++ {
		accounts = append(accounts, Account{Username: fmt.Sprintf("demo%02d", i), PlayerID: uuid.NewString(), Password: "synthetic-password-for-tests"})
	}
	tasks := []Task{}
	for i := 0; i < 54; i++ {
		tasks = append(tasks, Task{TaskID: uuid.NewString(), Version: 2, Answer: "synthetic-test-answer"})
	}
	for path, v := range map[string]any{cfg.AccountsFile: map[string]any{"version": 1, "applied": true, "accounts": accounts}, cfg.CatalogFile: map[string]any{"version": 1, "content_revision": 1, "tasks": tasks}} {
		if err := atomicJSON(path, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cfg.KeyFile, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e, cfg
}
func attachRun(e *Engine) {
	e.run = &Run{TournamentID: uuid.NewString(), ControllerID: e.cfg.ControllerID, HumanParticipantID: uuid.NewString(), Size: 8, Scenario: "free", Status: "running", Next: Policy{Outcome: "human_wins", Delay: 15}, Commands: map[string]Command{}, Policies: map[string]Policy{}, Bots: []Bot{}}
	slot := 0
	for _, a := range e.accounts[:7] {
		if slot == 1 {
			slot++
		}
		e.run.Bots = append(e.run.Bots, Bot{PlayerID: a.PlayerID, Username: a.Username, Slot: slot, ParticipantID: uuid.NewString()})
		slot++
	}
}
func TestRestartPausesAndLocksState(t *testing.T) {
	e, cfg := testEngine(t)
	attachRun(e)
	e.run.Commands["pending"] = Command{Key: uuid.NewString()}
	if err := e.save(); err != nil {
		t.Fatal(err)
	}
	if other, err := New(cfg); err == nil {
		other.Close()
		t.Fatal("second owner accepted")
	}
	e.Close()
	restored, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.run.Status != "paused" || restored.run.Commands["pending"].Key != e.run.Commands["pending"].Key {
		t.Fatal("recovery lost command identity")
	}
	info, _ := os.Stat(filepath.Join(cfg.StateDir, "run.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("state permissions")
	}
}
func TestCommandsRetrySameKeyAndDoNotReplaySuccess(t *testing.T) {
	e, _ := testEngine(t)
	attachRun(e)
	keys := []string{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if len(keys) == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer backend.Close()
	c := newClient(Config{BackendURL: backend.URL})
	body := map[string]any{"ready": true, "expected_projection_revision": 1}
	if e.command(context.Background(), c, &e.run.Bots[0], "/ready", body) == nil {
		t.Fatal("expected temporary failure")
	}
	if err := e.command(context.Background(), c, &e.run.Bots[0], "/ready", body); err != nil {
		t.Fatal(err)
	}
	if err := e.command(context.Background(), c, &e.run.Bots[0], "/ready", body); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("retry identity changed: count %d", len(keys))
	}
}
func TestPrivateCatalogAndAnswerVersion(t *testing.T) {
	e, cfg := testEngine(t)
	var catalog struct {
		Tasks []Task `json:"tasks"`
	}
	if err := privateJSON(cfg.CatalogFile, &catalog); err != nil {
		t.Fatal(err)
	}
	task := catalog.Tasks[0]
	if _, err := e.answer(uuid.MustParse(task.TaskID), 1); err == nil {
		t.Fatal("accepted stale version")
	}
	if answer, err := e.answer(uuid.MustParse(task.TaskID), 2); err != nil || answer != task.Answer {
		t.Fatal("current answer unavailable")
	}
	if err := os.Chmod(cfg.CatalogFile, 0644); err != nil {
		t.Fatal(err)
	}
	if privateJSON(cfg.CatalogFile, &catalog) == nil {
		t.Fatal("public manifest accepted")
	}
}
func TestControlBoundaryAndRedaction(t *testing.T) {
	e, _ := testEngine(t)
	attachRun(e)
	for _, authorized := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/api/v1/players/test-bots/"+e.run.TournamentID+"/state", nil)
		if authorized {
			req.Header.Set("X-Test-Bots-Key", e.key)
			req.Header.Set("X-Test-Bots-Actor", e.cfg.ControllerID)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		want := 404
		if authorized {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("status %d", w.Code)
		}
		if strings.Contains(w.Body.String(), "synthetic-") || strings.Contains(w.Body.String(), "commands") {
			t.Fatal("private data leaked")
		}
	}
}
func TestGoldenRejectsChangedResult(t *testing.T) {
	e, _ := testEngine(t)
	attachRun(e)
	r := e.run
	r.Scenario = "golden"
	pairs, _ := Pairings(r, 1)
	first := uuid.MustParse(pairs[0].First)
	second := uuid.MustParse(pairs[0].Second)
	id := uuid.New()
	m := Metadata{Configuration: api.TournamentConfiguration{SwissDefault: api.TournamentConfigurationStageDefault{Mode: "admin", Categories: []api.Category{"crypto"}}, SemifinalDefault: api.TournamentConfigurationStageDefault{Mode: "admin", Categories: []api.Category{"crypto"}}, GoldenDefault: api.TournamentConfigurationStageDefault{Categories: []api.Category{"crypto"}}, Series: []api.TournamentConfigurationSeries{{Id: id, Stage: "swiss", RoundNumber: 1, Mode: "admin", Categories: []api.Category{"crypto"}}}}, Series: []api.Series{{Id: id, Format: "bo1", FirstParticipantId: first, SecondParticipantId: second, WinnerId: &second}}}
	if validateGolden(r, m) == nil {
		t.Fatal("wrong winner accepted")
	}
	m.Series[0].WinnerId = &first
	if err := validateGolden(r, m); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []api.TournamentConfigurationSeriesStage{"swiss", "semifinal"} {
		m.Configuration.Series[0].Stage = stage
		m.Configuration.Series[0].Categories = []api.Category{"web"}
		if validateGolden(r, m) == nil {
			t.Fatal("series category override accepted")
		}
		m.Configuration.Series[0].Categories = []api.Category{"crypto"}
		m.Configuration.Series[0].Mode = "draft"
		if validateGolden(r, m) == nil {
			t.Fatal("series draft override accepted")
		}
		m.Configuration.Series[0].Mode = "admin"
	}
}
func TestRetryAfter(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Retry-After", "17"); w.WriteHeader(429) }))
	defer backend.Close()
	err := newClient(Config{BackendURL: backend.URL}).request(context.Background(), "GET", "/test", nil, "", nil)
	var status *apiError
	ok := errors.As(err, &status)
	if !ok || status.Retry != 17*time.Second {
		t.Fatal("Retry-After ignored")
	}
}
func TestAnswerCannotSelectAnotherPlayer(t *testing.T) {
	e, _ := testEngine(t)
	attachRun(e)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.CurrentPlayerResponse{Player: api.PlayerResponse{Id: uuid.New()}})
	}))
	defer backend.Close()
	e.cfg.BackendURL = backend.URL
	if _, err := e.humanAnswer(context.Background(), e.run.TournamentID, "test-cookie"); err == nil {
		t.Fatal("foreign session accepted")
	}
}
