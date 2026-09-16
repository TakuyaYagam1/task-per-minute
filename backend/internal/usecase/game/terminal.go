package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

const failedAttemptCommitAttempts = 2

var (
	ErrInvalidFailedAttempt      = errors.New("invalid failed game attempt")
	ErrFailedAttemptUnavailable  = errors.New("failed game attempt is unavailable")
	ErrFailedAttemptConflict     = errors.New("failed game attempt authority conflict")
	ErrFailedAttemptCommandReuse = errors.New("failed game attempt command was reused")
)

type Expectation struct {
	AttemptNo  int
	State      domain.GameState
	SnapshotID uuid.UUID
	Category   domain.Category
}

type AttemptRevisionSet struct {
	GameResultRevisionID domain.OfficialResultRevisionID
	ScoreRevisionID      domain.SeriesScoreRevisionID
	RouteEvidenceID      uuid.UUID
	AuditEventID         uuid.UUID
	OutboxEventID        uuid.UUID
	ProjectionRevisionID uuid.UUID
}

type AttemptCommand struct {
	Scope        domain.FailedAttemptScope
	CommandID    uuid.UUID
	FailureClass gamedomain.FailureClass
	Expected     Expectation
	Revisions    AttemptRevisionSet
}

type AttemptAuthority struct {
	Scope                        domain.FailedAttemptScope
	Revision                     int64
	Wave                         domain.Wave
	Series                       seriesdomain.Execution
	ActiveSnapshotID             uuid.UUID
	CurrentOrdinal               int
	CurrentProjectionRevision    int64
	CurrentGameResultRevisionIDs []domain.OfficialResultRevisionID
	Current                      *AttemptRecord
}

type AttemptGameResultRevision = reconnectusecase.AttemptGameResultRevision
type WaveMemberRoute = reconnectusecase.WaveMemberRoute

type AttemptRecord struct {
	Scope                     domain.FailedAttemptScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	ActiveSnapshotID          uuid.UUID
	Failure                   gamedomain.FailureDecision
	Series                    seriesdomain.Execution
	Game                      domain.Game
	AttemptGameResultRevision AttemptGameResultRevision
	ScoreRevision             seriesdomain.ScoreRevision
	WaveRoute                 WaveMemberRoute
	Evidence                  seriesdomain.SettlementEvidence
	TerminalizedAt            time.Time
}

type AttemptUseCase struct {
	repository AttemptRepository
	clock      AttemptClock
}

func AttemptNewUseCase(repository AttemptRepository, clock AttemptClock) *AttemptUseCase {
	return &AttemptUseCase{repository: repository, clock: clock}
}

func (u *AttemptUseCase) Terminalize(
	ctx context.Context,
	command AttemptCommand,
) (*AttemptRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateFailedAttemptCommand(command); err != nil {
		return nil, false, err
	}
	terminalizedAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(terminalizedAt) {
		return nil, false, domain.ErrValidation
	}

	for range failedAttemptCommitAttempts {
		record, changed, retry, err := u.terminalizeAttempt(ctx, command, terminalizedAt)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrFailedAttemptConflict
}

func (u *AttemptUseCase) terminalizeAttempt(
	ctx context.Context,
	command AttemptCommand,
	terminalizedAt time.Time,
) (*AttemptRecord, bool, bool, error) {
	authority, err := u.repository.LoadFailedAttemptAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("FailedAttemptUseCase - load authority: %w", err)
	}
	if err := validateFailedAttemptAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != command.Scope {
		return nil, false, false, failedAttemptError("authority scope does not match command")
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileFailedAttempt(*authority.Current, command)
		return current, false, false, reconcileErr
	}
	record, err := buildFailedAttemptRecord(command, authority, terminalizedAt)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitFailedAttempt(ctx, record)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("FailedAttemptUseCase - commit attempt: %w", err)
	}
	if !validCommittedFailedAttempt(committed, record, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneFailedAttemptRecord(*committed)
	return &result, changed, false, nil
}

func (r AttemptRecord) Validate() error {
	if !r.Scope.IsValid() || r.CommandID == uuid.Nil || r.ExpectedAuthorityRevision < 1 ||
		r.ActiveSnapshotID == uuid.Nil || !domain.IsValidServerTime(r.TerminalizedAt) ||
		r.Failure.Validate() != nil {
		return failedAttemptError("invalid record identity or failure")
	}
	if err := validateFailedAttemptGame(r); err != nil {
		return err
	}
	if err := validateFailedAttemptSeries(r); err != nil {
		return err
	}
	if err := validateFailedAttemptRevisions(r); err != nil {
		return err
	}
	if err := validateFailedAttemptWaveRoute(r); err != nil {
		return err
	}
	return validateFailedAttemptEvidence(r)
}

func (e Expectation) IsValid() bool {
	return e.AttemptNo > 0 && e.State == domain.GameStateActive &&
		e.SnapshotID != uuid.Nil && e.Category.IsValid()
}

func validateFailedAttemptCommand(command AttemptCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil || !command.Expected.IsValid() {
		return failedAttemptError("invalid command identity or expectation")
	}
	if _, err := gamedomain.ClassifyFailure(command.FailureClass, command.Expected.Category); err != nil {
		return err
	}
	identities := []uuid.UUID{
		command.CommandID,
		command.Revisions.GameResultRevisionID.UUID(),
		command.Revisions.ScoreRevisionID.UUID(),
		command.Revisions.RouteEvidenceID,
		command.Revisions.AuditEventID,
		command.Revisions.OutboxEventID,
		command.Revisions.ProjectionRevisionID,
	}
	if !attemptUniqueNonZeroUUIDs(identities) {
		return failedAttemptError("missing or duplicate revision identity")
	}
	return nil
}

func validateFailedAttemptAuthority(authority AttemptAuthority) error {
	if !validFailedAttemptAuthorityHeader(authority) {
		return failedAttemptError("invalid authority identity or live state")
	}
	if err := seriesdomain.ValidateGameResultRevisionIDs(authority.CurrentGameResultRevisionIDs); err != nil {
		return failedAttemptError("invalid retained Game revisions: %v", err)
	}
	if authority.CurrentOrdinal > 0 && authority.Series.Series.CurrentScoreRevisionID == nil {
		return failedAttemptError("revision ordinal has no current score revision")
	}
	if authority.Current == nil {
		return validateOpenFailedAttemptAuthority(authority)
	}
	return validateCurrentFailedAttemptAuthority(authority)
}

func validFailedAttemptAuthorityHeader(authority AttemptAuthority) bool {
	return authority.Scope.IsValid() && authority.Revision >= 1 &&
		authority.ActiveSnapshotID != uuid.Nil && authority.CurrentOrdinal >= 0 &&
		authority.CurrentProjectionRevision >= 1 && authority.Wave.Validate() == nil &&
		authority.Wave.ID == authority.Scope.WaveID &&
		authority.Wave.TournamentID == authority.Scope.TournamentID &&
		validFailedAttemptWaveState(authority) && authority.Series.Validate() == nil &&
		authority.Series.Series.ID == authority.Scope.SeriesID &&
		authority.Series.Series.TournamentID == authority.Scope.TournamentID
}

func validFailedAttemptWaveState(authority AttemptAuthority) bool {
	return authority.Wave.State == domain.WaveStateActive ||
		(authority.Current != nil && authority.Wave.State == domain.WaveStateCompleted)
}

func validateOpenFailedAttemptAuthority(authority AttemptAuthority) error {
	game, slot, found := currentFailedAttempt(authority.Series.Series, authority.Scope)
	if authority.Series.Series.State != domain.SeriesStateActive || !found ||
		game.State != domain.GameStateActive || !slot.Category.IsValid() {
		return ErrFailedAttemptUnavailable
	}
	return nil
}

func validateCurrentFailedAttemptAuthority(authority AttemptAuthority) error {
	if authority.Current == nil || authority.Current.Validate() != nil ||
		authority.Current.Scope != authority.Scope {
		return failedAttemptError("invalid current terminalization")
	}
	return nil
}

