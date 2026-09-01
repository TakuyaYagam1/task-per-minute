package arena

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidFinal = errors.New("invalid Arena final")

type FinalCommand struct {
	SeriesID               uuid.UUID
	Advancement            SemifinalAdvancement
	Draft                  domain.ArenaDraft
	InitialScoreRevisionID domain.ArenaSeriesScoreRevisionID
	FirstSlotID            uuid.UUID
	FirstGameID            uuid.UUID
}

type FinalProgressionCommand struct {
	Progression        SeriesScoreProgressionCommand
	ChampionRevisionID domain.ArenaDerivedRevisionID
	RecordedAt         time.Time
}

type Final struct {
	execution            SeriesExecution
	tournament           domain.ArenaTournament
	draft                domain.ArenaDraft
	gameCategories       [3]domain.Category
	bracketRevisionID    domain.ArenaDerivedRevisionID
	championRevisions    []domain.ArenaProjectionRevision
	championDependencies []domain.ArenaRevisionDependency
}

type finalChampionPayload struct {
	TournamentID uuid.UUID                 `json:"tournament_id"`
	SeriesID     uuid.UUID                 `json:"series_id"`
	ChampionID   uuid.UUID                 `json:"champion_id"`
	Score        finalChampionScorePayload `json:"score"`
}

type finalChampionScorePayload struct {
	FirstParticipantWins  int `json:"first_participant_wins"`
	SecondParticipantWins int `json:"second_participant_wins"`
}

func NewFinal(command FinalCommand) (Final, error) {
	participants := command.Advancement.FinalParticipants()
	categories, err := BO3FinalDraftGameCategories(command.Draft)
	if !command.Advancement.Complete() || len(participants) != 2 || err != nil ||
		command.SeriesID == uuid.Nil || command.InitialScoreRevisionID.IsZero() ||
		command.FirstSlotID == uuid.Nil || command.FirstGameID == uuid.Nil ||
		command.Draft.SeriesID != command.SeriesID ||
		command.Draft.FirstParticipantID != participants[0] ||
		command.Draft.SecondParticipantID != participants[1] {
		return Final{}, finalError("invalid final authority")
	}
	if command.SeriesID == command.FirstSlotID || command.SeriesID == command.FirstGameID ||
		command.FirstSlotID == command.FirstGameID {
		return Final{}, finalError("reused final identity")
	}
	tournamentID := command.Advancement.tournamentID
	result := Final{
		execution: SeriesExecution{Series: domain.ArenaSeries{
			ID: command.SeriesID, TournamentID: tournamentID,
			FirstParticipantID: participants[0], SecondParticipantID: participants[1],
			Format: domain.ArenaSeriesFormatBO3, State: domain.ArenaSeriesStateActive,
			CurrentScoreRevisionID: cloneSeriesScoreRevisionIDPointer(&command.InitialScoreRevisionID),
			Slots: []domain.ArenaGameSlot{{
				ID: command.FirstSlotID, SeriesID: command.SeriesID, Position: 1,
				Category: categories[0], ScoreBefore: domain.ArenaSeriesScore{},
				Attempts: []domain.ArenaGame{{
					ID: command.FirstGameID, SlotID: command.FirstSlotID,
					AttemptNo: 1, State: domain.ArenaGameStateActive,
				}},
			}},
		}},
		tournament:        domain.ArenaTournament{State: domain.ArenaTournamentStatePlayoffs},
		draft:             cloneArenaDraft(command.Draft),
		gameCategories:    categories,
		bracketRevisionID: command.Advancement.bracketRevisionID,
	}
	if err := result.Validate(); err != nil {
		return Final{}, err
	}
	return cloneFinal(result), nil
}

func ProgressFinal(current Final, command FinalProgressionCommand) (Final, bool, error) {
	if err := current.Validate(); err != nil {
		return Final{}, false, err
	}
	if err := validateFinalProgressionCommand(current, command); err != nil {
		return Final{}, false, err
	}
	working := cloneSeriesExecution(current.execution)
	activateFinalGame(&working.Series, command.Progression.Game.ID)
	progression, changed, err := ProgressSeriesScore(working, command.Progression)
	if err != nil {
		return Final{}, false, finalError("progress score: %v", err)
	}
	if !changed {
		return cloneFinal(current), false, nil
	}
	next := cloneFinal(current)
	next.execution = progression.Series
	if !next.execution.Series.State.IsTerminal() {
		if err := next.Validate(); err != nil {
			return Final{}, false, err
		}
		return next, true, nil
	}
	champion, err := newChampionRevision(next.execution.Series, command)
	if err != nil {
		return Final{}, false, err
	}
	transitioned, err := next.tournament.TransitionTo(domain.ArenaTournamentStateCompleted)
	if err != nil || !transitioned {
		return Final{}, false, finalError("complete Tournament: %v", err)
	}
	next.championRevisions = append(next.championRevisions, champion)
	next.championDependencies = append(next.championDependencies, domain.ArenaRevisionDependency{
		SourceRevisionID: next.bracketRevisionID, DerivedRevisionID: champion.Revision().ID(),
	})
	if err := next.Validate(); err != nil {
		return Final{}, false, err
	}
	return cloneFinal(next), true, nil
}

