package arena

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	"github.com/TakuyaYagam1/task-per-minute/internal/taskexec"
)

const (
	arenaSubmissionAttempts = 2
	maxArenaFlagBytes       = 4096

	arenaSubmissionEventAccepted   = "arena.command.accepted"
	arenaSubmissionEventRejected   = "arena.command.rejected"
	arenaSubmissionEventIdempotent = "arena.command.idempotent"
	arenaSubmissionEventRetry      = "arena.command.retry"
	arenaSubmissionEventFailed     = "arena.command.failed"
)

type arenaSubmissionEventReason string

const (
	arenaSubmissionReasonCommitted             arenaSubmissionEventReason = "committed"
	arenaSubmissionReasonAlreadyCommitted      arenaSubmissionEventReason = "already_committed"
	arenaSubmissionReasonInvalid               arenaSubmissionEventReason = "invalid_submission"
	arenaSubmissionReasonParticipantMismatch   arenaSubmissionEventReason = "participant_mismatch"
	arenaSubmissionReasonParticipantDisconnect arenaSubmissionEventReason = "participant_disconnected"
	arenaSubmissionReasonNotOpen               arenaSubmissionEventReason = "submission_not_open"
	arenaSubmissionReasonCommandReused         arenaSubmissionEventReason = "command_reused"
	arenaSubmissionReasonCommitConflict        arenaSubmissionEventReason = "commit_conflict"
	arenaSubmissionReasonConflictExhausted     arenaSubmissionEventReason = "conflict_exhausted"
	arenaSubmissionReasonLoadFailed            arenaSubmissionEventReason = "load_failed"
	arenaSubmissionReasonInvalidAuthority      arenaSubmissionEventReason = "invalid_authority"
	arenaSubmissionReasonSnapshotValidation    arenaSubmissionEventReason = "snapshot_validation_failed"
	arenaSubmissionReasonCommitFailed          arenaSubmissionEventReason = "commit_failed"
	arenaSubmissionReasonInvalidCommit         arenaSubmissionEventReason = "invalid_commit"
)

var (
	ErrInvalidArenaSubmission      = errors.New("invalid Arena submission")
	ErrArenaSubmissionNotOpen      = errors.New("arena submission is not open")
	ErrArenaSubmissionDisconnected = errors.New("arena participant is disconnected")
	ErrArenaSubmissionConflict     = errors.New("arena submission commit conflict")
	ErrArenaSubmissionCommandReuse = errors.New("arena submission command was reused")
)

type ArenaSubmissionScope struct {
	WaveID       uuid.UUID
	Game         GameScope
	AssignmentID uuid.UUID
}

type ArenaSubmissionAuthority struct {
	Scope                   ArenaSubmissionScope
	Revision                int64
	StartedGame             StartedWaveGame
	Snapshot                ImmutableTaskSnapshot
	ConnectedParticipantIDs []uuid.UUID
	Paused                  bool
	Submissions             []ArenaSubmissionRecord
}

type ArenaSubmissionCommand struct {
	Scope              ArenaSubmissionScope
	CommandID          uuid.UUID
	ActorParticipantID uuid.UUID
	ParticipantID      uuid.UUID
	SubmittedFlag      string
}

type ArenaSubmissionCommit struct {
	Scope                     ArenaSubmissionScope
	ExpectedAuthorityRevision int64
	ExpectedGameState         domain.ArenaGameState
	ExpectedDeadline          time.Time
	CommandID                 uuid.UUID
	ParticipantID             uuid.UUID
	SnapshotID                uuid.UUID
	TaskID                    uuid.UUID
	ContentDigest             [sha256.Size]byte
	Correct                   bool
}

type ArenaSubmissionRecord struct {
	Scope         ArenaSubmissionScope
	CommandID     uuid.UUID
	ParticipantID uuid.UUID
	Sequence      int64
	CommittedAt   time.Time
	Correct       bool
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	ContentDigest [sha256.Size]byte
}

// ArenaSubmissionRepository owns the atomic intake boundary. The commit must
// revalidate active, unpaused and connected authority, reject the persisted
// deadline, assign the server commit timestamp and allocate a strictly
// increasing sequence inside the Game. It must never persist SubmittedFlag.
type ArenaSubmissionRepository interface {
	LoadArenaSubmissionAuthority(
		ctx context.Context,
		scope ArenaSubmissionScope,
	) (ArenaSubmissionAuthority, error)
	CommitArenaSubmission(
		ctx context.Context,
		commit ArenaSubmissionCommit,
	) (*ArenaSubmissionRecord, bool, error)
}

