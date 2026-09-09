package correction

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func cloneCorrectionStageCommand(command StageCommand) StageCommand {
	clone := command
	clone.Corrected = cloneCorrectionStageLayout(command.Corrected)
	clone.GroupSupersessions = append(
		[]StageGroupSupersessionIntent(nil),
		command.GroupSupersessions...,
	)
	return clone
}

func cloneCorrectionStageSnapshot(snapshot StageSnapshot) StageSnapshot {
	clone := snapshot
	clone.CutoffEvents = append([]CutoffEvent(nil), snapshot.CutoffEvents...)
	clone.Layout = cloneCorrectionStageLayout(snapshot.Layout)
	clone.Swiss = cloneCorrectionStageSwissAuthority(snapshot.Swiss)
	return clone
}

func cloneCorrectionStageSwissAuthority(value StageSwissAuthority) StageSwissAuthority {
	clone := StageSwissAuthority{Complete: value.Complete}
	clone.Participants = append([]resultprojection.CanonicalSwissParticipant(nil), value.Participants...)
	clone.Ledger = make([]resultprojection.CanonicalSwissPointLedgerEntry, len(value.Ledger))
	for index, entry := range value.Ledger {
		clone.Ledger[index] = cloneCorrectionCanonicalSwissLedgerEntry(entry)
	}
	return clone
}

func cloneCorrectionCanonicalSwissLedgerEntry(
	entry resultprojection.CanonicalSwissPointLedgerEntry,
) resultprojection.CanonicalSwissPointLedgerEntry {
	clone := entry
	if entry.OpponentID != nil {
		opponentID := *entry.OpponentID
		clone.OpponentID = &opponentID
	}
	if entry.AcceptedSolveTime != nil {
		accepted := *entry.AcceptedSolveTime
		clone.AcceptedSolveTime = &accepted
	}
	return clone
}

func cloneCorrectionStageLayout(layout StageLayout) StageLayout {
	clone := layout
	clone.GoldenGroups = make([]domain.GoldenGroupState, len(layout.GoldenGroups))
	for index, group := range layout.GoldenGroups {
		clone.GoldenGroups[index] = cloneCorrectionStageGoldenGroup(group)
	}
	clone.Paused = append([]StagePauseExpectation(nil), layout.Paused...)
	return clone
}

func cloneCorrectionStageGoldenGroup(group domain.GoldenGroupState) domain.GoldenGroupState {
	clone := group
	clone.Members = append([]domain.GoldenMember(nil), group.Members...)
	clone.Attempts = make([]domain.GoldenAttempt, len(group.Attempts))
	for index, attempt := range group.Attempts {
		clone.Attempts[index] = cloneCorrectionStageGoldenAttempt(attempt)
	}
	return clone
}

func cloneCorrectionStageGoldenAttempt(attempt domain.GoldenAttempt) domain.GoldenAttempt {
	clone := attempt
	clone.PreviousAttemptID = cloneCorrectionUUIDPointer(attempt.PreviousAttemptID)
	clone.ParticipantIDs = append([]uuid.UUID(nil), attempt.ParticipantIDs...)
	clone.RetainedAt = cloneTimePointer(attempt.RetainedAt)
	clone.StartedAt = cloneTimePointer(attempt.StartedAt)
	clone.FinishedAt = cloneTimePointer(attempt.FinishedAt)
	return clone
}

func cloneCorrectionStageResult(result StageResult) StageResult {
	clone := result
	clone.Proof = append([]byte(nil), result.Proof...)
	clone.Corrected = cloneCorrectionStageLayout(result.Corrected)
	clone.GroupSupersessions = make([]StageGroupSupersession, len(result.GroupSupersessions))
	for index, supersession := range result.GroupSupersessions {
		clone.GroupSupersessions[index] = supersession
		clone.GroupSupersessions[index].Previous = cloneCorrectionStageGoldenGroup(supersession.Previous)
		clone.GroupSupersessions[index].ReplacementGroupID = cloneCorrectionUUIDPointer(supersession.ReplacementGroupID)
	}
	clone.CancelledAttempts = make([]domain.GoldenAttempt, len(result.CancelledAttempts))
	for index, attempt := range result.CancelledAttempts {
		clone.CancelledAttempts[index] = cloneCorrectionStageGoldenAttempt(attempt)
	}
	return clone
}
