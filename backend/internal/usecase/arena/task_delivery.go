package arena

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const privateTaskDeliveryAttempts = 2

var (
	ErrInvalidPrivateTaskDelivery  = errors.New("invalid private Arena task delivery")
	ErrPrivateTaskNotStarted       = errors.New("arena task delivery is not started")
	ErrPrivateTaskDeliveryConflict = errors.New("private Arena task delivery conflict")
)

type PrivateTaskDeliveryScope struct {
	AssignmentID uuid.UUID
	AttemptID    uuid.UUID
}

type PrivateTaskDeliveryAuthority struct {
	Scope     PrivateTaskDeliveryScope
	Revision  int64
	StartedAt *time.Time
	Snapshot  ImmutableTaskSnapshot
	Receipts  []domain.ArenaDeliveryReceipt
}

type PrivateTaskDeliveryCommand struct {
	Scope              PrivateTaskDeliveryScope
	ActorParticipantID uuid.UUID
	ParticipantID      uuid.UUID
	ReceiptID          uuid.UUID
	RequestedAt        time.Time
}

type PrivateTaskDeliveryCommit struct {
	Scope                 PrivateTaskDeliveryScope
	ExpectedRevision      int64
	ExpectedStartedAt     time.Time
	ContentDigest         [sha256.Size]byte
	ParticipantInstanceID uuid.UUID
	Receipt               domain.ArenaDeliveryReceipt
}

type PrivateTaskView struct {
	ReceiptID        uuid.UUID
	ParticipantID    uuid.UUID
	SnapshotID       uuid.UUID
	TaskID           uuid.UUID
	Version          int
	Kind             domain.ArenaTaskKind
	Title            string
	Description      string
	Category         domain.Category
	Difficulty       domain.Difficulty
	Hints            []string
	TaskURL          *string
	SourceFileURL    *string
	InstanceID       uuid.UUID
	RuntimeProfile   string
	ValidationPolicy TaskValidationPolicy
	DeadlineSeconds  int
	DeliveredAt      time.Time
}

// PrivateTaskDeliveryRepository owns the compare-and-set that checks the Wave
// start, assignment revision, immutable snapshot digest and participant-owned
// instance before inserting at most one delivery receipt per participant.
type PrivateTaskDeliveryRepository interface {
	LoadPrivateTaskDeliveryAuthority(
		ctx context.Context,
		scope PrivateTaskDeliveryScope,
	) (PrivateTaskDeliveryAuthority, error)
	CommitPrivateTaskDelivery(
		ctx context.Context,
		commit PrivateTaskDeliveryCommit,
	) (*domain.ArenaDeliveryReceipt, bool, error)
}

type PrivateTaskDeliveryUseCase struct {
	repository PrivateTaskDeliveryRepository
}

func NewPrivateTaskDeliveryUseCase(
	repository PrivateTaskDeliveryRepository,
) *PrivateTaskDeliveryUseCase {
	return &PrivateTaskDeliveryUseCase{repository: repository}
}

func (u *PrivateTaskDeliveryUseCase) Deliver(
	ctx context.Context,
	command PrivateTaskDeliveryCommand,
) (PrivateTaskView, bool, error) {
	if u == nil || u.repository == nil {
		return PrivateTaskView{}, false, domain.ErrValidation
	}
	if command.ActorParticipantID != command.ParticipantID || command.ParticipantID == uuid.Nil {
		return PrivateTaskView{}, false, domain.ErrArenaAssignmentParticipant
	}
	if err := validatePrivateTaskDeliveryCommand(command); err != nil {
		return PrivateTaskView{}, false, err
	}

	for range privateTaskDeliveryAttempts {
		authority, err := u.repository.LoadPrivateTaskDeliveryAuthority(ctx, command.Scope)
		if err != nil {
			return PrivateTaskView{}, false, fmt.Errorf("PrivateTaskDeliveryUseCase - load authority: %w", err)
		}
		view, changed, retry, err := u.deliverWithAuthority(ctx, command, authority)
		if retry {
			continue
		}
		if err != nil {
			return PrivateTaskView{}, false, err
		}
		return view, changed, nil
	}
	return PrivateTaskView{}, false, ErrPrivateTaskDeliveryConflict
}

