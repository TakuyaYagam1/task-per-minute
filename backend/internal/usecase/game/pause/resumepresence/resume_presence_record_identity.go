package resumepresence

import pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"

func pauseResumeDecisionExpectationEqual(first, second PauseResumeDecisionExpectation) bool {
	return first.PauseID == second.PauseID && first.ScopeKind == second.ScopeKind &&
		first.CurrentRevisionID == second.CurrentRevisionID && first.State == second.State && first.Revision == second.Revision &&
		first.SeriesID == second.SeriesID && first.GameID == second.GameID &&
		pauseResumeUUIDPointerEqual(first.ParentPauseID, second.ParentPauseID) && first.Depth == second.Depth &&
		first.DecisionNumber == second.DecisionNumber && first.StartedAt.Equal(second.StartedAt) &&
		pauseResumeGameClockPointerEqual(first.GameClock, second.GameClock)
}

func pauseResumeGameClockPointerEqual(first, second *pausedomain.PauseResumeGameClock) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return pauseResumeGameClockEqual(*first, *second)
}

func pauseResumeGameClockEqual(first, second pausedomain.PauseResumeGameClock) bool {
	return first.PauseID == second.PauseID && first.GameID == second.GameID &&
		first.OriginalDeadline.Equal(second.OriginalDeadline) && first.FrozenAt.Equal(second.FrozenAt) &&
		first.Remaining == second.Remaining && pauseResumeTimePointerEqual(first.ResumedAt, second.ResumedAt) &&
		pauseResumeTimePointerEqual(first.ResumedDeadline, second.ResumedDeadline) && first.Revision == second.Revision
}

func pauseResumePresenceCommandEqual(first, second PauseResumePresenceCommand) bool {
	return pauseResumePresenceResumeCommandEqual(first.Resume, second.Resume) &&
		pauseResumePresenceDecisionCommandEqual(first, second) && pauseResumePresenceInputCommandEqual(first, second)
}

func pauseResumePresenceResumeCommandEqual(first, second PauseResumeCommand) bool {
	return first.Scope == second.Scope && first.PauseID == second.PauseID &&
		first.CommandID == second.CommandID && first.ActorID == second.ActorID &&
		first.DraftResultRevisionID == second.DraftResultRevisionID &&
		pauseResumeExpectationEqual(first.Expected, second.Expected)
}

func pauseResumePresenceDecisionCommandEqual(first, second PauseResumePresenceCommand) bool {
	return first.SeriesDecisionID == second.SeriesDecisionID && first.GameDecisionID == second.GameDecisionID &&
		pauseResumeDecisionExpectationEqual(first.SeriesExpected, second.SeriesExpected) &&
		pauseResumeDecisionExpectationEqual(first.GameExpected, second.GameExpected)
}

func pauseResumePresenceInputCommandEqual(first, second PauseResumePresenceCommand) bool {
	return pauseResumePresenceSetEqual(first.Presence, second.Presence) &&
		pauseResumeReconnectSetEqual(first.Reconnect, second.Reconnect) && pauseResumeCounterSetEqual(first.Counters, second.Counters) &&
		pauseResumeFrozenBaselineEqual(first.FrozenDeadlines, second.FrozenDeadlines) &&
		pauseResumeIntervalInputEqual(first.FirstInterval, second.FirstInterval) &&
		pauseResumeIntervalInputEqual(first.SecondInterval, second.SecondInterval)
}

func pauseResumeIntervalInputEqual(first, second *PauseResumeIntervalInput) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func pauseResumeDecisionRecordEqual(first, second PauseResumeDecisionRecord) bool {
	return first.ID == second.ID && first.PauseID == second.PauseID && first.DecisionNumber == second.DecisionNumber &&
		first.Action == second.Action && pauseResumeUUIDPointerEqual(first.FirstReconnectIntervalID, second.FirstReconnectIntervalID) &&
		pauseResumeUUIDPointerEqual(first.SecondReconnectIntervalID, second.SecondReconnectIntervalID) && first.DecidedAt.Equal(second.DecidedAt)
}

func pauseResumeDecisionRecordPointerEqual(first, second *PauseResumeDecisionRecord) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return pauseResumeDecisionRecordEqual(*first, *second)
}

func pauseResumeParticipantResolutionEqual(first, second PauseResumeParticipantResolution) bool {
	return first.ParticipantID == second.ParticipantID && first.PresenceEpoch == second.PresenceEpoch &&
		first.Disposition == second.Disposition && first.Counter == second.Counter &&
		pauseResumeReconnectPointerEqual(first.SourceInterval, second.SourceInterval) &&
		pauseResumeReconnectPointerEqual(first.CurrentInterval, second.CurrentInterval)
}

func pauseResumeReconnectPointerEqual(first, second *pausedomain.PauseReconnectInterval) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return pauseResumeReconnectEqual(*first, *second)
}

func pauseResumePresenceRecordEqual(first, second PauseResumePresenceRecord) bool {
	return pauseResumePresenceCommandEqual(first.Command, second.Command) &&
		pauseResumeDecisionRecordEqual(first.GameDecision, second.GameDecision) &&
		pauseResumeDecisionRecordPointerEqual(first.SeriesDecision, second.SeriesDecision) &&
		pauseResumeGameClockEqual(first.GameClock, second.GameClock) && first.GamePauseState == second.GamePauseState &&
		first.SeriesPauseState == second.SeriesPauseState && first.NormalPauseState == second.NormalPauseState &&
		pauseResumeTimePointerEqual(first.NormalPauseResolvedAt, second.NormalPauseResolvedAt) &&
		pauseResumeGraphEvidenceEqual(first.Graph, second.Graph) &&
		pauseResumeParticipantResolutionEqual(first.First, second.First) &&
		pauseResumeParticipantResolutionEqual(first.Second, second.Second) && first.DecidedAt.Equal(second.DecidedAt)
}
