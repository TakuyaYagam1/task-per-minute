package game

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (r ReplayReplacement) Validate() error {
	if !validReplayReplacementHeader(r) {
		return replayReplacementError("invalid replacement identity or reserve")
	}
	if err := validateReplayReplacementSlot(r); err != nil {
		return err
	}
	if err := validateReplayReplacementWave(r); err != nil {
		return err
	}
	return validateFreshReplayIdentities(r)
}

func validReplayReplacementHeader(replacement ReplayReplacement) bool {
	return replacement.Scope.IsValid() && replacement.CommandID != uuid.Nil &&
		replacement.ExpectedAuthorityRevision >= 1 && !replacement.ClosureRevisionID.IsZero() &&
		replacement.FromSnapshotID != uuid.Nil && replacement.AssignmentAttemptID != uuid.Nil &&
		replacement.ReservePosition >= 2 &&
		replacement.ReservePosition <= operatorReplayReserveSnapshots &&
		replacement.Category.IsValid() && replayValidServerTime(replacement.OpenedAt) &&
		replacement.Snapshot.Validate() == nil &&
		replacement.Snapshot.Category == replacement.Category &&
		replacement.Snapshot.SnapshotID != replacement.FromSnapshotID
}

func (s ReplayReplacementScope) IsValid() bool {
	return s.TournamentID != uuid.Nil && s.OldWaveID != uuid.Nil && s.SeriesID != uuid.Nil &&
		s.SlotID != uuid.Nil && s.AssignmentID != uuid.Nil
}

func (c ReplayReserveChain) Validate(category domain.Category) error {
	if c.AssignmentID == uuid.Nil || c.ActiveIndex < 0 ||
		c.ActiveIndex >= len(c.Snapshots) ||
		!validReplayReserveSnapshotCount(len(c.Snapshots)) || !category.IsValid() {
		return replayReplacementError("invalid reserve chain identity or position")
	}
	taskIDs := make(map[uuid.UUID]struct{}, len(c.Snapshots))
	snapshotIDs := make(map[uuid.UUID]struct{}, len(c.Snapshots))
	for _, snapshot := range c.Snapshots {
		if snapshot.Validate() != nil || snapshot.Category != category {
			return replayReplacementError("reserve chain changed category or content")
		}
		if _, duplicate := taskIDs[snapshot.TaskID]; duplicate {
			return replayReplacementError("reserve chain repeats a task")
		}
		if _, duplicate := snapshotIDs[snapshot.SnapshotID]; duplicate {
			return replayReplacementError("reserve chain repeats a snapshot")
		}
		taskIDs[snapshot.TaskID] = struct{}{}
		snapshotIDs[snapshot.SnapshotID] = struct{}{}
	}
	return nil
}

func validReplayReserveSnapshotCount(snapshotCount int) bool {
	return snapshotCount == baseReplayReserveSnapshots ||
		snapshotCount == operatorReplayReserveSnapshots
}

func validateReplayReplacementCommand(command ReplayReplacementCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil ||
		command.ExpectedClosureRevisionID.IsZero() || command.WaveRevisionID.IsZero() ||
		command.ReadyWindowRevisionID.IsZero() {
		return replayReplacementError("invalid command identity or revision")
	}
	identities := []uuid.UUID{
		command.CommandID, command.AssignmentAttemptID, command.GameID, command.WaveID,
		command.WaveRevisionID.UUID(), command.ReadyWindowID,
		command.ReadyWindowRevisionID.UUID(),
	}
	if !replayUniqueNonZeroUUIDs(identities) {
		return replayReplacementError("missing or duplicate replacement identity")
	}
	return nil
}

func validateReplayReplacementAuthority(authority ReplayReplacementAuthority) error {
	if !authority.Scope.IsValid() || authority.Revision < 1 ||
		authority.FailedAttempt.Validate() != nil || authority.OldWaveClosure.Validate() != nil {
		return replayReplacementError("invalid authority header or retained evidence")
	}
	if err := validateReplayReplacementAuthorityLinks(authority); err != nil {
		return err
	}
	if err := authority.ReserveChain.Validate(authority.FailedAttempt.Failure.CategoryCutoff); err != nil {
		return err
	}
	if authority.ReserveChain.AssignmentID != authority.Scope.AssignmentID {
		return replayReplacementError("reserve chain belongs to another assignment")
	}
	if err := validateOperatorReserveChainPosition(authority); err != nil {
		return err
	}
	if !replayParticipantsMatchSeries(authority.ParticipantIDs, authority.FailedAttempt.Series.Series) ||
		!oldWaveContainsReplayParticipants(authority.OldWaveClosure.Wave, authority.ParticipantIDs) {
		return replayReplacementError("replacement participants do not match old evidence")
	}
	activeSnapshotID := authority.ReserveChain.Snapshots[authority.ReserveChain.ActiveIndex].SnapshotID
	if authority.Current == nil {
		if activeSnapshotID != authority.FailedAttempt.ActiveSnapshotID {
			return replayReplacementError("reserve head does not match failed attempt")
		}
		return nil
	}
	if authority.Current.Validate() != nil || authority.Current.Scope != authority.Scope ||
		activeSnapshotID != authority.Current.Snapshot.SnapshotID {
		return replayReplacementError("invalid current replacement")
	}
	return nil
}

