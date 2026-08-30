package arena

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	readyWindowDuration = 30 * time.Second
	readyWindowAttempts = 2
)

var (
	ErrInvalidReadyWindow           = errors.New("invalid Arena ready window")
	ErrReadyWindowAuthorityConflict = errors.New("arena ready-window authority conflict")
	ErrReadyWindowConflict          = errors.New("arena ready-window commit conflict")
)

type ReadyWindowScope struct {
	TournamentID uuid.UUID
	WaveID       uuid.UUID
}

type ReadyWindowSourceRevisions struct {
	WaveRevisionID       domain.ArenaWaveRevisionID
	WaveRevision         int64
	PlanRevisionID       uuid.UUID
	PlanRevision         int64
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
	ArtifactRevisionID   uuid.UUID
	ArtifactRevision     int64
}

type ReadyWindowAuthority struct {
	Scope     ReadyWindowScope
	Revision  int64
	Revisions ReadyWindowSourceRevisions
	Wave      domain.ArenaWave
	Current   *ReadyWindowRecord
}

type OpenReadyWindowCommand struct {
	Scope             ReadyWindowScope
	CommandID         uuid.UUID
	WindowID          uuid.UUID
	WindowRevisionID  domain.ArenaReadyWindowRevisionID
	ExpectedRevisions ReadyWindowSourceRevisions
}

type ReadyWindowRecord struct {
	Scope                     ReadyWindowScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	Revisions                 ReadyWindowSourceRevisions
	Wave                      domain.ArenaWave
	OpenedAt                  time.Time
	Deadline                  time.Time
}

// ReadyWindowRepository owns one compare-and-set that revalidates the Wave,
// exact plan, projection and artifact revisions before opening the window.
type ReadyWindowRepository interface {
	LoadReadyWindowAuthority(ctx context.Context, scope ReadyWindowScope) (ReadyWindowAuthority, error)
	CommitReadyWindow(ctx context.Context, record ReadyWindowRecord) (*ReadyWindowRecord, bool, error)
}

type ReadyWindowUseCase struct {
	repository ReadyWindowRepository
	clock      Clock
}

func NewReadyWindowUseCase(repository ReadyWindowRepository, clock Clock) *ReadyWindowUseCase {
	return &ReadyWindowUseCase{repository: repository, clock: clock}
}

func (u *ReadyWindowUseCase) Open(
	ctx context.Context,
	command OpenReadyWindowCommand,
) (*ReadyWindowRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateOpenReadyWindowCommand(command); err != nil {
		return nil, false, err
	}
	openedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(openedAt) {
		return nil, false, domain.ErrValidation
	}

	for range readyWindowAttempts {
		record, changed, retry, err := u.openAttempt(ctx, command, openedAt)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrReadyWindowConflict
}

func (u *ReadyWindowUseCase) openAttempt(
	ctx context.Context,
	command OpenReadyWindowCommand,
	openedAt time.Time,
) (*ReadyWindowRecord, bool, bool, error) {
	authority, err := u.repository.LoadReadyWindowAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("ReadyWindowUseCase - load authority: %w", err)
	}
	if err := validateReadyWindowAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Current != nil {
		record, reconcileErr := reconcileReadyWindow(*authority.Current, command)
		return record, false, false, reconcileErr
	}
	if authority.Revisions != command.ExpectedRevisions ||
		authority.Wave.State != domain.ArenaWaveStatePlanned || authority.Wave.ReadyWindow != nil {
		return nil, false, false, ErrReadyWindowAuthorityConflict
	}
	record, err := buildReadyWindowRecord(command, authority, openedAt)
	if err != nil {
		return nil, false, false, err
	}
	return u.commitReadyWindow(ctx, command, record)
}

func (u *ReadyWindowUseCase) commitReadyWindow(
	ctx context.Context,
	command OpenReadyWindowCommand,
	record ReadyWindowRecord,
) (*ReadyWindowRecord, bool, bool, error) {
	committed, changed, err := u.repository.CommitReadyWindow(ctx, record)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("ReadyWindowUseCase - commit window: %w", err)
	}
	if committed == nil || committed.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	result, reconcileErr := reconcileReadyWindow(*committed, command)
	if reconcileErr != nil || (changed && !readyWindowRecordsEqual(*result, record)) {
		return nil, false, false, domain.ErrInternal
	}
	return result, changed, false, nil
}

