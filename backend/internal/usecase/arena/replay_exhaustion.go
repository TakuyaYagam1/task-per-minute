package arena

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const replayReserveExhaustionAttempts = 2

var (
	ErrInvalidReplayReserveExhaustion  = errors.New("invalid arena replay reserve exhaustion")
	ErrReplayReserveExhaustionConflict = errors.New("arena replay reserve exhaustion conflict")
	ErrReplayReserveExhaustionReuse    = errors.New("arena replay reserve exhaustion command was reused")
)

type ReplayReserveExhaustionCommand struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedClosureRevisionID domain.ArenaWaveRevisionID
	ExpectedActiveSnapshotID  uuid.UUID
}

type ReplayReserveExhaustionAuthority struct {
	Scope          ReplayReplacementScope
	Revision       int64
	FailedAttempt  FailedAttemptRecord
	OldWaveClosure OldWaveClosure
	ReserveChain   ReplayReserveChain
	Current        *ReplayReserveExhaustion
}

type ReplayReserveExhaustion struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	ClosureRevisionID         domain.ArenaWaveRevisionID
	FailedAttemptCommandID    uuid.UUID
	AssignmentAttemptID       uuid.UUID
	GameID                    uuid.UUID
	ActiveSnapshotID          uuid.UUID
	ReservePosition           int
	Category                  domain.Category
	PreviousSeries            SeriesExecution
	Series                    SeriesExecution
	OldWave                   domain.ArenaWave
}

// ReplayReserveExhaustionRepository owns one transaction that revalidates the
// failed attempt, completed old Wave and fully consumed reserve chain before
// pausing the replay-required Series without creating a replacement Wave.
type ReplayReserveExhaustionRepository interface {
	LoadReplayReserveExhaustionAuthority(
		ctx context.Context,
		scope ReplayReplacementScope,
	) (ReplayReserveExhaustionAuthority, error)
	CommitReplayReserveExhaustion(
		ctx context.Context,
		record ReplayReserveExhaustion,
	) (*ReplayReserveExhaustion, bool, error)
}

type ReplayReserveExhaustionUseCase struct {
	repository ReplayReserveExhaustionRepository
}

func NewReplayReserveExhaustionUseCase(
	repository ReplayReserveExhaustionRepository,
) *ReplayReserveExhaustionUseCase {
	return &ReplayReserveExhaustionUseCase{repository: repository}
}

func (u *ReplayReserveExhaustionUseCase) Pause(
	ctx context.Context,
	command ReplayReserveExhaustionCommand,
) (*ReplayReserveExhaustion, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateReplayReserveExhaustionCommand(command); err != nil {
		return nil, false, err
	}
	for range replayReserveExhaustionAttempts {
		record, changed, retry, err := u.pauseAttempt(ctx, command)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrReplayReserveExhaustionConflict
}

func (u *ReplayReserveExhaustionUseCase) pauseAttempt(
	ctx context.Context,
	command ReplayReserveExhaustionCommand,
) (*ReplayReserveExhaustion, bool, bool, error) {
	authority, err := u.repository.LoadReplayReserveExhaustionAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("ReplayReserveExhaustionUseCase - load authority: %w", err)
	}
	if err := validateReplayReserveExhaustionAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != command.Scope {
		return nil, false, false, replayReserveExhaustionError("authority scope does not match command")
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileReplayReserveExhaustion(*authority.Current, command)
		return current, false, false, reconcileErr
	}
	record, err := buildReplayReserveExhaustion(command, authority)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitReplayReserveExhaustion(ctx, record)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("ReplayReserveExhaustionUseCase - commit pause: %w", err)
	}
	if !validCommittedReplayReserveExhaustion(committed, record, command, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneReplayReserveExhaustion(*committed)
	return &result, changed, false, nil
}

func (r ReplayReserveExhaustion) Validate() error {
	if !validReplayReserveExhaustionHeader(r) {
		return replayReserveExhaustionError("invalid record identity or reserve position")
	}
	if err := validateReplayReserveExhaustionWave(r); err != nil {
		return err
	}
	if err := validateReplayReserveExhaustionSourceSeries(r); err != nil {
		return err
	}
	want, changed, err := ResolveCompetitiveSeries(
		r.PreviousSeries,
		CompetitiveSeriesResolutionCommand{Route: CompetitiveSeriesRouteReplayExhausted},
	)
	if err != nil || !changed || !forfeitSeriesHeadsEqual(want, r.Series) {
		return replayReserveExhaustionError("Series pause does not match replay exhaustion")
	}
	return nil
}