func (f Final) Validate() error {
	if err := f.execution.Validate(); err != nil {
		return finalError("Series: %v", err)
	}
	if err := f.tournament.Validate(); err != nil {
		return finalError("Tournament: %v", err)
	}
	if err := validateFinalDraftBinding(f); err != nil {
		return err
	}
	switch len(f.championRevisions) {
	case 0:
		return validateLiveFinal(f)
	case 1:
		return validateCompletedFinal(f)
	default:
		return finalError("completed final must have one champion")
	}
}

func validateFinalDraftBinding(f Final) error {
	categories, err := BO3FinalDraftGameCategories(f.draft)
	if err != nil {
		return finalError("final has an invalid locked draft")
	}
	series := f.execution.Series
	if categories != f.gameCategories || f.bracketRevisionID.IsZero() {
		return finalError("final no longer matches its locked draft")
	}
	if series.Format != domain.ArenaSeriesFormatBO3 {
		return finalError("final no longer matches its locked draft")
	}
	if series.FirstParticipantID != f.draft.FirstParticipantID ||
		series.SecondParticipantID != f.draft.SecondParticipantID {
		return finalError("final no longer matches its locked draft")
	}
	if len(series.Slots) < 1 || len(series.Slots) > len(categories) {
		return finalError("final no longer matches its locked draft")
	}
	for index, slot := range series.Slots {
		if slot.Position != index+1 || slot.Category != categories[index] {
			return finalError("Game %d differs from locked draft", index+1)
		}
	}
	return nil
}

func validateLiveFinal(f Final) error {
	if len(f.championDependencies) != 0 {
		return finalError("live final has terminal evidence")
	}
	if f.execution.Series.State.IsTerminal() {
		return finalError("live final has terminal evidence")
	}
	if f.tournament.State != domain.ArenaTournamentStatePlayoffs {
		return finalError("live final has terminal evidence")
	}
	return nil
}

func validateCompletedFinal(f Final) error {
	series := f.execution.Series
	if len(f.championDependencies) != 1 {
		return finalError("completed final must have one champion")
	}
	if series.State != domain.ArenaSeriesStateCompleted || series.WinnerID == nil {
		return finalError("completed final must have one champion")
	}
	if f.tournament.State != domain.ArenaTournamentStateCompleted {
		return finalError("completed final must have one champion")
	}
	revision := f.championRevisions[0]
	if err := revision.Validate(); err != nil {
		return finalError("invalid champion revision")
	}
	if revision.Revision().Artifact() != (domain.ArenaArtifactRef{
		Kind: domain.ArenaArtifactKindChampion, EntityID: series.TournamentID,
	}) {
		return finalError("invalid champion revision")
	}
	dependency := f.championDependencies[0]
	if dependency.SourceRevisionID != f.bracketRevisionID {
		return finalError("invalid champion dependency")
	}
	if dependency.DerivedRevisionID != revision.Revision().ID() {
		return finalError("invalid champion dependency")
	}
	if dependency.SourceRevisionID == dependency.DerivedRevisionID {
		return finalError("invalid champion dependency")
	}
	return nil
}

func (f Final) Execution() SeriesExecution {
	return cloneSeriesExecution(f.execution)
}

func (f Final) Tournament() domain.ArenaTournament {
	return f.tournament
}

func (f Final) GameCategories() [3]domain.Category {
	return f.gameCategories
}

func (f Final) ChampionRevisions() []domain.ArenaProjectionRevision {
	return append([]domain.ArenaProjectionRevision(nil), f.championRevisions...)
}

func (f Final) ChampionDependencies() []domain.ArenaRevisionDependency {
	return append([]domain.ArenaRevisionDependency(nil), f.championDependencies...)
}

func (f Final) ChampionID() uuid.UUID {
	if f.execution.Series.WinnerID == nil {
		return uuid.Nil
	}
	return *f.execution.Series.WinnerID
}

func validateFinalProgressionCommand(current Final, command FinalProgressionCommand) error {
	series := current.execution.Series
	revision := command.Progression.ScoreRevision
	replay := series.CurrentScoreRevisionID != nil && *series.CurrentScoreRevisionID == revision.ID &&
		revision.ScoreAfter == series.Score
	if err := validateFinalScoreTarget(series, revision, replay); err != nil {
		return err
	}
	winner := revision.ScoreAfter.Winner(series.FirstParticipantID, series.SecondParticipantID, series.Format)
	if winner == nil {
		return validateContinuingFinalCommand(current, command, replay)
	}
	return validateTerminalFinalCommand(current, command, replay)
}

