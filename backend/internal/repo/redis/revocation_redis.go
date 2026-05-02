package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
)

const DefaultRevocationKeyPrefix = "jwt:revoked:"

var ErrNilRevocationClient = errors.New("revocation redis: nil client")

type RevocationRedis struct {
	client    *goredis.Client
	keyPrefix string
}

func NewRevocationRedis(client *goredis.Client, keyPrefix string) *RevocationRedis {
	if keyPrefix == "" {
		keyPrefix = DefaultRevocationKeyPrefix
	}
	return &RevocationRedis{client: client, keyPrefix: keyPrefix}
}

func (r *RevocationRedis) Revoke(ctx context.Context, jti string, expiresAt time.Time) error {
	if r == nil || r.client == nil {
		return ErrNilRevocationClient
	}
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return nil
	}
	ok, err := r.client.SetNX(ctx, r.key(jti), "1", ttl).Result()
	if err != nil {
		return fmt.Errorf("RevocationRedis - Revoke - Client.SetNX: %w", err)
	}
	if !ok {
		return apperr.ErrTokenRevoked
	}
	return nil
}

func (r *RevocationRedis) IsRevoked(ctx context.Context, jti string) (bool, error) {
	if r == nil || r.client == nil {
		return false, ErrNilRevocationClient
	}
	n, err := r.client.Exists(ctx, r.key(jti)).Result()
	if err != nil {
		return false, fmt.Errorf("RevocationRedis - IsRevoked - Client.Exists: %w", err)
	}
	return n > 0, nil
}

func (r *RevocationRedis) Cleanup() {}

func (r *RevocationRedis) key(jti string) string {
	return r.keyPrefix + jti
}
