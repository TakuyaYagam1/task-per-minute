package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	idempotencymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency/mocks"
)

func TestUseCaseFinalizeCreateReceiptUsesFreshFallbackContext(t *testing.T) {
	t.Parallel()

	receipts := idempotencymocks.NewMockStore(t)
	application := NewUseCase(Dependencies{Receipts: receipts})
	command, err := idempotency.NewCommand(
		"tournament-create",
		uuid.MustParse("c0000000-0000-0000-0000-000000000001"),
		[32]byte{1},
	)
	require.NoError(t, err)
	lease := idempotency.LeaseToken{1}
	receipts.EXPECT().MarkSucceeded(mock.Anything, command, lease).
		RunAndReturn(func(ctx context.Context, _ idempotency.Command, _ idempotency.LeaseToken) error {
			<-ctx.Done()
			return ctx.Err()
		}).Once()
	receipts.EXPECT().MarkFailed(mock.MatchedBy(func(ctx context.Context) bool {
		return ctx.Err() == nil
	}), command, lease).Return(nil).Once()

	application.finalizeCreateReceipt(context.Background(), command, lease, true)
}
