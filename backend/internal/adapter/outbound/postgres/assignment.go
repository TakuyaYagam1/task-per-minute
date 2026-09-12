package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrAssignmentPlanNotFound = errors.New("assignment repository: plan not found")
	ErrAssignmentNotFound     = errors.New("assignment repository: assignment not found")
	errAssignmentCAS          = errors.New("assignment compare-and-set failed")
)

type AssignmentPostgres struct {
	tx *TxManager
}

type ConservativePlanInput struct {
	ID                   uuid.UUID
	TournamentID         uuid.UUID
	RosterID             uuid.UUID
	RevisionID           uuid.UUID
	SourceRosterRevision int64
	SourcePoolRevisionID uuid.UUID
	ConstraintGraph      map[string]any
	ProofEvidence        map[string]any
	CreatedAt            time.Time
}

type ExactPlanInput struct {
	ID                   uuid.UUID
	TournamentID         uuid.UUID
	RosterID             uuid.UUID
	ParentPlanID         uuid.UUID
	RevisionID           uuid.UUID
	SourceRosterRevision int64
	SourcePoolRevisionID uuid.UUID
	SourceDraftRevision  uuid.UUID
	ConstraintGraph      map[string]any
	ProofEvidence        map[string]any
	DecisionEvidence     domain.DecisionEvidence
	Branches             []AssignmentBranchInput
	CreatedAt            time.Time
}

type AssignmentBranchInput struct {
	ID              uuid.UUID
	DraftID         uuid.UUID
	DraftRevisionID uuid.UUID
	Key             string
	Categories      []domain.Category
	Edges           []AssignmentEdgeInput
}

type AssignmentEdgeInput struct {
	ID                uuid.UUID
	ReservationID     uuid.UUID
	Position          int
	Snapshot          domain.AssignmentTaskSnapshot
	ContentDigest     [sha256.Size]byte
	SelectionEvidence map[string]any
}

type AssignmentPlanRecord struct {
	ID                    uuid.UUID
	TournamentID          uuid.UUID
	RosterID              uuid.UUID
	Kind                  string
	ParentPlanID          *uuid.UUID
	RevisionID            uuid.UUID
	SourceRosterRevision  int64
	SourcePoolRevisionID  uuid.UUID
	SourceDraftRevisionID *uuid.UUID
	ReachableBranchCount  int
	ConstraintGraph       map[string]any
	ProofEvidence         map[string]any
	DecisionEvidence      *domain.DecisionEvidence
	State                 string
	ActiveBranchID        *uuid.UUID
	CommittedAt           *time.Time
	SupersededAt          *time.Time
	SupersessionReason    *string
	CreatedAt             time.Time
}

type AssignmentBranchRecord struct {
	ID                 uuid.UUID
	PlanID             uuid.UUID
	DraftID            uuid.UUID
	DraftRevisionID    uuid.UUID
	Key                string
	Categories         []domain.Category
	State              string
	ActivatedAt        *time.Time
	ReleasedAt         *time.Time
	ReleaseReason      *string
	SupersededAt       *time.Time
	SupersessionReason *string
	CreatedAt          time.Time
}

type AssignmentEdgeRecord struct {
	ID                uuid.UUID
	PlanID            uuid.UUID
	BranchID          uuid.UUID
	Position          int
	TaskID            uuid.UUID
	TaskVersion       int
	SelectionEvidence map[string]any
	CreatedAt         time.Time
}

type TaskReservationRecord struct {
	ID                 uuid.UUID
	EdgeID             uuid.UUID
	PlanID             uuid.UUID
	BranchID           uuid.UUID
	TaskID             uuid.UUID
	TaskVersion        int
	State              string
	DisclosedAt        *time.Time
	CommittedAt        *time.Time
	ReleasedAt         *time.Time
	ReleaseReason      *string
	SupersededAt       *time.Time
	SupersessionReason *string
	CreatedAt          time.Time
}

type TaskSnapshotRecord struct {
	ReservationID uuid.UUID
	Snapshot      domain.AssignmentTaskSnapshot
	ContentDigest [sha256.Size]byte
	CreatedAt     time.Time
}

type AssignmentPlanAggregate struct {
	Plan         AssignmentPlanRecord
	Branches     []AssignmentBranchRecord
	Edges        []AssignmentEdgeRecord
	Reservations []TaskReservationRecord
	Snapshots    []TaskSnapshotRecord
}

