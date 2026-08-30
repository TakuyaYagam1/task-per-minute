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

const concurrentWinnerAttempts = 2

var (
	ErrInvalidConcurrentWinnerSettlement = errors.New("invalid concurrent winner settlement")
	ErrConcurrentWinnerUnavailable       = errors.New("no correct Arena submission is available")
	ErrConcurrentWinnerConflict          = errors.New("concurrent winner settlement conflict")
)

type ConcurrentWinnerAuthority struct {
	Scope                        ArenaSubmissionScope
	Revision                     int64
	StartedGame                  StartedWaveGame
	Submissions                  []ArenaSubmissionRecord
	CurrentScoreOrdinal          int
	CurrentGameResultRevisionIDs []domain.ArenaOfficialResultRevisionID
	CurrentProjectionRevision    int64
	Current                      *ConcurrentWinnerSettlement
}

type ConcurrentWinnerCommand struct {
	Scope                ArenaSubmissionScope
	GameResultRevisionID domain.ArenaOfficialResultRevisionID
	ScoreRevisionID      domain.ArenaSeriesScoreRevisionID
	AuditEventID         uuid.UUID
	OutboxEventID        uuid.UUID
	ProjectionRevisionID uuid.UUID
}

type ArenaGameResultRevision struct {
	ID                        domain.ArenaOfficialResultRevisionID
	GameID                    uuid.UUID
	PreviousRevisionID        *domain.ArenaOfficialResultRevisionID
	WinnerID                  uuid.UUID
	Reason                    domain.ArenaGameResultReason
	WinningSubmissionSequence int64
	RecordedAt                time.Time
}

type ArenaSettlementScoreRevision struct {
	ID                    domain.ArenaSeriesScoreRevisionID
	SeriesID              uuid.UUID
	FirstParticipantID    uuid.UUID
	SecondParticipantID   uuid.UUID
	PreviousRevisionID    *domain.ArenaSeriesScoreRevisionID
	Ordinal               int
	Format                domain.ArenaSeriesFormat
	ScoreBefore           domain.ArenaSeriesScore
	ScoreAfter            domain.ArenaSeriesScore
	GameResultRevisionIDs []domain.ArenaOfficialResultRevisionID
	RecordedAt            time.Time
}

type ArenaSettlementEvidence struct {
	AuditEventID             uuid.UUID
	OutboxEventID            uuid.UUID
	ProjectionRevisionID     uuid.UUID
	SourceProjectionRevision int64
	ProjectionRevision       int64
	RecordedAt               time.Time
}

type ConcurrentWinnerSettlement struct {
	Scope                     ArenaSubmissionScope
	ExpectedAuthorityRevision int64
	WinningSubmission         ArenaSubmissionRecord
	Game                      domain.ArenaGame
	GameResultRevision        ArenaGameResultRevision
	ScoreRevision             ArenaSettlementScoreRevision
	Evidence                  ArenaSettlementEvidence
	SettledAt                 time.Time
}

// ConcurrentWinnerRepository owns one transaction that revalidates the
// earliest correct submission and current Game, then commits the terminal Game
// result, Series score revision, audit row, outbox row and projection revision.
// A competing transaction must return domain.ErrConflict without partial data.
type ConcurrentWinnerRepository interface {
	LoadConcurrentWinnerAuthority(
		ctx context.Context,
		scope ArenaSubmissionScope,
	) (ConcurrentWinnerAuthority, error)
	CommitConcurrentWinnerSettlement(
		ctx context.Context,
		settlement ConcurrentWinnerSettlement,
	) (*ConcurrentWinnerSettlement, bool, error)
}

type ConcurrentWinnerSettlementUseCase struct {
	repository ConcurrentWinnerRepository
	clock      Clock
}

func NewConcurrentWinnerSettlementUseCase(
	repository ConcurrentWinnerRepository,
	clock Clock,
) *ConcurrentWinnerSettlementUseCase {
	return &ConcurrentWinnerSettlementUseCase{repository: repository, clock: clock}
}

