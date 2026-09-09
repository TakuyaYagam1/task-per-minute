package golden

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

var (
	ErrInvalidGroupRevision = errors.New("invalid Golden group revision")
	ErrGroupSourceStale     = errors.New("golden group source is stale")
)

type GroupRevisionPurpose string

type GroupRevisionEffect string

const (
	GroupRevisionPurposeSeedOnly       GroupRevisionPurpose = "golden_seed_only"
	GroupRevisionEffectNoSwissMutation GroupRevisionEffect  = "preserve_swiss_points_and_defer_rank"
)

type GroupRevisionCommand struct {
	TournamentID                uuid.UUID
	GroupID                     uuid.UUID
	RevisionID                  domain.DerivedRevisionID
	RevisionNo                  int
	PreviousRevisionID          *domain.DerivedRevisionID
	ExpectedSourceRevisionID    domain.DerivedRevisionID
	ExpectedSourcePayloadDigest [sha256.Size]byte
	PositionFrom                int
	PositionTo                  int
}

type RevisionIdentity struct {
	GroupID    uuid.UUID
	RevisionID domain.DerivedRevisionID
}

type goldenGroupRevisionState struct {
	Purpose                            GroupRevisionPurpose
	Effect                             GroupRevisionEffect
	TournamentID                       uuid.UUID
	GroupID                            uuid.UUID
	RevisionID                         domain.DerivedRevisionID
	RevisionNo                         int
	PreviousRevisionID                 *domain.DerivedRevisionID
	SourceProjectionID                 uuid.UUID
	SourceProjectionRevisionID         domain.DerivedRevisionID
	SourceProjectionRevisionNo         int
	SourceProjectionPreviousRevisionID *domain.DerivedRevisionID
	SourceProjectionPayloadDigest      [sha256.Size]byte
	PositionFrom                       int
	PositionTo                         int
	Members                            []GroupMemberSeed
	Payload                            []byte
	PayloadDigest                      [sha256.Size]byte
	ProofHash                          string
}

// RevisionSnapshot is the complete durable representation of one immutable
// Golden group revision. Its payload, digest, and proof bind every canonical
// field and are verified by RestoreGroupRevision.
type RevisionSnapshot struct {
	Purpose                            GroupRevisionPurpose
	Effect                             GroupRevisionEffect
	TournamentID                       uuid.UUID
	GroupID                            uuid.UUID
	RevisionID                         domain.DerivedRevisionID
	RevisionNo                         int
	PreviousRevisionID                 *domain.DerivedRevisionID
	SourceProjectionID                 uuid.UUID
	SourceProjectionRevisionID         domain.DerivedRevisionID
	SourceProjectionRevisionNo         int
	SourceProjectionPreviousRevisionID *domain.DerivedRevisionID
	SourceProjectionPayloadDigest      [sha256.Size]byte
	PositionFrom                       int
	PositionTo                         int
	Members                            []GroupMemberSeed
	Payload                            []byte
	PayloadDigest                      [sha256.Size]byte
	ProofHash                          string
}

// GroupRevision contains topology only. Mutable participation and
// attempt state remains in domain.GoldenGroup.
type GroupRevision struct {
	state goldenGroupRevisionState
}

func BuildGroupRevision(
	command GroupRevisionCommand,
	source StandingsProjection,
	seed TieGroupSeed,
	predecessor *GroupRevision,
	used []RevisionIdentity,
) (GroupRevision, error) {
	if err := source.Validate(); err != nil {
		return GroupRevision{}, goldenGroupError("source: %v", err)
	}
	if err := validateGoldenGroupCommand(command, source, predecessor, used); err != nil {
		return GroupRevision{}, err
	}
	if command.ExpectedSourceRevisionID != source.RevisionID ||
		command.ExpectedSourcePayloadDigest != source.PayloadDigest {
		return GroupRevision{}, fmt.Errorf("%w: expected projection does not match current source", ErrGroupSourceStale)
	}
	if err := validateGoldenGroupSeed(command, source, seed); err != nil {
		return GroupRevision{}, err
	}

	state := goldenGroupRevisionState{
		Purpose:                            GroupRevisionPurposeSeedOnly,
		Effect:                             GroupRevisionEffectNoSwissMutation,
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
		Members:                            CloneGroupMemberSeeds(seed.Members),
	}
	return buildGoldenGroupRevision(state)
}

