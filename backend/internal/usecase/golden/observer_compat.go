package golden

// GoldenAttemptEvent remains only while stale generated mocks are awaiting central cleanup.
// It has no production observer or emission path.
type GoldenAttemptEvent struct{}
