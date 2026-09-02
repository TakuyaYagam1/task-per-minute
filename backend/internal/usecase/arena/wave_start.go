package arena

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

const waveStartAttempts = 2

var (
	ErrInvalidWaveStart           = errors.New("invalid Arena Wave start")
	ErrWaveStartAuthorityConflict = errors.New("arena Wave start authority conflict")
	ErrWaveStartConflict          = errors.New("arena Wave start commit conflict")
)

type WaveStartScope struct {
	TournamentID uuid.UUID
	WaveID       uuid.UUID
	WindowID     uuid.UUID
}

type WaveStartGameAuthority struct {
	Scope              GameScope
	ParticipantIDs     [2]uuid.UUID
	Series             SeriesExecution
	AssignmentID       uuid.UUID
	AssignmentRevision int64
	PlanRevisionID     uuid.UUID
	SnapshotID         uuid.UUID
	ContentDigest      [sha256.Size]byte
	DeadlineSeconds    int
}

type WaveStartAuthority struct {
	Scope     WaveStartScope
	Revision  int64
	Revisions ReadyWindowSourceRevisions
	Wave      domain.ArenaWave
	Games     []WaveStartGameAuthority
	Current   *WaveStartRecord
}

type StartWaveCommand struct {
	Scope             WaveStartScope
	CommandID         uuid.UUID
	ExpectedRevisions ReadyWindowSourceRevisions
}

type StartedWaveGame struct {
	Scope              GameScope
	ParticipantIDs     [2]uuid.UUID
	Series             SeriesExecution
	AssignmentID       uuid.UUID
	AssignmentRevision int64
	PlanRevisionID     uuid.UUID
	SnapshotID         uuid.UUID
	ContentDigest      [sha256.Size]byte
	DeadlineSeconds    int
	StartedAt          time.Time
	Deadline           time.Time
	DeliveryEnabled    bool
}

type WaveStartRecord struct {
	Scope                     WaveStartScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	Revisions                 ReadyWindowSourceRevisions
	Wave                      domain.ArenaWave
	Games                     []StartedWaveGame
	StartedAt                 time.Time
}

// WaveStartRepository owns the transaction that revalidates every source,
// assignment and task digest, consumes one ready window, transitions all
// playable Games, persists one start, derives deadlines and enables delivery.
type WaveStartRepository interface {
	LoadWaveStartAuthority(ctx context.Context, scope WaveStartScope) (WaveStartAuthority, error)
	CommitWaveStart(ctx context.Context, record WaveStartRecord) (*WaveStartRecord, bool, error)
}

type WaveStartUseCase struct {
	repository WaveStartRepository
	clock      Clock
	observer   observability.ArenaEventObserver
}

func NewWaveStartUseCase(
	repository WaveStartRepository,
	clock Clock,
	observers ...observability.ArenaEventObserver,
) *WaveStartUseCase {
	return &WaveStartUseCase{
		repository: repository,
		clock:      clock,
		observer:   firstArenaEventObserver(observers...),
	}
}

func (u *WaveStartUseCase) Start(
	ctx context.Context,
	command StartWaveCommand,
) (record *WaveStartRecord, changed bool, err error) {
	var clock Clock
	var observer observability.ArenaEventObserver
	if u != nil {
		clock = u.clock
		observer = u.observer
	}
	measurement := newArenaEventMeasurement(clock, observer)
	phase := arenaEventPhaseValidation
	retried := false
	var revision int64
	defer func() {
		emitWaveStartEvent(
			ctx,
			measurement,
			command,
			record,
			changed,
			retried,
			err,
			phase,
			revision,
		)
	}()

	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateStartWaveCommand(command); err != nil {
		return nil, false, err
	}
	phase = arenaEventPhaseClock
	startedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(startedAt) {
		return nil, false, domain.ErrValidation
	}

	for range waveStartAttempts {
		phase = arenaEventPhaseAuthorityLookup
		attemptRecord, attemptChanged, retry, attemptErr := u.startAttempt(
			ctx,
			command,
			startedAt,
			&phase,
			&revision,
		)
		if retry {
			retried = true
			continue
		}
		return attemptRecord, attemptChanged, attemptErr
	}
	return nil, false, ErrWaveStartConflict
}

