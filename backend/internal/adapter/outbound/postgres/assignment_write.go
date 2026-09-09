package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (r *AssignmentPostgres) CreateAssignment(
	ctx context.Context,
	in AssignmentCreateInput,
) (*AssignmentRecord, error) {
	if err := validateAssignmentCreateInput(in); err != nil {
		return nil, err
	}
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		return r.createAssignmentTx(txCtx, in)
	})
	if err != nil {
		return nil, mapRepositoryWriteError("AssignmentPostgres - CreateAssignment", err)
	}
	return r.GetAssignment(ctx, in.ID)
}

func (r *AssignmentPostgres) createAssignmentTx(
	ctx context.Context,
	in AssignmentCreateInput,
) error {
	querier := r.tx.Querier(ctx)
	plan, err := querier.LockAssignmentPlan(ctx, in.PlanID)
	if err != nil {
		return err
	}
	if plan.Kind == "exact_draft" {
		children, lookupErr := querier.LockAssignmentDraftChildScope(ctx, sqlc.LockAssignmentDraftChildScopeParams{
			BranchID: in.BranchID, PlanID: in.PlanID, RosterID: in.RosterID, SeriesID: in.SeriesID,
		})
		if lookupErr != nil {
			return lookupErr
		}
		if !matchesExactDraftAssignmentPlan(plan, in, children) {
			return domain.ErrConflict
		}
	} else if !matchesAssignmentPlan(plan, in) {
		return domain.ErrConflict
	}
	reservations, err := querier.ListAssignmentTaskVersionReservations(ctx, in.PlanID)
	if err != nil {
		return err
	}
	reservation, ok := findReservation(reservations, in.ReservationID)
	if !ok || !reservationAvailableForAssignment(reservation, in.BranchID, uuid.Nil) {
		return domain.ErrConflict
	}
	edges, err := querier.ListAssignmentPlanEdges(ctx, in.PlanID)
	if err != nil {
		return err
	}
	if position, found := reservationPosition(edges, reservation); !found || position != 1 {
		return domain.ErrConflict
	}
	snapshot, err := querier.GetAssignmentTaskSnapshot(ctx, in.SnapshotID)
	if err != nil {
		return err
	}
	if !snapshotMatchesReservation(snapshot, reservation) {
		return domain.ErrConflict
	}
	_, err = querier.CreateAssignment(ctx, sqlc.CreateAssignmentParams{
		ID: in.ID, AttemptID: in.AttemptID, SeriesID: in.SeriesID, RosterID: in.RosterID,
		PlanID: in.PlanID, BranchID: in.BranchID, ReservationID: in.ReservationID,
		SnapshotID: in.SnapshotID, TaskID: snapshot.TaskID, TaskVersion: snapshot.TaskVersion,
		CreatedAt: tstz(in.CreatedAt),
	})
	if err != nil {
		return err
	}
	if plan.Kind != "exact_draft" {
		return nil
	}
	return createReplayReserveAuthorityTx(ctx, querier, in.ID)
}

func createReplayReserveAuthorityTx(
	ctx context.Context,
	querier *sqlc.Queries,
	assignmentID uuid.UUID,
) error {
	created, err := querier.CreateReplayReserveAuthority(ctx, assignmentID)
	if err != nil {
		return err
	}
	if created != assignmentID {
		return domain.ErrConflict
	}
	return querier.CreateReplayReserveAuthorityPoolVersion(ctx, assignmentID)
}

func matchesAssignmentPlan(plan sqlc.LockAssignmentPlanRow, in AssignmentCreateInput) bool {
	return plan.State == "committed" && plan.ActiveBranchID.Valid &&
		plan.ActiveBranchID.UUID == in.BranchID && plan.RosterID == in.RosterID
}

func matchesExactDraftAssignmentPlan(plan sqlc.LockAssignmentPlanRow, in AssignmentCreateInput, children []sqlc.LockAssignmentDraftChildScopeRow) bool {
	if plan.Kind != "exact_draft" || plan.State != "committed" || plan.ID != in.PlanID ||
		plan.RosterID != in.RosterID || !plan.ActiveDraftBranchID.Valid || len(children) != 1 {
		return false
	}
	child := children[0]
	return child.ID == in.BranchID && child.PlanID == in.PlanID && child.RosterID == in.RosterID &&
		child.SeriesID == in.SeriesID && child.GroupID == plan.ActiveDraftBranchID.UUID &&
		child.ChildState == "active" && child.GroupState == "active"
}

func reservationAvailableForAssignment(
	reservation sqlc.ListAssignmentTaskVersionReservationsRow,
	branchID uuid.UUID,
	excludedReservationID uuid.UUID,
) bool {
	return reservation.BranchID == branchID && reservation.State == "committed" &&
		!reservation.DisclosedAt.Valid && reservation.ID != excludedReservationID
}

