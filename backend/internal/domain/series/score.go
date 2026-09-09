package series

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidSeriesScoreProgression = errors.New("invalid series score progression")

type NextGameWave struct {
	WaveID         uuid.UUID
	WaveRevisionID domain.WaveRevisionID
	SlotID         uuid.UUID
	GameID         uuid.UUID
	Category       domain.Category
}

type ScoreProgressionCommand struct {
	Game                     domain.Game
	ScoreRevision            ScoreRevision
	TerminalResultRevisionID *domain.OfficialResultRevisionID
	Next                     *NextGameWave
}

type ScoreProgression struct {
	Series        Execution
	ScoreRevision ScoreRevision
	NextWave      *domain.Wave
}

func ProgressScore(
	current Execution,
	command ScoreProgressionCommand,
) (ScoreProgression, bool, error) {
	if err := current.Validate(); err != nil {
		return ScoreProgression{}, false, seriesScoreProgressionError("Series: %v", err)
	}
	if err := validateSeriesScoreProgressionCommand(command); err != nil {
		return ScoreProgression{}, false, err
	}
	if seriesScoreRevisionIsCurrent(current.Series, command.ScoreRevision.ID) {
		progression, err := reconcileSeriesScoreProgression(current, command)
		return progression, false, err
	}
	if err := validateSeriesScoreSource(current.Series, command); err != nil {
		return ScoreProgression{}, false, err
	}

	progression, err := buildSeriesScoreProgression(current, command)
	if err != nil {
		return ScoreProgression{}, false, err
	}
	return progression, true, nil
}

func (p ScoreProgression) Validate() error {
	if err := p.Series.Validate(); err != nil {
		return seriesScoreProgressionError("Series: %v", err)
	}
	series := p.Series.Series
	if err := validateProgressedScoreRevision(series, p.ScoreRevision); err != nil {
		return err
	}
	game, scoreBefore, found := seriesGameByResultRevision(series, p.ScoreRevision)
	if !found || game.State != domain.GameStateCompleted || game.WinnerID == nil {
		return seriesScoreProgressionError("score revision has no completed Game winner")
	}
	if p.ScoreRevision.ScoreBefore != scoreBefore ||
		!scoreRevisionAddsCompletedGameWinner(p.ScoreRevision, game) {
		return seriesScoreProgressionError("score revision does not match the completed Game")
	}
	winnerID := series.Score.Winner(
		series.FirstParticipantID,
		series.SecondParticipantID,
		series.Format,
	)
	if winnerID != nil {
		return validateTerminalSeriesScoreProgression(p, *winnerID)
	}
	return validateContinuingSeriesScoreProgression(p)
}

func validateSeriesScoreProgressionCommand(command ScoreProgressionCommand) error {
	if !isCompletedSeriesScoreGame(command.Game) {
		return seriesScoreProgressionError("Game is not a completed one-winner result")
	}
	revision := command.ScoreRevision
	if !validSeriesScoreRevisionIdentity(revision) {
		return seriesScoreProgressionError("invalid score revision")
	}
	if revision.GameResultRevisionIDs[len(revision.GameResultRevisionIDs)-1] !=
		*command.Game.ResultRevisionID {
		return seriesScoreProgressionError("score revision does not end at the Game result")
	}
	if err := validateCurrentGameResultRevisionIDs(revision.GameResultRevisionIDs); err != nil {
		return seriesScoreProgressionError("score revision result order: %v", err)
	}
	return nil
}

func isCompletedSeriesScoreGame(game domain.Game) bool {
	return game.Validate() == nil && game.State == domain.GameStateCompleted &&
		game.WinnerID != nil && game.ResultRevisionID != nil
}

func validSeriesScoreRevisionIdentity(revision ScoreRevision) bool {
	return !revision.ID.IsZero() && revision.SeriesID != uuid.Nil &&
		revision.FirstParticipantID != uuid.Nil && revision.SecondParticipantID != uuid.Nil &&
		revision.FirstParticipantID != revision.SecondParticipantID && revision.Ordinal >= 1 &&
		revision.Format.IsValid() && revision.ScoreBefore.Validate(revision.Format) == nil &&
		revision.ScoreAfter.Validate(revision.Format) == nil &&
		domain.IsValidServerTime(revision.RecordedAt) && len(revision.GameResultRevisionIDs) > 0
}

func validateSeriesScoreSource(
	series domain.Series,
	command ScoreProgressionCommand,
) error {
	if series.State != domain.SeriesStateActive || series.WinnerID != nil ||
		series.CurrentResultRevisionID != nil {
		return seriesScoreProgressionError("Series is not active")
	}
	revision := command.ScoreRevision
	if revision.SeriesID != series.ID ||
		revision.FirstParticipantID != series.FirstParticipantID ||
		revision.SecondParticipantID != series.SecondParticipantID || revision.Format != series.Format ||
		revision.ScoreBefore != series.Score ||
		!seriesScoreRevisionPointersEqual(revision.PreviousRevisionID, series.CurrentScoreRevisionID) {
		return seriesScoreProgressionError("score revision does not follow the current Series head")
	}
	if !scoreRevisionAddsCompletedGameWinner(revision, command.Game) {
		return seriesScoreProgressionError("score revision does not add exactly the Game winner")
	}
	if err := validateCurrentSeriesGame(series, command.Game); err != nil {
		return err
	}
	return validateSeriesScoreRoute(series, command)
}

func validateCurrentSeriesGame(series domain.Series, completed domain.Game) error {
	for slotIndex := range series.Slots {
		slot := series.Slots[slotIndex]
		for attemptIndex := range slot.Attempts {
			attempt := slot.Attempts[attemptIndex]
			if attempt.ID != completed.ID {
				continue
			}
			if slotIndex != len(series.Slots)-1 || attemptIndex != len(slot.Attempts)-1 ||
				slot.ID != completed.SlotID || slot.ScoreBefore != series.Score ||
				attempt.SlotID != completed.SlotID || attempt.AttemptNo != completed.AttemptNo ||
				attempt.State != domain.GameStateActive {
				return seriesScoreProgressionError("Game is not the current active attempt")
			}
			return nil
		}
	}
	return seriesScoreProgressionError("Game does not belong to the Series")
}

func validateSeriesScoreRoute(
	series domain.Series,
	command ScoreProgressionCommand,
) error {
	winnerID := command.ScoreRevision.ScoreAfter.Winner(
		series.FirstParticipantID,
		series.SecondParticipantID,
		series.Format,
	)
	if winnerID != nil {
		if command.Next != nil || command.TerminalResultRevisionID == nil ||
			command.TerminalResultRevisionID.IsZero() {
			return seriesScoreProgressionError("terminal score requires only terminal result evidence")
		}
		return validateProgressionIdentities(command, true)
	}
	if series.Format != domain.SeriesFormatBO3 || command.Next == nil ||
		command.TerminalResultRevisionID != nil {
		return seriesScoreProgressionError("continuing BO3 score requires only the next Game Wave")
	}
	if len(series.Slots) >= series.Format.WinsRequired()*2-1 {
		return seriesScoreProgressionError("Series has no remaining Game slot")
	}
	if err := validateProgressionIdentities(command, false); err != nil {
		return err
	}
	return validateNextSeriesGameIdentity(series, *command.Next)
}