type AssignmentCreateInput struct {
	ID            uuid.UUID
	AttemptID     uuid.UUID
	SeriesID      uuid.UUID
	RosterID      uuid.UUID
	PlanID        uuid.UUID
	BranchID      uuid.UUID
	ReservationID uuid.UUID
	SnapshotID    uuid.UUID
	CreatedAt     time.Time
}

type AssignmentRecord struct {
	ID                     uuid.UUID
	AttemptID              uuid.UUID
	SeriesID               uuid.UUID
	RosterID               uuid.UUID
	PlanID                 uuid.UUID
	BranchID               uuid.UUID
	ReservationID          uuid.UUID
	Snapshot               TaskSnapshotRecord
	SupersedesAssignmentID *uuid.UUID
	State                  string
	Revision               int64
	Receipts               []domain.TaskDeliveryReceipt
	CreatedAt              time.Time
	UpdatedAt              time.Time
	CompletedAt            *time.Time
	SupersededAt           *time.Time
	SupersessionReason     *string
}

type AssignmentSupersedeInput struct {
	ID            uuid.UUID
	ReservationID uuid.UUID
	SnapshotID    uuid.UUID
	Reason        string
	OccurredAt    time.Time
}

func NewAssignmentPostgres(tx *TxManager) *AssignmentPostgres {
	return &AssignmentPostgres{tx: tx}
}

func (r *AssignmentPostgres) CreateConservativePlan(
	ctx context.Context,
	in ConservativePlanInput,
) (*AssignmentPlanAggregate, error) {
	constraintGraph, proofEvidence, err := validateConservativePlanInput(in)
	if err != nil {
		return nil, err
	}
	_, err = r.tx.Querier(ctx).CreateConservativeAssignmentPlan(
		ctx,
		sqlc.CreateConservativeAssignmentPlanParams{
			ID:                   in.ID,
			TournamentID:         in.TournamentID,
			RosterID:             in.RosterID,
			RevisionID:           in.RevisionID,
			SourceRosterRevision: in.SourceRosterRevision,
			SourcePoolRevisionID: in.SourcePoolRevisionID,
			ConstraintGraph:      constraintGraph,
			ProofEvidence:        proofEvidence,
			CreatedAt:            tstz(in.CreatedAt),
		},
	)
	if err != nil {
		return nil, mapRepositoryWriteError("AssignmentPostgres - CreateConservativePlan", err)
	}
	return r.GetPlan(ctx, in.ID)
}

