package game

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gamewave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/wave"
)

const (
	waveStartAttempts        = 2
	waveStartDeadlineSeconds = 180
)

var (
	ErrInvalidWaveStart           = errors.New("invalid wave start")
	ErrWaveStartAuthorityConflict = errors.New("wave start authority conflict")
	ErrWaveStartConflict          = errors.New("wave start commit conflict")
)

type StartScope struct {
	TournamentID uuid.UUID
	WaveID       uuid.UUID
	WindowID     uuid.UUID
}

type GameAuthority struct {
	Scope              gamedomain.Scope
	ParticipantIDs     [2]uuid.UUID
	Series             seriesdomain.Execution
	AssignmentID       uuid.UUID
	AssignmentRevision int64
	PlanRevisionID     uuid.UUID
	SnapshotID         uuid.UUID
	ContentDigest      [sha256.Size]byte
	DeadlineSeconds    int
}

type StartAuthority struct {
	Scope              StartScope
	WaveRevision       int64
	Revisions          domain.ReadyWindowSourceRevisions
	ReadinessRevisions map[uuid.UUID]int64
	ByeParticipantID   *uuid.UUID
	Wave               domain.Wave
	Games              []GameAuthority
	Current            *StartRecord
}

type StartCommand struct {
	Scope                      StartScope
	CommandID                  uuid.UUID
	ActorID                    uuid.UUID
	ExecutionAuthority         authoritydomain.Identity
	ExpectedProjectionRevision int64
	ExpectedRevisions          domain.ReadyWindowSourceRevisions
	RequestDigest              [sha256.Size]byte
}

type StartRecord struct {
	Scope                      StartScope
	CommandID                  uuid.UUID
	ActorID                    uuid.UUID
	ExecutionAuthority         authoritydomain.Identity
	ExpectedWaveRevision       int64
	ExpectedProjectionRevision int64
	Revisions                  domain.ReadyWindowSourceRevisions
	ReadinessRevisions         map[uuid.UUID]int64
	ByeParticipantID           *uuid.UUID
	RequestDigest              [sha256.Size]byte
	Wave                       domain.Wave
	Games                      []gamedomain.Started
	StartedAt                  time.Time
}

type StartUseCase struct {
	repository StartRepository
	clock      WaveClock
}

func NewStartUseCase(
	repository StartRepository,
	clock WaveClock,
) *StartUseCase {
	return &StartUseCase{
		repository: repository,
		clock:      clock,
	}
}

func (u *StartUseCase) Start(
	ctx context.Context,
	command StartCommand,
) (record *StartRecord, changed bool, err error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateStartWaveCommand(command); err != nil {
		return nil, false, err
	}
	var startedAt time.Time

	for range waveStartAttempts {
		attemptRecord, attemptChanged, retry, attemptErr := u.startAttempt(
			ctx,
			command,
			&startedAt,
		)
		if retry {
			continue
		}
		return attemptRecord, attemptChanged, attemptErr
	}
	return nil, false, ErrWaveStartConflict
}

func (u *StartUseCase) startAttempt(
	ctx context.Context,
	command StartCommand,
	startedAt *time.Time,
) (*StartRecord, bool, bool, error) {
	authority, err := u.repository.LoadWaveStartAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("WaveStartUseCase - load authority: %w", err)
	}
	if err := validateWaveStartAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Current != nil {
		record, reconcileErr := reconcileWaveStart(*authority.Current, command)
		return record, false, false, reconcileErr
	}
	if startedAt == nil {
		return nil, false, false, domain.ErrInternal
	}
	if startedAt.IsZero() {
		observed, timeErr := u.repository.ReadWaveStartTime(ctx)
		if timeErr != nil {
			return nil, false, false, fmt.Errorf("WaveStartUseCase - read start time: %w", timeErr)
		}
		*startedAt = observed.Round(0).UTC()
		if !gamewave.ValidServerTime(*startedAt) {
			return nil, false, false, domain.ErrInternal
		}
	}
	if !waveStartAuthorityMatchesCommand(authority, command, *startedAt) {
		return nil, false, false, ErrWaveStartAuthorityConflict
	}
	record, err := buildWaveStartRecord(command, authority, *startedAt)
	if err != nil {
		return nil, false, false, err
	}
	return u.commitWaveStart(ctx, command, record)
}

