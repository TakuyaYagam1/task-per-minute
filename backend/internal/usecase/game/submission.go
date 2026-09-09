package game

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
)

const (
	submissionAttempts     = 2
	maxFlagBytes           = 4096
	submissionIntentDomain = "participant-submission-intent:v1\x00"
)

var (
	ErrSubmissionNotOpen      = errors.New("game submission is not open")
	ErrSubmissionDisconnected = errors.New("tournament participant is disconnected")
	ErrSubmissionConflict     = errors.New("game submission commit conflict")
	ErrSubmissionCommandReuse = errors.New("game submission command was reused")
)

type SubmissionAuthority struct {
	Scope                   gamedomain.SubmissionScope
	Revision                int64
	StartedGame             gamedomain.Started
	Snapshot                SubmissionSnapshot
	ConnectedParticipantIDs []uuid.UUID
	Paused                  bool
	Submissions             []gamedomain.Submission
}

type SubmissionSnapshot interface {
	Snapshot() domain.AssignmentTaskSnapshot
	ContentDigest() [sha256.Size]byte
	HasParticipant(participantID uuid.UUID) bool
}

type SubmissionCommand struct {
	Scope              gamedomain.SubmissionScope
	CommandID          uuid.UUID
	ActorParticipantID uuid.UUID
	ParticipantID      uuid.UUID
	SubmittedFlag      string
}

type SubmissionCommit struct {
	Scope                     gamedomain.SubmissionScope
	ExpectedAuthorityRevision int64
	ExpectedGameState         domain.GameState
	ExpectedDeadline          time.Time
	CommandID                 uuid.UUID
	ParticipantID             uuid.UUID
	SnapshotID                uuid.UUID
	TaskID                    uuid.UUID
	ContentDigest             [sha256.Size]byte
	IntentDigest              [sha256.Size]byte
	Correct                   bool
}

type SubmissionUseCase struct {
	repository SubmissionRepository
}

func NewSubmissionUseCase(
	repository SubmissionRepository,
) *SubmissionUseCase {
	return &SubmissionUseCase{
		repository: repository,
	}
}

func (u *SubmissionUseCase) Submit(
	ctx context.Context,
	command SubmissionCommand,
) (gamedomain.Submission, bool, error) {
	if u == nil || u.repository == nil {
		return gamedomain.Submission{}, false, domain.ErrValidation
	}
	if err := validateSubmissionCommand(command); err != nil {
		return gamedomain.Submission{}, false, err
	}

	for range submissionAttempts {
		record, changed, retry, err := u.submitAttempt(ctx, command)
		if retry {
			continue
		}
		return record, changed, err
	}
	return gamedomain.Submission{}, false, ErrSubmissionConflict
}

func (u *SubmissionUseCase) submitAttempt(
	ctx context.Context,
	command SubmissionCommand,
) (gamedomain.Submission, bool, bool, error) {
	authority, err := u.repository.LoadSubmissionAuthority(ctx, command.Scope)
	if err != nil {
		return gamedomain.Submission{}, false, false,
			fmt.Errorf("SubmissionUseCase - load authority: %w", err)
	}
	if err := validateSubmissionAuthority(authority); err != nil {
		return gamedomain.Submission{}, false, false, err
	}
	if command.ActorParticipantID != command.ParticipantID {
		return gamedomain.Submission{}, false, false, domain.ErrAssignmentParticipant
	}
	if !authority.Snapshot.HasParticipant(command.ParticipantID) {
		return gamedomain.Submission{}, false, false, domain.ErrAssignmentParticipant
	}

	correct, err := taskexec.ValidateSnapshotFlag(taskexec.FlagValidationInput{
		ParticipantID: command.ParticipantID,
		Snapshot:      authority.Snapshot.Snapshot(),
		SubmittedFlag: command.SubmittedFlag,
	})
	if err != nil {
		return gamedomain.Submission{}, false, false, submissionError("snapshot flag validation failed")
	}
	if retained, found := submissionForCommand(authority.Submissions, command.CommandID); found {
		if !submissionMatchesCommand(retained, command, authority, correct) {
			return gamedomain.Submission{}, false, false, ErrSubmissionCommandReuse
		}
		return retained, false, false, nil
	}
	if authority.Paused || submissionGameState(authority.StartedGame) != domain.GameStateActive {
		return gamedomain.Submission{}, false, false, ErrSubmissionNotOpen
	}
	if !participantConnected(authority.ConnectedParticipantIDs, command.ParticipantID) {
		return gamedomain.Submission{}, false, false, ErrSubmissionDisconnected
	}

	commit, err := newSubmissionCommit(authority, command, correct)
	if err != nil {
		return gamedomain.Submission{}, false, false, err
	}
	committed, changed, err := u.repository.CommitSubmission(ctx, commit)
	if errors.Is(err, domain.ErrConflict) {
		return gamedomain.Submission{}, false, true, nil
	}
	if err != nil {
		return gamedomain.Submission{}, false, false, fmt.Errorf("SubmissionUseCase - commit submission: %w", err)
	}
	if !validCommittedSubmission(committed, commit, authority.StartedGame) {
		return gamedomain.Submission{}, false, false, domain.ErrInternal
	}
	return *committed, changed, false, nil
}

