package attempt

// ValidateFailedAttemptCommand validates a command before execution replay.
func ValidateCommand(command AttemptCommand) error {
	return validateFailedAttemptCommand(command)
}

// ValidateFailedAttemptAuthority validates the authority loaded for execution replay.
func ValidateAuthority(authority AttemptAuthority) error {
	return validateFailedAttemptAuthority(authority)
}
