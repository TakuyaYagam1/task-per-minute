package golden

import (
	"bytes"
	"crypto/sha256"
	"reflect"
	"slices"
	"sort"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"

	"github.com/google/uuid"
)

func normalizeGoldenPlanGroups(
	source StandingsProjection,
	input []GroupAuthority,
) ([]GroupAuthority, []uuid.UUID, error) {
	partition, err := PartitionTies(source)
	if err != nil {
		return nil, nil, goldenExactPlanError("partition: %v", err)
	}
	groups := canonicalGoldenPlanGroups(input)
	if len(groups) == 0 {
		return nil, nil, goldenExactPlanError("active group set is empty")
	}
	state := newGoldenPlanGroupNormalization(source, partition.Groups(), len(groups))
	for _, group := range groups {
		if err := state.add(group); err != nil {
			return nil, nil, err
		}
	}
	sort.Slice(state.participants, func(i, j int) bool {
		return bytes.Compare(state.participants[i][:], state.participants[j][:]) < 0
	})
	return groups, state.participants, nil
}

func canonicalGoldenPlanGroups(input []GroupAuthority) []GroupAuthority {
	groups := make([]GroupAuthority, len(input))
	for index, group := range input {
		groups[index] = GroupAuthority{
			Revision:             group.Revision.Snapshot(),
			ActiveParticipantIDs: append([]uuid.UUID(nil), group.ActiveParticipantIDs...),
		}
		sort.Slice(groups[index].ActiveParticipantIDs, func(i, j int) bool {
			return bytes.Compare(groups[index].ActiveParticipantIDs[i][:], groups[index].ActiveParticipantIDs[j][:]) < 0
		})
	}
	sort.Slice(groups, func(i, j int) bool {
		left, _ := groups[i].Revision.Positions()
		right, _ := groups[j].Revision.Positions()
		return left < right
	})
	return groups
}

type goldenPlanGroupNormalization struct {
	source           StandingsProjection
	seeds            []TieGroupSeed
	participants     []uuid.UUID
	seenGroups       map[uuid.UUID]struct{}
	seenRevisions    map[domain.DerivedRevisionID]struct{}
	seenParticipants map[uuid.UUID]struct{}
	matchedSeeds     map[int]struct{}
	lastPosition     int
}

func newGoldenPlanGroupNormalization(
	source StandingsProjection,
	seeds []TieGroupSeed,
	groupCount int,
) *goldenPlanGroupNormalization {
	return &goldenPlanGroupNormalization{
		source: source, seeds: seeds,
		participants:     make([]uuid.UUID, 0, len(source.Standings)),
		seenGroups:       make(map[uuid.UUID]struct{}, groupCount),
		seenRevisions:    make(map[domain.DerivedRevisionID]struct{}, groupCount),
		seenParticipants: make(map[uuid.UUID]struct{}, len(source.Standings)),
		matchedSeeds:     make(map[int]struct{}, groupCount),
	}
}

func (state *goldenPlanGroupNormalization) add(group GroupAuthority) error {
	if err := validateGoldenPlanGroupSource(state.source, group); err != nil {
		return err
	}
	if err := state.claimTopology(group); err != nil {
		return err
	}
	return state.collectActiveParticipants(group)
}

func validateGoldenPlanGroupSource(
	source StandingsProjection,
	group GroupAuthority,
) error {
	if group.Revision.Validate() != nil || group.Revision.TournamentID() != source.TournamentID ||
		group.Revision.SourceProjectionID() != source.ProjectionID ||
		group.Revision.SourceProjectionRevisionID() != source.RevisionID ||
		group.Revision.SourceProjectionRevisionNo() != source.RevisionNo ||
		!equalGoldenPlanRevisionPointers(
			group.Revision.SourceProjectionPreviousRevisionID(),
			source.PreviousRevisionID,
		) ||
		group.Revision.SourceProjectionPayloadDigest() != source.PayloadDigest {
		return goldenExactPlanError("group revision does not match source")
	}
	return nil
}

