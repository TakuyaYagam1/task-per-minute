package clock

import "time"

// Real returns wall-clock time in UTC. The composition root injects this
// implementation into consumer-owned Clock ports.
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }
