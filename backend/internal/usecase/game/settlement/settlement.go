package settlement

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

const concurrentWinnerAttempts = 2

var (
	ErrInvalidConcurrentWinnerSettlement = errors.New("invalid concurrent winner settlement")
	ErrConcurrentWinnerUnavailable       = errors.New("no correct game submission is available")
	ErrConcurrentWinnerConflict          = errors.New("concurrent winner settlement conflict")
)

type SettlementAuthority struct {
	Scope                        gamedomain.SubmissionScope
	Revision                     int64
	StartedGame                  gamedomain.Started
	Submissions                  []gamedomain.Submission
	CurrentScoreOrdinal          int
	CurrentGameResultRevisionIDs []domain.OfficialResultRevisionID
	CurrentProjectionRevision    int64
	Current                      *SettlementRecord
}

type SettlementCommand struct {
	Scope     gamedomain.SubmissionScope
	CommandID uuid.UUID
}

type SettlementGameResultRevision struct {
	ID                        domain.OfficialResultRevisionID
	GameID                    uuid.UUID
	PreviousRevisionID        *domain.OfficialResultRevisionID
	WinnerID                  uuid.UUID
	Reason                    domain.GameResultReason
	WinningSubmissionSequence int64
	RecordedAt                time.Time
}

type SettlementRecord struct {
	Scope                        gamedomain.SubmissionScope
	CommandID                    uuid.UUID
	ExpectedAuthorityRevision    int64
	WinningSubmission            gamedomain.Submission
	EffectiveSolveTime           time.Duration
	Game                         domain.Game
	SettlementGameResultRevision SettlementGameResultRevision
	ScoreRevision                seriesdomain.ScoreRevision
	Series                       domain.Series
	Evidence                     seriesdomain.SettlementEvidence
	SettledAt                    time.Time
}

type SettlementUseCase struct {
	repository SettlementRepository
}

func SettlementNewUseCase(repository SettlementRepository) *SettlementUseCase {
	return &SettlementUseCase{repository: repository}
}

func (u *SettlementUseCase) Settle(
	ctx context.Context,
	command SettlementCommand,
) (*SettlementRecord, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateConcurrentWinnerCommand(command); err != nil {
		return nil, false, err
	}

	for range concurrentWinnerAttempts {
		settlement, changed, retry, err := u.settleAttempt(ctx, command)
		if retry {
			continue
		}
		return settlement, changed, err
	}
	return nil, false, ErrConcurrentWinnerConflict
}

