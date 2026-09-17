package semifinal

import (
	"math"
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func canonicalSemifinalBracketAuthority(
	command SemifinalBracketCommand,
) (semifinalBracketAuthority, error) {
	if command.TournamentID == uuid.Nil || command.RevisionID.IsZero() || command.RevisionNo < 1 ||
		command.RevisionNo == math.MaxInt ||
		!validSemifinalTime(command.CreatedAt) || command.Top4.Validate() != nil {
		return semifinalBracketAuthority{}, semifinalBracketError("invalid bracket identity, clock, or Top 4 source")
	}
	top4Revision := command.Top4.Projection().Revision()
	if top4Revision.TournamentID() != command.TournamentID ||
		top4Revision.Artifact() != (domain.ArtifactRef{
			Kind: domain.ArtifactKindTopFour, EntityID: command.TournamentID,
		}) || command.CreatedAt.Before(top4Revision.CreatedAt()) {
		return semifinalBracketAuthority{}, semifinalBracketError("Top 4 source is stale or belongs to another tournament")
	}
	previous, err := validateSemifinalBracketPredecessor(command)
	if err != nil {
		return semifinalBracketAuthority{}, err
	}
	authority := semifinalBracketAuthority{
		TournamentID: command.TournamentID, RevisionID: command.RevisionID,
		RevisionNo: command.RevisionNo, Previous: previous, Top4: command.Top4.Snapshot(),
		SeriesIDs: command.SeriesIDs, CreatedAt: command.CreatedAt,
	}
	if err := validateSemifinalBracketIdentityRoles(authority); err != nil {
		return semifinalBracketAuthority{}, err
	}
	return authority, nil
}

func validateSemifinalBracketPredecessor(
	command SemifinalBracketCommand,
) (*semifinalBracketPredecessorReceipt, error) {
	if command.RevisionNo == 1 {
		if command.Previous != nil {
			return nil, semifinalBracketError("initial bracket revision has a predecessor")
		}
		return nil, nil
	}
	if command.Previous == nil || command.Previous.Validate() != nil {
		return nil, semifinalBracketError("bracket revision lacks a valid predecessor")
	}
	previous := command.Previous.Projection()
	revision := previous.Revision()
	if revision.TournamentID() != command.TournamentID ||
		revision.Artifact() != (domain.ArtifactRef{
			Kind: domain.ArtifactKindBracket, EntityID: command.TournamentID,
		}) || revision.RevisionNo()+1 != command.RevisionNo || command.CreatedAt.Before(revision.CreatedAt()) {
		return nil, semifinalBracketError("bracket predecessor is not the exact current head")
	}
	previousTop4 := command.Previous.state.Authority.Top4.Projection().Revision().ID()
	currentTop4 := command.Top4.Projection().Revision().ID()
	participants := command.Top4.Participants()
	if previousTop4 != currentTop4 || validateSemifinalParticipants(participants) != nil {
		return nil, semifinalBracketError("bracket successor must retain its exact Top 4 source")
	}
	expected := []SemifinalMatch{
		newSemifinalMatch(1, command.SeriesIDs[0], command.TournamentID, participants[0], participants[1]),
		newSemifinalMatch(2, command.SeriesIDs[1], command.TournamentID, participants[2], participants[3]),
	}
	previousMatches := command.Previous.Semifinals()
	if command.Previous.LockedAt().IsZero() || !reflect.DeepEqual(previousMatches, expected) {
		return nil, semifinalBracketError("bracket successor cannot replace locked semifinal topology")
	}
	return newSemifinalPredecessorReceipt(command, previous, previousTop4, previousMatches)
}

func newSemifinalPredecessorReceipt(
	command SemifinalBracketCommand,
	previous domain.ProjectionRevision,
	previousTop4 domain.DerivedRevisionID,
	previousMatches []SemifinalMatch,
) (*semifinalBracketPredecessorReceipt, error) {
	reserved := make(map[uuid.UUID]struct{})
	if command.Previous.state.Authority.Previous != nil &&
		!mergePlayoffReservedIdentities(reserved, command.Previous.state.Authority.Previous.Reserved) {
		return nil, semifinalBracketError("retained bracket identity lineage exceeds DAG bounds")
	}
	if !reservePlayoffIdentity(reserved, previous.Revision().ID().UUID()) {
		return nil, semifinalBracketError("retained bracket identity lineage exceeds DAG bounds")
	}
	for _, match := range previousMatches {
		if !reservePlayoffIdentity(reserved, match.Series.ID) {
			return nil, semifinalBracketError("retained bracket identity lineage exceeds DAG bounds")
		}
	}
	if err := addSemifinalTop4Reserved(reserved, command.Previous.state.Authority.Top4); err != nil {
		return nil, err
	}
	return &semifinalBracketPredecessorReceipt{
		Projection: cloneSemifinalProjection(previous), Top4RevisionID: previousTop4,
		Reserved: canonicalSemifinalReservedIDs(reserved), Semifinals: cloneSemifinalMatches(previousMatches),
		LockedAt: command.Previous.LockedAt(),
	}, nil
}

func buildSemifinalBracket(authority semifinalBracketAuthority) (SemifinalBracket, error) {
	participants := authority.Top4.Participants()
	if err := validateSemifinalParticipants(participants); err != nil {
		return SemifinalBracket{}, err
	}
	if err := validateSemifinalPredecessorReceipt(authority, participants); err != nil {
		return SemifinalBracket{}, err
	}
	lockedAt := authority.CreatedAt
	matches := []SemifinalMatch{
		newSemifinalMatch(1, authority.SeriesIDs[0], authority.TournamentID, participants[0], participants[1]),
		newSemifinalMatch(2, authority.SeriesIDs[1], authority.TournamentID, participants[2], participants[3]),
	}
	if authority.Previous != nil {
		matches = cloneSemifinalMatches(authority.Previous.Semifinals)
		lockedAt = authority.Previous.LockedAt
	}
	if err := validateSemifinalMatches(matches); err != nil {
		return SemifinalBracket{}, err
	}
	payload, err := semifinalBracketPayload(authority, matches, lockedAt)
	if err != nil || len(payload) == 0 || len(payload) > maxSemifinalBracketPayload {
		return SemifinalBracket{}, semifinalBracketError("encode bounded semifinal bracket payload")
	}
	var previousID *domain.DerivedRevisionID
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID()
		previousID = &value
	}
	projection, err := domain.NewProjectionRevision(
		authority.RevisionID, authority.TournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindBracket, EntityID: authority.TournamentID},
		authority.RevisionNo, previousID, authority.CreatedAt, payload,
	)
	if err != nil {
		return SemifinalBracket{}, semifinalBracketError("build bracket revision: %v", err)
	}
	dependencies := []domain.RevisionDependency{{
		SourceRevisionID: authority.Top4.Projection().Revision().ID(), DerivedRevisionID: authority.RevisionID,
	}}
	if authority.Previous != nil {
		dependencies = append(dependencies, domain.RevisionDependency{
			SourceRevisionID:  authority.Previous.Projection.Revision().ID(),
			DerivedRevisionID: authority.RevisionID,
		})
	}
	return SemifinalBracket{state: semifinalBracketState{
		Authority: cloneSemifinalBracketAuthority(authority), Projection: projection,
		Dependencies: dependencies, Semifinals: cloneSemifinalMatches(matches), LockedAt: lockedAt,
	}}, nil
}

func newSemifinalMatch(
	position int,
	seriesID uuid.UUID,
	tournamentID uuid.UUID,
	first Top4Participant,
	second Top4Participant,
) SemifinalMatch {
	return SemifinalMatch{
		Position: position,
		Series: domain.Series{
			ID: seriesID, TournamentID: tournamentID,
			FirstParticipantID: first.ParticipantID, SecondParticipantID: second.ParticipantID,
			Format: domain.SeriesFormatBO1, State: domain.SeriesStateLocked,
			Slots: []domain.GameSlot{},
		},
		WinnerPath: SemifinalWinnerToFinal, LoserPath: SemifinalLoserEliminated,
	}
}