func buildFailedAttemptRecord(
	command AttemptCommand,
	authority AttemptAuthority,
	terminalizedAt time.Time,
) (AttemptRecord, error) {
	if authority.ActiveSnapshotID != command.Expected.SnapshotID {
		return AttemptRecord{}, ErrFailedAttemptConflict
	}
	game, slot, found := currentFailedAttempt(authority.Series.Series, command.Scope)
	if !found || game.AttemptNo != command.Expected.AttemptNo || game.State != command.Expected.State ||
		slot.Category != command.Expected.Category {
		return AttemptRecord{}, ErrFailedAttemptConflict
	}
	failure, err := gamedomain.ClassifyFailure(command.FailureClass, slot.Category)
	if err != nil {
		return AttemptRecord{}, err
	}
	working := seriesdomain.CloneExecution(authority.Series)
	transitioned, changed, err := gamedomain.TransitionSlotAttempt(slot, gamedomain.TransitionCommand{
		GameID: game.ID, ExpectedAttemptNo: game.AttemptNo, ExpectedState: game.State,
		NextState: failure.GameState,
		Terminal: &gamedomain.TerminalEvidence{
			Reason: failure.Reason, ResultRevisionID: &command.Revisions.GameResultRevisionID,
		},
	})
	if err != nil || !changed {
		return AttemptRecord{}, failedAttemptError("void Game: %v", err)
	}
	working.Series.Slots[len(working.Series.Slots)-1] = transitioned
	working.Series.CurrentScoreRevisionID = &command.Revisions.ScoreRevisionID
	working, changed, err = seriesdomain.Transition(working, seriesdomain.TransitionCommand{
		NextState: failure.SeriesState,
	})
	if err != nil || !changed {
		return AttemptRecord{}, failedAttemptError("route Series: %v", err)
	}
	voidGame := attemptCloneGame(transitioned.Attempts[len(transitioned.Attempts)-1])
	resultIDs := append(
		[]domain.OfficialResultRevisionID(nil),
		authority.CurrentGameResultRevisionIDs...,
	)
	resultIDs = append(resultIDs, command.Revisions.GameResultRevisionID)
	record := AttemptRecord{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision,
		ActiveSnapshotID:          authority.ActiveSnapshotID, Failure: failure,
		Series: working, Game: voidGame,
		AttemptGameResultRevision: AttemptGameResultRevision{
			Ordinal: authority.CurrentOrdinal + 1,
			ID:      command.Revisions.GameResultRevisionID, GameID: game.ID,
			Reason: failure.Reason, RecordedAt: terminalizedAt,
		},
		ScoreRevision: seriesdomain.ScoreRevision{
			ID: command.Revisions.ScoreRevisionID, SeriesID: command.Scope.SeriesID,
			FirstParticipantID:  authority.Series.Series.FirstParticipantID,
			SecondParticipantID: authority.Series.Series.SecondParticipantID,
			PreviousRevisionID: attemptCloneSeriesScoreRevisionIDPointer(
				authority.Series.Series.CurrentScoreRevisionID,
			),
			Ordinal: authority.CurrentOrdinal + 2, Format: authority.Series.Series.Format,
			ScoreBefore: authority.Series.Series.Score, ScoreAfter: authority.Series.Series.Score,
			GameResultRevisionIDs: resultIDs, RecordedAt: terminalizedAt,
		},
		WaveRoute: WaveMemberRoute{
			ID: command.Revisions.RouteEvidenceID, WaveID: command.Scope.WaveID,
			SeriesID: command.Scope.SeriesID, SlotID: command.Scope.SlotID,
			GameID: command.Scope.GameID, Category: failure.CategoryCutoff,
			RoutedAt: terminalizedAt,
		},
		Evidence: seriesdomain.SettlementEvidence{
			AuditEventID:             command.Revisions.AuditEventID,
			OutboxEventID:            command.Revisions.OutboxEventID,
			ProjectionRevisionID:     command.Revisions.ProjectionRevisionID,
			SourceProjectionRevision: authority.CurrentProjectionRevision,
			ProjectionRevision:       authority.CurrentProjectionRevision + 1,
			RecordedAt:               terminalizedAt,
		},
		TerminalizedAt: terminalizedAt,
	}
	if err := record.Validate(); err != nil {
		return AttemptRecord{}, err
	}
	return cloneFailedAttemptRecord(record), nil
}

func validateFailedAttemptGame(record AttemptRecord) error {
	game := record.Game
	if game.Validate() != nil || game.ID != record.Scope.GameID ||
		game.SlotID != record.Scope.SlotID || game.State != record.Failure.GameState ||
		game.ResultReason != record.Failure.Reason || game.WinnerID != nil ||
		game.ResultRevisionID == nil || *game.ResultRevisionID != record.AttemptGameResultRevision.ID {
		return failedAttemptError("invalid void Game evidence")
	}
	revision := record.AttemptGameResultRevision
	if revision.Ordinal < 1 || revision.ID.IsZero() || revision.GameID != game.ID ||
		revision.Reason != record.Failure.Reason ||
		!revision.RecordedAt.Equal(record.TerminalizedAt) {
		return failedAttemptError("invalid Game result revision")
	}
	return nil
}

