package cutoff

import resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"

func EvaluateCutoff(input CutoffInput) (Cutoff, error) {
	if err := preflightCorrectionDAGResults(input.DAG); err != nil {
		return Cutoff{}, err
	}
	snapshot := input.DAG.Snapshot()
	return evaluateCorrectionCutoffSnapshot(input, snapshot)
}

// EvaluatePrepared evaluates a caller-owned DAG snapshot after its result
// bounds have already been checked. It is used by the parent correction
// orchestration facade to keep validation and cutoff evaluation atomic.
func EvaluatePrepared(input CutoffInput, snapshot resultprojection.RevisionDAGSnapshot) (Cutoff, error) {
	return evaluateCorrectionCutoffPrepared(input, snapshot)
}

func evaluateCorrectionCutoffSnapshot(
	input CutoffInput,
	snapshot resultprojection.RevisionDAGSnapshot,
) (Cutoff, error) {
	if err := preflightCorrectionCutoff(input, snapshot); err != nil {
		return Cutoff{}, err
	}
	return evaluateCorrectionCutoffPrepared(input, snapshot)
}

func evaluateCorrectionCutoffPrepared(
	input CutoffInput,
	snapshot resultprojection.RevisionDAGSnapshot,
) (Cutoff, error) {
	index, err := newCorrectionCutoffIndex(snapshot, input)
	if err != nil {
		return Cutoff{}, err
	}
	descendants, affected, err := index.orderedDescendants(input.TargetRevisionID)
	if err != nil {
		return Cutoff{}, err
	}
	if input.TournamentState.IsTerminal() {
		return Cutoff{}, rejectCorrection(
			RejectionCutoff, ErrCutoff, "tournament is terminal",
		)
	}
	for _, event := range input.Events {
		if _, blocked := affected[event.SourceRevisionID]; blocked {
			return Cutoff{}, rejectCorrection(
				RejectionCutoff, ErrCutoff, "irreversible event exists",
			)
		}
	}
	return Cutoff{
		tournamentID:     input.TournamentID,
		targetRevisionID: input.TargetRevisionID,
		descendants:      descendants,
	}, nil
}
