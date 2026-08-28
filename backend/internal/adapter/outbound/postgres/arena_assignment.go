package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrArenaAssignmentPlanNotFound = errors.New("arena assignment repository: plan not found")
	ErrArenaAssignmentNotFound     = errors.New("arena assignment repository: assignment not found")
	errArenaAssignmentCAS          = errors.New("arena assignment compare-and-set failed")
)

type ArenaAssignmentPostgres struct {
	tx *TxManager
}

type ArenaConservativePlanInput struct {
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

type ArenaExactPlanInput struct {
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
	DecisionEvidence     domain.ArenaDecisionEvidence
	Branches             []ArenaAssignmentBranchInput
	CreatedAt            time.Time
}

type ArenaAssignmentBranchInput struct {
	ID              uuid.UUID
	DraftID         uuid.UUID
	DraftRevisionID uuid.UUID
	Key             string
	Categories      []domain.Category
	Edges           []ArenaAssignmentEdgeInput
}

type ArenaAssignmentEdgeInput struct {
	ID                uuid.UUID
	ReservationID     uuid.UUID
	Position          int
	Snapshot          domain.ArenaTaskSnapshot
	ContentDigest     [sha256.Size]byte
	SelectionEvidence map[string]any
}

type ArenaAssignmentPlanRecord struct {
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
	DecisionEvidence      *domain.ArenaDecisionEvidence
	State                 string
	ActiveBranchID        *uuid.UUID
	CommittedAt           *time.Time
	SupersededAt          *time.Time
	SupersessionReason    *string
	CreatedAt             time.Time
}

type ArenaAssignmentBranchRecord struct {
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

type ArenaAssignmentEdgeRecord struct {
	ID                uuid.UUID
	PlanID            uuid.UUID
	BranchID          uuid.UUID
	Position          int
	TaskID            uuid.UUID
	TaskVersion       int
	SelectionEvidence map[string]any
	CreatedAt         time.Time
}

type ArenaTaskReservationRecord struct {
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

type ArenaTaskSnapshotRecord struct {
	ReservationID uuid.UUID
	Snapshot      domain.ArenaTaskSnapshot
	ContentDigest [sha256.Size]byte
	CreatedAt     time.Time
}

type ArenaAssignmentPlanAggregate struct {
	Plan         ArenaAssignmentPlanRecord
	Branches     []ArenaAssignmentBranchRecord
	Edges        []ArenaAssignmentEdgeRecord
	Reservations []ArenaTaskReservationRecord
	Snapshots    []ArenaTaskSnapshotRecord
}

type ArenaAssignmentCreateInput struct {
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

type ArenaAssignmentRecord struct {
	ID                     uuid.UUID
	AttemptID              uuid.UUID
	SeriesID               uuid.UUID
	RosterID               uuid.UUID
	PlanID                 uuid.UUID
	BranchID               uuid.UUID
	ReservationID          uuid.UUID
	Snapshot               ArenaTaskSnapshotRecord
	SupersedesAssignmentID *uuid.UUID
	State                  string
	Revision               int64
	Receipts               []domain.ArenaDeliveryReceipt
	CreatedAt              time.Time
	UpdatedAt              time.Time
	CompletedAt            *time.Time
	SupersededAt           *time.Time
	SupersessionReason     *string
}

type ArenaAssignmentSupersedeInput struct {
	ID            uuid.UUID
	ReservationID uuid.UUID
	SnapshotID    uuid.UUID
	Reason        string
	OccurredAt    time.Time
}

func NewArenaAssignmentPostgres(tx *TxManager) *ArenaAssignmentPostgres {
	return &ArenaAssignmentPostgres{tx: tx}
}

func (r *ArenaAssignmentPostgres) CreateConservativePlan(
	ctx context.Context,
	in ArenaConservativePlanInput,
) (*ArenaAssignmentPlanAggregate, error) {
	constraintGraph, proofEvidence, err := validateConservativePlanInput(in)
	if err != nil {
		return nil, err
	}
	_, err = r.tx.Querier(ctx).CreateConservativeArenaAssignmentPlan(
		ctx,
		sqlc.CreateConservativeArenaAssignmentPlanParams{
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
		return nil, mapArenaRepositoryWriteError("ArenaAssignmentPostgres - CreateConservativePlan", err)
	}
	return r.GetPlan(ctx, in.ID)
}

func (r *ArenaAssignmentPostgres) CreateExactPlan(
	ctx context.Context,
	in ArenaExactPlanInput,
) (*ArenaAssignmentPlanAggregate, error) {
	constraintGraph, proofEvidence, err := validateExactPlanInput(in)
	if err != nil {
		return nil, err
	}
	err = r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		evidence := in.DecisionEvidence
		if _, err := querier.CreateExactArenaAssignmentPlan(
			txCtx,
			sqlc.CreateExactArenaAssignmentPlanParams{
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
				DecisionInputs:           mustJSON(evidence.NormalizedInputs),
				DecisionSeed:             append([]byte(nil), evidence.Seed[:]...),
				DecisionResult:           mustJSON(evidence.Result),
				DecisionReplayDigest:     append([]byte(nil), evidence.ReplayDigest[:]...),
				DecisionOwnerID:          uuid.NullUUID{UUID: evidence.OwnerID, Valid: true},
				DecidedAt:                tstz(evidence.DecidedAt),
				CreatedAt:                tstz(in.CreatedAt),
			},
		); err != nil {
			return err
		}
		for _, branch := range in.Branches {
			if _, err := querier.CreateArenaAssignmentBranch(
				txCtx,
				sqlc.CreateArenaAssignmentBranchParams{
					ID:               branch.ID,
					PlanID:           in.ID,
					DraftID:          branch.DraftID,
					DraftRevisionID:  branch.DraftRevisionID,
					BranchKey:        branch.Key,
					CategorySequence: categoryJSON(branch.Categories),
					CreatedAt:        tstz(in.CreatedAt),
				},
			); err != nil {
				return err
			}
			for _, edge := range branch.Edges {
				selectionEvidence, jsonErr := requiredJSONObject(edge.SelectionEvidence)
				if jsonErr != nil {
					return jsonErr
				}
				if _, err := querier.CreateArenaAssignmentPlanEdge(
					txCtx,
					sqlc.CreateArenaAssignmentPlanEdgeParams{
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
					return err
				}
				if _, err := querier.CreateArenaTaskVersionReservation(
					txCtx,
					sqlc.CreateArenaTaskVersionReservationParams{
						ID:          edge.ReservationID,
						EdgeID:      edge.ID,
						PlanID:      in.ID,
						BranchID:    branch.ID,
						TaskID:      edge.Snapshot.TaskID,
						TaskVersion: int32(edge.Snapshot.Version), //nolint:gosec // validation bounds versions to PostgreSQL int4.
						CreatedAt:   tstz(in.CreatedAt),
					},
				); err != nil {
					return err
				}
				if _, err := querier.CreateArenaTaskSnapshot(
					txCtx,
					arenaTaskSnapshotParams(edge, in.CreatedAt),
				); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, mapArenaRepositoryWriteError("ArenaAssignmentPostgres - CreateExactPlan", err)
	}
	return r.GetPlan(ctx, in.ID)
}

func (r *ArenaAssignmentPostgres) CommitBranch(
	ctx context.Context,
	planID uuid.UUID,
	expectedRevisionID uuid.UUID,
	expectedRosterRevision int64,
	expectedDraftRevisionID uuid.UUID,
	branchID uuid.UUID,
	committedAt time.Time,
	releaseReason string,
) (*ArenaAssignmentPlanAggregate, bool, error) {
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
		if errors.Is(err, errArenaAssignmentCAS) {
			return nil, false, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrArenaAssignmentPlanNotFound
		}
		return nil, false, mapArenaRepositoryWriteError("ArenaAssignmentPostgres - CommitBranch", err)
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

func (r *ArenaAssignmentPostgres) commitBranchTx(
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
	plan, err := querier.LockArenaAssignmentPlan(ctx, planID)
	if err != nil {
		return err
	}
	if !matchesArenaPlanCommit(
		plan, expectedRevisionID, expectedRosterRevision, expectedDraftRevisionID, committedAt,
	) {
		return errArenaAssignmentCAS
	}
	committed, err := querier.CommitArenaBranchReservations(ctx, sqlc.CommitArenaBranchReservationsParams{
		CommittedAt: tstz(committedAt), PlanID: planID, BranchID: branchID,
	})
	if err != nil {
		return err
	}
	if len(committed) != domain.ArenaAssignmentReserveCount+1 {
		return errArenaAssignmentCAS
	}
	if _, err = querier.ReleaseOtherArenaBranchReservations(
		ctx,
		sqlc.ReleaseOtherArenaBranchReservationsParams{
			ReleasedAt: tstz(committedAt), ReleaseReason: &releaseReason,
			PlanID: planID, ActiveBranchID: branchID,
		},
	); err != nil {
		return err
	}
	if err = activateArenaAssignmentBranch(ctx, querier, planID, branchID, committedAt); err != nil {
		return err
	}
	if _, err = querier.ReleaseOtherArenaAssignmentBranches(
		ctx,
		sqlc.ReleaseOtherArenaAssignmentBranchesParams{
			ReleasedAt: tstz(committedAt), ReleaseReason: &releaseReason,
			PlanID: planID, ActiveBranchID: branchID,
		},
	); err != nil {
		return err
	}
	_, err = querier.CommitArenaAssignmentPlanCAS(ctx, sqlc.CommitArenaAssignmentPlanCASParams{
		ActiveBranchID: uuid.NullUUID{UUID: branchID, Valid: true}, CommittedAt: tstz(committedAt),
		ID: planID, ExpectedRevisionID: expectedRevisionID, ExpectedRosterRevision: expectedRosterRevision,
		ExpectedDraftRevisionID: uuid.NullUUID{UUID: expectedDraftRevisionID, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errArenaAssignmentCAS
	}
	return err
}

func matchesArenaPlanCommit(
	plan sqlc.ArenaAssignmentPlan,
	expectedRevisionID uuid.UUID,
	expectedRosterRevision int64,
	expectedDraftRevisionID uuid.UUID,
	committedAt time.Time,
) bool {
	return plan.State == "planned" && plan.Kind == "exact" && plan.RevisionID == expectedRevisionID &&
		plan.SourceRosterRevision == expectedRosterRevision && plan.SourceDraftRevisionID.Valid &&
		plan.SourceDraftRevisionID.UUID == expectedDraftRevisionID && !committedAt.Before(plan.CreatedAt.Time)
}

func activateArenaAssignmentBranch(
	ctx context.Context,
	querier *sqlc.Queries,
	planID uuid.UUID,
	branchID uuid.UUID,
	activatedAt time.Time,
) error {
	_, err := querier.ActivateArenaAssignmentBranch(ctx, sqlc.ActivateArenaAssignmentBranchParams{
		ActivatedAt: tstz(activatedAt), ID: branchID, PlanID: planID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errArenaAssignmentCAS
	}
	return err
}

func (r *ArenaAssignmentPostgres) GetPlan(
	ctx context.Context,
	planID uuid.UUID,
) (*ArenaAssignmentPlanAggregate, error) {
	if planID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	plan, err := querier.GetArenaAssignmentPlan(ctx, planID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArenaAssignmentPlanNotFound
		}
		return nil, fmt.Errorf("ArenaAssignmentPostgres - GetPlan: %w", err)
	}
	branches, err := querier.ListArenaAssignmentBranches(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("ArenaAssignmentPostgres - GetPlan - branches: %w", err)
	}
	edges, err := querier.ListArenaAssignmentPlanEdges(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("ArenaAssignmentPostgres - GetPlan - edges: %w", err)
	}
	reservations, err := querier.ListArenaTaskVersionReservations(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("ArenaAssignmentPostgres - GetPlan - reservations: %w", err)
	}
	snapshots, err := querier.ListArenaTaskSnapshotsForPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("ArenaAssignmentPostgres - GetPlan - snapshots: %w", err)
	}
	return arenaAssignmentPlanAggregate(plan, branches, edges, reservations, snapshots)
}

func (r *ArenaAssignmentPostgres) CreateAssignment(
	ctx context.Context,
	in ArenaAssignmentCreateInput,
) (*ArenaAssignmentRecord, error) {
	if err := validateAssignmentCreateInput(in); err != nil {
		return nil, err
	}
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		return r.createAssignmentTx(txCtx, in)
	})
	if err != nil {
		return nil, mapArenaRepositoryWriteError("ArenaAssignmentPostgres - CreateAssignment", err)
	}
	return r.GetAssignment(ctx, in.ID)
}

func (r *ArenaAssignmentPostgres) createAssignmentTx(
	ctx context.Context,
	in ArenaAssignmentCreateInput,
) error {
	querier := r.tx.Querier(ctx)
	plan, err := querier.LockArenaAssignmentPlan(ctx, in.PlanID)
	if err != nil {
		return err
	}
	if !matchesAssignmentPlan(plan, in) {
		return domain.ErrConflict
	}
	reservations, err := querier.ListArenaTaskVersionReservations(ctx, in.PlanID)
	if err != nil {
		return err
	}
	reservation, ok := findArenaReservation(reservations, in.ReservationID)
	if !ok || !reservationAvailableForAssignment(reservation, in.BranchID, uuid.Nil) {
		return domain.ErrConflict
	}
	edges, err := querier.ListArenaAssignmentPlanEdges(ctx, in.PlanID)
	if err != nil {
		return err
	}
	if position, found := arenaReservationPosition(edges, reservation); !found || position != 1 {
		return domain.ErrConflict
	}
	snapshot, err := querier.GetArenaTaskSnapshot(ctx, in.SnapshotID)
	if err != nil {
		return err
	}
	if !snapshotMatchesReservation(snapshot, reservation) {
		return domain.ErrConflict
	}
	_, err = querier.CreateArenaAssignment(ctx, sqlc.CreateArenaAssignmentParams{
		ID: in.ID, AttemptID: in.AttemptID, SeriesID: in.SeriesID, RosterID: in.RosterID,
		PlanID: in.PlanID, BranchID: in.BranchID, ReservationID: in.ReservationID,
		SnapshotID: in.SnapshotID, TaskID: snapshot.TaskID, TaskVersion: snapshot.TaskVersion,
		CreatedAt: tstz(in.CreatedAt),
	})
	return err
}

func matchesAssignmentPlan(plan sqlc.ArenaAssignmentPlan, in ArenaAssignmentCreateInput) bool {
	return plan.State == "committed" && plan.ActiveBranchID.Valid &&
		plan.ActiveBranchID.UUID == in.BranchID && plan.RosterID == in.RosterID
}

func reservationAvailableForAssignment(
	reservation sqlc.ArenaTaskVersionReservation,
	branchID uuid.UUID,
	excludedReservationID uuid.UUID,
) bool {
	return reservation.BranchID == branchID && reservation.State == "committed" &&
		!reservation.DisclosedAt.Valid && reservation.ID != excludedReservationID
}

func snapshotMatchesReservation(
	snapshot sqlc.ArenaTaskSnapshot,
	reservation sqlc.ArenaTaskVersionReservation,
) bool {
	return snapshot.ReservationID == reservation.ID && snapshot.TaskID == reservation.TaskID &&
		snapshot.TaskVersion == reservation.TaskVersion
}

func (r *ArenaAssignmentPostgres) Deliver(
	ctx context.Context,
	assignmentID uuid.UUID,
	receiptID uuid.UUID,
	participantID uuid.UUID,
	deliveredAt time.Time,
) (domain.ArenaDeliveryReceipt, bool, error) {
	if assignmentID == uuid.Nil || receiptID == uuid.Nil || participantID == uuid.Nil ||
		!validServerTime(deliveredAt) {
		return domain.ArenaDeliveryReceipt{}, false, domain.ErrValidation
	}
	var receipt sqlc.ArenaTaskDeliveryReceipt
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		receipt, changed, err = r.deliverTx(txCtx, assignmentID, receiptID, participantID, deliveredAt)
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ArenaDeliveryReceipt{}, false, ErrArenaAssignmentNotFound
		}
		return domain.ArenaDeliveryReceipt{}, false,
			mapArenaRepositoryWriteError("ArenaAssignmentPostgres - Deliver", err)
	}
	return arenaDeliveryReceipt(receipt), changed, nil
}

func (r *ArenaAssignmentPostgres) deliverTx(
	ctx context.Context,
	assignmentID uuid.UUID,
	receiptID uuid.UUID,
	participantID uuid.UUID,
	deliveredAt time.Time,
) (sqlc.ArenaTaskDeliveryReceipt, bool, error) {
	querier := r.tx.Querier(ctx)
	assignment, err := querier.LockArenaAssignment(ctx, assignmentID)
	if err != nil {
		return sqlc.ArenaTaskDeliveryReceipt{}, false, err
	}
	if assignment.State != "active" || deliveredAt.Before(assignment.CreatedAt.Time) {
		return sqlc.ArenaTaskDeliveryReceipt{}, false, domain.ErrConflict
	}
	receipts, err := querier.ListArenaTaskDeliveryReceipts(ctx, assignmentID)
	if err != nil {
		return sqlc.ArenaTaskDeliveryReceipt{}, false, err
	}
	if existing, ok := findParticipantReceipt(receipts, participantID); ok {
		return existing, false, nil
	}
	if len(receipts) == 0 {
		if err = discloseArenaReservation(ctx, querier, assignment.ReservationID, deliveredAt); err != nil {
			return sqlc.ArenaTaskDeliveryReceipt{}, false, err
		}
	}
	receipt, err := querier.CreateArenaTaskDeliveryReceipt(ctx, sqlc.CreateArenaTaskDeliveryReceiptParams{
		ID: receiptID, AssignmentID: assignment.ID, AttemptID: assignment.AttemptID,
		RosterID: assignment.RosterID, ParticipantID: participantID, SnapshotID: assignment.SnapshotID,
		TaskID: assignment.TaskID, TaskVersion: assignment.TaskVersion, DeliveredAt: tstz(deliveredAt),
	})
	return receipt, err == nil, err
}

func findParticipantReceipt(
	receipts []sqlc.ArenaTaskDeliveryReceipt,
	participantID uuid.UUID,
) (sqlc.ArenaTaskDeliveryReceipt, bool) {
	for _, receipt := range receipts {
		if receipt.ParticipantID == participantID {
			return receipt, true
		}
	}
	return sqlc.ArenaTaskDeliveryReceipt{}, false
}

func discloseArenaReservation(
	ctx context.Context,
	querier *sqlc.Queries,
	reservationID uuid.UUID,
	disclosedAt time.Time,
) error {
	_, err := querier.DiscloseArenaTaskReservationCAS(ctx, sqlc.DiscloseArenaTaskReservationCASParams{
		DisclosedAt: tstz(disclosedAt), ID: reservationID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return err
}

func (r *ArenaAssignmentPostgres) Supersede(
	ctx context.Context,
	assignmentID uuid.UUID,
	expectedRevision int64,
	in ArenaAssignmentSupersedeInput,
) (*ArenaAssignmentRecord, bool, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if !validSupersedeCommand(assignmentID, expectedRevision, in) {
		return nil, false, domain.ErrValidation
	}
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		return r.supersedeTx(txCtx, assignmentID, expectedRevision, in)
	})
	if err != nil {
		if errors.Is(err, errArenaAssignmentCAS) {
			return nil, false, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrArenaAssignmentNotFound
		}
		return nil, false, mapArenaRepositoryWriteError("ArenaAssignmentPostgres - Supersede", err)
	}
	record, err := r.GetAssignment(ctx, in.ID)
	return record, true, err
}

func validSupersedeCommand(
	assignmentID uuid.UUID,
	expectedRevision int64,
	in ArenaAssignmentSupersedeInput,
) bool {
	return assignmentID != uuid.Nil && expectedRevision >= 1 && in.ID != uuid.Nil &&
		in.ReservationID != uuid.Nil && in.SnapshotID != uuid.Nil && in.Reason != "" &&
		validServerTime(in.OccurredAt)
}

func (r *ArenaAssignmentPostgres) supersedeTx(
	ctx context.Context,
	assignmentID uuid.UUID,
	expectedRevision int64,
	in ArenaAssignmentSupersedeInput,
) error {
	querier := r.tx.Querier(ctx)
	old, err := querier.LockArenaAssignment(ctx, assignmentID)
	if err != nil {
		return err
	}
	if old.State != "active" || old.Revision != expectedRevision || in.OccurredAt.Before(old.CreatedAt.Time) {
		return errArenaAssignmentCAS
	}
	reservation, snapshot, err := loadReplacementEvidence(ctx, querier, old, in)
	if err != nil {
		return err
	}
	if _, err = querier.SupersedeArenaAssignmentCAS(ctx, sqlc.SupersedeArenaAssignmentCASParams{
		SupersededAt: tstz(in.OccurredAt), SupersessionReason: &in.Reason,
		ID: assignmentID, ExpectedRevision: expectedRevision,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errArenaAssignmentCAS
		}
		return err
	}
	_, err = querier.CreateArenaAssignment(ctx, sqlc.CreateArenaAssignmentParams{
		ID: in.ID, AttemptID: old.AttemptID, SeriesID: old.SeriesID, RosterID: old.RosterID,
		PlanID: old.PlanID, BranchID: old.BranchID, ReservationID: reservation.ID,
		SnapshotID: snapshot.ID, TaskID: snapshot.TaskID, TaskVersion: snapshot.TaskVersion,
		SupersedesAssignmentID: uuid.NullUUID{UUID: old.ID, Valid: true}, CreatedAt: tstz(in.OccurredAt),
	})
	return err
}

func loadReplacementEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	old sqlc.ArenaAssignment,
	in ArenaAssignmentSupersedeInput,
) (sqlc.ArenaTaskVersionReservation, sqlc.ArenaTaskSnapshot, error) {
	reservations, err := querier.ListArenaTaskVersionReservations(ctx, old.PlanID)
	if err != nil {
		return sqlc.ArenaTaskVersionReservation{}, sqlc.ArenaTaskSnapshot{}, err
	}
	reservation, ok := findArenaReservation(reservations, in.ReservationID)
	if !ok || !reservationAvailableForAssignment(reservation, old.BranchID, old.ReservationID) {
		return sqlc.ArenaTaskVersionReservation{}, sqlc.ArenaTaskSnapshot{}, domain.ErrConflict
	}
	current, ok := findArenaReservation(reservations, old.ReservationID)
	if !ok {
		return sqlc.ArenaTaskVersionReservation{}, sqlc.ArenaTaskSnapshot{}, domain.ErrConflict
	}
	edges, err := querier.ListArenaAssignmentPlanEdges(ctx, old.PlanID)
	if err != nil {
		return sqlc.ArenaTaskVersionReservation{}, sqlc.ArenaTaskSnapshot{}, err
	}
	if !arenaReservationsAreSequential(edges, current, reservation) {
		return sqlc.ArenaTaskVersionReservation{}, sqlc.ArenaTaskSnapshot{}, domain.ErrConflict
	}
	snapshot, err := querier.GetArenaTaskSnapshot(ctx, in.SnapshotID)
	if err != nil {
		return sqlc.ArenaTaskVersionReservation{}, sqlc.ArenaTaskSnapshot{}, err
	}
	if !snapshotMatchesReservation(snapshot, reservation) {
		return sqlc.ArenaTaskVersionReservation{}, sqlc.ArenaTaskSnapshot{}, domain.ErrConflict
	}
	return reservation, snapshot, nil
}

func (r *ArenaAssignmentPostgres) GetAssignment(
	ctx context.Context,
	assignmentID uuid.UUID,
) (*ArenaAssignmentRecord, error) {
	if assignmentID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	assignment, err := querier.GetArenaAssignment(ctx, assignmentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArenaAssignmentNotFound
		}
		return nil, fmt.Errorf("ArenaAssignmentPostgres - GetAssignment: %w", err)
	}
	snapshot, err := querier.GetArenaTaskSnapshot(ctx, assignment.SnapshotID)
	if err != nil {
		return nil, fmt.Errorf("ArenaAssignmentPostgres - GetAssignment - snapshot: %w", err)
	}
	receipts, err := querier.ListArenaTaskDeliveryReceipts(ctx, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("ArenaAssignmentPostgres - GetAssignment - receipts: %w", err)
	}
	return arenaAssignmentRecord(assignment, snapshot, receipts)
}

func validateConservativePlanInput(in ArenaConservativePlanInput) ([]byte, []byte, error) {
	if in.ID == uuid.Nil || in.TournamentID == uuid.Nil || in.RosterID == uuid.Nil || in.RevisionID == uuid.Nil ||
		in.SourceRosterRevision < 1 || in.SourcePoolRevisionID == uuid.Nil || !validServerTime(in.CreatedAt) {
		return nil, nil, domain.ErrValidation
	}
	constraintGraph, err := requiredJSONObject(in.ConstraintGraph)
	if err != nil {
		return nil, nil, err
	}
	proofEvidence, err := requiredJSONObject(in.ProofEvidence)
	if err != nil {
		return nil, nil, err
	}
	return constraintGraph, proofEvidence, nil
}

func validateExactPlanInput(in ArenaExactPlanInput) ([]byte, []byte, error) {
	if !validExactPlanMetadata(in) || !validExactPlanDecision(in) {
		return nil, nil, domain.ErrValidation
	}
	constraintGraph, err := requiredJSONObject(in.ConstraintGraph)
	if err != nil {
		return nil, nil, err
	}
	proofEvidence, err := requiredJSONObject(in.ProofEvidence)
	if err != nil {
		return nil, nil, err
	}
	tracker := newExactPlanIdentityTracker(len(in.Branches))
	for _, branch := range in.Branches {
		if err := tracker.validateBranch(in.SourceDraftRevision, branch); err != nil {
			return nil, nil, err
		}
	}
	return constraintGraph, proofEvidence, nil
}

type exactPlanIdentityTracker struct {
	branchIDs  map[uuid.UUID]struct{}
	branchKeys map[string]struct{}
	taskIDs    map[uuid.UUID]struct{}
	identities map[uuid.UUID]struct{}
}

func newExactPlanIdentityTracker(branchCount int) *exactPlanIdentityTracker {
	return &exactPlanIdentityTracker{
		branchIDs:  make(map[uuid.UUID]struct{}, branchCount),
		branchKeys: make(map[string]struct{}, branchCount),
		taskIDs:    make(map[uuid.UUID]struct{}, branchCount*3),
		identities: make(map[uuid.UUID]struct{}, branchCount*9),
	}
}

func validExactPlanMetadata(in ArenaExactPlanInput) bool {
	return in.ID != uuid.Nil && in.TournamentID != uuid.Nil && in.RosterID != uuid.Nil &&
		in.ParentPlanID != uuid.Nil && in.RevisionID != uuid.Nil && in.SourceRosterRevision >= 1 &&
		in.SourcePoolRevisionID != uuid.Nil && in.SourceDraftRevision != uuid.Nil && len(in.Branches) > 0 &&
		len(in.Branches) <= math.MaxInt32 && validServerTime(in.CreatedAt)
}

func validExactPlanDecision(in ArenaExactPlanInput) bool {
	if err := in.DecisionEvidence.Validate(); err != nil {
		return false
	}
	return in.DecisionEvidence.Purpose == domain.ArenaDecisionPurposeTask &&
		in.DecisionEvidence.OwnerID == in.ID && !in.DecisionEvidence.DecidedAt.After(in.CreatedAt)
}

func (tracker *exactPlanIdentityTracker) validateBranch(
	sourceDraftRevision uuid.UUID,
	branch ArenaAssignmentBranchInput,
) error {
	key := strings.TrimSpace(branch.Key)
	if branch.ID == uuid.Nil || branch.DraftID == uuid.Nil || branch.DraftRevisionID != sourceDraftRevision ||
		key == "" || key != branch.Key || len(branch.Categories) == 0 || len(branch.Categories) > 3 ||
		len(branch.Edges) != domain.ArenaAssignmentReserveCount+1 {
		return domain.ErrValidation
	}
	if tracker.hasBranchIdentity(branch.ID, branch.Key) || !validAssignmentCategories(branch.Categories) {
		return domain.ErrValidation
	}
	tracker.branchIDs[branch.ID] = struct{}{}
	tracker.branchKeys[branch.Key] = struct{}{}
	positions := make(map[int]struct{}, domain.ArenaAssignmentReserveCount+1)
	for _, edge := range branch.Edges {
		if err := tracker.validateEdge(edge, positions); err != nil {
			return err
		}
	}
	return nil
}

func (tracker *exactPlanIdentityTracker) hasBranchIdentity(id uuid.UUID, key string) bool {
	_, duplicateID := tracker.branchIDs[id]
	_, duplicateKey := tracker.branchKeys[key]
	return duplicateID || duplicateKey
}

func validAssignmentCategories(categories []domain.Category) bool {
	for _, category := range categories {
		if !category.IsValid() {
			return false
		}
	}
	return true
}

func (tracker *exactPlanIdentityTracker) validateEdge(
	edge ArenaAssignmentEdgeInput,
	positions map[int]struct{},
) error {
	if !validExactPlanEdgeMetadata(edge) {
		return domain.ErrValidation
	}
	if err := edge.Snapshot.Validate(); err != nil {
		return domain.WrapError(err, domain.ErrValidation)
	}
	if _, err := requiredJSONObject(edge.SelectionEvidence); err != nil {
		return err
	}
	if _, duplicate := positions[edge.Position]; duplicate {
		return domain.ErrValidation
	}
	positions[edge.Position] = struct{}{}
	if tracker.hasEdgeIdentity(edge) {
		return domain.ErrValidation
	}
	tracker.taskIDs[edge.Snapshot.TaskID] = struct{}{}
	for _, id := range []uuid.UUID{edge.ID, edge.ReservationID, edge.Snapshot.SnapshotID} {
		tracker.identities[id] = struct{}{}
	}
	return nil
}

func validExactPlanEdgeMetadata(edge ArenaAssignmentEdgeInput) bool {
	return edge.ID != uuid.Nil && edge.ReservationID != uuid.Nil && edge.Position >= 1 && edge.Position <= 3 &&
		edge.Snapshot.SnapshotID != uuid.Nil && edge.Snapshot.Version <= math.MaxInt32 &&
		edge.Snapshot.TimeLimit <= math.MaxInt32 && edge.ContentDigest != [sha256.Size]byte{}
}

func (tracker *exactPlanIdentityTracker) hasEdgeIdentity(edge ArenaAssignmentEdgeInput) bool {
	if _, duplicate := tracker.taskIDs[edge.Snapshot.TaskID]; duplicate {
		return true
	}
	for _, id := range []uuid.UUID{edge.ID, edge.ReservationID, edge.Snapshot.SnapshotID} {
		if _, duplicate := tracker.identities[id]; duplicate {
			return true
		}
	}
	return false
}

func validateAssignmentCreateInput(in ArenaAssignmentCreateInput) error {
	if in.ID == uuid.Nil || in.AttemptID == uuid.Nil || in.SeriesID == uuid.Nil || in.RosterID == uuid.Nil ||
		in.PlanID == uuid.Nil || in.BranchID == uuid.Nil || in.ReservationID == uuid.Nil || in.SnapshotID == uuid.Nil ||
		!validServerTime(in.CreatedAt) {
		return domain.ErrValidation
	}
	return nil
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

func arenaTaskSnapshotParams(
	edge ArenaAssignmentEdgeInput,
	createdAt time.Time,
) sqlc.CreateArenaTaskSnapshotParams {
	snapshot := edge.Snapshot
	return sqlc.CreateArenaTaskSnapshotParams{
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
		Hints:         mustJSON(snapshot.Hints),
		TaskUrl:       snapshot.TaskURL,
		SourceFileUrl: snapshot.SourceFileURL,
		ContentDigest: append([]byte(nil), edge.ContentDigest[:]...),
		CreatedAt:     tstz(createdAt),
	}
}

func arenaAssignmentPlanAggregate(
	plan sqlc.ArenaAssignmentPlan,
	branches []sqlc.ArenaAssignmentBranch,
	edges []sqlc.ArenaAssignmentPlanEdge,
	reservations []sqlc.ArenaTaskVersionReservation,
	snapshots []sqlc.ArenaTaskSnapshot,
) (*ArenaAssignmentPlanAggregate, error) {
	planRecord, err := arenaAssignmentPlanRecord(plan)
	if err != nil {
		return nil, err
	}
	aggregate := &ArenaAssignmentPlanAggregate{Plan: planRecord}
	aggregate.Branches = make([]ArenaAssignmentBranchRecord, len(branches))
	for index, branch := range branches {
		categories, decodeErr := decodeCategories(branch.CategorySequence)
		if decodeErr != nil {
			return nil, decodeErr
		}
		aggregate.Branches[index] = ArenaAssignmentBranchRecord{
			ID: branch.ID, PlanID: branch.PlanID, DraftID: branch.DraftID,
			DraftRevisionID: branch.DraftRevisionID, Key: branch.BranchKey, Categories: categories,
			State: branch.State, ActivatedAt: nullableTime(branch.ActivatedAt), ReleasedAt: nullableTime(branch.ReleasedAt),
			ReleaseReason: branch.ReleaseReason, SupersededAt: nullableTime(branch.SupersededAt),
			SupersessionReason: branch.SupersessionReason, CreatedAt: branch.CreatedAt.Time,
		}
	}
	aggregate.Edges = make([]ArenaAssignmentEdgeRecord, len(edges))
	for index, edge := range edges {
		selectionEvidence := make(map[string]any)
		if err := json.Unmarshal(edge.SelectionEvidence, &selectionEvidence); err != nil {
			return nil, err
		}
		aggregate.Edges[index] = ArenaAssignmentEdgeRecord{
			ID: edge.ID, PlanID: edge.PlanID, BranchID: edge.BranchID, Position: int(edge.Position),
			TaskID: edge.TaskID, TaskVersion: int(edge.TaskVersion), SelectionEvidence: selectionEvidence,
			CreatedAt: edge.CreatedAt.Time,
		}
	}
	aggregate.Reservations = make([]ArenaTaskReservationRecord, len(reservations))
	for index, reservation := range reservations {
		aggregate.Reservations[index] = arenaTaskReservationRecord(reservation)
	}
	aggregate.Snapshots = make([]ArenaTaskSnapshotRecord, len(snapshots))
	for index, snapshot := range snapshots {
		record, mapErr := arenaTaskSnapshotRecord(snapshot)
		if mapErr != nil {
			return nil, mapErr
		}
		aggregate.Snapshots[index] = record
	}
	return aggregate, nil
}

func arenaAssignmentPlanRecord(row sqlc.ArenaAssignmentPlan) (ArenaAssignmentPlanRecord, error) {
	constraintGraph := make(map[string]any)
	proofEvidence := make(map[string]any)
	if err := json.Unmarshal(row.ConstraintGraph, &constraintGraph); err != nil {
		return ArenaAssignmentPlanRecord{}, err
	}
	if err := json.Unmarshal(row.ProofEvidence, &proofEvidence); err != nil {
		return ArenaAssignmentPlanRecord{}, err
	}
	record := ArenaAssignmentPlanRecord{
		ID: row.ID, TournamentID: row.TournamentID, RosterID: row.RosterID, Kind: row.Kind,
		RevisionID: row.RevisionID, SourceRosterRevision: row.SourceRosterRevision,
		SourcePoolRevisionID: row.SourcePoolRevisionID, ReachableBranchCount: int(row.ReachableBranchCount),
		ConstraintGraph: constraintGraph, ProofEvidence: proofEvidence, State: row.State,
		CommittedAt: nullableTime(row.CommittedAt), SupersededAt: nullableTime(row.SupersededAt),
		SupersessionReason: row.SupersessionReason, CreatedAt: row.CreatedAt.Time,
	}
	if row.ParentPlanID.Valid {
		value := row.ParentPlanID.UUID
		record.ParentPlanID = &value
	}
	if row.SourceDraftRevisionID.Valid {
		value := row.SourceDraftRevisionID.UUID
		record.SourceDraftRevisionID = &value
	}
	if row.ActiveBranchID.Valid {
		value := row.ActiveBranchID.UUID
		record.ActiveBranchID = &value
	}
	if row.DecisionEvidenceID.Valid {
		if row.DecisionAlgorithmVersion == nil || !row.DecisionOwnerID.Valid || !row.DecidedAt.Valid {
			return ArenaAssignmentPlanRecord{}, domain.ErrValidation
		}
		evidence, err := arenaDecisionEvidenceFromStorage(
			row.DecisionEvidenceID.UUID,
			domain.ArenaDecisionPurposeTask,
			*row.DecisionAlgorithmVersion,
			row.DecisionInputs,
			row.DecisionSeed,
			row.DecisionResult,
			row.DecisionReplayDigest,
			row.DecisionOwnerID.UUID,
			row.DecidedAt.Time,
		)
		if err != nil {
			return ArenaAssignmentPlanRecord{}, err
		}
		record.DecisionEvidence = &evidence
	}
	return record, nil
}

func arenaTaskReservationRecord(row sqlc.ArenaTaskVersionReservation) ArenaTaskReservationRecord {
	return ArenaTaskReservationRecord{
		ID: row.ID, EdgeID: row.EdgeID, PlanID: row.PlanID, BranchID: row.BranchID,
		TaskID: row.TaskID, TaskVersion: int(row.TaskVersion), State: row.State,
		DisclosedAt: nullableTime(row.DisclosedAt), CommittedAt: nullableTime(row.CommittedAt),
		ReleasedAt: nullableTime(row.ReleasedAt), ReleaseReason: row.ReleaseReason,
		SupersededAt: nullableTime(row.SupersededAt), SupersessionReason: row.SupersessionReason,
		CreatedAt: row.CreatedAt.Time,
	}
}

func arenaTaskSnapshotRecord(row sqlc.ArenaTaskSnapshot) (ArenaTaskSnapshotRecord, error) {
	var hints []string
	if err := json.Unmarshal(row.Hints, &hints); err != nil {
		return ArenaTaskSnapshotRecord{}, err
	}
	snapshot := domain.ArenaTaskSnapshot{
		SnapshotID: row.ID, TaskID: row.TaskID, Version: int(row.TaskVersion), Kind: domain.ArenaTaskKind(row.Kind),
		Title: row.Title, Description: row.Description, Category: domain.Category(row.Category),
		Difficulty: domain.Difficulty(row.Difficulty), TimeLimit: int(row.TimeLimit), Flag: row.Flag,
		Hints: hints, TaskURL: row.TaskUrl, SourceFileURL: row.SourceFileUrl,
	}
	if err := snapshot.Validate(); err != nil {
		return ArenaTaskSnapshotRecord{}, err
	}
	if len(row.ContentDigest) != sha256.Size {
		return ArenaTaskSnapshotRecord{}, domain.ErrValidation
	}
	record := ArenaTaskSnapshotRecord{ReservationID: row.ReservationID, Snapshot: snapshot, CreatedAt: row.CreatedAt.Time}
	copy(record.ContentDigest[:], row.ContentDigest)
	return record, nil
}

func arenaAssignmentRecord(
	assignment sqlc.ArenaAssignment,
	snapshot sqlc.ArenaTaskSnapshot,
	receipts []sqlc.ArenaTaskDeliveryReceipt,
) (*ArenaAssignmentRecord, error) {
	snapshotRecord, err := arenaTaskSnapshotRecord(snapshot)
	if err != nil {
		return nil, err
	}
	record := &ArenaAssignmentRecord{
		ID: assignment.ID, AttemptID: assignment.AttemptID, SeriesID: assignment.SeriesID,
		RosterID: assignment.RosterID, PlanID: assignment.PlanID, BranchID: assignment.BranchID,
		ReservationID: assignment.ReservationID, Snapshot: snapshotRecord, State: assignment.State,
		Revision: assignment.Revision, CreatedAt: assignment.CreatedAt.Time, UpdatedAt: assignment.UpdatedAt.Time,
		CompletedAt: nullableTime(assignment.CompletedAt), SupersededAt: nullableTime(assignment.SupersededAt),
		SupersessionReason: assignment.SupersessionReason,
	}
	if assignment.SupersedesAssignmentID.Valid {
		value := assignment.SupersedesAssignmentID.UUID
		record.SupersedesAssignmentID = &value
	}
	record.Receipts = make([]domain.ArenaDeliveryReceipt, len(receipts))
	for index, receipt := range receipts {
		record.Receipts[index] = arenaDeliveryReceipt(receipt)
	}
	return record, nil
}

func arenaDeliveryReceipt(row sqlc.ArenaTaskDeliveryReceipt) domain.ArenaDeliveryReceipt {
	return domain.ArenaDeliveryReceipt{
		ID: row.ID, AssignmentID: row.AssignmentID, AttemptID: row.AttemptID,
		ParticipantID: row.ParticipantID, SnapshotID: row.SnapshotID, TaskID: row.TaskID,
		DeliveredAt: row.DeliveredAt.Time,
	}
}

func findArenaReservation(
	reservations []sqlc.ArenaTaskVersionReservation,
	id uuid.UUID,
) (sqlc.ArenaTaskVersionReservation, bool) {
	for _, reservation := range reservations {
		if reservation.ID == id {
			return reservation, true
		}
	}
	return sqlc.ArenaTaskVersionReservation{}, false
}

func arenaReservationsAreSequential(
	edges []sqlc.ArenaAssignmentPlanEdge,
	current sqlc.ArenaTaskVersionReservation,
	next sqlc.ArenaTaskVersionReservation,
) bool {
	currentPosition, currentFound := arenaReservationPosition(edges, current)
	nextPosition, nextFound := arenaReservationPosition(edges, next)
	return currentFound && nextFound && nextPosition == currentPosition+1
}

func arenaReservationPosition(
	edges []sqlc.ArenaAssignmentPlanEdge,
	reservation sqlc.ArenaTaskVersionReservation,
) (int16, bool) {
	for _, edge := range edges {
		if edge.ID == reservation.EdgeID && edge.BranchID == reservation.BranchID {
			return edge.Position, true
		}
	}
	return 0, false
}
