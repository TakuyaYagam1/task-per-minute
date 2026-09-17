package plan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func validateGoldenGroupCommand(
	command GroupRevisionCommand,
	source StandingsProjection,
	predecessor *GroupRevision,
	used []RevisionIdentity,
) error {
	if err := validateGoldenGroupCommandIdentity(command, source); err != nil {
		return err
	}
	if command.PositionFrom < 1 || command.PositionTo < command.PositionFrom || command.PositionTo > len(source.Standings) {
		return goldenGroupError("invalid command interval")
	}
	if err := validateGoldenUsedRevisionIdentities(command, source, used); err != nil {
		return err
	}
	return validateGoldenGroupPredecessor(command, source, predecessor)
}

func validateGoldenGroupCommandIdentity(command GroupRevisionCommand, source StandingsProjection) error {
	if command.TournamentID == uuid.Nil || command.GroupID == uuid.Nil || command.RevisionID.IsZero() ||
		command.ExpectedSourceRevisionID.IsZero() || command.ExpectedSourcePayloadDigest == [sha256.Size]byte{} ||
		command.TournamentID != source.TournamentID || command.GroupID == command.TournamentID ||
		command.GroupID == source.ProjectionID || command.GroupID == source.RevisionID.UUID() ||
		command.RevisionID.UUID() == command.TournamentID ||
		command.RevisionID.UUID() == command.GroupID || command.RevisionID.UUID() == source.ProjectionID ||
		command.RevisionID == command.ExpectedSourceRevisionID {
		return goldenGroupError("invalid or reused command identity")
	}
	return nil
}

func validateGoldenUsedRevisionIdentities(
	command GroupRevisionCommand,
	source StandingsProjection,
	used []RevisionIdentity,
) error {
	usedGroups := make(map[uuid.UUID]struct{}, len(used))
	usedRevisions := make(map[domain.DerivedRevisionID]struct{}, len(used))
	for _, identity := range used {
		if err := validateGoldenUsedRevisionIdentity(command, source, identity); err != nil {
			return err
		}
		if _, duplicate := usedRevisions[identity.RevisionID]; duplicate {
			return goldenGroupError("used revision identity is duplicated")
		}
		if _, crossAlias := usedGroups[identity.RevisionID.UUID()]; crossAlias {
			return goldenGroupError("used identity is cross-aliased")
		}
		if _, crossAlias := usedRevisions[domain.DerivedRevisionID(identity.GroupID)]; crossAlias {
			return goldenGroupError("used identity is cross-aliased")
		}
		usedGroups[identity.GroupID] = struct{}{}
		usedRevisions[identity.RevisionID] = struct{}{}
	}
	return nil
}

func validateGoldenUsedRevisionIdentity(
	command GroupRevisionCommand,
	source StandingsProjection,
	identity RevisionIdentity,
) error {
	if err := validateGoldenUsedIdentityAgainstPreviousSource(source, identity); err != nil {
		return err
	}
	return validateGoldenUsedIdentityAgainstCurrentSource(command, source, identity)
}

func validateGoldenUsedIdentityAgainstCurrentSource(
	command GroupRevisionCommand,
	source StandingsProjection,
	identity RevisionIdentity,
) error {
	if identity.GroupID == uuid.Nil || identity.RevisionID.IsZero() || identity.GroupID == identity.RevisionID.UUID() ||
		identity.RevisionID == command.RevisionID || identity.GroupID == command.RevisionID.UUID() ||
		identity.RevisionID.UUID() == command.GroupID ||
		identity.GroupID == source.TournamentID || identity.GroupID == source.ProjectionID ||
		identity.GroupID == source.RevisionID.UUID() || identity.RevisionID.UUID() == source.TournamentID ||
		identity.RevisionID.UUID() == source.ProjectionID || identity.RevisionID == source.RevisionID ||
		(command.RevisionNo == 1 && identity.GroupID == command.GroupID) {
		return goldenGroupError("revision identity was already used")
	}
	return nil
}

func validateGoldenUsedIdentityAgainstPreviousSource(
	source StandingsProjection,
	identity RevisionIdentity,
) error {
	if source.PreviousRevisionID == nil {
		return nil
	}
	if identity.GroupID == source.PreviousRevisionID.UUID() || identity.RevisionID == *source.PreviousRevisionID {
		return goldenGroupError("revision identity was already used")
	}
	return nil
}

func validateGoldenGroupPredecessor(
	command GroupRevisionCommand,
	source StandingsProjection,
	predecessor *GroupRevision,
) error {
	if command.RevisionNo == 1 {
		if command.PreviousRevisionID != nil || predecessor != nil {
			return goldenGroupError("initial revision has a predecessor")
		}
		return nil
	}
	if err := validateGoldenRevisionPredecessor(command, predecessor); err != nil {
		return err
	}
	return validateGoldenSourcePredecessor(source, predecessor)
}

func validateGoldenRevisionPredecessor(
	command GroupRevisionCommand,
	predecessor *GroupRevision,
) error {
	if command.RevisionNo < 2 || command.PreviousRevisionID == nil || predecessor == nil {
		return goldenGroupError("predecessor lineage does not match")
	}
	if predecessor.Validate() != nil || predecessor.TournamentID() != command.TournamentID ||
		predecessor.GroupID() != command.GroupID || predecessor.RevisionNo()+1 != command.RevisionNo ||
		predecessor.RevisionID() != *command.PreviousRevisionID {
		return goldenGroupError("predecessor lineage does not match")
	}
	return nil
}

