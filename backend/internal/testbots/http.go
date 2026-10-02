package testbots

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

//nolint:gocyclo // The small internal control protocol validates credentials, run identity and each action before dispatch.
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/health" && r.Method == "GET" {
		writeJSON(w, 200, map[string]bool{"ready": true})
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Test-Bots-Key")), []byte(e.key)) != 1 || r.Header.Get("X-Test-Bots-Actor") != e.cfg.ControllerID {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 6 || parts[0] != "api" || parts[1] != "v1" || (parts[2] != "players" && parts[2] != "admin") || parts[3] != "test-bots" || !validID(parts[4]) {
		http.NotFound(w, r)
		return
	}
	id, route := parts[4], parts[5]
	e.mu.Lock()
	defer e.mu.Unlock()
	fail := func(message string) { writeJSON(w, http.StatusConflict, map[string]string{"message": message}) }
	if parts[2] == "admin" {
		if route != "pairings" || r.Method != "GET" || e.run == nil || e.run.TournamentID != id {
			http.NotFound(w, r)
			return
		}
		round, err := strconv.Atoi(r.URL.Query().Get("round"))
		if err != nil {
			fail("Укажите раунд.")
			return
		}
		pairs, err := Pairings(e.run, round)
		if err != nil {
			fail("Сценарий Golden или состав еще не готов.")
			return
		}
		writeJSON(w, 200, map[string]any{"pairs": pairs})
		return
	}
	switch {
	case route == "state" && r.Method == "GET":
		writeJSON(w, 200, e.view(id))
	case route == "answer" && r.Method == "POST":
		answer, err := e.humanAnswer(r.Context(), id, r.Header.Get("Cookie"))
		if err != nil {
			fail(err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"answer": answer})
	case route == "actions" && r.Method == "POST":
		var action Action
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&action) != nil || decoder.Decode(new(any)) != io.EOF {
			writeJSON(w, 400, map[string]string{"message": "Некорректная команда."})
			return
		}
		if action.Action == "start" {
			if err := e.startRun(r.Context(), id, action, r.Header.Get("Cookie")); err != nil {
				fail(err.Error())
				return
			}
		} else {
			if e.run == nil || e.run.TournamentID != id || (e.run.Status != "running" && e.run.Status != "paused") {
				fail("Нет активного прогона.")
				return
			}
			switch action.Action {
			case "pause":
				e.run.Status = "paused"
				e.run.Message = "Боты приостановлены. Таймеры турнира продолжают идти."
			case "resume":
				if err := e.resume(r.Context()); err != nil {
					fail(err.Error())
					return
				}
			case "configure":
				if (action.Outcome != "human_wins" && action.Outcome != "bot_wins") || action.Delay < 2 || action.Delay > 120 {
					writeJSON(w, 400, map[string]string{"message": "Задержка от 2 до 120 секунд."})
					return
				}
				// Capture already started series before replacing the next-series policy,
				// even when the scheduler has not observed their start yet.
				metadata, err := e.metadata(r.Context(), id)
				if err != nil {
					fail("Не удалось проверить текущие серии.")
					return
				}
				for _, series := range metadata.Series {
					e.policy(series)
				}
				e.run.Next = Policy{Outcome: action.Outcome, Delay: action.Delay}
			default:
				writeJSON(w, 400, map[string]string{"message": "Неизвестная команда."})
				return
			}
		}
		if err := e.save(); err != nil {
			e.run.Status = "paused"
			fail("Не удалось сохранить состояние.")
			return
		}
		writeJSON(w, 200, e.view(id))
	default:
		http.NotFound(w, r)
	}
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
