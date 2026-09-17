package cutoff

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

//nolint:gocyclo // The identity walk covers both ordinary and no-game result evidence.
func correctionProjectionRebuildReservedIDs(dag resultprojection.RevisionDAG) map[uuid.UUID]struct{} {
	reserved := make(map[uuid.UUID]struct{})
	add := func(id uuid.UUID) {
		if id != uuid.Nil {
			reserved[id] = struct{}{}
		}
	}
	for _, projection := range dag.Snapshot().Projections {
		revision := projection.Revision()
		add(revision.ID().UUID())
		add(revision.TournamentID())
		add(revision.Artifact().EntityID)
		if previous := revision.PreviousRevisionID(); previous != nil {
			add(previous.UUID())
		}
	}
	for _, input := range dag.Inputs() {
		if input.NoGame != nil {
			recorded := input.NoGame
			add(recorded.CommandID)
			add(recorded.Scope.WaveID)
			add(recorded.Scope.WindowID)
			add(recorded.Scope.SeriesID)
			add(recorded.FirstParticipantID)
			add(recorded.SecondParticipantID)
			if recorded.ReadyParticipantID != nil {
				add(*recorded.ReadyParticipantID)
			}
			add(recorded.Score.ID.UUID())
			add(recorded.Series.ID.UUID())
			if recorded.Score.PreviousRevisionID != nil {
				add(recorded.Score.PreviousRevisionID.UUID())
			}
			if recorded.Series.PreviousRevisionID != nil {
				add(recorded.Series.PreviousRevisionID.UUID())
			}
			for index, game := range recorded.GameResults {
				add(game.ID.UUID())
				add(game.GameID)
				if index < len(recorded.Topology) {
					add(recorded.Topology[index].SlotID)
				}
				if game.PreviousRevisionID != nil {
					add(game.PreviousRevisionID.UUID())
				}
			}
			continue
		}
		add(input.Result.ID.UUID())
		add(input.Result.CommandID)
		if input.Result.Actor.PrincipalID != nil {
			add(*input.Result.Actor.PrincipalID)
		}
		if input.Result.PreviousRevisionID != nil {
			add(input.Result.PreviousRevisionID.UUID())
		}
		if input.Score != nil {
			add(input.Score.ID.UUID())
			add(input.Score.CommandID)
			add(input.Score.FirstParticipantID)
			add(input.Score.SecondParticipantID)
			if input.Score.Actor.PrincipalID != nil {
				add(*input.Score.Actor.PrincipalID)
			}
			for _, attempt := range input.Score.Attempts {
				add(attempt.SlotID)
				add(attempt.GameID)
			}
			if input.Score.PreviousRevisionID != nil {
				add(input.Score.PreviousRevisionID.UUID())
			}
		}
	}
	return reserved
}

func validCorrectionRevisionDAGEdge(source, derived domain.ArtifactKind) bool {
	switch source {
	case domain.ArtifactKindGameResult:
		return derived == domain.ArtifactKindSeriesScore
	case domain.ArtifactKindSeriesScore:
		return derived == domain.ArtifactKindSeriesResult
	case domain.ArtifactKindSeriesResult:
		return derived == domain.ArtifactKindStandings
	case domain.ArtifactKindStandings:
		return derived == domain.ArtifactKindGoldenGroup || derived == domain.ArtifactKindTopFour
	case domain.ArtifactKindGoldenGroup:
		return derived == domain.ArtifactKindTopFour
	case domain.ArtifactKindTopFour:
		return derived == domain.ArtifactKindBracket
	case domain.ArtifactKindBracket:
		return derived == domain.ArtifactKindChampion
	case domain.ArtifactKindChampion:
		return false
	default:
		return false
	}
}

func validCorrectionServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}
