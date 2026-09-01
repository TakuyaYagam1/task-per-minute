package arena

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const goldenFailureOperatorActionLimit = 512

type GoldenReserveExhaustionCommand struct {
	Scope                  GoldenSubmissionScope
	CommandID              uuid.UUID
	FailureID              uuid.UUID
	Expected               GoldenFailureExpectation
	ClosedWaveRevisionID   domain.ArenaWaveRevisionID
	RequiredOperatorAction string
}

type GoldenReserveExhaustionUseCase struct {
	repository GoldenFailureRepository
	clock      Clock
}

func NewGoldenReserveExhaustionUseCase(
	repository GoldenFailureRepository,
	clock Clock,
) *GoldenReserveExhaustionUseCase {
	return &GoldenReserveExhaustionUseCase{repository: repository, clock: clock}
}

func (u *GoldenReserveExhaustionUseCase) Pause(
	ctx context.Context,
	command GoldenReserveExhaustionCommand,
) (*GoldenFailureRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil || !validGoldenReserveExhaustionCommand(command) {
		return nil, false, domain.ErrValidation
	}
	digest := goldenReserveExhaustionCommandDigest(command)
	if replay, err := u.repository.FindGoldenFailure(ctx, command.Scope.State.TournamentID, command.CommandID); err != nil {
		return nil, false, fmt.Errorf("GoldenReserveExhaustionUseCase - find replay: %w", err)
	} else if replay != nil {
		return reconcileGoldenFailure(*replay, command.Scope, command.CommandID, GoldenFailureRouteExhausted, digest)
	}
	failedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(failedAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenFailureCommitAttempts {
		record, changed, retry, err := u.pauseAttempt(ctx, command, digest, failedAt)
		if !retry {
			return record, changed, err
		}
		replay, replayErr := u.repository.FindGoldenFailure(
			ctx, command.Scope.State.TournamentID, command.CommandID,
		)
		if replayErr != nil {
			return nil, false, fmt.Errorf("GoldenReserveExhaustionUseCase - find retry replay: %w", replayErr)
		}
		if replay != nil {
			return reconcileGoldenFailure(*replay, command.Scope, command.CommandID, GoldenFailureRouteExhausted, digest)
		}
	}
	return nil, false, ErrGoldenFailureConflict
}

func (u *GoldenReserveExhaustionUseCase) pauseAttempt(
	ctx context.Context,
	command GoldenReserveExhaustionCommand,
	digest [sha256.Size]byte,
	failedAt time.Time,
) (*GoldenFailureRecord, bool, bool, error) {
	if replay, err := u.repository.FindGoldenFailure(ctx, command.Scope.State.TournamentID, command.CommandID); err != nil {
		return nil, false, false, fmt.Errorf("GoldenReserveExhaustionUseCase - find attempt replay: %w", err)
	} else if replay != nil {
		result, changed, replayErr := reconcileGoldenFailure(
			*replay, command.Scope, command.CommandID, GoldenFailureRouteExhausted, digest,
		)
		return result, changed, false, replayErr
	}
	authority, err := u.repository.LoadGoldenFailureAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenReserveExhaustionUseCase - load authority: %w", err)
	}
	if authority.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	if !authority.Expectation().Equal(command.Expected) {
		return nil, false, false, ErrGoldenFailureAuthorityConflict
	}
	if authority.Current != nil || !authority.Classification.Exhausted {
		return nil, false, false, ErrGoldenFailureRouteConflict
	}
	if failedAt.Before(authority.Active.StartedAt) {
		return nil, false, false, goldenFailureError("failure time precedes start")
	}
	record, err := buildGoldenFailureBase(
		authority, command.CommandID, command.FailureID, command.ClosedWaveRevisionID,
		GoldenFailureRouteExhausted, digest, failedAt,
	)
	if err != nil {
		return nil, false, false, err
	}
	record.TechnicalPause = &GoldenGroupTechnicalPause{
		GroupID:                authority.Active.Scope.State.GroupID,
		GroupRevisionID:        authority.Active.Scope.State.GroupRevisionID,
		State:                  GoldenGroupStateTechnicalPause,
		RequiredOperatorAction: command.RequiredOperatorAction,
		PausedAt:               failedAt,
	}
	if !uniqueNonZeroUUIDs(record.NewIdentityIDs) || goldenFailureAliasesAuthority(authority, record.NewIdentityIDs) {
		return nil, false, false, goldenFailureError("failure identity is reused")
	}
	record, err = sealGoldenFailureRecord(record)
	if err != nil {
		return nil, false, false, err
	}
	return commitGoldenFailure(ctx, u.repository, authority.Expectation(), record)
}

func validGoldenReserveExhaustionCommand(command GoldenReserveExhaustionCommand) bool {
	action := strings.TrimSpace(command.RequiredOperatorAction)
	return command.Scope.IsValid() && command.CommandID != uuid.Nil && command.FailureID != uuid.Nil &&
		command.Expected.Scope == command.Scope && !command.ClosedWaveRevisionID.IsZero() &&
		action != "" && action == command.RequiredOperatorAction && len(action) <= goldenFailureOperatorActionLimit &&
		uniqueNonZeroUUIDs([]uuid.UUID{
			command.CommandID, command.FailureID, command.ClosedWaveRevisionID.UUID(),
		})
}

func goldenReserveExhaustionCommandDigest(command GoldenReserveExhaustionCommand) [sha256.Size]byte {
	payload, _ := goldenEncode(command)
	return sha256.Sum256(payload)
}
