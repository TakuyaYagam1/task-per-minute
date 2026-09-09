package result

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateOfficialAuthoritySeries(authority OfficialResultRevisionAuthority) error {
	if err := authority.PersistedSeries.Validate(); err != nil {
		return invalidOfficialResultRevision("invalid persisted Series")
	}
	if err := authority.ProjectedSeries.Validate(); err != nil {
		return invalidOfficialResultRevision("invalid projected Series")
	}
	if authority.PersistedSeries.ID != authority.Scope.SeriesID ||
		authority.PersistedSeries.TournamentID != authority.Scope.TournamentID ||
		authority.ProjectedSeries.ID != authority.Scope.SeriesID ||
		authority.ProjectedSeries.TournamentID != authority.Scope.TournamentID {
		return invalidOfficialResultRevision("authority Series scope mismatch")
	}
	return nil
}

func validateOfficialAuthorityRowsAndGames(authority OfficialResultRevisionAuthority) error {
	if authority.SeriesRevision <= 0 ||
		(authority.Scope.Kind == OfficialResultSubjectGame && authority.AttemptRevision <= 0) ||
		(authority.Scope.Kind == OfficialResultSubjectSeries && authority.AttemptRevision != 0) {
		return invalidOfficialResultRevision("invalid authority row revision")
	}
	if err := validateSeriesGameIDsUnique(
		authority.PersistedSeries,
		invalidOfficialResultRevision,
	); err != nil {
		return err
	}
	if err := validateSeriesGameIDsUnique(
		authority.ProjectedSeries,
		invalidOfficialResultRevision,
	); err != nil {
		return err
	}
	return nil
}

func validateOfficialPersistedHead(authority OfficialResultRevisionAuthority) error {
	head, err := officialResultSubjectHead(authority.Scope, authority.PersistedSeries)
	if err != nil {
		return err
	}
	if authority.CurrentHead == nil {
		if head != nil {
			return officialResultRevisionConflict("persisted result head has no current revision")
		}
		return nil
	}
	if err := authority.CurrentHead.Validate(); err != nil ||
		authority.CurrentHead.Scope != authority.Scope ||
		head == nil || *head != authority.CurrentHead.ID {
		return officialResultRevisionConflict("current result revision does not match persisted head")
	}
	if !officialCurrentHeadMatchesSubject(*authority.CurrentHead, authority.PersistedSeries) {
		return officialResultRevisionConflict("persisted result payload does not match current revision")
	}
	return nil
}

func validateOfficialSeriesTransition(authority OfficialResultRevisionAuthority) error {
	if authority.PersistedSeries.FirstParticipantID != authority.ProjectedSeries.FirstParticipantID ||
		authority.PersistedSeries.SecondParticipantID != authority.ProjectedSeries.SecondParticipantID ||
		authority.PersistedSeries.Format != authority.ProjectedSeries.Format {
		return officialResultRevisionConflict("projected Series identity changed")
	}
	switch authority.Scope.Kind {
	case OfficialResultSubjectGame:
		if authority.ProjectedSeriesReason != "" {
			return invalidOfficialResultRevision("Game projection has a Series reason")
		}
		persistedGame, ok := findSeriesGame(authority.PersistedSeries, authority.Scope.GameID)
		if !ok {
			return officialResultRevisionConflict("persisted Game is missing")
		}
		projectedGame, ok := findSeriesGame(authority.ProjectedSeries, authority.Scope.GameID)
		if !ok {
			return officialResultRevisionConflict("projected Game is missing")
		}
		if persistedGame.ID != projectedGame.ID || persistedGame.SlotID != projectedGame.SlotID ||
			persistedGame.AttemptNo != projectedGame.AttemptNo {
			return officialResultRevisionConflict("projected Game identity changed")
		}
		normalized := cloneRevisionSeries(authority.ProjectedSeries)
		normalizedGame, _ := findSeriesGamePointer(&normalized, authority.Scope.GameID)
		*normalizedGame = cloneGame(persistedGame)
		normalized.State = authority.PersistedSeries.State
		normalized.Score = authority.PersistedSeries.Score
		normalized.WinnerID = cloneUUIDPointer(authority.PersistedSeries.WinnerID)
		normalized.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(
			authority.PersistedSeries.CurrentScoreRevisionID,
		)
		normalized.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(
			authority.PersistedSeries.CurrentResultRevisionID,
		)
		if !officialSeriesEqual(authority.PersistedSeries, normalized) {
			return officialResultRevisionConflict("projection changed unrelated Game state")
		}
	case OfficialResultSubjectSeries:
		if !authority.ProjectedSeriesReason.IsLegalFor(authority.ProjectedSeries.State) {
			return invalidOfficialResultRevision("invalid projected Series reason")
		}
		if !officialSeriesStructureEqual(
			authority.PersistedSeries,
			authority.ProjectedSeries,
		) {
			return officialResultRevisionConflict("projection changed unrelated Series state")
		}
	default:
		return invalidOfficialResultRevision("unknown result subject")
	}
	return nil
}

