package arena

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidSeriesScoreProgression = errors.New("invalid Arena Series score progression")

type NextSeriesGameWave struct {
	WaveID         uuid.UUID
	WaveRevisionID domain.ArenaWaveRevisionID
	SlotID         uuid.UUID
	GameID         uuid.UUID
	Category       domain.Category
}

type SeriesScoreProgressionCommand struct {
	Game                     domain.ArenaGame
	ScoreRevision            ArenaSettlementScoreRevision
	TerminalResultRevisionID *domain.ArenaOfficialResultRevisionID
	Next                     *NextSeriesGameWave
}

type SeriesScoreProgression struct {
	Series        SeriesExecution
	ScoreRevision ArenaSettlementScoreRevision
	NextWave      *domain.ArenaWave
}

func ProgressSeriesScore(
	current SeriesExecution,
	command SeriesScoreProgressionCommand,
) (SeriesScoreProgression, bool, error) {
	if err := current.Validate(); err != nil {
		return SeriesScoreProgression{}, false, seriesScoreProgressionError("Series: %v", err)
	}
	if err := validateSeriesScoreProgressionCommand(command); err != nil {
		return SeriesScoreProgression{}, false, err
	}
	if seriesScoreRevisionIsCurrent(current.Series, command.ScoreRevision.ID) {
		progression, err := reconcileSeriesScoreProgression(current, command)
		return progression, false, err
	}
	if err := validateSeriesScoreSource(current.Series, command); err != nil {
		return SeriesScoreProgression{}, false, err
	}

	progression, err := buildSeriesScoreProgression(current, command)
	if err != nil {
		return SeriesScoreProgression{}, false, err
	}
	return progression, true, nil
}

