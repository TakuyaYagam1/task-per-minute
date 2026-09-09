package playoff_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func TestFinalChampion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		winners    []int
		wantScore  domain.SeriesScore
		wantGames  int
		wantDigest string
	}{
		{
			name: "two zero", winners: []int{1, 1},
			wantScore: domain.SeriesScore{FirstParticipantWins: 2}, wantGames: 2,
			wantDigest: "9ed9bb9af89e4c0b94149444ef20d3fcc873118508ad981718d0330e987e6399",
		},
		{
			name: "two one", winners: []int{1, 2, 1},
			wantScore: domain.SeriesScore{FirstParticipantWins: 2, SecondParticipantWins: 1}, wantGames: 3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			final := newFinal(t)
			var terminal playoff.FinalProgressionCommand
			for index, winner := range test.winners {
				terminal = finalProgression(final, winner, index == len(test.winners)-1)
				next, changed, err := playoff.ProgressFinal(final, terminal)
				require.NoError(t, err)
				require.True(t, changed)
				if index < len(test.winners)-1 {
					replayed, replayChanged, replayErr := playoff.ProgressFinal(next, terminal)
					require.NoError(t, replayErr)
					require.False(t, replayChanged)
					require.Equal(t, next.Execution().Series, replayed.Execution().Series)
				}
				final = next
			}

			series := final.Execution().Series
			require.Equal(t, domain.SeriesStateCompleted, series.State)
			require.Equal(t, test.wantScore, series.Score)
			require.Len(t, series.Slots, test.wantGames)
			require.Equal(t, domain.TournamentStateCompleted, final.Tournament().State)
			require.Len(t, final.ChampionRevisions(), 1)
			require.Equal(t, domain.ArtifactKindChampion, final.ChampionRevisions()[0].Revision().Artifact().Kind)
			require.Equal(t, []domain.RevisionDependency{{
				SourceRevisionID:  playoffRevisionID(5202),
				DerivedRevisionID: final.ChampionRevisions()[0].Revision().ID(),
			}}, final.ChampionDependencies())
			require.Equal(t, *series.WinnerID, final.ChampionID())
			require.NoError(t, final.Validate())
			if test.wantDigest != "" {
				require.Equal(t, test.wantDigest, projectionDigestHex(final.ChampionRevisions()[0]))
			}

			repeated, changed, err := playoff.ProgressFinal(final, terminal)
			require.NoError(t, err)
			require.False(t, changed)
			require.Len(t, repeated.ChampionRevisions(), 1)
			require.Equal(t, domain.TournamentStateCompleted, repeated.Tournament().State)
		})
	}
}
