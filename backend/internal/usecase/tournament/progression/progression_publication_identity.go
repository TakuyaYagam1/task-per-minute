package progression

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// PlayoffPublicationIDs reserves every database identity created while a
// StartPlayoffs command materializes its projection and semifinal genesis.
// They are deliberately distinct from the planner's logical Top4 and bracket
// revisions, which describe canonical payload lineage rather than rows.
type PlayoffPublicationIDs struct {
	ProjectionRevisionID uuid.UUID
	CutoffID             uuid.UUID
	StandingsArtifactID  uuid.UUID
	Top4ArtifactID       uuid.UUID
	BracketArtifactID    uuid.UUID

	StandingsDependencyID uuid.UUID
	Top4DependencyID      uuid.UUID
	BracketDependencyID   uuid.UUID

	StageNodeAuthorityID           uuid.UUID
	Top4NodeID                     uuid.UUID
	BracketNodeID                  uuid.UUID
	FirstSemifinalScoreNodeID      uuid.UUID
	SecondSemifinalScoreNodeID     uuid.UUID
	FirstSemifinalScoreRevisionID  domain.SeriesScoreRevisionID
	SecondSemifinalScoreRevisionID domain.SeriesScoreRevisionID
}

// PlayoffPublicationIdentity derives a stable, command-scoped identity set.
// Retried command execution must use exactly these values, never UUID
// allocation in a persistence adapter.
func PlayoffPublicationIdentity(commandID uuid.UUID) (PlayoffPublicationIDs, error) {
	if commandID == uuid.Nil {
		return PlayoffPublicationIDs{}, domain.ErrValidation
	}
	ids := PlayoffPublicationIDs{
		ProjectionRevisionID: progressionID(commandID, "playoff-projection-revision"),
		CutoffID:             progressionID(commandID, "playoff-projection-cutoff"),
		StandingsArtifactID:  progressionID(commandID, "playoff-standings-artifact"),
		Top4ArtifactID:       progressionID(commandID, "playoff-top-four-artifact"),
		BracketArtifactID:    progressionID(commandID, "playoff-bracket-artifact"),

		StandingsDependencyID: progressionID(commandID, "playoff-standings-dependency"),
		Top4DependencyID:      progressionID(commandID, "playoff-top-four-dependency"),
		BracketDependencyID:   progressionID(commandID, "playoff-bracket-dependency"),

		StageNodeAuthorityID:       progressionID(commandID, "playoff-stage-node-authority"),
		Top4NodeID:                 progressionID(commandID, "playoff-top-four-node"),
		BracketNodeID:              progressionID(commandID, "playoff-bracket-node"),
		FirstSemifinalScoreNodeID:  progressionID(commandID, "playoff-semifinal-1-score-node"),
		SecondSemifinalScoreNodeID: progressionID(commandID, "playoff-semifinal-2-score-node"),
		FirstSemifinalScoreRevisionID: domain.SeriesScoreRevisionID(
			progressionID(commandID, "playoff-semifinal-1-initial-score-revision"),
		),
		SecondSemifinalScoreRevisionID: domain.SeriesScoreRevisionID(
			progressionID(commandID, "playoff-semifinal-2-initial-score-revision"),
		),
	}
	if !ids.Valid() {
		return PlayoffPublicationIDs{}, domain.ErrValidation
	}
	return ids, nil
}

func (ids PlayoffPublicationIDs) Valid() bool {
	values := []uuid.UUID{
		ids.ProjectionRevisionID,
		ids.CutoffID,
		ids.StandingsArtifactID,
		ids.Top4ArtifactID,
		ids.BracketArtifactID,
		ids.StandingsDependencyID,
		ids.Top4DependencyID,
		ids.BracketDependencyID,
		ids.StageNodeAuthorityID,
		ids.Top4NodeID,
		ids.BracketNodeID,
		ids.FirstSemifinalScoreNodeID,
		ids.SecondSemifinalScoreNodeID,
		ids.FirstSemifinalScoreRevisionID.UUID(),
		ids.SecondSemifinalScoreRevisionID.UUID(),
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

// GoldenDependencyID binds each additional Top4 dependency to this command
// and one immutable Golden position commit.
func (ids PlayoffPublicationIDs) GoldenDependencyID(commandID, commitID uuid.UUID) (uuid.UUID, error) {
	expected, err := PlayoffPublicationIdentity(commandID)
	if err != nil || ids != expected || commitID == uuid.Nil {
		return uuid.Nil, domain.ErrValidation
	}
	return progressionID(commandID, "playoff-golden-dependency-"+commitID.String()), nil
}
