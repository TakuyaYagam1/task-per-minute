package leaderboard

import (
	"context"
	"fmt"
	"sort"
)

const topLimit int32 = 50

type Ranking struct {
	repository StatsRepository
}

func NewRanking(repository StatsRepository) *Ranking {
	return &Ranking{repository: repository}
}

func (r *Ranking) Top50(ctx context.Context) ([]Entry, error) {
	stats, err := r.repository.TopStats(ctx, topLimit)
	if err != nil {
		return nil, fmt.Errorf("leaderboard ranking: load stats: %w", err)
	}
	if len(stats) == 0 {
		return []Entry{}, nil
	}

	entries := make([]Entry, 0, len(stats))
	for _, row := range stats {
		entries = append(entries, Entry{
			Username:           row.Username,
			Wins:               row.Wins,
			AverageSolveTimeMs: row.AverageSolveTimeMs,
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Wins != entries[j].Wins {
			return entries[i].Wins > entries[j].Wins
		}
		if entries[i].AverageSolveTimeMs != entries[j].AverageSolveTimeMs {
			return entries[i].AverageSolveTimeMs < entries[j].AverageSolveTimeMs
		}
		return entries[i].Username < entries[j].Username
	})

	if len(entries) > int(topLimit) {
		entries = entries[:topLimit]
	}
	for index := range entries {
		entries[index].Rank = index + 1
	}
	return entries, nil
}