func validateOperatorReserveChainPosition(authority ReplayReplacementAuthority) error {
	if len(authority.ReserveChain.Snapshots) != operatorReplayReserveSnapshots {
		return nil
	}

	expectedActiveIndex := domain.AssignmentReserveCount
	if authority.Current != nil {
		expectedActiveIndex++
	}
	if authority.ReserveChain.ActiveIndex != expectedActiveIndex {
		return replayReplacementError("operator reserve chain has an invalid active position")
	}
	return nil
}

func validateReplayReplacementAuthorityLinks(authority ReplayReplacementAuthority) error {
	failed := authority.FailedAttempt
	closure := authority.OldWaveClosure
	if failed.Scope.TournamentID != authority.Scope.TournamentID ||
		failed.Scope.WaveID != authority.Scope.OldWaveID ||
		failed.Scope.SeriesID != authority.Scope.SeriesID ||
		failed.Scope.SlotID != authority.Scope.SlotID ||
		failed.Scope.AssignmentID != authority.Scope.AssignmentID ||
		closure.Scope.TournamentID != authority.Scope.TournamentID ||
		closure.Scope.WaveID != authority.Scope.OldWaveID ||
		closure.Wave.State != domain.WaveStateCompleted ||
		!closureContainsFailedRoute(closure, failed) {
		return replayReplacementError("failed attempt and closure authority do not align")
	}
	return nil
}

func closureContainsFailedRoute(
	closure Closure,
	failed AttemptRecord,
) bool {
	for _, child := range closure.Children {
		if child.SeriesID == failed.Scope.SeriesID && child.SlotID == failed.Scope.SlotID &&
			child.GameID == failed.Scope.GameID && child.State == domain.GameStateVoid &&
			child.RouteID == failed.WaveRoute.ID {
			return true
		}
	}
	return false
}

func replayParticipantsMatchSeries(
	participantIDs [2]uuid.UUID,
	series domain.Series,
) bool {
	return participantIDs[0] != uuid.Nil && participantIDs[1] != uuid.Nil &&
		participantIDs[0] != participantIDs[1] &&
		((participantIDs[0] == series.FirstParticipantID &&
			participantIDs[1] == series.SecondParticipantID) ||
			(participantIDs[1] == series.FirstParticipantID &&
				participantIDs[0] == series.SecondParticipantID))
}

func oldWaveContainsReplayParticipants(
	wave domain.Wave,
	participantIDs [2]uuid.UUID,
) bool {
	found := 0
	for _, member := range wave.Members {
		if member.ParticipantID == participantIDs[0] || member.ParticipantID == participantIDs[1] {
			found++
		}
	}
	return found == len(participantIDs)
}

func replaySourceSlot(failed AttemptRecord) (domain.GameSlot, error) {
	series := failed.Series.Series
	if len(series.Slots) == 0 {
		return domain.GameSlot{}, replayReplacementError("failed Series has no slot")
	}
	slot := replayCloneGameSlot(series.Slots[len(series.Slots)-1])
	if slot.ID != failed.Scope.SlotID || len(slot.Attempts) == 0 ||
		slot.Attempts[len(slot.Attempts)-1].ID != failed.Scope.GameID ||
		slot.Attempts[len(slot.Attempts)-1].State != domain.GameStateVoid {
		return domain.GameSlot{}, replayReplacementError("failed attempt is not the slot head")
	}
	return slot, nil
}

func freshReplayCommandIdentities(
	command ReplayReplacementCommand,
	authority ReplayReplacementAuthority,
) bool {
	old := map[uuid.UUID]struct{}{
		authority.Scope.TournamentID:                      {},
		authority.Scope.SeriesID:                          {},
		authority.Scope.SlotID:                            {},
		authority.Scope.AssignmentID:                      {},
		authority.FailedAttempt.Scope.AssignmentAttemptID: {},
		authority.FailedAttempt.Scope.GameID:              {},
		authority.FailedAttempt.Scope.WaveID:              {},
		authority.FailedAttempt.WaveRoute.ID:              {},
		authority.OldWaveClosure.Wave.RevisionID.UUID():   {},
	}
	if authority.OldWaveClosure.Wave.ReadyWindow != nil {
		old[authority.OldWaveClosure.Wave.ReadyWindow.ID] = struct{}{}
		old[authority.OldWaveClosure.Wave.ReadyWindow.RevisionID.UUID()] = struct{}{}
	}
	for _, identity := range []uuid.UUID{
		command.CommandID, command.AssignmentAttemptID, command.GameID, command.WaveID,
		command.WaveRevisionID.UUID(), command.ReadyWindowID,
		command.ReadyWindowRevisionID.UUID(),
	} {
		if _, reused := old[identity]; reused {
			return false
		}
	}
	return true
}

