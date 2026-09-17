package stage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

var (
	ErrInvalidStage  = errors.New("invalid correction stage rollback")
	ErrStageConflict = errors.New("correction stage commit conflict")
)

type StageMode string

const (
	StageModePlayoff StageMode = "playoff"
	StageModeGolden  StageMode = "golden"
)

type StageTransition string

const (
	StageUnchanged           StageTransition = "unchanged"
	StagePlayoffToGolden     StageTransition = "playoff_to_golden"
	StageGoldenToPlayoff     StageTransition = "golden_to_playoff"
	StageGoldenGroupsChanged StageTransition = "golden_groups_changed"
)

type StagePauseState string

const StagePauseStatePaused StagePauseState = "paused"

type StagePauseExpectation struct {
	TournamentID    uuid.UUID
	GroupID         uuid.UUID
	GroupRevisionID domain.DerivedRevisionID
	SessionID       uuid.UUID
	RevisionID      uuid.UUID
	Revision        int64
	State           StagePauseState
	PayloadDigest   [sha256.Size]byte
}

type StageLayout struct {
	Mode         StageMode
	GoldenGroups []domain.GoldenGroupState
	Paused       []StagePauseExpectation
}

type StageSnapshot struct {
	TournamentID       uuid.UUID
	TournamentState    domain.TournamentState
	TournamentRevision int64
	CutoffEvents       []CutoffEvent
	Layout             StageLayout
	Swiss              StageSwissAuthority
}

// StageSwissAuthority is the locked, normalized Swiss authority used to
// derive a correction's next stage. It deliberately contains no presentation
// artifact payload and no client-selected topology.
type StageSwissAuthority struct {
	Participants []resultprojection.CanonicalSwissParticipant
	Ledger       []resultprojection.CanonicalSwissPointLedgerEntry
	Complete     bool
}

type StageGroupSupersessionIntent struct {
	GroupID    uuid.UUID
	RevisionID domain.DerivedRevisionID
}

type StageCommand struct {
	Correction         Plan
	Corrected          StageLayout
	GroupSupersessions []StageGroupSupersessionIntent
}

type StageGroupSupersession struct {
	GroupID            uuid.UUID
	PreviousRevisionID domain.DerivedRevisionID
	RevisionID         domain.DerivedRevisionID
	ReplacementGroupID *uuid.UUID
	Previous           domain.GoldenGroupState
}

type StageResult struct {
	Transition         StageTransition
	Corrected          StageLayout
	GroupSupersessions []StageGroupSupersession
	CancelledAttempts  []domain.GoldenAttempt
	WithdrawPlayoff    bool
	CreatePlayoff      bool
	Proof              []byte
	ProofDigest        [sha256.Size]byte
}

type StageCommit struct {
	ExpectedTournamentState    domain.TournamentState
	ExpectedTournamentRevision int64
	Correction                 []byte
	Result                     StageResult
}

type StageUseCase struct {
	transactions TransactionManager
	repository   StageRepository
	clock        Clock
}

func NewStageUseCase(
	transactions TransactionManager,
	repository StageRepository,
	clock Clock,
) *StageUseCase {
	return &StageUseCase{transactions: transactions, repository: repository, clock: clock}
}

func (u *StageUseCase) Rollback(
	ctx context.Context,
	command StageCommand,
) (StageResult, error) {
	if u == nil || u.transactions == nil || u.repository == nil || u.clock == nil {
		return StageResult{}, invalidCorrectionStage("missing transaction dependency", nil)
	}
	command = cloneCorrectionStageCommand(command)
	if err := command.Correction.Validate(); err != nil {
		return StageResult{}, invalidCorrectionStage("invalid correction plan", err)
	}
	condition := command.Correction.CutoffCondition()
	var committed StageResult
	err := u.transactions.Do(ctx, func(txCtx context.Context) error {
		snapshot, err := u.repository.LoadCorrectionStage(txCtx, condition.TournamentID())
		if err != nil {
			return err
		}
		snapshot = cloneCorrectionStageSnapshot(snapshot)
		if snapshot.TournamentID != condition.TournamentID() {
			return invalidCorrectionStage("loaded another Tournament", nil)
		}
		if err := condition.ValidateCurrent(
			snapshot.TournamentState,
			snapshot.TournamentRevision,
			snapshot.CutoffEvents,
		); err != nil {
			return err
		}
		changedAt := u.clock.Now()
		if !validCorrectionServerTime(changedAt) {
			return invalidCorrectionStage("invalid rollback clock", nil)
		}
		result, err := PlanStageRollback(command, snapshot, changedAt)
		if err != nil {
			return err
		}
		changed, err := u.repository.CommitCorrectionStage(txCtx, StageCommit{
			ExpectedTournamentState:    snapshot.TournamentState,
			ExpectedTournamentRevision: snapshot.TournamentRevision,
			Correction:                 command.Correction.Bytes(), Result: result,
		})
		if err != nil {
			return err
		}
		if !changed {
			return ErrStageConflict
		}
		committed = cloneCorrectionStageResult(result)
		return nil
	})
	if err != nil {
		return StageResult{}, err
	}
	return committed, nil
}

// PlanStageRollback is the shared server-owned stage policy. Callers that
// already hold the correction transaction use it before their durable commit;
// StageUseCase uses the same policy for its standalone path.
func PlanStageRollback(
	command StageCommand,
	snapshot StageSnapshot,
	changedAt time.Time,
) (StageResult, error) {
	return planCorrectionStageRollback(snapshot, command, changedAt)
}

// PlanServerOwnedStageRollback derives a correction rollback from the locked
// stage snapshot. A client never provides Golden identities or a target stage.
func PlanServerOwnedStageRollback(
	correction Plan,
	snapshot StageSnapshot,
	changedAt time.Time,
) (StageResult, error) {
	if err := correction.Validate(); err != nil {
		return StageResult{}, invalidCorrectionStage("invalid correction plan", err)
	}
	corrected, err := planServerOwnedStageLayout(correction, snapshot)
	if err != nil {
		return StageResult{}, err
	}
	command := StageCommand{Correction: correction, Corrected: corrected}
	command.GroupSupersessions = correctionStageServerSupersessionIntents(correction, snapshot.Layout, corrected)
	return PlanStageRollback(command, snapshot, changedAt)
}

// ValidateServerOwnedStageRollback rejects an adapter-supplied or stale stage
// result before it can reach a durable correction commit.
func ValidateServerOwnedStageRollback(
	correction Plan,
	snapshot StageSnapshot,
	changedAt time.Time,
	result StageResult,
) error {
	expected, err := PlanServerOwnedStageRollback(correction, snapshot, changedAt)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, result) {
		return invalidCorrectionStage("stage result differs from the locked server plan", nil)
	}
	if !correctionStageProofMatches(correction, result) {
		return invalidCorrectionStage("stage proof differs from the locked server plan", nil)
	}
	return nil
}

func invalidCorrectionStage(detail string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrInvalidStage, detail)
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalidStage, detail, cause)
}