func (u *StartUseCase) commitWaveStart(
	ctx context.Context,
	command StartCommand,
	record StartRecord,
) (*StartRecord, bool, bool, error) {
	committed, changed, err := u.repository.CommitWaveStart(ctx, record)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("WaveStartUseCase - commit start: %w", err)
	}
	if committed == nil || committed.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	result, reconcileErr := reconcileWaveStart(*committed, command)
	if reconcileErr != nil || (changed && !waveStartRecordsEqual(*result, record)) {
		return nil, false, false, domain.ErrInternal
	}
	return result, changed, false, nil
}

func (r StartRecord) Validate() error {
	if err := validateWaveStartRecordIdentity(r); err != nil {
		return err
	}
	if err := validateWaveStartRecordWave(r); err != nil {
		return err
	}
	return validateStartedWaveGames(r)
}

func validateWaveStartRecordIdentity(r StartRecord) error {
	if !validWaveStartScope(r.Scope) || r.CommandID == uuid.Nil || r.ActorID == uuid.Nil ||
		r.ExecutionAuthority.Validate() != nil || r.ExecutionAuthority.TournamentID != r.Scope.TournamentID ||
		r.ExpectedWaveRevision < 1 || r.ExpectedProjectionRevision < 1 ||
		!gamewave.ValidReadyWindowSourceRevisions(r.Revisions) ||
		!validWaveStartReadinessRevisions(r.Wave, r.ReadinessRevisions) ||
		r.RequestDigest == [sha256.Size]byte{} ||
		!gamewave.ValidServerTime(r.StartedAt) || len(r.Games) == 0 {
		return waveStartError("invalid record identity, revision or start")
	}
	return nil
}

func validateWaveStartRecordWave(r StartRecord) error {
	if err := r.Wave.Validate(); err != nil || r.Wave.ID != r.Scope.WaveID ||
		r.Wave.TournamentID != r.Scope.TournamentID || r.Wave.RevisionID != r.Revisions.WaveRevisionID ||
		r.Wave.State != domain.WaveStateActive || r.Wave.StartedAt == nil ||
		!r.Wave.StartedAt.Equal(r.StartedAt) || r.Wave.ReadyWindow == nil ||
		r.Wave.ReadyWindow.ID != r.Scope.WindowID ||
		r.Wave.ReadyWindow.State != domain.ReadyWindowStateConsumed {
		return waveStartError("record does not contain one consumed Wave start")
	}
	return nil
}

func buildWaveStartRecord(
	command StartCommand,
	authority StartAuthority,
	startedAt time.Time,
) (StartRecord, error) {
	wave := gamewave.Clone(authority.Wave)
	changed, err := wave.Start(command.Scope.WindowID, startedAt)
	if err != nil || !changed {
		return StartRecord{}, waveStartError("start Wave: %v", err)
	}
	startedGames := make([]gamedomain.Started, len(authority.Games))
	for index, game := range authority.Games {
		started, err := startWaveGame(game, startedAt)
		if err != nil {
			return StartRecord{}, err
		}
		startedGames[index] = started
	}
	record := StartRecord{
		Scope: command.Scope, CommandID: command.CommandID, ActorID: command.ActorID,
		ExecutionAuthority:         command.ExecutionAuthority,
		ExpectedWaveRevision:       authority.WaveRevision,
		ExpectedProjectionRevision: command.ExpectedProjectionRevision,
		Revisions:                  authority.Revisions,
		ReadinessRevisions:         cloneWaveStartReadinessRevisions(authority.ReadinessRevisions),
		ByeParticipantID:           cloneWaveStartUUID(authority.ByeParticipantID),
		RequestDigest:              command.RequestDigest,
		Wave:                       wave, Games: startedGames, StartedAt: startedAt,
	}
	if err := record.Validate(); err != nil {
		return StartRecord{}, err
	}
	return cloneWaveStartRecord(record), nil
}

