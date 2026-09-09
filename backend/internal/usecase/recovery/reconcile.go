package recovery

import (
	"context"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type RecoveryResult struct {
	FailReason RecoveryFailReason
	Plan       RecoveryRearmPlan
	Rearmed    int
}

type RecoveryRearmer interface {
	RearmRecovery(ctx context.Context, plan RecoveryRearmPlan) error
}

type RecoveryReconciler struct {
	rearmer  RecoveryRearmer
	clock    Clock
	observer RecoveryObserver
}

func NewRecoveryReconciler(
	rearmer RecoveryRearmer,
	clock Clock,
	observers ...RecoveryObserver,
) *RecoveryReconciler {
	return &RecoveryReconciler{
		rearmer:  rearmer,
		clock:    clock,
		observer: firstRecoveryObserver(observers...),
	}
}

func (r *RecoveryReconciler) Reconcile(
	ctx context.Context,
	graph RecoveryGraph,
) (RecoveryResult, error) {
	if r == nil || r.rearmer == nil || r.clock == nil {
		return RecoveryResult{}, domain.ErrValidation
	}
	now := r.clock.Now().Round(0).UTC()
	if now.IsZero() {
		return RecoveryResult{}, domain.ErrValidation
	}

	indexed, failReason := validateRecoveryGraph(graph, now)
	if failReason != "" {
		r.emitRecoveryEvent(
			ctx,
			graph,
			RecoveryOutcomeRejected,
			"fail_closed",
			recoveryEventReason(failReason),
		)
		return RecoveryResult{FailReason: failReason}, nil
	}
	plan := RecoveryRearmPlan{
		TournamentID: graph.TournamentID,
		Cursor:       graph.Cursor,
		Work:         indexed.deadlines,
	}
	if len(plan.Work) == 0 {
		r.emitRecoveryEvent(
			ctx,
			graph,
			RecoveryOutcomeSuccess,
			"no_work",
			recoveryReasonNoWork,
		)
		return RecoveryResult{Plan: plan}, nil
	}
	plan.Lease = *graph.Lease
	if err := r.rearmer.RearmRecovery(ctx, plan); err != nil {
		r.emitRecoveryEvent(
			ctx,
			graph,
			RecoveryOutcomeFailure,
			"rearm_failed",
			recoveryReasonRearmerFailed,
		)
		return RecoveryResult{}, fmt.Errorf("RecoveryReconciler - rearm: %w", err)
	}
	r.emitRecoveryEvent(
		ctx,
		graph,
		RecoveryOutcomeSuccess,
		"rearm_succeeded",
		recoveryReasonWorkRearmed,
	)
	return RecoveryResult{Plan: plan, Rearmed: len(plan.Work)}, nil
}
