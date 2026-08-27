package arena_test

import (
	"errors"
	"strconv"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestSwissPlanRosterSizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		rosterSize int
		rounds     int
		series     int
		byes       int
	}{
		{rosterSize: 4, rounds: 3, series: 2, byes: 0},
		{rosterSize: 5, rounds: 3, series: 2, byes: 1},
		{rosterSize: 6, rounds: 3, series: 3, byes: 0},
		{rosterSize: 7, rounds: 3, series: 3, byes: 1},
		{rosterSize: 8, rounds: 3, series: 4, byes: 0},
		{rosterSize: 9, rounds: 4, series: 4, byes: 1},
		{rosterSize: 10, rounds: 4, series: 5, byes: 0},
		{rosterSize: 11, rounds: 4, series: 5, byes: 1},
		{rosterSize: 12, rounds: 4, series: 6, byes: 0},
		{rosterSize: 13, rounds: 4, series: 6, byes: 1},
		{rosterSize: 14, rounds: 4, series: 7, byes: 0},
		{rosterSize: 15, rounds: 4, series: 7, byes: 1},
		{rosterSize: 16, rounds: 4, series: 8, byes: 0},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.rosterSize), func(t *testing.T) {
			t.Parallel()

			plan, err := arena.BuildSwissPlan(domain.ArenaPresetV1, tt.rosterSize)
			if err != nil {
				t.Fatalf("BuildSwissPlan(%d) error = %v", tt.rosterSize, err)
			}
			if plan.Preset != domain.ArenaPresetV1 {
				t.Errorf("BuildSwissPlan(%d) preset = %q, want %q", tt.rosterSize, plan.Preset, domain.ArenaPresetV1)
			}
			if plan.RosterSize != tt.rosterSize {
				t.Errorf("BuildSwissPlan(%d) roster size = %d", tt.rosterSize, plan.RosterSize)
			}
			if len(plan.Rounds) != tt.rounds {
				t.Fatalf("BuildSwissPlan(%d) rounds = %d, want %d", tt.rosterSize, len(plan.Rounds), tt.rounds)
			}
			for i, round := range plan.Rounds {
				if round.Number != i+1 {
					t.Errorf("BuildSwissPlan(%d) round[%d].Number = %d, want %d", tt.rosterSize, i, round.Number, i+1)
				}
				if round.SeriesSlots != tt.series {
					t.Errorf("BuildSwissPlan(%d) round %d SeriesSlots = %d, want %d", tt.rosterSize, round.Number, round.SeriesSlots, tt.series)
				}
				if round.ByeSlots != tt.byes {
					t.Errorf("BuildSwissPlan(%d) round %d ByeSlots = %d, want %d", tt.rosterSize, round.Number, round.ByeSlots, tt.byes)
				}
			}
		})
	}

	for _, rosterSize := range []int{3, 17} {
		if _, err := arena.BuildSwissPlan(domain.ArenaPresetV1, rosterSize); !errors.Is(err, domain.ErrInvalidArenaRosterSize) {
			t.Errorf("BuildSwissPlan(%d) error = %v, want ErrInvalidArenaRosterSize", rosterSize, err)
		}
	}
	if _, err := arena.BuildSwissPlan(domain.ArenaPreset("unknown"), 8); !errors.Is(err, domain.ErrInvalidArenaRosterSize) {
		t.Errorf("BuildSwissPlan(unknown, 8) error = %v, want ErrInvalidArenaRosterSize", err)
	}
}
