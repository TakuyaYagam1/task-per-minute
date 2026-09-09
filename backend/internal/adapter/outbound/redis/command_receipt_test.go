package redis

import (
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
)

func TestCommandReceiptKeyIsNamespaceScoped(t *testing.T) {
	t.Parallel()

	command := idempotency.Command{
		Namespace: "tournament-create",
		ID:        uuid.MustParse("123e4567-e89b-12d3-a456-426614174000"),
		PayloadDigest: [32]byte{
			1,
		},
	}

	require.Equal(
		t,
		"command-receipt:tournament-create:123e4567-e89b-12d3-a456-426614174000",
		commandReceiptKey(command),
	)
	require.True(t, validIdempotencyCommand(command))
}

func TestCommandReceiptRejectsUnboundedNamespace(t *testing.T) {
	t.Parallel()

	command := idempotency.Command{Namespace: "bad:scope", ID: uuid.New(), PayloadDigest: [32]byte{1}}

	require.False(t, validIdempotencyCommand(command))
}

func TestCommandReceiptStoreRejectsSubMillisecondTTL(t *testing.T) {
	t.Parallel()

	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store := NewCommandReceiptStore(client, time.Nanosecond, time.Second, time.Second)

	require.False(t, store.valid())
}
