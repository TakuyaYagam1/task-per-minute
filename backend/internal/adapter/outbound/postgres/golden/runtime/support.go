package runtime

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func taskHintsToDomain(hint1, hint2, hint3 *string) []string {
	hints, _ := domain.NormalizeTaskHints([]string{
		stringValue(hint1),
		stringValue(hint2),
		stringValue(hint3),
	})
	return hints
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func requiredJSONObject(value map[string]any) ([]byte, error) {
	if len(value) == 0 {
		return nil, domain.ErrValidation
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "{}" {
		return nil, domain.ErrValidation
	}
	return data, nil
}

func marshalJSON(operation string, value any) ([]byte, error) {
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Slice && reflected.IsNil() {
		return []byte("[]"), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s - marshal JSON: %w", operation, err)
	}
	return data, nil
}

func categoryJSON(categories []domain.Category) ([]byte, error) {
	values := make([]string, len(categories))
	for index, category := range categories {
		values[index] = string(category)
	}
	return marshalJSON("category sequence", values)
}

func taskSnapshotParams(
	edge assignmentrepo.AssignmentEdgeInput,
	createdAt time.Time,
) (sqlc.CreateAssignmentTaskSnapshotParams, error) {
	snapshot := edge.Snapshot
	hints, err := marshalJSON("AssignmentPostgres - CreateExactPlan - task hints", snapshot.Hints)
	if err != nil {
		return sqlc.CreateAssignmentTaskSnapshotParams{}, err
	}
	return sqlc.CreateAssignmentTaskSnapshotParams{
		ID:            snapshot.SnapshotID,
		ReservationID: edge.ReservationID,
		TaskID:        snapshot.TaskID,
		TaskVersion:   int32(snapshot.Version), //nolint:gosec // exact-plan validation bounds versions to PostgreSQL int4.
		Kind:          string(snapshot.Kind),
		Title:         snapshot.Title,
		Description:   snapshot.Description,
		Category:      string(snapshot.Category),
		Difficulty:    string(snapshot.Difficulty),
		TimeLimit:     int32(snapshot.TimeLimit), //nolint:gosec // exact-plan validation bounds time limits to PostgreSQL int4.
		Flag:          snapshot.Flag,
		Hints:         hints,
		TaskUrl:       snapshot.TaskURL,
		SourceFileUrl: snapshot.SourceFileURL,
		ContentDigest: append([]byte(nil), edge.ContentDigest[:]...),
		CreatedAt:     tstz(createdAt),
	}, nil
}

func cloneParticipantStateString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
