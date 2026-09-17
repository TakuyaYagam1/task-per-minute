package terminal

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func TestTerminalPublicationMatchesDistinctIdentityRoles(t *testing.T) {
	winner, tournamentID, seriesID, gameID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	gameResult := domain.OfficialResultRevisionID(uuid.New())
	seriesResult := domain.OfficialResultRevisionID(uuid.New())
	scoreID := domain.SeriesScoreRevisionID(uuid.New())
	ids, err := FinalPublicationIdentity(gameResult)
	require.NoError(t, err)
	final := Final{execution: seriesdomain.Execution{Series: domain.Series{ID: seriesID, TournamentID: tournamentID, State: domain.SeriesStateCompleted, WinnerID: &winner}}}
	progression := FinalProgressionCommand{ChampionRevisionID: ids.ChampionRevisionID}
	progression.Progression.Game = domain.Game{ID: gameID, ResultRevisionID: &gameResult}
	progression.Progression.ScoreRevision.ID = scoreID
	progression.Progression.TerminalResultRevisionID = &seriesResult
	publication := projection.FinalPublication{
		IDs:       projection.PublicationIDs{RevisionID: ids.ProjectionRevisionID, CutoffID: ids.CutoffID},
		Scope:     projection.FinalScope{TournamentID: tournamentID, SeriesID: seriesID, GameAttemptID: gameID},
		Expected:  projection.FinalHeadExpectation{WinnerID: winner, ScoreRevisionID: scoreID, GameResultRevisionID: gameResult, SeriesResultRevisionID: seriesResult},
		Artifacts: []projection.PublicationArtifact{{Kind: domain.ArtifactKindChampion, ID: ids.ChampionArtifactID}},
	}
	require.NotEqual(t, ids.ChampionRevisionID.UUID(), ids.ChampionArtifactID)
	require.True(t, terminalPublicationMatches(final, progression, publication))
	for _, field := range []string{"logical champion", "physical champion", "publication", "cutoff", "game head", "score head", "series head", "attempt"} {
		t.Run(field, func(t *testing.T) {
			p, command := publication.Snapshot(), progression
			switch field {
			case "logical champion":
				command.ChampionRevisionID = domain.DerivedRevisionID(uuid.New())
			case "physical champion":
				p.Artifacts[0].ID = ids.ChampionRevisionID.UUID()
			case "publication":
				p.IDs.RevisionID = uuid.New()
			case "cutoff":
				p.IDs.CutoffID = uuid.New()
			case "game head":
				p.Expected.GameResultRevisionID = domain.OfficialResultRevisionID(uuid.New())
			case "score head":
				p.Expected.ScoreRevisionID = domain.SeriesScoreRevisionID(uuid.New())
			case "series head":
				p.Expected.SeriesResultRevisionID = domain.OfficialResultRevisionID(uuid.New())
			case "attempt":
				p.Scope.GameAttemptID = uuid.New()
			}
			require.False(t, terminalPublicationMatches(final, command, p))
		})
	}
}
