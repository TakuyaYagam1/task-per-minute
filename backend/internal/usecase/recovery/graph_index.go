package recovery

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type recoveryGame struct {
	waveID   uuid.UUID
	seriesID uuid.UUID
	slotID   uuid.UUID
	game     domain.Game
}

type recoveryIndex struct {
	participants map[uuid.UUID]domain.Participant
	waves        map[uuid.UUID]domain.Wave
	series       map[uuid.UUID]RecoverySeries
	games        map[uuid.UUID]recoveryGame
	results      map[domain.ArtifactRef]RecoveryResultEvidence
	scores       map[uuid.UUID]RecoveryScoreEvidence
	pauses       map[[2]string]RecoveryPauseEvidence
	deadlines    []RecoveryDeadlineEvidence
}

//nolint:gocyclo // A single indexing pass rejects every cross-child identity before recovery side effects.
func indexRecoveryChildren(graph RecoveryGraph) (recoveryIndex, bool) {
	indexed := recoveryIndex{
		participants: make(map[uuid.UUID]domain.Participant, len(graph.Roster.Participants)),
		waves:        make(map[uuid.UUID]domain.Wave, len(graph.Waves)),
		series:       make(map[uuid.UUID]RecoverySeries, len(graph.Series)),
		games:        make(map[uuid.UUID]recoveryGame),
		results:      make(map[domain.ArtifactRef]RecoveryResultEvidence, len(graph.Results)),
		scores:       make(map[uuid.UUID]RecoveryScoreEvidence, len(graph.Scores)),
		pauses:       make(map[[2]string]RecoveryPauseEvidence, len(graph.Pauses)),
	}
	if graph.TournamentID == uuid.Nil || graph.Roster.TournamentID != graph.TournamentID ||
		graph.Roster.Validate() != nil {
		return recoveryIndex{}, false
	}
	for _, participant := range graph.Roster.Participants {
		indexed.participants[participant.ID] = participant
	}
	for _, wave := range graph.Waves {
		if wave.ID == uuid.Nil || wave.TournamentID != graph.TournamentID {
			return recoveryIndex{}, false
		}
		if _, exists := indexed.waves[wave.ID]; exists {
			return recoveryIndex{}, false
		}
		for _, member := range wave.Members {
			participant, exists := indexed.participants[member.ParticipantID]
			if !exists || participant.Attendance != domain.AttendanceStateCheckedIn {
				return recoveryIndex{}, false
			}
		}
		indexed.waves[wave.ID] = wave
	}
	for _, child := range graph.Series {
		series := child.Series
		wave, exists := indexed.waves[child.WaveID]
		if !exists || series.ID == uuid.Nil || series.TournamentID != graph.TournamentID {
			return recoveryIndex{}, false
		}
		if _, exists := indexed.series[series.ID]; exists ||
			!recoveryWaveHasParticipant(wave, series.FirstParticipantID) ||
			!recoveryWaveHasParticipant(wave, series.SecondParticipantID) {
			return recoveryIndex{}, false
		}
		indexed.series[series.ID] = child
		for _, slot := range series.Slots {
			for _, game := range slot.Attempts {
				if _, exists := indexed.games[game.ID]; exists {
					return recoveryIndex{}, false
				}
				indexed.games[game.ID] = recoveryGame{
					waveID: child.WaveID, seriesID: series.ID, slotID: slot.ID, game: game,
				}
			}
		}
	}
	if !indexed.indexAssignments(graph.Assignments) || !indexed.indexEvidence(graph) {
		return recoveryIndex{}, false
	}
	return indexed, true
}

func (i recoveryIndex) indexAssignments(assignments []RecoveryAssignment) bool {
	seenAssignments := make(map[uuid.UUID]struct{}, len(assignments))
	assignedGames := make(map[uuid.UUID]struct{}, len(assignments))
	for _, link := range assignments {
		game, exists := i.games[link.GameID]
		if !exists || game.waveID != link.WaveID || game.seriesID != link.SeriesID ||
			game.slotID != link.SlotID || link.Assignment.Validate() != nil ||
			link.Assignment.AttemptID() != link.GameID {
			return false
		}
		if _, exists := seenAssignments[link.Assignment.ID()]; exists {
			return false
		}
		if _, exists := assignedGames[link.GameID]; exists {
			return false
		}
		seenAssignments[link.Assignment.ID()] = struct{}{}
		assignedGames[link.GameID] = struct{}{}
	}
	for gameID, child := range i.games {
		if child.game.State != domain.GameStateActive &&
			child.game.State != domain.GameStatePaused {
			continue
		}
		if _, exists := assignedGames[gameID]; !exists {
			return false
		}
	}
	return true
}

func (i *recoveryIndex) indexEvidence(graph RecoveryGraph) bool {
	for _, evidence := range graph.Results {
		if evidence.Artifact.Validate() != nil ||
			(evidence.Artifact.Kind != domain.ArtifactKindGameResult &&
				evidence.Artifact.Kind != domain.ArtifactKindSeriesResult) {
			return false
		}
		if _, exists := i.results[evidence.Artifact]; exists {
			return false
		}
		i.results[evidence.Artifact] = evidence
	}
	for _, evidence := range graph.Scores {
		if evidence.SeriesID == uuid.Nil {
			return false
		}
		if _, exists := i.scores[evidence.SeriesID]; exists {
			return false
		}
		i.scores[evidence.SeriesID] = evidence
	}
	for _, evidence := range graph.Pauses {
		if !validRecoveryPauseKind(evidence.Kind) || evidence.EntityID == uuid.Nil {
			return false
		}
		key := recoveryPauseKey(evidence.Kind, evidence.EntityID)
		if _, exists := i.pauses[key]; exists {
			return false
		}
		i.pauses[key] = evidence
	}
	return true
}

func recoveryWaveHasParticipant(wave domain.Wave, participantID uuid.UUID) bool {
	for _, member := range wave.Members {
		if member.ParticipantID == participantID {
			return true
		}
	}
	return false
}

func recoveryTerminalCount(indexed recoveryIndex) int {
	count := 0
	for _, child := range indexed.games {
		if child.game.State.IsTerminal() {
			count++
		}
	}
	for _, child := range indexed.series {
		if child.Series.State.IsTerminal() {
			count++
		}
	}
	return count
}

func recoveryPauseKey(kind RecoveryPauseKind, entityID uuid.UUID) [2]string {
	return [2]string{string(kind), entityID.String()}
}

func sameRecoveryWinner(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
