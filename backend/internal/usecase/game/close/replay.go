package close

// ReconcileClosure compares a replay command with a retained closure.
func ReconcileClosure(
	closure Closure,
	command CloseCommand,
) (*Closure, error) {
	return reconcileClosure(closure, command)
}

// CloneClosure clones a retained closure at the execution boundary.
func CloneClosure(closure Closure) Closure {
	return cloneClosure(closure)
}