func validateProgressionIdentities(command ScoreProgressionCommand, terminal bool) error {
	identities := []uuid.UUID{
		command.Game.ID,
		command.Game.SlotID,
		command.Game.ResultRevisionID.UUID(),
		command.ScoreRevision.ID.UUID(),
	}
	if terminal {
		identities = append(identities, command.TerminalResultRevisionID.UUID())
	} else {
		if err := validateNextSeriesGameWave(*command.Next); err != nil {
			return err
		}
		identities = append(identities,
			command.Next.WaveID,
			command.Next.WaveRevisionID.UUID(),
			command.Next.SlotID,
			command.Next.GameID,
		)
	}
	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, identity := range identities {
		if identity == uuid.Nil {
			return seriesScoreProgressionError("missing progression identity")
		}
		if _, duplicate := seen[identity]; duplicate {
			return seriesScoreProgressionError("reused progression identity")
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func validateNextSeriesGameWave(next NextGameWave) error {
	if next.WaveID == uuid.Nil || next.WaveRevisionID.IsZero() ||
		next.SlotID == uuid.Nil || next.GameID == uuid.Nil || !next.Category.IsValid() {
		return seriesScoreProgressionError("invalid next Game Wave")
	}
	return nil
}

func validateNextSeriesGameIdentity(series domain.Series, next NextGameWave) error {
	for _, slot := range series.Slots {
		if slot.ID == next.SlotID || slot.Category == next.Category {
			return seriesScoreProgressionError("next Game reuses a Series slot or category")
		}
		for _, game := range slot.Attempts {
			if game.ID == next.GameID {
				return seriesScoreProgressionError("next Game reuses a Game identity")
			}
		}
	}
	return nil
}

func scoreRevisionAddsCompletedGameWinner(
	revision ScoreRevision,
	game domain.Game,
) bool {
	if game.WinnerID == nil {
		return false
	}
	firstDelta := revision.ScoreAfter.FirstParticipantWins - revision.ScoreBefore.FirstParticipantWins
	secondDelta := revision.ScoreAfter.SecondParticipantWins - revision.ScoreBefore.SecondParticipantWins
	switch *game.WinnerID {
	case revision.FirstParticipantID:
		return firstDelta == 1 && secondDelta == 0
	case revision.SecondParticipantID:
		return firstDelta == 0 && secondDelta == 1
	default:
		return false
	}
}

func buildSeriesScoreProgression(
	current Execution,
	command ScoreProgressionCommand,
) (ScoreProgression, error) {
	working := cloneSeriesExecution(current)
	if !replaceSeriesGameResult(&working.Series, command.Game) {
		return ScoreProgression{}, seriesScoreProgressionError("current Game changed during progression")
	}
	working.Series.Score = command.ScoreRevision.ScoreAfter
	scoreRevisionID := command.ScoreRevision.ID
	working.Series.CurrentScoreRevisionID = &scoreRevisionID

	winnerID := working.Series.Score.Winner(
		working.Series.FirstParticipantID,
		working.Series.SecondParticipantID,
		working.Series.Format,
	)
	progression := ScoreProgression{
		Series: working, ScoreRevision: cloneSettlementScoreRevision(command.ScoreRevision),
	}
	if winnerID != nil {
		terminal, changed, err := Transition(working, TransitionCommand{
			NextState: domain.SeriesStateCompleted,
			Terminal: &TerminalEvidence{
				Score: working.Series.Score, WinnerID: winnerID,
				ScoreRevisionID:  &scoreRevisionID,
				ResultRevisionID: cloneOfficialResultRevisionIDPointer(command.TerminalResultRevisionID),
			},
		})
		if err != nil || !changed {
			return ScoreProgression{}, seriesScoreProgressionError("complete Series: %v", err)
		}
		progression.Series = terminal
	} else {
		nextWave, err := appendNextSeriesGameWave(&working.Series, *command.Next)
		if err != nil {
			return ScoreProgression{}, err
		}
		progression.Series = working
		progression.NextWave = &nextWave
	}
	if err := progression.Validate(); err != nil {
		return ScoreProgression{}, err
	}
	return cloneSeriesScoreProgression(progression), nil
}

func replaceSeriesGameResult(series *domain.Series, completed domain.Game) bool {
	for slotIndex := range series.Slots {
		for attemptIndex := range series.Slots[slotIndex].Attempts {
			if series.Slots[slotIndex].Attempts[attemptIndex].ID == completed.ID {
				series.Slots[slotIndex].Attempts[attemptIndex] = cloneGame(completed)
				return true
			}
		}
	}
	return false
}

func appendNextSeriesGameWave(
	series *domain.Series,
	next NextGameWave,
) (domain.Wave, error) {
	if err := validateNextSeriesGameIdentity(*series, next); err != nil {
		return domain.Wave{}, err
	}
	position := len(series.Slots) + 1
	series.Slots = append(series.Slots, domain.GameSlot{
		ID: next.SlotID, SeriesID: series.ID, Position: position,
		Category: next.Category, ScoreBefore: series.Score,
		Attempts: []domain.Game{{
			ID: next.GameID, SlotID: next.SlotID, AttemptNo: 1,
			State: domain.GameStatePlanned,
		}},
	})
	wave := domain.Wave{
		ID: next.WaveID, TournamentID: series.TournamentID,
		RevisionID: next.WaveRevisionID, State: domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: series.FirstParticipantID},
			{ParticipantID: series.SecondParticipantID},
		},
	}
	if err := wave.Validate(); err != nil {
		return domain.Wave{}, seriesScoreProgressionError("next Wave: %v", err)
	}
	return wave, nil
}

func validateProgressedScoreRevision(
	series domain.Series,
	revision ScoreRevision,
) error {
	if !validSeriesScoreRevisionIdentity(revision) || revision.SeriesID != series.ID ||
		revision.FirstParticipantID != series.FirstParticipantID ||
		revision.SecondParticipantID != series.SecondParticipantID || revision.Format != series.Format ||
		revision.ScoreAfter != series.Score || series.CurrentScoreRevisionID == nil ||
		*series.CurrentScoreRevisionID != revision.ID ||
		validateCurrentGameResultRevisionIDs(revision.GameResultRevisionIDs) != nil {
		return seriesScoreProgressionError("score revision does not match progressed Series")
	}
	return nil
}

func seriesGameByResultRevision(
	series domain.Series,
	revision ScoreRevision,
) (domain.Game, domain.SeriesScore, bool) {
	want := revision.GameResultRevisionIDs[len(revision.GameResultRevisionIDs)-1]
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ResultRevisionID != nil && *game.ResultRevisionID == want {
				return game, slot.ScoreBefore, true
			}
		}
	}
	return domain.Game{}, domain.SeriesScore{}, false
}