func validateSubmissionCommand(command SubmissionCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil ||
		command.ActorParticipantID == uuid.Nil || command.ParticipantID == uuid.Nil ||
		!validSubmittedFlag(command.SubmittedFlag) {
		return submissionError("invalid command identity or flag shape")
	}
	return nil
}

// SubmissionIntentDigest creates the durable idempotency evidence for an
// already validated participant flag without retaining that flag itself.
func SubmissionIntentDigest(submittedFlag string) ([sha256.Size]byte, error) {
	if !validSubmittedFlag(submittedFlag) {
		return [sha256.Size]byte{}, submissionError("invalid submitted flag shape")
	}
	return sha256.Sum256([]byte(submissionIntentDomain + submittedFlag)), nil
}

func validSubmittedFlag(submittedFlag string) bool {
	return len(submittedFlag) > 0 && len(submittedFlag) <= maxFlagBytes && utf8.ValidString(submittedFlag)
}

func validateSubmissionAuthority(authority SubmissionAuthority) error {
	if !authority.Scope.IsValid() || authority.Revision < 1 ||
		authority.Scope.Game != authority.StartedGame.Scope ||
		authority.Scope.AssignmentID != authority.StartedGame.AssignmentID {
		return submissionError("invalid authority identity")
	}
	if err := validateStartedSubmissionGame(authority.Scope, authority.StartedGame); err != nil {
		return err
	}
	if err := validateSubmissionSnapshot(authority); err != nil {
		return err
	}
	if err := validateConnectedParticipants(authority); err != nil {
		return err
	}
	return validateSubmissionHistory(authority)
}

func validateStartedSubmissionGame(
	scope gamedomain.SubmissionScope,
	started gamedomain.Started,
) error {
	if err := validateStartedSubmissionMetadata(scope, started); err != nil {
		return err
	}
	if err := validateStartedSubmissionSeries(scope, started); err != nil {
		return err
	}
	if _, _, found := gamedomain.FindAttempt(started, scope.Game); !found {
		return submissionError("started Game is not current")
	}
	return nil
}

func validateStartedSubmissionMetadata(
	scope gamedomain.SubmissionScope,
	started gamedomain.Started,
) error {
	if started.Scope != scope.Game || started.AssignmentID != scope.AssignmentID ||
		started.AssignmentRevision < 1 || started.PlanRevisionID == uuid.Nil ||
		started.SnapshotID == uuid.Nil || started.ContentDigest == [sha256.Size]byte{} ||
		started.DeadlineSeconds < 1 || !started.DeliveryEnabled ||
		!domain.IsValidServerTime(started.StartedAt) || !domain.IsValidServerTime(started.Deadline) ||
		!started.Deadline.Equal(started.StartedAt.Add(time.Duration(started.DeadlineSeconds)*time.Second)) {
		return submissionError("invalid started Game authority")
	}
	return nil
}

func validateStartedSubmissionSeries(
	scope gamedomain.SubmissionScope,
	started gamedomain.Started,
) error {
	if err := started.Series.Validate(); err != nil ||
		started.Series.Series.ID != scope.Game.SeriesID ||
		started.Series.Series.TournamentID != scope.Game.TournamentID ||
		started.ParticipantIDs[0] != started.Series.Series.FirstParticipantID ||
		started.ParticipantIDs[1] != started.Series.Series.SecondParticipantID {
		return submissionError("invalid started Series authority")
	}
	return nil
}

func validateSubmissionSnapshot(authority SubmissionAuthority) error {
	if authority.Snapshot == nil {
		return submissionError("invalid immutable snapshot")
	}
	snapshot := authority.Snapshot.Snapshot()
	digest, err := taskexec.SnapshotDigest(snapshot)
	if err != nil || digest != authority.Snapshot.ContentDigest() {
		return submissionError("invalid immutable snapshot")
	}
	if snapshot.SnapshotID != authority.StartedGame.SnapshotID ||
		authority.Snapshot.ContentDigest() != authority.StartedGame.ContentDigest {
		return submissionError("snapshot does not match started Game")
	}
	for _, participantID := range authority.StartedGame.ParticipantIDs {
		if !authority.Snapshot.HasParticipant(participantID) {
			return submissionError("snapshot participant does not match started Game")
		}
	}
	return nil
}

