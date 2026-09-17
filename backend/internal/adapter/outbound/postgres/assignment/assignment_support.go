package assignment

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

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

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	out := value.Time
	return &out
}

func validServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func decisionEvidenceFromStorage(
	id uuid.UUID,
	purpose domain.DecisionPurpose,
	algorithm string,
	inputsJSON []byte,
	seed []byte,
	resultJSON []byte,
	digest []byte,
	ownerID uuid.UUID,
	decidedAt time.Time,
) (domain.DecisionEvidence, error) {
	if len(seed) != domain.DecisionSeedSize || len(digest) != domain.DecisionSeedSize {
		return domain.DecisionEvidence{}, domain.ErrValidation
	}
	var inputs, result []string
	if err := json.Unmarshal(inputsJSON, &inputs); err != nil {
		return domain.DecisionEvidence{}, err
	}
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return domain.DecisionEvidence{}, err
	}
	evidence := domain.DecisionEvidence{
		ID:               id,
		Purpose:          purpose,
		AlgorithmVersion: algorithm,
		NormalizedInputs: inputs,
		Result:           result,
		OwnerID:          ownerID,
		DecidedAt:        decidedAt.UTC(),
	}
	copy(evidence.Seed[:], seed)
	copy(evidence.ReplayDigest[:], digest)
	if err := evidence.Validate(); err != nil {
		return domain.DecisionEvidence{}, err
	}
	return evidence, nil
}

func categoryJSON(categories []domain.Category) ([]byte, error) {
	values := make([]string, len(categories))
	for index, category := range categories {
		values[index] = string(category)
	}
	return marshalJSON("category sequence", values)
}

func decodeCategories(data []byte) ([]domain.Category, error) {
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	categories := make([]domain.Category, len(values))
	seen := make(map[domain.Category]struct{}, len(values))
	for index, value := range values {
		category := domain.Category(value)
		if !category.IsValid() {
			return nil, domain.ErrValidation
		}
		if _, duplicate := seen[category]; duplicate {
			return nil, domain.ErrValidation
		}
		seen[category] = struct{}{}
		categories[index] = category
	}
	return categories, nil
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
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514", "40001":
			return domain.WrapError(err, domain.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
