package result

func ValidNoShowCommand(command NoShowCommand) bool {
	return validNoShowCommand(command)
}

func ValidForfeitCommand(command ForfeitCommand) bool {
	return validForfeitCommand(command)
}

func OperatorResultDigest(action OperatorResultAction, command any) ([32]byte, error) {
	return operatorResultDigest(action, command)
}