func officialResultSubjectHead(
	scope OfficialResultScope,
	series domain.Series,
) (*domain.OfficialResultRevisionID, error) {
	switch scope.Kind {
	case OfficialResultSubjectGame:
		game, ok := findSeriesGame(series, scope.GameID)
		if !ok {
			return nil, officialResultRevisionConflict("Game is missing from Series")
		}
		return cloneOfficialResultRevisionIDPointer(game.ResultRevisionID), nil
	case OfficialResultSubjectSeries:
		return cloneOfficialResultRevisionIDPointer(series.CurrentResultRevisionID), nil
	default:
		return nil, invalidOfficialResultRevision("unknown result subject")
	}
}

func officialCurrentHeadMatchesSubject(
	head OfficialResultRevisionHead,
	series domain.Series,
) bool {
	outcome := head.Outcome
	switch head.Scope.Kind {
	case OfficialResultSubjectGame:
		game, ok := findSeriesGame(series, head.Scope.GameID)
		if !ok {
			return false
		}
		return outcome.GameState == game.State && outcome.GameReason == game.ResultReason &&
			uuidPointersEqual(outcome.WinnerID, game.WinnerID)
	case OfficialResultSubjectSeries:
		return outcome.SeriesState == series.State &&
			uuidPointersEqual(outcome.WinnerID, series.WinnerID) &&
			seriesScoreRevisionPointersEqual(outcome.ScoreRevisionID, series.CurrentScoreRevisionID)
	default:
		return false
	}
}

func findSeriesGame(series domain.Series, gameID uuid.UUID) (domain.Game, bool) {
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == gameID {
				return cloneGame(game), true
			}
		}
	}
	return domain.Game{}, false
}

func findSeriesGamePointer(
	series *domain.Series,
	gameID uuid.UUID,
) (*domain.Game, bool) {
	for slotIndex := range series.Slots {
		for gameIndex := range series.Slots[slotIndex].Attempts {
			game := &series.Slots[slotIndex].Attempts[gameIndex]
			if game.ID == gameID {
				return game, true
			}
		}
	}
	return nil, false
}

func officialWinnerIsParticipant(winnerID uuid.UUID, series domain.Series) bool {
	return winnerID == series.FirstParticipantID || winnerID == series.SecondParticipantID
}

func officialSeriesEqual(first, second domain.Series) bool {
	if first.ID != second.ID || first.TournamentID != second.TournamentID ||
		first.FirstParticipantID != second.FirstParticipantID ||
		first.SecondParticipantID != second.SecondParticipantID || first.Format != second.Format ||
		first.State != second.State || first.Score != second.Score ||
		!uuidPointersEqual(first.WinnerID, second.WinnerID) ||
		!seriesScoreRevisionPointersEqual(first.CurrentScoreRevisionID, second.CurrentScoreRevisionID) ||
		!officialResultRevisionPointersEqual(first.CurrentResultRevisionID, second.CurrentResultRevisionID) ||
		len(first.Slots) != len(second.Slots) {
		return false
	}
	for index := range first.Slots {
		if !officialGameSlotEqual(first.Slots[index], second.Slots[index]) {
			return false
		}
	}
	return true
}

