package cutoff

import (
	"github.com/google/uuid"

	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

const maxCorrectionCutoffEvents = 4096

const (
	maxCorrectionDAGProjections  = 512
	maxCorrectionDAGDependencies = 2048
	maxCorrectionDAGResults      = 512
	maxCorrectionDAGPayloadBytes = 512 << 10
	maxCorrectionAuthorityIDs    = 8192
	maxCorrectionNoGameAttempts  = 2048
)

func PreflightDAGResults(dag resultprojection.RevisionDAG) error {
	return preflightCorrectionDAGResults(dag)
}

func PreflightCutoff(input CutoffInput, snapshot resultprojection.RevisionDAGSnapshot) error {
	return preflightCorrectionCutoff(input, snapshot)
}

func preflightCorrectionDAGResults(dag resultprojection.RevisionDAG) error {
	inputs := dag.Inputs()
	if len(inputs) == 0 || len(inputs) > maxCorrectionDAGResults {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "correction DAG result count is out of bounds",
		)
	}
	identityCount := 0
	noGameAttempts := 0
	add := func(count int) bool {
		if count < 0 || count > maxCorrectionAuthorityIDs-identityCount {
			return false
		}
		identityCount += count
		return true
	}
	for _, input := range inputs {
		if input.NoGame == nil {
			attempts := 0
			if input.Score != nil {
				attempts = len(input.Score.Attempts)
			}
			if !add(16 + attempts*4) {
				return rejectCorrection(
					RejectionMalformed, ErrInvalid, "correction authority identity count is out of bounds",
				)
			}
			continue
		}
		attempts := len(input.NoGame.GameResults)
		if attempts > maxCorrectionNoGameAttempts-noGameAttempts || !add(24+attempts*8) {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "correction no-game evidence is out of bounds",
			)
		}
		noGameAttempts += attempts
	}
	return nil
}

func preflightCorrectionCutoff(input CutoffInput, snapshot resultprojection.RevisionDAGSnapshot) error {
	if input.TournamentID == uuid.Nil || input.TargetRevisionID.IsZero() ||
		!input.TournamentState.IsValid() || len(input.Events) > maxCorrectionCutoffEvents ||
		len(snapshot.Projections) == 0 || len(snapshot.Projections) > maxCorrectionDAGProjections ||
		len(snapshot.Dependencies) > maxCorrectionDAGDependencies {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid cutoff bounds",
		)
	}
	totalPayload := 0
	for _, projection := range snapshot.Projections {
		if !validCorrectionTime(projection.Revision().CreatedAt()) {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "cutoff projection time is out of bounds",
			)
		}
		payloadSize := len(projection.Payload())
		if payloadSize > maxCorrectionDAGPayloadBytes-totalPayload {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "cutoff graph payload is too large",
			)
		}
		totalPayload += payloadSize
	}
	return nil
}
