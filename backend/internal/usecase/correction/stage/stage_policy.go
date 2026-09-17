package stage

import (
	"bytes"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func planCorrectionStageRollback(
	snapshot StageSnapshot,
	command StageCommand,
	changedAt time.Time,
) (StageResult, error) {
	correctedSource, err := correctionStageStandingsRevision(command.Correction)
	if err != nil {
		return StageResult{}, err
	}
	if err := validateCorrectionStageLayout(
		snapshot.TournamentID,
		snapshot.Layout,
		false,
		domain.DerivedRevisionID{},
	); err != nil {
		return StageResult{}, err
	}
	requiresFreshGoldenGroups := command.Corrected.Mode == StageModeGolden &&
		!correctionStageTopologyEqual(snapshot.Layout.GoldenGroups, command.Corrected.GoldenGroups)
	if err := validateCorrectionStageLayout(
		snapshot.TournamentID,
		command.Corrected,
		requiresFreshGoldenGroups,
		correctedSource,
	); err != nil {
		return StageResult{}, err
	}

	affected := correctionStageAffectedGroups(snapshot.Layout.GoldenGroups, command.Corrected.GoldenGroups)
	intents, err := correctionStageSupersessionIntents(command.GroupSupersessions, affected)
	if err != nil {
		return StageResult{}, err
	}
	if err := validateCorrectionStageFreshIdentities(
		snapshot.Layout.GoldenGroups,
		command.Corrected.GoldenGroups,
		intents,
	); err != nil {
		return StageResult{}, err
	}

	result := StageResult{
		Transition:      correctionStageTransition(snapshot.Layout, command.Corrected, len(affected) > 0),
		Corrected:       cloneCorrectionStageLayout(command.Corrected),
		WithdrawPlayoff: snapshot.Layout.Mode == StageModePlayoff && command.Corrected.Mode == StageModeGolden,
		CreatePlayoff:   snapshot.Layout.Mode == StageModeGolden && command.Corrected.Mode == StageModePlayoff,
	}
	for _, group := range affected {
		intent := intents[group.ID]
		replacementID := correctionStageReplacementGroup(group, command.Corrected.GoldenGroups)
		supersession := StageGroupSupersession{
			GroupID: group.ID, PreviousRevisionID: group.RevisionID,
			RevisionID: intent.RevisionID, ReplacementGroupID: replacementID,
			Previous: cloneCorrectionStageGoldenGroup(group),
		}
		result.GroupSupersessions = append(result.GroupSupersessions, supersession)
		attempt, found := correctionStageUnstartedAttempt(group)
		if !found {
			if len(group.Attempts) != 0 {
				return StageResult{}, invalidCorrectionStage(
					"Golden Attempt already has execution evidence", nil,
				)
			}
			continue
		}
		if !correctionStageHasPause(snapshot.Layout.Paused, group, attempt) {
			return StageResult{}, invalidCorrectionStage(
				"Golden Attempt is not in an eligible retained technical pause", nil,
			)
		}
		attempt.GroupRevisionID = intent.RevisionID
		attempt.State = domain.GoldenAttemptStateCancelled
		attempt.FinishedAt = cloneTimePointer(&changedAt)
		if err := attempt.Validate(); err != nil {
			return StageResult{}, invalidCorrectionStage("cancel retained Golden Attempt", err)
		}
		result.CancelledAttempts = append(result.CancelledAttempts, attempt)
	}
	return sealCorrectionStageResult(command.Correction, result)
}

func validateCorrectionStageLayout(
	tournamentID uuid.UUID,
	layout StageLayout,
	corrected bool,
	correctedSource domain.DerivedRevisionID,
) error {
	if tournamentID == uuid.Nil || (layout.Mode != StageModePlayoff && layout.Mode != StageModeGolden) {
		return invalidCorrectionStage("invalid stage layout", nil)
	}
	if layout.Mode == StageModePlayoff {
		if len(layout.GoldenGroups) != 0 || len(layout.Paused) != 0 {
			return invalidCorrectionStage("playoff layout contains Golden execution", nil)
		}
		return nil
	}
	if len(layout.GoldenGroups) == 0 {
		return invalidCorrectionStage("Golden layout has no tie groups", nil)
	}
	return validateCorrectionStageGoldenGroups(tournamentID, layout, corrected, correctedSource)
}

func validateCorrectionStageGoldenGroups(
	tournamentID uuid.UUID,
	layout StageLayout,
	corrected bool,
	correctedSource domain.DerivedRevisionID,
) error {
	seenGroups := make(map[uuid.UUID]struct{}, len(layout.GoldenGroups))
	seenRevisions := make(map[domain.DerivedRevisionID]struct{}, len(layout.GoldenGroups))
	for _, group := range layout.GoldenGroups {
		if err := validateCorrectionStageGoldenGroup(
			tournamentID, group, corrected, correctedSource, seenGroups, seenRevisions,
		); err != nil {
			return err
		}
		seenGroups[group.ID] = struct{}{}
		seenRevisions[group.RevisionID] = struct{}{}
	}
	if corrected && len(layout.Paused) != 0 {
		return invalidCorrectionStage("corrected layout retained a technical pause", nil)
	}
	return nil
}

func validateCorrectionStageGoldenGroup(
	tournamentID uuid.UUID,
	group domain.GoldenGroupState,
	corrected bool,
	correctedSource domain.DerivedRevisionID,
	seenGroups map[uuid.UUID]struct{},
	seenRevisions map[domain.DerivedRevisionID]struct{},
) error {
	if group.TournamentID != tournamentID {
		return invalidCorrectionStage("Golden group belongs to another Tournament", nil)
	}
	if _, err := domain.NewGoldenGroup(group); err != nil {
		return invalidCorrectionStage("invalid Golden group", err)
	}
	if _, duplicate := seenGroups[group.ID]; duplicate {
		return invalidCorrectionStage("duplicate Golden group", nil)
	}
	if _, duplicate := seenRevisions[group.RevisionID]; duplicate {
		return invalidCorrectionStage("duplicate Golden group revision", nil)
	}
	if corrected && (group.SourceProjectionRevisionID != correctedSource ||
		group.ParticipationEstablished || len(group.Attempts) != 0) {
		return invalidCorrectionStage("corrected Golden group is not a fresh seed", nil)
	}
	return nil
}

func correctionStageAffectedGroups(
	current []domain.GoldenGroupState,
	corrected []domain.GoldenGroupState,
) []domain.GoldenGroupState {
	affected := make([]domain.GoldenGroupState, 0)
	for _, group := range current {
		retained := false
		for _, candidate := range corrected {
			if correctionStageGroupTopologyEqual(group, candidate) {
				retained = true
				break
			}
		}
		if !retained {
			affected = append(affected, cloneCorrectionStageGoldenGroup(group))
		}
	}
	return affected
}

func correctionStageSupersessionIntents(
	input []StageGroupSupersessionIntent,
	affected []domain.GoldenGroupState,
) (map[uuid.UUID]StageGroupSupersessionIntent, error) {
	if len(input) != len(affected) {
		return nil, invalidCorrectionStage("Golden supersession coverage is incomplete", nil)
	}
	affectedIDs := make(map[uuid.UUID]struct{}, len(affected))
	for _, group := range affected {
		affectedIDs[group.ID] = struct{}{}
	}
	intents := make(map[uuid.UUID]StageGroupSupersessionIntent, len(input))
	for _, intent := range input {
		if intent.GroupID == uuid.Nil || intent.RevisionID.IsZero() {
			return nil, invalidCorrectionStage("invalid Golden supersession identity", nil)
		}
		if _, exists := affectedIDs[intent.GroupID]; !exists {
			return nil, invalidCorrectionStage("supersession targets an unaffected Golden group", nil)
		}
		if _, duplicate := intents[intent.GroupID]; duplicate {
			return nil, invalidCorrectionStage("duplicate Golden supersession", nil)
		}
		intents[intent.GroupID] = intent
	}
	return intents, nil
}

func validateCorrectionStageFreshIdentities(
	current []domain.GoldenGroupState,
	corrected []domain.GoldenGroupState,
	intents map[uuid.UUID]StageGroupSupersessionIntent,
) error {
	reserved := make(map[uuid.UUID]struct{})
	for _, group := range current {
		reserved[group.ID] = struct{}{}
		reserved[group.RevisionID.UUID()] = struct{}{}
		for _, attempt := range group.Attempts {
			reserved[attempt.ID] = struct{}{}
		}
	}
	for _, group := range corrected {
		if correctionStageMatchesAnyTopology(group, current) {
			continue
		}
		for _, id := range []uuid.UUID{group.ID, group.RevisionID.UUID()} {
			if _, reused := reserved[id]; reused {
				return invalidCorrectionStage("replacement Golden group reused an identity", nil)
			}
			reserved[id] = struct{}{}
		}
	}
	for _, intent := range intents {
		if _, reused := reserved[intent.RevisionID.UUID()]; reused {
			return invalidCorrectionStage("superseded Golden revision reused an identity", nil)
		}
		reserved[intent.RevisionID.UUID()] = struct{}{}
	}
	return nil
}

func correctionStageTransition(
	current StageLayout,
	corrected StageLayout,
	groupsChanged bool,
) StageTransition {
	switch {
	case current.Mode == StageModePlayoff && corrected.Mode == StageModeGolden:
		return StagePlayoffToGolden
	case current.Mode == StageModeGolden && corrected.Mode == StageModePlayoff:
		return StageGoldenToPlayoff
	case groupsChanged:
		return StageGoldenGroupsChanged
	default:
		return StageUnchanged
	}
}

func correctionStageStandingsRevision(plan Plan) (domain.DerivedRevisionID, error) {
	var current domain.DerivedRevision
	for _, projection := range plan.ProjectionRevisions() {
		revision := projection.Revision()
		if revision.Artifact().Kind == domain.ArtifactKindStandings && revision.RevisionNo() > current.RevisionNo() {
			current = revision
		}
	}
	if current.ID().IsZero() {
		return domain.DerivedRevisionID{}, invalidCorrectionStage("correction has no standings successor", nil)
	}
	return current.ID(), nil
}

func correctionStageUnstartedAttempt(
	group domain.GoldenGroupState,
) (domain.GoldenAttempt, bool) {
	if len(group.Attempts) == 0 {
		return domain.GoldenAttempt{}, false
	}
	attempt := cloneCorrectionStageGoldenAttempt(group.Attempts[len(group.Attempts)-1])
	if attempt.State != domain.GoldenAttemptStatePlanned &&
		attempt.State != domain.GoldenAttemptStateWaitingReady {
		return domain.GoldenAttempt{}, false
	}
	return attempt, true
}

func correctionStageHasPause(
	paused []StagePauseExpectation,
	group domain.GoldenGroupState,
	attempt domain.GoldenAttempt,
) bool {
	for _, expectation := range paused {
		if expectation.TournamentID == group.TournamentID &&
			expectation.GroupID == group.ID &&
			expectation.GroupRevisionID == group.RevisionID &&
			expectation.SessionID != uuid.Nil && expectation.RevisionID != uuid.Nil &&
			expectation.Revision > 0 && expectation.State == StagePauseStatePaused &&
			expectation.PayloadDigest != ([sha256.Size]byte{}) && attempt.StartedAt == nil &&
			attempt.FinishedAt == nil && attempt.RetainedAt != nil {
			return true
		}
	}
	return false
}

func correctionStageReplacementGroup(
	previous domain.GoldenGroupState,
	corrected []domain.GoldenGroupState,
) *uuid.UUID {
	for _, group := range corrected {
		if correctionStageGroupsOverlap(previous, group) {
			id := group.ID
			return &id
		}
	}
	return nil
}

func correctionStageGroupsOverlap(first, second domain.GoldenGroupState) bool {
	if first.PositionFrom <= second.PositionTo && second.PositionFrom <= first.PositionTo {
		return true
	}
	for _, member := range first.Members {
		for _, candidate := range second.Members {
			if member.ParticipantID == candidate.ParticipantID {
				return true
			}
		}
	}
	return false
}

func correctionStageMatchesAnyTopology(
	group domain.GoldenGroupState,
	groups []domain.GoldenGroupState,
) bool {
	for _, candidate := range groups {
		if correctionStageGroupTopologyEqual(group, candidate) {
			return true
		}
	}
	return false
}

func correctionStageGroupTopologyEqual(first, second domain.GoldenGroupState) bool {
	if first.SourceProjectionRevisionID != second.SourceProjectionRevisionID ||
		first.PositionFrom != second.PositionFrom || first.PositionTo != second.PositionTo ||
		len(first.Members) != len(second.Members) {
		return false
	}
	firstIDs := correctionStageMemberIDs(first.Members)
	secondIDs := correctionStageMemberIDs(second.Members)
	for index := range firstIDs {
		if firstIDs[index] != secondIDs[index] {
			return false
		}
	}
	return true
}

func correctionStageMemberIDs(members []domain.GoldenMember) []uuid.UUID {
	ids := make([]uuid.UUID, len(members))
	for index, member := range members {
		ids[index] = member.ParticipantID
	}
	for left := 0; left < len(ids); left++ {
		for right := left + 1; right < len(ids); right++ {
			if bytes.Compare(ids[right][:], ids[left][:]) < 0 {
				ids[left], ids[right] = ids[right], ids[left]
			}
		}
	}
	return ids
}
