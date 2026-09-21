package correction

import "crypto/sha256"

func ValidCorrectionCommand(command CorrectionCommand) bool {
	return validCorrectionCommand(command)
}

// ValidCorrectionCommandWithSource validates an operator request that carries
// the immutable result revision used as its compare-and-set source.
func ValidCorrectionCommandWithSource(command CorrectionCommand) bool {
	return validCorrectionCommandWithSource(command)
}

// ValidCorrectionDraftCommand validates a non-mutating preflight request
// before the server derives the complete projection and unlock intent sets.
func ValidCorrectionDraftCommand(command CorrectionCommand) bool {
	return validCorrectionDraftCommand(command)
}

func ValidCorrectionEvidence(evidence CorrectionEvidence, command CorrectionCommand) bool {
	return validCorrectionEvidence(evidence, command)
}

func ValidCorrectionPatch(patch CorrectionPatch) bool {
	return validCorrectionPatch(patch)
}

func CorrectionRequestDigest(command CorrectionCommand) ([sha256.Size]byte, error) {
	return correctionRequestDigest(command)
}