func validReplayReserveExhaustionHeader(record ReplayReserveExhaustion) bool {
	return record.Scope.IsValid() && record.CommandID != uuid.Nil &&
		record.ExpectedAuthorityRevision >= 1 && !record.ClosureRevisionID.IsZero() &&
		record.FailedAttemptCommandID != uuid.Nil && record.AssignmentAttemptID != uuid.Nil &&
		record.GameID != uuid.Nil && record.ActiveSnapshotID != uuid.Nil &&
		record.ReservePosition == domain.ArenaAssignmentReserveCount+1 && record.Category.IsValid()
}

func validateReplayReserveExhaustionCommand(command ReplayReserveExhaustionCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil ||
		command.ExpectedClosureRevisionID.IsZero() || command.ExpectedActiveSnapshotID == uuid.Nil {
		return replayReserveExhaustionError("invalid command identity or expected evidence")
	}
	return nil
}

func validateReplayReserveExhaustionAuthority(authority ReplayReserveExhaustionAuthority) error {
	if !authority.Scope.IsValid() || authority.Revision < 1 ||
		authority.FailedAttempt.Validate() != nil || authority.OldWaveClosure.Validate() != nil {
		return replayReserveExhaustionError("invalid authority header or retained evidence")
	}
	if err := validateReplayReplacementAuthorityLinks(ReplayReplacementAuthority{
		Scope: authority.Scope, FailedAttempt: authority.FailedAttempt,
		OldWaveClosure: authority.OldWaveClosure,
	}); err != nil {
		return replayReserveExhaustionError("source evidence does not align: %v", err)
	}
	category := authority.FailedAttempt.Failure.CategoryCutoff
	if err := authority.ReserveChain.Validate(category); err != nil {
		return err
	}
	if authority.ReserveChain.AssignmentID != authority.Scope.AssignmentID ||
		authority.ReserveChain.ActiveIndex != domain.ArenaAssignmentReserveCount ||
		authority.ReserveChain.ActiveIndex != len(authority.ReserveChain.Snapshots)-1 ||
		authority.ReserveChain.Snapshots[authority.ReserveChain.ActiveIndex].SnapshotID !=
			authority.FailedAttempt.ActiveSnapshotID {
		return replayReserveExhaustionError("reserve chain is not fully consumed")
	}
	if authority.Current != nil && !replayReserveExhaustionMatchesAuthority(*authority.Current, authority) {
		return replayReserveExhaustionError("invalid current exhaustion record")
	}
	return nil
}

func buildReplayReserveExhaustion(
	command ReplayReserveExhaustionCommand,
	authority ReplayReserveExhaustionAuthority,
) (ReplayReserveExhaustion, error) {
	if authority.OldWaveClosure.Wave.RevisionID != command.ExpectedClosureRevisionID ||
		authority.FailedAttempt.ActiveSnapshotID != command.ExpectedActiveSnapshotID {
		return ReplayReserveExhaustion{}, ErrReplayReserveExhaustionConflict
	}
	paused, changed, err := ResolveCompetitiveSeries(
		authority.FailedAttempt.Series,
		CompetitiveSeriesResolutionCommand{Route: CompetitiveSeriesRouteReplayExhausted},
	)
	if err != nil || !changed {
		return ReplayReserveExhaustion{}, replayReserveExhaustionError("pause Series: %v", err)
	}
	record := ReplayReserveExhaustion{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision,
		ClosureRevisionID:         command.ExpectedClosureRevisionID,
		FailedAttemptCommandID:    authority.FailedAttempt.CommandID,
		AssignmentAttemptID:       authority.FailedAttempt.Scope.AssignmentAttemptID,
		GameID:                    authority.FailedAttempt.Scope.GameID,
		ActiveSnapshotID:          authority.FailedAttempt.ActiveSnapshotID,
		ReservePosition:           authority.ReserveChain.ActiveIndex + 1,
		Category:                  authority.FailedAttempt.Failure.CategoryCutoff,
		PreviousSeries:            cloneSeriesExecution(authority.FailedAttempt.Series),
		Series:                    paused,
		OldWave:                   cloneArenaWaveExecution(authority.OldWaveClosure.Wave),
	}
	if err := record.Validate(); err != nil {
		return ReplayReserveExhaustion{}, err
	}
	return cloneReplayReserveExhaustion(record), nil
}

