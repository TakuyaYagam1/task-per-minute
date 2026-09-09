package game

import (
	"errors"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidFailureClassification = errors.New("invalid game failure classification")

type FailureClass string

const (
	FailureNoSolve             FailureClass = "no_solve"
	FailureTask                FailureClass = "task_failure"
	FailureCommonPlatform      FailureClass = "common_platform_failure"
	FailureDisconnect          FailureClass = "disconnect"
	FailureExecutionEpochBreak FailureClass = "execution_epoch_break"
)

type FailureDecision struct {
	Class          FailureClass
	Reason         domain.GameResultReason
	GameState      domain.GameState
	SeriesState    domain.SeriesState
	CategoryCutoff domain.Category
}

func ClassifyFailure(
	class FailureClass,
	categoryCutoff domain.Category,
) (FailureDecision, error) {
	reason, ok := normalAttemptFailureReasons[class]
	if !ok || !categoryCutoff.IsValid() {
		return FailureDecision{}, failureClassificationError(
			"unknown class %q or category cutoff",
			class,
		)
	}
	decision := FailureDecision{
		Class: class, Reason: reason,
		GameState:      domain.GameStateVoid,
		SeriesState:    domain.SeriesStateReplayRequired,
		CategoryCutoff: categoryCutoff,
	}
	if err := decision.Validate(); err != nil {
		return FailureDecision{}, err
	}
	return decision, nil
}

func (d FailureDecision) Validate() error {
	wantReason, ok := normalAttemptFailureReasons[d.Class]
	if !ok || d.Reason != wantReason || d.GameState != domain.GameStateVoid ||
		d.SeriesState != domain.SeriesStateReplayRequired || !d.CategoryCutoff.IsValid() ||
		!d.Reason.IsLegalFor(d.GameState) {
		return failureClassificationError("decision does not match the failure policy")
	}
	return nil
}

var normalAttemptFailureReasons = map[FailureClass]domain.GameResultReason{
	FailureNoSolve:             domain.GameResultReasonNoSolve,
	FailureTask:                domain.GameResultReasonTaskFailure,
	FailureCommonPlatform:      domain.GameResultReasonCommonPlatformFailure,
	FailureDisconnect:          domain.GameResultReasonDisconnect,
	FailureExecutionEpochBreak: domain.GameResultReasonExecutionEpochBreak,
}

func failureClassificationError(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %s",
		ErrInvalidFailureClassification,
		fmt.Sprintf(format, arguments...),
	)
}