func (p SeriesScoreProgression) Validate() error {
	if err := p.Series.Validate(); err != nil {
		return seriesScoreProgressionError("Series: %v", err)
	}
	series := p.Series.Series
	if err := validateProgressedScoreRevision(series, p.ScoreRevision); err != nil {
		return err
	}
	game, scoreBefore, found := seriesGameByResultRevision(series, p.ScoreRevision)
	if !found || game.State != domain.ArenaGameStateCompleted || game.WinnerID == nil {
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

func validateSeriesScoreProgressionCommand(command SeriesScoreProgressionCommand) error {
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

func isCompletedSeriesScoreGame(game domain.ArenaGame) bool {
	return game.Validate() == nil && game.State == domain.ArenaGameStateCompleted &&
		game.WinnerID != nil && game.ResultRevisionID != nil
}

func validSeriesScoreRevisionIdentity(revision ArenaSettlementScoreRevision) bool {
	return !revision.ID.IsZero() && revision.SeriesID != uuid.Nil &&
		revision.FirstParticipantID != uuid.Nil && revision.SecondParticipantID != uuid.Nil &&
		revision.FirstParticipantID != revision.SecondParticipantID && revision.Ordinal >= 1 &&
		revision.Format.IsValid() && revision.ScoreBefore.Validate(revision.Format) == nil &&
		revision.ScoreAfter.Validate(revision.Format) == nil &&
		validArenaServerTime(revision.RecordedAt) && len(revision.GameResultRevisionIDs) > 0
}

func validateSeriesScoreSource(
	series domain.ArenaSeries,
	command SeriesScoreProgressionCommand,
) error {
	if series.State != domain.ArenaSeriesStateActive || series.WinnerID != nil ||
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

func validateCurrentSeriesGame(series domain.ArenaSeries, completed domain.ArenaGame) error {
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
				attempt.State != domain.ArenaGameStateActive {
				return seriesScoreProgressionError("Game is not the current active attempt")
			}
			return nil
		}
	}
	return seriesScoreProgressionError("Game does not belong to the Series")
}

func validateSeriesScoreRoute(
	series domain.ArenaSeries,
	command SeriesScoreProgressionCommand,
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
	if series.Format != domain.ArenaSeriesFormatBO3 || command.Next == nil ||
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

func validateProgressionIdentities(command SeriesScoreProgressionCommand, terminal bool) error {
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

func validateNextSeriesGameWave(next NextSeriesGameWave) error {
	if next.WaveID == uuid.Nil || next.WaveRevisionID.IsZero() ||
		next.SlotID == uuid.Nil || next.GameID == uuid.Nil || !next.Category.IsValid() {
		return seriesScoreProgressionError("invalid next Game Wave")
	}
	return nil
}

func validateNextSeriesGameIdentity(series domain.ArenaSeries, next NextSeriesGameWave) error {
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
	revision ArenaSettlementScoreRevision,
	game domain.ArenaGame,
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
	current SeriesExecution,
	command SeriesScoreProgressionCommand,
) (SeriesScoreProgression, error) {
	working := cloneSeriesExecution(current)
	if !replaceSeriesGameResult(&working.Series, command.Game) {
		return SeriesScoreProgression{}, seriesScoreProgressionError("current Game changed during progression")
	}
	working.Series.Score = command.ScoreRevision.ScoreAfter
	scoreRevisionID := command.ScoreRevision.ID
	working.Series.CurrentScoreRevisionID = &scoreRevisionID

	winnerID := working.Series.Score.Winner(
		working.Series.FirstParticipantID,
		working.Series.SecondParticipantID,
		working.Series.Format,
	)
	progression := SeriesScoreProgression{
		Series: working, ScoreRevision: cloneArenaSettlementScoreRevision(command.ScoreRevision),
	}
	if winnerID != nil {
		terminal, changed, err := TransitionSeriesExecution(working, SeriesExecutionTransitionCommand{
			NextState: domain.ArenaSeriesStateCompleted,
			Terminal: &SeriesTerminalEvidence{
				Score: working.Series.Score, WinnerID: winnerID,
				ScoreRevisionID:  &scoreRevisionID,
				ResultRevisionID: cloneOfficialResultRevisionIDPointer(command.TerminalResultRevisionID),
			},
		})
		if err != nil || !changed {
			return SeriesScoreProgression{}, seriesScoreProgressionError("complete Series: %v", err)
		}
		progression.Series = terminal
	} else {
		nextWave, err := appendNextSeriesGameWave(&working.Series, *command.Next)
		if err != nil {
			return SeriesScoreProgression{}, err
		}
		progression.Series = working
		progression.NextWave = &nextWave
	}
	if err := progression.Validate(); err != nil {
		return SeriesScoreProgression{}, err
	}
	return cloneSeriesScoreProgression(progression), nil
}

func replaceSeriesGameResult(series *domain.ArenaSeries, completed domain.ArenaGame) bool {
	for slotIndex := range series.Slots {
		for attemptIndex := range series.Slots[slotIndex].Attempts {
			if series.Slots[slotIndex].Attempts[attemptIndex].ID == completed.ID {
				series.Slots[slotIndex].Attempts[attemptIndex] = cloneArenaGame(completed)
				return true
			}
		}
	}
	return false
}

func appendNextSeriesGameWave(
	series *domain.ArenaSeries,
	next NextSeriesGameWave,
) (domain.ArenaWave, error) {
	if err := validateNextSeriesGameIdentity(*series, next); err != nil {
		return domain.ArenaWave{}, err
	}
	position := len(series.Slots) + 1
	series.Slots = append(series.Slots, domain.ArenaGameSlot{
		ID: next.SlotID, SeriesID: series.ID, Position: position,
		Category: next.Category, ScoreBefore: series.Score,
		Attempts: []domain.ArenaGame{{
			ID: next.GameID, SlotID: next.SlotID, AttemptNo: 1,
			State: domain.ArenaGameStatePlanned,
		}},
	})
	wave := domain.ArenaWave{
		ID: next.WaveID, TournamentID: series.TournamentID,
		RevisionID: next.WaveRevisionID, State: domain.ArenaWaveStatePlanned,
		Members: []domain.ArenaWaveMember{
			{ParticipantID: series.FirstParticipantID},
			{ParticipantID: series.SecondParticipantID},
		},
	}
	if err := wave.Validate(); err != nil {
		return domain.ArenaWave{}, seriesScoreProgressionError("next Wave: %v", err)
	}
	return wave, nil
}

func reconcileSeriesScoreProgression(
	current SeriesExecution,
	command SeriesScoreProgressionCommand,
) (SeriesScoreProgression, error) {
	series := current.Series
	if series.Score != command.ScoreRevision.ScoreAfter ||
		command.ScoreRevision.SeriesID != series.ID ||
		command.ScoreRevision.FirstParticipantID != series.FirstParticipantID ||
		command.ScoreRevision.SecondParticipantID != series.SecondParticipantID ||
		command.ScoreRevision.Format != series.Format ||
		!seriesContainsCompletedGame(series, command.Game) {
		return SeriesScoreProgression{}, seriesScoreProgressionError("duplicate result does not match current Series")
	}
	winnerID := series.Score.Winner(
		series.FirstParticipantID,
		series.SecondParticipantID,
		series.Format,
	)
	if err := validateReconciledSeriesRoute(current, command, winnerID); err != nil {
		return SeriesScoreProgression{}, err
	}
	progression := SeriesScoreProgression{
		Series: current, ScoreRevision: cloneArenaSettlementScoreRevision(command.ScoreRevision),
	}
	if winnerID == nil {
		nextWave, err := reconciledNextSeriesWave(series, *command.Next)
		if err != nil {
			return SeriesScoreProgression{}, err
		}
		progression.NextWave = &nextWave
	}
	if err := progression.Validate(); err != nil {
		return SeriesScoreProgression{}, err
	}
	return cloneSeriesScoreProgression(progression), nil
}

func validateReconciledSeriesRoute(
	current SeriesExecution,
	command SeriesScoreProgressionCommand,
	winnerID *uuid.UUID,
) error {
	if winnerID != nil {
		if command.Next != nil || command.TerminalResultRevisionID == nil ||
			current.Series.State != domain.ArenaSeriesStateCompleted || current.Series.WinnerID == nil ||
			*current.Series.WinnerID != *winnerID || current.Series.CurrentResultRevisionID == nil ||
			*current.Series.CurrentResultRevisionID != *command.TerminalResultRevisionID {
			return seriesScoreProgressionError("duplicate terminal result does not match current Series")
		}
		return validateProgressionIdentities(command, true)
	}
	if command.Next == nil || command.TerminalResultRevisionID != nil ||
		current.Series.State != domain.ArenaSeriesStateActive || current.Series.WinnerID != nil ||
		current.Series.CurrentResultRevisionID != nil {
		return seriesScoreProgressionError("duplicate continuing result does not match current Series")
	}
	return validateProgressionIdentities(command, false)
}

func reconciledNextSeriesWave(
	series domain.ArenaSeries,
	next NextSeriesGameWave,
) (domain.ArenaWave, error) {
	if len(series.Slots) < 2 {
		return domain.ArenaWave{}, seriesScoreProgressionError("duplicate result has no next Game slot")
	}
	slot := series.Slots[len(series.Slots)-1]
	if slot.ID != next.SlotID || slot.Category != next.Category ||
		slot.ScoreBefore != series.Score || len(slot.Attempts) != 1 ||
		slot.Attempts[0].ID != next.GameID || slot.Attempts[0].State != domain.ArenaGameStatePlanned {
		return domain.ArenaWave{}, seriesScoreProgressionError("duplicate result has a different next Game slot")
	}
	series.Slots = series.Slots[:len(series.Slots)-1]
	return appendNextSeriesGameWave(&series, next)
}

func validateProgressedScoreRevision(
	series domain.ArenaSeries,
	revision ArenaSettlementScoreRevision,
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
	series domain.ArenaSeries,
	revision ArenaSettlementScoreRevision,
) (domain.ArenaGame, domain.ArenaSeriesScore, bool) {
	want := revision.GameResultRevisionIDs[len(revision.GameResultRevisionIDs)-1]
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ResultRevisionID != nil && *game.ResultRevisionID == want {
				return game, slot.ScoreBefore, true
			}
		}
	}
	return domain.ArenaGame{}, domain.ArenaSeriesScore{}, false
}

func validateTerminalSeriesScoreProgression(
	progression SeriesScoreProgression,
	winnerID uuid.UUID,
) error {
	series := progression.Series.Series
	if series.State != domain.ArenaSeriesStateCompleted || series.WinnerID == nil ||
		*series.WinnerID != winnerID || series.CurrentResultRevisionID == nil ||
		progression.NextWave != nil {
		return seriesScoreProgressionError("terminal score has invalid Series result evidence")
	}
	return nil
}

func validateContinuingSeriesScoreProgression(progression SeriesScoreProgression) error {
	series := progression.Series.Series
	if !isContinuingBO3Series(series, progression.NextWave) {
		return seriesScoreProgressionError("non-terminal score has invalid next Game evidence")
	}
	slot := series.Slots[len(series.Slots)-1]
	if slot.ScoreBefore != series.Score || len(slot.Attempts) != 1 ||
		slot.Attempts[0].State != domain.ArenaGameStatePlanned {
		return seriesScoreProgressionError("next Game slot is not planned from current score")
	}
	if !nextWaveMatchesSeries(*progression.NextWave, series) {
		return seriesScoreProgressionError("next Wave does not match Series participants")
	}
	return nil
}

func isContinuingBO3Series(series domain.ArenaSeries, nextWave *domain.ArenaWave) bool {
	return series.Format == domain.ArenaSeriesFormatBO3 &&
		series.State == domain.ArenaSeriesStateActive && series.WinnerID == nil &&
		series.CurrentResultRevisionID == nil && nextWave != nil && len(series.Slots) >= 2
}

func nextWaveMatchesSeries(wave domain.ArenaWave, series domain.ArenaSeries) bool {
	return wave.Validate() == nil && wave.TournamentID == series.TournamentID &&
		wave.State == domain.ArenaWaveStatePlanned && len(wave.Members) == 2 &&
		wave.Members[0].ParticipantID == series.FirstParticipantID &&
		wave.Members[1].ParticipantID == series.SecondParticipantID
}

func seriesScoreRevisionIsCurrent(
	series domain.ArenaSeries,
	revisionID domain.ArenaSeriesScoreRevisionID,
) bool {
	return series.CurrentScoreRevisionID != nil && *series.CurrentScoreRevisionID == revisionID
}

func seriesContainsCompletedGame(series domain.ArenaSeries, completed domain.ArenaGame) bool {
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == completed.ID {
				return arenaSettlementGamesEqual(game, completed)
			}
		}
	}
	return false
}

func cloneArenaSettlementScoreRevision(
	revision ArenaSettlementScoreRevision,
) ArenaSettlementScoreRevision {
	clone := revision
	clone.PreviousRevisionID = cloneSeriesScoreRevisionIDPointer(revision.PreviousRevisionID)
	clone.GameResultRevisionIDs = append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		revision.GameResultRevisionIDs...,
	)
	return clone
}

func cloneSeriesScoreProgression(progression SeriesScoreProgression) SeriesScoreProgression {
	clone := progression
	clone.Series = cloneSeriesExecution(progression.Series)
	clone.ScoreRevision = cloneArenaSettlementScoreRevision(progression.ScoreRevision)
	if progression.NextWave != nil {
		wave := cloneArenaWaveExecution(*progression.NextWave)
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