func (u *WaveStartUseCase) startAttempt(
	ctx context.Context,
	command StartWaveCommand,
	startedAt time.Time,
	phase *string,
	revision *int64,
) (*WaveStartRecord, bool, bool, error) {
	authority, err := u.repository.LoadWaveStartAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("WaveStartUseCase - load authority: %w", err)
	}
	*revision = authority.Revision
	*phase = arenaEventPhaseAuthorityValidation
	if err := validateWaveStartAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Current != nil {
		*phase = arenaEventPhaseResultValidation
		record, reconcileErr := reconcileWaveStart(*authority.Current, command)
		return record, false, false, reconcileErr
	}
	if !waveStartAuthorityMatchesCommand(authority, command, startedAt) {
		return nil, false, false, ErrWaveStartAuthorityConflict
	}
	*phase = arenaEventPhaseBuild
	record, err := buildWaveStartRecord(command, authority, startedAt)
	if err != nil {
		return nil, false, false, err
	}
	return u.commitWaveStart(ctx, command, record, phase)
}

func (u *WaveStartUseCase) commitWaveStart(
	ctx context.Context,
	command StartWaveCommand,
	record WaveStartRecord,
	phase *string,
) (*WaveStartRecord, bool, bool, error) {
	*phase = arenaEventPhaseCommit
	committed, changed, err := u.repository.CommitWaveStart(ctx, record)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("WaveStartUseCase - commit start: %w", err)
	}
	*phase = arenaEventPhaseResultValidation
	if committed == nil || committed.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	result, reconcileErr := reconcileWaveStart(*committed, command)
	if reconcileErr != nil || (changed && !waveStartRecordsEqual(*result, record)) {
		return nil, false, false, domain.ErrInternal
	}
	return result, changed, false, nil
}

func (r WaveStartRecord) Validate() error {
	if err := validateWaveStartRecordIdentity(r); err != nil {
		return err
	}
	if err := validateWaveStartRecordWave(r); err != nil {
		return err
	}
	return validateStartedWaveGames(r)
}

func validateWaveStartRecordIdentity(r WaveStartRecord) error {
	if !validWaveStartScope(r.Scope) || r.CommandID == uuid.Nil ||
		r.ExpectedAuthorityRevision < 1 || !validReadyWindowSourceRevisions(r.Revisions) ||
		!validArenaServerTime(r.StartedAt) || len(r.Games) == 0 {
		return waveStartError("invalid record identity, revision or start")
	}
	return nil
}

func validateWaveStartRecordWave(r WaveStartRecord) error {
	if err := r.Wave.Validate(); err != nil || r.Wave.ID != r.Scope.WaveID ||
		r.Wave.TournamentID != r.Scope.TournamentID || r.Wave.RevisionID != r.Revisions.WaveRevisionID ||
		r.Wave.State != domain.ArenaWaveStateActive || r.Wave.StartedAt == nil ||
		!r.Wave.StartedAt.Equal(r.StartedAt) || r.Wave.ReadyWindow == nil ||
		r.Wave.ReadyWindow.ID != r.Scope.WindowID ||
		r.Wave.ReadyWindow.State != domain.ArenaReadyWindowStateConsumed {
		return waveStartError("record does not contain one consumed Wave start")
	}
	return nil
}

func buildWaveStartRecord(
	command StartWaveCommand,
	authority WaveStartAuthority,
	startedAt time.Time,
) (WaveStartRecord, error) {
	wave := cloneArenaWaveExecution(authority.Wave)
	changed, err := wave.Start(command.Scope.WindowID, startedAt)
	if err != nil || !changed {
		return WaveStartRecord{}, waveStartError("start Wave: %v", err)
	}
	startedGames := make([]StartedWaveGame, len(authority.Games))
	for index, game := range authority.Games {
		started, err := startWaveGame(game, startedAt)
		if err != nil {
			return WaveStartRecord{}, err
		}
		startedGames[index] = started
	}
	record := WaveStartRecord{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision, Revisions: authority.Revisions,
		Wave: wave, Games: startedGames, StartedAt: startedAt,
	}
	if err := record.Validate(); err != nil {
		return WaveStartRecord{}, err
	}
	return cloneWaveStartRecord(record), nil
}

