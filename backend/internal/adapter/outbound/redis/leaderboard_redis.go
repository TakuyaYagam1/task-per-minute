package redis

import (
	"context"
	"errors"
	"fmt"

	goredis "github.com/redis/go-redis/v9"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

const DefaultLeaderboardKey = "leaderboard"

var ErrNilLeaderboardClient = errors.New("leaderboard redis: nil client")

type LeaderboardRedis struct {
	client *goredis.Client
	key    string
}

type Score struct {
	Username string
	Wins     int
}

func NewLeaderboardRedis(client *goredis.Client, key string) *LeaderboardRedis {
	if key == "" {
		key = DefaultLeaderboardKey
	}
	return &LeaderboardRedis{client: client, key: key}
}

var (
	_ duel.LeaderboardBumper = (*LeaderboardRedis)(nil)
	_ leaderboard.WinStore   = (*LeaderboardRedis)(nil)
)

func (r *LeaderboardRedis) IncrementWin(ctx context.Context, username string) error {
	if r == nil || r.client == nil {
		return ErrNilLeaderboardClient
	}
	if err := r.client.ZIncrBy(ctx, r.key, 1, username).Err(); err != nil {
		return fmt.Errorf("LeaderboardRedis - IncrementWin - Client.ZIncrBy: %w", err)
	}
	return nil
}

func (r *LeaderboardRedis) WinScores(ctx context.Context) ([]Score, error) {
	if r == nil || r.client == nil {
		return nil, ErrNilLeaderboardClient
	}
	rows, err := r.client.ZRevRangeByScoreWithScores(ctx, r.key, &goredis.ZRangeBy{
		Min: "-inf",
		Max: "+inf",
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("LeaderboardRedis - WinScores - Client.ZRevRangeByScoreWithScores: %w", err)
	}

	out := make([]Score, 0, len(rows))
	for _, row := range rows {
		username, ok := row.Member.(string)
		if !ok {
			return nil, fmt.Errorf("LeaderboardRedis - WinScores - unexpected member type %T", row.Member)
		}
		out = append(out, Score{
			Username: username,
			Wins:     int(row.Score),
		})
	}
	return out, nil
}
