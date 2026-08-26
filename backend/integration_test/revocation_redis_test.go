//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestRevocationRedis_RevokePersistsAcrossRepositoryInstances(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := sharedRedis(t).client
	keyPrefix := "revocation:" + uniq("jti") + ":"
	jti := uniq("token")
	t.Cleanup(func() {
		_ = client.Del(context.Background(), keyPrefix+jti).Err()
	})

	store1 := redisadapter.NewRevocationRedis(client, keyPrefix)
	store2 := redisadapter.NewRevocationRedis(client, keyPrefix)

	revoked, err := store1.IsRevoked(ctx, jti)
	require.NoError(t, err)
	require.False(t, revoked)

	require.NoError(t, store1.Revoke(ctx, jti, time.Now().Add(time.Hour)))
	require.ErrorIs(t, store1.Revoke(ctx, jti, time.Now().Add(time.Hour)), domain.ErrTokenRevoked)

	revoked, err = store2.IsRevoked(ctx, jti)
	require.NoError(t, err)
	require.True(t, revoked)
	store2.Cleanup()
}

func TestRevocationRedis_NilClient(t *testing.T) {
	t.Parallel()

	store := redisadapter.NewRevocationRedis(nil, "revocation:nil:")
	require.ErrorIs(t, store.Revoke(context.Background(), uniq("jti"), time.Now().Add(time.Hour)), redisadapter.ErrNilRevocationClient)
	revoked, err := store.IsRevoked(context.Background(), uniq("jti"))
	require.ErrorIs(t, err, redisadapter.ErrNilRevocationClient)
	require.False(t, revoked)
}
