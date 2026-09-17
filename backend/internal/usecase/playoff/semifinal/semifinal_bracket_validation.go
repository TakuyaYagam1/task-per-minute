package semifinal

import (
	"bytes"
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateSemifinalPredecessorReceipt(
	authority semifinalBracketAuthority,
	participants []Top4Participant,
) error {
	if authority.Previous == nil {
		return nil
	}
	previous := authority.Previous
	revision := previous.Projection.Revision()
	if previous.Projection.Validate() != nil || previous.LockedAt.IsZero() ||
		previous.Top4RevisionID != authority.Top4.Projection().Revision().ID() ||
		revision.TournamentID() != authority.TournamentID ||
		revision.Artifact() != (domain.ArtifactRef{
			Kind: domain.ArtifactKindBracket, EntityID: authority.TournamentID,
		}) || revision.RevisionNo()+1 != authority.RevisionNo || authority.CreatedAt.Before(revision.CreatedAt()) {
		return semifinalBracketError("invalid bounded bracket predecessor receipt")
	}
	expected := []SemifinalMatch{
		newSemifinalMatch(1, authority.SeriesIDs[0], authority.TournamentID, participants[0], participants[1]),
		newSemifinalMatch(2, authority.SeriesIDs[1], authority.TournamentID, participants[2], participants[3]),
	}
	if !reflect.DeepEqual(previous.Semifinals, expected) {
		return semifinalBracketError("bracket successor cannot replace locked semifinal topology")
	}
	if err := validateSemifinalReservedReceipt(previous, revision.ID().UUID()); err != nil {
		return err
	}
	return validateSemifinalMatches(previous.Semifinals)
}

func validateSemifinalReservedReceipt(
	previous *semifinalBracketPredecessorReceipt,
	revisionID uuid.UUID,
) error {
	if len(previous.Reserved) == 0 || len(previous.Reserved) > maxPlayoffReservedIdentities {
		return semifinalBracketError("bracket predecessor identity receipt exceeds DAG bounds")
	}
	foundRevision, foundFirst, foundSecond := false, false, false
	for index, id := range previous.Reserved {
		if id == uuid.Nil || (index > 0 && bytes.Compare(previous.Reserved[index-1][:], id[:]) >= 0) {
			return semifinalBracketError("non-canonical bracket predecessor identity receipt")
		}
		foundRevision = foundRevision || id == revisionID
		foundFirst = foundFirst || id == previous.Semifinals[0].Series.ID
		foundSecond = foundSecond || id == previous.Semifinals[1].Series.ID
	}
	if !foundRevision || !foundFirst || !foundSecond {
		return semifinalBracketError("incomplete bracket predecessor identity receipt")
	}
	return nil
}

func validateSemifinalParticipants(participants []Top4Participant) error {
	if len(participants) != finalSwissTop4Cutoff {
		return semifinalBracketError("Top 4 source does not contain exactly four participants")
	}
	for index, participant := range participants {
		if participant.Seed != index+1 || participant.ParticipantID == uuid.Nil {
			return semifinalBracketError("Top 4 source is not exactly ordered")
		}
	}
	return nil
}

func validateSemifinalMatches(matches []SemifinalMatch) error {
	for _, match := range matches {
		series := match.Series
		if series.Validate() != nil || series.State != domain.SeriesStateLocked ||
			series.Format != domain.SeriesFormatBO1 || len(series.Slots) != 0 ||
			series.WinnerID != nil || series.CurrentScoreRevisionID != nil ||
			series.CurrentResultRevisionID != nil {
			return semifinalBracketError("semifinal is not one locked unstarted BO1 Series")
		}
	}
	return nil
}