func (u *ConcurrentWinnerSettlementUseCase) Settle(
	ctx context.Context,
	command ConcurrentWinnerCommand,
) (*ConcurrentWinnerSettlement, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateConcurrentWinnerCommand(command); err != nil {
		return nil, false, err
	}
	settledAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(settledAt) {
		return nil, false, domain.ErrValidation
	}

	for range concurrentWinnerAttempts {
		settlement, changed, retry, err := u.settleAttempt(ctx, command, settledAt)
		if retry {
			continue
		}
		return settlement, changed, err
	}
	return nil, false, ErrConcurrentWinnerConflict
}

func (u *ConcurrentWinnerSettlementUseCase) settleAttempt(
	ctx context.Context,
	command ConcurrentWinnerCommand,
	settledAt time.Time,
) (*ConcurrentWinnerSettlement, bool, bool, error) {
	authority, err := u.repository.LoadConcurrentWinnerAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false,
			fmt.Errorf("ConcurrentWinnerSettlementUseCase - load authority: %w", err)
	}
	if err := validateConcurrentWinnerAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != command.Scope {
		return nil, false, false, concurrentWinnerError("authority scope does not match command")
	}
	if authority.Current != nil {
		current := cloneConcurrentWinnerSettlement(*authority.Current)
		return &current, false, false, nil
	}
	if authority.StartedGame.Series.Series.State != domain.ArenaSeriesStateActive ||
		arenaSubmissionGameState(authority.StartedGame) != domain.ArenaGameStateActive {
		return nil, false, false, ErrConcurrentWinnerUnavailable
	}
	winner, err := earliestCorrectArenaSubmission(authority.Submissions)
	if err != nil {
		return nil, false, false, err
	}
	settlement, err := buildConcurrentWinnerSettlement(command, authority, winner, settledAt)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitConcurrentWinnerSettlement(ctx, settlement)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false,
			fmt.Errorf("ConcurrentWinnerSettlementUseCase - commit settlement: %w", err)
	}
	if !validCommittedConcurrentWinner(committed, settlement, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneConcurrentWinnerSettlement(*committed)
	return &result, changed, false, nil
}

func (s ConcurrentWinnerSettlement) Validate() error {
	if !validArenaSubmissionScope(s.Scope) || s.ExpectedAuthorityRevision < 1 ||
		!validArenaServerTime(s.SettledAt) {
		return concurrentWinnerError("invalid settlement identity or timestamp")
	}
	if err := s.WinningSubmission.Validate(); err != nil ||
		s.WinningSubmission.Scope != s.Scope || !s.WinningSubmission.Correct ||
		s.SettledAt.Before(s.WinningSubmission.CommittedAt) {
		return concurrentWinnerError("invalid winning submission")
	}
	if err := validateSettledArenaGame(s); err != nil {
		return err
	}
	if err := validateArenaGameResultRevision(s); err != nil {
		return err
	}
	if err := validateArenaSettlementScoreRevision(s); err != nil {
		return err
	}
	return validateArenaSettlementEvidence(s)
}

