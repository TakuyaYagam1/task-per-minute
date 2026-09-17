package replay

import "crypto/sha256"

func ValidReserveCommand(command ReserveCommand) bool {
	return validReserveCommand(command)
}

func ValidReplayCommand(command ReplayCommand) bool {
	return validReplayCommand(command)
}

func ReplayWorkflowDigest(action string, command any) ([sha256.Size]byte, error) {
	return replayWorkflowDigest(action, command)
}
