package golden

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func RecoverGoldenGroup(input GoldenRecoveryInput) (GoldenRecoveryResult, error) {
	if err := validateGoldenRecoveryGroup(input.Group); err != nil {
		return GoldenRecoveryResult{}, err
	}
	liveIndex, err := goldenRecoveryLiveIndex(input.Attempts)
	if err != nil {
		return GoldenRecoveryResult{}, err
	}

	memberIDs := make(map[uuid.UUID]struct{}, len(input.Group.ParticipantIDs))
	for _, participantID := range input.Group.ParticipantIDs {
		memberIDs[participantID] = struct{}{}
	}
	excluded := make(map[uuid.UUID]struct{})
	attempts := make([]domain.GoldenAttempt, len(input.Attempts))
	for i := range input.Attempts {
		if err := validateGoldenRecoveryAttempt(input.Attempts[i], input.Group, memberIDs, excluded); err != nil {
			return GoldenRecoveryResult{}, err
		}
		attempts[i] = cloneGoldenRecoveryAttempt(input.Attempts[i].Attempt)
	}

	groupState := domain.GoldenGroupState{
		ID: input.Group.ID, TournamentID: input.Group.TournamentID,
		RevisionID:                 input.Group.RevisionID,
		SourceProjectionRevisionID: input.Group.SourceProjectionRevisionID,
		PositionFrom:               input.Group.PositionFrom, PositionTo: input.Group.PositionTo,
		ParticipationEstablished: goldenRecoveryParticipationEstablished(input.Attempts),
		Members:                  make([]domain.GoldenMember, len(input.Group.ParticipantIDs)),
		Attempts:                 attempts,
	}
	for i, participantID := range input.Group.ParticipantIDs {
		_, isExcluded := excluded[participantID]
		groupState.Members[i] = domain.GoldenMember{ParticipantID: participantID, Excluded: isExcluded}
	}
	group, err := domain.NewGoldenGroup(groupState)
	if err != nil {
		return GoldenRecoveryResult{}, goldenRecoveryError("reconstruct group", err)
	}

	result := GoldenRecoveryResult{
		Group:                      group,
		RetainedPreStartAttemptIDs: goldenRecoveryRetainedAttempts(input.Attempts),
		CommittedPriorAttempts:     goldenRecoveryPriorCommits(input.Attempts, liveIndex),
		PermanentExclusions:        goldenRecoverySortedIDs(excluded),
	}
	if liveIndex >= 0 {
		result.LiveAttempt = buildGoldenRecoveryLiveAttempt(input.Attempts[liveIndex])
	}
	return result, nil
}