func validateGoldenGroupSeed(
	command GroupRevisionCommand,
	source StandingsProjection,
	seed TieGroupSeed,
) error {
	if seed.TournamentID != source.TournamentID ||
		seed.SourceProjectionRevisionID != source.RevisionID ||
		seed.SourceProjectionPayloadDigest != source.PayloadDigest ||
		seed.PositionFrom != command.PositionFrom || seed.PositionTo != command.PositionTo {
		return goldenGroupError("seed does not match the exact source interval")
	}
	partition, err := PartitionTies(source)
	if err != nil {
		return goldenGroupError("partition source: %v", err)
	}
	for _, current := range partition.Groups() {
		if current.PositionFrom == seed.PositionFrom && current.PositionTo == seed.PositionTo &&
			reflect.DeepEqual(current, seed) {
			return nil
		}
	}
	return goldenGroupError("seed is not one exact maximal impacting Golden segment")
}

func buildGoldenGroupRevision(state goldenGroupRevisionState) (GroupRevision, error) {
	payload, err := goldenGroupPayload(state)
	if err != nil {
		return GroupRevision{}, goldenGroupError("encode payload: %v", err)
	}
	state.Payload = payload
	state.PayloadDigest = sha256.Sum256(payload)
	state.ProofHash = hex.EncodeToString(state.PayloadDigest[:])
	revision := GroupRevision{state: state}
	if err := revision.Validate(); err != nil {
		return GroupRevision{}, err
	}
	return revision.Snapshot(), nil
}

