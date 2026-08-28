package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type ArenaDraftPersistenceState string

const (
	ArenaDraftPersistenceStateActive           ArenaDraftPersistenceState = "active"
	ArenaDraftPersistenceStatePaused           ArenaDraftPersistenceState = "paused"
	ArenaDraftPersistenceStateRecoveryRequired ArenaDraftPersistenceState = "recovery_required"
	ArenaDraftPersistenceStateCompleted        ArenaDraftPersistenceState = "completed"
	ArenaDraftPersistenceStateSuperseded       ArenaDraftPersistenceState = "superseded"
)

var ErrArenaDraftNotFound = errors.New("arena draft repository: draft not found")

type ArenaDraftPostgres struct {
	tx *TxManager
}

type ArenaDraftCreateInput struct {
	ID                  uuid.UUID
	SeriesID            uuid.UUID
	RosterID            uuid.UUID
	CategoryRevisionID  uuid.UUID
	CategoryRevision    int64
	SourcePoolRevision  uuid.UUID
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	Format              domain.ArenaSeriesFormat
	Pool                []domain.Category
	InitialRevisionID   uuid.UUID
	CommandID           uuid.UUID
	ServiceEpoch        uuid.UUID
	AbsoluteDeadline    time.Time
	DecisionEvidence    domain.ArenaDecisionEvidence
	CreatedAt           time.Time
}

type ArenaDraftRevisionExpectation struct {
	ID           uuid.UUID
	Revision     int64
	ServiceEpoch uuid.UUID
}

type ArenaDraftActionInput struct {
	ID                uuid.UUID
	TurnNumber        int
	ActorID           uuid.UUID
	Action            domain.ArenaDraftActionType
	Category          domain.Category
	ScheduledDeadline time.Time
	OccurredAt        time.Time
	Automatic         bool
}

type ArenaDraftRevisionInput struct {
	ID                 uuid.UUID
	CommandID          uuid.UUID
	ServiceEpoch       uuid.UUID
	State              ArenaDraftPersistenceState
	TurnNumber         int
	CurrentActorID     *uuid.UUID
	CurrentAction      *domain.ArenaDraftActionType
	AbsoluteDeadline   *time.Time
	PausedRemainingMS  *int
	RecoveryReason     *string
	RecoveryEvidence   map[string]any
	SelectedCategories []domain.Category
	DecisionEvidence   *domain.ArenaDecisionEvidence
	Action             *ArenaDraftActionInput
	CreatedAt          time.Time
}

type ArenaDraftIdentityRecord struct {
	ID                  uuid.UUID
	SeriesID            uuid.UUID
	RosterID            uuid.UUID
	CategoryRevisionID  uuid.UUID
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	Format              domain.ArenaSeriesFormat
	CreatedAt           time.Time
}

type ArenaDraftRevisionRecord struct {
	ID                 uuid.UUID
	DraftID            uuid.UUID
	SeriesID           uuid.UUID
	RosterID           uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	CommandID          uuid.UUID
	ServiceEpoch       uuid.UUID
	State              ArenaDraftPersistenceState
	TurnNumber         int
	CurrentActorID     *uuid.UUID
	CurrentAction      *domain.ArenaDraftActionType
	AbsoluteDeadline   *time.Time
	PausedRemainingMS  *int
	RecoveryReason     *string
	RecoveryEvidence   map[string]any
	SelectedCategories []domain.Category
	DecisionEvidence   *domain.ArenaDecisionEvidence
	CreatedAt          time.Time
}

type ArenaDraftActionRecord struct {
	ID                uuid.UUID
	DraftID           uuid.UUID
	ResultRevisionID  uuid.UUID
	CommandID         uuid.UUID
	TurnNumber        int
	ActorID           uuid.UUID
	Action            domain.ArenaDraftActionType
	Category          domain.Category
	ScheduledDeadline time.Time
	OccurredAt        time.Time
	Automatic         bool
	CreatedAt         time.Time
}

type ArenaDraftAggregate struct {
	Draft            ArenaDraftIdentityRecord
	CategoryRevision int64
	Pool             []domain.Category
	Revisions        []ArenaDraftRevisionRecord
	Actions          []ArenaDraftActionRecord
}

