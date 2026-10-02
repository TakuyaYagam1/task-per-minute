package testbots

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

func TestSocketReconnectRetainsResumeAndPresence(t *testing.T) {
	id, resume := uuid.NewString(), uuid.NewString()
	var count atomic.Int32
	reconnected := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		n := count.Add(1)
		data, _ := json.Marshal(map[string]any{"type": "tournament.participant", "payload": map[string]any{"envelope": map[string]string{"tournament_id": id, "resume_id": resume}}})
		_ = connection.Write(r.Context(), websocket.MessageText, data)
		if n == 1 {
			return
		}
		select {
		case reconnected <- r.URL.Query().Get("resume_id"):
		default:
		}
		_, _, _ = connection.Read(r.Context())
	}))
	defer server.Close()
	c := newClient(Config{BackendURL: server.URL, Origin: server.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer c.close()
	c.connect(ctx, id)
	select {
	case got := <-reconnected:
		if got != resume {
			t.Fatal("lost resume cursor")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("socket did not reconnect")
	}
	deadline := time.Now().Add(time.Second)
	for !c.isConnected() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !c.isConnected() {
		t.Fatal("no participant presence after reconnect")
	}
}

func TestSecureSessionUsesPrivateBackendTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "tpm_player_session", Value: "synthetic", Secure: true, HttpOnly: true, Path: "/"})
			return
		}
		cookie, err := r.Cookie("tpm_player_session")
		if err != nil || cookie.Value != "synthetic" || r.Header.Get("Origin") != "https://test.example.invalid" {
			t.Error("secure session or origin was lost")
		}
	}))
	defer server.Close()
	c := newClient(Config{BackendURL: server.URL, Origin: "https://test.example.invalid"})
	if err := c.request(t.Context(), "POST", "/login", nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.request(t.Context(), "GET", "/me", nil, "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestSeriesPolicyLocksAtStartAcrossBO3Games(t *testing.T) {
	e, _ := testEngine(t)
	attachRun(e)
	series := api.Series{Id: uuid.New(), Format: "bo3", State: "ready"}
	e.policy(series)
	e.run.Next = Policy{Outcome: "bot_wins", Delay: 20}
	series.State = "active"
	if p := e.policy(series); p.Outcome != "bot_wins" {
		t.Fatal("policy locked before start")
	}
	e.run.Next = Policy{Outcome: "human_wins", Delay: 15}
	series.State = "ready"
	if p := e.policy(series); p.Outcome != "bot_wins" || p.Delay != 20 {
		t.Fatal("policy changed between games")
	}
	series.Id = uuid.New()
	series.State = "draft"
	if p := e.policy(series); p.Outcome != "human_wins" {
		t.Fatal("next series did not use new policy")
	}
}

func TestConfigureCapturesStartedSeriesBeforeScheduler(t *testing.T) {
	e, _ := testEngine(t)
	attachRun(e)
	series := api.Series{Id: uuid.New(), State: "active", Format: "bo3"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Metadata{Series: []api.Series{series}})
	}))
	defer server.Close()
	e.service = newClient(Config{BackendURL: server.URL})
	req := httptest.NewRequest("POST", "/api/v1/players/test-bots/"+e.run.TournamentID+"/actions", strings.NewReader(`{"action":"configure","outcome":"bot_wins","delay":20}`))
	req.Header.Set("X-Test-Bots-Key", e.key)
	req.Header.Set("X-Test-Bots-Actor", e.cfg.ControllerID)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, req)
	if response.Code != 200 || e.run.Next.Outcome != "bot_wins" || e.run.Policies[series.Id.String()].Outcome != "human_wins" {
		t.Fatal("configure changed an already started series")
	}
}

func TestMissingVersionStopsEvenLosingBot(t *testing.T) {
	e, _ := testEngine(t)
	attachRun(e)
	bot := &e.run.Bots[1]
	now, end := time.Now().Add(-time.Second), time.Now().Add(time.Minute)
	s := api.ParticipantRecoverySnapshot{Series: &api.Series{Id: uuid.New(), FirstParticipantId: uuid.MustParse(bot.ParticipantID), SecondParticipantId: uuid.MustParse(e.run.HumanParticipantID)}, Assignment: &api.ParticipantAssignment{}}
	s.Assignment.Context.StartedAt = &now
	s.Assignment.Context.EffectiveDeadline = &end
	s.Assignment.Context.GameState = "active"
	s.Assignment.ActiveSnapshot.TaskId = uuid.New()
	s.Assignment.ActiveSnapshot.Version = 99
	if err := e.play(context.Background(), newClient(e.cfg), bot, "/unused", s); err == nil {
		t.Fatal("unknown assignment was silently ignored")
	}
}

func TestUnavailableBackendSchedulesRetry(t *testing.T) {
	e, _ := testEngine(t)
	attachRun(e)
	b := &e.run.Bots[0]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := newClient(e.cfg).request(ctx, "GET", "/unavailable", nil, "", nil)
	e.handle(b, err)
	if b.Status == "error" || !e.nextTry[b.PlayerID].After(time.Now()) {
		t.Fatal("transport interruption was not retried")
	}
}

func TestPersistentServerFailureStopsAfterBoundedRetries(t *testing.T) {
	e, _ := testEngine(t)
	attachRun(e)
	b := &e.run.Bots[0]
	for i := 0; i < 2; i++ {
		e.handle(b, &apiError{Status: 500})
		if b.Status == "error" {
			t.Fatal("transient server failure stopped the bot immediately")
		}
	}
	e.handle(b, &apiError{Status: 500})
	if b.Status != "error" {
		t.Fatal("persistent server error was hidden by unlimited retries")
	}
}

func TestPauseRetainsPresenceWithoutPlayerCommands(t *testing.T) {
	e, _ := testEngine(t)
	attachRun(e)
	e.run.Status = "paused"
	closed := false
	for _, bot := range e.run.Bots {
		e.logged[bot.PlayerID] = true
		c := newClient(e.cfg)
		c.connected = true
		c.wsCancel = func() { closed = true }
		e.clients[bot.PlayerID] = c
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/internal/test-bots/"+e.run.TournamentID+"/metadata" {
			t.Error("paused runner attempted a player command")
		}
		_ = json.NewEncoder(w).Encode(Metadata{Tournament: api.Tournament{State: "swiss"}, Roster: api.Roster{Locked: true}})
	}))
	defer server.Close()
	e.service = newClient(Config{BackendURL: server.URL})
	e.tick(t.Context())
	if closed || len(e.run.Commands) != 0 {
		t.Fatal("pause closed presence or issued a game command")
	}
	for _, bot := range e.run.Bots {
		if bot.Status != "paused" || !e.clients[bot.PlayerID].isConnected() {
			t.Fatal("pause did not preserve connected bot")
		}
	}
}