type ArenaSubmissionUseCase struct {
	repository ArenaSubmissionRepository
	observer   observability.ArenaEventObserver
}

func NewArenaSubmissionUseCase(
	repository ArenaSubmissionRepository,
	observers ...observability.ArenaEventObserver,
) *ArenaSubmissionUseCase {
	return &ArenaSubmissionUseCase{
		repository: repository,
		observer:   observability.FirstArenaEventObserver(observers...),
	}
}

func (u *ArenaSubmissionUseCase) Submit(
	ctx context.Context,
	command ArenaSubmissionCommand,
) (ArenaSubmissionRecord, bool, error) {
	if u == nil || u.repository == nil {
		return ArenaSubmissionRecord{}, false, domain.ErrValidation
	}
	if err := validateArenaSubmissionCommand(command); err != nil {
		u.emitSubmissionResult(
			ctx, command, ArenaSubmissionRecord{}, false, "", err,
		)
		return ArenaSubmissionRecord{}, false, err
	}

	for attempt := range arenaSubmissionAttempts {
		record, changed, retry, failureReason, err := u.submitAttempt(ctx, command)
		if retry {
			if attempt+1 < arenaSubmissionAttempts {
				u.emitSubmissionEvent(
					ctx,
					command,
					arenaSubmissionEventRetry,
					observability.ArenaOutcomeRetry,
					"submission_conflict_retry",
					arenaSubmissionReasonCommitConflict,
					0,
				)
			}
			continue
		}
		u.emitSubmissionResult(ctx, command, record, changed, failureReason, err)
		return record, changed, err
	}
	u.emitSubmissionEvent(
		ctx,
		command,
		arenaSubmissionEventFailed,
		observability.ArenaOutcomeFailure,
		"submission_failed",
		arenaSubmissionReasonConflictExhausted,
		0,
	)
	return ArenaSubmissionRecord{}, false, ErrArenaSubmissionConflict
}

func (u *ArenaSubmissionUseCase) submitAttempt(
	ctx context.Context,
	command ArenaSubmissionCommand,
) (ArenaSubmissionRecord, bool, bool, arenaSubmissionEventReason, error) {
	authority, err := u.repository.LoadArenaSubmissionAuthority(ctx, command.Scope)
	if err != nil {
		return ArenaSubmissionRecord{}, false, false, arenaSubmissionReasonLoadFailed,
			fmt.Errorf("ArenaSubmissionUseCase - load authority: %w", err)
	}
	if err := validateArenaSubmissionAuthority(authority); err != nil {
		return ArenaSubmissionRecord{}, false, false, arenaSubmissionReasonInvalidAuthority, err
	}
	if command.ActorParticipantID != command.ParticipantID {
		return ArenaSubmissionRecord{}, false, false, "", domain.ErrArenaAssignmentParticipant
	}
	if _, assigned := authority.Snapshot.InstanceFor(command.ParticipantID); !assigned {
		return ArenaSubmissionRecord{}, false, false, "", domain.ErrArenaAssignmentParticipant
	}

	correct, err := taskexec.ValidateSnapshotFlag(taskexec.FlagValidationInput{
		ParticipantID: command.ParticipantID,
		Snapshot:      authority.Snapshot.Snapshot(),
		SubmittedFlag: command.SubmittedFlag,
	})
	if err != nil {
		return ArenaSubmissionRecord{}, false, false, arenaSubmissionReasonSnapshotValidation,
			arenaSubmissionError("snapshot flag validation failed")
	}
	if retained, found := arenaSubmissionForCommand(authority.Submissions, command.CommandID); found {
		if !arenaSubmissionMatchesCommand(retained, command, authority, correct) {
			return ArenaSubmissionRecord{}, false, false, "", ErrArenaSubmissionCommandReuse
		}
		return retained, false, false, "", nil
	}
	if authority.Paused || arenaSubmissionGameState(authority.StartedGame) != domain.ArenaGameStateActive {
		return ArenaSubmissionRecord{}, false, false, "", ErrArenaSubmissionNotOpen
	}
	if !arenaParticipantConnected(authority.ConnectedParticipantIDs, command.ParticipantID) {
		return ArenaSubmissionRecord{}, false, false, "", ErrArenaSubmissionDisconnected
	}

	commit := newArenaSubmissionCommit(authority, command, correct)
	committed, changed, err := u.repository.CommitArenaSubmission(ctx, commit)
	if errors.Is(err, domain.ErrConflict) {
		return ArenaSubmissionRecord{}, false, true, "", nil
	}
	if err != nil {
		return ArenaSubmissionRecord{}, false, false, arenaSubmissionReasonCommitFailed,
			fmt.Errorf("ArenaSubmissionUseCase - commit submission: %w", err)
	}
	if !validCommittedArenaSubmission(committed, commit, authority.StartedGame) {
		return ArenaSubmissionRecord{}, false, false, arenaSubmissionReasonInvalidCommit, domain.ErrInternal
	}
	return *committed, changed, false, "", nil
}