func (r ReadyWindowRecord) Validate() error {
	if err := validateReadyWindowRecordIdentity(r); err != nil {
		return err
	}
	return validateReadyWindowRecordWave(r)
}

func validateReadyWindowRecordIdentity(r ReadyWindowRecord) error {
	if !validReadyWindowScope(r.Scope) || r.CommandID == uuid.Nil ||
		r.ExpectedAuthorityRevision < 1 || !validReadyWindowSourceRevisions(r.Revisions) ||
		!validArenaServerTime(r.OpenedAt) || !validArenaServerTime(r.Deadline) ||
		!r.Deadline.Equal(r.OpenedAt.Add(readyWindowDuration)) {
		return readyWindowError("invalid record identity, revision or interval")
	}
	return nil
}

func validateReadyWindowRecordWave(r ReadyWindowRecord) error {
	if err := r.Wave.Validate(); err != nil {
		return readyWindowError("Wave: %v", err)
	}
	if r.Wave.ID != r.Scope.WaveID || r.Wave.TournamentID != r.Scope.TournamentID ||
		r.Wave.RevisionID != r.Revisions.WaveRevisionID ||
		r.Wave.State != domain.ArenaWaveStateReadyWindowOpen || r.Wave.ReadyWindow == nil ||
		!r.Wave.ReadyWindow.OpenedAt.Equal(r.OpenedAt) ||
		!r.Wave.ReadyWindow.Deadline.Equal(r.Deadline) {
		return readyWindowError("record does not match the opened Wave")
	}
	return nil
}

func buildReadyWindowRecord(
	command OpenReadyWindowCommand,
	authority ReadyWindowAuthority,
	openedAt time.Time,
) (ReadyWindowRecord, error) {
	wave := cloneArenaWaveExecution(authority.Wave)
	deadline := openedAt.Add(readyWindowDuration)
	if err := wave.OpenReadyWindow(
		command.WindowID,
		command.WindowRevisionID,
		openedAt,
		deadline,
	); err != nil {
		return ReadyWindowRecord{}, readyWindowError("open Wave: %v", err)
	}
	record := ReadyWindowRecord{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision,
		Revisions:                 authority.Revisions, Wave: wave, OpenedAt: openedAt, Deadline: deadline,
	}
	if err := record.Validate(); err != nil {
		return ReadyWindowRecord{}, err
	}
	return cloneReadyWindowRecord(record), nil
}

func validateOpenReadyWindowCommand(command OpenReadyWindowCommand) error {
	if !validReadyWindowScope(command.Scope) || command.CommandID == uuid.Nil ||
		command.WindowID == uuid.Nil || command.WindowRevisionID.IsZero() ||
		!validReadyWindowSourceRevisions(command.ExpectedRevisions) ||
		command.ExpectedRevisions.WaveRevisionID.IsZero() {
		return readyWindowError("invalid command identity or revisions")
	}
	return nil
}

func validateReadyWindowAuthority(authority ReadyWindowAuthority) error {
	if !validReadyWindowScope(authority.Scope) || authority.Revision < 1 ||
		!validReadyWindowSourceRevisions(authority.Revisions) {
		return readyWindowError("invalid authority identity or revisions")
	}
	if err := authority.Wave.Validate(); err != nil {
		return readyWindowError("authority Wave: %v", err)
	}
	if authority.Wave.ID != authority.Scope.WaveID ||
		authority.Wave.TournamentID != authority.Scope.TournamentID ||
		authority.Wave.RevisionID != authority.Revisions.WaveRevisionID {
		return readyWindowError("authority does not match the Wave")
	}
	if authority.Current == nil {
		return nil
	}
	if err := authority.Current.Validate(); err != nil {
		return readyWindowError("current record: %v", err)
	}
	if authority.Current.Scope != authority.Scope ||
		authority.Current.Revisions != authority.Revisions ||
		authority.Wave.ReadyWindow == nil ||
		authority.Current.Wave.ReadyWindow.ID != authority.Wave.ReadyWindow.ID {
		return readyWindowError("current record does not match authority")
	}
	return nil
}

