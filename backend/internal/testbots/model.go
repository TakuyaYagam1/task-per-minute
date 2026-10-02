// Package testbots implements an isolated client harness for test Compose stacks.
// It never writes tournament records or replaces player authentication.
package testbots

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/google/uuid"
)

type Config struct {
	BackendURL   string
	Origin       string
	ControllerID string
	AccountsFile string
	CatalogFile  string
	KeyFile      string
	StateDir     string
}

type Account struct {
	Username string `json:"username"`
	Password string `json:"password"`
	PlayerID string `json:"player_id"`
}
type Task struct {
	TaskID   string `json:"task_id"`
	Version  int32  `json:"version"`
	Answer   string `json:"answer"`
	Category string `json:"category"`
	Kind     string `json:"kind"`
}
type Metadata struct {
	Tournament    api.Tournament              `json:"tournament"`
	Roster        api.Roster                  `json:"roster"`
	Configuration api.TournamentConfiguration `json:"configuration"`
	Series        []api.Series                `json:"series"`
	Waves         []api.Wave                  `json:"waves"`
}
type Bot struct {
	Username      string `json:"username"`
	PlayerID      string `json:"player_id"`
	ParticipantID string `json:"participant_id"`
	Slot          int    `json:"slot"`
	Status        string `json:"status"`
	Message       string `json:"message"`
}
type Policy struct {
	Outcome string `json:"outcome"`
	Delay   int    `json:"delay"`
}
type Command struct {
	Key  string `json:"key"`
	Done bool   `json:"done"`
}
type Run struct {
	TournamentID       string             `json:"tournament_id"`
	ControllerID       string             `json:"controller_id"`
	HumanParticipantID string             `json:"human_participant_id"`
	Size               int                `json:"size"`
	Scenario           string             `json:"scenario"`
	Status             string             `json:"status"`
	Message            string             `json:"message"`
	Next               Policy             `json:"next"`
	Bots               []Bot              `json:"bots"`
	Policies           map[string]Policy  `json:"policies"`
	Commands           map[string]Command `json:"commands"`
	Acknowledged       map[string]bool    `json:"acknowledged,omitempty"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// View deliberately excludes the persisted command ledger and private manifests.
type View struct {
	Available    bool   `json:"available"`
	TournamentID string `json:"tournament_id"`
	Status       string `json:"status"`
	Message      string `json:"message"`
	Size         int    `json:"size"`
	Scenario     string `json:"scenario"`
	Next         Policy `json:"next"`
	Bots         []Bot  `json:"bots"`
}
type Action struct {
	Action   string `json:"action"`
	Scenario string `json:"scenario,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
	Delay    int    `json:"delay,omitempty"`
}
type Pair struct {
	First  string `json:"first_participant_id"`
	Second string `json:"second_participant_id"`
}

var errPrivateFile = errors.New("private test data is unavailable or invalid")

// userError is already safe to show in the Russian test control panel.
type userError string

func (m userError) Error() string { return string(m) }

func privateJSON(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 8<<20 {
		return errPrivateFile
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, target) != nil {
		return errPrivateFile
	}
	return nil
}

func atomicJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return errPrivateFile
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-")
	if err != nil {
		return errPrivateFile
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errPrivateFile
	}
	if err = os.Rename(name, path); err != nil {
		return errPrivateFile
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return errPrivateFile
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

func validID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}
func taskKey(id string, version int32) string { return fmt.Sprintf("%s/%d", id, version) }
func rounds(size int) int {
	if size == 8 {
		return 3
	}
	return 4
}

func Pairings(run *Run, round int) ([]Pair, error) {
	if run == nil || run.Scenario != "golden" || round < 1 || round > rounds(run.Size) {
		return nil, errors.New("scenario is unavailable")
	}
	slots := make([]string, run.Size)
	slots[1] = run.HumanParticipantID
	for _, b := range run.Bots {
		if b.Slot < 0 || b.Slot >= len(slots) {
			return nil, errPrivateFile
		}
		slots[b.Slot] = b.ParticipantID
	}
	pairs := make([]Pair, 0, run.Size/2)
	for i, id := range slots {
		if !validID(id) {
			return nil, errors.New("bots are still joining")
		}
		j := i ^ (1 << (round - 1))
		if i < j {
			pairs = append(pairs, Pair{First: id, Second: slots[j]})
		}
	}
	return pairs, nil
}