func (r GroupRevision) Validate() error {
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
	if state.Purpose != GroupRevisionPurposeSeedOnly || state.Effect != GroupRevisionEffectNoSwissMutation ||
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

func (r GroupRevision) Snapshot() GroupRevision {
	clone := r
	clone.state.PreviousRevisionID = cloneGoldenRevisionID(r.state.PreviousRevisionID)
	clone.state.SourceProjectionPreviousRevisionID = cloneGoldenRevisionID(r.state.SourceProjectionPreviousRevisionID)
	clone.state.Members = CloneGroupMemberSeeds(r.state.Members)
	clone.state.Payload = append([]byte(nil), r.state.Payload...)
	return clone
}

// PersistenceSnapshot returns a deep copy suitable for durable storage.
func (r GroupRevision) PersistenceSnapshot() RevisionSnapshot {
	state := r.state
	return RevisionSnapshot{
		Purpose:                            state.Purpose,
		Effect:                             state.Effect,
		TournamentID:                       state.TournamentID,
		GroupID:                            state.GroupID,
		RevisionID:                         state.RevisionID,
		RevisionNo:                         state.RevisionNo,
		PreviousRevisionID:                 cloneGoldenRevisionID(state.PreviousRevisionID),
		SourceProjectionID:                 state.SourceProjectionID,
		SourceProjectionRevisionID:         state.SourceProjectionRevisionID,
		SourceProjectionRevisionNo:         state.SourceProjectionRevisionNo,
		SourceProjectionPreviousRevisionID: cloneGoldenRevisionID(state.SourceProjectionPreviousRevisionID),
		SourceProjectionPayloadDigest:      state.SourceProjectionPayloadDigest,
		PositionFrom:                       state.PositionFrom,
		PositionTo:                         state.PositionTo,
		Members:                            CloneGroupMemberSeeds(state.Members),
		Payload:                            append([]byte(nil), state.Payload...),
		PayloadDigest:                      state.PayloadDigest,
		ProofHash:                          state.ProofHash,
	}
}

// RestoreGroupRevision rebuilds and verifies a durable Golden group revision.
func RestoreGroupRevision(snapshot RevisionSnapshot) (GroupRevision, error) {
	revision, err := buildGoldenGroupRevision(goldenGroupRevisionState{
		Purpose:                            snapshot.Purpose,
		Effect:                             snapshot.Effect,
		TournamentID:                       snapshot.TournamentID,
		GroupID:                            snapshot.GroupID,
		RevisionID:                         snapshot.RevisionID,
		RevisionNo:                         snapshot.RevisionNo,
		PreviousRevisionID:                 cloneGoldenRevisionID(snapshot.PreviousRevisionID),
		SourceProjectionID:                 snapshot.SourceProjectionID,
		SourceProjectionRevisionID:         snapshot.SourceProjectionRevisionID,
		SourceProjectionRevisionNo:         snapshot.SourceProjectionRevisionNo,
		SourceProjectionPreviousRevisionID: cloneGoldenRevisionID(snapshot.SourceProjectionPreviousRevisionID),
		SourceProjectionPayloadDigest:      snapshot.SourceProjectionPayloadDigest,
		PositionFrom:                       snapshot.PositionFrom,
		PositionTo:                         snapshot.PositionTo,
		Members:                            CloneGroupMemberSeeds(snapshot.Members),
	})
	if err != nil {
		return GroupRevision{}, err
	}
	canonical := revision.PersistenceSnapshot()
	if !bytes.Equal(snapshot.Payload, canonical.Payload) ||
		snapshot.PayloadDigest != canonical.PayloadDigest || snapshot.ProofHash != canonical.ProofHash {
		return GroupRevision{}, goldenGroupError("persistence payload, digest, or proof does not match canonical revision")
	}
	return revision, nil
}

func (r GroupRevision) TournamentID() uuid.UUID { return r.state.TournamentID }

func (r GroupRevision) Purpose() GroupRevisionPurpose { return r.state.Purpose }

func (r GroupRevision) Effect() GroupRevisionEffect { return r.state.Effect }

func (r GroupRevision) GroupID() uuid.UUID { return r.state.GroupID }

func (r GroupRevision) RevisionID() domain.DerivedRevisionID { return r.state.RevisionID }

func (r GroupRevision) RevisionNo() int { return r.state.RevisionNo }

func (r GroupRevision) PreviousRevisionID() *domain.DerivedRevisionID {
	return cloneGoldenRevisionID(r.state.PreviousRevisionID)
}

func (r GroupRevision) SourceProjectionRevisionID() domain.DerivedRevisionID {
	return r.state.SourceProjectionRevisionID
}

func (r GroupRevision) SourceProjectionID() uuid.UUID { return r.state.SourceProjectionID }

func (r GroupRevision) SourceProjectionRevisionNo() int {
	return r.state.SourceProjectionRevisionNo
}

func (r GroupRevision) SourceProjectionPreviousRevisionID() *domain.DerivedRevisionID {
	return cloneGoldenRevisionID(r.state.SourceProjectionPreviousRevisionID)
}

func (r GroupRevision) SourceProjectionPayloadDigest() [sha256.Size]byte {
	return r.state.SourceProjectionPayloadDigest
}

func (r GroupRevision) Positions() (int, int) {
	return r.state.PositionFrom, r.state.PositionTo
}

func (r GroupRevision) Members() []GroupMemberSeed {
	return CloneGroupMemberSeeds(r.state.Members)
}

func (r GroupRevision) Payload() []byte { return append([]byte(nil), r.state.Payload...) }

func (r GroupRevision) PayloadDigest() [sha256.Size]byte { return r.state.PayloadDigest }

func (r GroupRevision) ProofHash() string { return r.state.ProofHash }