func (u *PrivateTaskDeliveryUseCase) deliverWithAuthority(
	ctx context.Context,
	command PrivateTaskDeliveryCommand,
	authority PrivateTaskDeliveryAuthority,
) (PrivateTaskView, bool, bool, error) {
	if err := validatePrivateTaskDeliveryAuthority(command.Scope, authority); err != nil {
		return PrivateTaskView{}, false, false, err
	}
	instance, ok := authority.Snapshot.InstanceFor(command.ParticipantID)
	if !ok {
		return PrivateTaskView{}, false, false, domain.ErrArenaAssignmentParticipant
	}
	if authority.StartedAt == nil || command.RequestedAt.Before(*authority.StartedAt) {
		return PrivateTaskView{}, false, false, ErrPrivateTaskNotStarted
	}
	if receipt, found := privateTaskReceiptFor(authority, command.ParticipantID); found {
		view, err := newPrivateTaskView(authority.Snapshot, instance, receipt)
		return view, false, false, err
	}
	commit, err := newPrivateTaskDeliveryCommit(command, authority, instance)
	if err != nil {
		return PrivateTaskView{}, false, false, err
	}
	committed, changed, err := u.repository.CommitPrivateTaskDelivery(ctx, commit)
	if errors.Is(err, domain.ErrConflict) {
		return PrivateTaskView{}, false, true, nil
	}
	if err != nil {
		return PrivateTaskView{}, false, false,
			fmt.Errorf("PrivateTaskDeliveryUseCase - commit receipt: %w", err)
	}
	if err := validateCommittedPrivateTaskReceipt(commit, committed); err != nil {
		return PrivateTaskView{}, false, false, err
	}
	view, err := newPrivateTaskView(authority.Snapshot, instance, *committed)
	return view, changed, false, err
}

func newPrivateTaskDeliveryCommit(
	command PrivateTaskDeliveryCommand,
	authority PrivateTaskDeliveryAuthority,
	instance TaskInstanceDescriptor,
) (PrivateTaskDeliveryCommit, error) {
	snapshot := authority.Snapshot.Snapshot()
	receipt := domain.ArenaDeliveryReceipt{
		ID: command.ReceiptID, AssignmentID: command.Scope.AssignmentID,
		AttemptID: command.Scope.AttemptID, ParticipantID: command.ParticipantID,
		SnapshotID: snapshot.SnapshotID, TaskID: snapshot.TaskID,
		DeliveredAt: command.RequestedAt.Round(0).UTC(),
	}
	if err := receipt.Validate(); err != nil {
		return PrivateTaskDeliveryCommit{}, privateTaskDeliveryError("receipt: %v", err)
	}
	return PrivateTaskDeliveryCommit{
		Scope: command.Scope, ExpectedRevision: authority.Revision,
		ExpectedStartedAt: *authority.StartedAt, ContentDigest: authority.Snapshot.ContentDigest(),
		ParticipantInstanceID: instance.InstanceID, Receipt: receipt,
	}, nil
}

func validatePrivateTaskDeliveryCommand(command PrivateTaskDeliveryCommand) error {
	if !validPrivateTaskDeliveryScope(command.Scope) || command.ActorParticipantID == uuid.Nil ||
		command.ReceiptID == uuid.Nil || command.RequestedAt.IsZero() ||
		command.RequestedAt.Location() != time.UTC {
		return privateTaskDeliveryError("invalid command identity or timestamp")
	}
	return nil
}

func validatePrivateTaskDeliveryAuthority(
	scope PrivateTaskDeliveryScope,
	authority PrivateTaskDeliveryAuthority,
) error {
	if authority.Scope != scope || !validPrivateTaskDeliveryScope(authority.Scope) ||
		authority.Revision < 1 || authority.Snapshot.Validate() != nil {
		return privateTaskDeliveryError("invalid delivery authority")
	}
	if authority.StartedAt != nil && (authority.StartedAt.IsZero() || authority.StartedAt.Location() != time.UTC) {
		return privateTaskDeliveryError("invalid authoritative start")
	}
	return validateRetainedPrivateTaskReceipts(scope, authority)
}

