package gameclose

// ValidateCloseCommand validates a retained close command before replay.
func ValidateCloseCommand(command CloseCommand) error {
	return validateCloseCommand(command)
}
