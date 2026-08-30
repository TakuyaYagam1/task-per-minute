package arena

import (
	"errors"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidFailureClassification = errors.New("invalid arena failure classification")

type NormalAttemptFailureClass string

const (
	NormalAttemptFailureNoSolve             NormalAttemptFailureClass = "no_solve"
	NormalAttemptFailureTask                NormalAttemptFailureClass = "task_failure"
	NormalAttemptFailureCommonPlatform      NormalAttemptFailureClass = "common_platform_failure"
	NormalAttemptFailureDisconnect          NormalAttemptFailureClass = "disconnect"
	NormalAttemptFailureExecutionEpochBreak NormalAttemptFailureClass = "execution_epoch_break"
)

type NormalAttemptFailureDecision struct {
	Class          NormalAttemptFailureClass
	Reason         domain.ArenaGameResultReason
	GameState      domain.ArenaGameState
	SeriesState    domain.ArenaSeriesState
	CategoryCutoff domain.Category
}

func ClassifyNormalAttemptFailure(
	class NormalAttemptFailureClass,
	categoryCutoff domain.Category,
) (NormalAttemptFailureDecision, error) {
	reason, ok := normalAttemptFailureReasons[class]
	if !ok || !categoryCutoff.IsValid() {
		return NormalAttemptFailureDecision{}, failureClassificationError(
			"unknown class %q or category cutoff",
			class,
		)
	}
	decision := NormalAttemptFailureDecision{
		Class: class, Reason: reason,
		GameState:      domain.ArenaGameStateVoid,
		SeriesState:    domain.ArenaSeriesStateReplayRequired,
		CategoryCutoff: categoryCutoff,
	}
	if err := decision.Validate(); err != nil {
		return NormalAttemptFailureDecision{}, err
	}
	return decision, nil
}

func (d NormalAttemptFailureDecision) Validate() error {
	wantReason, ok := normalAttemptFailureReasons[d.Class]
	if !ok || d.Reason != wantReason || d.GameState != domain.ArenaGameStateVoid ||
		d.SeriesState != domain.ArenaSeriesStateReplayRequired || !d.CategoryCutoff.IsValid() ||
		!d.Reason.IsLegalFor(d.GameState) {
		return failureClassificationError("decision does not match the failure policy")
	}
	return nil
}

var normalAttemptFailureReasons = map[NormalAttemptFailureClass]domain.ArenaGameResultReason{
	NormalAttemptFailureNoSolve:             domain.ArenaGameResultReasonNoSolve,
	NormalAttemptFailureTask:                domain.ArenaGameResultReasonTaskFailure,
	NormalAttemptFailureCommonPlatform:      domain.ArenaGameResultReasonCommonPlatformFailure,
	NormalAttemptFailureDisconnect:          domain.ArenaGameResultReasonDisconnect,
	NormalAttemptFailureExecutionEpochBreak: domain.ArenaGameResultReasonExecutionEpochBreak,
}

func failureClassificationError(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %s",
		ErrInvalidFailureClassification,
		fmt.Sprintf(format, arguments...),
	)
}
