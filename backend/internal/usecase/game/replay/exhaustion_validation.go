package replay

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

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
	want, changed, err := seriesdomain.ResolveCompetitive(
		r.PreviousSeries,
		seriesdomain.ResolutionCommand{
			Route: seriesdomain.CompetitiveSeriesRouteReplayExhausted,
		},
	)
	if err != nil || !changed || !replaySeriesExecutionsEqual(want, r.Series) {
		return replayReserveExhaustionError("Series pause does not match replay exhaustion")
	}
	return nil
}

func validReplayReserveExhaustionHeader(record ReplayReserveExhaustion) bool {
	return record.Scope.IsValid() && record.CommandID != uuid.Nil &&
		record.ExpectedAuthorityRevision >= 1 && !record.ClosureRevisionID.IsZero() &&
		record.FailedAttemptCommandID != uuid.Nil && record.AssignmentAttemptID != uuid.Nil &&
		record.GameID != uuid.Nil && record.ActiveSnapshotID != uuid.Nil &&
		record.ReservePosition == domain.AssignmentReserveCount+1 && record.Category.IsValid()
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
		Scope:          authority.Scope,
		FailedAttempt:  authority.FailedAttempt,
		OldWaveClosure: authority.OldWaveClosure,
	}); err != nil {
		return replayReserveExhaustionError("source evidence does not align: %v", err)
	}
	category := authority.FailedAttempt.Failure.CategoryCutoff
	if err := authority.ReserveChain.Validate(category); err != nil {
		return err
	}
	if authority.ReserveChain.AssignmentID != authority.Scope.AssignmentID ||
		authority.ReserveChain.ActiveIndex != domain.AssignmentReserveCount ||
		authority.ReserveChain.ActiveIndex != len(authority.ReserveChain.Snapshots)-1 ||
		authority.ReserveChain.Snapshots[authority.ReserveChain.ActiveIndex].SnapshotID !=
			authority.FailedAttempt.ActiveSnapshotID {
		return replayReserveExhaustionError("reserve chain is not fully consumed")
	}
	if authority.Current != nil &&
		!replayReserveExhaustionMatchesAuthority(*authority.Current, authority) {
		return replayReserveExhaustionError("invalid current exhaustion record")
	}
	return nil
}

func validateReplayReserveExhaustionWave(record ReplayReserveExhaustion) error {
	wave := record.OldWave
	if wave.Validate() != nil || wave.ID != record.Scope.OldWaveID ||
		wave.TournamentID != record.Scope.TournamentID ||
		wave.State != domain.WaveStateCompleted || wave.RevisionID != record.ClosureRevisionID {
		return replayReserveExhaustionError("old Wave is not the committed closure")
	}
	return nil
}

func validateReplayReserveExhaustionSourceSeries(record ReplayReserveExhaustion) error {
	series := record.PreviousSeries.Series
	if record.PreviousSeries.Validate() != nil || series.ID != record.Scope.SeriesID ||
		series.TournamentID != record.Scope.TournamentID ||
		series.State != domain.SeriesStateReplayRequired || series.WinnerID != nil ||
		series.CurrentResultRevisionID != nil || len(series.Slots) == 0 {
		return replayReserveExhaustionError("invalid replay-required Series source")
	}
	slot := series.Slots[len(series.Slots)-1]
	if slot.ID != record.Scope.SlotID || slot.Category != record.Category ||
		len(slot.Attempts) == 0 {
		return replayReserveExhaustionError("Series lost the exhausted slot or category")
	}
	currentGame := slot.Attempts[len(slot.Attempts)-1]
	if currentGame.ID != record.GameID || currentGame.State != domain.GameStateVoid ||
		currentGame.WinnerID != nil {
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
		replaySeriesExecutionsEqual(record.PreviousSeries, authority.FailedAttempt.Series) &&
		replayWavesEqual(record.OldWave, authority.OldWaveClosure.Wave)
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

func replayReserveExhaustionError(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %s",
		ErrInvalidReplayReserveExhaustion,
		fmt.Sprintf(format, arguments...),
	)
}