func NewArenaDraftPostgres(tx *TxManager) *ArenaDraftPostgres {
	return &ArenaDraftPostgres{tx: tx}
}

func (r *ArenaDraftPostgres) Create(
	ctx context.Context,
	in ArenaDraftCreateInput,
) (*ArenaDraftAggregate, error) {
	if err := validateArenaDraftCreateInput(in); err != nil {
		return nil, err
	}
	categoryPool := categoryJSON(in.Pool)
	action := string(domain.ArenaDraftActionBan)
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.CreateArenaDraftCategoryRevision(
			txCtx,
			sqlc.CreateArenaDraftCategoryRevisionParams{
				ID:                   in.CategoryRevisionID,
				SeriesID:             in.SeriesID,
				RosterID:             in.RosterID,
				Revision:             in.CategoryRevision,
				SourcePoolRevisionID: in.SourcePoolRevision,
				CategoryPool:         categoryPool,
				CreatedAt:            tstz(in.CreatedAt),
			},
		); err != nil {
			return err
		}
		if _, err := querier.CreateArenaDraft(txCtx, sqlc.CreateArenaDraftParams{
			ID:                  in.ID,
			SeriesID:            in.SeriesID,
			RosterID:            in.RosterID,
			CategoryRevisionID:  in.CategoryRevisionID,
			FirstParticipantID:  in.FirstParticipantID,
			SecondParticipantID: in.SecondParticipantID,
			Format:              string(in.Format),
			CreatedAt:           tstz(in.CreatedAt),
		}); err != nil {
			return err
		}
		_, err := querier.CreateInitialArenaDraftRevision(
			txCtx,
			sqlc.CreateInitialArenaDraftRevisionParams{
				ID:                       in.InitialRevisionID,
				DraftID:                  in.ID,
				SeriesID:                 in.SeriesID,
				RosterID:                 in.RosterID,
				CommandID:                in.CommandID,
				ServiceEpoch:             in.ServiceEpoch,
				CurrentActorID:           uuid.NullUUID{UUID: in.FirstParticipantID, Valid: true},
				CurrentAction:            &action,
				AbsoluteDeadline:         tstz(in.AbsoluteDeadline),
				DecisionEvidenceID:       uuid.NullUUID{UUID: in.DecisionEvidence.ID, Valid: true},
				DecisionAlgorithmVersion: &in.DecisionEvidence.AlgorithmVersion,
				DecisionInputs:           mustJSON(in.DecisionEvidence.NormalizedInputs),
				DecisionSeed:             append([]byte(nil), in.DecisionEvidence.Seed[:]...),
				DecisionResult:           mustJSON(in.DecisionEvidence.Result),
				DecisionReplayDigest:     append([]byte(nil), in.DecisionEvidence.ReplayDigest[:]...),
				DecisionOwnerID:          uuid.NullUUID{UUID: in.DecisionEvidence.OwnerID, Valid: true},
				DecidedAt:                tstz(in.DecisionEvidence.DecidedAt),
				CreatedAt:                tstz(in.CreatedAt),
			},
		)
		return err
	})
	if err != nil {
		return nil, mapArenaRepositoryWriteError("ArenaDraftPostgres - Create", err)
	}
	return r.Get(ctx, in.ID)
}

func (r *ArenaDraftPostgres) AppendRevision(
	ctx context.Context,
	draftID uuid.UUID,
	expected ArenaDraftRevisionExpectation,
	in ArenaDraftRevisionInput,
) (*ArenaDraftRevisionRecord, bool, error) {
	if err := validateArenaDraftRevisionInput(draftID, expected, in); err != nil {
		return nil, false, err
	}
	var inserted sqlc.ArenaDraftRevision
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		params, err := arenaDraftRevisionParams(draftID, expected, in)
		if err != nil {
			return err
		}
		querier := r.tx.Querier(txCtx)
		inserted, err = querier.AppendArenaDraftRevisionCAS(txCtx, params)
		if err != nil {
			return err
		}
		if in.Action == nil {
			return nil
		}
		_, err = querier.CreateArenaDraftAction(txCtx, sqlc.CreateArenaDraftActionParams{
			ID:                in.Action.ID,
			DraftID:           draftID,
			ResultRevisionID:  in.ID,
			CommandID:         in.CommandID,
			TurnNumber:        int16(in.Action.TurnNumber), //nolint:gosec // validation bounds draft turns to 1..4.
			ActorID:           in.Action.ActorID,
			Action:            string(in.Action.Action),
			Category:          string(in.Action.Category),
			ScheduledDeadline: tstz(in.Action.ScheduledDeadline),
			OccurredAt:        tstz(in.Action.OccurredAt),
			Automatic:         in.Action.Automatic,
			CreatedAt:         tstz(in.CreatedAt),
		})
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || pgErrorCode(err) == "40001" {
			return nil, false, nil
		}
		return nil, false, mapArenaRepositoryWriteError("ArenaDraftPostgres - AppendRevision", err)
	}
	record, err := arenaDraftRevisionRecord(inserted)
	if err != nil {
		return nil, false, fmt.Errorf("ArenaDraftPostgres - AppendRevision - map row: %w", err)
	}
	return record, true, nil
}