func validateGoldenSourcePredecessor(
	source StandingsProjection,
	predecessor *GroupRevision,
) error {
	if source.ProjectionID != predecessor.SourceProjectionID() ||
		source.RevisionNo != predecessor.SourceProjectionRevisionNo()+1 || source.PreviousRevisionID == nil ||
		*source.PreviousRevisionID != predecessor.SourceProjectionRevisionID() {
		return goldenGroupError("predecessor lineage does not match")
	}
	return nil
}

func validateGoldenGroupMembers(members []GroupMemberSeed) error {
	for index, member := range members {
		if err := validateGoldenGroupMember(member); err != nil {
			return err
		}
		if index > 0 && bytes.Compare(members[index-1].ParticipantID[:], member.ParticipantID[:]) >= 0 {
			return goldenGroupError("members are duplicate or not canonical")
		}
		if index > 0 && members[index-1].Points != member.Points {
			return goldenGroupError("members do not share exact Swiss points")
		}
	}
	return nil
}

func validateGoldenGroupMember(member GroupMemberSeed) error {
	if member.ParticipantID == uuid.Nil || member.Points < 0 || member.Buchholz < 0 ||
		member.HeadToHeadPoints < 0 || member.EffectiveTime < 0 || member.Seed < 1 ||
		(member.AcceptedSolveTime != nil && (*member.AcceptedSolveTime < 0 || *member.AcceptedSolveTime > member.EffectiveTime)) ||
		(!member.HeadToHeadApplied && member.HeadToHeadPoints != 0) {
		return goldenGroupError("invalid member ordering input")
	}
	return nil
}

// ValidateGroupMember validates one retained Golden ordering input.
func ValidateGroupMember(member GroupMemberSeed) error {
	return validateGoldenGroupMember(member)
}

type goldenGroupPayloadDocument struct {
	Purpose                            GroupRevisionPurpose               `json:"purpose"`
	Effect                             GroupRevisionEffect                `json:"effect"`
	TournamentID                       string                             `json:"tournament_id"`
	GroupID                            string                             `json:"group_id"`
	RevisionID                         string                             `json:"revision_id"`
	RevisionNo                         int                                `json:"revision_no"`
	PreviousRevisionID                 *string                            `json:"previous_revision_id,omitempty"`
	SourceProjectionID                 string                             `json:"source_projection_id"`
	SourceProjectionRevisionID         string                             `json:"source_projection_revision_id"`
	SourceProjectionRevisionNo         int                                `json:"source_projection_revision_no"`
	SourceProjectionPreviousRevisionID *string                            `json:"source_projection_previous_revision_id,omitempty"`
	SourceProjectionPayloadDigest      string                             `json:"source_projection_payload_digest"`
	PositionFrom                       int                                `json:"position_from"`
	PositionTo                         int                                `json:"position_to"`
	Members                            []goldenGroupMemberPayloadDocument `json:"members"`
}

type goldenGroupMemberPayloadDocument struct {
	ParticipantID     string         `json:"participant_id"`
	Points            int            `json:"points"`
	Buchholz          int            `json:"buchholz"`
	HeadToHeadPoints  int            `json:"head_to_head_points"`
	HeadToHeadApplied bool           `json:"head_to_head_applied"`
	EffectiveTime     time.Duration  `json:"effective_time"`
	AcceptedSolveTime *time.Duration `json:"accepted_solve_time,omitempty"`
	Seed              int            `json:"seed"`
}

func goldenGroupPayload(state goldenGroupRevisionState) ([]byte, error) {
	document := goldenGroupPayloadDocument{
		Purpose: state.Purpose, Effect: state.Effect,
		TournamentID: state.TournamentID.String(), GroupID: state.GroupID.String(),
		RevisionID: state.RevisionID.UUID().String(), RevisionNo: state.RevisionNo,
		SourceProjectionID:            state.SourceProjectionID.String(),
		SourceProjectionRevisionID:    state.SourceProjectionRevisionID.UUID().String(),
		SourceProjectionRevisionNo:    state.SourceProjectionRevisionNo,
		SourceProjectionPayloadDigest: hex.EncodeToString(state.SourceProjectionPayloadDigest[:]),
		PositionFrom:                  state.PositionFrom, PositionTo: state.PositionTo,
		Members: make([]goldenGroupMemberPayloadDocument, len(state.Members)),
	}
	if state.PreviousRevisionID != nil {
		value := state.PreviousRevisionID.UUID().String()
		document.PreviousRevisionID = &value
	}
	if state.SourceProjectionPreviousRevisionID != nil {
		value := state.SourceProjectionPreviousRevisionID.UUID().String()
		document.SourceProjectionPreviousRevisionID = &value
	}
	for index, member := range state.Members {
		document.Members[index] = goldenGroupMemberPayloadDocument{
			ParticipantID: member.ParticipantID.String(), Points: member.Points, Buchholz: member.Buchholz,
			HeadToHeadPoints: member.HeadToHeadPoints, HeadToHeadApplied: member.HeadToHeadApplied,
			EffectiveTime:     member.EffectiveTime,
			AcceptedSolveTime: cloneGoldenDuration(member.AcceptedSolveTime), Seed: member.Seed,
		}
	}
	return json.Marshal(document)
}

func goldenGroupError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidGroupRevision, fmt.Sprintf(format, arguments...))
}