func validateConcurrentWinnerCommand(command ConcurrentWinnerCommand) error {
	identities := []uuid.UUID{
		command.GameResultRevisionID.UUID(), command.ScoreRevisionID.UUID(),
		command.AuditEventID, command.OutboxEventID, command.ProjectionRevisionID,
	}
	if !validArenaSubmissionScope(command.Scope) {
		return concurrentWinnerError("invalid command scope")
	}
	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, identity := range identities {
		if identity == uuid.Nil {
			return concurrentWinnerError("missing evidence identity")
		}
		if _, duplicate := seen[identity]; duplicate {
			return concurrentWinnerError("duplicate evidence identity")
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func validateConcurrentWinnerAuthority(authority ConcurrentWinnerAuthority) error {
	if !validArenaSubmissionScope(authority.Scope) || authority.Revision < 1 ||
		authority.CurrentScoreOrdinal < 0 || authority.CurrentProjectionRevision < 1 {
		return concurrentWinnerError("invalid authority identity or ordinal")
	}
	if err := validateStartedArenaSubmissionGame(authority.Scope, authority.StartedGame); err != nil {
		return concurrentWinnerError("started Game: %v", err)
	}
	if err := validateConcurrentWinnerSubmissions(authority); err != nil {
		return err
	}
	if err := validateCurrentGameResultRevisionIDs(authority.CurrentGameResultRevisionIDs); err != nil {
		return err
	}
	if authority.CurrentScoreOrdinal > 0 && authority.StartedGame.Series.Series.CurrentScoreRevisionID == nil {
		return concurrentWinnerError("score ordinal has no current revision")
	}
	if authority.Current != nil {
		if err := validateCurrentConcurrentWinner(authority); err != nil {
			return concurrentWinnerError("invalid current settlement")
		}
	}
	return nil
}

func validateCurrentConcurrentWinner(authority ConcurrentWinnerAuthority) error {
	current := authority.Current
	if current == nil || current.Validate() != nil || current.Scope != authority.Scope {
		return ErrInvalidConcurrentWinnerSettlement
	}
	series := authority.StartedGame.Series.Series
	if current.ScoreRevision.FirstParticipantID != series.FirstParticipantID ||
		current.ScoreRevision.SecondParticipantID != series.SecondParticipantID ||
		current.ScoreRevision.Format != series.Format {
		return ErrInvalidConcurrentWinnerSettlement
	}
	for _, submission := range authority.Submissions {
		if submission == current.WinningSubmission {
			return nil
		}
	}
	return ErrInvalidConcurrentWinnerSettlement
}

func validateConcurrentWinnerSubmissions(authority ConcurrentWinnerAuthority) error {
	ordered, err := OrderArenaSubmissions(authority.Submissions)
	if err != nil {
		return err
	}
	for _, submission := range ordered {
		if submission.Scope != authority.Scope ||
			submission.SnapshotID != authority.StartedGame.SnapshotID ||
			submission.ContentDigest != authority.StartedGame.ContentDigest ||
			!arenaStartedParticipant(authority.StartedGame, submission.ParticipantID) ||
			submission.CommittedAt.Before(authority.StartedGame.StartedAt) ||
			!submission.CommittedAt.Before(authority.StartedGame.Deadline) {
			return concurrentWinnerError("submission does not match started Game")
		}
	}
	return nil
}

func validateCurrentGameResultRevisionIDs(
	revisionIDs []domain.ArenaOfficialResultRevisionID,
) error {
	seen := make(map[domain.ArenaOfficialResultRevisionID]struct{}, len(revisionIDs))
	for _, revisionID := range revisionIDs {
		if revisionID.IsZero() {
			return concurrentWinnerError("empty retained Game result revision")
		}
		if _, duplicate := seen[revisionID]; duplicate {
			return concurrentWinnerError("duplicate retained Game result revision")
		}
		seen[revisionID] = struct{}{}
	}
	return nil
}

func earliestCorrectArenaSubmission(
	submissions []ArenaSubmissionRecord,
) (ArenaSubmissionRecord, error) {
	ordered, err := OrderArenaSubmissions(submissions)
	if err != nil {
		return ArenaSubmissionRecord{}, err
	}
	for _, submission := range ordered {
		if submission.Correct {
			return submission, nil
		}
	}
	return ArenaSubmissionRecord{}, ErrConcurrentWinnerUnavailable
}

func buildConcurrentWinnerSettlement(
	command ConcurrentWinnerCommand,
	authority ConcurrentWinnerAuthority,
	winner ArenaSubmissionRecord,
	settledAt time.Time,
) (ConcurrentWinnerSettlement, error) {
	if settledAt.Before(winner.CommittedAt) {
		return ConcurrentWinnerSettlement{}, concurrentWinnerError("settlement precedes submission commit")
	}
	game, slotIndex, found := arenaSubmissionGame(authority.StartedGame, command.Scope.Game)
	if !found || game.State != domain.ArenaGameStateActive {
		return ConcurrentWinnerSettlement{}, ErrConcurrentWinnerUnavailable
	}
	resultRevisionID := command.GameResultRevisionID
	winningParticipantID := winner.ParticipantID
	transitioned, changed, err := TransitionGameSlotAttempt(
		authority.StartedGame.Series.Series.Slots[slotIndex],
		GameAttemptTransitionCommand{
			GameID: game.ID, ExpectedAttemptNo: game.AttemptNo,
			ExpectedState: domain.ArenaGameStateActive, NextState: domain.ArenaGameStateCompleted,
			Terminal: &GameTerminalEvidence{
				Reason: domain.ArenaGameResultReasonSolved, WinnerID: &winningParticipantID,
				ResultRevisionID: &resultRevisionID,
			},
		},
	)
	if err != nil || !changed {
		return ConcurrentWinnerSettlement{}, concurrentWinnerError("complete Game: %v", err)
	}
	completedGame := transitioned.Attempts[len(transitioned.Attempts)-1]
	scoreBefore := authority.StartedGame.Series.Series.Score
	scoreAfter, err := scoreAfterArenaWinner(authority.StartedGame.Series.Series, winner.ParticipantID)
	if err != nil {
		return ConcurrentWinnerSettlement{}, err
	}
	gameResultRevisionIDs := append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		authority.CurrentGameResultRevisionIDs...,
	)
	gameResultRevisionIDs = append(gameResultRevisionIDs, command.GameResultRevisionID)
	settlement := ConcurrentWinnerSettlement{
		Scope: command.Scope, ExpectedAuthorityRevision: authority.Revision,
		WinningSubmission: winner, Game: completedGame,
		GameResultRevision: ArenaGameResultRevision{
			ID: command.GameResultRevisionID, GameID: game.ID, WinnerID: winner.ParticipantID,
			Reason:                    domain.ArenaGameResultReasonSolved,
			WinningSubmissionSequence: winner.Sequence, RecordedAt: settledAt,
		},
		ScoreRevision: ArenaSettlementScoreRevision{
			ID: command.ScoreRevisionID, SeriesID: command.Scope.Game.SeriesID,
			FirstParticipantID:  authority.StartedGame.Series.Series.FirstParticipantID,
			SecondParticipantID: authority.StartedGame.Series.Series.SecondParticipantID,
			PreviousRevisionID: cloneSeriesScoreRevisionIDPointer(
				authority.StartedGame.Series.Series.CurrentScoreRevisionID,
			),
			Ordinal:     authority.CurrentScoreOrdinal + 1,
			Format:      authority.StartedGame.Series.Series.Format,
			ScoreBefore: scoreBefore, ScoreAfter: scoreAfter,
			GameResultRevisionIDs: gameResultRevisionIDs, RecordedAt: settledAt,
		},
		Evidence: ArenaSettlementEvidence{
			AuditEventID: command.AuditEventID, OutboxEventID: command.OutboxEventID,
			ProjectionRevisionID:     command.ProjectionRevisionID,
			SourceProjectionRevision: authority.CurrentProjectionRevision,
			ProjectionRevision:       authority.CurrentProjectionRevision + 1,
			RecordedAt:               settledAt,
		},
		SettledAt: settledAt,
	}
	if err := settlement.Validate(); err != nil {
		return ConcurrentWinnerSettlement{}, err
	}
	return cloneConcurrentWinnerSettlement(settlement), nil
}

func scoreAfterArenaWinner(
	series domain.ArenaSeries,
	winnerID uuid.UUID,
) (domain.ArenaSeriesScore, error) {
	score := series.Score
	switch winnerID {
	case series.FirstParticipantID:
		score.FirstParticipantWins++
	case series.SecondParticipantID:
		score.SecondParticipantWins++
	default:
		return domain.ArenaSeriesScore{}, concurrentWinnerError("winner is not a Series participant")
	}
	if err := score.Validate(series.Format); err != nil {
		return domain.ArenaSeriesScore{}, concurrentWinnerError("score after winner: %v", err)
	}
	return score, nil
}

func validateSettledArenaGame(settlement ConcurrentWinnerSettlement) error {
	game := settlement.Game
	if err := game.Validate(); err != nil || game.ID != settlement.Scope.Game.GameID ||
		game.SlotID != settlement.Scope.Game.SlotID || game.State != domain.ArenaGameStateCompleted ||
		game.ResultReason != domain.ArenaGameResultReasonSolved || game.WinnerID == nil ||
		*game.WinnerID != settlement.WinningSubmission.ParticipantID ||
		game.ResultRevisionID == nil || *game.ResultRevisionID != settlement.GameResultRevision.ID {
		return concurrentWinnerError("invalid completed Game")
	}
	return nil
}

func validateArenaGameResultRevision(settlement ConcurrentWinnerSettlement) error {
	revision := settlement.GameResultRevision
	if revision.ID.IsZero() || revision.GameID != settlement.Game.ID ||
		revision.PreviousRevisionID != nil || revision.WinnerID != settlement.WinningSubmission.ParticipantID ||
		revision.Reason != domain.ArenaGameResultReasonSolved ||
		revision.WinningSubmissionSequence != settlement.WinningSubmission.Sequence ||
		!revision.RecordedAt.Equal(settlement.SettledAt) {
		return concurrentWinnerError("invalid Game result revision")
	}
	return nil
}

func validateArenaSettlementScoreRevision(settlement ConcurrentWinnerSettlement) error {
	revision := settlement.ScoreRevision
	if revision.ID.IsZero() || revision.SeriesID != settlement.Scope.Game.SeriesID ||
		revision.FirstParticipantID == uuid.Nil || revision.SecondParticipantID == uuid.Nil ||
		revision.FirstParticipantID == revision.SecondParticipantID ||
		revision.Ordinal < 1 || !revision.Format.IsValid() ||
		revision.ScoreBefore.Validate(revision.Format) != nil ||
		revision.ScoreAfter.Validate(revision.Format) != nil ||
		!revision.RecordedAt.Equal(settlement.SettledAt) ||
		len(revision.GameResultRevisionIDs) == 0 ||
		revision.GameResultRevisionIDs[len(revision.GameResultRevisionIDs)-1] != settlement.GameResultRevision.ID {
		return concurrentWinnerError("invalid Series score revision")
	}
	if !scoreRevisionAddsWinner(revision, settlement.WinningSubmission.ParticipantID, settlement) {
		return concurrentWinnerError("score revision does not add the Game winner")
	}
	return validateCurrentGameResultRevisionIDs(revision.GameResultRevisionIDs)
}

func scoreRevisionAddsWinner(
	revision ArenaSettlementScoreRevision,
	winnerID uuid.UUID,
	settlement ConcurrentWinnerSettlement,
) bool {
	if winnerID != settlement.WinningSubmission.ParticipantID ||
		winnerID != settlement.GameResultRevision.WinnerID {
		return false
	}
	firstDelta := revision.ScoreAfter.FirstParticipantWins - revision.ScoreBefore.FirstParticipantWins
	secondDelta := revision.ScoreAfter.SecondParticipantWins - revision.ScoreBefore.SecondParticipantWins
	switch winnerID {
	case revision.FirstParticipantID:
		return firstDelta == 1 && secondDelta == 0
	case revision.SecondParticipantID:
		return firstDelta == 0 && secondDelta == 1
	default:
		return false
	}
}

func validateArenaSettlementEvidence(settlement ConcurrentWinnerSettlement) error {
	evidence := settlement.Evidence
	identities := []uuid.UUID{
		settlement.GameResultRevision.ID.UUID(), settlement.ScoreRevision.ID.UUID(),
		evidence.AuditEventID, evidence.OutboxEventID, evidence.ProjectionRevisionID,
	}
	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, identity := range identities {
		if identity == uuid.Nil {
			return concurrentWinnerError("missing settlement evidence")
		}
		if _, duplicate := seen[identity]; duplicate {
			return concurrentWinnerError("duplicate settlement evidence")
		}
		seen[identity] = struct{}{}
	}
	if evidence.SourceProjectionRevision < 1 ||
		evidence.ProjectionRevision != evidence.SourceProjectionRevision+1 ||
		!evidence.RecordedAt.Equal(settlement.SettledAt) {
		return concurrentWinnerError("invalid projection evidence")
	}
	return nil
}

func validCommittedConcurrentWinner(
	committed *ConcurrentWinnerSettlement,
	proposed ConcurrentWinnerSettlement,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil || committed.Scope != proposed.Scope {
		return false
	}
	if !changed {
		return true
	}
	return concurrentWinnerSettlementsEqual(*committed, proposed)
}

func concurrentWinnerSettlementsEqual(
	first ConcurrentWinnerSettlement,
	second ConcurrentWinnerSettlement,
) bool {
	return first.Scope == second.Scope &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		first.WinningSubmission == second.WinningSubmission &&
		arenaSettlementGamesEqual(first.Game, second.Game) &&
		arenaGameResultRevisionsEqual(first.GameResultRevision, second.GameResultRevision) &&
		arenaSettlementScoreRevisionsEqual(first.ScoreRevision, second.ScoreRevision) &&
		first.Evidence == second.Evidence && first.SettledAt.Equal(second.SettledAt)
}

func arenaSettlementGamesEqual(first, second domain.ArenaGame) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID &&
		first.AttemptNo == second.AttemptNo && first.State == second.State &&
		first.ResultReason == second.ResultReason &&
		uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		officialResultRevisionPointersEqual(first.ResultRevisionID, second.ResultRevisionID)
}

func arenaGameResultRevisionsEqual(
	first ArenaGameResultRevision,
	second ArenaGameResultRevision,
) bool {
	return first.ID == second.ID && first.GameID == second.GameID &&
		officialResultRevisionPointersEqual(first.PreviousRevisionID, second.PreviousRevisionID) &&
		first.WinnerID == second.WinnerID && first.Reason == second.Reason &&
		first.WinningSubmissionSequence == second.WinningSubmissionSequence &&
		first.RecordedAt.Equal(second.RecordedAt)
}

func arenaSettlementScoreRevisionsEqual(
	first ArenaSettlementScoreRevision,
	second ArenaSettlementScoreRevision,
) bool {
	return first.ID == second.ID && first.SeriesID == second.SeriesID &&
		first.FirstParticipantID == second.FirstParticipantID &&
		first.SecondParticipantID == second.SecondParticipantID &&
		seriesScoreRevisionPointersEqual(first.PreviousRevisionID, second.PreviousRevisionID) &&
		first.Ordinal == second.Ordinal && first.Format == second.Format &&
		first.ScoreBefore == second.ScoreBefore && first.ScoreAfter == second.ScoreAfter &&
		slices.Equal(first.GameResultRevisionIDs, second.GameResultRevisionIDs) &&
		first.RecordedAt.Equal(second.RecordedAt)
}

func officialResultRevisionPointersEqual(
	first *domain.ArenaOfficialResultRevisionID,
	second *domain.ArenaOfficialResultRevisionID,
) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func seriesScoreRevisionPointersEqual(
	first *domain.ArenaSeriesScoreRevisionID,
	second *domain.ArenaSeriesScoreRevisionID,
) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func arenaStartedParticipant(started StartedWaveGame, participantID uuid.UUID) bool {
	return started.ParticipantIDs[0] == participantID || started.ParticipantIDs[1] == participantID
}

func cloneConcurrentWinnerSettlement(
	settlement ConcurrentWinnerSettlement,
) ConcurrentWinnerSettlement {
	cloned := settlement
	cloned.Game = cloneArenaGame(settlement.Game)
	cloned.GameResultRevision.PreviousRevisionID = cloneOfficialResultRevisionIDPointer(
		settlement.GameResultRevision.PreviousRevisionID,
	)
	cloned.ScoreRevision.PreviousRevisionID = cloneSeriesScoreRevisionIDPointer(
		settlement.ScoreRevision.PreviousRevisionID,
	)
	cloned.ScoreRevision.GameResultRevisionIDs = append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		settlement.ScoreRevision.GameResultRevisionIDs...,
	)
	return cloned
}

func concurrentWinnerError(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %s",
		ErrInvalidConcurrentWinnerSettlement,
		fmt.Sprintf(format, arguments...),
	)
}
