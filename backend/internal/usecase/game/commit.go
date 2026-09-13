package game

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

const reconnectMutationAttempts = 2

type reconnectRecordBuilder func(ReconnectAuthority, time.Time) (ReconnectRecord, bool, error)

func runClockedReconnectMutation(
	ctx context.Context,
	repository ReconnectRepository,
	clock ReconnectClock,
	scope pause.GraphScope,
	commandID uuid.UUID,
	participantID uuid.UUID,
	match func(ReconnectRecord) bool,
	build reconnectRecordBuilder,
) (*ReconnectRecord, bool, error) {
	for range reconnectMutationAttempts {
		receipt, found, err := findReconnectReceipt(ctx, repository, scope.TournamentID, commandID, match)
		if err != nil || found {
			return receipt, false, err
		}
		authority, err := loadReconnectMutationAuthority(ctx, repository, scope, participantID)
		if err != nil {
			return nil, false, err
		}
		record, commit, err := buildClockedReconnectRecord(authority, clock, build)
		if err != nil {
			return nil, false, err
		}
		if !commit {
			return cloneReconnectRecord(&record), false, nil
		}
		result, conflict, err := commitReconnectRecord(ctx, repository, authority.Revision, record)
		if conflict {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		return result, true, nil
	}
	return nil, false, ErrConflict
}

func findReconnectReceipt(
	ctx context.Context,
	repository ReconnectRepository,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
	match func(ReconnectRecord) bool,
) (*ReconnectRecord, bool, error) {
	recorded, err := repository.FindCommand(ctx, tournamentID, commandID)
	if err != nil {
		return nil, false, fmt.Errorf("reconnect - find command: %w", err)
	}
	if recorded == nil {
		return nil, false, nil
	}
	if validateReconnectRecord(*recorded) != nil || recorded.ReconnectAuthority.Revision != recorded.ExpectedAuthorityRevision+1 {
		return nil, true, domain.ErrInternal
	}
	if !match(*recorded) {
		return nil, true, ErrCommandReuse
	}
	return cloneReconnectRecord(recorded), true, nil
}

func loadReconnectMutationAuthority(
	ctx context.Context,
	repository ReconnectRepository,
	scope pause.GraphScope,
	participantID uuid.UUID,
) (ReconnectAuthority, error) {
	authority, err := repository.LoadAuthority(ctx, scope, participantID)
	if err != nil {
		return ReconnectAuthority{}, fmt.Errorf("reconnect - load authority: %w", err)
	}
	if authority.Scope != scope {
		return ReconnectAuthority{}, reconnectError("authority scope does not match command")
	}
	if err := validateReconnectAuthority(authority); err != nil {
		return ReconnectAuthority{}, err
	}
	return authority, nil
}

func buildClockedReconnectRecord(
	authority ReconnectAuthority,
	clock ReconnectClock,
	build reconnectRecordBuilder,
) (ReconnectRecord, bool, error) {
	now := clock.Now().Round(0).UTC()
	if !reconnectValidServerTime(now) {
		return ReconnectRecord{}, false, domain.ErrValidation
	}
	if authority.Current == nil {
		if err := validateReconnectMutationTime(authority, now); err != nil {
			return ReconnectRecord{}, false, err
		}
	}
	record, commit, err := build(authority, now)
	if err != nil {
		return ReconnectRecord{}, false, err
	}
	if err := validateReconnectRecord(record); err != nil {
		return ReconnectRecord{}, false, err
	}
	return record, commit, nil
}

func validateReconnectMutationTime(authority ReconnectAuthority, now time.Time) error {
	if !pause.TimeAtOrBefore(now, authority.GameClock.FrozenAt) || !pause.TimePointerAtOrBefore(now, authority.GameClock.ResumedAt) {
		return reconnectError("clock rollback before Game clock history")
	}
	for _, presence := range authority.Presence {
		if !pause.TimeCoversPresenceHistory(now, presence) {
			return reconnectError("clock rollback before Presence history")
		}
	}
	for _, interval := range authority.Reconnect {
		if !pause.TimeCoversReconnectHistory(now, interval) {
			return reconnectError("clock rollback before reconnect history")
		}
	}
	return nil
}

func commitReconnectRecord(
	ctx context.Context,
	repository ReconnectRepository,
	expectedRevision int64,
	record ReconnectRecord,
) (*ReconnectRecord, bool, error) {
	committed, changed, err := repository.CommitMutation(
		ctx,
		expectedRevision,
		cloneReconnectRecordValue(record),
	)
	if errors.Is(err, domain.ErrConflict) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reconnect - commit mutation: %w", err)
	}
	if !changed || committed == nil {
		return nil, false, domain.ErrInternal
	}
	result := cloneReconnectRecord(committed)
	if validateReconnectRecord(*result) != nil || !reflect.DeepEqual(*result, record) {
		return nil, false, domain.ErrInternal
	}
	return cloneReconnectRecord(result), false, nil
}

func advanceReconnectAuthority(authority *ReconnectAuthority) {
	authority.Revision++
}