func officialSeriesStructureEqual(first, second domain.Series) bool {
	if !officialSeriesStructureHeaderEqual(first, second) {
		return false
	}
	for slotIndex := range first.Slots {
		if !officialGameSlotStructureEqual(first.Slots[slotIndex], second.Slots[slotIndex]) {
			return false
		}
	}
	return true
}

func officialSeriesStructureHeaderEqual(first, second domain.Series) bool {
	if first.ID != second.ID || first.TournamentID != second.TournamentID ||
		first.FirstParticipantID != second.FirstParticipantID ||
		first.SecondParticipantID != second.SecondParticipantID || first.Format != second.Format ||
		len(first.Slots) != len(second.Slots) {
		return false
	}
	return true
}

func officialGameSlotStructureEqual(first, second domain.GameSlot) bool {
	if first.ID != second.ID || first.SeriesID != second.SeriesID ||
		first.Position != second.Position || first.Category != second.Category ||
		first.ScoreBefore != second.ScoreBefore || len(first.Attempts) != len(second.Attempts) {
		return false
	}
	for gameIndex := range first.Attempts {
		if !officialGameIdentityEqual(first.Attempts[gameIndex], second.Attempts[gameIndex]) {
			return false
		}
	}
	return true
}

func officialGameIdentityEqual(first, second domain.Game) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID && first.AttemptNo == second.AttemptNo
}

func officialGameSlotEqual(first, second domain.GameSlot) bool {
	if first.ID != second.ID || first.SeriesID != second.SeriesID || first.Position != second.Position ||
		first.Category != second.Category || first.ScoreBefore != second.ScoreBefore ||
		len(first.Attempts) != len(second.Attempts) {
		return false
	}
	for index := range first.Attempts {
		if !officialGameEqual(first.Attempts[index], second.Attempts[index]) {
			return false
		}
	}
	return true
}

func officialGameEqual(first, second domain.Game) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID &&
		first.AttemptNo == second.AttemptNo && first.State == second.State &&
		first.ResultReason == second.ResultReason && uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		officialResultRevisionPointersEqual(first.ResultRevisionID, second.ResultRevisionID)
}

func officialResultCurrentHeadID(
	head *OfficialResultRevisionHead,
) *domain.OfficialResultRevisionID {
	if head == nil {
		return nil
	}
	id := head.ID
	return &id
}

func officialResultOutcomesEqual(first, second OfficialResultOutcome) bool {
	return first.GameState == second.GameState && first.GameReason == second.GameReason &&
		first.SeriesState == second.SeriesState && first.SeriesReason == second.SeriesReason &&
		uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		seriesScoreRevisionPointersEqual(first.ScoreRevisionID, second.ScoreRevisionID)
}

func validateSeriesGameIDsUnique(
	series domain.Series,
	invalid func(string) error,
) error {
	gameIDs := make(map[uuid.UUID]struct{})
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if _, exists := gameIDs[game.ID]; exists {
				return invalid("duplicate Game identity")
			}
			gameIDs[game.ID] = struct{}{}
		}
	}
	return nil
}

func validateOfficialResultIdentityOwnership(
	series domain.Series,
	scope OfficialResultScope,
	proposed domain.OfficialResultRevisionID,
	allowProjectedTarget bool,
) error {
	seen := make(map[domain.OfficialResultRevisionID]struct{})
	add := func(
		id *domain.OfficialResultRevisionID,
		isTarget bool,
	) error {
		if id == nil {
			return nil
		}
		if _, exists := seen[*id]; exists {
			return invalidOfficialResultRevision("official result identity has multiple local owners")
		}
		seen[*id] = struct{}{}
		if *id == proposed && (!allowProjectedTarget || !isTarget) {
			return invalidOfficialResultRevision("proposed official result identity is already owned")
		}
		return nil
	}
	if err := add(
		series.CurrentResultRevisionID,
		scope.Kind == OfficialResultSubjectSeries,
	); err != nil {
		return err
	}
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if err := add(
				game.ResultRevisionID,
				scope.Kind == OfficialResultSubjectGame && game.ID == scope.GameID,
			); err != nil {
				return err
			}
		}
	}
	return nil
}