func (r *ArenaDraftPostgres) Get(
	ctx context.Context,
	draftID uuid.UUID,
) (*ArenaDraftAggregate, error) {
	if draftID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	draft, err := querier.GetArenaDraft(ctx, draftID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArenaDraftNotFound
		}
		return nil, fmt.Errorf("ArenaDraftPostgres - Get: %w", err)
	}
	categoryRevision, err := querier.GetArenaDraftCategoryRevision(ctx, draft.CategoryRevisionID)
	if err != nil {
		return nil, fmt.Errorf("ArenaDraftPostgres - Get - category revision: %w", err)
	}
	pool, err := decodeCategories(categoryRevision.CategoryPool)
	if err != nil {
		return nil, fmt.Errorf("ArenaDraftPostgres - Get - category pool: %w", err)
	}
	revisionRows, err := querier.ListArenaDraftRevisions(ctx, draftID)
	if err != nil {
		return nil, fmt.Errorf("ArenaDraftPostgres - Get - revisions: %w", err)
	}
	revisions := make([]ArenaDraftRevisionRecord, 0, len(revisionRows))
	for _, row := range revisionRows {
		record, mapErr := arenaDraftRevisionRecord(row)
		if mapErr != nil {
			return nil, fmt.Errorf("ArenaDraftPostgres - Get - revision: %w", mapErr)
		}
		revisions = append(revisions, *record)
	}
	actionRows, err := querier.ListArenaDraftActions(ctx, draftID)
	if err != nil {
		return nil, fmt.Errorf("ArenaDraftPostgres - Get - actions: %w", err)
	}
	actions := make([]ArenaDraftActionRecord, len(actionRows))
	for index, row := range actionRows {
		actions[index] = arenaDraftActionRecord(row)
	}
	return &ArenaDraftAggregate{
		Draft: ArenaDraftIdentityRecord{
			ID:                  draft.ID,
			SeriesID:            draft.SeriesID,
			RosterID:            draft.RosterID,
			CategoryRevisionID:  draft.CategoryRevisionID,
			FirstParticipantID:  draft.FirstParticipantID,
			SecondParticipantID: draft.SecondParticipantID,
			Format:              domain.ArenaSeriesFormat(draft.Format),
			CreatedAt:           draft.CreatedAt.Time,
		},
		CategoryRevision: categoryRevision.Revision,
		Pool:             pool,
		Revisions:        revisions,
		Actions:          actions,
	}, nil
}

func validateArenaDraftCreateInput(in ArenaDraftCreateInput) error {
	if !validArenaDraftCreateIdentity(in) || !validArenaDraftCreateEvidence(in) {
		return domain.ErrValidation
	}
	if _, err := domain.NewArenaDraft(
		in.ID,
		in.SeriesID,
		in.Format,
		in.FirstParticipantID,
		in.SecondParticipantID,
		in.Pool,
		in.AbsoluteDeadline,
	); err != nil {
		return domain.WrapError(err, domain.ErrValidation)
	}
	return nil
}

func validArenaDraftCreateIdentity(in ArenaDraftCreateInput) bool {
	return in.ID != uuid.Nil && in.SeriesID != uuid.Nil && in.RosterID != uuid.Nil &&
		in.CategoryRevisionID != uuid.Nil && in.CategoryRevision >= 1 && in.SourcePoolRevision != uuid.Nil &&
		in.InitialRevisionID != uuid.Nil && in.CommandID != uuid.Nil && in.ServiceEpoch != uuid.Nil
}