func (u *SettlementUseCase) settleAttempt(
	ctx context.Context,
	command SettlementCommand,
) (*SettlementRecord, bool, bool, error) {
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
	if authority.StartedGame.Series.Series.State != domain.SeriesStateActive ||
		gamedomain.StartedState(authority.StartedGame) != domain.GameStateActive {
		return nil, false, false, ErrConcurrentWinnerUnavailable
	}
	winner, err := earliestCorrectSubmission(authority.Submissions)
	if err != nil {
		return nil, false, false, err
	}
	// The submission has already been durably accepted with PostgreSQL time in
	// the coordinator transaction. Reusing that immutable timestamp prevents a
	// process clock from ordering the settlement before its source evidence.
	settledAt := winner.CommittedAt.Round(0).UTC()
	if !domain.IsValidServerTime(settledAt) {
		return nil, false, false, domain.ErrInternal
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

func (s SettlementRecord) Validate() error {
	if !s.Scope.IsValid() || s.CommandID == uuid.Nil || s.ExpectedAuthorityRevision < 1 ||
		!domain.IsValidServerTime(s.SettledAt) {
		return concurrentWinnerError("invalid settlement identity or timestamp")
	}
	if err := gamedomain.ValidateSubmission(s.WinningSubmission); err != nil ||
		s.WinningSubmission.Scope != s.Scope || !s.WinningSubmission.Correct ||
		s.EffectiveSolveTime < 0 || s.SettledAt.Before(s.WinningSubmission.CommittedAt) {
		return concurrentWinnerError("invalid winning submission")
	}
	if err := validateSettledGame(s); err != nil {
		return err
	}
	if err := validateGameResultRevision(s); err != nil {
		return err
	}
	if err := validateSettlementScoreRevision(s); err != nil {
		return err
	}
	if err := validateSettledSeries(s); err != nil {
		return err
	}
	return validateSettlementEvidence(s)
}

func validateConcurrentWinnerCommand(command SettlementCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil {
		return concurrentWinnerError("invalid command identity")
	}
	identities := settlementEvidenceIDs(command)
	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, identity := range identities {
		if identity == uuid.Nil {
			return concurrentWinnerError("missing deterministic evidence identity")
		}
		if _, duplicate := seen[identity]; duplicate {
			return concurrentWinnerError("duplicate deterministic evidence identity")
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func validateConcurrentWinnerAuthority(authority SettlementAuthority) error {
	if !authority.Scope.IsValid() || authority.Revision < 1 ||
		authority.CurrentScoreOrdinal < 0 || authority.CurrentProjectionRevision < 1 {
		return concurrentWinnerError("invalid authority identity or ordinal")
	}
	if err := gamedomain.ValidateStarted(authority.Scope, authority.StartedGame); err != nil {
		return concurrentWinnerError("started Game: %v", err)
	}
	if err := validateConcurrentWinnerSubmissions(authority); err != nil {
		return err
	}
	if err := seriesdomain.ValidateGameResultRevisionIDs(authority.CurrentGameResultRevisionIDs); err != nil {
		return concurrentWinnerError("%v", err)
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

func validateCurrentConcurrentWinner(authority SettlementAuthority) error {
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

func validateConcurrentWinnerSubmissions(authority SettlementAuthority) error {
	ordered, err := gamedomain.OrderSubmissions(authority.Submissions)
	if err != nil {
		return err
	}
	for _, submission := range ordered {
		if submission.Scope != authority.Scope ||
			submission.SnapshotID != authority.StartedGame.SnapshotID ||
			submission.ContentDigest != authority.StartedGame.ContentDigest ||
			!startedParticipant(authority.StartedGame, submission.ParticipantID) ||
			submission.CommittedAt.Before(authority.StartedGame.StartedAt) ||
			!submission.CommittedAt.Before(authority.StartedGame.Deadline) {
			return concurrentWinnerError("submission does not match started Game")
		}
	}
	return nil
}

func earliestCorrectSubmission(
	submissions []gamedomain.Submission,
) (gamedomain.Submission, error) {
	ordered, err := gamedomain.OrderSubmissions(submissions)
	if err != nil {
		return gamedomain.Submission{}, err
	}
	for _, submission := range ordered {
		if submission.Correct {
			return submission, nil
		}
	}
	return gamedomain.Submission{}, ErrConcurrentWinnerUnavailable
}

func buildConcurrentWinnerSettlement(
	command SettlementCommand,
	authority SettlementAuthority,
	winner gamedomain.Submission,
	settledAt time.Time,
) (SettlementRecord, error) {
	if settledAt.Before(winner.CommittedAt) {
		return SettlementRecord{}, concurrentWinnerError("settlement precedes submission commit")
	}
	game, slotIndex, found := gamedomain.FindAttempt(authority.StartedGame, command.Scope.Game)
	if !found || game.State != domain.GameStateActive {
		return SettlementRecord{}, ErrConcurrentWinnerUnavailable
	}
	// A later accepted command may discover an earlier correct submission. All
	// immutable settlement identities belong to that earliest source command.
	winnerCommand := SettlementCommand{Scope: command.Scope, CommandID: winner.CommandID}
	gameResultRevisionID := domain.OfficialResultRevisionID(settlementEvidenceID(winnerCommand, "game-result"))
	scoreRevisionID := domain.SeriesScoreRevisionID(settlementEvidenceID(winnerCommand, "score-revision"))
	winningParticipantID := winner.ParticipantID
	transitioned, changed, err := gamedomain.TransitionSlotAttempt(
		authority.StartedGame.Series.Series.Slots[slotIndex],
		gamedomain.TransitionCommand{
			GameID: game.ID, ExpectedAttemptNo: game.AttemptNo,
			ExpectedState: domain.GameStateActive, NextState: domain.GameStateCompleted,
			Terminal: &gamedomain.TerminalEvidence{
				Reason: domain.GameResultReasonSolved, WinnerID: &winningParticipantID,
				ResultRevisionID: &gameResultRevisionID,
			},
		},
	)
	if err != nil || !changed {
		return SettlementRecord{}, concurrentWinnerError("complete Game: %v", err)
	}
	completedGame := transitioned.Attempts[len(transitioned.Attempts)-1]
	scoreBefore := authority.StartedGame.Series.Series.Score
	scoreAfter, err := scoreAfterWinner(authority.StartedGame.Series.Series, winner.ParticipantID)
	if err != nil {
		return SettlementRecord{}, err
	}
	gameResultRevisionIDs := append(
		[]domain.OfficialResultRevisionID(nil),
		authority.CurrentGameResultRevisionIDs...,
	)
	gameResultRevisionIDs = append(gameResultRevisionIDs, gameResultRevisionID)
	series := seriesdomain.CloneExecution(authority.StartedGame.Series)
	series.Series.Slots[slotIndex] = transitioned
	series.Series.Score = scoreAfter
	series.Series.CurrentScoreRevisionID = &scoreRevisionID
	if seriesWinnerID := scoreAfter.Winner(
		series.Series.FirstParticipantID,
		series.Series.SecondParticipantID,
		series.Series.Format,
	); seriesWinnerID != nil {
		seriesResultRevisionID := domain.OfficialResultRevisionID(settlementEvidenceID(winnerCommand, "series-result"))
		completed, completedChanged, transitionErr := seriesdomain.Transition(series, seriesdomain.TransitionCommand{
			NextState: domain.SeriesStateCompleted,
			Terminal: &seriesdomain.TerminalEvidence{
				Score:            scoreAfter,
				WinnerID:         seriesWinnerID,
				ScoreRevisionID:  &scoreRevisionID,
				ResultRevisionID: &seriesResultRevisionID,
			},
		})
		if transitionErr != nil || !completedChanged {
			return SettlementRecord{}, concurrentWinnerError("complete Series: %v", transitionErr)
		}
		series = completed
	}
	settlement := SettlementRecord{
		Scope: command.Scope, CommandID: winner.CommandID, ExpectedAuthorityRevision: authority.Revision,
		WinningSubmission:  winner,
		EffectiveSolveTime: winner.CommittedAt.Sub(authority.StartedGame.StartedAt),
		Game:               completedGame,
		SettlementGameResultRevision: SettlementGameResultRevision{
			ID: gameResultRevisionID, GameID: game.ID, WinnerID: winner.ParticipantID,
			Reason:                    domain.GameResultReasonSolved,
			WinningSubmissionSequence: winner.Sequence, RecordedAt: settledAt,
		},
		ScoreRevision: seriesdomain.ScoreRevision{
			ID: scoreRevisionID, SeriesID: command.Scope.Game.SeriesID,
			FirstParticipantID:  authority.StartedGame.Series.Series.FirstParticipantID,
			SecondParticipantID: authority.StartedGame.Series.Series.SecondParticipantID,
			PreviousRevisionID: settlementCloneSeriesScoreRevisionIDPointer(
				authority.StartedGame.Series.Series.CurrentScoreRevisionID,
			),
			Ordinal:     authority.CurrentScoreOrdinal + 1,
			Format:      authority.StartedGame.Series.Series.Format,
			ScoreBefore: scoreBefore, ScoreAfter: scoreAfter,
			GameResultRevisionIDs: gameResultRevisionIDs, RecordedAt: settledAt,
		},
		Series: series.Series,
		Evidence: seriesdomain.SettlementEvidence{
			AuditEventID:             settlementEvidenceID(winnerCommand, "audit-event"),
			OutboxEventID:            settlementEvidenceID(winnerCommand, "outbox-event"),
			ProjectionRevisionID:     settlementEvidenceID(winnerCommand, "projection-revision"),
			SourceProjectionRevision: authority.CurrentProjectionRevision,
			ProjectionRevision:       authority.CurrentProjectionRevision + 1,
			RecordedAt:               settledAt,
		},
		SettledAt: settledAt,
	}
	if err := settlement.Validate(); err != nil {
		return SettlementRecord{}, err
	}
	return cloneConcurrentWinnerSettlement(settlement), nil
}

func settlementEvidenceIDs(command SettlementCommand) []uuid.UUID {
	return []uuid.UUID{
		settlementEvidenceID(command, "game-result"),
		settlementEvidenceID(command, "score-revision"),
		settlementEvidenceID(command, "series-result"),
		settlementEvidenceID(command, "audit-event"),
		settlementEvidenceID(command, "outbox-event"),
		settlementEvidenceID(command, "projection-revision"),
	}
}

func settlementEvidenceID(command SettlementCommand, role string) uuid.UUID {
	return uuid.NewSHA1(command.CommandID, []byte(
		"participant-settlement:"+role+":"+command.Scope.WaveID.String()+":"+
			command.Scope.Game.TournamentID.String()+":"+command.Scope.Game.SeriesID.String()+":"+
			command.Scope.Game.SlotID.String()+":"+command.Scope.Game.GameID.String()+":"+
			command.Scope.AssignmentID.String(),
	))
}

func scoreAfterWinner(
	series domain.Series,
	winnerID uuid.UUID,
) (domain.SeriesScore, error) {
	score := series.Score
	switch winnerID {
	case series.FirstParticipantID:
		score.FirstParticipantWins++
	case series.SecondParticipantID:
		score.SecondParticipantWins++
	default:
		return domain.SeriesScore{}, concurrentWinnerError("winner is not a Series participant")
	}
	if err := score.Validate(series.Format); err != nil {
		return domain.SeriesScore{}, concurrentWinnerError("score after winner: %v", err)
	}
	return score, nil
}

func validateSettledGame(settlement SettlementRecord) error {
	game := settlement.Game
	if err := game.Validate(); err != nil || game.ID != settlement.Scope.Game.GameID ||
		game.SlotID != settlement.Scope.Game.SlotID || game.State != domain.GameStateCompleted ||
		game.ResultReason != domain.GameResultReasonSolved || game.WinnerID == nil ||
		*game.WinnerID != settlement.WinningSubmission.ParticipantID ||
		game.ResultRevisionID == nil || *game.ResultRevisionID != settlement.SettlementGameResultRevision.ID {
		return concurrentWinnerError("invalid completed Game")
	}
	return nil
}

func validateGameResultRevision(settlement SettlementRecord) error {
	revision := settlement.SettlementGameResultRevision
	if revision.ID.IsZero() || revision.GameID != settlement.Game.ID ||
		revision.PreviousRevisionID != nil || revision.WinnerID != settlement.WinningSubmission.ParticipantID ||
		revision.Reason != domain.GameResultReasonSolved ||
		revision.WinningSubmissionSequence != settlement.WinningSubmission.Sequence ||
		!revision.RecordedAt.Equal(settlement.SettledAt) {
		return concurrentWinnerError("invalid Game result revision")
	}
	return nil
}

func validateSettlementScoreRevision(settlement SettlementRecord) error {
	revision := settlement.ScoreRevision
	if revision.ID.IsZero() || revision.SeriesID != settlement.Scope.Game.SeriesID ||
		revision.FirstParticipantID == uuid.Nil || revision.SecondParticipantID == uuid.Nil ||
		revision.FirstParticipantID == revision.SecondParticipantID ||
		revision.Ordinal < 1 || !revision.Format.IsValid() ||
		revision.ScoreBefore.Validate(revision.Format) != nil ||
		revision.ScoreAfter.Validate(revision.Format) != nil ||
		!revision.RecordedAt.Equal(settlement.SettledAt) ||
		len(revision.GameResultRevisionIDs) == 0 ||
		revision.GameResultRevisionIDs[len(revision.GameResultRevisionIDs)-1] != settlement.SettlementGameResultRevision.ID {
		return concurrentWinnerError("invalid Series score revision")
	}
	if !scoreRevisionAddsWinner(revision, settlement.WinningSubmission.ParticipantID, settlement) {
		return concurrentWinnerError("score revision does not add the Game winner")
	}
	if err := seriesdomain.ValidateGameResultRevisionIDs(revision.GameResultRevisionIDs); err != nil {
		return concurrentWinnerError("%v", err)
	}
	return nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validateSettledSeries(settlement SettlementRecord) error {
	series := settlement.Series
	if series.Validate() != nil || series.ID != settlement.Scope.Game.SeriesID ||
		series.TournamentID != settlement.Scope.Game.TournamentID ||
		series.Score != settlement.ScoreRevision.ScoreAfter || series.CurrentScoreRevisionID == nil ||
		*series.CurrentScoreRevisionID != settlement.ScoreRevision.ID {
		return concurrentWinnerError("invalid settled Series")
	}
	game, _, found := gamedomain.FindAttempt(
		gamedomain.Started{Series: seriesdomain.Execution{Series: series}},
		settlement.Scope.Game,
	)
	if !found || !settlementSettlementGamesEqual(game, settlement.Game) {
		return concurrentWinnerError("settled Series does not contain completed Game")
	}
	winnerID := series.Score.Winner(series.FirstParticipantID, series.SecondParticipantID, series.Format)
	if winnerID == nil {
		if series.State != domain.SeriesStateActive || series.WinnerID != nil || series.CurrentResultRevisionID != nil {
			return concurrentWinnerError("continuing Series has terminal evidence")
		}
		return nil
	}
	wantResultID := domain.OfficialResultRevisionID(settlementEvidenceID(SettlementCommand{
		Scope: settlement.Scope, CommandID: settlement.CommandID,
	}, "series-result"))
	if series.State != domain.SeriesStateCompleted || series.WinnerID == nil || *series.WinnerID != *winnerID ||
		series.CurrentResultRevisionID == nil || *series.CurrentResultRevisionID != wantResultID {
		return concurrentWinnerError("terminal Series has invalid result evidence")
	}
	return nil
}

func scoreRevisionAddsWinner(
	revision seriesdomain.ScoreRevision,
	winnerID uuid.UUID,
	settlement SettlementRecord,
) bool {
	if winnerID != settlement.WinningSubmission.ParticipantID ||
		winnerID != settlement.SettlementGameResultRevision.WinnerID {
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

func validateSettlementEvidence(settlement SettlementRecord) error {
	evidence := settlement.Evidence
	identities := []uuid.UUID{
		settlement.SettlementGameResultRevision.ID.UUID(), settlement.ScoreRevision.ID.UUID(),
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
	command := SettlementCommand{Scope: settlement.Scope, CommandID: settlement.CommandID}
	if evidence.AuditEventID != settlementEvidenceID(command, "audit-event") ||
		evidence.OutboxEventID != settlementEvidenceID(command, "outbox-event") ||
		evidence.ProjectionRevisionID != settlementEvidenceID(command, "projection-revision") {
		return concurrentWinnerError("settlement evidence is not deterministic")
	}
	return nil
}

func concurrentWinnerError(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %s",
		ErrInvalidConcurrentWinnerSettlement,
		fmt.Sprintf(format, arguments...),
	)
}
