package arena

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidGoldenGroupRevision = errors.New("invalid Golden group revision")
	ErrGoldenGroupSourceStale     = errors.New("golden group source is stale")
)

type GoldenGroupRevisionPurpose string

type GoldenGroupRevisionEffect string

const (
	GoldenGroupRevisionPurposeSeedOnly       GoldenGroupRevisionPurpose = "golden_seed_only"
	GoldenGroupRevisionEffectNoSwissMutation GoldenGroupRevisionEffect  = "preserve_swiss_points_and_defer_rank"
)

type GoldenGroupRevisionCommand struct {
	TournamentID                uuid.UUID
	GroupID                     uuid.UUID
	RevisionID                  domain.ArenaDerivedRevisionID
	RevisionNo                  int
	PreviousRevisionID          *domain.ArenaDerivedRevisionID
	ExpectedSourceRevisionID    domain.ArenaDerivedRevisionID
	ExpectedSourcePayloadDigest [sha256.Size]byte
	PositionFrom                int
	PositionTo                  int
}

type GoldenRevisionIdentity struct {
	GroupID    uuid.UUID
	RevisionID domain.ArenaDerivedRevisionID
}

type goldenGroupRevisionState struct {
	Purpose                            GoldenGroupRevisionPurpose
	Effect                             GoldenGroupRevisionEffect
	TournamentID                       uuid.UUID
	GroupID                            uuid.UUID
	RevisionID                         domain.ArenaDerivedRevisionID
	RevisionNo                         int
	PreviousRevisionID                 *domain.ArenaDerivedRevisionID
	SourceProjectionID                 uuid.UUID
	SourceProjectionRevisionID         domain.ArenaDerivedRevisionID
	SourceProjectionRevisionNo         int
	SourceProjectionPreviousRevisionID *domain.ArenaDerivedRevisionID
	SourceProjectionPayloadDigest      [sha256.Size]byte
	PositionFrom                       int
	PositionTo                         int
	Members                            []GoldenGroupMemberSeed
	Payload                            []byte
	PayloadDigest                      [sha256.Size]byte
	ProofHash                          string
}

// GoldenGroupRevision contains topology only. Mutable participation and
// attempt state remains in domain.ArenaGoldenGroup.
type GoldenGroupRevision struct {
	state goldenGroupRevisionState
}

func BuildGoldenGroupRevision(
	command GoldenGroupRevisionCommand,
	source GoldenStandingsProjection,
	seed GoldenTieGroupSeed,
	predecessor *GoldenGroupRevision,
	used []GoldenRevisionIdentity,
) (GoldenGroupRevision, error) {
	if err := source.Validate(); err != nil {
		return GoldenGroupRevision{}, goldenGroupError("source: %v", err)
	}
	if err := validateGoldenGroupCommand(command, source, predecessor, used); err != nil {
		return GoldenGroupRevision{}, err
	}
	if command.ExpectedSourceRevisionID != source.RevisionID ||
		command.ExpectedSourcePayloadDigest != source.PayloadDigest {
		return GoldenGroupRevision{}, fmt.Errorf("%w: expected projection does not match current source", ErrGoldenGroupSourceStale)
	}
	if err := validateGoldenGroupSeed(command, source, seed); err != nil {
		return GoldenGroupRevision{}, err
	}

	state := goldenGroupRevisionState{
		Purpose:                            GoldenGroupRevisionPurposeSeedOnly,
		Effect:                             GoldenGroupRevisionEffectNoSwissMutation,
		TournamentID:                       command.TournamentID,
		GroupID:                            command.GroupID,
		RevisionID:                         command.RevisionID,
		RevisionNo:                         command.RevisionNo,
		PreviousRevisionID:                 cloneGoldenRevisionID(command.PreviousRevisionID),
		SourceProjectionID:                 source.ProjectionID,
		SourceProjectionRevisionID:         source.RevisionID,
		SourceProjectionRevisionNo:         source.RevisionNo,
		SourceProjectionPreviousRevisionID: cloneGoldenRevisionID(source.PreviousRevisionID),
		SourceProjectionPayloadDigest:      source.PayloadDigest,
		PositionFrom:                       command.PositionFrom,
		PositionTo:                         command.PositionTo,
		Members:                            cloneGoldenMemberSeeds(seed.Members),
	}
	return buildGoldenGroupRevision(state)
}

