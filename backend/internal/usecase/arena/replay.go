package arena

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const replayReplacementAttempts = 2

var (
	ErrInvalidReplayReplacement  = errors.New("invalid arena replay replacement")
	ErrReplayReplacementConflict = errors.New("arena replay replacement conflict")
	ErrReplayReplacementReuse    = errors.New("arena replay replacement command was reused")
	ErrReplayReservesExhausted   = errors.New("arena replay reserves exhausted")
)

type ReplayReplacementScope struct {
	TournamentID uuid.UUID
	OldWaveID    uuid.UUID
	SeriesID     uuid.UUID
	SlotID       uuid.UUID
	AssignmentID uuid.UUID
}

type ReplayReserveChain struct {
	AssignmentID uuid.UUID
	ActiveIndex  int
	Snapshots    []domain.ArenaTaskSnapshot
}

type ReplayReplacementAuthority struct {
	Scope          ReplayReplacementScope
	Revision       int64
	FailedAttempt  FailedAttemptRecord
	OldWaveClosure OldWaveClosure
	ReserveChain   ReplayReserveChain
	ParticipantIDs [2]uuid.UUID
	Current        *ReplayReplacement
}

type ReplayReplacementCommand struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedClosureRevisionID domain.ArenaWaveRevisionID
	AssignmentAttemptID       uuid.UUID
	GameID                    uuid.UUID
	WaveID                    uuid.UUID
	WaveRevisionID            domain.ArenaWaveRevisionID
	ReadyWindowID             uuid.UUID
	ReadyWindowRevisionID     domain.ArenaReadyWindowRevisionID
}

type ReplayReplacement struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	ClosureRevisionID         domain.ArenaWaveRevisionID
	FromSnapshotID            uuid.UUID
	AssignmentAttemptID       uuid.UUID
	ReservePosition           int
	Snapshot                  domain.ArenaTaskSnapshot
	Category                  domain.Category
	Slot                      domain.ArenaGameSlot
	Game                      domain.ArenaGame
	Wave                      domain.ArenaWave
	OpenedAt                  time.Time
}

// ReplayReplacementRepository is called only after the failed-attempt and old
// Wave closure commits. Its transaction revalidates both records, advances one
// planned reserve, and writes fresh assignment-attempt, Game, Wave and ready
// window identities without changing the Series slot or score.
type ReplayReplacementRepository interface {
	LoadReplayReplacementAuthority(
		ctx context.Context,
		scope ReplayReplacementScope,
	) (ReplayReplacementAuthority, error)
	CommitReplayReplacement(
		ctx context.Context,
		replacement ReplayReplacement,
	) (*ReplayReplacement, bool, error)
}

type ReplayReplacementUseCase struct {
	repository ReplayReplacementRepository
	clock      Clock
}

func NewReplayReplacementUseCase(
	repository ReplayReplacementRepository,
	clock Clock,
) *ReplayReplacementUseCase {
	return &ReplayReplacementUseCase{repository: repository, clock: clock}
}

func (u *ReplayReplacementUseCase) Replace(
	ctx context.Context,
	command ReplayReplacementCommand,
) (*ReplayReplacement, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateReplayReplacementCommand(command); err != nil {
		return nil, false, err
	}
	openedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(openedAt) {
		return nil, false, domain.ErrValidation
	}
	for range replayReplacementAttempts {
		replacement, changed, retry, err := u.replaceAttempt(ctx, command, openedAt)
		if retry {
			continue
		}
		return replacement, changed, err
	}
	return nil, false, ErrReplayReplacementConflict
}