func snapshotMatchesReservation(
	snapshot sqlc.TaskSnapshot,
	reservation sqlc.ListAssignmentTaskVersionReservationsRow,
) bool {
	return snapshot.ReservationID == reservation.ID && snapshot.TaskID == reservation.TaskID &&
		snapshot.TaskVersion == reservation.TaskVersion
}

func (r *AssignmentPostgres) Deliver(
	ctx context.Context,
	assignmentID uuid.UUID,
	receiptID uuid.UUID,
	participantID uuid.UUID,
	deliveredAt time.Time,
) (domain.TaskDeliveryReceipt, bool, error) {
	if assignmentID == uuid.Nil || receiptID == uuid.Nil || participantID == uuid.Nil ||
		!validServerTime(deliveredAt) {
		return domain.TaskDeliveryReceipt{}, false, domain.ErrValidation
	}
	var receipt sqlc.TaskDeliveryReceipt
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		receipt, changed, err = r.deliverTx(txCtx, assignmentID, receiptID, participantID, deliveredAt)
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.TaskDeliveryReceipt{}, false, ErrAssignmentNotFound
		}
		return domain.TaskDeliveryReceipt{}, false,
			mapRepositoryWriteError("AssignmentPostgres - Deliver", err)
	}
	return deliveryReceipt(receipt), changed, nil
}

func (r *AssignmentPostgres) deliverTx(
	ctx context.Context,
	assignmentID uuid.UUID,
	receiptID uuid.UUID,
	participantID uuid.UUID,
	deliveredAt time.Time,
) (sqlc.TaskDeliveryReceipt, bool, error) {
	querier := r.tx.Querier(ctx)
	assignment, err := querier.LockAssignment(ctx, assignmentID)
	if err != nil {
		return sqlc.TaskDeliveryReceipt{}, false, err
	}
	if assignment.State != "active" || deliveredAt.Before(assignment.CreatedAt.Time) {
		return sqlc.TaskDeliveryReceipt{}, false, domain.ErrConflict
	}
	receipts, err := querier.ListAssignmentTaskDeliveryReceipts(ctx, assignmentID)
	if err != nil {
		return sqlc.TaskDeliveryReceipt{}, false, err
	}
	if existing, ok := findParticipantReceipt(receipts, participantID); ok {
		return existing, false, nil
	}
	if len(receipts) == 0 {
		if err = discloseReservation(ctx, querier, assignment.ReservationID, deliveredAt); err != nil {
			return sqlc.TaskDeliveryReceipt{}, false, err
		}
	}
	receipt, err := querier.CreateAssignmentTaskDeliveryReceipt(ctx, sqlc.CreateAssignmentTaskDeliveryReceiptParams{
		ID: receiptID, AssignmentID: assignment.ID, AttemptID: assignment.AttemptID,
		RosterID: assignment.RosterID, ParticipantID: participantID,
		InstanceID: domain.ParticipantTaskInstanceID(assignment.ID, participantID), SnapshotID: assignment.SnapshotID,
		TaskID: assignment.TaskID, TaskVersion: assignment.TaskVersion, DeliveredAt: tstz(deliveredAt),
	})
	return receipt, err == nil, err
}

func findParticipantReceipt(
	receipts []sqlc.TaskDeliveryReceipt,
	participantID uuid.UUID,
) (sqlc.TaskDeliveryReceipt, bool) {
	for _, receipt := range receipts {
		if receipt.ParticipantID == participantID {
			return receipt, true
		}
	}
	return sqlc.TaskDeliveryReceipt{}, false
}

func discloseReservation(
	ctx context.Context,
	querier *sqlc.Queries,
	reservationID uuid.UUID,
	disclosedAt time.Time,
) error {
	_, err := querier.DiscloseAssignmentTaskReservationCAS(ctx, sqlc.DiscloseAssignmentTaskReservationCASParams{
		DisclosedAt: tstz(disclosedAt), ID: reservationID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return err
}

func (r *AssignmentPostgres) Supersede(
	ctx context.Context,
	assignmentID uuid.UUID,
	expectedRevision int64,
	in AssignmentSupersedeInput,
) (*AssignmentRecord, bool, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if !validSupersedeCommand(assignmentID, expectedRevision, in) {
		return nil, false, domain.ErrValidation
	}
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		return r.supersedeTx(txCtx, assignmentID, expectedRevision, in)
	})
	if err != nil {
		if errors.Is(err, errAssignmentCAS) {
			return nil, false, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrAssignmentNotFound
		}
		return nil, false, mapRepositoryWriteError("AssignmentPostgres - Supersede", err)
	}
	record, err := r.GetAssignment(ctx, in.ID)
	return record, true, err
}