func validateGoldenGroupSeed(
	command GoldenGroupRevisionCommand,
	source GoldenStandingsProjection,
	seed GoldenTieGroupSeed,
) error {
	if seed.TournamentID != source.TournamentID ||
		seed.SourceProjectionRevisionID != source.RevisionID ||
		seed.SourceProjectionPayloadDigest != source.PayloadDigest ||
		seed.PositionFrom != command.PositionFrom || seed.PositionTo != command.PositionTo {
		return goldenGroupError("seed does not match the exact source interval")
	}
	partition, err := PartitionGoldenTies(source)
	if err != nil {
		return goldenGroupError("partition source: %v", err)
	}
	for _, current := range partition.GoldenGroups() {
		if current.PositionFrom == seed.PositionFrom && current.PositionTo == seed.PositionTo &&
			reflect.DeepEqual(current, seed) {
			return nil
		}
	}
	return goldenGroupError("seed is not one exact maximal impacting Golden segment")
}

func buildGoldenGroupRevision(state goldenGroupRevisionState) (GoldenGroupRevision, error) {
	payload, err := goldenGroupPayload(state)
	if err != nil {
		return GoldenGroupRevision{}, goldenGroupError("encode payload: %v", err)
	}
	state.Payload = payload
	state.PayloadDigest = sha256.Sum256(payload)
	state.ProofHash = hex.EncodeToString(state.PayloadDigest[:])
	revision := GoldenGroupRevision{state: state}
	if err := revision.Validate(); err != nil {
		return GoldenGroupRevision{}, err
	}
	return revision.Snapshot(), nil
}

func (r GoldenGroupRevision) Validate() error {
	state := r.state
	if err := validateGoldenGroupStateIdentity(state); err != nil {
		return err
	}
	if err := validateGoldenGroupStateIdentitySet(state); err != nil {
		return err
	}
	if err := validateGoldenGroupStateLineage(state); err != nil {
		return err
	}
	if err := validateGoldenGroupStateInterval(state); err != nil {
		return err
	}
	if err := validateGoldenGroupMembers(state.Members); err != nil {
		return err
	}
	return validateGoldenGroupStatePayload(state)
}

func validateGoldenGroupStateIdentity(state goldenGroupRevisionState) error {
	if state.Purpose != GoldenGroupRevisionPurposeSeedOnly || state.Effect != GoldenGroupRevisionEffectNoSwissMutation ||
		state.TournamentID == uuid.Nil || state.GroupID == uuid.Nil || state.RevisionID.IsZero() ||
		state.RevisionID.UUID() == state.TournamentID || state.RevisionID.UUID() == state.GroupID ||
		state.SourceProjectionRevisionID.IsZero() || state.RevisionID == state.SourceProjectionRevisionID ||
		state.SourceProjectionID == uuid.Nil || state.SourceProjectionRevisionNo < 1 ||
		state.SourceProjectionPayloadDigest == [sha256.Size]byte{} {
		return goldenGroupError("invalid revision identity")
	}
	return nil
}

func validateGoldenGroupStateIdentitySet(state goldenGroupRevisionState) error {
	ids := []uuid.UUID{
		state.TournamentID, state.GroupID, state.RevisionID.UUID(),
		state.SourceProjectionID, state.SourceProjectionRevisionID.UUID(),
	}
	if state.PreviousRevisionID != nil {
		ids = append(ids, state.PreviousRevisionID.UUID())
	}
	if state.SourceProjectionPreviousRevisionID != nil {
		ids = append(ids, state.SourceProjectionPreviousRevisionID.UUID())
	}
	seenIDs := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, duplicate := seenIDs[id]; duplicate {
			return goldenGroupError("revision identity is cross-aliased")
		}
		seenIDs[id] = struct{}{}
	}
	return nil
}

