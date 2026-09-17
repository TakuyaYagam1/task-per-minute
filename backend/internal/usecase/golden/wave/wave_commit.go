package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func findGoldenWaveReplay(
	ctx context.Context,
	repository WaveRepository,
	scope GoldenStateScope,
	commandID uuid.UUID,
) (GoldenWaveCommandReplay, bool, error) {
	replay, err := repository.FindGoldenWaveCommand(ctx, scope.TournamentID, commandID)
	if err != nil {
		return GoldenWaveCommandReplay{}, false, fmt.Errorf("golden Wave - find command: %w", err)
	}
	if replay == nil {
		return GoldenWaveCommandReplay{}, false, nil
	}
	return *replay, true, nil
}

func reconcileGoldenWaveReplay(
	scope GoldenStateScope,
	commandID uuid.UUID,
	kind GoldenWaveCommandKind,
	digest [sha256.Size]byte,
	replay GoldenWaveCommandReplay,
) (*GoldenWaveExecution, bool, error) {
	receipt := replay.Receipt
	if receipt.CommandID != commandID || receipt.Scope != scope || receipt.Kind != kind || receipt.CommandDigest != digest {
		return nil, false, ErrGoldenWaveCommandReuse
	}
	if replay.Execution.Scope != scope || replay.Execution.Validate() != nil ||
		!goldenExecutionHasReceipt(replay.Execution, receipt) {
		return nil, false, domain.ErrInternal
	}
	clone := replay.Execution.Snapshot()
	return &clone, false, nil
}

type goldenWaveAttemptLoad struct {
	Execution *GoldenWaveExecution
	Replay    *GoldenWaveExecution
}

func loadGoldenWaveCommandAttempt(
	ctx context.Context,
	repository WaveRepository,
	scope GoldenStateScope,
	commandID uuid.UUID,
	kind GoldenWaveCommandKind,
	digest [sha256.Size]byte,
) (goldenWaveAttemptLoad, error) {
	execution, err := repository.LoadGoldenWaveExecution(ctx, scope)
	if err != nil {
		return goldenWaveAttemptLoad{}, fmt.Errorf("load golden Wave execution: %w", err)
	}
	if replay, found, findErr := findGoldenWaveReplay(ctx, repository, scope, commandID); findErr != nil {
		return goldenWaveAttemptLoad{}, findErr
	} else if found {
		result, _, replayErr := reconcileGoldenWaveReplay(scope, commandID, kind, digest, replay)
		return goldenWaveAttemptLoad{Replay: result}, replayErr
	}
	if execution == nil {
		return goldenWaveAttemptLoad{}, nil
	}
	if execution.Validate() != nil {
		return goldenWaveAttemptLoad{}, domain.ErrInternal
	}
	if receipt, found := goldenExecutionReceiptByCommand(*execution, commandID); found {
		replay := GoldenWaveCommandReplay{Receipt: receipt, Execution: execution.Snapshot()}
		result, _, replayErr := reconcileGoldenWaveReplay(scope, commandID, kind, digest, replay)
		return goldenWaveAttemptLoad{Replay: result}, replayErr
	}
	return goldenWaveAttemptLoad{Execution: execution}, nil
}

func loadValidGoldenState(
	ctx context.Context,
	repository WaveRepository,
	scope GoldenStateScope,
) (GoldenState, error) {
	state, err := repository.LoadGoldenState(ctx, scope)
	if err != nil {
		return GoldenState{}, fmt.Errorf("load golden state: %w", err)
	}
	if state.Validate() != nil {
		return GoldenState{}, domain.ErrInternal
	}
	return state, nil
}

func commitGoldenWaveExecution(
	ctx context.Context,
	repository WaveRepository,
	commit GoldenWaveExecutionCommit,
	commandID uuid.UUID,
	kind GoldenWaveCommandKind,
	digest [sha256.Size]byte,
) (*GoldenWaveExecution, bool, bool, error) {
	proposed := commit.Next.Snapshot()
	frozen := commit
	frozen.ExpectedState = CloneExpectation(commit.ExpectedState)
	frozen.ExpectedExecution = cloneGoldenExecutionExpectation(commit.ExpectedExecution)
	frozen.NewIdentityIDs = append([]uuid.UUID(nil), commit.NewIdentityIDs...)
	frozen.Next = proposed.Snapshot()
	if commit.Authority != nil {
		authority := *commit.Authority
		frozen.Authority = &authority
	}
	if len(frozen.NewIdentityIDs) == 0 || !IDsCanonical(frozen.NewIdentityIDs) {
		return nil, false, false, domain.ErrInternal
	}
	committed, changed, err := repository.CommitGoldenWaveExecution(ctx, frozen)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if errors.Is(err, ErrGoldenWaveAuthorityNotLive) {
		return nil, false, false, ErrGoldenWaveAuthorityNotLive
	}
	if errors.Is(err, ErrGoldenWaveIdentityConflict) {
		return nil, false, false, ErrGoldenWaveIdentityConflict
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("golden Wave - commit execution: %w", err)
	}
	if committed == nil || committed.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	receipt, found := goldenExecutionReceiptByCommand(*committed, commandID)
	if !found || receipt.Kind != kind || receipt.CommandDigest != digest ||
		(changed && !committed.Expectation().Equal(proposed.Expectation())) {
		return nil, false, false, domain.ErrInternal
	}
	clone := committed.Snapshot()
	return &clone, changed, false, nil
}

func goldenWaveError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenWaveExecution, fmt.Sprintf(format, arguments...))
}
