package taskexec

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type SnapshotInput struct {
	SnapshotID uuid.UUID
	Version    int
	Kind       domain.ArenaTaskKind
	Task       domain.Task
}

func CloneTask(task *domain.Task) *domain.Task {
	if task == nil {
		return nil
	}
	cloned := *task
	cloned.Hints = append([]string(nil), task.Hints...)
	cloned.TaskURL = cloneStringPointer(task.TaskURL)
	cloned.SourceFileURL = cloneStringPointer(task.SourceFileURL)
	return &cloned
}

func BuildSnapshot(input SnapshotInput) (domain.ArenaTaskSnapshot, error) {
	task := CloneTask(&input.Task)
	return domain.NewArenaTaskSnapshot(input.SnapshotID, input.Version, input.Kind, *task)
}

func SnapshotDigest(snapshot domain.ArenaTaskSnapshot) ([sha256.Size]byte, error) {
	if err := snapshot.Validate(); err != nil {
		return [sha256.Size]byte{}, err
	}
	document := snapshotDigestDocument{
		SnapshotID: snapshot.SnapshotID.String(), TaskID: snapshot.TaskID.String(),
		Version: snapshot.Version, Kind: snapshot.Kind, Title: snapshot.Title,
		Description: snapshot.Description, Category: snapshot.Category, Difficulty: snapshot.Difficulty,
		TimeLimit: snapshot.TimeLimit, Flag: snapshot.Flag, Hints: append([]string(nil), snapshot.Hints...),
		TaskURL: cloneStringPointer(snapshot.TaskURL), SourceFileURL: cloneStringPointer(snapshot.SourceFileURL),
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

type snapshotDigestDocument struct {
	SnapshotID    string               `json:"snapshot_id"`
	TaskID        string               `json:"task_id"`
	Version       int                  `json:"version"`
	Kind          domain.ArenaTaskKind `json:"kind"`
	Title         string               `json:"title"`
	Description   string               `json:"description"`
	Category      domain.Category      `json:"category"`
	Difficulty    domain.Difficulty    `json:"difficulty"`
	TimeLimit     int                  `json:"time_limit"`
	Flag          string               `json:"flag"`
	Hints         []string             `json:"hints"`
	TaskURL       *string              `json:"task_url"`
	SourceFileURL *string              `json:"source_file_url"`
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
