package idempotency

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const receiptFinalizationTimeout = time.Second

var (
	ErrInvalidCommand = errors.New("idempotency: invalid command")
)

// InFlightError tells the caller that another replica currently owns the
// same receipt. It is a retryable domain conflict, not an internal failure.
type InFlightError struct{}

func (*InFlightError) Error() string {
	return "idempotency: command already in flight"
}

func (*InFlightError) Unwrap() error {
	return domain.ErrConflict
}

// Coordinator gates a mutation through a disposable distributed receipt. Its
// caller remains responsible for deriving the result from the durable command
// record on both first execution and completed receipt replay.
type Coordinator struct {
	store Store
}

func NewCoordinator(store Store) *Coordinator {
	return &Coordinator{store: store}
}

// Execute invokes the durable mutation after receipt acquisition. A completed
// receipt deliberately invokes the mutation again so the durable database
// command record remains the only source of an outcome.
func Execute[T any](
	ctx context.Context,
	coordinator *Coordinator,
	command Command,
	invoke func(context.Context) (T, error),
) (T, error) {
	var zero T
	if ctx == nil || !validCommand(command) || invoke == nil {
		return zero, domain.ErrValidation
	}
	if coordinator == nil || coordinator.store == nil {
		return zero, domain.ErrInternal
	}

	lease, err := NewLeaseToken()
	if err != nil {
		return zero, domain.ErrInternal
	}
	begin, err := coordinator.store.Begin(ctx, command, lease)
	if errors.Is(err, ErrPayloadConflict) {
		return zero, domain.ErrConflict
	}
	if err != nil {
		return zero, domain.ErrInternal
	}
	switch begin.Disposition {
	case BeginInFlight:
		return zero, &InFlightError{}
	case BeginSucceeded:
		return invoke(ctx)
	case BeginAcquired:
		if begin.Lease != lease {
			return zero, domain.ErrInternal
		}
		result, invokeErr := invoke(ctx)
		if invokeErr != nil {
			coordinator.markFailed(ctx, command, lease)
			return zero, invokeErr
		}
		coordinator.markSucceeded(ctx, command, lease)
		return result, nil
	default:
		return zero, domain.ErrInternal
	}
}

func (c *Coordinator) markSucceeded(requestContext context.Context, command Command, lease LeaseToken) {
	if c.transition(requestContext, command, lease, c.store.MarkSucceeded) != nil {
		// The database outcome is already committed. Reopen admission so the
		// next call can recover it through the durable command record.
		_ = c.transition(requestContext, command, lease, c.store.MarkFailed)
	}
}

func (c *Coordinator) markFailed(requestContext context.Context, command Command, lease LeaseToken) {
	_ = c.transition(requestContext, command, lease, c.store.MarkFailed)
}

func (c *Coordinator) transition(
	requestContext context.Context,
	command Command,
	lease LeaseToken,
	transition func(context.Context, Command, LeaseToken) error,
) error {
	if c == nil || c.store == nil || transition == nil || lease == (LeaseToken{}) {
		return domain.ErrInternal
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(requestContext), receiptFinalizationTimeout)
	defer cancel()
	return transition(ctx, command, lease)
}

// NewLeaseToken returns a cryptographically random receipt owner token. The
// token never represents command content and is retained only in the
// in-flight Redis record.
func NewLeaseToken() (LeaseToken, error) {
	value, err := uuid.NewRandom()
	if err != nil {
		return LeaseToken{}, err
	}
	return LeaseToken(value), nil
}

// NewCommand records an already canonicalized command digest without retaining
// the command payload. The owning application workflow defines its own stable
// digest format so receipt admission exactly matches durable replay semantics.
func NewCommand(namespace string, id uuid.UUID, digest [32]byte) (Command, error) {
	if id == uuid.Nil || digest == ([32]byte{}) || !validNamespace(namespace) {
		return Command{}, ErrInvalidCommand
	}
	return Command{Namespace: namespace, ID: id, PayloadDigest: digest}, nil
}

func validCommand(command Command) bool {
	return command.ID != uuid.Nil && command.PayloadDigest != ([32]byte{}) &&
		validNamespace(command.Namespace)
}

func validNamespace(namespace string) bool {
	return namespace != "" && namespace == strings.TrimSpace(namespace) && len(namespace) <= 64 &&
		!strings.ContainsAny(namespace, ":\x00")
}