func validSupersedeCommand(
	assignmentID uuid.UUID,
	expectedRevision int64,
	in AssignmentSupersedeInput,
) bool {
	return assignmentID != uuid.Nil && expectedRevision >= 1 && in.ID != uuid.Nil &&
		in.ReservationID != uuid.Nil && in.SnapshotID != uuid.Nil && in.Reason != "" &&
		validServerTime(in.OccurredAt)
}

func (r *AssignmentPostgres) supersedeTx(
	ctx context.Context,
	assignmentID uuid.UUID,
	expectedRevision int64,
	in AssignmentSupersedeInput,
) error {
	querier := r.tx.Querier(ctx)
	old, err := querier.LockAssignment(ctx, assignmentID)
	if err != nil {
		return err
	}
	if old.State != "active" || old.Revision != expectedRevision || in.OccurredAt.Before(old.CreatedAt.Time) {
		return errAssignmentCAS
	}
	reservation, snapshot, err := loadReplacementEvidence(ctx, querier, old, in)
	if err != nil {
		return err
	}
	if _, err = querier.SupersedeAssignmentCAS(ctx, sqlc.SupersedeAssignmentCASParams{
		SupersededAt: tstz(in.OccurredAt), SupersessionReason: &in.Reason,
		ID: assignmentID, ExpectedRevision: expectedRevision,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errAssignmentCAS
		}
		return err
	}
	plan, err := querier.LockAssignmentPlan(ctx, old.PlanID)
	if err != nil {
		return err
	}
	_, err = querier.CreateAssignment(ctx, sqlc.CreateAssignmentParams{
		ID: in.ID, AttemptID: old.AttemptID, SeriesID: old.SeriesID, RosterID: old.RosterID,
		PlanID: old.PlanID, BranchID: old.BranchID, ReservationID: reservation.ID,
		SnapshotID: snapshot.ID, TaskID: snapshot.TaskID, TaskVersion: snapshot.TaskVersion,
		SupersedesAssignmentID: uuid.NullUUID{UUID: old.ID, Valid: true}, CreatedAt: tstz(in.OccurredAt),
	})
	if err != nil {
		return err
	}
	if plan.Kind != "exact_draft" {
		return nil
	}
	return createReplayReserveAuthorityTx(ctx, querier, in.ID)
}

func loadReplacementEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	old sqlc.Assignment,
	in AssignmentSupersedeInput,
) (sqlc.ListAssignmentTaskVersionReservationsRow, sqlc.TaskSnapshot, error) {
	reservations, err := querier.ListAssignmentTaskVersionReservations(ctx, old.PlanID)
	if err != nil {
		return sqlc.ListAssignmentTaskVersionReservationsRow{}, sqlc.TaskSnapshot{}, err
	}
	reservation, ok := findReservation(reservations, in.ReservationID)
	if !ok || !reservationAvailableForAssignment(reservation, old.BranchID, old.ReservationID) {
		return sqlc.ListAssignmentTaskVersionReservationsRow{}, sqlc.TaskSnapshot{}, domain.ErrConflict
	}
	current, ok := findReservation(reservations, old.ReservationID)
	if !ok {
		return sqlc.ListAssignmentTaskVersionReservationsRow{}, sqlc.TaskSnapshot{}, domain.ErrConflict
	}
	edges, err := querier.ListAssignmentPlanEdges(ctx, old.PlanID)
	if err != nil {
		return sqlc.ListAssignmentTaskVersionReservationsRow{}, sqlc.TaskSnapshot{}, err
	}
	if !reservationsAreSequential(edges, current, reservation) {
		return sqlc.ListAssignmentTaskVersionReservationsRow{}, sqlc.TaskSnapshot{}, domain.ErrConflict
	}
	snapshot, err := querier.GetAssignmentTaskSnapshot(ctx, in.SnapshotID)
	if err != nil {
		return sqlc.ListAssignmentTaskVersionReservationsRow{}, sqlc.TaskSnapshot{}, err
	}
	if !snapshotMatchesReservation(snapshot, reservation) {
		return sqlc.ListAssignmentTaskVersionReservationsRow{}, sqlc.TaskSnapshot{}, domain.ErrConflict
	}
	return reservation, snapshot, nil
}

func (r *AssignmentPostgres) GetAssignment(
	ctx context.Context,
	assignmentID uuid.UUID,
) (*AssignmentRecord, error) {
	if assignmentID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	assignment, err := querier.GetAssignment(ctx, assignmentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAssignmentNotFound
		}
		return nil, fmt.Errorf("AssignmentPostgres - GetAssignment: %w", err)
	}
	snapshot, err := querier.GetAssignmentTaskSnapshot(ctx, assignment.SnapshotID)
	if err != nil {
		return nil, fmt.Errorf("AssignmentPostgres - GetAssignment - snapshot: %w", err)
	}
	receipts, err := querier.ListAssignmentTaskDeliveryReceipts(ctx, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("AssignmentPostgres - GetAssignment - receipts: %w", err)
	}
	return assignmentRecord(assignment, snapshot, receipts)
}
