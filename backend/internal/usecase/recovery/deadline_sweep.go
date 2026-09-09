package recovery

import (
	"context"
	"fmt"
)

type SweepResult struct {
	Scanned    int
	Rearmed    int
	NextCursor DeadlineCursor
	Complete   bool
}

type DeadlineSweep struct {
	source   DeadlineSource
	rearmer  DeadlineRearmer
	observer RecoveryObserver
}

func NewDeadlineSweep(
	source DeadlineSource,
	rearmer DeadlineRearmer,
	observers ...RecoveryObserver,
) *DeadlineSweep {
	return &DeadlineSweep{
		source: source, rearmer: rearmer, observer: firstRecoveryObserver(observers...),
	}
}

func (sweep *DeadlineSweep) Sweep(
	ctx context.Context,
	after DeadlineCursor,
	limit int32,
) (SweepResult, error) {
	if !validSweepRequest(ctx, sweep, after, limit) {
		return SweepResult{}, ErrInvalidBatch
	}
	if err := ctx.Err(); err != nil {
		return SweepResult{}, err
	}

	deadlines, err := sweep.source.ListPendingDeadlines(ctx, after, limit)
	if err != nil {
		return SweepResult{}, deadlineError("list pending", err)
	}
	if len(deadlines) > int(limit) {
		return SweepResult{}, ErrInvalidBatch
	}
	if err := validateDeadlineBatch(after, deadlines); err != nil {
		return SweepResult{}, err
	}

	result := SweepResult{Scanned: len(deadlines), Complete: len(deadlines) < int(limit)}
	result.Rearmed, err = sweep.rearmDeadlines(ctx, deadlines)
	if err != nil {
		return SweepResult{}, err
	}
	if len(deadlines) > 0 {
		result.NextCursor = deadlines[len(deadlines)-1].Cursor()
	}
	return result, nil
}

func validSweepRequest(
	ctx context.Context,
	sweep *DeadlineSweep,
	after DeadlineCursor,
	limit int32,
) bool {
	return ctx != nil && sweep != nil && sweep.source != nil && sweep.rearmer != nil &&
		after.Validate() == nil && limit > 0 && limit <= MaximumSweepBatchSize
}

func validateDeadlineBatch(after DeadlineCursor, deadlines []PendingDeadline) error {
	previous := after
	for index := range deadlines {
		deadline := deadlines[index]
		if deadline.Validate() != nil || !deadline.Cursor().after(previous) {
			return fmt.Errorf("%w: item %d", ErrInvalidBatch, index)
		}
		previous = deadline.Cursor()
	}
	return nil
}

func (sweep *DeadlineSweep) rearmDeadlines(
	ctx context.Context,
	deadlines []PendingDeadline,
) (int, error) {
	rearmedCount := 0
	for index := range deadlines {
		rearmed, err := sweep.rearmer.RearmDeadline(ctx, deadlines[index])
		if err != nil {
			sweep.emitDeadlineEvent(
				ctx,
				deadlines[index],
				RecoveryOutcomeFailure,
				"deadline_rearm_failed",
				"rearmer_failed",
			)
			return 0, deadlineError("rearm", err)
		}
		if rearmed {
			rearmedCount++
			sweep.emitDeadlineEvent(
				ctx,
				deadlines[index],
				RecoveryOutcomeSuccess,
				"deadline_rearmed",
				"work_rearmed",
			)
			continue
		}
		sweep.emitDeadlineEvent(
			ctx,
			deadlines[index],
			RecoveryOutcomeRejected,
			"deadline_already_current",
			"nothing_to_rearm",
		)
	}
	return rearmedCount, nil
}