func validArenaDraftCreateEvidence(in ArenaDraftCreateInput) bool {
	if !validServerTime(in.CreatedAt) || !validServerTime(in.AbsoluteDeadline) ||
		!in.AbsoluteDeadline.After(in.CreatedAt) {
		return false
	}
	if err := in.DecisionEvidence.Validate(); err != nil {
		return false
	}
	return in.DecisionEvidence.Purpose == domain.ArenaDecisionPurposeDraftOrder &&
		in.DecisionEvidence.OwnerID == in.ID && !in.DecisionEvidence.DecidedAt.After(in.CreatedAt)
}

func validateArenaDraftRevisionInput(
	draftID uuid.UUID,
	expected ArenaDraftRevisionExpectation,
	in ArenaDraftRevisionInput,
) error {
	if !validArenaDraftRevisionIdentity(draftID, expected, in) {
		return domain.ErrValidation
	}
	if !validArenaDraftRevisionState(in) || !validArenaDraftActionInput(in) ||
		!validArenaDraftRevisionDecision(draftID, in) {
		return domain.ErrValidation
	}
	return nil
}

func validArenaDraftRevisionIdentity(
	draftID uuid.UUID,
	expected ArenaDraftRevisionExpectation,
	in ArenaDraftRevisionInput,
) bool {
	return draftID != uuid.Nil && expected.ID != uuid.Nil && expected.Revision >= 1 &&
		expected.ServiceEpoch != uuid.Nil && in.ID != uuid.Nil && in.CommandID != uuid.Nil &&
		in.ServiceEpoch != uuid.Nil && in.State.IsValid() && in.TurnNumber >= 1 && in.TurnNumber <= 4 &&
		validServerTime(in.CreatedAt)
}

func validArenaDraftRevisionState(in ArenaDraftRevisionInput) bool {
	if (in.CurrentActorID == nil) != (in.CurrentAction == nil) {
		return false
	}
	if in.CurrentActorID != nil && (*in.CurrentActorID == uuid.Nil || !in.CurrentAction.IsValid()) {
		return false
	}
	if in.AbsoluteDeadline != nil && !validServerTime(*in.AbsoluteDeadline) {
		return false
	}
	if in.PausedRemainingMS != nil && (*in.PausedRemainingMS < 1 || *in.PausedRemainingMS > 15000) {
		return false
	}
	for _, category := range in.SelectedCategories {
		if !category.IsValid() {
			return false
		}
	}
	return true
}

func validArenaDraftActionInput(in ArenaDraftRevisionInput) bool {
	if in.Action == nil {
		return true
	}
	action := in.Action
	if action.ID == uuid.Nil || action.TurnNumber < 1 || action.TurnNumber > 4 || action.ActorID == uuid.Nil ||
		!action.Action.IsValid() || !action.Category.IsValid() || !validServerTime(action.ScheduledDeadline) ||
		!validServerTime(action.OccurredAt) || action.OccurredAt.After(action.ScheduledDeadline) {
		return false
	}
	if in.State == ArenaDraftPersistenceStateActive {
		return action.TurnNumber+1 == in.TurnNumber
	}
	return in.State == ArenaDraftPersistenceStateCompleted && action.TurnNumber == in.TurnNumber
}

func validArenaDraftRevisionDecision(draftID uuid.UUID, in ArenaDraftRevisionInput) bool {
	if in.DecisionEvidence != nil {
		if err := in.DecisionEvidence.Validate(); err != nil || in.DecisionEvidence.OwnerID != draftID ||
			in.DecisionEvidence.DecidedAt.After(in.CreatedAt) {
			return false
		}
	}
	return in.Action == nil || !in.Action.Automatic ||
		(in.DecisionEvidence != nil && in.DecisionEvidence.Purpose == domain.ArenaDecisionPurposeCategory)
}

func (state ArenaDraftPersistenceState) IsValid() bool {
	switch state {
	case ArenaDraftPersistenceStateActive,
		ArenaDraftPersistenceStatePaused,
		ArenaDraftPersistenceStateRecoveryRequired,
		ArenaDraftPersistenceStateCompleted,
		ArenaDraftPersistenceStateSuperseded:
		return true
	default:
		return false
	}
}

