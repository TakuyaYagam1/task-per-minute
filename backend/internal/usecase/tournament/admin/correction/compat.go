package correction

import "crypto/sha256"

func ValidCorrectionCommand(command CorrectionCommand) bool {
	return validCorrectionCommand(command)
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