func (u *ReplayReplacementUseCase) replaceAttempt(
	ctx context.Context,
	command ReplayReplacementCommand,
	openedAt time.Time,
) (*ReplayReplacement, bool, bool, error) {
	authority, err := u.repository.LoadReplayReplacementAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("ReplayReplacementUseCase - load authority: %w", err)
	}
	if err := validateReplayReplacementAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != command.Scope {
		return nil, false, false, replayReplacementError("authority scope does not match command")
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileReplayReplacement(*authority.Current, command)
		return current, false, false, reconcileErr
	}
	if authority.ReserveChain.ActiveIndex+1 >= len(authority.ReserveChain.Snapshots) {
		return nil, false, false, ErrReplayReservesExhausted
	}
	replacement, err := buildReplayReplacement(command, authority, openedAt)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitReplayReplacement(ctx, replacement)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("ReplayReplacementUseCase - commit replacement: %w", err)
	}
	if !validCommittedReplayReplacement(committed, replacement, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneReplayReplacement(*committed)
	return &result, changed, false, nil
}

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
		replacement.ReservePosition <= domain.ArenaAssignmentReserveCount+1 &&
		replacement.Category.IsValid() && validArenaServerTime(replacement.OpenedAt) &&
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
		len(c.Snapshots) != domain.ArenaAssignmentReserveCount+1 || !category.IsValid() {
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
	if !uniqueNonZeroUUIDs(identities) {
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
		closure.Wave.State != domain.ArenaWaveStateCompleted ||
		!closureContainsFailedRoute(closure, failed) {
		return replayReplacementError("failed attempt and closure authority do not align")
	}
	return nil
}

func closureContainsFailedRoute(closure OldWaveClosure, failed FailedAttemptRecord) bool {
	for _, child := range closure.Children {
		if child.SeriesID == failed.Scope.SeriesID && child.SlotID == failed.Scope.SlotID &&
			child.GameID == failed.Scope.GameID && child.State == domain.ArenaGameStateVoid &&
			child.RouteID == failed.WaveRoute.ID {
			return true
		}
	}
	return false
}

func replayParticipantsMatchSeries(
	participantIDs [2]uuid.UUID,
	series domain.ArenaSeries,
) bool {
	return participantIDs[0] != uuid.Nil && participantIDs[1] != uuid.Nil &&
		participantIDs[0] != participantIDs[1] &&
		((participantIDs[0] == series.FirstParticipantID &&
			participantIDs[1] == series.SecondParticipantID) ||
			(participantIDs[1] == series.FirstParticipantID &&
				participantIDs[0] == series.SecondParticipantID))
}

func oldWaveContainsReplayParticipants(
	wave domain.ArenaWave,
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

func buildReplayReplacement(
	command ReplayReplacementCommand,
	authority ReplayReplacementAuthority,
	openedAt time.Time,
) (ReplayReplacement, error) {
	if authority.OldWaveClosure.Wave.RevisionID != command.ExpectedClosureRevisionID {
		return ReplayReplacement{}, ErrReplayReplacementConflict
	}
	if !freshReplayCommandIdentities(command, authority) {
		return ReplayReplacement{}, replayReplacementError("replacement reuses old identity")
	}
	nextIndex := authority.ReserveChain.ActiveIndex + 1
	if nextIndex >= len(authority.ReserveChain.Snapshots) {
		return ReplayReplacement{}, ErrReplayReservesExhausted
	}
	snapshot := cloneTaskSnapshot(authority.ReserveChain.Snapshots[nextIndex])
	slot, err := replaySourceSlot(authority.FailedAttempt)
	if err != nil {
		return ReplayReplacement{}, err
	}
	openedSlot, changed, err := OpenGameSlotAttempt(slot, command.GameID)
	if err != nil || !changed {
		return ReplayReplacement{}, replayReplacementError("open Game attempt: %v", err)
	}
	game := cloneArenaGame(openedSlot.Attempts[len(openedSlot.Attempts)-1])
	wave := domain.ArenaWave{
		ID: command.WaveID, TournamentID: command.Scope.TournamentID,
		RevisionID: command.WaveRevisionID, State: domain.ArenaWaveStatePlanned,
		Members: []domain.ArenaWaveMember{
			{ParticipantID: authority.ParticipantIDs[0]},
			{ParticipantID: authority.ParticipantIDs[1]},
		},
	}
	if err := wave.OpenReadyWindow(
		command.ReadyWindowID,
		command.ReadyWindowRevisionID,
		openedAt,
		openedAt.Add(readyWindowDuration),
	); err != nil {
		return ReplayReplacement{}, replayReplacementError("open ready window: %v", err)
	}
	replacement := ReplayReplacement{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision,
		ClosureRevisionID:         command.ExpectedClosureRevisionID,
		FromSnapshotID:            authority.FailedAttempt.ActiveSnapshotID,
		AssignmentAttemptID:       command.AssignmentAttemptID,
		ReservePosition:           nextIndex + 1, Snapshot: snapshot, Category: snapshot.Category,
		Slot: openedSlot, Game: game, Wave: wave, OpenedAt: openedAt,
	}
	if err := replacement.Validate(); err != nil {
		return ReplayReplacement{}, err
	}
	return cloneReplayReplacement(replacement), nil
}

func replaySourceSlot(failed FailedAttemptRecord) (domain.ArenaGameSlot, error) {
	series := failed.Series.Series
	if len(series.Slots) == 0 {
		return domain.ArenaGameSlot{}, replayReplacementError("failed Series has no slot")
	}
	slot := cloneArenaGameSlot(series.Slots[len(series.Slots)-1])
	if slot.ID != failed.Scope.SlotID || len(slot.Attempts) == 0 ||
		slot.Attempts[len(slot.Attempts)-1].ID != failed.Scope.GameID ||
		slot.Attempts[len(slot.Attempts)-1].State != domain.ArenaGameStateVoid {
		return domain.ArenaGameSlot{}, replayReplacementError("failed attempt is not the slot head")
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
	if !arenaSettlementGamesEqual(last, replacement.Game) ||
		last.State != domain.ArenaGameStatePlanned || last.WinnerID != nil ||
		last.ResultRevisionID != nil || previous.State != domain.ArenaGameStateVoid ||
		last.AttemptNo != previous.AttemptNo+1 {
		return replayReplacementError("replacement Game resets or skips the attempt counter")
	}
	return nil
}

func validateReplayReplacementWave(replacement ReplayReplacement) error {
	wave := replacement.Wave
	if wave.Validate() != nil || wave.ID == replacement.Scope.OldWaveID ||
		wave.TournamentID != replacement.Scope.TournamentID ||
		wave.State != domain.ArenaWaveStateReadyWindowOpen || wave.ReadyWindow == nil ||
		wave.ReadyWindow.State != domain.ArenaReadyWindowStateOpen ||
		!wave.ReadyWindow.OpenedAt.Equal(replacement.OpenedAt) ||
		!wave.ReadyWindow.Deadline.Equal(replacement.OpenedAt.Add(readyWindowDuration)) ||
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
	if !uniqueNonZeroUUIDs(identities) {
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

func replayReplacementsEqual(first, second ReplayReplacement) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		first.ClosureRevisionID == second.ClosureRevisionID &&
		first.FromSnapshotID == second.FromSnapshotID &&
		first.AssignmentAttemptID == second.AssignmentAttemptID &&
		first.ReservePosition == second.ReservePosition &&
		taskSnapshotsEqual(first.Snapshot, second.Snapshot) && first.Category == second.Category &&
		arenaGameSlotsEqual(first.Slot, second.Slot) &&
		arenaSettlementGamesEqual(first.Game, second.Game) &&
		arenaWavesEqual(first.Wave, second.Wave) && first.OpenedAt.Equal(second.OpenedAt)
}

func cloneReplayReplacement(replacement ReplayReplacement) ReplayReplacement {
	clone := replacement
	clone.Snapshot = cloneTaskSnapshot(replacement.Snapshot)
	clone.Slot = cloneArenaGameSlot(replacement.Slot)
	clone.Game = cloneArenaGame(replacement.Game)
	clone.Wave = cloneArenaWaveExecution(replacement.Wave)
	return clone
}

func taskSnapshotsEqual(first, second domain.ArenaTaskSnapshot) bool {
	return first.SnapshotID == second.SnapshotID && first.TaskID == second.TaskID &&
		first.Version == second.Version && first.Kind == second.Kind &&
		first.Title == second.Title && first.Description == second.Description &&
		first.Category == second.Category && first.Difficulty == second.Difficulty &&
		first.TimeLimit == second.TimeLimit && first.Flag == second.Flag &&
		slices.Equal(first.Hints, second.Hints) &&
		stringPointersEqual(first.TaskURL, second.TaskURL) &&
		stringPointersEqual(first.SourceFileURL, second.SourceFileURL)
}

func stringPointersEqual(first, second *string) bool {
	return (first == nil && second == nil) ||
		(first != nil && second != nil && *first == *second)
}

type FailedAttemptTerminalizer interface {
	Terminalize(
		ctx context.Context,
		command FailedAttemptCommand,
	) (*FailedAttemptRecord, bool, error)
}

type OldWaveCloser interface {
	Close(
		ctx context.Context,
		command OldWaveCloseCommand,
	) (*OldWaveClosure, bool, error)
}

type ReplayReplacementPlanner interface {
	Replace(
		ctx context.Context,
		command ReplayReplacementCommand,
	) (*ReplayReplacement, bool, error)
}

type NoSolveReplayCommand struct {
	Terminalize FailedAttemptCommand
	Close       OldWaveCloseCommand
	Replace     ReplayReplacementCommand
}

type NoSolveReplayResult struct {
	FailedAttempt  *FailedAttemptRecord
	OldWaveClosure *OldWaveClosure
	Replacement    *ReplayReplacement
	Exhausted      bool
}

type NoSolveReplayUseCase struct {
	terminalizer FailedAttemptTerminalizer
	closer       OldWaveCloser
	replacer     ReplayReplacementPlanner
}

func NewNoSolveReplayUseCase(
	terminalizer FailedAttemptTerminalizer,
	closer OldWaveCloser,
	replacer ReplayReplacementPlanner,
) *NoSolveReplayUseCase {
	return &NoSolveReplayUseCase{
		terminalizer: terminalizer,
		closer:       closer,
		replacer:     replacer,
	}
}

func (u *NoSolveReplayUseCase) Replay(
	ctx context.Context,
	command NoSolveReplayCommand,
) (*NoSolveReplayResult, bool, error) {
	if u == nil || u.terminalizer == nil || u.closer == nil || u.replacer == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateNoSolveReplayCommand(command); err != nil {
		return nil, false, err
	}
	failed, failedChanged, err := u.terminalizer.Terminalize(ctx, command.Terminalize)
	if err != nil {
		return nil, false, err
	}
	if !validNoSolveFailedAttempt(failed, command.Terminalize) {
		return nil, false, domain.ErrInternal
	}
	return u.closeNoSolveWave(ctx, command, failed, failedChanged)
}

func (u *NoSolveReplayUseCase) closeNoSolveWave(
	ctx context.Context,
	command NoSolveReplayCommand,
	failed *FailedAttemptRecord,
	failedChanged bool,
) (*NoSolveReplayResult, bool, error) {
	closure, closureChanged, err := u.closer.Close(ctx, command.Close)
	if err != nil {
		return nil, failedChanged, err
	}
	if !validNoSolveClosure(closure, failed, command.Close) {
		return nil, failedChanged, domain.ErrInternal
	}
	return u.replaceNoSolveAttempt(
		ctx,
		command.Replace,
		failed,
		closure,
		failedChanged || closureChanged,
	)
}

func (u *NoSolveReplayUseCase) replaceNoSolveAttempt(
	ctx context.Context,
	command ReplayReplacementCommand,
	failed *FailedAttemptRecord,
	closure *OldWaveClosure,
	priorChanged bool,
) (*NoSolveReplayResult, bool, error) {
	replacement, replacementChanged, err := u.replacer.Replace(ctx, command)
	if errors.Is(err, ErrReplayReservesExhausted) {
		result := &NoSolveReplayResult{
			FailedAttempt:  cloneFailedAttemptRecordPointer(failed),
			OldWaveClosure: cloneOldWaveClosurePointer(closure),
			Exhausted:      true,
		}
		return result, priorChanged, nil
	}
	if err != nil {
		return nil, priorChanged, err
	}
	if !validNoSolveReplacement(replacement, closure, command) {
		return nil, priorChanged, domain.ErrInternal
	}
	result := &NoSolveReplayResult{
		FailedAttempt:  cloneFailedAttemptRecordPointer(failed),
		OldWaveClosure: cloneOldWaveClosurePointer(closure),
		Replacement:    cloneReplayReplacementPointer(replacement),
	}
	return result, priorChanged || replacementChanged, nil
}

func validNoSolveFailedAttempt(
	failed *FailedAttemptRecord,
	command FailedAttemptCommand,
) bool {
	if failed == nil || failed.Failure.Class != NormalAttemptFailureNoSolve {
		return false
	}
	_, err := reconcileFailedAttempt(*failed, command)
	return err == nil
}

func validNoSolveClosure(
	closure *OldWaveClosure,
	failed *FailedAttemptRecord,
	command OldWaveCloseCommand,
) bool {
	if closure == nil || failed == nil || !closureContainsFailedRoute(*closure, *failed) {
		return false
	}
	_, err := reconcileOldWaveClosure(*closure, command)
	return err == nil
}

func validNoSolveReplacement(
	replacement *ReplayReplacement,
	closure *OldWaveClosure,
	command ReplayReplacementCommand,
) bool {
	if replacement == nil || closure == nil ||
		replacement.ClosureRevisionID != closure.Wave.RevisionID {
		return false
	}
	_, err := reconcileReplayReplacement(*replacement, command)
	return err == nil
}

func validateNoSolveReplayCommand(command NoSolveReplayCommand) error {
	if command.Terminalize.FailureClass != NormalAttemptFailureNoSolve ||
		validateFailedAttemptCommand(command.Terminalize) != nil ||
		validateOldWaveCloseCommand(command.Close) != nil ||
		validateReplayReplacementCommand(command.Replace) != nil {
		return replayReplacementError("invalid no-solve pipeline command")
	}
	failedScope := command.Terminalize.Scope
	if command.Close.Scope.TournamentID != failedScope.TournamentID ||
		command.Close.Scope.WaveID != failedScope.WaveID ||
		command.Replace.Scope.TournamentID != failedScope.TournamentID ||
		command.Replace.Scope.OldWaveID != failedScope.WaveID ||
		command.Replace.Scope.SeriesID != failedScope.SeriesID ||
		command.Replace.Scope.SlotID != failedScope.SlotID ||
		command.Replace.Scope.AssignmentID != failedScope.AssignmentID ||
		command.Replace.ExpectedClosureRevisionID != command.Close.ClosedWaveRevisionID {
		return replayReplacementError("pipeline scopes or closure revision do not align")
	}
	return nil
}

func cloneFailedAttemptRecordPointer(record *FailedAttemptRecord) *FailedAttemptRecord {
	if record == nil {
		return nil
	}
	clone := cloneFailedAttemptRecord(*record)
	return &clone
}

func cloneOldWaveClosurePointer(closure *OldWaveClosure) *OldWaveClosure {
	if closure == nil {
		return nil
	}
	clone := cloneOldWaveClosure(*closure)
	return &clone
}

func cloneReplayReplacementPointer(replacement *ReplayReplacement) *ReplayReplacement {
	if replacement == nil {
		return nil
	}
	clone := cloneReplayReplacement(*replacement)
	return &clone
}

func replayReplacementError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidReplayReplacement, fmt.Sprintf(format, arguments...))
}