func (u *ArenaSubmissionUseCase) emitSubmissionResult(
	ctx context.Context,
	command ArenaSubmissionCommand,
	record ArenaSubmissionRecord,
	changed bool,
	failureReason arenaSubmissionEventReason,
	err error,
) {
	if err == nil && changed {
		u.emitSubmissionEvent(
			ctx, command, arenaSubmissionEventAccepted, observability.ArenaOutcomeSuccess,
			"submission_accepted", arenaSubmissionReasonCommitted, record.Sequence,
		)
		return
	}
	if err == nil {
		u.emitSubmissionEvent(
			ctx, command, arenaSubmissionEventIdempotent, observability.ArenaOutcomeSuccess,
			"submission_idempotent", arenaSubmissionReasonAlreadyCommitted, record.Sequence,
		)
		return
	}
	if failureReason != "" {
		u.emitSubmissionEvent(
			ctx, command, arenaSubmissionEventFailed, observability.ArenaOutcomeFailure,
			"submission_failed", failureReason, 0,
		)
		return
	}
	u.emitSubmissionEvent(
		ctx, command, arenaSubmissionEventRejected, observability.ArenaOutcomeRejected,
		"submission_rejected", arenaSubmissionRejectionReason(err), 0,
	)
}

func (u *ArenaSubmissionUseCase) emitSubmissionEvent(
	ctx context.Context,
	command ArenaSubmissionCommand,
	event string,
	outcome string,
	transition string,
	reason arenaSubmissionEventReason,
	revision int64,
) {
	if u == nil || u.observer == nil {
		return
	}
	commandID := command.CommandID.String()
	_ = observability.EmitArenaEvent(ctx, u.observer, observability.ArenaEventInput{
		Event: event, Outcome: outcome, CorrelationID: commandID, CommandID: commandID,
		TournamentID: command.Scope.Game.TournamentID.String(), EntityKind: "game",
		EntityID: command.Scope.Game.GameID.String(), Stage: "submission",
		Transition: transition, ReasonCode: string(reason), Revision: revision,
	})
}

func arenaSubmissionRejectionReason(err error) arenaSubmissionEventReason {
	switch {
	case errors.Is(err, ErrArenaSubmissionDisconnected):
		return arenaSubmissionReasonParticipantDisconnect
	case errors.Is(err, ErrArenaSubmissionNotOpen):
		return arenaSubmissionReasonNotOpen
	case errors.Is(err, ErrArenaSubmissionCommandReuse):
		return arenaSubmissionReasonCommandReused
	case errors.Is(err, domain.ErrArenaAssignmentParticipant):
		return arenaSubmissionReasonParticipantMismatch
	default:
		return arenaSubmissionReasonInvalid
	}
}

func (r ArenaSubmissionRecord) Validate() error {
	if !validArenaSubmissionScope(r.Scope) || r.CommandID == uuid.Nil || r.ParticipantID == uuid.Nil ||
		r.Sequence < 1 || !validArenaServerTime(r.CommittedAt) || r.SnapshotID == uuid.Nil ||
		r.TaskID == uuid.Nil || r.ContentDigest == [sha256.Size]byte{} {
		return arenaSubmissionError("invalid record identity, timestamp, or content binding")
	}
	return nil
}

func OrderArenaSubmissions(
	submissions []ArenaSubmissionRecord,
) ([]ArenaSubmissionRecord, error) {
	ordered := append([]ArenaSubmissionRecord(nil), submissions...)
	seenSequences := make(map[int64]struct{}, len(ordered))
	seenCommands := make(map[uuid.UUID]struct{}, len(ordered))
	var scope ArenaSubmissionScope
	for index, submission := range ordered {
		if err := submission.Validate(); err != nil {
			return nil, err
		}
		if index == 0 {
			scope = submission.Scope
		} else if submission.Scope != scope {
			return nil, arenaSubmissionError("ordering crosses Game scope")
		}
		if _, duplicate := seenSequences[submission.Sequence]; duplicate {
			return nil, arenaSubmissionError("duplicate Game sequence")
		}
		if _, duplicate := seenCommands[submission.CommandID]; duplicate {
			return nil, arenaSubmissionError("duplicate command identity")
		}
		seenSequences[submission.Sequence] = struct{}{}
		seenCommands[submission.CommandID] = struct{}{}
	}
	sort.Slice(ordered, func(first, second int) bool {
		if !ordered[first].CommittedAt.Equal(ordered[second].CommittedAt) {
			return ordered[first].CommittedAt.Before(ordered[second].CommittedAt)
		}
		return ordered[first].Sequence < ordered[second].Sequence
	})
	return ordered, nil
}