func startWaveGame(authority WaveStartGameAuthority, startedAt time.Time) (StartedWaveGame, error) {
	series := cloneSeriesExecution(authority.Series)
	if series.Series.State == domain.ArenaSeriesStateReady {
		active, changed, err := TransitionSeriesExecution(series, SeriesExecutionTransitionCommand{
			NextState: domain.ArenaSeriesStateActive,
		})
		if err != nil || !changed {
			return StartedWaveGame{}, waveStartError("activate Series: %v", err)
		}
		series = active
	}
	slotIndex, err := waveStartSlotIndex(series.Series, authority.Scope)
	if err != nil {
		return StartedWaveGame{}, err
	}
	slot := series.Series.Slots[slotIndex]
	attempt := slot.Attempts[len(slot.Attempts)-1]
	if attempt.State == domain.ArenaGameStatePlanned {
		ready, changed, transitionErr := TransitionGameSlotAttempt(slot, GameAttemptTransitionCommand{
			GameID: attempt.ID, ExpectedAttemptNo: attempt.AttemptNo,
			ExpectedState: attempt.State, NextState: domain.ArenaGameStateReady,
		})
		if transitionErr != nil || !changed {
			return StartedWaveGame{}, waveStartError("prepare Game: %v", transitionErr)
		}
		slot = ready
		attempt = slot.Attempts[len(slot.Attempts)-1]
	}
	active, changed, err := TransitionGameSlotAttempt(slot, GameAttemptTransitionCommand{
		GameID: attempt.ID, ExpectedAttemptNo: attempt.AttemptNo,
		ExpectedState: attempt.State, NextState: domain.ArenaGameStateActive,
	})
	if err != nil || !changed {
		return StartedWaveGame{}, waveStartError("activate Game: %v", err)
	}
	series.Series.Slots[slotIndex] = active
	return StartedWaveGame{
		Scope: authority.Scope, ParticipantIDs: authority.ParticipantIDs, Series: series,
		AssignmentID: authority.AssignmentID, AssignmentRevision: authority.AssignmentRevision,
		PlanRevisionID: authority.PlanRevisionID, SnapshotID: authority.SnapshotID,
		ContentDigest: authority.ContentDigest, DeadlineSeconds: authority.DeadlineSeconds,
		StartedAt:       startedAt,
		Deadline:        startedAt.Add(time.Duration(authority.DeadlineSeconds) * time.Second),
		DeliveryEnabled: true,
	}, nil
}

func validateStartWaveCommand(command StartWaveCommand) error {
	if !validWaveStartScope(command.Scope) || command.CommandID == uuid.Nil ||
		!validReadyWindowSourceRevisions(command.ExpectedRevisions) {
		return waveStartError("invalid command identity or revisions")
	}
	return nil
}

func validateWaveStartAuthority(authority WaveStartAuthority) error {
	if !validWaveStartScope(authority.Scope) || authority.Revision < 1 ||
		!validReadyWindowSourceRevisions(authority.Revisions) {
		return waveStartError("invalid authority identity or revisions")
	}
	if authority.Current != nil {
		if err := authority.Current.Validate(); err != nil || authority.Current.Scope != authority.Scope {
			return waveStartError("invalid current start")
		}
		return nil
	}
	if err := authority.Wave.Validate(); err != nil || authority.Wave.ID != authority.Scope.WaveID ||
		authority.Wave.TournamentID != authority.Scope.TournamentID ||
		authority.Wave.RevisionID != authority.Revisions.WaveRevisionID ||
		authority.Wave.ReadyWindow == nil || authority.Wave.ReadyWindow.ID != authority.Scope.WindowID ||
		authority.Wave.State != domain.ArenaWaveStateReady {
		return ErrWaveStartAuthorityConflict
	}
	return validateWaveStartGames(authority)
}

func validateWaveStartGames(authority WaveStartAuthority) error {
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
		if !covered {
			return waveStartError("Wave member %s has no playable Game", participantID)
		}
	}
	return nil
}

func validateWaveStartGameAuthority(scope WaveStartScope, game WaveStartGameAuthority) error {
	if err := validateWaveStartGameIdentity(scope, game); err != nil {
		return err
	}
	return validateWaveStartGameSeries(scope, game)
}

func validateWaveStartGameIdentity(scope WaveStartScope, game WaveStartGameAuthority) error {
	if !validGameScope(game.Scope) || game.Scope.TournamentID != scope.TournamentID ||
		game.ParticipantIDs[0] == uuid.Nil || game.ParticipantIDs[1] == uuid.Nil ||
		game.ParticipantIDs[0] == game.ParticipantIDs[1] || game.AssignmentID == uuid.Nil ||
		game.AssignmentRevision < 1 || game.PlanRevisionID == uuid.Nil || game.SnapshotID == uuid.Nil ||
		game.ContentDigest == [sha256.Size]byte{} || game.DeadlineSeconds < 1 {
		return waveStartError("invalid playable Game authority")
	}
	return nil
}

