package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func tournamentStringList(payload []byte, field string) ([]string, error) {
	values := []string{}
	if err := json.Unmarshal(payload, &values); err != nil {
		return nil, tournamentSnapshotInvalidError(field)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return nil, tournamentSnapshotInvalidError(field)
		}
	}
	return values, nil
}

func optionalTournamentUUID(value string) (*uuid.UUID, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil {
		return nil, ErrTournamentSnapshotInvalid
	}
	return &parsed, nil
}

func requiredTournamentUUID(value string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil {
		return uuid.Nil, ErrTournamentSnapshotInvalid
	}
	return parsed, nil
}

func utcNullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	utc := value.Time.UTC()
	return &utc
}

func tournamentSnapshotLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrTournamentNotFound
	}
	return fmt.Errorf("TournamentSnapshotPostgres - %s: %w", operation, err)
}

func tournamentSnapshotInvalidError(field string) error {
	return fmt.Errorf("%w: %s", ErrTournamentSnapshotInvalid, field)
}