func validateArenaSubmissionCommand(command ArenaSubmissionCommand) error {
	if !validArenaSubmissionScope(command.Scope) || command.CommandID == uuid.Nil ||
		command.ActorParticipantID == uuid.Nil || command.ParticipantID == uuid.Nil ||
		len(command.SubmittedFlag) == 0 || len(command.SubmittedFlag) > maxArenaFlagBytes ||
		!utf8.ValidString(command.SubmittedFlag) {
		return arenaSubmissionError("invalid command identity or flag shape")
	}
	return nil
}

func validateArenaSubmissionAuthority(authority ArenaSubmissionAuthority) error {
	if !validArenaSubmissionScope(authority.Scope) || authority.Revision < 1 ||
		authority.Scope.Game != authority.StartedGame.Scope ||
		authority.Scope.AssignmentID != authority.StartedGame.AssignmentID {
		return arenaSubmissionError("invalid authority identity")
	}
	if err := validateStartedArenaSubmissionGame(authority.Scope, authority.StartedGame); err != nil {
		return err
	}
	if err := validateArenaSubmissionSnapshot(authority); err != nil {
		return err
	}
	if err := validateArenaConnectedParticipants(authority); err != nil {
		return err
	}
	return validateArenaSubmissionHistory(authority)
}

func validateStartedArenaSubmissionGame(
	scope ArenaSubmissionScope,
	started StartedWaveGame,
) error {
	if err := validateStartedArenaSubmissionMetadata(scope, started); err != nil {
		return err
	}
	if err := validateStartedArenaSubmissionSeries(scope, started); err != nil {
		return err
	}
	if _, _, found := arenaSubmissionGame(started, scope.Game); !found {
		return arenaSubmissionError("started Game is not current")
	}
	return nil
}

func validateStartedArenaSubmissionMetadata(
	scope ArenaSubmissionScope,
	started StartedWaveGame,
) error {
	if started.Scope != scope.Game || started.AssignmentID != scope.AssignmentID ||
		started.AssignmentRevision < 1 || started.PlanRevisionID == uuid.Nil ||
		started.SnapshotID == uuid.Nil || started.ContentDigest == [sha256.Size]byte{} ||
		started.DeadlineSeconds < 1 || !started.DeliveryEnabled ||
		!validArenaServerTime(started.StartedAt) || !validArenaServerTime(started.Deadline) ||
		!started.Deadline.Equal(started.StartedAt.Add(time.Duration(started.DeadlineSeconds)*time.Second)) {
		return arenaSubmissionError("invalid started Game authority")
	}
	return nil
}

func validateStartedArenaSubmissionSeries(
	scope ArenaSubmissionScope,
	started StartedWaveGame,
) error {
	if err := started.Series.Validate(); err != nil ||
		started.Series.Series.ID != scope.Game.SeriesID ||
		started.Series.Series.TournamentID != scope.Game.TournamentID ||
		started.ParticipantIDs[0] != started.Series.Series.FirstParticipantID ||
		started.ParticipantIDs[1] != started.Series.Series.SecondParticipantID {
		return arenaSubmissionError("invalid started Series authority")
	}
	return nil
}

func validateArenaSubmissionSnapshot(authority ArenaSubmissionAuthority) error {
	if err := authority.Snapshot.Validate(); err != nil {
		return arenaSubmissionError("invalid immutable snapshot")
	}
	snapshot := authority.Snapshot.Snapshot()
	if snapshot.SnapshotID != authority.StartedGame.SnapshotID ||
		authority.Snapshot.ContentDigest() != authority.StartedGame.ContentDigest {
		return arenaSubmissionError("snapshot does not match started Game")
	}
	for _, participantID := range authority.StartedGame.ParticipantIDs {
		if _, assigned := authority.Snapshot.InstanceFor(participantID); !assigned {
			return arenaSubmissionError("snapshot participant does not match started Game")
		}
	}
	return nil
}

func validateArenaConnectedParticipants(authority ArenaSubmissionAuthority) error {
	seen := make(map[uuid.UUID]struct{}, len(authority.ConnectedParticipantIDs))
	for _, participantID := range authority.ConnectedParticipantIDs {
		if participantID == uuid.Nil {
			return arenaSubmissionError("empty connected participant")
		}
		if _, assigned := authority.Snapshot.InstanceFor(participantID); !assigned {
			return arenaSubmissionError("connected participant is not assigned")
		}
		if _, duplicate := seen[participantID]; duplicate {
			return arenaSubmissionError("duplicate connected participant")
		}
		seen[participantID] = struct{}{}
	}
	return nil
}