func (r *AssignmentPostgres) CreateExactPlan(
	ctx context.Context,
	in ExactPlanInput,
) (*AssignmentPlanAggregate, error) {
	constraintGraph, proofEvidence, err := validateExactPlanInput(in)
	if err != nil {
		return nil, err
	}
	decisionInputs, err := marshalJSON(
		"AssignmentPostgres - CreateExactPlan - decision inputs",
		in.DecisionEvidence.NormalizedInputs,
	)
	if err != nil {
		return nil, err
	}
	decisionResult, err := marshalJSON(
		"AssignmentPostgres - CreateExactPlan - decision result",
		in.DecisionEvidence.Result,
	)
	if err != nil {
		return nil, err
	}
	err = r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		evidence := in.DecisionEvidence
		if _, err := querier.CreateExactAssignmentPlan(
			txCtx,
			sqlc.CreateExactAssignmentPlanParams{
				ID:                       in.ID,
				TournamentID:             in.TournamentID,
				RosterID:                 in.RosterID,
				ParentPlanID:             uuid.NullUUID{UUID: in.ParentPlanID, Valid: true},
				RevisionID:               in.RevisionID,
				SourceRosterRevision:     in.SourceRosterRevision,
				SourcePoolRevisionID:     in.SourcePoolRevisionID,
				SourceDraftRevisionID:    uuid.NullUUID{UUID: in.SourceDraftRevision, Valid: true},
				ReachableBranchCount:     int32(len(in.Branches)), //nolint:gosec // validation bounds branch count to PostgreSQL int4.
				ConstraintGraph:          constraintGraph,
				ProofEvidence:            proofEvidence,
				DecisionEvidenceID:       uuid.NullUUID{UUID: evidence.ID, Valid: true},
				DecisionAlgorithmVersion: &evidence.AlgorithmVersion,
				DecisionInputs:           decisionInputs,
				DecisionSeed:             append([]byte(nil), evidence.Seed[:]...),
				DecisionResult:           decisionResult,
				DecisionReplayDigest:     append([]byte(nil), evidence.ReplayDigest[:]...),
				DecisionOwnerID:          uuid.NullUUID{UUID: evidence.OwnerID, Valid: true},
				DecidedAt:                tstz(evidence.DecidedAt),
				CreatedAt:                tstz(in.CreatedAt),
			},
		); err != nil {
			return fmt.Errorf("create exact assignment plan: %w", err)
		}
		for _, branch := range in.Branches {
			categories, jsonErr := categoryJSON(branch.Categories)
			if jsonErr != nil {
				return fmt.Errorf("AssignmentPostgres - CreateExactPlan - branch categories: %w", jsonErr)
			}
			if _, err := querier.CreateAssignmentBranch(
				txCtx,
				sqlc.CreateAssignmentBranchParams{
					ID:               branch.ID,
					PlanID:           in.ID,
					DraftID:          uuid.NullUUID{UUID: branch.DraftID, Valid: branch.DraftID != uuid.Nil},
					DraftRevisionID:  uuid.NullUUID{UUID: branch.DraftRevisionID, Valid: branch.DraftRevisionID != uuid.Nil},
					BranchKey:        branch.Key,
					CategorySequence: categories,
					CreatedAt:        tstz(in.CreatedAt),
				},
			); err != nil {
				return fmt.Errorf("create assignment branch: %w", err)
			}
			for _, edge := range branch.Edges {
				selectionEvidence, jsonErr := requiredJSONObject(edge.SelectionEvidence)
				if jsonErr != nil {
					return jsonErr
				}
				if _, err := querier.CreateAssignmentPlanEdge(
					txCtx,
					sqlc.CreateAssignmentPlanEdgeParams{
						ID:                edge.ID,
						PlanID:            in.ID,
						BranchID:          branch.ID,
						Position:          int16(edge.Position), //nolint:gosec // validation bounds positions to 1..3.
						TaskID:            edge.Snapshot.TaskID,
						TaskVersion:       int32(edge.Snapshot.Version), //nolint:gosec // validation bounds versions to PostgreSQL int4.
						SelectionEvidence: selectionEvidence,
						CreatedAt:         tstz(in.CreatedAt),
					},
				); err != nil {
					return fmt.Errorf(
						"create assignment plan edge %s position %d for task %s: %w",
						branch.Key,
						edge.Position,
						edge.Snapshot.TaskID,
						err,
					)
				}
				if _, err := querier.CreateAssignmentTaskVersionReservation(
					txCtx,
					sqlc.CreateAssignmentTaskVersionReservationParams{
						ID:          edge.ReservationID,
						EdgeID:      edge.ID,
						PlanID:      in.ID,
						BranchID:    branch.ID,
						TaskID:      edge.Snapshot.TaskID,
						TaskVersion: int32(edge.Snapshot.Version), //nolint:gosec // validation bounds versions to PostgreSQL int4.
						CreatedAt:   tstz(in.CreatedAt),
					},
				); err != nil {
					return fmt.Errorf("create assignment task-version reservation: %w", err)
				}
				snapshotParams, jsonErr := taskSnapshotParams(edge, in.CreatedAt)
				if jsonErr != nil {
					return jsonErr
				}
				if _, err := querier.CreateAssignmentTaskSnapshot(
					txCtx,
					snapshotParams,
				); err != nil {
					return fmt.Errorf("create assignment task snapshot: %w", err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, mapRepositoryWriteError("AssignmentPostgres - CreateExactPlan", err)
	}
	return r.GetPlan(ctx, in.ID)
}

func (r *AssignmentPostgres) CommitBranch(
	ctx context.Context,
	planID uuid.UUID,
	expectedRevisionID uuid.UUID,
	expectedRosterRevision int64,
	expectedDraftRevisionID uuid.UUID,
	branchID uuid.UUID,
	committedAt time.Time,
	releaseReason string,
) (*AssignmentPlanAggregate, bool, error) {
	releaseReason = strings.TrimSpace(releaseReason)
	if !validCommitBranchCommand(
		planID, expectedRevisionID, expectedRosterRevision, expectedDraftRevisionID, branchID, committedAt, releaseReason,
	) {
		return nil, false, domain.ErrValidation
	}
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		return r.commitBranchTx(
			txCtx, planID, expectedRevisionID, expectedRosterRevision,
			expectedDraftRevisionID, branchID, committedAt, releaseReason,
		)
	})
	if err != nil {
		if errors.Is(err, errAssignmentCAS) {
			return nil, false, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrAssignmentPlanNotFound
		}
		return nil, false, mapRepositoryWriteError("AssignmentPostgres - CommitBranch", err)
	}
	aggregate, err := r.GetPlan(ctx, planID)
	return aggregate, true, err
}

func validCommitBranchCommand(
	planID uuid.UUID,
	expectedRevisionID uuid.UUID,
	expectedRosterRevision int64,
	expectedDraftRevisionID uuid.UUID,
	branchID uuid.UUID,
	committedAt time.Time,
	releaseReason string,
) bool {
	return planID != uuid.Nil && expectedRevisionID != uuid.Nil && expectedRosterRevision >= 1 &&
		expectedDraftRevisionID != uuid.Nil && branchID != uuid.Nil && validServerTime(committedAt) &&
		releaseReason != ""
}

func (r *AssignmentPostgres) commitBranchTx(
	ctx context.Context,
	planID uuid.UUID,
	expectedRevisionID uuid.UUID,
	expectedRosterRevision int64,
	expectedDraftRevisionID uuid.UUID,
	branchID uuid.UUID,
	committedAt time.Time,
	releaseReason string,
) error {
	querier := r.tx.Querier(ctx)
	plan, err := querier.LockAssignmentPlan(ctx, planID)
	if err != nil {
		return err
	}
	if !matchesPlanCommit(
		plan, expectedRevisionID, expectedRosterRevision, expectedDraftRevisionID, committedAt,
	) {
		return errAssignmentCAS
	}
	committed, err := querier.CommitAssignmentBranchReservations(ctx, sqlc.CommitAssignmentBranchReservationsParams{
		CommittedAt: tstz(committedAt), PlanID: planID, BranchID: branchID,
	})
	if err != nil {
		return err
	}
	if len(committed) != domain.AssignmentReserveCount+1 {
		return errAssignmentCAS
	}
	if _, err = querier.ReleaseOtherAssignmentBranchReservations(
		ctx,
		sqlc.ReleaseOtherAssignmentBranchReservationsParams{
			ReleasedAt: tstz(committedAt), ReleaseReason: &releaseReason,
			PlanID: planID, ActiveBranchID: branchID,
		},
	); err != nil {
		return err
	}
	if err = activateAssignmentBranch(ctx, querier, planID, branchID, committedAt); err != nil {
		return err
	}
	if _, err = querier.ReleaseOtherAssignmentBranches(
		ctx,
		sqlc.ReleaseOtherAssignmentBranchesParams{
			ReleasedAt: tstz(committedAt), ReleaseReason: &releaseReason,
			PlanID: planID, ActiveBranchID: branchID,
		},
	); err != nil {
		return err
	}
	_, err = querier.CommitAssignmentPlanCAS(ctx, sqlc.CommitAssignmentPlanCASParams{
		ActiveBranchID: uuid.NullUUID{UUID: branchID, Valid: true}, CommittedAt: tstz(committedAt),
		ID: planID, ExpectedRevisionID: expectedRevisionID, ExpectedRosterRevision: expectedRosterRevision,
		ExpectedDraftRevisionID: uuid.NullUUID{UUID: expectedDraftRevisionID, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errAssignmentCAS
	}
	return err
}

func matchesPlanCommit(
	plan sqlc.LockAssignmentPlanRow,
	expectedRevisionID uuid.UUID,
	expectedRosterRevision int64,
	expectedDraftRevisionID uuid.UUID,
	committedAt time.Time,
) bool {
	return plan.State == "planned" && plan.Kind == "exact" && plan.RevisionID == expectedRevisionID &&
		plan.SourceRosterRevision == expectedRosterRevision && plan.SourceDraftRevisionID.Valid &&
		plan.SourceDraftRevisionID.UUID == expectedDraftRevisionID && !committedAt.Before(plan.CreatedAt.Time)
}

func activateAssignmentBranch(
	ctx context.Context,
	querier *sqlc.Queries,
	planID uuid.UUID,
	branchID uuid.UUID,
	activatedAt time.Time,
) error {
	_, err := querier.ActivateAssignmentBranch(ctx, sqlc.ActivateAssignmentBranchParams{
		ActivatedAt: tstz(activatedAt), ID: branchID, PlanID: planID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errAssignmentCAS
	}
	return err
}
