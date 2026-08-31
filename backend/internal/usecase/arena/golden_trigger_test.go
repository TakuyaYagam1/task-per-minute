package arena_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenTiePartition(t *testing.T) {
	t.Parallel()

	t.Run("emits only final points ties that can change Top 4 membership or seed", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			points     []int
			wantRanges [][2]int
		}{
			{name: "inside Top 4", points: []int{9, 9, 7, 6, 5, 4}, wantRanges: [][2]int{{1, 2}}},
			{name: "crosses cutoff", points: []int{9, 8, 7, 6, 6, 4}, wantRanges: [][2]int{{4, 5}}},
			{name: "independent groups", points: []int{9, 9, 8, 8, 6, 4}, wantRanges: [][2]int{{1, 2}, {3, 4}}},
			{name: "below Top 4", points: []int{9, 8, 7, 6, 5, 5}},
			{name: "three way", points: []int{9, 9, 9, 7, 6, 4}, wantRanges: [][2]int{{1, 3}}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				standings := goldenStandings(test.points)
				for index := range standings {
					standings[index].Buchholz = len(standings) - index
					standings[index].EffectiveTime = time.Duration(index+1) * time.Second
				}
				source := mustGoldenStandingsProjection(t, task046ID(1), task046ID(2), task046RevisionID(3), 1, standings)

				first, err := arena.PartitionGoldenTies(source)
				require.NoError(t, err)
				second, err := arena.PartitionGoldenTies(source)
				require.NoError(t, err)
				require.True(t, reflect.DeepEqual(first, second), "partition must be reproducible")
				require.Equal(t, source.TournamentID, first.TournamentID)
				require.Equal(t, source.RevisionID, first.SourceProjectionRevisionID)

				var ranges [][2]int
				seen := make(map[uuid.UUID]struct{}, len(standings))
				lastPosition := 0
				for _, segment := range first.Segments {
					require.Equal(t, lastPosition+1, segment.PositionFrom)
					require.GreaterOrEqual(t, segment.PositionTo, segment.PositionFrom)
					lastPosition = segment.PositionTo
					if segment.Golden {
						ranges = append(ranges, [2]int{segment.PositionFrom, segment.PositionTo})
						require.Empty(t, segment.NormalStandings)
						require.Len(t, segment.Members, segment.PositionTo-segment.PositionFrom+1)
						for _, member := range segment.Members {
							require.NotContains(t, seen, member.ParticipantID)
							seen[member.ParticipantID] = struct{}{}
						}
						continue
					}
					require.Empty(t, segment.Members)
					require.Len(t, segment.NormalStandings, segment.PositionTo-segment.PositionFrom+1)
					require.True(t, reflect.DeepEqual(
						standings[segment.PositionFrom-1:segment.PositionTo],
						segment.NormalStandings,
					), "normal segment must preserve exact normal ordering")
					for _, standing := range segment.NormalStandings {
						require.NotContains(t, seen, standing.ParticipantID)
						seen[standing.ParticipantID] = struct{}{}
					}
				}
				require.Equal(t, len(standings), lastPosition)
				require.Len(t, seen, len(standings))
				require.Equal(t, test.wantRanges, ranges)
			})
		}
	})

	t.Run("preserves legitimate pair head-to-head inside one three-way points group", func(t *testing.T) {
		t.Parallel()

		standings := goldenStandings([]int{9, 9, 9, 7, 6, 4})
		standings[0].Buchholz = 3
		standings[1].Buchholz = 3
		standings[2].Buchholz = 2
		standings[0].HeadToHeadApplied = true
		standings[0].HeadToHeadPoints = 1
		standings[1].HeadToHeadApplied = true
		source := mustGoldenStandingsProjection(t, task046ID(30), task046ID(31), task046RevisionID(32), 1, standings)
		partition, err := arena.PartitionGoldenTies(source)
		require.NoError(t, err)
		groups := partition.GoldenGroups()
		require.Len(t, groups, 1)
		require.Equal(t, 1, groups[0].PositionFrom)
		require.Equal(t, 3, groups[0].PositionTo)
		require.Len(t, groups[0].Members, 3)
		applied := 0
		for _, member := range groups[0].Members {
			if member.HeadToHeadApplied {
				applied++
			}
		}
		require.Equal(t, 2, applied)
	})

	t.Run("rejects non-final malformed reordered and invented three-way head-to-head input", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			final  bool
			mutate func([]arena.SwissNormalStanding)
		}{
			{name: "non-final", final: false, mutate: func([]arena.SwissNormalStanding) {}},
			{name: "malformed positions", final: true, mutate: func(standings []arena.SwissNormalStanding) { standings[1].Position = 7 }},
			{name: "reordered", final: true, mutate: func(standings []arena.SwissNormalStanding) {
				standings[0], standings[1] = standings[1], standings[0]
			}},
			{name: "invented head-to-head", final: true, mutate: func(standings []arena.SwissNormalStanding) {
				standings[0].HeadToHeadApplied = true
				standings[0].HeadToHeadPoints = 1
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				standings := goldenStandings([]int{9, 9, 9, 7, 6, 4})
				test.mutate(standings)
				_, err := arena.NewGoldenStandingsProjection(
					task046ID(10), task046ID(11), task046RevisionID(12), 1, nil,
					test.final, standings,
				)
				require.ErrorIs(t, err, arena.ErrInvalidGoldenStandingsProjection)
			})
		}
	})

	t.Run("rejects source identity and replay payload changes", func(t *testing.T) {
		t.Parallel()

		source := mustGoldenStandingsProjection(
			t, task046ID(20), task046ID(21), task046RevisionID(22), 1,
			goldenStandings([]int{9, 9, 8, 7, 6, 5}),
		)
		source.Payload[0] ^= 0xff
		_, err := arena.PartitionGoldenTies(source)
		require.ErrorIs(t, err, arena.ErrInvalidGoldenStandingsProjection)

		_, err = arena.NewGoldenStandingsProjection(
			uuid.Nil, task046ID(21), task046RevisionID(22), 1, nil, true,
			goldenStandings([]int{9, 9, 8, 7, 6, 5}),
		)
		require.ErrorIs(t, err, arena.ErrInvalidGoldenStandingsProjection)

		_, err = arena.NewGoldenStandingsProjection(
			task046ID(20), task046ID(21), task046RevisionID(22), 1, nil, true,
			goldenStandings([]int{17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}),
		)
		require.ErrorIs(t, err, arena.ErrInvalidGoldenStandingsProjection)

		for _, previous := range []domain.ArenaDerivedRevisionID{
			domain.ArenaDerivedRevisionID(task046ID(20)),
			domain.ArenaDerivedRevisionID(task046ID(21)),
		} {
			_, err = arena.NewGoldenStandingsProjection(
				task046ID(20), task046ID(21), task046RevisionID(23), 2, &previous, true,
				goldenStandings([]int{9, 9, 8, 7, 6, 5}),
			)
			require.ErrorIs(t, err, arena.ErrInvalidGoldenStandingsProjection)
		}
	})
}