func validateRetainedPrivateTaskReceipts(
	scope PrivateTaskDeliveryScope,
	authority PrivateTaskDeliveryAuthority,
) error {
	seenParticipants := make(map[uuid.UUID]struct{}, len(authority.Receipts))
	snapshot := authority.Snapshot.Snapshot()
	for _, receipt := range authority.Receipts {
		if err := receipt.Validate(); err != nil || receipt.AssignmentID != scope.AssignmentID ||
			receipt.AttemptID != scope.AttemptID || receipt.SnapshotID != snapshot.SnapshotID ||
			receipt.TaskID != snapshot.TaskID {
			return privateTaskDeliveryError("invalid retained receipt")
		}
		if _, ok := authority.Snapshot.InstanceFor(receipt.ParticipantID); !ok {
			return privateTaskDeliveryError("receipt participant is not assigned")
		}
		if _, duplicate := seenParticipants[receipt.ParticipantID]; duplicate {
			return privateTaskDeliveryError("duplicate retained participant receipt")
		}
		seenParticipants[receipt.ParticipantID] = struct{}{}
	}
	return nil
}

func privateTaskReceiptFor(
	authority PrivateTaskDeliveryAuthority,
	participantID uuid.UUID,
) (domain.ArenaDeliveryReceipt, bool) {
	for _, receipt := range authority.Receipts {
		if receipt.ParticipantID == participantID {
			return receipt, true
		}
	}
	return domain.ArenaDeliveryReceipt{}, false
}

func validateCommittedPrivateTaskReceipt(
	commit PrivateTaskDeliveryCommit,
	receipt *domain.ArenaDeliveryReceipt,
) error {
	if receipt == nil || receipt.Validate() != nil ||
		receipt.AssignmentID != commit.Scope.AssignmentID ||
		receipt.AttemptID != commit.Scope.AttemptID ||
		receipt.ParticipantID != commit.Receipt.ParticipantID ||
		receipt.SnapshotID != commit.Receipt.SnapshotID ||
		receipt.TaskID != commit.Receipt.TaskID {
		return domain.ErrInternal
	}
	return nil
}

func newPrivateTaskView(
	immutable ImmutableTaskSnapshot,
	instance TaskInstanceDescriptor,
	receipt domain.ArenaDeliveryReceipt,
) (PrivateTaskView, error) {
	snapshot := immutable.Snapshot()
	if receipt.SnapshotID != snapshot.SnapshotID || receipt.TaskID != snapshot.TaskID ||
		receipt.ParticipantID != instance.ParticipantID ||
		instance.ContentDigest != immutable.ContentDigest() {
		return PrivateTaskView{}, domain.ErrInternal
	}
	return PrivateTaskView{
		ReceiptID: receipt.ID, ParticipantID: receipt.ParticipantID,
		SnapshotID: snapshot.SnapshotID, TaskID: snapshot.TaskID, Version: snapshot.Version,
		Kind: snapshot.Kind, Title: snapshot.Title, Description: snapshot.Description,
		Category: snapshot.Category, Difficulty: snapshot.Difficulty,
		Hints:         append([]string(nil), snapshot.Hints...),
		TaskURL:       cloneArenaStringPointer(snapshot.TaskURL),
		SourceFileURL: cloneArenaStringPointer(snapshot.SourceFileURL),
		InstanceID:    instance.InstanceID, RuntimeProfile: instance.RuntimeProfile,
		ValidationPolicy: instance.ValidationPolicy, DeadlineSeconds: instance.DeadlineSeconds,
		DeliveredAt: receipt.DeliveredAt,
	}, nil
}

func validPrivateTaskDeliveryScope(scope PrivateTaskDeliveryScope) bool {
	return scope.AssignmentID != uuid.Nil && scope.AttemptID != uuid.Nil
}

func privateTaskDeliveryError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPrivateTaskDelivery, fmt.Sprintf(format, arguments...))
}