func validateReplayReserveExhaustionWave(record ReplayReserveExhaustion) error {
	wave := record.OldWave
	if wave.Validate() != nil || wave.ID != record.Scope.OldWaveID ||
		wave.TournamentID != record.Scope.TournamentID ||
		wave.State != domain.ArenaWaveStateCompleted || wave.RevisionID != record.ClosureRevisionID {
		return replayReserveExhaustionError("old Wave is not the committed closure")
	}
	return nil
}

func validateReplayReserveExhaustionSourceSeries(record ReplayReserveExhaustion) error {
	series := record.PreviousSeries.Series
	if record.PreviousSeries.Validate() != nil || series.ID != record.Scope.SeriesID ||
		series.TournamentID != record.Scope.TournamentID ||
		series.State != domain.ArenaSeriesStateReplayRequired || series.WinnerID != nil ||
		series.CurrentResultRevisionID != nil || len(series.Slots) == 0 {
		return replayReserveExhaustionError("invalid replay-required Series source")
	}
	slot := series.Slots[len(series.Slots)-1]
	if slot.ID != record.Scope.SlotID || slot.Category != record.Category || len(slot.Attempts) == 0 {
		return replayReserveExhaustionError("Series lost the exhausted slot or category")
	}
	game := slot.Attempts[len(slot.Attempts)-1]
	if game.ID != record.GameID || game.State != domain.ArenaGameStateVoid || game.WinnerID != nil {
		return replayReserveExhaustionError("Series lost the failed final reserve")
	}
	return nil
}

func replayReserveExhaustionMatchesAuthority(
	record ReplayReserveExhaustion,
	authority ReplayReserveExhaustionAuthority,
) bool {
	return record.Validate() == nil && record.Scope == authority.Scope &&
		record.ClosureRevisionID == authority.OldWaveClosure.Wave.RevisionID &&
		record.FailedAttemptCommandID == authority.FailedAttempt.CommandID &&
		record.ActiveSnapshotID == authority.FailedAttempt.ActiveSnapshotID &&
		forfeitSeriesHeadsEqual(record.PreviousSeries, authority.FailedAttempt.Series) &&
		arenaWavesEqual(record.OldWave, authority.OldWaveClosure.Wave)
}

func reconcileReplayReserveExhaustion(
	record ReplayReserveExhaustion,
	command ReplayReserveExhaustionCommand,
) (*ReplayReserveExhaustion, error) {
	if record.Validate() != nil || record.Scope != command.Scope ||
		record.CommandID != command.CommandID ||
		record.ClosureRevisionID != command.ExpectedClosureRevisionID ||
		record.ActiveSnapshotID != command.ExpectedActiveSnapshotID {
		return nil, ErrReplayReserveExhaustionReuse
	}
	clone := cloneReplayReserveExhaustion(record)
	return &clone, nil
}

func validCommittedReplayReserveExhaustion(
	committed *ReplayReserveExhaustion,
	proposed ReplayReserveExhaustion,
	command ReplayReserveExhaustionCommand,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil || committed.Scope != proposed.Scope {
		return false
	}
	if _, err := reconcileReplayReserveExhaustion(*committed, command); err != nil {
		return false
	}
	return !changed || replayReserveExhaustionsEqual(*committed, proposed)
}

func replayReserveExhaustionsEqual(first, second ReplayReserveExhaustion) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		first.ClosureRevisionID == second.ClosureRevisionID &&
		first.FailedAttemptCommandID == second.FailedAttemptCommandID &&
		first.AssignmentAttemptID == second.AssignmentAttemptID && first.GameID == second.GameID &&
		first.ActiveSnapshotID == second.ActiveSnapshotID &&
		first.ReservePosition == second.ReservePosition && first.Category == second.Category &&
		forfeitSeriesHeadsEqual(first.PreviousSeries, second.PreviousSeries) &&
		forfeitSeriesHeadsEqual(first.Series, second.Series) &&
		arenaWavesEqual(first.OldWave, second.OldWave)
}

func cloneReplayReserveExhaustion(record ReplayReserveExhaustion) ReplayReserveExhaustion {
	clone := record
	clone.PreviousSeries = cloneSeriesExecution(record.PreviousSeries)
	clone.Series = cloneSeriesExecution(record.Series)
	clone.OldWave = cloneArenaWaveExecution(record.OldWave)
	return clone
}

func replayReserveExhaustionError(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %s",
		ErrInvalidReplayReserveExhaustion,
		fmt.Sprintf(format, arguments...),
	)
}
