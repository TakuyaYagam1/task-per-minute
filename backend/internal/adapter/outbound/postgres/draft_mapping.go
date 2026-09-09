package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func draftRevisionParams(
	draftID uuid.UUID,
	expected DraftRevisionExpectation,
	in DraftRevisionInput,
) (sqlc.AppendDraftRevisionCASParams, error) {
	selected, err := categoryJSON(in.SelectedCategories)
	if err != nil {
		return sqlc.AppendDraftRevisionCASParams{}, fmt.Errorf("DraftPostgres - append revision - categories: %w", err)
	}
	recoveryEvidence, err := nullableJSONObject(in.RecoveryEvidence)
	if err != nil {
		return sqlc.AppendDraftRevisionCASParams{}, domain.ErrValidation
	}
	params := sqlc.AppendDraftRevisionCASParams{
		ID:                   in.ID,
		CommandID:            in.CommandID,
		ServiceEpoch:         in.ServiceEpoch,
		State:                string(in.State),
		TurnNumber:           int16(in.TurnNumber), //nolint:gosec // validation bounds draft turns to 1..4.
		CurrentActorID:       nullableUUID(in.CurrentActorID),
		AbsoluteDeadline:     nullableTSTZ(in.AbsoluteDeadline),
		PausedRemainingMs:    nullableInt32(in.PausedRemainingMS),
		RecoveryReason:       in.RecoveryReason,
		RecoveryEvidence:     recoveryEvidence,
		SelectedCategories:   selected,
		CreatedAt:            tstz(in.CreatedAt),
		ExpectedRevisionID:   expected.ID,
		DraftID:              draftID,
		ExpectedRevision:     expected.Revision,
		ExpectedServiceEpoch: expected.ServiceEpoch,
	}
	if in.CurrentAction != nil {
		action := string(*in.CurrentAction)
		params.CurrentAction = &action
	}
	if in.DecisionEvidence != nil {
		evidence := in.DecisionEvidence
		inputs, marshalErr := marshalJSON(
			"DraftPostgres - append revision - decision inputs",
			evidence.NormalizedInputs,
		)
		if marshalErr != nil {
			return sqlc.AppendDraftRevisionCASParams{}, marshalErr
		}
		result, marshalErr := marshalJSON(
			"DraftPostgres - append revision - decision result",
			evidence.Result,
		)
		if marshalErr != nil {
			return sqlc.AppendDraftRevisionCASParams{}, marshalErr
		}
		purpose := string(evidence.Purpose)
		params.DecisionEvidenceID = uuid.NullUUID{UUID: evidence.ID, Valid: true}
		params.DecisionPurpose = &purpose
		params.DecisionAlgorithmVersion = &evidence.AlgorithmVersion
		params.DecisionInputs = inputs
		params.DecisionSeed = append([]byte(nil), evidence.Seed[:]...)
		params.DecisionResult = result
		params.DecisionReplayDigest = append([]byte(nil), evidence.ReplayDigest[:]...)
		params.DecisionOwnerID = uuid.NullUUID{UUID: evidence.OwnerID, Valid: true}
		params.DecidedAt = tstz(evidence.DecidedAt)
	}
	return params, nil
}

func draftRevisionRecord(row sqlc.DraftRevision) (*DraftRevisionRecord, error) {
	selected, err := decodeCategories(row.SelectedCategories)
	if err != nil {
		return nil, err
	}
	recoveryEvidence := make(map[string]any)
	if len(row.RecoveryEvidence) > 0 {
		if err := json.Unmarshal(row.RecoveryEvidence, &recoveryEvidence); err != nil {
			return nil, err
		}
	}
	record := &DraftRevisionRecord{
		ID:                 row.ID,
		DraftID:            row.DraftID,
		SeriesID:           row.SeriesID,
		RosterID:           row.RosterID,
		Revision:           row.Revision,
		CommandID:          row.CommandID,
		ServiceEpoch:       row.ServiceEpoch,
		State:              DraftPersistenceState(row.State),
		TurnNumber:         int(row.TurnNumber),
		AbsoluteDeadline:   nullableTime(row.AbsoluteDeadline),
		RecoveryReason:     row.RecoveryReason,
		RecoveryEvidence:   recoveryEvidence,
		SelectedCategories: selected,
		CreatedAt:          row.CreatedAt.Time,
	}
	if row.PreviousRevisionID.Valid {
		value := row.PreviousRevisionID.UUID
		record.PreviousRevisionID = &value
	}
	if row.CurrentActorID.Valid {
		value := row.CurrentActorID.UUID
		record.CurrentActorID = &value
	}
	if row.CurrentAction != nil {
		value := domain.DraftActionType(*row.CurrentAction)
		record.CurrentAction = &value
	}
	if row.PausedRemainingMs != nil {
		value := int(*row.PausedRemainingMs)
		record.PausedRemainingMS = &value
	}
	if row.DecisionEvidenceID.Valid {
		if row.DecisionPurpose == nil || row.DecisionAlgorithmVersion == nil || !row.DecisionOwnerID.Valid ||
			!row.DecidedAt.Valid {
			return nil, domain.ErrValidation
		}
		evidence, evidenceErr := decisionEvidenceFromStorage(
			row.DecisionEvidenceID.UUID,
			domain.DecisionPurpose(*row.DecisionPurpose),
			*row.DecisionAlgorithmVersion,
			row.DecisionInputs,
			row.DecisionSeed,
			row.DecisionResult,
			row.DecisionReplayDigest,
			row.DecisionOwnerID.UUID,
			row.DecidedAt.Time,
		)
		if evidenceErr != nil {
			return nil, evidenceErr
		}
		record.DecisionEvidence = &evidence
	}
	return record, nil
}

func draftActionRecord(row sqlc.DraftAction) DraftActionRecord {
	return DraftActionRecord{
		ID:                row.ID,
		DraftID:           row.DraftID,
		ResultRevisionID:  row.ResultRevisionID,
		CommandID:         row.CommandID,
		TurnNumber:        int(row.TurnNumber),
		ActorID:           row.ActorID,
		Action:            domain.DraftActionType(row.Action),
		Category:          domain.Category(row.Category),
		ScheduledDeadline: row.ScheduledDeadline.Time,
		OccurredAt:        row.OccurredAt.Time,
		Automatic:         row.Automatic,
		CreatedAt:         row.CreatedAt.Time,
	}
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

func nullableJSONObject(value map[string]any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "{}" {
		return nil, domain.ErrValidation
	}
	return data, nil
}

func nullableInt32(value *int) *int32 {
	if value == nil {
		return nil
	}
	if *value < math.MinInt32 || *value > math.MaxInt32 {
		return nil
	}
	out := int32(*value)
	return &out
}

func pgErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
