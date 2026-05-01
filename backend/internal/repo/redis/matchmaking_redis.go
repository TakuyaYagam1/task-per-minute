package redis

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const DefaultMatchmakingQueueKey = "matchmaking:queue"

var ErrNilClient = errors.New("matchmaking redis: nil client")

var (
	enqueueScript = goredis.NewScript(`
redis.call("LREM", KEYS[1], 0, ARGV[1])
return redis.call("LPUSH", KEYS[1], ARGV[1])
`)
	popPairScript = goredis.NewScript(`
if redis.call("LLEN", KEYS[1]) < 2 then
  return {}
end
local first = redis.call("RPOP", KEYS[1])
local second = redis.call("RPOP", KEYS[1])
return {first, second}
`)
)

type MatchmakingRedis struct {
	client   *goredis.Client
	queueKey string
}

func NewMatchmakingRedis(client *goredis.Client, queueKey string) *MatchmakingRedis {
	if queueKey == "" {
		queueKey = DefaultMatchmakingQueueKey
	}
	return &MatchmakingRedis{client: client, queueKey: queueKey}
}

func (q *MatchmakingRedis) Enqueue(ctx context.Context, playerID uuid.UUID) error {
	if q == nil || q.client == nil {
		return ErrNilClient
	}
	if err := enqueueScript.Run(ctx, q.client, []string{q.queueKey}, playerID.String()).Err(); err != nil {
		return fmt.Errorf("MatchmakingRedis - Enqueue - Script.Run: %w", err)
	}
	return nil
}

func (q *MatchmakingRedis) PopPair(ctx context.Context) (uuid.UUID, uuid.UUID, bool, error) {
	if q == nil || q.client == nil {
		return uuid.Nil, uuid.Nil, false, ErrNilClient
	}
	rawPair, err := popPairScript.Run(ctx, q.client, []string{q.queueKey}).StringSlice()
	if err != nil {
		return uuid.Nil, uuid.Nil, false, fmt.Errorf("MatchmakingRedis - PopPair - Script.Run: %w", err)
	}
	if len(rawPair) == 0 {
		return uuid.Nil, uuid.Nil, false, nil
	}
	if len(rawPair) != 2 {
		return uuid.Nil, uuid.Nil, false, fmt.Errorf("MatchmakingRedis - PopPair - unexpected result length %d", len(rawPair))
	}
	first, err := uuid.Parse(rawPair[0])
	if err != nil {
		return uuid.Nil, uuid.Nil, false, fmt.Errorf("MatchmakingRedis - PopPair - parse first player id: %w", err)
	}
	second, err := uuid.Parse(rawPair[1])
	if err != nil {
		return uuid.Nil, uuid.Nil, false, fmt.Errorf("MatchmakingRedis - PopPair - parse second player id: %w", err)
	}
	return first, second, true, nil
}

func (q *MatchmakingRedis) Remove(ctx context.Context, playerID uuid.UUID) error {
	if q == nil || q.client == nil {
		return ErrNilClient
	}
	if err := q.client.LRem(ctx, q.queueKey, 0, playerID.String()).Err(); err != nil {
		return fmt.Errorf("MatchmakingRedis - Remove - Client.LRem: %w", err)
	}
	return nil
}
