package game

import assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"

func operatorReservesEqual(first, second OperatorReserve) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		replayReserveExhaustionsEqual(first.Exhaustion, second.Exhaustion) &&
		first.Reserve.ProofDigest == second.Reserve.ProofDigest &&
		replaySeriesExecutionsEqual(first.Series, second.Series)
}

func cloneOperatorReserve(record OperatorReserve) OperatorReserve {
	clone := record
	clone.Exhaustion = cloneReplayReserveExhaustion(record.Exhaustion)
	clone.Reserve = assignmentusecase.CloneReserveAssignmentRecord(record.Reserve)
	clone.Series = cloneSeriesExecution(record.Series)
	return clone
}