func equalGoldenPlanRevisionPointers(
	first *domain.DerivedRevisionID,
	second *domain.DerivedRevisionID,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func (state *goldenPlanGroupNormalization) claimTopology(group GroupAuthority) error {
	from, to := group.Revision.Positions()
	seedIndex := goldenPlanGroupSeedIndex(group.Revision, state.seeds)
	if from <= state.lastPosition || seedIndex < 0 {
		return goldenExactPlanError("group range or seed is not current")
	}
	if _, duplicate := state.matchedSeeds[seedIndex]; duplicate {
		return goldenExactPlanError("current Golden topology seed is reused")
	}
	if _, duplicate := state.seenGroups[group.Revision.GroupID()]; duplicate {
		return goldenExactPlanError("duplicate active group")
	}
	if _, duplicate := state.seenRevisions[group.Revision.RevisionID()]; duplicate {
		return goldenExactPlanError("duplicate active group revision")
	}
	state.matchedSeeds[seedIndex] = struct{}{}
	state.seenGroups[group.Revision.GroupID()] = struct{}{}
	state.seenRevisions[group.Revision.RevisionID()] = struct{}{}
	state.lastPosition = to
	return nil
}

func goldenPlanGroupSeedIndex(revision GroupRevision, seeds []TieGroupSeed) int {
	from, to := revision.Positions()
	members := revision.Members()
	for index, seed := range seeds {
		if from == seed.PositionFrom && to == seed.PositionTo && reflect.DeepEqual(members, seed.Members) {
			return index
		}
	}
	return -1
}

func (state *goldenPlanGroupNormalization) collectActiveParticipants(group GroupAuthority) error {
	members := group.Revision.Members()
	memberSet := make(map[uuid.UUID]struct{}, len(members))
	for _, member := range members {
		memberSet[member.ParticipantID] = struct{}{}
	}
	if len(group.ActiveParticipantIDs) < 2 {
		return goldenExactPlanError("active group has fewer than two members")
	}
	for index, participantID := range group.ActiveParticipantIDs {
		if participantID == uuid.Nil || (index > 0 && participantID == group.ActiveParticipantIDs[index-1]) {
			return goldenExactPlanError("active membership is duplicate")
		}
		if _, belongs := memberSet[participantID]; !belongs {
			return goldenExactPlanError("active membership contains a foreign participant")
		}
		if _, duplicate := state.seenParticipants[participantID]; duplicate {
			return goldenExactPlanError("participant belongs to multiple active groups")
		}
		state.seenParticipants[participantID] = struct{}{}
		state.participants = append(state.participants, participantID)
	}
	return nil
}

func normalizeGoldenExactCandidates(pool domain.TaskPoolRevision, input []TaskVersion) ([]TaskVersion, error) {
	result := cloneGoldenCandidates(input)
	sort.Slice(result, func(i, j int) bool {
		return domain.CompareTaskVersionRefs(
			domain.TaskVersionRef{TaskID: result[i].Task.ID, Version: result[i].Version},
			domain.TaskVersionRef{TaskID: result[j].Task.ID, Version: result[j].Version},
		) < 0
	})
	if len(result) != len(pool.Versions) {
		return nil, goldenExactPlanError("candidate inventory does not cover the Golden pool")
	}
	for index, candidate := range result {
		candidate.Health.InternalHealthDetail = ""
		result[index].Health.InternalHealthDetail = ""
		ref := domain.TaskVersionRef{TaskID: candidate.Task.ID, Version: candidate.Version}
		if ref != pool.Versions[index] || candidate.PoolRevisionID != pool.ID || candidate.Version < 1 ||
			!validGoldenTaskContent(candidate.Task) || candidate.Health.TaskID != candidate.Task.ID ||
			candidate.Health.Version != candidate.Version || candidate.Health.PoolRevisionID != pool.ID ||
			candidate.Health.PoolKind != domain.AssignmentTaskKindGolden ||
			candidate.ArtifactDigest == [sha256.Size]byte{} ||
			candidate.ArtifactDigest != TaskArtifactDigest(candidate.Task, candidate.Version) {
			return nil, goldenExactPlanError("candidate pool, health, or artifact evidence changed")
		}
	}
	return result, nil
}

func normalizeGoldenHistory(participants []uuid.UUID, input []assignmentusecase.TaskReceiptRef) ([]assignmentusecase.TaskReceiptRef, error) {
	participantSet := make(map[uuid.UUID]struct{}, len(participants))
	for _, participantID := range participants {
		participantSet[participantID] = struct{}{}
	}
	result := append([]assignmentusecase.TaskReceiptRef(nil), input...)
	sort.Slice(result, func(i, j int) bool {
		if comparison := bytes.Compare(result[i].ParticipantID[:], result[j].ParticipantID[:]); comparison != 0 {
			return comparison < 0
		}
		if comparison := bytes.Compare(result[i].TaskID[:], result[j].TaskID[:]); comparison != 0 {
			return comparison < 0
		}
		return result[i].Version < result[j].Version
	})
	for index, receipt := range result {
		if receipt.TaskID == uuid.Nil || receipt.Version < 1 {
			return nil, goldenExactPlanError("invalid task history evidence")
		}
		if _, belongs := participantSet[receipt.ParticipantID]; !belongs {
			return nil, goldenExactPlanError("task history contains a foreign participant")
		}
		if index > 0 && result[index-1].ParticipantID == receipt.ParticipantID &&
			result[index-1].TaskID == receipt.TaskID {
			return nil, goldenExactPlanError("duplicate task history evidence")
		}
	}
	return result, nil
}

func normalizeGoldenParticipantReservations(
	scope Scope,
	participants []uuid.UUID,
	input []ParticipantReservation,
) ([]ParticipantReservation, error) {
	result := append([]ParticipantReservation(nil), input...)
	sort.Slice(result, func(i, j int) bool {
		return bytes.Compare(result[i].ParticipantID[:], result[j].ParticipantID[:]) < 0
	})
	if len(result) != len(participants) {
		return nil, goldenExactPlanError("participant reservations do not cover active membership")
	}
	seenReservations := make(map[uuid.UUID]struct{}, len(result))
	seenPlayers := make(map[uuid.UUID]struct{}, len(result))
	for index, item := range result {
		if item.ParticipantID != participants[index] || item.PlayerID == uuid.Nil ||
			item.Reservation.PlayerID != item.PlayerID || !item.Reservation.IsValid() ||
			item.Reservation.TournamentID != scope.TournamentID {
			return nil, goldenExactPlanError("invalid participant reservation authority")
		}
		if _, duplicate := seenReservations[item.Reservation.ReservationID]; duplicate {
			return nil, goldenExactPlanError("participant reservation identity is reused")
		}
		if _, duplicate := seenPlayers[item.PlayerID]; duplicate {
			return nil, goldenExactPlanError("participant player identity is reused")
		}
		seenReservations[item.Reservation.ReservationID] = struct{}{}
		seenPlayers[item.PlayerID] = struct{}{}
	}
	return result, nil
}

func normalizeGoldenTaskReservations(pool domain.TaskPoolRevision, input []TaskReservation) ([]TaskReservation, error) {
	result := append([]TaskReservation(nil), input...)
	sort.Slice(result, func(i, j int) bool {
		return domain.CompareTaskVersionRefs(result[i].TaskVersion, result[j].TaskVersion) < 0
	})
	seenIDs := make(map[uuid.UUID]struct{}, len(result))
	ownerRevisions := make(map[uuid.UUID]uuid.UUID, len(result))
	for index, reservation := range result {
		if !goldenPoolContainsTaskVersion(pool, reservation.TaskVersion.TaskID, reservation.TaskVersion.Version) ||
			reservation.ReservationID == uuid.Nil || reservation.PlanID == uuid.Nil || reservation.PlanRevisionID == uuid.Nil ||
			reservation.ReservationID == reservation.PlanID || reservation.ReservationID == reservation.PlanRevisionID ||
			reservation.PlanID == reservation.PlanRevisionID {
			return nil, goldenExactPlanError("invalid existing task reservation")
		}
		if index > 0 && result[index-1].TaskVersion == reservation.TaskVersion {
			return nil, goldenExactPlanError("task version is globally reserved twice")
		}
		if _, duplicate := seenIDs[reservation.ReservationID]; duplicate {
			return nil, goldenExactPlanError("task reservation identity is reused")
		}
		if revisionID, exists := ownerRevisions[reservation.PlanID]; exists && revisionID != reservation.PlanRevisionID {
			return nil, goldenExactPlanError("task reservations disagree on owner revision")
		}
		seenIDs[reservation.ReservationID] = struct{}{}
		ownerRevisions[reservation.PlanID] = reservation.PlanRevisionID
	}
	return result, nil
}

func goldenPoolContainsTaskVersion(pool domain.TaskPoolRevision, taskID uuid.UUID, version int) bool {
	return slices.Contains(pool.Versions, domain.TaskVersionRef{TaskID: taskID, Version: version})
}

func validGoldenTaskContent(task domain.Task) bool {
	return domain.IsValidTaskTitle(task.Title) &&
		domain.IsValidTaskDescription(task.Description) &&
		task.Category.IsValid() && task.Difficulty.IsValid() &&
		domain.IsValidTaskTimeLimit(task.TimeLimit) &&
		domain.IsValidTaskFlag(task.Flag) &&
		domain.IsValidTaskHints(task.Hints) &&
		domain.IsValidTaskURLShape(task.Category, task.TaskURL, task.SourceFileURL)
}
