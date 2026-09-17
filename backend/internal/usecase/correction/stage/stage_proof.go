package stage

import (
	"crypto/sha256"
	"encoding/json"
	"sort"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const correctionStageProofSchema = "tournament-correction-stage-v1"

func sealCorrectionStageResult(correction Plan, result StageResult) (StageResult, error) {
	proof, err := marshalCorrectionStageProof(correction, result)
	if err != nil {
		return StageResult{}, invalidCorrectionStage("marshal server-owned stage proof", err)
	}
	result.Proof = proof
	result.ProofDigest = sha256.Sum256(proof)
	return result, nil
}

func marshalCorrectionStageProof(correction Plan, result StageResult) ([]byte, error) {
	type member struct {
		ParticipantID string `json:"participant_id"`
	}
	type group struct {
		ID           string   `json:"id"`
		RevisionID   string   `json:"revision_id"`
		SourceID     string   `json:"source_projection_revision_id"`
		PositionFrom int      `json:"position_from"`
		PositionTo   int      `json:"position_to"`
		Members      []member `json:"members"`
	}
	type supersession struct {
		GroupID       string  `json:"group_id"`
		PreviousID    string  `json:"previous_revision_id"`
		SuccessorID   string  `json:"successor_revision_id"`
		ReplacementID *string `json:"replacement_group_id,omitempty"`
	}
	type cancelledAttempt struct {
		ID              string `json:"id"`
		GroupID         string `json:"group_id"`
		GroupRevisionID string `json:"group_revision_id"`
		AttemptNo       int    `json:"attempt_no"`
		State           string `json:"state"`
	}
	document := struct {
		Schema            string             `json:"schema"`
		CorrectionCommand string             `json:"correction_command_id"`
		Transition        StageTransition    `json:"transition"`
		Mode              StageMode          `json:"mode"`
		WithdrawPlayoff   bool               `json:"withdraw_playoff"`
		CreatePlayoff     bool               `json:"create_playoff"`
		Groups            []group            `json:"groups"`
		Supersessions     []supersession     `json:"supersessions"`
		CancelledAttempts []cancelledAttempt `json:"cancelled_attempts"`
	}{
		Schema: correctionStageProofSchema, CorrectionCommand: correction.Audit().CommandID.String(),
		Transition: result.Transition, Mode: result.Corrected.Mode,
		WithdrawPlayoff: result.WithdrawPlayoff, CreatePlayoff: result.CreatePlayoff,
	}
	groups := append([]domain.GoldenGroupState(nil), result.Corrected.GoldenGroups...)
	sort.Slice(groups, func(first, second int) bool {
		if groups[first].PositionFrom != groups[second].PositionFrom {
			return groups[first].PositionFrom < groups[second].PositionFrom
		}
		return groups[first].ID.String() < groups[second].ID.String()
	})
	for _, state := range groups {
		entry := group{ID: state.ID.String(), RevisionID: state.RevisionID.UUID().String(),
			SourceID: state.SourceProjectionRevisionID.UUID().String(), PositionFrom: state.PositionFrom,
			PositionTo: state.PositionTo, Members: make([]member, len(state.Members))}
		for index, value := range state.Members {
			entry.Members[index] = member{ParticipantID: value.ParticipantID.String()}
		}
		document.Groups = append(document.Groups, entry)
	}
	supersessions := append([]StageGroupSupersession(nil), result.GroupSupersessions...)
	sort.Slice(supersessions, func(first, second int) bool {
		return supersessions[first].GroupID.String() < supersessions[second].GroupID.String()
	})
	for _, value := range supersessions {
		entry := supersession{GroupID: value.GroupID.String(), PreviousID: value.PreviousRevisionID.UUID().String(),
			SuccessorID: value.RevisionID.UUID().String()}
		if value.ReplacementGroupID != nil {
			clone := value.ReplacementGroupID.String()
			entry.ReplacementID = &clone
		}
		document.Supersessions = append(document.Supersessions, entry)
	}
	attempts := append([]domain.GoldenAttempt(nil), result.CancelledAttempts...)
	sort.Slice(attempts, func(first, second int) bool { return attempts[first].ID.String() < attempts[second].ID.String() })
	for _, attempt := range attempts {
		document.CancelledAttempts = append(document.CancelledAttempts, cancelledAttempt{
			ID: attempt.ID.String(), GroupID: attempt.GroupID.String(), GroupRevisionID: attempt.GroupRevisionID.UUID().String(),
			AttemptNo: attempt.AttemptNo, State: string(attempt.State),
		})
	}
	return json.Marshal(document)
}

func correctionStageProofMatches(correction Plan, result StageResult) bool {
	if len(result.Proof) == 0 || result.ProofDigest == ([sha256.Size]byte{}) {
		return false
	}
	clone := result
	clone.Proof = nil
	clone.ProofDigest = [sha256.Size]byte{}
	proof, err := marshalCorrectionStageProof(correction, clone)
	return err == nil && string(proof) == string(result.Proof) && sha256.Sum256(proof) == result.ProofDigest
}