func validateReplayReplacementSlot(replacement ReplayReplacement) error {
	if replacement.Slot.Validate() != nil || replacement.Slot.ID != replacement.Scope.SlotID ||
		replacement.Slot.SeriesID != replacement.Scope.SeriesID ||
		replacement.Slot.Category != replacement.Category || len(replacement.Slot.Attempts) < 2 {
		return replayReplacementError("invalid retained slot or category")
	}
	last := replacement.Slot.Attempts[len(replacement.Slot.Attempts)-1]
	previous := replacement.Slot.Attempts[len(replacement.Slot.Attempts)-2]
	if !replayGamesEqual(last, replacement.Game) ||
		last.State != domain.GameStatePlanned || last.WinnerID != nil ||
		last.ResultRevisionID != nil || previous.State != domain.GameStateVoid ||
		last.AttemptNo != previous.AttemptNo+1 {
		return replayReplacementError("replacement Game resets or skips the attempt counter")
	}
	return nil
}

func validateReplayReplacementWave(replacement ReplayReplacement) error {
	wave := replacement.Wave
	if wave.Validate() != nil || wave.ID == replacement.Scope.OldWaveID ||
		wave.TournamentID != replacement.Scope.TournamentID ||
		wave.State != domain.WaveStateReadyWindowOpen || wave.ReadyWindow == nil ||
		wave.ReadyWindow.State != domain.ReadyWindowStateOpen ||
		!wave.ReadyWindow.OpenedAt.Equal(replacement.OpenedAt) ||
		!domain.IsValidReadyWindowInterval(replacement.OpenedAt, wave.ReadyWindow.Deadline) ||
		len(wave.Members) != 2 || wave.Members[0].Ready || wave.Members[1].Ready {
		return replayReplacementError("invalid replacement Wave readiness")
	}
	return nil
}

func validateFreshReplayIdentities(replacement ReplayReplacement) error {
	wave := replacement.Wave
	identities := []uuid.UUID{
		replacement.CommandID, replacement.AssignmentAttemptID, replacement.Game.ID,
		wave.ID, wave.RevisionID.UUID(), wave.ReadyWindow.ID,
		wave.ReadyWindow.RevisionID.UUID(),
	}
	if !replayUniqueNonZeroUUIDs(identities) {
		return replayReplacementError("replacement identities are missing, reused, or duplicated")
	}
	reserved := map[uuid.UUID]struct{}{
		replacement.Scope.TournamentID: {}, replacement.Scope.OldWaveID: {},
		replacement.Scope.SeriesID: {}, replacement.Scope.SlotID: {},
		replacement.Scope.AssignmentID: {},
	}
	for _, identity := range identities {
		if _, reused := reserved[identity]; reused {
			return replayReplacementError("replacement reuses a stable identity")
		}
	}
	return nil
}

func reconcileReplayReplacement(
	replacement ReplayReplacement,
	command ReplayReplacementCommand,
) (*ReplayReplacement, error) {
	if replacement.Validate() != nil || replacement.Scope != command.Scope ||
		replacement.CommandID != command.CommandID ||
		replacement.ClosureRevisionID != command.ExpectedClosureRevisionID ||
		replacement.AssignmentAttemptID != command.AssignmentAttemptID ||
		replacement.Game.ID != command.GameID || replacement.Wave.ID != command.WaveID ||
		replacement.Wave.RevisionID != command.WaveRevisionID ||
		replacement.Wave.ReadyWindow == nil ||
		replacement.Wave.ReadyWindow.ID != command.ReadyWindowID ||
		replacement.Wave.ReadyWindow.RevisionID != command.ReadyWindowRevisionID {
		return nil, ErrReplayReplacementReuse
	}
	clone := cloneReplayReplacement(replacement)
	return &clone, nil
}

func validCommittedReplayReplacement(
	committed *ReplayReplacement,
	proposed ReplayReplacement,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil || committed.Scope != proposed.Scope {
		return false
	}
	if !changed {
		return true
	}
	return replayReplacementsEqual(*committed, proposed)
}

func replayReplacementError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidReplayReplacement, fmt.Sprintf(format, arguments...))
}
