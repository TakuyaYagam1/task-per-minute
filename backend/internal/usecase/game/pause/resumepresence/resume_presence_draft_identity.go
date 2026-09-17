package resumepresence

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func pauseResumeDraftPointerEqual(first, second *draftusecase.Execution) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return pauseResumeDraftEqual(*first, *second)
}

func pauseResumeDraftEqual(first, second draftusecase.Execution) bool {
	return pauseResumeDraftIdentityEqual(first, second) && pauseResumeDraftStateEqual(first, second) &&
		pauseResumeDraftTimingEqual(first, second) && pauseResumeDraftCollectionsEqual(first, second)
}

func pauseResumeDraftIdentityEqual(first, second draftusecase.Execution) bool {
	return first.ID == second.ID && first.SeriesID == second.SeriesID && first.Format == second.Format &&
		first.FirstParticipantID == second.FirstParticipantID && first.SecondParticipantID == second.SecondParticipantID &&
		first.RevisionID == second.RevisionID && first.PreviousRevisionID == second.PreviousRevisionID &&
		first.Revision == second.Revision && first.CommandID == second.CommandID && first.ServiceEpoch == second.ServiceEpoch &&
		first.Turn == second.Turn
}

func pauseResumeDraftStateEqual(first, second draftusecase.Execution) bool {
	return first.State == second.State &&
		pauseResumeUUIDPointerEqual(first.CurrentActorID, second.CurrentActorID) &&
		pauseResumeComparablePointerEqual(first.CurrentAction, second.CurrentAction) &&
		pauseResumeDraftRecoveryPointerEqual(first.Recovery, second.Recovery) &&
		pauseResumeDraftTransitionPointerEqual(first.Transition, second.Transition)
}

func pauseResumeDraftTimingEqual(first, second draftusecase.Execution) bool {
	return first.TurnDeadline.Equal(second.TurnDeadline) &&
		pauseResumeTimePointerEqual(first.AbsoluteDeadline, second.AbsoluteDeadline) &&
		first.PausedRemaining == second.PausedRemaining
}

func pauseResumeDraftCollectionsEqual(first, second draftusecase.Execution) bool {
	return pauseResumeComparableSliceEqual(first.Pool, second.Pool) &&
		pauseResumeComparableSliceEqual(first.LegalCategories, second.LegalCategories) &&
		pauseResumeDraftActionSliceEqual(first.Actions, second.Actions) &&
		pauseResumeComparableSliceEqual(first.SelectedCategories, second.SelectedCategories) &&
		pauseResumeDecisionEvidenceEqual(first.FirstActorDecision, second.FirstActorDecision)
}

func pauseResumeDraftRecoveryPointerEqual(first, second *draftusecase.RecoveryEvidence) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Policy == second.Policy && first.Reason == second.Reason && first.PreviousState == second.PreviousState &&
		first.PreviousServiceEpoch == second.PreviousServiceEpoch && first.CurrentServiceEpoch == second.CurrentServiceEpoch &&
		first.PreviousDeadline.Equal(second.PreviousDeadline) && first.RecordedAt.Equal(second.RecordedAt) &&
		first.ActorID == second.ActorID && first.Note == second.Note
}

func pauseResumeDraftTransitionPointerEqual(first, second *draftusecase.TransitionEvidence) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Operation == second.Operation && first.ActorID == second.ActorID && first.Reason == second.Reason &&
		first.OccurredAt.Equal(second.OccurredAt)
}

func pauseResumeDraftActionSliceEqual(first, second []draftusecase.ActionRecord) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !pauseResumeDraftActionEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func pauseResumeDraftActionEqual(first, second draftusecase.ActionRecord) bool {
	return first.ID == second.ID && first.ResultRevisionID == second.ResultRevisionID && first.CommandID == second.CommandID &&
		first.Turn == second.Turn && first.ActorID == second.ActorID && first.Action == second.Action &&
		first.Category == second.Category && first.ScheduledDeadline.Equal(second.ScheduledDeadline) &&
		first.OccurredAt.Equal(second.OccurredAt) && first.Automatic == second.Automatic &&
		pauseResumeDecisionEvidencePointerEqual(first.DecisionEvidence, second.DecisionEvidence)
}

func pauseResumeDecisionEvidencePointerEqual(first, second *domain.DecisionEvidence) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return pauseResumeDecisionEvidenceEqual(*first, *second)
}

func pauseResumeDecisionEvidenceEqual(first, second domain.DecisionEvidence) bool {
	return first.ID == second.ID && first.Purpose == second.Purpose && first.AlgorithmVersion == second.AlgorithmVersion &&
		pauseResumeComparableSliceEqual(first.NormalizedInputs, second.NormalizedInputs) && first.Seed == second.Seed &&
		pauseResumeComparableSliceEqual(first.Result, second.Result) && first.ReplayDigest == second.ReplayDigest &&
		first.OwnerID == second.OwnerID && first.DecidedAt.Equal(second.DecidedAt)
}

func pauseResumeComparablePointerEqual[T comparable](first, second *T) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func pauseResumeComparableSliceEqual[T comparable](first, second []T) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func pauseResumeTournamentStatePointerEqual(first, second *domain.TournamentState) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func clonePauseResumeDecisionExpectation(value PauseResumeDecisionExpectation) PauseResumeDecisionExpectation {
	clone := value
	clone.ParentPauseID = pauseCloneUUIDPointer(value.ParentPauseID)
	clone.GameClock = clonePauseResumeGameClockPointer(value.GameClock)
	return clone
}

func clonePauseResumeGameClockPointer(value *pausedomain.PauseResumeGameClock) *pausedomain.PauseResumeGameClock {
	if value == nil {
		return nil
	}
	clone := clonePauseResumeGameClock(*value)
	return &clone
}

func clonePauseResumeGameClock(value pausedomain.PauseResumeGameClock) pausedomain.PauseResumeGameClock {
	clone := value
	clone.ResumedAt = pauseCloneTimePointer(value.ResumedAt)
	clone.ResumedDeadline = pauseCloneTimePointer(value.ResumedDeadline)
	return clone
}
