package resultprojection

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func seriesScoreIDAsUUID(value *domain.SeriesScoreRevisionID) *uuid.UUID {
	if value == nil {
		return nil
	}
	id := value.UUID()
	return &id
}

func officialResultIDAsUUID(value *domain.OfficialResultRevisionID) *uuid.UUID {
	if value == nil {
		return nil
	}
	id := value.UUID()
	return &id
}

//nolint:gocyclo // Identity ownership is intentionally checked in one registry pass.
func validateRecordedNoGameIdentities(recorded RecordedNoGameResult) error {
	sqlOrigin := recorded.restoredOrigin == noGameSQLOrigin
	if recorded.restoredOrigin != 0 && (!sqlOrigin || !validSQLNoGameIdentity(recorded)) {
		return invalidOfficialResultProjection("invalid SQL no-show source lineage")
	}
	seen := make(map[uuid.UUID]string, 12+len(recorded.GameResults)*2)
	add := func(id uuid.UUID, owner string) error {
		if id == uuid.Nil {
			return invalidOfficialResultProjection("missing %s identity", owner)
		}
		if previous, exists := seen[id]; exists {
			return invalidOfficialResultProjection("identity aliases %s and %s", previous, owner)
		}
		seen[id] = owner
		return nil
	}
	identities := []struct {
		id    uuid.UUID
		owner string
	}{
		{recorded.Scope.TournamentID, "tournament"},
		{recorded.Scope.WaveID, "Wave"},
		{recorded.Scope.WindowID, "window"},
		{recorded.Scope.SeriesID, "Series"},
		{recorded.CommandID, "command"},
		{recorded.FirstParticipantID, "first participant"},
		{recorded.SecondParticipantID, "second participant"},
		{recorded.Score.ID.UUID(), "score revision"},
		{recorded.Series.ID.UUID(), "Series result revision"},
	}
	if !sqlOrigin {
		identities = append(identities, struct {
			id    uuid.UUID
			owner string
		}{recorded.ScoreProjection.Revision().ID().UUID(), "score projection"},
			struct {
				id    uuid.UUID
				owner string
			}{recorded.ResultProjection.Revision().ID().UUID(), "Series result projection"})
	}
	for _, identity := range identities {
		if err := add(identity.id, identity.owner); err != nil {
			return err
		}
	}
	for index, game := range recorded.GameResults {
		if err := add(game.ID.UUID(), fmt.Sprintf("Game result revision %d", index)); err != nil {
			return err
		}
		if err := add(game.GameID, fmt.Sprintf("Game %d", index)); err != nil {
			return err
		}
		if !sqlOrigin {
			if err := add(recorded.GameProjections[index].Revision().ID().UUID(), fmt.Sprintf("Game projection %d", index)); err != nil {
				return err
			}
		}
		if err := add(recorded.Topology[index].SlotID, fmt.Sprintf("Game slot %d", index)); err != nil {
			return err
		}
	}
	predecessors := []struct {
		id      *uuid.UUID
		current uuid.UUID
		owner   string
	}{
		{seriesScoreIDAsUUID(recorded.Score.PreviousRevisionID), recorded.Score.ID.UUID(), "score predecessor"},
		{officialResultIDAsUUID(recorded.Series.PreviousRevisionID), recorded.Series.ID.UUID(), "Series result predecessor"},
	}
	for index := range recorded.GameResults {
		predecessors = append(predecessors, struct {
			id      *uuid.UUID
			current uuid.UUID
			owner   string
		}{officialResultIDAsUUID(recorded.GameResults[index].PreviousRevisionID), recorded.GameResults[index].ID.UUID(), fmt.Sprintf("Game predecessor %d", index)})
	}
	for _, predecessor := range predecessors {
		if predecessor.id == nil {
			continue
		}
		if *predecessor.id == uuid.Nil || *predecessor.id == predecessor.current {
			return invalidOfficialResultProjection("invalid %s", predecessor.owner)
		}
		if err := add(*predecessor.id, predecessor.owner); err != nil {
			return err
		}
	}
	return nil
}
