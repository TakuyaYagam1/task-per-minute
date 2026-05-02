//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
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

	store1 := redisrepo.NewRevocationRedis(client, keyPrefix)
	store2 := redisrepo.NewRevocationRedis(client, keyPrefix)

	revoked, err := store1.IsRevoked(ctx, jti)
	require.NoError(t, err)
	require.False(t, revoked)

	require.NoError(t, store1.Revoke(ctx, jti, time.Now().Add(time.Hour)))
	require.ErrorIs(t, store1.Revoke(ctx, jti, time.Now().Add(time.Hour)), apperr.ErrTokenRevoked)

	revoked, err = store2.IsRevoked(ctx, jti)
	require.NoError(t, err)
	require.True(t, revoked)
	store2.Cleanup()
}

func TestRevocationRedis_NilClient(t *testing.T) {
	t.Parallel()

	store := redisrepo.NewRevocationRedis(nil, "revocation:nil:")
	require.ErrorIs(t, store.Revoke(context.Background(), uniq("jti"), time.Now().Add(time.Hour)), redisrepo.ErrNilRevocationClient)
	revoked, err := store.IsRevoked(context.Background(), uniq("jti"))
	require.ErrorIs(t, err, redisrepo.ErrNilRevocationClient)
	require.False(t, revoked)
}
