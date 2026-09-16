package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

const (
	resultActorServer = "server"
	resultOutboxTopic = "tournament.result.committed"
)

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func optionalTrimmedString(value string) *string {
	if value == "" {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	return &trimmed
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

func mapRepositoryWriteError(operation string, err error) error {
	return resultrepo.MapRepositoryWriteError(operation, err)
}

func resultCASWriteError(operation string, err error) error {
	return resultrepo.ResultCASWriteError(operation, err)
}

func lockTournamentResultScope(ctx context.Context, q *sqlc.Queries, tournamentID, rosterID uuid.UUID) error {
	_, err := q.LockTournamentResultScope(ctx, sqlc.LockTournamentResultScopeParams{
		TournamentID: tournamentID,
		RosterID:     uuid.NullUUID{UUID: rosterID, Valid: rosterID != uuid.Nil},
	})
	return err
}