func validateFinalScoreTarget(
	series domain.ArenaSeries,
	revision ArenaSettlementScoreRevision,
	replay bool,
) error {
	if revision.SeriesID != series.ID {
		return finalError("score command targets another final")
	}
	if revision.Format != domain.ArenaSeriesFormatBO3 {
		return finalError("score command targets another final")
	}
	if !replay && revision.ScoreBefore != series.Score {
		return finalError("score command targets another final")
	}
	return nil
}

func validateContinuingFinalCommand(
	current Final,
	command FinalProgressionCommand,
	replay bool,
) error {
	next := command.Progression.Next
	if next == nil {
		return finalError("continuing final must schedule its next locked category")
	}
	if !command.ChampionRevisionID.IsZero() || !command.RecordedAt.IsZero() {
		return finalError("continuing final has terminal evidence")
	}
	if replay {
		return nil
	}
	expectedPosition := len(current.execution.Series.Slots) + 1
	if expectedPosition > len(current.gameCategories) {
		return finalError("continuing final must schedule its next locked category")
	}
	if next.Category != current.gameCategories[expectedPosition-1] {
		return finalError("continuing final must schedule its next locked category")
	}
	return nil
}

func validateTerminalFinalCommand(
	current Final,
	command FinalProgressionCommand,
	replay bool,
) error {
	if command.Progression.Next != nil || command.Progression.TerminalResultRevisionID == nil {
		return finalError("terminal final lacks champion evidence")
	}
	if command.ChampionRevisionID.IsZero() || !validArenaServerTime(command.RecordedAt) {
		return finalError("terminal final lacks champion evidence")
	}
	if command.RecordedAt.Before(command.Progression.ScoreRevision.RecordedAt) {
		return finalError("terminal final lacks champion evidence")
	}
	if command.ChampionRevisionID == current.bracketRevisionID {
		return finalError("terminal final lacks champion evidence")
	}
	if replay {
		return validateTerminalFinalReplay(current, command)
	}
	return nil
}

func validateTerminalFinalReplay(current Final, command FinalProgressionCommand) error {
	if len(current.championRevisions) != 1 {
		return finalError("duplicate terminal result differs from champion revision")
	}
	revision := current.championRevisions[0].Revision()
	if revision.ID() != command.ChampionRevisionID || !revision.CreatedAt().Equal(command.RecordedAt) {
		return finalError("duplicate terminal result differs from champion revision")
	}
	return nil
}

func activateFinalGame(series *domain.ArenaSeries, gameID uuid.UUID) {
	for slotIndex := range series.Slots {
		for gameIndex := range series.Slots[slotIndex].Attempts {
			game := &series.Slots[slotIndex].Attempts[gameIndex]
			if game.ID == gameID && game.State == domain.ArenaGameStatePlanned {
				game.State = domain.ArenaGameStateActive
			}
		}
	}
}

func newChampionRevision(
	series domain.ArenaSeries,
	command FinalProgressionCommand,
) (domain.ArenaProjectionRevision, error) {
	if series.WinnerID == nil {
		return domain.ArenaProjectionRevision{}, finalError("completed final has no winner")
	}
	payload, err := json.Marshal(finalChampionPayload{
		TournamentID: series.TournamentID, SeriesID: series.ID,
		ChampionID: *series.WinnerID,
		Score: finalChampionScorePayload{
			FirstParticipantWins:  series.Score.FirstParticipantWins,
			SecondParticipantWins: series.Score.SecondParticipantWins,
		},
	})
	if err != nil {
		return domain.ArenaProjectionRevision{}, finalError("encode champion: %v", err)
	}
	projection, err := domain.NewArenaProjectionRevision(
		command.ChampionRevisionID,
		series.TournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindChampion, EntityID: series.TournamentID},
		1,
		nil,
		command.RecordedAt,
		payload,
	)
	if err != nil {
		return domain.ArenaProjectionRevision{}, finalError("build champion revision: %v", err)
	}
	return projection, nil
}

func cloneFinal(value Final) Final {
	clone := value
	clone.execution = cloneSeriesExecution(value.execution)
	clone.draft = cloneArenaDraft(value.draft)
	clone.championRevisions = append([]domain.ArenaProjectionRevision(nil), value.championRevisions...)
	clone.championDependencies = append(
		[]domain.ArenaRevisionDependency(nil), value.championDependencies...,
	)
	return clone
}

func finalError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidFinal, fmt.Sprintf(format, arguments...))
}