func arenaDraftRevisionParams(
	draftID uuid.UUID,
	expected ArenaDraftRevisionExpectation,
	in ArenaDraftRevisionInput,
) (sqlc.AppendArenaDraftRevisionCASParams, error) {
	selected := categoryJSON(in.SelectedCategories)
	recoveryEvidence, err := nullableJSONObject(in.RecoveryEvidence)
	if err != nil {
		return sqlc.AppendArenaDraftRevisionCASParams{}, domain.ErrValidation
	}
	params := sqlc.AppendArenaDraftRevisionCASParams{
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
		purpose := string(evidence.Purpose)
		params.DecisionEvidenceID = uuid.NullUUID{UUID: evidence.ID, Valid: true}
		params.DecisionPurpose = &purpose
		params.DecisionAlgorithmVersion = &evidence.AlgorithmVersion
		params.DecisionInputs = mustJSON(evidence.NormalizedInputs)
		params.DecisionSeed = append([]byte(nil), evidence.Seed[:]...)
		params.DecisionResult = mustJSON(evidence.Result)
		params.DecisionReplayDigest = append([]byte(nil), evidence.ReplayDigest[:]...)
		params.DecisionOwnerID = uuid.NullUUID{UUID: evidence.OwnerID, Valid: true}
		params.DecidedAt = tstz(evidence.DecidedAt)
	}
	return params, nil
}

func arenaDraftRevisionRecord(row sqlc.ArenaDraftRevision) (*ArenaDraftRevisionRecord, error) {
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
	record := &ArenaDraftRevisionRecord{
		ID:                 row.ID,
		DraftID:            row.DraftID,
		SeriesID:           row.SeriesID,
		RosterID:           row.RosterID,
		Revision:           row.Revision,
		CommandID:          row.CommandID,
		ServiceEpoch:       row.ServiceEpoch,
		State:              ArenaDraftPersistenceState(row.State),
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
		value := domain.ArenaDraftActionType(*row.CurrentAction)
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
		evidence, evidenceErr := arenaDecisionEvidenceFromStorage(
			row.DecisionEvidenceID.UUID,
			domain.ArenaDecisionPurpose(*row.DecisionPurpose),
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

func arenaDraftActionRecord(row sqlc.ArenaDraftAction) ArenaDraftActionRecord {
	return ArenaDraftActionRecord{
		ID:                row.ID,
		DraftID:           row.DraftID,
		ResultRevisionID:  row.ResultRevisionID,
		CommandID:         row.CommandID,
		TurnNumber:        int(row.TurnNumber),
		ActorID:           row.ActorID,
		Action:            domain.ArenaDraftActionType(row.Action),
		Category:          domain.Category(row.Category),
		ScheduledDeadline: row.ScheduledDeadline.Time,
		OccurredAt:        row.OccurredAt.Time,
		Automatic:         row.Automatic,
		CreatedAt:         row.CreatedAt.Time,
	}
}

func arenaDecisionEvidenceFromStorage(
	id uuid.UUID,
	purpose domain.ArenaDecisionPurpose,
	algorithm string,
	inputsJSON []byte,
	seed []byte,
	resultJSON []byte,
	digest []byte,
	ownerID uuid.UUID,
	decidedAt time.Time,
) (domain.ArenaDecisionEvidence, error) {
	if len(seed) != domain.ArenaDecisionSeedSize || len(digest) != domain.ArenaDecisionSeedSize {
		return domain.ArenaDecisionEvidence{}, domain.ErrValidation
	}
	var inputs, result []string
	if err := json.Unmarshal(inputsJSON, &inputs); err != nil {
		return domain.ArenaDecisionEvidence{}, err
	}
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return domain.ArenaDecisionEvidence{}, err
	}
	evidence := domain.ArenaDecisionEvidence{
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
		return domain.ArenaDecisionEvidence{}, err
	}
	return evidence, nil
}

func categoryJSON(categories []domain.Category) []byte {
	values := make([]string, len(categories))
	for index, category := range categories {
		values[index] = string(category)
	}
	return mustJSON(values)
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
