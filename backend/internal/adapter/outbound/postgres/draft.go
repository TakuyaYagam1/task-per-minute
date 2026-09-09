package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type DraftPersistenceState string

const (
	DraftPersistenceStateActive           DraftPersistenceState = "active"
	DraftPersistenceStatePaused           DraftPersistenceState = "paused"
	DraftPersistenceStateRecoveryRequired DraftPersistenceState = "recovery_required"
	DraftPersistenceStateCompleted        DraftPersistenceState = "completed"
	DraftPersistenceStateSuperseded       DraftPersistenceState = "superseded"
)

var ErrDraftNotFound = errors.New("draft repository: draft not found")

type DraftPostgres struct {
	tx *TxManager
}

type DraftCreateInput struct {
	ID                  uuid.UUID
	SeriesID            uuid.UUID
	RosterID            uuid.UUID
	CategoryRevisionID  uuid.UUID
	CategoryRevision    int64
	SourcePoolRevision  uuid.UUID
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	Format              domain.SeriesFormat
	Pool                []domain.Category
	InitialRevisionID   uuid.UUID
	CommandID           uuid.UUID
	ServiceEpoch        uuid.UUID
	AbsoluteDeadline    time.Time
	DecisionEvidence    domain.DecisionEvidence
	CreatedAt           time.Time
}

type DraftRevisionExpectation struct {
	ID           uuid.UUID
	Revision     int64
	ServiceEpoch uuid.UUID
}

type DraftActionInput struct {
	ID                uuid.UUID
	TurnNumber        int
	ActorID           uuid.UUID
	Action            domain.DraftActionType
	Category          domain.Category
	ScheduledDeadline time.Time
	OccurredAt        time.Time
	Automatic         bool
}

type DraftRevisionInput struct {
	ID                 uuid.UUID
	CommandID          uuid.UUID
	ServiceEpoch       uuid.UUID
	State              DraftPersistenceState
	TurnNumber         int
	CurrentActorID     *uuid.UUID
	CurrentAction      *domain.DraftActionType
	AbsoluteDeadline   *time.Time
	PausedRemainingMS  *int
	RecoveryReason     *string
	RecoveryEvidence   map[string]any
	SelectedCategories []domain.Category
	DecisionEvidence   *domain.DecisionEvidence
	Action             *DraftActionInput
	CreatedAt          time.Time
}

type DraftIdentityRecord struct {
	ID                  uuid.UUID
	SeriesID            uuid.UUID
	RosterID            uuid.UUID
	CategoryRevisionID  uuid.UUID
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	Format              domain.SeriesFormat
	CreatedAt           time.Time
}

type DraftRevisionRecord struct {
	ID                 uuid.UUID
	DraftID            uuid.UUID
	SeriesID           uuid.UUID
	RosterID           uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	CommandID          uuid.UUID
	ServiceEpoch       uuid.UUID
	State              DraftPersistenceState
	TurnNumber         int
	CurrentActorID     *uuid.UUID
	CurrentAction      *domain.DraftActionType
	AbsoluteDeadline   *time.Time
	PausedRemainingMS  *int
	RecoveryReason     *string
	RecoveryEvidence   map[string]any
	SelectedCategories []domain.Category
	DecisionEvidence   *domain.DecisionEvidence
	CreatedAt          time.Time
}

type DraftActionRecord struct {
	ID                uuid.UUID
	DraftID           uuid.UUID
	ResultRevisionID  uuid.UUID
	CommandID         uuid.UUID
	TurnNumber        int
	ActorID           uuid.UUID
	Action            domain.DraftActionType
	Category          domain.Category
	ScheduledDeadline time.Time
	OccurredAt        time.Time
	Automatic         bool
	CreatedAt         time.Time
}

type DraftAggregate struct {
	Draft            DraftIdentityRecord
	CategoryRevision int64
	Pool             []domain.Category
	Revisions        []DraftRevisionRecord
	Actions          []DraftActionRecord
}

func NewDraftPostgres(tx *TxManager) *DraftPostgres {
	return &DraftPostgres{tx: tx}
}