func validateGoldenGroupStateLineage(state goldenGroupRevisionState) error {
	if state.RevisionNo < 1 || (state.RevisionNo == 1 && state.PreviousRevisionID != nil) ||
		(state.RevisionNo > 1 && (state.PreviousRevisionID == nil || state.PreviousRevisionID.IsZero())) ||
		(state.PreviousRevisionID != nil && (*state.PreviousRevisionID == state.RevisionID || *state.PreviousRevisionID == state.SourceProjectionRevisionID)) {
		return goldenGroupError("invalid predecessor lineage")
	}
	if (state.SourceProjectionRevisionNo == 1 && state.SourceProjectionPreviousRevisionID != nil) ||
		(state.SourceProjectionRevisionNo > 1 && (state.SourceProjectionPreviousRevisionID == nil || state.SourceProjectionPreviousRevisionID.IsZero())) {
		return goldenGroupError("invalid source projection lineage")
	}
	return nil
}

func validateGoldenGroupStateInterval(state goldenGroupRevisionState) error {
	if state.PositionFrom < 1 || state.PositionTo < state.PositionFrom ||
		state.PositionTo-state.PositionFrom+1 != len(state.Members) || len(state.Members) < 2 {
		return goldenGroupError("invalid affected interval")
	}
	return nil
}

func validateGoldenGroupStatePayload(state goldenGroupRevisionState) error {
	payload, err := goldenGroupPayload(state)
	digest := sha256.Sum256(state.Payload)
	if err != nil || len(state.Payload) == 0 || !bytes.Equal(payload, state.Payload) ||
		digest != state.PayloadDigest || state.ProofHash != hex.EncodeToString(digest[:]) {
		return goldenGroupError("canonical payload or proof changed")
	}
	return nil
}

func (r GoldenGroupRevision) Snapshot() GoldenGroupRevision {
	clone := r
	clone.state.PreviousRevisionID = cloneGoldenRevisionID(r.state.PreviousRevisionID)
	clone.state.SourceProjectionPreviousRevisionID = cloneGoldenRevisionID(r.state.SourceProjectionPreviousRevisionID)
	clone.state.Members = cloneGoldenMemberSeeds(r.state.Members)
	clone.state.Payload = append([]byte(nil), r.state.Payload...)
	return clone
}

func (r GoldenGroupRevision) TournamentID() uuid.UUID { return r.state.TournamentID }

func (r GoldenGroupRevision) Purpose() GoldenGroupRevisionPurpose { return r.state.Purpose }

func (r GoldenGroupRevision) Effect() GoldenGroupRevisionEffect { return r.state.Effect }

func (r GoldenGroupRevision) GroupID() uuid.UUID { return r.state.GroupID }

func (r GoldenGroupRevision) RevisionID() domain.ArenaDerivedRevisionID { return r.state.RevisionID }

func (r GoldenGroupRevision) RevisionNo() int { return r.state.RevisionNo }

func (r GoldenGroupRevision) PreviousRevisionID() *domain.ArenaDerivedRevisionID {
	return cloneGoldenRevisionID(r.state.PreviousRevisionID)
}

func (r GoldenGroupRevision) SourceProjectionRevisionID() domain.ArenaDerivedRevisionID {
	return r.state.SourceProjectionRevisionID
}

func (r GoldenGroupRevision) SourceProjectionID() uuid.UUID { return r.state.SourceProjectionID }

func (r GoldenGroupRevision) SourceProjectionRevisionNo() int {
	return r.state.SourceProjectionRevisionNo
}

func (r GoldenGroupRevision) SourceProjectionPreviousRevisionID() *domain.ArenaDerivedRevisionID {
	return cloneGoldenRevisionID(r.state.SourceProjectionPreviousRevisionID)
}

func (r GoldenGroupRevision) SourceProjectionPayloadDigest() [sha256.Size]byte {
	return r.state.SourceProjectionPayloadDigest
}

