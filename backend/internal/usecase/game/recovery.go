package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

var (
	ErrInvalidRecovery  = errors.New("invalid execution recovery")
	ErrNotAuthoritative = errors.New("execution recovery is not authoritative")
)

type Recoverer struct {
	authority  RecoveryAuthorityReader
	source     RecoverySource
	timers     DeadlineRearmer
	replayer   RecoveryEpochReplayer
	timeSource AuthorityTimeSource
}

func NewRecoverer(
	authority RecoveryAuthorityReader,
	source RecoverySource,
	timers DeadlineRearmer,
	replayer RecoveryEpochReplayer,
	timeSource AuthorityTimeSource,
) *Recoverer {
	return &Recoverer{
		authority:  authority,
		source:     source,
		timers:     timers,
		replayer:   replayer,
		timeSource: timeSource,
	}
}

func (recoverer *Recoverer) Recover(
	ctx context.Context,
	command RecoveryCommand,
) (RecoveryReport, error) {
	if !recoverer.available() {
		return RecoveryReport{}, domain.ErrValidation
	}
	if err := validateRecoveryCommand(command); err != nil {
		return RecoveryReport{}, err
	}
	now, err := recoverer.authorityTime(ctx)
	if err != nil {
		return RecoveryReport{}, err
	}
	lease, err := recoverer.loadAuthorityAt(ctx, command, now)
	if err != nil {
		return RecoveryReport{}, err
	}
	candidates, err := recoverer.loadCandidates(ctx, command, *lease.Stamp())
	if err != nil {
		return RecoveryReport{}, err
	}
	return recoverer.recoverCandidates(ctx, candidates, *lease.Stamp())
}

func (recoverer *Recoverer) available() bool {
	return recoverer != nil && recoverer.authority != nil && recoverer.source != nil &&
		recoverer.timers != nil && recoverer.replayer != nil && recoverer.timeSource != nil
}

func (recoverer *Recoverer) authorityTime(ctx context.Context) (time.Time, error) {
	if ctx == nil || recoverer == nil || recoverer.timeSource == nil {
		return time.Time{}, domain.ErrValidation
	}
	now, err := recoverer.timeSource.AuthorityTime(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("execution recovery - authoritative time: %w", err)
	}
	now = now.Round(0).UTC()
	if !domain.IsValidServerTime(now) {
		return time.Time{}, domain.ErrValidation
	}
	return now, nil
}

func (recoverer *Recoverer) recoverCandidates(
	ctx context.Context,
	candidates []RecoveryCandidate,
	currentStamp authoritydomain.Stamp,
) (RecoveryReport, error) {
	report := RecoveryReport{}
	for _, candidate := range candidates {
		candidateReport, err := recoverer.recoverCandidate(ctx, candidate, currentStamp)
		if err != nil {
			return RecoveryReport{}, err
		}
		report.Rearmed += candidateReport.Rearmed
		report.TechnicalReplays += candidateReport.TechnicalReplays
		report.Paused += candidateReport.Paused
		report.Changed += candidateReport.Changed
	}
	return report, nil
}

func (recoverer *Recoverer) recoverCandidate(
	ctx context.Context,
	candidate RecoveryCandidate,
	currentStamp authoritydomain.Stamp,
) (RecoveryReport, error) {
	if candidate.State == domain.GameStatePaused {
		return RecoveryReport{Paused: 1}, nil
	}
	if candidate.BoundAuthority == currentStamp {
		if err := recoverer.timers.RearmDeadline(ctx, DeadlineArm{
			Scope:     candidate.Scope,
			RosterID:  candidate.RosterID,
			AttemptNo: candidate.AttemptNo,
			Authority: candidate.BoundAuthority,
			Deadline:  candidate.Deadline,
		}); err != nil {
			return RecoveryReport{}, fmt.Errorf("execution recovery - rearm deadline: %w", err)
		}
		return RecoveryReport{Rearmed: 1}, nil
	}
	changed, err := recoverer.replayer.ReplayEpoch(ctx, *candidate.EpochReplay)
	if err != nil {
		return RecoveryReport{}, fmt.Errorf("execution recovery - replay epoch: %w", err)
	}
	report := RecoveryReport{TechnicalReplays: 1}
	if changed {
		report.Changed = 1
	}
	return report, nil
}