func (r *DraftPostgres) Create(
	ctx context.Context,
	in DraftCreateInput,
) (*DraftAggregate, error) {
	if err := validateDraftCreateInput(in); err != nil {
		return nil, err
	}
	categoryPool, err := categoryJSON(in.Pool)
	if err != nil {
		return nil, fmt.Errorf("DraftPostgres - Create - category pool: %w", err)
	}
	decisionInputs, err := marshalJSON(
		"DraftPostgres - Create - decision inputs",
		in.DecisionEvidence.NormalizedInputs,
	)
	if err != nil {
		return nil, err
	}
	decisionResult, err := marshalJSON(
		"DraftPostgres - Create - decision result",
		in.DecisionEvidence.Result,
	)
	if err != nil {
		return nil, err
	}
	action := string(domain.DraftActionBan)
	err = r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.CreateDraftCategoryRevision(
			txCtx,
			sqlc.CreateDraftCategoryRevisionParams{
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
		if _, err := querier.CreateDraft(txCtx, sqlc.CreateDraftParams{
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
		_, err := querier.CreateInitialDraftRevision(
			txCtx,
			sqlc.CreateInitialDraftRevisionParams{
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
				DecisionInputs:           decisionInputs,
				DecisionSeed:             append([]byte(nil), in.DecisionEvidence.Seed[:]...),
				DecisionResult:           decisionResult,
				DecisionReplayDigest:     append([]byte(nil), in.DecisionEvidence.ReplayDigest[:]...),
				DecisionOwnerID:          uuid.NullUUID{UUID: in.DecisionEvidence.OwnerID, Valid: true},
				DecidedAt:                tstz(in.DecisionEvidence.DecidedAt),
				CreatedAt:                tstz(in.CreatedAt),
			},
		)
		return err
	})
	if err != nil {
		return nil, mapRepositoryWriteError("DraftPostgres - Create", err)
	}
	return r.Get(ctx, in.ID)
}

func (r *DraftPostgres) AppendRevision(
	ctx context.Context,
	draftID uuid.UUID,
	expected DraftRevisionExpectation,
	in DraftRevisionInput,
) (*DraftRevisionRecord, bool, error) {
	if err := validateDraftRevisionInput(draftID, expected, in); err != nil {
		return nil, false, err
	}
	var inserted sqlc.DraftRevision
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		params, err := draftRevisionParams(draftID, expected, in)
		if err != nil {
			return err
		}
		querier := r.tx.Querier(txCtx)
		inserted, err = querier.AppendDraftRevisionCAS(txCtx, params)
		if err != nil {
			return err
		}
		if in.Action == nil {
			return nil
		}
		_, err = querier.CreateDraftAction(txCtx, sqlc.CreateDraftActionParams{
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
		return nil, false, mapRepositoryWriteError("DraftPostgres - AppendRevision", err)
	}
	record, err := draftRevisionRecord(inserted)
	if err != nil {
		return nil, false, fmt.Errorf("DraftPostgres - AppendRevision - map row: %w", err)
	}
	return record, true, nil
}

func (r *DraftPostgres) Get(
	ctx context.Context,
	draftID uuid.UUID,
) (*DraftAggregate, error) {
	if draftID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	draft, err := querier.GetDraft(ctx, draftID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDraftNotFound
		}
		return nil, fmt.Errorf("DraftPostgres - Get: %w", err)
	}
	categoryRevision, err := querier.GetDraftCategoryRevision(ctx, draft.CategoryRevisionID)
	if err != nil {
		return nil, fmt.Errorf("DraftPostgres - Get - category revision: %w", err)
	}
	pool, err := decodeCategories(categoryRevision.CategoryPool)
	if err != nil {
		return nil, fmt.Errorf("DraftPostgres - Get - category pool: %w", err)
	}
	revisionRows, err := querier.ListDraftRevisions(ctx, draftID)
	if err != nil {
		return nil, fmt.Errorf("DraftPostgres - Get - revisions: %w", err)
	}
	revisions := make([]DraftRevisionRecord, 0, len(revisionRows))
	for _, row := range revisionRows {
		record, mapErr := draftRevisionRecord(row)
		if mapErr != nil {
			return nil, fmt.Errorf("DraftPostgres - Get - revision: %w", mapErr)
		}
		revisions = append(revisions, *record)
	}
	actionRows, err := querier.ListDraftActions(ctx, draftID)
	if err != nil {
		return nil, fmt.Errorf("DraftPostgres - Get - actions: %w", err)
	}
	actions := make([]DraftActionRecord, len(actionRows))
	for index, row := range actionRows {
		actions[index] = draftActionRecord(row)
	}
	return &DraftAggregate{
		Draft: DraftIdentityRecord{
			ID:                  draft.ID,
			SeriesID:            draft.SeriesID,
			RosterID:            draft.RosterID,
			CategoryRevisionID:  draft.CategoryRevisionID,
			FirstParticipantID:  draft.FirstParticipantID,
			SecondParticipantID: draft.SecondParticipantID,
			Format:              domain.SeriesFormat(draft.Format),
			CreatedAt:           draft.CreatedAt.Time,
		},
		CategoryRevision: categoryRevision.Revision,
		Pool:             pool,
		Revisions:        revisions,
		Actions:          actions,
	}, nil
}
