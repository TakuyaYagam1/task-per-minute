package terminal

import (
	"errors"
	"strconv"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidTerminalStage = errors.New("invalid playoff terminal stage")

var terminalStageNamespace = uuid.MustParse("cc79a52a-99ca-4e32-8fef-0f04b0d0752a")

// FinalStageIdentity reserves every final identity from the immutable playoff
// stage command. It never allocates randomness at a retry boundary.
func FinalStageIdentity(stageCommandID uuid.UUID) (FinalStageIDs, error) {
	if stageCommandID == uuid.Nil {
		return FinalStageIDs{}, ErrInvalidTerminalStage
	}
	ids := FinalStageIDs{
		FinalSeriesID:          terminalStageID(stageCommandID, "final-series"),
		CategoryRevisionID:     terminalStageID(stageCommandID, "final-category-revision"),
		DraftID:                terminalStageID(stageCommandID, "final-draft"),
		DraftInitialRevisionID: terminalStageID(stageCommandID, "final-draft-initial-revision"),
		DraftOrderDecisionID:   terminalStageID(stageCommandID, "final-draft-order-decision"),
		DraftServiceEpochID:    terminalStageID(stageCommandID, "final-draft-service-epoch"),
		DraftAssignmentPlanID:  terminalStageID(stageCommandID, "final-draft-assignment-plan"),
		DraftAssignmentRevisionID: terminalStageID(
			stageCommandID,
			"final-draft-assignment-plan-revision",
		),
		InitialScoreRevisionID: domain.SeriesScoreRevisionID(terminalStageID(stageCommandID, "final-initial-score")),
		FirstSlotID:            terminalStageID(stageCommandID, "final-game-1-slot"),
		FirstGameID:            terminalStageID(stageCommandID, "final-game-1"),
		FirstWaveID:            terminalStageID(stageCommandID, "final-game-1-wave"),
		FirstWaveRevisionID:    domain.WaveRevisionID(terminalStageID(stageCommandID, "final-game-1-wave-revision")),
		SecondSlotID:           terminalStageID(stageCommandID, "final-game-2-slot"),
		SecondGameID:           terminalStageID(stageCommandID, "final-game-2"),
		SecondWaveID:           terminalStageID(stageCommandID, "final-game-2-wave"),
		SecondWaveRevisionID:   domain.WaveRevisionID(terminalStageID(stageCommandID, "final-game-2-wave-revision")),
		ThirdSlotID:            terminalStageID(stageCommandID, "final-game-3-slot"),
		ThirdGameID:            terminalStageID(stageCommandID, "final-game-3"),
		ThirdWaveID:            terminalStageID(stageCommandID, "final-game-3-wave"),
		ThirdWaveRevisionID:    domain.WaveRevisionID(terminalStageID(stageCommandID, "final-game-3-wave-revision")),
	}
	if !ids.Valid() {
		return FinalStageIDs{}, ErrInvalidTerminalStage
	}
	return ids, nil
}

// FinalPublicationIdentity reserves the terminal projection graph from its
// authoritative Game result revision. It is deliberately separate from the
// stage command identity: a corrected result is a new immutable source and
// must never reuse the previous champion publication graph.
func FinalPublicationIdentity(
	gameResultRevisionID domain.OfficialResultRevisionID,
) (FinalPublicationIDs, error) {
	resultID := gameResultRevisionID.UUID()
	if resultID == uuid.Nil {
		return FinalPublicationIDs{}, ErrInvalidTerminalStage
	}
	ids := FinalPublicationIDs{
		ChampionRevisionID:    domain.DerivedRevisionID(terminalStageID(resultID, "final-champion-revision")),
		ProjectionRevisionID:  terminalStageID(resultID, "final-projection-revision"),
		CutoffID:              terminalStageID(resultID, "final-projection-cutoff"),
		StandingsArtifactID:   terminalStageID(resultID, "final-standings-artifact"),
		BracketArtifactID:     terminalStageID(resultID, "final-bracket-artifact"),
		TopFourArtifactID:     terminalStageID(resultID, "final-top-four-artifact"),
		ChampionArtifactID:    terminalStageID(resultID, "final-champion-artifact"),
		StandingsDependencyID: terminalStageID(resultID, "final-standings-dependency"),
		BracketDependencyID:   terminalStageID(resultID, "final-bracket-dependency"),
		TopFourDependencyID:   terminalStageID(resultID, "final-top-four-dependency"),
		ChampionBracketDependencyID: terminalStageID(
			resultID,
			"final-champion-bracket-dependency",
		),
		ChampionResultDependencyID: terminalStageID(
			resultID,
			"final-champion-result-dependency",
		),
	}
	if !ids.Valid() {
		return FinalPublicationIDs{}, ErrInvalidTerminalStage
	}
	return ids, nil
}

func (ids FinalPublicationIDs) Valid() bool {
	values := []uuid.UUID{
		ids.ChampionRevisionID.UUID(), ids.ProjectionRevisionID, ids.CutoffID,
		ids.StandingsArtifactID, ids.BracketArtifactID, ids.TopFourArtifactID,
		ids.ChampionArtifactID, ids.StandingsDependencyID, ids.BracketDependencyID,
		ids.TopFourDependencyID, ids.ChampionBracketDependencyID,
		ids.ChampionResultDependencyID,
	}
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func (ids FinalStageIDs) Valid() bool {
	values := []uuid.UUID{
		ids.FinalSeriesID, ids.CategoryRevisionID, ids.DraftID, ids.DraftInitialRevisionID,
		ids.DraftOrderDecisionID, ids.DraftServiceEpochID, ids.InitialScoreRevisionID.UUID(),
		ids.DraftAssignmentPlanID, ids.DraftAssignmentRevisionID,
		ids.FirstSlotID, ids.FirstGameID, ids.FirstWaveID, ids.FirstWaveRevisionID.UUID(),
		ids.SecondSlotID, ids.SecondGameID, ids.SecondWaveID, ids.SecondWaveRevisionID.UUID(),
		ids.ThirdSlotID, ids.ThirdGameID, ids.ThirdWaveID, ids.ThirdWaveRevisionID.UUID(),
	}
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func (ids FinalStageIDs) DraftAssignmentBranchID(key string) uuid.UUID {
	return terminalStageID(ids.DraftAssignmentPlanID, "branch:"+key)
}

func (ids FinalStageIDs) DraftAssignmentChildBranchID(key string, position int) uuid.UUID {
	return terminalStageID(ids.DraftAssignmentPlanID, "branch-child:"+key+":"+strconv.Itoa(position))
}

func (ids FinalStageIDs) DraftAssignmentDecisionID(key string, position int) uuid.UUID {
	return terminalStageID(ids.DraftAssignmentPlanID, "decision:"+key+":"+strconv.Itoa(position))
}

func (ids FinalStageIDs) DraftAssignmentEdgeID(key string, position, reserve int) uuid.UUID {
	return terminalStageID(
		ids.DraftAssignmentPlanID,
		"edge:"+key+":"+strconv.Itoa(position)+":"+strconv.Itoa(reserve),
	)
}

func (ids FinalStageIDs) DraftAssignmentReservationID(key string, position, reserve int) uuid.UUID {
	return terminalStageID(
		ids.DraftAssignmentPlanID,
		"reservation:"+key+":"+strconv.Itoa(position)+":"+strconv.Itoa(reserve),
	)
}

func (ids FinalStageIDs) DraftAssignmentSnapshotID(key string, position, reserve int) uuid.UUID {
	return terminalStageID(
		ids.DraftAssignmentPlanID,
		"snapshot:"+key+":"+strconv.Itoa(position)+":"+strconv.Itoa(reserve),
	)
}

func (ids FinalStageIDs) GameAssignmentID(position int) uuid.UUID {
	return terminalStageID(ids.DraftAssignmentPlanID, "game-assignment:"+strconv.Itoa(position))
}

func terminalStageID(stageCommandID uuid.UUID, label string) uuid.UUID {
	material := make([]byte, 0, len(label)+len(stageCommandID))
	material = append(material, label...)
	material = append(material, stageCommandID[:]...)
	return uuid.NewSHA1(terminalStageNamespace, material)
}