func validateArenaSubmissionHistory(authority ArenaSubmissionAuthority) error {
	ordered, err := OrderArenaSubmissions(authority.Submissions)
	if err != nil {
		return err
	}
	snapshot := authority.Snapshot.Snapshot()
	for _, record := range ordered {
		if record.SnapshotID != snapshot.SnapshotID || record.TaskID != snapshot.TaskID ||
			record.ContentDigest != authority.Snapshot.ContentDigest() ||
			record.CommittedAt.Before(authority.StartedGame.StartedAt) ||
			!record.CommittedAt.Before(authority.StartedGame.Deadline) {
			return arenaSubmissionError("submission history is outside started Game authority")
		}
		if _, assigned := authority.Snapshot.InstanceFor(record.ParticipantID); !assigned {
			return arenaSubmissionError("submission participant is not assigned")
		}
	}
	return nil
}

func newArenaSubmissionCommit(
	authority ArenaSubmissionAuthority,
	command ArenaSubmissionCommand,
	correct bool,
) ArenaSubmissionCommit {
	snapshot := authority.Snapshot.Snapshot()
	return ArenaSubmissionCommit{
		Scope: command.Scope, ExpectedAuthorityRevision: authority.Revision,
		ExpectedGameState: domain.ArenaGameStateActive,
		ExpectedDeadline:  authority.StartedGame.Deadline,
		CommandID:         command.CommandID, ParticipantID: command.ParticipantID,
		SnapshotID: snapshot.SnapshotID, TaskID: snapshot.TaskID,
		ContentDigest: authority.Snapshot.ContentDigest(), Correct: correct,
	}
}

func validCommittedArenaSubmission(
	record *ArenaSubmissionRecord,
	commit ArenaSubmissionCommit,
	started StartedWaveGame,
) bool {
	return record != nil && record.Validate() == nil && record.Scope == commit.Scope &&
		record.CommandID == commit.CommandID && record.ParticipantID == commit.ParticipantID &&
		record.SnapshotID == commit.SnapshotID && record.TaskID == commit.TaskID &&
		record.ContentDigest == commit.ContentDigest && record.Correct == commit.Correct &&
		!record.CommittedAt.Before(started.StartedAt) && record.CommittedAt.Before(commit.ExpectedDeadline)
}

func arenaSubmissionMatchesCommand(
	record ArenaSubmissionRecord,
	command ArenaSubmissionCommand,
	authority ArenaSubmissionAuthority,
	correct bool,
) bool {
	snapshot := authority.Snapshot.Snapshot()
	return record.Scope == command.Scope && record.ParticipantID == command.ParticipantID &&
		record.Correct == correct && record.SnapshotID == snapshot.SnapshotID &&
		record.TaskID == snapshot.TaskID && record.ContentDigest == authority.Snapshot.ContentDigest()
}

func arenaSubmissionForCommand(
	records []ArenaSubmissionRecord,
	commandID uuid.UUID,
) (ArenaSubmissionRecord, bool) {
	for _, record := range records {
		if record.CommandID == commandID {
			return record, true
		}
	}
	return ArenaSubmissionRecord{}, false
}

func arenaSubmissionGameState(started StartedWaveGame) domain.ArenaGameState {
	game, _, found := arenaSubmissionGame(started, started.Scope)
	if !found {
		return ""
	}
	return game.State
}

func arenaSubmissionGame(
	started StartedWaveGame,
	scope GameScope,
) (domain.ArenaGame, int, bool) {
	for slotIndex, slot := range started.Series.Series.Slots {
		if slot.ID != scope.SlotID || len(slot.Attempts) == 0 {
			continue
		}
		attempt := slot.Attempts[len(slot.Attempts)-1]
		if attempt.ID == scope.GameID {
			return attempt, slotIndex, true
		}
	}
	return domain.ArenaGame{}, 0, false
}

func arenaParticipantConnected(participantIDs []uuid.UUID, target uuid.UUID) bool {
	for _, participantID := range participantIDs {
		if participantID == target {
			return true
		}
	}
	return false
}

func validArenaSubmissionScope(scope ArenaSubmissionScope) bool {
	return scope.WaveID != uuid.Nil && scope.AssignmentID != uuid.Nil && validGameScope(scope.Game)
}

func arenaSubmissionError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidArenaSubmission, message)
}
