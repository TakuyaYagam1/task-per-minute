package clock

import "time"

// Clock abstracts time.Now so usecases can be tested deterministically.
// Multiple usecases share this contract; defining it in pkg/ keeps them
// decoupled from each other while still pointing at one production adapter.
type Clock interface {
	Now() time.Time
}

// Real returns the wall-clock time in UTC. Wire this in production; tests
// inject their own Clock implementation (mockery, fake, etc.).
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }
