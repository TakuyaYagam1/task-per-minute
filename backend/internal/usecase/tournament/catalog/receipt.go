package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
)

const (
	createTournamentReceiptNamespace = "tournament-create"
	receiptFinalizationTimeout       = time.Second
)

func createTournamentReceipt(command usecase.TournamentCreateCommand) idempotency.Command {
	return idempotency.Command{
		Namespace:     createTournamentReceiptNamespace,
		ID:            command.IdempotencyKey,
		PayloadDigest: createTournamentPayloadDigest(command),
	}
}

func (a *UseCase) beginCreateReceipt(
	ctx context.Context,
	command usecase.TournamentCreateCommand,
) (idempotency.Command, idempotency.LeaseToken, bool, error) {
	receipt := createTournamentReceipt(command)
	lease, err := idempotency.NewLeaseToken()
	if err != nil {
		return idempotency.Command{}, idempotency.LeaseToken{}, false, domain.ErrInternal
	}
	begin, err := a.receipts.Begin(ctx, receipt, lease)
	if errors.Is(err, idempotency.ErrPayloadConflict) {
		return idempotency.Command{}, idempotency.LeaseToken{}, false, domain.ErrConflict
	}
	if err != nil {
		return idempotency.Command{}, idempotency.LeaseToken{}, false, domain.ErrInternal
	}
	switch begin.Disposition {
	case idempotency.BeginAcquired:
		if begin.Lease != lease {
			return idempotency.Command{}, idempotency.LeaseToken{}, false, domain.ErrInternal
		}
		return receipt, lease, true, nil
	case idempotency.BeginSucceeded:
		return receipt, idempotency.LeaseToken{}, false, nil
	case idempotency.BeginInFlight:
		return idempotency.Command{}, idempotency.LeaseToken{}, false, domain.ErrConflict
	default:
		return idempotency.Command{}, idempotency.LeaseToken{}, false, domain.ErrInternal
	}
}

func (a *UseCase) finalizeCreateReceipt(
	requestContext context.Context,
	command idempotency.Command,
	lease idempotency.LeaseToken,
	succeeded bool,
) {
	if a == nil || a.receipts == nil || lease == (idempotency.LeaseToken{}) {
		return
	}
	if succeeded {
		if a.finalizeCreateReceiptTransition(requestContext, command, lease, a.receipts.MarkSucceeded) != nil {
			_ = a.finalizeCreateReceiptTransition(requestContext, command, lease, a.receipts.MarkFailed)
		}
		return
	}
	_ = a.finalizeCreateReceiptTransition(requestContext, command, lease, a.receipts.MarkFailed)
}

func (a *UseCase) finalizeCreateReceiptTransition(
	requestContext context.Context,
	command idempotency.Command,
	lease idempotency.LeaseToken,
	transition func(context.Context, idempotency.Command, idempotency.LeaseToken) error,
) error {
	if transition == nil {
		return domain.ErrInternal
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(requestContext), receiptFinalizationTimeout)
	defer cancel()
	return transition(ctx, command, lease)
}

func createTournamentPayloadDigest(command usecase.TournamentCreateCommand) [sha256.Size]byte {
	payload := make([]byte, 0, 64)
	payload = append(payload, "tournament-create:v1\x00"...)
	payload = append(payload, command.Operator.ActorID[:]...)
	revision := [8]byte{}
	//nolint:gosec // Signed timestamp bits are intentionally encoded as unsigned digest input.
	binary.BigEndian.PutUint64(revision[:], uint64(command.ExpectedRevision))
	payload = append(payload, revision[:]...)
	payload = append(payload, 0)
	payload = append(payload, command.Preset.String()...)
	return sha256.Sum256(payload)
}
