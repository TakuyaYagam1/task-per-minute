package arena

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const failedAttemptCommitAttempts = 2

var (
	ErrInvalidFailedAttempt      = errors.New("invalid failed arena attempt")
	ErrFailedAttemptUnavailable  = errors.New("failed arena attempt is unavailable")
	ErrFailedAttemptConflict     = errors.New("failed arena attempt authority conflict")
	ErrFailedAttemptCommandReuse = errors.New("failed arena attempt command was reused")
)

type FailedAttemptScope struct {
	TournamentID        uuid.UUID
	WaveID              uuid.UUID
	SeriesID            uuid.UUID
	SlotID              uuid.UUID
	GameID              uuid.UUID
	AssignmentID        uuid.UUID
	AssignmentAttemptID uuid.UUID
}

type FailedAttemptExpectation struct {
	AttemptNo  int
	State      domain.ArenaGameState
	SnapshotID uuid.UUID
	Category   domain.Category
}

type FailedAttemptRevisionSet struct {
	GameResultRevisionID domain.ArenaOfficialResultRevisionID
	ScoreRevisionID      domain.ArenaSeriesScoreRevisionID
	RouteEvidenceID      uuid.UUID
	AuditEventID         uuid.UUID
	OutboxEventID        uuid.UUID
	ProjectionRevisionID uuid.UUID
}

type FailedAttemptCommand struct {
	Scope        FailedAttemptScope
	CommandID    uuid.UUID
	FailureClass NormalAttemptFailureClass
	Expected     FailedAttemptExpectation
	Revisions    FailedAttemptRevisionSet
}

type FailedAttemptAuthority struct {
	Scope                        FailedAttemptScope
	Revision                     int64
	Wave                         domain.ArenaWave
	Series                       SeriesExecution
	ActiveSnapshotID             uuid.UUID
	CurrentOrdinal               int
	CurrentProjectionRevision    int64
	CurrentGameResultRevisionIDs []domain.ArenaOfficialResultRevisionID
	Current                      *FailedAttemptRecord
}

type FailedGameResultRevision struct {
	Ordinal    int
	ID         domain.ArenaOfficialResultRevisionID
	GameID     uuid.UUID
	Reason     domain.ArenaGameResultReason
	RecordedAt time.Time
}

type FailedWaveMemberRoute struct {
	ID       uuid.UUID
	WaveID   uuid.UUID
	SeriesID uuid.UUID
	SlotID   uuid.UUID
	GameID   uuid.UUID
	Category domain.Category
	RoutedAt time.Time
}

type FailedAttemptRecord struct {
	Scope                     FailedAttemptScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	ActiveSnapshotID          uuid.UUID
	Failure                   NormalAttemptFailureDecision
	Series                    SeriesExecution
	Game                      domain.ArenaGame
	GameResultRevision        FailedGameResultRevision
	ScoreRevision             ArenaSettlementScoreRevision
	WaveRoute                 FailedWaveMemberRoute
	Evidence                  ArenaSettlementEvidence
	TerminalizedAt            time.Time
}

// FailedAttemptRepository owns one transaction that locks the current normal
// attempt and score head, writes the void Game and unchanged-score revisions,
// routes the old Wave membership, and advances the projection together.
type FailedAttemptRepository interface {
	LoadFailedAttemptAuthority(
		ctx context.Context,
		scope FailedAttemptScope,
	) (FailedAttemptAuthority, error)
	CommitFailedAttempt(
		ctx context.Context,
		record FailedAttemptRecord,
	) (*FailedAttemptRecord, bool, error)
}

type FailedAttemptUseCase struct {
	repository FailedAttemptRepository
	clock      Clock
}

func NewFailedAttemptUseCase(repository FailedAttemptRepository, clock Clock) *FailedAttemptUseCase {
	return &FailedAttemptUseCase{repository: repository, clock: clock}
}