func validateFailedAttemptSeries(record AttemptRecord) error {
	series := record.Series.Series
	if record.Series.Validate() != nil || series.ID != record.Scope.SeriesID ||
		series.TournamentID != record.Scope.TournamentID || series.State != record.Failure.SeriesState ||
		series.WinnerID != nil || series.CurrentResultRevisionID != nil ||
		series.CurrentScoreRevisionID == nil || *series.CurrentScoreRevisionID != record.ScoreRevision.ID ||
		series.Score != record.ScoreRevision.ScoreAfter {
		return failedAttemptError("invalid replay-required Series")
	}
	game, slot, found := currentFailedAttempt(series, record.Scope)
	if !found || game.ID != record.Game.ID || game.State != domain.GameStateVoid ||
		slot.Category != record.Failure.CategoryCutoff {
		return failedAttemptError("Series lost the failed attempt")
	}
	return nil
}

func validateFailedAttemptRevisions(record AttemptRecord) error {
	revision := record.ScoreRevision
	if revision.ID.IsZero() || revision.SeriesID != record.Scope.SeriesID ||
		revision.FirstParticipantID != record.Series.Series.FirstParticipantID ||
		revision.SecondParticipantID != record.Series.Series.SecondParticipantID ||
		revision.Format != record.Series.Series.Format || revision.Ordinal < 2 ||
		revision.Ordinal != record.AttemptGameResultRevision.Ordinal+1 ||
		revision.ScoreBefore != revision.ScoreAfter ||
		revision.ScoreAfter != record.Series.Series.Score ||
		!revision.RecordedAt.Equal(record.TerminalizedAt) ||
		len(revision.GameResultRevisionIDs) == 0 ||
		revision.GameResultRevisionIDs[len(revision.GameResultRevisionIDs)-1] != record.AttemptGameResultRevision.ID {
		return failedAttemptError("invalid unchanged-score revision")
	}
	if err := seriesdomain.ValidateGameResultRevisionIDs(revision.GameResultRevisionIDs); err != nil {
		return failedAttemptError("invalid score provenance: %v", err)
	}
	return nil
}

func validateFailedAttemptWaveRoute(record AttemptRecord) error {
	route := record.WaveRoute
	if route.ID == uuid.Nil || route.WaveID != record.Scope.WaveID ||
		route.SeriesID != record.Scope.SeriesID || route.SlotID != record.Scope.SlotID ||
		route.GameID != record.Scope.GameID || route.Category != record.Failure.CategoryCutoff ||
		!route.RoutedAt.Equal(record.TerminalizedAt) {
		return failedAttemptError("invalid Wave membership route")
	}
	return nil
}

func validateFailedAttemptEvidence(record AttemptRecord) error {
	evidence := record.Evidence
	identities := []uuid.UUID{
		record.CommandID,
		record.AttemptGameResultRevision.ID.UUID(),
		record.ScoreRevision.ID.UUID(),
		record.WaveRoute.ID,
		evidence.AuditEventID,
		evidence.OutboxEventID,
		evidence.ProjectionRevisionID,
	}
	if !attemptUniqueNonZeroUUIDs(identities) || evidence.SourceProjectionRevision < 1 ||
		evidence.ProjectionRevision != evidence.SourceProjectionRevision+1 ||
		!evidence.RecordedAt.Equal(record.TerminalizedAt) {
		return failedAttemptError("invalid commit evidence")
	}
	return nil
}

func currentFailedAttempt(
	series domain.Series,
	scope domain.FailedAttemptScope,
) (domain.Game, domain.GameSlot, bool) {
	if len(series.Slots) == 0 {
		return domain.Game{}, domain.GameSlot{}, false
	}
	slot := series.Slots[len(series.Slots)-1]
	if slot.ID != scope.SlotID || len(slot.Attempts) == 0 {
		return domain.Game{}, domain.GameSlot{}, false
	}
	game := slot.Attempts[len(slot.Attempts)-1]
	if game.ID != scope.GameID {
		return domain.Game{}, domain.GameSlot{}, false
	}
	return attemptCloneGame(game), attemptCloneGameSlot(slot), true
}

func failedAttemptError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidFailedAttempt, fmt.Sprintf(format, arguments...))
}