func validateConnectedParticipants(authority SubmissionAuthority) error {
	seen := make(map[uuid.UUID]struct{}, len(authority.ConnectedParticipantIDs))
	for _, participantID := range authority.ConnectedParticipantIDs {
		if participantID == uuid.Nil {
			return submissionError("empty connected participant")
		}
		if !authority.Snapshot.HasParticipant(participantID) {
			return submissionError("connected participant is not assigned")
		}
		if _, duplicate := seen[participantID]; duplicate {
			return submissionError("duplicate connected participant")
		}
		seen[participantID] = struct{}{}
	}
	return nil
}

func validateSubmissionHistory(authority SubmissionAuthority) error {
	ordered, err := gamedomain.OrderSubmissions(authority.Submissions)
	if err != nil {
		return err
	}
	snapshot := authority.Snapshot.Snapshot()
	for _, record := range ordered {
		if record.SnapshotID != snapshot.SnapshotID || record.TaskID != snapshot.TaskID ||
			record.ContentDigest != authority.Snapshot.ContentDigest() ||
			record.CommittedAt.Before(authority.StartedGame.StartedAt) ||
			!record.CommittedAt.Before(authority.StartedGame.Deadline) {
			return submissionError("submission history is outside started Game authority")
		}
		if !authority.Snapshot.HasParticipant(record.ParticipantID) {
			return submissionError("submission participant is not assigned")
		}
	}
	return nil
}

func newSubmissionCommit(
	authority SubmissionAuthority,
	command SubmissionCommand,
	correct bool,
) (SubmissionCommit, error) {
	intentDigest, err := SubmissionIntentDigest(command.SubmittedFlag)
	if err != nil {
		return SubmissionCommit{}, err
	}
	snapshot := authority.Snapshot.Snapshot()
	return SubmissionCommit{
		Scope: command.Scope, ExpectedAuthorityRevision: authority.Revision,
		ExpectedGameState: domain.GameStateActive,
		ExpectedDeadline:  authority.StartedGame.Deadline,
		CommandID:         command.CommandID, ParticipantID: command.ParticipantID,
		SnapshotID: snapshot.SnapshotID, TaskID: snapshot.TaskID,
		ContentDigest: authority.Snapshot.ContentDigest(), IntentDigest: intentDigest, Correct: correct,
	}, nil
}

func validCommittedSubmission(
	record *gamedomain.Submission,
	commit SubmissionCommit,
	started gamedomain.Started,
) bool {
	return record != nil && gamedomain.ValidateSubmission(*record) == nil && record.Scope == commit.Scope &&
		record.CommandID == commit.CommandID && record.ParticipantID == commit.ParticipantID &&
		record.SnapshotID == commit.SnapshotID && record.TaskID == commit.TaskID &&
		record.ContentDigest == commit.ContentDigest && record.Correct == commit.Correct &&
		!record.CommittedAt.Before(started.StartedAt) && record.CommittedAt.Before(commit.ExpectedDeadline)
}

func submissionMatchesCommand(
	record gamedomain.Submission,
	command SubmissionCommand,
	authority SubmissionAuthority,
	correct bool,
) bool {
	snapshot := authority.Snapshot.Snapshot()
	return record.Scope == command.Scope && record.ParticipantID == command.ParticipantID &&
		record.Correct == correct && record.SnapshotID == snapshot.SnapshotID &&
		record.TaskID == snapshot.TaskID && record.ContentDigest == authority.Snapshot.ContentDigest()
}

func submissionForCommand(
	records []gamedomain.Submission,
	commandID uuid.UUID,
) (gamedomain.Submission, bool) {
	for _, record := range records {
		if record.CommandID == commandID {
			return record, true
		}
	}
	return gamedomain.Submission{}, false
}

func submissionGameState(started gamedomain.Started) domain.GameState {
	game, _, found := gamedomain.FindAttempt(started, started.Scope)
	if !found {
		return ""
	}
	return game.State
}

func participantConnected(participantIDs []uuid.UUID, target uuid.UUID) bool {
	for _, participantID := range participantIDs {
		if participantID == target {
			return true
		}
	}
	return false
}

func submissionError(message string) error {
	return fmt.Errorf("%w: %s", gamedomain.ErrInvalidSubmission, message)
}