func (r GoldenGroupRevision) Positions() (int, int) {
	return r.state.PositionFrom, r.state.PositionTo
}

func (r GoldenGroupRevision) Members() []GoldenGroupMemberSeed {
	return cloneGoldenMemberSeeds(r.state.Members)
}

func (r GoldenGroupRevision) Payload() []byte { return append([]byte(nil), r.state.Payload...) }

func (r GoldenGroupRevision) PayloadDigest() [sha256.Size]byte { return r.state.PayloadDigest }

func (r GoldenGroupRevision) ProofHash() string { return r.state.ProofHash }

func validateGoldenGroupCommand(
	command GoldenGroupRevisionCommand,
	source GoldenStandingsProjection,
	predecessor *GoldenGroupRevision,
	used []GoldenRevisionIdentity,
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

func validateGoldenGroupCommandIdentity(command GoldenGroupRevisionCommand, source GoldenStandingsProjection) error {
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
	command GoldenGroupRevisionCommand,
	source GoldenStandingsProjection,
	used []GoldenRevisionIdentity,
) error {
	usedGroups := make(map[uuid.UUID]struct{}, len(used))
	usedRevisions := make(map[domain.ArenaDerivedRevisionID]struct{}, len(used))
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
		if _, crossAlias := usedRevisions[domain.ArenaDerivedRevisionID(identity.GroupID)]; crossAlias {
			return goldenGroupError("used identity is cross-aliased")
		}
		usedGroups[identity.GroupID] = struct{}{}
		usedRevisions[identity.RevisionID] = struct{}{}
	}
	return nil
}

func validateGoldenUsedRevisionIdentity(
	command GoldenGroupRevisionCommand,
	source GoldenStandingsProjection,
	identity GoldenRevisionIdentity,
) error {
	if err := validateGoldenUsedIdentityAgainstPreviousSource(source, identity); err != nil {
		return err
	}
	return validateGoldenUsedIdentityAgainstCurrentSource(command, source, identity)
}

func validateGoldenUsedIdentityAgainstCurrentSource(
	command GoldenGroupRevisionCommand,
	source GoldenStandingsProjection,
	identity GoldenRevisionIdentity,
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
	source GoldenStandingsProjection,
	identity GoldenRevisionIdentity,
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
	command GoldenGroupRevisionCommand,
	source GoldenStandingsProjection,
	predecessor *GoldenGroupRevision,
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
	command GoldenGroupRevisionCommand,
	predecessor *GoldenGroupRevision,
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
	source GoldenStandingsProjection,
	predecessor *GoldenGroupRevision,
) error {
	if source.ProjectionID != predecessor.SourceProjectionID() ||
		source.RevisionNo != predecessor.SourceProjectionRevisionNo()+1 || source.PreviousRevisionID == nil ||
		*source.PreviousRevisionID != predecessor.SourceProjectionRevisionID() {
		return goldenGroupError("predecessor lineage does not match")
	}
	return nil
}

func validateGoldenGroupMembers(members []GoldenGroupMemberSeed) error {
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

func validateGoldenGroupMember(member GoldenGroupMemberSeed) error {
	if member.ParticipantID == uuid.Nil || member.Points < 0 || member.Buchholz < 0 ||
		member.HeadToHeadPoints < 0 || member.EffectiveTime < 0 || member.Seed < 1 ||
		(member.AcceptedSolveTime != nil && (*member.AcceptedSolveTime < 0 || *member.AcceptedSolveTime > member.EffectiveTime)) ||
		(!member.HeadToHeadApplied && member.HeadToHeadPoints != 0) {
		return goldenGroupError("invalid member ordering input")
	}
	return nil
}

type goldenGroupPayloadDocument struct {
	Purpose                            GoldenGroupRevisionPurpose         `json:"purpose"`
	Effect                             GoldenGroupRevisionEffect          `json:"effect"`
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
	return fmt.Errorf("%w: %s", ErrInvalidGoldenGroupRevision, fmt.Sprintf(format, arguments...))
}