func (u *FailedAttemptUseCase) Terminalize(
	ctx context.Context,
	command FailedAttemptCommand,
) (*FailedAttemptRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateFailedAttemptCommand(command); err != nil {
		return nil, false, err
	}
	terminalizedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(terminalizedAt) {
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

func (u *FailedAttemptUseCase) terminalizeAttempt(
	ctx context.Context,
	command FailedAttemptCommand,
	terminalizedAt time.Time,
) (*FailedAttemptRecord, bool, bool, error) {
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

func (r FailedAttemptRecord) Validate() error {
	if !r.Scope.IsValid() || r.CommandID == uuid.Nil || r.ExpectedAuthorityRevision < 1 ||
		r.ActiveSnapshotID == uuid.Nil || !validArenaServerTime(r.TerminalizedAt) ||
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

func (s FailedAttemptScope) IsValid() bool {
	return s.TournamentID != uuid.Nil && s.WaveID != uuid.Nil && s.SeriesID != uuid.Nil &&
		s.SlotID != uuid.Nil && s.GameID != uuid.Nil && s.AssignmentID != uuid.Nil &&
		s.AssignmentAttemptID != uuid.Nil
}

func (e FailedAttemptExpectation) IsValid() bool {
	return e.AttemptNo > 0 && e.State == domain.ArenaGameStateActive &&
		e.SnapshotID != uuid.Nil && e.Category.IsValid()
}

func validateFailedAttemptCommand(command FailedAttemptCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil || !command.Expected.IsValid() {
		return failedAttemptError("invalid command identity or expectation")
	}
	if _, err := ClassifyNormalAttemptFailure(command.FailureClass, command.Expected.Category); err != nil {
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
	if !uniqueNonZeroUUIDs(identities) {
		return failedAttemptError("missing or duplicate revision identity")
	}
	return nil
}

func validateFailedAttemptAuthority(authority FailedAttemptAuthority) error {
	if !validFailedAttemptAuthorityHeader(authority) {
		return failedAttemptError("invalid authority identity or live state")
	}
	if err := validateCurrentGameResultRevisionIDs(authority.CurrentGameResultRevisionIDs); err != nil {
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

func validFailedAttemptAuthorityHeader(authority FailedAttemptAuthority) bool {
	return authority.Scope.IsValid() && authority.Revision >= 1 &&
		authority.ActiveSnapshotID != uuid.Nil && authority.CurrentOrdinal >= 0 &&
		authority.CurrentProjectionRevision >= 1 && authority.Wave.Validate() == nil &&
		authority.Wave.ID == authority.Scope.WaveID &&
		authority.Wave.TournamentID == authority.Scope.TournamentID &&
		validFailedAttemptWaveState(authority) && authority.Series.Validate() == nil &&
		authority.Series.Series.ID == authority.Scope.SeriesID &&
		authority.Series.Series.TournamentID == authority.Scope.TournamentID
}

func validFailedAttemptWaveState(authority FailedAttemptAuthority) bool {
	return authority.Wave.State == domain.ArenaWaveStateActive ||
		(authority.Current != nil && authority.Wave.State == domain.ArenaWaveStateCompleted)
}

func validateOpenFailedAttemptAuthority(authority FailedAttemptAuthority) error {
	game, slot, found := currentFailedAttempt(authority.Series.Series, authority.Scope)
	if authority.Series.Series.State != domain.ArenaSeriesStateActive || !found ||
		game.State != domain.ArenaGameStateActive || !slot.Category.IsValid() {
		return ErrFailedAttemptUnavailable
	}
	return nil
}

func validateCurrentFailedAttemptAuthority(authority FailedAttemptAuthority) error {
	if authority.Current == nil || authority.Current.Validate() != nil ||
		authority.Current.Scope != authority.Scope {
		return failedAttemptError("invalid current terminalization")
	}
	return nil
}

func buildFailedAttemptRecord(
	command FailedAttemptCommand,
	authority FailedAttemptAuthority,
	terminalizedAt time.Time,
) (FailedAttemptRecord, error) {
	if authority.ActiveSnapshotID != command.Expected.SnapshotID {
		return FailedAttemptRecord{}, ErrFailedAttemptConflict
	}
	game, slot, found := currentFailedAttempt(authority.Series.Series, command.Scope)
	if !found || game.AttemptNo != command.Expected.AttemptNo || game.State != command.Expected.State ||
		slot.Category != command.Expected.Category {
		return FailedAttemptRecord{}, ErrFailedAttemptConflict
	}
	failure, err := ClassifyNormalAttemptFailure(command.FailureClass, slot.Category)
	if err != nil {
		return FailedAttemptRecord{}, err
	}
	working := cloneSeriesExecution(authority.Series)
	transitioned, changed, err := TransitionGameSlotAttempt(slot, GameAttemptTransitionCommand{
		GameID: game.ID, ExpectedAttemptNo: game.AttemptNo, ExpectedState: game.State,
		NextState: failure.GameState,
		Terminal: &GameTerminalEvidence{
			Reason: failure.Reason, ResultRevisionID: &command.Revisions.GameResultRevisionID,
		},
	})
	if err != nil || !changed {
		return FailedAttemptRecord{}, failedAttemptError("void Game: %v", err)
	}
	working.Series.Slots[len(working.Series.Slots)-1] = transitioned
	working.Series.CurrentScoreRevisionID = &command.Revisions.ScoreRevisionID
	working, changed, err = TransitionSeriesExecution(working, SeriesExecutionTransitionCommand{
		NextState: failure.SeriesState,
	})
	if err != nil || !changed {
		return FailedAttemptRecord{}, failedAttemptError("route Series: %v", err)
	}
	voidGame := cloneArenaGame(transitioned.Attempts[len(transitioned.Attempts)-1])
	resultIDs := append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		authority.CurrentGameResultRevisionIDs...,
	)
	resultIDs = append(resultIDs, command.Revisions.GameResultRevisionID)
	record := FailedAttemptRecord{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision,
		ActiveSnapshotID:          authority.ActiveSnapshotID, Failure: failure,
		Series: working, Game: voidGame,
		GameResultRevision: FailedGameResultRevision{
			Ordinal: authority.CurrentOrdinal + 1,
			ID:      command.Revisions.GameResultRevisionID, GameID: game.ID,
			Reason: failure.Reason, RecordedAt: terminalizedAt,
		},
		ScoreRevision: ArenaSettlementScoreRevision{
			ID: command.Revisions.ScoreRevisionID, SeriesID: command.Scope.SeriesID,
			FirstParticipantID:  authority.Series.Series.FirstParticipantID,
			SecondParticipantID: authority.Series.Series.SecondParticipantID,
			PreviousRevisionID: cloneSeriesScoreRevisionIDPointer(
				authority.Series.Series.CurrentScoreRevisionID,
			),
			Ordinal: authority.CurrentOrdinal + 2, Format: authority.Series.Series.Format,
			ScoreBefore: authority.Series.Series.Score, ScoreAfter: authority.Series.Series.Score,
			GameResultRevisionIDs: resultIDs, RecordedAt: terminalizedAt,
		},
		WaveRoute: FailedWaveMemberRoute{
			ID: command.Revisions.RouteEvidenceID, WaveID: command.Scope.WaveID,
			SeriesID: command.Scope.SeriesID, SlotID: command.Scope.SlotID,
			GameID: command.Scope.GameID, Category: failure.CategoryCutoff,
			RoutedAt: terminalizedAt,
		},
		Evidence: ArenaSettlementEvidence{
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
		return FailedAttemptRecord{}, err
	}
	return cloneFailedAttemptRecord(record), nil
}

func validateFailedAttemptGame(record FailedAttemptRecord) error {
	game := record.Game
	if game.Validate() != nil || game.ID != record.Scope.GameID ||
		game.SlotID != record.Scope.SlotID || game.State != record.Failure.GameState ||
		game.ResultReason != record.Failure.Reason || game.WinnerID != nil ||
		game.ResultRevisionID == nil || *game.ResultRevisionID != record.GameResultRevision.ID {
		return failedAttemptError("invalid void Game evidence")
	}
	revision := record.GameResultRevision
	if revision.Ordinal < 1 || revision.ID.IsZero() || revision.GameID != game.ID ||
		revision.Reason != record.Failure.Reason ||
		!revision.RecordedAt.Equal(record.TerminalizedAt) {
		return failedAttemptError("invalid Game result revision")
	}
	return nil
}

func validateFailedAttemptSeries(record FailedAttemptRecord) error {
	series := record.Series.Series
	if record.Series.Validate() != nil || series.ID != record.Scope.SeriesID ||
		series.TournamentID != record.Scope.TournamentID || series.State != record.Failure.SeriesState ||
		series.WinnerID != nil || series.CurrentResultRevisionID != nil ||
		series.CurrentScoreRevisionID == nil || *series.CurrentScoreRevisionID != record.ScoreRevision.ID ||
		series.Score != record.ScoreRevision.ScoreAfter {
		return failedAttemptError("invalid replay-required Series")
	}
	game, slot, found := currentFailedAttempt(series, record.Scope)
	if !found || game.ID != record.Game.ID || game.State != domain.ArenaGameStateVoid ||
		slot.Category != record.Failure.CategoryCutoff {
		return failedAttemptError("Series lost the failed attempt")
	}
	return nil
}

func validateFailedAttemptRevisions(record FailedAttemptRecord) error {
	revision := record.ScoreRevision
	if revision.ID.IsZero() || revision.SeriesID != record.Scope.SeriesID ||
		revision.FirstParticipantID != record.Series.Series.FirstParticipantID ||
		revision.SecondParticipantID != record.Series.Series.SecondParticipantID ||
		revision.Format != record.Series.Series.Format || revision.Ordinal < 2 ||
		revision.Ordinal != record.GameResultRevision.Ordinal+1 ||
		revision.ScoreBefore != revision.ScoreAfter ||
		revision.ScoreAfter != record.Series.Series.Score ||
		!revision.RecordedAt.Equal(record.TerminalizedAt) ||
		len(revision.GameResultRevisionIDs) == 0 ||
		revision.GameResultRevisionIDs[len(revision.GameResultRevisionIDs)-1] != record.GameResultRevision.ID {
		return failedAttemptError("invalid unchanged-score revision")
	}
	if err := validateCurrentGameResultRevisionIDs(revision.GameResultRevisionIDs); err != nil {
		return failedAttemptError("invalid score provenance: %v", err)
	}
	return nil
}

func validateFailedAttemptWaveRoute(record FailedAttemptRecord) error {
	route := record.WaveRoute
	if route.ID == uuid.Nil || route.WaveID != record.Scope.WaveID ||
		route.SeriesID != record.Scope.SeriesID || route.SlotID != record.Scope.SlotID ||
		route.GameID != record.Scope.GameID || route.Category != record.Failure.CategoryCutoff ||
		!route.RoutedAt.Equal(record.TerminalizedAt) {
		return failedAttemptError("invalid Wave membership route")
	}
	return nil
}

func validateFailedAttemptEvidence(record FailedAttemptRecord) error {
	evidence := record.Evidence
	identities := []uuid.UUID{
		record.CommandID,
		record.GameResultRevision.ID.UUID(),
		record.ScoreRevision.ID.UUID(),
		record.WaveRoute.ID,
		evidence.AuditEventID,
		evidence.OutboxEventID,
		evidence.ProjectionRevisionID,
	}
	if !uniqueNonZeroUUIDs(identities) || evidence.SourceProjectionRevision < 1 ||
		evidence.ProjectionRevision != evidence.SourceProjectionRevision+1 ||
		!evidence.RecordedAt.Equal(record.TerminalizedAt) {
		return failedAttemptError("invalid commit evidence")
	}
	return nil
}

func currentFailedAttempt(
	series domain.ArenaSeries,
	scope FailedAttemptScope,
) (domain.ArenaGame, domain.ArenaGameSlot, bool) {
	if len(series.Slots) == 0 {
		return domain.ArenaGame{}, domain.ArenaGameSlot{}, false
	}
	slot := series.Slots[len(series.Slots)-1]
	if slot.ID != scope.SlotID || len(slot.Attempts) == 0 {
		return domain.ArenaGame{}, domain.ArenaGameSlot{}, false
	}
	game := slot.Attempts[len(slot.Attempts)-1]
	if game.ID != scope.GameID {
		return domain.ArenaGame{}, domain.ArenaGameSlot{}, false
	}
	return cloneArenaGame(game), cloneArenaGameSlot(slot), true
}

func reconcileFailedAttempt(
	record FailedAttemptRecord,
	command FailedAttemptCommand,
) (*FailedAttemptRecord, error) {
	if record.Validate() != nil || record.Scope != command.Scope ||
		record.CommandID != command.CommandID || record.Failure.Class != command.FailureClass ||
		record.Game.AttemptNo != command.Expected.AttemptNo ||
		record.ActiveSnapshotID != command.Expected.SnapshotID ||
		record.Failure.CategoryCutoff != command.Expected.Category ||
		record.GameResultRevision.ID != command.Revisions.GameResultRevisionID ||
		record.ScoreRevision.ID != command.Revisions.ScoreRevisionID ||
		record.WaveRoute.ID != command.Revisions.RouteEvidenceID ||
		record.Evidence.AuditEventID != command.Revisions.AuditEventID ||
		record.Evidence.OutboxEventID != command.Revisions.OutboxEventID ||
		record.Evidence.ProjectionRevisionID != command.Revisions.ProjectionRevisionID {
		return nil, ErrFailedAttemptCommandReuse
	}
	clone := cloneFailedAttemptRecord(record)
	return &clone, nil
}

func validCommittedFailedAttempt(
	committed *FailedAttemptRecord,
	proposed FailedAttemptRecord,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil || committed.Scope != proposed.Scope {
		return false
	}
	if !changed {
		return true
	}
	return failedAttemptRecordsEqual(*committed, proposed)
}

func failedAttemptRecordsEqual(first, second FailedAttemptRecord) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		first.ActiveSnapshotID == second.ActiveSnapshotID && first.Failure == second.Failure &&
		forfeitSeriesHeadsEqual(first.Series, second.Series) &&
		arenaSettlementGamesEqual(first.Game, second.Game) &&
		first.GameResultRevision == second.GameResultRevision &&
		arenaSettlementScoreRevisionsEqual(first.ScoreRevision, second.ScoreRevision) &&
		first.WaveRoute == second.WaveRoute && first.Evidence == second.Evidence &&
		first.TerminalizedAt.Equal(second.TerminalizedAt)
}

func cloneFailedAttemptRecord(record FailedAttemptRecord) FailedAttemptRecord {
	clone := record
	clone.Series = cloneSeriesExecution(record.Series)
	clone.Game = cloneArenaGame(record.Game)
	clone.ScoreRevision.PreviousRevisionID = cloneSeriesScoreRevisionIDPointer(
		record.ScoreRevision.PreviousRevisionID,
	)
	clone.ScoreRevision.GameResultRevisionIDs = append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		record.ScoreRevision.GameResultRevisionIDs...,
	)
	return clone
}

func failedAttemptError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidFailedAttempt, fmt.Sprintf(format, arguments...))
}