func startWaveGame(authority GameAuthority, startedAt time.Time) (gamedomain.Started, error) {
	series := seriesdomain.CloneExecution(authority.Series)
	if series.Series.State == domain.SeriesStateReady {
		active, changed, err := seriesdomain.Transition(series, seriesdomain.TransitionCommand{
			NextState: domain.SeriesStateActive,
		})
		if err != nil || !changed {
			return gamedomain.Started{}, waveStartError("activate Series: %v", err)
		}
		series = active
	}
	slotIndex, err := waveStartSlotIndex(series.Series, authority.Scope)
	if err != nil {
		return gamedomain.Started{}, err
	}
	slot := series.Series.Slots[slotIndex]
	attempt := slot.Attempts[len(slot.Attempts)-1]
	if attempt.State == domain.GameStatePlanned {
		ready, changed, transitionErr := gamedomain.TransitionSlotAttempt(slot, gamedomain.TransitionCommand{
			GameID: attempt.ID, ExpectedAttemptNo: attempt.AttemptNo,
			ExpectedState: attempt.State, NextState: domain.GameStateReady,
		})
		if transitionErr != nil || !changed {
			return gamedomain.Started{}, waveStartError("prepare Game: %v", transitionErr)
		}
		slot = ready
		attempt = slot.Attempts[len(slot.Attempts)-1]
	}
	active, changed, err := gamedomain.TransitionSlotAttempt(slot, gamedomain.TransitionCommand{
		GameID: attempt.ID, ExpectedAttemptNo: attempt.AttemptNo,
		ExpectedState: attempt.State, NextState: domain.GameStateActive,
	})
	if err != nil || !changed {
		return gamedomain.Started{}, waveStartError("activate Game: %v", err)
	}
	series.Series.Slots[slotIndex] = active
	return gamedomain.Started{
		Scope: authority.Scope, ParticipantIDs: authority.ParticipantIDs, Series: series,
		AssignmentID: authority.AssignmentID, AssignmentRevision: authority.AssignmentRevision,
		PlanRevisionID: authority.PlanRevisionID, SnapshotID: authority.SnapshotID,
		ContentDigest: authority.ContentDigest, DeadlineSeconds: authority.DeadlineSeconds,
		StartedAt:       startedAt,
		Deadline:        startedAt.Add(time.Duration(authority.DeadlineSeconds) * time.Second),
		DeliveryEnabled: true,
	}, nil
}

func validateStartWaveCommand(command StartCommand) error {
	if !validWaveStartScope(command.Scope) || command.CommandID == uuid.Nil || command.ActorID == uuid.Nil ||
		command.ExecutionAuthority.Validate() != nil ||
		command.ExecutionAuthority.TournamentID != command.Scope.TournamentID ||
		command.ExpectedProjectionRevision < 1 || !gamewave.ValidReadyWindowSourceRevisions(command.ExpectedRevisions) ||
		command.RequestDigest == [sha256.Size]byte{} {
		return waveStartError("invalid command identity or revisions")
	}
	return nil
}

func validateWaveStartAuthority(authority StartAuthority) error {
	if !validWaveStartScope(authority.Scope) || authority.WaveRevision < 1 ||
		!gamewave.ValidReadyWindowSourceRevisions(authority.Revisions) {
		return waveStartError("invalid authority identity or revisions")
	}
	if authority.Current != nil {
		if err := authority.Current.Validate(); err != nil || authority.Current.Scope != authority.Scope {
			return waveStartError("invalid current start")
		}
		return nil
	}
	if !validWaveStartReadinessRevisions(authority.Wave, authority.ReadinessRevisions) {
		return ErrWaveStartAuthorityConflict
	}
	if err := authority.Wave.Validate(); err != nil || authority.Wave.ID != authority.Scope.WaveID ||
		authority.Wave.TournamentID != authority.Scope.TournamentID ||
		authority.Wave.RevisionID != authority.Revisions.WaveRevisionID ||
		authority.Wave.ReadyWindow == nil || authority.Wave.ReadyWindow.ID != authority.Scope.WindowID ||
		authority.Wave.State != domain.WaveStateReady {
		return ErrWaveStartAuthorityConflict
	}
	return validateWaveStartGames(authority)
}