func validateWaveStartGameSeries(scope WaveStartScope, game WaveStartGameAuthority) error {
	if err := game.Series.Validate(); err != nil || game.Series.Series.ID != game.Scope.SeriesID ||
		game.Series.Series.TournamentID != scope.TournamentID ||
		(game.Series.Series.State != domain.ArenaSeriesStateReady &&
			game.Series.Series.State != domain.ArenaSeriesStateActive) {
		return waveStartError("invalid playable Series")
	}
	if game.Series.Series.FirstParticipantID != game.ParticipantIDs[0] ||
		game.Series.Series.SecondParticipantID != game.ParticipantIDs[1] {
		return waveStartError("participant order does not match Series")
	}
	_, err := waveStartSlotIndex(game.Series.Series, game.Scope)
	return err
}

func validateStartedWaveGames(record WaveStartRecord) error {
	members := make(map[uuid.UUID]bool, len(record.Wave.Members))
	for _, member := range record.Wave.Members {
		members[member.ParticipantID] = false
	}
	for _, game := range record.Games {
		if err := validateWaveStartGameAuthority(record.Scope, WaveStartGameAuthority{
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
			game.Series.Series.State != domain.ArenaSeriesStateActive {
			return waveStartError("started Game does not share the persisted start")
		}
		slotIndex, err := waveStartSlotIndex(game.Series.Series, game.Scope)
		if err != nil {
			return err
		}
		attempts := game.Series.Series.Slots[slotIndex].Attempts
		if attempts[len(attempts)-1].State != domain.ArenaGameStateActive {
			return waveStartError("started Game attempt is not active")
		}
		for _, participantID := range game.ParticipantIDs {
			if members[participantID] {
				return waveStartError("started participant is duplicated")
			}
			members[participantID] = true
		}
	}
	for _, covered := range members {
		if !covered {
			return waveStartError("Wave start omitted a participant")
		}
	}
	return nil
}

func waveStartSlotIndex(series domain.ArenaSeries, scope GameScope) (int, error) {
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
	authority WaveStartAuthority,
	command StartWaveCommand,
	startedAt time.Time,
) bool {
	return authority.Revisions == command.ExpectedRevisions &&
		authority.Wave.State == domain.ArenaWaveStateReady && authority.Wave.ReadyWindow != nil &&
		authority.Wave.ReadyWindow.ID == command.Scope.WindowID &&
		!startedAt.Before(authority.Wave.ReadyWindow.OpenedAt) &&
		!startedAt.After(authority.Wave.ReadyWindow.Deadline)
}

func reconcileWaveStart(
	record WaveStartRecord,
	command StartWaveCommand,
) (*WaveStartRecord, error) {
	if err := record.Validate(); err != nil {
		return nil, domain.ErrInternal
	}
	if record.Scope != command.Scope || record.CommandID != command.CommandID ||
		record.Revisions != command.ExpectedRevisions {
		return nil, ErrWaveStartConflict
	}
	clone := cloneWaveStartRecord(record)
	return &clone, nil
}

func waveStartRecordsEqual(first, second WaveStartRecord) bool {
	if first.Scope != second.Scope || first.CommandID != second.CommandID ||
		first.ExpectedAuthorityRevision != second.ExpectedAuthorityRevision ||
		first.Revisions != second.Revisions || !first.StartedAt.Equal(second.StartedAt) ||
		!arenaWavesEqual(first.Wave, second.Wave) || len(first.Games) != len(second.Games) {
		return false
	}
	for index := range first.Games {
		if first.Games[index].Scope != second.Games[index].Scope ||
			first.Games[index].AssignmentID != second.Games[index].AssignmentID ||
			!first.Games[index].Deadline.Equal(second.Games[index].Deadline) {
			return false
		}
	}
	return true
}

func validWaveStartScope(scope WaveStartScope) bool {
	return scope.TournamentID != uuid.Nil && scope.WaveID != uuid.Nil && scope.WindowID != uuid.Nil
}

func validGameScope(scope GameScope) bool {
	return scope.TournamentID != uuid.Nil && scope.SeriesID != uuid.Nil &&
		scope.SlotID != uuid.Nil && scope.GameID != uuid.Nil
}

func cloneWaveStartRecord(record WaveStartRecord) WaveStartRecord {
	clone := record
	clone.Wave = cloneArenaWaveExecution(record.Wave)
	clone.Games = make([]StartedWaveGame, len(record.Games))
	for index, game := range record.Games {
		clone.Games[index] = game
		clone.Games[index].Series = cloneSeriesExecution(game.Series)
	}
	return clone
}

func waveStartError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidWaveStart, fmt.Sprintf(format, arguments...))
}
