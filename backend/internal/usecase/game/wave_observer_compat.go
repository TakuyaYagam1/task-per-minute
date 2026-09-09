package game

// StartEvent remains only while stale generated mocks are awaiting central cleanup.
// It has no production observer or emission path.
type StartEvent struct{}