func validateTerminalSeriesScoreProgression(
	progression ScoreProgression,
	winnerID uuid.UUID,
) error {
	series := progression.Series.Series
	if series.State != domain.SeriesStateCompleted || series.WinnerID == nil ||
		*series.WinnerID != winnerID || series.CurrentResultRevisionID == nil ||
		progression.NextWave != nil {
		return seriesScoreProgressionError("terminal score has invalid Series result evidence")
	}
	return nil
}

func validateContinuingSeriesScoreProgression(progression ScoreProgression) error {
	series := progression.Series.Series
	if !isContinuingBO3Series(series, progression.NextWave) {
		return seriesScoreProgressionError("non-terminal score has invalid next Game evidence")
	}
	slot := series.Slots[len(series.Slots)-1]
	if slot.ScoreBefore != series.Score || len(slot.Attempts) != 1 ||
		slot.Attempts[0].State != domain.GameStatePlanned {
		return seriesScoreProgressionError("next Game slot is not planned from current score")
	}
	if !nextWaveMatchesSeries(*progression.NextWave, series) {
		return seriesScoreProgressionError("next Wave does not match Series participants")
	}
	return nil
}

func isContinuingBO3Series(series domain.Series, nextWave *domain.Wave) bool {
	return series.Format == domain.SeriesFormatBO3 &&
		series.State == domain.SeriesStateActive && series.WinnerID == nil &&
		series.CurrentResultRevisionID == nil && nextWave != nil && len(series.Slots) >= 2
}

func nextWaveMatchesSeries(wave domain.Wave, series domain.Series) bool {
	return wave.Validate() == nil && wave.TournamentID == series.TournamentID &&
		wave.State == domain.WaveStatePlanned && len(wave.Members) == 2 &&
		wave.Members[0].ParticipantID == series.FirstParticipantID &&
		wave.Members[1].ParticipantID == series.SecondParticipantID
}

func seriesScoreRevisionIsCurrent(
	series domain.Series,
	revisionID domain.SeriesScoreRevisionID,
) bool {
	return series.CurrentScoreRevisionID != nil && *series.CurrentScoreRevisionID == revisionID
}

func seriesContainsCompletedGame(series domain.Series, completed domain.Game) bool {
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == completed.ID {
				return settlementGamesEqual(game, completed)
			}
		}
	}
	return false
}

func cloneSettlementScoreRevision(
	revision ScoreRevision,
) ScoreRevision {
	clone := revision
	clone.PreviousRevisionID = cloneSeriesScoreRevisionIDPointer(revision.PreviousRevisionID)
	clone.GameResultRevisionIDs = append(
		[]domain.OfficialResultRevisionID(nil),
		revision.GameResultRevisionIDs...,
	)
	return clone
}

func cloneSeriesScoreProgression(progression ScoreProgression) ScoreProgression {
	clone := progression
	clone.Series = cloneSeriesExecution(progression.Series)
	clone.ScoreRevision = cloneSettlementScoreRevision(progression.ScoreRevision)
	if progression.NextWave != nil {
		wave := cloneWaveExecution(*progression.NextWave)
		clone.NextWave = &wave
	}
	return clone
}

func seriesScoreProgressionError(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %s",
		ErrInvalidSeriesScoreProgression,
		fmt.Sprintf(format, arguments...),
	)
}