func reconcileReadyWindow(
	record ReadyWindowRecord,
	command OpenReadyWindowCommand,
) (*ReadyWindowRecord, error) {
	if err := record.Validate(); err != nil {
		return nil, domain.ErrInternal
	}
	if record.Scope != command.Scope || record.CommandID != command.CommandID ||
		record.Revisions != command.ExpectedRevisions || record.Wave.ReadyWindow == nil ||
		record.Wave.ReadyWindow.ID != command.WindowID ||
		record.Wave.ReadyWindow.RevisionID != command.WindowRevisionID {
		return nil, ErrReadyWindowAuthorityConflict
	}
	cloned := cloneReadyWindowRecord(record)
	return &cloned, nil
}

func validReadyWindowScope(scope ReadyWindowScope) bool {
	return scope.TournamentID != uuid.Nil && scope.WaveID != uuid.Nil
}

func validReadyWindowSourceRevisions(revisions ReadyWindowSourceRevisions) bool {
	return !revisions.WaveRevisionID.IsZero() && revisions.WaveRevision >= 1 &&
		revisions.PlanRevisionID != uuid.Nil && revisions.PlanRevision >= 1 &&
		revisions.ProjectionRevisionID != uuid.Nil && revisions.ProjectionRevision >= 1 &&
		revisions.ArtifactRevisionID != uuid.Nil && revisions.ArtifactRevision >= 1
}

func readyWindowRecordsEqual(first, second ReadyWindowRecord) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		first.Revisions == second.Revisions && first.OpenedAt.Equal(second.OpenedAt) &&
		first.Deadline.Equal(second.Deadline) && arenaWavesEqual(first.Wave, second.Wave)
}

func arenaWavesEqual(first, second domain.ArenaWave) bool {
	if first.ID != second.ID || first.TournamentID != second.TournamentID ||
		first.RevisionID != second.RevisionID || first.State != second.State ||
		len(first.Members) != len(second.Members) || !timePointersEqual(first.StartedAt, second.StartedAt) ||
		!timePointersEqual(first.PausedAt, second.PausedAt) {
		return false
	}
	for index := range first.Members {
		if first.Members[index] != second.Members[index] {
			return false
		}
	}
	return readyWindowsEqual(first.ReadyWindow, second.ReadyWindow)
}

func readyWindowsEqual(first, second *domain.ArenaReadyWindow) bool {
	if first == nil || second == nil {
		return first == second
	}
	return first.ID == second.ID && first.WaveID == second.WaveID &&
		first.RevisionID == second.RevisionID && first.State == second.State &&
		first.OpenedAt.Equal(second.OpenedAt) && first.Deadline.Equal(second.Deadline) &&
		timePointersEqual(first.ConsumedAt, second.ConsumedAt)
}

func timePointersEqual(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == second
	}
	return first.Equal(*second)
}

func cloneReadyWindowRecord(record ReadyWindowRecord) ReadyWindowRecord {
	clone := record
	clone.Wave = cloneArenaWaveExecution(record.Wave)
	return clone
}

func cloneArenaWaveExecution(wave domain.ArenaWave) domain.ArenaWave {
	clone := wave
	clone.Members = append([]domain.ArenaWaveMember(nil), wave.Members...)
	if wave.ReadyWindow != nil {
		window := *wave.ReadyWindow
		window.ConsumedAt = cloneArenaTimePointer(wave.ReadyWindow.ConsumedAt)
		clone.ReadyWindow = &window
	}
	clone.StartedAt = cloneArenaTimePointer(wave.StartedAt)
	clone.PausedAt = cloneArenaTimePointer(wave.PausedAt)
	return clone
}

func cloneArenaTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func readyWindowError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidReadyWindow, fmt.Sprintf(format, arguments...))
}
