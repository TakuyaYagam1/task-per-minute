package v1

import "context"

type HealthChecker interface {
	Check(ctx context.Context) error
}

type HealthCheckerFunc func(ctx context.Context) error

func (f HealthCheckerFunc) Check(ctx context.Context) error { return f(ctx) }

type SchemaVersionReader interface {
	SchemaVersion(ctx context.Context) (int64, error)
}

type SchemaVersionReaderFunc func(ctx context.Context) (int64, error)

func (f SchemaVersionReaderFunc) SchemaVersion(ctx context.Context) (int64, error) {
	return f(ctx)
}