func validWaveStartReadinessRevisions(wave domain.Wave, revisions map[uuid.UUID]int64) bool {
	if len(revisions) != len(wave.Members) {
		return false
	}
	for _, member := range wave.Members {
		if revisions[member.ParticipantID] < 1 {
			return false
		}
	}
	return true
}

func validateWaveStartGames(authority StartAuthority) error {
	if len(authority.Games) == 0 {
		return waveStartError("Wave has no playable Games")
	}
	members := make(map[uuid.UUID]bool, len(authority.Wave.Members))
	for _, member := range authority.Wave.Members {
		members[member.ParticipantID] = false
	}
	seenGames := make(map[uuid.UUID]struct{}, len(authority.Games))
	for _, game := range authority.Games {
		if err := validateWaveStartGameAuthority(authority.Scope, game); err != nil {
			return err
		}
		if _, duplicate := seenGames[game.Scope.GameID]; duplicate {
			return waveStartError("Game appears more than once")
		}
		seenGames[game.Scope.GameID] = struct{}{}
		for _, participantID := range game.ParticipantIDs {
			covered, exists := members[participantID]
			if !exists || covered {
				return waveStartError("playable membership is missing or duplicated")
			}
			members[participantID] = true
		}
	}
	for participantID, covered := range members {
		if !covered && !sameWaveStartUUID(authority.ByeParticipantID, participantID) {
			return waveStartError("Wave member %s has no playable Game", participantID)
		}
	}
	return validateWaveStartBye(authority.ByeParticipantID, members)
}

func validateWaveStartGameAuthority(scope StartScope, game GameAuthority) error {
	if err := validateWaveStartGameIdentity(scope, game); err != nil {
		return err
	}
	return validateWaveStartGameSeries(scope, game)
}

func validateWaveStartGameIdentity(scope StartScope, game GameAuthority) error {
	if !validGameScope(game.Scope) || game.Scope.TournamentID != scope.TournamentID ||
		game.ParticipantIDs[0] == uuid.Nil || game.ParticipantIDs[1] == uuid.Nil ||
		game.ParticipantIDs[0] == game.ParticipantIDs[1] || game.AssignmentID == uuid.Nil ||
		game.AssignmentRevision < 1 || game.PlanRevisionID == uuid.Nil || game.SnapshotID == uuid.Nil ||
		game.ContentDigest == [sha256.Size]byte{} ||
		game.DeadlineSeconds != waveStartDeadlineSeconds {
		return waveStartError("invalid playable Game authority")
	}
	return nil
}

func validateWaveStartGameSeries(scope StartScope, game GameAuthority) error {
	if err := game.Series.Validate(); err != nil || game.Series.Series.ID != game.Scope.SeriesID ||
		game.Series.Series.TournamentID != scope.TournamentID ||
		(game.Series.Series.State != domain.SeriesStateReady &&
			game.Series.Series.State != domain.SeriesStateActive) {
		return waveStartError("invalid playable Series")
	}
	if game.Series.Series.FirstParticipantID != game.ParticipantIDs[0] ||
		game.Series.Series.SecondParticipantID != game.ParticipantIDs[1] {
		return waveStartError("participant order does not match Series")
	}
	_, err := waveStartSlotIndex(game.Series.Series, game.Scope)
	return err
}

//nolint:gocyclo // One fail-closed pass validates Game coverage and the optional Swiss bye.
func validateStartedWaveGames(record StartRecord) error {
	members := make(map[uuid.UUID]bool, len(record.Wave.Members))
	for _, member := range record.Wave.Members {
		members[member.ParticipantID] = false
	}
	for _, game := range record.Games {
		if err := validateWaveStartGameAuthority(record.Scope, GameAuthority{
			Scope: game.Scope, ParticipantIDs: game.ParticipantIDs, Series: game.Series,
			AssignmentID: game.AssignmentID, AssignmentRevision: game.AssignmentRevision,
			PlanRevisionID: game.PlanRevisionID, SnapshotID: game.SnapshotID,
			ContentDigest: game.ContentDigest, DeadlineSeconds: game.DeadlineSeconds,
		}); err != nil {
			return err
		}
		if !game.DeliveryEnabled || !game.StartedAt.Equal(record.StartedAt) ||
			game.DeadlineSeconds < 1 ||
			!game.Deadline.Equal(record.StartedAt.Add(time.Duration(game.DeadlineSeconds)*time.Second)) ||
			game.Series.Series.State != domain.SeriesStateActive {
			return waveStartError("started Game does not share the persisted start")
		}
		slotIndex, err := waveStartSlotIndex(game.Series.Series, game.Scope)
		if err != nil {
			return err
		}
		attempts := game.Series.Series.Slots[slotIndex].Attempts
		if attempts[len(attempts)-1].State != domain.GameStateActive {
			return waveStartError("started Game attempt is not active")
		}
		for _, participantID := range game.ParticipantIDs {
			if members[participantID] {
				return waveStartError("started participant is duplicated")
			}
			members[participantID] = true
		}
	}
	for participantID, covered := range members {
		if !covered && !sameWaveStartUUID(record.ByeParticipantID, participantID) {
			return waveStartError("Wave start omitted a participant")
		}
	}
	return validateWaveStartBye(record.ByeParticipantID, members)
}

func validateWaveStartBye(byeParticipantID *uuid.UUID, members map[uuid.UUID]bool) error {
	if byeParticipantID == nil {
		return nil
	}
	if *byeParticipantID == uuid.Nil || members[*byeParticipantID] {
		return waveStartError("bye participant is invalid or has a playable Game")
	}
	if _, exists := members[*byeParticipantID]; !exists {
		return waveStartError("bye participant is not a Wave member")
	}
	return nil
}

func sameWaveStartUUID(value *uuid.UUID, expected uuid.UUID) bool {
	return value != nil && *value == expected
}

func waveStartSlotIndex(series domain.Series, scope gamedomain.Scope) (int, error) {
	for index, slot := range series.Slots {
		if slot.ID != scope.SlotID {
			continue
		}
		if len(slot.Attempts) == 0 || slot.Attempts[len(slot.Attempts)-1].ID != scope.GameID {
			return 0, waveStartError("scope does not identify the current Game attempt")
		}
		return index, nil
	}
	return 0, waveStartError("scope does not identify a Series slot")
}

func waveStartAuthorityMatchesCommand(
	authority StartAuthority,
	command StartCommand,
	startedAt time.Time,
) bool {
	return authority.Revisions == command.ExpectedRevisions &&
		authority.Revisions.ProjectionRevision == command.ExpectedProjectionRevision &&
		authority.Wave.State == domain.WaveStateReady && authority.Wave.ReadyWindow != nil &&
		authority.Wave.ReadyWindow.ID == command.Scope.WindowID &&
		!startedAt.Before(authority.Wave.ReadyWindow.OpenedAt) &&
		!startedAt.After(authority.Wave.ReadyWindow.Deadline)
}

func validWaveStartScope(scope StartScope) bool {
	return scope.TournamentID != uuid.Nil && scope.WaveID != uuid.Nil && scope.WindowID != uuid.Nil
}

func validGameScope(scope gamedomain.Scope) bool {
	return scope.IsValid()
}

func waveStartError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidWaveStart, fmt.Sprintf(format, arguments...))
}