func goldenStandings(points []int) []arena.SwissNormalStanding {
	standings := make([]arena.SwissNormalStanding, len(points))
	for index, value := range points {
		standings[index] = arena.SwissNormalStanding{
			ParticipantID: task046ID(100 + index), Position: index + 1,
			Points: value, PointsLabel: arena.SwissPointsFinal, Seed: index + 1,
		}
	}
	return standings
}

func mustGoldenStandingsProjection(
	t *testing.T,
	tournamentID uuid.UUID,
	projectionID uuid.UUID,
	revisionID domain.ArenaDerivedRevisionID,
	revisionNo int,
	standings []arena.SwissNormalStanding,
) arena.GoldenStandingsProjection {
	t.Helper()
	source, err := arena.NewGoldenStandingsProjection(
		tournamentID, projectionID, revisionID, revisionNo, nil, true, standings,
	)
	require.NoError(t, err)
	return source
}

func task046ID(value int) uuid.UUID {
	return uuid.MustParse("00000000-0000-4000-8000-" + task046Suffix(value))
}

func task046RevisionID(value int) domain.ArenaDerivedRevisionID {
	return domain.ArenaDerivedRevisionID(task046ID(value))
}

func task046Suffix(value int) string {
	const digits = "000000000000"
	encoded := []byte(digits)
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded)
}
